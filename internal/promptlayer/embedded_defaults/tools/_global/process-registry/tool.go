package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "list"
	}
	switch action {
	case "status":
		return toolcontract.Ok("process-registry", action, args, map[string]any{
			"status":    "ready",
			"platform":  runtime.GOOS,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "list":
		out, err := listProcesses()
		if err != nil {
			return toolcontract.Error("process-registry", toolcontract.ErrorCodeCommandFailed, err.Error(), action, args), nil
		}
		return toolcontract.Ok("process-registry", action, args, map[string]any{
			"processes": out,
			"count":     len(out),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "terminate", "kill":
		if !policyBool(args, "allow_admin", false) {
			return toolcontract.Error("process-registry", toolcontract.ErrorCodePermissionDenied, "policy allow_admin=false", action, args), nil
		}
		pid := asInt(args["pid"])
		if pid <= 0 {
			return toolcontract.Error("process-registry", toolcontract.ErrorCodeValidationError, "missing or invalid pid", action, args), nil
		}
		self := os.Getpid()
		parent := os.Getppid()
		force := policyBool(args, "allow_dangerous", false) || asBool(args["force"])
		if !force && pid == self {
			return toolcontract.Error("process-registry", toolcontract.ErrorCodePolicyBlocked, "refusing to kill current process without force", action, args), nil
		}
		if !force && parent > 0 && pid == parent {
			return toolcontract.Error("process-registry", toolcontract.ErrorCodePolicyBlocked, "refusing to kill parent process without force", action, args), nil
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			return toolcontract.Error("process-registry", toolcontract.ErrorCodeNotFound, err.Error(), action, args), nil
		}
		if err := proc.Kill(); err != nil {
			return toolcontract.Error("process-registry", toolcontract.ErrorCodeCommandFailed, err.Error(), action, args), nil
		}
		return toolcontract.Ok("process-registry", action, args, map[string]any{
			"terminated": true,
			"pid":        pid,
			"forced":     force,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("process-registry", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(t))
		return i
	default:
		return 0
	}
}

func listProcesses() ([]map[string]any, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("tasklist", "/FO", "CSV", "/NH")
	} else {
		cmd = exec.Command("ps", "-eo", "pid=,comm=")
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseProcessOutput(string(out)), nil
}

func parseProcessOutput(s string) []map[string]any {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	rows := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if runtime.GOOS == "windows" {
			line = strings.Trim(line, "\"")
			parts := strings.Split(line, "\",\"")
			if len(parts) >= 2 {
				rows = append(rows, map[string]any{
					"name": parts[0],
					"pid":  parts[1],
				})
			}
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			rows = append(rows, map[string]any{
				"pid":  parts[0],
				"name": strings.Join(parts[1:], " "),
			})
		}
	}
	return rows
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

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	default:
		return false
	}
}
