package scoring

import (
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalstats"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fixtures
// ─────────────────────────────────────────────────────────────────────────────

// seriesFor builds a metric series over a shared eval set: same cases for every variant (the only
// way a comparison means anything), variant-specific stochastic noise.
func seriesFor(variantID, metric string, nCases, nSeeds int, mean, noise float64, noiseSeed int64) evalstats.Series {
	rng := rand.New(rand.NewSource(noiseSeed))
	s := evalstats.Series{VariantID: variantID, Metric: metric}
	for c := 0; c < nCases; c++ {
		for seed := 0; seed < nSeeds; seed++ {
			s.Obs = append(s.Obs, evalstats.Observation{
				CaseID: caseID(c),
				Seed:   int64(seed),
				Value:  mean + (rng.Float64()-0.5)*2*noise,
			})
		}
	}
	return s
}

func caseID(i int) string { return "case-" + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	return string(d)
}

// variant builds a fully-measured variant. quality/cost/latency/reliability are the means.
func variant(id string, quality, cost, latency, reliability float64, providers []string, seed int64) Variant {
	const nCases, nSeeds = 20, 5
	return Variant{
		VariantID:  id,
		ConfigHash: repeatHex(id),
		Label:      id,
		Providers:  providers,
		Metrics: map[string]evalstats.Series{
			evalharness.MetricTaskSuccess:   seriesFor(id, evalharness.MetricTaskSuccess, nCases, nSeeds, quality, 0.02, seed),
			evalharness.MetricRunCostUSD:    seriesFor(id, evalharness.MetricRunCostUSD, nCases, nSeeds, cost, 0.0005, seed+1),
			evalharness.MetricRunLatencyMS:  seriesFor(id, evalharness.MetricRunLatencyMS, nCases, nSeeds, latency, 10, seed+2),
			evalharness.MetricReliability:   seriesFor(id, evalharness.MetricReliability, nCases, nSeeds, reliability, 0.01, seed+3),
			evalharness.MetricToolErrorRate: seriesFor(id, evalharness.MetricToolErrorRate, nCases, nSeeds, 0.02, 0.005, seed+4),
			evalharness.MetricRunTokens:     seriesFor(id, evalharness.MetricRunTokens, nCases, nSeeds, 900, 20, seed+5),
		},
	}
}

func repeatHex(seed string) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = "0123456789abcdef"[(int(seed[i%len(seed)])+i)%16]
	}
	return string(out)
}

// standardBoard: three honest variants plus one cheap-but-broken one.
func standardBoard() Board {
	return Board{
		EvalSetHash: repeatHex("evalset"),
		Specs:       DefaultSpecs(),
		Variants: []Variant{
			variant("v-quality", 0.92, 0.050, 1800, 0.99, []string{"anthropic"}, 10),
			variant("v-balanced", 0.80, 0.020, 900, 0.97, []string{"anthropic"}, 20),
			variant("v-cheap-broken", 0.35, 0.002, 400, 0.95, []string{"anthropic"}, 30),
		},
	}
}

func buildCache(t *testing.T, b Board) *Cache {
	t.Helper()
	c, err := Build(b, evalstats.DefaultConfig())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return c
}

func f(v float64) *float64 { return &v }

// ─────────────────────────────────────────────────────────────────────────────
// Task 5.1 — normalization
// ─────────────────────────────────────────────────────────────────────────────

func TestMetricsAreNormalizedToUnitRangeBeforeWeighting(t *testing.T) {
	c := buildCache(t, standardBoard())

	for _, id := range c.Order {
		for _, spec := range c.Specs {
			m, ok := c.Metric(id, spec.Name)
			if !ok {
				t.Fatalf("%s: missing %s", id, spec.Name)
			}
			if m.Normalized < 0 || m.Normalized > 1 {
				t.Fatalf("%s %s normalized to %v, outside [0,1]", id, spec.Name, m.Normalized)
			}
			// The raw value is preserved alongside: a user needs the dollars to act.
			if m.Raw.Mean == 0 && spec.Name == evalharness.MetricRunCostUSD {
				t.Fatalf("%s: raw cost must be preserved, not replaced by its normalized form", id)
			}
		}
	}

	// A lower-is-better metric is inverted, so 1 always means good: the cheapest variant scores 1 on
	// the normalized cost term.
	cheap, _ := c.Metric("v-cheap-broken", evalharness.MetricRunCostUSD)
	pricey, _ := c.Metric("v-quality", evalharness.MetricRunCostUSD)
	if cheap.Normalized <= pricey.Normalized {
		t.Fatalf("cheaper must normalize higher on an inverted metric: cheap=%v pricey=%v",
			cheap.Normalized, pricey.Normalized)
	}
	if math.Abs(cheap.Normalized-1) > 1e-9 {
		t.Fatalf("the best variant on a metric normalizes to 1, got %v", cheap.Normalized)
	}
}

