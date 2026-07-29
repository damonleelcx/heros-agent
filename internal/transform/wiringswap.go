package transform

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// wiringswap.go is P15 wave 15c: the ONE wiring change this engine materializes as source.
//
// # What it does, and why it is this small
//
// It exchanges two ADJACENT statements. Nothing else. Not a move to an arbitrary position, not a
// fusion of two calls, not a deletion — those keep the interim refusal (decisions.md D-3).
//
// The reason is a single property that only a transposition has: **the output is the input's lines,
// reordered**. Same count, same multiset. That is checkable in one comparison (`gateSwapPermutation`),
// it cannot be subtly wrong, and it collapses the review question from "did this codemod rewrite the
// file correctly" — unanswerable without reading the codemod — to "should these two statements run in
// the other order", which is the question the typed-contract gate and verification already answer.
//
// A general mover has no such invariant. It must reconstruct indentation, re-anchor comments, decide
// what travels with the statement, and prove the destination scope binds the same names; each of those
// is a place to be wrong in a way that still compiles, which is ADR-001's named top risk with no
// downstream net.
//
// # Fail closed, five times over
//
// A pair is materialized only when EVERY one of these holds; any failure is a refusal that names the
// condition, never a best-effort move:
//
//	same file · same indentation · consecutive (blank lines only between) · whole lines · not control flow
//	INDEPENDENT — neither statement binds a name the other reads or binds
//	the language has a statement materializer (Go, Python)
//
// The independence analysis is deliberately conservative. Where a frontend cannot say what a statement
// binds or reads, the pair is refused — "we could not tell" is never resolved as "they are independent".

// stmtBlock is one statement projected into the only shape the swap needs: a whole-line byte range,
// the indentation it sits at, and the names it binds and reads.
//
// Whole-line is load-bearing. A statement that shares a line with other code cannot be exchanged by
// moving lines, and a sub-line move would break the permutation invariant that makes this safe.
type stmtBlock struct {
	nodeID    string
	startLine int // 1-based, inclusive
	endLine   int // 1-based, inclusive
	startByte int // offset of the first byte of startLine
	endByte   int // offset just past the newline ending endLine
	indent    string
	binds     map[string]bool
	reads     map[string]bool
	// control marks a statement whose position is part of its meaning (return, raise, break, defer …).
	// Two of those are never exchangeable, however independent their data.
	control bool
	// kind is a short human label for the refusal message ("return statement", "compound statement").
	kind string
}

// statementResolver resolves the statement enclosing a call site's line, for one language. It returns
// a refusal (not a bool) when the statement cannot be analysed — the caller must not have to guess
// whether an empty result means "no statement" or "a statement we could not read".
type statementResolver func(src []byte, nodeID string, line int) (stmtBlock, error)

// statementResolvers is the per-language table (Decision 9). A language absent from it refuses BY NAME:
// a textual move for a language whose structure nobody parsed is a guess that compiles.
// 🔴 Wave 15e: the brace languages are ROWS, added by braceResolver over a syntax table
// (wiringswap_brace.go) rather than five hand-written resolvers. Decision 9's "Go and Python first" was
// an ordering, and D-5 fixes the shape that carries the rest: the resolver is the only per-language
// part, and the invariant, the plan, the edge check and the coherence gate stay one neutral path.
var statementResolvers = func() map[string]statementResolver {
	out := map[string]statementResolver{
		"go":     resolveGoStatement,
		"python": resolvePythonStatement,
	}
	for lang := range braceSyntaxes {
		out[lang] = braceResolver(lang)
	}
	return out
}()

// swapPlan is a materializable wiring change: exactly two node ids that exchange places.
type swapPlan struct {
	// First and Second are in DISCOVERED order — First currently runs before Second.
	First, Second string
}

