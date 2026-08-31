package planner

import (
	"fmt"
	"time"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/task"
)

// ── assess ───────────────────────────────────────────────────────────────────────────────────────

// Assess plans a fan-out over the axes and a join that synthesises them.
//
// # Why one task per axis rather than one task for the whole assessment
//
// Three reasons, and the third is the one that matters. (1) The axes are independent, so they run
// concurrently. (2) A model asked about one axis at a time answers about that axis, whereas one asked
// about nine produces nine paragraphs of varying effort. (3) 🔴 A per-axis task means a per-axis
// FAILURE: when the tools axis cannot be determined, that is one blocked row in a report that still has
// eight, rather than an assessment that did not happen. This report's product is partly its admissions.
type Assess struct{}

func (Assess) Intent() intent.Intent { return intent.Assess }

func (Assess) Plan(g *goal.Goal, now time.Time) ([]*task.Task, error) {
	axes := axesOf(g)
	tasks := make([]*task.Task, 0, len(axes)+1)
	deps := make([]task.ID, 0, len(axes))
	for _, a := range axes {
		id := "assess-" + a
		tasks = append(tasks, newTask(g, id, KindAssessAxis, 0, now))
		deps = append(deps, task.ID(id))
	}
	// 🔴 The synthesis depends on every axis task, so a failed axis BLOCKS it. Deliberate: a synthesis
	// over an unknown subset is a report whose completeness cannot be stated, and "what did you not
	// measure" is one of the questions this product exists to answer.
	tasks = append(tasks, newTask(g, "synthesise", KindSynthesise, 0, now, deps...))
	return tasks, nil
}

// Replan returns nothing: an assessment's shape is known from the goal alone.
//
// 🔴 Written as a deliberate no-op rather than left unimplemented. A planner that COULD replan and
// chooses not to is a different thing from one nobody finished, and the difference matters to whoever
// reads this next.
func (Assess) Replan(*goal.Goal, *task.DAG, time.Time) ([]*task.Task, error) { return nil, nil }

// ── improve ──────────────────────────────────────────────────────────────────────────────────────

// Improve is the planner that genuinely needs replanning: how many proposals exist cannot be known
// until the assessment has found things.
//
// The initial plan is therefore an assessment. Proposals appear in a later round, one per ACTIONABLE
// finding, each with its verification and pull request behind it.
type Improve struct{}

func (Improve) Intent() intent.Intent { return intent.Improve }

func (Improve) Plan(g *goal.Goal, now time.Time) ([]*task.Task, error) {
	return Assess{}.Plan(g, now)
}

// Replan emits one proposal chain per actionable finding.
//
// # 🔴 Why the chain is three tasks and not one
//
// Propose → verify → open a pull request. Splitting them is what lets the approval gate sit between
// work that costs money and work that changes the customer's repository, and what lets a proposal that
// fails verification die without anybody being asked to approve it. One task doing all three would have
// to decide internally whether to stop, and that decision would be invisible in the DAG.
//
// # 🔴 Why ids are derived from the finding rather than counted
//
// Deterministic ids mean replanning twice produces the same tasks, so Revise's "never re-add an existing
// id" rule converges. Counter-based ids would emit propose-1, propose-2 on the first pass and
// propose-3, propose-4 on the second: the same work twice, with the attempt counters reset.
func (Improve) Replan(g *goal.Goal, d *task.DAG, now time.Time) ([]*task.Task, error) {
	var out []*task.Task
	for _, assessed := range succeededOfKind(d, KindAssessAxis) {
		for i, f := range decodeFindings(assessed) {
			// 🔴 A finding that is real but not actionable must not spawn a proposal. An agent that
			// proposes a change for every observation produces noise in proportion to how carefully it
			// looked, which punishes the thorough assessment.
			if !f.Actionable {
				continue
			}
			base := fmt.Sprintf("%s-%d", f.Axis, i)
			proposeID, verifyID, prID := "propose-"+base, "verify-"+base, "pr-"+base

			out = append(out, newTask(g, proposeID, KindProposeChange, 1, now, assessed.ID))
			out = append(out, newTask(g, verifyID, KindVerifyProposal, 2, now, task.ID(proposeID)))

			pr := newTask(g, prID, KindOpenPR, 3, now, task.ID(verifyID))
			// 🔴 The key is derived from the goal, the pinned revision and the finding — never from a
			// clock or a counter. A retried pull-request task must produce the SAME key, or the retry
			// opens a second pull request and the customer finds it before we do.
			pr.IdempotencyKey = fmt.Sprintf("%s:%s:%s", g.ID, g.Subject.Revision, base)
			out = append(out, pr)
		}
	}
	return out, nil
}

// ── evalset ──────────────────────────────────────────────────────────────────────────────────────

// Generators are the four ways a case is produced. Independent by construction: each is blind to what
// the others surface, which is the point — one generation strategy does not find everything.
var Generators = []string{"seed_from_real_traces", "schema_driven", "llm_driven", "adversarial_perturbation"}

// EvalSet plans generation, a quality gate, and publication.
type EvalSet struct{}

func (EvalSet) Intent() intent.Intent { return intent.EvalSet }

func (EvalSet) Plan(g *goal.Goal, now time.Time) ([]*task.Task, error) {
	tasks := make([]*task.Task, 0, len(Generators)+2)
	deps := make([]task.ID, 0, len(Generators))
	for _, gen := range Generators {
		id := "generate-" + gen
		tasks = append(tasks, newTask(g, id, KindGenerateCases, 0, now))
		deps = append(deps, task.ID(id))
	}
	// 🔴 The quality gate is its own task between generation and publication. A generator scoring its own
	// output is marking its own homework, and the failure is silent: a set full of cases the agent finds
	// easy looks exactly like a set the agent is good at.
	tasks = append(tasks, newTask(g, "quality-gate", KindQualityGate, 0, now, deps...))

	publish := newTask(g, "publish", KindPublishEvalSet, 0, now, task.ID("quality-gate"))
	publish.IdempotencyKey = fmt.Sprintf("%s:%s:evalset", g.ID, g.Subject.Revision)
	tasks = append(tasks, publish)
	return tasks, nil
}

func (EvalSet) Replan(*goal.Goal, *task.DAG, time.Time) ([]*task.Task, error) { return nil, nil }

// ── compare ──────────────────────────────────────────────────────────────────────────────────────

// Compare runs one eval set against two revisions and compares the results.
//
// 🔴 Tier A rather than Tier B — a durable goal rather than a query — because answering honestly means
// RUNNING an eval set twice, which costs money and takes time. Presenting that as a page load would make
// a paid, slow operation look like a free, instant one.
type Compare struct{}

func (Compare) Intent() intent.Intent { return intent.Compare }

func (Compare) Plan(g *goal.Goal, now time.Time) ([]*task.Task, error) {
	// Both runs must SUCCEED before a comparison is drawn: a comparison against a half-finished run is a
	// number that looks like a measurement and is not one.
	return []*task.Task{
		newTask(g, "run-baseline", KindRunEvalSet, 0, now),
		newTask(g, "run-candidate", KindRunEvalSet, 0, now),
		newTask(g, "compare", KindCompare, 0, now, "run-baseline", "run-candidate"),
	}, nil
}

func (Compare) Replan(*goal.Goal, *task.DAG, time.Time) ([]*task.Task, error) { return nil, nil }

// Default returns a registry with every Tier-A planner wired.
func Default() (*Registry, error) { return NewRegistry(Assess{}, Improve{}, EvalSet{}, Compare{}) }
