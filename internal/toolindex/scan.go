package toolindex

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
	"github.com/heros-foreal/agentd/internal/skillindex"
	"gopkg.in/yaml.v3"
)

// ToolSpec is the authoritative tool.yaml on disk.
type ToolSpec struct {
	ID          string   `yaml:"id"`
	RiskTier    string   `yaml:"risk_tier"`
	Skills      []string `yaml:"skills"`
	Description string   `yaml:"description"`
	ScriptPath  string   `yaml:"script_path"`
}

// Entry is one indexed tool directory.
type Entry struct {
	RelPath      string   `json:"rel_path"`
	TenantScope  string   `json:"tenant_scope"`
	ToolID       string   `json:"tool_id"`
	RiskTier     string   `json:"risk_tier"`
	Skills       []string `json:"skills"`
	Description  string   `json:"description"`
	ScriptPath   string   `json:"script_path,omitempty"`
	SHA256       string   `json:"sha256"`
	MtimeUnix    int64    `json:"mtime_unix"`
	Size         int64    `json:"size"`
}

func Scan(dataDir string) ([]Entry, error) {
	root := agentlayout.ToolsRoot(dataDir)
	var out []Entry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Base(path) != agentlayout.ToolConfig {
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
		var spec ToolSpec
		if err := yaml.Unmarshal(b, &spec); err != nil {
			return err
		}
		id := spec.ID
		if id == "" {
			id = filepath.Base(filepath.Dir(path))
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		ts := ParseTenantScopeFromToolRel(rel)
		out = append(out, Entry{
			RelPath:     rel,
			TenantScope: ts,
			ToolID:      id,
			RiskTier:    spec.RiskTier,
			Skills:      spec.Skills,
			Description: spec.Description,
			ScriptPath:  spec.ScriptPath,
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

// Rebuild replaces tool_fs_index and syncs tool_registry using policy.
func Rebuild(db *sql.DB, dataDir string, p SyncPolicy) error {
	entries, err := Scan(dataDir)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tool_fs_index`); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, e := range entries {
		sk, _ := json.Marshal(e.Skills)
		_, err = tx.Exec(`
INSERT INTO tool_fs_index (rel_path, tool_id, risk_tier, skills_json, description, script_path, tenant_scope, sha256, size, mtime_unix, indexed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
			e.RelPath, e.ToolID, e.RiskTier, string(sk), e.Description, e.ScriptPath, e.TenantScope, e.SHA256, e.Size, e.MtimeUnix)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return SyncRegistryFromFSEntries(db, entries, p)
}

func List(db *sql.DB) ([]Entry, error) {
	rows, err := db.Query(`SELECT rel_path, tool_id, risk_tier, skills_json, description, script_path, tenant_scope, sha256, size, mtime_unix FROM tool_fs_index ORDER BY tenant_scope, tool_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

// ListByTenant returns tools under one tenant scope.
func ListByTenant(db *sql.DB, tenantScope string) ([]Entry, error) {
	ts := agentlayout.SanitizeTenantScope(tenantScope)
	rows, err := db.Query(`SELECT rel_path, tool_id, risk_tier, skills_json, description, script_path, tenant_scope, sha256, size, mtime_unix FROM tool_fs_index WHERE tenant_scope = ? ORDER BY tool_id`, ts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

// ListForTenantAndGlobal returns tools for a principal (tenant + _global).
func ListForTenantAndGlobal(db *sql.DB, tenantID string) ([]Entry, error) {
	ts := agentlayout.SanitizeTenantScope(tenantID)
	rows, err := db.Query(`SELECT rel_path, tool_id, risk_tier, skills_json, description, script_path, tenant_scope, sha256, size, mtime_unix FROM tool_fs_index WHERE tenant_scope IN (?, '_global') ORDER BY tenant_scope, tool_id`, ts)
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
		var sk string
		var sp sql.NullString
		if err := rows.Scan(&e.RelPath, &e.ToolID, &e.RiskTier, &sk, &e.Description, &sp, &e.TenantScope, &e.SHA256, &e.Size, &e.MtimeUnix); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(sk), &e.Skills)
		if sp.Valid {
			e.ScriptPath = sp.String
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GraphJSON adds tool nodes and skill<->tool edges for catalog APIs.
func GraphJSON(db *sql.DB) ([]byte, error) {
	entries, err := List(db)
	if err != nil {
		return nil, err
	}
	skills, err := skillindex.List(db)
	if err != nil {
		return nil, err
	}
	type node struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		TenantScope string   `json:"tenant_scope"`
		Kind        string   `json:"kind"`
		RelPath     string   `json:"rel_path"`
		RiskTier    string   `json:"risk_tier,omitempty"`
		Skills      []string `json:"skills,omitempty"`
		Description string   `json:"description,omitempty"`
		ScriptPath  string   `json:"script_path,omitempty"`
	}
	type edge struct {
		From string `json:"from"`
		To   string `json:"to"`
		Kind string `json:"kind"`
	}
	var nodes []node
	var edges []edge
	for _, e := range entries {
		tid := ScopedToolID(e.TenantScope, e.ToolID)
		nodes = append(nodes, node{
			ID:          tid,
			Name:        e.ToolID,
			TenantScope: e.TenantScope,
			Kind:        "tool",
			RelPath:     e.RelPath,
			RiskTier:    e.RiskTier,
			Skills:      e.Skills,
			Description: e.Description,
			ScriptPath:  e.ScriptPath,
		})
		for _, sk := range e.Skills {
			sk = strings.TrimSpace(sk)
			if sk == "" {
				continue
			}
			from := skillindex.ResolveDependsTarget(skills, e.TenantScope, sk)
			if from == "" {
				continue
			}
			edges = append(edges, edge{From: from, To: tid, Kind: "skill_uses_tool"})
		}
	}
	out := map[string]any{"source": "tool_fs_index", "nodes": nodes, "edges": edges}
	return json.Marshal(out)
}