// planWiringSwap decides whether the difference between the discovered wiring and the desired one is
// the ONE shape this engine can materialize: a single transposition of two adjacent nodes, with the
// edge set unchanged (task 15.1, FR15).
//
// Everything else — a merge (fewer nodes), a prune, an added or dropped edge, a non-adjacent move, two
// transpositions at once — returns false and keeps §4's refusal. The check is deliberately literal:
// it compares the two orders position by position and requires exactly one adjacent pair to differ, in
// the exchanged direction. A looser "is this a permutation" test would admit a three-cycle, which is
// two moves wearing one name.
func planWiringSwap(discovered variantspec.Wiring, order []string, edges []variantspec.ResolvedEdge) (*swapPlan, bool) {
	if len(discovered.Order) != len(order) {
		return nil, false // a node left or joined the graph: a merge or a prune, not a transposition
	}
	if !sameEdgeSet(discovered.Edges, edges) {
		return nil, false // the wiring changed beyond the order
	}
	var diffs []int
	for i := range order {
		if discovered.Order[i] != order[i] {
			diffs = append(diffs, i)
		}
	}
	if len(diffs) != 2 || diffs[1] != diffs[0]+1 {
		return nil, false // no difference, or a difference that is not one adjacent pair
	}
	i, j := diffs[0], diffs[1]
	if discovered.Order[i] != order[j] || discovered.Order[j] != order[i] {
		return nil, false // the two positions differ but are not each other's values
	}
	return &swapPlan{First: discovered.Order[i], Second: discovered.Order[j]}, true
}

func sameEdgeSet(a, b []variantspec.ResolvedEdge) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(e variantspec.ResolvedEdge) string { return e.FromNodeID + " -" + e.Kind + "-> " + e.ToNodeID }
	as := make([]string, 0, len(a))
	bs := make([]string, 0, len(b))
	for _, e := range a {
		as = append(as, key(e))
	}
	for _, e := range b {
		bs = append(bs, key(e))
	}
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// materializeSwap turns a plan into the two block edits, or refuses with the condition that failed.
//
// src is the file both call sites live in; the caller has already established that they are in one
// file, because two statements in different files are not adjacent in any sense a swap could realise.
func materializeSwap(language string, src []byte, plan *swapPlan, firstLine, secondLine int) ([]edit, error) {
	resolve, ok := statementResolvers[strings.ToLower(strings.TrimSpace(language))]
	if !ok {
		// 🔴 The ONE class here that names work the platform owes (P13 FR43). Every other wiring refusal
		// is about the requested shape or the source, and must not borrow this wording.
		return nil, refuseNoMaterializer(plan.First, wiringRefusalDim,
			"no statement resolver has landed for %s, so this engine did not parse its statements; moving a "+
				"call in a language whose boundaries it would have to guess is a textual guess, and a guess "+
				"that compiles is the failure mode with no downstream net. The languages it can transpose "+
				"are %s (decisions.md D-5)",
			languageDisplay(language), strings.Join(StatementMaterializerLanguages(), ", "))
	}

	a, err := resolve(src, plan.First, firstLine)
	if err != nil {
		return nil, err
	}
	b, err := resolve(src, plan.Second, secondLine)
	if err != nil {
		return nil, err
	}
	if a.startLine > b.startLine { // the plan names discovered order; the file decides which is first
		a, b = b, a
	}
	// 🔴 Two nodes that resolve to the SAME statement are not a transposable pair — a fact about the
	// source, in every language. Exchanging a statement with itself emits two edits that cancel, which
	// would ship an empty change under a variant's hash.
	if a.startLine == b.startLine && a.endLine == b.endLine {
		return nil, refuseWiringMaterialize(a.nodeID, fmt.Sprintf(
			"nodes %s and %s resolve to the same statement at line %d, so this workflow offers no adjacent "+
				"pair to transpose here; that is a property of the source rather than of %s support",
			a.nodeID, b.nodeID, a.startLine, languageDisplay(language)))
	}
	if err := admitSwap(src, a, b); err != nil {
		return nil, err
	}

	// Two half-edits: each block's range receives the other's bytes. Both are marked Swap, which routes
	// the file through the permutation gate instead of the value-rewrite rules.
	return []edit{
		{Start: a.startByte, End: a.endByte, New: string(src[b.startByte:b.endByte]),
			NodeID: a.nodeID, Dim: wiringRefusalDim, Swap: true},
		{Start: b.startByte, End: b.endByte, New: string(src[a.startByte:a.endByte]),
			NodeID: b.nodeID, Dim: wiringRefusalDim, Swap: true},
	}, nil
}

