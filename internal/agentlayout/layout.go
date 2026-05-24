// Package agentlayout defines the on-disk layout for skills, tools, memory, and system prompt.
// Filesystem is the source of truth; SQLite/Neo4j/Qdrant hold indexes and graph edges only.
package agentlayout

import (
	"path/filepath"
	"strings"
)

const (
	SkillsDir  = "skills"
	ToolsDir   = "tools"
	MemoryDir  = "memory"
	SystemDir  = "system"
	SkillFile  = "SKILL.md"
	ToolConfig = "tool.yaml"
	PromptFile = "prompt.md"
)

func SkillsRoot(dataDir string) string  { return filepath.Join(dataDir, SkillsDir) }
func ToolsRoot(dataDir string) string   { return filepath.Join(dataDir, ToolsDir) }
func MemoryRoot(dataDir string) string  { return filepath.Join(dataDir, MemoryDir) }
func SystemRoot(dataDir string) string  { return filepath.Join(dataDir, SystemDir) }
func SystemPromptPath(dataDir string) string {
	return filepath.Join(SystemRoot(dataDir), PromptFile)
}

// SanitizeTenantScope normalizes a tenant id for path segments; empty becomes _global.
func SanitizeTenantScope(tenantID string) string {
	if strings.TrimSpace(tenantID) == "" {
		return "_global"
	}
	s := SanitizeSlug(tenantID)
	if s == "" {
		return "_global"
	}
	return s
}

// TenantSkillsRoot is dataDir/skills/<tenantScope>/ (tenantScope is already sanitized).
func TenantSkillsRoot(dataDir, tenantScope string) string {
	return filepath.Join(SkillsRoot(dataDir), SanitizeTenantScope(tenantScope))
}

// SkillDirForTenant is the canonical directory for a skill under a tenant subtree.
func SkillDirForTenant(dataDir, tenantScope, skillSlug string) string {
	return filepath.Join(TenantSkillsRoot(dataDir, tenantScope), SanitizeSlug(skillSlug))
}

func SkillMarkdownPathForTenant(dataDir, tenantScope, skillSlug string) string {
	return filepath.Join(SkillDirForTenant(dataDir, tenantScope, skillSlug), SkillFile)
}

// SkillDir returns skills/_global/<slug>/ (legacy flat skills/<slug>/ is still indexed as _global).
func SkillDir(dataDir, skillSlug string) string {
	return SkillDirForTenant(dataDir, "_global", skillSlug)
}

func SkillMarkdownPath(dataDir, skillSlug string) string {
	return SkillMarkdownPathForTenant(dataDir, "_global", skillSlug)
}

func ToolDir(dataDir, toolSlug string) string {
	return ToolDirForTenant(dataDir, "_global", toolSlug)
}

// ToolDirForTenant is tools/<tenantScope>/<toolSlug>/ (legacy tools/<slug>/ is indexed as tenant _global).
func ToolDirForTenant(dataDir, tenantScope, toolSlug string) string {
	return filepath.Join(TenantToolsRoot(dataDir, tenantScope), SanitizeSlug(toolSlug))
}

// TenantToolsRoot is dataDir/tools/<tenantScope>/.
func TenantToolsRoot(dataDir, tenantScope string) string {
	return filepath.Join(ToolsRoot(dataDir), SanitizeTenantScope(tenantScope))
}

func ToolConfigPath(dataDir, toolSlug string) string {
	return filepath.Join(ToolDir(dataDir, toolSlug), ToolConfig)
}

func ToolConfigPathForTenant(dataDir, tenantScope, toolSlug string) string {
	return filepath.Join(ToolDirForTenant(dataDir, tenantScope, toolSlug), ToolConfig)
}

// SessionDir is memory/<tenant>/sessions/<sessionId>/
func SessionDir(dataDir, tenantID, sessionID string) string {
	t := SanitizeSlug(tenantID)
	if t == "" {
		t = "_global"
	}
	return filepath.Join(MemoryRoot(dataDir), t, "sessions", SanitizeSlug(sessionID))
}

func TurnsPath(dataDir, tenantID, sessionID string) string {
	return filepath.Join(SessionDir(dataDir, tenantID, sessionID), "turns.jsonl")
}

func SessionMetaPath(dataDir, tenantID, sessionID string) string {
	return filepath.Join(SessionDir(dataDir, tenantID, sessionID), "meta.json")
}

// SessionAgentMemoryPath stores session-scoped agent notes/summary for recall.
func SessionAgentMemoryPath(dataDir, tenantID, sessionID string) string {
	return filepath.Join(SessionDir(dataDir, tenantID, sessionID), "agent_memory.md")
}

// SanitizeSlug keeps a safe single path segment.
func SanitizeSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == '/' || r == '\\':
			b.WriteRune('-')
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "unnamed"
	}
	return out
}
