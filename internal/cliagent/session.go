package cliagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Session holds multi-turn chat state for one terminal session.
type Session struct {
	Agentd     *AgentdClient
	OpenAIBase string
	OpenAIKey  string
	Model      string
	SessionID  string
	// WorkDir is the absolute workspace root for heros_shell (local CLI host).
	WorkDir string
	// AgentShell enables heros_agent_shell (runs on agentd with server policy).
	AgentShell bool
	// Stream uses SSE streaming for assistant text when supported by the API.
	Stream bool
	// UseReadline enables line editing and history in the REPL.
	UseReadline bool
	// TargetTenant is optional default for heros_submit_proposal when the model omits target_tenant (admin keys).
	TargetTenant string
	// LogTurnsToEpisodic appends user+assistant lines to agentd episodic memory each turn.
	LogTurnsToEpisodic bool

	Messages []map[string]any
}

// PrimeSystem loads server system prompt + folder catalog block into Messages.
func (s *Session) PrimeSystem(ctx context.Context) error {
	base, err := s.Agentd.SystemPrompt(ctx)
	if err != nil {
		base = ""
	}
	block, err := s.Agentd.BuildContextBlock(ctx)
	if err != nil {
		return err
	}
	wd := strings.TrimSpace(s.WorkDir)
	if wd == "" {
		wd = "."
	}
	instructions := fmt.Sprintf(`You are the Heros OS-level agent CLI. You help with real work (code, pipelines, creative/production tasks).
Workspace root on this machine: %s
- heros_shell runs locally in that directory (git, builds, file ops on the developer machine).
- heros_agent_shell (if available) runs on the agentd server under server policy — use only when the task must execute there.
You have folder-backed skills and tool catalog from agentd, semantic memory, optional Neo4j graph, and heros_submit_proposal to queue self-evolution changes for human approval.
Use tools when they reduce error or fetch ground truth. Be concise in the terminal; say briefly what you are doing when invoking shell or graph calls.`,
		wd)

	full := strings.TrimSpace(base) + "\n\n" + instructions + "\n\n" + block
	s.Messages = []map[string]any{
		{"role": "system", "content": full},
	}
	return nil
}

// RefreshContext updates only the catalog section by re-fetching skills/tools (keeps conversation).
func (s *Session) RefreshContext(ctx context.Context) error {
	if len(s.Messages) == 0 {
		return s.PrimeSystem(ctx)
	}
	block, err := s.Agentd.BuildContextBlock(ctx)
	if err != nil {
		return err
	}
	// Prepend refreshed catalog as a synthetic system note (simplest without re-parsing first message).
	s.Messages = append(s.Messages, map[string]any{
		"role": "system",
		"content": "[catalog refresh]\n" + block,
	})
	return nil
}

// RunUserTurn runs one user message to completion (including tool loops).
func (s *Session) RunUserTurn(ctx context.Context, user string, out io.Writer) error {
	user = strings.TrimSpace(user)
	if user == "" {
		return nil
	}
	s.Messages = append(s.Messages, map[string]any{"role": "user", "content": user})

	tools := OpenAITools(ToolOptions{AgentShell: s.AgentShell})
	for step := 0; step < 48; step++ {
		var content string
		var calls []ToolCall
		var err error
		if s.Stream {
			content, calls, err = ChatCompletionStream(ctx, s.OpenAIBase, s.OpenAIKey, s.Model, s.Messages, tools, func(d string) error {
				_, werr := fmt.Fprint(out, d)
				return werr
			})
		} else {
			content, calls, err = ChatCompletion(ctx, s.OpenAIBase, s.OpenAIKey, s.Model, s.Messages, tools)
		}
		if err != nil {
			return err
		}

		assistantMsg := map[string]any{"role": "assistant"}
		if strings.TrimSpace(content) != "" {
			assistantMsg["content"] = content
		} else {
			assistantMsg["content"] = nil
		}
		if len(calls) > 0 {
			var tcs []any
			for _, c := range calls {
				tcs = append(tcs, map[string]any{
					"id":   c.ID,
					"type": "function",
					"function": map[string]any{
						"name":      c.Name,
						"arguments": c.Arguments,
					},
				})
			}
			assistantMsg["tool_calls"] = tcs
		}
		s.Messages = append(s.Messages, assistantMsg)

		if len(calls) == 0 {
			if !s.Stream && content != "" {
				_, _ = fmt.Fprintln(out, content)
			}
			if s.Stream && content != "" {
				_, _ = fmt.Fprint(out, "\n")
			}
			if s.LogTurnsToEpisodic {
				_ = s.Agentd.MemoryEpisodic(ctx, s.SessionID, "user", user, 0.2)
				_ = s.Agentd.MemoryEpisodic(ctx, s.SessionID, "assistant", content, 0.3)
			}
			return nil
		}

		for _, c := range calls {
			result, err := s.DispatchTool(ctx, c)
			if err != nil {
				result = "error: " + err.Error()
			}
			s.Messages = append(s.Messages, map[string]any{
				"role":         "tool",
				"tool_call_id": c.ID,
				"content":      result,
			})
		}
	}
	return fmt.Errorf("tool loop exceeded step limit")
}

