package billing

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ledger.go is the append-only `billing_event` ledger — the platform's audit of what was charged, when,
// and why.
//
// TWO PROPERTIES, BOTH STRUCTURAL:
//
//  1. `IdempotencyKey` is UNIQUE. A duplicate charge row cannot persist, so "never double-charge" does
//     not depend on a caller remembering to check first (design Decision 5).
//  2. The ledger is APPEND-ONLY. A correction is a NEW row; nothing is deleted or edited (design
//     Decision 6). The single exception is the write-ahead completion below — and it is narrow enough
//     to state exactly.
//
// ## The one permitted mutation, and why it is not an edit
//
// A charge is recorded LOCALLY BEFORE the provider is called (write-ahead), so an ambiguous provider
// failure leaves a durable record of the attempt instead of an invisible maybe-charge. `Settle` later
// stamps the provider's refs onto that row and moves it `pending -> recorded`.
//
// That is completion, not revision: it fills in fields that were unknowable at write time and can never
// change a decision, a customer, a period, a kind, or a key. It is the same discipline
// `optimizer.ChangeLedger.Backfill` applies to a merge commit — the decision is committed first, the
// external world's receipt is attached after. Settle refuses to touch an already-settled row, and the
// Postgres trigger enforces the identical rule at the storage layer, so neither is the only guard.

// EventType is one kind of ledger row.
type EventType string

const (
	// TypeCharge is a subscription or metered charge.
	TypeCharge EventType = "charge"
	// TypeGainshareCharge is a verified-savings charge. Distinct from TypeCharge on purpose: gainshare
	// has different evidence requirements, and a query that must find "everything we billed for savings"
	// should not have to guess from a kind column.
	TypeGainshareCharge EventType = "gainshare_charge"
	// TypeCredit is an additive balance correction.
	TypeCredit EventType = "credit"
	// TypeRefund is an additive money-back correction.
	TypeRefund EventType = "refund"
	// TypeSubscriptionChange records a subscription create/cancel.
	TypeSubscriptionChange EventType = "subscription_change"
	// TypePlanChange records an account moving between plans.
	TypePlanChange EventType = "plan_change"
)

// EventTypes is every ledger row type.
var EventTypes = []EventType{
	TypeCharge, TypeGainshareCharge, TypeCredit, TypeRefund, TypeSubscriptionChange, TypePlanChange,
}

// ChargeBearing reports whether a row moves money. Credits and refunds do too — in the customer's
// favour — which is why reversibility is tested by NET effect, not by counting charges.
func (t EventType) ChargeBearing() bool {
	switch t {
	case TypeCharge, TypeGainshareCharge, TypeCredit, TypeRefund:
		return true
	}
	return false
}

// Status is a ledger row's settlement state.
type Status string

const (
	// StatusPending means the row was written ahead of the provider call and the provider has not
	// confirmed. This is the BUFFERED state a provider outage leaves rows in.
	StatusPending Status = "pending"
	// StatusRecorded means the provider confirmed and its refs are stamped on the row.
	StatusRecorded Status = "recorded"
)

// BillingEvent is one append-only ledger row.
//
// Note the absence: there is no amount. `AmountRef` is an opaque provider handle, so the platform can
// point at what a charge came to without ever storing money.
type BillingEvent struct {
	EventID    string     `json:"event_id"`
	CustomerID string     `json:"customer_id"`
	Period     string     `json:"period"`
	Type       EventType  `json:"type"`
	Kind       ChargeKind `json:"kind,omitempty"`
	// IdempotencyKey is UNIQUE across the ledger and is the SAME key handed to the provider.
	IdempotencyKey string `json:"idempotency_key"`
	// ProviderRef / AmountRef are opaque provider handles, empty until Settle.
	ProviderRef string `json:"provider_ref,omitempty"`
	AmountRef   string `json:"amount_ref,omitempty"`
	// CausedBy names the platform record that justified this row ("usage_record:cus_a/2026-07/sum",
	// "billing_event:<id>" for a correction). It is what makes a period reconstructable.
	CausedBy string `json:"caused_by"`
	// Reason is a short, non-sensitive explanation. On a correction it is REQUIRED: a credit nobody can
	// explain is indistinguishable from a mistake.
	Reason    string     `json:"reason,omitempty"`
	Quantity  float64    `json:"quantity"`
	Status    Status     `json:"status"`
	Evidence  []string   `json:"evidence,omitempty"` // verified-delta refs / merge commits, for gainshare
	CreatedAt time.Time  `json:"created_at"`
	SettledAt *time.Time `json:"settled_at,omitempty"`
}

