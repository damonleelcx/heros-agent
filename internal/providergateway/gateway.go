package providergateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"time"

	"github.com/heros-foreal/agentd/internal/registry"
)

// Provider names. A closed set: an adapter is registered under one of these, and a model entry naming
// anything else fails closed rather than falling back to an OpenAI-shaped guess.
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
	ProviderBedrock   = "bedrock"
)

// Defaults from PRD §7 Reliability: "Every outbound provider call has a timeout (default 60 s,
// configurable per model entry) and bounded backoff (e.g. 3 retries, exponential with jitter)."
const (
	DefaultTimeout    = 60 * time.Second
	DefaultMaxRetries = 3
	baseBackoff       = 200 * time.Millisecond
	maxBackoff        = 8 * time.Second
)

var (
	// ErrNoCredential: the secrets manager has nothing for this provider. Fails the call — never a
	// fallback to an unauthenticated request.
	ErrNoCredential = errors.New("providergateway: no credential")
	// ErrUnsupportedProvider: no adapter for the model entry's provider.
	ErrUnsupportedProvider = errors.New("providergateway: unsupported provider")
	// ErrProvider: the provider returned a non-retryable error (4xx that is not 429).
	ErrProvider = errors.New("providergateway: provider rejected the request")
	// ErrTransient: the call failed after exhausting retries on retryable failures.
	ErrTransient = errors.New("providergateway: provider unavailable after retries")
	// ErrTimeout: the per-call deadline expired.
	ErrTimeout = errors.New("providergateway: call timed out")
)

// Role is a normalized message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one normalized message. The same struct describes a prompt to any provider — the
// adapters translate it into each SDK's wire shape, which is what makes a provider swap invisible to
// the caller (FR12).
type Message struct {
	Role    Role
	Content string
	// ToolCalls are set on an assistant message that requested tools.
	ToolCalls []ToolCall
	// ToolCallID links a RoleTool message back to the call it answers.
	ToolCallID string
}

// Tool is a skill bound at a call site. InputSchema is the JSON-schema contract straight from the
// Skill Registry (FR8) — the gateway does not invent or reshape it, so what a node validates against
// is what the provider is told.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Request is a normalized completion request.
type Request struct {
	Messages []Message
	Tools    []Tool
	// System is the system prompt. Modeled separately because Anthropic and Bedrock take it as a
	// top-level field while OpenAI takes it as a message — normalizing it here means the caller does
	// not have to know which.
	System string
	// IdempotencyKey makes a retried call safe to repeat.
	//
	// The gateway retries (task 4.4), and a retry that the provider treats as a new request is a
	// double charge — PRD §11's named risk. So the key travels with the request and every adapter
	// sends it. Task 5.1 owns DERIVING it from {run_id, node_id, attempt_group}; the gateway owns
	// carrying it, and shipping retries without this seam would be shipping the hazard.
	IdempotencyKey string
}

// StopReason is a normalized stop reason. Providers spell these differently ("stop"/"end_turn",
// "length"/"max_tokens", "tool_calls"/"tool_use"); the executor branches on one vocabulary.
type StopReason string

const (
	StopEndTurn       StopReason = "end_turn"
	StopMaxTokens     StopReason = "max_tokens"
	StopToolUse       StopReason = "tool_use"
	StopContentFilter StopReason = "content_filter"
	StopOther         StopReason = "other"
)

// Usage is normalized token accounting. P2.5 attaches cost metrics here; the field names are the
// GenAI semantic conventions' shape so that instrumentation is a read, not a translation.
type Usage struct {
	InputTokens      int
	OutputTokens     int
	ThinkingTokens   int
	CacheReadTokens  int
	CacheWriteTokens int
}

// Response is a normalized completion response.
type Response struct {
	Content    string
	ToolCalls  []ToolCall
	Usage      Usage
	StopReason StopReason
	// Provider and ModelID echo what actually served the call, so a run record can prove which
	// provider answered rather than which one the config asked for.
	Provider string
	ModelID  string
	// Attempts is how many HTTP requests it took, including retries. Surfaced rather than hidden:
	// a node that silently took 4 attempts is a latency and cost signal P2.5 needs.
	Attempts int
}

