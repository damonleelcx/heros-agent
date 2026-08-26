package improvementrun

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/eventname"
)

// ledger.go is FR29 and the spec's *"The system SHALL record every run in an append-only ledger"*.
//
// # What the ledger is FOR, which is not "history"
//
// It is the input to the reconciliation pass (design D6). A run interrupted between applying a change
// and delivering it has to be resolvable **with no human step**, and the only thing that can resolve it
// is a record written BEFORE each irreversible step. So the ordering rule here is the same one
// `optimizer.Controller.apply` uses for the change ledger: **the entry is durable before the act**, and
// a ledger that cannot append stops the act rather than producing an unrecorded one.
//
// 🔴 That is why `Append` returns an error and every caller treats it as fatal to the step. A ledger
// write that is best-effort is a ledger that is empty exactly when the reconciliation pass needs it —
// after a crash.

// EntryKind is what happened. A closed set; each value maps to exactly one central event name, so the
// ledger and the health endpoint cannot describe different systems.
type EntryKind string

const (
	// KindPlanCreated — a question became a bounded plan. Written BEFORE anything spends.
	KindPlanCreated EntryKind = "plan_created"
	// KindCandidateGenerated — the enumerator admitted a candidate.
	KindCandidateGenerated EntryKind = "candidate_generated"
	// KindCandidateVerified — a candidate passed the P5.5 gate and was surfaced.
	KindCandidateVerified EntryKind = "candidate_verified"
	// KindCandidateRejected — a candidate was refused before or by verification, with the cause.
	KindCandidateRejected EntryKind = "candidate_rejected"
	// KindProposalApproved / KindProposalDeclined — a person decided.
	KindProposalApproved EntryKind = "proposal_approved"
	KindProposalDeclined EntryKind = "proposal_declined"
	// KindChangeApplied — the change was applied and is about to be re-measured.
	KindChangeApplied EntryKind = "change_applied"
	// KindChangeWithdrawn — re-measurement disagreed (FR16).
	KindChangeWithdrawn EntryKind = "change_withdrawn"
	// KindDeliveryOpened — a pull request exists. 🔴 Written AFTER the forge confirms, never before: a
	// record claiming a pull request that does not exist would make the reconciliation pass skip the
	// delivery it was written to repair.
	KindDeliveryOpened EntryKind = "delivery_opened"
	// KindDeliveryDeduplicated — a second delivery of the same change returned the FIRST one (FR20).
	KindDeliveryDeduplicated EntryKind = "delivery_deduplicated"
	// KindDeliveryPendingForge — the branch is pushed and the forge did not answer (decisions.md
	// D-35.5). This is the entry the reconciliation pass reads to complete the delivery.
	KindDeliveryPendingForge EntryKind = "delivery_pending_forge"
	// KindRunBounded — the run stopped on a bound, named.
	KindRunBounded EntryKind = "run_bounded"
	// KindRunCancelled — the run was cancelled or the kill switch fired.
	KindRunCancelled EntryKind = "run_cancelled"
	// KindRunFaulted — the run ended on a dependency failure. 🚫 Never recorded as a bound.
	KindRunFaulted EntryKind = "run_faulted"
)

var kinds = []EntryKind{
	KindPlanCreated, KindCandidateGenerated, KindCandidateVerified, KindCandidateRejected,
	KindProposalApproved, KindProposalDeclined, KindChangeApplied, KindChangeWithdrawn,
	KindDeliveryOpened, KindDeliveryDeduplicated, KindDeliveryPendingForge,
	KindRunBounded, KindRunCancelled, KindRunFaulted,
}

// Kinds returns the closed set. A copy.
func Kinds() []EntryKind { return append([]EntryKind(nil), kinds...) }

// Valid reports membership. The empty kind is invalid: an entry nobody named is an entry the
// reconciliation pass cannot act on.
func (k EntryKind) Valid() bool {
	for _, v := range kinds {
		if v == k {
			return true
		}
	}
	return false
}

// String makes EntryKind printable.
func (k EntryKind) String() string { return string(k) }

// eventFor maps each kind onto the CENTRAL event name it emits, or "" for a kind that emits none.
//
// 🔴 A table rather than a `fmt.Sprintf("run.%s", kind)`, and the reason is `eventname`'s own: an
// interpolated event name is a free-text field on the far side of a boundary, which is the exfiltration
// shape a closed enum exists to close. `TestEveryLedgerKindMapsToACentralEventName` asserts the table
// is total over the kinds that emit.
var eventFor = map[EntryKind]eventname.Name{
	KindPlanCreated:          eventname.RunPlanCreated,
	KindCandidateVerified:    eventname.RunCandidateVerified,
	KindChangeWithdrawn:      eventname.RunChangeWithdrawn,
	KindDeliveryOpened:       eventname.DeliveryPROpened,
	KindDeliveryDeduplicated: eventname.DeliveryDeduplicated,
}

