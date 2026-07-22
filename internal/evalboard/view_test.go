package evalboard

import (
	"math/rand"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalgen"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/scoring"
)

// Every assertion below corresponds to a state the product journey (product-journey.md §3) requires
// to be visually distinct — and four of them are regression fences for defects the LIVE board
// surfaced, which is why they are here rather than in a prose checklist.

func series(variantID, metric string, mean, noise float64, seed int64, nCases, nSeeds int) evalstats.Series {
	rng := rand.New(rand.NewSource(seed))
	s := evalstats.Series{VariantID: variantID, Metric: metric}
	for c := 0; c < nCases; c++ {
		for sd := 0; sd < nSeeds; sd++ {
			s.Obs = append(s.Obs, evalstats.Observation{
				CaseID: "case-" + string(rune('a'+c)), Seed: int64(sd),
				Value: mean + (rng.Float64()-0.5)*2*noise,
			})
		}
	}
	return s
}

func variant(id string, quality, cost, latency float64, seed int64, nSeeds int) scoring.Variant {
	return scoring.Variant{
		VariantID:  id,
		ConfigHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Label:      id,
		Providers:  []string{"anthropic"},
		Metrics: map[string]evalstats.Series{
			evalharness.MetricTaskSuccess:   series(id, evalharness.MetricTaskSuccess, quality, 0.02, seed, 12, nSeeds),
			evalharness.MetricRunCostUSD:    series(id, evalharness.MetricRunCostUSD, cost, 0.0005, seed+1, 12, nSeeds),
			evalharness.MetricRunLatencyMS:  series(id, evalharness.MetricRunLatencyMS, latency, 10, seed+2, 12, nSeeds),
			evalharness.MetricReliability:   series(id, evalharness.MetricReliability, 0.98, 0.005, seed+3, 12, nSeeds),
			evalharness.MetricToolErrorRate: series(id, evalharness.MetricToolErrorRate, 0.01, 0.002, seed+4, 12, nSeeds),
			evalharness.MetricRunTokens:     series(id, evalharness.MetricRunTokens, 800, 20, seed+5, 12, nSeeds),
		},
	}
}

func buildView(t *testing.T, variants []scoring.Variant, gates scoring.GateSet, in Input) View {
	t.Helper()
	c, err := scoring.Build(scoring.Board{
		EvalSetHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Specs:       scoring.DefaultSpecs(),
		Variants:    variants,
	}, evalstats.DefaultConfig())
	if err != nil {
		t.Fatalf("build cache: %v", err)
	}
	in.Cache = c
	in.Gates = gates
	if in.Profile.Name == "" {
		in.Profile = scoring.Balanced()
	}
	if in.WorkflowID == "" {
		in.WorkflowID = "wf"
	}
	return Build(in)
}

// A disqualified variant is OUT of the ranked order — its own section, failed gate named.
func TestDisqualifiedVariantIsSeparatedAndNamed(t *testing.T) {
	v := buildView(t, []scoring.Variant{
		variant("v-good", 0.90, 0.02, 900, 10, 5),
		variant("v-broken", 0.30, 0.002, 300, 20, 5),
	}, scoring.GateSet{Name: "prod", MinQuality: f(0.55)}, Input{Profile: scoring.CostOptimized()})

	for _, r := range v.Ranked {
		if r.VariantID == "v-broken" {
			t.Fatal("a disqualified variant must not appear in the ranked order")
		}
	}
	if len(v.Disqualified) != 1 || v.Disqualified[0].VariantID != "v-broken" {
		t.Fatalf("want the broken variant listed separately, got %+v", v.Disqualified)
	}
	dq := v.Disqualified[0]
	if len(dq.FailedGates) == 0 || dq.FailedGates[0] != scoring.GateMinQuality {
		t.Fatalf("the failed gate must be named, got %v", dq.FailedGates)
	}
	if !containsStr(dq.Flags, "disqualified") {
		t.Fatalf("a disqualified row must carry a TEXT flag, not colour alone: %v", dq.Flags)
	}
	if dq.Rank != 0 {
		t.Fatalf("a disqualified variant has no rank; it is out, not last (got %d)", dq.Rank)
	}
}

