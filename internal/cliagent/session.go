package cliagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/harness"
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
	// DataDir is the agent filesystem root (skills/, tools/, system/, memory/); not the workspace.
	DataDir string
	// UserID scopes long-term profile retrieval; empty maps to server default profile.
	UserID string
	// AutoInjectMemory prepends small retrieved memory context each user turn.
	AutoInjectMemory bool
	// AutoInjectTopK controls retrieval size for per-turn memory injection.
	AutoInjectTopK int
	// AutoConsolidateEvery runs /api/memory/consolidate every N user turns (0 disables).
	AutoConsolidateEvery int
	// AutoConsolidateThreshold is the importance threshold for periodic consolidation.
	AutoConsolidateThreshold float64

	Messages []map[string]any
	turnOut  io.Writer
	turnN    int
}

// PrimeSystem loads server system prompt + folder catalog block into Messages.
func (s *Session) PrimeSystem(ctx context.Context) error {
	base, err := s.Agentd.SystemPrompt(ctx)
	if err != nil {
		base = ""
	}
	block, err := s.Agentd.BuildContextBlock(ctx, s.DataDir)
	if err != nil {
		return err
	}
	wd := strings.TrimSpace(s.WorkDir)
	if wd == "" {
		wd = "."
	}
	dataDirAbs := ""
	if d := strings.TrimSpace(s.DataDir); d != "" {
		if abs, err := filepath.Abs(d); err == nil {
			dataDirAbs = filepath.Clean(abs)
		} else {
			dataDirAbs = filepath.Clean(d)
		}
	}
	if dataDirAbs == "" {
		dataDirAbs = "(not set — match agentd `data_dir` from the heros startup log)"
	}
	harnessLine := ""
	if topo, err := s.Agentd.FetchTopology(ctx); err == nil {
		harnessLine = fmt.Sprintf(
			"\n**Harness (built into heros):** specialists=%v, leader_model=%q, critic_threshold=%.2f.\n"+
				"For multi-actor runs use tool **heros_run_harness** with a **goal** (equivalent to REPL /harness). "+
				"Use when decomposition, specialist angles, and critic review help; skip for trivial asks. "+
				"Otherwise use **heros_shell** across multiple turns.\n",
			topo.Specialists, topo.LeaderModel, topo.CriticThreshold)
	}
	instructions := fmt.Sprintf(`You are the **Heros OS agent** (not a passive chatbot): you **drive** work with **tools**, **catalog skills**, and **memory** — all inside the **heros** process (embedded daemon + data_dir). There is no separate product the user must start for daily use.

Workspace root on this machine: **%s**
- **heros_shell** — cwd is that directory. This is how you **see** the project (dir/ls, type/cat README*, package.json, src/, git status, etc.).
- **heros_agent_shell** — only when a command must run under the **embedded HTTP daemon’s** server-side CLI policy (same machine as heros), not your local shell defaults.

**Agent data_dir** on this machine: **%s**
- **Skills**, **tools**, **system/** (e.g. prompt.md), and **memory/** trees live under **this** heros home — **not** under the workspace root above.
- Catalog **rel_path** values (example: skills/_global/foo/SKILL.md) are relative to **data_dir** only. When the user asks for the filesystem path to a skill, give **data_dir** plus that rel_path (see **abs** in the catalog block) or **heros_read_skill** — **do not** prepend the workspace path unless **heros_shell** proves a copy exists there.

**Non‑negotiable grounding:** If the user asks what the project/repo/app **is**, does, or contains, you **must** use **heros_shell** (and optionally **heros_read_skill** / **heros_memory_search**) **before** answering. It is a **failure** to reply with generic “I need you to tell me…” or “I have no information” while a workspace path is set — inspect the tree first.

**Artifact-first filesystem rule (global):** Any request to create, update, delete, move, inspect, or read files/folders is an execution task. Use tools and perform the operation on disk; do not respond with instructions-only unless explicitly requested.
Use **heros_list_files / heros_read_file / heros_write_file / heros_make_dir / heros_delete_path** for direct filesystem work, and **heros_shell** when conversion/generation tooling is needed.
When a user asks for a deliverable (pptx/docx/xlsx/pdf/image/zip/etc), create the real artifact file in the workspace whenever possible and return its path. Do not stop at outline text.
For binary/container formats (for example **.pptx/.docx/.xlsx/.pdf/.png/.jpg/.zip**), do **not** write plain text bytes with **heros_write_file** to that extension. Use a generator path (for example **heros_shell** with a real exporter such as python-pptx, pandoc, libreoffice, etc.) that produces valid binary bytes. Only fall back to .md/.txt when generation is impossible.
For binary reads/writes with file tools, use encoding=base64 when needed instead of refusing.

**Presentation requests:** If the user asks for a PowerPoint or says "I need ppt/pptx file", generate an actual **.pptx** in the workspace (for example via **heros_shell** + python-pptx), verify the file exists with **heros_list_files** or **heros_read_file** metadata, then respond with the produced file path.

**Chat output style:** always return markdown suitable for UI preview rendering. Use fenced code blocks for code by default.
Before substantial tool work, print one short progress line describing what you are doing now.
When the user asks to implement/build/edit, execute autonomously without confirmation loops. Ask a question only when blocked by missing required input or a risky/destructive action.
Do not ask "shall I continue" / "would you like me to proceed" after partial progress; continue until the requested implementation and validation are complete.
When the user asks to run tests, do not assume repository root has the test manifest. First inspect workspace structure and detect test entrypoints in subdirectories (for example backend/frontend, go.mod, package.json, pyproject.toml, Cargo.toml). Run the relevant test commands in each detected project folder, then report a consolidated result.

**Long‑horizon tasks:** Break work into steps; after substantive progress call **heros_memory_save** or rely on session episodic logs; use **heros_memory_search** to recall earlier decisions in the same thread.

**Memory questions:** If the user asks what you remember / what is in memory / episodic recall, call **heros_memory_search** with a short query — do not invent “no memory” without querying.

**Governance (skills/tools):** Missing capability → **heros_submit_proposal** (response includes **id**).

**Approving / rejecting (user may not know /pending):** If they say "approve the skill" (or similar) without an id:
1) Call **heros_list_pending_proposals** — the tool returns a **numbered list** + JSON.
2) **Show them the numbered list** in your reply (title, layer, id per row).
3) If **more than one** pending, **do not** call **heros_approve_proposal** until they disambiguate — ask them to reply **"approve 2"**, **"reject 3"**, or paste the **id**. Only auto-pick when there is **exactly one** pending and they said "approve it" clearly referring to that queue.
4) Same pattern for reject. After approve, suggest **heros_read_skill** or **/refresh**.

Slash **/pending** lists pending; **/approve** plus proposal id, or **/reject** plus id, matches the tools. **/approve** or **/reject** alone re-lists pending and prints usage.

Collective sync when **collective_url** is set.

Skill proposal shape (layer **prompt_engineering**):
### SKILL:skill_slug_here
(markdown body)

Be concise in the terminal; one short plain-text status line before heavy tool use is enough.%s`,
		wd, dataDirAbs, harnessLine)

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
	block, err := s.Agentd.BuildContextBlock(ctx, s.DataDir)
	if err != nil {
		return err
	}
	// Prepend refreshed catalog as a synthetic system note (simplest without re-parsing first message).
	s.Messages = append(s.Messages, map[string]any{
		"role":    "system",
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
	if note := s.buildAutoMemoryInjection(ctx, user); strings.TrimSpace(note) != "" {
		s.Messages = append(s.Messages, map[string]any{"role": "system", "content": note})
	}
	s.turnOut = out
	defer func() { s.turnOut = nil }()
	toolUsed := map[string]bool{}
	skillUsed := map[string]bool{}
	memoryUsed := false
	toolCalls := []ToolCallUsage{}
	requireExecution := FileActionGroundingRequired(user)
	harnessUsed := false
	executionPromptInjected := false

	tools := OpenAITools(ToolOptions{AgentShell: s.AgentShell})
	var firstToolChoice any
	switch {
	case WorkspaceGroundingRequired(user, s.WorkDir):
		firstToolChoice = "required"
	case LongHorizonHarnessRequired(user):
		// Prefer harness for complex implementation/integration asks to show todo + sub-agent flow.
		firstToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": "heros_run_harness"}}
	case FileActionGroundingRequired(user):
		firstToolChoice = "required"
	case MemoryGroundingRequired(user):
		// Prefer memory search over guessing when user asks what is stored.
		firstToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": "heros_memory_search"}}
	}
	for step := 0; step < 64; step++ {
		modelStart := time.Now().UTC()
		_, _ = fmt.Fprint(out, "\n")
		emitHarnessStart(out, "assistant", "", "", modelStart)
		var cc *ChatOptions
		if step == 0 && firstToolChoice != nil {
			cc = &ChatOptions{ToolChoice: firstToolChoice}
		} else if requireExecution && harnessUsed {
			// After planning, force concrete execution tools instead of final prose-only replies.
			cc = &ChatOptions{ToolChoice: "required"}
		}
		var content string
		var calls []ToolCall
		var err error
		if s.Stream {
			content, calls, err = ChatCompletionStream(ctx, s.OpenAIBase, s.OpenAIKey, s.Model, s.Messages, tools, cc, func(d string) error {
				_, werr := fmt.Fprint(out, d)
				return werr
			})
		} else {
			content, calls, err = ChatCompletion(ctx, s.OpenAIBase, s.OpenAIKey, s.Model, s.Messages, tools, cc)
		}
		if err != nil {
			modelEnd := time.Now().UTC()
			emitHarnessEnd(out, "assistant", "", "", "error", modelStart, modelEnd)
			return err
		}
		modelEnd := time.Now().UTC()
		emitHarnessEnd(out, "assistant", "", "", "ok", modelStart, modelEnd)

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
				memoryUsed = true // automatic user/assistant turn logging into episodic memory
			}
			_, _ = fmt.Fprintln(out, FormatUsageDisclosure(toolUsed, skillUsed, memoryUsed, toolCalls))
			if s.LogTurnsToEpisodic {
				if err := s.Agentd.MemoryEpisodic(ctx, s.SessionID, "user", user, 0.2); err != nil {
					log.Printf("heros-cli: episodic user turn: %v", err)
				}
				if err := s.Agentd.MemoryEpisodic(ctx, s.SessionID, "assistant", content, 0.3); err != nil {
					log.Printf("heros-cli: episodic assistant turn: %v", err)
				}
				s.turnN++
				s.maybeAutoConsolidate(ctx)
			}
			return nil
		}

		for _, c := range calls {
			toolStart := time.Now().UTC()
			_, _ = fmt.Fprint(out, "\n")
			emitHarnessStart(out, "tool", c.ID, c.Name, toolStart)
			toolUsed[c.Name] = true
			if strings.HasPrefix(c.Name, "heros_memory_") {
				memoryUsed = true
			}
			if c.Name == "heros_read_skill" && strings.TrimSpace(c.Arguments) != "" {
				var a map[string]any
				if err := json.Unmarshal([]byte(c.Arguments), &a); err == nil {
					if n := strings.TrimSpace(ArgString(a, "name")); n != "" {
						skillUsed[n] = true
					}
				}
			}
			result, err := s.DispatchTool(ctx, c)
			toolEnd := time.Now().UTC()
			status := "ok"
			if err != nil {
				result = "error: " + err.Error()
				status = "error"
				emitHarnessEnd(out, "tool", c.ID, c.Name, "error", toolStart, toolEnd)
			} else {
				emitHarnessEnd(out, "tool", c.ID, c.Name, "ok", toolStart, toolEnd)
			}
			toolCalls = append(toolCalls, ToolCallUsage{
				Name:       c.Name,
				Status:     status,
				DurationMS: toolEnd.Sub(toolStart).Milliseconds(),
			})
			if c.Name == "heros_run_harness" {
				harnessUsed = true
			}
			s.Messages = append(s.Messages, map[string]any{
				"role":         "tool",
				"tool_call_id": c.ID,
				"content":      result,
			})
			if requireExecution && c.Name == "heros_run_harness" && !executionPromptInjected {
				s.Messages = append(s.Messages, map[string]any{
					"role":    "system",
					"content": "Execution required now. Do the work in the repository: create/modify files, run tests/build commands, fix failures, and only finish when validation passes or you are blocked by a hard external dependency. Do not ask the user for confirmation mid-task.",
				})
				executionPromptInjected = true
			}
		}
	}
	return fmt.Errorf("tool loop exceeded step limit")
}

