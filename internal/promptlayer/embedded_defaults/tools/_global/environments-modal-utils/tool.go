package tooldef

import (
	"encoding/json"
	"github.com/heros-foreal/agentd/internal/toolcontract"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func Execute(args map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(asString(args["action"])))
	if action == "" {
		action = "list"
	}
	state, err := loadState(stateFile(asString(args["_workdir"])))
	if err != nil {
		return toolcontract.Error("environments-modal-utils", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
	}
	key := strings.TrimSpace(asString(args["key"]))
	switch action {
	case "list":
		keys := sortedKeys(state)
		return toolcontract.Ok("environments-modal-utils", action, args, map[string]any{"keys": keys, "count": len(keys)}), nil
	case "get":
		if key == "" {
			return toolcontract.Error("environments-modal-utils", toolcontract.ErrorCodeValidationError, "missing key", action, args), nil
		}
		return toolcontract.Ok("environments-modal-utils", action, args, map[string]any{"key": key, "value": state[key]}), nil
	case "set", "delete", "clear", "set_active", "register_profile":
		if !policyBool(args, "allow_admin", false) {
			return toolcontract.Error("environments-modal-utils", toolcontract.ErrorCodePermissionDenied, "policy allow_admin=false", action, args), nil
		}
		if (action == "set" || action == "delete") && key == "" {
			return toolcontract.Error("environments-modal-utils", toolcontract.ErrorCodeValidationError, "missing key", action, args), nil
		}
		if action == "set" {
			state[key] = args["value"]
		}
		if action == "delete" {
			delete(state, key)
		}
		if action == "clear" {
			state = map[string]any{}
		}
		if action == "set_active" {
			name := strings.TrimSpace(asString(args["name"]))
			if name == "" {
				return toolcontract.Error("environments-modal-utils", toolcontract.ErrorCodeValidationError, "missing name", action, args), nil
			}
			state["active_profile"] = name
		}
		if action == "register_profile" {
			name := strings.TrimSpace(asString(args["name"]))
			if name == "" {
				return toolcontract.Error("environments-modal-utils", toolcontract.ErrorCodeValidationError, "missing name", action, args), nil
			}
			profiles, _ := state["profiles"].(map[string]any)
			if profiles == nil {
				profiles = map[string]any{}
			}
			cfg, _ := args["config"].(map[string]any)
			if cfg == nil {
				cfg = map[string]any{}
			}
			cfg["updated_at"] = time.Now().UTC().Format(time.RFC3339)
			profiles[name] = cfg
			state["profiles"] = profiles
		}
		if err := saveState(stateFile(asString(args["_workdir"])), state); err != nil {
			return toolcontract.Error("environments-modal-utils", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("environments-modal-utils", action, args, map[string]any{"updated": true}), nil
	default:
		return toolcontract.Error("environments-modal-utils", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func stateFile(workdir string) string {
	if strings.TrimSpace(workdir) == "" {
		workdir = "."
	}
	return filepath.Join(workdir, ".heros", "state", "environments-modal-utils.json")
}
func loadState(path string) (map[string]any, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	st := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st, nil
}
func saveState(path string, st map[string]any) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(path, b, 0o644)
}
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
