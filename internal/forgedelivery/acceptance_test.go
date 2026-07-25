package forgedelivery_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/entitlement"
	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
)

// acceptance_test.go is the 12a acceptance gate (tasks 7.1–7.9). Where §2/§4/§5 prove a mechanism,
// these prove the PROPERTY at acceptance rigor — the concurrency case run to stress, the gate asserted
// from every entry point, isolation asserted end to end. A property that only its unit test has seen is
// a property that has been demonstrated once, under the conditions most favorable to it.

// 7.1 🔴 — idempotency under concurrency, run to stress. A single concurrent round can pass by luck;
// the race that produces duplicates is intermittent, so this repeats the whole contended open many
// times and asserts EXACTLY ONE pull request and EXACTLY ONE 'opened' every time.
func TestAccept_IdempotencyUnderConcurrency(t *testing.T) {
	const rounds, racers = 25, 8
	for round := 0; round < rounds; round++ {
		h := newHarness(t)
		ctx := context.Background()
		r := route(fd.ModeCI)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var created int
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := h.d.Deliver(ctx, proposal(entitlement.LevelAssisted), r, h.forge)
				if err != nil {
					t.Errorf("round %d: delivery errored: %v", round, err)
					return
				}
				if res.Created {
					mu.Lock()
					created++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if created != 1 {
			t.Fatalf("round %d: exactly one delivery must report Created, got %d", round, created)
		}
		if n, _ := h.forge.OpenPRCount(ctx, r.Target); n != 1 {
			t.Fatalf("round %d: open PRs = %d, want exactly 1", round, n)
		}
		id := fd.DeliveryID("ch1", "rev1", r.Target.Key())
		hist, _ := h.rec.History(ctx, id)
		opened := 0
		for _, e := range hist {
			if e.State == fd.StateOpened {
				opened++
			}
		}
		if opened != 1 {
			t.Fatalf("round %d: 'opened' entries = %d, want exactly 1", round, opened)
		}
	}
}

// 7.2 🔴 — halt fails closed. Made to fail: with the halt readable-and-armed, and readable-and-not, the
// armed and the unreadable cases BOTH withhold delivery. If neither could be made to withhold, the
// requirement would be decoration.
func TestAccept_HaltFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		halt fd.HaltReaderFunc
	}{
		{"armed", func(string) (bool, string, error) { return true, "incident", nil }},
		{"unreadable", func(string) (bool, string, error) { return false, "", errors.New("kv down") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			d := fd.NewDeliverer(h.gate, h.ents, c.halt, h.rec, 3)
			_, err := d.Deliver(context.Background(), proposal(entitlement.LevelAssisted), route(fd.ModeCI), h.forge)
			if err == nil {
				t.Fatalf("%s halt must withhold delivery", c.name)
			}
			if n, _ := h.forge.OpenPRCount(context.Background(), route(fd.ModeCI).Target); n != 0 {
				t.Errorf("%s halt: %d PR(s) opened — did not fail closed", c.name, n)
			}
		})
	}
}

// 7.3 — gate integrity from EVERY entry point. An unverified change must be undeliverable through the
// App-mode Deliver AND through the CI-mediated fetch (Pending/Prepare).
func TestAccept_GateIntegrityEveryEntryPoint(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r := route(fd.ModeCI)
	unverified := proposal(entitlement.LevelAssisted)
	unverified.ConfigHash = "no-verdict"

	// Entry point 1: App-mode Deliver.
	if _, err := h.d.Deliver(ctx, unverified, r, h.forge); !errors.Is(err, fd.ErrNotVerified) {
		t.Errorf("Deliver entry point admitted an unverified change: %v", err)
	}
	// Entry point 2: CI-mediated Prepare (the fetch leg).
	if _, err := h.d.Prepare(ctx, unverified, r); !errors.Is(err, fd.ErrNotVerified) {
		t.Errorf("Prepare entry point admitted an unverified change: %v", err)
	}
	if n, _ := h.forge.OpenPRCount(ctx, r.Target); n != 0 {
		t.Errorf("an unverified change reached the forge (%d)", n)
	}
}

