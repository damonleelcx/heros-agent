// Package transform implements P2's Transform Engine: it realizes a resolved Variant Spec by
// generating a deterministic, AST-anchored source transformation (codemod) that rewrites the
// discovered call sites to the spec's values (ADR-001; PRD §6 FR1/FR5a–c; tasks 2.2, 2.3, 2.4).
//
// # AST to LOCATE, bytes to EDIT
//
// This is the design decision the whole package turns on, and it is not the obvious one.
//
// The obvious implementation parses the file, mutates the tree, and re-prints it with go/printer.
// That fails task 2.4 by construction: go/printer re-prints the WHOLE file from the AST, so it
// normalizes formatting everywhere — a target that is not already gofmt-clean comes back reformatted
// end to end, and every one of those lines lands in the diff. FR5c calls that an incidental edit and
// requires the transform be rejected for it. Worse, the reviewer's diff is then unreadable, which
// defeats the entire point of ADR-001's reviewable-PR model.
//
// So: use the AST only to FIND the exact expression to change (which is what makes this a codemod and
// not a regex — a model id that also appears in a comment or an unrelated string literal is not an
// AST argument node and cannot match), then take that expression's byte range and splice new text
// into the ORIGINAL bytes. Everything outside the spliced ranges is byte-identical to what was
// checked out, so "no incidental edits, no reformatting of untouched code" is structural rather than
// a property to be tested for afterwards.
//
// # Determinism
//
// Generation is a pure function of (source bytes, resolved config). There is no map iteration, no
// clock, no randomness, and no formatting choice: edits are collected, sorted by offset, and applied
// right-to-left. Same {config_hash, source_revision} therefore means byte-identical output — task
// 2.3's requirement, made mechanical.
package transform

import (
	"errors"
	"fmt"
	"sort"
)

// edit is one byte-range replacement in one file. Offsets are byte offsets into the original file,
// half-open [Start, End) — the same convention go/token uses.
type edit struct {
	Start, End int
	New        string
	// NodeID and Dim record which node's which dimension asked for this edit. Carried so a rejection
	// or a build failure can name them (FR5b: "surface a typed error naming the node and dimension
	// whose rewrite failed to build"), and so the minimality gate can attribute every changed line.
	NodeID string
	Dim    string
	// Swap marks this edit as one half of a WIRING TRANSPOSITION (P15 15c, decisions.md D-4) rather
	// than a value replacement.
	//
	// The two classes are gated differently and deliberately so. A value rewrite may never change the
	// file's line count — that rule is what makes "only the targeted lines changed" checkable, and it
	// stays exactly as strict as it was. A swap moves whole lines by construction, so it could not pass
	// that rule; instead it must satisfy a STRONGER one: the output is the input's lines REORDERED —
	// same count, same multiset, every changed line inside one of the two swapped blocks.
	//
	// 🚫 Marking a value rewrite as a swap to get it past the line-count rule would defeat both gates.
	// The flag is set in exactly one place (the wiring materializer) and read in exactly one place (the
	// minimality gate), so there is no third caller to get it wrong.
	Swap bool
	// Binding marks an edit that replaces a value the program bound BEFORE the call — a builder-chain
	// call or a request-value field (P13 FR52). See bindingSite().
	Binding bool
	// Import marks an import line a GENERATED-MODULE MATERIALIZATION adds to a file (P18 §9.1; widened
	// by §11 to the harness module as well as the memory one).
	//
	// It is a THIRD edit class, alongside Swap and Binding, and it exists for the same reason they do:
	// memory materialization needs something no value rewrite needs — a line at the top of the file, so
	// the rewritten call site can reach the generated module — and the two ways of getting there without
	// a new class are both worse.
	//
	// 🚫 Loosening the untargeted-line rule would remove that check from EVERY rewriter in the package,
	// forever, to serve one caller — the trade P15's decisions.md D-4 refuses by name. And emitting the
	// call without the import ships source that references a module nothing imported, which is broken
	// code in a customer's repository.
	//
	// So the class carries its own rule (gateMemoryImport), and that rule is STRONGER than the one it
	// replaces rather than weaker: the output must be the input's lines with EXACTLY ONE import line
	// inserted, every other difference confined to an allowed line. A reviewer who trusts only that
	// assertion knows one import was added and nothing outside the targeted call site moved.
	//
	// 🔴 It is the one edit in the package permitted to change the file's line count, and it changes it
	// by exactly +1. Attribution survives because generate() shifts the recorded TouchedDimension lines
	// below the insertion point to match — see memoryImportShift.
	Import bool
}

