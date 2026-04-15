package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "validate"
	}
	switch action {
	case "status":
		return toolcontract.Ok("path-security", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "validate", "run":
		target := strings.TrimSpace(asString(args["path"]))
		if target == "" {
			target = strings.TrimSpace(asString(args["target"]))
		}
		if target == "" {
			return toolcontract.Error("path-security", toolcontract.ErrorCodeValidationError, "missing path", action, args), nil
		}
		root := strings.TrimSpace(asString(args["workspace_root"]))
		if root == "" {
			root = strings.TrimSpace(asString(args["_workdir"]))
		}
		if root == "" {
			root = "."
		}
		ok, info := validatePath(root, target)
		return toolcontract.Ok("path-security", action, args, map[string]any{
			"allow":     ok,
			"safe":      ok,
			"risk":      ternary(ok, "low", "high"),
			"reason":    info["reason"],
			"path":      info["path"],
			"root":      info["root"],
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("path-security", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func validatePath(root, target string) (bool, map[string]string) {
	absRoot, err := canonicalPath(root)
	if err != nil {
		return false, map[string]string{"reason": "invalid root", "root": root, "path": target}
	}
	absTarget, err := resolveTarget(absRoot, target)
	if err != nil {
		return false, map[string]string{"reason": "invalid path", "root": absRoot, "path": target}
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return false, map[string]string{"reason": "unable to compare paths", "root": absRoot, "path": absTarget}
	}
	if rel == "." {
		return true, map[string]string{"reason": "path equals root", "root": absRoot, "path": absTarget}
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return false, map[string]string{"reason": "path escapes workspace root", "root": absRoot, "path": absTarget}
	}
	if hasSymlinkEscape(absRoot, absTarget) {
		return false, map[string]string{"reason": "symlink traversal escapes workspace root", "root": absRoot, "path": absTarget}
	}
	return true, map[string]string{"reason": "path is inside workspace root (canonical)", "root": absRoot, "path": absTarget}
}

func ternary(ok bool, a, b string) string {
	if ok {
		return a
	}
	return b
}

func canonicalPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(real), nil
	}
	return filepath.Clean(abs), nil
}

func resolveTarget(absRoot, target string) (string, error) {
	t := strings.TrimSpace(target)
	if !filepath.IsAbs(t) {
		t = filepath.Join(absRoot, t)
	}
	return canonicalPath(t)
}

func hasSymlinkEscape(absRoot, absTarget string) bool {
	// If target does not exist yet, no symlink check can be enforced.
	if _, err := os.Lstat(absTarget); err != nil {
		return false
	}
	realTarget, err := filepath.EvalSymlinks(absTarget)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, realTarget)
	if err != nil {
		return true
	}
	return strings.HasPrefix(rel, "..") || filepath.IsAbs(rel)
}
