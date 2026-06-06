package cliagent

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/heros-foreal/agentd/internal/harness"
	"golang.org/x/term"
)

// HarnessEventToProgressEvent maps a streamed harness_event JSON row to harness.ProgressEvent for display.
func HarnessEventToProgressEvent(ev HarnessEvent) harness.ProgressEvent {
	return harness.ProgressEvent{
		Phase:             strings.TrimPrefix(strings.TrimSpace(ev.Phase), "harness_"),
		Stage:             ev.Stage,
		Detail:            ev.Message,
		Index:             ev.Index,
		Total:             ev.Total,
		Attempt:           ev.Attempt,
		Role:              ev.Role,
		TodoID:            ev.TodoID,
		Status:            ev.Status,
		Tools:             ev.Tools,
		Skills:            ev.Skills,
		Memory:            ev.Memory,
		Score:             ev.Score,
		Threshold:         ev.Threshold,
		Section:           ev.Section,
		SectionLabel:      ev.SectionLabel,
		SectionStep:       ev.SectionStep,
		SectionStepsTotal: ev.SectionStepsTotal,
	}
}

func harnessProgressPrefix(section int, label string, sectionStep, sectionStepsTotal, index, total int) string {
	if section <= 0 || section > 5 {
		return "[harness] "
	}
	l := strings.TrimSpace(label)
	if l == "" {
		l = "?"
	}
	if sectionStep > 0 && sectionStepsTotal > 0 {
		return fmt.Sprintf("[%d/5·%s step %d/%d] ", section, l, sectionStep, sectionStepsTotal)
	}
	if index > 0 && total > 0 {
		return fmt.Sprintf("[%d/5·%s %d/%d] ", section, l, index, total)
	}
	return fmt.Sprintf("[%d/5·%s] ", section, l)
}

func harnessProgressPrefixFromEvent(ev harness.ProgressEvent) string {
	return harnessProgressPrefix(ev.Section, ev.SectionLabel, ev.SectionStep, ev.SectionStepsTotal, ev.Index, ev.Total)
}

// FormatHarnessProgressLine renders one human-readable harness progress line (same convention as heros-desktop).
func FormatHarnessProgressLine(ev harness.ProgressEvent) string {
	p := harnessProgressPrefixFromEvent(ev)
	phase := strings.TrimSpace(ev.Phase)
	switch phase {
	case "leader":
		if ev.Stage == "start" {
			return p + "main-agent planning: decompose goal → todo list"
		}
		if ev.Stage == "end" {
			if ev.Total > 0 {
				return p + fmt.Sprintf("todo list ready (%d steps)", ev.Total)
			}
			return p + "leader ready"
		}
	case "specialist":
		role := strings.TrimSpace(ev.Role)
		if role != "" {
			role = " " + role
		}
		todo := strings.TrimSpace(ev.TodoID)
		if todo != "" {
			todo = " " + todo
		}
		usage := ""
		if len(ev.Tools) > 0 || len(ev.Skills) > 0 || len(ev.Memory) > 0 {
			usage = fmt.Sprintf(" | tools=%s skills=%s memory=%s",
				strings.Join(ev.Tools, ","),
				strings.Join(ev.Skills, ","),
				strings.Join(ev.Memory, ","),
			)
		}
		if ev.Stage == "end" {
			return p + fmt.Sprintf("sub-agent%s%s work complete%s", role, todo, usage)
		}
		return p + fmt.Sprintf("sub-agent%s%s working%s", role, todo, usage)
	case "feedback":
		role := strings.TrimSpace(ev.Role)
		if role != "" {
			role = " " + role
		}
		todo := strings.TrimSpace(ev.TodoID)
		if todo != "" {
			todo = " " + todo
		}
		if ev.Stage == "start" {
			return p + fmt.Sprintf("feedback + critic starting%s%s", role, todo)
		}
		if ev.Stage == "end" {
			msg := strings.TrimSpace(ev.Detail)
			if len(msg) > 160 {
				msg = msg[:160] + "…"
			}
			if msg != "" {
				return p + fmt.Sprintf("feedback + critic score=%.2f (thresh %.2f) status=%s — %s", ev.Score, ev.Threshold, strings.TrimSpace(ev.Status), msg)
			}
			return p + fmt.Sprintf("feedback + critic score=%.2f (thresh %.2f) status=%s", ev.Score, ev.Threshold, strings.TrimSpace(ev.Status))
		}
	case "todo":
		if ev.Stage == "created" && strings.TrimSpace(ev.TodoID) != "" {
			msg := strings.TrimSpace(ev.Detail)
			if msg != "" {
				return p + fmt.Sprintf("todo created %s: %s", ev.TodoID, msg)
			}
			return p + fmt.Sprintf("todo created %s", ev.TodoID)
		}
		if ev.Stage == "iteration_start" && ev.Attempt > 0 {
			return p + fmt.Sprintf("repeat cycle: dispatch open todos (pass %d)", ev.Attempt)
		}
		if ev.Stage == "iteration_end" && ev.Attempt > 0 {
			return p + fmt.Sprintf("repeat cycle: sub-agents finished pass %d", ev.Attempt)
		}
	case "verify":
		if ev.Stage == "start" && ev.Attempt > 0 {
			return p + "verification: test + preview starting"
		}
		if ev.Attempt > 0 {
			return p + fmt.Sprintf("verification: attempt %d %s", ev.Attempt, strings.TrimSpace(ev.Status))
		}
		return p + fmt.Sprintf("verification: %s", ev.Status)
	case "critic":
		if ev.Stage == "retry" {
			if ev.Attempt > 0 {
				return p + fmt.Sprintf("retry / redo scheduled (after attempt %d)", ev.Attempt)
			}
			return p + "retry / redo scheduled"
		}
		if ev.Stage == "end" && ev.Attempt > 0 {
			return p + fmt.Sprintf("global critic: merged draft score %.2f (thresh %.2f)", ev.Score, ev.Threshold)
		}
		if ev.Stage == "start" && ev.Attempt > 0 {
			return p + fmt.Sprintf("global critic: scoring merged draft (attempt %d)", ev.Attempt)
		}
	case "refine":
		if ev.Attempt > 0 {
			return p + fmt.Sprintf("redo: refine / new todos (%s)", ev.Stage)
		}
		return p + fmt.Sprintf("redo: refine (%s)", ev.Stage)
	case "harness":
		if ev.Stage == "start" {
			g := strings.TrimSpace(ev.Detail)
			if len(g) > 160 {
				g = g[:160] + "…"
			}
			if g != "" {
				return p + "goal: " + g
			}
			return p + "run started"
		}
		if ev.Stage == "end" {
			return p + fmt.Sprintf("run complete (score %.2f)", ev.Score)
		}
	case "summary":
		if ev.Stage == "iteration_end" {
			msg := strings.TrimSpace(ev.Detail)
			if msg == "" {
				msg = "iteration summary ready"
			}
			return p + msg
		}
	}
	if ev.Section > 0 {
		return harnessProgressPrefixFromEvent(ev) + fmt.Sprintf("%s %s", phase, ev.Stage)
	}
	return fmt.Sprintf("[harness] %s %s", phase, ev.Stage)
}

