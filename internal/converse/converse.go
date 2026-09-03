// Package converse is the agent that reads a sentence and decides what to do about it.
//
// # What this replaces, and why the thing it replaces was not wrong
//
// Routing used to be `internal/router`: a hand-written table of weighted keyword phrases, scored, with
// anything below a threshold abstaining. That design was argued for explicitly and the argument was
// good — routing decides whether to SPEND MONEY, so it should not itself cost money on every keystroke;
// it must be testable against a fixed holdout, and a model's answers move under you; and it has to be
// fast enough to answer before the person has finished reading their own sentence.
//
// What that argument did not price was the product. A person's first sentence is "hi", which scores
// zero, and the console answered it with the whole list of nineteen capabilities and "I cannot route
// that". Every phrasing outside the vocabulary landed the same way. The surface promised a conversation
// and was a lookup table with a text box in front of it.
//
// So the trade was made deliberately, and it IS a trade — see §Cost at the bottom.
//
// # 🔴 What did NOT move, and must not
//
// The deterministic safety floor still runs FIRST, in `api.decide`, before this package is consulted:
//
//   - An unbounded request ("keep going until it is perfect") is refused before anything is planned.
//   - A sentence about billing, passwords, membership, or connecting a repository is redirected to the
//     page that owns it. Connecting a repository creates a standing read grant, and its disclosure must
//     be displayed before the grant exists — so that decision may never depend on something that can be
//     talked out of it.
//
// This package can be persuaded. The floor cannot, which is why the floor is where those two live.
//
// # 🔴 The action surface is CLOSED even though the conversation is open
//
// The agent may say anything. It may only DO one of `intent.All()`. That asymmetry is the whole design:
// `Tier` is the single discriminator for every spend ceiling and approval gate in the system, so an
// action outside the closed set is an action with nothing to hang a ceiling on. The model chooses among
// capabilities; it does not invent ways to spend money.
//
// # 🔴 Failure here is never failure for the person
//
// Every error path returns one, and `api.decide` falls back to the keyword router. The worst outcome
// of an outage, a rate limit, a timeout or unparseable JSON is that the console behaves the way it did
// before this package existed — and says so. A conversational surface that 500s when its provider is
// having a bad afternoon is worse than a blunt one that works.
//
// # Cost
//
// Understanding now costs a model call per turn and takes about a second where it used to take
// microseconds. `internal/router`'s speed argument was real and is knowingly given up. Its holdout
// argument is answered differently rather than abandoned: the labelled set still runs against the
// router, which is still the live fallback and must still be correct.
package converse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/provider"
)

// Errors this package returns. Typed because the caller does one thing with all of them — degrade —
// but the LOG has to say which happened: a rate limit and a prompt the model will never satisfy need
// completely different responses from whoever is on call.
var (
	// ErrUnavailable — the provider could not be reached or refused the call. Transient.
	ErrUnavailable = errors.New("converse: the model could not be reached")
	// ErrUnusable — the model answered, and the answer was not something this package can act on:
	// unparseable, or naming a capability that does not exist.
	ErrUnusable = errors.New("converse: the model's reply could not be used")
	// ErrExhausted — the turn hit its own ceiling before reaching a decision.
	ErrExhausted = errors.New("converse: the turn reached its ceiling without deciding")
)

// Bounds is what one TURN of conversation may consume.
//
// # 🔴 Why a turn has a ceiling at all
//
// It used to be true that answering a question cost nothing — `answerQuery` said so in a comment and
// the console said so to the customer. It is not true any more, and an unbounded per-turn cost is a
// customer holding the send key. These are deliberately small: a turn is one short reply, not a run.
//
// Denominated the same way `bounds.Ceilings` is — calls, tokens, and money — so the two read alike.
// Not REUSING `bounds.Ceilings`: two thirds of its fields (leases, spawn depth, wall clock, tasks) are
// about a durable DAG and would sit here permanently unset, which is how a struct starts lying about
// what it constrains.
type Bounds struct {
	// MaxCalls bounds model round trips within one turn. One is enough to decide; the loop exists so
	// that read-then-answer can be added without restructuring anything.
	MaxCalls int
	// MaxTokens bounds each individual completion. Required by provider.ValidateRequest, and for the
	// reason it states: a completion with no output bound is an unbounded spend inside a single call,
	// which no outer ceiling can interrupt.
	MaxTokens int
	// MaxCostMicroCents bounds the whole turn.
	//
	// 🔴 Checked BETWEEN calls, so it can only ever stop the NEXT one. The first call of a turn is
	// bounded by MaxTokens alone — which is why MaxTokens is not optional and why this number must sit
	// above the real cost of a single call rather than below it. A ceiling under one call's cost does
	// not save money; it silently shortens the loop to one step.
	//
	// Micro-cents because a turn costs a fraction of a cent, and a ledger in whole cents records every
	// one of them as zero.
	MaxCostMicroCents int64
	// HistoryWindow is how many past turns the model sees. See §conversation in prompt.go for why this
	// is a window rather than a summary.
	HistoryWindow int
}

