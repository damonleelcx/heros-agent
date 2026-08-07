package linkingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/heros-foreal/agentd/internal/runlink"
)

// transformreceipt.go stores and reads back the THIRD opt-in payload: what a transform the customer
// generated on their own machine actually did.
//
// # Why a new table rather than a column on an existing one
//
// The GRAIN differs from everything already here, and that is the only argument that justifies a table
// under `careful-table-creation`:
//
//	run_link          per RUN
//	workflow_ir       per (workflow, REVISION)
//	linked_transform  per (CONFIGURATION, revision)
//
// A transform is the pairing of a configuration with a revision — the same configuration applied at two
// revisions is two transforms, and two configurations at one revision are two more. Storing it on
// `workflow_ir` would key it by revision alone and make "which configuration produced this diffstat"
// unanswerable; storing it on `run_link` would key it by run, and a transform is generated without any
// run at all.
//
// It is also exactly the key `/app/transforms/{config_hash}/{source_revision}` addresses, which is the
// surface that could not resolve before this existed.

// TransformReceipt is one stored receipt, as the platform holds it.
type TransformReceipt struct {
	TenantID       string
	ConfigHash     string
	SourceRevision string
	WorkflowID     string
	ToolVersion    string
	// CoverageVersion is EMPTY when the client did not report one. Empty means NOT REPORTED and must
	// never be filled in with the platform's own table version — that would date somebody else's outcomes
	// to a table they were never computed against.
	CoverageVersion string
	Status          string
	ReceivedAt      time.Time
	NodeOutcomes    []runlink.WireNodeOutcome
	FilesChanged    int
	LinesAdded      int
	LinesRemoved    int
}

// TransformReceiptStore records and reads transform receipts.
//
// Every method returns an error, reads included, for the reason `WorkflowIRStore` does: a read failure
// and "this tenant never sent us a receipt" are different facts with different next actions, and a store
// that cannot distinguish them makes an outage look like a customer who has not opted in.
type TransformReceiptStore interface {
	// Put records a receipt, REPLACING any receipt previously stored for the same
	// (tenant, config_hash, source_revision).
	Put(r TransformReceipt) error
	// Get returns one receipt. ok=false means none was ever sent — never a read failure, which is the
	// error.
	Get(tenantID, configHash, sourceRevision string) (TransformReceipt, bool, error)
	// ListForTenant returns the tenant's receipts, newest first.
	ListForTenant(tenantID string, limit int) ([]TransformReceipt, error)
}

// MemTransformReceiptStore is the in-memory store, for tests and for a deployment with no database.
type MemTransformReceiptStore struct{ m map[string]TransformReceipt }

// NewMemTransformReceiptStore returns an empty in-memory store.
func NewMemTransformReceiptStore() *MemTransformReceiptStore {
	return &MemTransformReceiptStore{m: map[string]TransformReceipt{}}
}

func memReceiptKey(tenant, hash, rev string) string {
	return tenant + "\x00" + hash + "\x00" + rev
}

// Put upserts on the primary key. Two transmissions of the same receipt leave ONE entry.
func (s *MemTransformReceiptStore) Put(r TransformReceipt) error {
	s.m[memReceiptKey(r.TenantID, r.ConfigHash, r.SourceRevision)] = r
	return nil
}

func (s *MemTransformReceiptStore) Get(tenantID, configHash, sourceRevision string) (TransformReceipt, bool, error) {
	r, ok := s.m[memReceiptKey(tenantID, configHash, sourceRevision)]
	return r, ok, nil
}

