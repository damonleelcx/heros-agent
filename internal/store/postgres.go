package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/task"
)

// Postgres is the durable Store. Unlike Memory, its state survives a process restart — which is the
// entire point, and the reason every claim below is expressed as ONE statement rather than a
// read-then-write.
//
// # 🔴 Why the claim is a single UPDATE ... FOR UPDATE SKIP LOCKED
//
// A read-then-write claim ("find a ready task, then mark it mine") has a window between the two halves
// in which another worker does the same thing. Both see the task as free; both take it. The window is
// small, which is worse than large: it makes the bug rare enough to reach production and rare enough
// that the duplicate work is blamed on something else.
//
// SKIP LOCKED is what makes the single statement scale: a second worker arriving concurrently steps
// over the row being claimed rather than blocking behind it, so N workers claim N different tasks
// instead of serialising on the first one.
type Postgres struct{ db *sql.DB }

// NewPostgres wraps a pool. The caller owns the pool's lifecycle.
func NewPostgres(db *sql.DB) *Postgres { return &Postgres{db: db} }

var _ Store = (*Postgres)(nil)

// ── goals ────────────────────────────────────────────────────────────────────────────────────────

func (p *Postgres) CreateGoal(g *goal.Goal) error {
	axes := marshalArray(g.Axes)
	ceil, _ := json.Marshal(g.Ceilings)
	spend, _ := json.Marshal(g.Spend)
	crit := marshalArray(g.Criteria)
	miles := marshalArray(g.Milestones)
	var refusal any
	if g.Refusal != nil {
		b, _ := json.Marshal(g.Refusal)
		refusal = b
	}
	_, err := p.db.ExecContext(context.Background(), `
		INSERT INTO goals (id, tenant, intent, objective, repo_url, revision, workflow_id,
		                   axes, ceilings, spend, criteria, milestones, state, refusal,
		                   expected_duration_ns, created_at, updated_at, last_checkpoint)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		g.ID, g.Tenant, string(g.Intent), g.Objective, g.Subject.RepoURL, g.Subject.Revision,
		g.Subject.WorkflowID, axes, ceil, spend, crit, miles, string(g.State), refusal,
		int64(g.ExpectedDuration), nz(g.CreatedAt), nz(g.UpdatedAt), nullTime(g.LastCheckpoint))
	if err != nil {
		return fmt.Errorf("store: create goal %q: %w", g.ID, err)
	}
	return nil
}

func (p *Postgres) LoadGoal(id goal.ID) (*goal.Goal, error) {
	row := p.db.QueryRowContext(context.Background(), goalColumns+` WHERE id = $1`, id)
	g, err := scanGoal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %q", ErrGoalNotFound, id)
	}
	return g, err
}

const goalColumns = `
	SELECT id, tenant, intent, objective, repo_url, revision, workflow_id, axes, ceilings, spend,
	       criteria, milestones, state, refusal, expected_duration_ns, created_at, updated_at,
	       last_checkpoint
	FROM goals`

type scanner interface{ Scan(dest ...any) error }

func scanGoal(row scanner) (*goal.Goal, error) {
	var (
		g                              goal.Goal
		intentStr, stateStr            string
		axes, ceil, spend, crit, miles []byte
		refusal                        []byte
		durNS                          int64
		lastCP                         sql.NullTime
	)
	if err := row.Scan(&g.ID, &g.Tenant, &intentStr, &g.Objective, &g.Subject.RepoURL,
		&g.Subject.Revision, &g.Subject.WorkflowID, &axes, &ceil, &spend, &crit, &miles,
		&stateStr, &refusal, &durNS, &g.CreatedAt, &g.UpdatedAt, &lastCP); err != nil {
		return nil, err
	}
	g.Intent, g.State, g.ExpectedDuration = intentToType(intentStr), goal.State(stateStr), time.Duration(durNS)
	if lastCP.Valid {
		g.LastCheckpoint = lastCP.Time
	}
	_ = json.Unmarshal(axes, &g.Axes)
	_ = json.Unmarshal(ceil, &g.Ceilings)
	_ = json.Unmarshal(spend, &g.Spend)
	_ = json.Unmarshal(crit, &g.Criteria)
	_ = json.Unmarshal(miles, &g.Milestones)
	if len(refusal) > 0 {
		var r bounds.Refusal
		if json.Unmarshal(refusal, &r) == nil {
			g.Refusal = &r
		}
	}
	return &g, nil
}

func (p *Postgres) SaveGoal(g *goal.Goal) error {
	axes := marshalArray(g.Axes)
	ceil, _ := json.Marshal(g.Ceilings)
	spend, _ := json.Marshal(g.Spend)
	crit := marshalArray(g.Criteria)
	miles := marshalArray(g.Milestones)
	var refusal any
	if g.Refusal != nil {
		b, _ := json.Marshal(g.Refusal)
		refusal = b
	}
	// 🔴 A transaction with the row LOCKED, because this is a read-then-write and the thing being read
	// is what decides whether the write is legal. Without the lock two writers both read `running` and
	// both proceed — which is precisely the race refuseTerminalOverwrite exists to stop, reintroduced
	// one layer down. The Memory leg gets the same property from its mutex.
	//
	// 🔴 The terminal list is NOT retyped in SQL. Which states are terminal is a rule of the goal model;
	// a `state NOT IN ('succeeded','failed',…)` here would be a second copy that stops agreeing with it
	// the first time a state is added, and it would fail OPEN — silently allowing the overwrite.
	tx, err := p.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("store: save goal %q: %w", g.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	var current string
	err = tx.QueryRowContext(context.Background(),
		`SELECT state FROM goals WHERE id=$1 FOR UPDATE`, g.ID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %q", ErrGoalNotFound, g.ID)
	}
	if err != nil {
		return fmt.Errorf("store: save goal %q: %w", g.ID, err)
	}
	if err := refuseTerminalOverwrite(goal.State(current), g); err != nil {
		return err
	}

	res, err := tx.ExecContext(context.Background(), `
		UPDATE goals SET tenant=$2, intent=$3, objective=$4, repo_url=$5, revision=$6, workflow_id=$7,
		       axes=$8, ceilings=$9, spend=$10, criteria=$11, milestones=$12, state=$13, refusal=$14,
		       expected_duration_ns=$15, updated_at=$16, last_checkpoint=$17
		WHERE id=$1`,
		g.ID, g.Tenant, string(g.Intent), g.Objective, g.Subject.RepoURL, g.Subject.Revision,
		g.Subject.WorkflowID, axes, ceil, spend, crit, miles, string(g.State), refusal,
		int64(g.ExpectedDuration), nz(g.UpdatedAt), nullTime(g.LastCheckpoint))
	if err != nil {
		return fmt.Errorf("store: save goal %q: %w", g.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %q", ErrGoalNotFound, g.ID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: save goal %q: %w", g.ID, err)
	}
	return nil
}

func (p *Postgres) ListGoals(state goal.State) ([]*goal.Goal, error) {
	q, args := goalColumns+` ORDER BY id`, []any{}
	if state != "" {
		q, args = goalColumns+` WHERE state = $1 ORDER BY id`, []any{string(state)}
	}
	rows, err := p.db.QueryContext(context.Background(), q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list goals: %w", err)
	}
	defer rows.Close()
	var out []*goal.Goal
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ── tasks ────────────────────────────────────────────────────────────────────────────────────────

// SaveDAG upserts every task. Upsert rather than insert so a replanner that adds tasks to a live goal
// does not have to know which of them already exist.
//
// 🔴 It deliberately does NOT overwrite lease or execution columns. A DAG object in one worker's memory
// is a snapshot; writing its idea of `attempt` or `leased_by` back would clobber whatever another
// worker has done since. Only the structural fields are the planner's to write.
// LatestGoal returns the newest goal by creation time, tie-broken by id so the answer is stable.
func (p *Postgres) LatestGoal(tenant string) (*goal.Goal, bool, error) {
	q := goalColumns + ` ORDER BY created_at DESC, id DESC LIMIT 1`
	args := []any{}
	if tenant != "" {
		q = goalColumns + ` WHERE tenant = $1 ORDER BY created_at DESC, id DESC LIMIT 1`
		args = append(args, tenant)
	}
	g, err := scanGoal(p.db.QueryRowContext(context.Background(), q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: latest goal: %w", err)
	}
	return g, true, nil
}

func (p *Postgres) SaveDAG(d *task.DAG) error {
	tx, err := p.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("store: save dag: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, t := range d.Tasks {
		if err := t.RequireIdempotency(); err != nil {
			return err
		}
		deps, contributes := marshalArray(t.DependsOn), marshalArray(t.Contributes)
		if _, err := tx.ExecContext(context.Background(), `
			INSERT INTO tasks (goal_id, id, kind, depends_on, contributes, state, attempt, spawn_depth,
			                   idempotency_key, result, failure, leased_by, lease_expiry,
			                   created_at, updated_at)
			VALUES ($1,$2,$3,$4,$13,$5,$6,$7,$8,$9,$10,'',NULL,$11,$12)
			ON CONFLICT (goal_id, id) DO UPDATE
			  SET kind=EXCLUDED.kind, depends_on=EXCLUDED.depends_on,
			      contributes=EXCLUDED.contributes, updated_at=EXCLUDED.updated_at`,
			d.GoalID, t.ID, t.Kind, deps, string(t.State), t.Attempt, t.SpawnDepth,
			t.IdempotencyKey, t.Result, t.Failure, nz(t.CreatedAt), nz(t.UpdatedAt),
			contributes); err != nil {
			return fmt.Errorf("store: save task %q: %w", t.ID, err)
		}
	}
	return tx.Commit()
}

func (p *Postgres) LoadDAG(goalID goal.ID) (*task.DAG, error) {
	rows, err := p.db.QueryContext(context.Background(), `
		SELECT id, kind, depends_on, contributes, state, attempt, spawn_depth, idempotency_key, result,
		       failure, leased_by, lease_expiry, created_at, updated_at, approved
		FROM tasks WHERE goal_id = $1 ORDER BY id`, goalID)
	if err != nil {
		return nil, fmt.Errorf("store: load dag: %w", err)
	}
	defer rows.Close()
	var tasks []*task.Task
	for rows.Next() {
		var (
			t           task.Task
			deps        []byte
			contributes []byte
			stateStr    string
			expiry      sql.NullTime
		)
		if err := rows.Scan(&t.ID, &t.Kind, &deps, &contributes, &stateStr, &t.Attempt, &t.SpawnDepth,
			&t.IdempotencyKey, &t.Result, &t.Failure, &t.LeasedBy, &expiry,
			&t.CreatedAt, &t.UpdatedAt, &t.Approved); err != nil {
			return nil, err
		}
		t.GoalID, t.State = string(goalID), task.State(stateStr)
		if expiry.Valid {
			t.LeaseExpiry = expiry.Time
		}
		_ = json.Unmarshal(deps, &t.DependsOn)
		_ = json.Unmarshal(contributes, &t.Contributes)
		tasks = append(tasks, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("%w: no DAG for goal %q", ErrGoalNotFound, goalID)
	}
	return task.NewDAG(string(goalID), tasks)
}

// Claim leases one claimable task in a single statement.
//
// The inner SELECT is the whole design:
//
//   - it joins goals so a PAUSED goal hands out nothing, without the worker needing to know the goal
//     state list;
//   - `NOT EXISTS (unmet dependency)` is the DAG's readiness rule expressed in SQL, so the database and
//     the Go DAG cannot disagree about what is runnable;
//   - the lease predicate treats an expired lease as absent — evaluated here, on read, never by a
//     sweeper that can itself be down;
//   - FOR UPDATE SKIP LOCKED makes concurrent claimers take DIFFERENT rows instead of serialising.
func (p *Postgres) Claim(goalID goal.ID, worker string, lease time.Duration, now time.Time) (*task.Task, error) {
	row := p.db.QueryRowContext(context.Background(), `
		UPDATE tasks SET state='running', leased_by=$2, lease_expiry=$3, attempt=attempt+1, updated_at=$4
		WHERE (goal_id, id) = (
			SELECT t.goal_id, t.id FROM tasks t
			JOIN goals g ON g.id = t.goal_id AND g.state = 'running'
			WHERE t.goal_id = $1
			  AND (t.state IN ('pending','ready')
			       OR (t.state = 'running' AND (t.lease_expiry IS NULL OR t.lease_expiry <= $4)))
			  -- Required dependencies must have SUCCEEDED.
			  AND NOT EXISTS (
			      SELECT 1 FROM jsonb_array_elements_text(t.depends_on) AS dep(id)
			      JOIN tasks d ON d.goal_id = t.goal_id AND d.id = dep.id
			      WHERE d.state <> 'succeeded')
			  -- Contributory dependencies must merely be TERMINAL. The gate needs its generators
			  -- finished, not successful; see task.Task.Contributes.
			  AND NOT EXISTS (
			      SELECT 1 FROM jsonb_array_elements_text(t.contributes) AS dep(id)
			      JOIN tasks d ON d.goal_id = t.goal_id AND d.id = dep.id
			      WHERE d.state NOT IN ('succeeded','failed','blocked','cancelled'))
			ORDER BY t.id
			FOR UPDATE OF t SKIP LOCKED
			LIMIT 1)
		RETURNING id, kind, depends_on, contributes, state, attempt, spawn_depth, idempotency_key,
		          result, failure, leased_by, lease_expiry, created_at, updated_at, approved`,
		goalID, worker, now.Add(lease), now)

	// 🔴 `contributes` is returned here as well as in LoadDAG. It was missed the first time, and the
	// symptom was silent: a claimed task arrived with no contributory edges, so the quality gate was
	// handed zero generator results and refused a set that had three generators' worth of cases sitting
	// in the database. A column added to a table has to be added to every query that builds the struct,
	// and there are three.
	var (
		t           task.Task
		deps        []byte
		contributes []byte
		stateStr    string
		expiry      sql.NullTime
	)
	err := row.Scan(&t.ID, &t.Kind, &deps, &contributes, &stateStr, &t.Attempt, &t.SpawnDepth,
		&t.IdempotencyKey, &t.Result, &t.Failure, &t.LeasedBy, &expiry, &t.CreatedAt, &t.UpdatedAt,
		&t.Approved)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoWork
	}
	if err != nil {
		return nil, fmt.Errorf("store: claim: %w", err)
	}
	t.GoalID, t.State = string(goalID), task.State(stateStr)
	if expiry.Valid {
		t.LeaseExpiry = expiry.Time
	}
	_ = json.Unmarshal(deps, &t.DependsOn)
	_ = json.Unmarshal(contributes, &t.Contributes)
	return &t, nil
}

// heldClause is the predicate for "this worker still holds a live lease". Written once and reused by
// every mutation, because a lease check that differs between call sites is a lease check that is wrong
// at one of them.
const heldClause = ` AND leased_by = $3 AND lease_expiry > $4`

func (p *Postgres) Renew(goalID goal.ID, id task.ID, worker string, lease time.Duration, now time.Time) error {
	res, err := p.db.ExecContext(context.Background(),
		`UPDATE tasks SET lease_expiry=$5, updated_at=$4 WHERE goal_id=$1 AND id=$2`+heldClause,
		goalID, id, worker, now, now.Add(lease))
	return affectedOrLeaseLost(res, err, id, "renew")
}

func (p *Postgres) Complete(goalID goal.ID, id task.ID, worker string, next task.State,
	result []byte, failure string, now time.Time) error {
	res, err := p.db.ExecContext(context.Background(),
		`UPDATE tasks SET state=$5, result=$6, failure=$7, leased_by='', lease_expiry=NULL, updated_at=$4
		 WHERE goal_id=$1 AND id=$2`+heldClause,
		goalID, id, worker, now, string(next), result, failure)
	if err := affectedOrLeaseLost(res, err, id, "complete"); err != nil {
		return err
	}
	if next == task.Failed {
		return p.propagateFailure(goalID, now)
	}
	return nil
}

func (p *Postgres) Release(goalID goal.ID, id task.ID, worker string, now time.Time) error {
	res, err := p.db.ExecContext(context.Background(),
		`UPDATE tasks SET state='ready', leased_by='', lease_expiry=NULL, updated_at=$4
		 WHERE goal_id=$1 AND id=$2`+heldClause,
		goalID, id, worker, now)
	return affectedOrLeaseLost(res, err, id, "release")
}

// Decide answers a parked task. The WHERE clause carries the guard: only a row still in
// awaiting_approval is updated, so a second decision affects nothing and is reported as such.
func (p *Postgres) Decide(goalID goal.ID, id task.ID, approve bool, now time.Time) error {
	next := "cancelled"
	failure := "declined"
	if approve {
		next, failure = "ready", ""
	}
	res, err := p.db.ExecContext(context.Background(), `
		UPDATE tasks SET state=$3, failure=$4, approved=$6, updated_at=$5
		WHERE goal_id=$1 AND id=$2 AND state='awaiting_approval'`,
		goalID, id, next, failure, now, approve)
	if err != nil {
		return fmt.Errorf("store: decide %q: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %q is not awaiting approval", ErrNotClaimable, id)
	}
	if !approve {
		return p.propagateFailure(goalID, now)
	}
	return nil
}

// Cancel stops a run. See the Store interface for why a leased task is left running.
//
// 🔴 One transaction, and the goal row is taken FOR UPDATE first. Two people clicking Cancel on the
// same run, or a cancel racing the worker's own state write, must not both pass the "is it already
// terminal?" guard — the row lock is what makes the guard mean anything on this leg. The Memory leg
// gets the same property from its mutex.
//
// 🔴 The terminal guard is `goal.Cancel`, not a state list retyped here. Which states may be cancelled
// is a rule of the goal model; a second copy in SQL is a copy that stops agreeing with it the first
// time a state is added.
func (p *Postgres) Cancel(goalID goal.ID, now time.Time) (int, error) {
	tx, err := p.db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, fmt.Errorf("store: cancel %q: %w", goalID, err)
	}
	defer func() { _ = tx.Rollback() }()

	var state string
	err = tx.QueryRowContext(context.Background(),
		`SELECT state FROM goals WHERE id=$1 FOR UPDATE`, goalID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: %q", ErrGoalNotFound, goalID)
	}
	if err != nil {
		return 0, fmt.Errorf("store: cancel %q: %w", goalID, err)
	}
	g := &goal.Goal{State: goal.State(state)}
	if err := g.Cancel(now); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(context.Background(),
		`UPDATE goals SET state=$2, updated_at=$3 WHERE id=$1`, goalID, string(g.State), now); err != nil {
		return 0, fmt.Errorf("store: cancel %q: %w", goalID, err)
	}

	// The exemption mirrors `heldClause` and `cancellable`: only a lease that has not expired protects a
	// task. An expired lease belongs to a worker that is gone, and honouring it would leave a cancelled
	// run with tasks stuck pending behind a process that will never come back.
	res, err := tx.ExecContext(context.Background(), `
		UPDATE tasks SET state='cancelled', updated_at=$2
		WHERE goal_id=$1
		  AND state NOT IN ('succeeded','failed','blocked','cancelled')
		  AND NOT (leased_by IS NOT NULL AND leased_by <> ''
		           AND lease_expiry IS NOT NULL AND lease_expiry > $2)`, goalID, now)
	if err != nil {
		return 0, fmt.Errorf("store: cancel %q: %w", goalID, err)
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: cancel %q: %w", goalID, err)
	}
	return int(n), nil
}

// propagateFailure blocks everything transitively downstream of a failed task.
//
// It loops because blocking one task can block its dependents in turn; the loop terminates because each
// pass strictly reduces the number of non-terminal tasks, and stops as soon as a pass changes nothing.
func (p *Postgres) propagateFailure(goalID goal.ID, now time.Time) error {
	for {
		res, err := p.db.ExecContext(context.Background(), `
			UPDATE tasks t SET state='blocked', updated_at=$2,
			       failure='a dependency failed, was blocked, or was cancelled'
			WHERE t.goal_id=$1
			  AND t.state NOT IN ('succeeded','failed','blocked','cancelled')
			  -- 🔴 depends_on only. A failed CONTRIBUTOR is a gap in the result, recorded by whatever
			  -- consumes it, not a reason to abandon the branch.
			  AND EXISTS (
			      SELECT 1 FROM jsonb_array_elements_text(t.depends_on) AS dep(id)
			      JOIN tasks d ON d.goal_id = t.goal_id AND d.id = dep.id
			      WHERE d.state IN ('failed','blocked','cancelled'))`, goalID, now)
		if err != nil {
			return fmt.Errorf("store: propagate failure: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
	}
}

func affectedOrLeaseLost(res sql.Result, err error, id task.ID, op string) error {
	if err != nil {
		return fmt.Errorf("store: %s %q: %w", op, id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %q (lease expired or held by another worker)", ErrLeaseLost, id)
	}
	return nil
}

// ── checkpoints ──────────────────────────────────────────────────────────────────────────────────

// Checkpoint writes a resumable point. Idempotent on (goal_id, iteration): a worker that crashes after
// writing and before acknowledging re-writes the same iteration rather than creating a duplicate.
func (p *Postgres) Checkpoint(cp Checkpoint) error {
	_, err := p.db.ExecContext(context.Background(), `
		INSERT INTO checkpoints (goal_id, iteration, note, at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (goal_id, iteration) DO UPDATE SET note=EXCLUDED.note, at=EXCLUDED.at`,
		cp.GoalID, cp.Iteration, cp.Note, nz(cp.At))
	if err != nil {
		return fmt.Errorf("store: checkpoint: %w", err)
	}
	return nil
}

func (p *Postgres) LatestCheckpoint(goalID goal.ID) (Checkpoint, bool, error) {
	var cp Checkpoint
	err := p.db.QueryRowContext(context.Background(), `
		SELECT goal_id, iteration, note, at FROM checkpoints
		WHERE goal_id=$1 ORDER BY iteration DESC LIMIT 1`, goalID).
		Scan(&cp.GoalID, &cp.Iteration, &cp.Note, &cp.At)
	if errors.Is(err, sql.ErrNoRows) {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("store: latest checkpoint: %w", err)
	}
	return cp, true, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

// intentToType exists so the conversion has one home. Validity is the caller's business: a stored row
// is never REJECTED at read time for naming an intent this build does not know. Refusing to load such a
// row would make a rollback un-diagnosable exactly when somebody needs to read what the newer version
// wrote.
func intentToType(s string) intent.Intent { return intent.Intent(s) }

// marshalArray serialises a slice, rendering a nil slice as `[]` rather than `null`.
//
// # 🔴 The bug this exists to prevent, found by a real Postgres and invisible to the in-memory store
//
// `json.Marshal` on a nil slice produces the scalar `null`, not the empty array. Stored in a jsonb
// column that is later fed to `jsonb_array_elements_text`, that scalar raises
// "cannot extract elements from a scalar" — at CLAIM time, on a query whose error message says nothing
// about the write that caused it, for a task whose only distinguishing feature is having no
// dependencies. Which is to say: the very first task of every DAG.
//
// The in-memory implementation cannot reproduce it, because a nil slice and an empty slice behave
// identically in Go. This is the whole argument for running the conformance suite against a real
// database rather than a simulated one.
//
// The schema carries a matching CHECK so a writer that bypasses this helper fails at INSERT — loudly,
// at the site of the mistake — rather than at read time somewhere else entirely.
func marshalArray(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return []byte("[]")
	}
	return b
}

func nz(t time.Time) time.Time {
	if t.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return t
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
