package metering

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// usage.go is the idempotent meter (task 2.2 / design Decision 2).
//
// Every meter — SUM, seats, retention, cloud eval compute — is ONE row keyed
// `{customer_id, period, metric}`, written by an upsert. The key is the guarantee: a second
// charge-bearing row for the same tuple is not "prevented by careful code", it is *unrepresentable*.
// Re-reporting, re-deriving, or reconciling a period updates the one row in place.
//
// The `source_digest` is the second half. It records WHAT the quantity was computed from, so an
// identical re-derivation is recognized as a no-op rather than a rewrite — which is what lets a
// reconciler run on a schedule without churning updated_at on every pass.

// Metric is one metered dimension. These four are the whole taxonomy; a fifth meter is a new constant
// plus config, never a new table.
type Metric string

const (
	// MetricSUM is spend under management, derived from the P2.5 cost events.
	MetricSUM Metric = "sum"
	// MetricSeats is dashboard seats held in the period.
	MetricSeats Metric = "seats"
	// MetricRetention is trace/metric retention consumed in the period.
	MetricRetention Metric = "retention"
	// MetricEvalCompute is cloud eval compute consumed in the period.
	MetricEvalCompute Metric = "eval_compute"
)

// Metrics is every meter, in reporting order. Central, so a consumer cannot iterate a subset by
// accident and silently under-report a customer.
var Metrics = []Metric{MetricSUM, MetricSeats, MetricRetention, MetricEvalCompute}

// KnownMetric reports whether m is one of the four meters.
func KnownMetric(m Metric) bool {
	for _, k := range Metrics {
		if k == m {
			return true
		}
	}
	return false
}

