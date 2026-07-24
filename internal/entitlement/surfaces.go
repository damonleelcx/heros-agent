package entitlement

import (
	"github.com/heros-foreal/agentd/internal/plancfg"
)

// surfaces.go wires the gate into every gated surface (task 5.2). Each is a one-line call into
// CheckEntitlement with the surface's feature and the level the caller is operating at.
//
// They exist as named methods rather than as ad-hoc CheckEntitlement calls scattered across four
// packages for one reason: a surface that spells its own feature constant is a surface that can spell
// it wrong, and a gate that never fires is indistinguishable from a gate that always allows.
// `Surfaces` below enumerates them, so the matrix test covers every gated surface by construction
// rather than by whoever remembered to add a case.

// CLI gates the command-line client. Available on every plan including Free.
func (g *Gate) CLI(customerID string) (Decision, error) {
	return g.CheckEntitlement(customerID, plancfg.FeatureCLI, LevelAdvisory)
}

// Discovery gates repository discovery. Available on every plan including Free.
func (g *Gate) Discovery(customerID string) (Decision, error) {
	return g.CheckEntitlement(customerID, plancfg.FeatureDiscovery, LevelAdvisory)
}

// AssistedPR gates opening a verified optimization pull request. Team and above.
func (g *Gate) AssistedPR(customerID string) (Decision, error) {
	return g.CheckEntitlement(customerID, plancfg.FeatureAssistedPR, LevelAssisted)
}

// Dashboard gates the web console (seats / retention / SUM band per plan). Team and above.
func (g *Gate) Dashboard(customerID string) (Decision, error) {
	return g.CheckEntitlement(customerID, plancfg.FeatureDashboard, LevelAdvisory)
}

// AutoMerge gates P6 Autonomous auto-merge. Enterprise only.
func (g *Gate) AutoMerge(customerID string) (Decision, error) {
	return g.CheckEntitlement(customerID, plancfg.FeatureAutoMerge, LevelAutonomous)
}

// Surface is one gated entry point, as data: its feature, the level it operates at, and the gate call.
// Enumerated so the entitlement matrix test iterates the REAL set rather than a hand-copied list that
// silently stops covering a surface added later.
type Surface struct {
	Feature plancfg.Feature
	Level   AutomationLevel
	Check   func(g *Gate, customerID string) (Decision, error)
}

// Surfaces is every gated surface.
var Surfaces = []Surface{
	{plancfg.FeatureCLI, LevelAdvisory, (*Gate).CLI},
	{plancfg.FeatureDiscovery, LevelAdvisory, (*Gate).Discovery},
	{plancfg.FeatureAssistedPR, LevelAssisted, (*Gate).AssistedPR},
	{plancfg.FeatureDashboard, LevelAdvisory, (*Gate).Dashboard},
	{plancfg.FeatureAutoMerge, LevelAutonomous, (*Gate).AutoMerge},
}

// AutoMergeRollout is the P7 rollout switch that keeps the Enterprise auto-merge entitlement off until
// the gate itself is verified (task 8.3). It is a one-method interface declared HERE rather than an
// import of the billing package, for the same reason the optimizer declares MergeEntitlement: the
// dependency runs one way, and a rollout switch is a boolean, not a billing concern.
//
// `*billing.Rollout` satisfies it.
type AutoMergeRollout interface {
	AllowAutoMergeEntitlement() bool
}

// MergeGate adapts the entitlement gate to the P6 loop's `optimizer.MergeEntitlement` seam.
//
// It is a distinct type rather than a method on Gate so the dependency runs one way: the optimizer
// declares the one-method interface it needs and stays buildable with no billing stack; this package
// satisfies it. Nothing in the optimizer imports plancfg, metering, or account because of P7.
type MergeGate struct {
	gate    *Gate
	rollout AutoMergeRollout
}

// NewMergeGate wraps the entitlement gate for the P6 loop.
func NewMergeGate(g *Gate) *MergeGate { return &MergeGate{gate: g} }

// WithRollout attaches the rollout switch. Until it is attached the gate behaves as before (plan
// config decides); once attached, a disabled switch DENIES regardless of what the plan says.
//
// The asymmetry is deliberate: the switch can only ever take the entitlement AWAY. A rollout flag that
// could grant an entitlement the plan does not include would be a second, contradictory source of
// truth for packaging.
func (m *MergeGate) WithRollout(r AutoMergeRollout) *MergeGate {
	m.rollout = r
	return m
}

// AllowAutoMerge reports whether the customer's plan entitles Autonomous auto-merge, with a named
// reason and upgrade plan when it does not.
//
// A gate that cannot answer DENIES. Failing open here would let an unresolvable customer reach the
// highest-authority actor in the system — the one that edits and merges their code — which is the one
// place a permissive default is unacceptable.
func (m *MergeGate) AllowAutoMerge(customerID string) (bool, string, string) {
	if m.rollout != nil && !m.rollout.AllowAutoMergeEntitlement() {
		return false, "Autonomous auto-merge is not enabled on this deployment yet (the entitlement stays " +
			"off until the entitlement gate is verified)", ""
	}
	d, err := m.gate.AutoMerge(customerID)
	if err != nil {
		return false, "the entitlement gate could not be consulted: " + err.Error(), ""
	}
	if d.Allowed {
		return true, "", ""
	}
	return false, d.Reason, d.UpgradePlan
}