// 7.5 — volume bound: a burst cannot exceed the bound; reaching it is a reported condition and the
// proposal is not discarded (nothing recorded, nothing lost).
func TestAccept_VolumeBound(t *testing.T) {
	h := newHarness(t) // bound 3
	ctx := context.Background()
	for _, wf := range []string{"w1", "w2", "w3"} {
		ch := "ch_" + wf
		h.gate.verdicts[ch+"|rev1"] = passingVerdict(ch, "rev1")
		p := proposal(entitlement.LevelAssisted)
		p.ConfigHash, p.ProposalID = ch, "p"+wf
		rt := &fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub, Target: fd.Target{Owner: "o", Repo: "mono", Base: "main", Workflow: wf}}
		if _, err := h.d.Deliver(ctx, p, rt, h.forge); err != nil {
			t.Fatalf("deliver %s: %v", wf, err)
		}
	}
	h.gate.verdicts["ch_w4|rev1"] = passingVerdict("ch_w4", "rev1")
	over := proposal(entitlement.LevelAssisted)
	over.ConfigHash, over.ProposalID = "ch_w4", "pw4"
	rt := &fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub, Target: fd.Target{Owner: "o", Repo: "mono", Base: "main", Workflow: "w4"}}
	_, err := h.d.Deliver(ctx, over, rt, h.forge)
	if !errors.Is(err, fd.ErrBoundReached) || !fd.IsReportedCondition(err) {
		t.Fatalf("over-bound delivery should be a reported ErrBoundReached, got %v", err)
	}
	// Not discarded: the over-bound delivery left no partial record.
	id := fd.DeliveryID("ch_w4", "rev1", rt.Target.Key())
	if _, ok, _ := h.rec.Head(ctx, id); ok {
		t.Errorf("an over-bound delivery left a record — it should be retained by the caller, not partially recorded")
	}
}

// 7.6 — visibility asserts as RENDERINGS (the served view), not just log lines. The Service produces a
// route CONDITION with a next action for both absence and revocation, which is what the console renders.
func TestAccept_VisibilityIsRendered(t *testing.T) {
	ctx := context.Background()
	// Absence: verified proposals, no route. RouteConditionFor reads the routes + pending only, so the
	// deliverer's gate is not exercised here.
	rec := deliveryrecord.NewMemStore()
	del := fd.NewDeliverer(&fakeGate{}, &fakeEnts{deliver: true}, okHalt(), rec, 3)
	svcAbsent := fd.NewService(del,
		&stubRoutes{route: nil},
		&stubPending{proposals: []fd.Proposal{{TenantID: "t1", ConfigHash: "c1", SourceRevision: "r1"}}},
		"https://c.example")
	cond, err := svcAbsent.RouteConditionFor(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if cond.Kind != fd.RouteAbsent || cond.NextAction == "" {
		t.Errorf("absence must render as a condition with a next action, got %+v", cond)
	}

	// Revocation: capability reports revoked.
	svcRevoked := fd.NewService(del,
		&stubRoutes{capabilityKind: fd.RouteRevoked, capabilityDetail: "app removed"},
		&stubPending{}, "https://c.example")
	cond2, err := svcRevoked.RouteConditionFor(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if cond2.Kind != fd.RouteRevoked || cond2.NextAction == "" {
		t.Errorf("revocation must render as a condition with a next action, got %+v", cond2)
	}
}

// 7.7 — merge observation: a merge records `merged`; a close-without-merge does NOT; a later revert
// appends a further state. (7.4 credential-absence lives in credential_absence_test.go, in this same
// test binary; the acceptance gate runs it alongside these.)
func TestAccept_MergeObservation(t *testing.T) {
	ctx := context.Background()
	// Deliver two, then merge one and close the other.
	h := newHarness(t)
	h.gate.verdicts["chB|revB"] = passingVerdict("chB", "revB")
	a, _ := h.d.Deliver(ctx, proposal(entitlement.LevelAssisted), route(fd.ModeCI), h.forge)
	pB := proposal(entitlement.LevelAssisted)
	pB.ConfigHash, pB.SourceRevision, pB.ProposalID = "chB", "revB", "pB"
	// A different target so supersession does not close A.
	rB := &fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub, Target: fd.Target{Owner: "o", Repo: "r", Base: "main", Workflow: "wB"}}
	b, _ := h.d.Deliver(ctx, pB, rB, h.forge)

	obs := fd.NewMergeObserver(h.rec)
	if err := obs.ObserveMerge(ctx, a.DeliveryID, "commitA", "ci"); err != nil {
		t.Fatal(err)
	}
	if err := obs.ObserveClose(ctx, b.DeliveryID, "app-webhook", "declined"); err != nil {
		t.Fatal(err)
	}
	// A records merged; B records closed, never merged.
	ha, _, _ := h.rec.Head(ctx, a.DeliveryID)
	if ha.State != fd.StateMerged || ha.MergeCommit == "" {
		t.Errorf("A head = %+v, want merged with commit", ha)
	}
	histB, _ := h.rec.History(ctx, b.DeliveryID)
	for _, e := range histB {
		if e.State == fd.StateMerged {
			t.Errorf("B (closed without merge) was recorded as merged")
		}
	}
	// A revert appends a further state; the merged state stays.
	if err := obs.ObserveRevert(ctx, a.DeliveryID, "revertA", "ci"); err != nil {
		t.Fatal(err)
	}
	histA, _ := h.rec.History(ctx, a.DeliveryID)
	var mergedSeen, revertSeen bool
	for _, e := range histA {
		mergedSeen = mergedSeen || e.State == fd.StateMerged
		revertSeen = revertSeen || e.State == fd.StateReverted
	}
	if !mergedSeen || !revertSeen {
		t.Errorf("A history must retain merged and append reverted: %+v", histA)
	}
}

// 7.8 — isolation: a forge outage for one repository blocks no other and loses no proposal.
func TestAccept_Isolation(t *testing.T) {
	h := newHarness(t)
	h.gate.verdicts["ch1|r2"] = passingVerdict("ch1", "r2")
	down := fd.NewInMemForge(fd.ForgeGitHub, false)
	down.SetDown(true)
	up := fd.NewInMemForge(fd.ForgeGitHub, false)
	p2 := proposal(entitlement.LevelAssisted)
	p2.SourceRevision, p2.ProposalID = "r2", "p2"
	res := h.d.DeliverBatch(context.Background(), []fd.Job{
		{Proposal: proposal(entitlement.LevelAssisted), Route: &fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub, Target: fd.Target{Owner: "o", Repo: "down", Base: "main"}}, Forge: down},
		{Proposal: p2, Route: &fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub, Target: fd.Target{Owner: "o", Repo: "up", Base: "main"}}, Forge: up},
	})
	if res[0].Err == nil || !errors.Is(res[0].Err, fd.ErrForgeUnavailable) {
		t.Errorf("the down repo should report a forge outage, got %v", res[0].Err)
	}
	if res[1].Err != nil || !res[1].Result.Created {
		t.Errorf("the healthy repo must deliver despite the other's outage, got %v", res[1].Err)
	}
	if res[0].Job.Proposal.ProposalID == "" {
		t.Errorf("the failed delivery lost its proposal")
	}
}

