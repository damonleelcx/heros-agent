package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/tenancy"
)

// ── the pure parts ───────────────────────────────────────────────────────────────────────────────

// TestTruncationKeepsWhatBrokeAndWhatChanged.
//
// # 🔴 The rule this inherits
//
// The memory package refuses to compress failures and effects, because the two things a reader most
// needs from an old run are what broke and what it changed in the world. Truncating this response
// oldest-first would undo that at the very last step: a long run's early failure — the one that explains
// everything after it — is precisely the entry that falls off the front.
func TestTruncationKeepsWhatBrokeAndWhatChanged(t *testing.T) {
	var entries []timelineEntry
	// One early failure and one early effect, buried under far more than the cap of chatter.
	entries = append(entries,
		timelineEntry{Seq: 1, Kind: string(memory.EpisodeFailure), Summary: "the thing that broke"},
		timelineEntry{Seq: 2, Kind: string(memory.EpisodeEffect), Summary: "the thing it changed"},
	)
	for i := 3; i <= maxTimelineEntries*2; i++ {
		entries = append(entries, timelineEntry{
			Seq: int64(i), Kind: string(memory.EpisodeObservation), Summary: fmt.Sprintf("chatter %d", i),
		})
	}

	kept, dropped := trimEntries(entries)
	if len(kept) > maxTimelineEntries {
		t.Fatalf("kept %d entries against a cap of %d", len(kept), maxTimelineEntries)
	}
	if dropped != len(entries)-len(kept) {
		t.Errorf("dropped %d but the arithmetic says %d — a truncated history that miscounts what it "+
			"dropped is a history that reads as complete", dropped, len(entries)-len(kept))
	}
	if dropped == 0 {
		t.Fatal("nothing was dropped; this test is not exercising truncation")
	}
	var sawFailure, sawEffect bool
	for _, e := range kept {
		switch e.Kind {
		case string(memory.EpisodeFailure):
			sawFailure = true
		case string(memory.EpisodeEffect):
			sawEffect = true
		}
	}
	if !sawFailure {
		t.Error("the earliest FAILURE was truncated away — the entry that explains everything after it")
	}
	if !sawEffect {
		t.Error("the earliest EFFECT was truncated away — the record that the run changed the world")
	}
}

// TestTruncationIsBoundedEvenWhenEverythingIsEssential.
//
// A run that is nothing but failures still has to return a bounded response. What matters then is that
// the count of what was dropped is reported, because that count is itself the finding.
func TestTruncationIsBoundedEvenWhenEverythingIsEssential(t *testing.T) {
	var entries []timelineEntry
	for i := 1; i <= maxTimelineEntries*2; i++ {
		entries = append(entries, timelineEntry{Seq: int64(i), Kind: string(memory.EpisodeFailure)})
	}
	kept, dropped := trimEntries(entries)
	if len(kept) > maxTimelineEntries {
		t.Fatalf("returned %d entries against a cap of %d; the response is unbounded when every entry "+
			"is essential", len(kept), maxTimelineEntries)
	}
	if dropped == 0 {
		t.Fatal("dropped nothing yet returned fewer than it was given")
	}
}

// TestEveryUnfinishedTaskGetsAReason.
//
// 🔴 A task with an empty reason reads as "no reason" rather than "nobody wrote one", and the state
// somebody adds without a case here is the one a person will be staring at wondering why nothing is
// happening. This walks every task state the package defines.
func TestEveryUnfinishedTaskGetsAReason(t *testing.T) {
	states := []task.State{
		task.Pending, task.Ready, task.Running, task.AwaitingApproval, task.Blocked,
		task.Succeeded, task.Failed, task.Cancelled,
	}
	tasks := map[task.ID]*task.Task{}
	for i, st := range states {
		id := task.ID(fmt.Sprintf("t%d", i))
		tasks[id] = &task.Task{ID: id, Kind: "assess", State: st}
	}
	dag := &task.DAG{GoalID: "g", Tasks: tasks}

	next := whatNext(dag)
	// Terminal states are not "next" and must not appear.
	if len(next) != 5 {
		t.Fatalf("expected the 5 unfinished states, got %d: %+v", len(next), next)
	}
	for _, n := range next {
		if n.Why == "" {
			t.Errorf("task in state %q has no explanation", n.State)
		}
		if n.State == string(task.AwaitingApproval) && !n.NeedsAPerson {
			t.Error("a task awaiting approval is not flagged as needing a person — the one state that " +
				"will never resolve on its own")
		}
	}
	// 🔴 What needs a person sorts first: it is the only part of this list a reader can act on.
	if !next[0].NeedsAPerson {
		t.Errorf("the list does not lead with what needs a person; it leads with %q", next[0].State)
	}
}