func (s *Session) buildAutoMemoryInjection(ctx context.Context, user string) string {
	if !s.AutoInjectMemory {
		return ""
	}
	q := strings.TrimSpace(user)
	if q == "" {
		return ""
	}
	k := s.AutoInjectTopK
	if k <= 0 {
		k = 3
	}
	chunks, backend, err := s.Agentd.MemoryRetrieve(ctx, s.SessionID, q, k)
	if err != nil {
		return ""
	}
	profileText, _ := s.Agentd.MemoryProfile(ctx, s.UserID)
	if len(chunks) == 0 && strings.TrimSpace(profileText) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("[auto-memory]\n")
	if strings.TrimSpace(profileText) != "" {
		b.WriteString("user_profile: " + strings.TrimSpace(profileText) + "\n")
	}
	if len(chunks) > 0 {
		b.WriteString("retrieval_backend: " + strings.TrimSpace(backend) + "\n")
		for i := 0; i < len(chunks) && i < k; i++ {
			b.WriteString(fmt.Sprintf("- %s\n", strings.TrimSpace(chunks[i])))
		}
	}
	return strings.TrimSpace(b.String())
}

func (s *Session) maybeAutoConsolidate(ctx context.Context) {
	every := s.AutoConsolidateEvery
	if every <= 0 {
		every = 6
	}
	if s.turnN <= 0 || s.turnN%every != 0 {
		return
	}
	threshold := s.AutoConsolidateThreshold
	if threshold <= 0 {
		threshold = 0.45
	}
	promoted, err := s.Agentd.MemoryConsolidate(ctx, s.SessionID, threshold)
	if err != nil {
		log.Printf("heros-cli: auto consolidate: %v", err)
		return
	}
	log.Printf("heros-cli: auto consolidate session=%s threshold=%.2f promoted=%d", s.SessionID, threshold, promoted)
}

