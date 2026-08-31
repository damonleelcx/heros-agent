// Package goal is the long-running objective: what is being attempted, for whom, within what limits,
// and — the field people forget — how anyone will know it is finished.
//
// # Why completion is a stored criterion rather than a judgement at the end
//
// A goal without a measurable completion criterion ends when somebody loses interest, and the record
// cannot say whether it succeeded. Worse, an agent asked to decide for itself whether it is done will
// decide yes, because that is the shape of the reward. So the criterion is written down BEFORE the run
// starts and evaluated against stored state, not against the model's opinion of its own work.
package goal

import (
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/intent"
)

// ID identifies a goal.
type ID string

// State is a goal's life. Pause/resume is a STATE rather than a signal: a signal is lost when the
// process holding it dies, and the thing being paused outlives every process.
type State string

const (
	Draft     State = "draft"     // created, not yet admitted; ceilings may still be unset
	Running   State = "running"   // admitted; workers may claim its tasks
	Paused    State = "paused"    // deliberately stopped; resumable from the latest checkpoint
	Succeeded State = "succeeded" // completion criteria met
	Failed    State = "failed"    // terminal without meeting them
	Refused   State = "refused"   // never admitted — could not be bounded
	Cancelled State = "cancelled" // stopped by a person; terminal
)

// Terminal reports whether this goal can still progress.
func (s State) Terminal() bool {
	switch s {
	case Succeeded, Failed, Refused, Cancelled:
		return true
	}
	return false
}

// CriterionKind is how a completion criterion is measured. A closed set: each is evaluated against
// STORED state, and a kind whose evaluation requires asking the model is not admissible.
type CriterionKind string

const (
	// AllTasksSucceeded — every task in the DAG succeeded.
	//
	// 🔴 NOT "all tasks terminal". Terminal includes failed, blocked and cancelled, so a criterion
	// phrased that way is satisfied by TOTAL FAILURE: every task fails, every task is therefore
	// terminal, and the goal reports Succeeded. That is not a hypothetical — it is what this criterion
	// did until a test asserted a goal could not complete on unverified work and caught it.
	//
	// A goal that tolerates some failures expresses that with a counting criterion (`AxesAssessed >= 7`),
	// which states the tolerance instead of hiding it inside the word "terminal".
	AllTasksSucceeded CriterionKind = "all_tasks_succeeded"
	// ProposalsAccepted — at least Threshold proposals were approved by a human.
	ProposalsAccepted CriterionKind = "proposals_accepted"
	// EvalCasesGenerated — at least Threshold eval cases were generated and passed quality gates.
	EvalCasesGenerated CriterionKind = "eval_cases_generated"
	// AxesAssessed — at least Threshold of the nine axes produced a finding with evidence.
	AxesAssessed CriterionKind = "axes_assessed"
)

// Criterion is one measurable completion condition.
type Criterion struct {
	Kind      CriterionKind
	Threshold int
	// Met and MeasuredAt are filled by evaluation against stored state, never by a model's assertion.
	Met        bool
	Observed   int
	MeasuredAt time.Time
}

// Milestone is a checkpoint a human cares about — the unit progress is reported in.
type Milestone struct {
	Name       string
	Criterion  Criterion
	ReachedAt  time.Time
	TaskIDs    []string
	Annotation string
}

// Subject is what the goal is ABOUT: one repository at one pinned revision.
//
// 🔴 One subject, one revision, always. An unpinned run cannot be re-measured against what it changed,
// so its findings cannot be defended later; and a goal spanning two repositories cannot report a
// coherent before-and-after. Both are refused rather than accommodated.
type Subject struct {
	RepoURL  string
	Revision string
	// WorkflowID names which agent chain within the repository, where there is more than one.
	WorkflowID string
}

// Goal is the durable long-running objective.
type Goal struct {
	ID     ID
	Tenant string
	// Intent must be a Tier-A intent. Enforced at admission: a query does not get a goal record.
	Intent    intent.Intent
	Objective string
	Subject   Subject
	// Axes narrows the goal to specific axes. Empty means all nine.
	//
	// 🔴 The axes ARE read from the user's request, because "make my memory strategy better" is a scope
	// and discarding it would run a nine-axis search somebody asked to be a one-axis search. Everything
	// else — tenant, ceilings, budget — comes from context and entitlement, never from the sentence.
	Axes       []string
	Ceilings   bounds.Ceilings
	Spend      bounds.Spend
	Criteria   []Criterion
	Milestones []Milestone
	State      State
	// Refusal is set when State is Refused. Carried on the record so the reason survives the request.
	Refusal *bounds.Refusal
	// ExpectedDuration is what the user was told this would take. Compared against Elapsed by the
	// timeline, so "it is taking much longer than promised" is a fact rather than a feeling.
	ExpectedDuration time.Duration
	CreatedAt        time.Time
	UpdatedAt        time.Time
	// LastCheckpoint is where a resume starts. Never trusted as the ONLY record — the task DAG is
	// authoritative — but it is what makes a resume cheap rather than a full replay.
	LastCheckpoint time.Time
}