// DispatchTool executes one model tool call against agentd.
func (s *Session) DispatchTool(ctx context.Context, tc ToolCall) (string, error) {
	var args map[string]any
	if tc.Arguments != "" {
		_ = json.Unmarshal([]byte(tc.Arguments), &args)
	}
	if args == nil {
		args = map[string]any{}
	}
	switch tc.Name {
	case "heros_shell":
		cmd, _ := args["command"].(string)
		if strings.TrimSpace(cmd) == "" {
			return "", fmt.Errorf("missing command")
		}
		wd := strings.TrimSpace(s.WorkDir)
		if wd == "" {
			wd = "."
		}
		out, shellErr := RunLocalShell(ctx, wd, cmd)
		return LocalShellResult(out, shellErr), nil
	case "heros_agent_shell":
		cmd, _ := args["command"].(string)
		if strings.TrimSpace(cmd) == "" {
			return "", fmt.Errorf("missing command")
		}
		r, err := s.Agentd.CLIExec(ctx, cmd)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(r)
		return string(b), nil
	case "heros_memory_search":
		q, _ := args["query"].(string)
		k := 8
		if v, ok := args["k"].(float64); ok && v > 0 {
			k = int(v)
		}
		chunks, backend, err := s.Agentd.MemoryRetrieve(ctx, q, k)
		if err != nil {
			return "", err
		}
		out := map[string]any{"backend": backend, "chunks": chunks}
		b, _ := json.Marshal(out)
		return string(b), nil
	case "heros_memory_save":
		note, _ := args["note"].(string)
		imp := 0.4
		if v, ok := args["importance"].(float64); ok {
			imp = v
		}
		err := s.Agentd.MemoryEpisodic(ctx, s.SessionID, "note", note, imp)
		if err != nil {
			return "", err
		}
		return `{"status":"saved"}`, nil
	case "heros_read_skill":
		name, _ := args["name"].(string)
		body, err := s.Agentd.SkillBody(ctx, name)
		if err != nil {
			return "", err
		}
		return body, nil
	case "heros_graph_neighbors":
		id, _ := args["entity_id"].(string)
		g, err := s.Agentd.GraphNeighbors(ctx, id)
		if err != nil {
			return fmt.Sprintf(`{"error":%q}`, err.Error()), nil
		}
		b, _ := json.Marshal(g)
		return string(b), nil
	case "heros_submit_proposal":
		layer, _ := args["layer"].(string)
		title, _ := args["title"].(string)
		rationale, _ := args["rationale"].(string)
		diff, _ := args["diff"].(string)
		target, _ := args["target_tenant"].(string)
		if strings.TrimSpace(target) == "" {
			target = s.TargetTenant
		}
		if strings.TrimSpace(layer) == "" || strings.TrimSpace(title) == "" || strings.TrimSpace(rationale) == "" || strings.TrimSpace(diff) == "" {
			return "", fmt.Errorf("heros_submit_proposal requires layer, title, rationale, diff")
		}
		resp, err := s.Agentd.SubmitProposal(ctx, layer, title, rationale, diff, target)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(resp)
		return string(b), nil
	default:
		return "", fmt.Errorf("unknown tool %q", tc.Name)
	}
}

// RunREPL is a simple stdin loop; slash-commands are handled by caller or here.
func RunREPL(ctx context.Context, s *Session, in io.Reader, out io.Writer, errOut io.Writer) error {
	if err := s.PrimeSystem(ctx); err != nil {
		return fmt.Errorf("prime system: %w", err)
	}
	_, _ = fmt.Fprintf(out, "heros-cli — session=%s  workdir=%s  stream=%v (Ctrl+D or /exit to quit)\n",
		s.SessionID, s.WorkDir, s.Stream)
	br := bufio.NewReader(in)
	for {
		_, _ = fmt.Fprint(out, "> ")
		line, err := br.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if strings.TrimSpace(line) != "" {
					_ = s.RunUserTurn(ctx, strings.TrimSpace(line), out)
				}
				_, _ = fmt.Fprintln(out, "\nbye")
				return nil
			}
			return err
		}
		line = strings.TrimSpace(line)
		if strings.TrimSpace(line) == "" {
			continue
		}
		switch {
		case line == "/exit", line == "/quit":
			_, _ = fmt.Fprintln(out, "bye")
			return nil
		case line == "/help":
			printHelp(out)
			continue
		case line == "/refresh":
			if err := s.RefreshContext(ctx); err != nil {
				_, _ = fmt.Fprintf(errOut, "refresh: %v\n", err)
			} else {
				_, _ = fmt.Fprintln(out, "(catalog block appended to context)")
			}
			continue
		case strings.HasPrefix(line, "/"):
			_, _ = fmt.Fprintf(errOut, "unknown command %q (try /help)\n", line)
			continue
		}
		if err := s.RunUserTurn(ctx, line, out); err != nil {
			_, _ = fmt.Fprintf(errOut, "error: %v\n", err)
		}
	}
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, `Commands:
  /exit, /quit  — leave
  /refresh      — re-fetch folder skill + tool catalog from agentd
  /help         — this text
Anything else is sent to the model (OpenAI-compatible API).
heros_shell runs on this machine in -workdir; use heros_agent_shell only for server-side execution (flag -agent-shell).`)
}

// StdioREPL runs on os.Stdin/Stdout/Stderr.
func (s *Session) StdioREPL(ctx context.Context) error {
	if s.UseReadline {
		return RunReadlineREPL(ctx, s, os.Stdout, os.Stderr)
	}
	return RunREPL(ctx, s, os.Stdin, os.Stdout, os.Stderr)
}
