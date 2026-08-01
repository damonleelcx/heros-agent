package registry

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// P16 §4 — two new context policies, and the claim that adding one costs nothing structural.
//
// The interesting assertion here is not that the policies work; it is that adding them moved NOTHING
// else. `ContextSpec` is unchanged, the registry schema is unchanged, the `Dimension` enum is
// unchanged, and each policy validates its own params at registration without this package learning
// its shape. If that stops being true, the interface P2 landed for exactly this reason has failed.

// ── task 4.2 — hierarchical-summary ──────────────────────────────────────────────────────────────

func TestHierarchicalSummaryPolicyAddedNoSchemaChange(t *testing.T) {
	p := policyByName(t, "hierarchical-summary")

	// (1) It is reachable through the SAME seam every other policy uses, on a store that was told
	// nothing about it beyond the implementation.
	s := NewStore(nil, nil)
	if _, ok := s.policies["hierarchical-summary"]; !ok {
		t.Fatal("hierarchical-summary is a builtin but NewStore did not register it")
	}
	// A caller-supplied policy reaches the same map through AddPolicy — the registration seam is one
	// thing, not a builtin path plus an extension path.
	s.AddPolicy(p)

	// (2) Its params are validated against ITS OWN schema, at registration, by a registry that knows
	// nothing about tiers or summarizers.
	_, err := s.RegisterContextPolicy(context.Background(), "hs", "hierarchical-summary",
		json.RawMessage(`{"summarizer_model_ref":"m","recent_verbatim":"two"}`))
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("a bad param type must be rejected at REGISTRATION, not when a run reaches the node; got %v", err)
	}
	if _, err := s.RegisterContextPolicy(context.Background(), "hs", "hierarchical-summary",
		json.RawMessage(`{"summarizer_model_ref":"m","recent_verbatim":2,"tiers":9}`)); !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("an unknown param must be rejected (additionalProperties:false), got %v", err)
	}

	// (3) The behavior: the recent tier survives verbatim, the older tier becomes one host-side summary.
	host := &fakeHost{summary: "earlier: the user asked about billing"}
	conv := convOf(
		"user", "first question about billing",
		"assistant", "first answer",
		"user", "second question",
		"assistant", "second answer",
	)
	got, err := p.Assemble(context.Background(), host, conv,
		json.RawMessage(`{"summarizer_model_ref":"anthropic/claude-sonnet-5","recent_verbatim":2}`), 11)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("want 1 summary + 2 verbatim turns, got %d messages: %+v", len(got.Messages), got.Messages)
	}
	if got.Messages[1].Content != "second question" || got.Messages[2].Content != "second answer" {
		t.Errorf("the recent tier must survive EXACTLY as written; got %+v", got.Messages[1:])
	}
	// 🔴 ONE summarizer call, host-side, over the older tier only — and the resolved request is the
	// determinism handle, so it must carry the model and the seed.
	if len(host.summarizeReqs) != 1 {
		t.Fatalf("want exactly one host-side summarizer call, got %d", len(host.summarizeReqs))
	}
	req := host.summarizeReqs[0]
	if len(req.Messages) != 2 || req.Messages[0].Content != "first question about billing" {
		t.Errorf("the summarizer was given the wrong tier: %+v", req.Messages)
	}
	if req.Seed != 11 || req.ModelRef != "anthropic/claude-sonnet-5" {
		t.Errorf("the resolved request must pin model + seed: %+v", req)
	}
	if got.ResolvedRequest == nil || !reflect.DeepEqual(*got.ResolvedRequest, req) {
		t.Errorf("the captured ResolvedRequest must be the request that was issued: %+v vs %+v",
			got.ResolvedRequest, req)
	}
	if !got.Lossy {
		t.Error("hierarchical-summary replaces history with a summary and is lossy; a lossless flag would " +
			"keep the drop-tolerance gate from ever firing on it")
	}
	if got.DropRatio <= 0 {
		t.Errorf("summarizing four turns into one line dropped nothing? DropRatio=%v", got.DropRatio)
	}
}

