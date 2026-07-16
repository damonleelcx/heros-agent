package discovery

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// defaultMaxWalkDepth bounds AST recursion so hostile/deeply-nested source degrades to a diagnostic
// instead of a stack overflow (I7 / doc 08 F8). Legitimate Go nests far shallower than this.
const defaultMaxWalkDepth = 512

// DetectPackage runs the registry detector (§3.3) and declared-entrypoint detector (§3.4) over one
// package in a single AST walk, emitting one DetectedCallSite per (source, call) — the merge stage
// (§3.5) unifies sites that share a node_id. `decl` may be nil (no llm-eval.yaml — valid, doc 05 §3.2).
func DetectPackage(pkg *Package, reg *Registry, decl *declaredIndex) ([]DetectedCallSite, []Diagnostic) {
	var sites []DetectedCallSite
	var diags []Diagnostic
	for _, f := range pkg.Files {
		d := &detector{file: f, reg: reg, decl: decl, occ: map[string]int{}, clo: map[string]int{}, maxDepth: defaultMaxWalkDepth}
		ast.Walk(ctxVisitor{d: d, sym: "<file>", depth: 0}, f.AST)
		sites = append(sites, d.sites...)
		diags = append(diags, d.diags...)
	}
	return sites, diags
}

type detector struct {
	file          *ParsedFile
	reg           *Registry
	decl          *declaredIndex
	occ           map[string]int // (symbol \x00 selector) -> next occurrence index
	clo           map[string]int // parent symbol -> next closure ordinal
	sites         []DetectedCallSite
	diags         []Diagnostic
	maxDepth      int
	depthReported bool
}

// ctxVisitor walks the AST carrying the enclosing-symbol context and a depth bound.
type ctxVisitor struct {
	d     *detector
	sym   string
	depth int
}

func (v ctxVisitor) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		return nil
	}
	if v.depth > v.d.maxDepth {
		if !v.d.depthReported {
			v.d.diags = append(v.d.diags, Diagnostic{
				Code: CodeExprDepthExceeded, Severity: SeverityWarn, File: v.d.file.RelPath,
				Message: "AST depth bound exceeded; subtree skipped (hostile-input guard)",
			})
			v.d.depthReported = true
		}
		return nil // stop descending (skip-and-report)
	}
	switch x := n.(type) {
	case *ast.FuncDecl:
		return ctxVisitor{v.d, funcDeclSym(x), v.depth + 1}
	case *ast.FuncLit:
		k := v.d.clo[v.sym]
		v.d.clo[v.sym] = k + 1
		return ctxVisitor{v.d, v.sym + ".func" + strconv.Itoa(k), v.depth + 1}
	case *ast.CallExpr:
		v.d.handleCall(x, v.sym)
		return ctxVisitor{v.d, v.sym, v.depth + 1} // descend into args for nested calls
	}
	return ctxVisitor{v.d, v.sym, v.depth + 1}
}

func (d *detector) handleCall(call *ast.CallExpr, sym string) {
	root, rootIdent, chain := selectorParts(call.Fun)
	sel := selectorString(root, rootIdent, chain)
	if sel == "" {
		return
	}
	// Occurrence index is counted over EVERY call with this (symbol, selector), so a matched node's
	// index is its position among same-selector calls regardless of which detector matched it.
	key := sym + "\x00" + sel
	idx := d.occ[key]
	d.occ[key]++

	id := NodeIdentity{ModulePkgPath: d.file.PkgPath, EnclosingSymbolFQN: sym, Selector: sel, OccurrenceIndex: idx}
	nodeID := id.NodeID()

	// Language-neutral position (so the emitter never touches go/ast).
	lineStart := d.file.Fset.Position(call.Pos()).Line
	lineEnd := d.file.Fset.Position(call.End()).Line

	// §3.3 registry detector
	if row, basis, ok := matchRegistryRows(d.reg, d.file.Imports, d.file.importsPath, root, rootIdent, chain); ok {
		d.sites = append(d.sites, DetectedCallSite{
			Identity: id, NodeID: nodeID, Call: call, File: d.file,
			FileRel: d.file.RelPath, LineStart: lineStart, LineEnd: lineEnd,
			Sources: []DetectionSource{SourceRegistry}, Basis: []MatchBasis{basis},
			RegistryRow: row.ID, ArgMap: row.ArgMap, ProviderHint: row.ProviderHint, Opacity: row.Opacity,
		})
	}
	// §3.4 declared-entrypoint detector (co-equal source)
	if e, ok := matchDeclaredEntries(d.decl, d.file.Imports, d.file.importsPath, d.file.PkgPath, root, rootIdent, chain); ok {
		d.sites = append(d.sites, DetectedCallSite{
			Identity: id, NodeID: nodeID, Call: call, File: d.file,
			FileRel: d.file.RelPath, LineStart: lineStart, LineEnd: lineEnd,
			Sources: []DetectionSource{SourceDeclared}, Basis: []MatchBasis{BasisDeclared},
			DeclaredSym: e.Symbol, ArgMap: e.Args, ProviderHint: e.Provider,
			DetectOnly: e.DetectOnly, Invocation: e.Invocation,
		})
	}
}

