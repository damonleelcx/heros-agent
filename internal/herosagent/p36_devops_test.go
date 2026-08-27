package herosagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
)

// p36_devops_test.go is §8 — operating a definition that is a graph.
//
// The standing fact behind all three: **the blast radius here is every tenant at once.** This is the
// platform's own agent, not a per-tenant configuration, so there is no ring to fail in first.

// 🔴 §8.1 — PER-NODE COUNTS, LATENCY, SPEND AND FAILURE RATES ON A READABLE ENDPOINT.
//
// "An aggregate over a graph says the agent is slow, not which node is." The assertion is therefore not
// that numbers exist — it is that they are broken out per node, and that the three ways a node can
// produce no work stay distinguishable.
func TestNodeHealthIsPerNodeAndDistinguishesTheThreeZeros(t *testing.T) {
	reg := NewNodeHealthRegistry(1_000)

	reg.Observe(NodeRun{NodeID: "analyst", ProviderCalls: 1, TokensIn: 100, TokensOut: 20, LatencyMS: 400, Edges: 3})
	reg.Observe(NodeRun{NodeID: "analyst", ProviderCalls: 1, TokensIn: 120, TokensOut: 30, LatencyMS: 600, Edges: 2})
	reg.Observe(NodeRun{NodeID: "analyst", ProviderCalls: 1, Failed: true, Cause: "429", LatencyMS: 200})
	reg.Observe(NodeRun{NodeID: "critic", Skipped: true, SkipReason: `"abstained" did not hold`})
	reg.Observe(NodeRun{NodeID: "critic", Skipped: true, SkipReason: `"abstained" did not hold`})

	doc := reg.Document()
	if doc.SinceMS != 1_000 {
		t.Errorf("the document does not say when counting started (%d). A node with no inferences and a "+
			"freshly restarted process are the same zeros, and only this field separates them", doc.SinceMS)
	}
	if len(doc.Nodes) != 2 {
		t.Fatalf("the document carries %d node(s): %+v", len(doc.Nodes), doc.Nodes)
	}
	byID := map[string]NodeHealthEntry{}
	for _, n := range doc.Nodes {
		byID[n.NodeID] = n
	}

	a := byID["analyst"]
	if a.Inferences != 2 || a.Failures != 1 {
		t.Errorf("analyst: %d inference(s) and %d failure(s), want 2 and 1", a.Inferences, a.Failures)
	}
	// 🔴 The failure RATE is over ATTEMPTS, not over calls. A node that made three provider calls in one
	// loop and failed once failed ONE of its runs, not a third of its calls — and the first number is
	// the one an operator acts on.
	if got := a.FailureRate; got < 0.33 || got > 0.34 {
		t.Errorf("analyst failure rate is %v, want 1/3 (one failure over three attempts)", got)
	}
	// Latency is the MEAN over completed calls, derived from the sum. A running mean would not be
	// associative, and a fleet view built from means would be a mean of means.
	if a.LatencyMeanMS != (400+600+200)/3 {
		t.Errorf("analyst mean latency is %d ms", a.LatencyMeanMS)
	}

	c := byID["critic"]
	// 🚫 A SKIPPED node is not an inference and not a failure. Folding it into either would make a
	// definition whose conditional edge never fires look identical to one whose nodes all run — and one
	// of those is a configuration nobody is exercising.
	if c.Skips != 2 || c.Inferences != 0 || c.Failures != 0 {
		t.Errorf("critic: skips=%d inferences=%d failures=%d, want 2/0/0", c.Skips, c.Inferences, c.Failures)
	}
	if c.LatencyMeanMS != 0 || c.ProviderCalls != 0 {
		t.Errorf("a skipped node recorded latency (%d ms) or a provider call (%d). It cost nothing, and "+
			"adding a zero to the latency sum would report it as FAST", c.LatencyMeanMS, c.ProviderCalls)
	}
	if c.FailureRate != 0 {
		t.Errorf("a node that was only ever skipped has a failure rate of %v; it never attempted "+
			"anything, so there is nothing for a rate to be over", c.FailureRate)
	}

	// 🔴 READABLE: it serialises, and two reads of an unchanged registry are byte-identical.
	first, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(reg.Document())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("two reads of an unchanged registry differ, so a monitor diffing this endpoint would "+
			"see change that did not happen:\n  %s\n  %s", first, second)
	}
	for _, want := range []string{`"node_id"`, `"latency_mean_ms"`, `"failure_rate"`, `"since_ms"`} {
		if !strings.Contains(string(first), want) {
			t.Errorf("the health document carries no %s: %s", want, first)
		}
	}
	// ANTI-VACUITY: an EMPTY registry reports an empty list rather than nothing at all, so a reader can
	// tell "wired and idle" from "not wired" (the endpoint omits the key entirely for the latter).
	empty := NewNodeHealthRegistry(7).Document()
	if empty.Nodes == nil {
		t.Error("an empty registry renders a null node list; a wired-and-idle deployment must be " +
			"distinguishable from one that is not counting")
	}
}

