package e2e_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/heros/internal/autonomy"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/worker"
)

// autonomy_test.go is P14's acceptance: does the organization's setting actually change what a run does
// without a person, and does it leave a record when it does?

type fixedLevel autonomy.Level

func (f fixedLevel) AutonomyFor(string) (autonomy.Level, error) { return autonomy.Level(f), nil }

// runAt drives a whole improve run under one autonomy level and returns what the outside world saw.
func runAt(t *testing.T, level autonomy.Level) (*world, []memory.Episode, worker.Outcome) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	db := openDB(t)
	drillRan()
	s := store.NewPostgres(db)

	g := improveGoal(now)
	if err := g.Admit(now); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := s.CreateGoal(g); err != nil {
		t.Fatalf("create: %v", err)
	}
	plans, err := planner.Default()
	if err != nil {
		t.Fatalf("planners: %v", err)
	}
	d, err := plans.Build(g, now)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := s.SaveDAG(d); err != nil {
		t.Fatalf("save dag: %v", err)
	}

	w := newWorld()
	mem := memory.NewPG(db)
	p := worker.New("worker-autonomy", s, registry(t, w))
	p.Clock = &clock{t: now}
	p.Reviser = plans
	p.Lease = time.Minute
	p.Episodes = mem
	// 🔴 The policy under test, wired the way the daemon wires it.
	p.Policy = autonomy.Policy{Source: fixedLevel(level)}

	var last worker.Outcome
	for i := 0; i < 200; i++ {
		out, err := p.RunOnce(context.Background(), g.ID)
		if err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		last = out
		if !out.More {
			break
		}
	}
	eps, err := mem.Episodes(string(g.ID))
	if err != nil {
		t.Fatalf("episodes: %v", err)
	}
	return w, eps, last
}

// TestSupervisedStopsBeforeTouchingTheCustomersRepository.
//
// The default, and the behaviour every organization has until somebody deliberately changes it. This is
// also the control for the test below: without it, "autonomous opened a pull request" would not
// distinguish the setting from a gate that never worked.
func TestSupervisedStopsBeforeTouchingTheCustomersRepository(t *testing.T) {
	w, eps, last := runAt(t, autonomy.Supervised)

	if len(w.pullReqs) != 0 {
		t.Fatalf("%d pull request(s) were opened under `supervised`", len(w.pullReqs))
	}
	if last.Did != worker.DidBlockedOnApproval {
		t.Fatalf("the run ended on %q, want blocked_on_approval", last.Did)
	}
	var parked int
	for _, e := range eps {
		if strings.Contains(e.Summary, "parked for approval") {
			parked++
		}
	}
	if parked == 0 {
		t.Error("nothing was recorded as parked, so the record does not explain why the run stopped")
	}
}

// TestAutonomousProceedsAndSaysThatNobodyWasAsked.
//
// # 🔴 Why the record matters as much as the behaviour
//
// Turning this on is the most consequential setting in the product: it is the moment the system writes
// to a customer's repository with no person in the loop. If that leaves no trace, then "who approved
// this change?" has no answer at all — not "nobody, because the organization is set to autonomous", but
// silence, which reads like an approval somebody has forgotten giving.
//
// 🔴 And the record is written as an EFFECT, not a decision, because effects are not compressible. A
// summariser that folded this away would produce a tidy narrative of a run that quietly wrote to
// somebody's repository unattended — which is precisely the shape memory.Compressible exists to prevent.
func TestAutonomousProceedsAndSaysThatNobodyWasAsked(t *testing.T) {
	w, eps, last := runAt(t, autonomy.Autonomous)

	if len(w.pullReqs) == 0 {
		t.Fatal("no pull request was opened under `autonomous`; the setting changed nothing")
	}
	if last.Did == worker.DidBlockedOnApproval {
		t.Fatalf("the run still stopped for approval under `autonomous`: %+v", last)
	}

	var unapproved []memory.Episode
	for _, e := range eps {
		if strings.Contains(e.Summary, "proceeded without approval") {
			unapproved = append(unapproved, e)
		}
	}
	if len(unapproved) == 0 {
		t.Fatal("a pull request was opened with nobody asked and nothing recorded it. \"Who approved " +
			"this?\" is now unanswerable")
	}
	for _, e := range unapproved {
		if e.Kind != memory.EpisodeEffect {
			t.Errorf("the record is a %q episode; decisions are compressible, so a summariser is free "+
				"to fold away the one line saying the world was changed unattended", e.Kind)
		}
		if e.Compressible() {
			t.Error("the record that nobody was asked can be compressed away")
		}
		// It must name the setting that allowed it, or the record says what happened without saying why.
		if !strings.Contains(e.Detail, string(autonomy.Autonomous)) {
			t.Errorf("the record does not name the setting that allowed it: %q", e.Detail)
		}
	}
}

// TestAssistedEditsTheWorkspaceButStopsAtTheRepository.
//
// The intermediate level, and the reason there are classes at all. If `assisted` behaved like either
// neighbour it would not be worth having.
func TestAssistedEditsTheWorkspaceButStopsAtTheRepository(t *testing.T) {
	w, _, last := runAt(t, autonomy.Assisted)

	if len(w.pullReqs) != 0 {
		t.Fatalf("%d pull request(s) were opened under `assisted`, which must stop before the "+
			"customer's repository", len(w.pullReqs))
	}
	if last.Did != worker.DidBlockedOnApproval {
		t.Fatalf("the run ended on %q, want blocked_on_approval", last.Did)
	}
	// The plan this run produces has no workspace-class effect in it, so the difference from
	// `supervised` is not observable here — stated rather than asserted falsely. The class distinction
	// is proven directly in internal/autonomy's table tests.
	_ = task.EffectBearingKinds
}
