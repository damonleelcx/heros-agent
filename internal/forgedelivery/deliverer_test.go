package forgedelivery_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/evalstats"
	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/verification"
)

// ── Test doubles for the collaborators the core enforces preconditions against ─

type fakeGate struct {
	verdicts map[string]verification.Verdict
	err      error
}

func (g *fakeGate) Verdict(_ context.Context, tenant, ch, rev string) (verification.Verdict, bool, error) {
	if g.err != nil {
		return verification.Verdict{}, false, g.err
	}
	v, ok := g.verdicts[ch+"|"+rev]
	return v, ok, nil
}

type fakeEnts struct {
	deliver bool // FeatureAssistedPR allowed
	merge   bool // FeatureAutoMerge allowed
	err     error
}

func (e *fakeEnts) CheckEntitlement(_ string, f plancfg.Feature, _ entitlement.AutomationLevel) (entitlement.Decision, error) {
	if e.err != nil {
		return entitlement.Decision{}, e.err
	}
	allowed := false
	switch f {
	case plancfg.FeatureAssistedPR:
		allowed = e.deliver
	case plancfg.FeatureAutoMerge:
		allowed = e.merge
	}
	d := entitlement.Decision{Allowed: allowed, Feature: f}
	if !allowed {
		d.Reason = "not included in this plan"
		d.ReasonCode = entitlement.ReasonNotEntitled
		d.UpgradePlanName = "Team"
	}
	return d, nil
}

func passingVerdict(ch, rev string) verification.Verdict {
	return verification.Verdict{
		ConfigHash: ch, Metric: "quality",
		Delta:          evalstats.Interval{Mean: 0.12, Low: 0.04, High: 0.20, Confidence: 0.95},
		Significant:    true,
		HeldOut:        true,
		RegressionPass: true,
		GateResult:     verification.GatePass,
		CasesFixed:     []string{"c1", "c2"},
	}
}

// harness wires a deliverer against fakes and returns the pieces a test inspects.
type harness struct {
	d     *fd.Deliverer
	gate  *fakeGate
	ents  *fakeEnts
	rec   *deliveryrecord.MemStore
	halt  fd.HaltReaderFunc
	forge *fd.InMemForge
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	gate := &fakeGate{verdicts: map[string]verification.Verdict{"ch1|rev1": passingVerdict("ch1", "rev1")}}
	ents := &fakeEnts{deliver: true, merge: false}
	rec := deliveryrecord.NewMemStore()
	halt := fd.HaltReaderFunc(func(string) (bool, string, error) { return false, "", nil })
	d := fd.NewDeliverer(gate, ents, halt, rec, 3)
	return &harness{d: d, gate: gate, ents: ents, rec: rec, halt: halt, forge: fd.NewInMemForge(fd.ForgeGitHub, false)}
}

func route(mode fd.Mode) *fd.Route {
	return &fd.Route{Mode: mode, ForgeKind: fd.ForgeGitHub, Target: fd.Target{Owner: "o", Repo: "r", Base: "main"}}
}

func proposal(level fd.Level) fd.Proposal {
	return fd.Proposal{
		TenantID: "t1", ProposalID: "p1", ConfigHash: "ch1", SourceRevision: "rev1",
		Title: "Optimize the extraction node", DiffPatch: "--- a\n+++ b\n", Level: level,
		ConsoleRef: "https://console.example/evidence/ch1",
	}
}

// 2.1 + happy path: a verified, entitled, un-halted proposal opens a PR and records 'opened'.
func TestDeliver_HappyPath(t *testing.T) {
	h := newHarness(t)
	res, err := h.d.Deliver(context.Background(), proposal(entitlement.LevelAssisted), route(fd.ModeCI), h.forge)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !res.Created || res.PR.Ref == "" {
		t.Fatalf("expected a created PR, got %+v", res)
	}
	head, ok, _ := h.rec.Head(context.Background(), res.DeliveryID)
	if !ok || head.State != fd.StateOpened {
		t.Fatalf("expected recorded 'opened', got %+v ok=%v", head, ok)
	}
	if head.Mode != fd.ModeCI {
		t.Errorf("mode not recorded: %q", head.Mode)
	}
}

// 2.2: an unverified change is undeliverable, from the one entry point.
func TestDeliver_GateNotPassed(t *testing.T) {
	h := newHarness(t)
	// A verdict that ran the gate but did not pass.
	fail := passingVerdict("ch1", "rev1")
	fail.GateResult = verification.GateFailSig
	h.gate.verdicts["ch1|rev1"] = fail
	_, err := h.d.Deliver(context.Background(), proposal(entitlement.LevelAssisted), route(fd.ModeCI), h.forge)
	if !errors.Is(err, fd.ErrNotVerified) {
		t.Fatalf("want ErrNotVerified, got %v", err)
	}
	if n, _ := h.forge.OpenPRCount(context.Background(), route(fd.ModeCI).Target); n != 0 {
		t.Errorf("a PR was opened for an unverified change (%d open)", n)
	}
}

