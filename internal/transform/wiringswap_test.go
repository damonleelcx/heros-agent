package transform

import (
	"errors"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P15 15c §12 — the block-swap edit class and its permutation gate.
//
// 🔴 The gate is the whole safety argument for this rewriter. A reviewer who reads nothing but the
// assertion "the output is the input's lines, reordered" already knows the change cannot have altered
// what a single line SAYS — so these tests are not about the mechanics of splicing, they are about
// whether that assertion can be violated. Each one tries to violate it a different way.

// swapEdits builds the two half-edits of a transposition: block A's bytes take block B's place and
// vice versa. This is the shape the materializer emits; here it is hand-built so the GATE can be
// tested independently of the language analysis that decides whether a swap is admissible at all.
func swapEdits(src string, aStart, aEnd, bStart, bEnd int) []edit {
	return []edit{
		{Start: aStart, End: aEnd, New: src[bStart:bEnd], NodeID: "A", Dim: wiringRefusalDim, Swap: true},
		{Start: bStart, End: bEnd, New: src[aStart:aEnd], NodeID: "B", Dim: wiringRefusalDim, Swap: true},
	}
}

// twoStatements is a three-line body whose middle two lines are the swappable pair.
const twoStatements = "package p\n\nfunc f() {\n\ta := one()\n\tb := two()\n\t_ = a\n\t_ = b\n}\n"

func offsetsOf(t *testing.T, src, first, second string) (int, int, int, int) {
	t.Helper()
	i := strings.Index(src, first)
	j := strings.Index(src, second)
	if i < 0 || j < 0 {
		t.Fatalf("fixture does not contain %q / %q", first, second)
	}
	return i, i + len(first), j, j + len(second)
}

func TestBlockSwapAppliesAsPermutation(t *testing.T) {
	src := twoStatements
	a1, a2, b1, b2 := offsetsOf(t, src, "\ta := one()\n", "\tb := two()\n")
	edits := swapEdits(src, a1, a2, b1, b2)

	out, err := applyEdits([]byte(src), edits)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "package p\n\nfunc f() {\n\tb := two()\n\ta := one()\n\t_ = a\n\t_ = b\n}\n"
	if string(out) != want {
		t.Fatalf("swap produced:\n%q\nwant:\n%q", out, want)
	}
	// The gate admits it: same count, same multiset, changes confined to the two blocks.
	if err := gateSwapPermutation("f.go", []byte(src), out, edits); err != nil {
		t.Fatalf("a real transposition must pass its own gate: %v", err)
	}
}

// TestSwapIsSelfInverse — task 12.3. A transposition applied to its own output returns the original
// bytes. This is not a curiosity: it is what makes a wiring change trivially revertible, and a swap
// that were not self-inverse would be doing something other than exchanging two blocks.
func TestSwapIsSelfInverse(t *testing.T) {
	src := twoStatements
	a1, a2, b1, b2 := offsetsOf(t, src, "\ta := one()\n", "\tb := two()\n")
	once, err := applyEdits([]byte(src), swapEdits(src, a1, a2, b1, b2))
	if err != nil {
		t.Fatal(err)
	}
	s2 := string(once)
	c1, c2, d1, d2 := offsetsOf(t, s2, "\tb := two()\n", "\ta := one()\n")
	twice, err := applyEdits(once, swapEdits(s2, c1, c2, d1, d2))
	if err != nil {
		t.Fatal(err)
	}
	if string(twice) != src {
		t.Fatalf("a transposition must be its own inverse:\n got %q\nwant %q", twice, src)
	}
}

// TestSwapGateRejectsNonPermutation — 🔴 task 12.2 / 16.2. Three ways to break the invariant, three
// rejections. Each case is something a plausible "improvement" to a mover would do.
func TestSwapGateRejectsNonPermutation(t *testing.T) {
	src := []byte(twoStatements)
	a1, a2, b1, b2 := offsetsOf(t, twoStatements, "\ta := one()\n", "\tb := two()\n")
	edits := swapEdits(twoStatements, a1, a2, b1, b2)

	cases := []struct {
		what string
		out  string
		want string
	}{
		{
			// A mover that "tidied" the indentation while carrying the line. The count is right, the
			// order is right, and the file still compiles — this is the failure the multiset check exists
			// for, and the only one a reader could not spot in a diff summary.
			what: "a line edited while being moved",
			out:  "package p\n\nfunc f() {\n\tb := two()\n\ta := one()  // moved\n\t_ = a\n\t_ = b\n}\n",
			want: "did not preserve",
		},
		{
			what: "a line dropped",
			out:  "package p\n\nfunc f() {\n\tb := two()\n\t_ = a\n\t_ = b\n}\n",
			want: "line(s) to",
		},
		{
			// The two blocks were exchanged correctly AND something else moved with them.
			what: "a line outside the two blocks disturbed",
			out:  "package p\n\nfunc f() {\n\tb := two()\n\ta := one()\n\t_ = b\n\t_ = a\n}\n",
			want: "outside both swapped statements",
		},
	}
	for _, c := range cases {
		err := gateSwapPermutation("f.go", src, []byte(c.out), edits)
		if err == nil {
			t.Errorf("%s: the gate admitted a non-permutation", c.what)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: refusal must say %q, got %v", c.what, c.want, err)
		}
	}
}

// TestValueRewriteGateUnchanged — task 12.2's other half, and the reason the swap is a separate class.
// The original no-newline / no-multi-line rule must still fire for a VALUE rewrite; if adding the swap
// class had loosened it, this test would pass silently and every rewriter would have lost its check.
func TestValueRewriteGateUnchanged(t *testing.T) {
	src := []byte("package p\n\nfunc f() {\n\tx := \"a\"\n}\n")
	i := strings.Index(string(src), `"a"`)
	newline := []edit{{Start: i, End: i + 3, New: "\"a\\n\" +\n\t\t\"b\"", NodeID: "n", Dim: "prompt"}}
	err := gateMinimal("f.go", src, []byte("irrelevant"), newline, map[int]bool{4: true}, reparseGo)
	if err == nil || !strings.Contains(err.Error(), "newline") {
		t.Fatalf("a value rewrite that emits a newline must still be refused, got %v", err)
	}

	// And a swap-flagged edit must NOT be able to smuggle a value rewrite past the permutation gate:
	// changing what a line says is exactly what the multiset check forbids.
	smuggled := []edit{{Start: i, End: i + 3, New: `"CHANGED"`, NodeID: "n", Dim: wiringRefusalDim, Swap: true}}
	out, err := applyEdits(src, smuggled)
	if err != nil {
		t.Fatal(err)
	}
	if err := gateSwapPermutation("f.go", src, out, smuggled); err == nil {
		t.Fatal("the swap gate must reject an edit that changes a line's content, however it is labelled")
	}
}

// ── §13 — the Go statement materializer ─────────────────────────────────────────────────────────

// goSwapFixture is a function body whose two middle statements call out to different services and
// share nothing: the canonical swappable pair.
const goSwapFixture = `package p

func run(ctx C) {
	setup(ctx)
	a := summarize(ctx)
	b := classify(ctx)
	report(a, b)
}
`

// materializeGo is the section's harness: resolve both statements, admit or refuse, and (on admission)
// return the swapped bytes so a test can assert on the RESULT rather than on the plan.
func materializeGo(t *testing.T, src string, lineA, lineB int) ([]byte, []edit, error) {
	t.Helper()
	edits, err := materializeSwap("go", []byte(src), &swapPlan{First: "A", Second: "B"}, lineA, lineB)
	if err != nil {
		return nil, nil, err
	}
	out, applyErr := applyEdits([]byte(src), edits)
	if applyErr != nil {
		t.Fatalf("apply: %v", applyErr)
	}
	if gateErr := gateSwapPermutation("p.go", []byte(src), out, edits); gateErr != nil {
		t.Fatalf("a materialized swap must pass its own gate: %v", gateErr)
	}
	return out, edits, nil
}

func TestGoSwapSiblingStatements(t *testing.T) {
	out, edits, err := materializeGo(t, goSwapFixture, 5, 6)
	if err != nil {
		t.Fatalf("two independent sibling statements must be swappable: %v", err)
	}
	want := `package p

func run(ctx C) {
	setup(ctx)
	b := classify(ctx)
	a := summarize(ctx)
	report(a, b)
}
`
	if string(out) != want {
		t.Fatalf("swap produced:\n%s\nwant:\n%s", out, want)
	}
	if len(edits) != 2 || !edits[0].Swap || !edits[1].Swap {
		t.Fatalf("a materialized reorder must emit two swap-class edits, got %+v", edits)
	}
	if edits[0].Dim != "wiring" {
		t.Errorf("the edits must be attributed to the wiring axis, got %q", edits[0].Dim)
	}
	// The result still parses — the gate's own check, asserted here too because a swap that produced
	// unparseable Go would be the one failure mode a permutation check cannot see.
	if err := reparseGo("p.go", []byte(goSwapFixture), out); err != nil {
		t.Fatalf("the swapped file must still parse as Go: %v", err)
	}
}

// TestGoSwapRefusesDataDependency — 🔴 task 13.2. The pair that MUST NOT be swapped: the second
// statement consumes what the first produces. Nothing about the wiring spec knows this; only the
// materializer does, which is why it is the materializer's job to refuse.
func TestGoSwapRefusesDataDependency(t *testing.T) {
	dependent := `package p

func run(ctx C) {
	a := summarize(ctx)
	b := classify(a)
	report(b)
}
`
	_, _, err := materializeGo(t, dependent, 4, 5)
	if err == nil {
		t.Fatal("a statement that reads what the previous one binds must NOT be swapped")
	}
	if !strings.Contains(err.Error(), `reads "a"`) {
		t.Errorf("the refusal must name the shared identifier, got %v", err)
	}

	// The mirror direction, and the same-target case: both statements binding one name means their
	// order decides which value survives.
	sameTarget := `package p

func run(ctx C) {
	out := summarize(ctx)
	out = classify(ctx)
	report(out)
}
`
	if _, _, err := materializeGo(t, sameTarget, 4, 5); err == nil {
		t.Fatal("two statements binding the same name must NOT be swapped")
	} else if !strings.Contains(err.Error(), "both statements bind") {
		t.Errorf("the refusal must say both statements bind it, got %v", err)
	}
}

// TestGoSwapRefusesControlFlowAndComments — 🔴 task 13.3.
func TestGoSwapRefusesControlFlowAndComments(t *testing.T) {
	withReturn := `package p

func run(ctx C) bool {
	a := summarize(ctx)
	return classify(ctx)
}
`
	if _, _, err := materializeGo(t, withReturn, 4, 5); err == nil {
		t.Fatal("a return statement must never be exchanged with a neighbour")
	} else if !strings.Contains(err.Error(), "return statement") {
		t.Errorf("the refusal must name the control-flow kind, got %v", err)
	}

	withComment := `package p

func run(ctx C) {
	a := summarize(ctx)
	// classify AFTER summarize so the operator sees both
	b := classify(ctx)
	report(a, b)
}
`
	if _, _, err := materializeGo(t, withComment, 4, 6); err == nil {
		t.Fatal("a comment between the two statements must block the swap")
	} else if !strings.Contains(err.Error(), "not consecutive") {
		t.Errorf("the refusal must say they are not consecutive, got %v", err)
	}

	nested := `package p

func run(ctx C) {
	a := summarize(ctx)
	if ok {
		b := classify(ctx)
		_ = b
	}
	_ = a
}
`
	if _, _, err := materializeGo(t, nested, 4, 6); err == nil {
		t.Fatal("two statements at different nesting are not siblings and must not be swapped")
	} else if !strings.Contains(err.Error(), "different nesting") {
		t.Errorf("the refusal must say they are at different nesting, got %v", err)
	}
}

// TestGoSwapOutputIsGofmtStable — task 13.4. A permutation of gofmt-clean lines is gofmt-clean: the
// swap must never leave a file the target's CI would reformat, because a reformat in a later commit
// would make the wiring change look like it touched code it did not.
func TestGoSwapOutputIsGofmtStable(t *testing.T) {
	out, _, err := materializeGo(t, goSwapFixture, 5, 6)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := format.Source(out)
	if err != nil {
		t.Fatalf("the swapped file must be formattable: %v", err)
	}
	if string(formatted) != string(out) {
		t.Fatalf("gofmt would reformat the swapped file:\n got %s\nwant %s", out, formatted)
	}
}

// ── §14 — the Python statement materializer ─────────────────────────────────────────────────────

const pySwapFixture = `def run(ctx):
    setup(ctx)
    a = summarize(ctx)
    b = classify(ctx)
    return report(a, b)
`

func materializePy(t *testing.T, src string, lineA, lineB int) ([]byte, error) {
	t.Helper()
	edits, err := materializeSwap("python", []byte(src), &swapPlan{First: "A", Second: "B"}, lineA, lineB)
	if err != nil {
		return nil, err
	}
	out, applyErr := applyEdits([]byte(src), edits)
	if applyErr != nil {
		t.Fatalf("apply: %v", applyErr)
	}
	if gateErr := gateSwapPermutation("p.py", []byte(src), out, edits); gateErr != nil {
		t.Fatalf("a materialized swap must pass its own gate: %v", gateErr)
	}
	return out, nil
}

func TestPythonSwapSiblingStatements(t *testing.T) {
	out, err := materializePy(t, pySwapFixture, 3, 4)
	if err != nil {
		t.Fatalf("two independent sibling statements must be swappable: %v", err)
	}
	want := `def run(ctx):
    setup(ctx)
    b = classify(ctx)
    a = summarize(ctx)
    return report(a, b)
`
	if string(out) != want {
		t.Fatalf("swap produced:\n%s\nwant:\n%s", out, want)
	}

	// A multi-line call travels whole: the logical statement ends where its brackets balance, not where
	// the discovered call site's line happens to be.
	multi := `def run(ctx):
    a = summarize(
        ctx,
        depth=2,
    )
    b = classify(ctx)
    return (a, b)
`
	out, err = materializePy(t, multi, 2, 6)
	if err != nil {
		t.Fatalf("a multi-line statement must swap as one unit: %v", err)
	}
	if !strings.HasPrefix(string(out), "def run(ctx):\n    b = classify(ctx)\n    a = summarize(\n") {
		t.Fatalf("the whole logical statement must move together, got:\n%s", out)
	}
}

// TestPythonSwapRefusesDataDependency — 🔴 task 14.2.
func TestPythonSwapRefusesDataDependency(t *testing.T) {
	dependent := `def run(ctx):
    a = summarize(ctx)
    b = classify(a)
    return b
`
	if _, err := materializePy(t, dependent, 2, 3); err == nil {
		t.Fatal("a statement that reads what the previous one binds must NOT be swapped")
	} else if !strings.Contains(err.Error(), `reads "a"`) {
		t.Errorf("the refusal must name the shared identifier, got %v", err)
	}

	// An attribute write is a mutation the next statement may depend on, so the base name binds AND
	// reads. A resolver that treated `state.x = …` as binding nothing would let this pair through.
	mutation := `def run(ctx, state):
    state.summary = summarize(ctx)
    b = classify(state)
    return b
`
	if _, err := materializePy(t, mutation, 2, 3); err == nil {
		t.Fatal("a mutation the next statement reads must NOT be swapped")
	}
}

// TestPythonSwapRefusesUnanalysable — 🔴 the discipline that makes a line-based resolver honest:
// "I cannot tell what this binds" is a refusal, never an assumption of independence.
func TestPythonSwapRefusesUnanalysable(t *testing.T) {
	cases := []struct {
		what, src    string
		lineA, lineB int
		want         string
	}{
		{
			what: "tuple unpacking",
			src: `def run(ctx):
    a, meta = summarize(ctx)
    b = classify(ctx)
    return (a, b)
`, lineA: 2, lineB: 3, want: "does not model",
		},
		{
			what: "a walrus binding inside an expression",
			src: `def run(ctx):
    send(x := summarize(ctx))
    b = classify(ctx)
    return b
`, lineA: 2, lineB: 3, want: "walrus",
		},
		{
			what: "a backslash continuation",
			src: `def run(ctx):
    a = summarize(ctx) \
        or fallback(ctx)
    b = classify(ctx)
    return b
`, lineA: 2, lineB: 4, want: "backslash",
		},
	}
	for _, c := range cases {
		if _, err := materializePy(t, c.src, c.lineA, c.lineB); err == nil {
			t.Errorf("%s: must be refused, not assumed independent", c.what)
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: refusal must say %q, got %v", c.what, c.want, err)
		}
	}
}

// TestPythonSwapRefusesControlFlowAndComments — 🔴 task 14.3.
func TestPythonSwapRefusesControlFlowAndComments(t *testing.T) {
	withReturn := `def run(ctx):
    a = summarize(ctx)
    return classify(ctx)
`
	if _, err := materializePy(t, withReturn, 2, 3); err == nil {
		t.Fatal("a return statement must never be exchanged with a neighbour")
	} else if !strings.Contains(err.Error(), "return statement") {
		t.Errorf("the refusal must name the control-flow kind, got %v", err)
	}

	withComment := `def run(ctx):
    a = summarize(ctx)
    # classify second so the summary is in the log first
    b = classify(ctx)
    return (a, b)
`
	if _, err := materializePy(t, withComment, 2, 4); err == nil {
		t.Fatal("a comment between the two statements must block the swap")
	} else if !strings.Contains(err.Error(), "not consecutive") {
		t.Errorf("the refusal must say they are not consecutive, got %v", err)
	}

	nested := `def run(ctx):
    a = summarize(ctx)
    if ok:
        b = classify(ctx)
    return a
`
	if _, err := materializePy(t, nested, 2, 4); err == nil {
		t.Fatal("statements at different indentation are not siblings and must not be swapped")
	} else if !strings.Contains(err.Error(), "different nesting") {
		t.Errorf("the refusal must say they are at different nesting, got %v", err)
	}
}

// ── §15 — materialize-or-refuse routing, end to end on a real tree ──────────────────────────────

// wiringTree returns the fixture root plus the two node ids of `wiring.go`'s adjacent pair, in
// DISCOVERED order. Discovery is what decides that order, so the test reads it rather than assuming it.
func wiringTree(t *testing.T) (root, firstNode, secondNode string, discovered variantspec.Wiring) {
	t.Helper()
	root = newTarget(t)
	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	type site struct {
		id   string
		line int
	}
	var inWiring []site
	for id, s := range sites {
		if s.FileRel == "wiring.go" {
			inWiring = append(inWiring, site{id, s.LineStart})
		}
	}
	if len(inWiring) != 2 {
		t.Fatalf("the wiring fixture must contribute exactly two call sites, got %d", len(inWiring))
	}
	sort.Slice(inWiring, func(i, j int) bool { return inWiring[i].line < inWiring[j].line })
	firstNode, secondNode = inWiring[0].id, inWiring[1].id
	discovered = variantspec.Wiring{Order: []string{firstNode, secondNode}}
	return root, firstNode, secondNode, discovered
}

func reorderedResolved(discovered variantspec.Wiring, first, second string) *variantspec.Resolved {
	return &variantspec.Resolved{
		ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1", Language: "go",
		Config: variantspec.ResolvedConfig{
			IRVersion: "1.0.0",
			Nodes:     []variantspec.ResolvedNode{{NodeID: second}, {NodeID: first}}, // the swap
		},
		DiscoveredWiring: discovered,
	}
}

// TestOnlyAdjacentTranspositionAttempted — task 15.1. The plan admits ONE shape and rejects every
// other, including shapes that are permutations but not single transpositions.
func TestOnlyAdjacentTranspositionAttempted(t *testing.T) {
	disc := variantspec.Wiring{Order: []string{"a", "b", "c"}}
	edges := []variantspec.ResolvedEdge(nil)

	if _, ok := planWiringSwap(disc, []string{"b", "a", "c"}, edges); !ok {
		t.Error("one adjacent transposition is the shape this engine materializes")
	}
	cases := []struct {
		what  string
		order []string
		edges []variantspec.ResolvedEdge
	}{
		{"a merge (a node left)", []string{"a", "b"}, edges},
		{"a non-adjacent move", []string{"c", "b", "a"}, edges},
		{"a three-cycle — two moves wearing one name", []string{"b", "c", "a"}, edges},
		{"an added edge", []string{"a", "b", "c"}, []variantspec.ResolvedEdge{{FromNodeID: "a", ToNodeID: "c", Kind: "data"}}},
	}
	for _, c := range cases {
		if _, ok := planWiringSwap(disc, c.order, c.edges); ok {
			t.Errorf("%s must NOT be attempted as a transposition", c.what)
		}
	}
}

// TestMaterializedReorderOnBothEntrypoints — 🔴 task 15.2. The same reorder produces a real diff
// through Generate (the P2 submit path) and through GenerateTransform (the P5 commit path). Before
// 15c both refused it.
func TestMaterializedReorderOnBothEntrypoints(t *testing.T) {
	root, first, second, disc := wiringTree(t)
	r := reorderedResolved(disc, first, second)

	viaGenerate, err := Generate(r, root)
	if err != nil {
		t.Fatalf("Generate must materialize an adjacent transposition, not refuse it: %v", err)
	}
	if len(viaGenerate.Files) != 1 || viaGenerate.Files["wiring.go"] == nil {
		t.Fatalf("the swap must rewrite exactly wiring.go, got %v", keysOf(viaGenerate.Files))
	}
	diff := string(viaGenerate.Diff)
	if !strings.Contains(diff, "--- a/wiring.go") || !strings.Contains(diff, "+++ b/wiring.go") {
		t.Fatalf("the reorder must produce a reviewable diff, got:\n%s", diff)
	}
	// The two call lines moved, and NOTHING else did: a permutation, asserted on the emitted bytes.
	before, _ := os.ReadFile(filepath.Join(root, "wiring.go"))
	if _, _, ok := lineMultisetDiff(splitLines(before), splitLines(viaGenerate.Files["wiring.go"])); !ok {
		t.Fatal("the materialized reorder must be a permutation of the file's lines")
	}
	if string(before) == string(viaGenerate.Files["wiring.go"]) {
		t.Fatal("the reorder produced no change at all — a no-op is the one outcome 15b forbade")
	}
	var attributed bool
	for _, td := range viaGenerate.Touched {
		if td.Dim == "wiring" && td.File == "wiring.go" {
			attributed = true
		}
	}
	if !attributed {
		t.Errorf("the swap must be attributed in Touched so a build failure can name it, got %+v", viaGenerate.Touched)
	}

	spec := &variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		Order: []string{second, first}, Nodes: map[string]variantspec.NodeOverride{},
	}
	viaCommit, err := GenerateTransform(r, spec, root)
	if err != nil {
		t.Fatalf("GenerateTransform must materialize the same reorder: %v", err)
	}
	if viaCommit.DiffHash != viaGenerate.DiffHash {
		t.Fatalf("both entrypoints must produce the same diff, got %s vs %s", viaCommit.DiffHash, viaGenerate.DiffHash)
	}
}

