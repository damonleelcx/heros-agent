package billing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"
)

// preflight.go is P21 Decision 9: the provider account's configuration is verified BEFORE it is charged
// against, rather than by charging against it.
//
// ## The gap this closes
//
// Decision 7 makes a price an opaque reference the platform cannot validate by inspection. That is the
// right trade — a price the platform could inspect is a price the platform holds — and it has one
// consequence: the only thing that knows whether `price_ref_team_sub` means anything is the provider.
//
// Without a preflight, the first code that finds out is `RaiseCharge`, at the first charge of the
// period, and the answer arrives as `ErrProviderRejected`: correct, unretryable, and maximally badly
// timed. The customer's period has already accrued, the charge that should close it cannot be raised,
// and an operator learns about it from a failed charge rather than from a check they could have run at
// deploy.
//
// ## Three properties, each deliberate
//
//  1. **READ-ONLY.** It resolves prices. It creates no customer, no subscription, no charge, no price. A
//     preflight that created a probe subscription to prove a price works would be moving money to check
//     whether money can move — and the first time it half-failed it would leave an artefact nobody
//     expected on a customer's account.
//  2. **CACHED, not probed per request.** The result is computed when asked and then reported from
//     memory. `/readyz` reports the stored summary rather than resolving prices on every readiness
//     check, for the same reason it does not probe the secrets manager: an upstream latency spike would
//     otherwise read as this service being unhealthy.
//  3. **IT DOES NOT GATE CHARGES.** A stale preflight must not block a charge that would succeed, and a
//     clean one must not excuse a charge that would fail. The provider remains the authority at charge
//     time; the preflight's job is to make the answer available EARLIER, not to become a second opinion.

// PriceVerifier is the OPTIONAL capability of resolving a price reference at the provider.
//
// A separate interface, not an addition to [Provider] (Decision 1 is ratified): a processor integration
// that bills correctly but cannot look a price up is still a valid `Provider`. What it cannot do is
// preflight, and the honest way to express "cannot" is an interface it does not implement — which the
// report then states as UNVERIFIED rather than silently passing off "we did not check" as "it is fine".
type PriceVerifier interface {
	// ResolvePrice reports whether a price reference exists at the provider. It performs a READ.
	ResolvePrice(ctx context.Context, priceRef string) error
}

// UnresolvedPrice is one configured reference the provider does not know.
//
// It names the plan, the kind and the reference because those are the three things needed to fix it, and
// an error that named only the reference would send someone grepping a config store for a string.
type UnresolvedPrice struct {
	PlanID   string `json:"plan_id"`
	PlanName string `json:"plan_name"`
	// Kind is the charge kind the reference is configured for: subscription / metered / gainshare.
	Kind     string `json:"kind"`
	PriceRef string `json:"price_ref"`
	// Reason is the provider's own words for why it did not resolve.
	Reason string `json:"reason"`
}

// PricingPreflight is the whole report.
type PricingPreflight struct {
	// Verified is false when the wired provider cannot resolve prices at all. It is NOT the same as
	// "everything resolved", and collapsing the two would report an unchecked deployment as a clean one.
	Verified bool `json:"verified"`
	// Checked is how many references were resolved. Zero with Verified=true means the configuration
	// contains no price at all — which is a real state (an all-free catalog) and is reported as such
	// rather than as a pass.
	Checked int `json:"checked"`
	// Unresolved is every reference the provider did not know, in plan order.
	Unresolved []UnresolvedPrice `json:"unresolved,omitempty"`
	// Detail explains a Verified=false, in words an operator can act on.
	Detail string `json:"detail,omitempty"`
	// RanAt is when this report was produced. A cached report with no timestamp is a report nobody can
	// tell is stale.
	RanAt time.Time `json:"ran_at"`
}

// OK reports whether the configuration is usable: verified, with nothing unresolved.
func (p PricingPreflight) OK() bool { return p.Verified && len(p.Unresolved) == 0 }

// Summary is the one-line form for a health surface.
func (p PricingPreflight) Summary() string {
	switch {
	case !p.Verified:
		return "unverified"
	case len(p.Unresolved) > 0:
		return "unresolved:" + strconv.Itoa(len(p.Unresolved))
	case p.Checked == 0:
		return "no_prices_configured"
	}
	return "verified:" + strconv.Itoa(p.Checked)
}

// ErrPricingMisconfigured is returned when at least one configured reference does not resolve.
var ErrPricingMisconfigured = errors.New("billing: a configured price reference does not resolve at the provider")

// pricingCache holds the last report.
type pricingCache struct {
	mu  sync.RWMutex
	got bool
	rep PricingPreflight
}

// PreflightPricing resolves every configured price reference at the provider and caches the result.
//
// It returns the report AND an error when something is unresolved — both, on purpose. A caller that
// wants the detail reads the report; a caller that just wants to fail a deploy checks the error. Making
// it error-only would force every caller to reconstruct the list from a message.
func (s *Service) PreflightPricing(ctx context.Context) (PricingPreflight, error) {
	rep := PricingPreflight{RanAt: s.now().UTC()}

	verifier, ok := s.provider.(PriceVerifier)
	if !ok {
		rep.Detail = fmt.Sprintf("the configured billing provider (%s) cannot resolve price references, so the "+
			"plan configuration has NOT been checked against it", s.provider.Describe())
		s.pricing.store(rep)
		return rep, nil
	}
	rep.Verified = true

	// Deterministic order: a report whose rows move between runs is a report nobody can diff.
	plans := s.plans.Plans()
	sort.Slice(plans, func(i, j int) bool { return plans[i].Rank < plans[j].Rank })

	for _, plan := range plans {
		kinds := make([]string, 0, len(plan.PriceRefs))
		for kind := range plan.PriceRefs {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			ref := plan.PriceRefs[kind]
			if ref == "" {
				continue
			}
			rep.Checked++
			if err := verifier.ResolvePrice(ctx, ref); err != nil {
				if errors.Is(err, ErrProviderUnavailable) {
					// An outage is not a misconfiguration. Reporting it as one would send an operator to
					// fix a config store while the provider is simply down.
					rep.Verified = false
					rep.Unresolved = nil
					rep.Detail = "the provider was unreachable, so the plan configuration has NOT been checked: " + err.Error()
					s.pricing.store(rep)
					return rep, nil
				}
				rep.Unresolved = append(rep.Unresolved, UnresolvedPrice{
					PlanID: plan.PlanID, PlanName: plan.DisplayName, Kind: kind, PriceRef: ref,
					Reason: err.Error(),
				})
			}
		}
	}

	s.pricing.store(rep)
	if len(rep.Unresolved) > 0 {
		return rep, fmt.Errorf("%w: %d of %d reference(s) — first: plan %s / %s / %q",
			ErrPricingMisconfigured, len(rep.Unresolved), rep.Checked,
			rep.Unresolved[0].PlanName, rep.Unresolved[0].Kind, rep.Unresolved[0].PriceRef)
	}
	return rep, nil
}

// PricingStatus returns the last preflight report, and whether one has ever run.
//
// 🔴 "Never ran" is a distinct answer from "ran and found nothing wrong". A health surface that reported
// the second for the first would tell an operator their pricing is fine when nothing has checked it.
func (s *Service) PricingStatus() (PricingPreflight, bool) { return s.pricing.load() }

func (c *pricingCache) store(rep PricingPreflight) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got, c.rep = true, rep
}

func (c *pricingCache) load() (PricingPreflight, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rep, c.got
}