// 🔴 §8.2 — THE STAGED ROLLOUT AND THE KILL SWITCH BOTH HOLD FOR A MULTI-NODE DEFINITION.
//
// The gate is not per shape and must not become so. This asserts the ladder still refuses on a graph
// exactly as it does on a single node — the point being that P36 introduced no exemption.
func TestTheRolloutLadderAndTheKillSwitchHoldForAGraph(t *testing.T) {
	graph := twoNodeDefinition()
	hash, err := graph.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}

	// A definition that has NOT passed cannot advance a rung, graph or not.
	pending := RolloutEvidence{
		EnabledTenants:   []string{"internal-1"},
		InternalTenants:  []string{"internal-1"},
		ActiveConfigHash: hash, RehearsalState: RehearsalPending, TotalTenants: 10,
	}
	if err := Advance(pending, StagePartner); !errors.Is(err, ErrRehearsalNotPassed) {
		t.Errorf("a graph that never passed its rehearsal advanced to %q: %v", StagePartner, err)
	}
	// And the fleet ceiling is still required before the first customer is analysed.
	passed := pending
	passed.RehearsalState = RehearsalPassed
	if err := Advance(passed, StagePartner); !errors.Is(err, ErrNoFleetCap) {
		t.Errorf("a graph reached the first customer-analysing rung with no fleet ceiling: %v", err)
	}
	capped := passed
	capped.FleetCapSet = true
	if err := Advance(capped, StagePartner); err != nil {
		t.Errorf("a rehearsed graph under a fleet ceiling was refused the next rung: %v", err)
	}
	// 🔴 Retreat is never gated. During an incident the operator switching tenants off must not be
	// stopped by a rehearsal — and on a graph, whose blast radius is every tenant at once, that matters
	// more rather than less.
	atOptIn := RolloutEvidence{
		EnabledTenants: []string{"a", "b", "c"}, InternalTenants: []string{"a"},
		ActiveConfigHash: hash, RehearsalState: RehearsalFailed, TotalTenants: 10,
	}
	if err := Advance(atOptIn, StageInternal); err != nil {
		t.Errorf("retreating from a FAILING graph was refused: %v. An operator pulling back during an "+
			"incident must never be gated on the thing that is failing.", err)
	}
}