// A conversation that fits entirely in the verbatim tier is passed through WITHOUT a summarizer call.
// Summarizing an empty history is a model call that costs money and returns a copy of nothing.
func TestHierarchicalSummarySkipsTheCallWhenThereIsNoHistory(t *testing.T) {
	host := &fakeHost{summary: "should not be used"}
	p := policyByName(t, "hierarchical-summary")
	conv := convOf("user", "only question")

	got, err := p.Assemble(context.Background(), host, conv,
		json.RawMessage(`{"summarizer_model_ref":"m","recent_verbatim":5}`), 1)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if len(host.summarizeReqs) != 0 {
		t.Errorf("the summarizer was called with no history to summarize: %+v", host.summarizeReqs)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "only question" {
		t.Errorf("the conversation must pass through unchanged, got %+v", got.Messages)
	}
	if got.DropRatio != 0 {
		t.Errorf("nothing was dropped, so the measured drop must be 0, got %v", got.DropRatio)
	}
}

// The credential never moves: a host-calling policy with no host services fails closed rather than
// summarizing nowhere.
func TestHierarchicalSummaryFailsClosedWithoutHost(t *testing.T) {
	_, err := policyByName(t, "hierarchical-summary").Assemble(context.Background(), nil,
		convOf("user", "a", "assistant", "b", "user", "c"),
		json.RawMessage(`{"summarizer_model_ref":"m","recent_verbatim":1}`), 1)
	if !errors.Is(err, ErrInvalidPolicyParams) {
		t.Fatalf("want a fail-closed params error with no host services, got %v", err)
	}
}

// ── task 4.3 — structured-extraction, and the Q4 decision ────────────────────────────────────────

func TestStructuredExtractionDropMeasured(t *testing.T) {
	p := policyByName(t, "structured-extraction")
	conv := convOf(
		"user", "Hi, I have a problem with my order.\norder_id: A-1\nIt arrived damaged and I would like a "+
			"replacement, or a refund if that is faster, and I have attached photographs of the packaging.",
		"assistant", "Thanks — let me look that up.",
		// The value is the REST OF THE LINE after the marker, so a value may contain spaces
		// (`customer: acme corp`). A later line wins over an earlier one: a correction is a correction.
		"user", "customer: acme-corp\nHere is the corrected identifier.\norder_id: A-2",
	)
	got, err := p.Assemble(context.Background(), nil, conv,
		json.RawMessage(`{"fields":["order_id","customer"]}`), 0)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	// The extraction keeps the declared fields, in the DECLARED order (the params are hashed, so the
	// author's order is identity-bearing), and takes the LAST value — a correction wins.
	want := "order_id: A-2\ncustomer: acme-corp"
	if len(got.Messages) != 1 || got.Messages[0].Content != want {
		t.Fatalf("extraction produced:\n%q\nwant:\n%q", got.Messages[0].Content, want)
	}

	// 🔴 The Q4 decision, asserted rather than assumed: extraction is a PROJECTION, so it is lossy, and
	// the drop is MEASURED. A lossless flag here would mean the drop-tolerance gate never fired on the
	// one policy whose entire mechanism is discarding what the schema did not name.
	if !got.Lossy {
		t.Error("structured-extraction is a projection — everything outside the declared fields is " +
			"unrecoverable — so it must be marked lossy (PRD §14 Q4)")
	}
	if got.DropRatio <= 0.5 {
		t.Errorf("the extraction discarded most of a long conversation but measured a drop of only %v; "+
			"the gate reads the measurement, so an understated drop is a gate that does not bite",
			got.DropRatio)
	}

	// Measured, not a constant: a conversation that was almost entirely field markers reports a SMALL
	// drop through the same code path. This is the half that proves the number is measured.
	terse := convOf("user", "order_id: A-9")
	small, err := p.Assemble(context.Background(), nil, terse, json.RawMessage(`{"fields":["order_id"]}`), 0)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if small.DropRatio >= got.DropRatio {
		t.Errorf("a terse conversation measured drop %v, not less than the verbose one's %v; the ratio is "+
			"a constant, not a measurement", small.DropRatio, got.DropRatio)
	}
	if !small.Lossy {
		t.Error("the policy is lossy even when a particular run dropped little; Lossy is a property of the " +
			"policy, DropRatio is the measurement of the run")
	}
}

// A declared field the conversation does not carry is an ERROR. Extracting anyway would run the node on
// inputs it was not configured for and report the result under a config_hash claiming the field existed.
func TestStructuredExtractionFailsClosedOnAMissingField(t *testing.T) {
	_, err := policyByName(t, "structured-extraction").Assemble(context.Background(), nil,
		convOf("user", "order_id: A-1"), json.RawMessage(`{"fields":["order_id","customer"]}`), 0)
	if !errors.Is(err, ErrInvalidPolicyParams) {
		t.Fatalf("want a fail-closed error for a field the conversation lacks, got %v", err)
	}
	if !strings.Contains(err.Error(), "customer") {
		t.Errorf("the error must name the missing field: %v", err)
	}
}

// LLM-free means byte-identical across runs, with no host services involved at all.
func TestStructuredExtractionIsDeterministicAndHostFree(t *testing.T) {
	p := policyByName(t, "structured-extraction")
	conv := convOf("user", "order_id: A-1\ncustomer: acme")
	params := json.RawMessage(`{"fields":["customer","order_id"]}`)

	a, err := p.Assemble(context.Background(), nil, conv, params, 1)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	b, err := p.Assemble(context.Background(), nil, conv, params, 999) // a different seed changes nothing
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if !reflect.DeepEqual(a.Messages, b.Messages) {
		t.Errorf("an LLM-free policy must be byte-identical across runs:\n%+v\n%+v", a.Messages, b.Messages)
	}
	if a.ResolvedRequest != nil {
		t.Error("an LLM-free policy issues no resolved request; a non-nil one would claim a host call it " +
			"never made")
	}
}

// ── the additivity claim itself ──────────────────────────────────────────────────────────────────

// Adding a policy is an implementation and a row. This asserts the other half — that the two new
// policies did not need, and did not get, any change to the shape the registry stores.
func TestNewPoliciesRequiredNoSpecShapeChange(t *testing.T) {
	// ContextSpec still carries exactly a policy name and its params. A policy-specific field here would
	// be the extensibility failure the interface exists to prevent.
	fields := reflect.TypeOf(ContextSpec{})
	if fields.NumField() != 2 {
		t.Fatalf("ContextSpec has %d fields; P16 must add none — a new policy is an implementation plus a "+
			"row, never a schema change", fields.NumField())
	}

	// And every builtin, old and new, is reachable through one seam with one shape.
	for _, name := range []string{"full", "full-history", "sliding-window", "summarization",
		"rag-retrieval", "semantic-compaction", "hierarchical-summary", "structured-extraction"} {
		p := policyByName(t, name)
		if p.Name() != name {
			t.Errorf("policy %q reports its name as %q", name, p.Name())
		}
		if len(p.ParamsSchema()) == 0 {
			t.Errorf("policy %q declares no params schema, so its params are validated nowhere", name)
		}
	}
}
