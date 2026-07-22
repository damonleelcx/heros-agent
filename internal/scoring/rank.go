package scoring

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalstats"
)

// rank.go is steps 2 and 3 of the pipeline — GATE, then WEIGHT — and the ordering between them is
// the load-bearing part.
//
// Gates run FIRST and DISQUALIFY. A variant that violates max-cost, the latency SLA, min quality, or
// the provider allowlist is removed from the ranked order regardless of how favourable its weighted
// score would have been. If gates were penalties instead, a cheap-but-broken arrangement would top a
// cost-weighted board by being cheap enough to outrun its own quality deduction — which is exactly
// the failure the separation exists to prevent. Soft weighted preferences are only meaningful among
// variants that already clear the hard floor.

// Rank produces the leaderboard for a profile and gate set. It is a PURE function of the cache: it
// touches no queue, no executor and no provider, which is why RunsEnqueued is structurally zero
// rather than merely observed to be.
func (c *Cache) Rank(profile Profile, gates GateSet) Leaderboard {
	lb := Leaderboard{
		Profile:      profile.Name,
		GateSet:      gates.Name,
		EvalSetHash:  c.EvalSetHash,
		RunsEnqueued: 0,
	}

	// Step 2 — gates. Refusals are computed once for the whole board: a gate driven by an
	// uncalibrated judge is refused for EVERY variant, never for some.
	refused := c.refusedGates(gates)
	for _, r := range refused {
		lb.Notes = append(lb.Notes, r)
	}

	var passers, failures []Row
	for _, id := range c.Order {
		v := c.Variants[id]
		row := c.row(v, profile)
		row.Gate = c.evaluateGates(v, gates, refused)
		if row.Gate.Passed {
			passers = append(passers, row)
		} else {
			failures = append(failures, row)
		}
	}

	// Step 3 — weight, applied ONLY to gate-passers.
	sort.SliceStable(passers, func(i, j int) bool {
		if passers[i].Composite.Mean != passers[j].Composite.Mean {
			return passers[i].Composite.Mean > passers[j].Composite.Mean
		}
		return passers[i].VariantID < passers[j].VariantID // total order, so a board is stable
	})
	for i := range passers {
		passers[i].Rank = i + 1
	}
	markTies(passers)

	sort.SliceStable(failures, func(i, j int) bool { return failures[i].VariantID < failures[j].VariantID })

	lb.Ranked = passers
	lb.Disqualified = failures
	// ONE summary note, not one per variant: the eval set is a property of the BOARD, so N copies of
	// the same sentence is N times the noise and zero times the information.
	lowConf := 0
	for _, id := range c.Order {
		if c.Variants[id].LowConfidence {
			lowConf++
		}
	}
	if lowConf > 0 {
		lb.Notes = append(lb.Notes, fmt.Sprintf(
			"all %d variants were measured on a low-confidence eval set; no score on this board is trustworthy on its own",
			lowConf))
	}
	sort.Strings(lb.Notes)
	return lb
}

