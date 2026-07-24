package metering

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// sum.go derives SUM from the P2.5 cost-event substrate (task 2.1) and inherits its retry idempotency
// (task 2.3).
//
// The derivation is a pure function of the events, and it is deterministic in a way float arithmetic
// does not give away for free: the events are DEDUPED by their P2.5 invocation identity and then SORTED
// before summation. Sorting matters because floating-point addition is not associative — summing the
// same multiset in two different arrival orders can differ in the last bits, and "the same closed
// period re-derived to a different figure" is precisely the drift this phase exists to prevent.

// CostEventSource is the P2.5 substrate, seen from the meter. It is a READ interface on purpose: the
// meter must never be able to write a usage event, because the moment it can, there are two collectors.
//
// Customer attribution is the substrate's job (a run belongs to a customer), not a second tagging pass
// here — see MemCostEvents for the reference implementation of that join.
type CostEventSource interface {
	// CostEvents returns every cost event attributed to customerID whose timestamp falls in p. Order is
	// not significant; the meter sorts.
	CostEvents(customerID string, p Period) ([]metricevent.Event, error)
	// Describe names the substrate for the readiness surface.
	Describe() string
}

// SUMResult is a derived SUM together with the evidence needed to decide whether re-recording it is a
// no-op. The digest is a content hash of the exact event identities that produced the quantity, so an
// identical re-derivation upserts nothing.
type SUMResult struct {
	// Quantity is spend under management for the period, in the cost events' own unit (USD — the
	// customer's OWN provider spend, computed from the customer's own price book; the platform neither
	// resells nor marks up those tokens, see design Decision 9).
	Quantity float64 `json:"quantity"`
	Unit     string  `json:"unit"`
	// SourceDigest is the content hash of the deduped, sorted event identities behind Quantity.
	SourceDigest string `json:"source_digest"`
	// EventCount is how many DISTINCT cost events contributed. Duplicates are excluded, so a redelivery
	// storm does not move this number.
	EventCount int `json:"event_count"`
	// Duplicates is how many redelivered events were recognized and dropped. Surfaced rather than
	// swallowed: a redelivery rate that suddenly jumps is a substrate signal worth seeing.
	Duplicates int `json:"duplicates"`
}

// eventIdentity is the dedup key for one cost event.
//
// It prefers the P2/P2.5 invocation identity ({run_id, node_id, attempt_group}, stamped by the gateway
// as `invocation_id`), which is the SAME string the provider gateway de-dupes the charge on — that is
// what makes "a redelivered cost event does not double-count SUM" true by construction rather than by
// coincidence. When an event predates that stamping, the seven-tag set plus the timestamp is the
// fallback identity; the seven tags are non-null by contract, so this can never collapse two genuinely
// distinct events into one.
func eventIdentity(ev metricevent.Event) string {
	if id, ok := ev.Dimensions[telemetry.AttrInvocationID].(string); ok && id != "" {
		return "inv:" + id
	}
	seed := ""
	if ev.Seed != nil {
		seed = strconv.FormatInt(*ev.Seed, 10)
	}
	return "tag:" + ev.VariantID + "|" + ev.RunID + "|" + ev.NodeID + "|" + ev.CaseID + "|" +
		seed + "|" + ev.ConfigHash + "|" + ev.Timestamp
}

// DeriveSUM aggregates the P2.5 cost events attributed to a customer over a period.
//
// It is deterministic on a closed period: the same events always produce the same quantity and the same
// digest, in any delivery order, with any number of redeliveries.
func DeriveSUM(src CostEventSource, customerID string, p Period) (SUMResult, error) {
	evs, err := src.CostEvents(customerID, p)
	if err != nil {
		return SUMResult{}, fmt.Errorf("metering: read cost events for %s/%s: %w", customerID, p.ID, err)
	}

	seen := make(map[string]float64, len(evs))
	dupes := 0
	for _, ev := range evs {
		if ev.MetricName != telemetry.MetricCostUSD {
			// The source is contracted to return cost events. A non-cost event here is a wiring error, not
			// something to quietly skip: summing latency into spend would be catastrophic and invisible.
			return SUMResult{}, fmt.Errorf("metering: cost-event source returned a %q event — SUM is an aggregation of %q only",
				ev.MetricName, telemetry.MetricCostUSD)
		}
		if ev.Value == nil {
			return SUMResult{}, fmt.Errorf("metering: cost event %s has a nil value — an unpriced call must be surfaced as a P2.5 gap, never summed as zero",
				eventIdentity(ev))
		}
		if !p.Contains(parseTS(ev.Timestamp)) {
			return SUMResult{}, fmt.Errorf("metering: cost-event source returned an event at %s outside period %s", ev.Timestamp, p.ID)
		}
		id := eventIdentity(ev)
		if _, dup := seen[id]; dup {
			dupes++
			continue // redelivery: contributes exactly once (task 2.3)
		}
		seen[id] = *ev.Value
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	h := sha256.New()
	fmt.Fprintf(h, "sum/v1\ncustomer=%s\nperiod=%s\n", customerID, p.ID)
	total := 0.0
	for _, id := range ids {
		total += seen[id]
		fmt.Fprintf(h, "%s=%s\n", id, strconv.FormatFloat(seen[id], 'x', -1, 64))
	}

	return SUMResult{
		Quantity:     total,
		Unit:         telemetry.UnitUSD,
		SourceDigest: hex.EncodeToString(h.Sum(nil)),
		EventCount:   len(ids),
		Duplicates:   dupes,
	}, nil
}

func parseTS(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ─────────────────────────────────────────────────────────────────────────────
// MemCostEvents — the reference CostEventSource
// ─────────────────────────────────────────────────────────────────────────────

// MemCostEvents is an in-memory view of the P2.5 cost events with the run→customer attribution join
// applied. It is the shape a real deployment has, in miniature: cost events arrive from the telemetry
// collector keyed by `run_id`, and the platform knows which customer owns which run.
//
// It deliberately does NOT filter duplicates on ingest. A substrate that silently swallows redeliveries
// would hide whether the METER is idempotent, which is the property under test.
type MemCostEvents struct {
	mu     sync.RWMutex
	owner  map[string]string // run_id -> customer_id
	events []metricevent.Event
}

// NewMemCostEvents builds an empty substrate view.
func NewMemCostEvents() *MemCostEvents {
	return &MemCostEvents{owner: map[string]string{}}
}

// Attribute records that runID's spend belongs to customerID.
func (m *MemCostEvents) Attribute(runID, customerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.owner[runID] = customerID
}

// Put appends a cost event exactly as the collector delivered it — redeliveries included.
func (m *MemCostEvents) Put(ev metricevent.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, ev)
}

// CostEvents returns the customer's cost events inside p.
func (m *MemCostEvents) CostEvents(customerID string, p Period) ([]metricevent.Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []metricevent.Event
	for _, ev := range m.events {
		if ev.MetricName != telemetry.MetricCostUSD {
			continue
		}
		if m.owner[ev.RunID] != customerID {
			continue
		}
		if !p.Contains(parseTS(ev.Timestamp)) {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

// Describe names the substrate.
func (m *MemCostEvents) Describe() string { return "memory:p2.5-cost-events" }
