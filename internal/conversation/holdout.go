package conversation

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// holdout.go evaluates the intent router against a held-out, labelled set (tasks 3.2, 3.4, 3.7).
//
// # 🚫 Why there is no `Accuracy` field on Report, and why there will not be one
//
// A single figure over fourteen intents can sit at 93% while `coverage` — the intent that answers "what
// did you NOT measure" — is routed correctly one time in three, and nothing in the number says so. The
// mean is not merely less informative than the rows; it is ACTIVELY MISLEADING, because it is the number
// somebody will put in a status update and stop looking.
//
// So this file reports FOURTEEN ROWS and two precision figures, and offers no way to collapse them. A
// caller that wants a single number has to write the arithmetic itself, in the open, where a reviewer
// can see it happen.
//
// # The two numbers that are not recall
//
// **Abstention precision** — of the questions the router declined, how many SHOULD have been declined.
// This is the metric D5 is about. A router that is 95% accurate and never abstains silently answers a
// different question one time in twenty; a router that is 88% accurate and abstains on the rest is
// strictly better here, because every one of its failures is visible as a refusal.
//
// **Redirection recall** — of the out-of-scope questions, how many were declined BY NAME with the right
// surface. An abstention on "change my plan" is not wrong, but it is worse than "that is done at
// /app/billing", and collapsing the two would hide a regression from the second to the first.

// HoldoutQuestion is one labelled question.
type HoldoutQuestion struct {
	Text string `json:"text"`
	// Intent is the label. Empty when the question should not route.
	Intent string `json:"intent,omitempty"`
	// Expect is "abstain" or "out_of_scope" for an unlabelled question.
	Expect string `json:"expect,omitempty"`
	// Surface is the route an out-of-scope question must be redirected to.
	Surface string `json:"surface,omitempty"`
	// NearMiss names the intent this question is ALMOST (task 3.8). Reported separately, because a
	// router can look healthy overall and be wrong on exactly these.
	NearMiss string `json:"near_miss,omitempty"`
	// Why documents an abstention's reason for a reader of the fixture.
	Why string `json:"why,omitempty"`
}

// Holdout is the labelled set.
type Holdout struct {
	Questions []HoldoutQuestion `json:"questions"`
}

