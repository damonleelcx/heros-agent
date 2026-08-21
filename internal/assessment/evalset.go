package assessment

import (
	"errors"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/evalboard"
	"github.com/heros-foreal/agentd/internal/evalharness"
)

// evalset.go is the `eval-set-decisiveness` capability: the properties that say HOW TO READ a score,
// carried on the same object as the score so they cannot be separated from it.
//
// # The failure this closes, in one sentence
//
// A generated eval set whose oracles can never fail scores 1.0, and today nothing would say so.
//
// # 🔴 Why this is not "render CoverageView"
//
// `evalboard.CoverageView` already computes `OracleCoverage`, `NIndecisive` and the vacuous-dimension
// list, and P30 recorded that *"none of it reaches a screen that shows which cases"*. Rendering the
// view would fix the first half and leave the second: a reader sees `n=5 seeds · 8 cases` and *"cannot
// answer the only question that matters: 8 cases of what?"*. So this type carries the CASES, each with
// its oracle and whether that oracle can fail, and `FromCoverage` reads the aggregate numbers from
// `CoverageView` rather than recomputing them — one source for the counts, one new thing (the list).

// Interval is a measured value with its confidence interval and the number of seeds behind it.
//
// 🔴 There is no constructor that omits NSeeds. §7.1: no number is reported without the size of the
// set behind it, and a field a caller may leave at zero is a field a caller leaves at zero.
type Interval struct {
	Mean   float64 `json:"mean"`
	Low    float64 `json:"ci_low"`
	High   float64 `json:"ci_high"`
	NSeeds int     `json:"n_seeds"`
}

// SingleSeed reports whether this result came from one seed. The console labels it as such — an
// interval from one seed is a range around a single observation, and reading it as a spread over
// repeated runs is the mistake the label exists to prevent.
func (i Interval) SingleSeed() bool { return i.NSeeds == 1 }

// Overlaps reports whether two intervals overlap.
func (i Interval) Overlaps(other Interval) bool { return i.Low <= other.High && other.Low <= i.High }

// Tie reports whether two measured results must be reported as a TIE rather than as an ordering.
//
// A free function rather than a method, because the answer is symmetric and a method reads as though
// one of the two owns the verdict. It exists at all so that "overlapping intervals are a tie" is
// implemented once: every place that compares two numbers in this product has, historically, been a
// place that eventually ranked them.
func Tie(a, b Interval) bool { return a.Overlaps(b) }

// OracleKind and its reason come from `evalharness.OracleVerdict`, read rather than retyped: the
// question "can this oracle ever return no" is answered by probing the oracle, and a second
// implementation of that probe would be a second answer to a question with one correct one.

// CaseView is one enumerated case (FR13, task 4.3).
type CaseView struct {
	CaseID string `json:"case_id"`
	// Suite groups cases the way the generator grouped them, so a list of forty is readable.
	Suite string `json:"suite,omitempty"`
	// Oracle is the verdict from `evalharness.Case.DecisiveOracle` — its kind, whether it is decisive,
	// and when it is not, WHY. The reason is what turns "3 cases cannot fail" into a task.
	Oracle evalharness.OracleVerdict `json:"oracle"`
}

// CanFail reports whether this case's oracle can ever return "no".
func (c CaseView) CanFail() bool { return c.Oracle.Decisive }

// EvalSetReport travels with every measured finding.
type EvalSetReport struct {
	// EvalSetHash identifies the set, so a reader comparing two assessments can tell whether the same
	// questions were asked.
	EvalSetHash string `json:"eval_set_hash"`
	// Score is the measured result with its interval and seed count.
	Score Interval `json:"score"`

	NCases int `json:"n_cases"`
	// OracleCoverage is the fraction of cases whose oracle can actually return NO.
	OracleCoverage float64 `json:"oracle_coverage"`
	// NIndecisive counts cases carrying an oracle that can never fail.
	NIndecisive int `json:"n_indecisive"`
	// CoverageMeasured says whether oracle coverage was measured AT ALL.
	//
	// 🔴 A separate boolean rather than a sentinel value in OracleCoverage, for the reason
	// `evalboard.CostLatencyAnalysis` records the hard way: `0.0` is a number somebody measured, and
	// "we do not have this" is a different fact. A console that cannot tell them apart renders an
	// unmeasured set as a set with no decisive oracles, which is a much more alarming claim than the
	// truth and is made with the truth's confidence.
	CoverageMeasured bool `json:"coverage_measured"`

	// VacuousDimensions names coverage axes that no case exercises — the axes on which this set is
	// silent. Named, not counted: "nothing to measure" is not "everything covered".
	VacuousDimensions []string `json:"vacuous_dimensions,omitempty"`

	// Cases is the enumeration. 🔴 It is REQUIRED to be non-empty whenever NCases > 0, and
	// `Validate` enforces it: a report that counts eight cases and lists none is the exact gap P30
	// named, wearing a fix.
	Cases []CaseView `json:"cases"`
}

