package toolindex

import (
	"os"

	"github.com/heros-foreal/agentd/internal/agentlayout"
	"gopkg.in/yaml.v3"
)

// WriteToolYAML writes authoritative tools/<tenant>/<id>/tool.yaml (legacy flat tools/<id>/ uses tenant _global).
func WriteToolYAML(dataDir, tenantScope, toolID, description, riskTier, scriptPath string, skills []string) error {
	ts := agentlayout.SanitizeTenantScope(tenantScope)
	id := agentlayout.SanitizeSlug(toolID)
	dir := agentlayout.ToolDirForTenant(dataDir, ts, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	spec := ToolSpec{
		ID:          id,
		RiskTier:    riskTier,
		Skills:      skills,
		Description: description,
		ScriptPath:  scriptPath,
	}
	b, err := yaml.Marshal(&spec)
	if err != nil {
		return err
	}
	return os.WriteFile(agentlayout.ToolConfigPathForTenant(dataDir, ts, id), b, 0o644)
}
