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
			"description": "PRIMARY way to observe the user's project: runs on THIS machine with cwd = workspace root. REQUIRED before answering questions about repo layout, purpose, stack, or files (use dir/ls, type/cat README*, package.json, src/, etc.). Do not claim you lack context without calling this first. Prefer read-only commands first; Windows: dir, type, where; Unix: ls, cat, find.",
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
				"name":        "heros_list_files",
				"description": "List files/directories on the local machine. Use this first when user asks to inspect project files or locate where to create/edit files.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":        map[string]any{"type": "string", "description": "Absolute path or path relative to workspace root"},
						"recursive":   map[string]any{"type": "boolean", "description": "Walk subdirectories recursively (default false)"},
						"max_entries": map[string]any{"type": "integer", "description": "Result cap, default 200, max 5000"},
					},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_read_file",
				"description": "Read file content from local filesystem. Supports text and base64. Use this before answering detailed file questions.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":     map[string]any{"type": "string", "description": "Absolute path or path relative to workspace root"},
						"encoding": map[string]any{"type": "string", "description": "text (default) or base64"},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_write_file",
				"description": "Create or update files directly on local filesystem. Use this to implement requested file changes instead of only describing steps.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":        map[string]any{"type": "string", "description": "Absolute path or path relative to workspace root"},
						"content":     map[string]any{"type": "string", "description": "File content. For binary use base64 in content with encoding=base64"},
						"encoding":    map[string]any{"type": "string", "description": "text (default) or base64"},
						"append":      map[string]any{"type": "boolean", "description": "Append instead of overwrite (default false)"},
						"create_dirs": map[string]any{"type": "boolean", "description": "Auto-create parent dirs (default true)"},
					},
					"required": []string{"path", "content"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_make_dir",
				"description": "Create directories on local filesystem.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":      map[string]any{"type": "string"},
						"recursive": map[string]any{"type": "boolean", "description": "mkdir -p behavior (default true)"},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_delete_path",
				"description": "Delete a file or directory from local filesystem.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":      map[string]any{"type": "string"},
						"recursive": map[string]any{"type": "boolean", "description": "required for non-empty directories"},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_memory_search",
				"description": "Search prior facts and this session (episodic + vectors). REQUIRED when the user asks what you remember / what is in memory — never claim empty memory without calling this. Also use at the start of long-horizon work. Current chat is indexed for search without heros_memory_save.",
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
				"description": "Load full SKILL.md from the agent catalog (file lives under heros data_dir/skills/..., not the workspace). Call when a listed skill matches the task instead of guessing paths or procedures.",
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
				"name":        "heros_run_harness",
				"description": "Run the built-in multi-actor harness (leader decomposes goal → rotating specialists → critic) in the same heros process as POST /api/harness/run. Use when the user wants a structured multi-perspective pass, risk/quality review, or explicit decomposition—not for trivial one-liner questions. Slower and more LLM calls than normal chat; prefer heros_shell for simple file inspection.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"goal": map[string]any{"type": "string", "description": "High-level objective to plan and execute through the harness pipeline"},
					},
					"required": []string{"goal"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_list_pending_proposals",
				"description": "List proposals awaiting approval (same as REPL /pending). Returns a numbered list for the user to pick from. Always use first when the user says approve/reject without giving an id, or when multiple proposals exist.",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_approve_proposal",
				"description": "Apply one pending proposal by id (same as REPL /approve and the web UI). If several are pending, list first and wait until the user picks a number or id — do not guess. If exactly one is pending and the user clearly means that queue, you may approve that id.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"proposal_id": map[string]any{"type": "string", "description": "id field from heros_submit_proposal response or heros_list_pending_proposals"},
					},
					"required": []string{"proposal_id"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_reject_proposal",
				"description": "Reject a pending proposal (same as REPL /reject <id>).",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"proposal_id": map[string]any{"type": "string"},
					},
					"required": []string{"proposal_id"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_submit_proposal",
				"description": "YOU call this when you detect a missing or weak capability—do not wait for the user to ask for a proposal. Queue a concrete skill/tool/memory/harness change for human approval. Response JSON includes **id** — keep it; the user can approve in chat via **heros_approve_proposal** (or slash /approve). New skills: layer=prompt_engineering and diff with '### SKILL:slug' then markdown body. Tooling: JSON register payloads. After approval, org collective (if collective_url) receives the vetted mutation for fleet sync.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"layer": map[string]any{
							"type":        "string",
							"description": "Use prompt_engineering for new/updated SKILL.md content (### SKILL:slug blocks). context_engineering | harness_engineering | tooling for other mutations.",
							"enum":        []string{"prompt_engineering", "context_engineering", "harness_engineering", "tooling"},
						},
						"title":     map[string]any{"type": "string"},
						"rationale": map[string]any{"type": "string"},
						"diff":      map[string]any{"type": "string", "description": "For skills: markdown with '### SKILL:my_skill' header then body. For tooling: JSON per server docs."},
						"target_tenant": map[string]any{
							"type":        "string",
							"description": "Optional; admin API keys only — submit on behalf of tenant",
						},
					},
					"required": []string{"layer", "title", "rationale", "diff"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "heros_extension_tool",
				"description": "Run additional catalog tools (Go-backed) registered under tools/_global/<id>/tool.yaml. Pass tool_id matching the yaml id and an arguments object. Examples: tool_id=terminal-tool & {command}; tool_id=file-operations & {action:\"read\",path:\"README.md\"}; tool_id=memory-tool & {action:\"search\",query:\"...\"}; tool_id=web-tools & {url:\"https://...\"}.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tool_id": map[string]any{
							"type":        "string",
							"description": "Catalog tool id (directory name under tools/_global)",
						},
						"arguments": map[string]any{
							"type":        "object",
							"description": "Tool-specific parameters",
						},
					},
					"required": []string{"tool_id"},
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
