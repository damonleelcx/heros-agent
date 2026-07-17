package providergateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	awscreds "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/heros-foreal/agentd/internal/registry"
)

// adapter translates the normalized Request/Response to and from one provider's wire shape.
//
// This is the whole of FR12 / task 4.2: swapping a node's provider rewrites only its model_ref, and
// everything that differs between providers is confined to an implementation of this interface. If a
// provider-specific concept ever escapes into Request, Response, or Gateway, the swap stops being a
// one-argument change and the abstraction has failed.
type adapter interface {
	name() string
	build(ctx context.Context, endpoint string, cred Credential, spec registry.ModelSpec, req Request, seed *int64) (*http.Request, error)
	parse(body []byte) (*Response, error)
}

func defaultBaseURL(provider string, spec registry.ModelSpec) string {
	switch provider {
	case ProviderOpenAI:
		return "https://api.openai.com/v1"
	case ProviderAnthropic:
		return "https://api.anthropic.com/v1"
	case ProviderBedrock:
		return "" // per-region; built in the adapter from the credential's region
	}
	return ""
}

// ── OpenAI (chat/completions) ────────────────────────────────────────────────────────────────────

type openAIAdapter struct{}

func (openAIAdapter) name() string { return ProviderOpenAI }

func (openAIAdapter) build(ctx context.Context, endpoint string, cred Credential, spec registry.ModelSpec, req Request, seed *int64) (*http.Request, error) {
	msgs := make([]map[string]any, 0, len(req.Messages)+1)
	// OpenAI takes the system prompt as the first message; Anthropic takes it as a top-level field.
	// Normalizing that difference here is exactly this layer's job.
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		msg := map[string]any{"role": string(m.Role), "content": m.Content}
		if m.Role == RoleTool {
			msg["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				calls = append(calls, map[string]any{"id": tc.ID, "type": "function",
					"function": map[string]any{"name": tc.Name, "arguments": tc.Arguments}})
			}
			msg["tool_calls"] = calls
		}
		msgs = append(msgs, msg)
	}

	body := map[string]any{"model": spec.ModelID, "messages": msgs}
	if p := spec.Params.Temperature; p != nil {
		body["temperature"] = *p
	}
	if p := spec.Params.MaxTokens; p != nil {
		body["max_tokens"] = *p
	}
	if seed != nil {
		body["seed"] = *seed
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.InputSchema}})
		}
		body["tools"] = tools
	}

	hr, err := jsonPost(ctx, strings.TrimRight(endpoint, "/")+"/chat/completions", body)
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Authorization", "Bearer "+cred.APIKey)
	if req.IdempotencyKey != "" {
		hr.Header.Set("Idempotency-Key", req.IdempotencyKey)
	}
	return hr, nil
}

func (openAIAdapter) parse(body []byte) (*Response, error) {
	var p struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	if len(p.Choices) == 0 {
		// A well-formed envelope with no choices is not an empty answer — it is a response shape we
		// do not understand. Returning content="" would report it as a successful empty completion.
		return nil, fmt.Errorf("response contained no choices")
	}
	c := p.Choices[0]
	out := &Response{
		Content:    strings.TrimSpace(c.Message.Content),
		StopReason: normalizeStop(c.FinishReason),
		Usage: Usage{
			InputTokens: p.Usage.PromptTokens, OutputTokens: p.Usage.CompletionTokens,
			ThinkingTokens:  p.Usage.CompletionTokensDetails.ReasoningTokens,
			CacheReadTokens: p.Usage.PromptTokensDetails.CachedTokens,
		},
	}
	for _, tc := range c.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return out, nil
}

// ── Anthropic (Messages) ─────────────────────────────────────────────────────────────────────────

type anthropicAdapter struct{}

func (anthropicAdapter) name() string { return ProviderAnthropic }

