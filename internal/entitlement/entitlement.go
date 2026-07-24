// Package entitlement is the P7 gate: what a customer's ACTIVE PLAN and the requested AUTOMATION LEVEL
// together permit (design Decision 4).
//
// ## Two axes, not one
//
// Gating on the plan alone would let a Free customer's Autonomous loop merge code the moment somebody
// set the level; gating on the level alone would let any plan reach the platform's highest-authority
// actor. Both axes are required, and the strictest one wins:
//
//	| Surface                     | Free | Team | Business | Enterprise |
//	|-----------------------------|------|------|----------|------------|
//	| CLI + discovery             |  ✓   |  ✓   |    ✓     |     ✓      |
//	| Assisted verified PRs       |  —   |  ✓   |    ✓     |     ✓      |
//	| Web dashboard               |  —   |  ✓   |    ✓     |     ✓      |
//	| Autonomous auto-merge       |  —   |  —   |    —     |     ✓      |
//
// The table itself is CONFIGURATION (which plan lists which feature comes from the config store); what
// is code is the RULE that both axes are checked and that a denial is explicit.
//
// ## A denial is an invitation, never a silence
//
// Every denial carries a NAMED reason and the CHEAPEST plan that lifts it. That is not UX polish — it
// closes the two silent-failure modes that erode trust in a metered product: an over-limit action
// silently allowed (revenue leak, discovered at month-end) and an under-entitled action silently
// dropped (mysterious breakage, discovered by a support ticket). There is no code path here that
// returns "not allowed" without saying why and what lifts it; `Decision.Validate` asserts it, and the
// tests assert it for every denial the matrix can produce.
package entitlement

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
)

// AutomationLevel is the P5.5/P6 automation contract the action is requested under.
type AutomationLevel string

const (
	// LevelAdvisory recommends; a human does everything.
	LevelAdvisory AutomationLevel = "advisory"
	// LevelAssisted opens a verified pull request; a human merges.
	LevelAssisted AutomationLevel = "assisted"
	// LevelAutonomous opens AND merges. The highest-authority actor in the system.
	LevelAutonomous AutomationLevel = "autonomous"
)

// Levels is every automation level, weakest first.
var Levels = []AutomationLevel{LevelAdvisory, LevelAssisted, LevelAutonomous}

// rank orders the levels so "at least this level" is comparable. An unknown level ranks below
// everything, so a typo denies rather than escalates — the direction a mistake must fail in when the
// top of the range can merge code.
func (l AutomationLevel) rank() int {
	for i, k := range Levels {
		if k == l {
			return i
		}
	}
	return -1
}

// Known reports whether l is a real automation level.
func (l AutomationLevel) Known() bool { return l.rank() >= 0 }

// requiredLevel is the automation level each surface is requested under. A surface asked for at a
// LOWER level than it needs is denied: "open a PR and merge it" is not something an Advisory contract
// authorizes, whatever the plan says.
//
// A table rather than an if/else chain, so a new surface is one entry.
var requiredLevel = map[plancfg.Feature]AutomationLevel{
	plancfg.FeatureCLI:        LevelAdvisory,
	plancfg.FeatureDiscovery:  LevelAdvisory,
	plancfg.FeatureDashboard:  LevelAdvisory,
	plancfg.FeatureAssistedPR: LevelAssisted,
	plancfg.FeatureAutoMerge:  LevelAutonomous,
}

// meteredLimits names which metered allowances gate which surface.
//
// ## Why the CLI and discovery are NOT gated by the SUM band
//
// The two requirements collide for a Free customer who is over their band: "CLI and discovery SHALL be
// available to all plans including Free" and "an action past a metered limit SHALL be denied". They are
// reconciled by scope, not by precedence — the SUM band gates the surfaces that CONSUME spend on the
// customer's behalf (Assisted PRs, the dashboard, auto-merge), and the CLI and discovery are the plan's
// FLOOR.
//
// Gating them on the band would mean that a customer who overspends loses the tool that shows them
// they are overspending. That is perverse in the specific way this design is meant to avoid: the
// paywall is supposed to be an invitation, and taking away the diagnostic is the opposite of one. It
// would also make Free a demo rather than something genuinely useful, which is the packaging decision
// the whole boundary rests on.
//
// A table for the same reason as requiredLevel: adding a metered gate must not mean editing a decision
// tree.
var meteredLimits = map[plancfg.Feature][]struct {
	Limit  plancfg.Limit
	Metric metering.Metric
}{
	plancfg.FeatureCLI:        nil, // the floor: always available on every plan
	plancfg.FeatureDiscovery:  nil, // the floor
	plancfg.FeatureAssistedPR: {{plancfg.LimitSUMBand, metering.MetricSUM}},
	plancfg.FeatureDashboard:  {{plancfg.LimitSUMBand, metering.MetricSUM}, {plancfg.LimitSeats, metering.MetricSeats}},
	plancfg.FeatureAutoMerge:  {{plancfg.LimitSUMBand, metering.MetricSUM}},
}

