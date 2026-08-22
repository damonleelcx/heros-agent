package assessment

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalboard"
	"github.com/heros-foreal/agentd/internal/evalharness"
)

// measure_test.go covers §4: the four reasons, decisiveness travelling with the score, the enumerable
// cases, and the set that cannot fail.

// ── Doubles ──────────────────────────────────────────────────────────────────────────────────────

type sandbox struct {
	admits bool
	why    string
	err    error
}

func (s sandbox) Admits(context.Context, string) (bool, string, error) { return s.admits, s.why, s.err }

type creds struct{ missing []string }

func (c creds) Missing(context.Context, []string) ([]string, error) { return c.missing, nil }

// stubEval returns a fixed set. It measures NOTHING about a harness — it exists so the decisiveness
// plumbing above it can be driven through every shape a real set can take.
type stubEval struct {
	axes  []Axis
	score Interval
	cv    evalboard.CoverageView
	cases []evalharness.Case
	err   error
}

func (s stubEval) MeasurableAxes() []Axis { return s.axes }

func (s stubEval) Run(context.Context, Axis, Subject) (Interval, evalboard.CoverageView, []evalharness.Case, string, error) {
	return s.score, s.cv, s.cases, "set-stub", s.err
}

// decisiveCase and indecisiveCase are REAL `evalharness.Case` values, so the oracle verdict comes from
// the product's own probe rather than from a look-alike this test wrote.
func decisiveCase(id string) evalharness.Case {
	return evalharness.Case{CaseID: id, Suite: "s", Reference: []byte(`"the expected answer"`)}
}

func indecisiveCase(id string) evalharness.Case {
	// An output schema that constrains nothing: `{}` accepts every JSON value, so the oracle can never
	// return "no". This is `NIndecisive`'s definition, produced rather than asserted.
	return evalharness.Case{CaseID: id, Suite: "s", OutputSchema: []byte(`{}`)}
}

// ── The four reasons (task 4.4) ──────────────────────────────────────────────────────────────────

// TestTheFourReasonsStayFourAndReadDifferently is `eval-set-decisiveness`'s last requirement.
//
// 🔴 The assertion is not that four exist. It is that each renders a DISTINCT message and that each
// message names a different next action — because a reader does four different things about them and
// one apologetic sentence tells them to do none.
func TestTheFourReasonsStayFourAndReadDifferently(t *testing.T) {
	seen := map[string]MissingInput{}
	for _, reason := range EvalMissingInputs() {
		r := Runnability{Reason: reason, Detail: "the specific thing"}
		claim := r.Claim()
		if strings.TrimSpace(claim) == "" {
			t.Fatalf("%s renders no message, so it would reach a reader as a bare state name", reason)
		}
		if prior, dup := seen[claim]; dup {
			t.Fatalf("%s and %s render the SAME message: %q", reason, prior, claim)
		}
		seen[claim] = reason
		if !strings.Contains(claim, "the specific thing") {
			t.Fatalf("%s drops the detail, so a category reaches the reader where a task should: %q", reason, claim)
		}
	}
	if len(EvalMissingInputs()) != 4 {
		t.Fatalf("there are %d eval reasons, want exactly 4", len(EvalMissingInputs()))
	}
}

