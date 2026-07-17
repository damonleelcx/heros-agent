package executor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Store persists run and node_execution records (task 3.9, run half; task 5.2).
//
// Dialect: PostgreSQL only. Backed by 0005's `run` and `node_execution`.
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// ErrRunNotFound is returned when no run is recorded for a run_id.
var ErrRunNotFound = errors.New("executor: no run recorded for this run_id")

// ErrDuplicateNodeExecution is returned when a node execution is written twice for one
// (run_id, node_id, attempt_group) — the double-write race 0005's primary key catches (task 5.2).
var ErrDuplicateNodeExecution = errors.New("executor: this node execution is already recorded")

// ErrRunIDCollision is returned when a run_id is already recorded against a DIFFERENT
// {config_hash, source_revision, seed}. See Start.
var ErrRunIDCollision = errors.New("executor: this run_id already names a different configuration")

// Start records a dispatched run.
//
// Written BEFORE the run executes, so a crash mid-run leaves a `running` row rather than nothing. A
// run that vanished because we only recorded successes is indistinguishable from one that was never
// submitted, and PRD §9 requires a stuck run never be indistinguishable from a slow one — which is
// only possible if the stuck one exists in the table.
//
// # Idempotent, because the run_id is derived rather than minted
//
// RunIDFor makes a run's id a pure function of {config_hash, source_revision, seed}, so a re-submit
// of the same experiment — and an at-least-once redelivery (PRD OQ4) — arrives here with the SAME
// run_id. A bare INSERT would make the second one a primary-key error, and a caller forced to
// pattern-match "23505" out of a driver error to tell "already submitted" from "the database is
// broken" is a caller that will get it wrong.
//
// So: DO NOTHING plus a read-back that PROVES the stored row is this run, exactly as RecordNode and
// Finish already do in this file. The read-back is the load-bearing half. Treating 0 rows as
// "already there, fine" would report a write that never landed as a success, and — worse — would
// silently accept a run_id that names a DIFFERENT configuration, so the UI would then watch a run
// that has nothing to do with what the user submitted. That case is a loud error instead.
func (s *Store) Start(ctx context.Context, runID, configHash, sourceRevision string, seed int64) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO run (run_id, config_hash, source_revision, seed, status)
		 VALUES ($1, $2, $3, $4, 'running') ON CONFLICT (run_id) DO NOTHING`,
		runID, configHash, sourceRevision, seed)
	if err != nil {
		return fmt.Errorf("executor: start run %s: %w", runID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("executor: start run %s: rows affected: %w", runID, err)
	}
	if n == 1 {
		return nil
	}

	// n == 0: a row already claims this run_id. Prove it is OURS rather than accept a swallowed
	// constraint failure — or someone else's run — as a successful write.
	var gotConfig, gotRev string
	var gotSeed int64
	err = s.db.QueryRowContext(ctx,
		`SELECT config_hash, source_revision, seed FROM run WHERE run_id = $1`, runID).
		Scan(&gotConfig, &gotRev, &gotSeed)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("executor: start run %s: the insert affected no rows and no row exists "+
			"(likely a silently-swallowed constraint failure)", runID)
	case err != nil:
		return fmt.Errorf("executor: start run: read-back: %w", err)
	}
	if gotConfig != configHash || gotRev != sourceRevision || gotSeed != seed {
		// Only reachable if a run_id stopped being a function of these three — a caller minting ids
		// by hand, or a change to RunIDFor's domain separator. Either way the run on record is not the
		// one being started, and continuing would attribute this execution's results to it.
		return fmt.Errorf("%w: run %s is already recorded for {config %s, revision %s, seed %d} but this "+
			"Start carries {config %s, revision %s, seed %d}",
			ErrRunIDCollision, runID, gotConfig, gotRev, gotSeed, configHash, sourceRevision, seed)
	}
	return nil // the same run, started again: an idempotent re-submit or redelivery
}

// RecordNode persists one node execution (FR18).
//
// It never updates. 0005 makes node_execution immutable, and the reason is FR17: "a retried node
// invocation SHALL NOT double-write run results." An upsert here would turn the duplicate a retry
// produces into a silent overwrite — the row would look correct, and P4 would score one invocation
// twice with no trace that it happened. A conflict is returned as ErrDuplicateNodeExecution so the
// caller can tell "already done" (idempotent redelivery, which is fine) from a real failure.
func (s *Store) RecordNode(ctx context.Context, runID string, n NodeResult) error {
	var in, out any
	if n.InputBlobHash != "" {
		in = n.InputBlobHash
	}
	if n.OutputBlobHash != "" {
		out = n.OutputBlobHash
	}
	errText := n.Error
	// 0005's node_execution_failure_has_a_reason requires a non-empty reason on a non-success. A
	// failure we cannot explain must still be recordable — the fact that it failed is what matters.
	if n.Status != StatusSucceeded && errText == "" {
		errText = "(no reason reported)"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO node_execution (run_id, node_id, attempt_group, status, input_blob_hash,
		                             output_blob_hash, idempotency_key, error)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (run_id, node_id, attempt_group) DO NOTHING`,
		runID, n.NodeID, n.AttemptGroup, string(n.Status), in, out, n.IdempotencyKey, errText)
	if err != nil {
		return fmt.Errorf("executor: record node %s/%s: %w", runID, n.NodeID, err)
	}
	// DO NOTHING plus a read-back, rather than trusting "0 rows means duplicate": a swallowed
	// constraint failure looks identical to a duplicate from here, and reporting a write that never
	// landed as success is how a run's results end up empty while everything says green.
	var stored string
	err = s.db.QueryRowContext(ctx,
		`SELECT idempotency_key FROM node_execution WHERE run_id=$1 AND node_id=$2 AND attempt_group=$3`,
		runID, n.NodeID, n.AttemptGroup).Scan(&stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("executor: record node %s/%s: the insert affected no rows and no row exists "+
			"(likely a silently-swallowed constraint failure)", runID, n.NodeID)
	case err != nil:
		return fmt.Errorf("executor: record node: read-back: %w", err)
	}
	if stored != n.IdempotencyKey {
		// The same coordinates recorded under two keys means attempt_group was reused for what is
		// really a different invocation — the provider would bill both, and we would have one row.
		return fmt.Errorf("%w: %s/%s/%d is recorded under key %q but this write carries %q",
			ErrDuplicateNodeExecution, runID, n.NodeID, n.AttemptGroup, stored, n.IdempotencyKey)
	}
	return nil
}

