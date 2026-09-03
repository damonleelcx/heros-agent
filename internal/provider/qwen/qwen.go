// Package qwen is a real client for Alibaba Cloud Model Studio's (DashScope) OpenAI-compatible
// chat-completions API, which serves the Qwen family.
//
// # What this package is responsible for, and what it deliberately is not
//
// It turns one provider.Request into one HTTP call and one provider.Response, classifies failures into
// the typed error set, and reports the usage Qwen itself returned. That is all.
//
// 🚫 It does NOT retry. Retrying belongs to the tool contract and the worker's retry ladder, which know
// how many attempts this task has already spent and what the goal's remaining budget is. A client that
// retries on its own multiplies every outer ceiling by a factor nobody wrote down: a ladder of 3 over a
// client of 3 is 9 calls, and the goal was told it would make 3.
//
// 🚫 It does not cache, and it does not fall back to another model. Both would make the model that
// answered differ from the model that was asked for, which is a fact the caller records.
package qwen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/heros-foreal/heros/internal/provider"
)

// DefaultBaseURL is Model Studio's OpenAI-compatible endpoint in the Beijing region.
//
// 🔴 There are TWO regional hosts and they do not share an account namespace: this one, and
// `https://dashscope-intl.aliyuncs.com/compatible-mode/v1` (Singapore). A key issued in one region is
// rejected by the other with a 401 that reads exactly like a revoked key — verified against the live
// service on 2026-09-03, where the working key returned `invalid_api_key` on the -intl host. So the
// region is part of the credential, not a performance preference, and BaseURL must be changed together
// with the key rather than tuned on its own.
const DefaultBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"

// Models. Named constants because a typo'd model string is accepted by the API layer and only surfaces
// as a 400 at call time, on a task that has already been claimed and counted as an attempt.
//
// 🔴 Transcribed from the live `GET /models` listing on 2026-09-03, not from memory. Model Studio
// serves 249 ids and retires them on its own schedule; an id that is merely plausible ("qwen-max-latest",
// "qwen3-turbo") is not necessarily one that exists, and the 400 it earns does not say so.
const (
	// ModelFlash is the fast, cheap general model. This is what nearly every call site uses.
	ModelFlash = "qwen3.8-flash"
	// ModelMax is the strongest model, at roughly 14x the price of Flash on both input and output.
	ModelMax = "qwen3.8-max"
)

// Client calls Qwen.
type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
	// Prices price each model. Required: a call with no price reports zero spend against a money
	// ceiling, which is the one direction of error nobody investigates.
	//
	// 🔴 A flat map, not DeepSeek's peak/off-peak Tariff. Model Studio publishes ONE rate per model with
	// no time-of-day component, so carrying a rate period here would be inventing a distinction the
	// vendor does not make — and a reader would reasonably assume it had been verified.
	Prices map[string]provider.Price
	// Now supplies the current time. Kept so the pricing seam stays injectable if Qwen ever introduces
	// a time-varying rate, and so latency is measurable without a real clock in tests.
	Now func() time.Time
}

// New builds a client with sane transport defaults.
//
// 🔴 The timeout here is a BACKSTOP, not the operative one. Each call takes its deadline from the
// context the tool contract supplies; this exists so a caller that forgets a context cannot hang a
// worker until its lease expires, which from outside is indistinguishable from a crash.
func New(apiKey string) *Client {
	return &Client{
		APIKey:  apiKey,
		BaseURL: DefaultBaseURL,
		HTTP:    &http.Client{Timeout: 4 * time.Minute},
		Prices:  DefaultPrices(),
		Now:     func() time.Time { return time.Now().UTC() },
	}
}

func (c *Client) Name() string { return "qwen" }

// PriceFor returns the published price for a model.
func (c *Client) PriceFor(model string) (provider.Price, bool) {
	p, ok := c.Prices[model]
	return p, ok
}

// ── wire types ───────────────────────────────────────────────────────────────────────────────────

type wireReq struct {
	Model          string       `json:"model"`
	Messages       []wireMsg    `json:"messages"`
	MaxTokens      int          `json:"max_tokens"`
	Temperature    *float64     `json:"temperature,omitempty"`
	ResponseFormat *wireRespFmt `json:"response_format,omitempty"`
	Stream         bool         `json:"stream"`
	// EnableThinking is Qwen's chain-of-thought toggle. A plain bool on the wire, unlike DeepSeek's
	// nested {"thinking":{"type":…}} object, and it is ALWAYS sent — see Complete for why.
	EnableThinking *bool `json:"enable_thinking,omitempty"`
	// ThinkingBudget caps chain-of-thought in TOKENS. Qwen has no "reasoning_effort" enum; the budget
	// is the only dial, so the effort scale is translated into token counts by budgetFor.
	ThinkingBudget *int `json:"thinking_budget,omitempty"`
}

type wireMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ReasoningContent is the chain of thought, returned beside Content when thinking is on. Read so the
	// caller can see WHY a budget was consumed; never sent back.
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type wireRespFmt struct {
	Type string `json:"type"`
}

