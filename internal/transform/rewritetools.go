package transform

import (
	"go/ast"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Tool PRUNING — the one rewriter that DELETES
// ───────────────────────────────────────────
//
// A tool prune is the easy half of P14, and it is easy for a structural reason worth naming: a tool the
// node offers is ALREADY WRITTEN AT THE CALL SITE. Removing it is a deletion of an element the author
// wrote, so it needs no per-SDK knowledge, constructs nothing, and cannot invent a shape. It is squarely
// inside rewrite.go's rule ("replace an expression the call site already wrote") rather than the
// exception skillbind.go had to argue for — deleting is the degenerate case of replacing, with an empty
// replacement.
//
// That asymmetry is exactly why D-14.1 splits tools from skills in the IR. If the two shared a slice,
// "prune this unused tool" (a deletion) and "unbind this platform skill" (which un-does a construction)
// would be one sentence, and the engine would have to guess which one it was being asked for.
//
// # What is refused, and why the refusal is not a gap
//
// 🔴 A tool set assembled at RUNTIME (`Tools: buildTools(ctx)`) has no declaration to delete. The
// discovery frontend already records that fact — an IRTool with no `declared_at` (D-14.1) — and the
// only honest outcome is ErrUnsafeRewrite. Guessing a deletion against a list nobody wrote is how a
// codemod removes a tool the workflow needed, in a diff that compiles.
//
// # Why a deletion never removes a LINE
//
// gateMinimal forbids any rewrite that changes the file's line count, and that rule is load-bearing:
// TouchedDimension.Line has to stay valid in both files so the build gate can attribute a compiler
// error, and "only the targeted lines changed" is only checkable while the lines line up. So a deletion
// takes the element's bytes and its separator and stops at the newline; an element written on its own
// line leaves that line blank rather than closing it up. A blank line inside a composite literal is
// valid Go and gofmt closes it in the target repo's own CI, which is the right place for a formatting
// decision to be made.

// rewriteTools deletes the tools the selection does not keep.
func rewriteTools(site discovery.GoCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimTools)

	loc := site.ArgMap.Tools
	if loc == nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"the registry row for this call site declares no tools locator, so there is no tool list to "+
				"prune; the SDK this row covers does not take tools at the call site")
	}
	expr, ok := discovery.LocateArg(site.Call, loc, site.File.Fset)
	if !ok || expr == nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"this call site writes no tools argument, so the selection %v has nothing to prune; the tools "+
				"reach the SDK some other way and a deletion here would delete nothing while claiming to",
			o.ToolSelection)
	}

	cl, isStatic := unwrapValueExpr(expr).(*ast.CompositeLit)
	if !isStatic {
		// 🔴 FR14 / D-14.3. Refused by name, never guessed.
		return nil, unsafeRewrite(site.NodeID, dim,
			"this call site's tool set is assembled at runtime (%s), not written as a static list, so there "+
				"is no declaration to delete; pruning it would mean guessing which of the tools it builds to "+
				"drop, and a guess that compiles is the failure mode with no downstream net",
			describeExpr(expr))
	}

	kept := map[string]bool{}
	for _, name := range o.ToolSelection {
		kept[name] = true
	}

	fset := site.File.Fset
	var edits []edit
	present := make([]string, 0, len(cl.Elts))
	for _, el := range cl.Elts {
		// renderExpr prints the element back to source text — the SAME string discovery recorded in
		// IRTool.Name, so "the tool the selection names" and "the element at the call site" are one
		// spelling rather than two that have to be kept in step.
		name := renderExpr(fset, el)
		present = append(present, name)
		if kept[name] {
			continue
		}
		start, end, err := byteRange(fset, el, len(src))
		if err != nil {
			return nil, unsafeRewrite(site.NodeID, dim, "%v", err)
		}
		start, end = absorbSeparator(src, start, end)
		if lineOf(src, start) != lineOf(src, end) {
			return nil, unsafeRewrite(site.NodeID, dim,
				"tool %s spans %s:%d-%d; deleting a multi-line element would remove line(s) and shift every "+
					"line below it, which this engine does not do", name, site.FileRel,
				lineOf(src, start), lineOf(src, end))
		}
		edits = append(edits, edit{Start: start, End: end, New: "", NodeID: site.NodeID, Dim: dim})
	}

	// 🔴 Fail closed on a selection that does not match what the call site writes. Resolve already
	// validated the selection against the IR's recorded tool set, so reaching here means the TREE and
	// the IR disagree — a stale source_revision, or a discovery/codemod divergence. Emitting the diff
	// anyway would prune against a call site nobody validated.
	for _, name := range o.ToolSelection {
		if !containsStr(present, name) {
			return nil, unsafeRewrite(site.NodeID, dim,
				"the selection keeps tool %q, but this call site declares %v; the tree and the IR the "+
					"selection was validated against disagree, so no deletion is emitted", name, present)
		}
	}
	if len(edits) == 0 {
		// The selection keeps everything: a no-op prune. Not an error — resolve already recorded it in
		// config_hash — but no bytes change, which is the honest diff for "nothing was pruned".
		return nil, nil
	}
	return edits, nil
}

