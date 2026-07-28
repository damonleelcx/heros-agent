package transform

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/typedcontract"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P15 §4 — the interim refusal for un-materializable wiring.
//
// 🔴 These are must-FAIL gates. Each one asserts the engine says NO to something it could very easily
// say yes to: a spec whose config_hash already records a rearranged graph, handed to an engine that
// rewrites values. The dangerous outcome is not an exception — it is a "success" whose diff rewrote
// node contents while the hash claimed a different graph, and whose eval score would then be attached
// to a configuration that never ran.

// discoveredABC is the wiring the source has: A → B → C.
func discoveredABC() variantspec.Wiring {
	return variantspec.Wiring{
		Order: []string{"A", "B", "C"},
		Edges: []variantspec.ResolvedEdge{
			{FromNodeID: "A", ToNodeID: "B", Kind: "data"},
			{FromNodeID: "B", ToNodeID: "C", Kind: "data"},
		},
	}
}

func resolvedFor(w variantspec.Wiring) *variantspec.Resolved {
	return &variantspec.Resolved{
		Language: "go", SourceRevision: "rev1", ConfigHash: strings.Repeat("a", 64),
		DiscoveredWiring: w,
	}
}

func specWiring(order []string, edges ...variantspec.Edge) *variantspec.VariantSpec {
	return &variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		Order: order, Nodes: map[string]variantspec.NodeOverride{}, Edges: edges,
	}
}

// assertWiringRefusal checks the error is the TYPED refusal naming the wiring axis — not a generic
// failure that happens to mention wiring.
func assertWiringRefusal(t *testing.T, err error) *RewriteError {
	t.Helper()
	if err == nil {
		t.Fatal("a wiring-differing spec must be REFUSED; a nil error here means the engine silently " +
			"no-op'd the rearrangement and would let its config_hash be scored against unchanged source")
	}
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("refusal must be a typed *RewriteError, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("refusal must be an unsafeRewrite-class error, got %v", err)
	}
	if re.Dim != "wiring" {
		t.Fatalf("the refusal must name the wiring axis, got dimension %q", re.Dim)
	}
	return re
}

// ── §4.2 — refused, never a silent no-op ─────────────────────────────────────────────────────────

func TestWiringRefusedNotNoop(t *testing.T) {
	// A PRUNE: the spec drops a node. The source still contains that call, so a config_hash recording a
	// two-node graph would be scored against a three-node program — the false measurement §4 exists for.
	pruned := specWiring([]string{"A", "C"})
	patch, err := GenerateTransform(resolvedFor(discoveredABC()), pruned, t.TempDir())
	re := assertWiringRefusal(t, err)
	if patch != nil {
		t.Fatalf("a refused spec must produce no patch at all, got %+v", patch)
	}
	if re.NodeID == "" {
		t.Error("the refusal must name a node so the reader has somewhere to look")
	}
	if !strings.Contains(re.Detail, "drops node") {
		t.Errorf("the refusal must say WHICH change was asked for, got %q", re.Detail)
	}

	// A MERGE presents the same way from here — one node fewer than the source contains.
	merged := specWiring([]string{"A", "C"},
		variantspec.Edge{FromNodeID: "A", ToNodeID: "C", Kind: "data"})
	if _, err := GenerateTransform(resolvedFor(discoveredABC()), merged, t.TempDir()); err == nil {
		t.Fatal("a merge leaves the source containing a call the graph no longer has; it must be refused")
	}

	// A spec naming a node the source does not contain is refused in the other direction.
	invented := specWiring([]string{"A", "B", "C", "D"})
	if _, err := GenerateTransform(resolvedFor(discoveredABC()), invented, t.TempDir()); err == nil {
		t.Fatal("a spec that adds a node the source does not contain must be refused")
	}
}

// TestOrderTheSourceDoesNotStateIsNotARewire is the correction CI earned, written down as a test.
//
// The first version of this gate compared the spec's Order against the IR's NODE-EMISSION order — the
// order Discovery happened to walk files in — and called any difference a rearrangement. That refused
// twelve pre-existing specs that override only a model or a prompt, because their author listed the
// nodes in the workflow's logical sequence instead of in the order Discovery emitted them.
//
// The source states a relative order between two calls only when it ORDERS them: consecutive sibling
// statements in one block. Calls in different functions or different files have no source-stated order,
// so a spec listing them in any sequence contradicts nothing in the tree. Refusing there would have
// broken every spec ever authored while preventing no false measurement — there is no "different order"
// in the source for the spec to differ FROM.
//
// The same applies to edges: an IR that records no edge means NOT RECORDED, never "no edge" (the P14
// `Tools == nil` lesson), and a spec legitimately declares the edges the coherence gate needs.
func TestOrderTheSourceDoesNotStateIsNotARewire(t *testing.T) {
	// No tree behind these node ids, so nothing states their order.
	reordered := specWiring([]string{"A", "C", "B"})
	if _, err := GenerateTransform(resolvedFor(discoveredABC()), reordered, t.TempDir()); err != nil {
		t.Fatalf("an ordering the source does not state is a declaration, not a rewire: %v", err)
	}

	// An added edge, same nodes: also a declaration.
	rewired := specWiring([]string{"A", "B", "C"},
		variantspec.Edge{FromNodeID: "A", ToNodeID: "B", Kind: "data"},
		variantspec.Edge{FromNodeID: "B", ToNodeID: "C", Kind: "data"},
		variantspec.Edge{FromNodeID: "A", ToNodeID: "C", Kind: "data"})
	if _, err := GenerateTransform(resolvedFor(discoveredABC()), rewired, t.TempDir()); err != nil {
		t.Fatalf("a declared edge is not a request to rewrite source: %v", err)
	}
}

