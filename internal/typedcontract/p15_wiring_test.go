package typedcontract

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// P15 §5.4 — 🔴 an adapter is admissible ONLY if it drops nothing the consumer requires.
//
// This is the load-bearing half of the adapter posture. D-2 makes the adapter visible generated source
// so a reviewer can see the bridge; this rule makes the bridge SOUND, and the two are not
// interchangeable — a visible adapter that quietly dropped a required field would be a data loss a
// reviewer could read past, because "a rename adapter was inserted" looks reassuring. So the catalog
// re-validates every synthesized adapter against BOTH sides and an unbridgeable mismatch is refused,
// taking the whole ordering with it.

// TestAdapterDropsNothingRequired: a mismatch that no catalog adapter can FULLY bridge yields no
// adapter at all, and the ordering carrying that edge is rejected with no runnable ordering.
func TestAdapterDropsNothingRequired(t *testing.T) {
	// The producer has ONE spare field; the consumer requires TWO fields it does not have. A rename can
	// supply at most one of them, so bridging would mean leaving `extra` unsatisfied — exactly the
	// silent drop this rule exists to forbid.
	producer := obj(map[string]any{"answer": field("string")})
	consumer := obj(map[string]any{"response": field("string"), "extra": field("string")}, "response", "extra")

	if a, ok := DefaultCatalog().FindAdapter(producer, consumer); ok {
		t.Fatalf("an adapter that cannot satisfy every required consumer field must not be admitted, got %+v", a)
	}

	// And the ordering goes down with it: a mismatching edge with no admissible adapter is REJECTED,
	// never admitted as coherent and never best-effort coerced.
	ir := irWith(map[string]discovery.IRIOContract{
		"P": contract(map[string]any{"type": "object"}, producer),
		"C": contract(consumer, map[string]any{"type": "object"}),
	})
	v := ValidateOrdering(ir, Ordering{Order: []string{"P", "C"},
		Edges: []Edge{{FromNodeID: "P", ToNodeID: "C", Kind: "data"}}}, DefaultCatalog())
	if v.Kind != VerdictRejected {
		t.Fatalf("want rejected, got %s (%+v)", v.Kind, v)
	}
	if len(v.Diagnostics) == 0 || v.Diagnostics[0].Reason != ReasonUnadaptable {
		t.Fatalf("the rejection must say no catalog adapter bridges the edge, got %+v", v.Diagnostics)
	}
	var named bool
	for _, f := range v.Diagnostics[0].Fields {
		if f == "extra" || f == "response" {
			named = true
		}
	}
	if !named {
		t.Errorf("the rejection must name the unsatisfied field(s), got %+v", v.Diagnostics[0].Fields)
	}
}

// TestSatisfyingAdapterIsAdmitted is the other half of the requirement, and it is not decoration: a
// rule that only ever refuses would be indistinguishable from a broken catalog. When the producer CAN
// supply everything the consumer requires under a declared transformation, the adapter is admitted and
// its own io_contract is satisfied on both sides.
func TestSatisfyingAdapterIsAdmitted(t *testing.T) {
	producer := obj(map[string]any{"answer": field("string")})
	consumer := obj(map[string]any{"response": field("string")}, "response")

	a, ok := DefaultCatalog().FindAdapter(producer, consumer)
	if !ok {
		t.Fatal("a single-field rename is exactly what the catalog exists to bridge")
	}
	if a.Kind != KindRename {
		t.Errorf("want a rename adapter, got %q", a.Kind)
	}
	// The adapter's declared contract must itself hold: the producer satisfies its input, and its
	// output satisfies the consumer. This is what "drops nothing required" means structurally.
	if res := Satisfies(producer, a.InSchema); !res.OK {
		t.Errorf("the producer must satisfy the adapter's InSchema, got %+v", res.Mismatches)
	}
	if res := Satisfies(a.OutSchema, consumer); !res.OK {
		t.Errorf("the adapter's OutSchema must satisfy the consumer, got %+v", res.Mismatches)
	}

	ir := irWith(map[string]discovery.IRIOContract{
		"P": contract(map[string]any{"type": "object"}, producer),
		"C": contract(consumer, map[string]any{"type": "object"}),
	})
	v := ValidateOrdering(ir, Ordering{Order: []string{"P", "C"},
		Edges: []Edge{{FromNodeID: "P", ToNodeID: "C", Kind: "data"}}}, DefaultCatalog())
	if v.Kind != VerdictAdapted {
		t.Fatalf("want adapted, got %s (%+v)", v.Kind, v)
	}
	if len(v.Adapters) != 1 || v.Adapters[0].FromNodeID != "P" || v.Adapters[0].ToNodeID != "C" {
		t.Fatalf("the verdict must carry the insertion for the mismatching edge, got %+v", v.Adapters)
	}
}

// TestAdapterCatalogOrderIsFixed pins the determinism half of §5.6 at its source: the catalog is tried
// in ONE fixed order, so the same mismatch yields the same adapter kind on every evaluation. If the
// order were iteration-dependent, two runs of the same reorder could insert different-but-valid
// adapters and produce two config_hashes for one arrangement.
func TestAdapterCatalogOrderIsFixed(t *testing.T) {
	producer := obj(map[string]any{"answer": field("string")})
	consumer := obj(map[string]any{"response": field("string")}, "response")
	first, ok := DefaultCatalog().FindAdapter(producer, consumer)
	if !ok {
		t.Fatal("expected an adapter")
	}
	for i := 0; i < 20; i++ {
		got, ok := DefaultCatalog().FindAdapter(producer, consumer)
		if !ok || got.Kind != first.Kind {
			t.Fatalf("catalog matching is not deterministic: run %d gave %+v, first gave %+v", i, got, first)
		}
	}
}
