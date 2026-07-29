package transform

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Patch is a generated transformation: the new bytes per file, the unified diff a human reviews, and
// the diff's content hash.
type Patch struct {
	// ConfigHash and SourceRevision are the pair this patch is keyed by. Task 2.3's contract is
	// "same {config_hash, source_revision} -> byte-identical diff", so both belong on the artifact.
	ConfigHash     string
	SourceRevision string
	// Files maps repo-relative path -> the transformed bytes.
	Files map[string][]byte
	// Diff is the unified diff. This is the review artifact ADR-001 makes the product's output.
	Diff []byte
	// DiffHash is SHA-256 of Diff, so the review artifact is itself content-addressed and a
	// regeneration can be proved identical without re-reading it (task 2.3).
	DiffHash string
	// Touched records which node/dimension pairs actually produced edits, for the run record and the UI.
	Touched []TouchedDimension
}

// TouchedDimension is one dimension of one node that the patch rewrote.
type TouchedDimension struct {
	NodeID string
	Dim    string
	File   string
	// Line is the 1-based line the rewrite landed on. Carried so the build gate (task 3.5) can map a
	// compiler diagnostic's file:line back to the rewrite that caused it and name the node and
	// dimension, instead of blaming whichever override happens to be the only candidate.
	//
	// It is the same line in the original and the transformed file, because no rewriter may emit a
	// newline — gateMinimal enforces that, so a rewrite cannot shift the lines below it.
	Line int
}

// IsEmpty reports whether the resolved spec asked for no edits at all — a spec with no overrides.
// Not an error: it is the legitimate "run the code as discovered" baseline P4 compares against.
func (p *Patch) IsEmpty() bool { return len(p.Diff) == 0 }

// Generate produces the codemod for a resolved Variant Spec against a checked-out tree (tasks 2.2,
// 2.3, 2.4).
//
// It is a PURE FUNCTION of (bytes at root, resolved config). It reads the tree and returns bytes; it
// writes nothing, so it cannot touch the user's working copy — application to an isolated worktree is
// task 3.4's job, deliberately separate.
//
// root must be a tree checked out at r.SourceRevision. Generate cannot verify that (it is handed a
// directory, not a git repo), which is exactly why 3.4 owns the checkout: the caller that knows the
// revision is the caller that must guarantee the tree matches it.
// It dispatches on the workflow's LANGUAGE (ADR-003 decision 1). See engines.
func Generate(r *variantspec.Resolved, root string) (*Patch, error) {
	return generate(r, nil, root)
}

