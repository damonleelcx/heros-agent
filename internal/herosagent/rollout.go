package herosagent

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// rollout.go is task 9.4: internal tenant → one design partner → opt-in → default-on, with the
// rehearsal gate between each stage.
//
// # 🚫 "No stage verified by hand" — what that rules out and what it demands
//
// It rules out the thing every rollout actually does: somebody looks at a dashboard, decides it seems
// fine, and advances. That is not a gate, it is a habit, and it fails in one specific way — the stage
// that gets waved through is the one advanced under time pressure, which is the same stage where the
// evidence is thinnest.
//
// So `Advance` REFUSES unless it can read the evidence itself. Every precondition below is a query
// against a live store — how many tenants are actually placed, what the active definition's rehearsal
// state actually is, whether a ceiling actually exists — and the refusal names the one that failed.
//
// # Why the ladder is about PLACEMENTS and not a feature flag
//
// A flag would be a second source of truth for "who is analysed", and `heros_tenant_placement` already
// answers it. So a stage is a claim about the SHAPE of the placement table — one tenant, then two,
// then some, then most — and advancing is permitted or refused by counting rows. There is nothing to
// keep in step, because there is only one thing.
//
// A consequence worth stating: this ladder does not ENABLE anybody. It says whether the fleet's current
// shape is a legal next step, and an operator still sets each placement deliberately with a reason.
// Automating the enablement would put "read a customer's source under a platform credential" behind a
// scheduler, which is exactly the posture Q2 chose the default to avoid.

// Stage is one rung of the rollout ladder.
type Stage string

const (
	// StageNone: nobody is analysed. The state every deployment ships in.
	StageNone Stage = "none"
	// StageInternal: exactly one tenant, and it must be an internal one.
	StageInternal Stage = "internal"
	// StagePartner: the internal tenant plus one design partner.
	StagePartner Stage = "partner"
	// StageOptIn: any number of tenants, each explicitly placed. The long stage.
	StageOptIn Stage = "opt_in"
	// StageDefaultOn: most of the fleet. 🔴 Still not automatic — see the file header. This stage is a
	// statement that the fleet has mostly been enabled, not a switch that enables it.
	StageDefaultOn Stage = "default_on"
)

// Stages returns the ladder in order, so a caller can prove a step is adjacent rather than trusting a
// switch to have covered every pair.
func Stages() []Stage {
	return []Stage{StageNone, StageInternal, StagePartner, StageOptIn, StageDefaultOn}
}

func stageIndex(s Stage) int {
	for i, v := range Stages() {
		if v == s {
			return i
		}
	}
	return -1
}

// RolloutEvidence is what the ladder READS. Every field is a count or a state taken from a live store —
// never a claim a caller passes in about itself.
type RolloutEvidence struct {
	// EnabledTenants are the tenants placed anywhere but `disabled`, by id.
	EnabledTenants []string
	// InternalTenants are the tenant ids this deployment considers its own. Configuration, and the one
	// input here that IS declared — the platform cannot infer which of its customers is itself.
	InternalTenants []string
	// ActiveConfigHash is the definition currently serving, empty when none is.
	ActiveConfigHash string
	// RehearsalState is that definition's gate verdict.
	RehearsalState RehearsalState
	// FleetCapSet reports whether a fleet ceiling exists at all.
	FleetCapSet bool
	// TotalTenants is how many tenants this deployment has, for the default-on threshold.
	TotalTenants int
}

// DefaultOnThreshold is the share of the fleet that must be enabled before `default_on` is truthful.
//
// A threshold rather than "all", because a fleet always contains a few tenants nobody will enable —
// dormant accounts, trials that lapsed — and requiring every one would make the last stage
// unreachable, which turns the ladder into something people work around.
const DefaultOnThreshold = 0.8

// CurrentStage reports which rung the fleet's ACTUAL shape corresponds to.
//
// 🔴 Derived from the evidence rather than stored. A stored stage is a second source of truth for what
// the placement table already says, and the two diverge the first time somebody sets a placement
// without touching the stage — which is every time, because setting a placement is the ordinary act
// and advancing a stage is the ceremonial one.
func CurrentStage(e RolloutEvidence) Stage {
	internal := map[string]bool{}
	for _, id := range e.InternalTenants {
		internal[id] = true
	}
	enabled := append([]string(nil), e.EnabledTenants...)
	sort.Strings(enabled)

	switch n := len(enabled); {
	case n == 0:
		return StageNone
	case n == 1 && internal[enabled[0]]:
		return StageInternal
	case n == 2 && countInternal(enabled, internal) == 1:
		return StagePartner
	case e.TotalTenants > 0 && float64(n) >= DefaultOnThreshold*float64(e.TotalTenants):
		return StageDefaultOn
	default:
		return StageOptIn
	}
}

