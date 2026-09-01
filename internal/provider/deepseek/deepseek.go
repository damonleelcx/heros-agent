// Package deepseek is a real client for DeepSeek's chat-completions API.
//
// # What this package is responsible for, and what it deliberately is not
//
// It turns one provider.Request into one HTTP call and one provider.Response, classifies failures into
// the typed error set, and reports the usage DeepSeek itself returned. That is all.
//
// 🚫 It does NOT retry. Retrying belongs to the tool contract and the worker's retry ladder, which know
// how many attempts this task has already spent and what the goal's remaining budget is. A client that
// retries on its own multiplies every outer ceiling by a factor nobody wrote down: a ladder of 3 over a
// client of 3 is 9 calls, and the goal was told it would make 3.
//
// 🚫 It does not cache, and it does not fall back to another model. Both would make the model that
// answered differ from the model that was asked for, which is a fact the caller records.
package deepseek

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

// DefaultBaseURL is DeepSeek's OpenAI-compatible endpoint.
const DefaultBaseURL = "https://api.deepseek.com"

// Models. Named constants because a typo'd model string is accepted by the API layer and only surfaces
// as a 400 at call time, on a task that has already been claimed and counted as an attempt.
//
// 🔴 These are the V4 names. An earlier draft of this file used `deepseek-chat` and `deepseek-reasoner`
// from memory; both are gone, and every call would have failed with a 400 whose message does not say
// "that model was renamed". Transcribing an API surface from memory is how that happens — the constants
// below were read off the live pricing page on the date in DefaultTariffs.
const (
	// ModelFlash is the fast, cheap general model.
	ModelFlash = "deepseek-v4-flash"
	// ModelPro is the stronger model, at 3x the price of Flash on every axis.
	ModelPro = "deepseek-v4-pro"
	// ModelFlashVision accepts images, billed as input tokens after conversion.
	ModelFlashVision = "deepseek-v4-flash-vision-exp"
)

// Client calls DeepSeek.
type Client struct {
	APIKey  string
	BaseURL string
	HTTP    *http.Client
	// Tariffs price each model. Required: a call with no price reports zero spend against a money
	// ceiling, which is the one direction of error nobody investigates.
	Tariffs map[string]Tariff
	// Now supplies the current time for peak/off-peak selection. Injected so the pricing seam is
	// testable without waiting for a Tuesday morning.
	Now func() time.Time
}

// Tariff is one model's price in both rate periods.
//
// # 🔴 Why this is not a single Price
//
// DeepSeek charges DOUBLE during peak hours. A static price table is therefore correct roughly half the
// time and understates cost the rest of it — and understating is the direction that lets a run sail past
// a money ceiling the customer agreed to. So the period is selected per call, from the call's own clock.
type Tariff struct {
	Peak, OffPeak provider.Price
}

// peakHoursUTC are DeepSeek's published peak windows: 01:00-04:00 and 06:00-10:00 UTC, Monday-Friday.
// Every other hour is off-peak.
func isPeak(t time.Time) bool {
	u := t.UTC()
	switch u.Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	h := u.Hour()
	return (h >= 1 && h < 4) || (h >= 6 && h < 10)
}

// PriceFor returns the price in force for a model at an instant.
//
// 🔴 When the period cannot be determined it returns PEAK. Guessing cheap is how a ceiling is breached
// while the ledger still says there is room.
func (c *Client) PriceFor(model string, at time.Time) (provider.Price, bool) {
	tf, ok := c.Tariffs[model]
	if !ok {
		return provider.Price{}, false
	}
	if isPeak(at) {
		return tf.Peak, true
	}
	return tf.OffPeak, true
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
		Tariffs: DefaultTariffs(),
		Now:     func() time.Time { return time.Now().UTC() },
	}
}

func (c *Client) Name() string { return "deepseek" }

// ── wire types ───────────────────────────────────────────────────────────────────────────────────

