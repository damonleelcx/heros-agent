package improvementrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/optimizer"
)

// plan_test.go proves §2: a question becomes a bounded plan, an untranslatable one is refused, and the
// bounds the plan declares are the bounds the shipped loop enforces.

func okBounds() Bounds {
	return Bounds{
		TenantID: "ten_1", WorkflowID: "wf_1", SourceRevision: "abc123def456",
		Origin: OriginConsole, MaxCandidates: 8, MaxSpendUSD: 4.00, NowMS: 1_700_000_000_000,
	}
}

// ── 2.1 the plan carries every field the requirement names ───────────────────────────────────────

func TestPlanCarriesScopeCapBudgetAndStoppingCondition(t *testing.T) {
	p, err := Translate("improve my memory strategy and open a pull request", okBounds())
	if err != nil {
		t.Fatalf("a well-formed question was refused: %v", err)
	}
	if p.WorkflowID != "wf_1" || p.SourceRevision != "abc123def456" {
		t.Fatalf("the plan lost its subject: %+v", p)
	}
	if p.CandidateCap != 8 || p.SpendBudgetUSD != 4.00 {
		t.Fatalf("the plan lost a bound: cap=%d budget=%.2f", p.CandidateCap, p.SpendBudgetUSD)
	}
	if p.Stopping.MinImprovement <= 0 {
		t.Fatal("the plan declares no stopping condition; a run under it would chase gains inside the noise")
	}
	if len(p.Axes) != 1 || p.Axes[0] != assessment.AxisMemory {
		t.Fatalf("a question naming one axis produced scope %v; a nine-axis run would spend money on "+
			"eight surfaces nobody asked about", p.Axes)
	}
	if p.PlanID == "" {
		t.Fatal("the plan has no id, so an acknowledgement has nothing to bind to")
	}
}

func TestAQuestionNamingNoAxisScopesToAllNine(t *testing.T) {
	p, err := Translate("fix it, and open a pull request", okBounds())
	if err != nil {
		t.Fatalf("the canonical improve question was refused: %v", err)
	}
	if len(p.Axes) != len(assessment.Axes()) {
		t.Fatalf("got %d axes, want all %d. A question that names no surface asks about every surface; "+
			"narrowing it silently would hide the axis the answer was on", len(p.Axes), len(assessment.Axes()))
	}
}

func TestPlanIDIsDeterministicAndMovesWhenABoundMoves(t *testing.T) {
	a, err := Translate("fix it", okBounds())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Translate("fix it", okBounds())
	if err != nil {
		t.Fatal(err)
	}
	if a.PlanID != b.PlanID {
		t.Fatal("the same question over the same bounds produced two plan ids; an acknowledgement could " +
			"then never be found for the plan it was given for")
	}
	bigger := okBounds()
	bigger.MaxSpendUSD = 40.00
	c, err := Translate("fix it", bigger)
	if err != nil {
		t.Fatal(err)
	}
	if c.PlanID == a.PlanID {
		t.Fatal("a plan with ten times the budget shares an id with the cheap one — an acknowledgement " +
			"of the $4 plan would authorize the $40 one")
	}
}

// ── 2.3 an untranslatable question is REFUSED, never run with defaults ────────────────────────────

func TestAnUnboundedQuestionIsRefusedRatherThanBounded(t *testing.T) {
	_, err := Translate("keep improving it with no limit until it is perfect", okBounds())
	var ref *Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("a question asking for an unbounded run produced %v, not a refusal. Running it with "+
			"default bounds is how a sentence spends a month's allowance", err)
	}
	if ref.Cause != RefusedUnboundedRequested {
		t.Fatalf("cause = %q, want %q", ref.Cause, RefusedUnboundedRequested)
	}
	if ref.NextAction == "" {
		t.Fatal("the refusal names no next action, which is the shape of refusal that teaches nobody anything")
	}
}

