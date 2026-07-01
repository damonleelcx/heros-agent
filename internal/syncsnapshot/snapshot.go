package syncsnapshot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type Snapshot struct {
	Proposals      []map[string]any `json:"proposals"`
	InboxMessages  []map[string]any `json:"inbox_messages"`
	UserProfiles   []map[string]any `json:"user_profiles"`
	EpisodicMemory []map[string]any `json:"episodic_memory"`
	SkillIndex     []map[string]any `json:"skill_index"`
	ToolIndex      []map[string]any `json:"tool_index"`
	Version        int              `json:"version"`
}

func Export(db *sql.DB) (Snapshot, error) {
	s := Snapshot{Version: 1}
	if err := scanJSON(db, &s.Proposals, `SELECT json_object('id',id,'tenant_id',tenant_id,'layer',layer,'title',title,'rationale',rationale,'diff_text',diff_text,'status',status,'created_at',created_at,'reviewed_at',reviewed_at,'content_hash',content_hash,'rollback_ref',rollback_ref) FROM proposals ORDER BY created_at ASC`); err != nil {
		return s, err
	}
	if err := scanJSON(db, &s.InboxMessages, `SELECT json_object('message_id',message_id,'tenant_id',tenant_id,'payload_type',payload_type,'payload_version',payload_version,'signature',signature,'payload_json',payload_json,'state',state,'retry_count',retry_count,'dead_letter_reason',dead_letter_reason,'created_at',created_at,'expire_at',expire_at) FROM inbox_messages ORDER BY created_at ASC`); err != nil {
		return s, err
	}
	if err := scanJSON(db, &s.UserProfiles, `SELECT json_object('tenant_id',tenant_id,'user_id',user_id,'profile_json',profile_json,'updated_at',updated_at) FROM user_profiles ORDER BY updated_at ASC`); err != nil {
		return s, err
	}
	if err := scanJSON(db, &s.EpisodicMemory, `SELECT json_object('id',id,'session_id',session_id,'role',role,'content',content,'importance',importance,'created_at',created_at,'tenant_id',tenant_id,'memory_session_rel',memory_session_rel) FROM episodic_memory ORDER BY created_at ASC`); err != nil {
		return s, err
	}
	if err := scanJSON(db, &s.SkillIndex, `SELECT json_object('rel_path',rel_path,'name',name,'title',title,'tenant_scope',tenant_scope,'depends_json',depends_json,'tools_json',tools_json,'sha256',sha256,'size',size,'mtime_unix',mtime_unix,'indexed_at',indexed_at) FROM skill_fs_index ORDER BY rel_path ASC`); err != nil {
		return s, err
	}
	if err := scanJSON(db, &s.ToolIndex, `SELECT json_object('rel_path',rel_path,'tool_id',tool_id,'tenant_scope',tenant_scope,'risk_tier',risk_tier,'skills_json',skills_json,'description',description,'script_path',script_path,'sha256',sha256,'size',size,'mtime_unix',mtime_unix,'indexed_at',indexed_at) FROM tool_fs_index ORDER BY rel_path ASC`); err != nil {
		return s, err
	}
	return s, nil
}

func scanJSON(db *sql.DB, out *[]map[string]any, query string) error {
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return err
		}
		*out = append(*out, m)
	}
	return rows.Err()
}

type ImportPolicy struct {
	Conflict string
}

type ImportReport struct {
	Conflicts []string `json:"conflicts,omitempty"`
	Applied   int      `json:"applied"`
}

func Restore(db *sql.DB, snap Snapshot) (ImportReport, error) {
	return Import(db, snap, ImportPolicy{Conflict: "restore"})
}

func Import(db *sql.DB, snap Snapshot, pol ImportPolicy) (ImportReport, error) {
	var rep ImportReport
	if snap.Version <= 0 {
		return rep, fmt.Errorf("snapshot version missing")
	}
	_, err := db.Exec(`DELETE FROM inbox_messages`)
	if err != nil {
		return rep, err
	}
	if n, conflicts, err := upsertJSONRows(db, "proposals", snap.Proposals, []string{"id"}, pol); err != nil {
		return rep, err
	} else {
		rep.Applied += n
		rep.Conflicts = append(rep.Conflicts, conflicts...)
	}
	if n, conflicts, err := upsertJSONRows(db, "inbox_messages", snap.InboxMessages, []string{"message_id"}, pol); err != nil {
		return rep, err
	} else {
		rep.Applied += n
		rep.Conflicts = append(rep.Conflicts, conflicts...)
	}
	if n, conflicts, err := upsertJSONRows(db, "user_profiles", snap.UserProfiles, []string{"tenant_id", "user_id"}, pol); err != nil {
		return rep, err
	} else {
		rep.Applied += n
		rep.Conflicts = append(rep.Conflicts, conflicts...)
	}
	if n, conflicts, err := upsertJSONRows(db, "episodic_memory", snap.EpisodicMemory, []string{"id"}, pol); err != nil {
		return rep, err
	} else {
		rep.Applied += n
		rep.Conflicts = append(rep.Conflicts, conflicts...)
	}
	if n, conflicts, err := upsertJSONRows(db, "skill_fs_index", snap.SkillIndex, []string{"rel_path"}, pol); err != nil {
		return rep, err
	} else {
		rep.Applied += n
		rep.Conflicts = append(rep.Conflicts, conflicts...)
	}
	if n, conflicts, err := upsertJSONRows(db, "tool_fs_index", snap.ToolIndex, []string{"rel_path"}, pol); err != nil {
		return rep, err
	} else {
		rep.Applied += n
		rep.Conflicts = append(rep.Conflicts, conflicts...)
	}
	_, err = db.Exec(`UPDATE inbox_messages SET state = CASE
		WHEN state IN ('received','verified','applied','acked','retry','dead-letter') THEN state
		ELSE 'received' END`)
	return rep, err
}

func upsertJSONRows(db *sql.DB, table string, rows []map[string]any, keys []string, pol ImportPolicy) (int, []string, error) {
	if len(rows) == 0 {
		return 0, nil, nil
	}
	applied := 0
	var conflicts []string
	conflictMode := strings.ToLower(strings.TrimSpace(pol.Conflict))
	for _, row := range rows {
		cols := make([]string, 0, len(row))
		vals := make([]any, 0, len(row))
		ph := make([]string, 0, len(row))
		upd := make([]string, 0, len(row))
		for k, v := range row {
			cols = append(cols, k)
			vals = append(vals, v)
			ph = append(ph, "?")
			skip := false
			for _, key := range keys {
				if key == k {
					skip = true
					break
				}
			}
			if !skip {
				upd = append(upd, fmt.Sprintf("%s=excluded.%s", k, k))
			}
		}
		if conflictMode == "error" {
			return applied, conflicts, fmt.Errorf("conflict policy error for %s", table)
		}
		sqlStmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT(%s) DO %s",
			table, strings.Join(cols, ","), strings.Join(ph, ","), strings.Join(keys, ","), conflictClause(conflictMode, upd))
		if _, err := db.Exec(sqlStmt, vals...); err != nil {
			return applied, conflicts, err
		}
		applied++
		if len(keys) > 0 {
			conflicts = append(conflicts, fmt.Sprintf("%s:%s:%s", table, strings.Join(keys, ","), conflictMode))
		}
	}
	return applied, conflicts, nil
}

func conflictClause(mode string, upd []string) string {
	switch mode {
	case "local_wins":
		return "NOTHING"
	default:
		if len(upd) == 0 {
			return "NOTHING"
		}
		return "UPDATE SET " + strings.Join(upd, ",")
	}
}
