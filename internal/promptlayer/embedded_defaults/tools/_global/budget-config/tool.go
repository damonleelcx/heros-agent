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
		action = "list"
	}
	wd := strings.TrimSpace(asString(args["_workdir"]))
	if wd == "" {
		wd = "."
	}
	file := filepath.Join(wd, ".heros", "state", "budget-config.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return toolcontract.Error("budget-config", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	st := map[string]any{}
	if b, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	key := strings.TrimSpace(asString(args["key"]))
	switch action {
	case "list":
		return toolcontract.Ok("budget-config", action, args, map[string]any{"state": st}), nil
	case "get":
		return toolcontract.Ok("budget-config", action, args, map[string]any{"key": key, "value": st[key]}), nil
	case "set":
		st[key] = args["value"]
	case "delete":
		delete(st, key)
	default:
		return toolcontract.Error("budget-config", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
	st["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(file, b, 0o644); err != nil {
		return toolcontract.Error("budget-config", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	return toolcontract.Ok("budget-config", action, args, map[string]any{"updated": true}), nil
}

func asString(v any) string { s, _ := v.(string); return s }
