package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/toolcontract"
)

// fakeProvider returns a fixed reply. The MODEL is the substituted boundary; everything else here —
// collection, ranking, the unmeasured partition, validation — is the real code.
type fakeProvider struct {
	reply  string
	finish string
	seen   provider.Request
	err    error
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Complete(_ context.Context, r provider.Request) (provider.Response, error) {
	f.seen = r
	if f.err != nil {
		return provider.Response{}, f.err
	}
	fin := f.finish
	if fin == "" {
		fin = "stop"
	}
	return provider.Response{
		Content: f.reply, FinishReason: fin,
		Usage: provider.Usage{InputTokens: 400, OutputTokens: 60},
	}, nil
}

func axisResult(t *testing.T, axis, weakness string, actionable bool) []byte {
	t.Helper()
	b, err := json.Marshal([]map[string]any{
		{"axis": axis, "weakness": weakness, "actionable": actionable},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func run(t *testing.T, p *fakeProvider, inputs map[string][]byte) (Synthesis, toolcontract.Result, error) {
	t.Helper()
	tool := SynthesiseAssessment{Provider: p, Model: "test", MaxTokens: 400}
	res, err := tool.Execute(context.Background(), toolcontract.Call{
		TaskID: "synthesise", Kind: "synthesise_assessment", Inputs: inputs, Attempt: 1,
	})
	var s Synthesis
	if len(res.Output) > 0 {
		if uerr := json.Unmarshal(res.Output, &s); uerr != nil {
			t.Fatalf("undecodable synthesis: %v", uerr)
		}
	}
	return s, res, err
}

// TestTheRankingAndTheAdmissionsAreComputed.
//
// 🔴 The division of labour is the whole design: ordering, counts and the unmeasured list come from
// code, and only the connective sentence comes from a model. A generated ordering is unauditable — it
// cannot be reproduced or argued with — and "which of these matters most" is the one judgement a reader
// acts on directly.
func TestTheRankingAndTheAdmissionsAreComputed(t *testing.T) {
	p := &fakeProvider{reply: `{"overall":"The tools and memory findings share a root cause."}`}
	syn, _, err := run(t, p, map[string][]byte{
		"assess-tools":  axisResult(t, "tools", "an unused tool is offered", true),
		"assess-graph":  axisResult(t, "graph", "topology is built at runtime", false),
		"assess-memory": axisResult(t, "memory", "nothing persists between sessions", true),
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(syn.Ranked) != 3 {
		t.Fatalf("%d ranked findings, want 3", len(syn.Ranked))
	}
	// Actionable first, then alphabetical — deterministic, so two runs are comparable.
	if syn.Ranked[0].Actionable != true || syn.Ranked[2].Actionable != false {
		t.Errorf("ranking did not put actionable findings first: %+v", syn.Ranked)
	}
	if syn.Ranked[0].Axis != "memory" || syn.Ranked[1].Axis != "tools" || syn.Ranked[2].Axis != "graph" {
		t.Errorf("unexpected order: %s, %s, %s",
			syn.Ranked[0].Axis, syn.Ranked[1].Axis, syn.Ranked[2].Axis)
	}
	if syn.ActionableCount != 2 {
		t.Errorf("actionable count = %d, want 2", syn.ActionableCount)
	}
	// Six of the nine axes reported nothing, and saying so is part of the product.
	if len(syn.Unmeasured) != 6 {
		t.Fatalf("%d unmeasured axes (%v), want 6", len(syn.Unmeasured), syn.Unmeasured)
	}
	for _, a := range syn.Unmeasured {
		if a == "tools" || a == "graph" || a == "memory" {
			t.Errorf("%q was assessed but is listed as unmeasured", a)
		}
	}
}

// TestASynthesisMayNotCommentOnAnAxisItWasNotShown.
//
// 🔴 The fence that makes the model's contribution safe. It may connect the findings it was given; it
// may not add one. A sentence naming an unassessed axis is either an invention or a comment on an
// absence — and both read identically to a person, sitting in a paragraph where every other clause is
// true. Refusing costs one retry; publishing costs a customer acting on a weakness never observed in
// their code.
func TestASynthesisMayNotCommentOnAnAxisItWasNotShown(t *testing.T) {
	p := &fakeProvider{reply: `{"overall":"The agent also has no memory strategy at all."}`}
	_, _, err := run(t, p, map[string][]byte{
		"assess-tools": axisResult(t, "tools", "an unused tool is offered", true),
	})
	if err == nil {
		t.Fatal("a synthesis inventing a memory finding was accepted")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("the error does not name the invented axis: %v", err)
	}
}

// TestAnAxisNameInsideAnotherWordIsNotAMention. "remember" contains no axis; a substring check would
// reject valid syntheses and be very hard to explain.
func TestAnAxisNameInsideAnotherWordIsNotAMention(t *testing.T) {
	p := &fakeProvider{reply: `{"overall":"The agent does not remember prior turns, per the tools finding."}`}
	syn, _, err := run(t, p, map[string][]byte{
		"assess-tools": axisResult(t, "tools", "an unused tool is offered", true),
	})
	if err != nil {
		t.Fatalf("a valid synthesis was rejected: %v", err)
	}
	if syn.Overall == "" {
		t.Error("the overall sentence was dropped")
	}
}

// TestAnEmptyJoinIsAFailureNotAnEmptyReport.
//
// Reaching the synthesis with no findings means every dependency was skipped or produced nothing.
// Publishing "no weaknesses found" would be the most misleading possible reading of that.
func TestAnEmptyJoinIsAFailureNotAnEmptyReport(t *testing.T) {
	p := &fakeProvider{reply: `{"overall":"All clear."}`}
	for name, inputs := range map[string]map[string][]byte{
		"no dependencies":           {},
		"empty results":             {"assess-tools": {}},
		"unparseable":               {"assess-tools": []byte("not json")},
		"findings with no weakness": {"assess-tools": []byte(`[{"axis":"tools","weakness":"  "}]`)},
	} {
		if _, _, err := run(t, p, inputs); err == nil {
			t.Errorf("%s: an empty join produced a report instead of failing", name)
		}
	}
}

// TestTheModelNeverSeesTheUnmeasuredAxes. Showing them invites a comment on an absence, which is exactly
// what validate then rejects — cheaper not to ask.
func TestTheModelNeverSeesTheUnmeasuredAxes(t *testing.T) {
	p := &fakeProvider{reply: `{"overall":"The tools finding stands alone."}`}
	if _, _, err := run(t, p, map[string][]byte{
		"assess-tools": axisResult(t, "tools", "an unused tool is offered", true),
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	prompt := ""
	for _, m := range p.seen.Messages {
		prompt += m.Content + "\n"
	}
	// 🔴 Word boundaries, not substrings — "paragraph" contains "graph", and the first version of this
	// test failed on its own system prompt. That is the third time this session a substring check has
	// matched inside an unrelated word ("remember" contains "member" in the router; the same trap is
	// guarded in validate). Axis names are short common words; they need boundaries everywhere.
	for _, absent := range []string{"harness", "loop", "graph", "skills"} {
		if mentionsWord(strings.ToLower(prompt), absent) {
			t.Errorf("the prompt mentions %q, an axis that produced no finding", absent)
		}
	}
}

// TestSynthesisAsksForNoReasoning. It writes one paragraph over a list it was handed; chain-of-thought
// here is billed as output and buys nothing.
func TestSynthesisAsksForNoReasoning(t *testing.T) {
	p := &fakeProvider{reply: `{"overall":"One paragraph."}`}
	if _, _, err := run(t, p, map[string][]byte{
		"assess-tools": axisResult(t, "tools", "w", true),
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if p.seen.Reasoning != provider.NoReasoning {
		t.Errorf("reasoning = %q, want none", p.seen.Reasoning)
	}
	if p.seen.MaxTokens <= 0 {
		t.Error("the synthesis was sent with no output bound")
	}
}

// TestSpendIsRecordedEvenWhenTheReplyIsUnusable. A malformed reply still cost money, and a ceiling that
// only counts successes is one a malformed-output loop walks straight through.
func TestSpendIsRecordedEvenWhenTheReplyIsUnusable(t *testing.T) {
	p := &fakeProvider{reply: `not json at all`}
	_, res, err := run(t, p, map[string][]byte{
		"assess-tools": axisResult(t, "tools", "w", true),
	})
	if err == nil {
		t.Fatal("an unparseable synthesis was accepted")
	}
	if res.Tokens == 0 || res.ToolCalls == 0 {
		t.Errorf("spend was not recorded on a failed parse: %+v", res)
	}
}

// TestTruncationSaysTheAskIsTooBig, rather than reporting a parse failure — a reply cut mid-object
// always fails to parse, and the parse error would mask the cause.
func TestTruncationSaysTheAskIsTooBig(t *testing.T) {
	p := &fakeProvider{reply: `{"overall":"The tools`, finish: "length"}
	_, _, err := run(t, p, map[string][]byte{
		"assess-tools": axisResult(t, "tools", "w", true),
	})
	if err == nil || !strings.Contains(err.Error(), "cut off") {
		t.Fatalf("truncation was not reported as such: %v", err)
	}
}

// TestCollectionIsDeterministic. A synthesis that changes because a map iterated differently is not
// comparable to the one before it, which defeats the point of measuring twice.
func TestCollectionIsDeterministic(t *testing.T) {
	inputs := map[string][]byte{
		"assess-tools":   axisResult(t, "tools", "t", true),
		"assess-memory":  axisResult(t, "memory", "m", true),
		"assess-context": axisResult(t, "context", "c", true),
		"assess-loop":    axisResult(t, "loop", "l", false),
	}
	first, _ := collectFindings(inputs)
	for i := 0; i < 30; i++ {
		got, _ := collectFindings(inputs)
		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("collection order changed between runs at %d", j)
			}
		}
	}
}

// scriptedProvider answers a different thing on each call, so a repair round-trip can be exercised.
type scriptedProvider struct {
	replies []string
	calls   int
}

func (s *scriptedProvider) Name() string { return "scripted" }
func (s *scriptedProvider) Complete(_ context.Context, _ provider.Request) (provider.Response, error) {
	i := s.calls
	s.calls++
	if i >= len(s.replies) {
		i = len(s.replies) - 1
	}
	return provider.Response{
		Content: s.replies[i], FinishReason: "stop",
		Usage: provider.Usage{InputTokens: 400, OutputTokens: 60},
	}, nil
}

// 🔴 A synthesis that trips the fence on an ORDINARY use of an axis word must be repaired, not failed.
//
// Observed in production: a run scoped to the prompt axis found "can silently degrade context"; the
// synthesis reused the word "context", which is also an axis name, and was rejected for commenting on
// an axis it was not shown. Both attempts sent identical input, so the retry could only fail the same
// way, and the goal died having spent twice.
func TestASynthesisTrippedByAnOrdinaryWordIsRepairedRatherThanFailed(t *testing.T) {
	p := &scriptedProvider{replies: []string{
		`{"overall":"The prompt is built without checking it, which degrades context downstream."}`,
		`{"overall":"The prompt is built without checking it, which weakens what the agent is told."}`,
	}}
	tool := SynthesiseAssessment{Provider: p, Model: "test", MaxTokens: 400}
	res, err := tool.Execute(context.Background(), toolcontract.Call{
		TaskID: "synthesise", Kind: "synthesise_assessment", Attempt: 1,
		Inputs: map[string][]byte{
			"assess-prompt": axisResult(t, "prompt", "the system prompt falls back to an empty string", true),
		},
	})
	if err != nil {
		t.Fatalf("the run failed instead of repairing: %v", err)
	}
	if p.calls != 2 {
		t.Errorf("provider was called %d time(s), want 2 — one synthesis and one repair", p.calls)
	}
	var s Synthesis
	if uerr := json.Unmarshal(res.Output, &s); uerr != nil {
		t.Fatalf("undecodable synthesis: %v", uerr)
	}
	if mentionsWord(strings.ToLower(s.Overall), "context") {
		t.Errorf("the published synthesis still names an unassessed axis: %q", s.Overall)
	}
	// 🔴 The repair's tokens are on the bill. A repair that were free to the ledger would make the run's
	// ceiling a lie about what it spent.
	if res.ToolCalls != 2 {
		t.Errorf("ToolCalls=%d, want 2 — the repair must be counted", res.ToolCalls)
	}
	if res.Tokens != 920 {
		t.Errorf("Tokens=%d, want 920 (both calls) — the repair's tokens must be on the bill", res.Tokens)
	}
}

// 🔴 The fence still holds. One repair, and if that also names an unassessed axis the task fails with
// the ORIGINAL complaint — the repair widens what can succeed, never what is accepted.
func TestARepairThatAlsoTripsTheFenceStillFails(t *testing.T) {
	p := &scriptedProvider{replies: []string{
		`{"overall":"The prompt degrades context."}`,
		`{"overall":"The prompt still degrades context."}`,
	}}
	tool := SynthesiseAssessment{Provider: p, Model: "test", MaxTokens: 400}
	_, err := tool.Execute(context.Background(), toolcontract.Call{
		TaskID: "synthesise", Kind: "synthesise_assessment", Attempt: 1,
		Inputs: map[string][]byte{
			"assess-prompt": axisResult(t, "prompt", "the system prompt falls back to an empty string", true),
		},
	})
	if err == nil {
		t.Fatal("a synthesis naming an unassessed axis twice was accepted")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Errorf("the error does not name the offending axis: %v", err)
	}
	if p.calls != 2 {
		t.Errorf("provider was called %d time(s), want exactly 2 — one repair, not a loop", p.calls)
	}
}