// generate is the one implementation both entrypoints share. `spec` is optional: the P2 submit path
// (Generate) has only the resolved projection, while the P5 commit path (GenerateTransform) also has
// the authored spec — and the spec is where a pure re-arrangement lives, since it changes no override
// and therefore leaves no trace in `Overrides`. Passing it through rather than duplicating the wiring
// decision in two callers is what keeps ONE gate: a second copy is a second thing to keep true.
func generate(r *variantspec.Resolved, spec *variantspec.VariantSpec, root string) (*Patch, error) {
	if r == nil {
		return nil, fmt.Errorf("transform: Generate requires a resolved spec")
	}
	// 🔴 P15 task 4.2 / 15.1 — before anything is indexed, read, or rewritten: the wiring is either the
	// source's own, or the ONE shape this engine can materialize (an adjacent transposition), or it is
	// refused. Deciding here rather than after the per-node loop is what makes it impossible to emit a
	// partial diff that rewrites the contents of a graph nobody rewired.
	eng, err := engineFor(r.Language)
	if err != nil {
		return nil, err
	}
	sites, err := eng.index(root)
	if err != nil {
		return nil, err
	}
	// 🔴 P15 task 4.2 / 15.1 — the wiring decision, before any file is read or rewritten: the spec
	// either wires what the source wires, or asks for the ONE shape this engine can materialize (an
	// adjacent transposition of two sibling statements), or is refused. It runs after indexing because
	// the only evidence of a source-stated ORDER is where the call sites actually are (see checkWiring);
	// it still runs before any edit, so a refused wiring cannot leave a partial diff behind.
	plan, err := checkWiring(r, spec, sites, root)
	if err != nil {
		return nil, err
	}

	// Per-file edits, and the call-site line ranges the minimality gate will allow them in.
	editsByFile := map[string][]edit{}
	allowedLines := map[string]map[int]bool{}
	var touched []TouchedDimension

	// Sorted node order: map iteration would make the edit set — and therefore the diff — depend on
	// hash seeding. Task 2.3 is byte-identical output, so nothing here may iterate a map.
	for _, nodeID := range sortedNodeIDs(r.Overrides) {
		override := r.Overrides[nodeID]
		site, ok := sites[nodeID]
		if !ok {
			// The spec overrides a node whose call site is not in this tree. Fail closed: generating
			// a partial patch would silently drop an override the config_hash claims is applied.
			return nil, &RewriteError{NodeID: nodeID, Err: ErrNodeNotFound,
				Detail: fmt.Sprintf("no %s call site with this node_id was found under %s",
					languageDisplay(r.Language), root)}
		}

		// P13 Decision 7 — a parameter tune (same model, changed params) has no call-site rewriter. In
		// BOUND mode it is materialized by GenerateBoundArtifacts (as data in the binding document), so
		// there is nothing to rewrite inline here — skip it. In INLINE mode it is REFUSED with a named
		// cause, never silently dropped (task 3.6).
		if override.ParamTune {
			if r.ApplyModes[nodeID].Mode() == variantspec.ApplyBound {
				continue
			}
			return nil, refuseInlineParamTune(nodeID)
		}

		for _, dim := range override.Dimensions() {
			src, err := readFile(root, site.fileRel)
			if err != nil {
				return nil, err
			}
			edits, err := site.rewrite(dim, src, override)
			if err != nil {
				return nil, err // already a *RewriteError naming node + dimension
			}
			if len(edits) == 0 {
				continue
			}
			editsByFile[site.fileRel] = append(editsByFile[site.fileRel], edits...)
			if allowedLines[site.fileRel] == nil {
				allowedLines[site.fileRel] = map[int]bool{}
			}
			for l := site.lineStart; l <= site.lineEnd; l++ {
				allowedLines[site.fileRel][l] = true
			}
			// 🔴 A BINDING-SITE edit lands outside the call's own lines by construction: the SDK bound the
			// value in an earlier statement (P13 FR52). It is admitted as its OWN line and nothing more —
			// the window is not widened to the enclosing function, and a non-binding edit on that line
			// still fails. New edit class, not a loosened gate (decisions.md D-4's discipline).
			for _, e := range edits {
				if e.bindingSite() {
					allowedLines[site.fileRel][lineOf(src, e.Start)] = true
				}
			}
			touched = append(touched, TouchedDimension{
				NodeID: nodeID, Dim: string(dim), File: site.fileRel, Line: site.lineStart})
		}
	}

	// P18 §9.1 — the memory import. It is added after the per-node loop, and per FILE rather than per
	// node, because that is what it is: two memory nodes in one file need ONE import, and emitting it
	// from the per-site materializer would produce a duplicate the gate then rejects — a refusal caused
	// by our own emission rather than by anything the user wrote.
	//
	// 🔴 It also SHIFTS attribution. Every recorded line below the insertion point moves down by one in
	// the transformed file, and the build gate reads transformed-file coordinates (it compiles the
	// transformed tree). Adjusting here keeps a compiler diagnostic attributable to the node that caused
	// it, which is one of the two things the line-count invariant existed to protect.
	for _, rel := range sortedKeys(editsByFile) {
		if !hasMemoryEdit(editsByFile[rel]) {
			continue
		}
		src, err := readFile(root, rel)
		if err != nil {
			return nil, err
		}
		blame := firstMemoryEdit(editsByFile[rel])
		imp, err := memoryImportEdit(src, blame.NodeID, blame.Dim, r.Language, root)
		if err != nil {
			return nil, err
		}
		editsByFile[rel] = append(editsByFile[rel], imp)
		impLine := lineOf(src, imp.Start)
		if allowedLines[rel] == nil {
			allowedLines[rel] = map[int]bool{}
		}
		allowedLines[rel][impLine] = true
		for i := range touched {
			if touched[i].File == rel && touched[i].Line >= impLine {
				touched[i].Line++
			}
		}
	}

	// P15 15c task 15.2 — the wiring transposition, emitted into the SAME patch as the content edits so
	// one revert takes both. It is added after the per-node loop because its admissibility depends on the
	// file's statements, not on any override, and because a wiring change that cannot be materialized
	// must refuse before a partial patch exists.
	if plan != nil {
		swapTouched, err := planSwapEdits(plan, sites, root, r.Language, editsByFile)
		if err != nil {
			return nil, err
		}
		touched = append(touched, swapTouched)
	}

	patch := &Patch{
		ConfigHash: r.ConfigHash, SourceRevision: r.SourceRevision,
		Files: map[string][]byte{}, Touched: touched,
	}

	var diffs []byte
	for _, rel := range sortedKeys(editsByFile) { // sorted: the diff's file order must be stable
		src, err := readFile(root, rel)
		if err != nil {
			return nil, err
		}
		out, err := applyEdits(src, editsByFile[rel])
		if err != nil {
			return nil, err
		}
		if err := gateMinimal(rel, src, out, editsByFile[rel], allowedLines[rel], eng.reparse); err != nil {
			return nil, err
		}
		patch.Files[rel] = out
		diffs = append(diffs, unifiedDiff(rel, src, out, editsByFile[rel])...)
	}

	// P18 §2 — the generated memory module and its params document, emitted into the SAME patch as the
	// call-site edits so one revert restores all of it. Emitted only when a call site was actually
	// rewritten for memory: a module in a customer's tree that nothing calls is dead code we put there.
	if memoryWasMaterialized(editsByFile) {
		memFiles, err := GenerateMemoryArtifacts(r, r.Language)
		if err != nil {
			return nil, err
		}
		for _, path := range sortedFileKeys(memFiles) {
			if _, exists := patch.Files[path]; exists {
				return nil, fmt.Errorf("transform: the memory artifact would overwrite %s, which this patch "+
					"already changes; a generated file must never clobber the customer's own source", path)
			}
			patch.Files[path] = memFiles[path]
			diffs = append(diffs, newFileDiff(path, memFiles[path])...)
			patch.Touched = append(patch.Touched, TouchedDimension{
				Dim: string(variantspec.DimMemory), File: path, Line: 1})
		}
	}

	patch.Diff = diffs
	sum := sha256.Sum256(diffs)
	patch.DiffHash = hex.EncodeToString(sum[:])
	return patch, nil
}

