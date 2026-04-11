package skillindex

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/heros-foreal/agentd/internal/agentlayout"
)

// Entry is one indexed skill on disk (authoritative content lives in the file).
type Entry struct {
	RelPath      string   `json:"rel_path"`
	TenantScope  string   `json:"tenant_scope"`
	Name         string   `json:"name"`
	Title        string   `json:"title"`
	DependsOn    []string `json:"depends_on"`
	Tools        []string `json:"tools"`
	SHA256       string   `json:"sha256"`
	MtimeUnix    int64    `json:"mtime_unix"`
	Size         int64    `json:"size"`
}

// ParseTenantScopeFromRel extracts tenant from index rel_path (skills/.../SKILL.md).
// Legacy skills/<name>/SKILL.md → _global. skills/<tenant>/<slug>/SKILL.md → tenant (e.g. _global, acme).
func ParseTenantScopeFromRel(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) < 3 || parts[0] != agentlayout.SkillsDir || parts[len(parts)-1] != agentlayout.SkillFile {
		return "_global"
	}
	if len(parts) == 3 {
		return "_global"
	}
	return agentlayout.SanitizeTenantScope(parts[1])
}

// ScopedID is a stable id for graphs (tenant + logical name from frontmatter).
func ScopedID(tenantScope, logicalName string) string {
	return agentlayout.SanitizeTenantScope(tenantScope) + "/" + strings.TrimSpace(logicalName)
}

// ResolveDependsTarget finds a neighbor skill id for depends_on edges (tenant first, then _global).
func ResolveDependsTarget(all []Entry, fromTenant, depName string) string {
	depName = strings.TrimSpace(depName)
	if depName == "" {
		return ""
	}
	ids := map[string]struct{}{}
	for _, x := range all {
		ids[ScopedID(x.TenantScope, x.Name)] = struct{}{}
	}
	for _, cand := range []string{ScopedID(fromTenant, depName), ScopedID("_global", depName)} {
		if _, ok := ids[cand]; ok {
			return cand
		}
	}
	return ""
}

// Scan walks dataDir/skills/**/SKILL.md and returns entries (no DB write).
func Scan(dataDir string) ([]Entry, error) {
	root := agentlayout.SkillsRoot(dataDir)
	var out []Entry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) != agentlayout.SkillFile {
			return nil
		}
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		fm, _, err := ParseSkillMarkdown(string(b))
		if err != nil {
			return err
		}
		name := fm.Name
		if name == "" {
			name = filepath.Base(filepath.Dir(path))
		}
		title := fm.Title
		if title == "" {
			title = name
		}
		h := sha256.Sum256(b)
		out = append(out, Entry{
			RelPath:     rel,
			TenantScope: ParseTenantScopeFromRel(rel),
			Name:        name,
			Title:       title,
			DependsOn:   fm.DependsOn,
			Tools:       fm.Tools,
			SHA256:      hex.EncodeToString(h[:]),
			MtimeUnix:   fi.ModTime().Unix(),
			Size:        fi.Size(),
		})
		return nil
	})
	if err != nil && os.IsNotExist(err) {
		return nil, nil
	}
	return out, err
}