func emitHarnessProgressEvent(out io.Writer, ev harness.ProgressEvent) {
	if out == nil {
		return
	}
	if HarnessProgressWriterPrefersPlainText(out) {
		if s := FormatHarnessProgressLine(ev); strings.TrimSpace(s) != "" {
			_, _ = fmt.Fprintln(out, s)
		}
		return
	}
	emitHarnessEvent(out, HarnessEvent{
		Phase:             "harness_" + strings.TrimSpace(ev.Phase),
		Stage:             ev.Stage,
		Message:           ev.Detail,
		Index:             ev.Index,
		Total:             ev.Total,
		Attempt:           ev.Attempt,
		Role:              ev.Role,
		TodoID:            ev.TodoID,
		Score:             ev.Score,
		Threshold:         ev.Threshold,
		Status:            ev.Status,
		Tools:             ev.Tools,
		Skills:            ev.Skills,
		Memory:            ev.Memory,
		Section:           ev.Section,
		SectionLabel:      ev.SectionLabel,
		SectionStep:       ev.SectionStep,
		SectionStepsTotal: ev.SectionStepsTotal,
	})
}

// FormatUsageDisclosure prints a deterministic transparency line for each turn.
type ToolCallUsage struct {
	Name       string
	Status     string
	DurationMS int64
}

