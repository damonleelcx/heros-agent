package assessment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/herosagent"
)

// p36_budget_exhausted_test.go is P36 §5.3: a ceiling reached INSIDE the agent degrades the axis to
// `not_measured` with `budget_exhausted` — the state P33 already defines — rather than to a generic
// inference failure.
//
// # Why this needs a fence
//
// The runner's ordinary treatment of an inference failure is to leave the STRUCTURAL finding in place
// and warn: stage 1's answer is still true, and losing eight good findings because a ninth provider
// call timed out is the wrong trade. That is right for an OUTAGE and wrong for a CEILING.
//
// The difference is what the report claims. A ceiling that falls through to the outage branch produces
// a report that renders as COMPLETE and simply found nothing on that axis — an absence rendered as a
// measurement, with `Assessment.Partial()` returning false. Nothing goes red; a reader is told the
// platform looked and saw nothing, when in fact it stopped paying.

// capRefusingInference returns the given error for one axis and answers normally for the rest.
type capRefusingInference struct {
	failOn Axis
	err    error
	calls  int
}

func (c *capRefusingInference) Infer(_ context.Context, axis Axis, s Subject) (Finding, float64, error) {
	c.calls++
	if axis == c.failOn {
		return Finding{}, 0, c.err
	}
	f, err := Inferred(axis, "an inferred claim about "+string(axis), s.Evidence(),
		"model-v1", "sha256:"+string(axis))
	return f, 0.01, err
}

func TestACeilingInsideTheAgentDegradesToBudgetExhausted(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
	}{
		{"the agent's own tenant/fleet ceiling", fmt.Errorf("wrapped: %w", herosagent.ErrCapReached)},
		{"a seam that knows this package", fmt.Errorf("wrapped: %w", ErrInferenceBudgetExhausted)},
	} {
		t.Run(c.name, func(t *testing.T) {
			// 🔴 The axis is DISCOVERED rather than named. Stage 2 only runs on axes stage 1 left
			// `not_measured`, and which those are is a property of the fixture — naming one that the
			// python extractors happen to answer structurally would make this test assert nothing while
			// looking like it asserts everything.
			// 🔴 The LAST inferable axis, so every earlier one is genuinely inferred first. Failing on
			// the first would make the anti-vacuity check below unsatisfiable for a correct
			// implementation: once a ceiling is reached the runner degrades every REMAINING axis too,
			// which is right — having stopped paying, we have stopped paying — so the axes that prove
			// the report is not simply blank have to come BEFORE it.
			axis := lastInferableAxis(t)
			inf := &capRefusingInference{failOn: axis, err: c.err}
			a := runWithInference(t, inf)

			f := findingForAxis(t, a, axis)
			if f.State() != StateNotMeasured {
				t.Fatalf("the axis is %q, want %q", f.State(), StateNotMeasured)
			}
			if f.MissingInput() != MissingBudgetExhausted {
				t.Errorf("the missing input is %q, want %q. A ceiling that falls through to the outage "+
					"branch leaves the STRUCTURAL finding in place and the report renders as complete — "+
					"an absence rendered as a measurement.", f.MissingInput(), MissingBudgetExhausted)
			}
			if !strings.Contains(strings.ToLower(f.Claim()), "ceiling") {
				t.Errorf("the claim does not say a ceiling stopped it: %q", f.Claim())
			}
			// 🔴 The REPORT says it is incomplete. This is the reader-facing consequence and the reason
			// the state matters at all.
			if !a.Partial() {
				t.Error("the assessment does not report itself as partial. `Partial()` is what tells a " +
					"reader the report is incomplete rather than clean, and a ceiling is exactly the " +
					"case it exists for.")
			}

			// 🔴 ANTI-VACUITY: the OTHER axes are still answered. A ceiling must not blank the report —
			// "the report shrinks in state, never in axis count".
			if len(a.Findings) != len(Axes()) {
				t.Errorf("the report carries %d findings; there are %d axes", len(a.Findings), len(Axes()))
			}
			inferredElsewhere := 0
			for _, other := range a.Findings {
				if other.Axis() != axis && other.Origin() == OriginInferred {
					inferredElsewhere++
				}
			}
			if inferredElsewhere == 0 {
				t.Error("no other axis was inferred, so the ceiling assertion above passed over a " +
					"report where nothing was measured at all")
			}
		})
	}
}

// 🚫 The counter-case: an ORDINARY inference failure must still leave the structural finding standing.
// Without this, a fence that degraded everything to `budget_exhausted` would pass the test above and
// report every provider outage as a spend ceiling.
func TestAnOrdinaryInferenceFailureIsNotReportedAsABudgetCeiling(t *testing.T) {
	axis := lastInferableAxis(t)
	inf := &capRefusingInference{failOn: axis, err: errors.New("the provider timed out")}
	a := runWithInference(t, inf)
	f := findingForAxis(t, a, axis)
	if f.MissingInput() == MissingBudgetExhausted {
		t.Error("a provider timeout was reported as a spend ceiling. The two send a reader to two " +
			"different places — one is a bill and the other is an outage — and conflating them is the " +
			"same substitution this file exists to prevent, in the other direction.")
	}
	if a.Partial() {
		t.Error("a provider timeout made the report `partial`, which claims we stopped paying")
	}
}

// runWithInference runs one assessment over a real fixture with the given inference seam.
func runWithInference(t *testing.T, inf Inference) Assessment {
	t.Helper()
	tick := int64(0)
	r, err := NewRunner(&memStore{}, allResolve{}, inf, func() int64 { tick++; return tick },
		slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	a, err := r.Run(context.Background(), cfg(), subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return a
}

func findingForAxis(t *testing.T, a Assessment, axis Axis) Finding {
	t.Helper()
	for _, f := range a.Findings {
		if f.Axis() == axis {
			return f
		}
	}
	t.Fatalf("the report carries no finding for %s", axis)
	return Finding{}
}

// lastInferableAxis is the final axis this fixture leaves `not_measured` after the structural stage —
// the only kind stage 2 ever reaches, in the order the runner walks them.
//
// Discovered by running with NO inference seam, which is exactly the state stage 1 leaves behind.
// Naming an axis instead would make these fences assert nothing on the day an extractor learns to
// answer it structurally — and they would keep passing.
func lastInferableAxis(t *testing.T) Axis {
	t.Helper()
	a := runWithInference(t, nil)
	var found []Axis
	for _, want := range Axes() {
		for _, f := range a.Findings {
			if f.Axis() == want && f.State() == StateNotMeasured && f.Origin() != OriginInferred {
				found = append(found, want)
			}
		}
	}
	if len(found) < 2 {
		t.Fatalf("this fixture leaves %d axis/axes inferable, and these fences need at least two: one "+
			"to be answered and one to be stopped by the ceiling. With fewer, `Partial()` and \"the "+
			"other axes are still answered\" cannot both be exercised.", len(found))
	}
	return found[len(found)-1]
}
