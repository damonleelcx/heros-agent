package variantspec

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// Store persists Variant Specs and the lineage they hash to (task 2.1).
//
// Dialect: PostgreSQL only ($N placeholders, ON CONFLICT). Backed by 0001's lineage tables and
// 0003's variant_spec.
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// ErrSpecNotFound is returned when no spec is stored for a (config_hash, source_revision).
var ErrSpecNotFound = errors.New("variantspec: no spec stored for this config_hash and source_revision")

// EnsureWorkflow records the lineage root a spec hangs off: 0001's `workflow`.
//
// It lives here because this Store is already the writer of the lineage chain — Put inserts `variant`
// and `config`, both FK-bound to `workflow` — and a caller that had to write the root itself would be
// the second place in the codebase that knows how lineage is shaped.
//
// wf comes from a discovery run, never from a caller's guess. Repo URL, commit and language are
// facts about the scanned tree, and discovery is the only thing that has looked at it.
//
// # Why DO NOTHING and not DO UPDATE
//
// `workflow.commit_sha` is the revision the workflow was FIRST discovered at, not the revision of the
// submission in hand — those differ the moment anyone submits a spec against a second commit, which is
// the normal case, not the edge case. Every row that actually needs the revision (variant_spec,
// transform, run) carries its own source_revision, so nothing downstream reads this column to decide
// anything. Updating it on each submit would rewrite the history of a shared row to match whoever
// submitted last, and buy nothing.
func (s *Store) EnsureWorkflow(ctx context.Context, wf discovery.IRWorkflow, irVersion string) error {
	if wf.ID == "" {
		return fmt.Errorf("variantspec: EnsureWorkflow requires a workflow_id")
	}
	// 0001 declares repo_url, commit_sha, language and ir_version NOT NULL. A tree with no git remote
	// discovers an empty repo URL, which is a legitimate state (the e2e fixture is exactly that) and
	// must not become a constraint violation four steps into a submission.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		 VALUES ($1, $2, $3, $4, $5) ON CONFLICT (workflow_id) DO NOTHING`,
		wf.ID, wf.Repo.URL, wf.Repo.CommitSHA, wf.Language, irVersion); err != nil {
		return fmt.Errorf("variantspec: ensure workflow %s: %w", wf.ID, err)
	}
	// Prove it, as every other write in this package does: a DO NOTHING that swallowed a constraint
	// failure is indistinguishable from one that found the row already there, and the difference is
	// whether the FK the next statement depends on exists.
	var got string
	err := s.db.QueryRowContext(ctx,
		`SELECT workflow_id FROM workflow WHERE workflow_id = $1`, wf.ID).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("variantspec: ensure workflow %s: the insert affected no rows and no row "+
			"exists (likely a silently-swallowed constraint failure)", wf.ID)
	}
	if err != nil {
		return fmt.Errorf("variantspec: ensure workflow: read-back: %w", err)
	}
	return nil
}

