package assessment

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// holdout.go is §9.5's first obligation: **findings produced by a model reading source are a
// classifier's output, and a classifier without a holdout is an opinion.**
//
// # 🔴 The metric, and why the usual one is wrong here
//
// The usual metric is accuracy, and it would destroy FR10. A model that abstains when it cannot tell
// scores WORSE on accuracy than one that guesses, because a guess is right some of the time and an
// abstention is never "right". So the discipline the whole phase depends on — *"returns `not_measured`
// with a named missing input; it does not return a low-confidence conclusion"* — would be punished by
// its own evaluation and would erode within two model upgrades.
//
// So the report is THREE numbers per axis, and it never combines them:
//
//	precision  of the answers it GAVE, how many were right
//	abstention of the cases it was shown, how many it declined
//	wrong      the answers it gave that were wrong — the only number that is a failure
//
// An abstention on a case whose ground truth is `cannot be determined` is a SUCCESS and is counted as
// one. An abstention on a case that has an answer is not a failure either: it is a MISS, reported
// separately, because the reader's position is unchanged and the platform has not asserted anything
// false. Only a WRONG ANSWER is a failure, and it is the number that gets the alarm.
//
// # 🚫 There is deliberately no overall figure
//
// §9.5: *"an aggregate over axes hides the one axis that is broken."* `Report` has no mean, and
// `holdout_test.go` fences the absence.
//
// # ⚠️ What this file does NOT do
//
// It does not contain a model. `Run` takes an `Inference`, and what that inference is decides whether
// a run measures a model or measures a stub. Running it against a stub measures the HARNESS — that
// abstention is scored as a success, that precision is per-axis, that a wrong answer is caught — and
// nothing at all about a provider. See the header of `holdout_test.go`, which says so where a reader
// of the numbers will be standing.

// Truth is what a holdout case's correct outcome is.
type Truth string

const (
	// TruthConclusive — this axis CAN be determined from this repository, and the expected claim is
	// recorded. A conclusive answer that matches is a success; an abstention is a miss.
	TruthConclusive Truth = "conclusive"
	// TruthUndeterminable — this axis CANNOT be determined from this repository.
	//
	// 🔴 The set MUST contain these (task 3.4), and they are the most important cases in it. Without
	// them the holdout cannot distinguish a model that knows when to stop from one that always
	// answers — and "always answers" is the failure mode this phase's whole design is arranged around.
	TruthUndeterminable Truth = "undeterminable"
)

// Case is one holdout entry: a repository, an axis, and the correct outcome.
type Case struct {
	// Fixture names a repository under `internal/discovery/testdata/fixtures`. A REAL tree parsed by a
	// REAL frontend (task 7.11) — an inline fixture would test the model against a tree no customer
	// has, and against a frontend nobody ran.
	Fixture string `json:"fixture"`
	Axis    Axis   `json:"axis"`
	Truth   Truth  `json:"truth"`
	// Expect is what a correct conclusive answer must CONTAIN, lower-cased. A substring rather than an
	// exact sentence: the claim is prose and grading prose by equality measures phrasing.
	//
	// 🔴 It must be a term the answer cannot contain by accident. `TestEveryExpectationIsDiscriminating`
	// refuses one shorter than four characters, because a one-word expectation is a coin flip dressed
	// as a grade — the shape that turns a scorer into a rubber stamp.
	Expect string `json:"expect,omitempty"`
	// Why records what makes this the ground truth, so a reader of a failing case can tell a wrong
	// model from a wrong label. A holdout nobody can audit is a holdout that drifts to whatever the
	// model does.
	Why string `json:"why"`
}

// AxisResult is one axis's three numbers.
type AxisResult struct {
	Axis Axis `json:"axis"`
	// Cases is how many holdout entries this axis has. Reported because a precision over two cases and
	// a precision over two hundred are different claims and the float cannot say which.
	Cases int `json:"cases"`

	Answered  int `json:"answered"`
	Correct   int `json:"correct"`
	Wrong     int `json:"wrong"`
	Abstained int `json:"abstained"`
	// AbstainedCorrectly is abstentions on cases whose truth is `undeterminable` — the successes of the
	// abstention discipline.
	AbstainedCorrectly int `json:"abstained_correctly"`
	// Missed is abstentions on cases that DID have an answer. Not a failure; a shortfall.
	Missed int `json:"missed"`
	// Failed is a case the inference could not be run for at all.
	Failed int `json:"failed"`
}

// Precision is correct answers over answers GIVEN. It is -1 when the axis answered nothing.
//
// 🔴 -1 rather than 0, and rather than a float somebody has to remember is meaningless. A precision of
// 0.0 says "everything it said was wrong"; an axis that said nothing has no precision at all, and
// rendering the two alike reports a catastrophe where there is silence.
func (s AxisResult) Precision() float64 {
	if s.Answered == 0 {
		return -1
	}
	return float64(s.Correct) / float64(s.Answered)
}

