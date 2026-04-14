package tooldef

import (
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
	p := strings.TrimSpace(asString(args["path"]))
	if p == "" {
		p = "."
	}
	t := p
	if !filepath.IsAbs(t) {
		t = filepath.Join(wd, t)
	}
	t = filepath.Clean(t)
	switch action {
	case "list":
		items, err := os.ReadDir(t)
		if err != nil {
			return toolcontract.Error("file-operations", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		rows := []map[string]any{}
		for _, it := range items {
			rows = append(rows, map[string]any{"name": it.Name(), "is_dir": it.IsDir()})
		}
		return toolcontract.Ok("file-operations", action, args, map[string]any{"path": t, "entries": rows}), nil
	case "read":
		b, err := os.ReadFile(t)
		if err != nil {
			return toolcontract.Error("file-operations", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("file-operations", action, args, map[string]any{"path": t, "content": string(b)}), nil
	case "write":
		if err := os.MkdirAll(filepath.Dir(t), 0o755); err != nil {
			return toolcontract.Error("file-operations", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		if err := os.WriteFile(t, []byte(asString(args["content"])), 0o644); err != nil {
			return toolcontract.Error("file-operations", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("file-operations", action, args, map[string]any{"path": t, "updated_at": time.Now().UTC().Format(time.RFC3339)}), nil
	case "delete":
		if err := os.RemoveAll(t); err != nil {
			return toolcontract.Error("file-operations", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("file-operations", action, args, map[string]any{"path": t}), nil
	default:
		return toolcontract.Error("file-operations", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }
