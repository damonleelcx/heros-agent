package converse

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/provider"
)

// fake is a provider that answers from a script.
//
// 🔴 It records the REQUEST as well as answering, because half of what this package does is decide what
// the model is shown. A fake that only returns canned replies leaves the prompt — the part that decides
// whether a capability is reachable at all — completely untested.
type fake struct {
	replies []string
	errs    []error
	seen    []provider.Request
	// truncate makes the next reply report itself cut off at the token limit.
	truncate bool
	// cost is charged per call, in micro-cents.
	cost int64
}

func (f *fake) Name() string { return "fake" }

func (f *fake) Complete(_ context.Context, req provider.Request) (provider.Response, error) {
	f.seen = append(f.seen, req)
	i := len(f.seen) - 1
	if i < len(f.errs) && f.errs[i] != nil {
		return provider.Response{}, f.errs[i]
	}
	body := ""
	if i < len(f.replies) {
		body = f.replies[i]
	}
	resp := provider.Response{Content: body, Model: req.Model, CostMicroCents: f.cost}
	if f.truncate {
		resp.FinishReason = "length"
	}
	return resp, nil
}

func agentWith(f *fake) Agent {
	return Agent{Provider: f, Model: "fake-model", Bounds: DefaultBounds}
}

func noFacts() Facts { return Facts{} }

// TestAGreetingGetsAReplyRatherThanTheCatalogue.
//
// # The whole reason this package exists
//
// A person's first sentence is "hi". It scored zero against the keyword vocabulary, so the console
// answered it with all nineteen capabilities and "I cannot route that" — a surface that promised a
// conversation and behaved like a lookup table with a text box in front of it.
//
// 🔴 The assertion is two-sided. That it REPLIES is half the property; the other half is that replying
// did not quietly start something. A greeting must not become a run.
func TestAGreetingGetsAReplyRatherThanTheCatalogue(t *testing.T) {
	f := &fake{replies: []string{`{"action":"say","text":"Hello. Point me at a repository and I will read it."}`}}
	out, err := agentWith(f).Interpret(context.Background(), nil, "hi", noFacts())
	if err != nil {
		t.Fatalf("interpret: %v", err)
	}
	if !out.Talked() {
		t.Fatalf("a greeting chose the %q capability; nothing may run from 'hi'", out.Capability)
	}
	if out.Say == "" {
		t.Fatal("a greeting produced no reply at all")
	}
	if strings.Contains(out.Say, "cannot route") {
		t.Errorf("the reply still reads like an abstention: %q", out.Say)
	}
}

// TestAnInventedCapabilityIsRefusedHereRatherThanDispatchedEmpty.
//
// 🔴 The model is asked to choose from a list, so the only interesting question about its answer is
// whether it actually did. An unrecognised name has to fail HERE, where the cause is obvious, rather
// than reaching a dispatch two layers down — where the symptom would be a blank reply that nobody can
// trace back to a model that made something up.
//
// This is also the fence on the closed action surface: the conversation is open, the actions are not.
func TestAnInventedCapabilityIsRefusedHereRatherThanDispatchedEmpty(t *testing.T) {
	for _, name := range []string{"refactor", "delete_everything", "", "ASSESS"} {
		f := &fake{replies: []string{fmt.Sprintf(`{"action":"do","capability":%q,"why":"x"}`, name)}}
		out, err := agentWith(f).Interpret(context.Background(), nil, "do the thing", noFacts())
		if !errors.Is(err, ErrUnusable) {
			t.Errorf("capability %q returned %v, want ErrUnusable", name, err)
		}
		if out.Capability != "" {
			t.Errorf("capability %q was accepted as %q", name, out.Capability)
		}
	}
}

// TestAnUnknownActionIsRefused — the same argument one level up. A reply this package cannot act on is
// a degradation, not a silent no-op.
func TestAnUnknownActionIsRefused(t *testing.T) {
	f := &fake{replies: []string{`{"action":"execute","text":"rm -rf"}`}}
	if _, err := agentWith(f).Interpret(context.Background(), nil, "hi", noFacts()); !errors.Is(err, ErrUnusable) {
		t.Fatalf("an unknown action returned %v, want ErrUnusable", err)
	}
}

