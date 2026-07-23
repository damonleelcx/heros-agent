package irwriteback

import (
	"encoding/json"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/typedcontract"
)

func irNode(id string, in, out map[string]any) discovery.IRNode {
	return discovery.IRNode{NodeID: id, Kind: "static_definition",
		IOContract: discovery.IRIOContract{InputSchema: in, OutputSchema: out}}
}

func obj(props map[string]any, req ...string) map[string]any {
	r := make([]any, len(req))
	for i, x := range req {
		r[i] = x
	}
	m := map[string]any{"type": "object", "properties": props}
	if len(r) > 0 {
		m["required"] = r
	}
	return m
}

func confirmedReflection(subgraphRef string) patternclassifier.Label {
	return patternclassifier.Label{
		Pattern: patternclassifier.Reflection, Confidence: 0.9,
		Source: patternclassifier.SourceBehavioral, SubgraphRef: subgraphRef,
		TaxonomyVersion: patternclassifier.TaxonomyVersion, Candidate: false,
	}
}

// TASK 8.2: confirmed behavioral labels are written back additively at the same ir_version MAJOR, and a
// pre-P5 consumer (parsing MAJOR 1) still parses the enriched IR.
func TestAddBehavioralLabels_AdditiveSameMajor(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion,
		Nodes: []discovery.IRNode{irNode("critic", map[string]any{"type": "object"}, map[string]any{"type": "object"})}}

	out, err := AddBehavioralLabels(ir, []patternclassifier.Label{confirmedReflection("critic")})
	if err != nil {
		t.Fatal(err)
	}
	// Same MAJOR.
	if out.IRVersion[0] != discovery.IRVersion[0] {
		t.Fatalf("write-back must stay at the same ir_version MAJOR, got %s", out.IRVersion)
	}
	// The label was written onto the node.
	if len(out.Nodes[0].PatternLabels) != 1 || out.Nodes[0].PatternLabels[0].Source != "behavioral" {
		t.Fatalf("confirmed behavioral label must be written back, got %+v", out.Nodes[0].PatternLabels)
	}
	// A pre-P5 consumer parses it: the enriched IR round-trips through JSON without error.
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var reparsed discovery.IR
	if err := json.Unmarshal(b, &reparsed); err != nil {
		t.Fatalf("enriched IR must remain parseable: %v", err)
	}
	// Input IR untouched (write-back returns a copy).
	if len(ir.Nodes[0].PatternLabels) != 0 {
		t.Fatal("write-back must not mutate the input IR")
	}
}

// Idempotent: writing the same confirmed label twice does not duplicate it.
func TestAddBehavioralLabels_Idempotent(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion,
		Nodes: []discovery.IRNode{irNode("critic", map[string]any{"type": "object"}, map[string]any{"type": "object"})}}
	once, _ := AddBehavioralLabels(ir, []patternclassifier.Label{confirmedReflection("critic")})
	twice, _ := AddBehavioralLabels(once, []patternclassifier.Label{confirmedReflection("critic")})
	if len(twice.Nodes[0].PatternLabels) != 1 {
		t.Fatalf("re-confirmation must be idempotent, got %d labels", len(twice.Nodes[0].PatternLabels))
	}
}

// TASK 8.3: a permissive output schema is refined toward the observed shape; a node with no evidence
// stays permissive and is surfaced.
func TestRefineSchemas_TightensPermissiveAndSurfaces(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Nodes: []discovery.IRNode{
		irNode("producer", map[string]any{"type": "object"}, map[string]any{"type": "object"}),
		irNode("unseen", map[string]any{"type": "object"}, map[string]any{"type": "object"}),
	}}
	observed := map[string]ObservedShape{
		"producer": {OutputFields: map[string]string{"summary": "string", "score": "number"}},
	}
	out, rep := RefineSchemas(ir, observed)

	if len(rep.Refined) != 1 || rep.Refined[0] != "producer" {
		t.Fatalf("producer must be refined, got %+v", rep.Refined)
	}
	if len(rep.StillPermissive) != 1 || rep.StillPermissive[0] != "unseen" {
		t.Fatalf("unseen must be surfaced as still-permissive, got %+v", rep.StillPermissive)
	}
	// The refined schema now declares the observed properties.
	props, _ := out.Nodes[0].IOContract.OutputSchema["properties"].(map[string]any)
	if _, ok := props["summary"]; !ok {
		t.Fatalf("refined schema must declare observed fields, got %+v", out.Nodes[0].IOContract.OutputSchema)
	}
}

// TASK 8.3: a refinement that tightens coherence is SURFACED — the affected edge is reported, never
// silently changed.
func TestOrderingsAffectedByRefinement_Surfaced(t *testing.T) {
	// Before: producer output is permissive → cannot satisfy consumer requiring `summary` → the edge is
	// rejected. After: producer output declares `summary` → the edge becomes coherent.
	before := &discovery.IR{IRVersion: discovery.IRVersion, Nodes: []discovery.IRNode{
		irNode("producer", map[string]any{"type": "object"}, map[string]any{"type": "object"}),
		irNode("consumer", obj(map[string]any{"summary": map[string]any{"type": "string"}}, "summary"), map[string]any{"type": "object"}),
	}}
	after, _ := RefineSchemas(before, map[string]ObservedShape{
		"producer": {OutputFields: map[string]string{"summary": "string"}},
	})
	ordering := typedcontract.Ordering{
		Order: []string{"producer", "consumer"},
		Edges: []typedcontract.Edge{{FromNodeID: "producer", ToNodeID: "consumer", Kind: "data"}},
	}
	affected := OrderingsAffectedByRefinement(before, after, ordering)
	if len(affected) != 1 {
		t.Fatalf("the refinement must surface exactly the affected edge, got %+v", affected)
	}
	if affected[0].Before != "rejected" || affected[0].After != "coherent" {
		t.Fatalf("edge must flip rejected→coherent, got %+v", affected[0])
	}
}
