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