func countInternal(enabled []string, internal map[string]bool) int {
	n := 0
	for _, id := range enabled {
		if internal[id] {
			n++
		}
	}
	return n
}

// Advance reports whether moving from the fleet's current stage to `want` is permitted, and refuses
// with the reason when it is not.
//
// 🔴 THE REHEARSAL GATE IS BETWEEN EVERY PAIR, not only at the start. A definition that passed its
// rehearsal, served the internal tenant, and was then REPUBLISHED is a different definition — and the
// republished one has its own gate to pass before it reaches a design partner. Checking only at the
// first rung would let a definition that has never been rehearsed reach the whole fleet, one adjacent
// step at a time, with each step individually looking like a small move.
func Advance(e RolloutEvidence, want Stage) error {
	from := CurrentStage(e)
	fromIdx, wantIdx := stageIndex(from), stageIndex(want)
	if wantIdx < 0 {
		return fmt.Errorf("%w: %q is not a rollout stage; the ladder is %s",
			ErrInvalidDefinition, want, joinStages())
	}
	if wantIdx <= fromIdx {
		// Moving BACK is always permitted and is not this function's business — an operator switching
		// tenants off during an incident must never be gated on a rehearsal. Saying so explicitly beats
		// returning an error that reads as a refusal to retreat.
		return nil
	}
	if wantIdx > fromIdx+1 {
		return fmt.Errorf("%w: this fleet is at %q and %q is %d rungs up. Each stage exists to produce "+
			"the evidence the next one is decided on, so skipping one means deciding without it",
			ErrRolloutStageSkipped, from, want, wantIdx-fromIdx)
	}

	// 🔴 The gate, read from the store rather than asserted.
	if e.ActiveConfigHash == "" {
		return fmt.Errorf("%w: no agent definition is active, so there is nothing to roll out to %q",
			ErrRehearsalNotPassed, want)
	}
	if e.RehearsalState != RehearsalPassed {
		return fmt.Errorf("%w: the active definition %s is %q. The gate is between EVERY pair of "+
			"stages, not only the first: a republished definition that reached one rung has its own "+
			"rehearsal to pass before it reaches the next",
			ErrRehearsalNotPassed, confighashDisplay(e.ActiveConfigHash), e.RehearsalState)
	}
	// A ceiling before anybody outside this organization is analysed. The internal rung is exempt
	// deliberately: it is the rung whose whole purpose is to find out what an analysis costs, and
	// requiring the number before it can be measured would make the first step unreachable.
	if wantIdx >= stageIndex(StagePartner) && !e.FleetCapSet {
		return fmt.Errorf("%w: no fleet token ceiling is set. %q is the first stage that analyses a "+
			"customer, and an unbounded spend on somebody else's repository is not a thing to discover "+
			"from an invoice", ErrNoFleetCap, want)
	}
	return nil
}

func joinStages() string {
	names := make([]string, 0, len(Stages()))
	for _, s := range Stages() {
		names = append(names, string(s))
	}
	return strings.Join(names, " → ")
}

// RolloutReader gathers the evidence from live stores.
type RolloutReader struct {
	placements interface {
		List(context.Context) ([]TenantPlacement, error)
	}
	versions activeReader
	caps     CapStore
	internal []string
	total    func(context.Context) (int, error)
}

// NewRolloutReader wires the evidence gatherer.
func NewRolloutReader(
	placements interface {
		List(context.Context) ([]TenantPlacement, error)
	},
	versions activeReader, caps CapStore, internalTenants []string,
	total func(context.Context) (int, error),
) *RolloutReader {
	return &RolloutReader{
		placements: placements, versions: versions, caps: caps,
		internal: append([]string(nil), internalTenants...), total: total,
	}
}

// Evidence reads the current shape of the fleet.
func (r *RolloutReader) Evidence(ctx context.Context) (RolloutEvidence, error) {
	e := RolloutEvidence{InternalTenants: r.internal, EnabledTenants: []string{}}
	if r.placements != nil {
		ps, err := r.placements.List(ctx)
		if err != nil {
			return RolloutEvidence{}, err
		}
		for _, p := range ps {
			if p.Placement != PlacementDisabled {
				e.EnabledTenants = append(e.EnabledTenants, p.TenantID)
			}
		}
	}
	if r.versions != nil {
		v, ok, err := r.versions.Active(ctx)
		if err != nil {
			return RolloutEvidence{}, err
		}
		if ok {
			e.ActiveConfigHash, e.RehearsalState = v.ConfigHash, v.RehearsalState
		}
	}
	if r.caps != nil {
		_, ok, err := r.caps.Get(ctx, FleetTenantID)
		if err != nil {
			return RolloutEvidence{}, err
		}
		e.FleetCapSet = ok
	}
	if r.total != nil {
		n, err := r.total(ctx)
		if err != nil {
			return RolloutEvidence{}, err
		}
		e.TotalTenants = n
	}
	return e, nil
}