// row builds one variant's row under a profile, bootstrapping the composite CI directly from the
// cached per-metric replicates.
//
// Bootstrapping the composite rather than propagating per-metric CIs analytically is deliberate: the
// analytic route would need the metrics to be independent, and they are not — a variant that got a
// lucky-fast seed also got a cheap one. Recombining the replicates keeps that correlation.
func (c *Cache) row(v CachedVariant, p Profile) Row {
	row := Row{
		VariantID:     v.VariantID,
		ConfigHash:    v.ConfigHash,
		Label:         v.Label,
		Components:    map[string]Component{},
		Penalties:     map[string]float64{},
		WeakLabeled:   len(v.WeakCaseIDs) > 0,
		LowConfidence: v.LowConfidence,
	}

	// Penalties split into two kinds, and conflating them was a real defect.
	//
	// A penalty derived from a MEASURED quantity (tool-error rate) carries that measurement's
	// uncertainty, so it must enter the bootstrap per replicate — subtracting its mean as an exact
	// constant leaves the interval unchanged while shifting it, and on a board where every metric is
	// statistically degenerate that turns pure noise into a decisive ordering. Three indistinguishable
	// variants were ranked 1-2-3 on a difference of 1e-5 that came entirely from this.
	//
	// A penalty derived from a COUNT (what fraction of cases carry a weak reference) is exact: it is
	// a property of the eval set, not a measurement of the variant, so it is a true constant.
	var constPenalty float64
	measuredPenalties := map[string]string{} // term -> the metric its uncertainty comes from
	for _, term := range PenaltyTerms {
		w := p.Penalties[term]
		if w == 0 {
			continue
		}
		input := v.PenaltyInputs[term]
		if metric, ok := penaltyMetric[term]; ok {
			// On a DEGENERATE scale the metric cannot separate the variants, so every variant is
			// charged the same penalty — the scale's midpoint. Charging each its own noisy mean is
			// what let an indistinguishable difference decide a ranking. Using the same value here
			// as compositeReplicates uses is also what keeps the point estimate inside its own
			// interval, which it must be.
			if sc := c.Scales[metric]; sc.Degenerate {
				input = (sc.Min + sc.Max) / 2
			}
			measuredPenalties[term] = metric
		}
		amount := w * input
		if amount == 0 {
			continue
		}
		row.Penalties[term] = amount
		if _, ok := measuredPenalties[term]; ok {
			continue
		}
		constPenalty += amount
	}
	// The POINT composite still subtracts every penalty (that is the formula); only the interval
	// treats the two kinds differently.
	penaltyTotal := constPenalty
	for term := range measuredPenalties {
		penaltyTotal += row.Penalties[term]
	}

	// Point composite.
	mean := 0.0
	for _, spec := range c.Specs {
		m := v.Metrics[spec.Name]
		w := weightFor(p, spec.Role)
		comp := Component{
			Metric:        spec.Name,
			Role:          spec.Role,
			Raw:           m.Raw,
			Normalized:    m.Normalized,
			Direction:     spec.Direction,
			Unit:          spec.Unit,
			Contribution:  w * m.Normalized,
			JudgeStanding: spec.JudgeStanding,
		}
		row.Components[spec.Name] = comp
		mean += comp.Contribution
	}
	mean -= penaltyTotal

	// Bootstrapped composite interval.
	reps := c.compositeReplicates(v, p, constPenalty, measuredPenalties)
	low, high := evalstats.PercentileInterval(reps, c.Confidence)
	nSeeds, nCases, nObs := 0, 0, 0
	for _, spec := range c.Specs {
		iv := v.Metrics[spec.Name].Raw
		if iv.NSeeds > nSeeds {
			nSeeds = iv.NSeeds
		}
		if iv.NCases > nCases {
			nCases = iv.NCases
		}
		nObs += iv.NObs
	}
	row.Composite = evalstats.Interval{
		Mean:       mean,
		Low:        low,
		High:       high,
		NSeeds:     nSeeds,
		NCases:     nCases,
		NObs:       nObs,
		Method:     "bootstrap_composite_from_cached_replicates",
		Confidence: c.Confidence,
	}
	return row
}

// penaltyMetric maps a MEASUREMENT-derived penalty term to the metric whose uncertainty it inherits.
// A term absent from this map is treated as an exact constant.
var penaltyMetric = map[string]string{
	PenaltyToolError: evalharness.MetricToolErrorRate,
}

// compositeReplicates recombines the cached per-metric normalized replicates under a profile.
//
// constPenalty is subtracted from every replicate (it is exact). Each measured penalty is subtracted
// PER REPLICATE from its own metric's replicate, so its uncertainty widens the composite interval
// instead of silently shifting it.
func (c *Cache) compositeReplicates(v CachedVariant, p Profile, constPenalty float64, measured map[string]string) []float64 {
	n := c.replicateCount
	if n == 0 {
		return nil
	}
	out := make([]float64, n)
	for _, spec := range c.Specs {
		w := weightFor(p, spec.Role)
		if w == 0 {
			continue
		}
		reps := v.Metrics[spec.Name].replicates
		for i := 0; i < n && i < len(reps); i++ {
			out[i] += w * reps[i]
		}
	}
	for term, metric := range measured {
		w := p.Penalties[term]
		m, ok := v.Metrics[metric]
		if !ok {
			continue
		}
		// The penalty reads the RAW rate, and the cache holds NORMALIZED replicates, so the raw
		// replicate is reconstructed from the scale. Doing it here keeps one normalization pass in
		// the cache rather than a second raw copy of every replicate.
		sc := c.Scales[metric]
		for i := 0; i < n && i < len(m.replicates); i++ {
			out[i] -= w * denormalize(sc, m.replicates[i], c.directionOf(metric))
		}
	}
	for i := range out {
		out[i] -= constPenalty
	}
	return out
}

