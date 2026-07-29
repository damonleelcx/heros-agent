package transform

import (
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Context materialization for the SYNTACTIC engines — and the order the questions are asked in
// ────────────────────────────────────────────────────────────────────────────────────────────
//
// This file does two things that are easy to confuse for one:
//
//  1. it materializes a SELECTION policy at a Python call site, by deleting the turns the policy does
//     not retain from the message list the author wrote — the same construction-free deletion the Go
//     engine performs (contextmaterialize.go), at the syntactic floor;
//  2. it fixes the ORDER the refusal reasons are considered in, which was wrong before and produced a
//     true-but-useless sentence.
//
// # Why the order is the substance, not a tidy-up
//
// The old dispatch refused on LANGUAGE first: any context override on a tree-sitter language got "the
// %s materializer is still being built". On a call site like hermes-agent's —
//
//	client.chat.completions.create(**api_kwargs)
//
// that sentence is true and it is the wrong answer. There is no `messages` argument at this call site
// at all; the request is assembled elsewhere and unpacked. NO rewriter, in any language, can select
// among turns that are not written there, so "wait for the Python rewriter" points the reader at an
// event that will not help them. The reason they can act on — "write the turns at the call site, or
// set the policy where the kwargs dict is built" — was the one being suppressed.
//
// So the questions are now asked cheapest-and-most-specific first, and the language question is asked
// LAST, when it is genuinely the only thing left:
//
//	is the policy applicable at a call site AT ALL?   (a fact about the POLICY, no language involved)
//	does this call site name a message list?          (a fact about the ROW)
//	does it pass **kwargs instead?                    (a fact about the CALL — actionable, and permanent)
//	is what it names a list the author WROTE?         (a fact about the SOURCE — actionable)
//	…and only then: does this language have a rewriter yet?   (a fact about US — temporary)
//
// 🔴 Every one of those is still a refusal. Nothing here weakens the no-silent-drop guarantee; what
// changes is which sentence the user reads, and only the last of the five is a promise about future
// work. A refusal that names a cause the reader cannot act on is barely better than a refusal that
// names nothing.

// spanListSplitter splits ONE language's written list literal into its top-level element spans.
//
// It is a per-language function because "where does one element of a list end" is per-language syntax
// (quoting, comments, spreads), and this is the only thing the selection rewrite needs that differs
// between them. A language absent from the table below has no context materializer, which is a fact
// this file states rather than a gap it hides.
type spanListSplitter func(text string) ([]elementSpan, error)

// elementSpan is one written element of a list literal, as offsets RELATIVE to the list text.
type elementSpan struct {
	start, end int
	text       string
}

// spanContextMaterializers is the per-language coverage table for the syntactic engines.
//
// 🔴 It is DERIVED from discovery.ListSplitLanguages(), not listed here (P16 wave 16d, D-4). The earlier
// version carried one hand-written row and a comment promising the rest were "a row plus its splitter" —
// which was true, and which is exactly the shape of statement that ages into a product boundary once the
// data model has no way to record it as a promise. Deriving it means a language that can split a written
// list can select over one, in one step, and coverage cannot claim otherwise.
//
// 🚫 The splitter answers ONE question — what are the written elements — and never which of them are
// retained. Retention is registry.SelectionPolicy.Retain, shared by every language and by the runtime,
// because which turns a policy keeps IS the policy: a per-language retention decision would make one
// config_hash describe two configurations, and the harness would compare them as one.
var spanContextMaterializers = func() map[string]spanListSplitter {
	out := map[string]spanListSplitter{}
	for _, lang := range discovery.ListSplitLanguages() {
		lang := lang
		out[lang] = func(text string) ([]elementSpan, error) {
			elems, err := discovery.SplitWrittenList(lang, text)
			if err != nil {
				return nil, err
			}
			spans := make([]elementSpan, 0, len(elems))
			for _, e := range elems {
				spans = append(spans, elementSpan{start: e.Start, end: e.End, text: e.Text})
			}
			return spans, nil
		}
	}
	return out
}()

// hasContextSelection reports whether this language can materialize a SELECTION policy — i.e. whether it
// has a list splitter. Go is native (contextmaterialize.go) and is not in the splitter table.
func hasContextSelection(language string) bool {
	l := strings.ToLower(strings.TrimSpace(language))
	if l == "go" {
		return true
	}
	_, ok := spanContextMaterializers[l]
	return ok
}

// spanRewriteContext is the tree-sitter engines' entry for the context dimension.
func spanRewriteContext(site discovery.SpanCallSite, src []byte, o variantspec.ResolvedOverride) ([]edit, error) {
	const dim = string(variantspec.DimContext)
	entry := o.Context
	if entry == nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"the context dimension was dispatched with no resolved context entry, so there is no policy to materialize")
	}
	policy := entry.Spec.Policy

	// ── 1. a fact about the POLICY. No language is involved, so it is asked first: a summarization
	// override is refused identically in every language, and telling a Python user "your language's
	// rewriter is pending" about it would promise a rewriter that will never apply it.
	form, known := contextForms[policy]
	if !known {
		return nil, unsafeRewrite(site.NodeID, dim,
			"context policy %q has no declared call-site form in this engine, so there is no evidence for what "+
				"its assembly should look like in source; the policies it can materialize are %v",
			policy, materializablePolicies())
	}
	switch form.kind {
	case ctxIdentity:
		// Proof of equivalence: the un-rewritten call site already assembles exactly this.
		return nil, nil
	case ctxNotAtCallSite:
		// 🔴 Not our backlog and not the customer's call site: this policy's content does not exist in
		// source in ANY language. The class says so, so no surface can render it as pending work.
		return nil, refuseNotAtCallSite(site.NodeID, dim,
			"context policy %q %s. It is REFUSED at the call site rather than dropped; the policy still runs "+
				"host-side where it belongs, and no language's rewriter will change that", policy, form.reason)
	}

	// ── 2. a fact about the ROW: does anything here name a message list?
	if site.ArgMap.Prompt != nil && site.ArgMap.Prompt.Form == discovery.LocOpaque {
		return nil, unsafeRewrite(site.NodeID, dim,
			"this call site carries its messages inside an opaque serialized body, so the message list policy "+
				"%q would select over is bytes this engine cannot see into", policy)
	}
	loc, err := spanLocator(site, site.ArgMap.Prompt, dim)
	if err != nil {
		return nil, err
	}

	// ── 3. a fact about the CALL, and the one this file exists for. An unpacking is not "the argument
	// is missing" — the call site has a perfectly good argument list, and the messages are in it, at
	// runtime, unwritten. This is PERMANENT for this call site and no rewriter changes it.
	v, written := site.Keywords[loc.Name]
	if !written {
		if u := site.KeywordUnpacking; u != nil {
			return nil, unsafeRewrite(site.NodeID, dim,
				"this call site passes %s, so its message list is assembled somewhere else in the program and "+
					"is not written here — there are no turns at this call site for policy %q to select among. "+
					"🔴 This is a property of the call site, NOT of %s support: a context rewriter for this "+
					"language would refuse it for exactly this reason too. Apply the policy where %s is built, "+
					"or write the messages at the call site as a list this engine can select from",
				u.Text, policy, languageDisplay(site.Language), u.Text)
		}
		return nil, unsafeRewrite(site.NodeID, dim,
			"this call site writes no %s argument, so policy %q has no message list to assemble; the messages "+
				"reach the SDK some other way and a selection here would select over nothing while claiming to",
			loc.Name, policy)
	}
	if err := checkSpan(site.NodeID, dim, v.Value, len(src)); err != nil {
		return nil, err
	}

	// ── 4. a fact about the SOURCE: is what it names a list the author WROTE? `messages=history` is a
	// variable; selecting over it would mean guessing what it holds.
	listText := string(src[v.Value.Start:v.Value.End])
	if !isWrittenList(site.Language, listText) {
		return nil, unsafeRewrite(site.NodeID, dim,
			"this call site's %s is %s, not a written list, so policy %q has no declared turns to select "+
				"among; materializing it would mean guessing what that expression holds at runtime, and a "+
				"guess that parses is the failure mode with no downstream net",
			loc.Name, describeSpanValue(v, listText), policy)
	}

	// ── 5. …and ONLY now, a fact about US. Everything above is either permanent or the user's to change;
	// this is the one branch that is a promise about future work, so it is the last one reached.
	split, ok := spanContextMaterializers[site.Language]
	if !ok {
		return nil, refuseContext(site.NodeID, site.Language, o)
	}

	sel, isSel := entry.Policy.(registry.SelectionPolicy)
	if !isSel {
		return nil, unsafeRewrite(site.NodeID, dim,
			"context policy %q is declared materializable-by-selection but its implementation does not "+
				"declare which messages it retains, so this engine has no definition to rewrite against", policy)
	}

	elems, err := split(listText)
	if err != nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"policy %q cannot select over this call site's message list: %v", policy, err)
	}
	if len(elems) == 0 {
		return nil, nil // an empty written list: any selection over it is that same empty list
	}

	// The written turns, as the policy sees them. An element has no role/content split at the syntactic
	// floor — it is one expression — so it is measured as its own source text, by the SAME estimator the
	// host-side policy uses.
	msgs := make([]registry.Message, 0, len(elems))
	for _, e := range elems {
		msgs = append(msgs, registry.Message{Content: e.text})
	}
	keep, err := sel.Retain(entry.Spec.Params, msgs)
	if err != nil {
		return nil, unsafeRewrite(site.NodeID, dim,
			"context policy %q cannot select over this call site's %d written message(s): %v",
			policy, len(msgs), err)
	}
	kept := map[int]bool{}
	for _, i := range keep {
		kept[i] = true
	}
	if len(keep) == len(msgs) {
		return nil, nil // equivalence: the policy retains every turn the call site wrote
	}
	if len(keep) == 0 {
		return nil, unsafeRewrite(site.NodeID, dim,
			"context policy %q would retain NONE of this call site's %d written message(s), which would "+
				"assemble an empty context; that is a parameter mistake, not a rewrite", policy, len(msgs))
	}

	base := v.Value.Start
	var edits []edit
	for i, e := range elems {
		if kept[i] {
			continue
		}
		start, end := absorbSeparator(src, base+e.start, base+e.end)
		if lineOf(src, start) != lineOf(src, end) {
			return nil, unsafeRewrite(site.NodeID, dim,
				"materializing %q means dropping the turn written at %s:%d-%d, and deleting a multi-line "+
					"element would remove line(s) and shift every line below it, which this engine does not do",
				policy, site.FileRel, lineOf(src, start), lineOf(src, end))
		}
		edits = append(edits, edit{Start: start, End: end, New: "", NodeID: site.NodeID, Dim: dim})
	}
	return edits, nil
}

