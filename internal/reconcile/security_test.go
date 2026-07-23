package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/dynamictracing"
)

// blobCapture records everything written to the blob store, so a test can prove no secret was stored
// even in the (redacted) input/stack blobs.
type blobCapture struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func (b *blobCapture) Put(_ context.Context, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	b.mu.Lock()
	if b.blobs == nil {
		b.blobs = map[string][]byte{}
	}
	b.blobs[h] = append([]byte(nil), data...)
	b.mu.Unlock()
	return h, nil
}

type capSink struct {
	mu    sync.Mutex
	calls []dynamictracing.TracedCall
}

func (s *capSink) Record(_ context.Context, c dynamictracing.TracedCall) error {
	s.mu.Lock()
	s.calls = append(s.calls, c)
	s.mu.Unlock()
	return nil
}

// TASK 9.7: no secret/PII appears in trace logs, stacks, or the reconciliation report — end to end.
func TestSecurity_NoSecretInTraceOrReconciliationReport(t *testing.T) {
	const secret = "sk-ant-DEADBEEF0123456789abcdefSECRET"
	blobs, sink := &blobCapture{}, &capSink{}
	i := dynamictracing.New(blobs, sink)

	// A workflow that (wrongly) put a provider key in its prompt.
	i.Observe(context.Background(),
		dynamictracing.Tags{VariantID: "v", RunID: "r", NodeID: "n", CaseID: "c", ConfigHash: "cfg", Seed: 1},
		dynamictracing.LLMCall{Provider: "anthropic", ModelID: "m",
			Inputs: []byte(`{"prompt":"call the api with ` + secret + `"}`), InvocationIndex: 0})
	i.Flush()

	// 1. No secret in any traced record (the record carries only hashes + tags).
	sink.mu.Lock()
	calls := append([]dynamictracing.TracedCall(nil), sink.calls...)
	sink.mu.Unlock()
	recBytes, _ := json.Marshal(calls)
	if strings.Contains(string(recBytes), secret) {
		t.Fatalf("secret leaked into a traced record:\n%s", recBytes)
	}

	// 2. No secret in any stored blob (inputs are redacted before hashing; stacks carry no values).
	blobs.mu.Lock()
	for h, b := range blobs.blobs {
		if strings.Contains(string(b), secret) {
			t.Fatalf("secret leaked into blob %s:\n%s", h, b)
		}
	}
	blobs.mu.Unlock()

	// 3. No secret in the reconciliation report (it carries node ids, statuses, and hashes only).
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Nodes: []discovery.IRNode{node("n")}}
	rep := Reconcile(ir, calls)
	repBytes, _ := json.Marshal(rep)
	if strings.Contains(string(repBytes), secret) {
		t.Fatalf("secret leaked into the reconciliation report:\n%s", repBytes)
	}
}
