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
	"github.com/heros-foreal/agentd/internal/memoryfs"
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
	// OnDemandMemoryInjection injects memory only when intent indicates recall/long-horizon need.
	OnDemandMemoryInjection bool
	// AutoInjectTopK controls retrieval size for per-turn memory injection.
	AutoInjectTopK int
	// ContextIsolation constrains model-visible history to a recent rolling window.
	ContextIsolation bool
	// ContextIsolationWindow is max number of latest messages (excluding first system) visible to the model.
	ContextIsolationWindow int
	// ContextCompression compacts older chat into a short synthetic summary note.
	ContextCompression bool
	// ContextCompressionMaxMessages is the threshold above which compression runs.
	ContextCompressionMaxMessages int
	// ContextCompressionKeepRecent is number of latest messages retained uncompressed.
	ContextCompressionKeepRecent int
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
	instructions := fmt.Sprintf(`<session_prompt>You are the **Heros OS agent** (not a passive chatbot): you **drive** work with **tools**, **catalog skills**, and **memory** — all inside the **heros** process (embedded daemon + data_dir). There is no separate product the user must start for daily use.

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
**Smart tool defaults:** For multi-step tasks, start with **write_todos** and keep statuses updated. Prefer filesystem tools (**ls/read_file/write_file/edit_file/glob/grep**) for context and edits; use **execute** for commands/tests; use **task** (or **heros_run_harness**) when delegation/sub-agent flow is useful.
Before substantial tool work, print one short progress line describing what you are doing now.
When the user asks to implement/build/edit, execute autonomously without confirmation loops. Ask a question only when blocked by missing required input or a risky/destructive action.
Do not ask "shall I continue" / "would you like me to proceed" after partial progress; continue until the requested implementation and validation are complete.
When the user asks to run tests, do not assume repository root has the test manifest. First inspect workspace structure and detect test entrypoints in subdirectories (for example backend/frontend, go.mod, package.json, pyproject.toml, Cargo.toml). Run the relevant test commands in each detected project folder, then report a consolidated result.
If any command fails, do not stop at the first error. Inspect the workspace tree/files, infer likely cause (wrong folder, wrong command, missing flags, build/test prereq), try corrective commands, and continue until success or a true external blocker is proven.
For JavaScript test commands, prefer non-watch mode in automation (for example CI=true and --watch=false) so runs terminate.

**Long‑horizon tasks:** Break work into steps; after substantive progress call **heros_memory_save** or rely on session episodic logs; use **heros_memory_search** to recall earlier decisions in the same thread.
For multi-step implementation, debugging, or research work, explicitly run the **loop-engineering** pattern: frame the goal, ground the current state, plan the next step, execute, verify, then iterate or stop. Start with **write_todos** and keep them current.

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

Be concise in the terminal; one short plain-text status line before heavy tool use is enough.<session_prompt>%s`,
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
	s.compressConversationContext()
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
	testExecution := TestExecutionRequired(user)
	imageGeneration := ImageGenerationRequired(user)
	mustConverge := requireExecution || testExecution
	harnessUsed := false
	executionPromptInjected := false
	lastBatchHadFailures := false

	tools := OpenAITools(ToolOptions{AgentShell: s.AgentShell})
	var firstToolChoice any
	currentPhase := ""
	phaseChain := []string{}
	emitPhase := func(nextPhase, detail string, step int) {
		if !s.Stream {
			return
		}
		nextPhase = strings.TrimSpace(nextPhase)
		if nextPhase == "" {
			nextPhase = "thinking"
		}
		if nextPhase == currentPhase {
			return
		}
		currentPhase = nextPhase
		nextLine := FormatStreamProgressLine(1, step+1, 5, nextPhase, detail)
		phaseChain = append(phaseChain, nextLine)
		_, _ = fmt.Fprintf(out, "\r%s", strings.Join(phaseChain, " -> "))
	}
	flushPhaseChain := func() {
		if !s.Stream || len(phaseChain) == 0 {
			return
		}
		_, _ = fmt.Fprintln(out, strings.Join(phaseChain, " -> "))
	}
	if ClarificationRequired(user) {
		s.Messages = append(s.Messages, map[string]any{
			"role":    "system",
			"content": "User intent is ambiguous. Ask exactly one concise clarification question before using tools or making assumptions.",
		})
		firstToolChoice = "none"
	}
	if firstToolChoice == nil {
		switch {
		case imageGeneration:
			// Force tool mode and steer model to native image generation path.
			firstToolChoice = "required"
		case WorkspaceGroundingRequired(user, s.WorkDir):
			firstToolChoice = "required"
		case testExecution:
			// Test runs are multi-step by nature: discover layout, run, fix, rerun until pass.
			firstToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": "heros_run_harness"}}
		case LongHorizonHarnessRequired(user):
			// Prefer harness for complex implementation/integration asks to show todo + sub-agent flow.
			firstToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": "heros_run_harness"}}
		case FileActionGroundingRequired(user):
			firstToolChoice = "required"
		case MemoryGroundingRequired(user):
			// Prefer memory search over guessing when user asks what is stored.
			firstToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": "heros_memory_search"}}
		}
	}
	for step := 0; step < 64; step++ {
		if s.Stream && step == 0 {
			_, label, detail, _, _ := StreamProgressState(step, false, "", firstToolChoice, "")
			emitPhase(label, detail, step)
		}
		if step == 0 && imageGeneration {
			s.Messages = append(s.Messages, map[string]any{
				"role":    "system",
				"content": "Image-generation request detected. Use native image tooling only: call heros_extension_tool with tool_id=image-generation-tool. Do not call heros_shell, heros_agent_shell, or execute for image generation.",
			})
		}
		modelStart := time.Now().UTC()
		_, _ = fmt.Fprint(out, "\n")
		emitHarnessStart(out, "assistant", "", "", "", modelStart)
		var cc *ChatOptions
		if step == 0 && firstToolChoice != nil {
			cc = &ChatOptions{ToolChoice: firstToolChoice}
		} else if mustConverge && harnessUsed {
			// After planning, force concrete execution tools instead of final prose-only replies.
			cc = &ChatOptions{ToolChoice: "required"}
		}
		var content string
		var calls []ToolCall
		var err error
		modelMessages := s.modelMessagesForStep()
		if s.Stream {
			content, calls, err = ChatCompletionStream(ctx, s.OpenAIBase, s.OpenAIKey, s.Model, modelMessages, tools, cc, func(d string) error {
				_, werr := fmt.Fprint(out, d)
				return werr
			})
		} else {
			content, calls, err = ChatCompletion(ctx, s.OpenAIBase, s.OpenAIKey, s.Model, modelMessages, tools, cc)
		}
		if err != nil {
			modelEnd := time.Now().UTC()
			emitHarnessEnd(out, "assistant", "", "", "error", "", modelStart, modelEnd)
			return err
		}
		modelEnd := time.Now().UTC()
		emitHarnessEnd(out, "assistant", "", "", "ok", "", modelStart, modelEnd)
		if s.Stream && strings.TrimSpace(content) != "" {
			_, label, detail, _, _ := StreamProgressState(step, false, "", firstToolChoice, content)
			emitPhase(label, detail, step)
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
			if mustConverge && lastBatchHadFailures {
				s.Messages = append(s.Messages, map[string]any{
					"role":    "system",
					"content": "A previous execution step failed. Continue automatically: inspect files, fix root causes, rerun relevant tests, and only stop when tests pass or a hard external blocker is proven.",
				})
				continue
			}
			if !s.Stream && content != "" {
				_, _ = fmt.Fprintln(out, content)
			}
			if s.Stream && content != "" {
				_, _ = fmt.Fprint(out, "\n")
			}
			flushPhaseChain()
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
				s.updateSessionAgentMemory(user, content)
				s.saveLoopCheckpoint(ctx, user, content, toolCalls, false, false)
				s.turnN++
				s.maybeAutoConsolidate(ctx)
			}
			return nil
		}

		batchHadFailures := false
		for _, c := range calls {
			toolStart := time.Now().UTC()
			_, _ = fmt.Fprint(out, "\n")
			args := parseToolArguments(c.Arguments)
			emitHarnessStart(out, "tool", c.ID, c.Name, toolStartMessage(c.Name, args), toolStart)
			toolUsed[c.Name] = true
			if strings.HasPrefix(c.Name, "heros_memory_") {
				memoryUsed = true
			}
			if c.Name == "heros_read_skill" {
				if n := strings.TrimSpace(ArgString(args, "name")); n != "" {
					skillUsed[n] = true
				}
			}
			result, err := s.DispatchTool(ctx, c)
			toolEnd := time.Now().UTC()
			status := "ok"
			callFailed := err != nil || toolResultHasFailure(c.Name, result)
			endMsg := toolEndMessage(c.Name, result)
			if s.Stream {
				phase := StreamPhaseForTool(c.Name)
				detail := strings.TrimSpace(c.Name)
				if phase != "" {
					emitPhase(phase, detail, step)
				}
			}
			if callFailed {
				if err != nil {
					result = "error: " + err.Error()
				}
				status = "error"
				emitHarnessEnd(out, "tool", c.ID, c.Name, "error", endMsg, toolStart, toolEnd)
				batchHadFailures = true
			} else {
				emitHarnessEnd(out, "tool", c.ID, c.Name, "ok", endMsg, toolStart, toolEnd)
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
				"content":      s.contextSafeToolResult(c.Name, result),
			})
			if mustConverge && c.Name == "heros_run_harness" && !executionPromptInjected {
				s.Messages = append(s.Messages, map[string]any{
					"role":    "system",
					"content": "Execution required now. Do the work in the repository: create/modify files, run tests/build commands, fix failures, and only finish when validation passes or you are blocked by a hard external dependency. Do not ask the user for confirmation mid-task.",
				})
				executionPromptInjected = true
			}
		}
		lastBatchHadFailures = batchHadFailures
		s.compressConversationContext()
	}
	flushPhaseChain()
	return fmt.Errorf("tool loop exceeded step limit")
}

