package toolindex

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/heros-foreal/agentd/internal/agentlayout"
)

// SyncRegistryFromFSEntries upserts SQLite tool_registry from disk scan results according to policy.
func SyncRegistryFromFSEntries(db *sql.DB, entries []Entry, p SyncPolicy) error {
	p = p.Normalize()
	for _, e := range entries {
		if err := syncOneRegistryRow(db, e, p); err != nil {
			return err
		}
	}
	return nil
}

func syncOneRegistryRow(db *sql.DB, e Entry, p SyncPolicy) error {
	ts := agentlayout.SanitizeTenantScope(e.TenantScope)
	tid := strings.TrimSpace(e.ToolID)
	if tid == "" {
		return nil
	}
	yDesc := strings.TrimSpace(e.Description)
	yTier := strings.TrimSpace(e.RiskTier)
	yScript := strings.TrimSpace(e.ScriptPath)

	var approved int
	var curD, curT, curS sql.NullString
	err := db.QueryRow(`SELECT approved, description, risk_tier, script_path FROM tool_registry WHERE tenant_id = ? AND name = ?`,
		ts, tid).Scan(&approved, &curD, &curT, &curS)
	if err == sql.ErrNoRows {
		desc := yDesc
		if desc == "" {
			desc = "(indexed from disk)"
		}
		tier := yTier
		if tier == "" {
			tier = "low"
		}
		_, err := db.Exec(`
INSERT INTO tool_registry (tenant_id, name, description, risk_tier, script_path, approved, proposal_id)
VALUES (?, ?, ?, ?, ?, 0, NULL)`,
			ts, tid, desc, tier, nullEmpty(yScript))
		return err
	}
	if err != nil {
		return err
	}

	if p.conflictDB() {
		return nil
	}
	if p.diskToDBApprovedOnly() && approved != 1 {
		return nil
	}

	desc := yDesc
	tier := yTier
	script := yScript
	if p.conflictYAMLNonBlank() {
		if desc == "" && curD.Valid {
			desc = curD.String
		}
		if tier == "" && curT.Valid {
			tier = curT.String
		}
		if script == "" && curS.Valid {
			script = curS.String
		}
	}
	if desc == "" {
		desc = "(indexed from disk)"
	}
	if tier == "" {
		tier = "low"
	}

	if p.conflictYAMLNonBlank() {
		_, err = db.Exec(`UPDATE tool_registry SET description = ?, risk_tier = ?, script_path = ? WHERE tenant_id = ? AND name = ?`,
			desc, tier, strOrNil(script), ts, tid)
		return err
	}
	if yScript != "" {
		_, err = db.Exec(`UPDATE tool_registry SET description = ?, risk_tier = ?, script_path = ? WHERE tenant_id = ? AND name = ?`,
			desc, tier, yScript, ts, tid)
		return err
	}
	_, err = db.Exec(`UPDATE tool_registry SET description = ?, risk_tier = ? WHERE tenant_id = ? AND name = ?`,
		desc, tier, ts, tid)
	return err
}

func strOrNil(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
}

func nullEmpty(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
}

// PersistFromRegistry writes tools/<tenant>/<id>/tool.yaml from a registry row plus optional skills list.
func PersistFromRegistry(db *sql.DB, dataDir, tenantID, toolID string, skillsFromProposal []string) error {
	ts := agentlayout.SanitizeTenantScope(tenantID)
	tid := strings.TrimSpace(toolID)
	var desc, tier, script sql.NullString
	err := db.QueryRow(`SELECT description, risk_tier, script_path FROM tool_registry WHERE tenant_id = ? AND name = ?`, ts, tid).Scan(&desc, &tier, &script)
	if err != nil {
		return err
	}
	d, rt, sp := "", "", ""
	if desc.Valid {
		d = desc.String
	}
	if tier.Valid {
		rt = tier.String
	}
	if script.Valid {
		sp = script.String
	}
	skills := skillsFromProposal
	if len(skills) == 0 {
		var skJSON sql.NullString
		_ = db.QueryRow(`SELECT skills_json FROM tool_fs_index WHERE tool_id = ? AND tenant_scope = ? LIMIT 1`, tid, ts).Scan(&skJSON)
		if skJSON.Valid && skJSON.String != "" {
			_ = json.Unmarshal([]byte(skJSON.String), &skills)
		}
	}
	return WriteToolYAML(dataDir, ts, tid, d, rt, sp, skills)
}

// PushAllRegistryToDisk writes tool_registry rows to disk and rebuilds the filesystem index.
func PushAllRegistryToDisk(db *sql.DB, dataDir string, p SyncPolicy) error {
	p = p.Normalize()
	q := `SELECT tenant_id, name FROM tool_registry`
	if p.pushApprovedOnly() {
		q = `SELECT tenant_id, name FROM tool_registry WHERE approved = 1`
	}
	rows, err := db.Query(q)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	type pair struct{ tenant, name string }
	var list []pair
	for rows.Next() {
		var ttenant, n string
		if err := rows.Scan(&ttenant, &n); err != nil {
			return err
		}
		list = append(list, pair{ttenant, n})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, row := range list {
		if err := PersistFromRegistry(db, dataDir, row.tenant, row.name, nil); err != nil {
			return err
		}
	}
	return Rebuild(db, dataDir, p)
}
