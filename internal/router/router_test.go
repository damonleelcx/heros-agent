package router

import (
	"testing"

	"github.com/heros-foreal/heros/internal/intent"
)

// holdout is the labelled set. At least three phrasings per intent, written as a person would type
// them rather than as the vocabulary spells them — a holdout built from the vocabulary tests nothing
// except that strings compare equal.
var holdout = []Question{
	// Tier A
	{Text: "look at my repo and tell me what is weak", Intent: intent.Assess},
	{Text: "can you review my agent and find problems", Intent: intent.Assess},
	{Text: "run an audit on this codebase", Intent: intent.Assess},
	{Text: "what could be better about my setup", Intent: intent.Assess},

	{Text: "fix it, and open a pull request", Intent: intent.Improve},
	{Text: "go ahead and fix the memory problem", Intent: intent.Improve},
	{Text: "improve my agent please", Intent: intent.Improve},
	{Text: "make it better and open a pr", Intent: intent.Improve},

	{Text: "build me an eval set for this agent", Intent: intent.EvalSet},
	{Text: "generate evals so I can measure it", Intent: intent.EvalSet},
	{Text: "I need an evaluation set", Intent: intent.EvalSet},

	{Text: "is this version better than the last one", Intent: intent.Compare},
	{Text: "did it help, compare the two runs", Intent: intent.Compare},
	{Text: "which version performs better", Intent: intent.Compare},

	// Tier B
	{Text: "what does my agent do, step by step", Intent: intent.Graph},
	{Text: "show me the graph of my workflow", Intent: intent.Graph},
	{Text: "how does it work end to end", Intent: intent.Graph},

	{Text: "should these nodes run in this order", Intent: intent.GraphOrder},
	{Text: "is the ordering right here", Intent: intent.GraphOrder},
	{Text: "check the topology of my graph", Intent: intent.GraphOrder},

	{Text: "what happened in that run", Intent: intent.RunHistory},
	{Text: "show me the last run", Intent: intent.RunHistory},
	{Text: "pull up the run history", Intent: intent.RunHistory},

	{Text: "what did you measure and what did you not", Intent: intent.Coverage},
	{Text: "how much did you check", Intent: intent.Coverage},
	{Text: "what is your coverage here", Intent: intent.Coverage},

	{Text: "what conversation history does this node get", Intent: intent.Context},
	{Text: "how is the message list built", Intent: intent.Context},
	{Text: "how big is the context window", Intent: intent.Context},

	{Text: "what does this node remember between calls", Intent: intent.Memory},
	{Text: "does anything persist between sessions", Intent: intent.Memory},
	{Text: "what does it remember", Intent: intent.Memory},

	{Text: "what is this node allowed to do", Intent: intent.Harness},
	{Text: "what can it reach and what can it spend", Intent: intent.Harness},
	{Text: "are there any permissions on this", Intent: intent.Harness},

	{Text: "how many turns does it take", Intent: intent.Loop},
	{Text: "what is the stop condition", Intent: intent.Loop},
	{Text: "is there a max turns setting", Intent: intent.Loop},

	{Text: "what skills are bound at this call site", Intent: intent.Skills},
	{Text: "which skills does it have", Intent: intent.Skills},
	{Text: "what skills is it using", Intent: intent.Skills},

	{Text: "what tools does this agent have", Intent: intent.Tools},
	{Text: "which tools does it actually call", Intent: intent.Tools},
	{Text: "is there an unused tool anywhere", Intent: intent.Tools},

	{Text: "what exactly would you write into my source", Intent: intent.PreviewChange},
	{Text: "show me the diff first", Intent: intent.PreviewChange},
	{Text: "what would change if I accept", Intent: intent.PreviewChange},

	// Tier C
	{Text: "change something on the memory axis", Intent: intent.Author},
	{Text: "I want to author a change", Intent: intent.Author},
	{Text: "edit the axis config", Intent: intent.Author},

	{Text: "change the prompt for triage", Intent: intent.Prompt},
	{Text: "rewrite the prompt to be shorter", Intent: intent.Prompt},
	{Text: "update the system prompt", Intent: intent.Prompt},

	{Text: "change the model on this node", Intent: intent.Model},
	{Text: "switch model to something cheaper", Intent: intent.Model},
	{Text: "use a different model here", Intent: intent.Model},

	{Text: "how does an approved change reach my repository", Intent: intent.Deliver},
	{Text: "how does a change get merged", Intent: intent.Deliver},
	{Text: "explain the delivery process", Intent: intent.Deliver},

	// Redirections — named, not merely declined.
	{Text: "change my password", Redirect: "/app/account"},
	{Text: "I want to see the invoice", Redirect: "/app/billing"},
	{Text: "invite someone to my org", Redirect: "/app/settings/members"},
	{Text: "connect a repository for me", Redirect: "/app/connections"},

	// Should abstain — plausible sentences this surface does not serve.
	{Text: "what is the weather today"},
	{Text: "write me a poem about ducks"},
	{Text: "hello"},
	{Text: "thanks, that was helpful"},
	{Text: "who won the football"},
	{Text: "can you order me a pizza"},
	{Text: "translate this to french"},
}