// isWrittenList reports whether a value expression is a list literal written at the call site.
//
// Deliberately syntactic and deliberately strict: the text must OPEN with `[` and CLOSE with `]`. That
// rejects `history`, `build_messages()`, `a + b`, and `[*a] + [b]` — the last of which is a list
// expression whose written elements are not the call's elements.
func isWrittenList(language, text string) bool {
	return discovery.IsWrittenList(language, text)
}

// describeSpanValue names what the call site wrote there, for a refusal a reader can act on. It quotes
// a short expression verbatim and describes a long one, because a 400-character dict pasted into an
// error message is not evidence, it is noise.
func describeSpanValue(v discovery.ArgValue, text string) string {
	t := strings.TrimSpace(text)
	if v.Kind == discovery.ArgLiteralString || v.Kind == discovery.ArgInterpolatedString {
		return "a string"
	}
	if len(t) > 60 {
		return "an expression assembled at runtime"
	}
	return fmt.Sprintf("`%s`", t)
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// Splitting a Python list literal into its written elements
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// pythonListElements returns the top-level elements of a Python list literal.
//
// # Why this is a hand-written scan and why it REFUSES so much
//
// The syntactic frontends hand a rewriter a byte span, not a child-node list, so the element boundaries
// have to be recovered from the text. That is a scan, and a scan that gets a boundary wrong deletes the
// wrong bytes — the one failure mode this package is shaped to prevent. So the scan handles what it can
// prove and REFUSES everything else, rather than handling most cases and guessing at the rest:
//
//	*msgs / **d      REFUSED — a spread's length is unknown until runtime, so "the third turn" does not
//	                 identify anything. This is the real shape in the wild (hermes-agent's moa_loop.py
//	                 writes `[{...}, *ref_messages]`), and selecting over it would drop turns nobody
//	                 could count.
//	# comment        REFUSED — a comment binds visually to a neighbouring element and deleting one
//	                 without the other leaves a comment describing a turn that is gone.
//	''' or """       REFUSED — a triple-quoted string spans lines by nature, and a multi-line element
//	                 cannot be deleted without changing the file's line count.
//	unbalanced       REFUSED — the parse and the text disagree; nothing here is trustworthy.
//
// A trailing comma is normal Python and is not an element; it is skipped.
func pythonListElements(text string) ([]elementSpan, error) {
	t := strings.TrimSpace(text)
	off := strings.Index(text, t) // offsets must be relative to the ORIGINAL text, not the trimmed copy
	if len(t) < 2 || t[0] != '[' || t[len(t)-1] != ']' {
		return nil, fmt.Errorf("the value is not a written list literal")
	}
	inner := t[1 : len(t)-1]
	innerOff := off + 1

	var out []elementSpan
	depth := 0
	start := 0
	flush := func(end int) error {
		seg := inner[start:end]
		trimmed := strings.TrimSpace(seg)
		if trimmed == "" {
			return nil // a trailing comma, or whitespace between separators
		}
		if strings.HasPrefix(trimmed, "*") {
			return fmt.Errorf("element %q is a spread, so the list's length is not known until runtime and "+
				"a selection over it cannot identify which turns it would drop", trimmed)
		}
		lead := strings.Index(seg, trimmed)
		out = append(out, elementSpan{
			start: innerOff + start + lead,
			end:   innerOff + start + lead + len(trimmed),
			text:  trimmed,
		})
		return nil
	}

	for i := 0; i < len(inner); i++ {
		switch c := inner[i]; c {
		case '#':
			return nil, fmt.Errorf("the message list carries a comment, which binds to a neighbouring turn; " +
				"deleting a turn without it would leave a comment describing something that is gone")
		case '\'', '"':
			if strings.HasPrefix(inner[i:], strings.Repeat(string(c), 3)) {
				return nil, fmt.Errorf("the message list carries a triple-quoted string, which spans lines by " +
					"nature; a multi-line turn cannot be deleted without changing the file's line count")
			}
			j, err := skipPythonString(inner, i)
			if err != nil {
				return nil, err
			}
			i = j
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("the list's brackets are unbalanced; the parse tree and the text disagree")
			}
		case ',':
			if depth == 0 {
				if err := flush(i); err != nil {
					return nil, err
				}
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("the list's brackets are unbalanced; the parse tree and the text disagree")
	}
	if err := flush(len(inner)); err != nil {
		return nil, err
	}
	return out, nil
}

// skipPythonString returns the index of a string literal's CLOSING quote, starting at its opening one.
// Backslash escapes are honored; an unterminated literal is an error rather than a scan that runs off
// the end and mis-reads the rest of the list as string contents.
func skipPythonString(s string, i int) (int, error) {
	quote := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++ // skip the escaped byte
		case quote:
			return j, nil
		}
	}
	return 0, fmt.Errorf("a string literal in the message list is not terminated within the argument; the " +
		"parse tree and the text disagree")
}