// Gateway is the LiteLLM-style provider abstraction (task 4.1).
//
// # Why Complete takes the ModelEntry as an argument
//
// It would be simpler to construct a Gateway around one model. That would preclude task 4.3's model
// tiering: P6 selects a model entry PER NODE from cost/complexity, which is only expressible if the
// entry arrives at call time. Passing it per call means a tier selector is a function that picks an
// entry and hands it over — no re-plumbing, no gateway-per-tier, nothing in this package changes.
//
// # Why the entry carries no endpoint or credential
//
// Both are deployment facts, not configuration identity. A model entry is content-addressed and its
// version_id is hashed into config_hash: if it carried a base URL, moving to a proxy would change
// every config_hash and orphan every stored result, and if it carried a key, the secret would live
// forever inside a version_id. So endpoints are gateway config and credentials come from Secrets.
type Gateway struct {
	secrets  Secrets
	adapters map[string]adapter
	http     *http.Client

	// baseURLs overrides a provider's endpoint (a proxy, a self-hosted deployment, or a test server).
	baseURLs map[string]string

	maxRetries int
	// sleep and jitter are injected so retry behavior is testable without wall-clock waits and
	// without a random result. A backoff test that actually sleeps is a slow test nobody runs.
	sleep  func(ctx context.Context, d time.Duration) error
	jitter func() float64

	// observer is P2.5's attach point (task 6.4). Nil by default: P2 ships no instrumentation.
	observer Observer
}

// Option configures a Gateway.
type Option func(*Gateway)

// WithHTTPClient sets the HTTP client (timeouts are per-call via context, not on the client).
func WithHTTPClient(c *http.Client) Option { return func(g *Gateway) { g.http = c } }

// WithBaseURL overrides a provider's endpoint.
func WithBaseURL(provider, url string) Option {
	return func(g *Gateway) { g.baseURLs[provider] = url }
}

// WithMaxRetries bounds retries on transient failures. 0 disables retrying.
func WithMaxRetries(n int) Option { return func(g *Gateway) { g.maxRetries = n } }

// withClock replaces sleep and jitter. Unexported: this exists for tests, and a production caller
// that wants to control the gateway's sense of time is doing something that needs a different door.
func withClock(sleep func(context.Context, time.Duration) error, jitter func() float64) Option {
	return func(g *Gateway) { g.sleep = sleep; g.jitter = jitter }
}

// allAdapters is the ONE list of providers this gateway speaks.
//
// One list because there are two consumers — New, which wires the call path, and
// SupportedProviders, which the secrets factory uses to know what to fetch credentials for. Two
// literals would drift, and the drift would be silent in the direction that matters: a provider added
// to New but missing from the credential list is a provider that resolves, dispatches, and then
// fails at the last step with "no credential", which reads like a permissions problem and is not.
func allAdapters() []adapter {
	return []adapter{openAIAdapter{}, anthropicAdapter{}, bedrockAdapter{}}
}

// New builds a Gateway over a secrets source.
func New(secrets Secrets, opts ...Option) *Gateway {
	g := &Gateway{
		secrets:    secrets,
		adapters:   map[string]adapter{},
		http:       &http.Client{}, // no client timeout: the per-call deadline is the timeout (see Complete)
		baseURLs:   map[string]string{},
		maxRetries: DefaultMaxRetries,
		sleep:      sleepCtx,
		jitter:     rand.Float64, //nolint:gosec // jitter is scheduling, not cryptography
	}
	for _, a := range allAdapters() {
		g.adapters[a.name()] = a
	}
	for _, o := range opts {
		o(g)
	}
	return g
}

// Complete performs one normalized completion against the entry's provider (PRD §8:
// `Gateway.Complete(ModelEntry, Request, Seed) → Response`).
//
// seed is passed per call rather than read from the entry, because the reproducibility unit is
// {config_hash, seed} with the run supplying the seed (PRD §7/FR16) — the entry's seed is a default.
// A nil seed means the entry's, and no seed at all if it has none. This is the same split
// internal/variantspec's providerParams() keeps seed out of config_hash for.
func (g *Gateway) Complete(ctx context.Context, entry *registry.ModelEntry, req Request, seed *int64) (resp *Response, err error) {
	if entry == nil {
		return nil, fmt.Errorf("providergateway: Complete requires a model entry")
	}
	// The observer hook, on every exit path including the error ones. A deferred close is the only
	// shape that survives the seven returns below — an instrument that only sees successes would
	// report a provider outage as silence, which is precisely when someone is looking at it.
	started := time.Now()
	defer func() { g.observe(ctx, entry, req, seed, started, resp, err) }()
	spec := entry.Spec
	ad, ok := g.adapters[spec.Provider]
	if !ok {
		return nil, fmt.Errorf("%w: %q (model entry %q)", ErrUnsupportedProvider, spec.Provider, entry.Name)
	}

	cred, err := g.secrets.Credential(ctx, spec.Provider)
	if err != nil {
		return nil, err
	}
	// Everything from here on is scrubbed against these before it can leave the function.
	secretVals := cred.secretValues()

	if seed == nil {
		seed = spec.Params.Seed
	}

	// The per-call deadline. It covers ALL attempts, not each one: a 60 s budget that silently
	// becomes 240 s across four retries is not a timeout, and PRD §7's "one slow provider cannot
	// exhaust the executor" depends on the bound being on the call.
	ctx, cancel := context.WithTimeout(ctx, timeoutFor(spec))
	defer cancel()

	var lastErr error
	for attempt := 0; attempt <= g.maxRetries; attempt++ {
		if attempt > 0 {
			if err := g.sleep(ctx, g.backoff(attempt)); err != nil {
				return nil, g.deadlineErr(ctx, lastErr, secretVals)
			}
		}
		resp, retryable, err := g.attempt(ctx, ad, cred, spec, req, seed)
		switch {
		case err == nil:
			resp.Attempts = attempt + 1
			return resp, nil
		case !retryable:
			// A 4xx is the request being wrong. Retrying it burns the deadline to arrive at the same
			// answer, and on a 402/403 it is a queue of doomed calls behind a billing problem.
			return nil, scrubErr(err, secretVals, ErrProvider)
		default:
			lastErr = err
		}
		if ctx.Err() != nil {
			return nil, g.deadlineErr(ctx, lastErr, secretVals)
		}
	}
	return nil, scrubErr(fmt.Errorf("after %d attempts: %v", g.maxRetries+1, lastErr), secretVals, ErrTransient)
}