// Ledger errors.
var (
	// ErrDuplicateKey is returned by Append when the idempotency key already exists. It is a NORMAL
	// outcome of a retry, not a failure — the caller reads the existing row and returns it.
	ErrDuplicateKey  = errors.New("billing: idempotency key already recorded")
	ErrEventNotFound = errors.New("billing: no such billing event")
	ErrLedgerUnavail = errors.New("billing: billing ledger unavailable")
	// ErrAlreadySettled guards the write-ahead completion: a settled row is finished, and re-stamping it
	// would let a second provider call overwrite the first one's receipt.
	ErrAlreadySettled = errors.New("billing: billing event is already settled")
	ErrNoReason       = errors.New("billing: a correction must carry a reason")
	ErrNoCause        = errors.New("billing: a billing event must name what caused it")
)

// Ledger is the append-only billing-event store.
type Ledger interface {
	// Append records ev. It returns ErrDuplicateKey (with the EXISTING row) when the idempotency key is
	// already present — never a second row.
	Append(ev BillingEvent) (BillingEvent, error)
	// ByKey returns the row for an idempotency key.
	ByKey(idempotencyKey string) (BillingEvent, error)
	// Settle stamps the provider refs onto a pending row and marks it recorded. It NEVER changes the
	// customer, period, type, kind, key, quantity or cause.
	Settle(idempotencyKey, providerRef, amountRef string, at time.Time) (BillingEvent, error)
	// Events returns every row for a customer-period in append order. An empty period returns all of
	// the customer's rows.
	Events(customerID, period string) []BillingEvent
	// Pending returns every row still awaiting provider confirmation — the buffer a provider outage
	// fills and recovery drains.
	Pending() []BillingEvent
}

// MemLedger is the in-memory append-only Ledger. The `byKey` map IS the UNIQUE constraint, so this has
// the same never-two-rows property the Postgres index gives.
type MemLedger struct {
	mu     sync.Mutex
	rows   []BillingEvent
	byKey  map[string]int // idempotency key -> index into rows
	seq    int
	down   bool
	writes int
}

// NewMemLedger builds an empty ledger.
func NewMemLedger() *MemLedger { return &MemLedger{byKey: map[string]int{}} }

// SetDown flips the ledger between available and unavailable — the fail-closed seam.
func (l *MemLedger) SetDown(down bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.down = down
}

// Writes counts accepted Append calls, so a test can prove it genuinely retried.
func (l *MemLedger) Writes() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.writes
}

