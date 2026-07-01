package db

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	dsn := "file:" + strings.ReplaceAll(path, "\\", "/") + "?mode=rwc"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := d.Exec(`PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		_ = d.Close()
		return nil, err
	}
	if err := migrate(d); err != nil {
		_ = d.Close()
		return nil, err
	}
	if err := ensureTenantColumns(d); err != nil {
		_ = d.Close()
		return nil, err
	}
	if err := ensureToolRegistryV2(d); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

func EnsureInboxState(d *sql.DB) error {
	_, err := d.Exec(`UPDATE inbox_messages SET state = CASE
		WHEN state IN ('received','verified','applied','acked','retry','dead-letter') THEN state
		ELSE 'received' END`)
	return err
}

func ensureTenantColumns(d *sql.DB) error {
	alters := []string{
		`ALTER TABLE proposals ADD COLUMN tenant_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE episodic_memory ADD COLUMN tenant_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE semantic_chunks ADD COLUMN tenant_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE episodic_memory ADD COLUMN memory_session_rel TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE skill_fs_index ADD COLUMN tenant_scope TEXT NOT NULL DEFAULT '_global'`,
		`ALTER TABLE tool_fs_index ADD COLUMN script_path TEXT`,
		`ALTER TABLE tool_fs_index ADD COLUMN tenant_scope TEXT NOT NULL DEFAULT '_global'`,
	}
	for _, s := range alters {
		if _, err := d.Exec(s); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
				return fmt.Errorf("migrate alter: %w", err)
			}
		}
	}
	_, _ = d.Exec(`CREATE INDEX IF NOT EXISTS idx_skill_fs_tenant ON skill_fs_index(tenant_scope)`)
	_, _ = d.Exec(`CREATE INDEX IF NOT EXISTS idx_tool_fs_tenant ON tool_fs_index(tenant_scope)`)
	return nil
}

// ensureToolRegistryV2 migrates legacy PRIMARY KEY(name) to PRIMARY KEY(tenant_id, name).
func ensureToolRegistryV2(d *sql.DB) error {
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='tool_registry'`).Scan(&n); err != nil || n == 0 {
		return err
	}
	rows, err := d.Query(`PRAGMA table_info(tool_registry)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var hasTenant bool
	pkCols := 0
	for rows.Next() {
		var cid int
		var cname, ctype string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if cname == "tenant_id" {
			hasTenant = true
		}
		if pk != 0 {
			pkCols++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasTenant && pkCols >= 2 {
		return nil
	}
	if _, err := d.Exec(`CREATE TABLE tool_registry__v2 (
		tenant_id TEXT NOT NULL DEFAULT '_global',
		name TEXT NOT NULL,
		description TEXT NOT NULL,
		risk_tier TEXT NOT NULL,
		script_path TEXT,
		approved INTEGER NOT NULL DEFAULT 0,
		proposal_id TEXT,
		PRIMARY KEY (tenant_id, name)
	)`); err != nil {
		return fmt.Errorf("tool_registry v2 create: %w", err)
	}
	if _, err := d.Exec(`INSERT INTO tool_registry__v2 (tenant_id, name, description, risk_tier, script_path, approved, proposal_id)
		SELECT '_global', name, description, risk_tier, script_path, approved, proposal_id FROM tool_registry`); err != nil {
		_, _ = d.Exec(`DROP TABLE tool_registry__v2`)
		return fmt.Errorf("tool_registry v2 copy: %w", err)
	}
	if _, err := d.Exec(`DROP TABLE tool_registry`); err != nil {
		return fmt.Errorf("tool_registry drop old: %w", err)
	}
	if _, err := d.Exec(`ALTER TABLE tool_registry__v2 RENAME TO tool_registry`); err != nil {
		return fmt.Errorf("tool_registry rename: %w", err)
	}
	return nil
}

func migrate(d *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (id INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`CREATE TABLE IF NOT EXISTS proposals (
			id TEXT PRIMARY KEY,
			layer TEXT NOT NULL,
			title TEXT NOT NULL,
			rationale TEXT NOT NULL,
			diff_text TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			reviewed_at TEXT,
			content_hash TEXT,
			rollback_ref TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS skill_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			version INTEGER NOT NULL,
			body TEXT NOT NULL,
			committed_at TEXT NOT NULL DEFAULT (datetime('now')),
			proposal_id TEXT,
			UNIQUE(name, version)
		)`,
		`CREATE TABLE IF NOT EXISTS system_prompt_versions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			version INTEGER NOT NULL UNIQUE,
			body TEXT NOT NULL,
			committed_at TEXT NOT NULL DEFAULT (datetime('now')),
			proposal_id TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS episodic_memory (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			importance REAL NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_episodic_session ON episodic_memory(session_id)`,
		`CREATE TABLE IF NOT EXISTS semantic_chunks (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			text TEXT NOT NULL,
			embedding_json TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS user_profiles (
			tenant_id TEXT NOT NULL DEFAULT '',
			user_id TEXT NOT NULL DEFAULT 'default',
			profile_json TEXT NOT NULL DEFAULT '{}',
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (tenant_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS graph_entities (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			props_json TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS graph_edges (
			id TEXT PRIMARY KEY,
			src_id TEXT NOT NULL REFERENCES graph_entities(id) ON DELETE CASCADE,
			dst_id TEXT NOT NULL REFERENCES graph_entities(id) ON DELETE CASCADE,
			rel TEXT NOT NULL,
			props_json TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS harness_config (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			topology_json TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS tool_registry (
			tenant_id TEXT NOT NULL DEFAULT '_global',
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			risk_tier TEXT NOT NULL,
			script_path TEXT,
			approved INTEGER NOT NULL DEFAULT 0,
			proposal_id TEXT,
			PRIMARY KEY (tenant_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS cli_audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cmd TEXT NOT NULL,
			risk_tier TEXT NOT NULL,
			outcome TEXT NOT NULL,
			detail TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS scheduled_jobs (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			interval_sec INTEGER NOT NULL,
			action TEXT NOT NULL,
			payload_json TEXT,
			enabled INTEGER NOT NULL DEFAULT 1,
			last_fired_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sched_tenant ON scheduled_jobs(tenant_id)`,
		`CREATE TABLE IF NOT EXISTS inbox_messages (
			message_id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT '',
			payload_type TEXT NOT NULL,
			payload_version INTEGER NOT NULL DEFAULT 1,
			signature TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'received',
			retry_count INTEGER NOT NULL DEFAULT 0,
			dead_letter_reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			expire_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_inbox_tenant_state ON inbox_messages(tenant_id, state)`,
		`CREATE TABLE IF NOT EXISTS sync_ledger (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			direction TEXT NOT NULL,
			source TEXT NOT NULL,
			version INTEGER NOT NULL DEFAULT 1,
			applied INTEGER NOT NULL DEFAULT 0,
			conflict_count INTEGER NOT NULL DEFAULT 0,
			conflicts_json TEXT,
			snapshot_json TEXT,
			snapshot_sha256 TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS skill_fs_index (
			rel_path TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			title TEXT NOT NULL,
			depends_json TEXT,
			tools_json TEXT,
			sha256 TEXT,
			size INTEGER,
			mtime_unix INTEGER,
			indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
			tenant_scope TEXT NOT NULL DEFAULT '_global'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_skill_fs_name ON skill_fs_index(name)`,
		`CREATE TABLE IF NOT EXISTS tool_fs_index (
			rel_path TEXT PRIMARY KEY,
			tool_id TEXT NOT NULL,
			risk_tier TEXT,
			skills_json TEXT,
			description TEXT,
			sha256 TEXT,
			size INTEGER,
			mtime_unix INTEGER,
			indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
			script_path TEXT,
			tenant_scope TEXT NOT NULL DEFAULT '_global'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_fs_id ON tool_fs_index(tool_id)`,
		`CREATE TABLE IF NOT EXISTS vault_index_state (
			vault_root TEXT NOT NULL,
			rel_path TEXT NOT NULL,
			tenant_id TEXT NOT NULL DEFAULT '',
			file_key TEXT NOT NULL,
			content_sha256 TEXT NOT NULL,
			mtime_unix INTEGER NOT NULL,
			chunk_count INTEGER NOT NULL,
			indexed_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (vault_root, rel_path, tenant_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_vault_state_tenant ON vault_index_state(tenant_id)`,
	}
	for i, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			return fmt.Errorf("migration %d: %w", i, err)
		}
	}
	return nil
}
