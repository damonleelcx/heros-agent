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
		action = "append"
	}
	wd := strings.TrimSpace(asString(args["_workdir"]))
	if wd == "" {
		wd = "."
	}
	file := filepath.Join(wd, ".heros", "data", "todo-tool.jsonl")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return toolcontract.Error("todo-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	if action == "list" {
		b, err := os.ReadFile(file)
		if err != nil && !os.IsNotExist(err) {
			return toolcontract.Error("todo-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		rows := []string{}
		for _, ln := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(ln) != "" {
				rows = append(rows, ln)
			}
		}
		return toolcontract.Ok("todo-tool", action, args, map[string]any{"count": len(rows), "records": rows}), nil
	}
	rec := map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "payload": args}
	b, _ := json.Marshal(rec)
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return toolcontract.Error("todo-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return toolcontract.Error("todo-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	return toolcontract.Ok("todo-tool", action, args, map[string]any{"file": file}), nil
}

func asString(v any) string { s, _ := v.(string); return s }