func FormatUsageDisclosure(toolUsed, skillUsed map[string]bool, memoryUsed bool, toolCalls []ToolCallUsage) string {
	toolList := slices.Sorted(maps.Keys(toolUsed))
	skillList := slices.Sorted(maps.Keys(skillUsed))
	toolsPart := "none"
	if len(toolList) > 0 {
		toolsPart = strings.Join(toolList, ",")
	}
	skillsPart := "none"
	if len(skillList) > 0 {
		skillsPart = strings.Join(skillList, ",")
	}
	memoryPart := "none"
	if memoryUsed {
		memoryPart = "used"
	}
	callsPart := "none"
	if len(toolCalls) > 0 {
		parts := make([]string, 0, len(toolCalls))
		for i, c := range toolCalls {
			name := strings.TrimSpace(c.Name)
			if name == "" {
				name = "unknown_tool"
			}
			status := strings.TrimSpace(c.Status)
			if status == "" {
				status = "ok"
			}
			parts = append(parts, fmt.Sprintf("%d:%s(%s,%dms)", i+1, name, status, c.DurationMS))
		}
		callsPart = strings.Join(parts, " -> ")
	}
	return fmt.Sprintf("[usage] tools=%s | calls=%s | skills=%s | memory=%s", toolsPart, callsPart, skillsPart, memoryPart)
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
		cmd := ArgString(args, "command")
		if strings.TrimSpace(cmd) == "" {
			return "", fmt.Errorf("missing command")
		}
		wd := strings.TrimSpace(s.WorkDir)
		if wd == "" {
			wd = "."
		}
		out, shellErr := RunLocalShell(ctx, wd, cmd)
		return LocalShellResult(out, shellErr), nil
	case "heros_list_files":
		return listFilesJSON(s.WorkDir, args)
	case "heros_read_file":
		return readFileJSON(s.WorkDir, args)
	case "heros_write_file":
		return writeFileJSON(s.WorkDir, args)
	case "heros_make_dir":
		return makeDirJSON(s.WorkDir, args)
	case "heros_delete_path":
		return deletePathJSON(s.WorkDir, args)
	case "heros_agent_shell":
		cmd := ArgString(args, "command")
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
		q := ArgString(args, "query")
		k := 8
		if v, ok := args["k"].(float64); ok && v > 0 {
			k = int(v)
		}
		chunks, backend, err := s.Agentd.MemoryRetrieve(ctx, s.SessionID, q, k)
		if err != nil {
			return "", err
		}
		out := map[string]any{"backend": backend, "chunks": chunks}
		b, _ := json.Marshal(out)
		return string(b), nil
	case "heros_memory_save":
		note := ArgString(args, "note")
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
		name := ArgString(args, "name")
		body, err := s.Agentd.SkillBody(ctx, name)
		if err != nil {
			return "", err
		}
		return body, nil
	case "heros_graph_neighbors":
		id := ArgString(args, "entity_id")
		g, err := s.Agentd.GraphNeighbors(ctx, id)
		if err != nil {
			return fmt.Sprintf(`{"error":%q}`, err.Error()), nil
		}
		b, _ := json.Marshal(g)
		return string(b), nil
	case "heros_run_harness":
		goal := ArgString(args, "goal")
		if strings.TrimSpace(goal) == "" {
			return "", fmt.Errorf("heros_run_harness requires goal")
		}
		res, err := s.Agentd.HarnessRunWithProgress(ctx, goal, func(ev harness.ProgressEvent) {
			emitHarnessProgressEvent(s.turnOut, ev)
		})
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(res)
		if err != nil {
			return "", err
		}
		return string(b), nil
	case "heros_list_pending_proposals":
		list, err := s.Agentd.ListPendingProposals(ctx)
		if err != nil {
			return "", err
		}
		return FormatPendingProposalsToolResult(list), nil
	case "heros_approve_proposal":
		pid := ArgString(args, "proposal_id")
		if strings.TrimSpace(pid) == "" {
			return "", fmt.Errorf("heros_approve_proposal requires proposal_id")
		}
		out, err := s.Agentd.ApproveProposalJSON(ctx, strings.TrimSpace(pid))
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(out)
		return string(b) + "\n(note: skill catalog updated on disk; you may heros_read_skill for the new slug or user can /refresh)", nil
	case "heros_reject_proposal":
		pid := ArgString(args, "proposal_id")
		if strings.TrimSpace(pid) == "" {
			return "", fmt.Errorf("heros_reject_proposal requires proposal_id")
		}
		out, err := s.Agentd.RejectProposalJSON(ctx, strings.TrimSpace(pid))
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	case "heros_submit_proposal":
		layer := ArgString(args, "layer")
		title := ArgString(args, "title")
		rationale := ArgString(args, "rationale")
		diff := ArgString(args, "diff")
		target := ArgString(args, "target_tenant")
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
	case "heros_extension_tool":
		tid := strings.TrimSpace(ArgString(args, "tool_id"))
		if tid == "" {
			return "", fmt.Errorf("heros_extension_tool requires tool_id")
		}
		inner := ArgJSONObject(args, "arguments")
		return s.runImportedCatalogTool(ctx, tid, inner)
	default:
		return "", fmt.Errorf("unknown tool %q", tc.Name)
	}
}