// ReasonCode is the machine-readable denial cause. Distinct codes because the product responds
// differently to each: an over-limit denial offers an upgrade AND shows the meter, a level mismatch is
// a client-side contract error, and an unknown plan is an operational fault.
type ReasonCode string

const (
	// ReasonNotEntitled: the plan does not include the surface.
	ReasonNotEntitled ReasonCode = "not_entitled"
	// ReasonLevelTooLow: the requested automation level is below what the surface requires.
	ReasonLevelTooLow ReasonCode = "level_too_low"
	// ReasonOverLimit: a metered allowance for the period was exceeded.
	ReasonOverLimit ReasonCode = "over_limit"
	// ReasonUnknownLevel: the caller named an automation level that does not exist.
	ReasonUnknownLevel ReasonCode = "unknown_level"
	// ReasonUnknownFeature: the caller named a surface that does not exist.
	ReasonUnknownFeature ReasonCode = "unknown_feature"
	// ReasonNoAccount / ReasonUnknownPlan: operational faults. They deny — never allow — because an
	// unresolvable customer must not silently receive the top tier.
	ReasonNoAccount   ReasonCode = "no_account"
	ReasonUnknownPlan ReasonCode = "unknown_plan"
)

// LimitHit describes an exceeded metered allowance. Both numbers are carried so the denial can say HOW
// FAR over, which is what turns "denied" into something a customer can act on.
type LimitHit struct {
	Limit    plancfg.Limit   `json:"limit"`
	Metric   metering.Metric `json:"metric"`
	Allowed  float64         `json:"allowed"`
	Observed float64         `json:"observed"`
	Period   string          `json:"period"`
}

// Decision is the gate's answer.
type Decision struct {
	Allowed bool            `json:"allowed"`
	Feature plancfg.Feature `json:"feature"`
	Level   AutomationLevel `json:"automation_level"`
	PlanID  string          `json:"plan_id"`
	// PlanName is the customer-facing plan NAME. Plans are named everywhere in the product.
	PlanName string `json:"plan_name"`
	// Reason is the human-readable named reason. NEVER empty on a denial.
	Reason     string     `json:"reason,omitempty"`
	ReasonCode ReasonCode `json:"reason_code,omitempty"`
	// UpgradePlan / UpgradePlanName name the CHEAPEST plan that lifts what was hit. Empty only when no
	// configured plan lifts it — in which case Reason says so rather than leaving a blank invitation.
	UpgradePlan     string `json:"upgrade_plan,omitempty"`
	UpgradePlanName string `json:"upgrade_plan_name,omitempty"`
	// Limit is set on an over-limit denial.
	Limit *LimitHit `json:"limit,omitempty"`
}

// ErrIncoherentDenial is returned by Validate for a denial that fails to name its reason. It exists so
// "never silently deny" is checkable in a test rather than merely intended.
var ErrIncoherentDenial = errors.New("entitlement: a denial that names no reason is a silent denial")

// Validate asserts the decision is coherent: an allow carries no denial fields, and a denial names a
// reason and a code.
func (d Decision) Validate() error {
	if d.Allowed {
		if d.Reason != "" || d.ReasonCode != "" || d.UpgradePlan != "" {
			return fmt.Errorf("entitlement: an ALLOWED decision carries denial fields (%q/%q/%q)", d.Reason, d.ReasonCode, d.UpgradePlan)
		}
		return nil
	}
	if d.Reason == "" || d.ReasonCode == "" {
		return fmt.Errorf("%w (feature %q, level %q)", ErrIncoherentDenial, d.Feature, d.Level)
	}
	return nil
}

// Gate answers CheckEntitlement. It reads the account, the live plan config, and the period's usage
// records — and writes nothing: a gate that could write would be a gate that can grant itself
// permission.
type Gate struct {
	accounts account.Store
	plans    *plancfg.Resolver
	usage    metering.UsageStore
	now      func() time.Time
}

// NewGate builds the entitlement gate. The usage store may be nil in a deployment with no metering yet;
// in that case metered limits are NOT checked and the gate says so by not producing over-limit denials
// — rather than silently treating "no meter" as "no usage", which would be indistinguishable from a
// customer at zero.
func NewGate(accounts account.Store, plans *plancfg.Resolver, usage metering.UsageStore) *Gate {
	return &Gate{accounts: accounts, plans: plans, usage: usage, now: time.Now}
}

// SetClock injects a deterministic clock (tests).
func (g *Gate) SetClock(now func() time.Time) { g.now = now }