// admitSwap is the fail-closed checklist (FR16, FR17). Order matters only for the message: the cheapest
// and most legible condition is checked first, so a user reads "these are not consecutive" rather than
// a name-analysis result about statements that were never adjacent.
func admitSwap(src []byte, a, b stmtBlock) error {
	if a.control || b.control {
		who, kind := a.nodeID, a.kind
		if b.control {
			who, kind = b.nodeID, b.kind
		}
		return refuseWiringMaterialize(who, fmt.Sprintf(
			"node %s is a %s, whose POSITION is part of its meaning — exchanging it with a neighbour "+
				"changes what the function does, not merely when a call happens", who, kind))
	}
	if a.indent != b.indent {
		return refuseWiringMaterialize(a.nodeID, fmt.Sprintf(
			"the two statements sit at different nesting (%d vs %d columns of indentation), so they are "+
				"not siblings in one block; exchanging them would move a statement between scopes",
			len(a.indent), len(b.indent)))
	}
	// Consecutive: everything between them must be blank. A COMMENT is not blank, and that is
	// deliberate — a comment above the second statement documents it, and a mover that left the comment
	// behind would silently re-attach it to the other call.
	lines := splitLines(src)
	for l := a.endLine + 1; l <= b.startLine-1; l++ {
		if l-1 < 0 || l-1 >= len(lines) {
			continue
		}
		if strings.TrimSpace(lines[l-1]) != "" {
			return refuseWiringMaterialize(a.nodeID, fmt.Sprintf(
				"the two statements are not consecutive: line %d between them is %q. A transposition "+
					"exchanges NEIGHBOURS; anything else is a move, which this engine does not do",
				l, strings.TrimSpace(lines[l-1])))
		}
	}
	if shared := firstShared(a.binds, b.reads); shared != "" {
		return refuseWiringMaterialize(a.nodeID, fmt.Sprintf(
			"the second statement reads %q, which the first one binds: they are data-dependent, so "+
				"exchanging them changes the program", shared))
	}
	if shared := firstShared(b.binds, a.reads); shared != "" {
		return refuseWiringMaterialize(b.nodeID, fmt.Sprintf(
			"the first statement reads %q, which the second one binds: they are data-dependent in the "+
				"other direction, so exchanging them changes the program", shared))
	}
	if shared := firstShared(a.binds, b.binds); shared != "" {
		return refuseWiringMaterialize(a.nodeID, fmt.Sprintf(
			"both statements bind %q, so their order decides which assignment survives — exchanging "+
				"them changes the value that reaches the rest of the function", shared))
	}
	return nil
}

// firstShared returns the lexicographically first name present in both sets, or "". Sorted so the
// message is the same on every run: a refusal whose wording depends on map iteration cannot be tested
// and cannot be compared between two runs.
func firstShared(a, b map[string]bool) string {
	var hits []string
	for name := range a {
		if b[name] {
			hits = append(hits, name)
		}
	}
	if len(hits) == 0 {
		return ""
	}
	sort.Strings(hits)
	return hits[0]
}

// refuseWiringMaterialize is the 15c refusal: the wiring change is the shape this engine COULD
// materialize, but this particular pair failed one of the admissibility conditions.
//
// It is deliberately distinct in wording from refuseWiring (the 15b "no rewriter exists at all"
// refusal), because the two send the reader somewhere different: this one says "this pair, this
// condition", and the fix may be as small as moving a comment.
func refuseWiringMaterialize(nodeID, reason string) error {
	return unsafeRewrite(nodeID, wiringRefusalDim,
		"this reorder is the one wiring shape this engine can materialize — an exchange of two adjacent "+
			"statements — but %s. It is REFUSED rather than approximated: a move this engine cannot show to "+
			"be behaviour-preserving is exactly the diff nobody could review", reason)
}

