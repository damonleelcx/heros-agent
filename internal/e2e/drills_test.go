package e2e_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/toolcontract"
	"github.com/heros-foreal/heros/internal/worker"
)

// drills_test.go injects the four faults P13 names — a worker killed mid-task, duplicate events, stale
// data, an unavailable API — and asserts what must still be true afterwards.
//
// # 🔴 Why these are separate from the unit tests that already exist
//
// The store already proves `AnExpiredLeaseIsReclaimable` and `AZombieWorkerCannotWriteItsResult`; the
// worker proves `ACrashedWorkerIsRecoveredByAnother` and `TheRetryLadderIsBoundedAndThenTerminal`; and
// `TestTheRunResumesAfterTheProcessDies` already proves a killed process resumes without redoing
// finished work. Those establish that each MECHANISM behaves.
//
// What a drill asks is different and is the question an operator has: after the fault, is the RESULT
// still correct — was anything lost, was anything done twice, and does the run still report the truth?
// A system can pass every mechanism test and still answer "no" to the third, which is the failure that
// reaches a customer.
//
// 🚫 None of these inject faults through a test-only hook. Every one uses the store API the product
// uses, because a recovery path exercised only through a back door is a recovery path that was never
// exercised.

// drillsRan records that the fault injections actually executed.
//
// 🔴 Every drill here skips without a database, so an unset DSN turns the whole recovery suite into a
// green run that proved nothing — and "the recovery drills passed" is exactly the sentence somebody
// would rely on before a release. This mirrors TestZZPostgresLegActuallyRan next door: a skip is not a
// pass, and the only way to tell them apart is to count.
var drillsRan struct {
	sync.Mutex
	n int
}

func drillRan() {
	drillsRan.Lock()
	defer drillsRan.Unlock()
	drillsRan.n++
}

// seedPlan creates an admitted goal with its plan, and returns the store and the goal.
func seedPlan(t *testing.T, now time.Time) (store.Store, *goal.Goal, *planner.Registry) {
	t.Helper()
	s := store.NewPostgres(openDB(t))
	drillRan()
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
	return s, g, plans
}

