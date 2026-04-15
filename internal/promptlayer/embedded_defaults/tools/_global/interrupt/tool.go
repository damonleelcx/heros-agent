package tooldef

import (
	"encoding/json"
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "ack"
	}
	st, err := loadState(stateFile(asString(args["_workdir"])))
	if err != nil {
		return toolcontract.Error("interrupt", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	switch action {
	case "status":
		return toolcontract.Ok("interrupt", action, args, map[string]any{
			"status":         "ready",
			"interrupt_count": asInt(st["interrupt_count"]),
			"timestamp":      time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "ack", "record":
		payload, _ := args["payload"].(map[string]any)
		if payload == nil {
			payload = map[string]any{}
		}
		st["interrupt_count"] = asInt(st["interrupt_count"]) + 1
		st["last_interrupt_at"] = time.Now().UTC().Format(time.RFC3339)
		st["last_payload"] = payload
		if err := saveState(stateFile(asString(args["_workdir"])), st); err != nil {
			return toolcontract.Error("interrupt", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("interrupt", action, args, map[string]any{
			"acknowledged": true,
			"count":        st["interrupt_count"],
			"payload":      payload,
			"timestamp":    time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("interrupt", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func stateFile(workdir string) string {
	if strings.TrimSpace(workdir) == "" {
		workdir = "."
	}
	return filepath.Join(workdir, ".heros", "state", "interrupt.json")
}

func loadState(path string) (map[string]any, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	st := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st, nil
}

func saveState(path string, st map[string]any) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(path, b, 0o644)
}

func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}
