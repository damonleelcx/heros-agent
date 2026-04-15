package tooldef

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"regexp"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := asString(args["action"])
	if action == "" {
		action = "run"
	}

	switch action {
	case "run", "echo":
		text := sanitize(asString(args["text"]))
		if text == "" {
			text = sanitize(asString(args["message"]))
		}
		return toolcontract.Ok("echo-safe", action, args, map[string]any{
			"text":      text,
			"length":    len(text),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "redact":
		text := sanitize(asString(args["text"]))
		if text == "" {
			text = sanitize(asString(args["message"]))
		}
		redacted, count := redactSecrets(text)
		return toolcontract.Ok("echo-safe", action, args, map[string]any{
			"text":           redacted,
			"redaction_count": count,
			"timestamp":      time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "hash":
		text := sanitize(asString(args["text"]))
		if text == "" {
			text = sanitize(asString(args["message"]))
		}
		sum := sha256.Sum256([]byte(text))
		return toolcontract.Ok("echo-safe", action, args, map[string]any{
			"sha256":    hex.EncodeToString(sum[:]),
			"length":    len(text),
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "status":
		return toolcontract.Ok("echo-safe", action, args, map[string]any{
			"status":    "ready",
			"features":  []string{"echo", "redact", "hash"},
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("echo-safe", toolcontract.ErrorCodeInvalidAction, "unknown action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' && r != '\r' {
			return -1
		}
		return r
	}, s)
	if len(s) > 8192 {
		return s[:8192]
	}
	return s
}

func redactSecrets(s string) (string, int) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)(api[_-]?key|token|password)\s*[:=]\s*([^\s,;]+)`),
		regexp.MustCompile(`(?i)bearer\s+[a-z0-9\-_\.=]+`),
		regexp.MustCompile(`[A-Za-z0-9_\-]{24,}\.[A-Za-z0-9_\-]{6,}\.[A-Za-z0-9_\-]{12,}`),
	}
	count := 0
	out := s
	for _, re := range patterns {
		out = re.ReplaceAllStringFunc(out, func(string) string {
			count++
			return "[REDACTED]"
		})
	}
	return out, count
}
