package router

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/heros/internal/intent"
)

// holdout.go measures whether the router actually routes.
//
// # 🔴 Why this exists beside the structural fences
//
// Set membership is structural; routing accuracy is statistical, and neither substitutes for the other.
// A build in which every intent is well-formed and `coverage` is recognised one time in three is a green
// build over a broken product. The fences in `intent` cannot see that. This can.
//
// 🚫 There is deliberately no single "accuracy" number. An aggregate hides the failure that matters:
// eighteen intents at 95% and one at 0% averages to 90%, and the one at zero is a surface nobody can
// reach. Recall is reported PER INTENT, and the floor applies to each.

// Question is one labelled example. An empty Intent means the router SHOULD abstain.
type Question struct {
	Text   string
	Intent intent.Intent
	// Redirect names the surface a redirection should point at, for out-of-scope examples.
	Redirect string
}

// Row is one intent's result.
type Row struct {
	Intent   intent.Intent
	Labelled int
	Correct  int
	// Confused records what the router chose instead, and how often. A count of failures teaches
	// nothing; knowing that `loop` is being taken for `harness` names the fix.
	Confused  map[intent.Intent]int
	Abstained int
}

// Recall is the fraction routed correctly, or -1 when nothing was labelled.
func (r Row) Recall() float64 {
	if r.Labelled == 0 {
		return -1
	}
	return float64(r.Correct) / float64(r.Labelled)
}

// Report is the whole evaluation.
type Report struct {
	Rows []Row
	// AbstainLabelled and AbstainCorrect measure the router's willingness to say nothing.
	AbstainLabelled, AbstainCorrect int
	// FalseRoutes are abstain-labelled questions that were routed anyway. 🔴 The most expensive error
	// class: a false route on a durable intent spends money on a goal nobody asked for.
	FalseRoutes []string
}

// AbstentionPrecision is the fraction of should-abstain questions that did.
func (r Report) AbstentionPrecision() float64 {
	if r.AbstainLabelled == 0 {
		return -1
	}
	return float64(r.AbstainCorrect) / float64(r.AbstainLabelled)
}

// Evaluate runs the router over a labelled set.
func Evaluate(rt Router, qs []Question) Report {
	rows := map[intent.Intent]*Row{}
	for _, spec := range intent.All() {
		rows[spec.Intent] = &Row{Intent: spec.Intent, Confused: map[intent.Intent]int{}}
	}
	var rep Report

	for _, q := range qs {
		out := rt.Route(q.Text)
		if q.Intent == "" && q.Redirect == "" {
			rep.AbstainLabelled++
			if out.Abstained() {
				rep.AbstainCorrect++
			} else {
				got := string(out.Intent)
				if out.Redirect != nil {
					got = "→ " + out.Redirect.Surface
				}
				rep.FalseRoutes = append(rep.FalseRoutes,
					fmt.Sprintf("%q routed to %s (score %d)", q.Text, got, out.Score))
			}
			continue
		}
		if q.Redirect != "" {
			rep.AbstainLabelled++
			if out.Redirect != nil && out.Redirect.Surface == q.Redirect {
				rep.AbstainCorrect++
			} else {
				rep.FalseRoutes = append(rep.FalseRoutes,
					fmt.Sprintf("%q should redirect to %s", q.Text, q.Redirect))
			}
			continue
		}
		row := rows[q.Intent]
		if row == nil {
			continue // a fixture naming a non-intent; the fence below catches it
		}
		row.Labelled++
		switch {
		case out.Intent == q.Intent:
			row.Correct++
		case out.Abstained():
			row.Abstained++
		default:
			row.Confused[out.Intent]++
		}
	}

	for _, spec := range intent.All() {
		rep.Rows = append(rep.Rows, *rows[spec.Intent])
	}
	sort.Slice(rep.Rows, func(i, j int) bool { return rep.Rows[i].Intent < rep.Rows[j].Intent })
	return rep
}

// Table renders the report for a human.
func (r Report) Table() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-16s %8s %8s %8s  %s\n", "INTENT", "LABELLED", "CORRECT", "RECALL", "CONFUSED WITH")
	for _, row := range r.Rows {
		recall := "     —"
		if row.Recall() >= 0 {
			recall = fmt.Sprintf("%5.0f%%", row.Recall()*100)
		}
		var conf []string
		for i, n := range row.Confused {
			conf = append(conf, fmt.Sprintf("%s×%d", i, n))
		}
		sort.Strings(conf)
		if row.Abstained > 0 {
			conf = append(conf, fmt.Sprintf("(abstained×%d)", row.Abstained))
		}
		fmt.Fprintf(&b, "%-16s %8d %8d %8s  %s\n",
			row.Intent, row.Labelled, row.Correct, recall, strings.Join(conf, " "))
	}
	if p := r.AbstentionPrecision(); p >= 0 {
		fmt.Fprintf(&b, "\nabstention precision %.0f%% (%d of %d)\n",
			p*100, r.AbstainCorrect, r.AbstainLabelled)
	}
	for _, f := range r.FalseRoutes {
		fmt.Fprintf(&b, "  ✕ %s\n", f)
	}
	return b.String()
}
