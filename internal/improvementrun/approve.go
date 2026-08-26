package improvementrun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/heros-foreal/agentd/internal/approval"
	"github.com/heros-foreal/agentd/internal/assessment"
)

// approve.go is FR11–FR14 and design D4: approval is PER PROPOSAL, routed through `internal/approval`,
// and bound to `(config_hash, source_revision)`.
//
// # 🚫 Why there is no "approve all", written down in advance
//
// Design D4 predicts the request and this is where it gets refused. A bundle approval is one click that
// means several things, and the person will read the first item and accept the rest. It is the most
// predictable and most dangerous convenience in this phase, so the refusal is structural: `Approve`
// takes ONE proposal id, there is no plural form, and `TestNoBulkApprovalPathExists` asserts by
// reflection that no exported method on this package takes a list of them.
//
// # Why the binding lives HERE and `internal/approval` is untouched
//
// `internal/approval` is the consent primitive: it records WHO approved and WHEN, and refuses an empty
// actor. It has no `config_hash` or `source_revision` column, and adding two would be a migration on a
// shipped table for a fact this package already holds — `careful-table-creation`'s first question,
// answered by not asking it.
//
// So the BINDING is recorded on the run ledger (which P35 owns) and verified here, before the consent
// is acted on. An approval whose subject has moved is not deleted; it is reported VOID with the reason,
// and re-requested. That distinction matters: a deleted approval looks like one that was never given,
// and the person who gave it is entitled to see that it was.
//
// # 🔴 Why "void" rather than "still valid, we will use the new diff"
//
// An approval that survives a revision change is an approval for a diff nobody saw. This is the same
// reasoning P10 applies to measurement runs — what ran is reconciled against what was requested —
// applied to consent.

// DecisionState is what a person decided about one proposal. A closed set.
type DecisionState string

const (
	// DecisionPending — surfaced, not yet decided.
	DecisionPending DecisionState = "pending"
	// DecisionApproved — a person approved it, and the binding held.
	DecisionApproved DecisionState = "approved"
	// DecisionDeclined — a person declined it. 🔴 The proposal STAYS VISIBLE with this recorded (FR12):
	// a proposal that disappeared when it was declined looks like one that was never made.
	DecisionDeclined DecisionState = "declined"
	// DecisionVoid — an approval was given and its subject moved, so it is void and re-requested
	// (FR13). Distinct from `pending`, and the distinction is what the surface renders: "you approved
	// this and the revision moved" is a different sentence from "this is waiting for you".
	DecisionVoid DecisionState = "void"
)

var decisionStates = []DecisionState{DecisionPending, DecisionApproved, DecisionDeclined, DecisionVoid}

// DecisionStates returns the closed set. A copy.
func DecisionStates() []DecisionState { return append([]DecisionState(nil), decisionStates...) }

// Valid reports membership.
func (d DecisionState) Valid() bool {
	for _, v := range decisionStates {
		if v == d {
			return true
		}
	}
	return false
}

// String makes DecisionState printable.
func (d DecisionState) String() string { return string(d) }

// Binding is what an approval is an approval OF.
//
// 🔴 Both halves, always. A binding on `config_hash` alone would survive the source moving underneath a
// diff that still resolves to the same configuration; a binding on `source_revision` alone would
// survive the configuration being regenerated at the same revision. Either one alone is an approval for
// something nobody saw.
type Binding struct {
	ConfigHash     string `json:"config_hash"`
	SourceRevision string `json:"source_revision"`
}

// Equal reports whether two bindings describe the same subject.
func (b Binding) Equal(o Binding) bool {
	return b.ConfigHash == o.ConfigHash && b.SourceRevision == o.SourceRevision
}

// Complete reports whether both halves are present. An incomplete binding is refused rather than
// treated as a wildcard.
func (b Binding) Complete() bool { return b.ConfigHash != "" && b.SourceRevision != "" }

