package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/tenancy"
)

// timeline.go answers "what happened, why, when, and what next" for one goal.
//
// # 🔴 Why this is a query surface and not more logging
//
// The kernel already RECORDS all of this — episodes, task states, checkpoints. What was missing is the
// ability to ask. A record nobody can read is not observability; it is storage. The question an operator
// actually has is never "show me the log", it is "this run has been going for twenty minutes, what is it
// doing and what is it waiting for", and answering that needs the episodes, the DAG and the goal state
// read together.

// timelineEntry is one thing that happened.
type timelineEntry struct {
	At      time.Time `json:"at"`
	Seq     int64     `json:"seq"`
	Kind    string    `json:"kind"`
	Summary string    `json:"summary"`
	Detail  string    `json:"detail,omitempty"`
	TaskID  string    `json:"task_id,omitempty"`
	// Summarised marks an episode a summary now covers. Shown rather than hidden: "what did the summary
	// leave out" is the question compression makes hard, and the episode is deliberately kept for it.
	Summarised bool `json:"summarised,omitempty"`
}

// timelineNext is a task that has not finished, and why it is not running.
//
// 🔴 The "what next" half, and the reason this endpoint is worth building. A list of what happened
// answers a question nobody urgently has; "it is waiting for you to approve step four" is the answer
// somebody is actually looking for at the moment they open this.
type timelineNext struct {
	TaskID string `json:"task_id"`
	Kind   string `json:"kind"`
	State  string `json:"state"`
	Why    string `json:"why"`
	// NeedsAPerson marks the one state nothing will resolve on its own.
	NeedsAPerson bool `json:"needs_a_person,omitempty"`
}

