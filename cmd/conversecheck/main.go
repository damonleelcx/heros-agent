// Command conversecheck measures what one TURN of conversation actually costs, against the live
// provider, so that converse.DefaultBounds.MaxCostMicroCents is a measurement rather than a guess.
//
// # Why this exists as a command rather than a note in a commit message
//
// The figure in converse.DefaultBounds was first GUESSED at 5,000 micro-cents, with a comment claiming
// that was "roughly twenty times what a normal turn actually costs". A real turn billed 27,764 — the
// guess was wrong by 5.5x and in the dangerous direction, because a ceiling below the real cost of one
// call does not make anything cheaper, it makes the loop silently one call long.
//
// That correction is only durable while somebody can repeat it. The provider changed once already
// (DeepSeek → Qwen, 2026-09-03) and the bounds comment carried a measurement from a model the build no
// longer called. So the measurement is a command: `go run ./cmd/conversecheck`.
//
// 🔴 It drives the REAL converse.Agent with the REAL system prompt. The system prompt is most of the
// cost — the capability table, the axes and the facts block are well over a thousand input tokens
// before the person has typed anything — so a harness that approximates the prompt measures nothing
// worth knowing.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/heros-foreal/heros/internal/config"
	"github.com/heros-foreal/heros/internal/converse"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/provider/qwen"
)

// recorder wraps a provider and keeps the usage of every call it passes through.
//
// 🔴 Needed because converse.Outcome carries only a COST, and the total cannot answer the question that
// actually matters when a figure looks wrong: where did it go? An input-heavy turn and a turn that
// spent its budget on cached context are the same number and completely different problems — and the
// cached share is priced by an assumption this build could not verify (see qwen.DefaultPrices).
type recorder struct {
	inner provider.Provider
	calls []provider.Response
}

func (r *recorder) Name() string { return r.inner.Name() }

func (r *recorder) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	resp, err := r.inner.Complete(ctx, req)
	if err == nil {
		r.calls = append(r.calls, resp)
	}
	return resp, err
}

// drain returns the usage recorded since the last drain.
func (r *recorder) drain() (in, cached, out, reasoning int64) {
	for _, c := range r.calls {
		in += c.Usage.InputTokens
		cached += c.Usage.CachedInputTokens
		out += c.Usage.OutputTokens
		reasoning += c.Usage.ReasoningTokens
	}
	r.calls = nil
	return
}

// scenario is one shape of turn worth pricing separately.
//
// Two states, not one: the facts block is much fuller once a repository is loaded, and almost every
// real conversation happens in that state. Measuring only the empty one would understate the ceiling,
// which is the direction that silently truncates the loop.
type scenario struct {
	name     string
	facts    converse.Facts
	first    string
	followUp string
}