// TestRunnabilityChecksTheFourInOrder is why `AssessRunnability` has a fixed order: a reader must be
// told the thing they must fix FIRST, not the thing we happened to check first.
func TestRunnabilityChecksTheFourInOrder(t *testing.T) {
	base := subjectFor(t, "python")

	t.Run("unsupported language wins", func(t *testing.T) {
		s := base
		s.Report.Frontends = nil // nothing contributed
		got, err := AssessRunnability(context.Background(), s, sandbox{}, creds{missing: []string{"openai"}})
		if err != nil {
			t.Fatal(err)
		}
		if got.Reason != MissingLanguageSupport {
			t.Fatalf("got %q; with no runner for the language, telling a customer to supply a credential "+
				"sends them to fix something that would not help", got.Reason)
		}
	})

	t.Run("sandbox refusal beats a missing credential", func(t *testing.T) {
		got, err := AssessRunnability(context.Background(), base,
			sandbox{admits: false, why: "the workflow needs network egress"},
			creds{missing: []string{"openai"}})
		if err != nil {
			t.Fatal(err)
		}
		if got.Reason != MissingSandboxRefusal {
			t.Fatalf("got %q, want %q — our limit is named before we ask them for anything",
				got.Reason, MissingSandboxRefusal)
		}
		if !strings.Contains(got.Detail, "egress") {
			t.Fatalf("the sandbox's own reason was dropped: %q", got.Detail)
		}
	})

	t.Run("a missing credential is last and is named", func(t *testing.T) {
		got, err := AssessRunnability(context.Background(), base, sandbox{admits: true},
			creds{missing: []string{"openai"}})
		if err != nil {
			t.Fatal(err)
		}
		if got.Reason != MissingCredential || !strings.Contains(got.Detail, "openai") {
			t.Fatalf("got %+v, want a missing-credential reason naming openai", got)
		}
	})

	t.Run("everything in place is runnable", func(t *testing.T) {
		got, err := AssessRunnability(context.Background(), base, sandbox{admits: true}, creds{})
		if err != nil {
			t.Fatal(err)
		}
		if !got.Runnable {
			t.Fatalf("a workflow with a sandbox and credentials is not runnable: %+v", got)
		}
	})
}