// Append records ev, or returns the existing row for a duplicate key.
func (l *MemLedger) Append(ev BillingEvent) (BillingEvent, error) {
	if err := validate(ev); err != nil {
		return BillingEvent{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return BillingEvent{}, ErrLedgerUnavail
	}
	l.writes++
	if i, ok := l.byKey[ev.IdempotencyKey]; ok {
		return l.rows[i], fmt.Errorf("%w: %s", ErrDuplicateKey, ev.IdempotencyKey)
	}
	l.seq++
	if ev.EventID == "" {
		ev.EventID = fmt.Sprintf("be_%06d", l.seq)
	}
	if ev.Status == "" {
		ev.Status = StatusPending
	}
	l.rows = append(l.rows, ev)
	l.byKey[ev.IdempotencyKey] = len(l.rows) - 1
	return ev, nil
}

func validate(ev BillingEvent) error {
	if ev.CustomerID == "" {
		return errors.New("billing: a billing event needs a customer")
	}
	if ev.IdempotencyKey == "" {
		return errors.New("billing: a billing event needs an idempotency key — the key IS the never-double-charge guarantee")
	}
	known := false
	for _, t := range EventTypes {
		if t == ev.Type {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("billing: unknown billing event type %q", ev.Type)
	}
	if ev.Kind != "" && !KnownChargeKind(ev.Kind) {
		return fmt.Errorf("billing: unknown charge kind %q — the kinds are a closed set precisely so a "+
			"resold-token line cannot be invented at a call site", ev.Kind)
	}
	if ev.CausedBy == "" {
		return fmt.Errorf("%w (%s)", ErrNoCause, ev.Type)
	}
	if (ev.Type == TypeCredit || ev.Type == TypeRefund) && ev.Reason == "" {
		return fmt.Errorf("%w (%s)", ErrNoReason, ev.Type)
	}
	return nil
}

// ByKey returns the row for an idempotency key.
func (l *MemLedger) ByKey(key string) (BillingEvent, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return BillingEvent{}, ErrLedgerUnavail
	}
	i, ok := l.byKey[key]
	if !ok {
		return BillingEvent{}, fmt.Errorf("%w: %s", ErrEventNotFound, key)
	}
	return l.rows[i], nil
}

// Settle completes a write-ahead row with the provider's refs. See the file comment for why this is
// completion rather than an edit.
func (l *MemLedger) Settle(key, providerRef, amountRef string, at time.Time) (BillingEvent, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return BillingEvent{}, ErrLedgerUnavail
	}
	i, ok := l.byKey[key]
	if !ok {
		return BillingEvent{}, fmt.Errorf("%w: %s", ErrEventNotFound, key)
	}
	if l.rows[i].Status == StatusRecorded {
		return l.rows[i], fmt.Errorf("%w: %s", ErrAlreadySettled, key)
	}
	t := at.UTC()
	l.rows[i].ProviderRef, l.rows[i].AmountRef = providerRef, amountRef
	l.rows[i].Status, l.rows[i].SettledAt = StatusRecorded, &t
	return l.rows[i], nil
}

// Events returns a customer's rows in append order.
func (l *MemLedger) Events(customerID, period string) []BillingEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []BillingEvent
	for _, r := range l.rows {
		if r.CustomerID != customerID {
			continue
		}
		if period != "" && r.Period != period {
			continue
		}
		out = append(out, r)
	}
	return out
}

// Pending returns every unsettled row, oldest first — the outage buffer.
func (l *MemLedger) Pending() []BillingEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []BillingEvent
	for _, r := range l.rows {
		if r.Status == StatusPending {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// The idempotency keys are DERIVED, never typed at a call site: the key IS the identity of the
// operation, and two call sites that spell it differently is a double-charge with a green test suite.
// The plan config version is deliberately not part of any of them — a re-report of the same period's
// same meter is the same operation even if packaging was republished in between.
//
// Each key carries an OPERATION PREFIX, and that prefix is load-bearing rather than cosmetic. A billing
// provider's idempotency-key namespace is global per account: reusing one key across two different
// operations makes the provider return the FIRST operation's record for the second call. Reporting
// usage and charging for that usage under one key therefore silently yields a usage ref where a charge
// ref was expected, and no charge at all. Distinct prefixes make that unrepresentable — and
// StubProvider additionally REFUSES a key reused across operations, so the mistake fails loudly rather
// than becoming a missing invoice line nobody notices.

// UsageReportIdempotencyKey identifies the metered-usage REPORT for {customer, period, metric}.
func UsageReportIdempotencyKey(customerID, period, metric string) string {
	return fmt.Sprintf("usage_report:%s:%s:%s", customerID, period, metric)
}

// MeteredChargeIdempotencyKey identifies the metered CHARGE for {customer, period, metric}.
func MeteredChargeIdempotencyKey(customerID, period, metric string) string {
	return fmt.Sprintf("charge_metered:%s:%s:%s", customerID, period, metric)
}

// SubscriptionChargeIdempotencyKey identifies a period's subscription charge.
func SubscriptionChargeIdempotencyKey(customerID, period, planID string) string {
	return fmt.Sprintf("charge_subscription:%s:%s:%s", customerID, period, planID)
}

// GainshareIdempotencyKey identifies a period's gainshare charge.
func GainshareIdempotencyKey(customerID, period string) string {
	return fmt.Sprintf("charge_gainshare:%s:%s", customerID, period)
}

// CorrectionIdempotencyKey derives the key for a correction against a specific event. The correcting
// reason is part of the key so two DIFFERENT corrections against one charge remain distinct, while a
// retry of the SAME correction is recognized as one.
func CorrectionIdempotencyKey(kind EventType, againstEventID, reason string) string {
	return fmt.Sprintf("%s:%s:%s", kind, againstEventID, reason)
}