// TestDrillAKilledWorkerHandsOverTheSameIdempotencyKey.
//
// # 🔴 The failure this exists for
//
// A worker opens a pull request, and dies before it can record that it did. The lease expires, another
// worker reclaims the task, and runs it again — because from the database's point of view it never ran.
// The only thing standing between that and a second pull request in the customer's repository is that
// the retry presents the SAME idempotency key, so the remote can recognise the work as already done.
//
// If the key were minted per claim rather than per task, every crash in that window would duplicate the
// effect. Nothing else in the system would look wrong: the task succeeds, the run completes, the record
// is tidy, and the customer has two pull requests.
func TestDrillAKilledWorkerHandsOverTheSameIdempotencyKey(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	s, g, plans := seedPlan(t, now)

	// ⚠️ The first version of this drill claimed whatever was ready and compared keys. Whatever is ready
	// first is an ASSESS task, which bears no effect and therefore carries no idempotency key — so it
	// compared "" with "" and would have passed against a system that regenerated keys on every claim.
	// It failed under mutation only because appending a suffix to an empty string still changes it.
	//
	// 🔴 So the drill drives the run to the approval gate and takes the DELIVERY task, which is the only
	// one where this property matters, and asserts the key is non-empty before comparing anything. A
	// drill that cannot state what it is comparing is a drill that will one day compare nothing.
	w := worker.New("worker-1", s, registry(t, newWorld()))
	w.Clock = &clock{t: now}
	w.Reviser = plans
	w.Lease = time.Minute
	drive(t, w, g.ID, 100)

	dag, err := s.LoadDAG(g.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var parked task.ID
	for id, tk := range dag.Tasks {
		if tk.State == task.AwaitingApproval {
			parked = id
			break
		}
	}
	if parked == "" {
		t.Fatal("the run reached no approval gate, so there is no effect-bearing task to drill on")
	}
	// A person approves it, which returns it to the ready set.
	if err := s.Decide(g.ID, parked, true, now); err != nil {
		t.Fatalf("approve: %v", err)
	}

	first, err := s.Claim(g.ID, "worker-1", time.Minute, now)
	if err != nil {
		t.Fatalf("claim the approved delivery: %v", err)
	}
	if first.ID != parked {
		t.Fatalf("claimed %q, expected the approved delivery task %q", first.ID, parked)
	}
	// 🔴 The precondition that stops this drill going vacuous.
	if first.IdempotencyKey == "" {
		t.Fatal("the effect-bearing task carries no idempotency key, so comparing keys across a " +
			"recovery proves nothing")
	}

	// 🔴 worker-1 opens the pull request and is killed before it can record that it did. It never calls
	// Complete, Release or Renew — the only trace it leaves is a lease that will expire.
	later := now.Add(2 * time.Minute)
	second, err := s.Claim(g.ID, "worker-2", time.Minute, later)
	if err != nil {
		t.Fatalf("reclaim after the lease expired: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("the reclaim handed out %q, not the abandoned %q — the abandoned task is orphaned",
			second.ID, first.ID)
	}
	if second.IdempotencyKey != first.IdempotencyKey {
		t.Fatalf("the idempotency key changed across the recovery:\n  before crash: %q\n  after:        %q\n"+
			"A worker that died between opening the pull request and recording it would now open a "+
			"second one, under a key the remote has never seen and cannot recognise as already done",
			first.IdempotencyKey, second.IdempotencyKey)
	}
	if second.Attempt <= first.Attempt {
		t.Errorf("attempt did not advance across the reclaim (%d then %d); a task that crashes forever "+
			"would never exhaust its retry ladder", first.Attempt, second.Attempt)
	}
}

// TestDrillBDuplicateCompletionsAreRefused.
//
// A completion that arrives twice — a retried RPC, a worker that did not see its own success, an
// operator running a repair script twice. The second must change nothing.
func TestDrillBDuplicateCompletionsAreRefused(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	s, g, _ := seedPlan(t, now)

	claimed, err := s.Claim(g.ID, "worker-1", time.Minute, now)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	good := []byte(`{"the":"real result"}`)
	if err := s.Complete(g.ID, claimed.ID, "worker-1", task.Succeeded, good, "", now); err != nil {
		t.Fatalf("first completion: %v", err)
	}
	// The same worker, the same task, a second time — now carrying a different outcome.
	err = s.Complete(g.ID, claimed.ID, "worker-1", task.Failed, nil, "a stale retry", now.Add(time.Second))
	if err == nil {
		t.Error("a second completion for the same task was accepted; a retried delivery could overwrite " +
			"a success with a failure that happened before it")
	}

	after, err := s.LoadDAG(g.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := after.Tasks[claimed.ID]
	if got.State != task.Succeeded {
		t.Fatalf("the task is %q after a duplicate completion, want succeeded", got.State)
	}
	if string(got.Result) != string(good) {
		t.Errorf("the duplicate overwrote the result: %q", string(got.Result))
	}
	if got.Failure != "" {
		t.Errorf("the duplicate wrote a failure onto a succeeded task: %q", got.Failure)
	}
}

// TestDrillBDuplicateEpisodesAreRecordedNotMerged.
//
// 🚫 A deliberate non-deduplication, asserted so nobody "fixes" it. When a task is retried, its episodes
// are written again — and that is the truth: the work genuinely happened twice. Collapsing them would
// produce a history in which a run that took three attempts looks like it took one, which is exactly the
// question somebody reading a timeline after an incident is trying to answer.
//
// The store assigns the sequence itself, so a caller cannot force a collision even by trying.
func TestDrillBDuplicateEpisodesAreRecordedNotMerged(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	db := openDB(t)
	drillRan()
	// A real goal: episodes reference one, and a drill that invents an id would be testing against a
	// shape the product cannot produce.
	s := store.NewPostgres(db)
	g := improveGoal(now)
	if err := g.Admit(now); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := s.CreateGoal(g); err != nil {
		t.Fatalf("create: %v", err)
	}
	mem := memory.NewPG(db)
	goalID := string(g.ID)

	same := memory.Episode{
		GoalID: goalID, TaskID: "assess-tools", Kind: memory.EpisodeFailure,
		Summary: "the search API returned 429", At: time.Now().UTC(),
	}
	firstSeq, err := mem.AppendEpisode(same)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	secondSeq, err := mem.AppendEpisode(same)
	if err != nil {
		t.Fatalf("append again: %v", err)
	}
	if firstSeq == secondSeq {
		t.Fatalf("both appends returned sequence %d; two attempts share one row and the history now "+
			"says the work happened once", firstSeq)
	}
	eps, err := mem.Episodes(goalID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("read back %d episodes, want 2 — a retried attempt has been merged away", len(eps))
	}
}

// TestDrillCAZombiesLateWriteDoesNotDestroyTheGoodResult.
//
// # 🔴 Why "the write is refused" is only half the property
//
// The store already refuses a write from a worker whose lease has expired. The question a drill asks is
// what the RECORD looks like afterwards: worker-1 stalls, its lease expires, worker-2 reclaims and
// succeeds — and then worker-1 wakes up and reports the failure it was about to report ten minutes ago.
// If that write lands, a task that genuinely succeeded is marked failed by a process that no longer
// speaks for it, and everything downstream is blocked by a fact that is not true.
func TestDrillCAZombiesLateWriteDoesNotDestroyTheGoodResult(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	s, g, _ := seedPlan(t, now)

	zombie, err := s.Claim(g.ID, "worker-1", time.Minute, now)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	// worker-1 stalls. Its lease expires; worker-2 takes over and finishes the job.
	later := now.Add(2 * time.Minute)
	taken, err := s.Claim(g.ID, "worker-2", time.Minute, later)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if taken.ID != zombie.ID {
		t.Fatalf("worker-2 took %q, not the abandoned %q", taken.ID, zombie.ID)
	}
	good := []byte(`{"assessed":"properly"}`)
	if err := s.Complete(g.ID, taken.ID, "worker-2", task.Succeeded, good, "", later); err != nil {
		t.Fatalf("worker-2 completion: %v", err)
	}

	// 🔴 worker-1 wakes up, with no idea any of that happened.
	err = s.Complete(g.ID, zombie.ID, "worker-1", task.Failed, nil, "timed out", later.Add(time.Second))
	if err == nil {
		t.Error("the zombie's write was accepted")
	}
	if err := s.Renew(g.ID, zombie.ID, "worker-1", time.Minute, later.Add(time.Second)); err == nil {
		t.Error("the zombie renewed a lease it no longer holds, which would let it keep writing")
	}

	after, _ := s.LoadDAG(g.ID)
	got := after.Tasks[zombie.ID]
	if got.State != task.Succeeded {
		t.Fatalf("the task is %q; the zombie's late failure destroyed a genuine success", got.State)
	}
	if string(got.Result) != string(good) {
		t.Errorf("the good result was overwritten: %q", string(got.Result))
	}
}

// failingRegistry answers every tool call with an error, as an unreachable provider would.
func failingRegistry(t *testing.T, boom error) *toolcontract.Registry {
	t.Helper()
	r := toolcontract.NewRegistry()
	kinds := []string{planner.KindAssessAxis, planner.KindSynthesise, planner.KindProposeChange,
		planner.KindVerifyProposal}
	for _, k := range kinds {
		if err := r.Register(toolFn{
			spec: toolcontract.Spec{Kind: k, Timeout: time.Second, RetrySafe: true},
			fn:   func(toolcontract.Call) (toolcontract.Result, error) { return toolcontract.Result{}, boom },
		}, nil); err != nil {
			t.Fatalf("register %s: %v", k, err)
		}
	}
	return r
}

// TestDrillDAnUnavailableProviderFailsLoudlyRatherThanQuietly.
//
// # 🔴 The shape this is defending against
//
// This project has already shipped a run that reported SUCCESS for doing almost nothing: the worker was
// missing a reviser, so an improvement run assessed and stopped, and the goal's completion criterion was
// satisfied by the assessment alone. Nothing was red. The end-to-end test passed throughout.
//
// An unreachable model provider is the same shape arriving from outside. Every tool call errors, and the
// run must end by SAYING SO — bounded by the retry ladder, terminal, and never reporting completion.
// "The provider was down and the run finished successfully" is the single worst sentence this system
// could produce, because it is the one nobody checks.
func TestDrillDAnUnavailableProviderFailsLoudlyRatherThanQuietly(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	s, g, plans := seedPlan(t, now)

	boom := errors.New("dial tcp: connect: connection refused")
	w := worker.New("worker-offline", s, failingRegistry(t, boom))
	w.Clock = &clock{t: now}
	w.Reviser = plans
	w.Lease = time.Minute

	var last worker.Outcome
	for i := 0; i < 200; i++ {
		out, err := w.RunOnce(context.Background(), g.ID)
		if err != nil {
			t.Fatalf("cycle %d returned a transport error rather than recording a failed task: %v", i, err)
		}
		last = out
		if !out.More {
			break
		}
	}
	if last.More {
		t.Fatal("the run never terminated with every tool failing; a dead provider produces an " +
			"unbounded loop rather than a verdict")
	}
	// 🔴 The assertion that matters. Anything but DidComplete is an acceptable ending here — stalled,
	// stopped, out of attempts — but reporting completion would mean a run that did nothing announced
	// that it had done everything.
	if last.Did == worker.DidComplete {
		t.Fatalf("the run reported COMPLETE with every tool call failing: %+v", last)
	}

	dag, err := s.LoadDAG(g.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	done, total := dag.Progress()
	var failed int
	var withReason int
	for _, tk := range dag.Tasks {
		if tk.State == task.Failed {
			failed++
			if tk.Failure != "" {
				withReason++
			}
		}
	}
	if failed == 0 {
		t.Fatalf("no task is marked failed after %d/%d with every call erroring", done, total)
	}
	// 🔴 Every failure carries its reason. "Why is this red" has to be answerable from the record the DAG
	// is rebuilt from, or the only place the cause exists is a log line on a machine that has since been
	// replaced.
	if withReason != failed {
		t.Errorf("%d of %d failed tasks record no reason", failed-withReason, failed)
	}
	// And the attempts were bounded rather than retried forever.
	for _, tk := range dag.Tasks {
		if tk.Attempt > g.Ceilings.MaxAttemptsPerTask {
			t.Errorf("task %s ran %d attempts against a ceiling of %d",
				tk.ID, tk.Attempt, g.Ceilings.MaxAttemptsPerTask)
		}
	}
}

// TestZZDrillsActuallyRan fails if the database is configured and yet no drill executed.
//
// 🔴 LAST in this file on purpose. Go runs tests in the order they appear in the source, and the first
// version of this sat at the top — where it ran before any drill had registered, and failed every run
// with the message it was written to print on a real problem. A gate that fires when nothing is wrong
// gets deleted, and takes the real check with it.
//
// ⚠️ Running this alone with `-run TestZZDrills` fails by construction, exactly as the store's
// equivalent does. It is a gate on the suite, not a test of anything on its own.
func TestZZDrillsActuallyRan(t *testing.T) {
	if os.Getenv("HEROS_TEST_DATABASE_URL") == "" {
		t.Log("HEROS_TEST_DATABASE_URL unset: every recovery drill was SKIPPED, and a skip is not a pass")
		return
	}
	drillsRan.Lock()
	defer drillsRan.Unlock()
	if drillsRan.n == 0 {
		t.Fatal("HEROS_TEST_DATABASE_URL is set but no recovery drill ran: the suite would have " +
			"reported green having injected no fault at all")
	}
}
