package attribution

import (
	"context"
	"math"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// seriesWithMean builds a deterministic success series centered on `mean` over 10 cases × the given
// seeds, with a small fixed per-(case,seed) jitter so the bootstrap has real variance to measure. No
// randomness: the same inputs yield the same series, so the ablation test is reproducible.
func seriesWithMean(variant, metric string, mean float64, seeds []int64) evalstats.Series {
	s := evalstats.Series{VariantID: variant, ConfigHash: "cfg", Metric: metric}
	for ci := 0; ci < 10; ci++ {
		caseID := "case-" + string(rune('a'+ci))
		for _, seed := range seeds {
			jitter := 0.02 * math.Sin(float64(ci*7+int(seed)*3))
			v := mean + jitter
			if v < 0 {
				v = 0
			}
			if v > 1 {
				v = 1
			}
			s.Obs = append(s.Obs, evalstats.Observation{CaseID: caseID, Seed: seed, Value: v})
		}
	}
	return s
}

// fakeRunner is the stub AblationRunner. It records what it was asked to run so the test can assert
// the ablated run was an ephemeral measurement variant, and it has NO method that persists a variant
// — the interface cannot express a mutation, which is the property under test.
type fakeRunner struct {
	baselineMean float64
	// ablatedMean maps a node id to the mean the swap produces. A faulty node's swap lifts the metric
	// out of the noise band; a non-faulty node's swap leaves it inside.
	ablatedMean map[string]float64

	baselineCalls int
	ablatedNodes  []string
}

func (r *fakeRunner) Baseline(_ context.Context, v Variant, metric string, seeds []int64) (evalstats.Series, error) {
	r.baselineCalls++
	return seriesWithMean(v.VariantID, metric, r.baselineMean, seeds), nil
}

func (r *fakeRunner) Ablated(_ context.Context, v Variant, node, _ string, metric string, seeds []int64) (evalstats.Series, error) {
	r.ablatedNodes = append(r.ablatedNodes, node)
	mean := r.baselineMean
	if m, ok := r.ablatedMean[node]; ok {
		mean = m
	}
	// The ephemeral variant id is derived, never a user variant id, and it is never handed to a
	// persistence path — this fake has none.
	return seriesWithMean("ablation:"+v.VariantID+":"+node, metric, mean, seeds), nil
}

func fiveSeeds() []int64 { return []int64{0, 1, 2, 3, 4} }

// Task 3.5 (first half): swapping the FAULTY node's config → non-overlapping-zero delta names it the
// bottleneck.
func TestAblate_FaultyNodeIsBottleneck(t *testing.T) {
	runner := &fakeRunner{
		baselineMean: 0.30,
		ablatedMean:  map[string]float64{faultyNodeID: 0.90}, // large, real improvement
	}
	cfg := AblationConfig{
		Metric:    "task_success",
		Direction: evalstats.HigherIsBetter,
		Seeds:     fiveSeeds(),
		Stats:     evalstats.DefaultConfig(),
	}
	res, err := Ablate(context.Background(), runner, testVariant(), faultyNodeID, "cfg-ref-fixed", cfg)
	if err != nil {
		t.Fatalf("Ablate: %v", err)
	}
	if res.Verdict != VerdictBottleneck {
		t.Fatalf("verdict = %q, want bottleneck (delta CI [%.4f,%.4f])", res.Verdict, res.CILow, res.CIHigh)
	}
	// A bottleneck's delta CI must exclude zero.
	if res.CILow <= 0 && res.CIHigh >= 0 {
		t.Errorf("bottleneck delta CI [%.4f,%.4f] overlaps zero", res.CILow, res.CIHigh)
	}
	if !res.Ephemeral {
		t.Errorf("ablation result must be marked ephemeral")
	}
	if res.NodeID != faultyNodeID || res.SwappedConfigRef != "cfg-ref-fixed" {
		t.Errorf("result mis-keyed: %+v", res)
	}
}

// Task 3.5 (second half): swapping a NON-faulty node → inconclusive (CI overlaps zero).
func TestAblate_NonFaultyNodeIsInconclusive(t *testing.T) {
	runner := &fakeRunner{
		baselineMean: 0.50,
		ablatedMean:  map[string]float64{"router": 0.505}, // inside the noise band
	}
	cfg := AblationConfig{
		Metric:    "task_success",
		Direction: evalstats.HigherIsBetter,
		Seeds:     fiveSeeds(),
		Stats:     evalstats.DefaultConfig(),
	}
	res, err := Ablate(context.Background(), runner, testVariant(), "router", "cfg-ref-fixed", cfg)
	if err != nil {
		t.Fatalf("Ablate: %v", err)
	}
	if res.Verdict != VerdictInconclusive {
		t.Fatalf("verdict = %q, want inconclusive (delta CI [%.4f,%.4f])", res.Verdict, res.CILow, res.CIHigh)
	}
	// An inconclusive delta CI overlaps zero.
	if res.CILow > 0 || res.CIHigh < 0 {
		t.Errorf("inconclusive delta CI [%.4f,%.4f] should overlap zero", res.CILow, res.CIHigh)
	}
}

// Task 3.2: under-seeding is refused — an under-seeded CI understates variance and manufactures a
// false bottleneck.
func TestAblate_RefusesUnderSeeding(t *testing.T) {
	runner := &fakeRunner{baselineMean: 0.3, ablatedMean: map[string]float64{faultyNodeID: 0.9}}
	cfg := AblationConfig{
		Metric: "task_success", Direction: evalstats.HigherIsBetter,
		Seeds: []int64{0, 1, 2}, // below the floor of 5
		Stats: evalstats.DefaultConfig(),
	}
	if _, err := Ablate(context.Background(), runner, testVariant(), faultyNodeID, "ref", cfg); err == nil {
		t.Fatalf("expected under-seeding to be refused")
	}
}

// Task 3.4: the ablation candidate set is seeded from the per-node contribution ranking — the
// first-divergence node ranks first.
func TestCandidateNodes_SeededFromContributionRanking(t *testing.T) {
	ir := faultIR()
	contrib := Attribute(ir, testVariant(), []FailingCase{faultyCase("c1"), faultyCase("c2")})
	cands := CandidateNodes(contrib, 2)
	if len(cands) == 0 || cands[0] != faultyNodeID {
		t.Fatalf("candidate ranking = %v, want %q first", cands, faultyNodeID)
	}
}