// denormalize maps a cached normalized replicate back to its raw units.
func denormalize(sc Scale, norm float64, dir evalstats.Direction) float64 {
	if sc.Degenerate {
		// A degenerate scale carries no information: every replicate normalized to 1, so the only
		// honest raw value to attribute is the scale's own midpoint — identical for every variant,
		// which is exactly the point.
		return (sc.Min + sc.Max) / 2
	}
	if dir == evalstats.LowerIsBetter {
		norm = 1 - norm
	}
	return sc.Min + norm*(sc.Max-sc.Min)
}

// directionOf reports a metric's declared direction.
func (c *Cache) directionOf(metric string) evalstats.Direction {
	if s, ok := c.SpecFor(metric); ok {
		return s.Direction
	}
	return evalstats.HigherIsBetter
}

// weightFor maps a role to its profile weight. Note that the CACHE already inverted lower-is-better
// metrics, so the formula's `(1 - cost)` and `(1 - latency)` terms are applied there once rather
// than here repeatedly — the arithmetic is identical and the inversion has exactly one home.
func weightFor(p Profile, role Role) float64 {
	switch role {
	case RoleQuality:
		return p.WQuality
	case RoleCost:
		return p.WCost
	case RoleLatency:
		return p.WLatency
	case RoleReliability:
		return p.WReliability
	default:
		return 0
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Gates
// ─────────────────────────────────────────────────────────────────────────────

// refusedGates returns the human-readable refusal for every configured gate that cannot legitimately
// be evaluated. Today that is exactly one case: a min-quality gate reading a judge metric that is
// uncalibrated or below its agreement floor. Such a gate disqualifies NOBODY, and the board says so.
func (c *Cache) refusedGates(g GateSet) []string {
	var out []string
	if g.MinQuality == nil {
		return nil
	}
	metric := g.QualityMetric
	if metric == "" {
		metric = evalharness.MetricTaskSuccess
	}
	spec, ok := c.SpecFor(metric)
	if !ok {
		return []string{fmtGateReason(GateMinQuality,
			fmt.Sprintf("metric %q is not on this board, so the gate cannot be evaluated and disqualifies nobody", metric))}
	}
	if spec.JudgeStanding == nil {
		return nil
	}
	if err := evalharness.EnsureGateEligible(*spec.JudgeStanding); err != nil {
		out = append(out, fmtGateReason(GateMinQuality, err.Error()+
			"; the gate is refused and disqualifies no variant"))
	}
	return out
}

// evaluateGates applies every configured, non-refused gate to one variant.
//
// Every check reads the variant's RAW value, not its normalized one: a max-cost gate is a claim
// about dollars, and normalizing first would make the gate mean "cheaper than the worst variant on
// this board", which changes meaning every time a variant is added.
func (c *Cache) evaluateGates(v CachedVariant, g GateSet, refused []string) GateOutcome {
	out := GateOutcome{Passed: true, Refused: refused}

	fail := func(name, reason string) {
		out.Passed = false
		out.Failed = append(out.Failed, name)
		out.Reasons = append(out.Reasons, fmtGateReason(name, reason))
	}

	if g.MaxCostPerRun != nil {
		if m, ok := v.Metrics[evalharness.MetricRunCostUSD]; ok && m.Raw.Mean > *g.MaxCostPerRun {
			fail(GateMaxCostPerRun, fmt.Sprintf("mean cost $%.4f exceeds the $%.4f ceiling", m.Raw.Mean, *g.MaxCostPerRun))
		}
	}
	if g.LatencySLAMs != nil {
		if m, ok := v.Metrics[evalharness.MetricRunLatencyMS]; ok && m.Raw.Mean > *g.LatencySLAMs {
			fail(GateLatencySLA, fmt.Sprintf("mean latency %.0fms exceeds the %.0fms SLA", m.Raw.Mean, *g.LatencySLAMs))
		}
	}
	if g.MinQuality != nil && len(refused) == 0 {
		metric := g.QualityMetric
		if metric == "" {
			metric = evalharness.MetricTaskSuccess
		}
		if m, ok := v.Metrics[metric]; ok && m.Raw.Mean < *g.MinQuality {
			fail(GateMinQuality, fmt.Sprintf("mean %s %.4f is below the %.4f floor", metric, m.Raw.Mean, *g.MinQuality))
		}
	}
	if len(g.ProviderAllowlist) > 0 {
		allowed := map[string]bool{}
		for _, p := range g.ProviderAllowlist {
			allowed[p] = true
		}
		var banned []string
		for _, p := range v.Providers {
			if !allowed[p] {
				banned = append(banned, p)
			}
		}
		if len(banned) > 0 {
			fail(GateProviderAllowlist, fmt.Sprintf("uses non-allowlisted provider(s) %v", banned))
		}
	}
	sort.Strings(out.Failed)
	sort.Strings(out.Reasons)
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Ties
// ─────────────────────────────────────────────────────────────────────────────

// markTies records, per row, which other rows it is statistically tied with.
//
// The rows still have ranks — something has to be printed in order — but a pair listed as tied is
// explicitly NOT a strict ordering. This is the composite-score half of the same rule
// evalstats.Compare enforces per metric: overlapping intervals are not a winner.
func markTies(rows []Row) {
	for i := range rows {
		var tied []string
		for j := range rows {
			if i == j {
				continue
			}
			if rows[i].Composite.Overlaps(rows[j].Composite) {
				tied = append(tied, rows[j].VariantID)
			}
		}
		sort.Strings(tied)
		rows[i].TiedWith = tied
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pareto frontier
// ─────────────────────────────────────────────────────────────────────────────

// ParetoPoint is one variant on the quality/cost/latency frontier, in RAW units — the frontier is
// about real tradeoffs, and a user deciding between $0.004 and 900ms needs the actual numbers.
type ParetoPoint struct {
	VariantID    string  `json:"variant_id"`
	ConfigHash   string  `json:"config_hash"`
	Quality      float64 `json:"quality"`
	CostUSD      float64 `json:"cost_usd"`
	LatencyMS    float64 `json:"latency_ms"`
	NonDominated bool    `json:"non_dominated"`
	Composite    float64 `json:"composite"`
}

// Pareto returns the quality/cost/latency points for the gate-passing rows, flagging the
// non-dominated ones.
//
// A variant is DOMINATED when another is at least as good on all three objectives and strictly
// better on one. Surfacing the frontier is what keeps a single collapsed score from hiding the
// tradeoff: variant X is cheaper, variant Y is higher-quality, and neither dominates.
func (c *Cache) Pareto(lb Leaderboard) []ParetoPoint {
	pts := make([]ParetoPoint, 0, len(lb.Ranked))
	for _, r := range lb.Ranked {
		pts = append(pts, ParetoPoint{
			VariantID:  r.VariantID,
			ConfigHash: r.ConfigHash,
			Quality:    r.Components[evalharness.MetricTaskSuccess].Raw.Mean,
			CostUSD:    r.Components[evalharness.MetricRunCostUSD].Raw.Mean,
			LatencyMS:  r.Components[evalharness.MetricRunLatencyMS].Raw.Mean,
			Composite:  r.Composite.Mean,
		})
	}
	for i := range pts {
		pts[i].NonDominated = true
		for j := range pts {
			if i == j {
				continue
			}
			if dominates(pts[j], pts[i]) {
				pts[i].NonDominated = false
				break
			}
		}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].VariantID < pts[j].VariantID })
	return pts
}

// dominates reports whether a is at least as good as b on quality, cost and latency, and strictly
// better on at least one.
func dominates(a, b ParetoPoint) bool {
	atLeast := a.Quality >= b.Quality && a.CostUSD <= b.CostUSD && a.LatencyMS <= b.LatencyMS
	strictly := a.Quality > b.Quality || a.CostUSD < b.CostUSD || a.LatencyMS < b.LatencyMS
	return atLeast && strictly
}
