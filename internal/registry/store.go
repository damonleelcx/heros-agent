package registry

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Store is the Postgres-backed registry. It holds no cache: registry rows are immutable, so a cache
// is safe to add later (PRD §7 sizes ref resolution at <100 ms for a 200-node spec precisely because
// immutable rows are cacheable), but P2 does not need one and an empty cache cannot go stale.
//
// Dialect: PostgreSQL only. The SQL here uses $N placeholders and ON CONFLICT, and the invariants it
// leans on are enforced by db/migrations/postgres/0002_p2_registries.up.sql. This is not a
// cross-dialect repository, and the SQLite dev ledger in internal/db is a different store entirely.
type Store struct {
	db    *sql.DB
	blobs BlobStore

	// policies are the context-policy implementations RegisterContextPolicy will accept. Seeded with
	// the builtins (P2 ships only `full`); P3 adds its policies via AddPolicy.
	policies map[string]Policy
}

// NewStore returns a Store over an open Postgres handle and a blob store for prompt bodies.
// The `full` context policy is registered; see AddPolicy for P3's.
func NewStore(db *sql.DB, blobs BlobStore) *Store {
	s := &Store{db: db, blobs: blobs, policies: map[string]Policy{}}
	for _, p := range BuiltinPolicies() {
		s.policies[p.Name()] = p
	}
	return s
}

// AddPolicy makes a context-policy implementation available to RegisterContextPolicy. This is the
// seam P3 plugs sliding-window / summarization / RAG into; P2 deliberately ships only `full` (PRD §3
// non-goals) but the shape is already policy-generic, so a P3 policy is a new implementation here
// and a new row — not a schema change.
func (s *Store) AddPolicy(p Policy) { s.policies[p.Name()] = p }

// publish inserts a sealed entry, or confirms an identical one is already published.
//
// ON CONFLICT DO NOTHING makes re-registering identical content a no-op rather than an error, which
// is what lets a caller register-then-reference without a "does it exist yet?" dance. It is safe here
// precisely BECAUSE version_id is the content address: a conflict on the primary key means the same
// bytes, not a competing definition.
//
// But "0 rows affected" is not evidence of that, and treating it as evidence is how a swallowed
// constraint failure turns into a green write over an empty table. So a no-op insert is always
// followed by a read-back that proves the stored bytes are byte-for-byte the ones we meant to
// publish. A row that is absent, or present with different content, is an error — never a shrug.
func (s *Store) publish(ctx context.Context, table, versionID, name string, env []byte, extraCol string, extraVal any) error {
	var (
		q    string
		args []any
	)
	if extraCol == "" {
		q = fmt.Sprintf(
			`INSERT INTO %s (version_id, name, envelope) VALUES ($1, $2, $3)
			 ON CONFLICT (version_id) DO NOTHING`, table)
		args = []any{versionID, name, env}
	} else {
		q = fmt.Sprintf(
			`INSERT INTO %s (version_id, name, envelope, %s) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (version_id) DO NOTHING`, table, extraCol)
		args = []any{versionID, name, env, extraVal}
	}

	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("registry: publish %s %s: %w", table, versionID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("registry: publish %s %s: rows affected: %w", table, versionID, err)
	}
	if n == 1 {
		return nil
	}

	// n == 0: claimed to be an already-published identical entry. Prove it.
	var stored []byte
	err = s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT envelope FROM %s WHERE version_id = $1`, table), versionID,
	).Scan(&stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// The insert affected no rows AND no row exists: the write was rejected somewhere that did
		// not surface as an error. Fail loud rather than report a publish that did not happen.
		return fmt.Errorf("registry: publish %s %s: insert affected 0 rows and no row exists "+
			"(likely a silently-swallowed constraint failure)", table, versionID)
	case err != nil:
		return fmt.Errorf("registry: publish %s %s: read-back: %w", table, versionID, err)
	}
	if !bytes.Equal(stored, env) {
		// Only reachable via a SHA-256 collision or a bypassing writer. Either way the published
		// content is not ours, and silently accepting it would change a generated diff under a
		// pinned ref.
		return fmt.Errorf("%w: %s %s already published with different content", ErrCorruptEntry, table, versionID)
	}
	return nil
}

// resolve reads the stored envelope for a version_id.
//
// It returns the bytes as stored — resolution never re-marshals a decoded struct — which is what
// keeps a Variant Spec pinning an old version resolving after the spec type grows a field (FR10).
func (s *Store) resolve(ctx context.Context, table, versionID string) (env []byte, err error) {
	err = s.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT envelope FROM %s WHERE version_id = $1`, table), versionID,
	).Scan(&env)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s %s", ErrNotFound, table, versionID)
	}
	if err != nil {
		return nil, fmt.Errorf("registry: resolve %s %s: %w", table, versionID, err)
	}
	return env, nil
}

// versions lists every published version_id for a name, oldest first. Served by the
// (name, version_id) unique constraint from 0002. This is the "what has this entry looked like over
// time?" read path — the git-like history the registries promise (PRD §8).
func (s *Store) versions(ctx context.Context, table, name string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT version_id FROM %s WHERE name = $1 ORDER BY created_at, version_id`, table), name)
	if err != nil {
		return nil, fmt.Errorf("registry: versions %s %s: %w", table, name, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("registry: versions %s %s: scan: %w", table, name, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("registry: versions %s %s: %w", table, name, err)
	}
	return out, nil
}

// ModelVersions lists every published version of a model entry name, oldest first.
func (s *Store) ModelVersions(ctx context.Context, name string) ([]string, error) {
	return s.versions(ctx, tableModel, name)
}

// PromptVersions lists every published version of a prompt entry name, oldest first.
func (s *Store) PromptVersions(ctx context.Context, name string) ([]string, error) {
	return s.versions(ctx, tablePrompt, name)
}

// SkillVersions lists every published version of a skill entry name, oldest first.
func (s *Store) SkillVersions(ctx context.Context, name string) ([]string, error) {
	return s.versions(ctx, tableSkill, name)
}

// ContextVersions lists every published version of a context entry name, oldest first.
func (s *Store) ContextVersions(ctx context.Context, name string) ([]string, error) {
	return s.versions(ctx, tableContext, name)
}