// TestSayingNothingIsRefused. An empty reply renders as a blank bubble and records as a blank turn, so
// the agent then has nothing to refer back to. Better to degrade and let the router answer.
func TestSayingNothingIsRefused(t *testing.T) {
	for _, body := range []string{`{"action":"say","text":""}`, `{"action":"say","text":"   "}`,
		`{"action":"ask","text":""}`} {
		f := &fake{replies: []string{body}}
		if _, err := agentWith(f).Interpret(context.Background(), nil, "hi", noFacts()); !errors.Is(err, ErrUnusable) {
			t.Errorf("%s returned %v, want ErrUnusable", body, err)
		}
	}
}

// TestTruncationIsReportedAsTruncationNotAsBadJSON.
//
// 🔴 A reply cut mid-object always fails to parse, so a version that let the parser speak first would
// name the wrong cause — sending somebody to rewrite a prompt when the fix is a bigger budget. The two
// need different responses, so they need different messages.
func TestTruncationIsReportedAsTruncationNotAsBadJSON(t *testing.T) {
	f := &fake{replies: []string{`{"action":"say","text":"half a sen`}, truncate: true}
	_, err := agentWith(f).Interpret(context.Background(), nil, "hi", noFacts())
	if !errors.Is(err, ErrUnusable) {
		t.Fatalf("interpret: %v", err)
	}
	if !strings.Contains(err.Error(), "cut off") {
		t.Errorf("a truncated reply was reported as %q, which does not say it was truncated", err)
	}
}

// TestAProviderFailureDegradesRatherThanErroring.
//
// The caller falls back to the keyword router on any error, so what matters here is that a provider
// outage produces the error CLASS that says "transient" — a rate limit and a prompt the model will
// never satisfy need completely different responses from whoever is on call.
func TestAProviderFailureDegradesRatherThanErroring(t *testing.T) {
	f := &fake{errs: []error{provider.ErrRateLimited}}
	_, err := agentWith(f).Interpret(context.Background(), nil, "hi", noFacts())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a rate limit returned %v, want ErrUnavailable", err)
	}
}

// TestATurnWillNotSpendPastItsCeiling.
//
// 🔴 It used to be true that answering cost nothing, and both the code and the UI said so. Now every
// turn costs, and an unbounded per-turn cost is a customer holding the send key. The ceiling is checked
// BEFORE each call: one tested only afterwards has already been exceeded by the call that tested it.
func TestATurnWillNotSpendPastItsCeiling(t *testing.T) {
	// Every reply is unusable, so the loop would keep going; the ceiling is what stops it.
	f := &fake{replies: []string{"{}", "{}", "{}", "{}"}, cost: 4_000}
	a := agentWith(f)
	a.Bounds.MaxCostMicroCents = 5_000

	out, err := a.Interpret(context.Background(), nil, "hi", noFacts())
	if !errors.Is(err, ErrUnusable) && !errors.Is(err, ErrExhausted) {
		t.Fatalf("interpret: %v", err)
	}
	if out.CostMicroCents > 8_000 {
		t.Errorf("the turn spent %d micro-cents against a ceiling of 5000", out.CostMicroCents)
	}
}

// TestBoundsThatDoNotBoundAreRefused.
//
// 🔴 Refused rather than defaulted, the same way provider.ValidateRequest refuses an unset Reasoning. A
// zero here is a forgotten field, and quietly filling it in means nobody ever finds out the ceiling
// they thought they set was never applied.
func TestBoundsThatDoNotBoundAreRefused(t *testing.T) {
	for name, b := range map[string]Bounds{
		"no calls":  {MaxCalls: 0, MaxTokens: 100, MaxCostMicroCents: 100},
		"no tokens": {MaxCalls: 1, MaxTokens: 0, MaxCostMicroCents: 100},
		"no money":  {MaxCalls: 1, MaxTokens: 100, MaxCostMicroCents: 0},
	} {
		a := Agent{Provider: &fake{}, Model: "m", Bounds: b}
		if _, err := a.Interpret(context.Background(), nil, "hi", noFacts()); err == nil {
			t.Errorf("%s: bounds with a hole in them were accepted", name)
		}
	}
}

// TestNoProviderDegradesRatherThanPanicking — a deployment with no key configured must fall back, not
// crash on the first sentence anybody types.
func TestNoProviderDegradesRatherThanPanicking(t *testing.T) {
	a := Agent{Bounds: DefaultBounds}
	if _, err := a.Interpret(context.Background(), nil, "hi", noFacts()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an unconfigured agent returned %v, want ErrUnavailable", err)
	}
}

// ── the prompt ───────────────────────────────────────────────────────────────────────────────────