func (s *Session) buildAutoMemoryInjection(ctx context.Context, user string) string {
	if !s.AutoInjectMemory {
		return ""
	}
	if s.OnDemandMemoryInjection && !s.shouldInjectMemory(user) {
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
	sessionAgentMemory, _ := memoryfs.ReadSessionAgentMemory(s.DataDir, "_global", s.SessionID)
	if len(chunks) == 0 && strings.TrimSpace(profileText) == "" {
		if strings.TrimSpace(sessionAgentMemory) == "" {
			return ""
		}
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
	if strings.TrimSpace(sessionAgentMemory) != "" {
		b.WriteString("session_agent_memory:\n")
		b.WriteString(summarizeForHarness(sessionAgentMemory, 1200))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func (s *Session) updateSessionAgentMemory(user, assistant string) {
	if strings.TrimSpace(s.DataDir) == "" || strings.TrimSpace(s.SessionID) == "" {
		return
	}
	prior, _ := memoryfs.ReadSessionAgentMemory(s.DataDir, "_global", s.SessionID)
	var b strings.Builder
	if strings.TrimSpace(prior) != "" {
		b.WriteString(prior)
		b.WriteString("\n")
	}
	b.WriteString("- user: ")
	b.WriteString(summarizeForHarness(user, 240))
	b.WriteString("\n- assistant: ")
	b.WriteString(summarizeForHarness(assistant, 360))
	b.WriteString("\n")
	text := strings.TrimSpace(b.String())
	if len(text) > 5000 {
		text = text[len(text)-5000:]
	}
	if err := memoryfs.UpsertSessionAgentMemory(s.DataDir, "_global", s.SessionID, text); err != nil {
		log.Printf("heros-cli: session agent memory write: %v", err)
	}
}

func (s *Session) saveLoopCheckpoint(ctx context.Context, user, assistant string, toolCalls []ToolCallUsage, hadFailures bool, blocked bool) {
	if strings.TrimSpace(s.SessionID) == "" {
		return
	}
	var toolNames []string
	for _, tc := range toolCalls {
		if name := strings.TrimSpace(tc.Name); name != "" {
			toolNames = append(toolNames, name)
		}
	}
	status := "progress"
	switch {
	case blocked:
		status = "blocked"
	case hadFailures:
		status = "retry"
	case len(toolNames) > 0:
		status = "verified"
	}
	note := fmt.Sprintf(
		"[loop-checkpoint]\nstatus=%s\nuser=%s\nassistant=%s\ntools=%s",
		status,
		summarizeForHarness(user, 220),
		summarizeForHarness(assistant, 320),
		strings.Join(toolNames, ","),
	)
	if err := s.Agentd.MemoryEpisodic(ctx, s.SessionID, "note", note, 0.45); err != nil {
		log.Printf("heros-cli: loop checkpoint write: %v", err)
	}
}

func (s *Session) shouldInjectMemory(user string) bool {
	u := strings.TrimSpace(strings.ToLower(user))
	if u == "" {
		return false
	}
	if MemoryGroundingRequired(user) || LongHorizonHarnessRequired(user) || TestExecutionRequired(user) {
		return true
	}
	for _, k := range []string{"remember", "recall", "earlier", "previous", "history", "context", "continue from"} {
		if strings.Contains(u, k) {
			return true
		}
	}
	return false
}

func (s *Session) modelMessagesForStep() []map[string]any {
	msgs := s.Messages
	if !s.ContextIsolation {
		return msgs
	}
	if len(msgs) <= 1 {
		return msgs
	}
	win := s.ContextIsolationWindow
	if win <= 0 {
		win = 28
	}
	if len(msgs)-1 <= win {
		return msgs
	}
	start := len(msgs) - win
	out := make([]map[string]any, 0, win+1)
	out = append(out, msgs[0])
	out = append(out, msgs[start:]...)
	return out
}

func (s *Session) contextSafeToolResult(toolName, result string) string {
	if !s.ContextIsolation {
		return result
	}
	max := 1200
	switch strings.TrimSpace(toolName) {
	case "heros_shell", "heros_agent_shell":
		return summarizeForHarness(result, max)
	default:
		return summarizeForHarness(result, 800)
	}
}

func (s *Session) compressConversationContext() {
	if !s.ContextCompression || len(s.Messages) <= 2 {
		return
	}
	maxMsgs := s.ContextCompressionMaxMessages
	if maxMsgs <= 0 {
		maxMsgs = 90
	}
	keepRecent := s.ContextCompressionKeepRecent
	if keepRecent <= 0 {
		keepRecent = 28
	}
	if len(s.Messages) <= maxMsgs {
		return
	}
	base := s.Messages[0]
	rest := make([]map[string]any, 0, len(s.Messages)-1)
	for _, m := range s.Messages[1:] {
		if strings.TrimSpace(ArgString(m, "role")) == "system" && strings.HasPrefix(strings.TrimSpace(ArgString(m, "content")), "[compressed-context]") {
			continue
		}
		rest = append(rest, m)
	}
	if len(rest) <= keepRecent {
		s.Messages = append([]map[string]any{base}, rest...)
		return
	}
	cut := len(rest) - keepRecent
	removed := cut
	head := rest[:cut]
	tail := rest[cut:]
	summary := map[string]any{
		"role":    "system",
		"content": buildCompressedContextNote(head),
	}
	next := make([]map[string]any, 0, 2+len(tail))
	next = append(next, base, summary)
	next = append(next, tail...)
	s.Messages = next
	s.emitContextCompressionEvent(removed, len(tail))
}

func (s *Session) emitContextCompressionEvent(removed, keptRecent int) {
	if removed <= 0 {
		return
	}
	msg := fmt.Sprintf("Compressed %d older message(s); kept %d recent message(s).", removed, keptRecent)
	now := time.Now().UTC()
	emitHarnessStart(s.turnOut, "context_compression", "", "", msg, now)
	emitHarnessEnd(s.turnOut, "context_compression", "", "", "ok", msg, now, now)
}

func buildCompressedContextNote(msgs []map[string]any) string {
	if len(msgs) == 0 {
		return "[compressed-context] none"
	}
	maxLines := 24
	if len(msgs) < maxLines {
		maxLines = len(msgs)
	}
	var b strings.Builder
	b.WriteString("[compressed-context]\n")
	b.WriteString("Older conversation compressed for context efficiency:\n")
	for i := 0; i < maxLines; i++ {
		m := msgs[i]
		role := strings.TrimSpace(ArgString(m, "role"))
		if role == "" {
			role = "unknown"
		}
		c := strings.TrimSpace(ArgString(m, "content"))
		if c == "" {
			c = "(no text content)"
		}
		b.WriteString(fmt.Sprintf("- %s: %s\n", role, summarizeForHarness(c, 180)))
	}
	if len(msgs) > maxLines {
		b.WriteString(fmt.Sprintf("- ... %d additional message(s) omitted\n", len(msgs)-maxLines))
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

func parseToolArguments(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
	}
	return args
}

func summarizeForHarness(msg string, max int) string {
	s := strings.TrimSpace(msg)
	if s == "" {
		return ""
	}
	if max <= 0 {
		max = 320
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func toolStartMessage(name string, args map[string]any) string {
	switch strings.TrimSpace(name) {
	case "heros_shell", "heros_agent_shell":
		return summarizeForHarness(ArgString(args, "command"), 220)
	case "heros_run_harness":
		return summarizeForHarness(ArgString(args, "goal"), 220)
	default:
		return ""
	}
}

func toolEndMessage(name, result string) string {
	switch strings.TrimSpace(name) {
	case "heros_shell", "heros_agent_shell", "execute":
		return summarizeForHarness(result, 700)
	default:
		return ""
	}
}

func saveLargeToolOutput(workDir, toolName, output string) (string, error) {
	if strings.TrimSpace(output) == "" {
		return output, nil
	}
	limit := 12000
	if len(output) <= limit {
		return output, nil
	}
	ts := time.Now().UTC().Format("20060102-150405")
	base := strings.TrimSpace(workDir)
	if base == "" {
		base = "."
	}
	dir := filepath.Join(base, ".heros", "outputs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return output, err
	}
	name := strings.ReplaceAll(strings.TrimSpace(toolName), " ", "_")
	if name == "" {
		name = "output"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.txt", name, ts))
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		return output, err
	}
	return fmt.Sprintf("Output was large (%d chars) and saved to: %s\n\nPreview:\n%s", len(output), filepath.Clean(path), summarizeForHarness(output, 1500)), nil
}

func adaptShellCommandForTests(cmd string) string {
	c := strings.TrimSpace(cmd)
	lc := strings.ToLower(c)
	if strings.HasPrefix(lc, "npm test") && !strings.Contains(lc, "watch") && !strings.Contains(lc, "ci=") && !strings.Contains(lc, "$env:ci") && !strings.Contains(lc, "set ci=") {
		// Avoid hanging watch mode in common frontend test runners.
		return "set CI=true&& " + c + " -- --watch=false"
	}
	return c
}

func extractNpmPrefix(cmd string) string {
	fields := strings.Fields(strings.TrimSpace(cmd))
	for i := 0; i < len(fields)-1; i++ {
		if strings.TrimSpace(strings.ToLower(fields[i])) == "--prefix" {
			return strings.Trim(strings.TrimSpace(fields[i+1]), "\"")
		}
	}
	return ""
}

func retryGoTestInSubdirIfNeeded(ctx context.Context, workDir, originalCmd, out string, shellErr error) (string, error, string) {
	if shellErr == nil {
		return out, nil, ""
	}
	cmd := strings.TrimSpace(originalCmd)
	lc := strings.ToLower(cmd)
	lout := strings.ToLower(out)
	if !strings.HasPrefix(lc, "go test ") || !strings.Contains(lout, "go.mod file not found") {
		return out, shellErr, ""
	}
	fields := strings.Fields(cmd)
	if len(fields) < 3 {
		return out, shellErr, ""
	}
	target := strings.TrimSpace(fields[2])
	if target == "" || strings.HasPrefix(target, "-") {
		return out, shellErr, ""
	}
	target = strings.TrimPrefix(target, "./")
	target = strings.Trim(target, "\"")
	if target == "" {
		return out, shellErr, ""
	}
	abs := filepath.Clean(filepath.Join(workDir, filepath.FromSlash(target)))
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return out, shellErr, ""
	}
	retryCmd := "go test ./..."
	retryOut, retryErr := RunLocalShell(ctx, abs, retryCmd)
	note := fmt.Sprintf("auto-retry: `%s` in `%s`", retryCmd, target)
	if retryErr != nil {
		return out + "\n" + note + "\n" + retryOut, retryErr, note
	}
	return out + "\n" + note + "\n" + retryOut, nil, note
}

func retryNpmTestInSubdirIfNeeded(ctx context.Context, workDir, originalCmd, out string, shellErr error) (string, error, string) {
	if shellErr == nil {
		return out, nil, ""
	}
	cmd := strings.TrimSpace(originalCmd)
	lc := strings.ToLower(cmd)
	if !strings.Contains(lc, "npm test") {
		return out, shellErr, ""
	}
	lout := strings.ToLower(out)
	prefix := extractNpmPrefix(cmd)
	if prefix == "" {
		// common layout fallback
		if st, err := os.Stat(filepath.Join(workDir, "frontend", "package.json")); err == nil && !st.IsDir() {
			prefix = "frontend"
		}
	}
	if prefix == "" {
		return out, shellErr, ""
	}
	abs := filepath.Clean(filepath.Join(workDir, filepath.FromSlash(prefix)))
	// If npm prefix points to a Go module, recover by running Go tests there.
	if st, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil && !st.IsDir() && strings.Contains(lout, "package.json") {
		retryCmd := "go test ./..."
		retryOut, retryErr := RunLocalShell(ctx, abs, retryCmd)
		note := fmt.Sprintf("auto-retry: `%s` in `%s`", retryCmd, prefix)
		if retryErr != nil {
			return out + "\n" + note + "\n" + retryOut, retryErr, note
		}
		return out + "\n" + note + "\n" + retryOut, nil, note
	}
	if st, err := os.Stat(filepath.Join(abs, "package.json")); err != nil || st.IsDir() {
		return out, shellErr, ""
	}
	retryCmd := "set CI=true&& npm test -- --watch=false"
	retryOut, retryErr := RunLocalShell(ctx, abs, retryCmd)
	note := fmt.Sprintf("auto-retry: `%s` in `%s`", retryCmd, prefix)
	if retryErr != nil {
		return out + "\n" + note + "\n" + retryOut, retryErr, note
	}
	return out + "\n" + note + "\n" + retryOut, nil, note
}

func toolResultHasFailure(toolName, result string) bool {
	name := strings.TrimSpace(toolName)
	if name != "heros_shell" && name != "heros_agent_shell" && name != "execute" {
		return false
	}
	var r map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(result)), &r); err != nil {
		return false
	}
	errVal := strings.TrimSpace(ArgString(r, "error"))
	return errVal != ""
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
	case "heros_shell", "execute":
		cmd := ArgString(args, "command")
		if strings.TrimSpace(cmd) == "" {
			return "", fmt.Errorf("missing command")
		}
		wd := strings.TrimSpace(s.WorkDir)
		if wd == "" {
			wd = "."
		}
		cmd = adaptShellCommandForTests(cmd)
		out, shellErr := RunLocalShell(ctx, wd, cmd)
		out, shellErr, _ = retryGoTestInSubdirIfNeeded(ctx, wd, cmd, out, shellErr)
		out, shellErr, _ = retryNpmTestInSubdirIfNeeded(ctx, wd, cmd, out, shellErr)
		if shrunk, err := saveLargeToolOutput(wd, tc.Name, out); err == nil {
			out = shrunk
		}
		return LocalShellResult(out, shellErr), nil
	case "heros_list_files", "ls":
		return listFilesJSON(s.WorkDir, args)
	case "heros_read_file", "read_file":
		return readFileJSON(s.WorkDir, args)
	case "heros_write_file", "write_file":
		return writeFileJSON(s.WorkDir, args)
	case "edit_file":
		return editFileJSON(s.WorkDir, args)
	case "glob":
		return globJSON(s.WorkDir, args)
	case "grep":
		return grepJSON(s.WorkDir, args)
	case "write_todos":
		return writeTodosJSON(s.WorkDir, args)
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
	case "heros_memory_link":
		source := ArgString(args, "source")
		target := ArgString(args, "target")
		rel := ArgString(args, "rel")
		provenance := ArgString(args, "provenance")
		sessionID := ArgString(args, "session_id")
		if strings.TrimSpace(sessionID) == "" {
			sessionID = s.SessionID
		}
		confidence := 0.8
		if v, ok := args["confidence"].(float64); ok && v >= 0 {
			confidence = v
		}
		if strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" || strings.TrimSpace(rel) == "" {
			return "", fmt.Errorf("heros_memory_link requires source, target, rel")
		}
		id, err := s.Agentd.MemoryLink(ctx, sessionID, source, target, rel, provenance, confidence)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"status": "linked", "id": id})
		return string(b), nil
	case "heros_memory_links_list":
		sessionID := ArgString(args, "session_id")
		if strings.TrimSpace(sessionID) == "" {
			sessionID = s.SessionID
		}
		links, err := s.Agentd.MemoryLinks(ctx, sessionID)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"session_id": sessionID, "links": links})
		return string(b), nil
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
	case "heros_run_harness", "task":
		goal := ArgString(args, "goal")
		if strings.TrimSpace(goal) == "" {
			goal = ArgString(args, "description")
		}
		if strings.TrimSpace(goal) == "" {
			return "", fmt.Errorf("%s requires goal/description", tc.Name)
		}
		if w, ok := args["context_window"].(float64); ok && w > 0 {
			goal = fmt.Sprintf("%s\n\n[delegation context window hint=%d messages]", goal, int(w))
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
  /skills        — list indexed catalog skills from agentd
  /tools         — list registered catalog tools from agentd
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