// RunREPL is a simple stdin loop; slash-commands are handled by caller or here.
func RunREPL(ctx context.Context, s *Session, in io.Reader, out io.Writer, errOut io.Writer) error {
	if err := s.PrimeSystem(ctx); err != nil {
		return fmt.Errorf("prime system: %w", err)
	}
	_, _ = fmt.Fprintf(out, "heros — session=%s  workdir=%s  stream=%v  (/exit to quit)\n"+
		"  Tip: /pending | approve N | approve all | /approve <id>\n",
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
				_ = config.SaveCLIWorkdir(s.WorkDir)
				_, _ = fmt.Fprintln(out, "\nbye")
				return nil
			}
			return err
		}
		line = strings.TrimSpace(line)
		if strings.TrimSpace(line) == "" {
			continue
		}
		if s.TryBulkApproveCommand(ctx, line, out, errOut) {
			continue
		}
		if s.TryApprovalNumberCommand(ctx, line, out, errOut) {
			continue
		}
		if c, q := s.DispatchReplSlash(ctx, line, out, errOut); c {
			if q {
				return nil
			}
			continue
		}
		if err := s.RunUserTurn(ctx, line, out); err != nil {
			_, _ = fmt.Fprintf(errOut, "error: %v\n", err)
		}
	}
}