// isGeneratedImport reports whether an edit set carries a generated module's import line.
//
// Unlike isSwap, this is a MIXED set by construction: the import travels with the value edits it exists
// to serve, and gating them separately would let one pass while the other failed — leaving a file with an
// import nothing uses, or a call nothing imported.
func isGeneratedImport(edits []edit) bool {
	for _, e := range edits {
		if e.Import {
			return true
		}
	}
	return false
}

// isSwap reports whether an edit set is a wiring transposition. A set is either all swap or none: a
// file cannot be simultaneously line-permuted and value-rewritten under one gate, and mixing the two
// would leave each edit checked by the other's rule.
func isSwap(edits []edit) bool {
	for _, e := range edits {
		if e.Swap {
			return true
		}
	}
	return false
}

// bindingSite marks an edit that replaces a value the program bound BEFORE the call — a builder-chain
// call or a request-value field (P13 FR52).
//
// 🔴 It exists so the minimality gate can admit exactly this edit's own line and nothing else. The
// alternative — widening every node's allowed window to "the enclosing function" — would have removed
// the untargeted-line check from every rewriter in the package, forever, to serve one caller. That is
// the same trade P15's swap refused (decisions.md D-4): a NEW EDIT CLASS with its own admission rule,
// never a loosened old one.
func (e edit) bindingSite() bool { return e.Binding }

// insertion reports whether this edit adds text rather than replacing an expression (Start == End),
// which is what happens when a call site never set the field being overridden.
func (e edit) insertion() bool { return e.Start == e.End }

var (
	// ErrOverlappingEdits means two dimensions tried to rewrite overlapping bytes. Always a bug in a
	// rewriter, never something a spec author can cause — but it must fail loudly, because applying
	// overlapping splices would silently corrupt the source.
	ErrOverlappingEdits = errors.New("transform: two rewrites overlap")
	// ErrUnsafeRewrite means the call site cannot be rewritten for this dimension without risking a
	// behavior change. FR5 requires rejecting these BEFORE any transform is applied or run, rather
	// than emitting a diff nobody can trust.
	ErrUnsafeRewrite = errors.New("transform: cannot rewrite this call site safely")
	// ErrNodeNotFound means the spec names a node the tree at source_revision does not contain.
	ErrNodeNotFound = errors.New("transform: no call site for this node at source_revision")
)

// RewriteError names the node and dimension a failure is about, and — for a refusal — WHICH KIND of
// thing is missing (P13 FR43).
//
// # Why Cause is a field and not a sentence
//
// Detail is written for a human and is the right place for the specifics. But three genuinely different
// failures reach this type, and they are answered by three different people:
//
//	CauseNotAtCallSite   — nobody. The value does not exist in source in ANY language.
//	CauseCallSiteShape   — the customer's engineer. Their call site cannot express it.
//	CauseNoMaterializer  — us. The artifact has not landed.
//
// A console badge, a CLI exit path and a test all have to branch on that distinction, and branching on
// prose is how a reworded refusal silently changes behaviour. So the class is DATA, and the sentence
// stays free to be as specific as it likes.
type RewriteError struct {
	NodeID string
	Dim    string
	Err    error
	Detail string
	// Cause is the refusal class. Empty on errors that are not refusals (ErrNodeNotFound, an internal
	// failure); every ErrUnsafeRewrite carries one — TestEveryRefusalCarriesACauseClass holds it.
	Cause CauseClass
}

