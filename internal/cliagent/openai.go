package cliagent

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
	ID       string
	Name     string
	Arguments string // raw JSON object string
}

// ChatCompletion calls OpenAI-compatible POST .../chat/completions.
func ChatCompletion(ctx context.Context, baseURL, apiKey, model string, messages []map[string]any, tools []map[string]any) (content string, toolCalls []ToolCall, err error) {
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

// ToolOptions configures which tools are registered for the model.
type ToolOptions struct {
	// AgentShell registers heros_agent_shell (runs on agentd host with server policy).
	AgentShell bool
}

// OpenAITools returns function definitions for the CLI agent.
func OpenAITools(opts ToolOptions) []map[string]any {
	localShell := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "heros_shell",
			"description": "Run a shell command on THIS machine (the CLI process), with cwd set to the configured workspace root. Use for git, builds, local file inspection. Prefer read-only commands first.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "Full shell command line"},
				},
				"required": []string{"command"},
			},
		},
	}
	agentShell := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "heros_agent_shell",
			"description": "Run shell on the agentd SERVER host with server risk policy and audit (only when the task must execute on the server, not your laptop).",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string"},
				},
				"required": []string{"command"},
			},
		},
	}
	rest := []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_memory_search",
				"description": "Semantic / episodic memory search via agentd (Qdrant when configured, else SQLite).",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
						"k":     map[string]any{"type": "integer", "description": "max chunks, default 8"},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_memory_save",
				"description": "Append an episodic note to this CLI session (persisted under memory/<tenant>/sessions/...).",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"note":       map[string]any{"type": "string"},
						"importance": map[string]any{"type": "number", "description": "0-1, optional"},
					},
					"required": []string{"note"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_read_skill",
				"description": "Load full SKILL.md body for a logical skill name from the folder-backed catalog.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string", "description": "Skill name from catalog"},
					},
					"required": []string{"name"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_graph_neighbors",
				"description": "Query Neo4j graph neighbors for an entity id (requires agentd enterprise Neo4j).",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"entity_id": map[string]any{"type": "string"},
					},
					"required": []string{"entity_id"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_submit_proposal",
				"description": "Queue a self-evolution change for human approval on agentd (updates skills, prompts, tools, harness, or context when approved).",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"layer": map[string]any{
							"type":        "string",
							"description": "One of: prompt_engineering | context_engineering | harness_engineering | tooling",
							"enum":        []string{"prompt_engineering", "context_engineering", "harness_engineering", "tooling"},
						},
						"title":     map[string]any{"type": "string"},
						"rationale": map[string]any{"type": "string"},
						"diff":      map[string]any{"type": "string", "description": "Human-readable diff or JSON (tooling layer uses register JSON)"},
						"target_tenant": map[string]any{
							"type":        "string",
							"description": "Optional; admin API keys only — submit on behalf of tenant",
						},
					},
					"required": []string{"layer", "title", "rationale", "diff"},
				},
			},
		},
	}
	var out []map[string]any
	out = append(out, localShell)
	if opts.AgentShell {
		out = append(out, agentShell)
	}
	out = append(out, rest...)
	return out
}