type wireResp struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      wireMsg `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
		// PromptTokensDetails carries the cache split. 🔴 Qwen reports cache hits HERE, nested, as
		// `cached_tokens` — not as DeepSeek's flat `prompt_cache_hit_tokens`. Reading the wrong field
		// yields a clean zero rather than an error, which would price every cached token at the full
		// input rate and overstate spend on exactly the workloads that were built to be cheap.
		PromptTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		// CompletionTokensDetails carries the reasoning split when thinking is on.
		CompletionTokensDetails struct {
			ReasoningTokens int64 `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// budgetFor translates the effort scale onto a thinking budget in tokens.
//
// # Why a table and not a formula
//
// Qwen exposes no effort enum, so the mapping has to be chosen by somebody. A table makes the choice
// greppable: when a bill looks wrong, the number that caused it is on one line here rather than
// distributed through an expression.
//
// The values are deliberately conservative. Reasoning tokens are billed as OUTPUT and consume MaxTokens
// before a single character of the answer is written, so a generous default does not buy better answers
// — it buys truncated ones at a higher price.
func budgetFor(r provider.Reasoning) int {
	switch r {
	case provider.LowReasoning:
		return 1024
	case provider.HighReasoning:
		return 8192
	case provider.MaxReasoning:
		return 32768
	}
	return 0
}

// clampBudget keeps the chain of thought from eating the answer it is supposed to serve.
//
// 🔴 This exists because of a failure the provider package calls out by name: a response that spends
// 900 tokens thinking and 60 answering reports itself truncated, and "raise the ceiling" and "think
// less" are indistinguishable from the total alone. Qwen counts reasoning against max_tokens, so an
// 8192-token budget under a 1200-token ceiling is not a high-effort call — it is a guaranteed
// truncation that still bills for the thinking.
//
// Half the ceiling, so at least half of what the caller paid for is available for the answer.
func clampBudget(budget, maxTokens int) int {
	if half := maxTokens / 2; budget > half {
		return half
	}
	return budget
}

// Complete makes one call.
func (c *Client) Complete(ctx context.Context, req provider.Request) (provider.Response, error) {
	if err := provider.ValidateRequest(req); err != nil {
		return provider.Response{}, err
	}
	if c.APIKey == "" {
		return provider.Response{}, fmt.Errorf("%w: no API key configured", provider.ErrAuth)
	}

	msgs := make([]wireMsg, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, wireMsg{Role: m.Role, Content: m.Content})
	}
	body := wireReq{Model: req.Model, Messages: msgs, MaxTokens: req.MaxTokens, Stream: false}

	// 🔴 enable_thinking is sent EXPLICITLY in both directions rather than omitted when off. Qwen's
	// default differs per model — the qwen3.8 line thinks by default, older ids do not — so an omitted
	// field means "whatever this particular model prefers", which is exactly the silent, per-model
	// spend difference the Reasoning field was introduced to eliminate.
	thinking := req.Reasoning != provider.NoReasoning
	body.EnableThinking = &thinking
	if thinking {
		if b := clampBudget(budgetFor(req.Reasoning), req.MaxTokens); b > 0 {
			body.ThinkingBudget = &b
		}
	} else {
		// Temperature is only meaningful with thinking off, which is what makes a run reproducible.
		body.Temperature = req.Temperature
	}

	if req.JSONObject {
		body.ResponseFormat = &wireRespFmt{Type: "json_object"}
		// 🔴 Qwen rejects json_object unless the word "json" appears in the prompt, exactly as DeepSeek
		// does — verified against the live service on 2026-09-03, which answered
		// `'messages' must contain the word 'json' in some form`. Enforced here rather than left to each
		// call site: the failure is a 400 on a task that has already been claimed and counted as an
		// attempt, and the error text does not say which of our prompts did it.
		if !mentionsJSON(req.Messages) {
			return provider.Response{}, fmt.Errorf(
				"%w: json_object was requested but no message mentions JSON, which this API rejects",
				provider.ErrRequest)
		}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return provider.Response{}, fmt.Errorf("%w: %v", provider.ErrRequest, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return provider.Response{}, fmt.Errorf("%w: %v", provider.ErrRequest, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	start := time.Now()
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return provider.Response{}, ctx.Err()
		}
		// A transport failure is upstream-ish and retryable: the request may not have been seen at all.
		return provider.Response{}, fmt.Errorf("%w: %v", provider.ErrUpstream, err)
	}
	defer resp.Body.Close()

	// 🔴 Capped read. An unbounded io.ReadAll on a response body is an out-of-memory waiting for a
	// malformed or hostile upstream, and the worker it kills takes its lease with it.
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return provider.Response{}, fmt.Errorf("%w: reading body: %v", provider.ErrUpstream, err)
	}

	if resp.StatusCode != http.StatusOK {
		return provider.Response{}, classify(resp.StatusCode, payload)
	}

	var out wireResp
	if err := json.Unmarshal(payload, &out); err != nil {
		return provider.Response{}, fmt.Errorf("%w: undecodable body: %v", provider.ErrUpstream, err)
	}
	if out.Error != nil {
		return provider.Response{}, fmt.Errorf("%w: %s", provider.ErrUpstream, out.Error.Message)
	}
	if len(out.Choices) == 0 {
		// 🔴 A 200 with no choices is NOT success. Treating it as an empty completion would let a task
		// "succeed" having produced nothing, and the empty result would flow downstream as a fact.
		return provider.Response{}, fmt.Errorf("%w: HTTP 200 with no choices", provider.ErrUpstream)
	}

	usage := provider.Usage{
		InputTokens:       out.Usage.PromptTokens,
		OutputTokens:      out.Usage.CompletionTokens,
		CachedInputTokens: out.Usage.PromptTokensDetails.CachedTokens,
		ReasoningTokens:   out.Usage.CompletionTokensDetails.ReasoningTokens,
	}
	// Price on the model that ANSWERED, not the one requested. An unknown model would price at zero,
	// silently under-reporting spend, so it is an error instead.
	price, ok := c.PriceFor(out.Model)
	if !ok {
		price, ok = c.PriceFor(req.Model)
	}
	if !ok {
		return provider.Response{}, fmt.Errorf(
			"%w: no price for model %q, and a call with no price would report zero spend against a "+
				"money ceiling", provider.ErrRequest, out.Model)
	}

	return provider.Response{
		Content:        out.Choices[0].Message.Content,
		Usage:          usage,
		CostMicroCents: price.CostMicroCents(usage),
		Model:          out.Model,
		Latency:        time.Since(start),
		FinishReason:   out.Choices[0].FinishReason,
	}, nil
}

