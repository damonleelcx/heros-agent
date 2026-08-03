package proposalgen

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/hostdiscovery"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/proposalstore"
	"github.com/heros-foreal/agentd/internal/runlink"
)

type fakeRuns struct {
	runs []linkingest.LinkedRun
	err  error
}

func (f fakeRuns) ForWorkflow(string, string) ([]linkingest.LinkedRun, error) { return f.runs, f.err }

type fakeGraphs struct {
	g     hostdiscovery.Graph
	found bool
	err   error
}

func (f fakeGraphs) Latest(context.Context, string, string) (hostdiscovery.Graph, bool, error) {
	return f.g, f.found, f.err
}

type fakeMenu struct {
	menu   proposal.Menu
	detail string
	err    error
}

func (f fakeMenu) Menu(context.Context) (proposal.Menu, string, error) {
	return f.menu, f.detail, f.err
}

type fakeSink struct {
	put []proposalstore.Record
	err error
}

func (f *fakeSink) Put(_ context.Context, r proposalstore.Record) error {
	if f.err != nil {
		return f.err
	}
	f.put = append(f.put, r)
	return nil
}

func run(perNode map[string]runlink.NodeMetric) linkingest.LinkedRun {
	return linkingest.LinkedRun{
		RunID: "run-1", WorkflowID: "wf", ConfigHash: strings.Repeat("a", 64),
		SourceRevision: "rev1", LinkedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		PerNode: perNode,
	}
}

// One node holding the overwhelming majority of cost, and two models with published tiers.
func generator(t *testing.T, sink *fakeSink) *Generator {
	t.Helper()
	return &Generator{
		Runs: fakeRuns{runs: []linkingest.LinkedRun{run(map[string]runlink.NodeMetric{
			"n_router":  {CostUSD: 0.90, LatencyMS: 400},
			"n_cleanup": {CostUSD: 0.02, LatencyMS: 20},
		})}},
		Graphs: fakeGraphs{found: true, g: hostdiscovery.Graph{View: patternclassifier.GraphView{
			Nodes: []patternclassifier.ViewNode{
				// Model is `provider/model`, as the classifier records it. It is what lets the baseline
				// resolve to a registry ref, and therefore what stops the engine proposing a downgrade to
				// the model the node already runs.
				{NodeID: "n_router", Model: "anthropic/big",
					Labels: []patternclassifier.ViewLabel{{Pattern: "Routing", Confidence: 0.9}}},
				{NodeID: "n_cleanup", Model: "anthropic/small"},
			},
		}}},
		Menus: fakeMenu{menu: proposal.Menu{Models: []proposal.ModelChoice{
			{Ref: strings.Repeat("1", 64), Provider: "anthropic", ModelID: "big", Tier: 3, CostPerRun: 0.05},
			{Ref: strings.Repeat("2", 64), Provider: "anthropic", ModelID: "small", Tier: 1, CostPerRun: 0.004},
		}}},
		Sink: sink,
		Now:  func() time.Time { return time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC) },
	}
}

func TestGeneratesAgainstTheCostBottleneck(t *testing.T) {
	sink := &fakeSink{}
	res, err := generator(t, sink).Generate(context.Background(), "t1", "wf")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if res.State != StateGenerated {
		t.Fatalf("state = %q (%s)", res.State, res.Detail)
	}
	if len(sink.put) == 0 {
		t.Fatal("nothing was recorded")
	}
	for _, r := range sink.put {
		if r.TenantID != "t1" || r.WorkflowID != "wf" {
			t.Errorf("scope missing: %+v", r)
		}
		// 🔴 Every proposal is UNVERIFIED and UNBUILT. This platform compiled no diff and ran no gate;
		// a row claiming otherwise would let P12 offer to deliver bytes that do not exist.
		if r.Status != proposalstore.StatusCandidate || r.BuildStatus != proposalstore.BuildUnbuilt {
			t.Errorf("a generated proposal must be candidate/unbuilt, got %s/%s", r.Status, r.BuildStatus)
		}
		if r.SourceDiffBlobHash != "" {
			t.Errorf("a diff hash was recorded for a diff nobody compiled: %q", r.SourceDiffBlobHash)
		}
		if len(r.Evidence) != 0 {
			t.Errorf("case evidence was recorded on a platform that holds no cases: %+v", r.Evidence)
		}
		if r.BaseVariantID != strings.Repeat("a", 64) || r.SourceRevision != "rev1" {
			t.Errorf("the proposal does not name the run it came from: %+v", r)
		}
	}
	// The dominant node is the one proposed against.
	if len(res.Bottlenecks) == 0 {
		t.Fatal("no bottleneck reported")
	}
	found := false
	for _, b := range res.Bottlenecks {
		if b.NodeID == "n_router" {
			found = true
		}
	}
	if !found {
		t.Errorf("the dominant node was not flagged: %+v", res.Bottlenecks)
	}
}

