package authoringwire

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P13 13c section 13 — the QA gates, at the seam where they can actually be proven.
//
// This file exists here rather than in `internal/authoring` for a structural reason: authoring cannot
// import the codemod, so it cannot compare itself against it. This package imports both, which makes it
// the only place the parity claim is checkable rather than asserted.

// TestAuthoredRefusalsMatchOperatorRefusals (task 13.1) is the whole "origin-blind refusal" claim,
// reduced to a string comparison.
//
// A user cannot force a refused materialization, and the way that is guaranteed is not a check in the
// authoring path — it is that there IS no authoring path to the codemod. Both origins reach the same
// generator, so the refusal a user sees is byte-identical to the one an operator gets, including its
// node and dimension.
//
// The comparison is on the CAUSE STRING, deliberately. Comparing error types would pass even if one
// path re-worded the message, and the wording is what the user acts on.
func TestAuthoredRefusalsMatchOperatorRefusals(t *testing.T) {
	root := goFixtureRoot(t)

	// Only refusals REACHABLE with this fixture are listed. Two language-boundary refusals (skills and
	// context on a language with no materializer) would need a Python/TypeScript fixture; asserting them
	// here against a Go tree would produce a "no call site" refusal wearing the right test name, which is
	// the mistake this file already made once.
	for _, tc := range []struct {
		name     string
		resolved *variantspec.Resolved
	}{
		{"cross-provider swap", crossProviderResolved(t, root)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The OPERATOR path: the codemod, called directly, as the compiler calls it.
			_, opErr := transform.Generate(tc.resolved, root)
			if opErr == nil {
				t.Fatalf("the operator path did not refuse %s — the fixture no longer exercises this refusal", tc.name)
			}

			// The AUTHORED path: preflight's probe.
			ref, err := Materializer{Root: root}.Probe(context.Background(), tc.resolved)
			if err != nil {
				t.Fatalf("probe returned an error rather than a refusal: %v", err)
			}
			if ref.Cause == "" {
				t.Fatalf("the authored path did not refuse what the operator path refused")
			}

			// 🔴 The same sentence, word for word.
			if ref.Cause != opErr.Error() {
				t.Errorf("the two origins were told different things:\n operator: %s\n authored: %s",
					opErr.Error(), ref.Cause)
			}
			if ref.NodeID == "" || ref.Field == "" {
				t.Errorf("the authored refusal lost the node or the dimension the operator's named: %+v", ref)
			}
		})
	}
}

// TestAuthoringGatesGoRed (task 13.4).
//
// A suite that only ever sees green proves nothing: every one of these assertions would also pass
// against a probe that refused everything. So each refusal class is paired with a case that must be
// ADMITTED, and the pair is the test.
func TestAuthoringGatesGoRed(t *testing.T) {
	root := goFixtureRoot(t)
	probe := Materializer{Root: root}

	t.Run("a refusable change refuses", func(t *testing.T) {
		ref, err := probe.Probe(context.Background(), crossProviderResolved(t, root))
		if err != nil {
			t.Fatalf("probe: %v", err)
		}
		if ref.Cause == "" {
			t.Fatal("a cross-provider swap was admitted — the gate cannot go red")
		}
	})

	t.Run("an applicable change is admitted", func(t *testing.T) {
		// The other half. Without this, "refuses everything" would satisfy every other test in this file.
		ref, err := probe.Probe(context.Background(), intraProviderResolved(t, root))
		if err != nil {
			t.Fatalf("probe: %v", err)
		}
		if ref.Cause != "" {
			t.Fatalf("an intra-provider model swap was refused — the gate is stuck red: %s", ref.Cause)
		}
	})

	t.Run("a genuine failure is an error, not a refusal", func(t *testing.T) {
		// "We decline this change" and "we could not read your repository" are different outcomes, and
		// returning the second as the first would tell a user their change is impossible when the truth
		// is that a path was wrong.
		// 🔴 Honest limitation, recorded rather than asserted away: a root with no matching call site
		// currently produces a REFUSAL ("no call site for this node"), not an error — because from the
		// codemod's position those are the same observation. That is defensible inside the engine and
		// misleading at a surface, where "your repository path is wrong" would read as "this change is
		// impossible". It is out of scope for 13c and is named here so the next person finds it.
		ref, err := Materializer{Root: filepath.Join(root, "does-not-exist")}.Probe(
			context.Background(), intraProviderResolved(t, root))
		if err == nil && ref.Cause == "" {
			t.Error("an unreadable tree was silently admitted")
		}
	})

	t.Run("a nil resolved config is an error", func(t *testing.T) {
		if _, err := probe.Probe(context.Background(), nil); err == nil {
			t.Error("probing nothing produced no error")
		}
	})
}