// TestWhatNextIsStableAcrossCalls.
//
// # 🔴 The bug this caught while it was being written
//
// `DAG.Tasks` is a MAP, and Go randomises map iteration deliberately. The first version of `whatNext`
// ranged over it and sorted only by whether a task needed a person — so everything else came back in a
// different order every call. On screen that reads as the run churning: a reader refreshes, the list
// reorders, and they go looking for what changed. Two screenshots of the same instant disagree.
//
// It is the kind of defect that never fails once. Sixty calls is enough to make randomised iteration
// show itself; one call could not.
func TestWhatNextIsStableAcrossCalls(t *testing.T) {
	tasks := map[task.ID]*task.Task{}
	for i := range 12 {
		id := task.ID(fmt.Sprintf("task-%02d", i))
		st := task.Pending
		if i%4 == 0 {
			st = task.Ready
		}
		tasks[id] = &task.Task{ID: id, Kind: "assess", State: st}
	}
	// One that needs a person, which must lead every time.
	tasks["zzz-parked"] = &task.Task{ID: "zzz-parked", Kind: "deliver", State: task.AwaitingApproval}
	dag := &task.DAG{GoalID: "g", Tasks: tasks}

	first := whatNext(dag)
	for call := range 60 {
		got := whatNext(dag)
		if len(got) != len(first) {
			t.Fatalf("call %d returned %d entries, first returned %d", call, len(got), len(first))
		}
		for i := range got {
			if got[i].TaskID != first[i].TaskID {
				t.Fatalf("call %d differs at position %d: %q vs %q — the list is ordered by map "+
					"iteration, so it reshuffles on every refresh",
					call, i, got[i].TaskID, first[i].TaskID)
			}
		}
	}
	if first[0].TaskID != "zzz-parked" {
		t.Errorf("the task needing a person does not lead; %q does", first[0].TaskID)
	}
}

// TestPendingSaysWhatItIsWaitingFor.
//
// "Pending" on its own is a shrug. The useful answer names the dependency that has not finished.
func TestPendingSaysWhatItIsWaitingFor(t *testing.T) {
	dag := &task.DAG{GoalID: "g", Tasks: map[task.ID]*task.Task{
		"first":  {ID: "first", Kind: "assess", State: task.Running},
		"second": {ID: "second", Kind: "propose", State: task.Succeeded},
		"third":  {ID: "third", Kind: "deliver", State: task.Pending, DependsOn: []task.ID{"first", "second"}},
	}}
	var third timelineNext
	for _, n := range whatNext(dag) {
		if n.TaskID == "third" {
			third = n
		}
	}
	if third.TaskID == "" {
		t.Fatal("the pending task is missing from what-next")
	}
	if !contains(third.Why, "first") {
		t.Errorf("the reason does not name the unfinished dependency: %q", third.Why)
	}
	if contains(third.Why, "second") {
		t.Errorf("the reason names a dependency that has already succeeded: %q", third.Why)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	}()
}

// ── through HTTP ─────────────────────────────────────────────────────────────────────────────────

// seedRun creates a goal with a plan and some history, for one tenant.
func seedRun(t *testing.T, hz *harness, tenant, id string) goal.ID {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	gid := goal.ID(id)
	g := &goal.Goal{
		ID: gid, Tenant: tenant, Intent: "assess", State: goal.Draft,
		Subject:  goal.Subject{RepoURL: "git@github.com:acme/bot.git", Revision: "abc123"},
		Criteria: []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 1}},
		Ceilings: bounds.Ceilings{
			MaxIterations: 10, MaxTasks: 10, MaxAttemptsPerTask: 2, MaxToolCalls: 10,
			MaxTokens: 1000, MaxCostCents: 10, MaxWallClock: time.Minute, MaxSpawnDepth: 2,
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := g.Admit(now); err != nil {
		t.Fatalf("admit: %v", err)
	}
	st := hz.Root.For(tenant)
	if err := st.CreateGoal(g); err != nil {
		t.Fatalf("create goal: %v", err)
	}
	tasks := []*task.Task{
		{ID: "one", GoalID: id, Kind: "assess", State: task.Succeeded, CreatedAt: now, UpdatedAt: now},
		{ID: "two", GoalID: id, Kind: "deliver", State: task.AwaitingApproval,
			DependsOn: []task.ID{"one"}, CreatedAt: now, UpdatedAt: now},
	}
	dag, err := task.NewDAG(id, tasks)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}
	if err := st.SaveDAG(dag); err != nil {
		t.Fatalf("save dag: %v", err)
	}
	return gid
}

