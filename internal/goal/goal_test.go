package goal

import (
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/intent"
)

func ceilings() bounds.Ceilings {
	return bounds.Ceilings{MaxIterations: 10, MaxTasks: 50, MaxAttemptsPerTask: 3, MaxToolCalls: 100,
		MaxTokens: 1_000_000, MaxCostCents: 500, MaxWallClock: time.Hour, MaxSpawnDepth: 3}
}

func draft() *Goal {
	return &Goal{
		ID: "g1", Tenant: "t1", Intent: intent.Assess, State: Draft,
		Objective: "assess the memory strategy",
		Subject:   Subject{RepoURL: "git@github.com:acme/bot.git", Revision: "abc123"},
		Ceilings:  ceilings(),
		Criteria:  []Criterion{{Kind: AxesAssessed, Threshold: 9}},
	}
}

// TestAdmissionIsTheOnlyDoorIn. Every bound is checked before any budget is spent; a goal that
// discovers at task 40 that it was never bounded has already spent the money.
func TestAdmissionIsTheOnlyDoorIn(t *testing.T) {
	now := time.Now()
	g := draft()
	if err := g.Admit(now); err != nil {
		t.Fatalf("a well-formed goal was refused: %v", err)
	}
	if g.State != Running || !g.Claimable() {
		t.Fatalf("admitted goal is %s and claimable=%v", g.State, g.Claimable())
	}
	if err := g.Admit(now); !errors.Is(err, ErrIllegalState) {
		t.Error("a running goal was admitted a second time")
	}
}

// TestOnlyTierAGetsAGoalRecord. Putting a queue, a lease and a checkpoint behind a database read is the
// over-application the tiering exists to prevent.
func TestOnlyTierAGetsAGoalRecord(t *testing.T) {
	for _, i := range intent.InTier(intent.TierQuery) {
		g := draft()
		g.Intent = i
		if err := g.Admit(time.Now()); !errors.Is(err, ErrNotDurable) {
			t.Errorf("query intent %q was admitted as a durable goal: %v", i, err)
		}
	}
	for _, i := range intent.InTier(intent.TierEffect) {
		g := draft()
		g.Intent = i
		if err := g.Admit(time.Now()); !errors.Is(err, ErrNotDurable) {
			t.Errorf("effect intent %q was admitted as a durable goal: %v", i, err)
		}
	}
	for _, i := range intent.InTier(intent.TierGoal) {
		g := draft()
		g.Intent = i
		if err := g.Admit(time.Now()); err != nil {
			t.Errorf("durable intent %q was refused: %v", i, err)
		}
	}
}

// TestAnUnboundedGoalIsRefusedNotDefaulted. The case the whole rule exists for.
func TestAnUnboundedGoalIsRefusedNotDefaulted(t *testing.T) {
	g := draft()
	g.Ceilings = bounds.Ceilings{} // every field zero
	err := g.Admit(time.Now())
	var r bounds.Refusal
	if !errors.As(err, &r) || r.Cause != bounds.UnboundedRequested {
		t.Fatalf("a goal with no ceilings must be REFUSED, not given defaults; got %v", err)
	}
	if r.NextAction() == "" {
		t.Error("the refusal does not tell the person how to make the goal bounded")
	}
	// One missing ceiling is as fatal as all of them: a forgotten field is not a licence.
	g2 := draft()
	g2.Ceilings.MaxCostCents = 0
	if err := g2.Admit(time.Now()); err == nil {
		t.Error("a goal with no money ceiling was admitted")
	}
}

// TestASubjectMustBePinned. An unpinned run cannot be re-measured against what it changed, so its
// findings cannot be defended afterwards.
func TestASubjectMustBePinned(t *testing.T) {
	g := draft()
	g.Subject.Revision = ""
	var r bounds.Refusal
	if err := g.Admit(time.Now()); !errors.As(err, &r) || r.Cause != bounds.NoSourceRevision {
		t.Fatalf("unpinned subject admitted: %v", err)
	}
	g2 := draft()
	g2.Subject.RepoURL = ""
	if err := g2.Admit(time.Now()); !errors.As(err, &r) || r.Cause != bounds.NoSubject {
		t.Fatalf("subjectless goal admitted: %v", err)
	}
}