// absorbSeparator widens a deleted element's byte range to swallow its list separator, so the result is
// still a well-formed composite literal.
//
// It prefers the comma AFTER the element and falls back to the one before, which is what makes deleting
// the LAST element work: `{a, b}` minus `b` has no trailing comma to take, so the comma after `a` is the
// one that has to go. It stops at a newline in both directions, because crossing one would change the
// file's line count (see the file header).
func absorbSeparator(src []byte, start, end int) (int, int) {
	j := end
	for j < len(src) && isHSpace(src[j]) {
		j++
	}
	if j < len(src) && src[j] == ',' {
		j++
		for j < len(src) && isHSpace(src[j]) {
			j++
		}
		return start, j
	}
	k := start
	for k > 0 && isHSpace(src[k-1]) {
		k--
	}
	if k > 0 && src[k-1] == ',' {
		k--
		for k > 0 && isHSpace(src[k-1]) {
			k--
		}
		return k, end
	}
	return start, end
}

func isHSpace(b byte) bool { return b == ' ' || b == '\t' }

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// spanRewriteTools is the tree-sitter engines' entry for the tools dimension.
//
// 🔴 It used to be a flat refusal whose sentence named the wrong owner — "no pruner for this language has
// landed yet (Go is the first)". That read like a rewriter gap and never was one: deleting a written
// element from a written list needs no per-SDK knowledge and constructs nothing. What was missing was
// the frontend RECORDING which written element is which tool, and wave 14d landed that
// (discovery/toolsplit_span.go). So this is now the same deletion the Go pruner performs, over spans
// instead of AST positions, and the refusals below are about the SOURCE rather than about the language.
//
// 🚫 It never infers which element is which. The elements come back from the same splitter the frontend
// recorded the split with, so "the tool the selection names" and "the element at the call site" are one
// spelling — not two that have to be kept in step by whoever edits next.
func spanRewriteTools(site discovery.SpanCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimTools)

	loc := site.ArgMap.Tools
	if loc == nil {
		return nil, refuseShape(site.NodeID, dim,
			"the registry row matched for this call site declares no tools locator, so there is no tool list "+
				"to prune; the SDK this row covers does not take tools at the call site")
	}
	v, written := site.Keywords[loc.Name]
	if !written {
		if u := site.KeywordUnpacking; u != nil {
			return nil, refuseShape(site.NodeID, dim,
				"this call site passes %s, so the tools it offers are assembled somewhere else in the program "+
					"and are not written here; there is no declaration to delete. 🔴 A property of the call "+
					"site, NOT of %s support — a pruner for this language refuses it for the same reason",
				u.Text, languageDisplay(site.Language))
		}
		return nil, refuseShape(site.NodeID, dim,
			"this call site writes no %s argument, so the selection %v has nothing to prune; the tools reach "+
				"the SDK some other way and a deletion here would delete nothing while claiming to",
			loc.Name, o.ToolSelection)
	}
	if err := checkSpan(site.NodeID, dim, v.Value, len(src)); err != nil {
		return nil, err
	}
	text := string(src[v.Value.Start:v.Value.End])
	if !discovery.IsWrittenList(site.Language, text) {
		return nil, refuseShape(site.NodeID, dim,
			"this call site's tool set is %s, not a list written here, so there is no declaration to delete; "+
				"pruning it would mean guessing which of the tools it builds to drop, and a guess that parses "+
				"is the failure mode with no downstream net", describeSpanValue(v, text))
	}
	elems, err := discovery.SplitWrittenList(site.Language, text)
	if err != nil {
		return nil, refuseShape(site.NodeID, dim,
			"this call site's tool list cannot be split into the elements the author wrote: %v", err)
	}

	kept := map[string]bool{}
	for _, name := range o.ToolSelection {
		kept[name] = true
	}

	base := v.Value.Start
	var edits []edit
	present := make([]string, 0, len(elems))
	for _, e := range elems {
		present = append(present, e.Text)
		if kept[e.Text] {
			continue
		}
		start, end := absorbSeparator(src, base+e.Start, base+e.End)
		if lineOf(src, start) != lineOf(src, end) {
			return nil, refuseShape(site.NodeID, dim,
				"tool %s spans %s:%d-%d; deleting a multi-line element would remove line(s) and shift every "+
					"line below it, which this engine does not do", e.Text, site.FileRel,
				lineOf(src, start), lineOf(src, end))
		}
		edits = append(edits, edit{Start: start, End: end, New: "", NodeID: site.NodeID, Dim: dim})
	}

	// 🔴 Fail closed on a selection the call site does not carry — same rule as the Go pruner, and for
	// the same reason: reaching here means the TREE and the IR the selection was validated against
	// disagree, and emitting anyway would prune against a call site nobody validated.
	for _, name := range o.ToolSelection {
		if !containsStr(present, name) {
			return nil, refuseShape(site.NodeID, dim,
				"the selection keeps tool %q, but this call site declares %v; the tree and the IR the "+
					"selection was validated against disagree, so no deletion is emitted", name, present)
		}
	}
	if len(edits) == 0 {
		return nil, nil // the selection keeps everything: recorded in config_hash, no bytes change
	}
	return edits, nil
}
