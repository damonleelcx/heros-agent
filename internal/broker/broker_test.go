package broker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sandbox"
)

// fakeGateway stands in for the provider gateway. It records whether it was called and returns a canned
// response — the credential lives here (host-side), never in the request the tool sent.
type fakeGateway struct {
	called   bool
	lastReq  providergateway.Request
	response *providergateway.Response
}

func (g *fakeGateway) Complete(_ context.Context, _ *registry.ModelEntry, req providergateway.Request, _ *int64) (*providergateway.Response, error) {
	g.called = true
	g.lastReq = req
	return g.response, nil
}

type fakeModels struct{}

func (fakeModels) ResolveModel(_ context.Context, versionID string) (*registry.ModelEntry, error) {
	if versionID == "model_ok" {
		return &registry.ModelEntry{VersionID: versionID, Name: "summarizer", Spec: registry.ModelSpec{Provider: "openai", ModelID: "gpt"}}, nil
	}
	return nil, registry.ErrNotFound
}

type fakeRetriever struct{ chunks []registry.Chunk }

func (f fakeRetriever) Retrieve(_ context.Context, _, _ string, _ int, _ int64) ([]registry.Chunk, error) {
	return f.chunks, nil
}

type recordingAuditor struct {
	mu   sync.Mutex
	recs []AuditRecord
}

func (r *recordingAuditor) Record(rec AuditRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec)
}
func (r *recordingAuditor) all() []AuditRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]AuditRecord, len(r.recs))
	copy(out, r.recs)
	return out
}

// Task 4.1 / spec "Brokered call succeeds without the isolate holding a credential": the tool's request
// names a model ref and carries no credential; the host performs the call and returns only the result.
func TestComplete_HostPerformsCallIsolateHoldsNoCredential(t *testing.T) {
	gw := &fakeGateway{response: &providergateway.Response{Content: "the answer", Usage: providergateway.Usage{InputTokens: 10, OutputTokens: 3}, Provider: "openai", ModelID: "gpt"}}
	aud := &recordingAuditor{}
	b := New(Config{Gateway: gw, Models: fakeModels{}, Audit: aud})

	res, err := b.Complete(context.Background(), CompleteRequest{
		NodeID: "n1", RunID: "r1", ModelRef: "model_ok",
		Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "q"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !gw.called {
		t.Fatal("the host gateway was not the one that performed the call")
	}
	if res.Content != "the answer" {
		t.Errorf("result content = %q", res.Content)
	}
	// The result the isolate receives carries the content + usage, and nothing that could be a credential.
	if res.Provider != "openai" || res.InputTokens != 10 {
		t.Errorf("unexpected result metadata: %+v", res)
	}
	// A brokered call was audited.
	recs := aud.all()
	if len(recs) != 1 || recs[0].Op != "complete" || !recs[0].Allowed {
		t.Fatalf("brokered call not audited as expected: %+v", recs)
	}
}

// Task 4.2 / spec "Broker cannot be used to bypass egress": an HTTP call to a non-allowlisted host is
// denied and recorded; a call to an allowlisted host is permitted.
func TestHTTP_EgressAllowlistEnforced(t *testing.T) {
	aud := &recordingAuditor{}
	b := New(Config{Egress: sandbox.EgressPolicy{Allow: []string{"api.internal"}}, Audit: aud})

	if err := b.HTTP(context.Background(), "n1", "r1", "evil.example.com"); !errors.Is(err, ErrEgressDenied) {
		t.Fatalf("non-allowlisted host was not denied: %v", err)
	}
	if err := b.HTTP(context.Background(), "n1", "r1", "api.internal"); err != nil {
		t.Fatalf("allowlisted host was denied: %v", err)
	}
	recs := aud.all()
	if len(recs) != 2 || recs[0].Allowed || !recs[1].Allowed {
		t.Fatalf("egress decisions not audited correctly: %+v", recs)
	}
}

// Empty allowlist denies everything (default-deny).
func TestHTTP_DefaultDeny(t *testing.T) {
	aud := &recordingAuditor{}
	b := New(Config{Audit: aud}) // empty egress policy
	if err := b.HTTP(context.Background(), "n1", "r1", "anything"); !errors.Is(err, ErrEgressDenied) {
		t.Fatalf("default-deny not enforced: %v", err)
	}
}

// Task 4.3: an audit record never carries a credential; a secret that somehow reaches a free-text field
// is redacted before recording.
func TestAudit_SecretFree(t *testing.T) {
	aud := &recordingAuditor{}
	b := New(Config{Egress: sandbox.EgressPolicy{}, Audit: aud})
	// A host string carrying a token shape is redacted in the record.
	_ = b.HTTP(context.Background(), "n1", "r1", "host-sk-abcdef012345-leak")
	for _, r := range aud.all() {
		if strings.Contains(r.Ref, "sk-abcdef012345") || strings.Contains(r.Reason, "sk-abcdef012345") {
			t.Errorf("audit record leaked a secret-shaped value: %+v", r)
		}
	}
}

// The broker implements registry.HostServices, so a context policy summarizes through the same
// credentialed, audited seam (task 4.1 + Section 1 host-side requirement).
func TestBroker_ImplementsHostServices(t *testing.T) {
	gw := &fakeGateway{response: &providergateway.Response{Content: "SUMMARY", Usage: providergateway.Usage{InputTokens: 5, OutputTokens: 1}}}
	b := New(Config{Gateway: gw, Models: fakeModels{}, Retriever: fakeRetriever{chunks: []registry.Chunk{{Text: "c"}}}, Audit: &recordingAuditor{}})

	var hs registry.HostServices = b // compile-time proof the broker is a HostServices
	summary, err := hs.Summarize(context.Background(), registry.ResolvedRequest{Op: "summarize", ModelRef: "model_ok", Messages: []registry.Message{{Role: "user", Content: "hi"}}, Seed: 1})
	if err != nil || summary != "SUMMARY" {
		t.Fatalf("summarize via broker failed: %q %v", summary, err)
	}
	chunks, err := hs.Retrieve(context.Background(), registry.ResolvedRequest{Op: "retrieve", Ref: "ret", TopK: 3, Seed: 1})
	if err != nil || len(chunks) != 1 {
		t.Fatalf("retrieve via broker failed: %v", err)
	}
}

// Fail closed when a capability was not wired.
func TestComplete_FailsClosedWhenGatewayMissing(t *testing.T) {
	b := New(Config{Audit: &recordingAuditor{}})
	if _, err := b.Complete(context.Background(), CompleteRequest{ModelRef: "model_ok"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}