// DefaultBounds is one short reply, with room for a read step that P3 will add.
//
// # 🔴 The money figure is MEASURED, not guessed, and the guess was wrong by 5.5x
//
// It was first set to 5,000 micro-cents with a comment claiming that was "roughly twenty times what a
// normal turn actually costs". Then a real turn on deepseek-v4-flash was run and billed 27,764
// micro-cents — the opposite direction. The system prompt is most of it: the capability table, the axes
// and the facts block are around 1,500 input tokens before the person has said anything, and they are
// sent on every turn.
//
// Measured on 2026-09-01, deepseek-v4-flash: a first turn costs ~28,000 micro-cents and a follow-up in
// the same conversation ~11,000. So 150,000 leaves room for the full four-call loop and still stops a
// runaway. In money that is about $0.0015 a turn at the ceiling and about $0.0003 in practice.
//
// 🚫 Do not "tidy" this to a round number without re-measuring. A ceiling below the real cost of one
// call does not make anything cheaper — it makes the loop effectively one call long, silently, and the
// read step P3 adds would never get to run.
//
// # Re-measured on qwen3.8-flash, 2026-09-03, and the figure still holds
//
// The DeepSeek numbers above are kept because they are the evidence for why this is 150,000 rather
// than the 5,000 somebody guessed. But the provider changed, so the figure was re-measured against
// the live service with `go run ./cmd/conversecheck` — which drives THIS agent with THIS system
// prompt, because the prompt is most of the cost:
//
//	cold, no repository, first turn      1,123 in / 48 out    17,738 µ¢
//	loaded repository, first turn        1,223 in / 27 out    18,256 µ¢
//	full 12-turn history window          1,748 in / 84 out    28,000 µ¢   ← worst legitimate turn
//
// Repeated three times: the worst turn landed at 26,698 / 27,034 / 27,622 / 28,000 µ¢, so the figure
// is stable and 28,000 is the conservative end of it, not a single lucky reading.
//
// So 150,000 is 5.4x the most expensive real turn, and a full four-call loop at that price is
// 112,000 — under the ceiling, with MaxCalls binding first. That is the same arithmetic the DeepSeek
// figure produced, which is why the number does not move.
//
// 🔴 The WORST case is the one that matters and it is NOT the first turn. HistoryWindow is 12, so a
// conversation that has been going a while sends twelve prior turns with every sentence — that is the
// 28,000 row, and it costs ~55% more than the two-turn case anyone would reach for when spot-checking.
//
// 🔴 These figures OVERSTATE the real bill, deliberately. Qwen served 1,024 cached input tokens on
// every single call, but qwen.DefaultPrices charges cached input at the FULL input rate because the
// cached rate is not published anywhere that could be found. Cached tokens are ~81% of the input cost
// here, so the true bill is materially lower than what the ledger records. That is the safe direction
// — and it is why the DeepSeek follow-up looked cheaper than its first turn (that price table DID
// model a 30x cache discount) while these rows stay flat.
var DefaultBounds = Bounds{
	MaxCalls:          4,
	MaxTokens:         700,
	MaxCostMicroCents: 150_000,
	HistoryWindow:     12,
}

// Valid refuses bounds that do not bound.
//
// 🔴 Refused rather than defaulted, the same way provider.ValidateRequest refuses an unset Reasoning:
// a zero here is a forgotten field, and the failure mode of quietly filling it in is that nobody ever
// finds out the ceiling they thought they set was never applied.
func (b Bounds) Valid() error {
	switch {
	case b.MaxCalls <= 0:
		return fmt.Errorf("converse: MaxCalls is %d — a loop with no bound is not a turn", b.MaxCalls)
	case b.MaxTokens <= 0:
		return fmt.Errorf("converse: MaxTokens is %d — an unbounded completion cannot be interrupted "+
			"by any outer ceiling", b.MaxTokens)
	case b.MaxCostMicroCents <= 0:
		return fmt.Errorf("converse: MaxCostMicroCents is %d — a turn with no money ceiling is a "+
			"customer holding the send key", b.MaxCostMicroCents)
	}
	return nil
}

// Agent decides what one sentence means.
type Agent struct {
	Provider provider.Provider
	Model    string
	Bounds   Bounds
}