// EventFor returns the central event name for a kind, and whether it emits one.
func EventFor(k EntryKind) (eventname.Name, bool) {
	n, ok := eventFor[k]
	return n, ok
}

// Entry is one append-only ledger row.
type Entry struct {
	Seq      int64     `json:"seq"`
	RunID    string    `json:"run_id"`
	TenantID string    `json:"tenant_id"`
	Kind     EntryKind `json:"kind"`

	// WorkflowID and Origin are on the `plan_created` entry, and they are there because the
	// RECONCILIATION PASS needs them.
	//
	// 🔴 Found by opening the page. A run reconstructed from the ledger had neither, so `Deliver`
	// resolved `surfaceFor("")` — which is the safe CLI default — and every CONSOLE run completed from
	// the record silently became a CI-mediated one. That failed loudly here (CI-mediated needs no
	// platform writer, so there was nothing to deliver with) and the loudness was luck: the same defect
	// on a deployment configured for both modes would have delivered a console customer's change down a
	// path they have no CI integration for, and reported success.
	//
	// The rule this makes concrete: a ledger entry has to carry everything the repair path needs, not
	// everything the writing path happened to have in scope. The origin decides the delivery MODE (R3)
	// and whether the run may deliver at all (D-35.3), and neither is re-derivable after the fact.
	WorkflowID string    `json:"workflow_id,omitempty"`
	Origin     RunOrigin `json:"origin,omitempty"`
	// Axes is the plan's SCOPE, on the `plan_created` entry.
	//
	// 🔴 Also found by opening the page, and it is the same class of defect as `Origin` one field over.
	// A run reconstructed without it had to assume a scope, and the only available assumption — all
	// nine — makes every out-of-scope axis render as a measured zero. "The plan did not look at this
	// axis" and "this axis produced nothing" are OPPOSITE findings, and `AxisStage.InScope` exists
	// precisely so a bare zero cannot conflate them. Assuming the scope re-created the conflation the
	// field was added to remove.
	Axes []assessment.Axis `json:"axes,omitempty"`

	// PlanID, ProposalID, ConfigHash, SourceRevision and DeliveryID are the join keys the
	// reconciliation pass reads. Present on the entries where they mean something, empty elsewhere.
	PlanID         string `json:"plan_id,omitempty"`
	ProposalID     string `json:"proposal_id,omitempty"`
	ConfigHash     string `json:"config_hash,omitempty"`
	SourceRevision string `json:"source_revision,omitempty"`
	DeliveryID     string `json:"delivery_id,omitempty"`

	// Axis is the per-axis breakdown's source of truth. 🔴 Recorded on the ENTRY rather than derived
	// later from the proposal, because a run that was interrupted has entries and may have no
	// proposals — and "which axes did this run reach" must still be answerable.
	Axis assessment.Axis `json:"axis,omitempty"`

	// Actor is who caused it. A person for an approval, `"run"` for a machine step. 🚫 Never empty on
	// an approval or a decline.
	Actor string `json:"actor,omitempty"`
	// Detail is a named condition, never a raw error string from a dependency.
	Detail string `json:"detail,omitempty"`
	// SpendUSD is the provider spend this entry accounts for.
	SpendUSD float64 `json:"spend_usd,omitempty"`

	AtMS int64 `json:"at_ms"`
}

// Validate refuses an entry the reconciliation pass could not act on.
func (e Entry) Validate() error {
	if !e.Kind.Valid() {
		return fmt.Errorf("improvementrun: %q is not a ledger entry kind", e.Kind)
	}
	if e.RunID == "" || e.TenantID == "" {
		return errors.New("improvementrun: a ledger entry must name its run and its tenant")
	}
	if (e.Kind == KindProposalApproved || e.Kind == KindProposalDeclined) && e.Actor == "" {
		return fmt.Errorf("improvementrun: a %q entry must name the person who decided; a row that "+
			"records a decision and cannot say who made it is worse than no row, because it is believed",
			e.Kind)
	}
	if e.Kind == KindPlanCreated && (e.WorkflowID == "" || !e.Origin.Valid()) {
		return fmt.Errorf("improvementrun: a %q entry must carry the workflow and the origin; without "+
			"them a run reconstructed from this ledger delivers on the wrong surface, which changes "+
			"which credential is used", e.Kind)
	}
	if e.Kind == KindPlanCreated && len(e.Axes) == 0 {
		return fmt.Errorf("improvementrun: a %q entry must carry the plan's axes; without them a "+
			"reconstructed run has to assume a scope, and every out-of-scope axis then renders as a "+
			"measured zero rather than as an axis nobody looked at", e.Kind)
	}
	if e.Kind == KindDeliveryOpened && e.DeliveryID == "" {
		return errors.New("improvementrun: a delivery entry must carry its delivery id, which is what " +
			"the reconciliation pass joins on")
	}
	return nil
}

