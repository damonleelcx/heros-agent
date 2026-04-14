package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "plan_sync"
	}
	workdir := cleanWorkdir(asString(args["_workdir"]))
	source := resolve(workdir, asString(args["source"]))
	target := resolve(workdir, asString(args["target"]))
	if action == "plan_sync" {
		if source == "" {
			source = workdir
		}
		items, err := collectFiles(source)
		if err != nil {
			return toolcontract.Error("environments-file-sync", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("environments-file-sync", action, args, map[string]any{"source": source, "files": items, "count": len(items)}), nil
	}
	if action != "sync_copy" {
		return toolcontract.Error("environments-file-sync", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
	if !policyBool(args, "allow_sync", false) || !policyBool(args, "allow_write", false) {
		return toolcontract.Error("environments-file-sync", toolcontract.ErrorCodePermissionDenied, "policy allow_sync/allow_write required", action, args), nil
	}
	if source == "" || target == "" {
		return toolcontract.Error("environments-file-sync", toolcontract.ErrorCodeValidationError, "missing source or target", action, args), nil
	}
	dryRun, _ := args["dry_run"].(bool)
	copied, skipped, err := syncCopy(source, target, dryRun)
	if err != nil {
		return toolcontract.Error("environments-file-sync", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	return toolcontract.Ok("environments-file-sync", action, args, map[string]any{"source": source, "target": target, "copied": copied, "skipped": skipped, "dry_run": dryRun}), nil
}

func cleanWorkdir(w string) string {
	if strings.TrimSpace(w) == "" {
		return "."
	}
	return w
}
func resolve(workdir, p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(workdir, p))
}
func collectFiles(root string) ([]string, error) {
	out := []string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}
func syncCopy(source, target string, dry bool) (int, int, error) {
	copied := 0
	skipped := 0
	err := filepath.WalkDir(source, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(source, path)
		dst := filepath.Join(target, rel)
		if d.IsDir() {
			if dry {
				return nil
			}
			return os.MkdirAll(dst, 0o755)
		}
		if dry {
			copied++
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(path, dst); err != nil {
			skipped++
			return nil
		}
		copied++
		return nil
	})
	return copied, skipped, err
}
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
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
func asString(v any) string { s, _ := v.(string); return s }