// TestATimelineForAnotherTenantsRunIsIndistinguishableFromOneThatDoesNotExist.
//
// 🔴 This endpoint takes a goal id straight from the URL, which is whatever the caller typed. Answering
// "forbidden" for a real id belonging to somebody else would confirm the id exists and turn a guessable
// identifier into an enumeration of every customer's runs — the property the tenancy work established
// and the one a new read surface is most likely to lose.
func TestATimelineForAnotherTenantsRunIsIndistinguishableFromOneThatDoesNotExist(t *testing.T) {
	hz := newHarness(t)
	hz.Episodes = memory.NewMem()
	owner, _ := hz.user(t, tenancy.Owner)

	// A run belonging to somebody else entirely.
	strangerID := seedRun(t, hz, "some-other-tenant", "g-stranger-"+randSuffix())

	real := hz.do(t, "GET", "/api/goals/"+string(strangerID)+"/timeline", "", owner)
	fake := hz.do(t, "GET", "/api/goals/g-definitely-not-real/timeline", "", owner)
	if real.Code != http.StatusNotFound {
		t.Errorf("another tenant's run answered %d, not 404: %s", real.Code, real.Body.String())
	}
	if real.Code != fake.Code || real.Body.String() != fake.Body.String() {
		t.Errorf("a real id from another tenant is distinguishable from a nonexistent one:\n"+
			"  real: %d %s\n  fake: %d %s", real.Code, real.Body.String(), fake.Code, fake.Body.String())
	}
}

// TestATimelineAnswersWhatHappenedAndWhatNext.
func TestATimelineAnswersWhatHappenedAndWhatNext(t *testing.T) {
	hz := newHarness(t)
	mem := memory.NewMem()
	hz.Episodes = mem
	owner, _ := hz.user(t, tenancy.Owner)
	gid := seedRun(t, hz, hz.tenant, "g-mine-"+randSuffix())

	// 🔴 Episodes written with DESCENDING timestamps and ascending sequence, which is what concurrent
	// workers and a clock that steps backwards produce. The response must follow the sequence.
	base := time.Now().UTC()
	for i, e := range []memory.Episode{
		{Seq: 1, Kind: memory.EpisodeDecision, Summary: "first, chronologically last"},
		{Seq: 2, Kind: memory.EpisodeFailure, Summary: "second"},
		{Seq: 3, Kind: memory.EpisodeEffect, Summary: "third"},
	} {
		e.GoalID = string(gid)
		e.At = base.Add(time.Duration(-i) * time.Minute)
		if _, err := mem.AppendEpisode(e); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	rec := hz.do(t, "GET", "/api/goals/"+string(gid)+"/timeline", "", owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("timeline: %d %s", rec.Code, rec.Body.String())
	}
	var out timelineResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("undecodable: %v", err)
	}

	if out.Goal.State != string(goal.Running) {
		t.Errorf("goal state is %q", out.Goal.State)
	}
	if out.Goal.Total != 2 || out.Goal.Done != 1 {
		t.Errorf("progress is %d/%d, want 1/2", out.Goal.Done, out.Goal.Total)
	}
	if len(out.Entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(out.Entries))
	}
	// 🔴 Sequence order, not wall clock. Sorted by `At`, this list would come back reversed — and a
	// timeline in which an effect precedes the decision that caused it is worse than none.
	for i, want := range []int64{1, 2, 3} {
		if out.Entries[i].Seq != want {
			t.Fatalf("entry %d has seq %d, want %d — the timeline is ordered by timestamp, so two "+
				"workers writing concurrently produce a story in the wrong order",
				i, out.Entries[i].Seq, want)
		}
	}
	// What next: the delivery task is parked for a person, and that must lead.
	if len(out.Next) != 1 {
		t.Fatalf("what-next has %d entries, want 1: %+v", len(out.Next), out.Next)
	}
	if !out.Next[0].NeedsAPerson {
		t.Errorf("the parked task is not flagged as needing a person: %+v", out.Next[0])
	}
	if out.Next[0].TaskID != "two" {
		t.Errorf("what-next names %q", out.Next[0].TaskID)
	}
}

var _ = store.NewMemory