// Rebuild replaces skill_fs_index from a fresh scan.
func Rebuild(db *sql.DB, dataDir string) error {
	entries, err := Scan(dataDir)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM skill_fs_index`); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, e := range entries {
		deps, _ := json.Marshal(e.DependsOn)
		tools, _ := json.Marshal(e.Tools)
		_, err = tx.Exec(`
INSERT INTO skill_fs_index (rel_path, tenant_scope, name, title, depends_json, tools_json, sha256, size, mtime_unix, indexed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
			e.RelPath, e.TenantScope, e.Name, e.Title, string(deps), string(tools), e.SHA256, e.Size, e.MtimeUnix)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// List returns all indexed skills ordered by tenant and name.
func List(db *sql.DB) ([]Entry, error) {
	rows, err := db.Query(`SELECT rel_path, tenant_scope, name, title, depends_json, tools_json, sha256, size, mtime_unix FROM skill_fs_index ORDER BY tenant_scope, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

// ListByTenant returns skills for one tenant scope only.
func ListByTenant(db *sql.DB, tenantScope string) ([]Entry, error) {
	ts := agentlayout.SanitizeTenantScope(tenantScope)
	rows, err := db.Query(`SELECT rel_path, tenant_scope, name, title, depends_json, tools_json, sha256, size, mtime_unix FROM skill_fs_index WHERE tenant_scope = ? ORDER BY name`, ts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

// ListForTenantAndGlobal returns skills visible to a tenant (their subtree + _global).
func ListForTenantAndGlobal(db *sql.DB, tenantID string) ([]Entry, error) {
	ts := agentlayout.SanitizeTenantScope(tenantID)
	rows, err := db.Query(`SELECT rel_path, tenant_scope, name, title, depends_json, tools_json, sha256, size, mtime_unix FROM skill_fs_index WHERE tenant_scope IN (?, '_global') ORDER BY tenant_scope, name`, ts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

func scanRows(rows *sql.Rows) ([]Entry, error) {
	var out []Entry
	for rows.Next() {
		var e Entry
		var deps, tools string
		if err := rows.Scan(&e.RelPath, &e.TenantScope, &e.Name, &e.Title, &deps, &tools, &e.SHA256, &e.Size, &e.MtimeUnix); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(deps), &e.DependsOn)
		_ = json.Unmarshal([]byte(tools), &e.Tools)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ResolveForTenant picks a skill by logical name: tenant match first, then _global.
func ResolveForTenant(db *sql.DB, tenantID, name string) (*Entry, error) {
	ts := agentlayout.SanitizeTenantScope(tenantID)
	try := func(scope string) (*Entry, error) {
		row := db.QueryRow(`SELECT rel_path, tenant_scope, name, title, depends_json, tools_json, sha256, size, mtime_unix FROM skill_fs_index WHERE name = ? AND tenant_scope = ?`, name, scope)
		return scanOne(row)
	}
	if e, err := try(ts); err == nil {
		return e, nil
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	if ts != "_global" {
		if e, err := try("_global"); err == nil {
			return e, nil
		} else if err != sql.ErrNoRows {
			return nil, err
		}
	}
	return nil, sql.ErrNoRows
}

func scanOne(row *sql.Row) (*Entry, error) {
	var e Entry
	var deps, tools string
	if err := row.Scan(&e.RelPath, &e.TenantScope, &e.Name, &e.Title, &deps, &tools, &e.SHA256, &e.Size, &e.MtimeUnix); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(deps), &e.DependsOn)
	_ = json.Unmarshal([]byte(tools), &e.Tools)
	return &e, nil
}

// ByName returns a skill from the _global subtree only (legacy helper).
func ByName(db *sql.DB, name string) (*Entry, error) {
	return ResolveForTenant(db, "_global", name)
}

// ReadBody loads markdown body from disk using index rel_path.
func ReadBody(dataDir, relPath string) (string, error) {
	full := filepath.Join(dataDir, filepath.FromSlash(relPath))
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	_, body, err := ParseSkillMarkdown(string(b))
	return body, err
}

// UpsertOne updates index for a single skill file (after proposal apply).
func UpsertOne(db *sql.DB, dataDir, relPath string) error {
	full := filepath.Join(dataDir, filepath.FromSlash(relPath))
	b, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	fi, err := os.Stat(full)
	if err != nil {
		return err
	}
	fm, _, err := ParseSkillMarkdown(string(b))
	if err != nil {
		return err
	}
	name := fm.Name
	if name == "" {
		name = filepath.Base(filepath.Dir(full))
	}
	title := fm.Title
	if title == "" {
		title = name
	}
	ts := ParseTenantScopeFromRel(relPath)
	h := sha256.Sum256(b)
	deps, _ := json.Marshal(fm.DependsOn)
	tools, _ := json.Marshal(fm.Tools)
	_, err = db.Exec(`
INSERT INTO skill_fs_index (rel_path, tenant_scope, name, title, depends_json, tools_json, sha256, size, mtime_unix, indexed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
ON CONFLICT(rel_path) DO UPDATE SET
  tenant_scope=excluded.tenant_scope, name=excluded.name, title=excluded.title, depends_json=excluded.depends_json, tools_json=excluded.tools_json,
  sha256=excluded.sha256, size=excluded.size, mtime_unix=excluded.mtime_unix, indexed_at=excluded.indexed_at`,
		relPath, ts, name, title, string(deps), string(tools), hex.EncodeToString(h[:]), fi.Size(), fi.ModTime().Unix())
	return err
}

// GraphJSON builds nodes/edges for /api/skills/graph from the index.
func GraphJSON(db *sql.DB) ([]byte, error) {
	entries, err := List(db)
	if err != nil {
		return nil, err
	}
	type node struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		TenantScope string   `json:"tenant_scope"`
		Title       string   `json:"title"`
		RelPath     string   `json:"rel_path"`
		Tools       []string `json:"tools"`
	}
	type edge struct {
		From string `json:"from"`
		To   string `json:"to"`
		Kind string `json:"kind"`
	}
	var nodes []node
	for _, e := range entries {
		nodes = append(nodes, node{
			ID:          ScopedID(e.TenantScope, e.Name),
			Name:        e.Name,
			TenantScope: e.TenantScope,
			Title:       e.Title,
			RelPath:     e.RelPath,
			Tools:       e.Tools,
		})
	}
	var edges []edge
	for _, e := range entries {
		sid := ScopedID(e.TenantScope, e.Name)
		for _, d := range e.DependsOn {
			if to := ResolveDependsTarget(entries, e.TenantScope, d); to != "" {
				edges = append(edges, edge{From: sid, To: to, Kind: "depends_on"})
			}
		}
		for _, t := range e.Tools {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			edges = append(edges, edge{From: sid, To: t, Kind: "uses_tool"})
		}
	}
	out := map[string]any{
		"source": "filesystem_index",
		"nodes":  nodes,
		"edges":  edges,
	}
	return json.Marshal(out)
}