// UsageRecord is one meter's quantity for one customer in one period — the platform's system of record
// for **what was used** (design Decision 7).
type UsageRecord struct {
	CustomerID string  `json:"customer_id"`
	Period     string  `json:"period"`
	Metric     Metric  `json:"metric"`
	Quantity   float64 `json:"quantity"`
	// SourceDigest is the content hash of the inputs that produced Quantity.
	SourceDigest string `json:"source_digest"`
	// ReportedToProvider / ProviderUsageRef record the provider hand-off. The ref is an opaque provider
	// handle — never an amount.
	ReportedToProvider bool      `json:"reported_to_provider"`
	ProviderUsageRef   string    `json:"provider_usage_ref,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Key is the record's primary key, as one comparable value.
type Key struct {
	CustomerID string
	Period     string
	Metric     Metric
}

// Key returns the record's primary key.
func (u UsageRecord) Key() Key { return Key{u.CustomerID, u.Period, u.Metric} }

// Errors the usage store returns.
var (
	ErrUnknownMetric  = errors.New("metering: unknown metric")
	ErrUsageNotFound  = errors.New("metering: no usage record")
	ErrNegativeUsage  = errors.New("metering: refusing a negative usage quantity")
	ErrMissingDigest  = errors.New("metering: a usage record must carry the source digest that produced it")
	ErrStoreUnavail   = errors.New("metering: usage store unavailable")
	ErrPeriodMismatch = errors.New("metering: usage record period does not match the requested period")
)

// UsageStore is the `usage_record` table behind its primary key.
type UsageStore interface {
	// Upsert writes rec keyed {customer, period, metric}. It returns the stored record and whether the
	// write CHANGED anything: an upsert whose source digest and quantity already match is a no-op.
	//
	// The contract that matters: however many times Upsert is called for one key, exactly one row
	// exists afterwards.
	Upsert(rec UsageRecord) (UsageRecord, bool, error)
	// Get returns one record.
	Get(k Key) (UsageRecord, error)
	// Period returns every record for a customer's period, in metric order.
	Period(customerID, period string) ([]UsageRecord, error)
	// MarkReported records the provider hand-off ref on an existing record. It never changes the
	// quantity — reporting is not a re-measurement.
	MarkReported(k Key, providerUsageRef string) (UsageRecord, error)
}

// MemUsageStore is the in-memory `usage_record` table. The Go map key IS the primary key, so this
// implementation has the same never-two-rows property the Postgres PRIMARY KEY gives.
type MemUsageStore struct {
	mu   sync.RWMutex
	rows map[Key]UsageRecord
	// writes counts every accepted Upsert call, so a test can prove N replays produced N calls and
	// still ONE row — a count of rows alone cannot distinguish "idempotent" from "never ran twice".
	writes int
	down   bool
}

// NewMemUsageStore builds an empty usage store.
func NewMemUsageStore() *MemUsageStore { return &MemUsageStore{rows: map[Key]UsageRecord{}} }

// SetDown flips the store between available and unavailable (fault-injection seam).
func (s *MemUsageStore) SetDown(down bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.down = down
}

// Writes is how many Upsert calls the store accepted.
func (s *MemUsageStore) Writes() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.writes
}

// Rows is the total number of rows held — the never-double-count assertion reads this.
func (s *MemUsageStore) Rows() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rows)
}

// Upsert writes one record keyed {customer, period, metric}.
func (s *MemUsageStore) Upsert(rec UsageRecord) (UsageRecord, bool, error) {
	if !KnownMetric(rec.Metric) {
		return UsageRecord{}, false, fmt.Errorf("%w: %q", ErrUnknownMetric, rec.Metric)
	}
	if rec.CustomerID == "" || rec.Period == "" {
		return UsageRecord{}, false, errors.New("metering: usage record needs both a customer and a period")
	}
	if rec.Quantity < 0 {
		return UsageRecord{}, false, fmt.Errorf("%w: %v", ErrNegativeUsage, rec.Quantity)
	}
	if rec.SourceDigest == "" {
		return UsageRecord{}, false, ErrMissingDigest
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down {
		return UsageRecord{}, false, ErrStoreUnavail
	}
	s.writes++
	k := rec.Key()
	prev, existed := s.rows[k]
	if existed && prev.SourceDigest == rec.SourceDigest && prev.Quantity == rec.Quantity {
		// Identical re-derivation: keep the row exactly as it is, including updated_at and the provider
		// hand-off state. Touching it would churn the reconciler and make "when did this last change"
		// unanswerable.
		return prev, false, nil
	}
	if existed {
		// A re-report carries the measurement forward, never the provider hand-off: the ref belongs to
		// the quantity that was reported, so a changed quantity is un-reported until it is sent again.
		rec.ReportedToProvider = false
		rec.ProviderUsageRef = ""
	}
	s.rows[k] = rec
	return rec, true, nil
}

// Get returns one record.
func (s *MemUsageStore) Get(k Key) (UsageRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.down {
		return UsageRecord{}, ErrStoreUnavail
	}
	rec, ok := s.rows[k]
	if !ok {
		return UsageRecord{}, fmt.Errorf("%w: %s/%s/%s", ErrUsageNotFound, k.CustomerID, k.Period, k.Metric)
	}
	return rec, nil
}

// Period returns every record for a customer's period in metric order.
func (s *MemUsageStore) Period(customerID, period string) ([]UsageRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.down {
		return nil, ErrStoreUnavail
	}
	var out []UsageRecord
	for k, v := range s.rows {
		if k.CustomerID == customerID && k.Period == period {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return metricRank(out[i].Metric) < metricRank(out[j].Metric) })
	return out, nil
}

// MarkReported records the provider hand-off ref without touching the quantity.
func (s *MemUsageStore) MarkReported(k Key, providerUsageRef string) (UsageRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down {
		return UsageRecord{}, ErrStoreUnavail
	}
	rec, ok := s.rows[k]
	if !ok {
		return UsageRecord{}, fmt.Errorf("%w: %s/%s/%s", ErrUsageNotFound, k.CustomerID, k.Period, k.Metric)
	}
	rec.ReportedToProvider, rec.ProviderUsageRef = true, providerUsageRef
	s.rows[k] = rec
	return rec, nil
}

func metricRank(m Metric) int {
	for i, k := range Metrics {
		if k == m {
			return i
		}
	}
	return len(Metrics)
}

// ─────────────────────────────────────────────────────────────────────────────
// Meter — the capability's front door
// ─────────────────────────────────────────────────────────────────────────────

// Meter binds the cost-event substrate to the usage store. It is the object the API, the billing
// service, and the reconciler all talk to, so there is exactly one place a meter can be written.
type Meter struct {
	events CostEventSource
	usage  UsageStore
	now    func() time.Time
}

// NewMeter builds a meter over the P2.5 substrate and the usage store.
func NewMeter(events CostEventSource, usage UsageStore) *Meter {
	return &Meter{events: events, usage: usage, now: time.Now}
}

// SetClock injects a deterministic clock (tests).
func (m *Meter) SetClock(now func() time.Time) { m.now = now }

// Usage exposes the underlying store for read paths (the API surface, the reconciler).
func (m *Meter) Usage() UsageStore { return m.usage }

// DeriveSUM re-derives SUM for a period from the cost events. It writes nothing.
func (m *Meter) DeriveSUM(customerID string, p Period) (SUMResult, error) {
	return DeriveSUM(m.events, customerID, p)
}

// RecordUsage upserts one meter's quantity for a period (the `RecordUsage(...)` of the design's key
// interfaces). It returns the stored record and whether the write changed anything.
func (m *Meter) RecordUsage(customerID string, p Period, metric Metric, quantity float64, sourceDigest string) (UsageRecord, bool, error) {
	return m.usage.Upsert(UsageRecord{
		CustomerID:   customerID,
		Period:       p.ID,
		Metric:       metric,
		Quantity:     quantity,
		SourceDigest: sourceDigest,
		UpdatedAt:    m.now().UTC(),
	})
}

// RecordSUM derives SUM from the cost events and upserts it as the period's `sum` meter — the whole
// metering path in one call, and the one a scheduler runs.
//
// Running it N times over the same events writes ONE row with the correct quantity, because the
// derivation is deterministic and the record is keyed.
func (m *Meter) RecordSUM(customerID string, p Period) (UsageRecord, SUMResult, error) {
	res, err := DeriveSUM(m.events, customerID, p)
	if err != nil {
		return UsageRecord{}, SUMResult{}, err
	}
	rec, _, err := m.RecordUsage(customerID, p, MetricSUM, res.Quantity, res.SourceDigest)
	if err != nil {
		return UsageRecord{}, res, err
	}
	return rec, res, nil
}
