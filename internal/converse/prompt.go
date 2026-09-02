package converse

import (
	"fmt"
	"strings"

	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/provider"
)

// prompt.go builds what the agent is told, FROM the tables the rest of the system dispatches on.
//
// # 🔴 Why the capability list is generated rather than written
//
// The same argument that made `intent.CanDo()` generated. A hand-written list of capabilities in a
// prompt, beside a table of capabilities in Go, is two lists — and the prompt is the copy that nothing
// compiles, nothing tests, and nobody reads until a capability quietly stops being reachable.
//
// The failure has a direction: a capability ships, nobody adds it to the prompt, and the agent simply
// never chooses it. Nothing goes red. A person asks for it and is told something else instead, which
// is indistinguishable from the feature not existing — the exact failure the closed set was built to
// end, reintroduced one layer up.
//
// So the prompt is a function of `intent.All()` and `intent.Axes()`. A capability added tomorrow is in
// the prompt today.

// systemPrompt is the standing instruction: who the agent is, what it may do, and what it must never
// pretend to do.
//
// # 🔴 Why it is told to REFUSE to invent facts about the repository
//
// This agent's whole product is telling people true things about their code. A model that fills a gap
// with a plausible sentence is worse than one that says it does not know, because the person cannot
// tell the two apart and the plausible sentence is the one they act on. Everything factual it says has
// to come from the facts block it is handed.
func systemPrompt(facts Facts) string {
	var b strings.Builder

	b.WriteString(`You are Heros. You read AI agents — the code someone has written that calls a model —
and answer questions about them, and you can start work on them.

Talk like a careful colleague: plain, short, specific. No bullet lists unless asked. Never open with
"Great question" or similar. Do not use emoji.

You do exactly one of these things per reply, as a JSON object:

  {"action":"say","text":"..."}
      Talk. Use this for greetings, thanks, questions about yourself or what you can do, and for
      anything you can answer from THIS conversation. Nothing runs and nothing is spent.

  {"action":"ask","text":"..."}
      Ask them back, when what they want is genuinely ambiguous and guessing would send them somewhere
      they did not intend. Prefer this over choosing badly. Do not use it for detail you do not
      actually need.

  {"action":"do","capability":"<name>","axis":"<one of the axes, or empty>","why":"..."}
      Carry out one of the named capabilities below. `)

	b.WriteString("`why` is one short sentence, in your own\n" +
		"      words, that will be shown to the person before anything happens — say what you understood\n" +
		"      them to want, not what the capability is called.\n\n")

	b.WriteString("What you can do — this is the whole list, and you may not invent entries:\n\n")
	for _, s := range intent.All() {
		axis := s.Axis
		if axis == "" {
			axis = "—"
		}
		b.WriteString(fmt.Sprintf("  %-14s %-8s axis:%-8s e.g. %q\n",
			s.Intent, tierWord(s.Tier), axis, s.Question))
	}

	b.WriteString("\nThe nine axes of the agent you are looking at: ")
	b.WriteString(strings.Join(intent.Axes(), ", "))
	b.WriteString(`.
Name an axis only when the person named one, or when the capability is about exactly one axis. If they
named two, or none, leave it empty rather than narrowing a question they did not narrow.

Rules you do not get to weigh against anything else:

  - Never state a fact about their repository that is not in "What is loaded" below. If you do not know,
    say you do not know and offer the capability that would find out. A plausible sentence about
    somebody's code is worse than an admission, because they cannot tell the two apart.
  - Never claim something has run, is running, or has finished. You choose; the system runs.
  - Never offer to change a password, a plan, an invoice, team membership, or to connect or disconnect a
    repository. Those are done on other pages and the system routes them there before you are asked.
  - If they want something you genuinely cannot do, say so plainly in one sentence and name the closest
    thing you can do.

`)
	b.WriteString(facts.describe())
	return b.String()
}

