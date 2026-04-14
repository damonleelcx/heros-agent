package tooldef

import (
	"encoding/json"
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var validSkill = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,80}$`)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "list"
	}
	root := skillsRoot(asString(args["_workdir"]))
	if err := os.MkdirAll(root, 0o755); err != nil {
		return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	name := strings.TrimSpace(asString(args["name"]))
	if name != "" && !validSkill.MatchString(name) {
		return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeValidationError, "invalid skill name", action, args), nil
	}
	target := filepath.Join(root, name+".md")
	metaPath := filepath.Join(root, ".metadata.json")
	switch action {
	case "list":
		items, err := os.ReadDir(root)
		if err != nil {
			return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		names := []string{}
		for _, it := range items {
			if !it.IsDir() && strings.HasSuffix(strings.ToLower(it.Name()), ".md") {
				names = append(names, strings.TrimSuffix(it.Name(), ".md"))
			}
		}
		sort.Strings(names)
		return toolcontract.Ok("skill-manager-tool", action, args, map[string]any{"skills": names, "count": len(names)}), nil
	case "read":
		if name == "" {
			return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeValidationError, "missing name", action, args), nil
		}
		b, err := os.ReadFile(target)
		if err != nil {
			return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeNotFound, err.Error(), action, args), nil
		}
		return toolcontract.Ok("skill-manager-tool", action, args, map[string]any{"name": name, "content": string(b)}), nil
	case "search":
		query := strings.ToLower(strings.TrimSpace(asString(args["query"])))
		if query == "" {
			return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeValidationError, "missing query", action, args), nil
		}
		items, _ := os.ReadDir(root)
		matches := []string{}
		for _, it := range items {
			if it.IsDir() || !strings.HasSuffix(strings.ToLower(it.Name()), ".md") {
				continue
			}
			b, _ := os.ReadFile(filepath.Join(root, it.Name()))
			if strings.Contains(strings.ToLower(string(b)), query) || strings.Contains(strings.ToLower(it.Name()), query) {
				matches = append(matches, strings.TrimSuffix(it.Name(), ".md"))
			}
		}
		sort.Strings(matches)
		return toolcontract.Ok("skill-manager-tool", action, args, map[string]any{"query": query, "matches": matches, "count": len(matches)}), nil
	case "write", "delete", "set_meta":
		if !policyBool(args, "allow_write", false) {
			return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodePermissionDenied, "policy allow_write=false", action, args), nil
		}
		if name == "" && action != "set_meta" {
			return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeValidationError, "missing name", action, args), nil
		}
		if action == "write" {
			content := asString(args["content"])
			if strings.TrimSpace(content) == "" {
				return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeValidationError, "empty content", action, args), nil
			}
			if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
				return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
			}
			return toolcontract.Ok("skill-manager-tool", action, args, map[string]any{"name": name, "updated_at": time.Now().UTC().Format(time.RFC3339)}), nil
		}
		if action == "delete" {
			if err := os.Remove(target); err != nil {
				return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
			}
			return toolcontract.Ok("skill-manager-tool", action, args, map[string]any{"deleted": name}), nil
		}
		meta, _ := args["metadata"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
		}
		meta["updated_at"] = time.Now().UTC().Format(time.RFC3339)
		b, _ := json.MarshalIndent(meta, "", "  ")
		if err := os.WriteFile(metaPath, b, 0o644); err != nil {
			return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("skill-manager-tool", action, args, map[string]any{"metadata_file": metaPath}), nil
	case "get_meta":
		b, err := os.ReadFile(metaPath)
		if err != nil && !os.IsNotExist(err) {
			return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		meta := map[string]any{}
		if len(b) > 0 {
			_ = json.Unmarshal(b, &meta)
		}
		return toolcontract.Ok("skill-manager-tool", action, args, map[string]any{"metadata": meta}), nil
	default:
		return toolcontract.Error("skill-manager-tool", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
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
