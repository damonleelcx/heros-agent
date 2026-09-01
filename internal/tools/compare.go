package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/planner"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/toolcontract"
)

// compare.go answers "is this version better than the last one?" — see boundary.go for why it compares
// assessments rather than eval scores.

// AxisChange is one axis's movement between two assessments.
type AxisChange struct {
	Axis string `json:"axis"`
	// Was and Now are the weaknesses reported then and now. Either may be empty.
	Was string `json:"was,omitempty"`
	Now string `json:"now,omitempty"`
	// Change is one of: resolved, appeared, changed, unchanged, unmeasured_now, unmeasured_then.
	//
	// 🔴 The two "unmeasured" values are not the same as "unchanged", and separating them is the whole
	// honesty of this report. An axis nobody looked at this time has not stayed the same; it is unknown,
	// and calling it unchanged would let a run that got WORSE on an unmeasured axis read as stable.
	Change string `json:"change"`
}

// Comparison is the result.
type Comparison struct {
	BaselineGoal     string       `json:"baseline_goal"`
	BaselineRevision string       `json:"baseline_revision"`
	CurrentRevision  string       `json:"current_revision"`
	Changes          []AxisChange `json:"changes"`
	Resolved         int          `json:"resolved"`
	Appeared         int          `json:"appeared"`
	Unknown          int          `json:"unknown"`
	// Claim is the strongest honest statement about the two runs.
	Claim string `json:"claim"`
}

// CompareAssessments diffs this run's findings against the most recent prior assessment.
type CompareAssessments struct {
	Store  store.Store
	Tenant string
}

func (CompareAssessments) Spec() toolcontract.Spec {
	return toolcontract.Spec{Kind: planner.KindCompare, Timeout: 60 * time.Second, RetrySafe: true}
}

func (c CompareAssessments) Execute(_ context.Context, call toolcontract.Call) (toolcontract.Result, error) {
	current, _ := collectFindings(call.Inputs)
	if len(current) == 0 {
		return toolcontract.Result{}, fmt.Errorf(
			"this run produced no findings, so there is nothing to compare")
	}

	base, baseGoal, err := c.baseline(call.GoalID)
	if err != nil {
		return toolcontract.Result{}, err
	}

	nowBy := byAxis(current)
	wasBy := byAxis(base)
	seen := map[string]bool{}
	var out Comparison
	out.BaselineGoal = string(baseGoal.ID)
	out.BaselineRevision = shortRev(baseGoal.Subject.Revision)

	for _, axis := range sortedAxes(nowBy, wasBy) {
		if seen[axis] {
			continue
		}
		seen[axis] = true
		was, hadWas := wasBy[axis]
		now, hadNow := nowBy[axis]
		ch := AxisChange{Axis: axis, Was: was, Now: now}
		switch {
		case hadWas && !hadNow:
			ch.Change = "unmeasured_now"
			out.Unknown++
		case !hadWas && hadNow:
			ch.Change = "unmeasured_then"
			out.Unknown++
		case was == now:
			ch.Change = "unchanged"
		default:
			ch.Change = "changed"
		}
		out.Changes = append(out.Changes, ch)
	}

	// 🔴 The claim is deliberately weak, and says why. "The findings changed" is what an assessment diff
	// supports; "it got better" would need the agent to have been RUN, which this system does not do.
	out.Claim = fmt.Sprintf(
		"Compared against %s at %s: %d axis change(s), %d axis/axes not measured in both runs. "+
			"This compares what the assessment FOUND, not how the agent performed — heros does not run "+
			"your agent, so it cannot tell you whether it got better, only whether it looks different.",
		out.BaselineGoal, out.BaselineRevision, countChanged(out.Changes), out.Unknown)

	b, err := json.Marshal(out)
	if err != nil {
		return toolcontract.Result{}, err
	}
	return toolcontract.Result{Output: b}, nil
}

// baseline finds the most recent earlier assessment of the same subject.
func (c CompareAssessments) baseline(currentGoal string) ([]RankedFinding, *goal.Goal, error) {
	if c.Store == nil {
		return nil, nil, fmt.Errorf("no store is configured, so there is no earlier run to compare against")
	}
	cur, err := c.Store.LoadGoal(goal.ID(currentGoal))
	if err != nil {
		return nil, nil, err
	}
	gs, err := c.Store.ListGoals("")
	if err != nil {
		return nil, nil, err
	}
	var best *goal.Goal
	for _, g := range gs {
		if g.ID == cur.ID || g.Subject.RepoURL != cur.Subject.RepoURL {
			continue
		}
		if g.CreatedAt.After(cur.CreatedAt) {
			continue // only EARLIER runs are a baseline
		}
		if best == nil || g.CreatedAt.After(best.CreatedAt) {
			best = g
		}
	}
	if best == nil {
		// 🔴 A refusal with a next action, not an empty comparison. "Nothing changed" and "there is
		// nothing to compare against" are different answers and only one of them is true here.
		return nil, nil, fmt.Errorf(
			"there is no earlier assessment of this repository to compare against — ask me to look at " +
				"what is weak first, then ask again after you have changed something")
	}
	d, err := c.Store.LoadDAG(best.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("the earlier run's results could not be read: %w", err)
	}
	inputs := map[string][]byte{}
	for id, t := range d.Tasks {
		if t.Kind == planner.KindAssessAxis && len(t.Result) > 0 {
			inputs[string(id)] = t.Result
		}
	}
	fs, _ := collectFindings(inputs)
	if len(fs) == 0 {
		return nil, nil, fmt.Errorf("the earlier run (%s) recorded no findings to compare against", best.ID)
	}
	return fs, best, nil
}

func byAxis(fs []RankedFinding) map[string]string {
	out := map[string]string{}
	for _, f := range fs {
		out[f.Axis] = f.Weakness
	}
	return out
}

func sortedAxes(a, b map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]string{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

func countChanged(cs []AxisChange) int {
	n := 0
	for _, c := range cs {
		if c.Change == "changed" {
			n++
		}
	}
	return n
}

func shortRev(r string) string {
	if len(r) > 8 {
		return r[:8]
	}
	return r
}
