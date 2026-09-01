// Package router turns a sentence into one of the nineteen intents, a named redirection, or an
// abstention.
//
// # Why the router ABSTAINS rather than guessing
//
// The cost of the two mistakes is not symmetric. An abstention costs one exchange: the person is shown
// the finite list of what this surface does and asks again. A wrong route costs money and trust — it
// runs a durable goal the person did not ask for, or answers confidently about the wrong axis, and the
// conversational surface is precisely where a reader is least equipped to notice they were sent
// somewhere else.
//
// So the router scores, and a score below AbstainThreshold produces no route at all.
//
// # 🔴 Why this is deterministic and not a model call
//
// Three reasons, in order of weight. Routing decides whether to SPEND MONEY, and a component that
// decides whether to spend money should not itself cost money on every keystroke. It must be testable
// against a fixed holdout, and a model's answers move under you. And it has to be fast enough to run
// before the person has finished reading their own sentence.
//
// The honest cost is that phrasings outside the vocabulary abstain rather than route. That is the
// failure this design chooses, and §Evaluate measures how often it happens.
package router

import (
	"sort"
	"strings"

	"github.com/heros-foreal/heros/internal/intent"
)

// AbstainThreshold is the minimum score for a route. Below it the router says it cannot route.
//
// 🔴 A single tunable rather than per-intent thresholds: per-intent values would be tuned one at a time
// against whichever example was in front of somebody, and the resulting surface would route `memory`
// eagerly and `coverage` never, for reasons nobody wrote down.
const AbstainThreshold = 2

// MinRecall is the floor each intent's recall must clear on the holdout.
const MinRecall = 0.80

// Outcome is what the router decided.
type Outcome struct {
	// Intent is the route, empty when the router abstained or redirected.
	Intent intent.Intent
	// Redirect is set when the sentence names something done on another surface.
	Redirect *intent.OutOfScopeTopic
	// Score is the winning score, for diagnosis.
	Score int
	// Axis is the scope the sentence named, empty when it named none or more than one. Carried beside
	// the intent because a request is a verb AND a subject, and dropping the subject runs the verb over
	// everything.
	Axis string
	// Runner-up and its score, so a near-miss is visible when someone asks why a sentence routed where
	// it did. A router that cannot explain itself gets tuned by superstition.
	RunnerUp      intent.Intent
	RunnerUpScore int
}

// Abstained reports that nothing was routed.
func (o Outcome) Abstained() bool { return o.Intent == "" && o.Redirect == nil }

// signal is a phrase and what it is worth.
type signal struct {
	phrase string
	weight int
}

