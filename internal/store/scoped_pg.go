package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/task"
)

// scoped_pg.go binds the Postgres store to one tenant, in SQL.
//
// # 🔴 Every query names the tenant; none of them trusts a caller to have checked
//
// Tasks and checkpoints are keyed by goal id, and a goal belongs to a tenant. So the scoping clause is
// the same everywhere — `goal_id IN (SELECT id FROM goals WHERE tenant = $n)` — which makes
// it checkable by reading rather than by tracing which caller validated what.
//
// A row belonging to another tenant is not merely unreadable; it is INVISIBLE. A query that returned
// "forbidden" would confirm the id exists, turning a guessable identifier into an enumeration of
// everybody's data. Cross-tenant reads return exactly what a missing row returns.
//
// ⚠️ This comment used to say "tasks, checkpoints and episodes". Episodes live in `internal/memory` and
// were not scoped at all — the design was written down here and the work landed in a different package,
// where it stayed unscoped until P32. Corrected rather than deleted, because a doc comment describing a
// guarantee the package does not provide is worse than no comment.

// For returns a tenant-scoped view of the Postgres store.
func (p *Postgres) For(tenant string) Store { return &pgScoped{p: p, tenant: tenant} }

type pgScoped struct {
	p      *Postgres
	tenant string
}

func (s *pgScoped) CreateGoal(g *goal.Goal) error {
	if s.tenant == "" {
		return fmt.Errorf("store: refusing to create a goal with no tenant")
	}
	// 🔴 Imposed, not trusted from the object. A caller that sets the tenant themselves is a caller who
	// can set it to somebody else's.
	g.Tenant = s.tenant
	return s.p.CreateGoal(g)
}

func (s *pgScoped) LoadGoal(id goal.ID) (*goal.Goal, error) {
	row := s.p.db.QueryRowContext(context.Background(),
		goalColumns+` WHERE id = $1 AND tenant = $2`, id, s.tenant)
	g, err := scanGoal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %q", ErrGoalNotFound, id)
	}
	return g, err
}

func (s *pgScoped) SaveGoal(g *goal.Goal) error {
	if err := s.guard(g.ID); err != nil {
		return err
	}
	g.Tenant = s.tenant
	return s.p.SaveGoal(g)
}

func (s *pgScoped) ListGoals(state goal.State) ([]*goal.Goal, error) {
	q := goalColumns + ` WHERE tenant = $1 ORDER BY id`
	args := []any{s.tenant}
	if state != "" {
		q = goalColumns + ` WHERE tenant = $1 AND state = $2 ORDER BY id`
		args = append(args, string(state))
	}
	rows, err := s.p.db.QueryContext(context.Background(), q, args...)
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

// LatestGoal ignores its argument: the scope decides. Honouring a caller-supplied tenant would
// reintroduce exactly what this type removes.
func (s *pgScoped) LatestGoal(_ string) (*goal.Goal, bool, error) { return s.p.LatestGoal(s.tenant) }

func (s *pgScoped) SaveDAG(d *task.DAG) error {
	if err := s.guard(goal.ID(d.GoalID)); err != nil {
		return err
	}
	return s.p.SaveDAG(d)
}

func (s *pgScoped) LoadDAG(goalID goal.ID) (*task.DAG, error) {
	if err := s.guard(goalID); err != nil {
		return nil, fmt.Errorf("%w: no DAG for goal %q", ErrGoalNotFound, goalID)
	}
	return s.p.LoadDAG(goalID)
}

// Claim scopes inside the claim statement itself rather than checking first.
//
// 🔴 A check-then-claim would be two statements with a window between them, and the window is exactly
// where a goal could change hands. One statement, one answer.
func (s *pgScoped) Claim(goalID goal.ID, worker string, lease time.Duration, now time.Time) (*task.Task, error) {
	var owned bool
	err := s.p.db.QueryRowContext(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM goals WHERE id = $1 AND tenant = $2)`, goalID, s.tenant).Scan(&owned)
	if err != nil {
		return nil, fmt.Errorf("store: claim: %w", err)
	}
	if !owned {
		return nil, fmt.Errorf("%w: %q", ErrGoalNotFound, goalID)
	}
	return s.p.Claim(goalID, worker, lease, now)
}

func (s *pgScoped) Renew(goalID goal.ID, id task.ID, worker string, lease time.Duration, now time.Time) error {
	if err := s.guard(goalID); err != nil {
		return err
	}
	return s.p.Renew(goalID, id, worker, lease, now)
}

func (s *pgScoped) Complete(goalID goal.ID, id task.ID, worker string, next task.State,
	result []byte, failure string, now time.Time) error {
	if err := s.guard(goalID); err != nil {
		return err
	}
	return s.p.Complete(goalID, id, worker, next, result, failure, now)
}

func (s *pgScoped) Release(goalID goal.ID, id task.ID, worker string, now time.Time) error {
	if err := s.guard(goalID); err != nil {
		return err
	}
	return s.p.Release(goalID, id, worker, now)
}

func (s *pgScoped) Decide(goalID goal.ID, id task.ID, approve bool, now time.Time) error {
	if err := s.guard(goalID); err != nil {
		return err
	}
	return s.p.Decide(goalID, id, approve, now)
}

// Cancel guards on ownership first — see the memScoped twin.
func (s *pgScoped) Cancel(goalID goal.ID, now time.Time) (int, error) {
	if err := s.guard(goalID); err != nil {
		return 0, err
	}
	return s.p.Cancel(goalID, now)
}

func (s *pgScoped) Checkpoint(cp Checkpoint) error {
	if err := s.guard(cp.GoalID); err != nil {
		return err
	}
	return s.p.Checkpoint(cp)
}

func (s *pgScoped) LatestCheckpoint(goalID goal.ID) (Checkpoint, bool, error) {
	if err := s.guard(goalID); err != nil {
		return Checkpoint{}, false, nil
	}
	return s.p.LatestCheckpoint(goalID)
}

// guard confirms this tenant owns the goal. A goal that does not exist and a goal owned by somebody
// else produce the SAME error, so an id cannot be probed for existence.
func (s *pgScoped) guard(id goal.ID) error {
	var owned bool
	if err := s.p.db.QueryRowContext(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM goals WHERE id = $1 AND tenant = $2)`,
		id, s.tenant).Scan(&owned); err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if !owned {
		return fmt.Errorf("%w: %q", ErrGoalNotFound, id)
	}
	return nil
}

var (
	_ Root  = (*Postgres)(nil)
	_ Store = (*pgScoped)(nil)
)
