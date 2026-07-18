package telemetry

import (
	"fmt"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/metricevent"
)

// variantHash gives each variant its own 1:1 config_hash, so config_hash is low-card (one per variant)
// and does not multiply series beyond variant_id — the realistic snapshot the budget assumes.
func variantHash(v int) string {
	s := fmt.Sprintf("%064x", v+1)
	return s[:64]
}

// Task 3.3: a run over 200 cases keeps active series ≈ 3×10⁴ (variant×node×metric×seed), NOT multiplied
// by case_id. Proven by projecting a full label space to series keys and counting distinct — case_id is
// not a label, so the count is independent of how many cases the events span.
func TestSection3_SeriesBudgetIsUnaffectedByCaseCount(t *testing.T) {
	const (
		variants = 20
		nodes    = 20
		metrics  = 15
		seeds    = 5
		cases    = 5 // events span 5 cases; a real run spans ~200. The property is the same either way.
	)
	seriesNoCase := map[string]struct{}{}
	seriesIfCaseWereALabel := map[string]struct{}{}

	for v := 0; v < variants; v++ {
		for n := 0; n < nodes; n++ {
			for m := 0; m < metrics; m++ {
				for s := 0; s < seeds; s++ {
					for c := 0; c < cases; c++ {
						seed := int64(s)
						ev := metricevent.Event{
							VariantID:  fmt.Sprintf("variant_%d", v),
							NodeID:     fmt.Sprintf("node_%d", n),
							RunID:      "run_1",
							CaseID:     fmt.Sprintf("case_%d", c),
							Seed:       &seed,
							ConfigHash: variantHash(v),
							MetricName: fmt.Sprintf("metric_%d", m),
						}
						seriesNoCase[SeriesKey(ev)] = struct{}{}
						seriesIfCaseWereALabel[SeriesKey(ev)+"|case="+ev.CaseID] = struct{}{}
					}
				}
			}
		}
	}

	const wantBudget = variants * nodes * metrics * seeds // 3×10⁴
	if len(seriesNoCase) != wantBudget {
		t.Errorf("active series = %d, want %d (variant×node×metric×seed); case_id must not multiply it",
			len(seriesNoCase), wantBudget)
	}
	// Had case_id been a label, series would be `cases`× larger — the explosion the discipline forbids.
	if len(seriesIfCaseWereALabel) != wantBudget*cases {
		t.Errorf("sanity: case-as-label series = %d, want %d", len(seriesIfCaseWereALabel), wantBudget*cases)
	}
	t.Logf("active series with the label discipline: %d (~3×10⁴); with case_id as a label it would be %d",
		len(seriesNoCase), len(seriesIfCaseWereALabel))
}

// Task 3.1 / 3.2: the projected label set contains ONLY the low-card tags, and NONE of the
// high-cardinality identifiers — but they are retained as exemplars.
func TestSection3_LabelsAreLowCardOnlyAndHighCardIsRetained(t *testing.T) {
	seed := int64(2)
	ev := metricevent.Event{
		VariantID: "v1", NodeID: "n_a", RunID: "run_xyz", CaseID: "case_42", Seed: &seed,
		ConfigHash: testConfigHash, MetricName: MetricCostUSD,
		Dimensions: map[string]any{AttrInvocationID: "run_xyz:n_a:0"},
	}
	labels := SeriesLabels(ev)

	// Only the five low-card tags are labels.
	if len(labels) != len(SeriesLabelTags) {
		t.Errorf("label set has %d keys, want %d: %v", len(labels), len(SeriesLabelTags), labels)
	}
	for _, high := range HighCardinalityTags {
		if _, isLabel := labels[high]; isLabel {
			t.Errorf("high-cardinality tag %q leaked into TSDB labels", high)
		}
	}

	// But high-cardinality ids are retained as exemplars — present and queryable, just not labels.
	ex := Exemplars(ev)
	if ex[AttrCaseID] != "case_42" || ex[AttrRunID] != "run_xyz" || ex[AttrInvocationID] != "run_xyz:n_a:0" {
		t.Errorf("high-cardinality ids were not retained as exemplars: %v", ex)
	}
}

var _ = time.Now
