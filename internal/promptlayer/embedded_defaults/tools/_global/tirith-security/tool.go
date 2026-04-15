package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "classify"
	}
	switch action {
	case "status":
		return toolcontract.Ok("tirith-security", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "classify", "assess":
		cmd := strings.TrimSpace(asString(args["command"]))
		if cmd == "" {
			cmd = strings.TrimSpace(asString(args["input"]))
		}
		if cmd == "" {
			return toolcontract.Error("tirith-security", toolcontract.ErrorCodeValidationError, "missing command", action, args), nil
		}
		risk, reason, blocked := classifyCommand(cmd)
		if blocked && !policyBool(args, "allow_dangerous", false) {
			return toolcontract.Error("tirith-security", toolcontract.ErrorCodePolicyBlocked, reason, action, args, map[string]any{
				"command": cmd,
				"risk":    risk,
			}), nil
		}
		return toolcontract.Ok("tirith-security", action, args, map[string]any{
			"command":   cmd,
			"risk":      risk,
			"reason":    reason,
			"blocked":   blocked,
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("tirith-security", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func classifyCommand(cmd string) (risk, reason string, blocked bool) {
	c := strings.ToLower(cmd)
	highPatterns := []string{
		"rm -rf /", "mkfs", "dd if=", "shutdown", "reboot",
		"del /f /s /q", "format c:", "cipher /w",
	}
	mediumPatterns := []string{
		"rm -rf", "chmod 777", "chown -r", "git reset --hard",
		"drop table", "truncate table", "kubectl delete",
	}
	for _, p := range highPatterns {
		if strings.Contains(c, p) {
			return "high", "dangerous destructive pattern detected", true
		}
	}
	for _, p := range mediumPatterns {
		if strings.Contains(c, p) {
			return "medium", "potentially destructive pattern detected", false
		}
	}
	return "low", "no dangerous pattern detected", false
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
