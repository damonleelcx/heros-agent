package cliagent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/heros-foreal/agentd/internal/config"
)

// DispatchReplSlash handles /commands. If consumed is false, line should go to the model.
// quit means exit the REPL (after optional persistence).
func (s *Session) DispatchReplSlash(ctx context.Context, line string, out, errOut io.Writer) (consumed, quit bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") {
		return false, false
	}
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return true, false
	}
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/exit", "/quit":
		_ = config.SaveCLIWorkdir(s.WorkDir)
		_, _ = fmt.Fprintln(out, "bye")
		return true, true
	case "/help":
		printHelp(out)
		return true, false
	case "/refresh":
		if err := s.RefreshContext(ctx); err != nil {
			_, _ = fmt.Fprintf(errOut, "refresh: %v\n", err)
		} else {
			_, _ = fmt.Fprintln(out, "(catalog block appended to context)")
		}
		return true, false
	case "/pwd":
		_, _ = fmt.Fprintln(out, s.WorkDir)
		return true, false
	case "/cd":
		if len(parts) < 2 {
			_, _ = fmt.Fprintln(errOut, "usage: /cd <directory>")
			return true, false
		}
		target := strings.TrimSpace(strings.Join(parts[1:], " "))
		abs, err := filepath.Abs(target)
		if err != nil {
			_, _ = fmt.Fprintf(errOut, "/cd: %v\n", err)
			return true, false
		}
		st, err := os.Stat(abs)
		if err != nil || !st.IsDir() {
			_, _ = fmt.Fprintf(errOut, "/cd: not a directory: %s\n", abs)
			return true, false
		}
		s.WorkDir = abs
		if err := config.SaveCLIWorkdir(abs); err != nil {
			_, _ = fmt.Fprintf(errOut, "/cd: could not save default workdir: %v\n", err)
		}
		_, _ = fmt.Fprintf(out, "workdir=%s (saved for next launch)\n", abs)
		return true, false
	case "/pending":
		list, err := s.Agentd.ListPendingProposals(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(errOut, "pending: %v\n", err)
			return true, false
		}
		_, _ = fmt.Fprint(out, FormatPendingProposalsForUser(list))
		return true, false
	case "/approve":
		if len(parts) < 2 {
			list, err := s.Agentd.ListPendingProposals(ctx)
			if err != nil {
				_, _ = fmt.Fprintf(errOut, "pending: %v\n", err)
				return true, false
			}
			_, _ = fmt.Fprint(out, FormatPendingProposalsForUser(list))
			_, _ = fmt.Fprintln(errOut, "usage: /approve <proposal_id>  (copy id from the list above)")
			return true, false
		}
		id := parts[1]
		if err := s.Agentd.ApproveProposal(ctx, id); err != nil {
			_, _ = fmt.Fprintf(errOut, "approve: %v\n", err)
			return true, false
		}
		_, _ = fmt.Fprintf(out, "approved %s (skills/tools refreshed on disk; you may /refresh)\n", id)
		return true, false
	case "/reject":
		if len(parts) < 2 {
			list, err := s.Agentd.ListPendingProposals(ctx)
			if err != nil {
				_, _ = fmt.Fprintf(errOut, "pending: %v\n", err)
				return true, false
			}
			_, _ = fmt.Fprint(out, FormatPendingProposalsForUser(list))
			_, _ = fmt.Fprintln(errOut, "usage: /reject <proposal_id>")
			return true, false
		}
		id := parts[1]
		if err := s.Agentd.RejectProposal(ctx, id); err != nil {
			_, _ = fmt.Fprintf(errOut, "reject: %v\n", err)
			return true, false
		}
		_, _ = fmt.Fprintf(out, "rejected %s\n", id)
		return true, false
	case "/harness":
		rest := strings.TrimSpace(line[len(parts[0]):])
		if rest == "" {
			_, _ = fmt.Fprintln(errOut, "usage: /harness <goal>  — 规划→子代理→反馈/评分→（必要时）重试→循环直到完成（与 heros 相同流程；使用当前 LLM 配置）")
			return true, false
		}
		_, _ = fmt.Fprintln(out, "（harness 运行中：① 规划/目标/待办 ② 子代理执行 ③ 反馈与评分 ④ 必要时重试与重做 ⑤ 循环直到完成；可能需要一分钟…）")
		res, err := s.Agentd.HarnessRun(ctx, rest)
		if err != nil {
			_, _ = fmt.Fprintf(errOut, "harness: %v\n", err)
			return true, false
		}
		_, _ = fmt.Fprintf(out, "\n--- plan (%d steps) ---\n", len(res.Plan))
		for i, step := range res.Plan {
			_, _ = fmt.Fprintf(out, "%d. %s\n", i+1, step)
		}
		_, _ = fmt.Fprintf(out, "\n--- todo progress (%d/%d done) ---\n", res.CompletedTodos, res.TotalTodos)
		for _, td := range res.Todos {
			_, _ = fmt.Fprintf(out, "- %s [%s] role=%s attempt=%d :: %s\n", td.ID, td.Status, td.Assignee, td.Attempt, td.Title)
			if strings.TrimSpace(td.Feedback) != "" {
				_, _ = fmt.Fprintf(out, "  feedback: %s\n", td.Feedback)
			}
		}
		_, _ = fmt.Fprintln(out, "\n--- merged draft (before final critic line) ---")
		var merged strings.Builder
		for k, v := range res.SubResults {
			_, _ = fmt.Fprintf(&merged, "### %s\n%s\n\n", k, v)
		}
		_, _ = fmt.Fprintln(out, strings.TrimSpace(merged.String()))
		_, _ = fmt.Fprintln(out, "\n--- agent visibility (memory/skills/tools) ---")
		for _, v := range res.AgentVisibility {
			_, _ = fmt.Fprintf(out, "- %s | tools=%s | skills=%s | memory=%s\n",
				v.Role, strings.Join(v.ToolsUsed, ","), strings.Join(v.SkillsUsed, ","), strings.Join(v.MemoryUsed, ","))
		}
		_, _ = fmt.Fprintln(out, "\n--- verification (test + preview) ---")
		_, _ = fmt.Fprintf(out, "passed=%v summary=%s\n", res.Verification.Passed, res.Verification.Summary)
		for _, c := range res.Verification.Checks {
			_, _ = fmt.Fprintf(out, "- %s\n", c)
		}
		_, _ = fmt.Fprintln(out, "\n--- final ---")
		_, _ = fmt.Fprintln(out, res.Final)
		return true, false
	default:
		_, _ = fmt.Fprintf(errOut, "unknown command %q (try /help)\n", cmd)
		return true, false
	}
}
