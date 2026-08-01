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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := p.db.ExecContext(ctx,
		`INSERT INTO run_link
		   (tenant_id, run_id, workflow_id, config_hash, source_revision, tool_version, linked_at, scores_json)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (tenant_id, run_id) DO NOTHING`,
		lr.TenantID, lr.RunID, lr.WorkflowID, lr.ConfigHash, lr.SourceRevision, lr.ToolVersion,
		lr.LinkedAt, scores)
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

// Get returns one linked run, or ok=false when it is not linked.
//
// ok=false now means exactly that, and nothing else: a read failure is the error. Those were the same
// value before the interface widened, which made a database outage look like a run nobody had linked.
func (p *PGStore) Get(tenantID, runID string) (LinkedRun, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var lr LinkedRun
	var scores []byte
	err := p.db.QueryRowContext(ctx,
		`SELECT run_id, tenant_id, workflow_id, config_hash, source_revision, tool_version, linked_at, scores_json
		   FROM run_link WHERE tenant_id = $1 AND run_id = $2`, tenantID, runID).
		Scan(&lr.RunID, &lr.TenantID, &lr.WorkflowID, &lr.ConfigHash, &lr.SourceRevision,
			&lr.ToolVersion, &lr.LinkedAt, &scores)
	switch {
	case err == sql.ErrNoRows:
		return LinkedRun{}, false, nil
	case err != nil:
		return LinkedRun{}, false, fmt.Errorf("linkingest: get run %s: %w", runID, err)
	}
	if len(scores) > 0 {
		var sc []runlink.Score
		if err := json.Unmarshal(scores, &sc); err != nil {
			return LinkedRun{}, false, fmt.Errorf("linkingest: decode scores for run %s: %w", runID, err)
		}
		lr.Scores = sc
	}
	return lr, true, nil
}
