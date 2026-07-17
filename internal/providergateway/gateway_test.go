package providergateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/registry"
)

func ptrI(i int) *int         { return &i }
func ptrF(f float64) *float64 { return &f }
func ptrI64(i int64) *int64   { return &i }

func entry(provider, modelID string) *registry.ModelEntry {
	return &registry.ModelEntry{VersionID: strings.Repeat("a", 64), Name: "m",
		Spec: registry.ModelSpec{Provider: provider, ModelID: modelID,
			Params: registry.ModelParams{Temperature: ptrF(0), MaxTokens: ptrI(1024)}}}
}

// testGateway wires a gateway to a stub server for one provider, with retries instant and jitter
// fixed so behavior is deterministic and the suite does not sleep.
func testGateway(t *testing.T, provider string, h http.HandlerFunc, opts ...Option) (*Gateway, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	base := []Option{
		WithBaseURL(provider, srv.URL),
		withClock(func(context.Context, time.Duration) error { return nil }, func() float64 { return 1 }),
	}
	creds := StaticSecrets{
		ProviderOpenAI:    {APIKey: "sk-test-openai-secret-value"},
		ProviderAnthropic: {APIKey: "sk-ant-test-secret-value"},
		ProviderBedrock: {Region: "us-east-1", AWS: &AWSCredential{
			AccessKeyID: "AKIATESTTESTTESTTEST", SecretAccessKey: "aws-secret-access-key-value"}},
	}
	return New(creds, append(base, opts...)...), srv
}

const openAIBody = `{"choices":[{"finish_reason":"stop","message":{"content":"hello from openai"}}],
	"usage":{"prompt_tokens":10,"completion_tokens":5}}`
const anthropicBody = `{"content":[{"type":"text","text":"hello from anthropic"}],"stop_reason":"end_turn",
	"usage":{"input_tokens":10,"output_tokens":5}}`
const bedrockBody = `{"output":{"message":{"content":[{"text":"hello from bedrock"}]}},"stopReason":"end_turn",
	"usage":{"inputTokens":10,"outputTokens":5}}`

func simpleReq() Request {
	return Request{System: "be brief", Messages: []Message{{Role: RoleUser, Content: "hi"}}}
}

// ── 4.1 / 4.2: normalized shape and provider-swap transparency ───────────────────────────────────

// The headline FR12 property: the SAME request against three different model entries produces the
// same normalized Response shape. The caller changes model_ref and nothing else.
func TestComplete_ProviderSwapIsTransparent(t *testing.T) {
	cases := []struct {
		provider, modelID, body, want string
	}{
		{ProviderOpenAI, "gpt-5", openAIBody, "hello from openai"},
		{ProviderAnthropic, "claude-sonnet-5", anthropicBody, "hello from anthropic"},
		{ProviderBedrock, "anthropic.claude-sonnet-5", bedrockBody, "hello from bedrock"},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			g, _ := testGateway(t, tc.provider, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			})
			got, err := g.Complete(context.Background(), entry(tc.provider, tc.modelID), simpleReq(), nil)
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			// Identical assertions across all three providers — that is the point of the test.
			if got.Content != tc.want {
				t.Errorf("Content = %q, want %q", got.Content, tc.want)
			}
			if got.StopReason != StopEndTurn {
				t.Errorf("StopReason = %q; every provider's own spelling must normalize to end_turn", got.StopReason)
			}
			if got.Usage.InputTokens != 10 || got.Usage.OutputTokens != 5 {
				t.Errorf("Usage = %+v, want 10 in / 5 out normalized", got.Usage)
			}
			if got.Provider != tc.provider || got.ModelID != tc.modelID {
				t.Errorf("Response should echo who served it: got %s/%s", got.Provider, got.ModelID)
			}
			if got.Attempts != 1 {
				t.Errorf("Attempts = %d, want 1", got.Attempts)
			}
		})
	}
}

