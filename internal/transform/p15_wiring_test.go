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
	// A reorder: the source runs A→B→C, the spec asks for A→C→B.
	reordered := specWiring([]string{"A", "C", "B"},
		variantspec.Edge{FromNodeID: "A", ToNodeID: "C", Kind: "data"})
	patch, err := GenerateTransform(resolvedFor(discoveredABC()), reordered, t.TempDir())
	re := assertWiringRefusal(t, err)
	if patch != nil {
		t.Fatalf("a refused spec must produce no patch at all, got %+v", patch)
	}
	if re.NodeID == "" {
		t.Error("the refusal must name a node so the reader has somewhere to look")
	}
	if !strings.Contains(re.Detail, "order differs") {
		t.Errorf("the refusal must say WHICH rewire was asked for, got %q", re.Detail)
	}

	// A prune: same order prefix, one node fewer. Still a rewire, still refused.
	pruned := specWiring([]string{"A", "C"},
		variantspec.Edge{FromNodeID: "A", ToNodeID: "C", Kind: "data"})
	if _, err := GenerateTransform(resolvedFor(discoveredABC()), pruned, t.TempDir()); err == nil {
		t.Fatal("a prune changes the graph and must be refused too")
	}

	// A merge: two nodes fused into one.
	merged := specWiring([]string{"A", "C"},
		variantspec.Edge{FromNodeID: "A", ToNodeID: "C", Kind: "data"})
	if _, err := GenerateTransform(resolvedFor(discoveredABC()), merged, t.TempDir()); err == nil {
		t.Fatal("a merge changes the graph and must be refused too")
	}

	// An added edge with the SAME node order is still a wiring change — the graph is Order AND Edges.
	rewired := specWiring([]string{"A", "B", "C"},
		variantspec.Edge{FromNodeID: "A", ToNodeID: "B", Kind: "data"},
		variantspec.Edge{FromNodeID: "B", ToNodeID: "C", Kind: "data"},
		variantspec.Edge{FromNodeID: "A", ToNodeID: "C", Kind: "data"})
	err = nil
	if _, err = GenerateTransform(resolvedFor(discoveredABC()), rewired, t.TempDir()); err == nil {
		t.Fatal("an added edge is a wiring change and must be refused")
	}
	if re := assertWiringRefusal(t, err); !strings.Contains(re.Detail, "adds the edge") {
		t.Errorf("the refusal must name the added edge, got %q", re.Detail)
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
	reordered := specWiring([]string{"C", "B", "A"})
	patch, err := GenerateTransform(resolvedFor(discoveredABC()), reordered, t.TempDir())
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

	// The plain Generate path refuses on the same evidence, before it reads a single file: a spec whose
	// resolved node order differs from the source's is refused even with no VariantSpec in hand.
	r := resolvedFor(discoveredABC())
	r.Config = variantspec.ResolvedConfig{
		IRVersion: "1.0.0",
		Nodes:     []variantspec.ResolvedNode{{NodeID: "C"}, {NodeID: "B"}, {NodeID: "A"}},
	}
	if _, err := Generate(r, "/nonexistent-tree"); err == nil {
		t.Fatal("Generate must refuse a wiring-differing resolved config before it indexes the tree")
	} else {
		assertWiringRefusal(t, err)
	}
}

// ── §5.5 — an inserted adapter ships as generated source in the SAME reviewable diff ─────────────

// TestAdapterIsInReviewableDiff: the bridge a reviewer approves is the bridge that runs. The adapter
// appears in the patch as a new source file, in the same diff as the rest of the change, carrying its
// declared io_contract — and there is no other place a coercion could live, because the only thing
// this engine emits is that diff.
func TestAdapterIsInReviewableDiff(t *testing.T) {
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
			InSchema:    map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}},
			OutSchema:   map[string]any{"type": "object", "properties": map[string]any{"response": map[string]any{"type": "string"}}},
		}},
	}
	patch, err := GenerateTransform(resolvedFor(discoveredABC()), spec, t.TempDir())
	if err != nil {
		t.Fatalf("an adapted spec must generate a patch: %v", err)
	}

	// 1. The adapter is a FILE in the patch, under the generated-adapter directory.
	var path string
	for p := range patch.Files {
		if strings.HasPrefix(p, AdapterPackageDir+"/") {
			path = p
		}
	}
	if path == "" {
		t.Fatalf("no generated adapter source in the patch, got files %v", keysOf(patch.Files))
	}

	// 2. It is in the DIFF a human reads — not merely in the file map — and the diff shows it as new.
	diff := string(patch.Diff)
	if !strings.Contains(diff, "+++ b/"+path) || !strings.Contains(diff, "--- /dev/null") {
		t.Fatalf("the adapter must appear in the reviewable diff as a new file, got:\n%s", diff)
	}

	// 3. The generated source states WHAT it bridges and under WHICH contract, so the reviewer can
	// check the transformation rather than trust the word "adapter".
	src := string(patch.Files[path])
	for _, want := range []string{"A -> B", string(typedcontract.KindRename), "answer", "response"} {
		if !strings.Contains(src, want) {
			t.Errorf("generated adapter source must mention %q, got:\n%s", want, src)
		}
	}

	// 4. It is attributed in Touched, so the run record and the UI can name it like any other change.
	var attributed bool
	for _, td := range patch.Touched {
		if td.NodeID == adapterID && strings.HasPrefix(td.Dim, "adapter:") {
			attributed = true
		}
	}
	if !attributed {
		t.Errorf("the inserted adapter must be attributed in Touched, got %+v", patch.Touched)
	}

	// 5. Determinism: the same spec regenerates a byte-identical diff, so "the adapter I reviewed" and
	// "the adapter that ships" cannot drift apart between two runs.
	again, err := GenerateTransform(resolvedFor(discoveredABC()), spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if again.DiffHash != patch.DiffHash {
		t.Fatalf("adapter emission is not deterministic: %s vs %s", again.DiffHash, patch.DiffHash)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
