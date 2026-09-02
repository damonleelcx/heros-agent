package store

import (
	"fmt"
	"time"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/task"
)

// scoped.go binds an in-memory store to one tenant.
//
// The Postgres implementation does its scoping in SQL; this one filters in Go. Both must behave
// identically, which the conformance suite asserts — an isolation guarantee that holds in one
// implementation and not the other is worth nothing, because production uses the other one.

// For returns a tenant-scoped view of the in-memory store.
func (m *Memory) For(tenant string) Store { return &memScoped{m: m, tenant: tenant} }

type memScoped struct {
	m      *Memory
	tenant string
}

// ErrCrossTenant means a request named an object belonging to somebody else.
//
// 🔴 Deliberately indistinguishable from "not found" at the API boundary. Telling a caller that a goal
// exists but belongs to another tenant confirms the id is real, which is a probe that turns a guessable
// identifier into an enumeration of everybody's data.
var ErrCrossTenant = ErrGoalNotFound

// owns reports whether this tenant owns the goal, treating a missing goal and another tenant's goal
// identically.
func (s *memScoped) owns(id goal.ID) bool {
	g, ok := s.m.goals[id]
	return ok && g.Tenant == s.tenant
}

func (s *memScoped) CreateGoal(g *goal.Goal) error {
	if s.tenant == "" {
		return fmt.Errorf("store: refusing to create a goal with no tenant")
	}
	// 🔴 The tenant is IMPOSED, not trusted from the object. A caller that sets it themselves is a caller
	// who can set it to somebody else.
	g.Tenant = s.tenant
	return s.m.CreateGoal(g)
}

func (s *memScoped) LoadGoal(id goal.ID) (*goal.Goal, error) {
	s.m.mu.Lock()
	ok := s.owns(id)
	s.m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrGoalNotFound, id)
	}
	return s.m.LoadGoal(id)
}

func (s *memScoped) SaveGoal(g *goal.Goal) error {
	s.m.mu.Lock()
	ok := s.owns(g.ID)
	s.m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrGoalNotFound, g.ID)
	}
	g.Tenant = s.tenant
	return s.m.SaveGoal(g)
}

func (s *memScoped) ListGoals(state goal.State) ([]*goal.Goal, error) {
	all, err := s.m.ListGoals(state)
	if err != nil {
		return nil, err
	}
	var out []*goal.Goal
	for _, g := range all {
		if g.Tenant == s.tenant {
			out = append(out, g)
		}
	}
	return out, nil
}

func (s *memScoped) LatestGoal(_ string) (*goal.Goal, bool, error) {
	// 🔴 The argument is IGNORED. The scope decides, so a caller passing another tenant's name gets their
	// own data rather than somebody else's — the parameter survives only because the interface predates
	// scoping, and honouring it would reintroduce exactly what this type removes.
	return s.m.LatestGoal(s.tenant)
}

func (s *memScoped) SaveDAG(d *task.DAG) error {
	s.m.mu.Lock()
	ok := s.owns(goal.ID(d.GoalID))
	s.m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrGoalNotFound, d.GoalID)
	}
	return s.m.SaveDAG(d)
}

func (s *memScoped) LoadDAG(goalID goal.ID) (*task.DAG, error) {
	s.m.mu.Lock()
	ok := s.owns(goalID)
	s.m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: no DAG for goal %q", ErrGoalNotFound, goalID)
	}
	return s.m.LoadDAG(goalID)
}

func (s *memScoped) Claim(goalID goal.ID, worker string, lease time.Duration, now time.Time) (*task.Task, error) {
	s.m.mu.Lock()
	ok := s.owns(goalID)
	s.m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrGoalNotFound, goalID)
	}
	return s.m.Claim(goalID, worker, lease, now)
}

func (s *memScoped) Renew(goalID goal.ID, id task.ID, worker string, lease time.Duration, now time.Time) error {
	if err := s.guard(goalID); err != nil {
		return err
	}
	return s.m.Renew(goalID, id, worker, lease, now)
}

func (s *memScoped) Complete(goalID goal.ID, id task.ID, worker string, next task.State,
	result []byte, failure string, now time.Time) error {
	if err := s.guard(goalID); err != nil {
		return err
	}
	return s.m.Complete(goalID, id, worker, next, result, failure, now)
}

func (s *memScoped) Release(goalID goal.ID, id task.ID, worker string, now time.Time) error {
	if err := s.guard(goalID); err != nil {
		return err
	}
	return s.m.Release(goalID, id, worker, now)
}

func (s *memScoped) Decide(goalID goal.ID, id task.ID, approve bool, now time.Time) error {
	if err := s.guard(goalID); err != nil {
		return err
	}
	return s.m.Decide(goalID, id, approve, now)
}

// Cancel guards on ownership first: cancelling somebody else's run is exactly the cross-tenant write
// `guard` exists to refuse, and it is aliased to not-found so a probe cannot confirm the id is real.
func (s *memScoped) Cancel(goalID goal.ID, now time.Time) (int, error) {
	if err := s.guard(goalID); err != nil {
		return 0, err
	}
	return s.m.Cancel(goalID, now)
}

func (s *memScoped) Checkpoint(cp Checkpoint) error {
	if err := s.guard(cp.GoalID); err != nil {
		return err
	}
	return s.m.Checkpoint(cp)
}

func (s *memScoped) LatestCheckpoint(goalID goal.ID) (Checkpoint, bool, error) {
	if err := s.guard(goalID); err != nil {
		return Checkpoint{}, false, nil // absent, not an error: the goal does not exist for this tenant
	}
	return s.m.LatestCheckpoint(goalID)
}

func (s *memScoped) guard(id goal.ID) error {
	s.m.mu.Lock()
	ok := s.owns(id)
	s.m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrGoalNotFound, id)
	}
	return nil
}

var (
	_ Root  = (*Memory)(nil)
	_ Store = (*memScoped)(nil)
)