// Hash is a stable content address for the binding, for a store that wants one key.
func (b Binding) Hash() string {
	sum := sha256.Sum256([]byte("improvementrun.binding\x00" + b.ConfigHash + "\x00" + b.SourceRevision))
	return hex.EncodeToString(sum[:16])
}

// Decision is one proposal's approval state.
type Decision struct {
	ProposalID string          `json:"proposal_id"`
	Axis       assessment.Axis `json:"axis"`
	State      DecisionState   `json:"state"`
	// By is the person, from the AUTHENTICATED SESSION. 🚫 Never request-supplied, never defaulted.
	By   string `json:"by,omitempty"`
	AtMS int64  `json:"at_ms,omitempty"`
	// Binding is what was decided about. Carried on the decision so a reader can see what an approval
	// covered without resolving it from somewhere else.
	Binding Binding `json:"binding"`
	// VoidReason names WHICH half moved, for a `void` decision. 🔴 "The revision moved" and "the
	// configuration was regenerated" send a person to two different places.
	VoidReason string `json:"void_reason,omitempty"`
}

// Sentence is what a surface says about this decision. One per state, none of them "unknown".
func (d Decision) Sentence() string {
	switch d.State {
	case DecisionApproved:
		return "Approved by " + d.By + "."
	case DecisionDeclined:
		return "Declined by " + d.By + ". It stays here with that recorded."
	case DecisionVoid:
		return "This approval is no longer valid: " + d.VoidReason + ". It has been asked for again."
	default:
		return "Waiting for a decision."
	}
}

// ErrApprovalVoid is returned when an approval's subject has moved (FR13).
var ErrApprovalVoid = errors.New(
	"improvementrun: this approval is void because what it approved has changed; an approval that " +
		"survived a revision change would be an approval for a diff nobody saw")

// ErrNotSurfaced is returned when a decision names a proposal this run never surfaced.
var ErrNotSurfaced = errors.New("improvementrun: this run surfaced no such proposal")

// ApprovalGate is the consent primitive. `internal/approval` is the ONLY implementation that matters;
// this is an interface so the binding logic above is testable without a database, and so this package
// never learns about `*sql.DB`.
//
// 🚫 There is deliberately no `ApproveAll`. See the file header.
type ApprovalGate interface {
	// Submit queues a proposal for review and returns the id the gate knows it by.
	Submit(ctx context.Context, tenantID string, p VerifiedProposal) (gateID string, err error)
	// Approve records a person's authorization. It MUST refuse an empty actor.
	Approve(ctx context.Context, gateID, approvedBy string) error
	// Decline records a refusal.
	Decline(ctx context.Context, gateID, declinedBy string) error
}

// SQLApprovalGate routes to `internal/approval` over a database handle.
//
// 🔴 It is a thin adapter and it adds NO logic: the layer that decides whether a person may approve is
// `internal/approval`, and a second decision here would be a second place the entitlement check, the
// automation-level check and the attribution could be wrong.
type SQLApprovalGate struct {
	// Store is the platform's approval store — `internal/approval` over the platform database.
	Store ApprovalStore
	// Layer is the approval layer these proposals are filed under. It is a REQUIRED field rather than a
	// default, because `approval.Layer` is a closed vocabulary a reviewer filters on, and a proposal
	// filed under the wrong one is invisible to the person who reviews that layer.
	Layer approval.Layer
}

// ApprovalStore is the narrow slice of `internal/approval` this adapter needs.
//
// 🔴 An interface rather than a `*sql.DB`, so this package does not import `database/sql` for one type
// and so a test can drive every refusal without a database. The implementation lives beside the
// database handle — `internal/launch` — which is where the decision about WHICH database this is
// already lives.
type ApprovalStore interface {
	// Submit queues a proposal and returns the id the store knows it by.
	Submit(tenantID string, layer approval.Layer, title, rationale, diff string) (string, error)
	// Approve records a person's authorization. It MUST refuse an empty actor.
	Approve(id, approvedBy string) error
	// Decline records a refusal.
	Decline(id string) error
}