// vocabulary maps each intent to its signals.
//
// # 🔴 Why weights, and why multi-word phrases score higher
//
// A single word is ambiguous across axes — "memory" appears in a question about memory AND in "how much
// memory does this use". A phrase is not. So multi-word signals carry more weight, and a single generic
// word carries one point, which alone is below the threshold. The effect is that a bare noun abstains
// and a sentence routes, which is the behaviour a person actually experiences as "it understood me".
var vocabulary = map[intent.Intent][]signal{
	// ── Tier A · durable goals ────────────────────────────────────────────────────────────────
	intent.Assess: {
		{"what is weak", 4}, {"whats weak", 4}, {"look at my repo", 4}, {"look at my repository", 4},
		{"assess", 3}, {"review my agent", 4}, {"audit", 3}, {"health check", 3},
		{"what could be better", 4}, {"whats wrong with", 4}, {"tell me what", 2}, {"weak", 1},
	},
	intent.Improve: {
		{"fix it", 4}, {"fix them", 4}, {"open a pull request", 4}, {"open a pr", 4},
		{"make it better", 4}, {"improve", 3}, {"apply the fix", 4}, {"go ahead and fix", 4},
	},
	intent.EvalSet: {
		{"eval set", 4}, {"evaluation set", 4}, {"eval suite", 4}, {"test cases", 3},
		{"build me an eval", 4}, {"generate evals", 4}, {"benchmark", 3}, {"eval", 1},
	},
	intent.Compare: {
		{"better than", 4}, {"compare", 3}, {"versus", 3}, {"vs", 1}, {"did it help", 4},
		{"regression", 3}, {"which version", 4},
	},
	// ── Tier B · queries ──────────────────────────────────────────────────────────────────────
	intent.Graph: {
		{"what does my agent do", 4}, {"step by step", 3}, {"show me the graph", 4},
		{"what does it do", 3}, {"how does it work", 3},
	},
	intent.GraphOrder: {
		{"run in this order", 4}, {"right order", 4}, {"should these run", 4}, {"ordering", 3},
		{"sequence", 2}, {"topology", 3},
	},
	intent.RunHistory: {
		{"what happened", 4}, {"that run", 3}, {"last run", 4}, {"run history", 4}, {"trace", 1},
	},
	intent.Coverage: {
		{"what did you measure", 4}, {"what did you not", 4}, {"coverage", 3}, {"how much did you check", 4},
	},
	intent.Context: {
		{"conversation history", 4}, {"what history", 4}, {"context window", 4},
		{"message list", 4}, {"context", 1},
	},
	intent.Memory: {
		{"remember between", 4}, {"what does it remember", 4}, {"between sessions", 4},
		{"persist", 3}, {"memory", 1},
	},
	intent.Harness: {
		{"allowed to do", 4}, {"what can it reach", 4}, {"what can it spend", 4},
		{"sandbox", 3}, {"permissions", 3}, {"ceiling", 3}, {"harness", 1},
	},
	intent.Loop: {
		{"how many turns", 4}, {"max turns", 4}, {"what loop", 4}, {"iterations", 3},
		{"stop condition", 4}, {"loop", 1},
	},
	intent.Skills: {
		{"what skills", 4}, {"skills bound", 4}, {"which skills", 4}, {"skill", 1},
	},
	intent.Tools: {
		{"what tools", 4}, {"which tools", 4}, {"tools does", 4}, {"tool calls", 3},
		{"unused tool", 4}, {"tools", 1}, {"tool", 1},
	},
	intent.PreviewChange: {
		{"would you write", 4}, {"show me the diff", 4}, {"preview", 3}, {"what would change", 4},
		{"would change if", 4}, {"see the change first", 4}, {"diff", 2},
	},
	// ── Tier C · effects ──────────────────────────────────────────────────────────────────────
	intent.Author: {
		{"change something on", 4}, {"edit the axis", 4}, {"author a change", 4},
		{"author", 1}, {"axis config", 3},
	},
	intent.Prompt: {
		{"change the prompt", 4}, {"change what it is told", 4}, {"rewrite the prompt", 4},
		{"system prompt", 3}, {"prompt", 1},
	},
	intent.Model: {
		{"change the model", 4}, {"switch model", 4}, {"use a different model", 4},
		{"which model", 3}, {"model", 1},
	},
	intent.Deliver: {
		{"reach my repository", 4}, {"how does a change get", 4}, {"delivery", 3}, {"merge", 3},
	},
}

// unboundedSignals are phrases that ask for a run with no limit. Detected HERE rather than after
// planning, because the refusal has to happen before anything is spent — which is the entire point of
// refusing.
var unboundedSignals = []string{
	"until it is perfect", "until its perfect", "keep going until", "as many as it takes",
	"no limit", "unlimited", "forever", "until you run out", "everything you can find",
}

// axisWords maps the words a person uses to the axis they mean.
//
// 🔴 Separate from the intent vocabulary, because an axis is a SCOPE rather than a goal. "How do I
// improve the prompt?" names both — the thing to do (improve) and the thing to do it to (prompt) — and
// collapsing them loses one of the two. Which is exactly what happened: the sentence routed to `improve`
// and planned a nine-axis run over a repository, when the person had named one axis.
var axisWords = map[string][]string{
	"prompt":  {"prompt", "prompts", "instruction", "instructions", "system message"},
	"model":   {"model", "models", "temperature", "sampling"},
	"tools":   {"tool", "tools", "function calling"},
	"skills":  {"skill", "skills", "capability", "capabilities"},
	"context": {"context", "history", "message list", "context window", "conversation history"},
	"memory":  {"memory", "remember", "persistence", "recall"},
	"harness": {"harness", "permission", "permissions", "sandbox", "timeout", "retries", "ceiling"},
	"loop":    {"loop", "loops", "turns", "iteration", "iterations", "stop condition"},
	"graph":   {"graph", "topology", "edges", "ordering", "wiring"},
}