// wholeLineSpan expands a byte range to the whole lines it covers, and reports whether anything other
// than whitespace (or a trailing comment) shares those lines. A statement sharing a line with other
// code cannot be moved by moving lines.
func wholeLineSpan(src []byte, start, end int, commentPrefix string) (startByte, endByte, startLine, endLine int, indent string, ok bool) {
	if start < 0 || end > len(src) || start > end {
		return 0, 0, 0, 0, "", false
	}
	startByte = start
	for startByte > 0 && src[startByte-1] != '\n' {
		startByte--
	}
	endByte = end
	for endByte < len(src) && src[endByte-1] != '\n' {
		endByte++
	}
	prefix := string(src[startByte:start])
	if strings.TrimSpace(prefix) != "" {
		return 0, 0, 0, 0, "", false // something else opens this line
	}
	suffix := strings.TrimSpace(string(src[end:endByte]))
	if suffix != "" && !strings.HasPrefix(suffix, commentPrefix) {
		return 0, 0, 0, 0, "", false // something else closes this line (a trailing comment travels along)
	}
	return startByte, endByte, lineOf(src, startByte), lineOf(src, maxInt(startByte, endByte-1)), prefix, true
}

// planSwapEdits turns an admitted transposition into edits on the file both statements live in, and
// records what was touched (P15 15c task 15.2).
//
// It refuses three situations that are not "the swap failed" but "the swap cannot be checked":
//
//	the two call sites are in DIFFERENT FILES — statements in different files are not adjacent in any
//	  sense a transposition could realise, whatever the Order says;
//	one of the nodes has no call site in this tree — the same fail-closed rule the override loop uses:
//	  a partial patch would silently drop a change the config_hash claims;
//	the file ALREADY has value edits — the two edit classes are gated by different rules, and a file
//	  carrying both would have each edit checked by the other's gate. Refusing keeps every edit checked
//	  by the rule written for it.
func planSwapEdits(plan *swapPlan, sites map[string]boundSite, root, language string, editsByFile map[string][]edit) (TouchedDimension, error) {
	first, ok := sites[plan.First]
	if !ok {
		return TouchedDimension{}, &RewriteError{NodeID: plan.First, Dim: wiringRefusalDim, Err: ErrNodeNotFound,
			Detail: fmt.Sprintf("the reorder names %q, but no %s call site with that node_id was found under %s",
				plan.First, languageDisplay(language), root)}
	}
	second, ok := sites[plan.Second]
	if !ok {
		return TouchedDimension{}, &RewriteError{NodeID: plan.Second, Dim: wiringRefusalDim, Err: ErrNodeNotFound,
			Detail: fmt.Sprintf("the reorder names %q, but no %s call site with that node_id was found under %s",
				plan.Second, languageDisplay(language), root)}
	}
	if first.fileRel != second.fileRel {
		return TouchedDimension{}, refuseWiringMaterialize(plan.First, fmt.Sprintf(
			"the two calls are in different files (%s and %s), so they are not adjacent statements and no "+
				"transposition can exchange them", first.fileRel, second.fileRel))
	}
	if len(editsByFile[first.fileRel]) > 0 {
		return TouchedDimension{}, refuseWiringMaterialize(plan.First, fmt.Sprintf(
			"%s also carries a content rewrite in this change, and a wiring transposition is gated by a "+
				"different rule (a permutation of the file's lines) than a value replacement; the two cannot "+
				"be checked together, so this pass refuses rather than let either edit past the wrong gate",
			first.fileRel))
	}

	src, err := readFile(root, first.fileRel)
	if err != nil {
		return TouchedDimension{}, err
	}
	edits, err := materializeSwap(language, src, plan, first.lineStart, second.lineStart)
	if err != nil {
		return TouchedDimension{}, err
	}
	editsByFile[first.fileRel] = edits

	line := first.lineStart
	if second.lineStart < line {
		line = second.lineStart
	}
	return TouchedDimension{NodeID: plan.First, Dim: wiringRefusalDim, File: first.fileRel, Line: line}, nil
}

// HasStatementMaterializer reports whether this language has a statement resolver — i.e. whether an
// adjacent transposition can be emitted for it (P15 15d task 19.6).
//
// Exported so the AUTHORING surface can state the language boundary before a user drags anything, while
// reading the same table `planWiringSwap` dispatches on. A second list in the console is how an editor
// starts offering a swap the codemod then refuses.
func HasStatementMaterializer(language string) bool {
	_, ok := statementResolvers[strings.ToLower(strings.TrimSpace(language))]
	return ok
}

// StatementMaterializerLanguages lists every language with a statement resolver, sorted, so a refusal can
// say what WOULD have worked.
func StatementMaterializerLanguages() []string {
	out := make([]string, 0, len(statementResolvers))
	for l := range statementResolvers {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}