// tierWord renders a tier as something a reader understands without a glossary.
//
// The model is told the CONSEQUENCE rather than the internal name: "goal" and "effect" mean nothing to
// it, while "starts a run that costs money" is the fact that should make it hesitate.
func tierWord(t intent.Tier) string {
	switch t {
	case intent.TierGoal:
		return "[runs]"
	case intent.TierEffect:
		return "[writes]"
	default:
		return "[reads]"
	}
}

// Facts is everything true the agent is allowed to assert without looking anything up.
//
// 🔴 A struct rather than a free-text blob so that the set of things it can know is enumerable. The
// question "where could it have got that from?" has to have an answer, and a string somebody appends to
// does not have one.
type Facts struct {
	// SubjectLoaded is false when no repository has been pointed at yet. The single most important
	// fact: almost every question means something different without one.
	SubjectLoaded bool
	// Reference is what the person typed to load it, and Revision is what it pinned to.
	Reference string
	Revision  string
	// IsAgent records whether the loaded repository actually calls a model, and Why explains the
	// verdict. 🔴 Carried because it changes what every other answer MEANS: nine axes over a repository
	// that never calls a model is nine paragraphs about nothing.
	IsAgent bool
	Why     string
	// AxesFound lists the axes discovery actually found evidence for, so the agent can say "I have not
	// found a memory strategy in this repository" instead of inventing one.
	AxesFound   []string
	AxesMissing []string
}

func (f Facts) describe() string {
	var b strings.Builder
	b.WriteString("What is loaded:\n")
	if !f.SubjectLoaded {
		b.WriteString(`  Nothing yet. No repository has been pointed at, so you know NOTHING about their
  code and must not imply otherwise. To load one they paste a GitHub link (github.com/acme/bot) or a
  path on this machine (../their-agent); tell them that if it is what they need.
`)
		return b.String()
	}
	b.WriteString(fmt.Sprintf("  Repository: %s\n", f.Reference))
	if f.Revision != "" {
		b.WriteString(fmt.Sprintf("  Pinned at revision: %s\n", f.Revision))
	}
	if f.IsAgent {
		b.WriteString(fmt.Sprintf("  It does call a model: %s\n", f.Why))
	} else {
		b.WriteString(fmt.Sprintf(`  🔴 It does NOT appear to call a model: %s
  Say so if they ask anything that assumes it is an agent. Assessing it would produce nine findings of
  "no signal found", which reads as nine weaknesses rather than one wrong subject.
`, f.Why))
	}
	if len(f.AxesFound) > 0 {
		b.WriteString(fmt.Sprintf("  Axes with evidence found: %s\n", strings.Join(f.AxesFound, ", ")))
	}
	if len(f.AxesMissing) > 0 {
		b.WriteString(fmt.Sprintf("  Axes with NOTHING found: %s — absence is a finding, not an error.\n",
			strings.Join(f.AxesMissing, ", ")))
	}
	return b.String()
}

// conversation renders the recent transcript as the messages the model sees.
//
// # 🔴 Why only the last few turns, and why that is a stated limit rather than a silent one
//
// Input tokens are billed on every turn, so carrying an unbounded transcript makes the cost of a
// conversation grow with its length — quadratically over the conversation as a whole. A window is the
// standard answer and the honest cost is that the agent forgets the beginning of a long thread.
//
// It is a WINDOW, not a summary, on purpose: summarising would need its own model call on every turn,
// which is a second per-turn cost to avoid a first one, and a summary that drops the sentence the
// person is about to refer back to fails in a way nobody can see.
func conversation(history []memory.Turn, window int) []provider.Message {
	if window > 0 && len(history) > window {
		history = history[len(history)-window:]
	}
	out := make([]provider.Message, 0, len(history))
	for _, t := range history {
		role := "user"
		if t.Role == memory.TurnAgent {
			role = "assistant"
		}
		out = append(out, provider.Message{Role: role, Content: t.Body})
	}
	return out
}