// A metric on which every variant scores identically does not decide the ranking.
func TestDegenerateMetricDoesNotDecideTheRanking(t *testing.T) {
	b := standardBoard()
	for i := range b.Variants {
		b.Variants[i].Metrics[evalharness.MetricReliability] =
			seriesFor(b.Variants[i].VariantID, evalharness.MetricReliability, 20, 5, 0.97, 0, 99)
	}
	c := buildCache(t, b)
	if !c.Scales[evalharness.MetricReliability].Degenerate {
		t.Fatal("an identical-across-variants metric must be flagged degenerate")
	}
	for _, id := range c.Order {
		m, _ := c.Metric(id, evalharness.MetricReliability)
		if m.Normalized != 1 {
			t.Fatalf("%s: a degenerate metric normalizes to 1 for everyone, got %v", id, m.Normalized)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Task 5.2 — the composite formula
// ─────────────────────────────────────────────────────────────────────────────

func TestCompositeMatchesTheDefinedFormula(t *testing.T) {
	c := buildCache(t, standardBoard())
	p := Balanced()
	lb := c.Rank(p, GateSet{Name: "none"})

	for _, row := range lb.Ranked {
		v := c.Variants[row.VariantID]
		want := p.WQuality*v.Metrics[evalharness.MetricTaskSuccess].Normalized +
			p.WCost*v.Metrics[evalharness.MetricRunCostUSD].Normalized +
			p.WLatency*v.Metrics[evalharness.MetricRunLatencyMS].Normalized +
			p.WReliability*v.Metrics[evalharness.MetricReliability].Normalized
		for _, amount := range row.Penalties {
			want -= amount
		}
		if math.Abs(row.Composite.Mean-want) > 1e-9 {
			t.Fatalf("%s: composite %v does not match the formula %v", row.VariantID, row.Composite.Mean, want)
		}
		// The cost and latency terms enter INVERTED (1 - cost̂): the cached normalized value already
		// carries the inversion, which the component breakdown must reflect.
		if row.Components[evalharness.MetricRunCostUSD].Direction != evalstats.LowerIsBetter {
			t.Fatal("cost must be declared lower-is-better")
		}
	}
}

// Task 5.7 — every composite carries a CI, and CI-overlapping variants are shown TIED.
func TestCompositeCarriesACIAndOverlappingPairsAreTied(t *testing.T) {
	b := Board{
		EvalSetHash: repeatHex("evalset"),
		Specs:       DefaultSpecs(),
		Variants: []Variant{
			// Two variants that are genuinely indistinguishable, plus one clearly better.
			variant("v-a", 0.700, 0.020, 900, 0.97, []string{"anthropic"}, 40),
			variant("v-b", 0.702, 0.020, 900, 0.97, []string{"anthropic"}, 50),
			variant("v-far", 0.980, 0.020, 900, 0.97, []string{"anthropic"}, 60),
		},
	}
	c := buildCache(t, b)
	lb := c.Rank(Balanced(), GateSet{Name: "none"})

	byID := map[string]Row{}
	for _, r := range lb.Ranked {
		byID[r.VariantID] = r
		if r.Composite.Low >= r.Composite.High && r.Composite.Width() != 0 {
			t.Fatalf("%s: composite must carry a real interval, got %+v", r.VariantID, r.Composite)
		}
		if r.Composite.Method == "" {
			t.Fatalf("%s: an interval must state its method", r.VariantID)
		}
	}
	if !containsStr(byID["v-a"].TiedWith, "v-b") {
		t.Fatalf("v-a and v-b have overlapping composite CIs and must be shown tied: %+v vs %+v",
			byID["v-a"].Composite, byID["v-b"].Composite)
	}
	if containsStr(byID["v-far"].TiedWith, "v-a") {
		t.Fatalf("a clearly-better variant must not be tied with the field: %+v", byID["v-far"].Composite)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tasks 5.5 / 5.6 / 8.3 — gate/weight separation
// ─────────────────────────────────────────────────────────────────────────────

// THE test: the cheapest variant violates the min-quality gate and is DISQUALIFIED — listed
// separately with the failed gate named — rather than ranked #1 on the cost-optimized profile.
func TestCheapestBelowMinQualityIsDisqualifiedNotRankedFirst(t *testing.T) {
	c := buildCache(t, standardBoard())
	gates := GateSet{Name: "production", MinQuality: f(0.60)}
	lb := c.Rank(CostOptimized(), gates)

	for _, r := range lb.Ranked {
		if r.VariantID == "v-cheap-broken" {
			t.Fatalf("the below-quality variant appears in the ranked order at rank %d", r.Rank)
		}
	}
	if len(lb.Ranked) == 0 || lb.Ranked[0].VariantID == "v-cheap-broken" {
		t.Fatal("the cheapest-but-broken variant must not be ranked first")
	}

	var dq *Row
	for i := range lb.Disqualified {
		if lb.Disqualified[i].VariantID == "v-cheap-broken" {
			dq = &lb.Disqualified[i]
		}
	}
	if dq == nil {
		t.Fatalf("the violating variant must be listed separately, got %v", lb.Disqualified)
	}
	if !containsStr(dq.Gate.Failed, GateMinQuality) {
		t.Fatalf("the failed gate must be NAMED, got %v", dq.Gate.Failed)
	}
	if len(dq.Gate.Reasons) == 0 {
		t.Fatal("the disqualification must explain itself")
	}

	// Control: WITHOUT the gate, the cost-optimized profile really would rank it first — which is
	// what makes the gate load-bearing rather than decorative.
	ungated := c.Rank(CostOptimized(), GateSet{Name: "none"})
	if ungated.Ranked[0].VariantID != "v-cheap-broken" {
		t.Fatalf("the fixture is not exercising the failure: without the gate the cheap variant should top "+
			"the cost board, got %s", ungated.Ranked[0].VariantID)
	}
	t.Logf("ungated cost-optimized #1 = %s (composite %.4f); gated board excludes it entirely",
		ungated.Ranked[0].VariantID, ungated.Ranked[0].Composite.Mean)
}

// A small overrun disqualifies rather than reducing the score: gates are not penalties.
func TestSmallGateOverrunDisqualifiesRatherThanPenalizes(t *testing.T) {
	c := buildCache(t, standardBoard())
	// v-balanced averages ~$0.020; a ceiling a hair under it must exclude it entirely.
	lb := c.Rank(Balanced(), GateSet{Name: "tight", MaxCostPerRun: f(0.0199)})

	for _, r := range lb.Ranked {
		if r.VariantID == "v-balanced" {
			t.Fatal("a variant over the cost ceiling must not appear anywhere in the ranked list")
		}
	}
	found := false
	for _, r := range lb.Disqualified {
		if r.VariantID == "v-balanced" {
			found = true
			if !containsStr(r.Gate.Failed, GateMaxCostPerRun) {
				t.Fatalf("want the cost gate named, got %v", r.Gate.Failed)
			}
		}
	}
	if !found {
		t.Fatal("the over-budget variant is missing from the disqualified section")
	}
}

func TestLatencySLAAndProviderAllowlistGates(t *testing.T) {
	b := standardBoard()
	b.Variants = append(b.Variants, variant("v-forbidden", 0.90, 0.010, 500, 0.99, []string{"unapproved-vendor"}, 70))
	c := buildCache(t, b)

	lb := c.Rank(Balanced(), GateSet{Name: "prod", LatencySLAMs: f(1000), ProviderAllowlist: []string{"anthropic"}})

	dq := map[string][]string{}
	for _, r := range lb.Disqualified {
		dq[r.VariantID] = r.Gate.Failed
	}
	if !containsStr(dq["v-quality"], GateLatencySLA) {
		t.Fatalf("the 1800ms variant must fail the 1000ms SLA, got %v", dq["v-quality"])
	}
	if !containsStr(dq["v-forbidden"], GateProviderAllowlist) {
		t.Fatalf("a non-allowlisted provider must disqualify, got %v", dq["v-forbidden"])
	}
}

// An unset gate is not a gate set to zero: nil means "not configured".
func TestUnsetGateDisqualifiesNobody(t *testing.T) {
	c := buildCache(t, standardBoard())
	lb := c.Rank(Balanced(), GateSet{Name: "empty"})
	if len(lb.Disqualified) != 0 {
		t.Fatalf("no gate is configured, so nothing may be disqualified, got %v", lb.Disqualified)
	}
	if len(lb.Ranked) != 3 {
		t.Fatalf("want all 3 variants ranked, got %d", len(lb.Ranked))
	}
}

// Task 3.4 x 5.5 — a min-quality gate reading an UNCALIBRATED judge is REFUSED: it disqualifies
// nobody, and the board says so out loud instead of behaving as if the gate were never configured.
func TestGateOnUncalibratedJudgeIsRefusedAndDisqualifiesNobody(t *testing.T) {
	b := standardBoard()
	specs := DefaultSpecs()
	for i := range specs {
		if specs[i].Name == evalharness.MetricTaskSuccess {
			specs[i].JudgeStanding = &evalharness.JudgeStanding{
				Metric: evalharness.MetricTaskSuccess, Floor: 0.6, Calibrated: false,
			}
		}
	}
	b.Specs = specs
	c := buildCache(t, b)

	lb := c.Rank(CostOptimized(), GateSet{Name: "prod", MinQuality: f(0.60)})
	if len(lb.Disqualified) != 0 {
		t.Fatalf("an uncalibrated judge must disqualify nobody, got %v", lb.Disqualified)
	}
	if len(lb.Notes) == 0 {
		t.Fatal("the refused gate must be surfaced on the board, not silently dropped")
	}
	found := false
	for _, n := range lb.Notes {
		if containsSub(n, "uncalibrated") && containsSub(n, GateMinQuality) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the note must name the gate and the reason, got %v", lb.Notes)
	}

	// And with a CALIBRATED judge the same gate does bite.
	for i := range specs {
		if specs[i].Name == evalharness.MetricTaskSuccess {
			specs[i].JudgeStanding = &evalharness.JudgeStanding{
				Metric: evalharness.MetricTaskSuccess, Agreement: 0.8, NHuman: 40, Floor: 0.6, Calibrated: true,
			}
		}
	}
	b.Specs = specs
	c2 := buildCache(t, b)
	lb2 := c2.Rank(CostOptimized(), GateSet{Name: "prod", MinQuality: f(0.60)})
	if len(lb2.Disqualified) == 0 {
		t.Fatal("a calibrated judge above its floor must be allowed to gate")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tasks 5.3 / 5.4 / 8.3 — profile switch re-ranks from cache, zero new runs, < 200 ms
// ─────────────────────────────────────────────────────────────────────────────

func TestProfileSwitchReRanksWithZeroNewRuns(t *testing.T) {
	c := buildCache(t, standardBoard())
	gates := GateSet{Name: "none"}

	quality := c.Rank(QualityFirst(), gates)
	cost := c.Rank(CostOptimized(), gates)

	if quality.RunsEnqueued != 0 || cost.RunsEnqueued != 0 {
		t.Fatalf("ranking must enqueue no runs, got %d and %d", quality.RunsEnqueued, cost.RunsEnqueued)
	}
	if quality.Ranked[0].VariantID == cost.Ranked[0].VariantID {
		t.Fatalf("the two profiles must actually re-rank; both put %s first", quality.Ranked[0].VariantID)
	}
	if quality.Ranked[0].VariantID != "v-quality" {
		t.Fatalf("quality-first must favour the highest-quality variant, got %s", quality.Ranked[0].VariantID)
	}
	if cost.Ranked[0].VariantID != "v-cheap-broken" {
		t.Fatalf("cost-optimized must favour the cheapest variant, got %s", cost.Ranked[0].VariantID)
	}
	t.Logf("quality-first #1=%s  cost-optimized #1=%s (0 runs enqueued for either)",
		quality.Ranked[0].VariantID, cost.Ranked[0].VariantID)
}

// ─────────────────────────────────────────────────────────────────────────────
// Task 5.4 / 8.3 — the re-rank stays fast enough to be interactive
//
// # Why this fence measures a RATIO and not a stopwatch
//
// It used to assert `best-of-5 < 200ms`, justified in its own comment by "the isolated re-rank runs
// in ~50 ms, so a genuine >4x regression still blows the 200 ms budget even at its best sample".
// That argument is only sound on the machine the 50 ms was measured on, and CI is not that machine.
// On 2026-08-12 the identical code — `internal/scoring/` had not changed one byte since v0.21.0 —
// measured 46-51 ms on a developer laptop and 217 ms and 236 ms on GitHub's shared ubuntu runners.
// The runner is 4-5x slower, so the environment consumed the whole 4x margin the teeth depended on,
// and the fence failed 2 of 3 consecutive main-branch runs. A fence that says "someone made Rank
// quadratic" in the same words it says "the runner was busy" has stopped carrying information.
//
// The fix is to stop measuring in seconds and start measuring in units of THIS machine, taken on
// THIS run. referenceRerank below does the same quantity of work as one healthy re-rank, built out
// of nothing but slices and a sort, and the real re-rank must stay within maxRerankRatio of it. A
// slow machine slows both sides and the ratio does not move; a real regression moves the numerator
// only. That is the property a test running on hardware it does not choose can honestly own.
//
// # What this deliberately does NOT assert
//
// The absolute 200 ms product budget. A shared runner cannot tell you whether a developer machine
// meets it, so asserting it there is how the flake got in. The absolute figure is LOGGED on every
// run for a human to read, and never asserted. It is a product claim about a class of machine, not
// a property of the code, and the two want different instruments.
// ─────────────────────────────────────────────────────────────────────────────

// The ruler's dimensions. They are LITERALS rather than values read off the cache, because a ruler
// that resizes itself to whatever the code under test now does is not a ruler — if Rank started
// weighting zero metrics, a derived reference would shrink with it and the ratio would stay 1.
// TestReRankStaysWithinItsReferenceWorkload checks the cache still has this shape and fails loudly
// if not, so a real change to the workload forces a deliberate re-sizing of the ruler here.
const (
	refVariants   = 500  // variants on the board
	refMetrics    = 6    // len(DefaultSpecs())
	refWeighted   = 4    // of those, the ones a profile gives a non-zero weight (2 are informational)
	refReplicates = 2000 // evalstats.DefaultConfig().Bootstrap
)

// maxRerankRatio is how many reference workloads one re-rank may cost.
//
// The reference does the same per-variant work as `row` does — accumulate the weighted metric
// replicates, subtract a per-replicate penalty and a constant, then sort for the percentile
// interval — so a healthy re-rank sits near 1.
//
// Measured on healthy code, 2026-08-12, by running this fence on both machine classes:
//
//	developer laptop (arm64, 18 cores) : re-rank 46ms / reference 36ms = 1.26x  (spread 0.03 over 3 runs)
//	GitHub shared ubuntu runner        : re-rank 145ms / reference 91ms = 1.60x
//
// That pair IS the argument for this design. The absolute time moves 3.1x between the two machines —
// the swing that made the old wall-clock budget fire on a docs-only commit — while the ratio moves
// 1.26 to 1.60. The ruler is not perfectly invariant (Rank does map lookups and allocation the
// slice-only reference does not, and those degrade faster on a shared runner), so the drift is real
// and is why the budget is not set just above the laptop's number.
//
// 4.0 keeps the teeth the original comment claimed while clearing the worst observed baseline by
// 2.5x: a regression has to more than double Rank's cost on CI before this fires. Tightening below
// ~3.0 wants more than the single CI sample above, because one measurement cannot show how far the
// ratio itself moves run to run — and guessing at that is what put the flake here in the first place.
const maxRerankRatio = 4.0

// referenceSink defeats dead-store elimination: without a consumed result the compiler is free to
// delete the reference workload entirely, which would make the ruler measure nothing and the ratio
// unbounded — a fence that goes red for the one reason it must not.
var referenceSink float64

// referenceData builds the replicate slices the reference workload chews through. Built once,
// OUTSIDE the timed region, exactly as the real cache is built outside it.
func referenceData(variants, metrics, replicates int) [][][]float64 {
	rng := rand.New(rand.NewSource(0x5EED))
	out := make([][][]float64, variants)
	for v := range out {
		out[v] = make([][]float64, metrics)
		for m := range out[v] {
			s := make([]float64, replicates)
			for i := range s {
				s[i] = rng.Float64()
			}
			out[v][m] = s
		}
	}
	return out
}

// referenceRerank is a synthetic workload shaped like one re-rank of the same board.
//
// It is implemented HERE, in the test, out of the standard library only, and it must stay that way:
// it is the ruler, and a ruler that calls into the thing it measures cannot detect that thing
// getting slower.
func referenceRerank(data [][][]float64) float64 {
	if len(data) == 0 || len(data[0]) == 0 {
		return 0
	}
	n := len(data[0][0])
	buf := make([]float64, n)
	sorted := make([]float64, n)
	var acc float64
	for _, variant := range data {
		for i := range buf {
			buf[i] = 0
		}
		// The weighted composite: refWeighted metrics accumulated per replicate.
		for m := 0; m < refWeighted && m < len(variant); m++ {
			w := 0.1 + float64(m)*0.2
			reps := variant[m]
			for i := 0; i < n && i < len(reps); i++ {
				buf[i] += w * reps[i]
			}
		}
		// One MEASURED penalty, subtracted per replicate from its own metric's replicate.
		if pr := variant[len(variant)-1]; len(pr) > 0 {
			for i := 0; i < n && i < len(pr); i++ {
				buf[i] -= 0.05 * pr[i]
			}
		}
		// One CONSTANT penalty, subtracted from every replicate.
		for i := range buf {
			buf[i] -= 0.01
		}
		// The percentile interval: a sort of the composite replicates.
		copy(sorted, buf)
		sort.Float64s(sorted)
		acc += sorted[n/40] + sorted[n-1-n/40]
	}
	return acc
}

// bestOf runs fn iterations times and returns its fastest wall-clock sample. The minimum is used
// rather than the mean because a single sample conflates how fast the work IS with whatever
// scheduler stall the machine was under; the fastest sample is the closest available estimate of
// the former, and both sides of the ratio are estimated the same way.
func bestOf(iterations int, fn func()) time.Duration {
	var best time.Duration
	for i := 0; i < iterations; i++ {
		start := time.Now()
		fn()
		if elapsed := time.Since(start); i == 0 || elapsed < best {
			best = elapsed
		}
	}
	return best
}

func TestReRankStaysWithinItsReferenceWorkload(t *testing.T) {
	b := Board{EvalSetHash: repeatHex("evalset"), Specs: DefaultSpecs()}
	for i := 0; i < refVariants; i++ {
		q := 0.50 + float64(i%40)/100
		b.Variants = append(b.Variants, variant("v-"+itoa(i), q, 0.005+float64(i%10)/1000, 400+float64(i%20)*30, 0.95, []string{"anthropic"}, int64(1000+i)))
	}
	c := buildCache(t, b)

	// The ruler is sized by hand, so a change to the real workload's shape must not slip past it.
	if len(c.Specs) != refMetrics || c.replicateCount != refReplicates || len(c.Order) != refVariants {
		t.Fatalf("the reference workload is sized for %d variants x %d metrics x %d replicates, but the cache "+
			"now holds %d x %d x %d — re-size the ruler deliberately, do not let it drift",
			refVariants, refMetrics, refReplicates, len(c.Order), len(c.Specs), c.replicateCount)
	}
	weighted := 0
	for _, spec := range c.Specs {
		if weightFor(CostOptimized(), spec.Role) != 0 {
			weighted++
		}
	}
	if weighted != refWeighted {
		t.Fatalf("the reference workload accumulates %d weighted metrics, but the profile now weights %d — "+
			"re-size the ruler deliberately", refWeighted, weighted)
	}

	// The cache build is the expensive step and happens once; the profile switch is what must be fast.
	const iterations = 5
	var lb Leaderboard
	rerank := bestOf(iterations, func() {
		lb = c.Rank(CostOptimized(), GateSet{Name: "prod", MinQuality: f(0.55)})
	})

	// The ruler, measured on the same machine, in the same process, moments later.
	refData := referenceData(refVariants, refMetrics, refReplicates)
	reference := bestOf(iterations, func() {
		referenceSink = referenceRerank(refData)
	})
	if reference <= 0 {
		t.Fatalf("the reference workload measured %v — the ruler is broken, so the ratio below would be "+
			"meaningless", reference)
	}

	ratio := float64(rerank) / float64(reference)
	t.Logf("re-rank of %d variants: %v (best of %d) | reference workload: %v | ratio %.2fx of %.2fx allowed "+
		"| %d ranked, %d disqualified, %d runs enqueued",
		len(b.Variants), rerank, iterations, reference, ratio, maxRerankRatio,
		len(lb.Ranked), len(lb.Disqualified), lb.RunsEnqueued)

	if ratio > maxRerankRatio {
		t.Fatalf("re-rank cost %.2fx the reference workload (%v vs %v), over the %.2fx budget — this is a "+
			"regression in Rank, not a slow machine: a slow machine slows the reference by the same factor",
			ratio, rerank, reference, maxRerankRatio)
	}
	if lb.RunsEnqueued != 0 {
		t.Fatalf("re-rank must enqueue zero runs, got %d", lb.RunsEnqueued)
	}
	if len(lb.Ranked)+len(lb.Disqualified) != refVariants {
		t.Fatalf("every variant must appear exactly once, got %d + %d", len(lb.Ranked), len(lb.Disqualified))
	}
}

// The cache is keyed by eval-set hash: numbers measured on one set are never reused for another.
func TestCacheCarriesItsEvalSetHash(t *testing.T) {
	c := buildCache(t, standardBoard())
	if c.EvalSetHash == "" {
		t.Fatal("the cache must record which eval set produced it")
	}
	lb := c.Rank(Balanced(), GateSet{Name: "none"})
	if lb.EvalSetHash != c.EvalSetHash {
		t.Fatal("every board must be attributable to an exact eval set")
	}
	for _, r := range lb.Ranked {
		if r.ConfigHash == "" {
			t.Fatalf("%s: every row must carry its config lineage", r.VariantID)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Pareto
// ─────────────────────────────────────────────────────────────────────────────

func TestParetoFrontierIsTheNonDominatedSet(t *testing.T) {
	b := Board{
		EvalSetHash: repeatHex("evalset"),
		Specs:       DefaultSpecs(),
		Variants: []Variant{
			variant("v-best-quality", 0.95, 0.050, 1500, 0.99, []string{"anthropic"}, 10),
			variant("v-cheapest", 0.60, 0.002, 300, 0.99, []string{"anthropic"}, 20),
			// Dominated: worse quality than best-quality, dearer and slower than cheapest.
			variant("v-dominated", 0.55, 0.030, 1200, 0.99, []string{"anthropic"}, 30),
		},
	}
	c := buildCache(t, b)
	lb := c.Rank(Balanced(), GateSet{Name: "none"})
	pts := c.Pareto(lb)

	byID := map[string]ParetoPoint{}
	for _, p := range pts {
		byID[p.VariantID] = p
	}
	if !byID["v-best-quality"].NonDominated {
		t.Fatal("the highest-quality variant is on the frontier")
	}
	if !byID["v-cheapest"].NonDominated {
		t.Fatal("the cheapest and fastest variant is on the frontier")
	}
	if byID["v-dominated"].NonDominated {
		t.Fatalf("a variant worse on all three objectives is dominated: %+v", byID["v-dominated"])
	}
	// The frontier is reported in RAW units, because a user deciding between $0.002 and 1500ms needs
	// the actual numbers.
	if byID["v-cheapest"].CostUSD > 0.01 {
		t.Fatalf("Pareto must report raw cost, got %v", byID["v-cheapest"].CostUSD)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Missing data
// ─────────────────────────────────────────────────────────────────────────────

// A missing metric is an ERROR, not a zero: a variant nobody measured must not be disqualified for
// scoring zero quality.
func TestMissingMetricIsAnErrorNotAZero(t *testing.T) {
	b := standardBoard()
	delete(b.Variants[0].Metrics, evalharness.MetricTaskSuccess)
	if _, err := Build(b, evalstats.DefaultConfig()); err == nil {
		t.Fatal("a variant missing a declared metric must be refused, not scored zero")
	}
}

func TestEmptyBoardIsRefused(t *testing.T) {
	if _, err := Build(Board{EvalSetHash: "x"}, evalstats.DefaultConfig()); err == nil {
		t.Fatal("an empty board must be refused")
	}
}

// Weak-labeled evidence lowers the score softly and is flagged — it never disqualifies.
func TestWeakLabeledEvidenceIsFlaggedAndPenalizedNotGated(t *testing.T) {
	b := standardBoard()
	b.Variants[1].WeakCaseIDs = []string{"case-1", "case-2", "case-3", "case-4"}
	c := buildCache(t, b)
	lb := c.Rank(QualityFirst(), GateSet{Name: "none"})

	var row *Row
	for i := range lb.Ranked {
		if lb.Ranked[i].VariantID == "v-balanced" {
			row = &lb.Ranked[i]
		}
	}
	if row == nil {
		t.Fatal("a weak-labeled variant must still be ranked, not disqualified")
	}
	if !row.WeakLabeled {
		t.Fatal("a row resting on unreviewed references must be flagged weak")
	}
	if row.Penalties[PenaltyWeakReference] <= 0 {
		t.Fatalf("the weak-reference penalty must apply, got %v", row.Penalties)
	}
}

func containsStr(hay []string, want string) bool {
	for _, h := range hay {
		if h == want {
			return true
		}
	}
	return false
}

func containsSub(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// REGRESSION FENCE for the normalization defect this package's own tests found.
//
// Min-max normalization divides by the observed spread. When that spread is comparable to the
// measurement noise — three variants that are statistically indistinguishable on latency — dividing
// by it amplifies noise across the whole [0,1] axis and hands the composite a confidence interval
// half the board wide. The step meant to make metrics COMPARABLE was promoting noise to a ranking
// signal. A metric that cannot separate the field must not decide the ranking.
func TestMetricThatCannotSeparateTheFieldDoesNotDecideTheRanking(t *testing.T) {
	// Latency means differ by ~1ms against ±40ms of noise: statistically identical.
	b := Board{
		EvalSetHash: repeatHex("evalset"),
		Specs:       DefaultSpecs(),
		Variants: []Variant{
			variant("v-a", 0.90, 0.020, 900, 0.97, []string{"anthropic"}, 10),
			variant("v-b", 0.60, 0.020, 901, 0.97, []string{"anthropic"}, 20),
			variant("v-c", 0.30, 0.020, 902, 0.97, []string{"anthropic"}, 30),
		},
	}
	for i := range b.Variants {
		id := b.Variants[i].VariantID
		b.Variants[i].Metrics[evalharness.MetricRunLatencyMS] =
			seriesFor(id, evalharness.MetricRunLatencyMS, 20, 5, 900+float64(i), 40, int64(500+i))
	}
	c := buildCache(t, b)

	sc := c.Scales[evalharness.MetricRunLatencyMS]
	if !sc.Degenerate {
		t.Fatalf("a metric whose variants are statistically indistinguishable must be degenerate: %+v", sc)
	}
	if sc.DegenerateReason == "" {
		t.Fatal("a degenerate metric must say which condition fired")
	}
	t.Logf("latency scale: %+v", sc)

	lb := c.Rank(Balanced(), GateSet{Name: "none"})
	for _, r := range lb.Ranked {
		if got := r.Components[evalharness.MetricRunLatencyMS].Normalized; got != 1 {
			t.Fatalf("%s: a non-separating metric normalizes to 1 for everyone, got %v", r.VariantID, got)
		}
		// And the composite interval stays tight: it now reflects the metrics that DO separate.
		if r.Composite.Width() > 0.25 {
			t.Fatalf("%s: composite interval %v is implausibly wide; noise is leaking through normalization",
				r.VariantID, r.Composite.Width())
		}
	}
	// Quality DOES separate, so it still drives the order.
	if lb.Ranked[0].VariantID != "v-a" {
		t.Fatalf("the separating metric must still decide the ranking, got %s", lb.Ranked[0].VariantID)
	}
}

// REGRESSION FENCE: a measurement-derived penalty must enter the BOOTSTRAP, not be subtracted as an
// exact constant.
//
// The defect: three variants that are statistically indistinguishable on every metric normalize to
// 1 everywhere, so every composite replicate is identical and the interval collapses to zero width.
// The tool-error penalty was then subtracted as each variant's exact mean — a number that differs
// between them only by noise. Zero-width intervals plus a noise-sized offset produced a confident
// 1-2-3 ranking of three variants nothing could tell apart, with no tie flag anywhere.
func TestMeasuredPenaltyDoesNotTurnNoiseIntoARanking(t *testing.T) {
	b := Board{EvalSetHash: repeatHex("evalset"), Specs: DefaultSpecs()}
	// Identical generating parameters, different noise streams: genuinely indistinguishable.
	for i, id := range []string{"v-a", "v-b", "v-c"} {
		b.Variants = append(b.Variants, variant(id, 0.700, 0.020, 900, 0.98, []string{"anthropic"}, int64(10+i*10)))
	}
	c := buildCache(t, b)

	// Every metric must be degenerate — otherwise the fixture is not exercising the failure.
	for _, spec := range c.Specs {
		if !c.Scales[spec.Name].Degenerate {
			t.Fatalf("fixture is not degenerate on %s; the test would prove nothing", spec.Name)
		}
	}

	lb := c.Rank(Balanced(), GateSet{Name: "none"})
	for _, r := range lb.Ranked {
		t.Logf("%s composite=%.6f interval=[%.6f,%.6f] tiedWith=%v",
			r.VariantID, r.Composite.Mean, r.Composite.Low, r.Composite.High, r.TiedWith)
	}
	for _, r := range lb.Ranked {
		if len(r.TiedWith) != len(lb.Ranked)-1 {
			t.Fatalf("%s: variants indistinguishable on every metric must ALL be tied, got tiedWith=%v",
				r.VariantID, r.TiedWith)
		}
		// The point estimate must lie inside its own interval. It did not while the penalty was an
		// exact per-variant constant subtracted from a zero-width bootstrap.
		if r.Composite.Mean < r.Composite.Low || r.Composite.Mean > r.Composite.High {
			t.Fatalf("%s: composite %.6f lies outside its own interval [%.6f,%.6f]",
				r.VariantID, r.Composite.Mean, r.Composite.Low, r.Composite.High)
		}
	}
}
