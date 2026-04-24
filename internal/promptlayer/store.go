package promptlayer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/heros-foreal/agentd/internal/agentlayout"
	"github.com/heros-foreal/agentd/internal/skillindex"
	"github.com/heros-foreal/agentd/internal/toolindex"
)

// ParsedMutation is a human-readable diff: optional SKILL blocks and/or SYSTEM_PROMPT.
type ParsedMutation struct {
	Skills          map[string]string // name -> body (markdown only; frontmatter added on write)
	SystemPrompt    string
	SystemPromptSet bool
}

var skillHeader = regexp.MustCompile(`(?i)^###\s*SKILL:\s*(\S+)\s*$`)
var sysHeader = regexp.MustCompile(`(?i)^###\s*SYSTEM_PROMPT\s*$`)

// ParseMutation parses document-style diffs (Layer 1).
func ParseMutation(diff string) (*ParsedMutation, error) {
	lines := strings.Split(diff, "\n")
	m := &ParsedMutation{Skills: map[string]string{}}
	var mode string
	var skillName string
	var buf strings.Builder
	flushSkill := func() {
		if mode == "skill" && skillName != "" {
			m.Skills[strings.TrimSpace(skillName)] = strings.TrimSpace(buf.String())
			buf.Reset()
		}
	}
	for _, line := range lines {
		if sm := skillHeader.FindStringSubmatch(line); sm != nil {
			flushSkill()
			if mode == "system" {
				m.SystemPrompt = strings.TrimSpace(buf.String())
				buf.Reset()
				m.SystemPromptSet = true
			}
			mode = "skill"
			skillName = sm[1]
			buf.Reset()
			continue
		}
		if sysHeader.MatchString(line) {
			flushSkill()
			buf.Reset()
			mode = "system"
			m.SystemPromptSet = true
			continue
		}
		if mode == "skill" {
			if buf.Len() > 0 {
				buf.WriteByte('\n')
			}
			buf.WriteString(line)
		} else if mode == "system" {
			if buf.Len() > 0 {
				buf.WriteByte('\n')
			}
			buf.WriteString(line)
		}
	}
	flushSkill()
	if m.SystemPromptSet && mode == "system" {
		m.SystemPrompt = strings.TrimSpace(buf.String())
	}
	if len(m.Skills) == 0 && !m.SystemPromptSet {
		return nil, fmt.Errorf("no ### SKILL:name or ### SYSTEM_PROMPT section found in diff")
	}
	return m, nil
}

