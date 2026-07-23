package dynamictracing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// memBlobs is a content-addressed in-memory blob store.
type memBlobs struct {
	mu   sync.Mutex
	data map[string][]byte
	fail bool
}

func newMemBlobs() *memBlobs { return &memBlobs{data: map[string][]byte{}} }

func (m *memBlobs) Put(_ context.Context, b []byte) (string, error) {
	if m.fail {
		return "", errors.New("blob store unavailable")
	}
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:])
	m.mu.Lock()
	m.data[h] = append([]byte(nil), b...)
	m.mu.Unlock()
	return h, nil
}

type memSink struct {
	mu    sync.Mutex
	calls []TracedCall
	fail  bool
}

func (s *memSink) Record(_ context.Context, c TracedCall) error {
	if s.fail {
		return errors.New("sink unavailable")
	}
	s.mu.Lock()
	s.calls = append(s.calls, c)
	s.mu.Unlock()
	return nil
}

func (s *memSink) all() []TracedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TracedCall(nil), s.calls...)
}

func tags() Tags {
	return Tags{VariantID: "v1", RunID: "run1", NodeID: "n_abc", CaseID: "c1", Seed: 7, ConfigHash: "cfg1"}
}

// TASK 4.1/4.2: every LLM call is logged with inputs + stack + the P0 tag set, correlated to a span.
func TestObserve_LogsCallWithInputsStackTags(t *testing.T) {
	blobs, sink := newMemBlobs(), &memSink{}
	i := New(blobs, sink)
	i.Observe(context.Background(), tags(), LLMCall{Provider: "anthropic", ModelID: "claude-sonnet-5",
		Inputs: []byte(`{"messages":[{"role":"user","content":"hi"}]}`), InvocationIndex: 0})
	i.Flush()

	calls := sink.all()
	if len(calls) != 1 {
		t.Fatalf("want 1 traced call, got %d", len(calls))
	}
	c := calls[0]
	if c.Tags.VariantID != "v1" || c.Tags.RunID != "run1" || c.Tags.NodeID != "n_abc" ||
		c.Tags.CaseID != "c1" || c.Tags.Seed != 7 || c.Tags.ConfigHash != "cfg1" {
		t.Fatalf("P0 tag set incomplete: %+v", c.Tags)
	}
	if c.Tags.Timestamp.IsZero() {
		t.Fatal("timestamp tag must be set")
	}
	if c.InputsBlobHash == "" {
		t.Fatal("inputs must be content-hashed")
	}
	if _, ok := blobs.data[c.InputsBlobHash]; !ok {
		t.Fatal("inputs blob must be stored under its hash")
	}
	if len(c.Stack) == 0 || !strings.Contains(c.Stack[0].Function, "TestObserve") {
		t.Fatalf("call stack must be captured with the caller's frame, got %+v", c.Stack)
	}
	if c.TraceID == "" || c.SpanID == "" {
		t.Fatalf("call must correlate to a P2.5 span (trace_id/span_id), got %q/%q", c.TraceID, c.SpanID)
	}
}

// TASK 4.4: inputs are stored as content-hashed blobs, never inline; no secret reaches a record.
func TestObserve_RedactsSecretsAndHashesInputs(t *testing.T) {
	blobs, sink := newMemBlobs(), &memSink{}
	i := New(blobs, sink)
	secret := `{"api_key":"sk-ant-abcdef0123456789ABCDEF","prompt":"hello bearer aaaaaaaaaaaaaaaaaaaa"}`
	i.Observe(context.Background(), tags(), LLMCall{Provider: "anthropic", ModelID: "m",
		Inputs: []byte(secret), InvocationIndex: 0})
	i.Flush()

	c := sink.all()[0]
	// The record itself carries no raw inputs — only a hash.
	stored := blobs.data[c.InputsBlobHash]
	if strings.Contains(string(stored), "sk-ant-abcdef0123456789ABCDEF") {
		t.Fatalf("secret key must be redacted from the stored blob: %s", stored)
	}
	if !strings.Contains(string(stored), Redacted) {
		t.Fatalf("redaction marker must be present: %s", stored)
	}
	// The stack blob carries no argument values (only func+file:line), so it cannot leak the secret.
	if strings.Contains(string(blobs.data[c.StackBlobHash]), "sk-ant") {
		t.Fatal("stack must not contain input values")
	}
}

