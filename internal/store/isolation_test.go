package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
)

// roots returns both implementations as tenant roots.
func roots() []struct {
	name string
	open func(*testing.T) store.Root
} {
	return []struct {
		name string
		open func(*testing.T) store.Root
	}{
		{"memory", func(t *testing.T) store.Root { return store.NewMemory() }},
		{"postgres", func(t *testing.T) store.Root {
			t.Helper()
			s := newPostgres(t) // skips without a DSN
			pg, ok := s.(*store.Postgres)
			if !ok {
				t.Fatalf("expected a *store.Postgres, got %T", s)
			}
			return pg
		}},
	}
}

// seedFor creates a goal owned by one tenant and returns its id.
func seedFor(t *testing.T, root store.Root, tenant, prefix string, tasks ...*task.Task) goal.ID {
	t.Helper()
	s := root.For(tenant)
	now := time.Now().UTC().Truncate(time.Millisecond)
	id := uniqueGoalID(prefix)
	g := &goal.Goal{
		ID: id, Tenant: tenant, Intent: "assess", State: goal.Draft,
		Subject:   goal.Subject{RepoURL: "git@github.com:acme/bot.git", Revision: "abc123"},
		Ceilings:  ceilings(),
		Criteria:  []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 1}},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := g.Admit(now); err != nil {
		t.Fatalf("admit: %v", err)
	}
	if err := s.CreateGoal(g); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, tk := range tasks {
		tk.GoalID, tk.CreatedAt, tk.UpdatedAt = string(id), now, now
	}
	if len(tasks) > 0 {
		d, err := task.NewDAG(string(id), tasks)
		if err != nil {
			t.Fatalf("dag: %v", err)
		}
		if err := s.SaveDAG(d); err != nil {
			t.Fatalf("save dag: %v", err)
		}
	}
	return id
}

// TestATenantCannotReachAnotherTenantsData is THE isolation fence.
//
// # 🔴 Why every method, and not a representative sample
//
// Twelve of the fourteen store methods take a goal id and nothing else. Before scoping, a goal id was
// therefore sufficient to read, mutate, claim or approve any customer's work — and a sample would prove
// only that the sampled methods were fixed. The one that was missed is the one that gets used.
//
// Every method is exercised with a goal that belongs to somebody else, on both implementations, and an
// isolation guarantee that held in one and not the other would be worth nothing: production uses the
// other one.
func TestATenantCannotReachAnotherTenantsData(t *testing.T) {
	for _, r := range roots() {
		t.Run(r.name, func(t *testing.T) {
			postgresRan(r.name)
			root := r.open(t)

			// Tenant A owns a goal with one task.
			victim := seedFor(t, root, "tenant-a", "iso-a", pending("t1", "analyse"))
			attacker := root.For("tenant-b")
			now := time.Now().UTC()

			// Every read.
			if _, err := attacker.LoadGoal(victim); !errors.Is(err, store.ErrGoalNotFound) {
				t.Errorf("LoadGoal reached another tenant's goal: %v", err)
			}
			if _, err := attacker.LoadDAG(victim); !errors.Is(err, store.ErrGoalNotFound) {
				t.Errorf("LoadDAG reached another tenant's DAG: %v", err)
			}
			if _, ok, _ := attacker.LatestCheckpoint(victim); ok {
				t.Error("LatestCheckpoint reached another tenant's checkpoint")
			}

			// Every mutation.
			stolen := &goal.Goal{ID: victim, Tenant: "tenant-b", State: goal.Running}
			if err := attacker.SaveGoal(stolen); !errors.Is(err, store.ErrGoalNotFound) {
				t.Errorf("SaveGoal overwrote another tenant's goal: %v", err)
			}
			d, _ := task.NewDAG(string(victim), []*task.Task{pending("evil", "analyse")})
			if err := attacker.SaveDAG(d); !errors.Is(err, store.ErrGoalNotFound) {
				t.Errorf("SaveDAG wrote into another tenant's goal: %v", err)
			}
			if err := attacker.Checkpoint(store.Checkpoint{GoalID: victim, Iteration: 1, At: now}); !errors.Is(err, store.ErrGoalNotFound) {
				t.Errorf("Checkpoint wrote into another tenant's goal: %v", err)
			}

			// Every execution primitive. 🔴 Claim is the worst of these: it would hand another
			// customer's work to this tenant's worker, which then runs it with this tenant's budget and
			// writes the result where this tenant can read it.
			if _, err := attacker.Claim(victim, "attacker", time.Minute, now); !errors.Is(err, store.ErrGoalNotFound) {
				t.Errorf("Claim leased another tenant's task: %v", err)
			}
			if err := attacker.Renew(victim, "t1", "attacker", time.Minute, now); !errors.Is(err, store.ErrGoalNotFound) {
				t.Errorf("Renew touched another tenant's lease: %v", err)
			}
			if err := attacker.Complete(victim, "t1", "attacker", task.Succeeded, []byte("x"), "", now); !errors.Is(err, store.ErrGoalNotFound) {
				t.Errorf("Complete wrote a result into another tenant's task: %v", err)
			}
			if err := attacker.Release(victim, "t1", "attacker", now); !errors.Is(err, store.ErrGoalNotFound) {
				t.Errorf("Release touched another tenant's task: %v", err)
			}
			// 🔴 Decide is the approval gate. Reaching it across tenants means approving somebody else's
			// write to their own repository.
			if err := attacker.Decide(victim, "t1", true, now); !errors.Is(err, store.ErrGoalNotFound) {
				t.Errorf("Decide approved another tenant's gated task: %v", err)
			}
			// 🔴 Cancel is denial of service against another customer: one call halts a run they are
			// paying for and waiting on, and the run's own record would say a person stopped it — so the
			// customer would be looking for a colleague who did not do it.
			if _, err := attacker.Cancel(victim, now); !errors.Is(err, store.ErrGoalNotFound) {
				t.Errorf("Cancel stopped another tenant's run: %v", err)
			}

			// And the victim's data is untouched throughout.
			owner := root.For("tenant-a")
			g, err := owner.LoadGoal(victim)
			if err != nil {
				t.Fatalf("the owner can no longer read their own goal: %v", err)
			}
			if g.Tenant != "tenant-a" || g.State != goal.Running {
				t.Errorf("the goal was modified: tenant=%s state=%s", g.Tenant, g.State)
			}
			od, err := owner.LoadDAG(victim)
			if err != nil {
				t.Fatalf("the owner can no longer read their own DAG: %v", err)
			}
			if len(od.Tasks) != 1 || od.Tasks["t1"].State != task.Pending {
				t.Errorf("the DAG was modified: %d tasks", len(od.Tasks))
			}
		})
	}
}

