package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// The end-to-end proof of task 5.1 and PRD §11's named risk:
//
//	| Retry double-charges a provider | Backend | Idempotency key per node invocation; gateway
//	| de-dupes; integration test asserts one charge under forced retry |
//
// The per-package tests each prove half. internal/executor proves the key is derived and stable;
// internal/providergateway proves the same key rides every retry. Neither catches the failure that
// actually costs money — that nothing sets the field — because each half is correct in isolation.
// This test spans both, through the real gateway and a real HTTP server, and counts charges the way
// a provider would.

// billingProvider is a stub that bills the way a real provider does: requests carrying the same
// Idempotency-Key are ONE charge; requests without a key, or with distinct keys, are separate ones.
type billingProvider struct {
	mu sync.Mutex
	// requests is every HTTP request received, retries included.
	requests int
	// charges is what the provider would actually bill: distinct idempotency keys, plus one for each
	// keyless request (a provider with nothing to de-duplicate on bills every attempt).
	charges  int
	seen     map[string]bool
	failNext int
}

func newBillingProvider(failNext int) *billingProvider {
	return &billingProvider{seen: map[string]bool{}, failNext: failNext}
}

func (p *billingProvider) handler(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.requests++
	key := r.Header.Get("Idempotency-Key")
	if key == "" || !p.seen[key] {
		p.charges++ // a new billable request
		if key != "" {
			p.seen[key] = true
		}
	}
	fail := p.failNext > 0
	if fail {
		p.failNext--
	}
	p.mu.Unlock()

	if fail {
		// A transient failure AFTER the provider has already accepted (and charged for) the request —
		// the exact shape that makes a naive retry expensive.
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}],
		"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
}

func (p *billingProvider) counts() (requests, charges int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests, p.charges
}

func modelEntry() *registry.ModelEntry {
	maxTok := 16
	return &registry.ModelEntry{VersionID: "v", Name: "m",
		Spec: registry.ModelSpec{Provider: providergateway.ProviderOpenAI, ModelID: "gpt-5",
			Params: registry.ModelParams{MaxTokens: &maxTok}}}
}

func newGateway(t *testing.T, srv *httptest.Server) *providergateway.Gateway {
	t.Helper()
	return providergateway.New(
		providergateway.StaticSecrets{providergateway.ProviderOpenAI: {APIKey: "sk-test-openai-secret-value"}},
		providergateway.WithBaseURL(providergateway.ProviderOpenAI, srv.URL),
	)
}

// PRD §11: "integration test asserts one charge under forced retry."
func TestCallProvider_ForcedRetryProducesExactlyOneCharge(t *testing.T) {
	p := newBillingProvider(2) // two 503s, then success: the gateway will make three attempts
	srv := httptest.NewServer(http.HandlerFunc(p.handler))
	t.Cleanup(srv.Close)

	resp, err := CallProvider(context.Background(), newGateway(t, srv), modelEntry(),
		providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}}},
		NodeInvocation{RunID: "run1", NodeID: "n_a", AttemptGroup: 0})
	if err != nil {
		t.Fatalf("CallProvider: %v", err)
	}

	requests, charges := p.counts()
	if requests != 3 {
		t.Fatalf("the provider saw %d requests, want 3 (two forced failures + one success)", requests)
	}
	if charges != 1 {
		t.Errorf("the provider would bill %d times for one logical call; a retry must not double-charge "+
			"(PRD §11). requests=%d", charges, requests)
	}
	if resp.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3 — the retries should be reported, not hidden", resp.Attempts)
	}
}

// The test above is only meaningful if a MISSING key would actually have been billed three times.
// Without this, "charges == 1" could hold for a reason that has nothing to do with the key.
func TestCallProvider_WithoutTheKeyTheSameRetryWouldBeBilledEveryTime(t *testing.T) {
	p := newBillingProvider(2)
	srv := httptest.NewServer(http.HandlerFunc(p.handler))
	t.Cleanup(srv.Close)

	// The gateway called directly, with no key stamped — what CallProvider exists to prevent.
	_, err := newGateway(t, srv).Complete(context.Background(), modelEntry(),
		providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}}}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	requests, charges := p.counts()
	if charges != requests {
		t.Fatalf("test bug: a keyless retry should bill every attempt (requests=%d charges=%d)", requests, charges)
	}
	if charges < 2 {
		t.Errorf("charges = %d; the billing stub is not counting keyless retries, so the "+
			"one-charge test above proves nothing", charges)
	}
}