// Outcome is what the agent decided. Exactly one of Say, Ask or Capability is set.
type Outcome struct {
	// Say is a reply in the agent's own words. Nothing runs.
	Say string
	// Ask is a question back, when what was wanted is genuinely ambiguous. Nothing runs.
	//
	// 🔴 Kept SEPARATE from Say although both are prose the console prints. They are different acts: one
	// ends a turn, the other is waiting for an answer, and a transcript that cannot tell them apart
	// cannot later be evaluated for how often the agent stalls instead of deciding.
	Ask string
	// Capability is the chosen action, empty when the agent only talked.
	Capability intent.Intent
	// Axis is the scope it understood, empty when none was named.
	Axis string
	// Why is the agent's own one-line account of what it understood, shown to the person BEFORE a
	// spending or writing capability runs. 🔴 In its words rather than the capability's `Question`: a
	// person confirming "look at my repository and tell me what is weak" is confirming a label, while
	// "you want me to check whether the retry loop is sound" is something they can actually catch as
	// wrong.
	Why string
	// CostMicroCents and Calls are what this turn consumed, for the ledger and for the ceiling.
	CostMicroCents int64
	Calls          int
}

// Talked reports that the agent replied without choosing an action.
func (o Outcome) Talked() bool { return o.Capability == "" }

// wire is the model's reply. Small on purpose: every field is something the agent must be able to
// justify, and a field it can fill freely is a field it will fill freely.
type wire struct {
	Action     string `json:"action"`
	Text       string `json:"text"`
	Capability string `json:"capability"`
	Axis       string `json:"axis"`
	Why        string `json:"why"`
}

// Interpret turns one sentence, in the context of the conversation so far, into an Outcome.
//
// The caller has ALREADY applied the deterministic floor. Anything reaching here is a sentence the
// system is willing to let a model think about.
func (a Agent) Interpret(ctx context.Context, history []memory.Turn, text string, facts Facts) (Outcome, error) {
	return a.InterpretStream(ctx, history, text, facts, nil)
}

// InterpretStream is Interpret, plus best-effort prose deltas as the reply is written.
//
// # 🔴 onText is DECORATION. The Outcome is unchanged by it
//
// The turn is still decided by unmarshalling the COMPLETE object and validating every field, exactly as
// it was before streaming existed. onText is fed by a scanner reading the same bytes on their way past
// (see stream.go); if it emits nothing, or the provider cannot stream, or a delta is misread, the
// caller still gets the identical Outcome. That is the only arrangement in which a display improvement
// cannot cost somebody their answer.
//
// A nil onText, or a provider that does not implement provider.StreamingProvider, takes the ordinary
// non-streaming path. Both remain first-class: the console streams, everything else does not, and
// neither has a second copy of this loop.
//
// 🔴 The caller must treat the final Outcome as authoritative and REPLACE whatever it displayed from
// deltas, not append to it. The loop may make more than one call — Bounds.MaxCalls is 4 so a read step
// can be added — and text streamed by a call that then did not decide is text that was never part of
// the answer.
func (a Agent) InterpretStream(ctx context.Context, history []memory.Turn, text string, facts Facts,
	onText func(string)) (Outcome, error) {
	if a.Provider == nil || a.Model == "" {
		return Outcome{}, fmt.Errorf("%w: no provider is configured", ErrUnavailable)
	}
	if err := a.Bounds.Valid(); err != nil {
		return Outcome{}, err
	}

	msgs := make([]provider.Message, 0, len(history)+2)
	msgs = append(msgs, provider.Message{Role: "system", Content: systemPrompt(facts)})
	msgs = append(msgs, conversation(history, a.Bounds.HistoryWindow)...)
	msgs = append(msgs, provider.Message{Role: "user", Content: text})

	var out Outcome
	for out.Calls < a.Bounds.MaxCalls {
		// Checked BEFORE the call, not after: a ceiling tested only afterwards has already been
		// exceeded by the call that tested it.
		if out.CostMicroCents >= a.Bounds.MaxCostMicroCents {
			return out, fmt.Errorf("%w: %d micro-cents spent against a ceiling of %d",
				ErrExhausted, out.CostMicroCents, a.Bounds.MaxCostMicroCents)
		}

		temp := 0.0
		call := provider.Request{
			Model:     a.Model,
			MaxTokens: a.Bounds.MaxTokens,
			// 🔴 No chain of thought, and temperature zero. This call chooses among a listed set and
			// writes two sentences; it is not solving anything. With thinking on at the provider's
			// default `high` effort, reasoning tokens are billed as output and eat MaxTokens before the
			// JSON begins — which is how a 700-token budget truncates on a reply that needed 80.
			//
			// Turning reasoning off is also what makes temperature effective at all, and temperature
			// zero is what makes the same sentence route the same way twice. A conversational surface
			// that answers differently on a retry cannot be evaluated and cannot be supported.
			Reasoning:   provider.NoReasoning,
			Temperature: &temp,
			JSONObject:  true,
			Messages:    msgs,
		}

		// Streaming is taken only when BOTH the provider offers it and the caller wants it. A fresh
		// scanner per call: text from a previous call in this loop is not part of this one's reply.
		var resp provider.Response
		var err error
		streamer, canStream := a.Provider.(provider.StreamingProvider)
		if canStream && onText != nil {
			var scan textFieldStream
			resp, err = streamer.CompleteStream(ctx, call, func(d provider.Delta) {
				if d.Text == "" {
					return
				}
				if piece := scan.Write(d.Text); piece != "" {
					onText(piece)
				}
			})
		} else {
			resp, err = a.Provider.Complete(ctx, call)
		}
		out.Calls++
		if err != nil {
			// Usage is unknown on a failed call, so nothing is added to the ledger — but the attempt is
			// counted, because a provider failing repeatedly must still exhaust the loop rather than
			// spinning inside it.
			return out, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		out.CostMicroCents += resp.CostMicroCents

		// 🔴 Truncation is reported as its own thing rather than being left to the parser. A reply cut
		// mid-object always fails to parse, and the parse error would name the wrong cause — sending
		// somebody to look at the prompt when the fix is a bigger budget.
		if resp.Truncated() {
			return out, fmt.Errorf("%w: the reply was cut off at %d tokens", ErrUnusable, a.Bounds.MaxTokens)
		}

		var w wire
		if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &w); err != nil {
			return out, fmt.Errorf("%w: not the requested JSON object: %v", ErrUnusable, err)
		}
		decided, err := a.apply(&out, w)
		if err != nil {
			return out, err
		}
		if decided {
			return out, nil
		}
	}
	return out, fmt.Errorf("%w: %d calls", ErrExhausted, out.Calls)
}

