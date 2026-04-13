// Package fleetworker applies vetted prompt-layer mutations from fleet events to local disk
// (skills/ and optionally system/prompt.md) without touching SQLite — pair with agentd reindex.
package fleetworker

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/heros-foreal/agentd/internal/agentlayout"
	"github.com/heros-foreal/agentd/internal/promptlayer"
)

// Options controls what parts of a prompt_engineering diff are written.
type Options struct {
	// ApplySystemPrompt writes system/prompt.md when the diff contains ### SYSTEM_PROMPT.
	ApplySystemPrompt bool
}

// ApplyPromptLayerDiff writes SKILL.md files under dataDir/skills/<tenant>/<slug>/ from a Layer-1 diff.
// It mirrors the on-disk shape used by promptlayer.ApplyMutation (frontmatter + body) but does not update SQLite.
// Returns relative paths like "skills/_global/my-skill/SKILL.md" for logging.
func ApplyPromptLayerDiff(dataDir, tenantID, diff string, opts Options) ([]string, error) {
	m, err := promptlayer.ParseMutation(diff)
	if err != nil {
		return nil, err
	}
	ts := agentlayout.SanitizeTenantScope(tenantID)
	var written []string

	if opts.ApplySystemPrompt && m.SystemPromptSet {
		if err := os.MkdirAll(agentlayout.SystemRoot(dataDir), 0o755); err != nil {
			return nil, fmt.Errorf("system dir: %w", err)
		}
		sp := agentlayout.SystemPromptPath(dataDir)
		if err := os.WriteFile(sp, []byte(m.SystemPrompt), 0o644); err != nil {
			return nil, fmt.Errorf("system prompt: %w", err)
		}
		written = append(written, filepath.ToSlash(filepath.Join(agentlayout.SystemDir, agentlayout.PromptFile)))
	}

	for name, body := range m.Skills {
		slug := agentlayout.SanitizeSlug(name)
		if slug == "" {
			continue
		}
		dir := agentlayout.SkillDirForTenant(dataDir, ts, slug)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("skill dir %s: %w", slug, err)
		}
		fm := fmt.Sprintf("---\nname: %s\ntitle: %s\ndepends_on: []\ntools: []\n---\n\n%s\n", slug, name, body)
		fp := filepath.Join(dir, agentlayout.SkillFile)
		if err := os.WriteFile(fp, []byte(fm), 0o644); err != nil {
			return nil, fmt.Errorf("skill %s: %w", slug, err)
		}
		rel := filepath.ToSlash(filepath.Join(agentlayout.SkillsDir, ts, slug, agentlayout.SkillFile))
		written = append(written, rel)
	}

	if len(written) == 0 {
		return nil, fmt.Errorf("no skill or system content to apply")
	}
	return written, nil
}
