package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"os"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "read"
	}
	switch action {
	case "status":
		return toolcontract.Ok("env-passthrough", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "read":
		keys := asStringSlice(args["keys"])
		if len(keys) == 0 {
			if k := strings.TrimSpace(asString(args["key"])); k != "" {
				keys = []string{k}
			}
		}
		if len(keys) == 0 {
			return toolcontract.Error("env-passthrough", toolcontract.ErrorCodeValidationError, "missing keys", action, args), nil
		}
		out := map[string]any{}
		for _, key := range keys {
			val, ok := os.LookupEnv(key)
			if !ok {
				out[key] = nil
				continue
			}
			out[key] = maskValue(key, val)
		}
		return toolcontract.Ok("env-passthrough", action, args, map[string]any{
			"values":    out,
			"count":     len(keys),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("env-passthrough", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return nil
	}
}

func maskValue(key, value string) string {
	k := strings.ToLower(key)
	if strings.Contains(k, "key") || strings.Contains(k, "token") || strings.Contains(k, "secret") || strings.Contains(k, "password") {
		if len(value) <= 4 {
			return "****"
		}
		return value[:2] + "****" + value[len(value)-2:]
	}
	return value
}
