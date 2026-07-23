package evalenrich

import (
	"context"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/dynamictracing"
	"github.com/heros-foreal/agentd/internal/evalgen"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/reconcile"
)

// memBlobs maps content hash → bytes.
type memBlobs map[string][]byte

func (m memBlobs) Get(_ context.Context, h string) ([]byte, error) {
	if b, ok := m[h]; ok {
		return b, nil
	}
	return nil, errNotFound
}

var errNotFound = &notFound{}

type notFound struct{}

func (*notFound) Error() string { return "not found" }

func irNode(id string) discovery.IRNode {
	return discovery.IRNode{NodeID: id, Kind: "static_definition",
		IOContract: discovery.IRIOContract{InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}}
}

// TASK 7.1 / 7.3: real trace inputs appear as seed cases.
func TestCasesFromTraces_SeedsFromRealInputs(t *testing.T) {
	input := []byte(`{"question":"what is the refund policy?"}`)
	h := HashInput(input)
	blobs := memBlobs{h: input}
	calls := []dynamictracing.TracedCall{
		{Tags: dynamictracing.Tags{RunID: "r", NodeID: "a"}, InputsBlobHash: h},
		{Tags: dynamictracing.Tags{RunID: "r", NodeID: "a"}, InputsBlobHash: h}, // duplicate → deduped
	}
	src := &TraceSeedSource{WorkflowID: "wf", Calls: calls, Blobs: blobs}

	// Wire it into the P4 generator to prove it ACTIVATES the previously-inactive layer.
	gen := &evalgen.SeedTraceGenerator{Source: src}
	cases, err := gen.Generate(context.Background(), &discovery.IR{}, evalgen.Gap{}, nil)
	if err != nil {
		t.Fatalf("activated generator must not error: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("want 1 deduped seed case, got %d", len(cases))
	}
	c := cases[0]
	if c.Origin != evalharness.OriginSeedTrace {
		t.Fatalf("seed case must carry the seed_trace origin, got %q", c.Origin)
	}
	if string(c.Input) != string(input) {
		t.Fatalf("seed case input must be the observed trace input, got %s", c.Input)
	}
}

// TASK 7.2 / 7.3: a per-path target generates a case that forces the reconciled runtime-only edge.
func TestPerPathTargets_ForcesRuntimeOnlyEdge(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion,
		Nodes: []discovery.IRNode{irNode("router"), irNode("branchA"), irNode("branchB")},
		Edges: []discovery.IREdge{{FromNodeID: "router", ToNodeID: "branchA", Kind: "data"}}}
	trace := []dynamictracing.TracedCall{
		{Tags: dynamictracing.Tags{RunID: "r", NodeID: "router"}},
		{Tags: dynamictracing.Tags{RunID: "r", NodeID: "branchB"}},
	}
	rep := reconcile.Reconcile(ir, trace)

	targets := PerPathTargets("wf", rep)
	// The runtime-only edge router→branchB must have a per-path target case tagged with its edge id.
	wantTag := evalgen.EdgeID("router", "branchB")
	var found bool
	for _, c := range targets {
		for _, tag := range c.PathTags {
			if tag == wantTag {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("a per-path target must force the runtime-only edge %s, got %+v", wantTag, targets)
	}
}

// TASK 7.2: a looping node gets min/typical/max iteration-bound targets.
func TestPerPathTargets_LoopBounds(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Nodes: []discovery.IRNode{irNode("gen")},
		Edges: []discovery.IREdge{{FromNodeID: "gen", ToNodeID: "gen", Kind: "data"}}}
	var trace []dynamictracing.TracedCall
	for k := 0; k < 5; k++ {
		trace = append(trace, dynamictracing.TracedCall{Tags: dynamictracing.Tags{RunID: "r", NodeID: "gen"}, InvocationIndex: k})
	}
	rep := reconcile.Reconcile(ir, trace)
	targets := PerPathTargets("wf", rep)

	bounds := map[string]bool{}
	for _, c := range targets {
		for _, tag := range c.PathTags {
			if strings.HasPrefix(tag, "gen@") {
				bounds[strings.TrimPrefix(tag, "gen@")] = true
			}
		}
	}
	for _, want := range []string{"min", "typical", "max"} {
		if !bounds[want] {
			t.Fatalf("loop node must get a %q iteration-bound target, got %+v", want, bounds)
		}
	}
}

// The generator returns ErrGeneratorInactive without a source — proving P5 is what activates it.
func TestSeedGenerator_InactiveWithoutSource(t *testing.T) {
	gen := &evalgen.SeedTraceGenerator{} // no source
	_, err := gen.Generate(context.Background(), &discovery.IR{}, evalgen.Gap{}, nil)
	if err == nil {
		t.Fatal("the seed generator must be inactive until P5 supplies a trace source")
	}
}