// Each provider spells stop reasons differently; the executor must see one vocabulary.
func TestNormalizeStop_MapsEveryProviderVocabularyOntoOne(t *testing.T) {
	cases := map[string]StopReason{
		"stop": StopEndTurn, "end_turn": StopEndTurn, "stop_sequence": StopEndTurn,
		"length": StopMaxTokens, "max_tokens": StopMaxTokens,
		"tool_calls": StopToolUse, "tool_use": StopToolUse,
		"content_filter": StopContentFilter, "guardrail_intervened": StopContentFilter,
		// An unknown value must NOT pass through raw: a caller branching on StopReason would get a
		// string it has no case for and silently fall through its default.
		"something_new": StopOther, "": StopOther,
	}
	for in, want := range cases {
		if got := normalizeStop(in); got != want {
			t.Errorf("normalizeStop(%q) = %q, want %q", in, got, want)
		}
	}
}

// Tool calls normalize across providers' very different shapes (OpenAI function calls, Anthropic
// tool_use blocks, Bedrock toolUse blocks).
func TestComplete_ToolCallsNormalizeAcrossProviders(t *testing.T) {
	cases := []struct{ provider, body string }{
		{ProviderOpenAI, `{"choices":[{"finish_reason":"tool_calls","message":{"content":null,
			"tool_calls":[{"id":"c1","function":{"name":"search","arguments":"{\"q\":\"x\"}"}}]}}]}`},
		{ProviderAnthropic, `{"content":[{"type":"tool_use","id":"c1","name":"search","input":{"q":"x"}}],
			"stop_reason":"tool_use"}`},
		{ProviderBedrock, `{"output":{"message":{"content":[{"toolUse":{"toolUseId":"c1","name":"search",
			"input":{"q":"x"}}}]}},"stopReason":"tool_use"}`},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			g, _ := testGateway(t, tc.provider, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			})
			got, err := g.Complete(context.Background(), entry(tc.provider, "m"), simpleReq(), nil)
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if got.StopReason != StopToolUse {
				t.Errorf("StopReason = %q, want tool_use", got.StopReason)
			}
			if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "c1" || got.ToolCalls[0].Name != "search" {
				t.Fatalf("ToolCalls = %+v, want one call c1/search", got.ToolCalls)
			}
			var args map[string]string
			if err := json.Unmarshal([]byte(got.ToolCalls[0].Arguments), &args); err != nil {
				t.Fatalf("arguments are not JSON: %q", got.ToolCalls[0].Arguments)
			}
			if args["q"] != "x" {
				t.Errorf("arguments = %v, want q=x", args)
			}
		})
	}
}

// The system prompt is one normalized field but two wire shapes: a message for OpenAI, a top-level
// field for Anthropic. If the caller had to know which, the swap would not be transparent.
func TestComplete_SystemPromptTakesEachProvidersShape(t *testing.T) {
	var body map[string]any
	capture := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, openAIBody)
	}
	g, _ := testGateway(t, ProviderOpenAI, capture)
	if _, err := g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"), simpleReq(), nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	msgs := body["messages"].([]any)
	if first := msgs[0].(map[string]any); first["role"] != "system" || first["content"] != "be brief" {
		t.Errorf("openai should carry the system prompt as the first message, got %v", first)
	}

	capture2 := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, anthropicBody)
	}
	g2, _ := testGateway(t, ProviderAnthropic, capture2)
	if _, err := g2.Complete(context.Background(), entry(ProviderAnthropic, "claude-sonnet-5"), simpleReq(), nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if body["system"] != "be brief" {
		t.Errorf("anthropic should carry the system prompt top-level, got %v", body["system"])
	}
	for _, m := range body["messages"].([]any) {
		if m.(map[string]any)["role"] == "system" {
			t.Error("anthropic must not receive a system-role message")
		}
	}
}