// A retry keeps its attempt_group and so its key: one charge. A loop's next iteration gets a new
// group: a second charge, because it is a second call.
func TestCallProvider_NewInvocationIsANewChargeButARetryIsNot(t *testing.T) {
	p := newBillingProvider(0)
	srv := httptest.NewServer(http.HandlerFunc(p.handler))
	t.Cleanup(srv.Close)
	gw := newGateway(t, srv)
	req := providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}}}

	// Same invocation reported twice (an at-least-once queue redelivering) — one charge.
	for i := 0; i < 2; i++ {
		if _, err := CallProvider(context.Background(), gw, modelEntry(), req,
			NodeInvocation{RunID: "run1", NodeID: "n_a", AttemptGroup: 0}); err != nil {
			t.Fatalf("CallProvider: %v", err)
		}
	}
	if _, charges := p.counts(); charges != 1 {
		t.Errorf("a redelivered invocation billed %d times, want 1", charges)
	}

	// A genuinely new invocation — a second charge, correctly.
	if _, err := CallProvider(context.Background(), gw, modelEntry(), req,
		NodeInvocation{RunID: "run1", NodeID: "n_a", AttemptGroup: 1}); err != nil {
		t.Fatalf("CallProvider: %v", err)
	}
	if _, charges := p.counts(); charges != 2 {
		t.Errorf("a new invocation billed to %d total, want 2 — a loop's iterations are real calls", charges)
	}
}

// CallProvider stamps the key from the invocation's coordinates, ignoring whatever the caller put in
// the Request. A caller-supplied key would let a retry arrive with a fresh one, which is the whole
// failure this path exists to prevent.
func TestCallProvider_StampsTheDerivedKeyOverAnythingTheCallerSet(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Idempotency-Key")
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(srv.Close)

	if _, err := CallProvider(context.Background(), newGateway(t, srv), modelEntry(),
		providergateway.Request{
			Messages:       []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}},
			IdempotencyKey: "something-the-caller-made-up",
		},
		NodeInvocation{RunID: "run1", NodeID: "n_a", AttemptGroup: 0}); err != nil {
		t.Fatalf("CallProvider: %v", err)
	}
	if want := IdempotencyKey("run1", "n_a", 0); got != want {
		t.Errorf("Idempotency-Key = %q, want the derived %q", got, want)
	}
}

// Task 5.3, the last link: the seed threaded from the Variant Spec reaches the provider call.
func TestCallProvider_ThreadsTheRunsSeedToTheProvider(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(srv.Close)

	seed := int64(4242)
	if _, err := CallProvider(context.Background(), newGateway(t, srv), modelEntry(),
		providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}}},
		NodeInvocation{RunID: "run1", NodeID: "n_a", AttemptGroup: 0, Seed: &seed}); err != nil {
		t.Fatalf("CallProvider: %v", err)
	}
	if !contains(body, `"seed":4242`) {
		t.Errorf("the run's seed did not reach the provider call: %s", body)
	}
}

// The same {config_hash, seed} must send the identical seed on every replay — the assertion FR16
// actually makes, since providers do not guarantee identical output at a fixed seed (PRD OQ2).
func TestCallProvider_SameSeedReachesEveryProviderCallIdentically(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(srv.Close)

	gw := newGateway(t, srv)
	seed := int64(7)
	for i := 0; i < 3; i++ {
		if _, err := CallProvider(context.Background(), gw, modelEntry(),
			providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}}},
			NodeInvocation{RunID: "run1", NodeID: "n_a", AttemptGroup: i, Seed: &seed}); err != nil {
			t.Fatalf("CallProvider %d: %v", i, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for i, b := range bodies {
		if !contains(b, `"seed":7`) {
			t.Errorf("call %d did not carry the run's seed: %s", i, b)
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