// LoadHoldout reads the labelled set from disk.
func LoadHoldout(path string) (Holdout, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Holdout{}, err
	}
	var h Holdout
	if err := json.Unmarshal(b, &h); err != nil {
		return Holdout{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(h.Questions) == 0 {
		// 🔴 An empty holdout would make every threshold below pass vacuously — the "green fence over
		// nothing" shape, and the one this file would take if nobody were watching.
		return Holdout{}, fmt.Errorf("%s: the holdout is empty, so every measurement over it is vacuous", path)
	}
	return h, nil
}

// IntentRow is one intent's result. One row per intent, always.
type IntentRow struct {
	Intent Intent
	// Labelled is how many holdout questions carry this label. 🔴 Reported even when zero: an intent
	// with no held-out question is an UNMEASURED intent, and the row saying `0` is the only thing that
	// distinguishes it from one that is doing fine.
	Labelled int
	// Correct is how many routed to this intent.
	Correct int
	// Abstained is how many the router declined despite a label.
	Abstained int
	// Misrouted is how many routed somewhere else.
	Misrouted int
	// NearMissLabelled and NearMissCorrect cover the task 3.8 subset.
	NearMissLabelled int
	NearMissCorrect  int
}

// Recall is Correct / Labelled. Returns -1 for an unmeasured intent rather than 0 or 1.
//
// 🔴 NEGATIVE ON PURPOSE. Zero would read as "this intent is broken" and one would read as "this intent
// is perfect"; both are claims, and the truth is that nobody measured it. A sentinel a formatter has to
// handle is how "unmeasured" stays distinguishable from a measurement.
func (r IntentRow) Recall() float64 {
	if r.Labelled == 0 {
		return -1
	}
	return float64(r.Correct) / float64(r.Labelled)
}

// Misroute is one question that went to the wrong place, kept so a report names them rather than
// counting them. A count tells you a router is 4 questions worse; the list tells you why.
type Misroute struct {
	Question string
	Want     string
	Got      string
	NearMiss string
}

// Report is the whole evaluation.
type Report struct {
	// Rows is FOURTEEN entries, in the intent set's own order. Always.
	Rows []IntentRow
	// AbstainLabelled is how many questions should have been declined.
	AbstainLabelled int
	// AbstainCorrect is how many of those were declined.
	AbstainCorrect int
	// AbstainTotal is how many the router declined in all — including labelled questions it should have
	// routed. The DENOMINATOR of abstention precision.
	AbstainTotal int
	// OutOfScopeLabelled / OutOfScopeNamed cover FR26: declined, and declined naming the right surface.
	OutOfScopeLabelled int
	OutOfScopeNamed    int
	// Misroutes are the questions that went somewhere they should not have.
	Misroutes []Misroute
}

// AbstentionPrecision is: of everything the router declined, what fraction should have been declined.
//
// 🔴 The denominator is every abstention, not every abstainable question. Measuring the other way round
// would be recall over abstentions, which rewards a router that declines everything.
func (r Report) AbstentionPrecision() float64 {
	if r.AbstainTotal == 0 {
		return -1
	}
	return float64(r.AbstainCorrect) / float64(r.AbstainTotal)
}

// RedirectionRecall is: of the out-of-scope questions, how many were declined BY NAME.
func (r Report) RedirectionRecall() float64 {
	if r.OutOfScopeLabelled == 0 {
		return -1
	}
	return float64(r.OutOfScopeNamed) / float64(r.OutOfScopeLabelled)
}

// Evaluate runs every holdout question through a router and returns the fourteen rows.
func Evaluate(router Router, h Holdout) Report {
	byIntent := map[Intent]*IntentRow{}
	for _, spec := range intents {
		byIntent[spec.Intent] = &IntentRow{Intent: spec.Intent}
	}
	rep := Report{}

	for _, q := range h.Questions {
		routing := router.Route(q.Text)

		if q.Intent == "" {
			rep.AbstainLabelled++
			if routing.Abstained() {
				rep.AbstainCorrect++
			} else {
				rep.Misroutes = append(rep.Misroutes, Misroute{
					Question: q.Text, Want: "(abstain)", Got: routing.Intent.String()})
			}
			if q.Expect == "out_of_scope" {
				rep.OutOfScopeLabelled++
				if routing.Abstained() && routing.SurfaceHref == q.Surface {
					rep.OutOfScopeNamed++
				}
			}
			if routing.Abstained() {
				rep.AbstainTotal++
			}
			continue
		}

		row := byIntent[Intent(q.Intent)]
		if row == nil {
			// A label naming no intent is a broken fixture, and it must not be silently skipped: a
			// typo'd label would quietly shrink the denominator and IMPROVE every number.
			rep.Misroutes = append(rep.Misroutes, Misroute{
				Question: q.Text, Want: q.Intent + " (NOT AN INTENT — the fixture is wrong)",
				Got: routing.Intent.String()})
			continue
		}
		row.Labelled++
		if q.NearMiss != "" {
			row.NearMissLabelled++
		}
		switch {
		case routing.Abstained():
			row.Abstained++
			rep.AbstainTotal++
			rep.Misroutes = append(rep.Misroutes, Misroute{
				Question: q.Text, Want: q.Intent, Got: "(abstained)", NearMiss: q.NearMiss})
		case routing.Intent == Intent(q.Intent):
			row.Correct++
			if q.NearMiss != "" {
				row.NearMissCorrect++
			}
		default:
			row.Misrouted++
			rep.Misroutes = append(rep.Misroutes, Misroute{
				Question: q.Text, Want: q.Intent, Got: routing.Intent.String(), NearMiss: q.NearMiss})
		}
	}

	rep.Rows = make([]IntentRow, 0, len(intents))
	for _, spec := range intents {
		rep.Rows = append(rep.Rows, *byIntent[spec.Intent])
	}
	sort.SliceStable(rep.Misroutes, func(i, j int) bool { return rep.Misroutes[i].Question < rep.Misroutes[j].Question })
	return rep
}

// Table renders the fourteen rows plus the two precision figures, for a spike's output and for a test
// failure. Fixed-width so two runs can be diffed.
func (r Report) Table() string {
	var b strings.Builder
	b.WriteString("intent          labelled  correct  misrouted  abstained   recall   near-miss\n")
	b.WriteString("─────────────── ────────  ───────  ─────────  ─────────  ───────   ─────────\n")
	for _, row := range r.Rows {
		recall := "  n/a  "
		if row.Recall() >= 0 {
			recall = fmt.Sprintf("%6.1f%%", row.Recall()*100)
		}
		near := "     —   "
		if row.NearMissLabelled > 0 {
			near = fmt.Sprintf("%4d/%-4d", row.NearMissCorrect, row.NearMissLabelled)
		}
		b.WriteString(fmt.Sprintf("%-15s %8d  %7d  %9d  %9d  %s   %s\n",
			row.Intent, row.Labelled, row.Correct, row.Misrouted, row.Abstained, recall, near))
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("abstention precision   %s   (%d of %d abstentions were correct)\n",
		pct(r.AbstentionPrecision()), r.AbstainCorrect, r.AbstainTotal))
	b.WriteString(fmt.Sprintf("redirection recall     %s   (%d of %d out-of-scope questions named their surface)\n",
		pct(r.RedirectionRecall()), r.OutOfScopeNamed, r.OutOfScopeLabelled))
	b.WriteString("\n🚫 There is deliberately no overall accuracy figure. A mean over fourteen intents can\n" +
		"   sit at 93% while one of them is broken, and the mean is the number people stop reading at.\n")
	if len(r.Misroutes) > 0 {
		b.WriteString("\nquestions that did not land where they should:\n")
		for _, m := range r.Misroutes {
			near := ""
			if m.NearMiss != "" {
				near = "   [near-miss vs " + m.NearMiss + "]"
			}
			b.WriteString(fmt.Sprintf("  want %-14s got %-14s %q%s\n", m.Want, m.Got, m.Question, near))
		}
	}
	return b.String()
}

func pct(v float64) string {
	if v < 0 {
		return "  n/a  "
	}
	return fmt.Sprintf("%6.1f%%", v*100)
}

// ── The floors, and why they are here rather than in a test ──────────────────────────────────────
//
// They are constants in the PACKAGE so the spike binary and the CI test enforce the same numbers. A
// floor written into a test is a floor a spike cannot report against, and two floors is how a spike
// starts saying "fine" about a router CI is about to reject.

// MinIntentRecall is the floor EVERY intent must clear individually.
//
// 🔴 Per intent, never on a mean. The whole argument of §3 is that an aggregate hides the single broken
// intent, so the floor is applied fourteen times. It is deliberately not 100%: a router that must be
// perfect on a holdout is a router somebody will tune to the holdout, at which point the holdout has
// stopped measuring anything.
const MinIntentRecall = 0.80

// MinAbstentionPrecision is the floor on "when it declined, it was right to".
//
// Higher than the recall floor, because the two failures are not symmetric. A missed route produces a
// REFUSAL — visible, polite, and something a person can act on. A wrong abstention on a question the
// surface can answer is annoying. A wrong ROUTE is the dangerous one, and abstention precision is what
// keeps the router honest about declining rather than guessing.
const MinAbstentionPrecision = 0.90