func (s *MemTransformReceiptStore) ListForTenant(tenantID string, limit int) ([]TransformReceipt, error) {
	var out []TransformReceipt
	for _, r := range s.m {
		// 🔴 Scoped to the AUTHENTICATED tenant here as well as in the query the PG store issues. An
		// in-memory store that returned everything would make a cross-tenant leak invisible in every test
		// that uses it — which is every test.
		if r.TenantID == tenantID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ReceivedAt.Equal(out[j].ReceivedAt) {
			return out[i].ReceivedAt.After(out[j].ReceivedAt)
		}
		// A deterministic tiebreak, so two receipts stored in the same millisecond do not reorder between
		// reads and make a list look like it changed when nothing did.
		return out[i].ConfigHash < out[j].ConfigHash
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// PGTransformReceiptStore is the durable store, backed by migration 0043.
type PGTransformReceiptStore struct{ db *sql.DB }

// NewPGTransformReceiptStore returns a store over an open Postgres handle.
func NewPGTransformReceiptStore(db *sql.DB) *PGTransformReceiptStore {
	return &PGTransformReceiptStore{db: db}
}

// Put upserts on (tenant_id, config_hash, source_revision).
//
// 🔴 Replace, not append. Re-running `heros apply --link-receipt` for the same configuration at the same
// revision has not produced a second transform — the engine is a pure function of those two inputs, so a
// second row would be the same answer twice and "which one is this" would depend on insertion order.
// This is the same argument `PGWorkflowIRStore.Put` makes, and it is the reason the primary key is the
// grain rather than a surrogate id.
func (p *PGTransformReceiptStore) Put(r TransformReceipt) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	outcomes, err := json.Marshal(r.NodeOutcomes)
	if err != nil {
		return fmt.Errorf("linkingest: encode node outcomes: %w", err)
	}
	_, err = p.db.ExecContext(ctx,
		`INSERT INTO linked_transform
		   (tenant_id, config_hash, source_revision, workflow_id, tool_version, coverage_version,
		    status, received_at, node_outcomes_json, files_changed, lines_added, lines_removed)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		 ON CONFLICT (tenant_id, config_hash, source_revision) DO UPDATE
		   SET workflow_id = EXCLUDED.workflow_id,
		       tool_version = EXCLUDED.tool_version,
		       coverage_version = EXCLUDED.coverage_version,
		       status = EXCLUDED.status,
		       received_at = EXCLUDED.received_at,
		       node_outcomes_json = EXCLUDED.node_outcomes_json,
		       files_changed = EXCLUDED.files_changed,
		       lines_added = EXCLUDED.lines_added,
		       lines_removed = EXCLUDED.lines_removed`,
		r.TenantID, r.ConfigHash, r.SourceRevision, r.WorkflowID, r.ToolVersion,
		nullIfEmpty(r.CoverageVersion), r.Status, r.ReceivedAt, outcomes,
		r.FilesChanged, r.LinesAdded, r.LinesRemoved)
	if err != nil {
		return fmt.Errorf("linkingest: put transform receipt %s@%s: %w", r.ConfigHash, r.SourceRevision, err)
	}
	return nil
}

// Get returns one receipt, or ok=false when none was ever sent for that key.
func (p *PGTransformReceiptStore) Get(tenantID, configHash, sourceRevision string) (TransformReceipt, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	row := p.db.QueryRowContext(ctx,
		`SELECT tenant_id, config_hash, source_revision, workflow_id, tool_version, coverage_version,
		        status, received_at, node_outcomes_json, files_changed, lines_added, lines_removed
		   FROM linked_transform
		  WHERE tenant_id = $1 AND config_hash = $2 AND source_revision = $3`,
		tenantID, configHash, sourceRevision)
	r, err := scanReceipt(row)
	switch {
	case err == sql.ErrNoRows:
		return TransformReceipt{}, false, nil
	case err != nil:
		return TransformReceipt{}, false, fmt.Errorf("linkingest: get transform receipt: %w", err)
	}
	return r, true, nil
}

// ListForTenant returns the tenant's receipts, newest first.
func (p *PGTransformReceiptStore) ListForTenant(tenantID string, limit int) ([]TransformReceipt, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT tenant_id, config_hash, source_revision, workflow_id, tool_version, coverage_version,
		        status, received_at, node_outcomes_json, files_changed, lines_added, lines_removed
		   FROM linked_transform
		  WHERE tenant_id = $1
		  ORDER BY received_at DESC, config_hash ASC
		  LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("linkingest: list transform receipts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []TransformReceipt{}
	for rows.Next() {
		r, err := scanReceipt(rows)
		if err != nil {
			return nil, fmt.Errorf("linkingest: scan transform receipt: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("linkingest: list transform receipts: %w", err)
	}
	return out, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so one scan function serves the single read
// and the list — two copies of a column order is how a new column reaches one reader and not the other.
type rowScanner interface{ Scan(dest ...any) error }

func scanReceipt(sc rowScanner) (TransformReceipt, error) {
	var r TransformReceipt
	var outcomes []byte
	// 🔴 `coverage_version` is NULLABLE and scanned into a sql.NullString, not a string. NULL means the
	// client did not report one, and it must read back as ABSENT — a plain string scan would turn it into
	// "", which is indistinguishable from a client that reported an empty version, and downstream would
	// have no way to render `not reported`.
	var coverage sql.NullString
	err := sc.Scan(&r.TenantID, &r.ConfigHash, &r.SourceRevision, &r.WorkflowID, &r.ToolVersion,
		&coverage, &r.Status, &r.ReceivedAt, &outcomes,
		&r.FilesChanged, &r.LinesAdded, &r.LinesRemoved)
	if err != nil {
		return TransformReceipt{}, err
	}
	if coverage.Valid {
		r.CoverageVersion = coverage.String
	}
	if len(outcomes) > 0 {
		if err := json.Unmarshal(outcomes, &r.NodeOutcomes); err != nil {
			return TransformReceipt{}, fmt.Errorf("decode node outcomes: %w", err)
		}
	}
	return r, nil
}

// nullIfEmpty writes SQL NULL for an unreported value.
//
// The distinction is the whole of D4's "no backfill": a row with NULL `coverage_version` reads back as
// `not reported`, which is true of a client that predates this change. Writing "" would make it read as
// "reported, and empty" — a different and false claim.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
