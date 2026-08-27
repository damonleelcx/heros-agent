package herosagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/providercall"
)

// p36_cost_test.go is §5 — the spend ceiling, the rehearsal gate and rollback, on a definition that is
// a graph.

// 🔴 §5.1 / §9.10 — ADDING A NODE DOES NOT RAISE THE ASSESSMENT BUDGET.
//
// # What "does not raise the budget" can and cannot mean
//
// It cannot mean "total spend never exceeds the ceiling". No cap checked BEFORE a call can promise
// that: the call's cost is not known until it returns, so any pre-call ceiling is overshot by whatever
// the call that crossed it cost. The single-node runner has always had that property.
//
// What it MUST mean, and what is asserted here, is three things:
//
//  1. the CEILING ITSELF is the same number for one node and for many — no multiplier, no per-node
//     allowance, nothing that moves when the topology does;
//  2. spend does not GROW with the node count once the ceiling binds — four nodes and eight nodes
//     spend the same, because the extra nodes are never entered;
//  3. the overshoot is bounded by ONE node's call rather than by N. This is the assertion that goes
//     red when the pending-spend argument is removed: without it every node reads the same stale meter
//     and a four-node definition spends 4× a one-node one.
func TestAddingANodeDoesNotRaiseTheAssessmentBudget(t *testing.T) {
	ctx := context.Background()

	// A ceiling of 10 tokens. Each node's call costs 4 in + 4 out = 8.
	const ceiling = 10
	const perCall = 8
	type run struct {
		spend, calls int
		limit        int64
	}
	spendOf := func(t *testing.T, d Definition) run {
		t.Helper()
		caps := NewMemCapStore()
		if err := caps.Set(ctx, Cap{TenantID: "t1", MaxTokens: ceiling, Reason: "the fence"}); err != nil {
			t.Fatal(err)
		}
		meter := NewMemSpendStore()
		checker, err := NewCapChecker(caps, meter, func() int64 { return 1_000_000 })
		if err != nil {
			t.Fatal(err)
		}
		model := &fixedCostModel{in: 4, out: 4}
		r, err := NewRunner(model, NewMemInferenceStore(), 0.5, func() int64 { return 1_000_000 },
			WithCaps(checker, meter),
			WithNodeModels(func(Node) (Model, error) { return model, nil }))
		if err != nil {
			t.Fatal(err)
		}
		hash, err := d.ConfigHash()
		if err != nil {
			t.Fatal(err)
		}
		res, _ := r.Infer(ctx, inputFor(irWith([]string{"a", "b", "c"})),
			BindDefinition(hash, d), PlacementPlatform)
		// The ceiling as the checker itself reports it, with nothing pending — the number an operator
		// would read, independent of any run.
		v, cerr := checker.Check(ctx, "t1", ceiling)
		if cerr != nil {
			t.Fatal(cerr)
		}
		return run{spend: res.Usage.InputTokens + res.Usage.OutputTokens,
			calls: res.ProviderCalls, limit: v.Limit}
	}

	one := spendOf(t, goodDefinition())
	four := spendOf(t, nNodeDefinition(4))
	eight := spendOf(t, nNodeDefinition(8))

	// 1 — the ceiling did not move.
	if one.limit != four.limit || four.limit != eight.limit || four.limit != ceiling {
		t.Errorf("the ceiling reads %d / %d / %d for 1, 4 and 8 nodes (want %d each). A topology change "+
			"that moves a budget is the least visible way for a system to start spending more: nobody "+
			"edited a budget, and the number that moved is one nobody was watching.",
			one.limit, four.limit, eight.limit, ceiling)
	}

	// 2 — spend does not grow with the node count once the ceiling binds.
	if eight.spend != four.spend {
		t.Errorf("four nodes spent %d and eight spent %d. Beyond the ceiling the extra nodes must never "+
			"be entered, or every node added to a definition is a node added to the bill.",
			four.spend, eight.spend)
	}
	if eight.calls != four.calls {
		t.Errorf("four nodes made %d call(s) and eight made %d", four.calls, eight.calls)
	}

	// 3 — the overshoot is ONE call, not N. This is the assertion the pending-spend argument earns.
	if four.spend >= ceiling+perCall {
		t.Errorf("a four-node definition spent %d against a ceiling of %d. A pre-call ceiling is "+
			"overshot by at most the call that crossed it (%d here); spending more means the per-node "+
			"check is reading a meter that cannot see what this assessment already spent, so every node "+
			"passes the same stale total.", four.spend, ceiling, perCall)
	}
	// ANTI-VACUITY: the graph must actually have tried to do more work than the single node.
	if four.calls <= one.calls {
		t.Errorf("the four-node definition made %d provider call(s) and the one-node made %d — the "+
			"graph did no more work, so the budget was never under pressure", four.calls, one.calls)
	}
}