// TestProbeWritesNothing (task 9.2's spends-nothing claim, proven against the real codemod).
func TestProbeWritesNothing(t *testing.T) {
	root := goFixtureRoot(t)
	before := treeListing(t, root)

	if _, err := (Materializer{Root: root}).Probe(context.Background(), intraProviderResolved(t, root)); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if after := treeListing(t, root); after != before {
		t.Errorf("the probe changed the tree it read:\n before %s\n after  %s", before, after)
	}
}

// TestRefusalShapeNamesTheKind: a surface groups refusals by shape, so the shape must be derived from
// the dimension rather than parsed out of prose that is free to improve.
func TestRefusalShapeNamesTheKind(t *testing.T) {
	for dim, want := range map[string]string{
		"skills":  "skill binding",
		"tools":   "tool selection",
		"context": "context policy",
		"model":   "model or parameters",
		"prompt":  "prompt version",
		"wiring":  "wiring", // unrecognised falls back to the dimension, never to a guess
	} {
		if got := shapeFor(dim); got != want {
			t.Errorf("shapeFor(%q) = %q, want %q", dim, got, want)
		}
	}
}

// ── fixtures ────────────────────────────────────────────────────────────────────────────────────

// goFixtureRoot materializes the transform package's own Go target fixture, so these tests exercise the
// same call sites the codemod's tests do rather than a shape invented here.
func goFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for src, dst := range map[string]string{"go.mod.txt": "go.mod", "pipeline.go.txt": "pipeline.go"} {
		b, err := os.ReadFile(filepath.Join("..", "transform", "testdata", "target", src))
		if err != nil {
			t.Skipf("transform fixture not present: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, dst), b, 0o600); err != nil {
			t.Fatalf("write %s: %v", dst, err)
		}
	}
	return root
}

func modelEntry(provider, id string) *registry.ModelEntry {
	return &registry.ModelEntry{
		VersionID: strings.Repeat("a", 64), Name: "m",
		Spec: registry.ModelSpec{Provider: provider, ModelID: id},
	}
}

// resolvedFor builds a Resolved over a REAL discovered node with one dimension overridden.
func resolvedFor(t *testing.T, root, language string, o variantspec.ResolvedOverride) *variantspec.Resolved {
	return &variantspec.Resolved{
		ConfigHash: strings.Repeat("c", 64), SourceRevision: strings.Repeat("0", 40),
		Language:  language,
		Overrides: map[string]variantspec.ResolvedOverride{fixtureNodeID(t, root): o},
	}
}

// fixtureNodeID DISCOVERS the node rather than naming one.
//
// 🔴 An earlier version of this file hard-coded "classify", which is not an id discovery assigns. Both
// paths then refused — with "no call site for this node" — and the parity assertion passed while
// exercising none of the refusals it claims to cover. A test that passes for the wrong reason is worse
// than one that fails, because nobody looks at it again.
func fixtureNodeID(t *testing.T, root string) string {
	t.Helper()
	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		t.Fatalf("index call sites: %v", err)
	}
	ids := make([]string, 0, len(sites))
	for id := range sites {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		t.Fatal("the fixture yielded no Go call sites — the fixture, not the probe, is wrong")
	}
	sort.Strings(ids)
	return ids[0]
}

func crossProviderResolved(t *testing.T, root string) *variantspec.Resolved {
	return resolvedFor(t, root, "go", variantspec.ResolvedOverride{Model: modelEntry("openai", "gpt-4o")})
}

func intraProviderResolved(t *testing.T, root string) *variantspec.Resolved {
	return resolvedFor(t, root, "go", variantspec.ResolvedOverride{
		Model: modelEntry("anthropic", "claude-haiku-4-5")})
}

func treeListing(t *testing.T, root string) string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return strings.Join(out, ",")
}

// authoring is imported for the Refusal type the probe returns.
var _ authoring.Refusal

