package proposal

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Constraints are the user's hard limits. A candidate that would violate any of them is
// constraint-excluded — never ranked as a recommendation (design Decision 4, gates-not-penalties). A
// zero/empty field means "unset" and never excludes.
type Constraints struct {
	// BudgetCeilingUSD caps a candidate's projected cost per run.
	BudgetCeilingUSD float64
	// LatencySLAms caps a candidate's projected per-run latency.
	LatencySLAms float64
	// ProviderAllowlist, when non-empty, is the set of permitted model providers.
	ProviderAllowlist []string
}

// CostOfChange is the pre-verification cost signal for one candidate: the projected absolute cost and
// latency per run, the model provider, and the blast radius (design Q4 — run-cost for ranking,
// blast-radius as a separate risk badge).
type CostOfChange struct {
	ProjectedCostUSD   float64 `json:"projected_cost_usd"`
	ProjectedLatencyMS float64 `json:"projected_latency_ms"`
	Provider           string  `json:"provider"`
	// BlastRadius is how many node dimensions the diff changes — a risk badge, not a rank input.
	BlastRadius int `json:"blast_radius"`
}

const (
	constraintOK       = "ok"
	constraintExcluded = "excluded"
)

// RankedProposal is one candidate in ranked order, with its cost signal, score, constraint status, and
// reviewable presentation.
type RankedProposal struct {
	Compiled           Compiled     `json:"-"`
	Presentation       Presentation `json:"presentation"`
	Cost               CostOfChange `json:"cost"`
	ExpectedGain       float64      `json:"expected_gain"`
	Score              float64      `json:"score"`
	ConstraintStatus   string       `json:"constraint_status"`
	ViolatedConstraint string       `json:"violated_constraint,omitempty"`
}

// RankResult separates the ranked recommendations from the constraint-excluded candidates. Excluded
// candidates are listed separately with the violated constraint named (§3.2) — never mixed into the
// recommendation order.
type RankResult struct {
	Ranked   []RankedProposal `json:"ranked"`
	Excluded []RankedProposal `json:"excluded"`
}

// Rank orders surfaceable candidates by expected gain per unit cost-of-change and filters out the ones
// that violate a hard constraint (§3.1, §3.2). Build-failed candidates are dropped up front — they are
// never ranked (§1b.2). The order is deterministic: ties in score break by config_hash.
func Rank(compiled []Compiled, base *variantspec.VariantSpec, menu Menu, cons Constraints) RankResult {
	var res RankResult
	for _, c := range compiled {
		if !c.Surfaceable() {
			continue // build-failed: never ranked, verified, or presented
		}
		cost := projectCost(c.Candidate, base, menu)
		rp := RankedProposal{
			Compiled:     c,
			Presentation: Present(c, base),
			Cost:         cost,
			ExpectedGain: c.Candidate.ExpectedGain,
			Score:        rankScore(c.Candidate.ExpectedGain, cost.ProjectedCostUSD),
		}
		if viol := violatedConstraint(cost, cons); viol != "" {
			rp.ConstraintStatus = constraintExcluded
			rp.ViolatedConstraint = viol
			res.Excluded = append(res.Excluded, rp)
			continue
		}
		rp.ConstraintStatus = constraintOK
		res.Ranked = append(res.Ranked, rp)
	}
	sort.SliceStable(res.Ranked, func(i, j int) bool {
		if res.Ranked[i].Score != res.Ranked[j].Score {
			return res.Ranked[i].Score > res.Ranked[j].Score
		}
		return res.Ranked[i].Compiled.ConfigHash < res.Ranked[j].Compiled.ConfigHash
	})
	sort.SliceStable(res.Excluded, func(i, j int) bool {
		return res.Excluded[i].Compiled.ConfigHash < res.Excluded[j].Compiled.ConfigHash
	})
	return res
}

// rankScore is expected gain per unit cost-of-change. A floor on the denominator keeps a near-zero-cost
// change from producing an unbounded score; higher score ranks first.
func rankScore(gain, cost float64) float64 {
	const floor = 1e-4
	if cost < floor {
		cost = floor
	}
	return gain / cost
}

// projectCost estimates a candidate's absolute per-run cost, latency, and provider from its changed
// model (resolved against the menu). A candidate that changes no model inherits the baseline node's
// model cost; skills/context/order changes are treated as cost-neutral at this pre-verification stage
// (the measured verdict replaces the estimate post-verification).
func projectCost(cand Candidate, base *variantspec.VariantSpec, menu Menu) CostOfChange {
	node := cand.NodeID
	// The candidate's model ref at the changed node (falls back to baseline).
	ref := ""
	if cand.Spec != nil {
		ref = cand.Spec.Nodes[node].ModelRef
	}
	if ref == "" && base != nil {
		ref = base.Nodes[node].ModelRef
	}
	cost := CostOfChange{BlastRadius: blastRadius(base, cand.Spec)}
	for _, m := range menu.Models {
		if m.Ref == ref {
			cost.ProjectedCostUSD = m.CostPerRun
			cost.ProjectedLatencyMS = m.LatencyMS
			cost.Provider = m.Provider
			return cost
		}
	}
	return cost
}

// blastRadius counts the node dimensions the candidate changes vs the baseline — the risk badge.
func blastRadius(base, cand *variantspec.VariantSpec) int {
	return len(specDiff(base, cand))
}

// violatedConstraint returns the name of the first hard constraint a candidate breaches, or "".
func violatedConstraint(cost CostOfChange, cons Constraints) string {
	if cons.BudgetCeilingUSD > 0 && cost.ProjectedCostUSD > cons.BudgetCeilingUSD {
		return "budget ceiling"
	}
	if cons.LatencySLAms > 0 && cost.ProjectedLatencyMS > cons.LatencySLAms {
		return "latency SLA"
	}
	if len(cons.ProviderAllowlist) > 0 && cost.Provider != "" {
		allowed := false
		for _, p := range cons.ProviderAllowlist {
			if p == cost.Provider {
				allowed = true
				break
			}
		}
		if !allowed {
			return "provider allowlist"
		}
	}
	return ""
}
