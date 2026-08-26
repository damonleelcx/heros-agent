package improvementrun

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// acknowledge.go is FR2: the plan is shown before execution, and above the disclosure threshold
// execution does not begin until it is acknowledged.
//
// # Why an acknowledgement is a RECORD and not a request field
//
// The obvious implementation is `{"question": "...", "acknowledged": true}` on the run request, and it
// is wrong in the way that matters: a client that always sets it — a retry wrapper, a script somebody
// wrote, an SDK with a convenient default — turns the disclosure into a formality, and nothing about
// the system looks different afterwards. The threshold would still be enforced, the flag would still be
// checked, and no person would ever have seen a plan.
//
// So the acknowledgement is recorded against a PLAN ID, and the plan id is derived from every field
// that changes what the run will do (`NewPlanID`). Acknowledging is therefore acknowledging a specific
// scope at a specific budget, and a plan whose budget moved has a different id and needs a new one.
// That is the same reasoning design D4 applies to approvals, one step earlier: **consent is bound to
// the thing consented to.**
//
// # 🔴 The actor comes from the session
//
// `approval.Approve` refuses an empty actor because an audit row that cannot say who approved is worse
// than no row, and the same applies here for the same reason. Below the threshold no acknowledgement is
// required at all, so there is no case where defaulting the actor would be convenient.

// Acknowledgement is a person's agreement to a specific plan.
type Acknowledgement struct {
	PlanID   string `json:"plan_id"`
	TenantID string `json:"tenant_id"`
	// By is the person, from the AUTHENTICATED SESSION. 🚫 Nothing request-supplied may reach it.
	By string `json:"by"`
	// ProjectedSpendUSD is what they were shown. Recorded on the acknowledgement as well as on the plan
	// so a dispute is answerable from the acknowledgement alone — "what number was on the screen" must
	// not require re-deriving the plan from inputs that may have moved.
	ProjectedSpendUSD float64 `json:"projected_spend_usd"`
	AtMS              int64   `json:"at_ms"`
}

// Validate refuses an acknowledgement that cannot be audited.
func (a Acknowledgement) Validate() error {
	if a.PlanID == "" {
		return errors.New("improvementrun: an acknowledgement must name the plan it acknowledges")
	}
	if a.TenantID == "" {
		return errors.New("improvementrun: an acknowledgement must name its tenant")
	}
	if a.By == "" {
		return errors.New("improvementrun: an acknowledgement must name the person who gave it; a row " +
			"that says a plan was acknowledged and cannot say by whom is worse than no row, because it " +
			"is believed")
	}
	return nil
}

// AckStore records and reads acknowledgements.
//
// An interface so a deployment can hold them wherever it holds its other consent records, and so the
// gate below is testable without a database. There is deliberately no Delete: an acknowledgement is a
// fact about a moment, and un-acknowledging is expressed by the plan id changing.
type AckStore interface {
	Record(ctx context.Context, a Acknowledgement) error
	// Acknowledged returns the acknowledgement for a plan, and ok=false when there is none. An error is
	// a READ FAILURE and must never be reported as "not acknowledged" — see RequireAcknowledgement.
	Acknowledged(ctx context.Context, tenantID, planID string) (Acknowledgement, bool, error)
}

// ErrAwaitingAcknowledgement is the run's terminal state when a plan above the threshold has not been
// acknowledged. It is a CONDITION the surface renders with the plan beside it, not a failure: the whole
// point is that the person now looks at the plan.
var ErrAwaitingAcknowledgement = errors.New(
	"improvementrun: this plan's projected spend is above the disclosure threshold and has not been " +
		"acknowledged, so nothing has run and nothing has been spent")

// RequireAcknowledgement is the gate. It returns nil when the run may begin.
//
// 🔴 A read failure FAILS CLOSED — it withholds the run rather than admitting it. The direction is the
// one that costs nothing to be wrong about: refusing a run that was acknowledged wastes a click, while
// admitting a run that was not spends money nobody agreed to. This is the same fail-closed contract
// `forgedelivery.HaltReader` carries, and it is stated here rather than assumed because the tempting
// reading of `(Acknowledgement{}, false, err)` is "not found".
func RequireAcknowledgement(ctx context.Context, store AckStore, p Plan) error {
	if !p.RequiresAcknowledgement() {
		return nil
	}
	if store == nil {
		return fmt.Errorf("%w: no acknowledgement store is configured, so this plan cannot be "+
			"acknowledged at all", ErrAwaitingAcknowledgement)
	}
	ack, ok, err := store.Acknowledged(ctx, p.TenantID, p.PlanID)
	if err != nil {
		return fmt.Errorf("%w: the acknowledgement could not be read, so the run is withheld: %v",
			ErrAwaitingAcknowledgement, err)
	}
	if !ok {
		return ErrAwaitingAcknowledgement
	}
	if ack.By == "" {
		// A stored row with no actor is a defect upstream of here, and admitting the run on it would
		// launder that defect into an authorization.
		return fmt.Errorf("%w: the stored acknowledgement names nobody", ErrAwaitingAcknowledgement)
	}
	return nil
}

// MemAckStore is the in-memory acknowledgement store used by the demo and the tests. A deployment with
// a database implements AckStore over it; the gate above does not change.
type MemAckStore struct {
	mu sync.Mutex
	by map[string]Acknowledgement // tenant\x00plan -> ack
}

// NewMemAckStore builds an empty store.
func NewMemAckStore() *MemAckStore { return &MemAckStore{by: map[string]Acknowledgement{}} }

// Record implements AckStore.
func (s *MemAckStore) Record(_ context.Context, a Acknowledgement) error {
	if err := a.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := a.TenantID + "\x00" + a.PlanID
	if _, exists := s.by[key]; exists {
		// 🔴 First writer wins. A second acknowledgement of the same plan is a double-click or a replay,
		// and re-stamping would overwrite who actually agreed — the same guard `approval.Approve` puts
		// on `status = 'pending'`, for the same reason.
		return nil
	}
	s.by[key] = a
	return nil
}

// Acknowledged implements AckStore.
func (s *MemAckStore) Acknowledged(_ context.Context, tenantID, planID string) (Acknowledgement, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.by[tenantID+"\x00"+planID]
	return a, ok, nil
}

var _ AckStore = (*MemAckStore)(nil)