type timelineGoal struct {
	ID        string    `json:"id"`
	Intent    string    `json:"intent"`
	State     string    `json:"state"`
	Done      int       `json:"done"`
	Total     int       `json:"total"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Iteration int       `json:"iteration"`
	// Stalled means nothing is claimable and nothing is running — the run is not finished and is not
	// going to move without something changing.
	Stalled bool `json:"stalled"`
}

type timelineResp struct {
	Goal    timelineGoal    `json:"goal"`
	Entries []timelineEntry `json:"entries"`
	Next    []timelineNext  `json:"next"`
	// Dropped counts entries omitted by the cap, and is ALWAYS reported when non-zero. A truncated
	// history that does not say it is truncated is worse than no history: it reads as the whole story.
	Dropped int `json:"dropped,omitempty"`
}

// maxTimelineEntries caps what one response carries.
//
// A long improvement run produces thousands of episodes, and a timeline that returns all of them is a
// timeline that stops loading. See trimEntries for what is dropped first — it is not simply "the oldest".
const maxTimelineEntries = 400

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	tenant, err := tenancy.MustTenant(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	id := goal.ID(r.PathValue("id"))

	// 🔴 The ownership check, and the reason the assembly below takes a *goal.Goal rather than an id.
	// This handler's id comes straight from the URL, so it is whatever the caller typed. LoadGoal on a
	// tenant-scoped store answers "not found" for another customer's goal exactly as it does for one
	// that never existed — telling them apart would confirm the id is real and turn a guessable
	// identifier into an enumeration of everybody's runs.
	scoped := s.Root.For(tenant)
	g, err := scoped.LoadGoal(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such run"})
		return
	}

	out, err := buildTimeline(scoped, s.Episodes, g)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// buildTimeline assembles the answer from a goal that has ALREADY been proven to belong to the caller.
//
// # 🔴 Why it takes a loaded goal and a scoped store rather than a tenant and an id
//
// Everything it reads is keyed by goal id. `store.Store` is tenant-scoped, so the DAG read below cannot
// cross a boundary. `memory.Store` is NOT — `Episodes(goalID)` will return whatever goal it is given,
// for any customer, exactly the shape the tenancy work removed from the goal store. Passing the loaded
// goal instead of an id means the only way to call this is to have already gone through a scoped load.
//
// ⚠️ That is a convention, not a wall: nothing stops somebody constructing a `goal.Goal` by hand. The
// wall would be scoping `memory.Store` the way `store.Root.For` scopes the other one, which is a real
// refactor and is named in the plan rather than pretended away.
func buildTimeline(st store.Store, eps memory.Store, g *goal.Goal) (timelineResp, error) {
	out := timelineResp{Goal: timelineGoal{
		ID: string(g.ID), Intent: string(g.Intent), State: string(g.State),
		CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
	}}

	// The DAG may legitimately not exist yet — a goal admitted a moment ago has no plan. That is a state
	// to report, not an error.
	dag, err := st.LoadDAG(g.ID)
	if err == nil && dag != nil {
		out.Goal.Done, out.Goal.Total = dag.Progress()
		out.Goal.Stalled = dag.Stalled()
		out.Next = whatNext(dag)
	}

	if cp, ok, err := st.LatestCheckpoint(g.ID); err == nil && ok {
		out.Goal.Iteration = cp.Iteration
	}

	if eps != nil {
		episodes, err := eps.Episodes(string(g.ID))
		if err != nil {
			return timelineResp{}, err
		}
		entries := make([]timelineEntry, 0, len(episodes))
		for _, e := range episodes {
			entries = append(entries, timelineEntry{
				At: e.At, Seq: e.Seq, Kind: string(e.Kind), Summary: e.Summary,
				Detail: e.Detail, TaskID: e.TaskID, Summarised: e.SummarisedBy != 0,
			})
		}
		// 🔴 Ordered by SEQ, not by timestamp. Seq is assigned by the store and is a total order; wall
		// clock is not, because two workers write concurrently and a clock can step backwards. Sorting a
		// timeline by `At` produces a narrative where an effect precedes the decision that caused it —
		// rarely, unreproducibly, and only under the concurrency that makes it matter.
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].Seq < entries[j].Seq })
		out.Entries, out.Dropped = trimEntries(entries)
	}
	return out, nil
}

// trimEntries caps the response, dropping the least important entries first.
//
// # 🔴 Why not simply the oldest
//
// The memory package refuses to compress failures and effects, on the grounds that the two things a
// reader most needs from an old run are what broke and what it changed in the world. Truncating this
// response oldest-first would undo that at the last step: a long run's early failure — the one that
// explains everything after it — is exactly the entry that falls off the front.
//
// So observations and decisions are dropped first, oldest first, and failures and effects are kept until
// there is no other choice. Whatever is dropped is counted and reported.
func trimEntries(entries []timelineEntry) ([]timelineEntry, int) {
	if len(entries) <= maxTimelineEntries {
		return entries, 0
	}
	essential := func(kind string) bool {
		return kind == string(memory.EpisodeFailure) || kind == string(memory.EpisodeEffect)
	}
	// How many droppable entries must go, oldest first.
	toDrop := len(entries) - maxTimelineEntries
	drop := make(map[int]bool, toDrop)
	for i := 0; i < len(entries) && len(drop) < toDrop; i++ {
		if !essential(entries[i].Kind) {
			drop[i] = true
		}
	}
	// Still over the cap because almost everything is a failure or an effect: drop the oldest of those
	// too, rather than returning an unbounded response. A run in that state is one where the count of
	// what was dropped is itself the finding.
	for i := 0; i < len(entries) && len(drop) < toDrop; i++ {
		drop[i] = true
	}
	kept := make([]timelineEntry, 0, maxTimelineEntries)
	for i, e := range entries {
		if !drop[i] {
			kept = append(kept, e)
		}
	}
	return kept, len(drop)
}

// whatNext explains, for every unfinished task, why it is not running.
//
// 🔴 Every non-terminal state gets a sentence. A task in a state this does not recognise would otherwise
// appear with an empty reason, which reads as "no reason" rather than "nobody wrote one" — and the state
// that gets added without a case here is the one somebody is staring at wondering why nothing happens.
func whatNext(dag *task.DAG) []timelineNext {
	var out []timelineNext
	for _, t := range dag.Tasks {
		var why string
		needsPerson := false
		switch t.State {
		case task.Succeeded, task.Failed, task.Cancelled:
			continue
		case task.Ready:
			why = "ready — a worker will claim it next"
		case task.Running:
			why = "running now"
			if t.LeasedBy != "" {
				why = "running now, held by " + t.LeasedBy
			}
		case task.AwaitingApproval:
			why = "waiting for a person to approve it — nothing here moves until somebody does"
			needsPerson = true
		case task.Pending:
			why = "waiting on " + describeWaiting(dag, t)
		case task.Blocked:
			why = "blocked — a task it depends on failed"
		default:
			why = "in state " + string(t.State) + ", which this build has no explanation for"
		}
		out = append(out, timelineNext{
			TaskID: string(t.ID), Kind: t.Kind, State: string(t.State),
			Why: why, NeedsAPerson: needsPerson,
		})
	}
	// 🔴 Sorted deterministically, because `dag.Tasks` is a MAP and Go randomises map iteration on
	// purpose. Without a total order here the what-next list would reshuffle on every refresh — which
	// reads as the run churning, sends somebody looking for the change, and makes two screenshots of the
	// same moment disagree.
	//
	// Whatever needs a person leads: it is the only part of this list a reader can act on. Everything
	// else falls back to task id, which is stable and arbitrary rather than unstable and arbitrary.
	sort.Slice(out, func(i, j int) bool {
		if out[i].NeedsAPerson != out[j].NeedsAPerson {
			return out[i].NeedsAPerson
		}
		return out[i].TaskID < out[j].TaskID
	})
	return out
}

// describeWaiting names the dependencies that have not finished, so "pending" is a fact rather than a
// shrug.
func describeWaiting(dag *task.DAG, t *task.Task) string {
	var waiting []string
	for _, dep := range append(append([]task.ID{}, t.DependsOn...), t.Contributes...) {
		d, ok := dag.Tasks[dep]
		if !ok {
			waiting = append(waiting, string(dep)+" (missing from the plan)")
			continue
		}
		switch d.State {
		case task.Succeeded, task.Failed, task.Cancelled:
		default:
			waiting = append(waiting, string(dep))
		}
	}
	if len(waiting) == 0 {
		return "its dependencies, which have all finished — it should become ready on the next pass"
	}
	// Sorted for the same reason the list above is: the dependencies come from a map.
	sort.Strings(waiting)
	return joinAnd(waiting)
}

func joinAnd(items []string) string {
	switch len(items) {
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		out := ""
		for i, s := range items[:len(items)-1] {
			if i > 0 {
				out += ", "
			}
			out += s
		}
		return out + ", and " + items[len(items)-1]
	}
}