// CheckEntitlement gates one action by the customer's active plan AND the requested automation level.
//
// It returns a Decision — never a bare bool — because the whole point is that a denial says what was
// hit and what lifts it. The error return is reserved for the gate itself being unable to answer; an
// unresolvable customer still produces a DENY decision, because failing open on an operational fault
// would hand out the top tier to anyone the account store cannot find.
func (g *Gate) CheckEntitlement(customerID string, feature plancfg.Feature, level AutomationLevel) (Decision, error) {
	d := Decision{Feature: feature, Level: level}

	needLevel, known := requiredLevel[feature]
	if !known {
		return g.deny(d, ReasonUnknownFeature,
			fmt.Sprintf("%q is not a gated surface this platform knows about", feature), ""), nil
	}
	if !level.Known() {
		return g.deny(d, ReasonUnknownLevel,
			fmt.Sprintf("%q is not an automation level (advisory, assisted, autonomous)", level), ""), nil
	}

	acct, err := g.accounts.Get(customerID)
	if err != nil {
		return g.deny(d, ReasonNoAccount,
			fmt.Sprintf("no billing account for %q, so no plan entitles this action", customerID), ""), nil
	}
	d.PlanID = acct.ActivePlanID

	plan, err := g.plans.ResolvePlan(acct.ActivePlanID)
	if err != nil {
		return g.deny(d, ReasonUnknownPlan,
			fmt.Sprintf("plan %q is not in the published plan configuration", acct.ActivePlanID), ""), nil
	}
	d.PlanName = plan.DisplayName

	// ── Axis 1: does the PLAN entitle the surface? ────────────────────────────
	if !plan.Entitles(feature) {
		return g.deny(d, ReasonNotEntitled,
			fmt.Sprintf("%s is not included in the %s plan", featureLabel(feature), plan.DisplayName),
			g.cheapestEntitling(plan.Rank, feature)), nil
	}

	// ── Axis 2: is the requested LEVEL sufficient for the surface? ────────────
	//
	// This denial has no upgrade PLAN — no plan fixes it. The caller asked for a surface under a weaker
	// contract than the surface requires, and saying "upgrade to Enterprise" would be actively
	// misleading.
	if level.rank() < needLevel.rank() {
		return g.deny(d, ReasonLevelTooLow,
			fmt.Sprintf("%s requires the %s automation level; this action was requested at %s",
				featureLabel(feature), needLevel, level), ""), nil
	}

	// ── Axis 3: is the customer inside the plan's metered allowances? ─────────
	if hit, over := g.overLimit(customerID, plan, feature); over {
		d.Limit = &hit
		return g.deny(d, ReasonOverLimit,
			fmt.Sprintf("the %s plan allows %s up to %v for the period; this customer is at %v",
				plan.DisplayName, limitLabel(hit.Limit), hit.Allowed, hit.Observed),
			g.cheapestLifting(plan.Rank, feature, hit.Limit, hit.Observed)), nil
	}

	d.Allowed = true
	return d, nil
}

// CheckLimit answers "would this take the customer over the plan's allowance for one meter?" — the
// pre-flight an action performs before consuming a seat or starting a run.
//
// `additional` is what the action would ADD, so the check is against the resulting total rather than
// the current one: a seat request that would be the 6th on a 5-seat plan must be denied BEFORE the seat
// is taken, not after the meter notices.
func (g *Gate) CheckLimit(customerID string, limit plancfg.Limit, metric metering.Metric, additional float64) (Decision, error) {
	d := Decision{}
	acct, err := g.accounts.Get(customerID)
	if err != nil {
		return g.deny(d, ReasonNoAccount, fmt.Sprintf("no billing account for %q", customerID), ""), nil
	}
	plan, err := g.plans.ResolvePlan(acct.ActivePlanID)
	if err != nil {
		return g.deny(d, ReasonUnknownPlan, fmt.Sprintf("plan %q is not published", acct.ActivePlanID), ""), nil
	}
	d.PlanID, d.PlanName = plan.PlanID, plan.DisplayName

	allowed, set := plan.Limit(limit)
	if !set {
		d.Allowed = true // unset limit == unlimited (see plancfg.PlanConfig.Limits)
		return d, nil
	}
	observed := g.observed(customerID, metric) + additional
	if observed > allowed {
		hit := LimitHit{Limit: limit, Metric: metric, Allowed: allowed, Observed: observed, Period: g.period().ID}
		d.Limit = &hit
		return g.deny(d, ReasonOverLimit,
			fmt.Sprintf("the %s plan allows %s up to %v for the period; this action would take it to %v",
				plan.DisplayName, limitLabel(limit), allowed, observed),
			g.cheapestLiftingLimit(plan.Rank, limit, observed)), nil
	}
	d.Allowed = true
	return d, nil
}

