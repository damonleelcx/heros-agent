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
		action = "status"
	}
	wd := strings.TrimSpace(asString(args["_workdir"]))
	if wd == "" {
		wd = "."
	}
	file := filepath.Join(wd, ".heros", "runtime", "browser-tool.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return toolcontract.Error("browser-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	st := map[string]any{}
	if b, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if action == "status" {
		return toolcontract.Ok("browser-tool", action, args, map[string]any{"state": st}), nil
	}
	if action == "reset" {
		st = map[string]any{}
	} else {
		st[action] = args
	}
	st["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(file, b, 0o644); err != nil {
		return toolcontract.Error("browser-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	return toolcontract.Ok("browser-tool", action, args, map[string]any{"updated": true}), nil
}

func asString(v any) string { s, _ := v.(string); return s }