var (
	ErrNotDurable   = errors.New("goal: intent is not a durable goal")
	ErrNoCriteria   = errors.New("goal: no completion criteria, so nothing can decide when it is done")
	ErrNotAdmitted  = errors.New("goal: not admitted")
	ErrUnknownAxis  = errors.New("goal: unknown axis")
	ErrIllegalState = errors.New("goal: illegal state transition")
)

// Admit validates a goal and moves it from Draft to Running, or refuses it.
//
// 🔴 This is the ONE place a goal becomes runnable. Every bound is checked here, before any budget is
// spent, because a goal that discovers at task 40 that it was never properly bounded has already spent
// the money the bound existed to protect.
func (g *Goal) Admit(now time.Time) error {
	if g.State != Draft {
		return fmt.Errorf("%w: %s → running (only a draft may be admitted)", ErrIllegalState, g.State)
	}
	if !g.Intent.Durable() {
		return fmt.Errorf("%w: %q is not Tier A; a query does not get a goal record", ErrNotDurable, g.Intent)
	}
	if g.Subject.RepoURL == "" {
		return bounds.Refusal{Cause: bounds.NoSubject}
	}
	if g.Subject.Revision == "" {
		return bounds.Refusal{Cause: bounds.NoSourceRevision, Detail: g.Subject.RepoURL}
	}
	if err := g.Ceilings.Validate(); err != nil {
		return bounds.Refusal{Cause: bounds.UnboundedRequested, Detail: err.Error()}
	}
	valid := map[string]bool{}
	for _, a := range intent.Axes() {
		valid[a] = true
	}
	for _, a := range g.Axes {
		if !valid[a] {
			return bounds.Refusal{Cause: bounds.UnknownAxis, Detail: a}
		}
	}
	if len(g.Criteria) == 0 {
		return ErrNoCriteria
	}
	g.State = Running
	g.UpdatedAt = now
	return nil
}

// Refuse records why a goal was never admitted, keeping the reason on the record.
func (g *Goal) Refuse(r bounds.Refusal, now time.Time) {
	g.State = Refused
	g.Refusal = &r
	g.UpdatedAt = now
}

// EvaluateCompletion measures every criterion against observed values drawn from stored state.
//
// The caller supplies the observations because THIS package must not reach into a store — but it must
// also not accept an assertion that the goal is done. The distinction is that `observed` is a count of
// things that exist in the database, not a judgement.
func (g *Goal) EvaluateCompletion(observed map[CriterionKind]int, now time.Time) bool {
	all := true
	for i := range g.Criteria {
		c := &g.Criteria[i]
		c.Observed = observed[c.Kind]
		c.Met = c.Observed >= c.Threshold
		c.MeasuredAt = now
		if !c.Met {
			all = false
		}
	}
	return all
}

// Pause and Resume. 🔴 Modelled as states rather than signals: the process holding a signal dies, and
// the thing being paused outlives every process that ever touched it.
func (g *Goal) Pause(now time.Time) error {
	if g.State != Running {
		return fmt.Errorf("%w: only a running goal can pause, this is %s", ErrIllegalState, g.State)
	}
	g.State, g.UpdatedAt = Paused, now
	return nil
}

func (g *Goal) Resume(now time.Time) error {
	if g.State != Paused {
		return fmt.Errorf("%w: only a paused goal can resume, this is %s", ErrIllegalState, g.State)
	}
	g.State, g.UpdatedAt = Running, now
	return nil
}

// Claimable reports whether workers may take this goal's tasks. The single predicate a worker asks, so
// pause takes effect without every worker knowing the state list.
func (g *Goal) Claimable() bool { return g.State == Running }

// CheckCeilings stops a goal that reached a limit, recording which one.
func (g *Goal) CheckCeilings(now time.Time) (string, bool) {
	which, exceeded := g.Spend.Exceeded(g.Ceilings)
	if !exceeded {
		return "", false
	}
	g.State = Failed
	g.Refusal = &bounds.Refusal{Cause: bounds.CeilingExceeded, Detail: which}
	g.UpdatedAt = now
	return which, true
}
