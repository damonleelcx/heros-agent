package improvementrun

import (
	"context"
	"fmt"

	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/plancfg"
)

// bounds.go derives a tenant's run bounds from what they contracted for.
//
// # Why the bounds come from the ENTITLEMENT and not from a configuration key
//
// A run's spend budget is the only thing standing between one typed sentence and an invoice, and the
// person typing the sentence is the one least able to price it. So the number has to come from
// somewhere the person cannot reach — and the only place in this platform that already knows what a
// customer agreed to pay for is `internal/entitlement`.
//
// A deployment-wide configuration key would be the easy alternative and it is wrong twice: it gives
// every tenant the same ceiling regardless of plan, and it is editable by whoever can edit the
// deployment's environment, which on a self-hosted install is the customer.
//
// # 🔴 An unentitled tenant gets ZERO, not a small default
//
// `Translate` refuses a zero budget with `RefusedNoBudget`, naming the plan action. That is the whole
// point: "this organization has not bought improvement runs" and "this organization has a tiny budget"
// are different facts, and giving the first one a small default would silently spend money for a
// customer who never agreed to any.

// PlanBounds is one plan's run allowance.
//
// It is DATA rather than a computation over `plancfg.Plan`, because the allowance is a commercial
// decision somebody makes per plan, and deriving it from (say) the seat count would make a customer's
// spend ceiling move when they added a seat — which is a surprise on an invoice.
type PlanBounds struct {
	// MaxCandidates is how many distinct candidates one run may enumerate.
	MaxCandidates int
	// MaxSpendUSD is the cumulative provider-spend ceiling for one run.
	MaxSpendUSD float64
}

// EntitlementBounds reads a tenant's run allowance from the entitlement gate.
type EntitlementBounds struct {
	// Gate answers whether this customer's plan includes opening a verified pull request. 🔴 The run's
	// bounds are keyed to `FeatureAssistedPR` rather than to a feature of their own, because that is
	// the entitlement an improvement run actually consumes — it ends in a pull request, and P7 already
	// gates that. A second feature key would be a second place the same boundary is decided.
	Gate *entitlement.Gate
	// ByPlan is the per-plan allowance. A plan absent from this map yields no allowance, which
	// `Translate` refuses by name — never a default.
	ByPlan map[string]PlanBounds
	// Subject resolves the tenant's workflow and its current source revision. Without both, `Translate`
	// refuses with `no_subject` or `no_source_revision` rather than running unpinned.
	Subject func(ctx context.Context, tenantID string) (workflowID, sourceRevision string, err error)
}

// BoundsFor implements BoundsSource.
//
// 🔴 A denial is not an error. An organization whose plan does not include improvement runs is a real,
// ordinary state, and returning an error would make it indistinguishable from the entitlement store
// being unreachable — which is ours to fix and theirs to wait for. The denial travels as a ZERO
// allowance, which `Translate` turns into `RefusedNoBudget` with the plan action named.
func (b EntitlementBounds) BoundsFor(ctx context.Context, tenantID string) (Bounds, error) {
	out := Bounds{TenantID: tenantID}
	if b.Subject != nil {
		wf, rev, err := b.Subject(ctx, tenantID)
		if err != nil {
			return Bounds{}, fmt.Errorf("improvementrun: resolving this organization's workflow: %w", err)
		}
		out.WorkflowID, out.SourceRevision = wf, rev
	}
	if b.Gate == nil {
		// No commercial gate configured — correct for a self-hosted or pre-commercial deployment. The
		// allowance is then whatever `ByPlan` names under the empty plan id, and absent that it is zero,
		// which refuses by name rather than running unbounded.
		pb := b.ByPlan[""]
		out.MaxCandidates, out.MaxSpendUSD = pb.MaxCandidates, pb.MaxSpendUSD
		return out, nil
	}
	dec, err := b.Gate.CheckEntitlement(tenantID, plancfg.FeatureAssistedPR, entitlement.LevelAssisted)
	if err != nil {
		return Bounds{}, fmt.Errorf("improvementrun: reading this organization's entitlement: %w", err)
	}
	if !dec.Allowed {
		// Zero, deliberately. See the doc comment.
		return out, nil
	}
	pb := b.ByPlan[dec.PlanID]
	out.MaxCandidates, out.MaxSpendUSD = pb.MaxCandidates, pb.MaxSpendUSD
	return out, nil
}

var _ BoundsSource = EntitlementBounds{}
