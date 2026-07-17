package discovery

import (
	"fmt"
	"go/ast"
	"go/token"
)

// This file is the seam P2's Transform Engine (internal/transform) anchors on. Discovery does not
// import P2 and never will; it exposes what it already knows so the codemod does not have to
// re-derive it.
//
// Why a seam rather than P2 re-implementing the lookup: registry.yaml is the single source of truth
// for WHERE each dimension lives at a call site (`model: {field: "params.Model", unwrap: ["anthropic.F"]}`),
// and locateArg is the single implementation of resolving that. If the codemod carried its own copy,
// adding an SDK row would silently make discovery and the codemod disagree about which argument is
// the model — discovery would report a node the codemod then rewrites in the wrong place. One row,
// one locator, both readers.

// GoCallSite is one discovered Go call site with its AST retained, which is what a codemod needs and
// the emitted IR deliberately drops. The IR's call_site is coordinates (file/symbol/lines); this is
// the syntax tree those coordinates point at.
type GoCallSite struct {
	// NodeID is the same id the IR carries: DetectedCallSite.NodeID flows unchanged through
	// ExtractedNode into IRNode, so a Variant Spec's node_id addresses this call site.
	NodeID string
	// File owns the FileSet the positions below are relative to.
	File *ParsedFile
	// Call is the matched call expression.
	Call *ast.CallExpr
	// ArgMap says where model/prompt/tools live in this call's arguments — from the registry row that
	// matched it, so it is the SDK's shape, not a guess.
	ArgMap ArgMap
	// ProviderHint is the provider the matched registry row declares (e.g. "anthropic"). The codemod
	// checks an override against it: a call site's SDK is not something a model argument can change.
	ProviderHint       string
	FileRel            string
	LineStart, LineEnd int
}

// IndexGoCallSites re-runs Go detection over a checked-out tree and returns the call sites keyed by
// node_id.
//
// It mirrors GoFrontend.Discover's detection path exactly — same language-filtered registry, same
// loader, same DetectPackage, same Merge — because any divergence would produce node_ids that do not
// match the IR the Variant Spec was authored against. A node the Spec names but this index lacks is
// therefore not a silent miss: the caller rejects it as un-rewritable (FR5), which is the correct
// fail-closed outcome rather than a diff generated against the wrong site.
//
// `decl` (llm-eval.yaml declared entrypoints) is nil here: P2's fixtures are registry-matched SDK
// calls. A declared-only node will simply be absent from the index and rejected loudly.
//
// It never executes the target — parsing only, like every other path in this package.
func IndexGoCallSites(root string, reg *Registry) (map[string]GoCallSite, error) {
	if reg == nil {
		var err error
		if reg, err = DefaultRegistry(); err != nil {
			return nil, fmt.Errorf("discovery: load registry: %w", err)
		}
	}
	goReg := reg.ForLanguage("go") // a Go call can only match a Go registry row (design 10.2)

	loader, err := NewLoader(root)
	if err != nil {
		return nil, fmt.Errorf("discovery: load %s: %w", root, err)
	}

	out := map[string]GoCallSite{}
	if _, err := loader.ForEachPackage(func(pkg *Package) error {
		sites, _ := DetectPackage(pkg, goReg, nil)
		merged, _ := Merge(sites)
		for _, s := range merged {
			// Sites from the syntactic (tree-sitter) frontends carry no go/ast, so they cannot be
			// spliced by offset. Skipping them here means a non-Go node is absent from the index and
			// rejected by the caller, rather than half-rewritten.
			if s.Call == nil || s.File == nil {
				continue
			}
			out[s.NodeID] = GoCallSite{
				NodeID: s.NodeID, File: s.File, Call: s.Call, ArgMap: s.ArgMap,
				ProviderHint: s.ProviderHint,
				FileRel:      s.FileRel, LineStart: s.LineStart, LineEnd: s.LineEnd,
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("discovery: walk %s: %w", root, err)
	}
	return out, nil
}

// LocateArg resolves an ArgLocator against a call expression, returning the expression that holds
// that dimension's value.
//
// Exported for the codemod. This is the same resolution the extractor uses to READ a dimension, which
// is exactly what makes it the right thing to use to WRITE one: the expression discovery reported a
// model from is the expression a model override must replace.
func LocateArg(call *ast.CallExpr, loc *ArgLocator, fset *token.FileSet) (ast.Expr, bool) {
	if call == nil || loc == nil {
		return nil, false
	}
	return locateArg(call, loc, fset)
}

// FindStructArg returns the options/request struct literal in a call's arguments, if there is one.
// Exported so the codemod can INSERT a field that is absent — locateArg only finds fields that are
// already written, and "the call site never set Model" is a normal case that still needs rewriting.
func FindStructArg(call *ast.CallExpr) *ast.CompositeLit {
	if call == nil {
		return nil
	}
	return findStructArg(call)
}
