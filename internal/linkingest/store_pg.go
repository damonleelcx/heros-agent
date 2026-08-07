package linkingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/runlink"
)

// store_pg.go is the durable Store — the one that lets run linking actually mount on a deployment.
//
// # Why this had to exist before the capability could ship
//
// `MemStore` was the only implementation, so mounting P11 would have accepted a developer's linked run,
// answered 200, and lost it on the next pod restart. `internal/launch` therefore registered the surface
// with a nil source and answered 503 not-mounted, which is worse-sounding and better-behaved: "this
// capability is not installed here" is actionable, "installed and quietly lossy" is discovered weeks
// later by someone whose coverage number was never right.
//
// # Every failure is RETURNED, and that is why Store's signatures changed
//
// This type is the reason the interface carries errors on its reads. Written against a map, four of
// Store's methods returned none — and the first durable implementation had nowhere to report a failed
// read to. The workaround it shipped with (record the error on the store, publish it through an `Err()`
// the readiness probe polled) made outages visible but left every CALLER still receiving a plausible,
// wrong answer: an empty run list, an unknown denominator, a run reported as simply not linked.
//
// Widening the interface removed the need for any of that. A failure here is a failure at the call
// site, the /readyz component that used to paper over it is gone, and the `postgres` probe already
// reports the database itself.

// PGStore is the PostgreSQL Store, backed by 0020_p11_run_links.
type PGStore struct {
	db *sql.DB
}

// NewPGStore returns a Store over an open Postgres handle.
func NewPGStore(db *sql.DB) *PGStore { return &PGStore{db: db} }

