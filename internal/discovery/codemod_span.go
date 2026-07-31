package discovery

import (
	"fmt"
	"strings"
)

// This file is the seam the LANGUAGE-NEUTRAL half of P2's Transform Engine anchors on, and it is the
// exact counterpart of codemod.go — same job, different substrate.
//
// codemod.go hands the codemod a *ast.CallExpr and a FileSet, because for Go the codemod can ask the
// real syntax tree where an argument is. The tree-sitter frontends have no such thing to hand over: the
// parse tree is closed when Analyze returns (`defer tree.Close()`), and it must be — keeping seven
// languages' trees alive across a whole-repo walk is exactly the unbounded memory NFR3 forbids. So what
// crosses this seam is what the analyzer already extracted while the tree WAS open: byte spans.
//
// That asymmetry is the design, not a shortfall. Go keeps go/ast because it is strictly stronger (real
// positions, real expression structure, and rewritePrompt can descend into a message construction
// safely because Go SDKs put the role in a TYPED constant). A span-based site is what the languages
// without types can honestly offer, and the rewriter on the other side refuses precisely what a span
// cannot answer for.

// SpanCallSite is one discovered call site in a syntactically-analyzed (tree-sitter) language, carrying
// everything a byte-splicing rewriter needs and nothing it must not trust.
//
// It is deliberately NOT a second normalized call-site shape: every field below is copied verbatim off
// the DetectedCallSite the shared detector already produced, after the same Merge the IR was built
// from. That is what makes a node_id here address the same call site the Variant Spec was authored
// against — the property IndexGoCallSites protects by mirroring GoFrontend.Discover, protected here the
// same way and for the same reason.
type SpanCallSite struct {
	// NodeID is the id the IR carries, so a Variant Spec's node_id addresses this call site.
	NodeID string
	// Language is the Discovery language label of the frontend that found it. Carried on the SITE rather
	// than passed alongside it: the refusal a rewriter has to write ("Java has no named-argument form")
	// is about the language, and a caller that had to remember which language it asked for is a caller
	// that can name the wrong one in an error a human then acts on.
	Language string
	// ArgMap says where model/prompt/tools live in this call's arguments — from the registry row (or
	// declared entrypoint) that matched it. A nil locator means the row names no argument for that
	// dimension, which for Java/Kotlin/Rust is EVERY row. See argumentForms in internal/transform.
	ArgMap ArgMap
	// ProviderHint is the provider the matched row declares. The codemod checks an override against it:
	// a call site's SDK is not something a model argument can change (ADR-002).
	ProviderHint string
	// Keywords is the keyword/named arguments the call site WRITES: name -> span + kind. Empty for Java
	// and Rust, whose analyzers emit an empty map because the LANGUAGES have no named-argument form.
	Keywords map[string]ArgValue
	// KeywordInsert is where a keyword argument the call site does not write could be added; nil means
	// nowhere. Also nil when the parser RECOVERED the argument list rather than parsing it — an
	// insertion invents a position, and inventing one inside a region the parser was guessing at is how
	// a codemod corrupts a file (see argInsertAtEnd).
	KeywordInsert *ArgInsert
	// KeywordUnpacking is the unpacking (`**kwargs`, `*args`) the call site passes, when one is why
	// KeywordInsert is nil. Carried for the same reason Opacity is: so the rewriter refuses with the
	// REAL reason. "This call site has no argument list to add an argument to" would be plainly false
	// about `create(**kwargs)` — it has one, and that is the problem. See ArgUnpacking.
	KeywordUnpacking *ArgUnpacking
	// Opacity names IR fields inherently unresolvable for this entrypoint (e.g. Bedrock's serialized
	// body). Carried so a rewriter can refuse with the real reason rather than "argument not present".
	Opacity []string
	// BindingSites are the values this call's row says are bound BEFORE the call — a builder-chain call
	// or a request value's field — keyed by IR dimension. Empty for every row that points inside the
	// argument list, so the existing rewriters see no change at all (P13 FR52).
	BindingSites map[string]BindingScan
	// CallSpan is the byte span of the call expression itself — the extent a harness loop wraps (P18
	// §11). Zero when the analyzer recorded none; a rewriter must refuse rather than read that as
	// offset 0.
	CallSpan ArgSpan

	FileRel            string
	LineStart, LineEnd int
}