// 7.9 — entitlement: delivery below Team refused; auto-merge below Enterprise refused. Both server-side.
func TestAccept_Entitlement(t *testing.T) {
	// Below Team.
	h := newHarness(t)
	h.ents.deliver = false
	if _, err := h.d.Deliver(context.Background(), proposal(entitlement.LevelAssisted), route(fd.ModeCI), h.forge); !isNotEntitled(err) {
		t.Errorf("delivery below Team must be refused, got %v", err)
	}
	// Auto-merge below Enterprise.
	h2 := newHarness(t)
	h2.ents.deliver = true
	h2.ents.merge = false
	_, err := h2.d.Deliver(context.Background(), proposal(entitlement.LevelAutonomous), route(fd.ModeApp), fd.NewInMemForge(fd.ForgeGitHub, true))
	if !isNotEntitled(err) {
		t.Errorf("auto-merge below Enterprise must be refused, got %v", err)
	}
}

func isNotEntitled(err error) bool {
	var ne *fd.NotEntitledError
	return errors.As(err, &ne)
}

// ── small stubs for the visibility acceptance test ────────────────────────────

func okHalt() fd.HaltReaderFunc { return func(string) (bool, string, error) { return false, "", nil } }

type stubRoutes struct {
	route            *fd.Route
	capabilityKind   fd.RouteConditionKind
	capabilityDetail string
}

func (s *stubRoutes) RouteFor(context.Context, string, string) (*fd.Route, error) { return s.route, nil }
func (s *stubRoutes) Capability(context.Context, string) (fd.RouteConditionKind, string, error) {
	return s.capabilityKind, s.capabilityDetail, nil
}

type stubPending struct{ proposals []fd.Proposal }

func (s *stubPending) PendingVerified(context.Context, string) ([]fd.Proposal, error) {
	return s.proposals, nil
}