func (anthropicAdapter) build(ctx context.Context, endpoint string, cred Credential, spec registry.ModelSpec, req Request, seed *int64) (*http.Request, error) {
	body := map[string]any{"model": spec.ModelID, "messages": anthropicMessages(req.Messages)}
	if req.System != "" {
		body["system"] = req.System // top-level, not a message — the OpenAI adapter does the opposite
	}
	// Anthropic REQUIRES max_tokens. Defaulting silently would pick a number nobody configured and
	// bake it into behavior the config_hash does not record, so this fails loud instead.
	if p := spec.Params.MaxTokens; p != nil {
		body["max_tokens"] = *p
	} else {
		return nil, fmt.Errorf("anthropic requires max_tokens; model entry %q does not set it", spec.ModelID)
	}
	if p := spec.Params.Temperature; p != nil {
		body["temperature"] = *p
	}
	if p := spec.Params.ThinkingBudget; p != nil && *p > 0 {
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": *p}
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"name": t.Name, "description": t.Description, "input_schema": t.InputSchema})
		}
		body["tools"] = tools
	}
	// Seed: Anthropic's Messages API has no seed parameter. Rather than drop it silently (the caller
	// asked for determinism and would never learn it was not honored), it is not sent and the
	// omission is a documented, tested fact — see TestComplete_SeedIsNotSilentlyDroppedForAnthropic.
	// Reproducibility is asserted on seed PROPAGATION, and there is nothing here to propagate to.
	_ = seed

	hr, err := jsonPost(ctx, strings.TrimRight(endpoint, "/")+"/messages", body)
	if err != nil {
		return nil, err
	}
	hr.Header.Set("x-api-key", cred.APIKey)
	hr.Header.Set("anthropic-version", "2023-06-01")
	if req.IdempotencyKey != "" {
		hr.Header.Set("Idempotency-Key", req.IdempotencyKey)
	}
	return hr, nil
}

// anthropicMessages converts normalized messages into Anthropic's content-block shape.
func anthropicMessages(in []Message) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, m := range in {
		switch m.Role {
		case RoleTool:
			out = append(out, map[string]any{"role": "user", "content": []map[string]any{{
				"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content}}})
		case RoleAssistant:
			blocks := []map[string]any{}
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input any
				if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
					input = map[string]any{}
				}
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": input})
			}
			out = append(out, map[string]any{"role": "assistant", "content": blocks})
		default:
			out = append(out, map[string]any{"role": string(m.Role), "content": m.Content})
		}
	}
	return out
}

func (anthropicAdapter) parse(body []byte) (*Response, error) {
	var p struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	if len(p.Content) == 0 && p.StopReason == "" {
		return nil, fmt.Errorf("response had neither content nor a stop_reason")
	}
	out := &Response{
		StopReason: normalizeStop(p.StopReason),
		Usage: Usage{
			InputTokens: p.Usage.InputTokens, OutputTokens: p.Usage.OutputTokens,
			CacheReadTokens: p.Usage.CacheReadInputTokens, CacheWriteTokens: p.Usage.CacheCreationInputTokens,
		},
	}
	var text []string
	for _, b := range p.Content {
		switch b.Type {
		case "text":
			text = append(text, b.Text)
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Arguments: string(b.Input)})
		}
	}
	out.Content = strings.TrimSpace(strings.Join(text, ""))
	return out, nil
}

// ── Bedrock (Converse) ───────────────────────────────────────────────────────────────────────────

type bedrockAdapter struct{}

func (bedrockAdapter) name() string { return ProviderBedrock }

func (bedrockAdapter) build(ctx context.Context, endpoint string, cred Credential, spec registry.ModelSpec, req Request, seed *int64) (*http.Request, error) {
	if cred.AWS == nil {
		return nil, fmt.Errorf("%w: bedrock requires AWS credentials", ErrNoCredential)
	}
	if cred.Region == "" {
		return nil, fmt.Errorf("%w: bedrock requires a region", ErrNoCredential)
	}
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", cred.Region)
	}

	inf := map[string]any{}
	if p := spec.Params.MaxTokens; p != nil {
		inf["maxTokens"] = *p
	}
	if p := spec.Params.Temperature; p != nil {
		inf["temperature"] = *p
	}
	body := map[string]any{"messages": bedrockMessages(req.Messages)}
	if len(inf) > 0 {
		body["inferenceConfig"] = inf
	}
	if req.System != "" {
		body["system"] = []map[string]any{{"text": req.System}}
	}
	if len(req.Tools) > 0 {
		specs := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			var schema any
			if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
				return nil, fmt.Errorf("tool %q has an unparseable input schema: %v", t.Name, err)
			}
			specs = append(specs, map[string]any{"toolSpec": map[string]any{
				"name": t.Name, "description": t.Description,
				"inputSchema": map[string]any{"json": schema}}})
		}
		body["toolConfig"] = map[string]any{"tools": specs}
	}
	_ = seed // Converse has no seed field; same reasoning as Anthropic above.

	url := fmt.Sprintf("%s/model/%s/converse", strings.TrimRight(endpoint, "/"), spec.ModelID)
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if req.IdempotencyKey != "" {
		hr.Header.Set("Idempotency-Key", req.IdempotencyKey)
	}

	// SigV4, via the AWS SDK's signer rather than a hand-rolled HMAC chain.
	//
	// Signing is cryptographic protocol code: an implementation is either exactly right or silently
	// rejected, and a bug in it is not something this package's tests could catch — a stub server
	// validating my signature with my own verifier proves only that I am consistently wrong. The SDK's
	// signer is the reference implementation, tested against AWS's own vectors. This is the one place
	// in P2 where taking a dependency is clearly cheaper than being clever.
	sum := sha256.Sum256(raw)
	signer := v4.NewSigner()
	err = signer.SignHTTP(ctx, awscreds.Credentials{
		AccessKeyID: cred.AWS.AccessKeyID, SecretAccessKey: cred.AWS.SecretAccessKey,
		SessionToken: cred.AWS.SessionToken,
	}, hr, hex.EncodeToString(sum[:]), "bedrock", cred.Region, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("sign bedrock request: %w", err)
	}
	return hr, nil
}