func TestEveryRefusalCauseHasItsOwnSentenceAndANextAction(t *testing.T) {
	cases := map[RefusalCause]struct {
		question string
		bounds   Bounds
	}{
		RefusedUnboundedRequested: {"improve it with no limit", okBounds()},
		RefusedMultipleSubjects:   {"improve all my repositories", okBounds()},
		RefusedUnknownAxis:        {"improve my retriever", okBounds()},
		RefusedNoSubject:          {"fix it", boundsWithout("workflow")},
		RefusedNoSourceRevision:   {"fix it", boundsWithout("revision")},
		RefusedNoBudget:           {"fix it", boundsWithout("budget")},
	}
	if len(cases) != len(RefusalCauses()) {
		t.Fatalf("the closed set has %d causes and this test drives %d; a cause with no case is a "+
			"sentence nobody has read", len(RefusalCauses()), len(cases))
	}
	seen := map[string]RefusalCause{}
	for want, tc := range cases {
		_, err := Translate(tc.question, tc.bounds)
		var ref *Refusal
		if !errors.As(err, &ref) {
			t.Fatalf("%s: got %v, want a refusal", want, err)
		}
		if ref.Cause != want {
			t.Fatalf("%q produced cause %q, want %q", tc.question, ref.Cause, want)
		}
		if ref.Detail == "" || ref.NextAction == "" {
			t.Fatalf("%s carries an empty detail or next action", want)
		}
		if prior, dup := seen[ref.Detail]; dup {
			t.Fatalf("%s and %s render the SAME sentence; two causes a reader cannot tell apart are "+
				"one cause with two names", want, prior)
		}
		seen[ref.Detail] = want
	}
}

func boundsWithout(field string) Bounds {
	b := okBounds()
	switch field {
	case "workflow":
		b.WorkflowID = ""
	case "revision":
		b.SourceRevision = ""
	case "budget":
		b.MaxCandidates, b.MaxSpendUSD = 0, 0
	}
	return b
}

func TestAZeroBoundIsRefusedNotTreatedAsUnbounded(t *testing.T) {
	p := Plan{
		TenantID: "t", WorkflowID: "w", SourceRevision: "r", Origin: OriginConsole,
		Axes: []assessment.Axis{assessment.AxisModel},
	}
	err := p.Validate()
	if err == nil {
		t.Fatal("a plan with every bound at zero validated. A zero bound either stops instantly or runs " +
			"unbounded, and which one it does is decided by a `<= 0` somewhere nobody is looking at")
	}
	for _, want := range []string{"candidate_cap", "spend_budget_usd", "min_improvement"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %q, so somebody has to read code to learn which bound "+
				"is missing: %v", want, err)
		}
	}
}

func TestAxisMarkersCoverEveryAxis(t *testing.T) {
	covered := map[assessment.Axis]bool{}
	for _, axes := range AxisMarkers() {
		for _, a := range axes {
			covered[a] = true
		}
	}
	for _, a := range assessment.Axes() {
		if !covered[a] {
			t.Fatalf("no word a person would type reaches the %q axis, so a question about it silently "+
				"widens to all nine — which spends the budget on eight surfaces nobody asked about", a)
		}
	}
}

// ── 2.2 the disclosure threshold ─────────────────────────────────────────────────────────────────

