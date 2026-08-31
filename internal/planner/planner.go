// Package planner turns a goal into a task DAG, and revises that DAG as facts arrive.
//
// # Why the planner is a separate component from the worker
//
// One component owns the whole plan; workers own one task each and know nothing about the shape around
// them. The split is what makes both halves simple: a worker never has to decide what should happen
// next, and the planner never has to know how anything is executed.
//
// It also makes the plan INSPECTABLE. A plan that exists only as control flow inside a worker can be
// read by running it; a plan that exists as rows can be shown to a person before it costs anything.
//
// # Why replanning is a diff and not a conversation
//
// "Periodically compare current state against the goal and modify the plan" is a comparison, not a
// judgement. `Replan` reads what tasks have produced and emits the tasks that are now justified — one
// proposal task per finding that exists, not per finding a model believes ought to exist. Anything that
// asks the model "what should we do next?" and appends the answer has no bound that survives contact
// with a model in an agreeable mood.
package planner

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/task"
)

// Task kinds this package emits. Constants rather than literals because the worker's tool registry is
// keyed on them, and a typo produces "no tool registered" at execution time rather than at wiring time.
const (
	KindAssessAxis     = "assess_axis"
	KindSynthesise     = "synthesise_assessment"
	KindGenerateCases  = "generate_eval_cases"
	KindQualityGate    = "eval_quality_gate"
	KindPublishEvalSet = "publish_eval_set"
	KindProposeChange  = "propose_change"
	KindVerifyProposal = "verify_proposal"
	KindOpenPR         = "open_pull_request"
	KindRunEvalSet     = "run_eval_set"
	KindCompare        = "compare_results"
)

// Planner builds the initial DAG for one intent and revises it as results arrive.
type Planner interface {
	// Intent says which goal this planner serves.
	Intent() intent.Intent
	// Plan builds the initial task set. It must be deterministic for a given goal: the same goal planned
	// twice produces the same tasks with the same ids, so a replanned goal converges rather than
	// accumulating near-duplicates.
	Plan(g *goal.Goal, now time.Time) ([]*task.Task, error)
	// Replan returns tasks to ADD given what the DAG has produced so far. Returning none is the normal
	// case and means the plan still fits the facts.
	Replan(g *goal.Goal, d *task.DAG, now time.Time) ([]*task.Task, error)
}

// Registry holds one planner per durable intent.
type Registry struct{ byIntent map[intent.Intent]Planner }

// NewRegistry wires the planners for every Tier-A intent.
//
// 🔴 It REFUSES to build if a durable intent has no planner. Without that check, adding a fifth Tier-A
// intent produces a goal that admits successfully, plans nothing, and reports "no claimable task"
// forever — which an operator reads as a healthy idle goal. The failure moves to startup, where
// somebody is watching.
func NewRegistry(ps ...Planner) (*Registry, error) {
	r := &Registry{byIntent: map[intent.Intent]Planner{}}
	for _, p := range ps {
		if !p.Intent().Durable() {
			return nil, fmt.Errorf("planner: %q is not a durable goal and does not get a planner", p.Intent())
		}
		if _, dup := r.byIntent[p.Intent()]; dup {
			return nil, fmt.Errorf("planner: two planners registered for %q", p.Intent())
		}
		r.byIntent[p.Intent()] = p
	}
	var missing []string
	for _, i := range intent.InTier(intent.TierGoal) {
		if _, ok := r.byIntent[i]; !ok {
			missing = append(missing, i.String())
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("planner: durable intents with no planner: %v — such a goal would admit, "+
			"plan nothing, and report an idle state forever", missing)
	}
	return r, nil
}

// For returns the planner for an intent.
func (r *Registry) For(i intent.Intent) (Planner, bool) { p, ok := r.byIntent[i]; return p, ok }

// Build creates the initial DAG for a goal, enforcing its ceilings.
func (r *Registry) Build(g *goal.Goal, now time.Time) (*task.DAG, error) {
	p, ok := r.byIntent[g.Intent]
	if !ok {
		return nil, fmt.Errorf("planner: no planner for %q", g.Intent)
	}
	tasks, err := p.Plan(g, now)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("planner: %q planned no tasks; a goal with an empty plan is idle forever", g.Intent)
	}
	if len(tasks) > g.Ceilings.MaxTasks {
		return nil, fmt.Errorf("planner: %q planned %d tasks, ceiling is %d",
			g.Intent, len(tasks), g.Ceilings.MaxTasks)
	}
	for _, t := range tasks {
		if err := t.RequireIdempotency(); err != nil {
			return nil, err
		}
	}
	return task.NewDAG(string(g.ID), tasks)
}

