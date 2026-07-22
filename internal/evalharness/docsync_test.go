package evalharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DOC↔CODE DRIFT FENCE (task 1.7).
//
// evaluation.md publishes every evaluator's range, admissible patterns and calibration status. A
// prose table cannot be kept in sync by intention — the failure mode is not a missing row, it is a
// row that stayed behind after the code moved, so the document confidently states a range the code
// no longer enforces. This test parses the SHIPPED document and asserts its generated region IS the
// registry's rendering, which fails if either side moves without the other.

const evaluationDocPath = "../../openspec/changes/p4-eval-harness/evaluation.md"

func readEvaluationDoc(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(evaluationDocPath)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", evaluationDocPath, err)
	}
	return string(b)
}

func TestEvaluationDocMatchesTheRegistry(t *testing.T) {
	doc := readEvaluationDoc(t)
	begin := strings.Index(doc, EvaluationTableBegin)
	end := strings.Index(doc, EvaluationTableEnd)
	if begin < 0 || end < 0 || end < begin {
		t.Fatalf("evaluation.md is missing the generated-table markers %q / %q",
			EvaluationTableBegin, EvaluationTableEnd)
	}
	got := strings.TrimSpace(doc[begin+len(EvaluationTableBegin) : end])
	want := strings.TrimSpace(EvaluationTable(NewBuiltinRegistry()))
	if got != want {
		t.Fatalf("evaluation.md's evaluator table has drifted from the registry.\n--- doc ---\n%s\n--- code ---\n%s", got, want)
	}
}

// Every metric name the package defines must be documented. A metric that exists in code and not in
// the catalogue is a number that appears on a board with nothing explaining what it means.
func TestEveryStandardMetricIsDocumented(t *testing.T) {
	doc := readEvaluationDoc(t)
	for _, m := range append(append([]string{}, StandardFamily...), ContributionFamily...) {
		if !strings.Contains(doc, "`"+m+"`") {
			t.Fatalf("metric %q is computed by the harness but absent from evaluation.md", m)
		}
	}
}

// The gate-eligibility rule is the one sentence that must never be softened by a doc edit that the
// code does not follow, so the predicate itself is asserted here beside the document.
func TestGateEligibilityPredicate(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   JudgeStanding
		want bool
	}{
		{"calibrated above floor", JudgeStanding{Agreement: 0.7, NHuman: 40, Floor: 0.6, Calibrated: true}, true},
		{"calibrated at floor", JudgeStanding{Agreement: 0.6, NHuman: 40, Floor: 0.6, Calibrated: true}, true},
		{"below floor", JudgeStanding{Agreement: 0.5, NHuman: 40, Floor: 0.6, Calibrated: true}, false},
		{"uncalibrated", JudgeStanding{Agreement: 0.9, NHuman: 40, Floor: 0.6}, false},
		{"no human labels", JudgeStanding{Agreement: 0.9, NHuman: 0, Floor: 0.6, Calibrated: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.st.GateEligible(); got != tc.want {
				t.Fatalf("GateEligible: want %v got %v for %+v", tc.want, got, tc.st)
			}
			if !tc.want && tc.st.Reason() == "" {
				t.Fatal("an ineligible judge must explain itself")
			}
		})
	}
}