// TestEveryCapabilityAppearsInThePrompt.
//
// # 🔴 The drift fence, and the failure it prevents
//
// A capability the prompt does not mention is a capability the agent will never choose. Nothing goes
// red: the build passes, the tests pass, and a person asking for it is told something else instead —
// indistinguishable from the feature not existing. That is the exact failure the closed set was built
// to end, and putting a hand-written list in a prompt would reintroduce it one layer up.
//
// The prompt is generated from `intent.All()`, so this test is really asking whether it still is.
func TestEveryCapabilityAppearsInThePrompt(t *testing.T) {
	p := systemPrompt(noFacts())
	for _, s := range intent.All() {
		if !strings.Contains(p, string(s.Intent)) {
			t.Errorf("capability %q is not in the prompt: the agent can never choose it, and nothing "+
				"else would report that", s.Intent)
		}
		if !strings.Contains(p, s.Question) {
			t.Errorf("capability %q is in the prompt without its example question", s.Intent)
		}
	}
	for _, axis := range intent.Axes() {
		if !strings.Contains(p, axis) {
			t.Errorf("axis %q is not in the prompt", axis)
		}
	}
}

// TestThePromptSaysWhichCapabilitiesCostMoney.
//
// The model is told the CONSEQUENCE rather than the internal tier name: "goal" means nothing to it,
// while "[runs]" beside a capability is the fact that should make it hesitate before choosing one.
func TestThePromptSaysWhichCapabilitiesCostMoney(t *testing.T) {
	p := systemPrompt(noFacts())
	for _, want := range []string{"[runs]", "[writes]", "[reads]"} {
		if !strings.Contains(p, want) {
			t.Errorf("the prompt never marks a capability %s", want)
		}
	}
}

// TestWithNoRepositoryThePromptSaysSoLoudly.
//
// 🔴 The single most important fact in the block. Almost every question means something different
// without a repository, and a model left to guess will describe code it has never seen — which is the
// one failure this product cannot absorb, because the customer cannot tell it from a real answer.
func TestWithNoRepositoryThePromptSaysSoLoudly(t *testing.T) {
	p := systemPrompt(Facts{SubjectLoaded: false})
	if !strings.Contains(p, "Nothing yet") {
		t.Error("the prompt does not say that no repository is loaded")
	}
	if !strings.Contains(p, "NOTHING about their") {
		t.Error("the prompt does not forbid inventing facts about code it has not seen")
	}
}

// TestANonAgentRepositoryIsFlaggedInThePrompt. Nine axes over a repository that never calls a model is
// nine paragraphs about nothing — and every axis would honestly report "no signal found", which reads
// as nine weaknesses rather than one wrong subject.
func TestANonAgentRepositoryIsFlaggedInThePrompt(t *testing.T) {
	p := systemPrompt(Facts{
		SubjectLoaded: true, Reference: "acme/website", IsAgent: false,
		Why: "No code in 812 non-test files calls a model.",
	})
	if !strings.Contains(p, "does NOT appear to call a model") {
		t.Error("a repository that is not an agent is not flagged to the model")
	}
	if !strings.Contains(p, "812") {
		t.Error("the reason the verdict was reached is not carried")
	}
}

// TestTheConversationIsWindowedAndOrdered.
//
// Input tokens are billed every turn, so an unbounded transcript makes a conversation cost more the
// longer it goes on. The window is the stated cost of that; what must not happen is the window
// silently reordering history, which would have the agent answering a question that was already
// answered.
func TestTheConversationIsWindowedAndOrdered(t *testing.T) {
	var history []memory.Turn
	for i := 1; i <= 10; i++ {
		history = append(history, memory.Turn{
			Seq: int64(i), Role: memory.TurnUser, Body: fmt.Sprintf("turn %d", i)})
	}
	msgs := conversation(history, 4)
	if len(msgs) != 4 {
		t.Fatalf("the window returned %d messages, want 4", len(msgs))
	}
	// The LAST four, in order — the most recent context, not the oldest.
	for i, want := range []string{"turn 7", "turn 8", "turn 9", "turn 10"} {
		if msgs[i].Content != want {
			t.Errorf("message %d is %q, want %q", i, msgs[i].Content, want)
		}
	}
}

// TestAnAgentTurnIsRenderedAsTheAssistant. A transcript that hands the model its own past replies
// labelled as the user's makes it answer questions it already answered, and argue with itself.
func TestAnAgentTurnIsRenderedAsTheAssistant(t *testing.T) {
	msgs := conversation([]memory.Turn{
		{Role: memory.TurnUser, Body: "hi"},
		{Role: memory.TurnAgent, Body: "Hello."},
	}, 0)
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("roles came back as %q/%q, want user/assistant", msgs[0].Role, msgs[1].Role)
	}
}