// TestEveryIntentIsReachableFromASentence.
//
// 🔴 The fence that structural checks cannot provide. An intent can be perfectly well-formed — a tier, a
// question, a valid axis — and still be unreachable, because nothing a person would type scores above
// the threshold for it. The user then gets a refusal indistinguishable from the feature not existing,
// which is the exact failure the whole closed-set design was meant to end.
func TestEveryIntentIsReachableFromASentence(t *testing.T) {
	rep := Evaluate(New(), holdout)
	for _, row := range rep.Rows {
		if row.Labelled == 0 {
			t.Errorf("%s has NO held-out question: it is unmeasured, not healthy", row.Intent)
			continue
		}
		if row.Recall() < MinRecall {
			t.Errorf("%s recall %.0f%% is below the %.0f%% floor (%d/%d correct, confused: %v, abstained %d)",
				row.Intent, row.Recall()*100, MinRecall*100, row.Correct, row.Labelled,
				row.Confused, row.Abstained)
		}
	}
	if t.Failed() {
		t.Logf("\n%s", rep.Table())
	}
}

// TestTheRouterWouldRatherSayNothingThanGuess.
//
// The two mistakes are not symmetric. An abstention costs one exchange; a false route on a durable
// intent spends money on a goal nobody asked for.
func TestTheRouterWouldRatherSayNothingThanGuess(t *testing.T) {
	rep := Evaluate(New(), holdout)
	if p := rep.AbstentionPrecision(); p < 0.85 {
		t.Errorf("abstention precision %.0f%% is too low", p*100)
		for _, f := range rep.FalseRoutes {
			t.Errorf("  %s", f)
		}
	}
}

// TestABareNounAbstains. A single generic word scores one point, which is below the threshold — so
// "memory" alone does not launch anything, and "what does it remember between calls" does.
func TestABareNounAbstains(t *testing.T) {
	r := New()
	for _, bare := range []string{"tool", "model", "prompt", "loop"} {
		if out := r.Route(bare); !out.Abstained() {
			t.Errorf("the bare word %q routed to %s (score %d); a noun on its own is ambiguous "+
				"across axes and must not launch anything", bare, out.Intent, out.Score)
		}
	}
}

// TestRedirectionsBeatKeywordScores.
//
// 🔴 "Change my password" contains "change", and several intents score on it. The redirection is
// correct regardless: an agent that offers to change a password has crossed from answering about a
// system to administering an account.
func TestRedirectionsBeatKeywordScores(t *testing.T) {
	r := New()
	for _, text := range []string{
		"change my password", "change my plan", "connect a repository",
		"revoke access to my repo", "show me the invoice",
	} {
		out := r.Route(text)
		if out.Redirect == nil {
			t.Errorf("%q routed to %s instead of naming the surface where it is done", text, out.Intent)
			continue
		}
		if out.Redirect.Surface == "" || out.Redirect.Does == "" {
			t.Errorf("%q redirected without saying where or to what", text)
		}
	}
}