// FormatStreamProgressLine renders a compact step/status line for non-harness streaming turns.
func FormatStreamProgressLine(section, stepInSection, stepTotal int, label, detail string) string {
	p := harnessProgressPrefix(section, label, stepInSection, stepTotal, 0, 0)
	msg := strings.TrimSpace(detail)
	if msg == "" {
		return strings.TrimSpace(p)
	}
	return strings.TrimSpace(p + msg)
}

// StreamProgressState maps turn state and tool selection to a stable user-facing label.
func StreamProgressState(step int, awaitingTool bool, toolName string, toolChoice any, content string) (section int, label string, detail string, stepInSection int, stepTotal int) {
	section = 1
	stepInSection = step + 1
	stepTotal = 5
	if awaitingTool {
		switch {
		case strings.Contains(strings.ToLower(toolName), "read") || strings.Contains(strings.ToLower(toolName), "search") || strings.Contains(strings.ToLower(toolName), "grep") || strings.Contains(strings.ToLower(toolName), "glob"):
			label = "searching"
			detail = strings.TrimSpace(toolName)
		case strings.Contains(strings.ToLower(toolName), "write") || strings.Contains(strings.ToLower(toolName), "edit"):
			label = "editing"
			detail = strings.TrimSpace(toolName)
		case strings.Contains(strings.ToLower(toolName), "test") || strings.Contains(strings.ToLower(toolName), "verify") || strings.Contains(strings.ToLower(toolName), "critic"):
			label = "verifying"
			detail = strings.TrimSpace(toolName)
		case strings.Contains(strings.ToLower(toolName), "harness") || strings.Contains(strings.ToLower(toolName), "task"):
			label = "planning"
			detail = "delegating deeper work"
		default:
			label = "thinking"
			detail = strings.TrimSpace(toolName)
		}
		return
	}
	if step == 0 {
		switch toolChoice.(type) {
		case string:
			if strings.EqualFold(strings.TrimSpace(toolChoice.(string)), "required") {
				label = "planning"
				detail = "tool-backed turn starting"
				return
			}
		case map[string]any:
			label = "planning"
			detail = "delegating structured work"
			return
		}
		label = "planning"
		detail = "reviewing context and choosing a next action"
		return
	}
	if strings.TrimSpace(content) != "" {
		text := strings.ToLower(strings.TrimSpace(content))
		switch {
		case strings.Contains(text, "verify") || strings.Contains(text, "test"):
			label = "verifying"
			detail = "checking response quality"
		case strings.Contains(text, "search") || strings.Contains(text, "find"):
			label = "searching"
			detail = "gathering missing context"
		case strings.Contains(text, "write") || strings.Contains(text, "edit") || strings.Contains(text, "update"):
			label = "editing"
			detail = "adjusting the plan or output"
		default:
			label = "thinking"
			detail = "continuing the task"
		}
		return
	}
	label = "thinking"
	detail = "continuing the task"
	return
}

// StreamPhaseForTool classifies a tool call into a turn phase.
func StreamPhaseForTool(toolName string) string {
	t := strings.ToLower(strings.TrimSpace(toolName))
	switch {
	case t == "":
		return ""
	case strings.Contains(t, "read") || strings.Contains(t, "search") || strings.Contains(t, "grep") || strings.Contains(t, "glob") || strings.Contains(t, "find"):
		return "searching"
	case strings.Contains(t, "write") || strings.Contains(t, "edit") || strings.Contains(t, "patch"):
		return "editing"
	case strings.Contains(t, "test") || strings.Contains(t, "verify") || strings.Contains(t, "critic") || strings.Contains(t, "check"):
		return "verifying"
	case strings.Contains(t, "harness") || strings.Contains(t, "task") || strings.Contains(t, "plan"):
		return "planning"
	default:
		return "thinking"
	}
}

// HarnessProgressWriterPrefersPlainText is true when harness run progress should be printed as [1/5·planning …] lines
// instead of [harness_event] JSON. Default: stdout is a terminal. Override with HEROS_HARNESS_PROGRESS=json|text.
func HarnessProgressWriterPrefersPlainText(out io.Writer) bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv("HEROS_HARNESS_PROGRESS"))) {
	case "json":
		return false
	case "text", "plain", "tty":
		return true
	}
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
