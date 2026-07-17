package worktree

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Store persists transform records (task 3.9, transform half).
//
// Dialect: PostgreSQL only. Backed by 0004's `transform` table.
type Store struct {
	db *sql.DB
	// blobs stores the generated diff under its content hash. The DB holds only the reference
	// (PRD §7): a diff is the user's source code, and inlining it into a row that gets logged,
	// replicated, and dumped is how proprietary logic ends up somewhere nobody intended.
	blobs BlobStore
}

// BlobStore is the object store for generated diffs. Structurally identical to
// internal/registry.BlobStore and satisfied by the same *registry.FSBlobStore — declared here rather
// than imported so this package does not depend on the registry to write a patch.
type BlobStore interface {
	Put(ctx context.Context, data []byte) (contentHash string, err error)
	Get(ctx context.Context, contentHash string) ([]byte, error)
}

func NewStore(db *sql.DB, blobs BlobStore) *Store { return &Store{db: db, blobs: blobs} }

// ErrTransformNotFound is returned when no transform is recorded for a (config_hash, source_revision).
var ErrTransformNotFound = errors.New("worktree: no transform recorded for this config_hash and source_revision")

// Put records a terminal transform — built or build-rejected.
//
// Both are recorded, deliberately. A build-rejected transform is not a failure to be swallowed: it is
// a terminal state the UI renders and P4 must not re-attempt, and PRD §6 FR18 lists `build-rejected`
// alongside succeeded/failed/halted as a status a run is queryable by.
//
// Idempotent: the transform is a pure function of (config_hash, source_revision), so a second Put of
// the same pair is a no-op rather than a conflict. As elsewhere, a no-op insert is not taken on
// trust — the read-back proves what is actually stored.
func (s *Store) Put(ctx context.Context, applied *Applied, rej *BuildRejection) error {
	if applied == nil {
		return fmt.Errorf("worktree: Put requires an applied transform")
	}
	// A transform whose gate did not say what it proved is not recordable (ADR-003 decision 2). The
	// column is NOT NULL with no default, so Postgres would refuse this anyway — the check is here so
	// the caller gets a sentence naming the actual problem instead of a constraint name, and so the
	// blob below is not written for a row that will never exist.
	//
	// The alternative — defaulting an absent strength to `type-checked` — is the single most dangerous
	// line that could be written in this package: it would let a caller that never ran a type checker
	// present a diff as though a compiler had stood behind it. Refusing is the only safe direction.
	if !applied.Strength.Valid() {
		return fmt.Errorf("worktree: refusing to record transform %s@%s: the verification strength is "+
			"%q, which is not one of %q or %q; a transform must state what its gate PROVED (ADR-003)",
			applied.ConfigHash, applied.SourceRevision, applied.Strength,
			StrengthTypeChecked, StrengthSyntaxChecked)
	}

	// The diff goes to the object store first: a row citing a blob that was never written would
	// resolve and then fail to render, which is worse than no row.
	var diffHash any
	if len(applied.Diff) > 0 {
		h, err := s.blobs.Put(ctx, applied.Diff)
		if err != nil {
			return fmt.Errorf("worktree: store diff for %s: %w", applied.ConfigHash, err)
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO blob (content_hash, size_bytes, media_type) VALUES ($1, $2, 'text/x-diff')
			 ON CONFLICT (content_hash) DO NOTHING`, h, len(applied.Diff)); err != nil {
			return fmt.Errorf("worktree: catalog diff %s: %w", h, err)
		}
		diffHash = h
	}

	var nodeID, dim any
	if rej != nil {
		if rej.NodeID != "" {
			nodeID = rej.NodeID
		}
		if rej.Dim != "" {
			dim = rej.Dim
		}
	}
	// 0004's transform_rejection_has_a_reason CHECK requires a non-empty log on a rejection. Supplying
	// a placeholder keeps a rejection whose builder printed nothing (a killed process, an OOM) from
	// being unrecordable — the row still has to exist, because "we tried and it did not build" is the
	// fact the user needs.
	log := applied.BuildLog
	if applied.Status == StatusBuildRejected && log == "" {
		log = "(the build produced no output)"
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO transform (config_hash, source_revision, diff_blob_hash, build_status,
		                        verification_strength, worktree_ref, variant_branch, variant_commit,
		                        build_log, rejected_node_id, rejected_dim)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		 ON CONFLICT (config_hash, source_revision) DO NOTHING`,
		applied.ConfigHash, applied.SourceRevision, diffHash, string(applied.Status),
		string(applied.Strength), applied.Dir, applied.Branch, applied.Commit, log, nodeID, dim)
	if err != nil {
		return fmt.Errorf("worktree: record transform %s@%s: %w", applied.ConfigHash, applied.SourceRevision, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("worktree: record transform: rows affected: %w", err)
	}
	if n == 1 {
		return nil
	}

	// n == 0: an identical pair is already recorded. Prove it, rather than accept a swallowed
	// constraint failure as a successful write.
	var status, strength string
	err = s.db.QueryRowContext(ctx,
		`SELECT build_status, verification_strength FROM transform
		 WHERE config_hash = $1 AND source_revision = $2`,
		applied.ConfigHash, applied.SourceRevision).Scan(&status, &strength)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("worktree: record transform %s@%s: insert affected 0 rows and no row exists "+
			"(likely a silently-swallowed constraint failure)", applied.ConfigHash, applied.SourceRevision)
	case err != nil:
		return fmt.Errorf("worktree: record transform: read-back: %w", err)
	}
	if status != string(applied.Status) {
		// The same pair reaching two different verdicts means the transform was not a pure function of
		// it — a drifting toolchain, or a worktree that was not at source_revision. Either way, silently
		// keeping the first verdict would let P4 score against a build nobody can reproduce.
		return fmt.Errorf("worktree: transform %s@%s is already recorded as %q but this run produced %q; "+
			"the same (config_hash, source_revision) must build the same way",
			applied.ConfigHash, applied.SourceRevision, status, applied.Status)
	}
	if strength != string(applied.Strength) {
		// Same pair, same verdict, DIFFERENT strength — and this is what a half-provisioned fleet looks
		// like. One worker has pyright and records `type-checked`; the next does not, takes the
		// py_compile path, and records `syntax-checked`. Both say `built`, so the check above sees
		// nothing wrong, and whether a customer's diff can be auto-applied silently starts depending on
		// which worker happened to pick it up. The row is immutable, so the first answer would win
		// permanently and invisibly. It is caught here instead.
		return fmt.Errorf("worktree: transform %s@%s is recorded as %q but this run proved %q; the same "+
			"(config_hash, source_revision) must be verified by the same gate — this usually means two "+
			"workers do not have the same toolchain installed",
			applied.ConfigHash, applied.SourceRevision, strength, applied.Strength)
	}
	return nil
}

// Record is a stored transform.
type Record struct {
	ConfigHash     string
	SourceRevision string
	DiffHash       string
	Status         BuildStatus
	// Strength is what the gate PROVED (ADR-003). Ask it, not the language, whether a change may be
	// applied without a human: Strength.AllowsAutonomousApply.
	Strength       Strength
	WorktreeRef    string
	Branch         string
	Commit         string
	BuildLog       string
	RejectedNodeID string
	RejectedDim    string
}

// Get returns the transform recorded for a (config_hash, source_revision), and its diff bytes.
func (s *Store) Get(ctx context.Context, configHash, sourceRevision string) (*Record, []byte, error) {
	var r Record
	var diffHash, worktree, branch, commit, nodeID, dim sql.NullString
	var status, strength string
	err := s.db.QueryRowContext(ctx,
		`SELECT diff_blob_hash, build_status, verification_strength, worktree_ref, variant_branch,
		        variant_commit, build_log, rejected_node_id, rejected_dim
		 FROM transform WHERE config_hash = $1 AND source_revision = $2`,
		configHash, sourceRevision).Scan(&diffHash, &status, &strength, &worktree, &branch, &commit,
		&r.BuildLog, &nodeID, &dim)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("%w: %s@%s", ErrTransformNotFound, configHash, sourceRevision)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("worktree: get transform: %w", err)
	}
	r.ConfigHash, r.SourceRevision, r.Status = configHash, sourceRevision, BuildStatus(status)
	r.Strength = Strength(strength)
	r.DiffHash, r.WorktreeRef, r.Branch, r.Commit = diffHash.String, worktree.String, branch.String, commit.String
	r.RejectedNodeID, r.RejectedDim = nodeID.String, dim.String

	if !diffHash.Valid {
		return &r, nil, nil // a baseline with no edits has no diff
	}
	diff, err := s.blobs.Get(ctx, diffHash.String)
	if err != nil {
		return nil, nil, fmt.Errorf("worktree: read diff %s: %w", diffHash.String, err)
	}
	return &r, diff, nil
}
