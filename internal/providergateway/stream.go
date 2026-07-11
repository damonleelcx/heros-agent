package providergateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

type streamToolAcc struct {
	ID   string
	Name string
	Args strings.Builder
}

// ChatCompletionStream calls chat/completions with stream:true, prints deltas via onDelta,
// and returns final aggregated content and tool calls (if any).
func ChatCompletionStream(ctx context.Context, baseURL, apiKey, model string, messages []map[string]any, tools []map[string]any, opt *ChatOptions, onDelta func(string) error) (content string, toolCalls []ToolCall, err error) {
	if model == "" {
		model = "gpt-4o-mini"
	}
	body := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   true,
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
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		rb, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("openai stream %s: %s", resp.Status, string(rb))
	}

	var contentBuf strings.Builder
	toolMap := map[int]*streamToolAcc{}
	sc := bufio.NewScanner(resp.Body)
	// Large argument fragments
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var ev struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		if len(ev.Choices) == 0 {
			continue
		}
		ch := ev.Choices[0]
		if ch.Delta.Content != "" {
			contentBuf.WriteString(ch.Delta.Content)
			if onDelta != nil {
				if err := onDelta(ch.Delta.Content); err != nil {
					return "", nil, err
				}
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			idx := tc.Index
			if _, ok := toolMap[idx]; !ok {
				toolMap[idx] = &streamToolAcc{}
			}
			acc := toolMap[idx]
			if tc.ID != "" {
				acc.ID = tc.ID
			}
			if tc.Function.Name != "" {
				acc.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				acc.Args.WriteString(tc.Function.Arguments)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", nil, err
	}

	indices := make([]int, 0, len(toolMap))
	for i := range toolMap {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	for _, i := range indices {
		acc := toolMap[i]
		if acc.Name == "" && acc.Args.Len() == 0 && acc.ID == "" {
			continue
		}
		toolCalls = append(toolCalls, ToolCall{
			ID:        acc.ID,
			Name:      acc.Name,
			Arguments: acc.Args.String(),
		})
	}
	return strings.TrimSpace(contentBuf.String()), toolCalls, nil
}