// gateMinimal enforces FR5c / task 2.4: the diff touches only the targeted call sites and only the
// configured dimensions — no incidental edits, no reformatting of untouched code.
//
// The byte-splice design already makes an incidental edit close to unconstructible, so this is not
// where minimality comes from — it is the check that the design still holds. It is worth having
// anyway: a future rewriter that reached for go/printer, or an ArgMap pointing at an expression
// spanning half the file, would both be caught here rather than shipped to a reviewer.
//
// Two independent assertions:
//  1. Every changed line is inside a targeted call site's line range.
//  2. The result still parses. A splice that produced invalid source is rejected now, not at the build
//     gate — the build gate (task 3.5) is a slower, coarser net, and this one can name the dimension.
//
// `reparse` is assertion 2, supplied by the language's engine. It is a parameter rather than a call to
// go/parser because "still parses" means something different in each language and nothing generic can
// answer it — but it is NOT optional, and a language whose engine omitted it would be caught by
// engineFor rather than quietly skipping the check (see langEngine).
func gateMinimal(rel string, src, out []byte, edits []edit, allowed map[int]bool, reparse reparser) error {
	// A WIRING TRANSPOSITION is a different class of edit and gets a different — stronger — check
	// (P15 15c, decisions.md D-4). It is not routed through the rules below because it would fail the
	// first of them by construction: a swap moves whole lines, and every rule below exists to forbid
	// exactly that for a VALUE rewrite. Relaxing them instead would have removed the check from every
	// rewriter in the package, forever, to serve one caller.
	if isSwap(edits) {
		if err := gateSwapPermutation(rel, src, out, edits); err != nil {
			return err
		}
		return reparseOrReject(rel, src, out, edits, reparse)
	}
	// A MEMORY MATERIALIZATION is the third edit class (P18 §9.1). It is routed here for the same reason
	// a swap is: it would fail the line-count rule below by construction, because it adds an import line.
	// Its own rule is stricter, not laxer — see gateMemoryImport.
	if isMemoryImport(edits) {
		if err := gateMemoryImport(rel, src, out, edits, allowed); err != nil {
			return err
		}
		return reparseOrReject(rel, src, out, edits, reparse)
	}
	for line := range changedLines(src, edits) {
		if !allowed[line] {
			e := edits[0]
			return &RewriteError{NodeID: e.NodeID, Dim: e.Dim, Err: ErrUnsafeRewrite,
				Detail: fmt.Sprintf("the rewrite would change %s:%d, which is outside the targeted "+
					"call site; a transform may not edit untargeted lines", rel, line)}
		}
	}
	// No rewrite may change the file's line count. Two things depend on it: TouchedDimension.Line
	// stays valid in both files (so the build gate can attribute a compiler error), and the diff stays
	// a same-line-count edit, which is what makes "only the targeted lines changed" checkable at all.
	//
	// It takes BOTH halves below, and only the first was here originally. That was sound while the
	// only rewriter replaced a model constant — an expression that never spans lines. A rewriter that
	// replaces a prompt expression can be handed a multi-line one, and collapsing it to a single line
	// deletes a line just as surely as emitting "\n" adds one. The gate is supposed to catch a
	// rewriter that breaks the design's assumptions; a gate that only checks the direction the
	// original rewriter could fail in is a gate that would have passed this one.
	//
	// A rewriter that needs to change the line count is not a value replacement and belongs behind
	// ErrUnsafeRewrite.
	for _, e := range edits {
		if strings.Contains(e.New, "\n") {
			return &RewriteError{NodeID: e.NodeID, Dim: e.Dim, Err: ErrUnsafeRewrite,
				Detail: fmt.Sprintf("the rewrite of %s would insert a newline, shifting every line below it", rel)}
		}
		if from, to := lineOf(src, e.Start), lineOf(src, e.End); from != to {
			return &RewriteError{NodeID: e.NodeID, Dim: e.Dim, Err: ErrUnsafeRewrite,
				Detail: fmt.Sprintf("the expression being rewritten spans %s:%d-%d; replacing a multi-line "+
					"expression with a single-line one would delete %d line(s) and shift every line below it",
					rel, from, to, to-from)}
		}
	}
	return reparseOrReject(rel, src, out, edits, reparse)
}

