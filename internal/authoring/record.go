package authoring

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// The APPEND-ONLY authored-change record (task 9.6, FR30).
//
// It follows P12's delivery-record posture, and for the same reason. A transform is produced ONCE and is
// immutable by nature; an authored change has a LIFECYCLE — submitted, later verified, later reverted —
// and forcing a lifecycle-bearing fact into an immutable row was the wrong shape there too. So every
// state change is a NEW entry, and a revert is an ADDITIONAL row rather than an edit to the one it
// undoes: the history of who changed what, and what happened to it, is reconstructable in order.
//
// 🚫 Nothing here is hashed. This record answers "who did this, and may we say anything about it"; it
// never touches the bytes that answer "which configuration is this".

// VerificationState is what the platform is allowed to CLAIM about an authored change (FR25).
//
// It is a state the ledger and every aggregate FILTER ON — not a badge on a card. That distinction is
// the whole of the honesty guarantee: a badge is cosmetic and a refactor can drop it, while a filter
// condition that disappears makes a query fail loudly rather than quietly start including changes
// nobody measured.
type VerificationState string

const (
	// StateUnverified: applied, but the harness never ran. It is outside the verified-delta ledger,
	// contributes zero to every aggregate improvement/savings/quality figure, and never auto-merges.
	StateUnverified VerificationState = "unverified"
	// StateVerified: the harness ran and produced a verdict. The verdict itself lives with verification;
	// this only records that one exists.
	StateVerified VerificationState = "verified"
)

// Countable reports whether a change in this state may contribute to an aggregate figure. It exists as
// a FUNCTION rather than as a comparison at each call site so that every aggregate asks the same
// question in the same place — an `if state != "unverified"` copied into six reports is six chances to
// get it wrong once.
func (s VerificationState) Countable() bool { return s == StateVerified }

// Action is what happened. The vocabulary is closed so a typo cannot invent a state whose consumers
// silently mishandle it.
type Action string

const (
	ActionSubmitted Action = "submitted"
	ActionVerified  Action = "verified"
	ActionReverted  Action = "reverted"
)

// Entry is one immutable row of the authored-change history.
type Entry struct {
	// Seq is the append order and the row's own identity. A monotonic sequence is what lets the history
	// be reconstructed in order independently of clock skew on At.
	Seq int64 `json:"seq"`
	// ChangeID groups every row of one logical authored change.
	ChangeID string `json:"change_id"`
	// Action is what this row records.
	Action Action `json:"action"`

	TenantID string `json:"tenant_id"`
	ActorID  string `json:"actor_id"`

	WorkflowID      string `json:"workflow_id"`
	ParentVariantID string `json:"parent_variant_id"`
	// ConfigHash is the resolved hash of the configuration this change produced.
	ConfigHash string `json:"config_hash"`
	// Axis names the dimensions touched, joined in the closed enum's order.
	Axis string `json:"axis"`
	// DiffRef cites the transform output. An indirect reference: a transform is content-addressed and
	// produced once, so a foreign key would tie a lifecycle-bearing row to an immutable one.
	DiffRef string `json:"diff_ref,omitempty"`

	Origin             string            `json:"origin"`
	ForkedFromProposal string            `json:"forked_from_proposal,omitempty"`
	VerificationState  VerificationState `json:"verification_state"`
	// RevertOf is the change_id this row reverts, on an ActionReverted row.
	RevertOf string    `json:"revert_of,omitempty"`
	At       time.Time `json:"at"`
}

// Recorder appends authored-change history. Append-only: it has no Update and no Delete, and that
// absence is the contract rather than a convention callers are asked to honour.
type Recorder interface {
	Append(ctx context.Context, e Entry) error
	// History returns one change's rows in append order.
	History(ctx context.Context, changeID string) ([]Entry, error)
	// ListByTenant returns a tenant's rows, newest first.
	ListByTenant(ctx context.Context, tenantID string) ([]Entry, error)
}

var (
	// ErrRecordUnreachable is returned when the store cannot be reached. It is distinct from a refusal:
	// "we could not write the audit row" and "we refused your change" are different outcomes and a
	// surface must not render them the same.
	ErrRecordUnreachable = errors.New("authoring: the authored-change record is unreachable")
	// ErrDuplicateSubmit: a second `submitted` row for one change id. The durable schema enforces this
	// with a partial unique index; the in-memory store enforces the same rule so a test against it
	// proves something.
	ErrDuplicateSubmit = errors.New("authoring: this authored change is already submitted")
)

// MemRecorder is the in-memory record. It is faithful to the durable table's two load-bearing rules —
// APPEND-ONLY, and at most one `submitted` row per change id — because a test double that enforced
// neither would prove nothing about the system it stands in for.
type MemRecorder struct {
	mu          sync.Mutex
	entries     []Entry
	nextSeq     int64
	submitted   map[string]bool
	unreachable bool
}

// NewMemRecorder builds an empty record.
func NewMemRecorder() *MemRecorder {
	return &MemRecorder{nextSeq: 1, submitted: map[string]bool{}}
}

// SetUnreachable takes the store offline (or brings it back), for isolation tests.
func (m *MemRecorder) SetUnreachable(down bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unreachable = down
}

// Append adds one immutable row.
func (m *MemRecorder) Append(_ context.Context, e Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unreachable {
		return ErrRecordUnreachable
	}
	if e.Action == ActionSubmitted {
		if m.submitted[e.ChangeID] {
			return ErrDuplicateSubmit
		}
		m.submitted[e.ChangeID] = true
	}
	e.Seq = m.nextSeq
	m.nextSeq++
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	m.entries = append(m.entries, e)
	return nil
}

// History returns a change's rows in append order.
func (m *MemRecorder) History(_ context.Context, changeID string) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unreachable {
		return nil, ErrRecordUnreachable
	}
	var out []Entry
	for _, e := range m.entries {
		if e.ChangeID == changeID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// ListByTenant returns a tenant's rows, newest first.
func (m *MemRecorder) ListByTenant(_ context.Context, tenantID string) ([]Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unreachable {
		return nil, ErrRecordUnreachable
	}
	var out []Entry
	for _, e := range m.entries {
		if e.TenantID == tenantID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq > out[j].Seq })
	return out, nil
}

// CountableAggregate sums a per-change contribution across a tenant's history, skipping everything the
// platform has not measured (FR25, NFR14).
//
// It exists so that every aggregate in the product goes through ONE filter. The failure this prevents is
// not a missing badge — it is an `unverified` change quietly appearing in a "here is what we saved you"
// figure, which is the single most damaging thing this feature could do to the ledger's credibility.
func CountableAggregate(entries []Entry, contribution func(Entry) float64) float64 {
	var total float64
	for _, e := range entries {
		if !e.VerificationState.Countable() {
			continue
		}
		total += contribution(e)
	}
	return total
}
