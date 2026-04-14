package cliagent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ArgString returns a tool argument as a non-empty string; objects/arrays are JSON-encoded.
func ArgString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		// JSON numbers; avoid scientific notation for small ints used as k
		if x == float64(int64(x)) {
			return fmt.Sprintf("%.0f", x)
		}
		return fmt.Sprint(x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case json.Number:
		return strings.TrimSpace(x.String())
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return strings.TrimSpace(string(b))
	}
}

// ArgJSONObject returns a JSON object argument; accepts a map or a JSON object string.
func ArgJSONObject(args map[string]any, key string) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	v, ok := args[key]
	if !ok || v == nil {
		return map[string]any{}
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	if s, ok := v.(string); ok {
		var m map[string]any
		if json.Unmarshal([]byte(strings.TrimSpace(s)), &m) == nil && m != nil {
			return m
		}
	}
	return map[string]any{}
}
