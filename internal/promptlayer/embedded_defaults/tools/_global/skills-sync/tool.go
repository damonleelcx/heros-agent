package tooldef

import (
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "sync_from_dir"
	}
	root := skillsRoot(asString(args["_workdir"]))
	if err := os.MkdirAll(root, 0o755); err != nil {
		return toolcontract.Error("skills-sync", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	src := asString(args["source_dir"])
	if src == "" {
		src = asString(args["path"])
	}
	switch action {
	case "list":
		items, _ := os.ReadDir(root)
		names := []string{}
		for _, it := range items {
			if !it.IsDir() && strings.HasSuffix(strings.ToLower(it.Name()), ".md") {
				names = append(names, strings.TrimSuffix(it.Name(), ".md"))
			}
		}
		sort.Strings(names)
		return toolcontract.Ok("skills-sync", action, args, map[string]any{"skills": names, "count": len(names)}), nil
	case "sync_from_dir":
		if !policyBool(args, "allow_write", false) {
			return toolcontract.Error("skills-sync", toolcontract.ErrorCodePermissionDenied, "policy allow_write=false", action, args), nil
		}
		if strings.TrimSpace(src) == "" {
			return toolcontract.Error("skills-sync", toolcontract.ErrorCodeValidationError, "missing source_dir", action, args), nil
		}
		dry, _ := args["dry_run"].(bool)
		copied := 0
		err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
				return nil
			}
			dst := filepath.Join(root, d.Name())
			if dry {
				copied++
				return nil
			}
			if err := copyFile(path, dst); err != nil {
				return err
			}
			copied++
			return nil
		})
		if err != nil {
			return toolcontract.Error("skills-sync", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("skills-sync", action, args, map[string]any{"source_dir": src, "copied": copied, "dry_run": dry}), nil
	case "drift":
		if strings.TrimSpace(src) == "" {
			return toolcontract.Error("skills-sync", toolcontract.ErrorCodeValidationError, "missing source_dir", action, args), nil
		}
		local := map[string]bool{}
		source := map[string]bool{}
		l, _ := os.ReadDir(root)
		for _, it := range l {
			if !it.IsDir() && strings.HasSuffix(strings.ToLower(it.Name()), ".md") {
				local[it.Name()] = true
			}
		}
		s, _ := os.ReadDir(src)
		for _, it := range s {
			if !it.IsDir() && strings.HasSuffix(strings.ToLower(it.Name()), ".md") {
				source[it.Name()] = true
			}
		}
		missing := []string{}
		extra := []string{}
		for n := range source {
			if !local[n] {
				missing = append(missing, n)
			}
		}
		for n := range local {
			if !source[n] {
				extra = append(extra, n)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		return toolcontract.Ok("skills-sync", action, args, map[string]any{"missing_local": missing, "extra_local": extra}), nil
	default:
		return toolcontract.Error("skills-sync", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
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
func skillsRoot(workdir string) string {
	if strings.TrimSpace(workdir) == "" {
		workdir = "."
	}
	return filepath.Join(workdir, ".heros", "skills")
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
