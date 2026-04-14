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
	workdir := strings.TrimSpace(asString(args["_workdir"]))
	if workdir == "" {
		workdir = "."
	}
	if action == "status" {
		return toolcontract.Ok("environments-docker", "status", args, map[string]any{"environment": "docker", "runtime": runtime.GOOS + "/" + runtime.GOARCH, "workdir": workdir}), nil
	}
	if action != "run" {
		return toolcontract.Error("environments-docker", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
	if !policyBool(args, "allow_exec", false) {
		return toolcontract.Error("environments-docker", toolcontract.ErrorCodePermissionDenied, "policy allow_exec=false", action, args), nil
	}
	cmdText := strings.TrimSpace(asString(args["command"]))
	if cmdText == "" {
		return toolcontract.Error("environments-docker", toolcontract.ErrorCodeValidationError, "missing command", action, args), nil
	}
	if isDangerous(cmdText) && !policyBool(args, "allow_dangerous", false) {
		return toolcontract.Error("environments-docker", toolcontract.ErrorCodePolicyBlocked, "dangerous command blocked", action, args), nil
	}
	start := time.Now()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", cmdText)
	} else {
		cmd = exec.Command("sh", "-lc", cmdText)
	}
	cmd.Dir = workdir
	out, runErr := cmd.CombinedOutput()
	data := map[string]any{"environment": "docker", "command": cmdText, "output": string(out), "duration_ms": time.Since(start).Milliseconds()}
	if runErr != nil {
		data["run_error"] = runErr.Error()
		return toolcontract.Error("environments-docker", toolcontract.ErrorCodeCommandFailed, runErr.Error(), action, args, data), nil
	}
	return toolcontract.Ok("environments-docker", action, args, data), nil
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
func isDangerous(cmd string) bool {
	c := strings.ToLower(cmd)
	bad := []string{"rm -rf", "mkfs", "shutdown", "reboot", "format", "del /f", "diskpart"}
	for _, k := range bad {
		if strings.Contains(c, k) {
			return true
		}
	}
	return false
}
func asString(v any) string { s, _ := v.(string); return s }
