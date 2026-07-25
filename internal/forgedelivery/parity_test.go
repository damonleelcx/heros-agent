package forgedelivery_test

import (
	"context"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/entitlement"
	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
)

// 9.1 🔴 — CONTENT PARITY, verified by byte-comparison rather than by review. The same proposal
// delivered through the CI-mediated mode and the hosted-App mode must produce the byte-identical
// pull-request body — the difference between the modes is which credential opened it, not what it says.
// Two renderings drift; a byte-compare cannot.
func TestParity_PullRequestContentIsByteIdentical(t *testing.T) {
	ctx := context.Background()

	// One proposal, two routes differing ONLY in mode.
	build := func(mode fd.Mode) (*fd.Deliverer, *deliveryrecord.MemStore) {
		rec := deliveryrecord.NewMemStore()
		del := fd.NewDeliverer(newGateWith("ch1", "rev1"), &fakeEnts{deliver: true}, okHalt(), rec, 5)
		return del, rec
	}
	prop := proposal(entitlement.LevelAssisted)

	// CI-mediated: Prepare (served to CI) → open with the CI runner's writer.
	delCI, _ := build(fd.ModeCI)
	ciRoute := &fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub, Target: fd.Target{Owner: "o", Repo: "r", Base: "main"}}
	prepCI, err := delCI.Prepare(ctx, prop, ciRoute)
	if err != nil {
		t.Fatalf("CI prepare: %v", err)
	}
	ciForge := fd.NewInMemForge(fd.ForgeGitHub, false)
	prCI, _, err := fd.OpenFromPrepared(ctx, ciForge, prepCI, 5)
	if err != nil {
		t.Fatalf("CI open: %v", err)
	}
	ciBody, _ := ciForge.PRBody(prCI.Ref)

	// Hosted App: the platform opens with its own credentialed writer.
	delApp, _ := build(fd.ModeApp)
	appRoute := &fd.Route{Mode: fd.ModeApp, ForgeKind: fd.ForgeGitHub, Target: fd.Target{Owner: "o", Repo: "r", Base: "main"}}
	appForge := fd.NewInMemForge(fd.ForgeGitHub, true)
	resApp, err := delApp.Deliver(ctx, prop, appRoute, appForge)
	if err != nil {
		t.Fatalf("App deliver: %v", err)
	}
	appBody, _ := appForge.PRBody(resApp.PR.Ref)

	if ciBody == "" || appBody == "" {
		t.Fatalf("a body was empty (ci=%d app=%d bytes)", len(ciBody), len(appBody))
	}
	if ciBody != appBody {
		t.Errorf("PR content drifted between modes.\n--- CI ---\n%s\n--- APP ---\n%s", ciBody, appBody)
	}
	// The Prepared bodies (the single render both modes share) are also identical.
	if prepCI.Body != appBody {
		t.Errorf("the Prepared body and the opened App body diverged")
	}
}

// 9.2 — re-run the core 12a invariants against the HOSTED mode: the same properties, asserted through
// the AppForgeWriter (standing-credential path) rather than the CI writer. If any invariant held only
// for the CI path, this catches it.
func TestParity_HostedModeReRun(t *testing.T) {
	ctx := context.Background()

	newApp := func() (*fd.Deliverer, *deliveryrecord.MemStore, *fd.AppForgeWriter, *fd.InMemForge, *oneGate) {
		rec := deliveryrecord.NewMemStore()
		g := &oneGate{}
		del := fd.NewDeliverer(g, &fakeEnts{deliver: true, merge: true}, okHalt(), rec, 3)
		store := fd.NewInstallationStore()
		_ = store.Install(fd.Installation{InstallationID: "i1", TenantID: "t1",
			Repositories: []string{"o/r"}, Permissions: fd.LeastPrivilegePermissions()})
		secrets := fd.NewMemSecretsManager()
		secrets.Put("i1", secretToken)
		delegate := fd.NewInMemForge(fd.ForgeGitHub, true)
		w := fd.NewAppForgeWriter(store, secrets, "t1", func(string) fd.ForgeWriter { return delegate })
		return del, rec, w, delegate, g
	}
	appRoute := &fd.Route{Mode: fd.ModeApp, ForgeKind: fd.ForgeGitHub, Target: fd.Target{Owner: "o", Repo: "r", Base: "main"}}

	// Gate integrity (App path): unverified is undeliverable.
	t.Run("gate", func(t *testing.T) {
		del, _, w, _, _ := newApp() // gate allows nothing
		_, err := del.Deliver(ctx, proposal(entitlement.LevelAssisted), appRoute, w)
		if err == nil {
			t.Errorf("hosted mode admitted an unverified change")
		}
	})

	// Idempotency under concurrency (App path): exactly one PR.
	t.Run("idempotency", func(t *testing.T) {
		del, _, w, delegate, g := newApp()
		g.ch, g.rev = "ch1", "rev1"
		var wg sync.WaitGroup
		var mu sync.Mutex
		created := 0
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := del.Deliver(ctx, proposal(entitlement.LevelAssisted), appRoute, w)
				if err == nil && res.Created {
					mu.Lock()
					created++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if created != 1 {
			t.Errorf("hosted concurrent delivery: created=%d, want 1", created)
		}
		if n, _ := delegate.OpenPRCount(ctx, appRoute.Target); n != 1 {
			t.Errorf("hosted concurrent delivery: open PRs=%d, want 1", n)
		}
	})

	// Autonomous merge (App path): entitled → merged, recorded from observation.
	t.Run("autonomous-merge", func(t *testing.T) {
		del, rec, w, _, g := newApp()
		g.ch, g.rev = "ch1", "rev1"
		res, err := del.Deliver(ctx, proposal(entitlement.LevelAutonomous), appRoute, w)
		if err != nil {
			t.Fatalf("hosted autonomous deliver: %v", err)
		}
		if !res.Merged {
			t.Errorf("hosted autonomous delivery did not merge")
		}
		hist, _ := rec.History(ctx, res.DeliveryID)
		if hist[len(hist)-1].State != fd.StateMerged {
			t.Errorf("hosted merge not recorded")
		}
	})
}
