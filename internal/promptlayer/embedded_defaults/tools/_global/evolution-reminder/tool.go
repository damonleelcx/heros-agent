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
		action = "remind"
	}
	st, err := loadState(stateFile(asString(args["_workdir"])))
	if err != nil {
		return toolcontract.Error("evolution-reminder", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	switch action {
	case "status":
		return toolcontract.Ok("evolution-reminder", action, args, map[string]any{
			"status":          "ready",
			"reminders_shown": asInt(st["reminders_shown"]),
			"dismissed":       asBool(st["dismissed"]),
			"timestamp":       time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "dismiss":
		st["dismissed"] = true
		if err := saveState(stateFile(asString(args["_workdir"])), st); err != nil {
			return toolcontract.Error("evolution-reminder", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("evolution-reminder", action, args, map[string]any{
			"dismissed":  true,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "remind":
		if asBool(st["dismissed"]) {
			return toolcontract.Ok("evolution-reminder", action, args, map[string]any{
				"remind":     false,
				"message":    "reminder dismissed",
				"timestamp":  time.Now().UTC().Format(time.RFC3339),
			}), nil
		}
		st["reminders_shown"] = asInt(st["reminders_shown"]) + 1
		_ = saveState(stateFile(asString(args["_workdir"])), st)
		return toolcontract.Ok("evolution-reminder", action, args, map[string]any{
			"remind":  true,
			"message": "Consider creating a proposal/skill update for durable workflow improvements.",
			"count":   st["reminders_shown"],
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("evolution-reminder", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func stateFile(workdir string) string {
	if strings.TrimSpace(workdir) == "" {
		workdir = "."
	}
	return filepath.Join(workdir, ".heros", "state", "evolution-reminder.json")
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

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
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