// A 200 whose body will not parse is not a success. Returning content="" here is the classic
// "HTTP 200 + parsed all zeros + zero logs" failure that hides a wire-format change for months.
func TestComplete_UnparseableSuccessBodyIsAnErrorNotAnEmptyCompletion(t *testing.T) {
	g, _ := testGateway(t, ProviderOpenAI, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[]}`) // well-formed JSON, no choices
	})
	got, err := g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"), simpleReq(), nil)
	if err == nil {
		t.Fatalf("a 200 with no choices was reported as success: %+v", got)
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("the error should say what was wrong with the body, got: %v", err)
	}
}

// Anthropic requires max_tokens. Picking one silently would bake a number nobody configured into
// behavior config_hash does not record.
func TestComplete_AnthropicWithoutMaxTokensFailsLoud(t *testing.T) {
	e := entry(ProviderAnthropic, "claude-sonnet-5")
	e.Spec.Params.MaxTokens = nil
	g, _ := testGateway(t, ProviderAnthropic, func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request should never have been sent")
	})
	if _, err := g.Complete(context.Background(), e, simpleReq(), nil); err == nil {
		t.Fatal("a model entry with no max_tokens was accepted for anthropic")
	} else if !strings.Contains(err.Error(), "max_tokens") {
		t.Errorf("the error should name the missing param, got: %v", err)
	}
}

// ── 4.3: model tiering must stay possible ────────────────────────────────────────────────────────

// P6 selects a model entry per node from cost/complexity. That is only expressible because Complete
// takes the entry as an ARGUMENT — a tier selector is just a function that picks one. If this test
// ever needs a second Gateway, or any change in this package, the interface has precluded tiering.
func TestComplete_ModelEntryIsPerCallSoTieringNeedsNoReplumbing(t *testing.T) {
	var gotModels []string
	g, _ := testGateway(t, ProviderOpenAI, func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		gotModels = append(gotModels, b["model"].(string))
		_, _ = io.WriteString(w, openAIBody)
	})

	// A stand-in for P6's selector: cheap model for a short prompt, expensive for a long one.
	tier := func(req Request) *registry.ModelEntry {
		if len(req.Messages[0].Content) > 10 {
			return entry(ProviderOpenAI, "expensive-model")
		}
		return entry(ProviderOpenAI, "cheap-model")
	}
	for _, prompt := range []string{"hi", "a much longer and more complex prompt"} {
		req := Request{Messages: []Message{{Role: RoleUser, Content: prompt}}}
		if _, err := g.Complete(context.Background(), tier(req), req, nil); err != nil {
			t.Fatalf("Complete: %v", err)
		}
	}
	if len(gotModels) != 2 || gotModels[0] != "cheap-model" || gotModels[1] != "expensive-model" {
		t.Errorf("per-call tier selection did not reach the provider: %v", gotModels)
	}
}

// ── 4.4: timeout and bounded backoff ─────────────────────────────────────────────────────────────

func TestTimeoutFor_DefaultsTo60sAndHonoursThePerModelOverride(t *testing.T) {
	e := entry(ProviderOpenAI, "gpt-5")
	if got := timeoutFor(e.Spec); got != DefaultTimeout {
		t.Errorf("default timeout = %v, want %v (PRD §7)", got, DefaultTimeout)
	}
	e.Spec.Params.TimeoutSeconds = ptrI(5)
	if got := timeoutFor(e.Spec); got != 5*time.Second {
		t.Errorf("per-model override = %v, want 5s", got)
	}
}

// Transient failures retry and then succeed; the attempt count is reported rather than hidden.
func TestComplete_RetriesTransientFailuresAndReportsAttempts(t *testing.T) {
	var calls int32
	g, _ := testGateway(t, ProviderOpenAI, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, openAIBody)
	})
	got, err := g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"), simpleReq(), nil)
	if err != nil {
		t.Fatalf("Complete should have retried through two 503s: %v", err)
	}
	if got.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", got.Attempts)
	}
}

// The retry/no-retry line. A 4xx is the request being wrong; retrying burns the deadline to arrive at
// the same answer, and on a 402 it queues doomed calls behind a billing problem.
func TestComplete_RetriesOnlyRetryableStatuses(t *testing.T) {
	cases := []struct {
		status    int
		wantCalls int32
		wantErr   error
	}{
		{http.StatusTooManyRequests, 4, ErrTransient},    // 429: provider says "not now"
		{http.StatusServiceUnavailable, 4, ErrTransient}, // 5xx: same
		{http.StatusBadRequest, 1, ErrProvider},          // 400: "not like that" — never retried
		{http.StatusUnauthorized, 1, ErrProvider},        // 401: retrying cannot fix a bad key
		{http.StatusPaymentRequired, 1, ErrProvider},     // 402: retrying makes a billing problem worse
		{http.StatusNotFound, 1, ErrProvider},            // 404: the model does not exist
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			var calls int32
			g, _ := testGateway(t, ProviderOpenAI, func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.WriteHeader(tc.status)
			})
			_, err := g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"), simpleReq(), nil)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got := atomic.LoadInt32(&calls); got != tc.wantCalls {
				t.Errorf("%d made %d attempts, want %d", tc.status, got, tc.wantCalls)
			}
		})
	}
}

// Backoff is exponential, capped, and jittered. Full jitter (a draw over [0,exp)) rather than
// exp+jitter, because the failure being guarded against is a fleet retrying in lockstep.
func TestBackoff_IsExponentialCappedAndJittered(t *testing.T) {
	g := New(StaticSecrets{}, withClock(nil, func() float64 { return 1 })) // jitter=1 => the ceiling
	var last time.Duration
	for attempt := 1; attempt <= 3; attempt++ {
		d := g.backoff(attempt)
		if d <= last {
			t.Errorf("attempt %d backoff %v is not greater than the previous %v", attempt, d, last)
		}
		last = d
	}
	if got := g.backoff(20); got != maxBackoff {
		t.Errorf("backoff(20) = %v, want it capped at %v", got, maxBackoff)
	}
	// jitter=0 => no wait at all; the delay must be a DRAW over the window, not the window plus noise.
	g0 := New(StaticSecrets{}, withClock(nil, func() float64 { return 0 }))
	if got := g0.backoff(3); got != 0 {
		t.Errorf("with jitter 0 the backoff should be 0 (full jitter), got %v", got)
	}
}

// The deadline covers ALL attempts, not each one. A 60 s budget that silently becomes 240 s across
// four retries is not a timeout, and one slow provider would exhaust the executor.
func TestComplete_DeadlineBoundsTheWholeCallNotEachAttempt(t *testing.T) {
	e := entry(ProviderOpenAI, "gpt-5")
	e.Spec.Params.TimeoutSeconds = ptrI(1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never answer within the deadline, so the gateway's own timeout is what ends the call.
		// The upper bound is not optional: httptest.Server.Close waits for active handlers, so a
		// handler that blocks on r.Context().Done() alone hangs the suite when the client's timeout
		// fires without the connection closing.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	g := New(StaticSecrets{ProviderOpenAI: {APIKey: "sk-test-openai-secret-value"}},
		WithBaseURL(ProviderOpenAI, srv.URL))

	start := time.Now()
	_, err := g.Complete(context.Background(), e, simpleReq(), nil)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("the call took %v; a 1s deadline must bound every attempt together", elapsed)
	}
}

// ── 4.5: secrets ─────────────────────────────────────────────────────────────────────────────────

// The credential reaches the provider, in each provider's own header.
func TestComplete_CredentialIsInjectedAtCallTime(t *testing.T) {
	for _, tc := range []struct{ provider, header, want, body string }{
		{ProviderOpenAI, "Authorization", "Bearer sk-test-openai-secret-value", openAIBody},
		{ProviderAnthropic, "x-api-key", "sk-ant-test-secret-value", anthropicBody},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			var got string
			g, _ := testGateway(t, tc.provider, func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get(tc.header)
				_, _ = io.WriteString(w, tc.body)
			})
			if _, err := g.Complete(context.Background(), entry(tc.provider, "m"), simpleReq(), nil); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

// PRD §7: "Provider secrets never appear in ... logs, error messages, or run records."
//
// Asserted on the ERROR TEXT, across every failure path, because that is what gets logged. A provider
// that echoes the key back in its error body is the case a well-behaved gateway still leaks on — so
// the stub does exactly that.
func TestComplete_SecretsNeverAppearInAnyError(t *testing.T) {
	const secret = "sk-test-openai-secret-value"

	paths := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"provider error echoing the key back", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, fmt.Sprintf(`{"error":"invalid key: %s"}`, secret))
		}},
		{"transient error echoing the key back", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, fmt.Sprintf(`{"error":"overloaded, key %s"}`, secret))
		}},
		{"unparseable 200 echoing the key back", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, fmt.Sprintf(`{"choices":[],"debug":"%s"}`, secret))
		}},
	}
	for _, tc := range paths {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := testGateway(t, ProviderOpenAI, tc.handler)
			_, err := g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"), simpleReq(), nil)
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the secret leaked into an error message:\n%v", err)
			}
			if !strings.Contains(err.Error(), redacted) {
				t.Errorf("the secret was neither present nor redacted; is scrubbing running at all?\n%v", err)
			}
		})
	}
}

// scrubErr must break the error chain, not wrap. A wrapped error keeps the unredacted message
// reachable via Unwrap, and something downstream will eventually print it.
func TestScrubErr_DoesNotLeaveTheUnredactedMessageReachable(t *testing.T) {
	const secret = "super-secret-key-value"
	orig := fmt.Errorf("boom: %s", secret)
	got := scrubErr(orig, []string{secret}, ErrProvider)

	if !errors.Is(got, ErrProvider) {
		t.Error("the sentinel must survive scrubbing; callers branch on it")
	}
	if errors.Is(got, orig) {
		t.Error("the original unredacted error is still reachable through the chain")
	}
	for err := got; err != nil; err = errors.Unwrap(err) {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("an error in the chain still contains the secret: %v", err)
		}
	}
}

// A short "secret" is not scrubbed: redacting a 3-character string would redact fragments of ordinary
// words everywhere and make errors useless.
func TestScrub_LeavesImplausiblyShortSecretsAlone(t *testing.T) {
	if got := scrub("the cat sat", []string{"cat"}); got != "the cat sat" {
		t.Errorf("scrub redacted a 3-char value and mangled the message: %q", got)
	}
	if got := scrub("key is abcdefghij here", []string{"abcdefghij"}); !strings.Contains(got, redacted) {
		t.Errorf("a plausible secret was not redacted: %q", got)
	}
}

// A missing credential fails the call. There is no unauthenticated fallback: a request that goes out
// without a key is at best a 401 and at worst an unbilled call to a proxy that does not need one.
func TestComplete_MissingCredentialFailsClosed(t *testing.T) {
	g := New(StaticSecrets{}) // no credentials at all
	_, err := g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"), simpleReq(), nil)
	if !errors.Is(err, ErrNoCredential) {
		t.Fatalf("want ErrNoCredential, got %v", err)
	}
}

func TestEnvSecrets_ErrorNamesTheVariableNeverAValue(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := EnvSecrets{}.Credential(context.Background(), ProviderOpenAI)
	if !errors.Is(err, ErrNoCredential) {
		t.Fatalf("want ErrNoCredential, got %v", err)
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("the error should name the variable so an operator can fix it: %v", err)
	}
}

func TestComplete_UnsupportedProviderFailsClosed(t *testing.T) {
	g := New(StaticSecrets{"nope": {APIKey: "x"}})
	_, err := g.Complete(context.Background(), entry("nope", "m"), simpleReq(), nil)
	if !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("want ErrUnsupportedProvider, got %v", err)
	}
}

// ── seed handling ────────────────────────────────────────────────────────────────────────────────

// The seed reaches the provider that has one. Reproducibility is asserted on seed PROPAGATION
// (PRD OQ2), so this is the assertion that matters.
func TestComplete_SeedPropagatesToOpenAI(t *testing.T) {
	var body map[string]any
	g, _ := testGateway(t, ProviderOpenAI, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, openAIBody)
	})
	if _, err := g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"), simpleReq(), ptrI64(42)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if body["seed"] != float64(42) {
		t.Errorf("seed = %v, want 42 to reach the provider", body["seed"])
	}
}

// A per-call seed overrides the entry's, because the reproducibility unit is {config_hash, seed} with
// the RUN supplying the seed — the entry's is a default. This is the same split that keeps seed out
// of config_hash in internal/variantspec.
func TestComplete_PerCallSeedOverridesTheEntrysDefault(t *testing.T) {
	var body map[string]any
	g, _ := testGateway(t, ProviderOpenAI, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, openAIBody)
	})
	e := entry(ProviderOpenAI, "gpt-5")
	e.Spec.Params.Seed = ptrI64(1)

	if _, err := g.Complete(context.Background(), e, simpleReq(), ptrI64(99)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if body["seed"] != float64(99) {
		t.Errorf("seed = %v, want the per-call 99 to win over the entry's 1", body["seed"])
	}
	if _, err := g.Complete(context.Background(), e, simpleReq(), nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if body["seed"] != float64(1) {
		t.Errorf("seed = %v, want the entry's 1 when the call supplies none", body["seed"])
	}
}

// Anthropic's Messages API has no seed parameter. This test exists to make that a KNOWN, pinned fact
// rather than a silent drop: if a future API version adds one, this fails and someone wires it up.
// P4's multi-seed scoring depends on knowing which providers can honour a seed at all.
func TestComplete_SeedIsNotSilentlyDroppedForAnthropic(t *testing.T) {
	var body map[string]any
	g, _ := testGateway(t, ProviderAnthropic, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, anthropicBody)
	})
	if _, err := g.Complete(context.Background(), entry(ProviderAnthropic, "claude-sonnet-5"), simpleReq(), ptrI64(42)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := body["seed"]; ok {
		t.Error("the anthropic adapter sent a seed; the Messages API has no such parameter, so either " +
			"the API gained one (wire it up properly) or this is a field the provider will reject")
	}
}

// ── idempotency seam (task 5.1 derives the key; the gateway must carry it) ───────────────────────

// The gateway retries, and a retry the provider treats as a new request is a double charge — PRD
// §11's named risk. Retrying without carrying this key would be shipping the hazard.
func TestComplete_IdempotencyKeyIsSentOnEveryAttempt(t *testing.T) {
	var keys []string
	var calls int32
	g, _ := testGateway(t, ProviderOpenAI, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if atomic.AddInt32(&calls, 1) < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, openAIBody)
	})
	req := simpleReq()
	req.IdempotencyKey = "run1:node1:attempt1"
	if _, err := g.Complete(context.Background(), entry(ProviderOpenAI, "gpt-5"), req, nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(keys))
	}
	for i, k := range keys {
		if k != "run1:node1:attempt1" {
			t.Errorf("attempt %d sent Idempotency-Key %q; a retry with a different key is a second charge", i+1, k)
		}
	}
}

// ── bedrock signing ──────────────────────────────────────────────────────────────────────────────

// The signature itself is the AWS SDK's business — asserting my own signer against my own verifier
// would prove only that I am consistently wrong. What IS this package's business, and is asserted
// here: that the request was signed at all, is addressed at the right operation, and that the signing
// material never appears in the request beyond the Authorization header AWS requires.
func TestComplete_BedrockRequestIsSignedAndAddressed(t *testing.T) {
	var auth, path, amzDate string
	var raw []byte
	g, _ := testGateway(t, ProviderBedrock, func(w http.ResponseWriter, r *http.Request) {
		auth, path, amzDate = r.Header.Get("Authorization"), r.URL.Path, r.Header.Get("X-Amz-Date")
		raw, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, bedrockBody)
	})
	if _, err := g.Complete(context.Background(), entry(ProviderBedrock, "anthropic.claude-sonnet-5"), simpleReq(), nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIATESTTESTTESTTEST/") {
		t.Errorf("request was not SigV4-signed: %q", auth)
	}
	if !strings.Contains(auth, "/us-east-1/bedrock/aws4_request") {
		t.Errorf("signature scope should name the region and service: %q", auth)
	}
	if amzDate == "" {
		t.Error("a signed request must carry X-Amz-Date")
	}
	if path != "/model/anthropic.claude-sonnet-5/converse" {
		t.Errorf("path = %q, want the Converse operation for the model", path)
	}
	if strings.Contains(string(raw), "aws-secret-access-key-value") {
		t.Error("the AWS secret key appeared in the request body")
	}
}