// 2.2 (variant): a change with NO recorded verdict is also undeliverable — absence is not a pass.
func TestDeliver_NoVerdict(t *testing.T) {
	h := newHarness(t)
	p := proposal(entitlement.LevelAssisted)
	p.ConfigHash = "unknown"
	_, err := h.d.Deliver(context.Background(), p, route(fd.ModeCI), h.forge)
	if !errors.Is(err, fd.ErrNotVerified) {
		t.Fatalf("want ErrNotVerified for a change with no verdict, got %v", err)
	}
}

// 6.1 seam: no route is a distinct reported condition, not silence.
func TestDeliver_NoRoute(t *testing.T) {
	h := newHarness(t)
	_, err := h.d.Deliver(context.Background(), proposal(entitlement.LevelAssisted), nil, h.forge)
	if !errors.Is(err, fd.ErrNoRoute) {
		t.Fatalf("want ErrNoRoute, got %v", err)
	}
	if !fd.IsReportedCondition(err) {
		t.Errorf("ErrNoRoute should be a reported condition")
	}
}

// 2.1 / 7.9: delivery below Team is refused server-side.
func TestDeliver_NotEntitled(t *testing.T) {
	h := newHarness(t)
	h.ents.deliver = false
	_, err := h.d.Deliver(context.Background(), proposal(entitlement.LevelAssisted), route(fd.ModeCI), h.forge)
	var ne *fd.NotEntitledError
	if !errors.As(err, &ne) {
		t.Fatalf("want NotEntitledError, got %v", err)
	}
	if ne.Decision.UpgradePlanName == "" {
		t.Errorf("denial should carry the upgrade path")
	}
}

// 2.7: an armed halt stops delivery.
func TestDeliver_HaltArmed(t *testing.T) {
	h := newHarness(t)
	d := fd.NewDeliverer(h.gate, h.ents, fd.HaltReaderFunc(func(string) (bool, string, error) {
		return true, "incident 42", nil
	}), h.rec, 3)
	_, err := d.Deliver(context.Background(), proposal(entitlement.LevelAssisted), route(fd.ModeCI), h.forge)
	var he *fd.HaltedError
	if !errors.As(err, &he) {
		t.Fatalf("want HaltedError, got %v", err)
	}
	if n, _ := h.forge.OpenPRCount(context.Background(), route(fd.ModeCI).Target); n != 0 {
		t.Errorf("delivered while halted (%d open)", n)
	}
}

// 2.7 / 7.2: an unreadable halt state fails closed — delivery does NOT proceed.
func TestDeliver_HaltUnreadable_FailsClosed(t *testing.T) {
	h := newHarness(t)
	d := fd.NewDeliverer(h.gate, h.ents, fd.HaltReaderFunc(func(string) (bool, string, error) {
		return false, "", errors.New("kv store unreachable")
	}), h.rec, 3)
	_, err := d.Deliver(context.Background(), proposal(entitlement.LevelAssisted), route(fd.ModeCI), h.forge)
	if !errors.Is(err, fd.ErrHaltUnreadable) {
		t.Fatalf("want ErrHaltUnreadable (fail closed), got %v", err)
	}
	if n, _ := h.forge.OpenPRCount(context.Background(), route(fd.ModeCI).Target); n != 0 {
		t.Errorf("delivered despite an unreadable halt (%d open) — did NOT fail closed", n)
	}
}

// 2.3: a retried delivery updates the existing PR rather than opening a second.
func TestDeliver_Idempotent_Retry(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r := route(fd.ModeCI)
	first, err := h.d.Deliver(ctx, proposal(entitlement.LevelAssisted), r, h.forge)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := h.d.Deliver(ctx, proposal(entitlement.LevelAssisted), r, h.forge)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.Created {
		t.Errorf("re-delivery opened a new PR instead of updating")
	}
	if first.PR.Ref != second.PR.Ref {
		t.Errorf("re-delivery targeted a different PR: %q vs %q", first.PR.Ref, second.PR.Ref)
	}
	if n, _ := h.forge.OpenPRCount(ctx, r.Target); n != 1 {
		t.Errorf("open PRs after retry = %d, want 1", n)
	}
}

