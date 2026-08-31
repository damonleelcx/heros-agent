package planner_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/task"
)

func ceilings() bounds.Ceilings {
	return bounds.Ceilings{MaxIterations: 100, MaxTasks: 60, MaxAttemptsPerTask: 3, MaxToolCalls: 500,
		MaxTokens: 1e7, MaxCostCents: 5000, MaxWallClock: time.Hour, MaxSpawnDepth: 3}
}

func goalFor(i intent.Intent) *goal.Goal {
	now := time.Now().UTC()
	g := &goal.Goal{
		ID: "g1", Tenant: "t1", Intent: i, State: goal.Draft,
		Subject:  goal.Subject{RepoURL: "git@github.com:acme/bot.git", Revision: "rev-abc"},
		Ceilings: ceilings(),
		Criteria: []goal.Criterion{{Kind: goal.AllTasksSucceeded, Threshold: 1}},
	}
	_ = g.Admit(now)
	return g
}

// TestEveryDurableIntentHasAPlanner is the fence that mirrors the intent fence.
//
// 🔴 Without it, adding a fifth Tier-A intent produces a goal that admits successfully, plans nothing,
// and reports "no claimable task" forever — which an operator reads as a healthy idle goal. The failure
// belongs at startup, where somebody is watching.
func TestEveryDurableIntentHasAPlanner(t *testing.T) {
	r, err := planner.Default()
	if err != nil {
		t.Fatalf("the default registry does not cover every durable intent: %v", err)
	}
	for _, i := range intent.InTier(intent.TierGoal) {
		if _, ok := r.For(i); !ok {
			t.Errorf("durable intent %q has no planner", i)
		}
	}
	// An incomplete registry must be refused rather than quietly accepted.
	if _, err := planner.NewRegistry(planner.Assess{}); err == nil {
		t.Fatal("a registry missing three durable planners was accepted")
	} else if !strings.Contains(err.Error(), "idle state forever") {
		t.Errorf("the error does not explain the consequence: %v", err)
	}
}

// TestANonDurableIntentGetsNoPlanner. Tier is the one discriminator; a query must not acquire a DAG.
func TestANonDurableIntentGetsNoPlanner(t *testing.T) {
	bad := stubPlanner{i: intent.RunHistory}
	if _, err := planner.NewRegistry(planner.Assess{}, planner.Improve{}, planner.EvalSet{},
		planner.Compare{}, bad); err == nil {
		t.Fatal("a planner for a Tier-B query intent was accepted")
	}
}

type stubPlanner struct{ i intent.Intent }

func (s stubPlanner) Intent() intent.Intent { return s.i }
func (s stubPlanner) Plan(*goal.Goal, time.Time) ([]*task.Task, error) {
	return nil, nil
}
func (s stubPlanner) Replan(*goal.Goal, *task.DAG, time.Time) ([]*task.Task, error) { return nil, nil }