// ── scope and confirmation ───────────────────────────────────────────────────────────────────────

// TestACapabilityKeepsItsOwnAxisAndAnUnknownAxisIsDropped.
//
// Two separate rules with one reason each. A capability that is ABOUT one axis takes it from the table,
// not from the model — the table is the authority. And an axis the system does not have becomes EMPTY
// rather than an error, because scope is an optimisation: a model saying "retries" instead of "harness"
// should cost breadth, not the whole turn.
func TestACapabilityKeepsItsOwnAxisAndAnUnknownAxisIsDropped(t *testing.T) {
	a := agentWith(&fake{})

	// `memory` is defined on the memory axis; a model naming something else must not move it.
	spec, _ := intent.Lookup(intent.Memory)
	if got := a.axisFor(spec, "prompt"); got != "memory" {
		t.Errorf("the memory capability was re-scoped to %q by the model", got)
	}
	// `assess` has no axis of its own, so the model may narrow it — but only to a real one.
	assess, _ := intent.Lookup(intent.Assess)
	if got := a.axisFor(assess, "PROMPT"); got != "prompt" {
		t.Errorf("a named axis was not honoured (case): got %q", got)
	}
	if got := a.axisFor(assess, "retries"); got != "" {
		t.Errorf("an axis the system does not have was accepted as %q", got)
	}
}

// TestAMissingWhyIsFilledRatherThanFailing.
//
// `why` is shown to a person before a run starts, so its absence must not take the turn down — but it
// must not be blank either, because an empty confirmation prompt is one people click through without
// reading, which defeats the entire point of asking.
func TestAMissingWhyIsFilledRatherThanFailing(t *testing.T) {
	f := &fake{replies: []string{`{"action":"do","capability":"assess","why":"  "}`}}
	out, err := agentWith(f).Interpret(context.Background(), nil, "look at my repo", noFacts())
	if err != nil {
		t.Fatalf("interpret: %v", err)
	}
	if out.Why == "" {
		t.Fatal("a capability was chosen with nothing to show the person before it runs")
	}
}

// TestEverythingThatSpendsOrWritesNeedsConfirmation.
//
// # 🔴 Why this is derived from Tier rather than listed
//
// Anything that is not a read either spends money on a durable run or writes to somebody's repository,
// and `Tier` is already the single discriminator the whole system uses for that. A second list here
// would be a copy of a fact `intent` owns — and the copy would go wrong the first time a capability
// changed tier, silently, in the permissive direction: something that spends money would stop asking.
//
// The assertion is two-sided. That the eight are gated is half of it; the other half is that the eleven
// reads are NOT, because a version that confirmed everything would pass a one-sided test and make the
// product unusable.
func TestEverythingThatSpendsOrWritesNeedsConfirmation(t *testing.T) {
	var gated, straight int
	for _, s := range intent.All() {
		got := NeedsConfirmation(s.Intent)
		want := s.Tier != intent.TierQuery
		if got != want {
			t.Errorf("%s (tier %s): NeedsConfirmation=%v, want %v", s.Intent, s.Tier, got, want)
		}
		if want {
			gated++
		} else {
			straight++
		}
	}
	if gated == 0 || straight == 0 {
		t.Fatalf("%d gated and %d straight-through: a gate that catches everything or nothing is not "+
			"a gate", gated, straight)
	}
	// Named explicitly so that a capability changing tier shows up here as a number nobody expected,
	// rather than passing quietly because the rule still matches itself.
	if gated != 8 || straight != 11 {
		t.Errorf("%d capabilities are gated and %d are not; it was 8 and 11. If a capability changed "+
			"tier that may be correct — confirm it deliberately rather than updating this number",
			gated, straight)
	}
}

// TestConfirmationIsNotAskedForAReadThatCostsNothing — the mirror, spelled out on the capability people
// hit most often.
func TestConfirmationIsNotAskedForAReadThatCostsNothing(t *testing.T) {
	for _, i := range []intent.Intent{intent.Memory, intent.Tools, intent.Graph, intent.RunHistory} {
		if NeedsConfirmation(i) {
			t.Errorf("%s reads persisted state and costs nothing, but asks for confirmation", i)
		}
	}
	for _, i := range []intent.Intent{intent.Assess, intent.Improve, intent.Deliver, intent.Model} {
		if !NeedsConfirmation(i) {
			t.Errorf("%s spends money or writes to a repository without asking", i)
		}
	}
}
