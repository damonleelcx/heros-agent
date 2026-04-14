package cliagent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEmitHarnessToolEventsStructured(t *testing.T) {
	var buf bytes.Buffer
	start := time.Date(2026, 4, 14, 10, 0, 0, 0, time.UTC)
	end := start.Add(1250 * time.Millisecond)

	emitHarnessStart(&buf, "tool", "toolcall_1", "heros_shell", start)
	emitHarnessEnd(&buf, "tool", "toolcall_1", "heros_shell", "ok", start, end)

	raw := strings.TrimSpace(buf.String())
	lines := strings.Split(raw, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 event lines, got %d: %q", len(lines), raw)
	}

	for i, line := range lines {
		if !strings.HasPrefix(line, HarnessEventPrefix) {
			t.Fatalf("line %d missing prefix: %q", i, line)
		}
		payload := strings.TrimPrefix(line, HarnessEventPrefix)
		var ev HarnessEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("line %d invalid JSON: %v", i, err)
		}
		if ev.Phase != "tool" {
			t.Fatalf("line %d phase=%q", i, ev.Phase)
		}
		if ev.ToolID != "toolcall_1" {
			t.Fatalf("line %d tool_id=%q", i, ev.ToolID)
		}
	}

	var endEv HarnessEvent
	if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[1], HarnessEventPrefix)), &endEv); err != nil {
		t.Fatalf("decode end event: %v", err)
	}
	if endEv.Stage != "end" || endEv.DurationMS != 1250 || endEv.Status != "ok" {
		t.Fatalf("unexpected end event: %+v", endEv)
	}
}
