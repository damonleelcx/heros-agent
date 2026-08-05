package signup

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/plancfg"
)

// plans.go adapts the platform's plan configuration to the two questions sign-up asks.
//
// # Why "the free plan" is DERIVED rather than named
//
// A deployment's plan catalog is configuration and its ids are the deployment's own. Hardcoding `free`
// would be wrong for anyone who calls their entry tier something else, and putting the id in a second
// environment variable would create a value that can disagree with the catalog — the failure being a
// sign-up that succeeds and lands somebody on a plan that charges.
//
// So the free plan is *the lowest-ranked plan that carries no price reference*. Rank is already the
// catalog's own ordering (`PlanConfig.Rank` exists so a denial can name the cheapest plan that lifts
// what was hit), and "carries no price reference" is already how the platform knows a plan is free —
// `PlanConfig.Charges()`. Both facts come from the catalog, so they cannot disagree with it.
//
// A catalog with no free plan is a REFUSAL, not a fallback to the cheapest paid one. Falling back would
// demand a payment method from somebody who has not seen the product yet.

// CatalogPlans reads the live plan snapshot on every call.
//
// Re-reading rather than caching is deliberate and cheap: sign-up happens once per organization, and a
// cached catalog is how a deployment that just published a new plan keeps creating accounts against the
// old one — silently, because nothing about a stale plan looks wrong.
type CatalogPlans struct{ src plancfg.Source }

// NewCatalogPlans adapts a plan configuration source.
func NewCatalogPlans(src plancfg.Source) *CatalogPlans { return &CatalogPlans{src: src} }

// Resolve returns one plan by id, with the version it resolved under stamped on it.
func (c *CatalogPlans) Resolve(planID string) (plancfg.PlanConfig, error) {
	snap, err := c.src.Load()
	if err != nil {
		return plancfg.PlanConfig{}, err
	}
	p, ok := snap.Plans[planID]
	if !ok {
		return plancfg.PlanConfig{}, fmt.Errorf("%w: %s", plancfg.ErrUnknownPlan, planID)
	}
	// The version travels with the plan, so whoever writes it onto an account cannot pin the wrong one.
	p.Version = snap.Version
	return p, nil
}

// FreePlanID names the lowest-ranked plan that charges nothing, or "" when the catalog has none.
func (c *CatalogPlans) FreePlanID() string {
	snap, err := c.src.Load()
	if err != nil {
		return ""
	}
	free := make([]plancfg.PlanConfig, 0, len(snap.Plans))
	for _, p := range snap.Plans {
		if !p.Charges() {
			free = append(free, p)
		}
	}
	if len(free) == 0 {
		return ""
	}
	sort.Slice(free, func(i, j int) bool {
		if free[i].Rank != free[j].Rank {
			return free[i].Rank < free[j].Rank
		}
		// Ties broken by id so two boots of the same catalog choose the same plan. A tie means the
		// catalog has two free plans at one rank, which is a configuration mistake — but an unstable
		// choice would make it an intermittent one.
		return free[i].PlanID < free[j].PlanID
	})
	return free[0].PlanID
}