type wireReq struct {
	Model          string       `json:"model"`
	Messages       []wireMsg    `json:"messages"`
	MaxTokens      int          `json:"max_tokens"`
	Temperature    *float64     `json:"temperature,omitempty"`
	ResponseFormat *wireRespFmt `json:"response_format,omitempty"`
	Stream         bool         `json:"stream"`
	// Thinking is the documented toggle: {"thinking":{"type":"enabled"|"disabled"}}.
	Thinking *wireThinking `json:"thinking,omitempty"`
	// ReasoningEffort is only meaningful when thinking is enabled.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

type wireThinking struct {
	Type string `json:"type"`
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
		PromptTokens         int64 `json:"prompt_tokens"`
		CompletionTokens     int64 `json:"completion_tokens"`
		TotalTokens          int64 `json:"total_tokens"`
		PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
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

	// 🔴 Thinking is enabled by default at HIGH effort, so it is always sent explicitly. Omitting the
	// field is not neutral — it buys the expensive option.
	if req.Reasoning == provider.NoReasoning {
		body.Thinking = &wireThinking{Type: "disabled"}
		// Temperature is only honoured with thinking off. The provider documents that it "will not
		// trigger an error but will also have no effect" otherwise, so sending it in thinking mode would
		// give a caller a determinism they did not get and no way to notice.
		body.Temperature = req.Temperature
	} else {
		body.Thinking = &wireThinking{Type: "enabled"}
		body.ReasoningEffort = string(req.Reasoning)
	}
	if req.JSONObject {
		body.ResponseFormat = &wireRespFmt{Type: "json_object"}
		// 🔴 DeepSeek rejects json_object unless the word "json" appears in the prompt. Enforced here
		// rather than left to each call site: the failure is a 400 on a task that has already been
		// claimed and counted as an attempt, and the error text does not say which of our prompts did it.
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
		CachedInputTokens: out.Usage.PromptCacheHitTokens,
		ReasoningTokens:   out.Usage.CompletionTokensDetails.ReasoningTokens,
	}
	// Price on the model that ANSWERED, not the one requested. An unknown model would price at zero,
	// silently under-reporting spend, so it is an error instead.
	now := c.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	at := now()
	price, ok := c.PriceFor(out.Model, at)
	if !ok {
		price, ok = c.PriceFor(req.Model, at)
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
		return fmt.Errorf("%w: HTTP %d — the key is wrong, revoked, or out of credit; retrying will "+
			"not change that: %s", provider.ErrAuth, status, msg)
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

// DefaultTariffs is DeepSeek's published list price, in cents per million tokens.
//
// 🔴 TRANSCRIBED FROM THE LIVE PAGE, NOT MEASURED. A price change makes every cost figure here quietly
// wrong in the direction of under-reporting, which is the direction nobody investigates. This exists so
// a spend CEILING has a number to work with; anything that BILLS a customer must read live pricing.
//
// Read 2026-08-31 from https://api-docs.deepseek.com/quick_start/pricing. Off-peak is exactly half of
// peak on every line, which the page states as a rule rather than a coincidence.
func DefaultTariffs() map[string]Tariff {
	const src = "https://api-docs.deepseek.com/quick_start/pricing"
	const at = "2026-08-31"
	flash := Tariff{
		Peak:    provider.Price{InputCentsPerMTok: 44, CachedInputCentsPerMTok: 1.4, OutputCentsPerMTok: 132, StatedAt: at, Source: src},
		OffPeak: provider.Price{InputCentsPerMTok: 22, CachedInputCentsPerMTok: 0.7, OutputCentsPerMTok: 66, StatedAt: at, Source: src},
	}
	return map[string]Tariff{
		ModelFlash:       flash,
		ModelFlashVision: flash,
		ModelPro: {
			Peak:    provider.Price{InputCentsPerMTok: 132, CachedInputCentsPerMTok: 4.4, OutputCentsPerMTok: 396, StatedAt: at, Source: src},
			OffPeak: provider.Price{InputCentsPerMTok: 66, CachedInputCentsPerMTok: 2.2, OutputCentsPerMTok: 198, StatedAt: at, Source: src},
		},
	}
}

var _ provider.Provider = (*Client)(nil)