// classify maps an HTTP status onto the typed error set the retry ladder reads.
//
// The mapping is the whole point: retrying a 401 burns the ladder to arrive at the same answer, and
// retrying a 429 immediately is how a rate limit becomes a longer one.
func classify(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		// 🔴 The region hint is here rather than in a comment because this is where somebody reads it.
		// A Beijing key on the Singapore host fails EXACTLY like a revoked one, and without the hint the
		// obvious next move is to reissue a working key.
		return fmt.Errorf("%w: HTTP %d — the key is wrong, revoked, out of credit, or issued for a "+
			"different Model Studio region than BaseURL points at; retrying will not change that: %s",
			provider.ErrAuth, status, msg)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("%w: HTTP %d: %s", provider.ErrRateLimited, status, msg)
	case status == http.StatusRequestEntityTooLarge || status == http.StatusBadRequest:
		return fmt.Errorf("%w: HTTP %d — the same request will fail the same way: %s",
			provider.ErrRequest, status, msg)
	case status >= 500:
		return fmt.Errorf("%w: HTTP %d: %s", provider.ErrUpstream, status, msg)
	default:
		return fmt.Errorf("%w: HTTP %d: %s", provider.ErrUpstream, status, msg)
	}
}

func mentionsJSON(msgs []provider.Message) bool {
	for _, m := range msgs {
		if strings.Contains(strings.ToLower(m.Content), "json") {
			return true
		}
	}
	return false
}

// DefaultPrices is Qwen's published list price, in cents per million tokens.
//
// # 🔴 TRANSCRIBED FROM PUBLISHED FIGURES, NOT MEASURED — and the source is SECOND-HAND
//
// Alibaba's own model pages did not render machine-readable pricing when this was written, so these
// were read on 2026-09-03 from third-party trackers that agree with one another. That is weaker
// evidence than the DeepSeek table this replaced, which came from the vendor's own page. Treat these as
// good enough for a spend CEILING and not good enough to bill a customer — anything that bills must
// read live pricing, which the provider package already says.
//
// 🔴 ModelFlash's CACHED input rate is not published anywhere that could be found, so it is set EQUAL
// to the full input rate. That over-reports the cost of a cached token rather than under-reporting it,
// which is the only safe direction: a ledger that guesses cheap lets a run sail past a ceiling the
// customer agreed to while the recorded total still says there is room.
func DefaultPrices() map[string]provider.Price {
	const src = "third-party pricing trackers (openrouter.ai, felloai.com); Alibaba's own pages do not publish machine-readable rates"
	const at = "2026-09-03"
	return map[string]provider.Price{
		// $0.14 / $0.42 per million tokens.
		ModelFlash: {
			InputCentsPerMTok:       14,
			CachedInputCentsPerMTok: 14,
			OutputCentsPerMTok:      42,
			StatedAt:                at,
			Source:                  src,
		},
		// $2.00 / $6.00 per million tokens, cached input $0.25.
		ModelMax: {
			InputCentsPerMTok:       200,
			CachedInputCentsPerMTok: 25,
			OutputCentsPerMTok:      600,
			StatedAt:                at,
			Source:                  src,
		},
	}
}

var _ provider.Provider = (*Client)(nil)