// EnsureLayout creates skills/, tools/, memory/, system/ under dataDir.
func EnsureLayout(dataDir string) error {
	for _, d := range []string{
		agentlayout.SkillsRoot(dataDir),
		agentlayout.ToolsRoot(dataDir),
		agentlayout.MemoryRoot(dataDir),
		agentlayout.SystemRoot(dataDir),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// LoadActiveSystemPrompt prefers system/prompt.md on disk; falls back to SQLite audit trail.
func LoadActiveSystemPrompt(db *sql.DB, dataDir string) (string, int, error) {
	p := agentlayout.SystemPromptPath(dataDir)
	if b, err := os.ReadFile(p); err == nil {
		return strings.TrimSpace(string(b)), 0, nil
	}
	var body string
	var ver int
	err := db.QueryRow(`SELECT body, version FROM system_prompt_versions ORDER BY version DESC LIMIT 1`).Scan(&body, &ver)
	if err == sql.ErrNoRows {
		return defaultSystemPrompt(), 0, nil
	}
	return body, ver, err
}

// LoadSkillMarkdown returns skill body from indexed filesystem (authoritative).
// Resolution order: tenant-specific skill, then _global.
func LoadSkillMarkdown(dataDir string, db *sql.DB, tenantID, name string) (string, error) {
	e, err := skillindex.ResolveForTenant(db, tenantID, name)
	if err != nil {
		return "", err
	}
	return skillindex.ReadBody(dataDir, e.RelPath)
}

func defaultSystemPrompt() string {
	return `You are an **agent** on the Heros node: you **use tools** (shell, memory, skills catalog) — not a chatbot that only asks the user for paragraphs of context.

**Workspace:** When the CLI gives a workdir, you **must** ground repo/product questions with **heros_shell** (list/read files) before claiming you lack information.

**Skills & memory:** Call **heros_read_skill** when a catalog skill matches the task. Use **heros_memory_search** on multi-step work to recall prior decisions.

**You spot your own gaps:** Initiate **heros_submit_proposal** with a concrete diff; humans approve/deny. Vetted changes can sync via **collective** when configured.

Never silently write durable files; proposals → approval → apply.`
}

// SeedIfEmpty creates default folders, seed files, and rebuilds indexes (no skill body in DB).
func SeedIfEmpty(db *sql.DB, dataDir string) error {
	if err := EnsureLayout(dataDir); err != nil {
		return err
	}
	sp := agentlayout.SystemPromptPath(dataDir)
	if err := seedEmbeddedDefaults(dataDir); err != nil {
		return err
	}
	if err := migrateLegacyCustomSkillsPath(dataDir); err != nil {
		return err
	}
	if _, err := os.Stat(sp); os.IsNotExist(err) {
		if err := os.WriteFile(sp, []byte(defaultSystemPrompt()), 0o644); err != nil {
			return err
		}
	}

	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM system_prompt_versions`).Scan(&n)
	if n == 0 {
		body, _ := os.ReadFile(sp)
		if _, err := db.Exec(`INSERT INTO system_prompt_versions (version, body) VALUES (1, ?)`, string(body)); err != nil {
			return err
		}
	}
	if err := skillindex.Rebuild(db, dataDir); err != nil {
		return err
	}
	return toolindex.Rebuild(db, dataDir, toolindex.DefaultSyncPolicy())
}

// migrateLegacyCustomSkillsPath moves skills/_global/custom/* -> skills/_global/*
// during startup so a plain restart reflects the new layout without manual API steps.
func migrateLegacyCustomSkillsPath(dataDir string) error {
	src := filepath.Join(dataDir, "skills", "_global", "custom")
	st, err := os.Stat(src)
	if err != nil || !st.IsDir() {
		return nil
	}
	dst := filepath.Join(dataDir, "skills", "_global")
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := strings.TrimSpace(e.Name())
		if name == "" {
			continue
		}
		from := filepath.Join(src, name)
		to := filepath.Join(dst, name)
		if _, err := os.Stat(to); err == nil {
			// Already migrated or replaced; keep destination.
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	_ = os.Remove(src)
	return nil
}

// ApplyMutation writes approved changes to disk and updates skill index rows (not full skill blobs in DB).
// tenantScope is the skills/ subtree (e.g. _global or a tenant slug).
func ApplyMutation(db *sql.DB, dataDir string, tenantScope string, proposalID string, diff string) (rollbackRef string, err error) {
	m, err := ParseMutation(diff)
	if err != nil {
		return "", err
	}
	var roll []string
	if m.SystemPromptSet {
		if err := os.MkdirAll(agentlayout.SystemRoot(dataDir), 0o755); err != nil {
			return "", err
		}
		sp := agentlayout.SystemPromptPath(dataDir)
		var prev []byte
		if prev, err = os.ReadFile(sp); err == nil {
			roll = append(roll, "system_prompt:previous_file")
		} else {
			prev = []byte(defaultSystemPrompt())
		}
		if err := os.WriteFile(sp, []byte(m.SystemPrompt), 0o644); err != nil {
			return "", err
		}
		_ = prev
		var prevVer int
		var prevBody string
		_ = db.QueryRow(`SELECT version, body FROM system_prompt_versions ORDER BY version DESC LIMIT 1`).Scan(&prevVer, &prevBody)
		nv := prevVer + 1
		if prevVer == 0 {
			nv = 1
		}
		if _, err := db.Exec(`INSERT INTO system_prompt_versions (version, body, proposal_id) VALUES (?, ?, ?)`, nv, m.SystemPrompt, proposalID); err != nil {
			return "", err
		}
	}
	ts := agentlayout.SanitizeTenantScope(tenantScope)
	for name, body := range m.Skills {
		slug := agentlayout.SanitizeSlug(name)
		dir := agentlayout.SkillDirForTenant(dataDir, ts, slug)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		fm := fmt.Sprintf("---\nname: %s\ntitle: %s\ndepends_on: []\ntools: []\n---\n\n%s\n", slug, name, body)
		fp := filepath.Join(dir, agentlayout.SkillFile)
		if err := os.WriteFile(fp, []byte(fm), 0o644); err != nil {
			return "", err
		}
		rel := filepath.ToSlash(filepath.Join(agentlayout.SkillsDir, ts, slug, agentlayout.SkillFile))
		if err := skillindex.UpsertOne(db, dataDir, rel); err != nil {
			return "", err
		}
		roll = append(roll, "skill:"+rel)
	}
	b, _ := json.Marshal(roll)
	return string(b), nil
}

// FullCatalogGraphJSON merges skill + tool index graphs (filesystem-backed).
func FullCatalogGraphJSON(db *sql.DB) ([]byte, error) {
	sk, err := skillindex.GraphJSON(db)
	if err != nil {
		return nil, err
	}
	tl, err := toolindex.GraphJSON(db)
	if err != nil {
		return nil, err
	}
	var skM, tlM map[string]any
	_ = json.Unmarshal(sk, &skM)
	_ = json.Unmarshal(tl, &tlM)
	out := map[string]any{
		"skills": skM,
		"tools":  tlM,
	}
	return json.Marshal(out)
}