// Finish moves a run to a terminal status.
//
// The UPDATE is conditional on status='running', which is what makes a double-finish a caught no-op
// rather than a silent overwrite of the first verdict. An at-least-once queue (PRD OQ4) can deliver
// the same run twice; the second must not be able to rewrite `halted` to `succeeded`.
func (s *Store) Finish(ctx context.Context, run *Run) error {
	var nodeID, reason any
	if run.Status == StatusHalted {
		nodeID = run.HaltedNodeID
		if run.HaltedReason != "" {
			reason = run.HaltedReason
		}
	}
	finished := run.FinishedAt
	if finished.IsZero() {
		finished = time.Now().UTC()
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE run SET status=$2, halted_node_id=$3, halted_reason=$4, finished_at=$5
		 WHERE run_id=$1 AND status='running'`,
		run.RunID, string(run.Status), nodeID, reason, finished)
	if err != nil {
		return fmt.Errorf("executor: finish run %s: %w", run.RunID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("executor: finish run: rows affected: %w", err)
	}
	if n == 1 {
		return nil
	}

	// 0 rows: either the run is not there, or it is already terminal. These are very different, and
	// collapsing them would hide a lost run behind a benign-looking duplicate.
	var status string
	err = s.db.QueryRowContext(ctx, `SELECT status FROM run WHERE run_id=$1`, run.RunID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s (Finish without Start)", ErrRunNotFound, run.RunID)
	}
	if err != nil {
		return fmt.Errorf("executor: finish run: read-back: %w", err)
	}
	if status == string(run.Status) {
		return nil // already terminal with the same verdict: an idempotent redelivery
	}
	return fmt.Errorf("executor: run %s is already terminal as %q; refusing to overwrite it with %q",
		run.RunID, status, run.Status)
}

// RunRecord is a stored run.
type RunRecord struct {
	RunID          string
	ConfigHash     string
	SourceRevision string
	Seed           int64
	Status         Status
	HaltedNodeID   string
	HaltedReason   string
	Nodes          []NodeResult
}

// Get returns a run and its node executions — the query the UI's inspect view is (FR18).
func (s *Store) Get(ctx context.Context, runID string) (*RunRecord, error) {
	var r RunRecord
	var status string
	var nodeID, reason sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT config_hash, source_revision, seed, status, halted_node_id, halted_reason
		 FROM run WHERE run_id = $1`, runID).
		Scan(&r.ConfigHash, &r.SourceRevision, &r.Seed, &status, &nodeID, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	if err != nil {
		return nil, fmt.Errorf("executor: get run: %w", err)
	}
	r.RunID, r.Status = runID, Status(status)
	r.HaltedNodeID, r.HaltedReason = nodeID.String, reason.String

	rows, err := s.db.QueryContext(ctx,
		`SELECT node_id, attempt_group, status, input_blob_hash, output_blob_hash, idempotency_key, error
		 FROM node_execution WHERE run_id=$1 ORDER BY node_id, attempt_group`, runID)
	if err != nil {
		return nil, fmt.Errorf("executor: get node executions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var n NodeResult
		var st string
		var in, out sql.NullString
		if err := rows.Scan(&n.NodeID, &n.AttemptGroup, &st, &in, &out, &n.IdempotencyKey, &n.Error); err != nil {
			return nil, fmt.Errorf("executor: scan node execution: %w", err)
		}
		n.Status, n.InputBlobHash, n.OutputBlobHash = Status(st), in.String, out.String
		r.Nodes = append(r.Nodes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("executor: get node executions: %w", err)
	}
	return &r, nil
}