// AbstentionRate is abstentions over cases shown.
func (s AxisResult) AbstentionRate() float64 {
	if s.Cases == 0 {
		return -1
	}
	return float64(s.Abstained) / float64(s.Cases)
}

// Report is the holdout result.
//
// 🚫 It has NO overall accuracy, no mean and no total score, and `TestTheHoldoutReportHasNoOverallNumber`
// keeps it that way. §9.5: an aggregate over axes hides the one axis that is broken.
type Report struct {
	// PerAxis is one result per axis, in report order. Axes with no holdout case are PRESENT with zero cases,
	// because "we never tested this axis" is the finding a reader most needs and it is invisible in a
	// list that only shows what was tested.
	PerAxis []AxisResult `json:"per_axis"`
	// UntestedAxes names the axes with no case at all. Derived from PerAxis, carried separately so it
	// cannot be missed in a table of nine rows.
	UntestedAxes []Axis `json:"untested_axes,omitempty"`
}

// LoadCases reads a holdout file.
func LoadCases(path string) ([]Case, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("assessment: reading the holdout set: %w", err)
	}
	var doc struct {
		Cases []Case `json:"cases"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("assessment: parsing the holdout set: %w", err)
	}
	if len(doc.Cases) == 0 {
		return nil, fmt.Errorf("assessment: the holdout set at %s is empty, so any report from it is a "+
			"claim about nothing", path)
	}
	return doc.Cases, nil
}

// SubjectLoader turns a fixture name into a Subject. Injected so this file needs no knowledge of where
// fixtures live or how discovery is invoked — and so a future holdout over CUSTOMER repositories needs
// no change here.
type SubjectLoader func(fixture string) (Subject, error)

// Run scores an inference against a holdout set.
//
// It never fails the whole run for one case: a case that cannot be loaded or inferred is counted as
// `Failed` for its axis and the rest continue. A holdout that aborts on the first problem reports
// nothing about the other eight axes, which is the opposite of what it is for.
func Run(ctx context.Context, inf Inference, cases []Case, load SubjectLoader) (Report, error) {
	if inf == nil {
		return Report{}, ErrNoModel
	}
	if load == nil {
		return Report{}, fmt.Errorf("assessment: a holdout run needs a way to load a fixture")
	}
	byAxis := map[Axis]*AxisResult{}
	for _, axis := range Axes() {
		byAxis[axis] = &AxisResult{Axis: axis}
	}

	for _, c := range cases {
		result, ok := byAxis[c.Axis]
		if !ok {
			return Report{}, fmt.Errorf("assessment: the holdout names axis %q, which is not one of the nine", c.Axis)
		}
		result.Cases++

		s, err := load(c.Fixture)
		if err != nil {
			result.Failed++
			continue
		}
		f, _, err := inf.Infer(ctx, c.Axis, s)
		if err != nil {
			result.Failed++
			continue
		}
		judge(result, c, f)
	}

	out := Report{}
	for _, axis := range Axes() {
		s := *byAxis[axis]
		out.PerAxis = append(out.PerAxis, s)
		if s.Cases == 0 {
			out.UntestedAxes = append(out.UntestedAxes, axis)
		}
	}
	sort.Slice(out.UntestedAxes, func(i, j int) bool { return axisOrder(out.UntestedAxes[i]) < axisOrder(out.UntestedAxes[j]) })
	return out, nil
}

// judge scores one case. The whole of §3.5's rule lives in these fifteen lines.
func judge(result *AxisResult, c Case, f Finding) {
	abstained := f.State() == StateNotMeasured
	if abstained {
		result.Abstained++
		switch c.Truth {
		case TruthUndeterminable:
			// 🔴 A SUCCESS. The model was shown a repository where this axis genuinely cannot be
			// determined and it said so. This is the counter that makes FR10 survive contact with an
			// evaluation, and it is why there is no accuracy figure anywhere in this file.
			result.AbstainedCorrectly++
		default:
			result.Missed++
		}
		return
	}

	result.Answered++
	switch {
	case c.Truth == TruthUndeterminable:
		// 🔴 The WORST outcome in the set, and the reason `undeterminable` cases must exist. The model
		// asserted something about a repository that cannot support the assertion — a confident
		// sentence with nothing under it, which is exactly what a reader cannot detect.
		result.Wrong++
	case containsFold(f.Claim(), c.Expect):
		result.Correct++
	default:
		result.Wrong++
	}
}

func containsFold(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	h, n := []rune(lower(haystack)), []rune(lower(needle))
	if len(n) > len(h) {
		return false
	}
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			if h[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func lower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + ('a' - 'A')
		}
	}
	return string(out)
}
