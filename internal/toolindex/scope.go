package toolindex

import (
	"strings"

	"github.com/heros-foreal/agentd/internal/agentlayout"
)

// ParseTenantScopeFromToolRel derives tenant from tools/.../tool.yaml (mirrors skills layout).
// tools/<id>/tool.yaml → _global; tools/<tenant>/<id>/tool.yaml → tenant.
func ParseTenantScopeFromToolRel(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) < 3 || parts[0] != agentlayout.ToolsDir || parts[len(parts)-1] != agentlayout.ToolConfig {
		return "_global"
	}
	if len(parts) == 3 {
		return "_global"
	}
	return agentlayout.SanitizeTenantScope(parts[1])
}

// ScopedToolID is a stable graph/catalog id (tenant + tool id).
func ScopedToolID(tenantScope, toolID string) string {
	return agentlayout.SanitizeTenantScope(tenantScope) + "/" + strings.TrimSpace(toolID)
}

// ResolveToolTarget finds a tool graph id: same tenant first, then _global.
func ResolveToolTarget(all []Entry, fromTenant, toolRef string) string {
	toolRef = strings.TrimSpace(toolRef)
	if toolRef == "" {
		return ""
	}
	ids := map[string]struct{}{}
	for _, t := range all {
		ids[ScopedToolID(t.TenantScope, t.ToolID)] = struct{}{}
	}
	for _, cand := range []string{ScopedToolID(fromTenant, toolRef), ScopedToolID("_global", toolRef)} {
		if _, ok := ids[cand]; ok {
			return cand
		}
	}
	return ""
}