// TestListingsAreScoped. A list is the easiest place to leak everything at once, and the hardest to
// notice: it returns a plausible answer, just somebody else's as well.
func TestListingsAreScoped(t *testing.T) {
	for _, r := range roots() {
		t.Run(r.name, func(t *testing.T) {
			postgresRan(r.name)
			root := r.open(t)
			mine := seedFor(t, root, "list-a", "iso-list-a")
			theirs := seedFor(t, root, "list-b", "iso-list-b")

			got, err := root.For("list-a").ListGoals("")
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			for _, g := range got {
				if g.Tenant != "list-a" {
					t.Errorf("listing returned a goal owned by %q", g.Tenant)
				}
				if g.ID == theirs {
					t.Error("listing returned another tenant's goal")
				}
			}
			var sawMine bool
			for _, g := range got {
				if g.ID == mine {
					sawMine = true
				}
			}
			if !sawMine {
				t.Error("listing did not return the tenant's own goal")
			}
		})
	}
}

// TestLatestGoalIgnoresACallerSuppliedTenant.
//
// 🔴 The parameter survives from before scoping existed. Honouring it would reintroduce exactly what the
// scoped store removes: a caller naming whichever tenant it liked.
func TestLatestGoalIgnoresACallerSuppliedTenant(t *testing.T) {
	for _, r := range roots() {
		t.Run(r.name, func(t *testing.T) {
			postgresRan(r.name)
			root := r.open(t)
			seedFor(t, root, "latest-a", "iso-lat-a")
			theirs := seedFor(t, root, "latest-b", "iso-lat-b")

			got, ok, err := root.For("latest-a").LatestGoal("latest-b")
			if err != nil {
				t.Fatalf("latest: %v", err)
			}
			if ok && got.ID == theirs {
				t.Fatal("a caller-supplied tenant overrode the scope and returned another tenant's goal")
			}
			if ok && got.Tenant != "latest-a" {
				t.Fatalf("returned a goal owned by %q", got.Tenant)
			}
		})
	}
}

// TestTheTenantIsImposedNotTrusted. A caller that sets the tenant on the object is a caller who can set
// it to somebody else's.
func TestTheTenantIsImposedNotTrusted(t *testing.T) {
	for _, r := range roots() {
		t.Run(r.name, func(t *testing.T) {
			postgresRan(r.name)
			root := r.open(t)
			now := time.Now().UTC()
			g := &goal.Goal{
				ID: uniqueGoalID("iso-impose"), Tenant: "somebody-else", Intent: "assess",
				State: goal.Draft, Subject: goal.Subject{RepoURL: "r", Revision: "v"},
				Ceilings: ceilings(), Criteria: []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 1}},
				CreatedAt: now, UpdatedAt: now,
			}
			if err := g.Admit(now); err != nil {
				t.Fatalf("admit: %v", err)
			}
			if err := root.For("real-owner").CreateGoal(g); err != nil {
				t.Fatalf("create: %v", err)
			}
			if g.Tenant != "real-owner" {
				t.Fatalf("the object's tenant was trusted: %q", g.Tenant)
			}
			if _, err := root.For("somebody-else").LoadGoal(g.ID); !errors.Is(err, store.ErrGoalNotFound) {
				t.Error("the tenant named on the object could read the goal")
			}
		})
	}
}

// TestAnEmptyTenantCannotCreateAnything. "" is the value an unset variable has, and treating it as a
// tenant would put unowned rows in the database that any scoped query might later match.
func TestAnEmptyTenantCannotCreateAnything(t *testing.T) {
	for _, r := range roots() {
		t.Run(r.name, func(t *testing.T) {
			postgresRan(r.name)
			root := r.open(t)
			now := time.Now().UTC()
			g := &goal.Goal{
				ID: uniqueGoalID("iso-empty"), Intent: "assess", State: goal.Draft,
				Subject: goal.Subject{RepoURL: "r", Revision: "v"}, Ceilings: ceilings(),
				Criteria:  []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 1}},
				CreatedAt: now, UpdatedAt: now,
			}
			_ = g.Admit(now)
			if err := root.For("").CreateGoal(g); err == nil {
				t.Fatal("a store scoped to no tenant created a goal")
			}
		})
	}
}