// Record marks a run linked, idempotently.
//
// `already` comes from whether the INSERT actually inserted, not from a prior SELECT: a check-then-act
// pair loses the race between two CI jobs linking the same run, and both would report a first link.
// ON CONFLICT DO NOTHING makes the database the arbiter, and the primary key makes it impossible to
// double-count even if this code is wrong.
func (p *PGStore) Record(lr LinkedRun) (bool, error) {
	scores, err := json.Marshal(lr.Scores)
	if err != nil {
		return false, fmt.Errorf("linkingest: encode scores for run %s: %w", lr.RunID, err)
	}
	if len(lr.Scores) == 0 {
		scores = []byte("[]")
	}
	gateFailures, err := json.Marshal(lr.Eval.GateFailures)
	if err != nil {
		return false, fmt.Errorf("linkingest: encode gate failures for run %s: %w", lr.RunID, err)
	}
	if len(lr.Eval.GateFailures) == 0 {
		gateFailures = []byte("[]")
	}
	perNode, err := json.Marshal(lr.PerNode)
	if err != nil {
		return false, fmt.Errorf("linkingest: encode per-node metrics for run %s: %w", lr.RunID, err)
	}
	if len(lr.PerNode) == 0 {
		perNode = []byte("{}")
	}
	// NULL, not zero, when the evidence is absent. A run linked by a CLI that predates it has no case
	// count and no verdict — writing 0 and \'not-configured\' would make "we were never told" and "there
	// were no cases, no gate was set" the same row, and only one of those is a fact about the run.
	var caseCount, seedCount, gateOutcome any
	if lr.EvalEvidencePresent() {
		caseCount = lr.Eval.CaseCount
		seedCount = lr.Eval.SeedCount
		gateOutcome = string(lr.Eval.GateOutcome)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := p.db.ExecContext(ctx,
		`INSERT INTO run_link
		   (tenant_id, run_id, workflow_id, config_hash, source_revision, tool_version, linked_at, scores_json,
		    eval_case_count, eval_seed_count, eval_gate_outcome, eval_gate_failures, eval_single_seed, per_node_json)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		 ON CONFLICT (tenant_id, run_id) DO NOTHING`,
		lr.TenantID, lr.RunID, lr.WorkflowID, lr.ConfigHash, lr.SourceRevision, lr.ToolVersion,
		lr.LinkedAt, scores, caseCount, seedCount, gateOutcome, gateFailures, lr.Eval.SingleSeed, perNode)
	if err != nil {
		return false, fmt.Errorf("linkingest: record run %s: %w", lr.RunID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("linkingest: record run %s: %w", lr.RunID, err)
	}
	return n == 0, nil
}

// ObserveRunsReported raises the denominator, never lowers it.
//
// GREATEST rather than assignment: a CLI reporting 40 after reporting 100 has narrowed the slice it is
// describing, not shrunk the tenant's activity, and taking the latest would quietly IMPROVE the
// coverage ratio. Monotonicity is the property that keeps coverage a lower bound.
func (p *PGStore) ObserveRunsReported(tenantID string, n int) error {
	if n <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.db.ExecContext(ctx,
		`INSERT INTO run_link_coverage (tenant_id, runs_reported, updated_at)
		 VALUES ($1,$2,now())
		 ON CONFLICT (tenant_id) DO UPDATE
		   SET runs_reported = GREATEST(run_link_coverage.runs_reported, EXCLUDED.runs_reported),
		       updated_at    = now()`,
		tenantID, n); err != nil {
		return fmt.Errorf("linkingest: observe runs_reported for %s: %w", tenantID, err)
	}
	return nil
}

// Coverage returns the tenant's link coverage.
//
// The semantics MIRROR MemStore deliberately — it is the reference implementation, and two Stores that
// answer the same question differently is a bug that only shows up after a deployment switches stores.
// In particular: a tenant with links and no reported denominator has COMPLETE coverage over what we were
// told about, not unknown coverage; and the denominator is never allowed below the numerator, so
// coverage cannot exceed 100%.
func (p *PGStore) Coverage(tenantID string) (LinkCoverage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var linked int
	if err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM run_link WHERE tenant_id = $1`, tenantID).Scan(&linked); err != nil {
		return LinkCoverage{}, fmt.Errorf("linkingest: coverage numerator for %s: %w", tenantID, err)
	}

	var reported int
	known := true
	err := p.db.QueryRowContext(ctx,
		`SELECT runs_reported FROM run_link_coverage WHERE tenant_id = $1`, tenantID).Scan(&reported)
	switch {
	case err == sql.ErrNoRows:
		known = false // nobody reported a denominator — NOT an error
	case err != nil:
		return LinkCoverage{}, fmt.Errorf("linkingest: coverage denominator for %s: %w", tenantID, err)
	}

	if linked > reported {
		reported = linked
	}
	cov := LinkCoverage{RunsLinked: linked, RunsReported: reported, Known: known}
	cov.Complete = known && reported > 0 && linked >= reported
	if !known && linked > 0 {
		cov.Known, cov.RunsReported, cov.Complete = true, linked, true
	}
	return cov, nil
}

// LinkedRunIDs returns the run ids a tenant has linked, newest first.
func (p *PGStore) LinkedRunIDs(tenantID string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx,
		`SELECT run_id FROM run_link WHERE tenant_id = $1 ORDER BY linked_at DESC, run_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("linkingest: linked run ids for %s: %w", tenantID, err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("linkingest: linked run ids for %s: %w", tenantID, err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("linkingest: linked run ids for %s: %w", tenantID, err)
	}
	return out, nil
}

// ListForTenant returns a tenant's linked runs, newest first, before an optional cursor.
func (p *PGStore) ListForTenant(tenantID string, limit int, before time.Time) ([]LinkedRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if limit <= 0 {
		limit = 50
	}
	// One query with a NULL-tolerant cursor rather than two, so the paged and unpaged reads cannot
	// diverge in their column list or their ordering.
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+runLinkColumns+`
		   FROM run_link
		  WHERE tenant_id = $1 AND ($2::timestamptz IS NULL OR linked_at < $2)
		  ORDER BY linked_at DESC, run_id
		  LIMIT $3`, tenantID, nullTime(before), limit)
	if err != nil {
		return nil, fmt.Errorf("linkingest: list linked runs for %s: %w", tenantID, err)
	}
	defer func() { _ = rows.Close() }()
	out := []LinkedRun{}
	for rows.Next() {
		lr, err := scanRunLink(rows)
		if err != nil {
			return nil, fmt.Errorf("linkingest: list linked runs for %s: %w", tenantID, err)
		}
		out = append(out, lr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("linkingest: list linked runs for %s: %w", tenantID, err)
	}
	return out, nil
}

// nullTime writes SQL NULL for a zero cursor, so "the first page" and "before this instant" are one
// query rather than two that can drift.
func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// Get returns one linked run, or ok=false when it is not linked.
//
// ok=false now means exactly that, and nothing else: a read failure is the error. Those were the same
// value before the interface widened, which made a database outage look like a run nobody had linked.
// runLinkColumns is the SELECT list both readers use.
//
// Shared deliberately: Get and ForWorkflow return the same type, and two hand-maintained column lists
// for one struct drift — the usual way being that a column added for one reader silently reads as its
// zero value in the other, which for `eval_gate_outcome` would mean a board quietly treating a measured
// run as unmeasured.
const runLinkColumns = `run_id, tenant_id, workflow_id, config_hash, source_revision, tool_version,
	linked_at, scores_json, eval_case_count, eval_seed_count, eval_gate_outcome, eval_gate_failures, eval_single_seed,
	per_node_json`

// scanRunLink reads one row in runLinkColumns order.
func scanRunLink(sc interface{ Scan(...any) error }) (LinkedRun, error) {
	var lr LinkedRun
	var scores, gateFailures, perNode []byte
	// Nullable: a run linked before migration 0023 carries neither. sql.Null* rather than a zero value,
	// because "absent" and "zero cases / no gate" must stay distinguishable all the way to the console.
	var caseCount, seedCount sql.NullInt64
	var gateOutcome sql.NullString

	if err := sc.Scan(&lr.RunID, &lr.TenantID, &lr.WorkflowID, &lr.ConfigHash, &lr.SourceRevision,
		&lr.ToolVersion, &lr.LinkedAt, &scores, &caseCount, &seedCount, &gateOutcome, &gateFailures,
		&lr.Eval.SingleSeed, &perNode); err != nil {
		return LinkedRun{}, err
	}
	if len(scores) > 0 {
		if err := json.Unmarshal(scores, &lr.Scores); err != nil {
			return LinkedRun{}, fmt.Errorf("decode scores for run %s: %w", lr.RunID, err)
		}
	}
	if caseCount.Valid {
		lr.Eval.CaseCount = int(caseCount.Int64)
	}
	if seedCount.Valid {
		lr.Eval.SeedCount = int(seedCount.Int64)
	}
	if gateOutcome.Valid {
		lr.Eval.GateOutcome = runlink.GateOutcome(gateOutcome.String)
	}
	if len(gateFailures) > 0 {
		if err := json.Unmarshal(gateFailures, &lr.Eval.GateFailures); err != nil {
			return LinkedRun{}, fmt.Errorf("decode gate failures for run %s: %w", lr.RunID, err)
		}
	}
	if len(perNode) > 0 {
		if err := json.Unmarshal(perNode, &lr.PerNode); err != nil {
			return LinkedRun{}, fmt.Errorf("decode per-node metrics for run %s: %w", lr.RunID, err)
		}
	}
	return lr, nil
}

func (p *PGStore) Get(tenantID, runID string) (LinkedRun, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	row := p.db.QueryRowContext(ctx,
		`SELECT `+runLinkColumns+` FROM run_link WHERE tenant_id = $1 AND run_id = $2`, tenantID, runID)
	lr, err := scanRunLink(row)
	switch {
	case err == sql.ErrNoRows:
		return LinkedRun{}, false, nil
	case err != nil:
		return LinkedRun{}, false, fmt.Errorf("linkingest: get run %s: %w", runID, err)
	}
	return lr, true, nil
}

// ForWorkflow returns a tenant's runs for one workflow, newest first.
//
// Ordered in SQL with run_id as the tiebreak so two runs linked in the same instant keep a stable
// position — a board whose rows reshuffle between reloads is one a user cannot trust to compare.
func (p *PGStore) ForWorkflow(tenantID, workflowID string) ([]LinkedRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := p.db.QueryContext(ctx,
		`SELECT `+runLinkColumns+` FROM run_link
		  WHERE tenant_id = $1 AND workflow_id = $2
		  ORDER BY linked_at DESC, run_id ASC`, tenantID, workflowID)
	if err != nil {
		return nil, fmt.Errorf("linkingest: runs for workflow %s: %w", workflowID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []LinkedRun
	for rows.Next() {
		lr, err := scanRunLink(rows)
		if err != nil {
			return nil, fmt.Errorf("linkingest: runs for workflow %s: %w", workflowID, err)
		}
		out = append(out, lr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("linkingest: runs for workflow %s: %w", workflowID, err)
	}
	return out, nil
}