// 🔴 §8.3 — READINESS REFLECTS A DEFINITION THIS BUILD CANNOT EXECUTE.
//
// The state P36 makes reachable: a definition binding a loop strategy this binary does not implement is
// publishable where it was authored and unrunnable where it landed. Without its own state that
// deployment reports `ready` and fails at the first analysis, naming a strategy rather than the build.
func TestReadinessReportsADefinitionThisBuildCannotExecute(t *testing.T) {
	ctx := context.Background()

	d := goodDefinition()
	d.Nodes[0].LoopRef = "loop-react-v1"
	versions := NewMemVersionStore()
	hash, err := d.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	if err := versions.Put(ctx, Version{ConfigHash: hash, Definition: d,
		RehearsalState: RehearsalPassed}); err != nil {
		t.Fatal(err)
	}
	if err := versions.Activate(ctx, hash, 1); err != nil {
		t.Fatal(err)
	}
	places := &fixedPlacements{list: []TenantPlacement{{TenantID: "acme", Placement: PlacementPlatform}}}
	axes := fakeAxisRegistry{loops: map[string]*registry.LoopEntry{
		"loop-react-v1": {VersionID: "loop-react-v1", Spec: registry.LoopSpec{Strategy: "react-loop"}},
	}}

	// This build supplies NO tool executor.
	got := Check(ctx, ReadinessInput{Versions: versions, Placements: places, Axes: axes,
		Runner: RunnerHosts{}})
	if got.State != ReadyUnexecutable {
		t.Fatalf("readiness is %q, want %q. A build that cannot run the active definition reporting "+
			"`ready` fails at the first analysis, and the failure names a STRATEGY rather than the "+
			"build — which sends an operator to the definition instead of to the deployed version.",
			got.State, ReadyUnexecutable)
	}
	for _, want := range []string{"react-loop", "tool-executor", "build"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("the detail does not mention %q, so a reader cannot act on it: %s", want, got.Detail)
		}
	}

	// 🔴 ANTI-VACUITY, both halves.
	//
	// A build that DOES supply the service reports ready — otherwise this check refuses every loop and
	// makes the axis unusable.
	if ok := Check(ctx, ReadinessInput{Versions: versions, Placements: places, Axes: axes,
		Runner: RunnerHosts{ToolInvoker: true}}); ok.State == ReadyUnexecutable {
		t.Errorf("a build that supplies a tool executor still reports the definition unexecutable: %s",
			ok.Detail)
	}
	// And a definition binding NO loop is unaffected, with no axis registry at all — which is every
	// pre-P36 definition.
	plain := NewMemVersionStore()
	ph, _ := goodDefinition().ConfigHash()
	if err := plain.Put(ctx, Version{ConfigHash: ph, Definition: goodDefinition(),
		RehearsalState: RehearsalPassed}); err != nil {
		t.Fatal(err)
	}
	if err := plain.Activate(ctx, ph, 1); err != nil {
		t.Fatal(err)
	}
	if got := Check(ctx, ReadinessInput{Versions: plain, Placements: places}); got.State == ReadyUnexecutable {
		t.Errorf("a definition binding no loop was reported unexecutable: %s", got.Detail)
	}
}

// 🔴 §8.3 — a loop that cannot be RESOLVED at all fails CLOSED, on the same terms publish does.
func TestReadinessFailsClosedWhenALoopCannotBeResolved(t *testing.T) {
	ctx := context.Background()
	d := goodDefinition()
	d.Nodes[0].LoopRef = "loop-something"
	versions := NewMemVersionStore()
	hash, _ := d.ConfigHash()
	if err := versions.Put(ctx, Version{ConfigHash: hash, Definition: d,
		RehearsalState: RehearsalPassed}); err != nil {
		t.Fatal(err)
	}
	if err := versions.Activate(ctx, hash, 1); err != nil {
		t.Fatal(err)
	}
	places := &fixedPlacements{list: []TenantPlacement{{TenantID: "acme", Placement: PlacementPlatform}}}

	// No axis registry: the check cannot be made.
	got := Check(ctx, ReadinessInput{Versions: versions, Placements: places})
	if got.State != ReadyUnexecutable {
		t.Errorf("a deployment that cannot resolve the active definition's loop reported %q. An unknown "+
			"here is discovered by whoever the next analysis reaches, which makes it a customer's "+
			"problem rather than an operator's.", got.State)
	}
}

// fixedPlacements returns a fixed placement list.
type fixedPlacements struct{ list []TenantPlacement }

func (f *fixedPlacements) List(context.Context) ([]TenantPlacement, error) { return f.list, nil }
