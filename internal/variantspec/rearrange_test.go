package variantspec

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/typedcontract"
)

func objSchema(props map[string]any, required ...string) map[string]any {
	req := make([]any, len(required))
	for i, r := range required {
		req[i] = r
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(req) > 0 {
		out["required"] = req
	}
	return out
}

func strField() map[string]any { return map[string]any{"type": "string"} }

func irFor(nodes map[string]discovery.IRIOContract) *discovery.IR {
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Workflow: discovery.IRWorkflow{Language: "go"}}
	for id, c := range nodes {
		ir.Nodes = append(ir.Nodes, discovery.IRNode{NodeID: id, Kind: "static_definition", IOContract: c})
	}
	return ir
}

// TASK 1.6: a coherent reorder produces a new config_hash with lineage to the parent.
func TestReorder_CoherentYieldsNewConfigHashWithLineage(t *testing.T) {
	// Two independent nodes (no data edge) so either order is coherent.
	parent := &VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		Order: []string{"A", "B"}, Nodes: map[string]NodeOverride{},
		Edges: []Edge{},
	}
	parentCfg := ResolvedConfig{IRVersion: discovery.IRVersion,
		Nodes: []ResolvedNode{{NodeID: "A"}, {NodeID: "B"}}}
	parentHash, err := parentCfg.Hash()
	if err != nil {
		t.Fatal(err)
	}

	ir := irFor(map[string]discovery.IRIOContract{
		"A": {InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}},
		"B": {InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}},
	})
	cand := Reorder(parent, parentHash, []string{"B", "A"}, []Edge{})
	got, verdict := GateReorder(ir, cand, typedcontract.DefaultCatalog())
	if verdict.Kind != typedcontract.VerdictCoherent {
		t.Fatalf("want coherent, got %s", verdict.Kind)
	}
	if got.ParentVariantID != parentHash {
		t.Fatalf("candidate must carry lineage to parent, got %q", got.ParentVariantID)
	}
	// Reordered config hashes differently (node order is identity-bearing).
	reorderedCfg := ResolvedConfig{IRVersion: discovery.IRVersion,
		Nodes: []ResolvedNode{{NodeID: "B"}, {NodeID: "A"}}}
	reHash, _ := reorderedCfg.Hash()
	if reHash == parentHash {
		t.Fatalf("a reorder must produce a new config_hash")
	}
}

// TASK 1.7: validation gates transform generation — a rejected reorder returns no runnable spec.
func TestGateReorder_RejectedYieldsNoRunnableSpec(t *testing.T) {
	ir := irFor(map[string]discovery.IRIOContract{
		"produce": {InputSchema: map[string]any{"type": "object"}, OutputSchema: objSchema(map[string]any{"summary": strField()})},
		"consume": {InputSchema: objSchema(map[string]any{"summary": strField()}, "summary"), OutputSchema: map[string]any{"type": "object"}},
	})
	parent := &VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		Order: []string{"produce", "consume"}, Nodes: map[string]NodeOverride{},
		Edges: []Edge{{FromNodeID: "produce", ToNodeID: "consume", Kind: "data"}},
	}
	// Reorder consume before produce: incoherent.
	cand := Reorder(parent, "parent-hash", []string{"consume", "produce"},
		[]Edge{{FromNodeID: "produce", ToNodeID: "consume", Kind: "data"}})
	got, verdict := GateReorder(ir, cand, typedcontract.DefaultCatalog())
	if got != nil {
		t.Fatalf("a rejected reorder must yield no runnable spec (nothing to transform), got %+v", got)
	}
	if verdict.Kind != typedcontract.VerdictRejected {
		t.Fatalf("want rejected, got %s", verdict.Kind)
	}
}

// TASK 1.6 (adapted path): an adaptable reorder records the inserted adapter and rewires the edge.
func TestGateReorder_AdaptedRecordsAdapter(t *testing.T) {
	ir := irFor(map[string]discovery.IRIOContract{
		"A": {InputSchema: map[string]any{"type": "object"}, OutputSchema: objSchema(map[string]any{"answer": strField()})},
		"B": {InputSchema: objSchema(map[string]any{"response": strField()}, "response"), OutputSchema: map[string]any{"type": "object"}},
	})
	parent := &VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		Order: []string{"A", "B"}, Nodes: map[string]NodeOverride{},
		Edges: []Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}},
	}
	cand := Reorder(parent, "parent-hash", []string{"A", "B"},
		[]Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}})
	got, verdict := GateReorder(ir, cand, typedcontract.DefaultCatalog())
	if verdict.Kind != typedcontract.VerdictAdapted || got == nil {
		t.Fatalf("want adapted with a runnable spec, got %s %+v", verdict.Kind, got)
	}
	if len(got.InsertedAdapters) != 1 || got.InsertedAdapters[0].CatalogKind != string(typedcontract.KindRename) {
		t.Fatalf("must record the inserted rename adapter, got %+v", got.InsertedAdapters)
	}
	// The direct A→B edge is replaced by A→adapter and adapter→B.
	adapterID := got.InsertedAdapters[0].AdapterNodeID
	var haveAToAdapter, haveAdapterToB, haveDirect bool
	for _, e := range got.Edges {
		switch {
		case e.FromNodeID == "A" && e.ToNodeID == adapterID:
			haveAToAdapter = true
		case e.FromNodeID == adapterID && e.ToNodeID == "B":
			haveAdapterToB = true
		case e.FromNodeID == "A" && e.ToNodeID == "B":
			haveDirect = true
		}
	}
	if !haveAToAdapter || !haveAdapterToB || haveDirect {
		t.Fatalf("edge must be rewired through the adapter, got %+v", got.Edges)
	}
}