func bedrockMessages(in []Message) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, m := range in {
		switch m.Role {
		case RoleTool:
			out = append(out, map[string]any{"role": "user", "content": []map[string]any{{
				"toolResult": map[string]any{"toolUseId": m.ToolCallID,
					"content": []map[string]any{{"text": m.Content}}}}}})
		case RoleAssistant:
			blocks := []map[string]any{}
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input any
				if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
					input = map[string]any{}
				}
				blocks = append(blocks, map[string]any{"toolUse": map[string]any{
					"toolUseId": tc.ID, "name": tc.Name, "input": input}})
			}
			out = append(out, map[string]any{"role": "assistant", "content": blocks})
		default:
			out = append(out, map[string]any{"role": string(m.Role),
				"content": []map[string]any{{"text": m.Content}}})
		}
	}
	return out
}

func (bedrockAdapter) parse(body []byte) (*Response, error) {
	var p struct {
		Output struct {
			Message struct {
				Content []struct {
					Text    string `json:"text"`
					ToolUse *struct {
						ToolUseID string          `json:"toolUseId"`
						Name      string          `json:"name"`
						Input     json.RawMessage `json:"input"`
					} `json:"toolUse"`
				} `json:"content"`
			} `json:"message"`
		} `json:"output"`
		StopReason string `json:"stopReason"`
		Usage      struct {
			InputTokens  int `json:"inputTokens"`
			OutputTokens int `json:"outputTokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	if len(p.Output.Message.Content) == 0 && p.StopReason == "" {
		return nil, fmt.Errorf("response had neither output content nor a stopReason")
	}
	out := &Response{
		StopReason: normalizeStop(p.StopReason),
		Usage:      Usage{InputTokens: p.Usage.InputTokens, OutputTokens: p.Usage.OutputTokens},
	}
	var text []string
	for _, b := range p.Output.Message.Content {
		if b.ToolUse != nil {
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID: b.ToolUse.ToolUseID, Name: b.ToolUse.Name, Arguments: string(b.ToolUse.Input)})
			continue
		}
		if b.Text != "" {
			text = append(text, b.Text)
		}
	}
	out.Content = strings.TrimSpace(strings.Join(text, ""))
	return out, nil
}

// ── shared ───────────────────────────────────────────────────────────────────────────────────────

// normalizeStop maps each provider's stop vocabulary onto one. Unknown values become StopOther
// rather than being passed through: a caller branching on StopReason must never receive a raw
// provider string it has no case for.
func normalizeStop(s string) StopReason {
	switch strings.ToLower(s) {
	case "stop", "end_turn", "stop_sequence":
		return StopEndTurn
	case "length", "max_tokens":
		return StopMaxTokens
	case "tool_calls", "tool_use":
		return StopToolUse
	case "content_filter", "guardrail_intervened", "refusal":
		return StopContentFilter
	case "":
		return StopOther
	default:
		return StopOther
	}
}

func jsonPost(ctx context.Context, url string, body map[string]any) (*http.Request, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	return hr, nil
}
