package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "parse"
	}
	switch action {
	case "status":
		return toolcontract.Ok("patch-parser", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "parse":
		patch := asString(args["patch"])
		if strings.TrimSpace(patch) == "" {
			patch = asString(args["diff"])
		}
		if strings.TrimSpace(patch) == "" {
			return toolcontract.Error("patch-parser", toolcontract.ErrorCodeValidationError, "missing patch text", action, args), nil
		}
		s := parsePatchSummary(patch)
		return toolcontract.Ok("patch-parser", action, args, map[string]any{
			"files":      s.Files,
			"hunks":      s.Hunks,
			"additions":  s.Additions,
			"deletions":  s.Deletions,
			"is_binary":  s.IsBinary,
			"risk_flags": s.RiskFlags,
			"timestamp":  time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("patch-parser", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

type patchSummary struct {
	Files     int
	Hunks     int
	Additions int
	Deletions int
	IsBinary  bool
	RiskFlags []string
}

func parsePatchSummary(patch string) patchSummary {
	lines := strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n")
	s := patchSummary{}
	riskSet := map[string]struct{}{}
	for _, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			s.Files++
		case strings.HasPrefix(ln, "@@"):
			s.Hunks++
		case strings.HasPrefix(ln, "+") && !strings.HasPrefix(ln, "+++"):
			s.Additions++
		case strings.HasPrefix(ln, "-") && !strings.HasPrefix(ln, "---"):
			s.Deletions++
		}
		l := strings.ToLower(ln)
		if strings.Contains(l, "binary files") || strings.Contains(l, "git binary patch") {
			s.IsBinary = true
		}
		if strings.Contains(l, "private_key") || strings.Contains(l, "api_key") || strings.Contains(l, "password") {
			riskSet["possible_secret"] = struct{}{}
		}
		if strings.Contains(l, "chmod 777") || strings.Contains(l, "rm -rf") {
			riskSet["dangerous_command"] = struct{}{}
		}
	}
	for k := range riskSet {
		s.RiskFlags = append(s.RiskFlags, k)
	}
	return s
}