// 2.3 / 7.1: concurrent deliveries of the same change leave exactly one PR and exactly one 'opened'.
func TestDeliver_Idempotent_Concurrent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r := route(fd.ModeCI)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	created := make([]bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := h.d.Deliver(ctx, proposal(entitlement.LevelAssisted), r, h.forge)
			errs[i] = err
			created[i] = res.Created
		}(i)
	}
	wg.Wait()
	nCreated := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("concurrent delivery %d errored: %v", i, errs[i])
		}
		if created[i] {
			nCreated++
		}
	}
	if nCreated != 1 {
		t.Errorf("exactly one delivery should report Created; got %d", nCreated)
	}
	if openN, _ := h.forge.OpenPRCount(ctx, r.Target); openN != 1 {
		t.Errorf("open PRs after concurrent delivery = %d, want 1", openN)
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
		t.Errorf("'opened' entries = %d, want exactly 1", opened)
	}
}

// 2.4: a newer verified proposal for the same target closes the older PR as superseded, with a reason.
func TestDeliver_Supersession(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	r := route(fd.ModeCI)
	h.gate.verdicts["ch2|rev2"] = passingVerdict("ch2", "rev2")

	old, err := h.d.Deliver(ctx, proposal(entitlement.LevelAssisted), r, h.forge)
	if err != nil {
		t.Fatalf("old delivery: %v", err)
	}
	newer := proposal(entitlement.LevelAssisted)
	newer.ConfigHash, newer.SourceRevision, newer.ProposalID = "ch2", "rev2", "p2"
	res, err := h.d.Deliver(ctx, newer, r, h.forge)
	if err != nil {
		t.Fatalf("newer delivery: %v", err)
	}
	if len(res.Superseded) != 1 {
		t.Fatalf("expected 1 superseded delivery, got %v", res.Superseded)
	}
	if st, _ := h.forge.PRState(old.PR.Ref); st != "closed" {
		t.Errorf("old PR state = %q, want closed", st)
	}
	oldHead, _, _ := h.rec.Head(ctx, old.DeliveryID)
	if oldHead.State != fd.StateSuperseded || oldHead.Reason == "" {
		t.Errorf("old delivery head = %+v, want superseded with a reason", oldHead)
	}
	// Exactly one candidate remains open for the decision.
	openHeads, _ := h.rec.OpenForTarget(ctx, "t1", r.Target.Key())
	if len(openHeads) != 1 || openHeads[0].DeliveryID != res.DeliveryID {
		t.Errorf("open candidates = %+v, want only the newer one", openHeads)
	}
}

// 2.5: a burst cannot exceed the per-repository bound; reaching it is reported and the proposal kept.
//
// The bound is per REPOSITORY, across workflow targets — supersession keeps one open PR per target
// (one candidate per decision), so the several open PRs a bound governs are DISTINCT workflows on one
// monorepo, each with its own config. That is exactly the scenario the bound exists for.
func TestDeliver_Bound(t *testing.T) {
	h := newHarness(t) // bound is 3
	ctx := context.Background()
	// Three distinct workflows on one repository, each its own config, all opening a PR.
	for _, wf := range []string{"w1", "w2", "w3"} {
		ch := "ch_" + wf
		h.gate.verdicts[ch+"|rev1"] = passingVerdict(ch, "rev1")
		p := proposal(entitlement.LevelAssisted)
		p.ConfigHash, p.ProposalID = ch, "p_"+wf
		r := &fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub,
			Target: fd.Target{Owner: "o", Repo: "mono", Base: "main", Workflow: wf}}
		if _, err := h.d.Deliver(ctx, p, r, h.forge); err != nil {
			t.Fatalf("delivery %s: %v", wf, err)
		}
	}
	if n, _ := h.forge.OpenPRCount(ctx, fd.Target{Owner: "o", Repo: "mono"}); n != 3 {
		t.Fatalf("open PRs before bound = %d, want 3", n)
	}
	// A 4th distinct workflow exceeds the bound.
	h.gate.verdicts["ch_w4|rev1"] = passingVerdict("ch_w4", "rev1")
	p := proposal(entitlement.LevelAssisted)
	p.ConfigHash, p.ProposalID = "ch_w4", "p_w4"
	r4 := &fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub,
		Target: fd.Target{Owner: "o", Repo: "mono", Base: "main", Workflow: "w4"}}
	_, err := h.d.Deliver(ctx, p, r4, h.forge)
	if !errors.Is(err, fd.ErrBoundReached) {
		t.Fatalf("want ErrBoundReached, got %v", err)
	}
	if !fd.IsReportedCondition(err) {
		t.Errorf("ErrBoundReached should be a reported condition, not a discard")
	}
	if n, _ := h.forge.OpenPRCount(ctx, fd.Target{Owner: "o", Repo: "mono"}); n != 3 {
		t.Errorf("open PRs = %d, want the bound of 3 (nothing extra opened)", n)
	}
	// An UPDATE of an already-open delivery is still allowed at the bound (not a new open).
	p2 := proposal(entitlement.LevelAssisted)
	p2.ConfigHash, p2.ProposalID = "ch_w1", "p_w1"
	r1 := &fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub,
		Target: fd.Target{Owner: "o", Repo: "mono", Base: "main", Workflow: "w1"}}
	if _, err := h.d.Deliver(ctx, p2, r1, h.forge); err != nil {
		t.Errorf("updating an existing delivery at the bound should be allowed, got %v", err)
	}
}

