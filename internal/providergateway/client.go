package providergateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ToolCall is one OpenAI-style function call from the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // raw JSON object string
}

// ChatOptions configures one chat/completions call (tool policy, etc.).
type ChatOptions struct {
	// ToolChoice is sent as JSON field "tool_choice" when non-nil (e.g. "required" on OpenAI forces ≥1 tool call).
	ToolChoice any
}

// ChatCompletion calls OpenAI-compatible POST .../chat/completions.
func ChatCompletion(ctx context.Context, baseURL, apiKey, model string, messages []map[string]any, tools []map[string]any, opt *ChatOptions) (content string, toolCalls []ToolCall, err error) {
	if model == "" {
		model = "gpt-4o-mini"
	}
	body := map[string]any{
		"model":    model,
		"messages": messages,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	if opt != nil && opt.ToolChoice != nil {
		body["tool_choice"] = opt.ToolChoice
	}
	b, err := json.Marshal(body)
	if err != nil {
		return "", nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("openai %s: %s", resp.Status, string(rb))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rb, &parsed); err != nil {
		return "", nil, err
	}
	if len(parsed.Choices) == 0 {
		return "", nil, fmt.Errorf("no choices")
	}
	msg := parsed.Choices[0].Message
	for _, tc := range msg.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return strings.TrimSpace(msg.Content), toolCalls, nil
}