// apply validates one reply and folds it into the outcome. The bool says whether the turn is decided.
//
// 🔴 Every field the model supplies is CHECKED, not trusted. It is asked to choose from a list, so the
// only interesting question about its answer is whether it actually did — and "capability": "refactor"
// has to be a refusal here rather than an empty dispatch two layers down, where the symptom would be a
// blank reply nobody can trace back to this.
func (a Agent) apply(out *Outcome, w wire) (bool, error) {
	switch w.Action {
	case "say", "ask":
		text := strings.TrimSpace(w.Text)
		if text == "" {
			return false, fmt.Errorf("%w: %q with no text", ErrUnusable, w.Action)
		}
		if w.Action == "ask" {
			out.Ask = text
		} else {
			out.Say = text
		}
		return true, nil

	case "do":
		i := intent.Intent(strings.TrimSpace(w.Capability))
		if !i.Valid() {
			return false, fmt.Errorf("%w: %q is not one of the capabilities", ErrUnusable, w.Capability)
		}
		spec, _ := intent.Lookup(i)
		out.Capability = i
		out.Why = strings.TrimSpace(w.Why)
		out.Axis = a.axisFor(spec, w.Axis)
		if out.Why == "" {
			// 🔴 Filled rather than refused. `why` is shown to a person before a run starts, so its
			// absence must not be the thing that takes the turn down — but it must not be BLANK either,
			// because an empty confirmation prompt is one people click through.
			out.Why = spec.Question
		}
		return true, nil

	default:
		return false, fmt.Errorf("%w: unknown action %q", ErrUnusable, w.Action)
	}
}

// axisFor decides the scope, refusing an axis the system does not have.
//
// 🔴 An unrecognised axis becomes EMPTY rather than an error. Scope is an optimisation — it narrows a
// run that would otherwise cover all nine — so a model naming "retries" instead of "harness" should
// cost breadth, not the whole turn. A capability that is ABOUT one axis keeps its own, which is the
// table's answer and not the model's.
func (a Agent) axisFor(spec intent.Spec, named string) string {
	if spec.Axis != "" {
		return spec.Axis
	}
	named = strings.ToLower(strings.TrimSpace(named))
	for _, axis := range intent.Axes() {
		if axis == named {
			return axis
		}
	}
	return ""
}

// NeedsConfirmation reports whether a capability must be confirmed by a person before it runs.
//
// # 🔴 One predicate over the existing discriminator, not a second list
//
// Anything that is not a read either spends money on a durable run or writes to somebody's repository,
// and `Tier` is already the single discriminator the whole system uses for that. Enumerating the eight
// affected capabilities here would be a second copy of a fact `intent` already owns — and the copy
// would be wrong the first time a capability changed tier, silently, in the permissive direction.
//
// The customer's decision was "anything that spends or writes is confirmed first, every time", rather
// than confirming only below some confidence. The cost is one extra tap on eight of nineteen
// capabilities; the eleven read-only ones answer straight through.
func NeedsConfirmation(i intent.Intent) bool {
	spec, ok := intent.Lookup(i)
	return ok && spec.Tier != intent.TierQuery
}
