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
	return `You are a careful assistant backed by folder skills and tools under skills/ and tools/ on the agent node.

You improve over time through **human-approved proposals only**: durable changes to prompts, skills, memory structure, harness topology, or the tool registry are queued (e.g. via heros_submit_proposal from the CLI) and applied after review—never silently rewrite disk.

Between proposals, use episodic memory tools to remember and retrieve user-specific facts across turns. Load specialized skills with heros_read_skill when evolving behavior or packaging recurring workflows.`
}

// SeedIfEmpty creates default folders, seed files, and rebuilds indexes (no skill body in DB).
func SeedIfEmpty(db *sql.DB, dataDir string) error {
	if err := EnsureLayout(dataDir); err != nil {
		return err
	}
	sp := agentlayout.SystemPromptPath(dataDir)
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
	for _, sd := range defaultSkillSeeds() {
		if err := seedSkillIfMissing(dataDir, "_global", sd); err != nil {
			return err
		}
	}
	for _, td := range defaultToolSeeds() {
		if err := seedToolIfMissing(dataDir, "_global", td); err != nil {
			return err
		}
	}
	if err := skillindex.Rebuild(db, dataDir); err != nil {
		return err
	}
	return toolindex.Rebuild(db, dataDir, toolindex.DefaultSyncPolicy())
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

// --- Default seeds (Hermes-inspired: memory loop + proposal-gated self-evolution) ---

type skillDef struct {
	Slug      string
	Name      string
	Title     string
	DependsOn []string
	Tools     []string
	Body      string
}

type toolDef struct {
	Slug        string
	ID          string
	RiskTier    string
	Description string
	Skills      []string
}

func formatSkillFrontmatter(d skillDef) string {
	dep := "[]"
	if len(d.DependsOn) > 0 {
		dep = "[" + strings.Join(d.DependsOn, ", ") + "]"
	}
	tools := "[]"
	if len(d.Tools) > 0 {
		tools = "[" + strings.Join(d.Tools, ", ") + "]"
	}
	return fmt.Sprintf("---\nname: %s\ntitle: %s\ndepends_on: %s\ntools: %s\n---\n\n%s\n",
		d.Name, d.Title, dep, tools, strings.TrimSpace(d.Body))
}

func seedSkillIfMissing(dataDir, tenant string, d skillDef) error {
	p := agentlayout.SkillMarkdownPathForTenant(dataDir, tenant, d.Slug)
	if _, err := os.Stat(p); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(formatSkillFrontmatter(d)), 0o644)
}

func seedToolIfMissing(dataDir, tenant string, t toolDef) error {
	dir := agentlayout.ToolDirForTenant(dataDir, tenant, t.Slug)
	yamlPath := filepath.Join(dir, agentlayout.ToolConfig)
	if _, err := os.Stat(yamlPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "id: %s\nrisk_tier: %s\ndescription: %s\n", t.ID, t.RiskTier, t.Description)
	if len(t.Skills) > 0 {
		b.WriteString("skills:\n")
		for _, s := range t.Skills {
			_, _ = fmt.Fprintf(&b, "  - %s\n", s)
		}
	}
	return os.WriteFile(yamlPath, []byte(b.String()), 0o644)
}