// Revise asks the planner what new tasks the current results justify, and enforces the bounds that stop
// replanning from running away.
//
// # 🔴 The three bounds, and what each one prevents
//
//   - MaxTasks caps the graph. A planner that emits one follow-up per completed task otherwise never
//     terminates, and each round looks locally reasonable.
//   - MaxSpawnDepth caps RECURSION specifically. The runaway shape is depth, not width: a task that
//     creates a task that creates a task is the pattern that escapes a width limit.
//   - Existing ids are never re-added. A planner that re-emits a task it already emitted would reset
//     that task's attempt counter, and a failing task would retry forever while every individual
//     replanning round stayed inside its limits.
func (r *Registry) Revise(g *goal.Goal, d *task.DAG, now time.Time) ([]*task.Task, error) {
	p, ok := r.byIntent[g.Intent]
	if !ok {
		return nil, fmt.Errorf("planner: no planner for %q", g.Intent)
	}
	proposed, err := p.Replan(g, d, now)
	if err != nil {
		return nil, err
	}
	var added []*task.Task
	for _, t := range proposed {
		if _, exists := d.Tasks[t.ID]; exists {
			continue // never re-add: it would reset the attempt counter
		}
		if t.SpawnDepth > g.Ceilings.MaxSpawnDepth {
			return nil, fmt.Errorf("planner: task %q at spawn depth %d exceeds the ceiling of %d",
				t.ID, t.SpawnDepth, g.Ceilings.MaxSpawnDepth)
		}
		if len(d.Tasks)+len(added) >= g.Ceilings.MaxTasks {
			return added, fmt.Errorf("planner: adding %q would exceed the task ceiling of %d",
				t.ID, g.Ceilings.MaxTasks)
		}
		if err := t.RequireIdempotency(); err != nil {
			return nil, err
		}
		added = append(added, t)
	}
	return added, nil
}

// ── helpers shared by the concrete planners ──────────────────────────────────────────────────────

func newTask(g *goal.Goal, id, kind string, depth int, now time.Time, deps ...task.ID) *task.Task {
	return &task.Task{
		ID: task.ID(id), GoalID: string(g.ID), Kind: kind, State: task.Pending,
		DependsOn: deps, SpawnDepth: depth, CreatedAt: now, UpdatedAt: now,
	}
}

// axesOf returns the axes a goal covers: its own narrowing, or all nine.
//
// 🔴 The narrowing is honoured rather than ignored. "Make my memory strategy better" is a SCOPE, and
// discarding it runs a nine-axis search somebody asked to be a one-axis search — on their budget.
func axesOf(g *goal.Goal) []string {
	if len(g.Axes) > 0 {
		return g.Axes
	}
	return intent.Axes()
}

// succeededOfKind returns the successful tasks of a kind, in id order, with their results decoded.
func succeededOfKind(d *task.DAG, kind string) []*task.Task {
	var out []*task.Task
	for _, t := range d.Tasks {
		if t.Kind == kind && t.State == task.Succeeded {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Finding is the shape an assess_axis task writes into its result. Declared here because the planner is
// what READS it to decide the next round, and a producer and consumer that describe a payload
// separately are two descriptions that will disagree.
type Finding struct {
	Axis     string `json:"axis"`
	Weakness string `json:"weakness"`
	// Actionable says a change could be proposed for this. 🔴 A finding that is real but not actionable
	// must not spawn a proposal task: an agent that proposes a change for every observation produces
	// noise proportional to how carefully it looked.
	Actionable bool `json:"actionable"`
}

func decodeFindings(t *task.Task) []Finding {
	var out []Finding
	if len(t.Result) == 0 {
		return nil
	}
	_ = json.Unmarshal(t.Result, &out)
	return out
}