// reparseOrReject is the "the result still parses" half of the gate, shared by both edit classes. A
// splice that produced invalid source is rejected here rather than at the build gate, because this one
// can name the node and the dimension.
func reparseOrReject(rel string, src, out []byte, edits []edit, reparse reparser) error {
	if err := reparse(rel, src, out); err != nil {
		e := edits[0]
		return &RewriteError{NodeID: e.NodeID, Dim: e.Dim, Err: ErrUnsafeRewrite, Detail: err.Error()}
	}
	return nil
}

// gateMemoryImport is the memory materialization's gate (P18 §9.1).
//
// 🔴 It asserts the one property that makes an import insertion reviewable without reading the codemod:
// THE OUTPUT IS THE INPUT'S LINES WITH EXACTLY ONE IMPORT LINE INSERTED, and every other difference
// confined to a targeted call-site line. Five checks, each catching a different way of being wrong:
//
//	exactly one import edit    two would leave a duplicate import, or an import for a module the file
//	                           does not call
//	it is an INSERTION         Start == End: it replaces nothing, so it cannot delete code by claiming
//	                           to add some
//	its text is ONE import     a single `import <name>` line, matched against a pattern. This is what
//	                           stops the class being a general "insert arbitrary lines" escape hatch —
//	                           the thing a future contributor would most naturally reach for it as.
//	line count +1 exactly      not +2, not 0; the shift attribution accounts for is the shift that
//	                           happened
//	lines match, minus one     removing the inserted line from the output must reproduce the input
//	                           EXCEPT on allowed lines. This is the check that makes the class safe: if
//	                           it holds, nothing outside the targeted call site changed, and the one
//	                           thing that moved is a line whose content the previous check pinned.
//
// A rewriter cannot satisfy these by accident. A reviewer who trusts only this assertion still knows the
// change added one import and altered nothing else outside the call site it named.
func gateMemoryImport(rel string, src, out []byte, edits []edit, allowed map[int]bool) error {
	blame := edits[0]
	fail := func(format string, args ...any) error {
		return &RewriteError{NodeID: blame.NodeID, Dim: blame.Dim, Err: ErrUnsafeRewrite,
			Detail: fmt.Sprintf(format, args...)}
	}

	var imports []edit
	for _, e := range edits {
		if e.Import {
			imports = append(imports, e)
		}
	}
	if len(imports) != 1 {
		return fail("the memory materialization of %s carries %d import edits; exactly one is admitted, "+
			"or the file gains a duplicate import", rel, len(imports))
	}
	imp := imports[0]
	if imp.Start != imp.End {
		return fail("the memory import edit for %s replaces bytes [%d,%d) instead of inserting; this class "+
			"may add a line and may not remove one", rel, imp.Start, imp.End)
	}
	if !memoryImportLine.MatchString(imp.New) {
		return fail("the memory import edit for %s would insert %q, which is not a single import line. "+
			"This class exists to add ONE import; admitting arbitrary text would make it the "+
			"insert-anything escape hatch the untargeted-line rule exists to prevent", rel, imp.New)
	}

	before, after := splitLines(src), splitLines(out)
	if len(after) != len(before)+1 {
		return fail("the memory materialization changed %s from %d line(s) to %d; this class adds exactly "+
			"one import line, and attribution below the insertion is shifted by exactly that", rel,
			len(before), len(after))
	}

	// Where the line landed, in the OUTPUT's coordinates. The edit inserted at a line boundary, so the
	// inserted line is the one at the input line index the offset falls on.
	k := lineOf(src, imp.Start) - 1 // 0-based index in `after`
	if k < 0 || k >= len(after) {
		return fail("the memory import for %s landed outside the file", rel)
	}
	if !memoryImportLine.MatchString(after[k] + "\n") {
		return fail("the memory materialization of %s did not put the import where it claimed (line %d is "+
			"%q); the shift attribution relies on knowing which line moved", rel, k+1, after[k])
	}

	// Remove the inserted line and compare what is left against the input, line for line.
	reduced := make([]string, 0, len(before))
	reduced = append(reduced, after[:k]...)
	reduced = append(reduced, after[k+1:]...)
	for i := range before {
		if reduced[i] == before[i] {
			continue
		}
		if !allowed[i+1] {
			return fail("the memory materialization would change %s:%d, which is outside the targeted "+
				"call site; adding an import does not license editing anything else", rel, i+1)
		}
	}
	return nil
}