// 🔴 §5.2 — THE CEILING IS ENFORCED BEFORE EVERY PROVIDER CALL ON EVERY NODE.
//
// Checked once at the top of an assessment, a cap is an accounting record for every node after the
// first: the first node's spend is not visible to a check that already ran. The assertion is that a
// graph STOPS PART WAY, not that it stops at all.
func TestTheCeilingIsCheckedBeforeEveryNodesProviderCall(t *testing.T) {
	ctx := context.Background()
	caps := NewMemCapStore()
	// Room for exactly two nodes' worth of spend (8 tokens each), then the ceiling binds.
	if err := caps.Set(ctx, Cap{TenantID: "t1", MaxTokens: 12, Reason: "two nodes' worth"}); err != nil {
		t.Fatal(err)
	}
	meter := NewMemSpendStore()
	checker, err := NewCapChecker(caps, meter, func() int64 { return 1_000_000 })
	if err != nil {
		t.Fatal(err)
	}
	// 🔴 The meter is PRE-LOADED to just under the ceiling, so the FIRST node passes and the second
	// does not. Without this the run would be stopped before it began, which proves nothing about
	// "before every call".
	if err := meter.Record(ctx, Spend{TenantID: "t1", TokensIn: 5, TokensOut: 0,
		CreatedAtMS: 1_000_000}); err != nil {
		t.Fatal(err)
	}

	model := &fixedCostModel{in: 4, out: 4, meter: meter, tenant: "t1", nowMS: 1_000_000}
	d := nNodeDefinition(4)
	hash, err := d.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRunner(model, NewMemInferenceStore(), 0.5, func() int64 { return 1_000_000 },
		WithCaps(checker, meter),
		WithNodeModels(func(Node) (Model, error) { return model, nil }))
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Infer(ctx, inputFor(irWith([]string{"a", "b", "c"})),
		BindDefinition(hash, d), PlacementPlatform)

	if !errors.Is(err, ErrCapReached) {
		t.Fatalf("a four-node definition ran to completion past a ceiling that binds after the first "+
			"node: err=%v calls=%d", err, res.ProviderCalls)
	}
	if res.ProviderCalls == 0 {
		t.Error("the run made NO provider call, so the ceiling stopped it before it started — this " +
			"proves the cap is checked once, not that it is checked before every node")
	}
	if res.ProviderCalls >= len(d.Nodes) {
		t.Errorf("the run made %d of %d nodes' calls, so it did not stop part way — a cap enforced "+
			"once at the top is an accounting record for every node after the first", res.ProviderCalls,
			len(d.Nodes))
	}
	// 🔴 §5.3 — the refusal NAMES THE NODE it stopped at. "The assessment ran out of budget" is not
	// actionable; "it ran out at node-2, having completed node-1" is.
	if !strings.Contains(res.Cause, "stopped at node") {
		t.Errorf("the refusal does not name the node it stopped at: %q", res.Cause)
	}
	// 🚫 AND NOTHING WAS WRITTEN. A half-applied assessment is a graph nobody can reproduce from its key.
	if !strings.Contains(res.Cause, "nothing was written") {
		t.Errorf("the refusal does not say that nothing was written: %q", res.Cause)
	}
	if res.Code != CodeCapReached {
		t.Errorf("the code is %q, want %q — a surface switching on it would render the wrong state",
			res.Code, CodeCapReached)
	}
}