func main() {
	model := flag.String("model", qwen.ModelFlash, "model id to measure")
	flag.Parse()

	if err := config.LoadDotEnv(".env.local"); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	key, err := config.QwenKey()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	rec := &recorder{inner: qwen.New(key)}
	agent := converse.Agent{Provider: rec, Model: *model, Bounds: converse.DefaultBounds}

	// The loaded-subject facts mirror what api.subjectFacts builds: every axis classified as found or
	// missing, a pinned revision, and a verdict on whether the repository calls a model at all.
	axes := intent.Axes()
	loaded := converse.Facts{
		SubjectLoaded: true,
		Reference:     "github.com/heros-foreal/heros",
		Revision:      "a1b2c3d",
		IsAgent:       true,
		Why:           "calls a model provider in internal/provider",
		AxesFound:     axes[:len(axes)/2],
		AxesMissing:   axes[len(axes)/2:],
		Person: converse.Person{
			DisplayName: "Damon", Role: "platform engineer", ReplyLanguage: "English",
		},
	}

	scenarios := []scenario{
		{
			name:     "cold (no repository loaded)",
			facts:    converse.Facts{SubjectLoaded: false},
			first:    "hi",
			followUp: "what can you actually do for me?",
		},
		{
			name:     "loaded repository, person known",
			facts:    loaded,
			first:    "is my retry loop any good?",
			followUp: "and what about the memory strategy?",
		},
	}

	fmt.Printf("model            %s\n", *model)
	fmt.Printf("ceiling          %d micro-cents/turn (converse.DefaultBounds)\n",
		converse.DefaultBounds.MaxCostMicroCents)
	fmt.Printf("MaxTokens        %d   MaxCalls %d   HistoryWindow %d\n\n",
		converse.DefaultBounds.MaxTokens, converse.DefaultBounds.MaxCalls,
		converse.DefaultBounds.HistoryWindow)
	fmt.Printf("%-30s %-9s %5s %6s %6s %6s %8s %8s  %s\n",
		"SCENARIO", "TURN", "CALLS", "IN", "CACHED", "OUT", "COST", "HEADROOM", "OUTCOME")

	var worst int64
	for _, sc := range scenarios {
		// A first turn carries no history — this is the expensive one, because the whole system prompt
		// is paid for with nothing amortised.
		first, ok := run(agent, rec, sc.name, "first", sc.first, nil, sc.facts, &worst)
		if !ok {
			continue
		}

		// The follow-up carries the first exchange, which is what a real second sentence looks like.
		history := []memory.Turn{
			{Role: memory.TurnUser, Body: sc.first},
			{Role: memory.TurnAgent, Body: first},
		}
		run(agent, rec, sc.name, "follow-up", sc.followUp, history, sc.facts, &worst)

		// 🔴 The WORST legitimate turn, not the typical one. HistoryWindow is 12, so a conversation
		// that has been going for a while sends twelve prior turns with every sentence. A ceiling
		// validated only against a two-turn history is a ceiling nobody has tested against the shape
		// the product actually reaches — and the first person to have a long conversation finds out.
		run(agent, rec, sc.name, "full window", sc.followUp,
			fullWindow(sc.first, first, converse.DefaultBounds.HistoryWindow), sc.facts, &worst)
	}

	fmt.Println()
	ceiling := converse.DefaultBounds.MaxCostMicroCents
	fmt.Printf("most expensive turn observed: %d micro-cents (%s)\n", worst, dollars(worst))
	if worst == 0 {
		fmt.Println("no turn was priced — nothing can be concluded about the ceiling")
		os.Exit(1)
	}
	fmt.Printf("ceiling is %.1fx the most expensive turn observed\n", float64(ceiling)/float64(worst))
	if worst >= ceiling {
		fmt.Printf("\n‼️  A REAL TURN MEETS OR EXCEEDS THE CEILING. Raise MaxCostMicroCents: a ceiling\n" +
			"    below the real cost of one call makes the loop silently one call long.\n")
		os.Exit(2)
	}
}

// fullWindow builds a history that fills HistoryWindow completely, alternating roles, using turns of
// realistic length rather than one-word stubs — a window of "hi"/"ok" would measure nothing.
func fullWindow(userSeed, agentSeed string, window int) []memory.Turn {
	padding := " I want to understand whether this part of the agent is sound, and what you would " +
		"change first if it is not."
	var out []memory.Turn
	for len(out) < window {
		out = append(out, memory.Turn{Role: memory.TurnUser, Body: userSeed + padding})
		if len(out) >= window {
			break
		}
		out = append(out, memory.Turn{Role: memory.TurnAgent, Body: agentSeed + padding})
	}
	return out
}

// run performs one turn and prints its line. Returns the agent's reply, for use as history.
func run(a converse.Agent, rec *recorder, name, label, text string, history []memory.Turn, facts converse.Facts, worst *int64) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	out, err := a.Interpret(ctx, history, text, facts)
	elapsed := time.Since(start)

	in, cached, outTok, reasoning := rec.drain()
	if err != nil {
		// The cost is still printed: a turn that failed after spending is exactly the case the ceiling
		// exists for, and hiding its cost would flatter the measurement.
		fmt.Printf("%-30s %-9s %5d %6d %6d %6d %8d %8s  FAILED: %v\n",
			trunc(name, 30), label, out.Calls, in, cached, outTok, out.CostMicroCents, "-", err)
		return "", false
	}
	_ = reasoning
	if out.CostMicroCents > *worst {
		*worst = out.CostMicroCents
	}
	verdict := "said"
	if !out.Talked() {
		verdict = "chose " + string(out.Capability)
	}
	reply := out.Say
	if reply == "" {
		reply = out.Ask
	}
	headroom := fmt.Sprintf("%.1fx", float64(converse.DefaultBounds.MaxCostMicroCents)/float64(max64(out.CostMicroCents, 1)))
	_ = elapsed
	fmt.Printf("%-30s %-9s %5d %6d %6d %6d %8d %8s  %s: %s\n",
		trunc(name, 30), label, out.Calls, in, cached, outTok, out.CostMicroCents, headroom,
		verdict, trunc(oneLine(reply), 34))
	return reply, true
}

func dollars(microCents int64) string {
	return fmt.Sprintf("$%.6f", float64(microCents)/1_000_000/100)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func oneLine(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		out = append(out, r)
	}
	return string(out)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