// The all-tie board is flagged as data, so the UI never has to count overlaps itself.
func TestAllTieIsAComputedFactOnTheBoard(t *testing.T) {
	v := buildView(t, []scoring.Variant{
		variant("v-a", 0.700, 0.02, 900, 10, 5),
		variant("v-b", 0.702, 0.02, 900, 20, 5),
		variant("v-c", 0.701, 0.02, 900, 30, 5),
	}, scoring.GateSet{Name: "none"}, Input{})

	if !v.AllTie {
		t.Fatalf("three indistinguishable variants must set AllTie: %+v",
			[]string{v.Ranked[0].VariantID, v.Ranked[1].VariantID, v.Ranked[2].VariantID})
	}
	for _, r := range v.Ranked {
		if !containsStr(r.Flags, "tie") {
			t.Fatalf("%s: every row of an all-tie board carries the tie flag, got %v", r.VariantID, r.Flags)
		}
	}
	// REGRESSION: the all-tie sentence must appear ONCE. The first live render emitted it as both a
	// styled banner and a plain note, which read as two separate findings.
	for _, n := range v.Notes {
		if containsSub(n, "No winner") {
			t.Fatalf("the all-tie message must not also be a note; the UI renders AllTie itself: %q", n)
		}
	}
}

// REGRESSION (found live): a variant whose runs did not complete is EXCLUDED and NAMED, and the
// board goes partial — it does not crash, and it does not silently show fewer rows than variants.
func TestUnmeasuredVariantsAreExcludedNamedAndMakeTheBoardPartial(t *testing.T) {
	good := variant("v-good", 0.90, 0.02, 900, 10, 5)
	// The budget cut this one off after two seeds — under the floor of five.
	cut := variant("v-cut-off", 0.80, 0.02, 900, 20, 2)

	v := buildView(t, []scoring.Variant{good, cut}, scoring.GateSet{Name: "none"},
		Input{Progress: Progress{UnitsPlanned: 120, UnitsCompleted: 120, SeedFloor: 5}})

	if len(v.Ranked) != 1 || v.Ranked[0].VariantID != "v-good" {
		t.Fatalf("only the measured variant may be ranked, got %+v", v.Ranked)
	}
	if len(v.Unmeasured) != 1 || v.Unmeasured[0].VariantID != "v-cut-off" {
		t.Fatalf("the unmeasured variant must be named, got %+v", v.Unmeasured)
	}
	if v.Unmeasured[0].Reason == "" {
		t.Fatal("an excluded variant must say why")
	}
	if v.State != StatePartial {
		t.Fatalf("a board missing a variant's measurements is partial, got %q", v.State)
	}
	// It is ONE summary note plus the list — not one note per variant. Fifty-eight notes buried the
	// banner that explained them.
	summaries := 0
	for _, n := range v.Notes {
		if containsSub(n, "could not be scored") {
			summaries++
		}
		if containsSub(n, "v-cut-off") {
			t.Fatalf("per-variant detail belongs in Unmeasured, not in Notes: %q", n)
		}
	}
	if summaries != 1 {
		t.Fatalf("want exactly one summary note, got %d in %v", summaries, v.Notes)
	}
}

// A board where NOTHING was measured is an error, not an empty board.
func TestBoardWithNoUsableMeasurementIsRefused(t *testing.T) {
	_, err := scoring.Build(scoring.Board{
		EvalSetHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Specs:       scoring.DefaultSpecs(),
		Variants:    []scoring.Variant{variant("v-a", 0.9, 0.02, 900, 10, 1)},
	}, evalstats.DefaultConfig())
	if err == nil {
		t.Fatal("a board where no variant has a usable measurement must be refused")
	}
}