func printHelp(out io.Writer) {
	fmt.Fprintln(out, `Commands:
  /exit, /quit   — leave (saves default workspace for next launch)
  /cd <dir>      — change workspace + save as default (%APPDATA%\heros\config.json → cli_workdir)
  /pwd           — print current workspace
  /harness <goal>— multi-actor run (leader → specialists → critic) inside heros; uses your LLM config
  /pending       — numbered list of proposals waiting for approval (ids on each line)
  /approve       — alone: reprints that list + usage; /approve <id> applies one proposal
  /reject        — alone: reprints list; /reject <id> rejects one
  /refresh       — re-fetch folder skill + tool catalog from the embedded daemon
  /help          — this text
Non-slash: **approve N** / **reject N** (number from /pending), or **approve all** / **approve all proposals** — handled locally without the LLM.

Anything else is sent to the model (OpenAI-compatible API).
Skills & memory live under heros’s data_dir; heros_memory_search includes this session’s auto-logged turns.
File edits are executable: the agent can call heros_read_file/heros_write_file/heros_delete_path instead of only giving instructions.
Env: HEROS_NO_TOOL_FORCE=1 disables forcing tool use on “tell me about this project”-style questions (for APIs that reject tool_choice=required).`)
}

// StdioREPL runs on os.Stdin/Stdout/Stderr.
func (s *Session) StdioREPL(ctx context.Context) error {
	if s.UseReadline {
		return RunReadlineREPL(ctx, s, os.Stdout, os.Stderr)
	}
	return RunREPL(ctx, s, os.Stdin, os.Stdout, os.Stderr)
}