// IndexSpanCallSites re-runs ONE syntactic frontend's detection over a checked-out tree and returns its
// call sites keyed by node_id — the tree-sitter counterpart of IndexGoCallSites.
//
// One language, named by the caller, rather than all of them: the workflow's language is Discovery's
// answer (workflow.language) and it flows in from there, exactly as it does into worktree.VerifierFor.
// Indexing every frontend would be a second, weaker way to answer a question that already has an
// answer, and it would produce a patch spanning two languages that the single per-language verifier
// behind it could not honestly gate.
//
// `decl` is nil here, matching IndexGoCallSites: a declared-only node is simply absent from the index
// and the caller rejects it loudly (FR5), rather than being half-rewritten.
//
// It never executes the target — tree-sitter is a pure parser (I1).
func IndexSpanCallSites(root, language string, reg *Registry) (map[string]SpanCallSite, error) {
	fe, err := syntacticFrontendFor(language)
	if err != nil {
		return nil, err
	}
	if reg == nil {
		if reg, err = DefaultRegistry(); err != nil {
			return nil, fmt.Errorf("discovery: load registry: %w", err)
		}
	}
	langReg := reg.ForLanguage(fe.Language()) // a call can only match a row of its own language (10.2)

	out := map[string]SpanCallSite{}
	var diags []Diagnostic
	err = fe.eachUnit(root, &diags, func(unit SyntacticUnit) {
		nodes, _, _ := detectSyntacticUnit(unit, langReg, nil)
		for _, n := range nodes {
			s := n.Site
			out[s.NodeID] = SpanCallSite{
				NodeID: s.NodeID, Language: fe.Language(), ArgMap: s.ArgMap,
				ProviderHint: s.ProviderHint, Keywords: s.Keywords, KeywordInsert: s.KeywordInsert,
				KeywordUnpacking: s.KeywordUnpacking, Opacity: s.Opacity, BindingSites: s.BindingSites,
				CallSpan: s.CallSpan,
				FileRel:  s.FileRel, LineStart: s.LineStart, LineEnd: s.LineEnd,
			}
		}
	})
	if err != nil {
		return nil, fmt.Errorf("discovery: walk %s: %w", root, err)
	}
	return out, nil
}

// ParseCheck reports whether src parses as `language` with no syntax errors.
//
// # Why this exists, and why it is not a bool
//
// It is the non-Go half of the transform engine's minimality gate. Go's gate re-runs go/parser on the
// rewritten bytes and rejects a splice that produced invalid Go before the build gate ever sees it. The
// same assertion has to hold for a Python or Kotlin splice, and it cannot be skipped just because
// tree-sitter never "fails": tree-sitter is ERROR-TOLERANT, so a corrupted splice does not raise — it
// yields a tree with ERROR/MISSING nodes in it and everything downstream carries on. "It parsed" is the
// answer tree-sitter always gives; whether it parsed CLEANLY is the question, and this asks it.
//
// # It runs the analyzer, rather than picking a grammar
//
// Deliberately: `parser.SetLanguage(python.GetLanguage())` already lives inside pythonAnalyzer, and a
// second place choosing a grammar per language is a second place to get it wrong — a gate checking
// rewritten TSX against the JavaScript grammar would reject sound rewrites, or pass broken ones.
// Same door, same grammar, by construction.
//
// A nil error means: the file parses cleanly. Any error names what is wrong, in a sentence a human can
// act on.
func ParseCheck(language, rel string, src []byte) error {
	fe, err := syntacticFrontendFor(language)
	if err != nil {
		return err
	}
	_, diags := fe.analyzer.Analyze(rel, src)
	for _, d := range diags {
		if d.Code == CodeParseError {
			return fmt.Errorf("%s does not parse cleanly as %s: %s (first problem near line %d)",
				rel, language, d.Message, d.Line)
		}
	}
	return nil
}

// syntacticFrontendFor returns the tree-sitter frontend for a Discovery language label.
//
// Built from DefaultFrontends rather than from a table of its own, so "which languages are analyzed
// syntactically" has exactly one answer and adding a frontend cannot forget to update this one. The Go
// frontend is not a *SyntacticFrontend and is rejected here BY TYPE, not by name: Go has its own,
// stronger codemod seam (IndexGoCallSites), and a Go label arriving here means a caller dispatched
// wrong — a loud error, never a silent weaker path.
func syntacticFrontendFor(language string) (*SyntacticFrontend, error) {
	want := strings.ToLower(strings.TrimSpace(language))
	var known []string
	for _, fe := range DefaultFrontends() {
		sf, ok := fe.(*SyntacticFrontend)
		if !ok {
			continue
		}
		known = append(known, sf.Language())
		if sf.Language() == want {
			return sf, nil
		}
	}
	return nil, fmt.Errorf("discovery: no syntactic frontend for language %q (have: %s)",
		language, strings.Join(known, ", "))
}
