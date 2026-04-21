package cliagent

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const HarnessEventPrefix = "[harness_event] "

// HarnessEvent is a normalized streaming event for harness-style timeline views.
// One event is emitted per line with a stable prefix + JSON payload.
type HarnessEvent struct {
	Phase      string   `json:"phase"`
	Stage      string   `json:"stage"`
	Message    string   `json:"message,omitempty"`
	Index      int      `json:"index,omitempty"`
	Total      int      `json:"total,omitempty"`
	Attempt    int      `json:"attempt,omitempty"`
	Role       string   `json:"role,omitempty"`
	TodoID     string   `json:"todo_id,omitempty"`
	Score      float64  `json:"score,omitempty"`
	Threshold  float64  `json:"threshold,omitempty"`
	Tools      []string `json:"tools,omitempty"`
	Skills     []string `json:"skills,omitempty"`
	Memory     []string `json:"memory,omitempty"`
	Start      string   `json:"start,omitempty"`
	End        string   `json:"end,omitempty"`
	DurationMS int64    `json:"duration_ms,omitempty"`
	ToolID     string   `json:"tool_id,omitempty"`
	ToolName   string   `json:"tool_name,omitempty"`
	Status     string   `json:"status,omitempty"`
}

func emitHarnessEvent(out io.Writer, ev HarnessEvent) {
	if out == nil {
		return
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(out, "%s%s\n", HarnessEventPrefix, string(b))
}

func emitHarnessStart(out io.Writer, phase, toolID, toolName string, start time.Time) {
	emitHarnessEvent(out, HarnessEvent{
		Phase:    phase,
		Stage:    "start",
		Start:    start.UTC().Format(time.RFC3339Nano),
		ToolID:   toolID,
		ToolName: toolName,
	})
}

func emitHarnessEnd(out io.Writer, phase, toolID, toolName, status string, start, end time.Time) {
	emitHarnessEvent(out, HarnessEvent{
		Phase:      phase,
		Stage:      "end",
		Start:      start.UTC().Format(time.RFC3339Nano),
		End:        end.UTC().Format(time.RFC3339Nano),
		DurationMS: end.Sub(start).Milliseconds(),
		ToolID:     toolID,
		ToolName:   toolName,
		Status:     status,
	})
}