// Put persists a resolved spec: the lineage row 0001 models plus the authored spec 0003 holds.
//
// Idempotent by construction. Everything here is content-addressed or content-derived, so
// re-submitting the same spec is a no-op rather than a conflict — which is what lets P4 fan out over
// variants without first asking "have I seen this one?". As in internal/registry, a no-op insert is
// never taken on trust: Put reads back and proves the stored lineage is byte-for-byte the lineage it
// meant to store, so a swallowed constraint failure cannot present as a successful write.
//
// variantID is the caller's stable logical label (PRD §8.4: a variant_id is a label a human edits
// over time; one variant_id maps to many config_hash values across its edit history).
func (s *Store) Put(ctx context.Context, r *Resolved, spec *VariantSpec, variantID, label string) error {
	if r == nil || spec == nil {
		return fmt.Errorf("variantspec: Put requires a resolved spec")
	}
	if variantID == "" {
		return fmt.Errorf("variantspec: Put requires a variant_id (the stable label this config is one version of)")
	}

	lineage, err := r.Config.Canonical()
	if err != nil {
		return err
	}
	specJSON, err := jsonMarshal(spec)
	if err != nil {
		return fmt.Errorf("variantspec: marshal spec: %w", err)
	}

	// One transaction: a variant_spec row whose lineage never landed would claim a config_hash that
	// resolves to nothing, which is precisely the dangling state the FK exists to prevent.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("variantspec: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO variant (variant_id, workflow_id, label) VALUES ($1, $2, $3)
		 ON CONFLICT (variant_id) DO NOTHING`,
		variantID, spec.WorkflowID, label); err != nil {
		return fmt.Errorf("variantspec: upsert variant %s: %w", variantID, err)
	}

	// The lineage row. config_hash is its primary key, so a second insert of the same configuration
	// is the no-op P0 designed for ("a second insert of the same immutable configuration is rejected").
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
		 VALUES ($1, $2, $3, $4, $5) ON CONFLICT (config_hash) DO NOTHING`,
		r.ConfigHash, variantID, spec.WorkflowID, r.Config.IRVersion, string(lineage)); err != nil {
		return fmt.Errorf("variantspec: insert config %s: %w", Display(r.ConfigHash), err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO variant_spec (config_hash, source_revision, spec_json) VALUES ($1, $2, $3)
		 ON CONFLICT (config_hash, source_revision) DO NOTHING`,
		r.ConfigHash, r.SourceRevision, string(specJSON)); err != nil {
		return fmt.Errorf("variantspec: insert variant_spec %s@%s: %w",
			Display(r.ConfigHash), r.SourceRevision, err)
	}

	// Prove the write. lineage_json is JSONB, which re-serializes (its key order is length-then-byte,
	// not RFC 8785's), so the stored TEXT is deliberately not compared to `lineage` — what must hold
	// is the property that actually matters: the stored lineage still re-canonicalizes to the
	// config_hash it is filed under. If Postgres ever altered a number on the way in, replay-from-
	// lineage would be broken and every result keyed by this hash would be unreproducible. Checking
	// it here, on every write, is cheaper than discovering it from a P4 run months later.
	stored, err := readLineage(ctx, tx, r.ConfigHash)
	if err != nil {
		return err
	}
	if !bytes.Equal(stored, lineage) {
		return fmt.Errorf("variantspec: config %s was already stored with different lineage:\n stored: %s\n ours:   %s",
			Display(r.ConfigHash), stored, lineage)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("variantspec: commit: %w", err)
	}
	return nil
}

// Get returns the authored spec and its lineage for a (config_hash, source_revision).
func (s *Store) Get(ctx context.Context, configHash, sourceRevision string) (*VariantSpec, ResolvedConfig, error) {
	var specJSON []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT spec_json::text FROM variant_spec WHERE config_hash = $1 AND source_revision = $2`,
		configHash, sourceRevision).Scan(&specJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ResolvedConfig{}, fmt.Errorf("%w: %s@%s", ErrSpecNotFound, Display(configHash), sourceRevision)
	}
	if err != nil {
		return nil, ResolvedConfig{}, fmt.Errorf("variantspec: get spec: %w", err)
	}

	var spec VariantSpec
	if err := jsonUnmarshal(specJSON, &spec); err != nil {
		return nil, ResolvedConfig{}, fmt.Errorf("variantspec: decode stored spec: %w", err)
	}

	lineage, err := readLineage(ctx, s.db, configHash)
	if err != nil {
		return nil, ResolvedConfig{}, err
	}
	var rc ResolvedConfig
	if err := jsonUnmarshal(lineage, &rc); err != nil {
		return nil, ResolvedConfig{}, fmt.Errorf("variantspec: decode stored lineage: %w", err)
	}
	// Verify on read, as internal/registry does: a lineage that no longer hashes to the config_hash
	// it is filed under is a corrupt configuration, and replaying it would silently run something
	// other than what the hash promises.
	got, err := rc.Hash()
	if err != nil {
		return nil, ResolvedConfig{}, err
	}
	if got != configHash {
		return nil, ResolvedConfig{}, fmt.Errorf(
			"variantspec: lineage for %s re-hashes to %s; the stored configuration is not the one this hash names",
			Display(configHash), Display(got))
	}
	return &spec, rc, nil
}

// querier is the subset of *sql.DB / *sql.Tx readLineage needs, so it works inside Put's transaction
// (where the row is not yet committed) and outside it.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// readLineage returns the stored resolved_config, re-canonicalized.
//
// The re-canonicalization is what makes JSONB storage safe. JSONB does not preserve byte order, so
// the text Postgres returns is not the canonical form — but canonicalization sorts keys anyway, and
// JSONB preserves number tokens that are already in shortest form (which every stored lineage is,
// because confighash refuses to hash any other kind). Sorting again on the way out therefore
// reconstructs the exact bytes the hash was taken over.
func readLineage(ctx context.Context, q querier, configHash string) ([]byte, error) {
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT lineage_json::text FROM config WHERE config_hash = $1`, configHash).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: no lineage for config %s", ErrSpecNotFound, Display(configHash))
	}
	if err != nil {
		return nil, fmt.Errorf("variantspec: read lineage %s: %w", Display(configHash), err)
	}
	canon, err := canonicalizeBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("variantspec: re-canonicalize stored lineage %s: %w", Display(configHash), err)
	}
	return canon, nil
}
