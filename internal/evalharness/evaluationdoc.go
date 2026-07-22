package evalharness

import (
	"fmt"
	"strings"
)

// evaluationdoc.go renders the evaluator table that ships in
// openspec/changes/p4-eval-harness/evaluation.md (task 1.7).
//
// The table is GENERATED from the registry rather than hand-written, and a test asserts the shipped
// document contains exactly this rendering. A prose table cannot be kept in sync by intention: the
// failure mode is not a missing row, it is a row that stayed behind after the code moved — an
// evaluator whose published range or admissible-pattern list no longer matches what it enforces.
// That is worse than no document, because a reader has no way to tell.

// EvaluationTableMarker delimits the generated region inside evaluation.md.
const (
	EvaluationTableBegin = "<!-- BEGIN GENERATED: evaluator table (internal/evalharness) -->"
	EvaluationTableEnd   = "<!-- END GENERATED -->"
)

// EvaluationTable renders every registered evaluator with its metric, output range, admissible
// patterns and calibration status, in stable name order.
func EvaluationTable(r *Registry) string {
	var b strings.Builder
	b.WriteString("| Evaluator | Metric | Range | Admissible patterns | Calibration |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, d := range r.Describe() {
		pats := "any (pattern-agnostic)"
		if len(d.Patterns) > 0 {
			names := make([]string, 0, len(d.Patterns))
			for _, p := range d.Patterns {
				names = append(names, string(p))
			}
			pats = strings.Join(names, ", ")
		}
		cal := "n/a (deterministic oracle)"
		if d.Judge {
			cal = "uncalibrated — barred from gating"
			if d.Calibrated {
				cal = "calibrated — see judge_calibration"
			}
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | [%g, %g] | %s | %s |\n",
			d.Name, d.Metric, d.Range.Min, d.Range.Max, pats, cal)
	}
	return b.String()
}