// TestNoSandboxIsARefusalNotASuccess is task 6.3's teeth. "Assessment executes customer code under the
// sandbox, not beside it" stops being true the moment a nil sandbox reads as permission.
func TestNoSandboxIsARefusalNotASuccess(t *testing.T) {
	got, err := AssessRunnability(context.Background(), subjectFor(t, "python"), nil, creds{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Runnable {
		t.Fatal("a deployment with NO sandbox reported the workflow runnable — customer code would then " +
			"be executed beside the posture rather than under it")
	}
	if got.Reason != MissingSandboxRefusal {
		t.Fatalf("got %q, want %q", got.Reason, MissingSandboxRefusal)
	}
}

// ── Decisiveness (tasks 4.2, 4.3, 4.5) ───────────────────────────────────────────────────────────

func measurementFor(t *testing.T, cases []evalharness.Case, cv evalboard.CoverageView) *Measurement {
	t.Helper()
	m, err := NewMeasurement(stubEval{
		axes:  []Axis{AxisPrompt},
		score: Interval{Mean: 0.94, Low: 0.90, High: 0.98, NSeeds: 5},
		cv:    cv,
		cases: cases,
	}, sandbox{admits: true}, creds{})
	if err != nil {
		t.Fatalf("NewMeasurement: %v", err)
	}
	return m
}

// TestASetThatCannotFailSaysSoBesideItsScore is task 4.5 and acceptance A5, and it is the failure this
// whole capability exists for: a generated set whose oracles cannot fail scores 1.0.
func TestASetThatCannotFailSaysSoBesideItsScore(t *testing.T) {
	cases := []evalharness.Case{indecisiveCase("c1"), indecisiveCase("c2"), indecisiveCase("c3")}
	m := measurementFor(t, cases, evalboard.CoverageView{
		Measured: true, NCases: 3, OracleCoverage: 0, NIndecisive: 3,
	})
	f, err := m.Measure(context.Background(), AxisPrompt, subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if f.State() != StateMeasured {
		t.Fatalf("got state %s, want measured", f.State())
	}
	if !f.Eval().CannotFail() {
		t.Fatal("a set of three indecisive oracles does not report that it cannot fail")
	}
	if !strings.Contains(f.Claim(), "never fail") {
		t.Fatalf("the CLAIM — the sentence somebody pastes into a message — does not say the set cannot "+
			"fail: %q", f.Claim())
	}
	if !strings.Contains(f.Claim(), "not evidence of quality") {
		t.Fatalf("the claim reports the number without withdrawing it: %q", f.Claim())
	}
}

// TestDecisivenessTravelsWithEveryScore is task 4.2 — beside the number, not behind a link.
func TestDecisivenessTravelsWithEveryScore(t *testing.T) {
	cases := []evalharness.Case{decisiveCase("c1"), decisiveCase("c2"), indecisiveCase("c3")}
	m := measurementFor(t, cases, evalboard.CoverageView{
		Measured: true, NCases: 3, OracleCoverage: 2.0 / 3.0, NIndecisive: 1,
		Dimensions: []evalboard.DimensionView{{Name: "path", Vacuous: true}, {Name: "node"}},
	})
	f, err := m.Measure(context.Background(), AxisPrompt, subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	rep := f.Eval()
	if rep.NCases != 3 || rep.NIndecisive != 1 {
		t.Fatalf("the report carries %d cases and %d indecisive, want 3 and 1", rep.NCases, rep.NIndecisive)
	}
	if len(rep.VacuousDimensions) != 1 || rep.VacuousDimensions[0] != "path" {
		t.Fatalf("the vacuous dimension is not NAMED: %v. \"1 axis not measurable\" tells a reader "+
			"nothing they can act on", rep.VacuousDimensions)
	}
	if !strings.Contains(f.Claim(), "never fail") {
		t.Fatalf("the claim does not mention the indecisive case: %q", f.Claim())
	}
}

// TestCasesAreEnumerableNotOnlyCounted is task 4.3, FR13, and P30's named gap: *"a reader cannot answer
// the only question that matters: 8 cases of what?"*
func TestCasesAreEnumerableNotOnlyCounted(t *testing.T) {
	cases := []evalharness.Case{decisiveCase("c1"), indecisiveCase("c2")}
	m := measurementFor(t, cases, evalboard.CoverageView{Measured: true, NCases: 2, OracleCoverage: 0.5, NIndecisive: 1})
	f, err := m.Measure(context.Background(), AxisPrompt, subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	got := f.Eval().Cases
	if len(got) != 2 {
		t.Fatalf("the report enumerates %d cases, want 2", len(got))
	}
	for _, c := range got {
		if c.Oracle.Kind == "" {
			t.Fatalf("case %s does not say what decided it", c.CaseID)
		}
		if !c.CanFail() && c.Oracle.Reason == "" {
			t.Fatalf("case %s cannot fail and does not say why — a count without a reason is not a task",
				c.CaseID)
		}
	}
	if got[0].CanFail() == got[1].CanFail() {
		t.Fatal("both enumerated cases report the same decisiveness; the probe is not being run per case")
	}
}

// TestUnmeasuredCoverageIsStatedRatherThanRenderedAsZero is `evalboard.CostLatencyAnalysis`'s lesson,
// applied: 0.0 is a number somebody measured, and "we do not have this" is a different fact.
func TestUnmeasuredCoverageIsStatedRatherThanRenderedAsZero(t *testing.T) {
	cases := []evalharness.Case{decisiveCase("c1")}
	m := measurementFor(t, cases, evalboard.CoverageView{Measured: false, NCases: 1})
	f, err := m.Measure(context.Background(), AxisPrompt, subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if f.Eval().CoverageMeasured {
		t.Fatal("an unmeasured coverage pass reports itself measured")
	}
	if !strings.Contains(f.Claim(), "not measured") {
		t.Fatalf("the claim does not say coverage is unknown, so 0%% would read as measured-and-zero: %q",
			f.Claim())
	}
}

// TestAScoreWithNoSeedCountIsRefused is §7.1: no number is reported without the size of the set behind
// it. The refusal happens at construction, so such a finding cannot exist.
func TestAScoreWithNoSeedCountIsRefused(t *testing.T) {
	m, err := NewMeasurement(stubEval{
		axes:  []Axis{AxisPrompt},
		score: Interval{Mean: 0.9, Low: 0.8, High: 1.0}, // NSeeds unset
		cv:    evalboard.CoverageView{Measured: true, NCases: 1},
		cases: []evalharness.Case{decisiveCase("c1")},
	}, sandbox{admits: true}, creds{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Measure(context.Background(), AxisPrompt, subjectFor(t, "python")); err == nil ||
		!strings.Contains(err.Error(), "seed count") {
		t.Fatalf("a score with no seed count was accepted: %v", err)
	}
}

// ── The runner's application rule ────────────────────────────────────────────────────────────────

// TestMeasurementNeverDowngradesAnObservation is the rule that keeps a report from getting WORSE for
// having tried to measure.
func TestMeasurementNeverDowngradesAnObservation(t *testing.T) {
	store := &memStore{}
	tick := int64(0)
	r, err := NewRunner(store, allResolve{}, nil, func() int64 { tick++; return tick }, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	// A measurer that can address `model` — which structural extraction ANSWERS on the python fixture
	// — but whose sandbox refuses. It will return `not_measured`.
	m, err := NewMeasurement(stubEval{axes: []Axis{AxisModel}}, sandbox{admits: false, why: "no egress"}, creds{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.WithMeasurement(m).Run(context.Background(), cfg(), subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range a.Findings {
		if f.Axis() != AxisModel {
			continue
		}
		if f.State() == StateNotMeasured {
			t.Fatalf("a failed measurement overwrote a structural observation. The report is now WORSE "+
				"for having tried: a true claim the reader could check was replaced by a failure of "+
				"ours.\n  claim: %q", f.Claim())
		}
	}
}

// TestMeasurementUpgradesANotMeasuredAxis is the other direction — the rule must not be "never
// change anything".
func TestMeasurementUpgradesANotMeasuredAxis(t *testing.T) {
	store := &memStore{}
	tick := int64(0)
	r, err := NewRunner(store, allResolve{}, nil, func() int64 { tick++; return tick }, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	// `memory` is `not_measured` structurally on every repository, so a measurement is strictly
	// stronger.
	m := measurementFor(t, []evalharness.Case{decisiveCase("c1")},
		evalboard.CoverageView{Measured: true, NCases: 1, OracleCoverage: 1})
	m.run = stubEval{
		axes:  []Axis{AxisMemory},
		score: Interval{Mean: 0.81, Low: 0.74, High: 0.88, NSeeds: 5},
		cv:    evalboard.CoverageView{Measured: true, NCases: 1, OracleCoverage: 1},
		cases: []evalharness.Case{decisiveCase("c1")},
	}
	a, err := r.WithMeasurement(m).Run(context.Background(), cfg(), subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range a.Findings {
		if f.Axis() == AxisMemory && f.State() != StateMeasured {
			t.Fatalf("memory is %s after a successful measurement, want measured", f.State())
		}
	}
}

// TestAFailedEvalLeavesTheExistingFindingStanding — the rollout plan's rule, third occurrence.
func TestAFailedEvalLeavesTheExistingFindingStanding(t *testing.T) {
	store := &memStore{}
	tick := int64(0)
	r, err := NewRunner(store, allResolve{}, nil, func() int64 { tick++; return tick }, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMeasurement(stubEval{axes: []Axis{AxisModel}, err: errors.New("the harness died")},
		sandbox{admits: true}, creds{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.WithMeasurement(m).Run(context.Background(), cfg(), subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("a dead eval harness took down the whole assessment: %v", err)
	}
	if len(a.Findings) != len(Axes()) {
		t.Fatalf("got %d findings, want %d", len(a.Findings), len(Axes()))
	}
}

// TestAMeasurerThatDeclaresNoAxesChangesNothing is the default posture: stages 1 and 2 measure nothing,
// and a runner with a measurer attached but nothing declared must behave exactly like one without.
func TestAMeasurerThatDeclaresNoAxesChangesNothing(t *testing.T) {
	subject := subjectFor(t, "python")
	build := func(m Measurer) Assessment {
		t.Helper()
		tick := int64(0)
		r, err := NewRunner(&memStore{}, allResolve{}, nil, func() int64 { tick++; return tick },
			slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		if m != nil {
			r = r.WithMeasurement(m)
		}
		a, err := r.Run(context.Background(), cfg(), subject)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	silent, err := NewMeasurement(stubEval{axes: nil}, sandbox{admits: true}, creds{})
	if err != nil {
		t.Fatal(err)
	}
	without, with := build(nil), build(silent)
	for i := range without.Findings {
		if without.Ordered()[i] != with.Ordered()[i] {
			t.Fatalf("a measurer declaring no axes changed finding %d", i)
		}
	}
}
