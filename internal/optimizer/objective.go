package optimizer

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// objective.go inherits P4's objective and constraints rather than reinventing them (design Decision
// 1). The search objective IS the P4 composite score under the active weight profile; the hard
// constraints ARE the P4 gates. A candidate that fails any gate is NEVER selected or applied, however
// high its composite — so the honesty properties P4 fought for (gates-disqualify-not-penalize, CIs,
// tie-on-overlap) cannot be bypassed by the optimizer.

// CandidateMetrics is the measured evidence the objective and the gates read for one candidate: the
// providers it calls, its per-run cost/latency, its held-out quality, and its composite score with a
// confidence interval. These come from the P4 harness / P5.5 verification — the optimizer measures
// nothing new.
type CandidateMetrics struct {
	ConfigHash string
	Providers  []string
	CostUSD    float64
	LatencyMS  float64
	// Quality is the held-out quality (task_success) mean the min-quality gate reads.
	Quality float64
	// Composite is the P4 composite score with its CI. The search maximizes its point value among
	// gate-passers; the min-improvement stop reads its CI LOWER bound (design Q2).
	Composite evalstats.Interval
}

// GateVerdict is a candidate's hard-constraint outcome. Passed is false when ANY gate fails; Failed
// names them in stable order with a renderable reason. A gate failure DISQUALIFIES — it is never a
// score penalty (design Decision 1 / P4 Decision 6).
type GateVerdict struct {
	Passed  bool     `json:"passed"`
	Failed  []string `json:"failed,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
}

// Gate names — stable identifiers the UI and the audit trail key off (the P4 gate constants, so a
// verification constraint-violation and a search gate speak the same vocabulary).
const (
	GateProviderAllowlist = "provider_allowlist"
	GateMinQuality        = "min_quality"
	GateLatencySLA        = "latency_sla"
	GateMaxCostPerRun     = "max_cost_per_run"
)

// EvaluateGates applies the P4 hard-constraint gates to a candidate under the run's immutable
// constraints. It is the single place "gate-failing candidates are never applied" is decided, so the
// loop and the UI cannot disagree about whether a candidate is admissible.
//
// A nil/zero constraint is UNSET (not "zero"), following the P4 GateSet discipline: a min-quality of
// 0.0 would disqualify everything, so an unset gate is simply not evaluated. maxCostPerRun is passed
// separately because the run's cumulative ceiling is a HALT, not a per-candidate gate.
func EvaluateGates(c Constraints, m CandidateMetrics, maxCostPerRun float64) GateVerdict {
	v := GateVerdict{Passed: true}
	fail := func(gate, reason string) {
		v.Passed = false
		v.Failed = append(v.Failed, gate)
		v.Reasons = append(v.Reasons, gate+": "+reason)
	}

	// Provider allowlist: any provider outside the closed set disqualifies. Empty allowlist = unset.
	if len(c.ProviderAllowlist) > 0 {
		allowed := map[string]bool{}
		for _, p := range c.ProviderAllowlist {
			allowed[p] = true
		}
		var offenders []string
		for _, p := range m.Providers {
			if !allowed[p] {
				offenders = append(offenders, p)
			}
		}
		if len(offenders) > 0 {
			sort.Strings(offenders)
			fail(GateProviderAllowlist, fmt.Sprintf("calls provider(s) not on the allowlist: %v", offenders))
		}
	}
	if c.MinQuality > 0 && m.Quality < c.MinQuality {
		fail(GateMinQuality, fmt.Sprintf("held-out quality %.3f below floor %.3f", m.Quality, c.MinQuality))
	}
	if c.LatencySLAMs > 0 && m.LatencyMS > c.LatencySLAMs {
		fail(GateLatencySLA, fmt.Sprintf("latency %.0fms exceeds SLA %.0fms", m.LatencyMS, c.LatencySLAMs))
	}
	if maxCostPerRun > 0 && m.CostUSD > maxCostPerRun {
		fail(GateMaxCostPerRun, fmt.Sprintf("cost $%.4f/run exceeds cap $%.4f", m.CostUSD, maxCostPerRun))
	}
	return v
}

// ── Objective helpers ───────────────────────────────────────────────────────────────────────────────

// PreferByComposite chooses the best candidate among a set by the P4 composite score, considering ONLY
// gate-passing candidates (design Decision 1: a higher-scoring gate-failing candidate is never
// preferred over a lower-scoring gate-passing one). It returns the index of the winner, or -1 when no
// candidate passes its gates. Ties on composite break by config_hash for determinism.
func PreferByComposite(metrics []CandidateMetrics, gates []GateVerdict) int {
	best := -1
	for i := range metrics {
		if i < len(gates) && !gates[i].Passed {
			continue // gate-failing: never selectable, whatever its composite
		}
		if best < 0 {
			best = i
			continue
		}
		if better(metrics[i], metrics[best]) {
			best = i
		}
	}
	return best
}

func better(a, b CandidateMetrics) bool {
	if a.Composite.Mean != b.Composite.Mean {
		return a.Composite.Mean > b.Composite.Mean
	}
	return a.ConfigHash < b.ConfigHash
}

// CompositeGain is the marginal improvement the min-improvement stop reads: the candidate's composite
// CI LOWER bound minus the current best's composite CI lower bound (design Q2 — the conservative,
// honest measure of "how much better, at least"). A negative gain means the candidate is not
// convincingly better at all.
func CompositeGain(candidate, currentBest evalstats.Interval) float64 {
	return candidate.Low - currentBest.Low
}