// The error view carries the message and NOTHING else — no hollow scaffold.
func TestErrorViewCarriesNoRows(t *testing.T) {
	v := Error("wf", errString("the score cache could not be built"))
	if v.State != StateError || v.Error == "" {
		t.Fatalf("error view must name what failed: %+v", v)
	}
	if len(v.Ranked) != 0 || len(v.Disqualified) != 0 || len(v.Pareto) != 0 {
		t.Fatal("an error view must not render a partial board as if whole")
	}
	if v.EvalSetHash != "" {
		t.Fatal("an error view has no eval set to cite")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// An empty board is distinct from an error: the action is "add a variant", not "wait".
func TestEmptyBoardIsItsOwnState(t *testing.T) {
	v := Build(Input{WorkflowID: "wf", Profile: scoring.Balanced()})
	if v.State != StateEmpty {
		t.Fatalf("want empty, got %q", v.State)
	}
	if v.Error != "" {
		t.Fatal("an empty board is not an error")
	}
	if len(v.Profiles) == 0 {
		t.Fatal("the profile list must be present even on an empty board")
	}
}

// Ranking never enqueues a run — the field exists so this is asserted, not assumed.
func TestRankingEnqueuesNothing(t *testing.T) {
	for _, p := range []scoring.Profile{scoring.Balanced(), scoring.QualityFirst(), scoring.CostOptimized()} {
		v := buildView(t, []scoring.Variant{
			variant("v-a", 0.90, 0.02, 900, 10, 5),
			variant("v-b", 0.60, 0.01, 400, 20, 5),
		}, scoring.GateSet{Name: "none"}, Input{Profile: p})
		if v.RunsEnqueued != 0 {
			t.Fatalf("profile %s enqueued %d runs", p.Name, v.RunsEnqueued)
		}
	}
}

// The coverage view keeps the residual as first-class data, with a reason per obligation.
func TestCoverageViewSurfacesTheResidualWithReasons(t *testing.T) {
	rep := evalgen.CoverageReport{
		NCases:     4,
		Iterations: 3,
		Path: evalgen.Dimension{Name: "path", Target: 1, Achieved: 0.5, Items: []evalgen.Item{
			{ID: "a->b", Kind: "edge", Covered: true},
			{ID: "a->dead", Kind: "edge", Covered: false, Unreachable: true},
		}},
		Node:     evalgen.Dimension{Name: "node", Target: 1, Achieved: 1},
		EdgeCase: evalgen.Dimension{Name: "edge_case", Target: 1, Achieved: 1},
	}
	q := evalgen.SetQuality{NCases: 4, Diversity: 0.7, NGold: 2, NWeak: 2, OracleCoverage: 1, NOracle: 4}

	v := Build(Input{WorkflowID: "wf", Profile: scoring.Balanced(), Coverage: &rep, Quality: &q,
		StoppedBecause: "max-iteration bound reached"})

	cv := v.Coverage
	if !cv.Measured {
		t.Fatal("a supplied report must read as measured")
	}
	if cv.Met {
		t.Fatal("50% path coverage does not meet a 100% target")
	}
	if len(cv.Residual) != 1 || cv.Residual[0].ID != "a->dead" {
		t.Fatalf("the uncovered obligation must be named, got %+v", cv.Residual)
	}
	if !cv.Residual[0].Unreachable || cv.Residual[0].Reason == "" {
		t.Fatalf("a residual must carry its reason: %+v", cv.Residual[0])
	}
	if cv.StoppedBecause == "" {
		t.Fatal("the coverage screen must say why the loop stopped")
	}
	// The obligation stays in the DENOMINATOR — 1 of 2, not 1 of 1.
	if cv.Dimensions[0].Total != 2 || cv.Dimensions[0].Covered != 1 {
		t.Fatalf("an unreachable obligation must stay in the denominator, got %d of %d",
			cv.Dimensions[0].Covered, cv.Dimensions[0].Total)
	}
}

// An interval computed from fewer seeds than the floor is marked provisional, not ranked as final.
func TestUnderSeededRowIsMarkedProvisional(t *testing.T) {
	v := buildView(t, []scoring.Variant{
		variant("v-a", 0.90, 0.02, 900, 10, 5),
		variant("v-b", 0.70, 0.02, 900, 20, 5),
	}, scoring.GateSet{Name: "none"}, Input{Progress: Progress{SeedFloor: 9}})

	for _, r := range v.Ranked {
		if !r.Provisional {
			t.Fatalf("%s: 5 seeds under a floor of 9 must be provisional", r.VariantID)
		}
		if !containsStr(r.Flags, "provisional") {
			t.Fatalf("%s: provisional must be a TEXT flag, got %v", r.VariantID, r.Flags)
		}
	}
}

// Every row carries its config lineage — that is what makes a win attributable.
func TestEveryRowCarriesConfigLineage(t *testing.T) {
	v := buildView(t, []scoring.Variant{
		variant("v-a", 0.90, 0.02, 900, 10, 5),
		variant("v-b", 0.70, 0.02, 900, 20, 5),
	}, scoring.GateSet{Name: "none"}, Input{})
	for _, r := range append(v.Ranked, v.Disqualified...) {
		if len(r.ConfigHash) != 64 {
			t.Fatalf("%s: full config_hash must be carried, got %q", r.VariantID, r.ConfigHash)
		}
		if len(r.ConfigHashShort) != 12 {
			t.Fatalf("%s: a display prefix must be provided, got %q", r.VariantID, r.ConfigHashShort)
		}
		if len(r.Components) == 0 {
			t.Fatalf("%s: the component breakdown must be present on every row", r.VariantID)
		}
		if r.Method == "" {
			t.Fatalf("%s: the interval must state its method", r.VariantID)
		}
	}
}

func f(v float64) *float64 { return &v }

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

// REGRESSION FENCE for two defects, one line apart: the coverage pass correctly set LowConfidence
// for a not-measurable path axis and a zero-covered node axis, and the set-quality pass then
// ASSIGNED over both the flag and the reasons — so the board reported `low_confidence: false` for a
// board carrying neither. A board that quiet about that is worse than no board.
//
// PROVENANCE, stated exactly because the surrounding phase is about not overclaiming it:
//   - the VACUOUS path axis is real, and came from P1 discovery over nousresearch/hermes-agent
//     (40 call sites, zero edges — inter-node flow is P5, not P1);
//   - the ZERO-COVERED node axis in that same run was an artifact of the demo harness, whose prober
//     is hardcoded to a fixture topology and could never have reached that repo's nodes. It is a
//     real defect class and belongs in this fence, but it was NOT a measurement of that repo.
//
// The clobber itself is a pure logic defect and is provable from this test alone.
func TestCoverageDerivedLowConfidenceSurvivesTheSetQualityPass(t *testing.T) {
	rep := evalgen.CoverageReport{
		NCases: 53,
		// A vacuous path axis: the IR carried no edges, so there are no path obligations.
		Path: evalgen.Dimension{Name: "path", Target: 1, Achieved: 0, Vacuous: true},
		// A zero-covered node axis: 40 obligations, not one discharged.
		Node: evalgen.Dimension{Name: "node", Target: 1, Achieved: 0, Items: fortyItems()},
		EdgeCase: evalgen.Dimension{Name: "edge_case", Target: 1, Achieved: 1,
			Items: []evalgen.Item{{ID: "empty_input", Kind: "edge_case", Covered: true}}},
	}
	// A set-quality report that is itself perfectly happy — which is what made the clobber invisible.
	q := evalgen.SetQuality{NCases: 53, Difficulty: 0.4, DifficultyMeasured: true,
		Diversity: 0.8, OracleCoverage: 1, NOracle: 53, NGold: 53, LowConfidence: false}

	v := Build(Input{WorkflowID: "wf", Profile: scoring.Balanced(), Coverage: &rep, Quality: &q})

	if !v.Coverage.LowConfidence {
		t.Fatal("a not-measurable path axis and a zero-covered node axis must force low-confidence")
	}
	var sawVacuous, sawZero bool
	for _, r := range v.Coverage.Reasons {
		if containsSub(r, "could not be measured") {
			sawVacuous = true
		}
		if containsSub(r, "0 of 40") {
			sawZero = true
		}
	}
	if !sawVacuous {
		t.Fatalf("the not-measurable axis must be explained, got %v", v.Coverage.Reasons)
	}
	if !sawZero {
		t.Fatalf("the zero-covered axis must be explained, got %v", v.Coverage.Reasons)
	}
	// And the vacuous axis renders as "not measurable", never as a met target.
	for _, d := range v.Coverage.Dimensions {
		if d.Name == "path" && (!d.Vacuous || d.Met) {
			t.Fatalf("a vacuous axis must be flagged and never met: %+v", d)
		}
	}
}

func fortyItems() []evalgen.Item {
	out := make([]evalgen.Item, 0, 40)
	for i := 0; i < 40; i++ {
		out = append(out, evalgen.Item{ID: "n_" + itoa(i), Kind: "node"})
	}
	return out
}

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