// Re-running an unchanged pass must UPSERT the same rows, not mint a second copy of every proposal.
func TestASecondPassIsIdempotent(t *testing.T) {
	first, second := &fakeSink{}, &fakeSink{}
	if _, err := generator(t, first).Generate(context.Background(), "t1", "wf"); err != nil {
		t.Fatal(err)
	}
	if _, err := generator(t, second).Generate(context.Background(), "t1", "wf"); err != nil {
		t.Fatal(err)
	}
	if len(first.put) != len(second.put) {
		t.Fatalf("passes recorded %d and %d proposals", len(first.put), len(second.put))
	}
	for i := range first.put {
		if first.put[i].ProposalID != second.put[i].ProposalID {
			t.Errorf("proposal id is not deterministic: %q then %q — a repeated pass would accumulate a "+
				"duplicate of every proposal", first.put[i].ProposalID, second.put[i].ProposalID)
		}
	}
}

// Two candidates that differ ONLY in the model they select must not collide onto one row.
//
// This is what the canonical-JSON hash buys over a hand-listed field set: enumerate the fields and a
// candidate differing in a field the list forgot hashes identically, and Put — an upsert — silently
// keeps one of the two.
func TestCandidatesDifferingOnlyInTheirSpecGetDifferentIds(t *testing.T) {
	sink := &fakeSink{}
	if _, err := generator(t, sink).Generate(context.Background(), "t1", "wf"); err != nil {
		t.Fatal(err)
	}
	if len(sink.put) < 2 {
		t.Skipf("only %d candidate(s) emitted; this check needs two", len(sink.put))
	}
	seen := map[string]bool{}
	for _, r := range sink.put {
		if seen[r.ProposalID] {
			t.Fatalf("two candidates share proposal id %s — one silently overwrote the other", r.ProposalID)
		}
		seen[r.ProposalID] = true
	}
}

// Every "nothing was produced" reason must be its OWN state. An empty list cannot tell a customer
// whether to link a run, push their source, publish a catalog, or celebrate.
func TestEachEmptyReasonIsItsOwnState(t *testing.T) {
	base := func() *Generator { return generator(t, &fakeSink{}) }

	for name, tc := range map[string]struct {
		mutate func(*Generator)
		want   State
	}{
		"no linked runs": {
			mutate: func(g *Generator) { g.Runs = fakeRuns{} },
			want:   StateNoRuns,
		},
		"runs with no per-node metrics": {
			mutate: func(g *Generator) { g.Runs = fakeRuns{runs: []linkingest.LinkedRun{run(nil)}} },
			want:   StateNoPerNode,
		},
		"no pushed source": {
			mutate: func(g *Generator) { g.Graphs = fakeGraphs{} },
			want:   StateNoGraph,
		},
		"no published model catalog": {
			mutate: func(g *Generator) {
				g.Menus = fakeMenu{err: errNoCatalog{}}
			},
			want: StateNoMenu,
		},
		"catalog published but no model is usable": {
			mutate: func(g *Generator) { g.Menus = fakeMenu{detail: "3 registered, 0 judged"} },
			want:   StateNoMenu,
		},
		"a run with cost nowhere": {
			mutate: func(g *Generator) {
				g.Runs = fakeRuns{runs: []linkingest.LinkedRun{run(map[string]runlink.NodeMetric{
					"n_router": {LatencyMS: 400}, "n_cleanup": {LatencyMS: 20},
				})}}
			},
			want: StateNoBottleneck,
		},
		"the node already runs the cheapest published model": {
			mutate: func(g *Generator) {
				// The node's own model is the only published one, so there is nothing below it.
				g.Menus = fakeMenu{menu: proposal.Menu{Models: []proposal.ModelChoice{
					{Ref: strings.Repeat("2", 64), Provider: "anthropic", ModelID: "big", Tier: 1, CostPerRun: 0.05},
				}}}
			},
			want: StateNoCandidates,
		},
	} {
		t.Run(name, func(t *testing.T) {
			g := base()
			tc.mutate(g)
			res, err := g.Generate(context.Background(), "t1", "wf")
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			if res.State != tc.want {
				t.Errorf("state = %q, want %q (detail: %s)", res.State, tc.want, res.Detail)
			}
			if res.Detail == "" {
				t.Error("every state must carry a sentence naming the next action; this one is silent")
			}
		})
	}
}

type errNoCatalog struct{}

func (errNoCatalog) Error() string { return "no model catalog is published on this deployment" }

// A read failure is an ERROR, never a state. "We could not read your linked runs" reported as "you have
// linked no runs" tells a customer to do something they have already done.
func TestAReadFailureIsNotAState(t *testing.T) {
	for name, mutate := range map[string]func(*Generator){
		"runs":  func(g *Generator) { g.Runs = fakeRuns{err: errNoCatalog{}} },
		"graph": func(g *Generator) { g.Graphs = fakeGraphs{err: errNoCatalog{}} },
		"sink":  func(g *Generator) { g.Sink = &fakeSink{err: errNoCatalog{}} },
	} {
		t.Run(name, func(t *testing.T) {
			g := generator(t, &fakeSink{})
			mutate(g)
			if _, err := g.Generate(context.Background(), "t1", "wf"); err == nil {
				t.Error("a read/write failure was reported as a normal result")
			}
		})
	}
}