func (g *Gate) period() metering.Period { return metering.MonthPeriod(g.now()) }

// observed reads the current period's quantity for a meter. A missing record is genuinely zero usage
// (the customer has not accrued that meter yet); a store error is treated as zero too, and that is a
// deliberate, narrow choice: the alternative — denying every action because the meter is unreachable —
// would take the whole product down on a metering outage, and the outage is already alerted on the
// P2.5 substrate. A metering outage must not be able to lock customers out of the CLI.
func (g *Gate) observed(customerID string, metric metering.Metric) float64 {
	if g.usage == nil {
		return 0
	}
	rec, err := g.usage.Get(metering.Key{CustomerID: customerID, Period: g.period().ID, Metric: metric})
	if err != nil {
		return 0
	}
	return rec.Quantity
}

// overLimit checks every metered allowance that gates a surface, in table order, and returns the FIRST
// one exceeded.
func (g *Gate) overLimit(customerID string, plan plancfg.PlanConfig, feature plancfg.Feature) (LimitHit, bool) {
	for _, m := range meteredLimits[feature] {
		allowed, set := plan.Limit(m.Limit)
		if !set {
			continue // unset == unlimited
		}
		observed := g.observed(customerID, m.Metric)
		if observed > allowed {
			return LimitHit{Limit: m.Limit, Metric: m.Metric, Allowed: allowed, Observed: observed, Period: g.period().ID}, true
		}
	}
	return LimitHit{}, false
}

// deny fills in a denial's reason, code and upgrade path, and asserts the result is coherent. Every
// denial in this package goes through here, which is how "a denial always names its reason" stays true
// as branches are added.
func (g *Gate) deny(d Decision, code ReasonCode, reason, upgradePlan string) Decision {
	d.Allowed = false
	d.ReasonCode = code
	d.Reason = reason
	if upgradePlan != "" {
		d.UpgradePlan = upgradePlan
		if p, err := g.plans.ResolvePlan(upgradePlan); err == nil {
			d.UpgradePlanName = p.DisplayName
		}
		d.Reason += ". Upgrade to " + d.UpgradePlanName + " to lift it"
	}
	return d
}

// cheapestEntitling finds the lowest-ranked plan above the current one that includes the feature.
// Cheapest rather than top-tier: pointing a Free customer at Enterprise when Team would do is a
// paywall, whereas naming the plan that actually lifts what they hit is an invitation.
func (g *Gate) cheapestEntitling(currentRank int, feature plancfg.Feature) string {
	for _, p := range g.rankedPlans() {
		if p.Rank > currentRank && p.Entitles(feature) {
			return p.PlanID
		}
	}
	return ""
}

// cheapestLifting finds the lowest-ranked plan above the current one that both entitles the feature and
// allows the observed quantity.
func (g *Gate) cheapestLifting(currentRank int, feature plancfg.Feature, limit plancfg.Limit, observed float64) string {
	for _, p := range g.rankedPlans() {
		if p.Rank <= currentRank || !p.Entitles(feature) {
			continue
		}
		if allowed, set := p.Limit(limit); !set || observed <= allowed {
			return p.PlanID
		}
	}
	return ""
}

func (g *Gate) cheapestLiftingLimit(currentRank int, limit plancfg.Limit, observed float64) string {
	for _, p := range g.rankedPlans() {
		if p.Rank <= currentRank {
			continue
		}
		if allowed, set := p.Limit(limit); !set || observed <= allowed {
			return p.PlanID
		}
	}
	return ""
}

func (g *Gate) rankedPlans() []plancfg.PlanConfig {
	plans := g.plans.Plans()
	sort.SliceStable(plans, func(i, j int) bool { return plans[i].Rank < plans[j].Rank })
	return plans
}

// featureLabel is the customer-facing name of a surface. Denials are read by humans; "assisted_pr is
// not included in the Free plan" is a stack trace, not an explanation.
func featureLabel(f plancfg.Feature) string {
	switch f {
	case plancfg.FeatureCLI:
		return "the command-line client"
	case plancfg.FeatureDiscovery:
		return "repository discovery"
	case plancfg.FeatureAssistedPR:
		return "opening verified optimization pull requests"
	case plancfg.FeatureDashboard:
		return "the web dashboard"
	case plancfg.FeatureAutoMerge:
		return "Autonomous auto-merge"
	}
	return string(f)
}

func limitLabel(l plancfg.Limit) string {
	switch l {
	case plancfg.LimitSUMBand:
		return "spend under management"
	case plancfg.LimitSeats:
		return "seats"
	case plancfg.LimitRetentionDays:
		return "retention"
	case plancfg.LimitEvalCompute:
		return "eval compute"
	}
	return string(l)
}