func (e *RewriteError) Error() string {
	s := e.Err.Error()
	if e.NodeID != "" {
		s += fmt.Sprintf(": node %q", e.NodeID)
	}
	if e.Dim != "" {
		s += fmt.Sprintf(", dimension %q", e.Dim)
	}
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

func (e *RewriteError) Unwrap() error { return e.Err }

// unsafeRewrite is the un-classified refusal constructor, retained so that a refusal is never blocked on
// choosing a class — but see refuseShape / refuseNoMaterializer / refuseNotAtCallSite, which are what
// callers should reach for. A refusal built here defaults to CauseCallSiteShape, the only class that is
// safe as a default: it says "this site cannot carry it", which is true of every refusal by
// construction, and it never over-claims that the platform owes work.
func unsafeRewrite(nodeID, dim, format string, args ...any) *RewriteError {
	return &RewriteError{NodeID: nodeID, Dim: dim, Err: ErrUnsafeRewrite,
		Detail: fmt.Sprintf(format, args...), Cause: CauseCallSiteShape}
}

// refuseShape refuses because THIS CALL SITE's own source cannot express the change — unpacked
// arguments, a runtime-assembled list, an SDK that binds nowhere locatable, a registry row with no
// locator. It stays true after every materializer lands.
func refuseShape(nodeID, dim, format string, args ...any) *RewriteError {
	return &RewriteError{NodeID: nodeID, Dim: dim, Err: ErrUnsafeRewrite,
		Detail: fmt.Sprintf(format, args...), Cause: CauseCallSiteShape}
}

// refuseNoMaterializer refuses because the PLATFORM has not landed the artifact for this cell. It is the
// only class that names work we owe, which is why nothing else may borrow it.
func refuseNoMaterializer(nodeID, dim, format string, args ...any) *RewriteError {
	return &RewriteError{NodeID: nodeID, Dim: dim, Err: ErrUnsafeRewrite,
		Detail: fmt.Sprintf(format, args...), Cause: CauseNoMaterializer}
}

// refuseNotAtCallSite refuses because the value does not exist in source in ANY language — a summarized
// context, a retrieved chunk set, a routing decision that belongs at the gateway. Identical in every
// language, before and after any rewriter lands.
func refuseNotAtCallSite(nodeID, dim, format string, args ...any) *RewriteError {
	return &RewriteError{NodeID: nodeID, Dim: dim, Err: ErrUnsafeRewrite,
		Detail: fmt.Sprintf(format, args...), Cause: CauseNotAtCallSite}
}

// applyEdits splices edits into src and returns the new bytes.
//
// Right-to-left application is what keeps the offsets valid: every edit's offsets are relative to the
// ORIGINAL bytes, so editing from the end backwards means an earlier edit's offsets are never shifted
// by a later one's length change. Doing it left-to-right would require tracking a running delta —
// the kind of arithmetic that is correct until the first insertion of a different length.
func applyEdits(src []byte, edits []edit) ([]byte, error) {
	if len(edits) == 0 {
		return src, nil
	}
	sorted := make([]edit, len(edits))
	copy(sorted, edits)
	// Sort by start, then end: a total order, so the result does not depend on the order rewriters
	// happened to run in. Determinism starts here.
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].End < sorted[j].End
	})

	for i := range sorted {
		e := sorted[i]
		if e.Start < 0 || e.End > len(src) || e.Start > e.End {
			return nil, fmt.Errorf("transform: edit [%d,%d) is outside the file (%d bytes)", e.Start, e.End, len(src))
		}
		if i > 0 {
			prev := sorted[i-1]
			// Touching is fine (prev.End == e.Start); overlapping is not.
			if e.Start < prev.End {
				return nil, fmt.Errorf("%w: node %q dimension %q [%d,%d) overlaps node %q dimension %q [%d,%d)",
					ErrOverlappingEdits, e.NodeID, e.Dim, e.Start, e.End, prev.NodeID, prev.Dim, prev.Start, prev.End)
			}
		}
	}

	out := make([]byte, len(src))
	copy(out, src)
	for i := len(sorted) - 1; i >= 0; i-- {
		e := sorted[i]
		out = append(out[:e.Start], append([]byte(e.New), out[e.End:]...)...)
	}
	return out, nil
}

// changedLines returns the 1-based line numbers an edit set touches, for the minimality gate. Derived
// from the ORIGINAL bytes, since that is the coordinate space the IR's call_site lines are in.
func changedLines(src []byte, edits []edit) map[int]bool {
	lines := map[int]bool{}
	for _, e := range edits {
		start := lineOf(src, e.Start)
		end := lineOf(src, e.End)
		for l := start; l <= end; l++ {
			lines[l] = true
		}
	}
	return lines
}

// lineOf returns the 1-based line containing a byte offset.
func lineOf(src []byte, off int) int {
	if off > len(src) {
		off = len(src)
	}
	line := 1
	for i := 0; i < off; i++ {
		if src[i] == '\n' {
			line++
		}
	}
	return line
}