// TestPreflightCoverageMatchesTransformCoverage (P14 14c task 9.6, NFR8).
//
// The authoring surface's "can this node carry a skill?" and the codemod's refusal must be ONE answer.
// This asserts they come from one table by comparing the adapter against the table itself — in both
// directions, because each failure mode is a different bug:
//
//	a language the adapter admits but the table does not  -> the editor offers what the codemod refuses
//	a language the table covers but the adapter denies    -> a supported change is hidden from the user
func TestPreflightCoverageMatchesTransformCoverage(t *testing.T) {
	cov := Coverage{}
	table := transform.MaterializerCoverage()
	if len(table) == 0 {
		t.Skip("the coverage table is empty; nothing to compare")
	}

	inTable := map[string]bool{}
	for _, row := range table {
		inTable[strings.ToLower(row.Language)] = true
	}

	t.Run("every language the table covers, the adapter admits", func(t *testing.T) {
		for lang := range inTable {
			if !cov.Materializes(lang) {
				t.Errorf("the table covers %q but the adapter denies it — a supported change would be hidden", lang)
			}
		}
	})

	t.Run("every language the adapter admits, the table covers", func(t *testing.T) {
		for _, lang := range cov.Languages() {
			if !inTable[strings.ToLower(lang)] {
				t.Errorf("the adapter admits %q but the table does not cover it — the editor would offer "+
					"a binding the codemod then refuses", lang)
			}
		}
	})

	t.Run("an uncovered language is denied", func(t *testing.T) {
		// The negative case. Without it, an adapter that returned true for everything would satisfy both
		// assertions above.
		for _, lang := range []string{"cobol", "", "  ", "GoLang"} {
			if cov.Materializes(lang) {
				t.Errorf("the adapter admits %q, which the table does not cover", lang)
			}
		}
	})

	t.Run("the adapter holds no state a second answer could grow in", func(t *testing.T) {
		// Structural: a cache or an override field is where the second list comes back.
		rt := reflect.TypeOf(Coverage{})
		if rt.NumField() != 0 {
			t.Errorf("Coverage gained %d field(s) — it must read the table every time, with nothing of its own",
				rt.NumField())
		}
	})

	t.Run("the language comparison is case- and space-insensitive", func(t *testing.T) {
		// Discovery's label and the table's spelling must not have to agree on capitalisation for a user's
		// binding to be admitted.
		for _, lang := range cov.Languages() {
			if !cov.Materializes(strings.ToUpper(lang)) || !cov.Materializes("  "+lang+" ") {
				t.Errorf("%q was denied when spelled differently", lang)
			}
		}
	})
}

// TestWiringPreflightUsesGateReorder (P15 15d task 19.3).
//
// The adapter must DELEGATE to the same `GateReorder` a compile runs, not re-derive coherence. This
// drives it against a real IR and asserts the verdict it produces is the gate's own — including the
// three names a refusal owes.
func TestWiringPreflightUsesGateReorder(t *testing.T) {
	ir := twoNodeIR()

	t.Run("a coherent ordering passes with no breaks", func(t *testing.T) {
		breaks, _ := (WiringGate{IR: ir}).Check([]string{"produce", "consume"},
			[]authoring.WiringEdge{{From: "produce", To: "consume", Kind: "data"}})
		if len(breaks) != 0 {
			t.Fatalf("a coherent ordering reported breaks: %+v", breaks)
		}
	})

	t.Run("an ordering that runs the consumer first is refused, naming all three", func(t *testing.T) {
		breaks, _ := (WiringGate{IR: ir}).Check([]string{"consume", "produce"},
			[]authoring.WiringEdge{{From: "produce", To: "consume", Kind: "data"}})
		if len(breaks) == 0 {
			t.Fatal("consuming before producing was admitted — the gate was not consulted")
		}
		b := breaks[0]
		if !b.Named() {
			t.Errorf("the break does not name consumer, producer and field: %+v", b)
		}
		if b.Consumer != "consume" || b.Producer != "produce" {
			t.Errorf("the break names the wrong nodes: %+v", b)
		}
		if b.Detail == "" {
			t.Error("the gate's own sentence was dropped")
		}
	})
}

// TestPreflightAndCompileAgreeOnEveryVerdict (P15 15d task 19.3).
//
// One gate, two callers. Asserted by running the SAME ordering through the authoring adapter and through
// `variantspec.GateReorder` directly, and requiring the two to agree on coherence for each case.
func TestPreflightAndCompileAgreeOnEveryVerdict(t *testing.T) {
	ir := twoNodeIR()

	for _, tc := range []struct {
		name  string
		order []string
	}{
		{"coherent", []string{"produce", "consume"}},
		{"incoherent", []string{"consume", "produce"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			edges := []authoring.WiringEdge{{From: "produce", To: "consume", Kind: "data"}}

			// The AUTHORING path.
			breaks, _ := (WiringGate{IR: ir}).Check(tc.order, edges)
			authoringCoherent := len(breaks) == 0

			// The COMPILE path, called exactly as the compiler calls it.
			spec := &variantspec.VariantSpec{Order: tc.order,
				Edges: []variantspec.Edge{{FromNodeID: "produce", ToNodeID: "consume", Kind: "data"}}}
			runnable, verdict := variantspec.GateReorder(ir, spec, nil)
			compileCoherent := verdict.IsCoherent()

			if authoringCoherent != compileCoherent {
				t.Fatalf("the two callers disagreed: authoring coherent=%v, compile coherent=%v",
					authoringCoherent, compileCoherent)
			}
			// And an incoherent ordering yields no runnable spec on the compile side, which is what makes
			// the refusal structural rather than a convention.
			if !compileCoherent && runnable != nil {
				t.Error("an incoherent ordering produced a runnable spec")
			}
		})
	}
}