// TestWiringUnchangedIsNotRefused is the negative control that keeps the gate honest: the refusal
// fires on a wiring DIFFERENCE, not on the mere presence of an Order. A content-only change — the
// overwhelming majority of what this engine does — must pass through untouched.
func TestWiringUnchangedIsNotRefused(t *testing.T) {
	same := specWiring([]string{"A", "B", "C"},
		variantspec.Edge{FromNodeID: "A", ToNodeID: "B", Kind: "data"},
		variantspec.Edge{FromNodeID: "B", ToNodeID: "C", Kind: "data"})
	if _, err := GenerateTransform(resolvedFor(discoveredABC()), same, t.TempDir()); err != nil {
		t.Fatalf("a spec that wires exactly what the source wires must not be refused: %v", err)
	}

	// Edge ORDER is not graph identity: the same edges listed the other way round are one graph.
	shuffled := specWiring([]string{"A", "B", "C"},
		variantspec.Edge{FromNodeID: "B", ToNodeID: "C", Kind: "data"},
		variantspec.Edge{FromNodeID: "A", ToNodeID: "B", Kind: "data"})
	if _, err := GenerateTransform(resolvedFor(discoveredABC()), shuffled, t.TempDir()); err != nil {
		t.Fatalf("edges are a SET; re-listing them is not a rewire: %v", err)
	}

	// No discovered wiring recorded → nothing to compare against, so nothing is concluded. Refusing on
	// absent evidence would block every caller that assembled a Resolved by hand.
	unknown := &variantspec.Resolved{Language: "go", SourceRevision: "rev1"}
	if _, err := GenerateTransform(unknown, specWiring([]string{"A", "C", "B"}), t.TempDir()); err != nil {
		t.Fatalf("with no discovered wiring on record the engine must not invent a refusal: %v", err)
	}
}

// TestAdapterInsertionIsNotAWiringRefusal: an inserted adapter changes Order and Edges too — and it is
// the one wiring change that IS materializable, because EmitAdapter generates its source into the same
// diff (decisions.md D-2). Collapsing the adapter hop before comparing is what keeps the refusal aimed
// at un-materializable rewires only.
func TestAdapterInsertionIsNotAWiringRefusal(t *testing.T) {
	adapterID := "adapter:rename:A->B"
	spec := &variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		Order: []string{"A", adapterID, "B", "C"},
		Nodes: map[string]variantspec.NodeOverride{},
		Edges: []variantspec.Edge{
			{FromNodeID: "A", ToNodeID: adapterID, Kind: "data"},
			{FromNodeID: adapterID, ToNodeID: "B", Kind: "data"},
			{FromNodeID: "B", ToNodeID: "C", Kind: "data"},
		},
		InsertedAdapters: []variantspec.InsertedAdapter{{
			AdapterNodeID: adapterID, FromNodeID: "A", ToNodeID: "B",
			CatalogKind: string(typedcontract.KindRename),
			Params:      map[string]any{"renames": []map[string]any{{"from": "answer", "to": "response"}}},
			InSchema:    map[string]any{"type": "object"},
			OutSchema:   map[string]any{"type": "object"},
		}},
	}
	patch, err := GenerateTransform(resolvedFor(discoveredABC()), spec, t.TempDir())
	if err != nil {
		t.Fatalf("an adapter insertion is materialized as source and must not be refused as a rewire: %v", err)
	}
	if !strings.Contains(string(patch.Diff), AdapterPackageDir) {
		t.Fatalf("the adapter must appear as generated source in the diff, got %q", patch.Diff)
	}
}

// ── §4.3 — the refusal is observable and emits no diff ───────────────────────────────────────────

func TestWiringRefusalIsObservableNoDiff(t *testing.T) {
	pruned := specWiring([]string{"A", "B"})
	patch, err := GenerateTransform(resolvedFor(discoveredABC()), pruned, t.TempDir())
	re := assertWiringRefusal(t, err)

	if patch != nil {
		t.Fatalf("no diff may be emitted for a refused spec, got %d file(s)", len(patch.Files))
	}
	// Observable means a reader learns the axis, the node, and what was asked for — from the error
	// value itself, not from a log line.
	msg := re.Error()
	for _, want := range []string{"wiring", "node ", "refused"} {
		if !strings.Contains(strings.ToLower(msg), want) {
			t.Errorf("the refusal message must contain %q, got %q", want, msg)
		}
	}
	// And it says WHY it is a refusal rather than a no-op — the false-measurement argument travels with
	// the error, because the person reading it is the one deciding whether to work around it.
	if !strings.Contains(msg, "config_hash") {
		t.Errorf("the refusal must explain that a no-op would score a hash against unrewired source, got %q", msg)
	}

	// The plain Generate path refuses on the same evidence: a resolved config whose node SET differs
	// from the source's is refused with no patch, whatever the caller.
	r := resolvedFor(discoveredABC())
	r.Config = variantspec.ResolvedConfig{
		IRVersion: "1.0.0",
		Nodes:     []variantspec.ResolvedNode{{NodeID: "A"}, {NodeID: "B"}},
	}
	if p, err := Generate(r, t.TempDir()); err == nil {
		t.Fatalf("Generate must refuse a spec whose node set differs from the source's, got %+v", p)
	} else if re := assertWiringRefusal(t, err); !strings.Contains(re.Detail, "drops node") {
		t.Errorf("the refusal must name the dropped node, got %q", re.Detail)
	}
}
