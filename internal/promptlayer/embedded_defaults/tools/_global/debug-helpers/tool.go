package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"os"
	"runtime"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "metadata"
	}
	switch action {
	case "status":
		return toolcontract.Ok("debug-helpers", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "metadata", "context":
		envKeys := []string{"GOOS", "GOARCH", "USER", "USERNAME", "SHELL"}
		env := map[string]any{}
		for _, k := range envKeys {
			if v, ok := os.LookupEnv(k); ok {
				env[k] = v
			}
		}
		return toolcontract.Ok("debug-helpers", action, args, map[string]any{
			"session_id": asString(args["_session_id"]),
			"workdir":    asString(args["_workdir"]),
			"runtime":    runtime.GOOS + "/" + runtime.GOARCH,
			"num_cpu":    runtime.NumCPU(),
			"go_version": runtime.Version(),
			"env":        env,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("debug-helpers", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }
