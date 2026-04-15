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
		action = "enqueue"
	}
	st, err := loadState(stateFile(asString(args["_workdir"])))
	if err != nil {
		return toolcontract.Error("neutts-synth", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	switch action {
	case "status":
		queue := asQueue(st["queue"])
		return toolcontract.Ok("neutts-synth", action, args, map[string]any{
			"status":    "ready",
			"queued":    len(queue),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "enqueue":
		text := strings.TrimSpace(asString(args["text"]))
		if text == "" {
			return toolcontract.Error("neutts-synth", toolcontract.ErrorCodeValidationError, "missing text", action, args), nil
		}
		voice := strings.TrimSpace(asString(args["voice"]))
		if voice == "" {
			voice = "default"
		}
		queue := asQueue(st["queue"])
		item := map[string]any{
			"id":         time.Now().UTC().Format("20060102150405.000000000"),
			"text_len":   len(text),
			"voice":      voice,
			"created_at": time.Now().UTC().Format(time.RFC3339),
		}
		queue = append(queue, item)
		st["queue"] = queue
		if err := saveState(stateFile(asString(args["_workdir"])), st); err != nil {
			return toolcontract.Error("neutts-synth", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("neutts-synth", action, args, map[string]any{
			"queued":     true,
			"queue_size": len(queue),
			"item":       item,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "list":
		queue := asQueue(st["queue"])
		return toolcontract.Ok("neutts-synth", action, args, map[string]any{
			"queue":      queue,
			"queue_size": len(queue),
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "clear":
		if !policyBool(args, "allow_admin", false) {
			return toolcontract.Error("neutts-synth", toolcontract.ErrorCodePermissionDenied, "policy allow_admin=false", action, args), nil
		}
		st["queue"] = []map[string]any{}
		if err := saveState(stateFile(asString(args["_workdir"])), st); err != nil {
			return toolcontract.Error("neutts-synth", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("neutts-synth", action, args, map[string]any{
			"cleared":   true,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("neutts-synth", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func stateFile(workdir string) string {
	if strings.TrimSpace(workdir) == "" {
		workdir = "."
	}
	return filepath.Join(workdir, ".heros", "state", "neutts-synth.json")
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

func asQueue(v any) []map[string]any {
	raw, ok := v.([]any)
	if !ok {
		if q, ok2 := v.([]map[string]any); ok2 {
			return q
		}
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, _ := item.(map[string]any)
		if m != nil {
			out = append(out, m)
		}
	}
	return out
}

func policyBool(args map[string]any, key string, def bool) bool {
	p, _ := args["policy"].(map[string]any)
	if p == nil {
		return def
	}
	b, ok := p[key].(bool)
	if !ok {
		return def
	}
	return b
}
