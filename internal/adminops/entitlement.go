package adminops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/plancfg"
)

// entitlement.go is the plan/entitlement override (FR8).
//
// # Why this takes effect with no code deploy, and how that is provable
//
// The override does not change code, does not change a limit, and does not write a number anywhere.
// It repoints the tenant's account at a plan_id that already exists in the PUBLISHED plan
// configuration, and pins the config version it resolved under. Everything that follows — which
// features the tenant may reach, which allowances gate them, which price references the provider is
// billed against — is resolved from the config store at the moment of use by P7's existing resolver.
//
// That is what makes "effective with no code deploy" a structural fact rather than a claim: there is
// no code path here that could require one. The test asserts it by exercising the entitlement gate
// before and after the override in the SAME process, with no rebuild.
//
// # Why no price value passes through here
//
// The command records the plan ID, the plan NAME and the config version. A price is an opaque
// REFERENCE living in the config store; it is never read here, never returned, and never written to
// a git-tracked file. TestNoPriceValueInAdminSource is the fence.

// EntitlementService implements the plan/entitlement override.
type EntitlementService struct {
	exec     *Executor
	accounts account.Store
	plans    *plancfg.Resolver
}

// NewEntitlementService wires the service.
func NewEntitlementService(exec *Executor, accounts account.Store, plans *plancfg.Resolver) (*EntitlementService, error) {
	if exec == nil || accounts == nil || plans == nil {
		return nil, errors.New("adminops: the entitlement service needs the command path, the account store and the plan resolver")
	}
	return &EntitlementService{exec: exec, accounts: accounts, plans: plans}, nil
}

// PlanOption is one plan an operator may move a tenant to — the NAME and the id, never a price.
type PlanOption struct {
	PlanID   string `json:"plan_id"`
	PlanName string `json:"plan_name"`
	// Rank orders the named plans so the console can present them in packaging order without
	// hardcoding one.
	Rank int `json:"rank"`
	// Features is what the plan entitles, by name. It is what makes an override legible before it is
	// applied: "this moves the tenant from Team to Enterprise, which adds auto-merge".
	Features []string `json:"features"`
}

// Plans lists the plans available for an override, from the config store. Permission-gated on the
// override capability itself: which plans exist is packaging information, and the operator who cannot
// change a plan has no reason to enumerate them.
func (s *EntitlementService) Plans(ctx context.Context) ([]PlanOption, error) {
	if _, _, err := s.exec.Authorize(ctx, adminrbac.CapEntitlementOverride, TargetGlobal); err != nil {
		return nil, err
	}
	plans := s.plans.Plans()
	out := make([]PlanOption, 0, len(plans))
	for _, p := range plans {
		opt := PlanOption{PlanID: p.PlanID, PlanName: p.DisplayName, Rank: p.Rank}
		for _, f := range plancfg.Features {
			if p.Entitles(f) {
				opt.Features = append(opt.Features, string(f))
			}
		}
		out = append(out, opt)
	}
	return out, nil
}

// Override repoints a tenant at a different plan. Permission-gated to Billing-Ops (and Superadmin),
// reason-required, confirmed, audited — and effective immediately, with no deploy.
func (s *EntitlementService) Override(ctx context.Context, tenantID, planRef, reason string, confirm Confirmation) (Receipt, error) {
	planID := plancfg.NormalizePlanID(planRef)
	if planID == "" {
		return Receipt{}, errors.New("adminops: an entitlement override must name the plan to move the tenant to")
	}
	// Resolve BEFORE the command runs, so an override onto an unpublished plan is refused rather than
	// applied and then discovered by the tenant as a broken entitlement.
	plan, err := s.plans.ResolvePlan(planID)
	if err != nil {
		return Receipt{}, fmt.Errorf("adminops: %q is not in the published plan configuration: %w", planRef, err)
	}
	version := s.plans.Version()

	return s.exec.Execute(ctx, Command{
		Capability: adminrbac.CapEntitlementOverride,
		Action:     adminaudit.ActionEntitlementOverride,
		Target:     TenantTarget(tenantID),
		Reason:     reason,
		Confirm:    confirm,
		Params:     []string{tenantID, plan.PlanID, version},
		Evidence: map[string]string{
			"tenant_id": tenantID, "plan_id": plan.PlanID, "plan_name": plan.DisplayName,
			"plan_config_version": version, "config_source": s.plans.Describe(),
		},
	}, func(context.Context) (map[string]string, error) {
		before, err := s.accounts.Get(tenantID)
		if err != nil {
			return nil, err
		}
		after, err := s.accounts.SetPlan(tenantID, plan.PlanID, version, plan.Charges())
		if err != nil {
			return nil, err
		}
		return map[string]string{
			"from_plan_id": before.ActivePlanID, "to_plan_id": after.ActivePlanID,
			"to_plan_name": plan.DisplayName, "plan_config_version": after.PlanConfigVersion,
		}, nil
	})
}

// priceLikeFieldNames are the names an override must never carry. Kept as data so the fence test and
// the reviewer read the same list.
var priceLikeFieldNames = []string{"amount", "price", "usd", "cents", "percent", "rate"}

// AssertNoPriceValue reports an error if a rendered override payload contains a price-like key.
//
// It exists because "no price value in git" is enforced by a CI fence over FILES, and this is the
// runtime half: a payload assembled at request time cannot be scanned by CI. It is called by the
// service's own tests and by the BFF before serialization, so a field added later that carries an
// amount is caught where it is introduced.
func AssertNoPriceValue(payload map[string]string) error {
	for k := range payload {
		lower := strings.ToLower(k)
		for _, bad := range priceLikeFieldNames {
			if strings.Contains(lower, bad) {
				return fmt.Errorf("adminops: refusing to emit %q — the operator console carries plan names and price REFERENCES, never values", k)
			}
		}
	}
	return nil
}