// AxisOf returns the axis a sentence names, or empty when it names none or more than one.
//
// 🔴 Ambiguity yields EMPTY rather than a guess. A sentence naming two axes is a request whose scope the
// person has not settled, and picking one silently narrows a search they did not narrow — the mirror of
// the bug this function exists to fix, and just as invisible.
func AxisOf(text string) string {
	q := normalise(text)
	found := map[string]bool{}
	for axis, words := range axisWords {
		for _, w := range words {
			if containsWord(q, w) {
				found[axis] = true
				break
			}
		}
	}
	if len(found) != 1 {
		return ""
	}
	for axis := range found {
		return axis
	}
	return ""
}

// Router routes sentences.
type Router struct{}

// New builds a router.
func New() Router { return Router{} }

// Unbounded reports whether a sentence asks for a run with no bound.
func Unbounded(text string) bool {
	q := normalise(text)
	for _, s := range unboundedSignals {
		if strings.Contains(q, s) {
			return true
		}
	}
	return false
}

// Route scores a sentence against the vocabulary.
//
// Redirections are checked FIRST. "Change my password" contains "change", and the redirection is the
// correct answer regardless of what else the sentence scores — an agent that offers to change a password
// has crossed from answering about a system to administering an account.
func (Router) Route(text string) Outcome {
	q := normalise(text)
	if q == "" {
		return Outcome{}
	}
	for _, t := range intent.OutOfScopeTopics() {
		// 🔴 WORD-BOUNDARY, not substring. `strings.Contains` matched "member" inside "remember", so
		// "what does this node remember between calls" redirected to the members page — a redirection is
		// the one outcome that beats every keyword score, so a false one is unrecoverable within the
		// turn. Caught by the holdout at 33% recall on `memory`.
		if containsWord(q, normalise(t.Topic)) {
			topic := t
			return Outcome{Redirect: &topic, Score: 4}
		}
	}

	scores := map[intent.Intent]int{}
	for i, sigs := range vocabulary {
		for _, s := range sigs {
			if containsWord(q, s.phrase) {
				scores[i] += s.weight
			}
		}
	}
	if len(scores) == 0 {
		return Outcome{}
	}

	type scored struct {
		i intent.Intent
		n int
	}
	ranked := make([]scored, 0, len(scores))
	for i, n := range scores {
		ranked = append(ranked, scored{i, n})
	}
	// Sorted by score, then by name — deterministic, so a tie resolves the same way on every machine and
	// a holdout result is reproducible.
	sort.Slice(ranked, func(a, b int) bool {
		if ranked[a].n != ranked[b].n {
			return ranked[a].n > ranked[b].n
		}
		return ranked[a].i < ranked[b].i
	})

	out := Outcome{Score: ranked[0].n, Axis: AxisOf(text)}
	if len(ranked) > 1 {
		out.RunnerUp, out.RunnerUpScore = ranked[1].i, ranked[1].n
	}
	if ranked[0].n < AbstainThreshold {
		return out // abstain, but carry the scores so a near-miss is diagnosable
	}
	out.Intent = ranked[0].i
	return out
}

// containsWord reports whether phrase appears in q on word boundaries. Both are already normalised to
// space-separated lowercase words, so this is a slice comparison over the word list rather than a
// regular expression — cheaper, and it cannot be defeated by punctuation the normaliser already removed.
func containsWord(q, phrase string) bool {
	if phrase == "" {
		return false
	}
	hay, needle := strings.Fields(q), strings.Fields(phrase)
	if len(needle) == 0 || len(needle) > len(hay) {
		return false
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// normalise lowercases and strips punctuation, so "what tools?" and "What tools" are one thing.
func normalise(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == ' ':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '/':
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