// 🔴 §5.4 / §9.9 — A MULTI-NODE DEFINITION CANNOT BE ACTIVATED WITHOUT A REHEARSAL.
func TestActivatingAMultiNodeDefinitionRequiresARehearsal(t *testing.T) {
	ctx := context.Background()
	pub, store := p36Publisher(t, RunnerHosts{}, nil)

	d := twoNodeDefinition()
	res, err := pub.Publish(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	// 🔴 It lands PENDING, never passed.
	v, ok, err := store.Get(ctx, res.ConfigHash)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if v.RehearsalState != RehearsalPending {
		t.Errorf("a freshly published graph is %q, want %q", v.RehearsalState, RehearsalPending)
	}

	aerr := pub.Activate(ctx, res.ConfigHash)
	if !errors.Is(aerr, ErrRehearsalNotPassed) {
		t.Fatalf("a multi-node definition was activated without a rehearsal: %v", aerr)
	}
	// The sentence says why it matters MORE here, because a graph is where somebody wants an exception.
	for _, want := range []string{"2 nodes", "every tenant"} {
		if !strings.Contains(aerr.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, aerr)
		}
	}
	// 🔴 ANTI-VACUITY: it activates once it has passed. A gate that refused unconditionally would
	// satisfy the assertion above and make the agent unconfigurable.
	if err := store.SetRehearsal(ctx, res.ConfigHash, RehearsalPassed, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := pub.Activate(ctx, res.ConfigHash); err != nil {
		t.Errorf("a rehearsed graph was still refused: %v", err)
	}
}

// 🔴 §5.5 / §9.15 — ROLLBACK IS ONE ACT AND REQUIRES NO RE-AUTHORING.
//
// The assertion is that activating the PREVIOUS VERSION BY HASH is sufficient — no definition is
// submitted, no version is created, and the hash that comes back is the one that was serving before.
func TestRollbackIsOneActAndRequiresNoReAuthoring(t *testing.T) {
	ctx := context.Background()
	pub, store := p36Publisher(t, RunnerHosts{}, nil)

	first := goodDefinition()
	firstRes, err := pub.Publish(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRehearsal(ctx, firstRes.ConfigHash, RehearsalPassed, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := pub.Activate(ctx, firstRes.ConfigHash); err != nil {
		t.Fatal(err)
	}

	second := twoNodeDefinition()
	secondRes, err := pub.Publish(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRehearsal(ctx, secondRes.ConfigHash, RehearsalPassed, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := pub.Activate(ctx, secondRes.ConfigHash); err != nil {
		t.Fatal(err)
	}

	// ── THE ROLLBACK. One call, one argument, and it is a HASH rather than a definition. ──
	if err := pub.Rollback(ctx, firstRes.ConfigHash); err != nil {
		t.Fatalf("rolling back to a version that already passed and already served: %v", err)
	}

	active, ok, err := store.Active(ctx)
	if err != nil || !ok {
		t.Fatalf("nothing is active after a rollback: ok=%v err=%v", ok, err)
	}
	if active.ConfigHash != firstRes.ConfigHash {
		t.Errorf("after rollback %s is serving, want %s", active.ConfigHash, firstRes.ConfigHash)
	}
	// 🔴 NO NEW VERSION. A rollback that minted a row would be a THIRD configuration — the retyped
	// one — activated in place of the one known to work.
	all, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("the store holds %d versions after a rollback between two; a rollback that creates a "+
			"version is a re-authoring wearing a different name", len(all))
	}
	// And exactly ONE is active — a rollback that left two serving would be worse than one that failed.
	activeCount := 0
	for _, v := range all {
		if v.Active() {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Errorf("%d versions are active after a rollback", activeCount)
	}

	// 🚫 Rolling back to something that never served is refused, naming why.
	third := goodDefinition()
	third.Nodes[0].ContextRef = "ctx-never-served"
	thirdRes, err := pub.Publish(ctx, third)
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Rollback(ctx, thirdRes.ConfigHash); !errors.Is(err, ErrRehearsalNotPassed) {
		t.Errorf("rolled back to a version that never served: %v", err)
	}
	if err := pub.Rollback(ctx, "a-hash-nobody-published"); err == nil {
		t.Error("rolled back to a hash this deployment does not have")
	}
}

// ── fixtures ────────────────────────────────────────────────────────────────────────────────────

// nNodeDefinition is a linear definition of n nodes.
func nNodeDefinition(n int) Definition {
	d := Definition{}
	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		d.Nodes = append(d.Nodes, Node{
			NodeID: "node-" + id, PromptRef: "prompt-v1", ModelRef: "claude-opus-5",
			CredentialRef: "anthropic", ContextRef: "ctx-v1", HarnessRef: "harness-single-shot-v1",
		})
		d.Order = append(d.Order, "node-"+id)
	}
	if n == 1 {
		// One node is the DEFAULT shape and refuses an ordering.
		d.Order = nil
		d.Nodes[0].NodeID = DefaultNodeID
	}
	return d
}

// fixedCostModel answers identically and costs a fixed number of tokens, recording to a meter when one
// is wired — so a later node's cap check sees what the earlier ones spent.
type fixedCostModel struct {
	in, out int
	meter   SpendReader
	tenant  string
	nowMS   int64
	calls   int
}

func (m *fixedCostModel) Infer(ctx context.Context, _ Input) (RawResult, providercall.Usage, error) {
	m.calls++
	if m.meter != nil {
		// 🔴 The meter is written PER NODE, which is what makes the next node's ceiling check see this
		// node's spend. The production runner records once per assessment; this fixture records per
		// call so the fence can prove the check is re-read rather than cached.
		_ = m.meter.Record(ctx, Spend{TenantID: m.tenant, InferenceID: "n", TokensIn: int64(m.in),
			TokensOut: int64(m.out), CreatedAtMS: m.nowMS})
	}
	conf := 0.9
	return RawResult{
		Edges:     []RawEdge{{From: "a", To: "b", Kind: "data", Confidence: &conf}},
		Narrative: "n",
	}, providercall.Usage{InputTokens: m.in, OutputTokens: m.out}, nil
}