// ErrLedgerUnavailable is what a fail-closed ledger returns. Callers treat it as fatal to the STEP:
// with the ledger down, an irreversible act would be unrecorded, and an unrecorded act is one the
// reconciliation pass cannot repair.
var ErrLedgerUnavailable = errors.New("improvementrun: the run ledger is unavailable")

// Ledger is the append-only record.
//
// 🚫 There is deliberately no Update and no Delete: "the record is append-only" is a property of the
// interface rather than a rule a caller has to remember — the same shape `forgedelivery.Recorder` takes.
type Ledger interface {
	// Append durably records e (assigning Seq) and returns the assigned sequence number. It MUST be
	// durable before it returns; the write-ahead guarantee depends on it.
	Append(ctx context.Context, e Entry) (int64, error)
	// Entries returns every entry for a run in sequence order. A copy.
	Entries(ctx context.Context, runID string) ([]Entry, error)
	// Unresolved returns the runs that have an entry the reconciliation pass must act on — a change
	// applied with no delivery, or a delivery pending the forge. 🔴 A QUERY rather than a scan of every
	// run, because the reconciliation pass runs every cycle (design D6) and a pass that got slower with
	// history would be the first thing an operator disabled.
	Unresolved(ctx context.Context, tenantID string) ([]Entry, error)
}

// MemLedger is the in-memory append-only ledger for the demo and the tests.
type MemLedger struct {
	mu      sync.Mutex
	seq     int64
	byRun   map[string][]Entry
	byOrder []Entry
	down    bool
}

// NewMemLedger builds an empty ledger.
func NewMemLedger() *MemLedger { return &MemLedger{byRun: map[string][]Entry{}} }

// SetDown flips the store between available and unavailable — the fail-closed seam.
//
// 🔴 It affects READS as well as writes, unlike `optimizer.MemLedger`'s, and the difference is
// deliberate. That one models a write-ahead store whose only fail-closed path is the append; this one
// is also the reconciliation pass's INPUT, and "the ledger is unavailable" has to be able to produce
// the failure that matters there — a pass that cannot read must not report success, because a fresh
// last-success timestamp over a pass that examined nothing makes the staleness signal lie.
func (l *MemLedger) SetDown(down bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.down = down
}

// Append implements Ledger.
func (l *MemLedger) Append(_ context.Context, e Entry) (int64, error) {
	if err := e.Validate(); err != nil {
		return 0, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return 0, ErrLedgerUnavailable
	}
	l.seq++
	e.Seq = l.seq
	l.byRun[e.RunID] = append(l.byRun[e.RunID], e)
	l.byOrder = append(l.byOrder, e)
	return e.Seq, nil
}

// Entries implements Ledger.
func (l *MemLedger) Entries(_ context.Context, runID string) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return nil, ErrLedgerUnavailable
	}
	return append([]Entry(nil), l.byRun[runID]...), nil
}

// Unresolved implements Ledger: entries describing an act whose completion is not recorded.
//
// The rule is stated once, here, rather than in the reconciliation pass: an applied change with no
// delivery, and a delivery pending the forge with no delivery opened, are the two interrupted states
// (design D6, decisions.md D-35.5). Everything else is resolved.
func (l *MemLedger) Unresolved(_ context.Context, tenantID string) ([]Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.down {
		return nil, ErrLedgerUnavailable
	}

	opened := map[string]bool{}    // proposal id -> a delivery exists
	withdrawn := map[string]bool{} // proposal id -> it was withdrawn, so no delivery is owed
	for _, e := range l.byOrder {
		if e.TenantID != tenantID {
			continue
		}
		switch e.Kind {
		case KindDeliveryOpened, KindDeliveryDeduplicated:
			opened[e.ProposalID] = true
		case KindChangeWithdrawn:
			withdrawn[e.ProposalID] = true
		}
	}
	var out []Entry
	for _, e := range l.byOrder {
		if e.TenantID != tenantID {
			continue
		}
		if e.Kind != KindChangeApplied && e.Kind != KindDeliveryPendingForge {
			continue
		}
		if opened[e.ProposalID] || withdrawn[e.ProposalID] {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

var _ Ledger = (*MemLedger)(nil)