// TestUnsupportedLanguageRefusesByName — 🔴 task 15.3, retargeted by wave 15e.
//
// Kotlin has a resolver now (wiringswap_brace.go), so the language it had to be asked about moved to one
// discovery does not register. The GUARANTEE is unchanged and is what this still pins: a language with
// no statement resolver never gets a textual move attempted on it, and the refusal NAMES it.
func TestUnsupportedLanguageRefusesByName(t *testing.T) {
	_, err := materializeSwap("elixir", []byte("defmodule M do\nend\n"), &swapPlan{First: "a", Second: "b"}, 1, 1)
	if err == nil {
		t.Fatal("a language with no statement materializer must refuse, never attempt a textual move")
	}
	if !strings.Contains(err.Error(), "elixir") {
		t.Errorf("the refusal must NAME the language, got %v", err)
	}
	var re *RewriteError
	if errors.As(err, &re) && re.Cause != CauseNoMaterializer {
		t.Errorf("a missing resolver is the class that names work WE owe, got %q", re.Cause)
	}
}

// TestMaterializedSwapIsDeterministic — task 15.4 / FR21.
func TestMaterializedSwapIsDeterministic(t *testing.T) {
	root, first, second, disc := wiringTree(t)
	r := reorderedResolved(disc, first, second)
	a, err := Generate(r, root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(r, root)
	if err != nil {
		t.Fatal(err)
	}
	if a.DiffHash != b.DiffHash || string(a.Diff) != string(b.Diff) {
		t.Fatal("the same reorder against the same tree must produce a byte-identical diff")
	}
}

// ── §16 — the QA acceptance gate ────────────────────────────────────────────────────────────────

// TestSwapRefusalMatrix — 🔴 task 16.1. One table, every condition that must produce a refusal, each
// checked for the RIGHT refusal rather than merely for failure. A matrix is the honest shape here: the
// value of this rewriter is entirely in what it declines, so the declines are the specification.
func TestSwapRefusalMatrix(t *testing.T) {
	cases := []struct {
		what     string
		language string
		src      string
		lineA    int
		lineB    int
		want     string
	}{
		{
			what: "a data dependency", language: "go",
			src:   "package p\n\nfunc f(c C) {\n\ta := one(c)\n\tb := two(a)\n\t_ = b\n}\n",
			lineA: 4, lineB: 5, want: `reads "a"`,
		},
		{
			what: "both statements binding one name", language: "go",
			src:   "package p\n\nfunc f(c C) {\n\tout := one(c)\n\tout = two(c)\n\t_ = out\n}\n",
			lineA: 4, lineB: 5, want: "both statements bind",
		},
		{
			what: "a control-flow statement", language: "go",
			src:   "package p\n\nfunc f(c C) int {\n\ta := one(c)\n\treturn two(c)\n}\n",
			lineA: 4, lineB: 5, want: "return statement",
		},
		{
			what: "a comment between the two", language: "go",
			src:   "package p\n\nfunc f(c C) {\n\ta := one(c)\n\t// keep this order\n\tb := two(c)\n\t_, _ = a, b\n}\n",
			lineA: 4, lineB: 6, want: "not consecutive",
		},
		{
			what: "another statement between the two", language: "go",
			src:   "package p\n\nfunc f(c C) {\n\ta := one(c)\n\tmid(c)\n\tb := two(c)\n\t_, _ = a, b\n}\n",
			lineA: 4, lineB: 6, want: "not consecutive",
		},
		{
			what: "different nesting", language: "go",
			src:   "package p\n\nfunc f(c C) {\n\ta := one(c)\n\tif ok {\n\t\tb := two(c)\n\t\t_ = b\n\t}\n\t_ = a\n}\n",
			lineA: 4, lineB: 6, want: "different nesting",
		},
		{
			what: "an unanalysable Python target", language: "python",
			src:   "def f(c):\n    a, meta = one(c)\n    b = two(c)\n    return (a, b)\n",
			lineA: 2, lineB: 3, want: "does not model",
		},
		{
			// 🔴 Rust HAS a resolver now (wave 15e). What it does not have here is a transposable pair —
			// which is a fact about the SOURCE, and the refusal must say so rather than blaming Rust.
			what: "a Rust file with no transposable pair", language: "rust",
			src:   "fn main() {}\n",
			lineA: 1, lineB: 1, want: "same statement",
		},
		{
			what: "a language with no resolver at all", language: "elixir",
			src:   "defmodule M do\nend\n",
			lineA: 1, lineB: 1, want: "elixir",
		},
	}
	for _, c := range cases {
		edits, err := materializeSwap(c.language, []byte(c.src), &swapPlan{First: "A", Second: "B"}, c.lineA, c.lineB)
		if err == nil {
			t.Errorf("%s: must be refused, got %d edit(s)", c.what, len(edits))
			continue
		}
		if !errors.Is(err, ErrUnsafeRewrite) {
			t.Errorf("%s: must be an unsafeRewrite-class refusal, got %T", c.what, err)
		}
		var re *RewriteError
		if errors.As(err, &re) && re.Dim != "wiring" {
			t.Errorf("%s: the refusal must name the wiring axis, got %q", c.what, re.Dim)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: refusal must say %q, got %v", c.what, c.want, err)
		}
		if edits != nil {
			t.Errorf("%s: a refused pair must produce NO edits", c.what)
		}
	}
}

// TestSwappedFileParsesAndIsAttributed — task 16.3.
func TestSwappedFileParsesAndIsAttributed(t *testing.T) {
	root, first, second, disc := wiringTree(t)
	patch, err := Generate(reorderedResolved(disc, first, second), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := reparseGo("wiring.go", nil, patch.Files["wiring.go"]); err != nil {
		t.Fatalf("the swapped file must still parse: %v", err)
	}
	if _, err := format.Source(patch.Files["wiring.go"]); err != nil {
		t.Fatalf("the swapped file must remain formattable: %v", err)
	}
	for _, td := range patch.Touched {
		if td.Dim == "wiring" {
			if td.NodeID == "" || td.File == "" || td.Line == 0 {
				t.Errorf("the swap's attribution must be complete, got %+v", td)
			}
			return
		}
	}
	t.Fatal("the swap must appear in Touched")
}

// TestMergeAndPruneStillRefused — 🔴 task 16.4. 15c widened the applicable set by EXACTLY one shape.
// This is the test that keeps that true: a merge and a prune must still refuse, at the same place,
// with the same axis named, after the rewriter landed.
func TestMergeAndPruneStillRefused(t *testing.T) {
	root, first, second, disc := wiringTree(t)

	// A prune: the graph loses a node. Not a transposition, so not materializable.
	pruned := &variantspec.Resolved{
		ConfigHash: strings.Repeat("d", 64), SourceRevision: "rev1", Language: "go",
		Config: variantspec.ResolvedConfig{IRVersion: "1.0.0",
			Nodes: []variantspec.ResolvedNode{{NodeID: first}}},
		DiscoveredWiring: disc,
	}
	if _, err := Generate(pruned, root); err == nil {
		t.Fatal("a prune must still be refused after 15c")
	} else if !strings.Contains(err.Error(), "wiring") {
		t.Errorf("the refusal must still name the wiring axis, got %v", err)
	}

	// A declared EDGE, by contrast, is NOT a rewire and must pass: an IR that records no edge means
	// "not recorded", and the coherence gate is exactly what a spec declares edges for.
	declared := reorderedResolved(disc, first, second)
	declared.Config.Nodes = []variantspec.ResolvedNode{{NodeID: first}, {NodeID: second}}
	declared.Config.Edges = []variantspec.ResolvedEdge{{FromNodeID: first, ToNodeID: second, Kind: "data"}}
	if _, err := Generate(declared, root); err != nil {
		t.Fatalf("a declared edge is not a request to rewrite source: %v", err)
	}
}

// TestPythonScannerSurvivesDocstrings is a regression test for a defect the hermes-agent survey found.
//
// A docstring containing an unmatched bracket — ordinary prose, "the client (see below" — shifted a
// naive bracket counter for the whole rest of the file, and every statement after it was reported as
// "does not close its brackets before the end of the file". The pairs were REFUSED, so nothing unsafe
// happened; but the reason was false, and a false reason sends a user hunting a defect in their own
// code. The fix carries string state across lines; this test is what keeps it.
func TestPythonScannerSurvivesDocstrings(t *testing.T) {
	src := `def helper():
    """Return a client (see the note below.

    The bracket above is deliberately unmatched: it is prose, not code.
    """
    return 1


def run(ctx):
    a = summarize(ctx)
    b = classify(ctx)
    return (a, b)
`
	out, err := materializePy(t, src, 10, 11)
	if err != nil {
		t.Fatalf("an unmatched bracket inside a docstring must not make later statements unanalysable: %v", err)
	}
	if !strings.Contains(string(out), "    b = classify(ctx)\n    a = summarize(ctx)\n") {
		t.Fatalf("the two statements after the docstring must swap normally, got:\n%s", out)
	}
	// And the docstring itself is untouched — the permutation gate would have caught it, but the point
	// of the fix is that the scanner never confuses prose for code in the first place.
	if !strings.Contains(string(out), "Return a client (see the note below.") {
		t.Error("the docstring must be carried through unchanged")
	}
}

// keysOf lists a file map's paths, for a failure message that says which files a patch touched.
func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