// 2.6: below Autonomous the platform never merges.
func TestDeliver_NeverMergeBelowAutonomous(t *testing.T) {
	h := newHarness(t)
	res, err := h.d.Deliver(context.Background(), proposal(entitlement.LevelAssisted), route(fd.ModeCI), h.forge)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if res.Merged {
		t.Fatalf("merged below Autonomous")
	}
	if st, _ := h.forge.PRState(res.PR.Ref); st != "open" {
		t.Errorf("PR state = %q, want open (a human merges)", st)
	}
}

// 2.6 / 7.9: under Autonomous, an entitled gate-passed change is merged and recorded from observation.
func TestDeliver_AutonomousMerges(t *testing.T) {
	h := newHarness(t)
	h.ents.merge = true // Enterprise
	res, err := h.d.Deliver(context.Background(), proposal(entitlement.LevelAutonomous), route(fd.ModeApp), fd.NewInMemForge(fd.ForgeGitHub, true))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if !res.Merged || res.MergeCommit == "" {
		t.Fatalf("expected an autonomous merge, got %+v", res)
	}
	hist, _ := h.rec.History(context.Background(), res.DeliveryID)
	last := hist[len(hist)-1]
	if last.State != fd.StateMerged || last.MergeCommit == "" {
		t.Errorf("merge not recorded from observation: %+v", last)
	}
}

// 2.6 / 7.9: Autonomous without the auto-merge entitlement is refused BEFORE anything opens.
func TestDeliver_AutonomousNotEntitled(t *testing.T) {
	h := newHarness(t)
	h.ents.merge = false
	_, err := h.d.Deliver(context.Background(), proposal(entitlement.LevelAutonomous), route(fd.ModeCI), h.forge)
	var ne *fd.NotEntitledError
	if !errors.As(err, &ne) {
		t.Fatalf("want NotEntitledError for un-entitled auto-merge, got %v", err)
	}
	if n, _ := h.forge.OpenPRCount(context.Background(), route(fd.ModeCI).Target); n != 0 {
		t.Errorf("a PR was opened for an un-entitled autonomous request (%d)", n)
	}
}

// 2.8: the platform writes only pull requests and their branches — nothing else.
func TestDeliver_WriteScope(t *testing.T) {
	h := newHarness(t)
	if _, err := h.d.Deliver(context.Background(), proposal(entitlement.LevelAssisted), route(fd.ModeCI), h.forge); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if other := h.forge.OtherWrites(); len(other) != 0 {
		t.Errorf("the platform performed non-PR writes: %v", other)
	}
}

// 2.9: one repository's forge outage blocks no other and loses no proposal.
func TestDeliverBatch_Isolation(t *testing.T) {
	h := newHarness(t)
	h.gate.verdicts["ch1|rev2"] = passingVerdict("ch1", "rev2")

	downForge := fd.NewInMemForge(fd.ForgeGitHub, false)
	downForge.SetDown(true)
	okForge := fd.NewInMemForge(fd.ForgeGitHub, false)

	badJob := fd.Job{Proposal: proposal(entitlement.LevelAssisted), Route: &fd.Route{
		Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub, Target: fd.Target{Owner: "o", Repo: "down", Base: "main"}}, Forge: downForge}
	good := proposal(entitlement.LevelAssisted)
	good.SourceRevision, good.ProposalID = "rev2", "p2"
	goodJob := fd.Job{Proposal: good, Route: &fd.Route{
		Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub, Target: fd.Target{Owner: "o", Repo: "up", Base: "main"}}, Forge: okForge}

	results := h.d.DeliverBatch(context.Background(), []fd.Job{badJob, goodJob})
	if results[0].Err == nil {
		t.Errorf("the down repository should have errored")
	}
	if !errors.Is(results[0].Err, fd.ErrForgeUnavailable) {
		t.Errorf("down repo error should be ErrForgeUnavailable, got %v", results[0].Err)
	}
	if results[1].Err != nil {
		t.Errorf("the healthy repository must not be blocked by the outage: %v", results[1].Err)
	}
	if !results[1].Result.Created {
		t.Errorf("the healthy repository should have delivered")
	}
	// The failed job's proposal is retained in its result — not lost.
	if results[0].Job.Proposal.ProposalID == "" {
		t.Errorf("the failed delivery lost its proposal")
	}
}
