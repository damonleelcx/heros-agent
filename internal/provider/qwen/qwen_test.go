package qwen

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/heros-foreal/heros/internal/provider"
)

// newTestClient points a real Client at a stub speaking Qwen's wire format. The client under test is
// the one the binaries use — only the destination is substituted.
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("test-key")
	c.BaseURL = srv.URL
	return c, srv
}

func okBody(model string) string {
	return `{"model":"` + model + `","choices":[{"message":{"role":"assistant","content":"hi"},` +
		`"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
}

func baseReq() provider.Request {
	return provider.Request{
		Model:     ModelFlash,
		Messages:  []provider.Message{{Role: "user", Content: "hello"}},
		MaxTokens: 1000,
		Reasoning: provider.NoReasoning,
	}
}

// captureRequest runs one Complete and hands back the decoded request body the client actually sent.
func captureRequest(t *testing.T, req provider.Request) map[string]any {
	t.Helper()
	var got map[string]any
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, okBody(ModelFlash))
	})
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return got
}

// TestThinkingIsAlwaysStatedExplicitly is the fence over the reason Reasoning has no zero value.
//
// Qwen's qwen3.8 line thinks BY DEFAULT. Omitting enable_thinking when the caller asked for no
// reasoning would buy chain-of-thought on every call, billed as output, against MaxTokens — and nothing
// in the response says that is what happened. So `false` must appear on the wire, not be left out.
func TestThinkingIsAlwaysStatedExplicitly(t *testing.T) {
	got := captureRequest(t, baseReq())
	v, present := got["enable_thinking"]
	if !present {
		t.Fatal("enable_thinking was OMITTED for NoReasoning; the qwen3.8 line thinks by default, so " +
			"omitting it silently buys reasoning tokens the caller declined")
	}
	if v != false {
		t.Fatalf("enable_thinking = %v, want false", v)
	}
	if _, ok := got["thinking_budget"]; ok {
		t.Error("thinking_budget was sent with thinking disabled")
	}
}

// TestReasoningSendsClampedBudget checks that a chain of thought cannot eat the answer it serves.
//
// Reasoning tokens are billed as output and count against max_tokens, so an 8192-token budget under a
// 1200-token ceiling is a guaranteed truncation that still bills for the thinking.
func TestReasoningSendsClampedBudget(t *testing.T) {
	req := baseReq()
	req.Reasoning = provider.HighReasoning
	req.MaxTokens = 1200

	got := captureRequest(t, req)
	if got["enable_thinking"] != true {
		t.Fatalf("enable_thinking = %v, want true", got["enable_thinking"])
	}
	budget, ok := got["thinking_budget"].(float64)
	if !ok {
		t.Fatalf("thinking_budget missing or not a number: %v", got["thinking_budget"])
	}
	if int(budget) != 600 {
		t.Errorf("thinking_budget = %d, want 600 (half of MaxTokens=1200); an unclamped budget of %d "+
			"would leave no room for the answer", int(budget), budgetFor(provider.HighReasoning))
	}
}

// TestTemperatureOnlySentWithThinkingOff pins the documented interaction: temperature is ignored in
// thinking mode without an error, so sending it there promises a determinism the caller never gets.
func TestTemperatureOnlySentWithThinkingOff(t *testing.T) {
	temp := 0.0

	req := baseReq()
	req.Temperature = &temp
	if got := captureRequest(t, req); got["temperature"] != float64(0) {
		t.Errorf("temperature = %v with thinking off, want 0", got["temperature"])
	}

	req.Reasoning = provider.MaxReasoning
	if got := captureRequest(t, req); got["temperature"] != nil {
		t.Errorf("temperature = %v was sent in thinking mode, where the provider ignores it silently",
			got["temperature"])
	}
}

// TestCachedTokensReadFromNestedField is the fence over a field that moved between providers.
//
// DeepSeek reported cache hits as a FLAT usage.prompt_cache_hit_tokens. Qwen nests them under
// usage.prompt_tokens_details.cached_tokens. Reading the old path decodes cleanly as zero — no error,
// no warning — and prices every cached token at the full input rate.
func TestCachedTokensReadFromNestedField(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"model":"`+ModelFlash+`","choices":[{"message":{"content":"hi"},`+
			`"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":10,`+
			`"prompt_tokens_details":{"cached_tokens":800},`+
			`"completion_tokens_details":{"reasoning_tokens":4},`+
			`"prompt_cache_hit_tokens":12345}}`)
	})
	resp, err := c.Complete(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Usage.CachedInputTokens != 800 {
		t.Fatalf("CachedInputTokens = %d, want 800 — read from "+
			"usage.prompt_tokens_details.cached_tokens, not DeepSeek's flat prompt_cache_hit_tokens",
			resp.Usage.CachedInputTokens)
	}
	if resp.Usage.ReasoningTokens != 4 {
		t.Errorf("ReasoningTokens = %d, want 4", resp.Usage.ReasoningTokens)
	}
}