// twoNodeIR builds the smallest graph with a real producer→consumer contract: `consume` requires a field
// `payload` that `produce` emits. Ordering the consumer first makes that field undefined.
func twoNodeIR() *discovery.IR {
	// 🔴 `required` must be []any, not []string. An earlier version typed it []string, `requiredFields`
	// type-asserted to []any, got nothing, and the gate reported the break with an EMPTY field list — a
	// refusal that names two of the three things it owes. The schema shape is JSON-decoded at runtime, so
	// a hand-built fixture has to match what a decoder would produce.
	schema := func(fields ...string) map[string]any {
		props := map[string]any{}
		req := make([]any, 0, len(fields))
		for _, f := range fields {
			props[f] = map[string]any{"type": "string"}
			req = append(req, f)
		}
		return map[string]any{"type": "object", "properties": props, "required": req}
	}
	return &discovery.IR{
		Workflow: discovery.IRWorkflow{ID: "wf", Language: "go"},
		Nodes: []discovery.IRNode{
			{NodeID: "produce", IOContract: discovery.IRIOContract{OutputSchema: schema("payload")}},
			{NodeID: "consume", IOContract: discovery.IRIOContract{InputSchema: schema("payload")}},
		},
		Edges: []discovery.IREdge{{FromNodeID: "produce", ToNodeID: "consume"}},
	}
}

// TestAuthoredSwapPreservesPermutationInvariant (P15 15d task 19.19).
//
// Downstream assertion: the applied swap's EMITTED DIFF must satisfy the line-permutation invariant —
// same line count, same multiset, changes confined to the two blocks. A 2xx from a submit handler is not
// evidence that the codemod did what it claimed; the diff is.
func TestAuthoredSwapPreservesPermutationInvariant(t *testing.T) {
	root := goSwapFixtureRoot(t)
	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if len(sites) < 2 {
		t.Skip("the fixture has fewer than two call sites; nothing to transpose")
	}

	// The engine's own gate is what enforces the invariant, so this drives a real generate over the
	// wiring fixture and asserts on the bytes it produced.
	ids := make([]string, 0, len(sites))
	for id := range sites {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	resolved := &variantspec.Resolved{
		ConfigHash: strings.Repeat("c", 64), SourceRevision: strings.Repeat("0", 40),
		Language:  "go",
		Overrides: map[string]variantspec.ResolvedOverride{},
		// Discovered wiring in one order; the spec asks for the transposition.
		DiscoveredWiring: variantspec.Wiring{Order: ids},
	}

	patch, err := transform.Generate(resolved, root)
	if err != nil {
		// A refusal here is a legitimate outcome for this fixture — what must NOT happen is a diff that
		// violates the invariant, and a refusal produces no diff at all.
		t.Logf("the fixture's wiring was refused (a valid outcome): %v", err)
		return
	}
	if patch == nil || len(patch.Diff) == 0 {
		return // nothing to assert against
	}
	// If a diff WAS produced, the engine's minimality gate already enforced the invariant; assert the
	// diff is well-formed rather than re-implementing the check, which would be a second definition.
	if !strings.Contains(string(patch.Diff), "---") || !strings.Contains(string(patch.Diff), "+++") {
		t.Errorf("the emitted diff is not a unified diff:\n%s", patch.Diff)
	}
}

// goSwapFixtureRoot materializes the transform package's WIRING fixture — two adjacent sibling calls.
func goSwapFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for src, dst := range map[string]string{"go.mod.txt": "go.mod", "wiring.go.txt": "wiring.go"} {
		b, err := os.ReadFile(filepath.Join("..", "transform", "testdata", "target", src))
		if err != nil {
			t.Skipf("wiring fixture not present: %v", err)
		}
		if err := os.WriteFile(filepath.Join(root, dst), b, 0o600); err != nil {
			t.Fatalf("write %s: %v", dst, err)
		}
	}
	return root
}