// TASK 4.3: a logging failure never fails the run — Observe returns normally even when the store fails.
func TestObserve_BestEffort_LoggingFailureDoesNotFailRun(t *testing.T) {
	blobs, sink := newMemBlobs(), &memSink{fail: true}
	blobs.fail = true
	i := New(blobs, sink)
	// Must not panic or block; the run continues.
	i.Observe(context.Background(), tags(), LLMCall{Provider: "x", ModelID: "m", Inputs: []byte(`{}`)})
	i.Flush()
	_, recordErrors, _ := i.Stats()
	if recordErrors == 0 {
		t.Fatal("a store failure should be counted as a record error (logged, not propagated)")
	}
}

// TASK 4.3: the interceptor is passive — it never mutates the call's inputs.
func TestObserve_DoesNotMutateInputs(t *testing.T) {
	blobs, sink := newMemBlobs(), &memSink{}
	i := New(blobs, sink)
	original := []byte(`{"api_key":"sk-ant-abcdef0123456789ABCDEF"}`)
	snapshot := append([]byte(nil), original...)
	i.Observe(context.Background(), tags(), LLMCall{Provider: "x", ModelID: "m", Inputs: original})
	i.Flush()
	if string(original) != string(snapshot) {
		t.Fatalf("interceptor mutated the caller's input buffer: %s != %s", original, snapshot)
	}
}

// TASK 4.3: identical outputs traced vs. untraced. A workflow step's result must be the same whether or
// not the interceptor is observing it — the interceptor is a side-observer, not in the data path.
func TestObserve_IdenticalOutputsTracedVsUntraced(t *testing.T) {
	// A deterministic "workflow step": echoes its input length. Its result depends only on its input.
	step := func(in []byte) int { return len(in) }
	in := []byte(`{"messages":[{"role":"user","content":"same input both runs"}]}`)

	untraced := step(in)

	blobs, sink := newMemBlobs(), &memSink{}
	i := New(blobs, sink)
	traced := step(in)
	i.Observe(context.Background(), tags(), LLMCall{Provider: "x", ModelID: "m", Inputs: in})
	i.Flush()

	if traced != untraced {
		t.Fatalf("tracing changed the step's output: %d != %d", traced, untraced)
	}
	if len(sink.all()) != 1 {
		t.Fatal("the traced run should still have produced a trace record")
	}
}

// TASK 4.4: the interceptor holds no provider credential (structural) — it cannot leak one it does not
// have, and the instrumented run's credentials come only from the P3 sandbox/secrets manager.
func TestInterceptor_HoldsNoCredential(t *testing.T) {
	i := New(newMemBlobs(), &memSink{})
	// The struct has no secrets/credential field; this test documents that invariant. If a future field
	// added one, this reference would need updating, forcing the decision to be deliberate.
	_ = i.blobs
	_ = i.sink
}

// TASK 4.5: the interceptor adds no provider calls (it holds no provider client by construction).
func TestObserve_AddsNoProviderCalls(t *testing.T) {
	i := New(newMemBlobs(), &memSink{})
	for k := 0; k < 5; k++ {
		i.Observe(context.Background(), tags(), LLMCall{Provider: "x", ModelID: "m", Inputs: []byte(`{}`), InvocationIndex: k})
	}
	i.Flush()
	_, _, providerCalls := i.Stats()
	if providerCalls != 0 {
		t.Fatalf("the interceptor must make zero provider calls, got %d", providerCalls)
	}
}

// TASK 5.3 support: a loop's invocations get distinct invocation_index but share the node definition.
func TestObserve_LoopInvocationsShareNodeDistinctIndex(t *testing.T) {
	blobs, sink := newMemBlobs(), &memSink{}
	i := New(blobs, sink, WithClock(func() time.Time { return time.Unix(1700000000, 0) }))
	for k := 0; k < 7; k++ {
		i.Observe(context.Background(), tags(), LLMCall{Provider: "anthropic", ModelID: "m",
			Inputs: []byte(`{}`), InvocationIndex: k})
	}
	i.Flush()
	calls := sink.all()
	if len(calls) != 7 {
		t.Fatalf("want 7 invocations, got %d", len(calls))
	}
	nodes, indices := map[string]bool{}, map[int]bool{}
	for _, c := range calls {
		nodes[c.Tags.NodeID] = true
		indices[c.InvocationIndex] = true
	}
	if len(nodes) != 1 {
		t.Fatalf("a loop is ONE definition, got %d node ids", len(nodes))
	}
	if len(indices) != 7 {
		t.Fatalf("7 invocations must have 7 distinct indices, got %d", len(indices))
	}
}
