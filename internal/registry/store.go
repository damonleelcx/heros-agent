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

	// strategies are the memory-strategy implementations RegisterMemory will accept (P17). Seeded with
	// the closed builtin set; a future memory-runtime phase adds to it via AddMemoryStrategy, which is
	// the same seam AddPolicy is. Kept in its OWN map rather than sharing `policies`: a memory strategy
	// and a context policy are different dimensions (decisions.md D2), and one map keyed by name would
	// let `full` and a same-named strategy shadow each other.
	strategies map[string]MemoryStrategy

	// harnesses are the harness-strategy implementations RegisterHarness will accept (P18). Seeded with
	// the closed builtin set; AddHarnessStrategy is the same seam AddPolicy and AddMemoryStrategy are.
	// Its OWN map, for the reason `strategies` is its own: a harness strategy and a memory strategy are
	// different dimensions, and one map keyed by name would let a same-named entry in either shadow the
	// other — silently binding a scaffold where a memory was asked for.
	harnesses map[string]HarnessStrategy

	// loops are the loop-strategy implementations RegisterLoop will accept (P34). Its OWN map, for the
	// reason `harnesses` is its own: after ADR-014's split a harness entry names an EXECUTION ENVELOPE
	// and a loop entry names an ITERATION POLICY, and one map keyed by name would let a same-named entry
	// in either shadow the other — silently binding an envelope where an iteration policy was asked for,
	// which is precisely the confusion the Kind is hashed into the version_id to prevent.
	loops map[string]LoopStrategy
}

// NewStore returns a Store over an open Postgres handle and a blob store for prompt bodies.
// The `full` context policy is registered; see AddPolicy for P3's. The five builtin memory strategies
// are registered; see AddMemoryStrategy.
func NewStore(db *sql.DB, blobs BlobStore) *Store {
	s := &Store{db: db, blobs: blobs, policies: map[string]Policy{},
		strategies: map[string]MemoryStrategy{}, harnesses: map[string]HarnessStrategy{},
		loops: map[string]LoopStrategy{}}
	for _, p := range BuiltinPolicies() {
		s.policies[p.Name()] = p
	}
	for _, st := range BuiltinMemoryStrategies() {
		s.strategies[st.Name()] = st
	}
	for _, st := range BuiltinHarnessStrategies() {
		s.harnesses[st.Name()] = st
	}
	for _, st := range BuiltinLoopStrategies() {
		s.loops[st.Name()] = st
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

// AllModelVersions lists every published model version, oldest first.
//
// # Why this exists, when ModelVersions already lists by name
//
// P30's publisher validates a definition's `model_ref` against the registry BEFORE recording the
// definition, and it can only do that against the whole set: a ref is a version id, and the caller
// asking "is this ref registered" does not know which entry NAME it belongs to — that is exactly what
// it is trying to find out.
//
// 🔴 It returns the same key space `ResolveModel` reads, and that matters more than it looks. The
// publisher's check and the runtime's resolution must agree about what a `model_ref` IS; if publish
// validated against the operator price registry (keyed by model id, e.g. `claude-sonnet-4-5`) while
// the runner resolved against this one (keyed by content-addressed version id), publishing would
// accept references the runner could never resolve, and the failure would arrive at the first
// analysis rather than at the moment somebody pressed publish.
func (s *Store) AllModelVersions(ctx context.Context) ([]*ModelEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT version_id, envelope FROM %s ORDER BY created_at, version_id`, tableModel))
	if err != nil {
		return nil, fmt.Errorf("registry: all model versions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []*ModelEntry
	for rows.Next() {
		var versionID string
		var env []byte
		if err := rows.Scan(&versionID, &env); err != nil {
			return nil, fmt.Errorf("registry: all model versions: scan: %w", err)
		}
		var spec ModelSpec
		name, derr := decodeEnvelope(KindModel, versionID, env, &spec)
		if derr != nil {
			// 🚫 NOT skipped. A row this store cannot decode is a registry that is not what the process
			// thinks it is, and a caller validating a reference against a silently shortened list would
			// reject a model that IS registered.
			return nil, fmt.Errorf("registry: all model versions: %s: %w", versionID, derr)
		}
		out = append(out, &ModelEntry{VersionID: versionID, Name: name, Spec: spec, Envelope: env})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("registry: all model versions: %w", err)
	}
	return out, nil
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