func TestAPlanAboveTheThresholdDoesNotRunUntilItIsAcknowledged(t *testing.T) {
	b := okBounds()
	b.MaxCandidates, b.MaxSpendUSD = 40, 50.00 // projects to 40 * 0.25 = $10, above the threshold
	p, err := Translate("fix it", b)
	if err != nil {
		t.Fatal(err)
	}
	if !p.RequiresAcknowledgement() {
		t.Fatalf("a plan projected at $%.2f does not require acknowledgement against a $%.2f threshold",
			p.ProjectedSpendUSD, DisclosureThresholdUSD)
	}
	store := NewMemAckStore()
	if err := RequireAcknowledgement(context.Background(), store, p); !errors.Is(err, ErrAwaitingAcknowledgement) {
		t.Fatalf("an unacknowledged plan above the threshold was admitted: %v", err)
	}
	if err := store.Record(context.Background(), Acknowledgement{
		PlanID: p.PlanID, TenantID: p.TenantID, By: "person@example.com",
		ProjectedSpendUSD: p.ProjectedSpendUSD, AtMS: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := RequireAcknowledgement(context.Background(), store, p); err != nil {
		t.Fatalf("an acknowledged plan was still withheld: %v", err)
	}
}

func TestAPlanBelowTheThresholdRunsWithoutAnAcknowledgement(t *testing.T) {
	b := okBounds()
	b.MaxCandidates, b.MaxSpendUSD = 2, 0.50
	p, err := Translate("fix it", b)
	if err != nil {
		t.Fatal(err)
	}
	if p.RequiresAcknowledgement() {
		t.Fatalf("a $%.2f plan asks for an acknowledgement; a threshold that fires on everything is a "+
			"click people learn to make without reading", p.ProjectedSpendUSD)
	}
	if err := RequireAcknowledgement(context.Background(), nil, p); err != nil {
		t.Fatalf("a below-threshold plan was withheld: %v", err)
	}
}

func TestAnUnreadableAcknowledgementStoreWithholdsTheRun(t *testing.T) {
	b := okBounds()
	b.MaxCandidates, b.MaxSpendUSD = 40, 50.00
	p, _ := Translate("fix it", b)
	err := RequireAcknowledgement(context.Background(), unreadableAckStore{}, p)
	if !errors.Is(err, ErrAwaitingAcknowledgement) {
		t.Fatalf("an unreadable acknowledgement store admitted the run (%v). Refusing an acknowledged "+
			"run wastes a click; admitting an unacknowledged one spends money nobody agreed to", err)
	}
}

type unreadableAckStore struct{}

func (unreadableAckStore) Record(context.Context, Acknowledgement) error { return nil }
func (unreadableAckStore) Acknowledged(context.Context, string, string) (Acknowledgement, bool, error) {
	return Acknowledgement{}, false, errors.New("the store is unreachable")
}

func TestAnAcknowledgementMustNameThePersonWhoGaveIt(t *testing.T) {
	err := NewMemAckStore().Record(context.Background(), Acknowledgement{PlanID: "p", TenantID: "t"})
	if err == nil {
		t.Fatal("an acknowledgement naming nobody was recorded. A row that says a plan was acknowledged " +
			"and cannot say by whom is worse than no row, because it is believed")
	}
}

// ── 2.4 the plan's bounds are the SHIPPED loop's bounds ──────────────────────────────────────────

func TestPlanBoundsProjectOntoTheOptimizersOwnConstraints(t *testing.T) {
	p, err := Translate("fix it", okBounds())
	if err != nil {
		t.Fatal(err)
	}
	c := p.Constraints()
	if c.BudgetCeilingUSD != p.SpendBudgetUSD {
		t.Fatalf("the spend budget did not reach the loop: %v vs %v", c.BudgetCeilingUSD, p.SpendBudgetUSD)
	}
	if c.MaxIterations != p.CandidateCap {
		t.Fatalf("the candidate cap did not reach the loop: %v vs %v", c.MaxIterations, p.CandidateCap)
	}
	if c.MinImprovement != p.Stopping.MinImprovement {
		t.Fatalf("the stopping condition did not reach the loop: %v vs %v", c.MinImprovement, p.Stopping.MinImprovement)
	}
	if c.BlindSubBudgetUSD != 0 {
		t.Fatal("blind expansion is enabled. A person who agreed to a budget for a named scope did not " +
			"agree to a grid sweep over everything else")
	}
}

// TestOneIterationConsumesOneCandidate is the assertion `Plan.Constraints` relies on rather than
// asserts in a comment: the cap→MaxIterations mapping is EXACT only while the loop consumes exactly one
// candidate per iteration. If that changes, the cap silently stops being a cap.
func TestOneIterationConsumesOneCandidate(t *testing.T) {
	cap := 3
	enum := &countingEnumerator{perTarget: 5}
	be := NewBoundedEnumerator(Plan{
		Axes: []assessment.Axis{assessment.AxisModel}, CandidateCap: cap,
		SpendBudgetUSD: 1, Stopping: StoppingCondition{MinImprovement: 0.01},
	}, enum)

	res := runLoop(t, be, cap)
	if be.Admitted() != cap {
		t.Fatalf("the enumerator admitted %d distinct candidates under a cap of %d; it had %d on offer",
			be.Admitted(), cap, 5)
	}
	// 🔴 THE ASSERTION. Every admitted candidate must have been consumed by exactly one iteration. Fewer
	// iterations than admitted candidates means the loop ended early — which is the shape of the defect
	// a cumulative-emission cap produced: the run reports a normal terminal state having tried one thing.
	if len(res.Iterations) != be.Admitted() {
		t.Fatalf("the loop ran %d iterations over %d admitted candidates (terminal state %q, reason %q). "+
			"One iteration must consume exactly one candidate, or Plan.Constraints' cap→MaxIterations "+
			"mapping is not a cap at all",
			len(res.Iterations), be.Admitted(), res.State, res.StopReason)
	}
}

func TestTheBoundedEnumeratorDropsOutOfScopeCandidates(t *testing.T) {
	be := NewBoundedEnumerator(Plan{
		Axes: []assessment.Axis{assessment.AxisMemory}, CandidateCap: 10,
		SpendBudgetUSD: 1, Stopping: StoppingCondition{MinImprovement: 0.01},
	}, &countingEnumerator{perTarget: 3})

	got := be.Enumerate(optimizer.Target{Node: "n1", Dimension: string(assessment.AxisModel)})
	if len(got) != 0 {
		t.Fatalf("a target on the model axis produced %d candidates under a memory-only plan; the person "+
			"agreed to spend on memory", len(got))
	}
	if be.OutOfScope() == 0 {
		t.Fatal("the out-of-scope count is zero, so \"the scope excluded everything\" is indistinguishable " +
			"from \"the operators produced nothing\" — opposite findings")
	}
	got = be.Enumerate(optimizer.Target{Node: "n2", Dimension: string(assessment.AxisMemory)})
	if len(got) != 3 {
		t.Fatalf("an in-scope target produced %d candidates, want 3", len(got))
	}
	// Re-enumerating must not double-count: the loop asks again on every iteration.
	if again := be.Enumerate(optimizer.Target{Node: "n2", Dimension: string(assessment.AxisMemory)}); len(again) != 3 {
		t.Fatalf("a second enumeration returned %d candidates, want the same 3 re-offered. Withholding "+
			"already-admitted candidates starves the loop, which then reports the search space as exhausted", len(again))
	}
	if be.PerAxis()[string(assessment.AxisMemory)] != 3 {
		t.Fatalf("the per-axis breakdown is %v; an axis truncated by the cap and an axis that produced "+
			"nothing must not read the same", be.PerAxis())
	}
}

func TestAnOutOfScopeTargetNeverReachesTheDelegate(t *testing.T) {
	enum := &countingEnumerator{perTarget: 2}
	be := NewBoundedEnumerator(Plan{
		Axes: []assessment.Axis{assessment.AxisMemory}, CandidateCap: 10,
		SpendBudgetUSD: 1, Stopping: StoppingCondition{MinImprovement: 0.01},
	}, enum)
	be.Enumerate(optimizer.Target{Node: "n1", Dimension: string(assessment.AxisGraph)})
	if enum.calls != 0 {
		t.Fatal("the delegate was asked to enumerate an out-of-scope target. Filtering only the OUTPUT " +
			"lets a production enumerator do work — possibly a provider call — on an axis nobody asked about")
	}
}

// ── 2.5 which bound stopped the run ──────────────────────────────────────────────────────────────

func TestEachBoundReportsItsOwnSentence(t *testing.T) {
	seen := map[string]Bound{}
	for _, b := range BoundsSet() {
		s := b.Sentence()
		if s == "" {
			t.Fatalf("bound %q renders no sentence", b)
		}
		if prior, dup := seen[s]; dup {
			t.Fatalf("%q and %q render the SAME sentence; a person cannot tell a converged run from a "+
				"truncated one, so they keep raising a budget that was never the constraint", b, prior)
		}
		seen[s] = b
	}
}

func TestOutcomeMapsEveryTerminalStateToABoundOrAFaultButNeverBoth(t *testing.T) {
	cases := []struct {
		state     optimizer.RunState
		killed    bool
		wantBound Bound
		wantFault bool
	}{
		{optimizer.StateHaltedBudget, false, BoundBudget, false},
		{optimizer.StateMaxIter, false, BoundCandidateCap, false},
		{optimizer.StateConverged, false, BoundStoppingCondition, false},
		{optimizer.StateStalled, false, BoundStoppingCondition, false},
		{optimizer.StateHaltedRegression, false, BoundStoppingCondition, false},
		{optimizer.StateStopped, true, BoundKillSwitch, false},
		{optimizer.StateStopped, false, BoundNone, true},
	}
	for _, tc := range cases {
		o := OutcomeOf(optimizer.RunResult{State: tc.state, StopReason: "verification unavailable"}, tc.killed)
		if err := o.Validate(); err != nil {
			t.Fatalf("%s(killed=%v): %v", tc.state, tc.killed, err)
		}
		if o.Bound != tc.wantBound {
			t.Fatalf("%s(killed=%v) → bound %q, want %q", tc.state, tc.killed, o.Bound, tc.wantBound)
		}
		if o.Faulted() != tc.wantFault {
			t.Fatalf("%s(killed=%v) → faulted=%v, want %v", tc.state, tc.killed, o.Faulted(), tc.wantFault)
		}
	}
}

// TestAFaultIsNeverReportedAsABound is the fence for the distinction bound.go exists to keep: a
// dependency being unreachable must not be rendered as a bound the customer reached.
func TestAFaultIsNeverReportedAsABound(t *testing.T) {
	o := OutcomeOf(optimizer.RunResult{
		State: optimizer.StateStopped, StopReason: "verification unavailable: dial tcp: connection refused",
	}, false)
	if o.Stopped() {
		t.Fatalf("an unreachable verification service was reported as the bound %q. That tells a customer "+
			"their budget stopped a run that our own dependency stopped", o.Bound)
	}
	if !strings.Contains(o.Sentence(), "not a result about your repository") {
		t.Fatalf("the fault's sentence does not say it is not a verdict about the repository: %q", o.Sentence())
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

// countingEnumerator is DETERMINISTIC per target, which is the contract every real enumerator honours
// (`proposal.Engine.Propose` is a pure function of its targets and menu).
//
// 🔴 It was written call-varying first, and that hid the starvation defect the cap fix above records:
// with fresh config hashes every call, the loop's own consumed-set filter never matched, so the run
// looked exhausted for a second, unrelated reason. A test double that is less deterministic than the
// thing it stands in for can make a real defect look like a different real defect.
type countingEnumerator struct {
	perTarget int
	calls     int
}

func (e *countingEnumerator) Enumerate(t optimizer.Target) []optimizer.SearchCandidate {
	e.calls++
	out := make([]optimizer.SearchCandidate, 0, e.perTarget)
	for i := 0; i < e.perTarget; i++ {
		out = append(out, optimizer.SearchCandidate{
			DiagnosisID: "diag", Node: t.Node, Dimension: t.Dimension,
			ConfigHash:   fmt.Sprintf("%s-%s-%02d%s", t.Node, t.Dimension, i, strings.Repeat("f", 8)),
			Operator:     "test_op",
			SpecBytes:    []byte(`{}`),
			ExpectedGain: float64(e.perTarget - i),
		})
	}
	return out
}