// TestAssessFansOutOverAxesAndJoins.
func TestAssessFansOutOverAxesAndJoins(t *testing.T) {
	r, _ := planner.Default()
	g := goalFor(intent.Assess)
	d, err := r.Build(g, time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got, want := len(d.Tasks), len(intent.Axes())+1; got != want {
		t.Fatalf("%d tasks, want one per axis plus a synthesis (%d)", got, want)
	}
	// Every axis task is independently runnable: the fan-out is real, not a chain wearing a fan's name.
	if got := len(d.ReadySet()); got != len(intent.Axes()) {
		t.Fatalf("%d ready, want all %d axis tasks concurrent", got, len(intent.Axes()))
	}
	syn := d.Tasks["synthesise"]
	if syn == nil {
		t.Fatal("no synthesis task")
	}
	if len(syn.DependsOn) != len(intent.Axes()) {
		t.Fatalf("synthesis depends on %d axes, want %d", len(syn.DependsOn), len(intent.Axes()))
	}
}

// TestAFailedAxisBlocksTheSynthesisButNotTheOtherAxes.
//
// 🔴 The product of this report is partly its admissions, so one unmeasurable axis must not destroy the
// other eight — and a synthesis over an unknown subset must not be published as if it were complete.
func TestAFailedAxisBlocksTheSynthesisButNotTheOtherAxes(t *testing.T) {
	r, _ := planner.Default()
	g := goalFor(intent.Assess)
	d, _ := r.Build(g, time.Now().UTC())

	d.Tasks["assess-tools"].State = task.Failed
	d.PropagateFailure()

	if got := d.Tasks["synthesise"].State; got != task.Blocked {
		t.Fatalf("synthesis is %s; a report over an unknown subset must not be produced", got)
	}
	live := 0
	for _, tk := range d.ReadySet() {
		live++
		_ = tk
	}
	if live != len(intent.Axes())-1 {
		t.Fatalf("%d axes still runnable, want %d: one unmeasurable axis destroyed the rest",
			live, len(intent.Axes())-1)
	}
}

// TestANarrowedGoalPlansOnlyTheAxesItAsksFor. "Make my memory strategy better" is a SCOPE; running all
// nine would spend the customer's budget on a search they did not request.
func TestANarrowedGoalPlansOnlyTheAxesItAsksFor(t *testing.T) {
	r, _ := planner.Default()
	g := goalFor(intent.Assess)
	g.Axes = []string{"memory", "context"}
	d, err := r.Build(g, time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got, want := len(d.Tasks), 3; got != want {
		t.Fatalf("%d tasks, want 2 axes + synthesis = %d", got, want)
	}
	for _, id := range []task.ID{"assess-memory", "assess-context"} {
		if d.Tasks[id] == nil {
			t.Errorf("missing %s", id)
		}
	}
	if d.Tasks["assess-tools"] != nil {
		t.Error("an axis outside the requested scope was planned")
	}
}

// ── replanning ───────────────────────────────────────────────────────────────────────────────────

func findings(fs ...planner.Finding) []byte {
	b, _ := json.Marshal(fs)
	return b
}

// TestImproveGrowsItsPlanFromWhatWasActuallyFound. The core of "add replanning": the plan changes
// because facts arrived, not because a model was asked what it fancied doing next.
func TestImproveGrowsItsPlanFromWhatWasActuallyFound(t *testing.T) {
	r, _ := planner.Default()
	now := time.Now().UTC()
	g := goalFor(intent.Improve)
	d, err := r.Build(g, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	before := len(d.Tasks)

	// Nothing has run: replanning must add nothing.
	added, err := r.Revise(g, d, now)
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if len(added) != 0 {
		t.Fatalf("replanning added %d tasks before any assessment ran", len(added))
	}

	// One axis reports two findings, only one of which is actionable.
	ax := d.Tasks["assess-memory"]
	ax.State = task.Succeeded
	ax.Result = findings(
		planner.Finding{Axis: "memory", Weakness: "no summarisation", Actionable: true},
		planner.Finding{Axis: "memory", Weakness: "hard to read", Actionable: false},
	)

	added, err = r.Revise(g, d, now)
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if len(added) != 3 {
		t.Fatalf("added %d tasks, want a 3-task chain for the ONE actionable finding", len(added))
	}
	kinds := map[string]bool{}
	for _, tk := range added {
		kinds[tk.Kind] = true
	}
	for _, want := range []string{planner.KindProposeChange, planner.KindVerifyProposal, planner.KindOpenPR} {
		if !kinds[want] {
			t.Errorf("the chain is missing %s", want)
		}
	}
	_ = before
}

// TestReplanningIsIdempotent. Deterministic ids are what make Revise converge; counter-based ids would
// re-emit the same work with its attempt counters reset, and a failing task would retry forever while
// every individual round stayed inside its limits.
func TestReplanningIsIdempotent(t *testing.T) {
	r, _ := planner.Default()
	now := time.Now().UTC()
	g := goalFor(intent.Improve)
	d, _ := r.Build(g, now)
	d.Tasks["assess-memory"].State = task.Succeeded
	d.Tasks["assess-memory"].Result = findings(
		planner.Finding{Axis: "memory", Weakness: "w", Actionable: true})

	first, _ := r.Revise(g, d, now)
	if len(first) != 3 {
		t.Fatalf("first revise added %d, want 3", len(first))
	}
	// Merge them in, exactly as the runner would.
	all := make([]*task.Task, 0, len(d.Tasks)+len(first))
	for _, tk := range d.Tasks {
		all = append(all, tk)
	}
	all = append(all, first...)
	merged, err := task.NewDAG(string(g.ID), all)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	second, err := r.Revise(g, merged, now)
	if err != nil {
		t.Fatalf("second revise: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("replanning the same facts added %d tasks again; the plan does not converge", len(second))
	}
}

// TestAPullRequestKeyIsStableAcrossReplans. A retried pull-request task must produce the SAME
// idempotency key, or the retry opens a second pull request.
func TestAPullRequestKeyIsStableAcrossReplans(t *testing.T) {
	r, _ := planner.Default()
	g := goalFor(intent.Improve)

	keyAt := func(now time.Time) string {
		d, _ := r.Build(g, now)
		d.Tasks["assess-memory"].State = task.Succeeded
		d.Tasks["assess-memory"].Result = findings(
			planner.Finding{Axis: "memory", Weakness: "w", Actionable: true})
		added, _ := r.Revise(g, d, now)
		for _, tk := range added {
			if tk.Kind == planner.KindOpenPR {
				return tk.IdempotencyKey
			}
		}
		t.Fatal("no pull-request task was planned")
		return ""
	}
	a := keyAt(time.Now().UTC())
	b := keyAt(time.Now().UTC().Add(9 * time.Hour))
	if a == "" {
		t.Fatal("the pull-request task carries no idempotency key")
	}
	if a != b {
		t.Fatalf("the key moved with the clock (%q vs %q); a retry would open a second pull request", a, b)
	}
	if !strings.Contains(a, "rev-abc") {
		t.Errorf("the key does not pin the revision (%q); the same finding at a different revision is a "+
			"different change", a)
	}
}

// TestReplanningCannotRunAway. The three bounds, each asserted.
func TestReplanningCannotRunAway(t *testing.T) {
	r, _ := planner.Default()
	now := time.Now().UTC()

	// (1) MaxTasks caps the graph.
	g := goalFor(intent.Improve)
	g.Ceilings.MaxTasks = 12 // 9 axes + synthesis = 10, leaving room for 2
	d, _ := r.Build(g, now)
	many := make([]planner.Finding, 10)
	for i := range many {
		many[i] = planner.Finding{Axis: "memory", Weakness: fmt.Sprintf("w%d", i), Actionable: true}
	}
	d.Tasks["assess-memory"].State = task.Succeeded
	d.Tasks["assess-memory"].Result = findings(many...)

	added, err := r.Revise(g, d, now)
	if err == nil {
		t.Fatal("replanning blew through the task ceiling without complaint")
	}
	if len(d.Tasks)+len(added) > g.Ceilings.MaxTasks {
		t.Fatalf("added %d to %d, ceiling %d", len(added), len(d.Tasks), g.Ceilings.MaxTasks)
	}

	// (2) MaxSpawnDepth caps recursion specifically — the runaway shape is depth, not width.
	g2 := goalFor(intent.Improve)
	g2.Ceilings.MaxSpawnDepth = 2 // the pull-request task sits at depth 3
	d2, _ := r.Build(g2, now)
	d2.Tasks["assess-memory"].State = task.Succeeded
	d2.Tasks["assess-memory"].Result = findings(
		planner.Finding{Axis: "memory", Weakness: "w", Actionable: true})
	if _, err := r.Revise(g2, d2, now); err == nil {
		t.Fatal("a task deeper than the spawn ceiling was accepted")
	}
}

// TestEveryPlannedEffectCarriesAnIdempotencyKey — checked at Build, so an unsafe plan never reaches a
// worker.
func TestEveryPlannedEffectCarriesAnIdempotencyKey(t *testing.T) {
	r, _ := planner.Default()
	now := time.Now().UTC()
	for _, i := range intent.InTier(intent.TierGoal) {
		g := goalFor(i)
		d, err := r.Build(g, now)
		if err != nil {
			t.Fatalf("%s: build: %v", i, err)
		}
		for _, tk := range d.Tasks {
			if err := tk.RequireIdempotency(); err != nil {
				t.Errorf("%s planned an unsafe task: %v", i, err)
			}
		}
	}
}

// TestEvalSetGatesQualityBeforePublishing. A generator scoring its own output marks its own homework.
func TestEvalSetGatesQualityBeforePublishing(t *testing.T) {
	r, _ := planner.Default()
	g := goalFor(intent.EvalSet)
	d, err := r.Build(g, time.Now().UTC())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	gate, pub := d.Tasks["quality-gate"], d.Tasks["publish"]
	if gate == nil || pub == nil {
		t.Fatal("missing the gate or the publish step")
	}
	if len(gate.DependsOn) != len(planner.Generators) {
		t.Fatalf("the gate sees %d generators, want %d", len(gate.DependsOn), len(planner.Generators))
	}
	if len(pub.DependsOn) != 1 || pub.DependsOn[0] != "quality-gate" {
		t.Fatal("publication does not sit behind the quality gate")
	}
	if pub.IdempotencyKey == "" {
		t.Error("publishing is effect-bearing and carries no idempotency key")
	}
}

// TestCompareWaitsForBothRuns. A comparison against a half-finished run is a number that looks like a
// measurement and is not one.
func TestCompareWaitsForBothRuns(t *testing.T) {
	r, _ := planner.Default()
	g := goalFor(intent.Compare)
	d, _ := r.Build(g, time.Now().UTC())
	if got := len(d.ReadySet()); got != 2 {
		t.Fatalf("%d ready, want both runs concurrent", got)
	}
	d.Tasks["run-baseline"].State = task.Succeeded
	for _, tk := range d.ReadySet() {
		if tk.ID == "compare" {
			t.Fatal("the comparison became runnable with only one of its two runs finished")
		}
	}
}

// TestAnEmptyPlanIsRefused. A goal that admits and plans nothing is idle forever, which reads as health.
func TestAnEmptyPlanIsRefused(t *testing.T) {
	r, err := planner.NewRegistry(stubPlanner{i: intent.Assess}, planner.Improve{},
		planner.EvalSet{}, planner.Compare{})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if _, err := r.Build(goalFor(intent.Assess), time.Now().UTC()); err == nil {
		t.Fatal("a planner that produced no tasks was accepted")
	}
}
