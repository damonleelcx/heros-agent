package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"regexp"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "strip"
	}
	switch action {
	case "status":
		return toolcontract.Ok("ansi-strip", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "strip":
		text := asString(args["text"])
		if text == "" {
			text = asString(args["input"])
		}
		if text == "" {
			return toolcontract.Error("ansi-strip", toolcontract.ErrorCodeValidationError, "missing text", action, args), nil
		}
		clean := ansiRegex.ReplaceAllString(text, "")
		return toolcontract.Ok("ansi-strip", action, args, map[string]any{
			"text":        clean,
			"changed":     clean != text,
			"input_len":   len(text),
			"output_len":  len(clean),
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("ansi-strip", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
