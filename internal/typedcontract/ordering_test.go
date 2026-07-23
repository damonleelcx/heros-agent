package typedcontract

import (
	"reflect"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// irWith builds a minimal IR carrying only what the validator reads: node ids and their io_contracts.
func irWith(nodes map[string]discovery.IRIOContract) *discovery.IR {
	ir := &discovery.IR{IRVersion: discovery.IRVersion}
	for id, c := range nodes {
		ir.Nodes = append(ir.Nodes, discovery.IRNode{NodeID: id, Kind: "static_definition", IOContract: c})
	}
	return ir
}

func contract(in, out map[string]any) discovery.IRIOContract {
	return discovery.IRIOContract{InputSchema: in, OutputSchema: out}
}

// A→B where A produces `summary` and B requires it, correctly ordered → coherent.
func TestValidateOrdering_Coherent(t *testing.T) {
	ir := irWith(map[string]discovery.IRIOContract{
		"A": contract(map[string]any{"type": "object"}, obj(map[string]any{"summary": field("string")})),
		"B": contract(obj(map[string]any{"summary": field("string")}, "summary"), map[string]any{"type": "object"}),
	})
	ord := Ordering{Order: []string{"A", "B"}, Edges: []Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}}}
	v := ValidateOrdering(ir, ord, DefaultCatalog())
	if v.Kind != VerdictCoherent {
		t.Fatalf("want coherent, got %s: %+v", v.Kind, v)
	}
}

// TASK 1.8: a known-incoherent reorder (consumer before its producer) → rejected, not coherent.
func TestValidateOrdering_ConsumerBeforeProducerRejected(t *testing.T) {
	ir := irWith(map[string]discovery.IRIOContract{
		"produce_summary": contract(map[string]any{"type": "object"}, obj(map[string]any{"summary": field("string")})),
		"consume_summary": contract(obj(map[string]any{"summary": field("string")}, "summary"), map[string]any{"type": "object"}),
	})
	// B ordered BEFORE A, but the data edge A→B still exists (data flow is inherent to the code).
	ord := Ordering{
		Order: []string{"consume_summary", "produce_summary"},
		Edges: []Edge{{FromNodeID: "produce_summary", ToNodeID: "consume_summary", Kind: "data"}},
	}
	v := ValidateOrdering(ir, ord, DefaultCatalog())
	if v.Kind != VerdictRejected {
		t.Fatalf("want rejected, got %s", v.Kind)
	}
	d := v.Diagnostics[0]
	if d.Reason != ReasonMissingProducer || d.ToNodeID != "consume_summary" || d.FromNodeID != "produce_summary" {
		t.Fatalf("diagnostic must name producer, consumer: %+v", d)
	}
	if !reflect.DeepEqual(d.Fields, []string{"summary"}) {
		t.Fatalf("diagnostic must name the field summary, got %v", d.Fields)
	}
}

// TASK 1.8: determinism — same reorder over same IR yields the same verdict + adapters twice.
func TestValidateOrdering_Deterministic(t *testing.T) {
	ir := irWith(map[string]discovery.IRIOContract{
		"A": contract(map[string]any{"type": "object"}, obj(map[string]any{"answer": field("string")})),
		"B": contract(obj(map[string]any{"response": field("string")}, "response"), map[string]any{"type": "object"}),
	})
	ord := Ordering{Order: []string{"A", "B"}, Edges: []Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}}}
	first := ValidateOrdering(ir, ord, DefaultCatalog())
	for i := 0; i < 20; i++ {
		got := ValidateOrdering(ir, ord, DefaultCatalog())
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("non-deterministic verdict: %+v vs %+v", got, first)
		}
	}
	if first.Kind != VerdictAdapted {
		t.Fatalf("answer→response should be adapted by a rename, got %s", first.Kind)
	}
}

// An unadaptable schema mismatch (missing field, no rename source, no default) → rejected.
func TestValidateOrdering_UnadaptableRejected(t *testing.T) {
	ir := irWith(map[string]discovery.IRIOContract{
		"A": contract(map[string]any{"type": "object"}, obj(map[string]any{"answer": field("string")})),
		"B": contract(obj(map[string]any{"totally_different": field("number")}, "totally_different"), map[string]any{"type": "object"}),
	})
	ord := Ordering{Order: []string{"A", "B"}, Edges: []Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "data"}}}
	v := ValidateOrdering(ir, ord, DefaultCatalog())
	if v.Kind != VerdictRejected {
		t.Fatalf("want rejected, got %s: %+v", v.Kind, v)
	}
	if v.Diagnostics[0].Reason != ReasonUnadaptable {
		t.Fatalf("want unadaptable diagnostic, got %+v", v.Diagnostics[0])
	}
}

// Every edge is classified — rejection dominates over an otherwise-adaptable edge.
func TestValidateOrdering_RejectionDominates(t *testing.T) {
	ir := irWith(map[string]discovery.IRIOContract{
		"A": contract(map[string]any{"type": "object"}, obj(map[string]any{"answer": field("string")})),
		"B": contract(obj(map[string]any{"response": field("string")}, "response"), obj(map[string]any{"x": field("string")})),
		// C requires `missing` as a number; B's only spare field `x` is a string, so no rename bridges it.
		"C": contract(obj(map[string]any{"missing": field("number")}, "missing"), map[string]any{"type": "object"}),
	})
	ord := Ordering{
		Order: []string{"A", "B", "C"},
		Edges: []Edge{
			{FromNodeID: "A", ToNodeID: "B", Kind: "data"}, // adaptable rename
			{FromNodeID: "B", ToNodeID: "C", Kind: "data"}, // unadaptable
		},
	}
	if v := ValidateOrdering(ir, ord, DefaultCatalog()); v.Kind != VerdictRejected {
		t.Fatalf("a single unadaptable edge must reject the whole ordering, got %s", v.Kind)
	}
}

// Control edges carry no data-contract obligation and never affect the verdict.
func TestValidateOrdering_ControlEdgesIgnored(t *testing.T) {
	ir := irWith(map[string]discovery.IRIOContract{
		"A": contract(map[string]any{"type": "object"}, map[string]any{"type": "object"}),
		"B": contract(obj(map[string]any{"summary": field("string")}, "summary"), map[string]any{"type": "object"}),
	})
	// A control edge from a producer that lacks `summary` must not reject — it is not a data dependency.
	ord := Ordering{Order: []string{"A", "B"}, Edges: []Edge{{FromNodeID: "A", ToNodeID: "B", Kind: "control"}}}
	if v := ValidateOrdering(ir, ord, DefaultCatalog()); v.Kind != VerdictCoherent {
		t.Fatalf("control edges carry no contract obligation, got %s", v.Kind)
	}
}
