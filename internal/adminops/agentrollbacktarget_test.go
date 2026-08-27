package adminops_test

import (
	"context"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/herosagent"
)

// agentrollbacktarget_test.go fences the operator-facing half of the activation sentinel.
//
// `AgentVersionRow.RollbackTarget` is `passed && !v.Active()`. It is the reason a definition appears in
// the "roll back to a previous definition" list, and it is DECIDED BY THE PLATFORM rather than by the
// console — precisely so the control is offered exactly where the backend would accept it.
//
// # What it is guarding against
//
// That computation reads `Version.Active()`, which used to be `ActivatedAtMS != 0` over a field that
// discarded `sql.NullInt64.Valid`. A version stamped 0 satisfied the store's `WHERE activated_at_ms IS
// NOT NULL` — so it was SERVING — and reported `Active() == false`, so it would have been offered as
// something to roll back TO. Rolling back to the definition already serving is an operator taking an
// action during an incident that changes nothing, while the page tells them it was the fix.
//
// The store-level fences live in `internal/herosagent`; this asserts the consequence at the surface,
// because a fix that satisfied the predicate and left this expression reading a different field would
// pass those and still offer the control.

// twoVersions serves an active row and a stood-down one.
type twoVersions struct {
	active herosagent.Version
	other  herosagent.Version
}

func (f *twoVersions) Active(context.Context) (herosagent.Version, bool, error) {
	return f.active, true, nil
}

func (f *twoVersions) List(context.Context) ([]herosagent.Version, error) {
	return []herosagent.Version{f.active, f.other}, nil
}

func (f *twoVersions) Get(_ context.Context, hash string) (herosagent.Version, bool, error) {
	for _, v := range []herosagent.Version{f.active, f.other} {
		if v.ConfigHash == hash {
			return v, true, nil
		}
	}
	return herosagent.Version{}, false, nil
}

// activatedAt builds a version the store would return as serving.
func activatedAt(hash string, atMS int64) herosagent.Version {
	return herosagent.Version{
		ConfigHash: hash, RehearsalState: herosagent.RehearsalPassed, ActivatedAtMS: &atMS,
	}
}

// 🔴 THE SERVING VERSION IS NEVER OFFERED AS SOMETHING TO ROLL BACK TO.
//
// Run at a REAL timestamp and at EPOCH 0, because the second is the value that used to break it: the
// column accepts a 0, the store returns that row as active, and the Go predicate used to disagree.
func TestTheServingVersionIsNeverARollbackTarget(t *testing.T) {
	for _, c := range []struct {
		name string
		at   int64
	}{
		{"a real timestamp", 1_700_000_000_000},
		{
			// The database accepts this and its partial unique index gives the row the one active slot.
			// Any surface that disagrees is describing a different row than the one that is serving.
			"epoch 0 — which the column accepts and the store returns as active",
			0,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			store := &twoVersions{
				active: activatedAt("cfg-serving", c.at),
				other: herosagent.Version{
					ConfigHash: "cfg-previous", RehearsalState: herosagent.RehearsalPassed,
				},
			}
			svc, err := adminops.NewAgentService(h.exec, store, &fakePublisher{}, nil, nil, nil,
				herosagent.RunnerHosts{})
			if err != nil {
				t.Fatalf("NewAgentService: %v", err)
			}

			view, err := svc.Overview(h.ctx(adminrbac.RoleSuperadmin))
			if err != nil {
				t.Fatalf("Overview: %v", err)
			}

			byHash := map[string]adminops.AgentVersionRow{}
			for _, r := range view.Versions {
				byHash[r.ConfigHash] = r
			}
			serving, ok := byHash["cfg-serving"]
			if !ok {
				t.Fatal("the serving version is missing from the versions list")
			}
			if !serving.Active {
				t.Errorf("the version the store returned as ACTIVE renders as not serving. The overview " +
					"would then show `serving_config_hash` set with no row saying `serving` — two answers " +
					"to one question, on one page.")
			}
			if serving.RollbackTarget {
				t.Errorf("the SERVING definition is offered as something to roll back TO. During an " +
					"incident that is an operator taking an action that changes nothing while the page " +
					"tells them it was the fix.")
			}

			// 🔴 ANTI-VACUITY: the previous version IS offered, or the control has no targets and this
			// test would pass over a list that offers nothing at all.
			previous, ok := byHash["cfg-previous"]
			if !ok {
				t.Fatal("the previous version is missing from the versions list")
			}
			if !previous.RollbackTarget {
				t.Error("a passed, non-serving definition is NOT offered as a rollback target, so the " +
					"assertion above passed over an empty list — and rollback has nothing to press")
			}
			if previous.Active {
				t.Error("a version the store did not return as active renders as serving")
			}

			// And the overview names the serving hash, from the same row.
			if view.Serving != "cfg-serving" {
				t.Errorf("the overview says %q is serving, want cfg-serving", view.Serving)
			}
		})
	}
}