// TestJSONObjectRequiresTheWordJSON pins a live-verified precondition: Model Studio 400s a json_object
// request whose messages never say "json". Caught before the call, because the 400 lands on a task that
// has already been claimed and counted as an attempt.
func TestJSONObjectRequiresTheWordJSON(t *testing.T) {
	called := false
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		io.WriteString(w, okBody(ModelFlash))
	})

	req := baseReq()
	req.JSONObject = true
	req.Messages = []provider.Message{{Role: "user", Content: "give me an object"}}
	_, err := c.Complete(context.Background(), req)
	if !errors.Is(err, provider.ErrRequest) {
		t.Fatalf("err = %v, want provider.ErrRequest", err)
	}
	if called {
		t.Error("the request was sent anyway; the guard exists to avoid spending an attempt on a 400")
	}

	req.Messages = []provider.Message{{Role: "user", Content: "give me JSON"}}
	if _, err := c.Complete(context.Background(), req); err != nil {
		t.Fatalf("mentioning JSON should pass the guard, got %v", err)
	}
}

// TestUnknownModelIsAnErrorNotFreeTokens: an unpriced model must fail loudly. Pricing it at zero would
// report no spend against a money ceiling — the one direction of error nobody investigates.
func TestUnknownModelIsAnErrorNotFreeTokens(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, okBody("qwen-something-nobody-priced"))
	})
	req := baseReq()
	req.Model = "qwen-something-nobody-priced"
	if _, err := c.Complete(context.Background(), req); !errors.Is(err, provider.ErrRequest) {
		t.Fatalf("err = %v, want provider.ErrRequest for an unpriced model", err)
	}
}

// TestEmptyChoicesIsNotSuccess: a 200 carrying no choices is an upstream failure, not an empty answer.
func TestEmptyChoicesIsNotSuccess(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"model":"`+ModelFlash+`","choices":[],"usage":{}}`)
	})
	if _, err := c.Complete(context.Background(), baseReq()); !errors.Is(err, provider.ErrUpstream) {
		t.Fatalf("err = %v, want provider.ErrUpstream", err)
	}
}

// TestStatusClassification pins the mapping the retry ladder reads. Retrying a 401 burns the ladder to
// reach the same answer; retrying a 429 without backing off lengthens the rate limit.
func TestStatusClassification(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
		retry  bool
	}{
		{http.StatusUnauthorized, provider.ErrAuth, false},
		{http.StatusForbidden, provider.ErrAuth, false},
		{http.StatusTooManyRequests, provider.ErrRateLimited, true},
		{http.StatusBadRequest, provider.ErrRequest, false},
		{http.StatusInternalServerError, provider.ErrUpstream, true},
		{http.StatusServiceUnavailable, provider.ErrUpstream, true},
	} {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			io.WriteString(w, `{"error":{"message":"nope"}}`)
		})
		_, err := c.Complete(context.Background(), baseReq())
		if !errors.Is(err, tc.want) {
			t.Errorf("HTTP %d classified as %v, want %v", tc.status, err, tc.want)
		}
		if provider.Retryable(err) != tc.retry {
			t.Errorf("HTTP %d Retryable = %v, want %v", tc.status, provider.Retryable(err), tc.retry)
		}
	}
}

// TestAuthErrorNamesTheRegionalTrap: a Beijing key on the Singapore host 401s exactly like a revoked
// one. Without the hint in the message the obvious next move is to reissue a key that already works.
func TestAuthErrorNamesTheRegionalTrap(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"invalid_api_key"}}`)
	})
	_, err := c.Complete(context.Background(), baseReq())
	if err == nil || !contains(err.Error(), "region") {
		t.Fatalf("auth error does not mention the regional trap: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestEveryNamedModelIsPriced: a model constant with no price is a call that reports zero spend.
func TestEveryNamedModelIsPriced(t *testing.T) {
	prices := DefaultPrices()
	for _, m := range []string{ModelFlash, ModelMax} {
		p, ok := prices[m]
		if !ok {
			t.Errorf("%s has no price", m)
			continue
		}
		if p.InputCentsPerMTok <= 0 || p.OutputCentsPerMTok <= 0 {
			t.Errorf("%s priced at zero on an axis: %+v", m, p)
		}
		if p.CachedInputCentsPerMTok > p.InputCentsPerMTok {
			t.Errorf("%s cached input (%v) dearer than uncached (%v)",
				m, p.CachedInputCentsPerMTok, p.InputCentsPerMTok)
		}
		if p.StatedAt == "" || p.Source == "" {
			t.Errorf("%s carries a price with no date or source", m)
		}
	}
}

// TestNoKeyIsAuthError, not a request that goes out with an empty bearer token.
func TestNoKeyIsAuthError(t *testing.T) {
	c := New("")
	c.BaseURL = "http://127.0.0.1:1"
	if _, err := c.Complete(context.Background(), baseReq()); !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("err = %v, want provider.ErrAuth", err)
	}
}

// TestLatencyIsRecorded so a slow provider is visible as slow rather than as a mystery.
func TestLatencyIsRecorded(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		io.WriteString(w, okBody(ModelFlash))
	})
	resp, err := c.Complete(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Latency <= 0 {
		t.Error("Latency not recorded")
	}
}