func defaultSkillSeeds() []skillDef {
	return []skillDef{
		{
			Slug: "core-reasoning", Name: "core-reasoning", Title: "Core reasoning",
			DependsOn: nil, Tools: []string{"echo-safe"},
			Body: `Break problems into steps. Prefer evidence over speculation. Output structured JSON when asked by harness.

Before contradicting something the user may have said earlier, call **heros_memory_search** with a short query.

When the user wants **lasting** changes to how you behave, how the system prompt works, or new reusable procedures, you cannot edit disk yourself: use **heros_read_skill** to load **interaction-learning-loop**, **self-evolution-via-proposals**, or **agentskills-packaging**, then **heros_submit_proposal** so a human can approve.`,
		},
		{
			Slug: "interaction-learning-loop", Name: "interaction-learning-loop", Title: "Learn from interactions (memory loop)",
			DependsOn: []string{"core-reasoning"}, Tools: nil,
			Body: `Inspired by closed-loop assistants (e.g. Hermes-style): turn **repeated or high-value** user signals into **durable** state.

**During conversation**
- Save stable facts, preferences, and project constraints with **heros_memory_save** (short notes; include enough context to retrieve later).
- Before answering “what did I say”, “last time”, or “my preference”, search with **heros_memory_search**.
- If the catalog on agentd may be stale after disk edits elsewhere, the user can **slash /refresh** in heros-cli; remind them if skills or tools seem missing.

**When to escalate to evolution (not just memory)**
- The same correction or workflow appears **multiple times** → consider a **new or updated skill** (layer **prompt_engineering**).
- The user agrees a **global** behavior change belongs in **system/prompt.md** → **prompt_engineering** with a **### SYSTEM_PROMPT** block.
- You need new **structured memory/graph** ops → **context_engineering** JSON (see **self-evolution-via-proposals**).
- Multi-agent **harness** tuning → **harness_engineering**.
- A new **registered tool** on agentd → **tooling** JSON **register** object.

Never imply that memory or chat alone changed approved files; only **approved proposals** do.`,
		},
		{
			Slug: "self-evolution-via-proposals", Name: "self-evolution-via-proposals", Title: "Self-evolution via proposals",
			DependsOn: []string{"core-reasoning", "interaction-learning-loop"}, Tools: []string{"evolution-reminder"},
			Body: `Use **heros_submit_proposal** to queue changes. Every mutation is reviewed; treat the **diff** as the contract.

## Layer: prompt_engineering (skills + system prompt on disk)

Use a **single text document** with one or more blocks:

` + "```" + `
### SKILL:my-skill-slug
Markdown body only here (no frontmatter in the diff; the server adds YAML on apply).

### SYSTEM_PROMPT
Full replacement text for system/prompt.md (only if you intend to change global behavior).
` + "```" + `

At least one **### SKILL:name** or **### SYSTEM_PROMPT** section is required. Slugs should be kebab-case.

## Layer: context_engineering

**diff** must be **JSON**:

` + "```json" + `
{
  "promote": [{"session_id": "<uuid-or-session-id>", "threshold": 0.35}],
  "links": [
    {"entity_id": "user:acme", "name": "Acme", "kind": "org", "props": {}},
    {"edge_id": "e1", "src": "user:acme", "dst": "project:foo", "rel": "OWNS", "props": {}}
  ]
}
` + "```" + `

Arrays may be empty. Use **promote** to consolidate episodic session content into longer-lived memory when policy allows.

## Layer: harness_engineering

**diff** is **JSON** partial **Topology** (only non-empty fields override):

` + "```json" + `
{
  "specialists": ["researcher", "coder", "writer"],
  "critic_threshold": 0.55,
  "max_critic_retries": 2,
  "leader_model": "gpt-4o-mini"
}
` + "```" + `

## Layer: tooling

**diff** is **JSON**:

` + "```json" + `
{
  "register": {
    "name": "my-tool-id",
    "description": "What it does; linked skills for catalog graph",
    "risk_tier": "low",
    "script_path": "",
    "skills": ["core-reasoning"]
  }
}
` + "```" + `

**rationale** should name risk, rollback mindset, and who benefits. **title** is a one-line summary for the reviewer UI.`,
		},
		{
			Slug: "agentskills-packaging", Name: "agentskills-packaging", Title: "Packaging skills (agentskills-style)",
			DependsOn: []string{"core-reasoning"}, Tools: nil,
			Body: `Align new skills with small, composable **procedural memory** (similar in spirit to [agentskills.io](https://agentskills.io)):

- **One skill = one job** (e.g. “how we run migrations here”, “how we format API errors”).
- **Frontmatter** on disk: ` + "`name`" + `, ` + "`title`" + `, ` + "`depends_on`" + `, ` + "`tools`" + ` — keep ` + "`depends_on`" + ` minimal and real.
- Prefer **updating** an existing skill via **### SKILL:existing-slug** proposals over duplicating overlapping skills.
- Include **triggers**: when to apply this skill (keywords, repo areas, user roles).
- After approval, remind the user they can **reindex** if their deployment requires **POST /api/catalog/reindex**.

Proposal path for *new* on-disk skills still goes through **prompt_engineering** diffs as above, not by writing files from the chat model directly.`,
		},
	}
}

func defaultToolSeeds() []toolDef {
	return []toolDef{
		{
			Slug: "echo-safe", ID: "echo-safe", RiskTier: "low",
			Description: "Example low-risk tool placeholder; replace script_path with a real command when you add automation.",
			Skills:      []string{"core-reasoning"},
		},
		{
			Slug: "evolution-reminder", ID: "evolution-reminder", RiskTier: "low",
			Description: "Catalog hint- any durable change to skills, prompts, memory graph ops, harness, or tool registry must use heros_submit_proposal after reading skill self-evolution-via-proposals.",
			Skills:      []string{"self-evolution-via-proposals"},
		},
	}
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