// TestUnboundedRequestsAreDetectedBeforeAnythingIsSpent. The refusal must happen before planning, which
// is the entire point of refusing.
func TestUnboundedRequestsAreDetectedBeforeAnythingIsSpent(t *testing.T) {
	for _, text := range []string{
		"keep improving until it is perfect",
		"fix everything, no limit",
		"run as many as it takes",
		"just keep going until you run out",
	} {
		if !Unbounded(text) {
			t.Errorf("%q was not detected as unbounded", text)
		}
	}
	for _, bounded := range []string{
		"fix my memory axis, up to $5",
		"look at my repo and tell me what is weak",
		"give me three proposals",
	} {
		if Unbounded(bounded) {
			t.Errorf("%q was wrongly treated as unbounded", bounded)
		}
	}
}

// TestRoutingIsDeterministic. A tie must resolve the same way on every machine, or a holdout result is
// not reproducible and tuning becomes superstition.
func TestRoutingIsDeterministic(t *testing.T) {
	r := New()
	for _, q := range holdout {
		first := r.Route(q.Text)
		for i := 0; i < 20; i++ {
			if got := r.Route(q.Text); got.Intent != first.Intent {
				t.Fatalf("%q routed to %s then %s", q.Text, first.Intent, got.Intent)
			}
		}
	}
}

// TestANearMissIsDiagnosable. A router that cannot explain itself gets tuned by superstition.
func TestANearMissIsDiagnosable(t *testing.T) {
	out := New().Route("what can it reach and what can it spend")
	if out.Intent != intent.Harness {
		t.Fatalf("routed to %s", out.Intent)
	}
	if out.Score == 0 {
		t.Error("no score was reported, so nobody can tell a confident route from a marginal one")
	}
}

// TestTheScopeInASentenceIsNotThrownAway.
//
// 🔴 Regression fence for a bug a user caught in the console. "how to improve prompt?" routed to the
// `improve` goal and planned a NINE-AXIS assessment of the whole repository — because the router
// returned only the verb, and the server had no subject to narrow with.
//
// The principle was already written down, in goal.Axes: "the axes ARE read from the user's request,
// because 'make my memory strategy better' is a scope and discarding it would run a nine-axis search
// somebody asked to be a one-axis search". It was a comment and not a code path.
func TestTheScopeInASentenceIsNotThrownAway(t *testing.T) {
	cases := map[string]string{
		"how to improve prompt?":               "prompt",
		"make my memory strategy better":       "memory",
		"fix the tools on this agent":          "tools",
		"can you improve the context handling": "context",
		"what is weak about my loop":           "loop",
		"improve the harness":                  "harness",
	}
	r := New()
	for text, wantAxis := range cases {
		out := r.Route(text)
		if out.Axis != wantAxis {
			t.Errorf("%q: axis = %q, want %q — a nine-axis run over a one-axis question spends "+
				"nine times what the person asked for", text, out.Axis, wantAxis)
		}
	}
}

// TestAWholeRepositoryQuestionHasNoAxis. "Tell me what is weak" genuinely means all nine, and inventing
// a scope for it would narrow a search the person deliberately left wide.
func TestAWholeRepositoryQuestionHasNoAxis(t *testing.T) {
	r := New()
	for _, text := range []string{
		"look at my repo and tell me what is weak",
		"fix it, and open a pull request",
		"review my agent",
	} {
		if out := r.Route(text); out.Axis != "" {
			t.Errorf("%q was narrowed to %q; it names no axis", text, out.Axis)
		}
	}
}

// TestTwoAxesIsNotAScope. A sentence naming two is a request whose scope the person has not settled;
// silently picking one is the same mistake pointed the other way.
func TestTwoAxesIsNotAScope(t *testing.T) {
	if got := AxisOf("compare the prompt and the model"); got != "" {
		t.Errorf("a two-axis sentence was narrowed to %q", got)
	}
}