// Submit implements ApprovalGate.
func (g SQLApprovalGate) Submit(_ context.Context, tenantID string, p VerifiedProposal) (string, error) {
	if g.Store == nil {
		return "", fmt.Errorf("%w: no approval store is configured, so nothing can be approved",
			ErrNotConfigured)
	}
	if g.Layer == "" {
		return "", fmt.Errorf("%w: this approval gate names no layer, and a proposal filed under the "+
			"wrong layer is invisible to the person who reviews that layer", ErrNotConfigured)
	}
	title := fmt.Sprintf("%s on %s (%s)", p.Operator, p.Node, p.Axis)
	// 🔴 The BINDING travels in the rationale, verbatim, because that is what a human reviewer reads.
	// It is ALSO checked structurally by `Service.Approve` — this is the copy for the person, not the
	// copy that enforces anything, and saying so here stops a future reader from parsing it.
	rationale := fmt.Sprintf("%s\n\nVerified delta: %s\nConfiguration: %s\nSource revision: %s\n"+
		"Provider model version: %s",
		p.Rationale, p.DeltaLabel(), p.ConfigHash, p.SourceRevision, p.ProviderModelVersion)
	return g.Store.Submit(tenantID, g.Layer, title, rationale, p.DiffRef)
}

// Approve implements ApprovalGate.
func (g SQLApprovalGate) Approve(_ context.Context, gateID, approvedBy string) error {
	if g.Store == nil {
		return fmt.Errorf("%w: no approval store is configured", ErrNotConfigured)
	}
	// 🚫 The empty-actor refusal is `approval.Approve`'s, not repeated here. Two places refusing the
	// same thing is two places one of them can be relaxed.
	return g.Store.Approve(gateID, approvedBy)
}

// Decline implements ApprovalGate.
func (g SQLApprovalGate) Decline(_ context.Context, gateID, _ string) error {
	if g.Store == nil {
		return fmt.Errorf("%w: no approval store is configured", ErrNotConfigured)
	}
	return g.Store.Decline(gateID)
}

var _ ApprovalGate = SQLApprovalGate{}

// MemApprovalGate is the in-memory gate for the demo and the tests. It reproduces the two refusals that
// matter — an empty actor, and a second decision on an already-decided proposal — because a double
// that accepted both would let a fence pass over a service that relies on them.
type MemApprovalGate struct {
	mu       sync.Mutex
	next     int
	decided  map[string]string
	declined map[string]bool
}

// NewMemApprovalGate builds an empty gate.
func NewMemApprovalGate() *MemApprovalGate {
	return &MemApprovalGate{decided: map[string]string{}, declined: map[string]bool{}}
}

// Submit implements ApprovalGate.
func (g *MemApprovalGate) Submit(_ context.Context, _ string, p VerifiedProposal) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return fmt.Sprintf("gate_%d_%s", g.next, p.ProposalID), nil
}

// Approve implements ApprovalGate.
func (g *MemApprovalGate) Approve(_ context.Context, gateID, approvedBy string) error {
	if approvedBy == "" {
		return errors.New("approval: an approval must name the person who gave it")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if who, done := g.decided[gateID]; done {
		return fmt.Errorf("approval: %s was already decided by %s", gateID, who)
	}
	g.decided[gateID] = approvedBy
	return nil
}

// Decline implements ApprovalGate.
func (g *MemApprovalGate) Decline(_ context.Context, gateID, declinedBy string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if who, done := g.decided[gateID]; done {
		return fmt.Errorf("approval: %s was already decided by %s", gateID, who)
	}
	g.decided[gateID] = declinedBy
	g.declined[gateID] = true
	return nil
}

var _ ApprovalGate = (*MemApprovalGate)(nil)