// matchRegistryRows resolves a call against the signature registry (FR1) — a LANGUAGE-NEUTRAL function
// shared by the Go and tree-sitter frontends. Package/module-qualified calls resolve the root via the
// import map (high confidence); method calls fall back to import-presence + selector-suffix
// (BasisSelectorImport) since the receiver is untyped without type resolution.
func matchRegistryRows(reg *Registry, imports map[string]string, importsPath func(string) bool, root string, rootIdent bool, chain []string) (*SignatureRow, MatchBasis, bool) {
	rootPath := ""
	if rootIdent {
		rootPath = imports[root]
	}
	for i := range reg.Rows {
		r := &reg.Rows[i]
		selParts := strings.Split(r.Selector, ".")
		if r.SymbolKind == PackageFunc {
			// Bare imported function: `generateText(...)` with `import { generateText } from "ai"`, or
			// `from langchain import f; f()` — root is the imported name, chain is empty.
			if len(chain) == 0 && rootIdent && imports[root] == r.ImportPath && root == r.Selector {
				return r, BasisPackageQualified, true
			}
			// Package-qualified: `llms.GenerateFromSinglePrompt(...)` — root is the package, chain the func.
			if rootPath != "" && rootPath == r.ImportPath && equalStrs(chain, selParts) {
				return r, BasisPackageQualified, true
			}
			continue
		}
		if !importsPath(r.ImportPath) {
			continue
		}
		if hasSuffixStrs(chain, selParts) {
			if rootPath == r.ImportPath {
				return r, BasisPackageQualified, true
			}
			return r, BasisSelectorImport, true
		}
	}
	return nil, "", false
}

// matchDeclaredEntries resolves a call against user-declared entrypoints (FR2) — LANGUAGE-NEUTRAL,
// shared by both frontends. Free-function wrappers resolve the qualifying package via the import map;
// method wrappers fall back to import-presence + method name.
func matchDeclaredEntries(decl *declaredIndex, imports map[string]string, importsPath func(string) bool, pkgPath string, root string, rootIdent bool, chain []string) (*DeclaredEntry, bool) {
	if decl == nil {
		return nil, false
	}
	for i := range decl.entries {
		e := &decl.entries[i]
		if len(chain) == 0 {
			// A bare call `func(...)`: either unqualified within the wrapper's own defining package
			// (`Complete()` in Go), or a `from module import func` binding (`complete()` in Python).
			if rootIdent && !e.IsMethod && root == e.FuncName && (pkgPath == e.ImportPath || imports[root] == e.ImportPath) {
				decl.matched[i] = true
				return e, true
			}
			continue
		}
		if chain[len(chain)-1] != e.FuncName {
			continue
		}
		if !e.IsMethod {
			// Qualified free func: `llm.Complete(...)` with alias -> the declared import path.
			if rootIdent && len(chain) == 1 && imports[root] == e.ImportPath {
				decl.matched[i] = true
				return e, true
			}
			continue
		}
		// Method wrapper: `svc.Summarize(...)`. Receiver type is unresolvable without type info, so match
		// on import-presence of the defining package + method name (heuristic, documented in doc.go).
		if importsPath(e.ImportPath) {
			decl.matched[i] = true
			return e, true
		}
	}
	return nil, false
}

// selectorParts unwinds a call target into its root identifier and the selector chain after the root.
// `client.Messages.New` -> ("client", true, ["Messages","New"]);  `foo()` -> ("foo", true, []);
// `bar().Baz` -> ("", false, ["Baz"]).
func selectorParts(e ast.Expr) (root string, rootIdent bool, chain []string) {
	cur := e
	for {
		switch x := cur.(type) {
		case *ast.SelectorExpr:
			chain = append([]string{x.Sel.Name}, chain...)
			cur = x.X
		case *ast.Ident:
			return x.Name, true, chain
		default:
			return "", false, chain
		}
	}
}

// selectorString is the node-identity selector: the chain AFTER the receiver/package root, so it is
// stable under renaming the receiver variable or import alias (doc 06 §3.1). For an unqualified call
// it is the function name.
func selectorString(root string, rootIdent bool, chain []string) string {
	if len(chain) == 0 {
		if rootIdent {
			return root
		}
		return ""
	}
	return strings.Join(chain, ".")
}

func funcDeclSym(fd *ast.FuncDecl) string {
	name := fd.Name.Name
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		if recv := recvTypeName(fd.Recv.List[0].Type); recv != "" {
			return "(" + recv + ")." + name
		}
	}
	return name
}

func recvTypeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			return "*" + id.Name
		}
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr: // generic receiver Foo[T]
		if id, ok := x.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.IndexListExpr: // generic receiver Foo[T, U]
		if id, ok := x.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// hasSuffixStrs reports whether chain ends with the sel sequence.
func hasSuffixStrs(chain, sel []string) bool {
	if len(sel) == 0 || len(sel) > len(chain) {
		return false
	}
	off := len(chain) - len(sel)
	for i := range sel {
		if chain[off+i] != sel[i] {
			return false
		}
	}
	return true
}

// parseSingle parses one source string into a ParsedFile (test/detection helper; AST + text only).
func parseSingle(pkgPath, name, src string) (*ParsedFile, error) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	pf := &ParsedFile{RelPath: name, PkgPath: pkgPath, Fset: fset, AST: af, Imports: map[string]string{}, importPaths: map[string]bool{}}
	buildImportMap(pf)
	return pf, nil
}