// memoryImportLine is the ONLY text this edit class may insert. Anchored at both ends and permitting a
// single module name, because a looser pattern is how a class meant for one import becomes a way to
// insert arbitrary source past the minimality gate.
var memoryImportLine = regexp.MustCompile(`^import (?:[a-zA-Z_][a-zA-Z0-9_]*|"[^"\n]+")\n$`)

// gateSwapPermutation is the wiring transposition's gate (P15 15c task 12.2, FR18).
//
// 🔴 It asserts the one property that makes a statement swap reviewable without reading the codemod:
// THE OUTPUT IS THE INPUT'S LINES, REORDERED. Three checks, and each catches a different way of being
// wrong:
//
//	line COUNT unchanged      a move that dropped or duplicated a line
//	line MULTISET unchanged   a move that also EDITED a line while carrying it — re-indentation, a
//	                          trailing-comma fix, a "harmless" whitespace normalisation. This is the
//	                          check that makes the class safe: if it holds, not one character of the
//	                          file's content changed, only the order of its lines.
//	changed lines ⊆ blocks    a move that disturbed code outside the two statements it claimed to swap
//
// A rewriter cannot satisfy these by accident, and a reviewer who trusts only this assertion still
// knows the change cannot have altered what any line SAYS.
func gateSwapPermutation(rel string, src, out []byte, edits []edit) error {
	blame := edits[0]
	fail := func(format string, args ...any) error {
		return &RewriteError{NodeID: blame.NodeID, Dim: blame.Dim, Err: ErrUnsafeRewrite,
			Detail: fmt.Sprintf(format, args...)}
	}

	// 🔴 THE INVARIANT, in ONE implementation for every language (P15 20.5, decisions.md D-5 part 2).
	//
	// Two statements of the same rule. The LINE multiset is the stricter special case, and it is checked
	// first because where it applies — Go and Python always, and every line-aligned brace statement — its
	// failure message names the line a reader can go and look at. The STATEMENT multiset is the general
	// form: the exchanged byte ranges must come back unedited, which stays true when statements stop
	// being line-aligned (a TypeScript chain, a Rust expression-statement sharing a line).
	//
	// 🚫 Choosing the general formulation BEFORE the first non-line-aligned resolver landed is deliberate:
	// a per-language relaxation is indistinguishable, in every passing run, from a per-language
	// correctness, and the weakened one would be the least reviewed. Neither check is per language.
	before, after := splitLines(src), splitLines(out)
	if len(before) != len(after) {
		return fail("the wiring swap changed %s from %d line(s) to %d: a transposition moves lines, it "+
			"never adds or removes one", rel, len(before), len(after))
	}
	if missing, extra, ok := lineMultisetDiff(before, after); !ok {
		return fail("the wiring swap did not preserve %s's lines: it would drop %q and introduce %q. A "+
			"transposition may REORDER lines and may not edit one while moving it", rel, missing, extra)
	}
	if missing, extra, ok := statementMultisetDiff(src, out, edits); !ok {
		return fail("the wiring swap did not preserve %s's statements: it would drop %q and introduce %q. "+
			"A transposition may REORDER what the author wrote and may not edit any of it while moving it",
			rel, missing, extra)
	}

	// Every changed line must lie inside one of the swapped blocks. The blocks are the edits' own byte
	// ranges, in ORIGINAL coordinates, which is the same space changedLines reports in.
	inBlock := map[int]bool{}
	for _, e := range edits {
		for l := lineOf(src, e.Start); l <= lineOf(src, maxInt(e.Start, e.End-1)); l++ {
			inBlock[l] = true
		}
	}
	for i := range before {
		if before[i] == after[i] {
			continue
		}
		if !inBlock[i+1] { // changedLines and lineOf are 1-based
			return fail("the wiring swap changed %s:%d, which is outside both swapped statements; a "+
				"transposition may only disturb the two blocks it exchanges", rel, i+1)
		}
	}
	return nil
}