// Latency dominance alone proposes nothing: the only signal-driven operator answers COST, and offering
// a downgrade against a slow node would answer "this is slow" with "make it cheaper".
func TestLatencyDominanceAloneProposesNothing(t *testing.T) {
	g := generator(t, &fakeSink{})
	g.Runs = fakeRuns{runs: []linkingest.LinkedRun{run(map[string]runlink.NodeMetric{
		"n_slow": {LatencyMS: 900},
		"n_fast": {LatencyMS: 10},
	})}}
	res, err := g.Generate(context.Background(), "t1", "wf")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if res.State != StateNoBottleneck {
		t.Errorf("state = %q (%s)", res.State, res.Detail)
	}
}

// An unlabelled node keeps the zero Pattern rather than a defaulted one. An operator with a non-empty
// admissibility list then declines it, which is correct — a made-up label would let a change be
// proposed against a node nobody classified.
func TestAnUnclassifiedNodeIsNotGivenALabel(t *testing.T) {
	got := patternsByNode(patternclassifier.GraphView{Nodes: []patternclassifier.ViewNode{
		{NodeID: "labelled", Labels: []patternclassifier.ViewLabel{
			{Pattern: "Routing", Confidence: 0.4},
			{Pattern: "ToolUse", Confidence: 0.8},
		}},
		{NodeID: "bare"},
	}})
	if got["labelled"] != "ToolUse" {
		t.Errorf("highest-confidence label must win, got %q", got["labelled"])
	}
	if got["bare"] != "" {
		t.Errorf("an unclassified node was given the label %q", got["bare"])
	}
}

// Uniform cost still flags a Pareto PREFIX, and that is deliberate rather than a bug in either place.
//
// `attribution.BottleneckFromTotals` returns the smallest set of nodes whose cumulative share reaches
// the coverage target (default: the majority), so six equally-costly nodes flag three. The generator
// reuses that function precisely so the node the console calls a bottleneck and the node it proposes
// against are the same node. A second, stricter threshold here would let the two disagree, and the
// disagreement would surface as "the scorecard flagged this node and nothing was ever proposed for it".
func TestUniformCostStillFlagsAParetoPrefix(t *testing.T) {
	sink := &fakeSink{}
	g := generator(t, sink)
	g.Runs = fakeRuns{runs: []linkingest.LinkedRun{run(map[string]runlink.NodeMetric{
		"a": {CostUSD: 0.1}, "b": {CostUSD: 0.1}, "c": {CostUSD: 0.1},
		"d": {CostUSD: 0.1}, "e": {CostUSD: 0.1}, "f": {CostUSD: 0.1},
	})}}
	res, err := g.Generate(context.Background(), "t1", "wf")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if res.State != StateGenerated {
		t.Fatalf("state = %q (%s)", res.State, res.Detail)
	}
	var cost int
	for _, b := range res.Bottlenecks {
		if b.Dimension == "cost" {
			cost++
		}
	}
	if cost != 3 {
		t.Errorf("flagged %d cost bottleneck(s) over six equal nodes; the shared Pareto reaches the "+
			"majority at three, and the generator must not use a threshold of its own", cost)
	}
}

// 🔴 The engine must not propose a downgrade to the model the node is ALREADY running.
//
// It did, until the baseline was reconstructed from the discovered graph: with a nil Base,
// `currentTier` reports "no discoverable current model" and returns maxTier+1, so every published model
// — the incumbent included — counts as cheaper. The candidate then resolves to the baseline's own
// configuration and occupies a verification slot measuring it against itself.
func TestNoCandidateProposesTheModelTheNodeAlreadyRuns(t *testing.T) {
	sink := &fakeSink{}
	if _, err := generator(t, sink).Generate(context.Background(), "t1", "wf"); err != nil {
		t.Fatal(err)
	}
	if len(sink.put) == 0 {
		t.Fatal("nothing generated")
	}
	incumbent := strings.Repeat("1", 64) // anthropic/big, which n_router runs
	base := baseSpec("wf", "rev1", patternclassifier.GraphView{Nodes: []patternclassifier.ViewNode{
		{NodeID: "n_router", Model: "anthropic/big"},
	}}, proposal.Menu{Models: []proposal.ModelChoice{
		{Ref: incumbent, Provider: "anthropic", ModelID: "big", Tier: 3},
	}})
	if base.Nodes["n_router"].ModelRef != incumbent {
		t.Fatalf("the baseline did not resolve the node's current model: %+v", base.Nodes)
	}
	// One published model is cheaper than `big` (tier 3), so exactly one candidate is admissible.
	if len(sink.put) != 1 {
		t.Errorf("expected exactly one downgrade candidate (only `small` is below `big`), got %d — a "+
			"second one is the engine proposing the incumbent against itself", len(sink.put))
	}
}