// attempt performs one HTTP round trip. retryable reports whether the failure is worth another.
func (g *Gateway) attempt(ctx context.Context, ad adapter, cred Credential, spec registry.ModelSpec, req Request, seed *int64) (resp *Response, retryable bool, err error) {
	httpReq, err := ad.build(ctx, g.endpoint(ad.name(), spec), cred, spec, req, seed)
	if err != nil {
		return nil, false, err // a request we cannot even build will not build next time either
	}
	httpResp, err := g.http.Do(httpReq)
	if err != nil {
		// A transport error (connection reset, DNS blip, TLS handshake) is the canonical transient.
		return nil, true, err
	}
	defer func() { _ = httpResp.Body.Close() }()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, true, err
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, isRetryableStatus(httpResp.StatusCode),
			fmt.Errorf("%s returned %s: %s", ad.name(), httpResp.Status, truncate(string(body), 512))
	}
	out, err := ad.parse(body)
	if err != nil {
		// A 200 whose body will not parse is not a success. Returning content="" here is exactly the
		// "HTTP 200 + parsed all zeros + no log" failure that hides a wire-format change for months.
		return nil, false, fmt.Errorf("%s returned 200 but the body did not parse: %v (body: %s)",
			ad.name(), err, truncate(string(body), 512))
	}
	out.Provider, out.ModelID = spec.Provider, spec.ModelID
	return out, false, nil
}

// isRetryableStatus: 429 and 5xx are the provider saying "not now"; every other 4xx is it saying
// "not like that", which no amount of retrying fixes.
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// backoff returns the delay before an attempt: exponential, capped, with full jitter.
//
// Full jitter (a uniform draw over [0, exp)) rather than exponential-plus-a-little, because the
// failure this guards against is a fleet of executors retrying a rate-limited provider in lockstep.
// Fixed backoff synchronizes them into waves; jitter spreads them out. Capped so a late attempt does
// not sit out the entire per-call deadline doing nothing.
func (g *Gateway) backoff(attempt int) time.Duration {
	exp := time.Duration(float64(baseBackoff) * math.Pow(2, float64(attempt-1)))
	if exp > maxBackoff {
		exp = maxBackoff
	}
	return time.Duration(g.jitter() * float64(exp))
}

func (g *Gateway) deadlineErr(ctx context.Context, lastErr error, secrets []string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return scrubErr(fmt.Errorf("deadline expired (last error: %v)", lastErr), secrets, ErrTimeout)
	}
	return scrubErr(fmt.Errorf("cancelled (last error: %v)", lastErr), secrets, ErrTransient)
}

// timeoutFor resolves the per-call deadline: the model entry's override, else the 60 s default.
func timeoutFor(spec registry.ModelSpec) time.Duration {
	if t := spec.Params.TimeoutSeconds; t != nil && *t > 0 {
		return time.Duration(*t) * time.Second
	}
	return DefaultTimeout
}

func (g *Gateway) endpoint(provider string, spec registry.ModelSpec) string {
	if u, ok := g.baseURLs[provider]; ok {
		return u
	}
	return defaultBaseURL(provider, spec)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// truncate bounds a provider body quoted into an error. An error is a log line, and a multi-megabyte
// body in one helps nobody and can itself be the incident.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(truncated)"
}