// statementMultisetDiff reports whether the exchanged STATEMENTS survived the swap unedited: every
// block's original bytes must appear in the result exactly as many times as before.
//
// This is the general form of the invariant. It is checked first because it is the one that stays true
// when statements stop being line-aligned, and because its failure message names a STATEMENT — which is
// what the author moved — rather than a line.
func statementMultisetDiff(src, out []byte, edits []edit) (missing, extra string, ok bool) {
	for _, e := range edits {
		if e.Start < 0 || e.End > len(src) || e.Start > e.End {
			return "", "", false
		}
		block := string(src[e.Start:e.End])
		if strings.Count(string(out), block) < strings.Count(string(src), block) {
			return block, e.New, false
		}
	}
	return "", "", true
}

// lineMultisetDiff reports whether two line slices are permutations of each other, and if not, one
// concrete line from each side of the difference — a message that names the offending line is what
// turns a failed invariant into something a person can act on.
func lineMultisetDiff(before, after []string) (missing, extra string, ok bool) {
	count := make(map[string]int, len(before))
	for _, l := range before {
		count[l]++
	}
	for _, l := range after {
		count[l]--
	}
	// Sorted iteration: the reported line must not depend on map ordering, or the same defect produces
	// a different message on every run and nobody can tell two failures apart.
	keys := make([]string, 0, len(count))
	for k, v := range count {
		if v != 0 {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return "", "", true
	}
	sort.Strings(keys)
	for _, k := range keys {
		if count[k] > 0 && missing == "" {
			missing = k
		}
		if count[k] < 0 && extra == "" {
			extra = k
		}
	}
	return missing, extra, false
}

// reparser is gateMinimal's "the result still parses" assertion for one language.
//
// It is handed BOTH the original and the rewritten bytes, which the Go implementation ignores and the
// tree-sitter ones need. See reparseSyntactic: tree-sitter frontends analyze files that go/parser would
// have refused outright, so "out has syntax errors" does not by itself mean the splice broke it, and
// blaming a rewriter for damage it did not do sends a user to fix the wrong thing.
type reparser func(rel string, src, out []byte) error

// reparseGo is the Go engine's assertion, unchanged in behavior from when it was inline: the rewritten
// file must parse as Go.
//
// It ignores src because for Go the question cannot arise: discovery's loader parses with go/parser,
// which yields no AST at all for a file with a syntax error, so a file that produced a call site is a
// file that parsed. The original bytes are always clean by the time a rewriter has touched them.
func reparseGo(rel string, _, out []byte) error {
	if _, err := parser.ParseFile(token.NewFileSet(), rel, out, parser.ParseComments); err != nil {
		return fmt.Errorf("the rewritten %s no longer parses as Go: %v", rel, err)
	}
	return nil
}

// reparseSyntactic is the tree-sitter engines' assertion, and it checks the ORIGINAL first.
//
// # Why the original is checked at all
//
// Because tree-sitter is error-TOLERANT, and that changes what a failing parse means. go/parser refuses
// a broken file, so Go never reaches a rewriter with one. tree-sitter RECOVERS: a Python file with a
// syntax error a hundred lines away still yields call sites, still yields node_ids, and still reaches
// this gate — where a naive "out must parse cleanly" check would blame the rewrite for a defect that was
// already in the tree. The user would go looking for a codemod bug that does not exist.
//
// So the two cases are separated and named, because they have different owners and different fixes:
//
//	the original was already broken -> the TARGET's problem. We refuse to codemod a file the parser was
//	                                   guessing at — the same fail-closed rule argInsertAtEnd applies to
//	                                   insertion, applied to the whole file.
//	the original was clean, out is not -> OUR problem. The splice broke it, and this is the gate catching
//	                                   a rewriter that broke the design's assumptions before a reviewer
//	                                   ever sees the diff.
func reparseSyntactic(language, rel string, src, out []byte) error {
	if err := discovery.ParseCheck(language, rel, src); err != nil {
		return fmt.Errorf("the original %s already has syntax errors, so this codemod refuses to edit it: "+
			"%v; a rewrite spliced into source the parser RECOVERED rather than parsed cannot be shown to "+
			"preserve behavior", rel, err)
	}
	if err := discovery.ParseCheck(language, rel, out); err != nil {
		return fmt.Errorf("the rewritten %s no longer parses as %s: %v", rel, language, err)
	}
	return nil
}

func readFile(root, rel string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return nil, fmt.Errorf("transform: read %s: %w", rel, err)
	}
	return b, nil
}

func sortedNodeIDs(m map[string]variantspec.ResolvedOverride) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string][]edit) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
