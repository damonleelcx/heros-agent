package proposal

import (
	"fmt"
	"sort"
	"strings"
)

// PER-AXIS PASS RATE — the honest measurement of a new operator (P34 task 6.3, PRD §9.5)
// ─────────────────────────────────────────────────────────────────────────────────────
//
// # The failure this exists to prevent
//
// P34 adds two operators, which is two new SEARCH SPACES. The tempting way to report on them is a
// single number: "the proposal engine's candidates pass verification 62% of the time." PRD §9.5 refuses
// it in as many words — *"an aggregate hides the single-sample defect"* — and the arithmetic is why:
//
//	model      120 candidates, 70% pass  ─┐
//	prompt      80 candidates, 60% pass   ├─  aggregate: 61%. Healthy.
//	graph        6 candidates,  5% pass  ─┘
//
// The graph operator is not working, and nothing in that 61% says so. It never will, either: the
// operator with the smallest sample is the newest one, so the number that would reveal a broken new
// operator is the number an aggregate is least sensitive to.
//
// 🔴 So this file computes PER-AXIS rates and refuses to compute a mean across axes. `Overall()` does
// not exist, and its absence is the design: a caller that wanted one would have to write the loop, and
// writing it is a decision somebody makes rather than a default they inherit.
//
// # What "the axis" means here
//
// The AXIS, not the operator. Three operators can land on `prompt` and a reader asking "does the prompt
// axis work" wants one answer, while an operator whose candidates all refuse at transform is a
// different question (`transform.AxisCoverage()` answers that one). Every candidate declares its
// dimensions, so the axis is read from the candidate rather than inferred from the operator's name.

// AxisPassRate is one axis's rate through the P5.5 verification gate.
type AxisPassRate struct {
	// Axis is the dimension label the candidates declared — "loop", "graph", "model", …
	Axis string `json:"axis"`
	// Verified is how many of this axis's candidates reached a terminal verdict. It is the DENOMINATOR,
	// and it is published because a rate over three candidates and a rate over three hundred are
	// different facts that the same percentage would present identically.
	Verified int `json:"verified"`
	// Passed is how many passed.
	Passed int `json:"passed"`
	// Rate is Passed/Verified, or -1 when nothing has been verified.
	//
	// 🔴 -1, never 0. A rate over zero candidates is UNDEFINED, and 0.0 would tell a reader "we measured
	// this axis and it never works" about an axis nobody has measured — the same distinction
	// `assessment.Health.AllNotMeasuredRate` draws and for the same reason.
	Rate float64 `json:"rate"`
}

// Measured reports whether this axis has a rate at all.
func (r AxisPassRate) Measured() bool { return r.Verified > 0 }

// PassRateOutcome is one candidate's terminal result, in the minimal shape this computation needs.
//
// 🔴 It takes the AXES and a BOOLEAN rather than a `verification.Verdict`, and the decoupling is
// deliberate: `internal/verification` imports the eval runner and, through it, the sandbox, and a
// package computing a ratio must not drag a process isolator behind it. The caller — which already
// holds both — does the projection.
type PassRateOutcome struct {
	// Axes are the dimensions the candidate declared. A candidate touching two axes counts toward both,
	// because "does the loop axis work" is a question about every candidate that changed a loop.
	Axes []string
	// Passed is whether the verification gate passed it.
	Passed bool
}

// PassRatesByAxis computes each axis's rate, sorted by axis name.
//
// 🚫 It returns no aggregate, and adding one would defeat the file. See the package comment.
func PassRatesByAxis(outcomes []PassRateOutcome) []AxisPassRate {
	agg := map[string]*AxisPassRate{}
	for _, o := range outcomes {
		for _, axis := range o.Axes {
			if axis == "" {
				continue
			}
			r, ok := agg[axis]
			if !ok {
				r = &AxisPassRate{Axis: axis}
				agg[axis] = r
			}
			r.Verified++
			if o.Passed {
				r.Passed++
			}
		}
	}
	out := make([]AxisPassRate, 0, len(agg))
	for _, r := range agg {
		r.Rate = -1
		if r.Verified > 0 {
			r.Rate = float64(r.Passed) / float64(r.Verified)
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Axis < out[j].Axis })
	return out
}

// UnderperformingAxes names the axes whose rate is below `floor`, with enough evidence to say so,
// sorted worst-first. This is the read PRD §9.5 asks for: "a graph operator with a 5% pass rate hidden
// inside a healthy average is an operator that is not working."
//
// `minVerified` is the evidence bar. An axis with two candidates and one failure is at 50% and means
// nothing; reporting it would train a reader to ignore this list, which costs more than the report is
// worth.
func UnderperformingAxes(rates []AxisPassRate, floor float64, minVerified int) []AxisPassRate {
	var out []AxisPassRate
	for _, r := range rates {
		if r.Verified < minVerified || !r.Measured() {
			continue
		}
		if r.Rate < floor {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rate != out[j].Rate {
			return out[i].Rate < out[j].Rate
		}
		return out[i].Axis < out[j].Axis
	})
	return out
}

// FormatPassRates renders the per-axis table for a report or a log line.
//
// 🔴 It prints the DENOMINATOR beside every rate. "graph 5%" and "graph 5% (1/20)" are the same number
// and different evidence, and a reader deciding whether to act on a low rate needs the second.
func FormatPassRates(rates []AxisPassRate) string {
	var b strings.Builder
	for _, r := range rates {
		if !r.Measured() {
			fmt.Fprintf(&b, "%-10s  not measured\n", r.Axis)
			continue
		}
		fmt.Fprintf(&b, "%-10s  %5.1f%%  (%d/%d)\n", r.Axis, 100*r.Rate, r.Passed, r.Verified)
	}
	return b.String()
}