// CannotFail reports whether EVERY case in this set carries an oracle that can never fail.
//
// When true the console states that the set cannot fail and does not present the score as evidence of
// quality. It is computed from the case list rather than from `NIndecisive == NCases` so that the
// claim and the enumeration behind it cannot disagree.
func (r EvalSetReport) CannotFail() bool {
	if len(r.Cases) == 0 {
		return false
	}
	for _, c := range r.Cases {
		if c.CanFail() {
			return false
		}
	}
	return true
}

// Validate is the report's own rule set.
func (r EvalSetReport) Validate() error {
	if r.NCases < 0 {
		return errors.New("assessment: an eval-set report counts a negative number of cases")
	}
	if r.NCases > 0 && len(r.Cases) == 0 {
		return fmt.Errorf("assessment: the eval-set report counts %d cases and enumerates none — "+
			"a count is not a case list (FR13)", r.NCases)
	}
	if len(r.Cases) > 0 && len(r.Cases) != r.NCases {
		return fmt.Errorf("assessment: the eval-set report counts %d cases and enumerates %d",
			r.NCases, len(r.Cases))
	}
	if r.Score.NSeeds < 1 {
		return errors.New("assessment: a measured score reports no seed count, so the size of the " +
			"set behind it is unstated (§7.1)")
	}
	if r.Score.Low > r.Score.High {
		return fmt.Errorf("assessment: the interval [%g, %g] is inverted", r.Score.Low, r.Score.High)
	}
	for _, c := range r.Cases {
		if c.CaseID == "" {
			return errors.New("assessment: an enumerated case has no id")
		}
		if c.Oracle.Kind == "" {
			return fmt.Errorf("assessment: case %s names no oracle kind, so a reader cannot tell "+
				"what decided it", c.CaseID)
		}
		if !c.Oracle.Decisive && c.Oracle.Reason == "" {
			return fmt.Errorf("assessment: case %s carries an oracle that cannot fail and does not "+
				"say why", c.CaseID)
		}
	}
	if !r.CoverageMeasured && (r.OracleCoverage != 0 || r.NIndecisive != 0) {
		return errors.New("assessment: coverage was not measured, yet the report carries coverage numbers")
	}
	return nil
}

// FromCoverage builds a report from the board's own coverage view and the eval set's cases.
//
// 🔴 The aggregate numbers are COPIED from `CoverageView`, never recomputed here. Recomputing them
// would make this a second answer to "how decisive is this set", and the two would drift in exactly
// the circumstances where the answer matters. What this function ADDS is the enumeration, which no
// existing type carries.
func FromCoverage(hash string, score Interval, cv evalboard.CoverageView, cases []evalharness.Case) EvalSetReport {
	out := EvalSetReport{
		EvalSetHash:      hash,
		Score:            score,
		NCases:           cv.NCases,
		OracleCoverage:   cv.OracleCoverage,
		NIndecisive:      cv.NIndecisive,
		CoverageMeasured: cv.Measured,
		Cases:            []CaseView{},
	}
	if !cv.Measured {
		// Do not carry numbers computed from an unmeasured coverage pass. `CoverageView` leaves them
		// at their zero values in that case, and shipping a zero is the failure the flag exists for.
		out.OracleCoverage, out.NIndecisive = 0, 0
	}
	for _, d := range cv.Dimensions {
		if d.Vacuous {
			out.VacuousDimensions = append(out.VacuousDimensions, d.Name)
		}
	}
	sort.Strings(out.VacuousDimensions)

	for _, c := range cases {
		out.Cases = append(out.Cases, CaseView{CaseID: c.CaseID, Suite: c.Suite, Oracle: c.DecisiveOracle()})
	}
	sort.Slice(out.Cases, func(i, j int) bool { return out.Cases[i].CaseID < out.Cases[j].CaseID })

	// NCases comes from the coverage view, which is the set's own count. When the caller hands over
	// the cases too, the enumeration is authoritative for its own length — a mismatch is a bug, and
	// `Validate` reports it rather than this function papering over it.
	if cv.NCases == 0 && len(out.Cases) > 0 {
		out.NCases = len(out.Cases)
	}
	return out
}
