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
		action = "scan"
	}
	switch action {
	case "status":
		return toolcontract.Ok("credential-files", action, args, map[string]any{
			"status":    "ready",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}), nil
	case "run", "scan":
		root := strings.TrimSpace(asString(args["root"]))
		if root == "" {
			root = strings.TrimSpace(asString(args["_workdir"]))
		}
		if root == "" {
			root = "."
		}
		found, err := scanCredentialFiles(root)
		if err != nil {
			return toolcontract.Error("credential-files", toolcontract.ErrorCodeIOError, err.Error(), action, args), nil
		}
		return toolcontract.Ok("credential-files", action, args, map[string]any{
			"root":           root,
			"matches":        found,
			"count":          len(found),
			"risk_score":     totalRisk(found),
			"highest_severity": highestSeverity(found),
			"timestamp":      time.Now().UTC().Format(time.RFC3339),
		}), nil
	default:
		return toolcontract.Error("credential-files", toolcontract.ErrorCodeInvalidAction, "unsupported action", action, args), nil
	}
}

func asString(v any) string { s, _ := v.(string); return s }

func scanCredentialFiles(root string) ([]map[string]any, error) {
	patterns := []string{
		".env", ".env.local", ".env.production",
		".npmrc", ".pypirc", ".netrc", "id_rsa", "id_ed25519",
		"credentials", "secrets.json", "auth.json",
	}
	found := make([]map[string]any, 0)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := strings.ToLower(d.Name())
		if d.IsDir() && (name == ".git" || name == "node_modules" || name == ".venv") {
			return filepath.SkipDir
		}
		for _, p := range patterns {
			if name == strings.ToLower(p) {
				found = append(found, map[string]any{
					"path":     path,
					"name":     d.Name(),
					"severity": severityForName(name),
					"risk":     riskForName(name),
				})
				break
			}
		}
		return nil
	})
	return found, err
}

func riskForName(name string) int {
	switch strings.ToLower(name) {
	case "id_rsa", "id_ed25519":
		return 90
	case ".env.production", "secrets.json", "credentials", ".netrc":
		return 80
	case ".env", ".env.local", "auth.json", ".npmrc", ".pypirc":
		return 60
	default:
		return 40
	}
}

func severityForName(name string) string {
	r := riskForName(name)
	switch {
	case r >= 85:
		return "critical"
	case r >= 70:
		return "high"
	case r >= 50:
		return "medium"
	default:
		return "low"
	}
}

func totalRisk(matches []map[string]any) int {
	sum := 0
	for _, m := range matches {
		switch t := m["risk"].(type) {
		case int:
			sum += t
		case float64:
			sum += int(t)
		}
	}
	if sum > 100 {
		return 100
	}
	return sum
}

func highestSeverity(matches []map[string]any) string {
	order := map[string]int{"low": 1, "medium": 2, "high": 3, "critical": 4}
	best := "low"
	for _, m := range matches {
		s, _ := m["severity"].(string)
		if order[s] > order[best] {
			best = s
		}
	}
	if len(matches) == 0 {
		return "none"
	}
	return best
}
