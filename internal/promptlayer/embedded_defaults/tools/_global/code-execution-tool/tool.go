package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "run"
	}
	if action == "status" {
		return toolcontract.Ok("code-execution-tool", "status", args, map[string]any{"runtime": runtime.GOOS + "/" + runtime.GOARCH}), nil
	}
	if action != "run" {
		return toolcontract.Error("code-execution-tool", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
	if p, _ := args["policy"].(map[string]any); p != nil {
		if allow, ok := p["allow_exec"].(bool); ok && !allow {
			return toolcontract.Error("code-execution-tool", toolcontract.ErrorCodePermissionDenied, "policy allow_exec=false", action, args), nil
		}
	}
	cmdText := strings.TrimSpace(asString(args["command"]))
	if cmdText == "" {
		return toolcontract.Error("code-execution-tool", toolcontract.ErrorCodeValidationError, "missing command", action, args), nil
	}
	wd := strings.TrimSpace(asString(args["_workdir"]))
	if wd == "" {
		wd = "."
	}
	start := time.Now()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", cmdText)
	} else {
		cmd = exec.Command("sh", "-lc", cmdText)
	}
	cmd.Dir = wd
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return toolcontract.Ok("code-execution-tool", action, args, map[string]any{"output": string(out), "duration_ms": time.Since(start).Milliseconds(), "run_error": runErr.Error()}), nil
	}
	return toolcontract.Ok("code-execution-tool", action, args, map[string]any{"output": string(out), "duration_ms": time.Since(start).Milliseconds()}), nil
}

func asString(v any) string { s, _ := v.(string); return s }