// TestAGoalWithoutCompletionCriteriaCannotStart. Otherwise it ends when somebody loses interest, and an
// agent asked to judge its own completion will say yes.
func TestAGoalWithoutCompletionCriteriaCannotStart(t *testing.T) {
	g := draft()
	g.Criteria = nil
	if err := g.Admit(time.Now()); !errors.Is(err, ErrNoCriteria) {
		t.Fatalf("a goal nothing can declare finished was admitted: %v", err)
	}
}

// TestCompletionIsMeasuredAgainstObservationsNotAssertions.
func TestCompletionIsMeasuredAgainstObservationsNotAssertions(t *testing.T) {
	now := time.Now()
	g := draft()
	g.Criteria = []Criterion{{Kind: AxesAssessed, Threshold: 9}, {Kind: ProposalsAccepted, Threshold: 1}}
	if g.EvaluateCompletion(map[CriterionKind]int{AxesAssessed: 9}, now) {
		t.Fatal("goal reported complete with one criterion unmet")
	}
	if !g.Criteria[0].Met || g.Criteria[1].Met {
		t.Fatal("per-criterion results are wrong")
	}
	if !g.EvaluateCompletion(map[CriterionKind]int{AxesAssessed: 9, ProposalsAccepted: 2}, now) {
		t.Fatal("goal did not report complete with every criterion met")
	}
	if g.Criteria[0].Observed != 9 {
		t.Error("the observed value is not recorded, so nobody can audit the completion decision")
	}
}

// TestPauseIsAStateNotASignal. A signal dies with the process holding it; the goal outlives every
// process that touches it.
func TestPauseIsAStateNotASignal(t *testing.T) {
	now := time.Now()
	g := draft()
	_ = g.Admit(now)
	if err := g.Pause(now); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if g.Claimable() {
		t.Fatal("a paused goal is still handing work to workers")
	}
	if err := g.Pause(now); err == nil {
		t.Error("double pause accepted")
	}
	if err := g.Resume(now); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !g.Claimable() {
		t.Fatal("a resumed goal is not claimable")
	}
}

// TestACeilingStopsTheGoalAndSaysWhich. "It stopped" without a reason sends an operator hunting.
func TestACeilingStopsTheGoalAndSaysWhich(t *testing.T) {
	now := time.Now()
	g := draft()
	_ = g.Admit(now)
	if _, hit := g.CheckCeilings(now); hit {
		t.Fatal("a fresh goal reported a ceiling")
	}
	g.Spend.CostCents = 500
	which, hit := g.CheckCeilings(now)
	if !hit || which != "MaxCostCents" {
		t.Fatalf("got (%q,%v), want MaxCostCents", which, hit)
	}
	if g.State != Failed || g.Refusal == nil || g.Refusal.Cause != bounds.CeilingExceeded {
		t.Fatal("the goal did not stop, or did not record why")
	}
	if g.Refusal.NextAction() == "" {
		t.Error("a stopped goal must tell the operator what to do next")
	}
}

// TestAxesComeFromTheRequestButAreValidated. Scope is read from the sentence; an unknown axis is
// refused rather than silently widening the run to all nine.
func TestAxesComeFromTheRequestButAreValidated(t *testing.T) {
	g := draft()
	g.Axes = []string{"memory", "context"}
	if err := g.Admit(time.Now()); err != nil {
		t.Fatalf("a narrowed goal was refused: %v", err)
	}
	g2 := draft()
	g2.Axes = []string{"memory", "vibes"}
	var r bounds.Refusal
	if err := g2.Admit(time.Now()); !errors.As(err, &r) || r.Cause != bounds.UnknownAxis {
		t.Fatalf("unknown axis accepted, which would silently widen the run: %v", err)
	}
}
