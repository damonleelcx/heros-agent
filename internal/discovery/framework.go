package discovery

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// FrameworkReader derives nodes/edges from a framework's DECLARATIVE graph definition rather than
// inferring topology from call order (§4.4, FR5, design doc 07). Readers are versioned and isolated:
// an unrecognized version degrades to a flagged, best-effort subgraph — never a crash, never silent
// mis-inference (D7). A reader NEVER executes target code (I1); it reads the builder calls statically.
//
// It reads a normalized SyntacticUnit — the language-neutral shape every frontend produces — so ONE
// reader contract serves every language. This is deliberate (10.11): a reader keyed on a language AST
// (`*ast.Package`) is structurally Go-locked and can never serve a tree-sitter frontend, which is what
// forced a second, parallel reader interface to exist in the first place. See the convergence note in
// frameworkReadersByLanguage.
type FrameworkReader interface {
	Name() string
	// Read reports whether the unit uses this framework (present) and returns its declarative graph.
	// present=false => the unit does not use the framework and no subgraph is emitted (no false subgraph).
	// A recognized-but-drifted version returns present=true with Degraded set + a drift diagnostic.
	Read(unit SyntacticUnit) (g FrameworkGraph, diags []Diagnostic, present bool)
}

// frameworkReadersByLanguage is the single source of truth for WHICH languages have declarative-framework
// support and which readers serve them — a config table, not an if/else chain, so adding a language's
// framework support is adding a row (禁止胶水逻辑 / 配置表穷举). It also answers "does a framework-DAG
// fixture make sense for language X?" in one place: a language absent from this table has no declarative
// framework we read, and an honest N/A is the correct fixture outcome for it.
//
// 🔴 CONVERGENCE NOTE (10.11) — this repo briefly had TWO framework-reader interfaces: the exported
// `FrameworkReader` (keyed on `*Package`, i.e. go/ast — Go-only) and an unexported, parallel
// `syntacticFrameworkReader` (keyed on SyntacticUnit — served Python/TS/JS/Rust/Java). Per
// 「暴露冲突不要折中平均」 the SyntacticUnit-keyed contract WON and the `*Package`-keyed one was deleted
// rather than averaged: the data model is the substantive conflict, and `*Package` is structurally
// incapable of serving any non-Go frontend (八级法则 #6 不可扩展 — a contract that hardcodes one
// language's AST cannot be extended sideways). The surviving interface keeps the loser's proven
// degrade-to-flag + diagnostics contract, which is a capability, not a competing design.
var frameworkReadersByLanguage = map[string][]FrameworkReader{
	"go":         {goGraphBuilderReader{}},
	"python":     {langGraphReader{}, crewAIReader{}},
	"typescript": {langGraphReader{}},
	"javascript": {langGraphReader{}},
	// rust / java / kotlin: no declarative agent-graph framework is read for these yet. They are absent
	// from this table on purpose — an absent row means "no framework support", which the fixtures record
	// as a documented N/A rather than a silently missing capability.
}

// FrameworkEdge is a declarative-graph edge (AddEdge => data, AddConditionalEdges => control).
type FrameworkEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// FrameworkGraph is the result of reading one declarative graph. FrameworkSource + Version + Degraded
// travel in the run report (Finding A), not on IR nodes.
type FrameworkGraph struct {
	FrameworkSource string
	Version         string
	Recognized      bool
	Degraded        bool
	SubgraphID      string
	Nodes           []string // declared node names
	Edges           []FrameworkEdge
}

// readFrameworks runs the readers registered for one language over a normalized unit. A language with no
// registered readers yields nothing — the honest outcome, not an error.
func readFrameworks(lang string, unit SyntacticUnit) ([]FrameworkGraph, []Diagnostic) {
	var graphs []FrameworkGraph
	var diags []Diagnostic
	for _, r := range frameworkReadersByLanguage[lang] {
		g, d, present := safeFramework(r, unit)
		if present {
			graphs = append(graphs, g)
			diags = append(diags, d...)
		}
	}
	return graphs, diags
}

// safeFramework runs one framework reader with a recover guard: a reader panic (a versioned reader bug on
// drifted input) becomes a flagged diagnostic, never a crash (doc 07 §2.1 / doc 08 F10).
func safeFramework(reader FrameworkReader, unit SyntacticUnit) (fg FrameworkGraph, diags []Diagnostic, present bool) {
	defer func() {
		if r := recover(); r != nil {
			present = true
			fg = FrameworkGraph{FrameworkSource: reader.Name(), Degraded: true, SubgraphID: "sg_" + sanitizeID(unit.PkgPath)}
			diags = []Diagnostic{{
				Code: CodeFrameworkReaderErr, Severity: SeverityWarn, Symbol: fg.SubgraphID,
				Message: "framework reader " + reader.Name() + " panicked; subgraph degraded",
			}}
		}
	}()
	return reader.Read(unit)
}

// goGraphBuilderReader is the Go-native reader (design doc 07 §4): it recognizes the declarative
// state-graph builder API shared by langgraphgo / langchaingo-style graphs — AddNode / AddEdge /
// AddConditionalEdges / SetEntryPoint — by method name, within a package importing a known framework
// module. It is deliberately NOT the Python LangGraph reader: it reports its own framework_source, and
// the language table keeps the two from ever firing on the same unit.
type goGraphBuilderReader struct{}

// NewGoGraphBuilderReader returns the Go framework reader.
func NewGoGraphBuilderReader() FrameworkReader { return goGraphBuilderReader{} }

func (goGraphBuilderReader) Name() string { return "go-graph-builder" }

// knownFrameworkModules maps an import-path substring to the version this reader is validated against.
// A match with an unexpected version => recognized=false => degrade-to-flag.
var knownFrameworkModules = map[string]string{
	"langgraphgo":        "v0",
	"langchaingo/graph":  "v0",
	"langchaingo/agents": "v0",
}

// detect reports framework presence + version recognition from the unit's imports.
func (goGraphBuilderReader) detect(unit SyntacticUnit) (string, bool, bool) {
	for path := range unit.ImportPaths {
		for mod, ver := range knownFrameworkModules {
			if strings.Contains(path, mod) {
				// P1 recognizes the v0 line only; anything else degrades (doc 07 §3.2).
				return ver, true, true
			}
		}
	}
	return "", false, false
}

func (r goGraphBuilderReader) Read(unit SyntacticUnit) (FrameworkGraph, []Diagnostic, bool) {
	ver, present, recognized := r.detect(unit)
	if !present {
		return FrameworkGraph{}, nil, false
	}
	g := FrameworkGraph{
		FrameworkSource: r.Name(),
		Version:         ver,
		Recognized:      recognized,
		Degraded:        !recognized,
		SubgraphID:      "sg_" + sanitizeID(unit.PkgPath),
	}
	readBuilderGraph(&g, unit, goBuilderMethod)

	var diags []Diagnostic
	if g.Degraded {
		diags = append(diags, Diagnostic{
			Code: CodeFrameworkVersionDrift, Severity: SeverityWarn, Symbol: g.SubgraphID,
			Message: "framework version not recognized; subgraph read best-effort and flagged (degrade-to-flag)",
		})
	}
	return g, diags, true
}

// goBuilderMethod maps the Go builder API's PascalCase method names to the canonical builder verbs.
func goBuilderMethod(m string) string {
	switch m {
	case "AddNode":
		return "add_node"
	case "AddEdge":
		return "add_edge"
	case "AddConditionalEdges":
		return "add_conditional_edges"
	case "SetEntryPoint":
		return "set_entry_point"
	}
	return ""
}

// readBuilderGraph is the shared declarative-state-graph walk used by every builder-style reader
// (Go's AddNode/AddEdge, Python/JS's add_node/addNode). `verb` maps a language's method spelling to the
// canonical builder verb, so the graph semantics live in exactly one place for every language.
//
// Node/edge names come from the call's string-literal args in source order (RawCallSite.PositionalStrings,
// which flattens routing-map VALUES and drops the label KEYS — see pyPositionalStrings / goPositionalStrings).
// This is the syntactic floor: a builder arg that is not a string literal is not resolvable without types
// and is honestly skipped, never guessed (I5).
func readBuilderGraph(g *FrameworkGraph, unit SyntacticUnit, verb func(string) string) {
	nodeSet := map[string]bool{}
	edgeSeen := map[FrameworkEdge]bool{}
	addEdge := func(e FrameworkEdge) {
		if !edgeSeen[e] {
			edgeSeen[e] = true
			g.Edges = append(g.Edges, e)
		}
	}
	for _, cs := range unit.CallSites {
		switch verb(builderLeaf(cs)) {
		case "add_node", "set_entry_point":
			if len(cs.PositionalStrings) >= 1 {
				nodeSet[cs.PositionalStrings[0]] = true
			}
		case "add_edge":
			if len(cs.PositionalStrings) >= 2 {
				from, to := cs.PositionalStrings[0], cs.PositionalStrings[1]
				addEdge(FrameworkEdge{From: from, To: to, Kind: "data"})
				nodeSet[from], nodeSet[to] = true, true
			}
		case "add_conditional_edges":
			if len(cs.PositionalStrings) >= 1 {
				from := cs.PositionalStrings[0]
				nodeSet[from] = true
				for _, to := range cs.PositionalStrings[1:] { // routing-map values (keys were skipped upstream)
					if to != from {
						addEdge(FrameworkEdge{From: from, To: to, Kind: "control"})
						nodeSet[to] = true
					}
				}
			}
		}
	}
	for n := range nodeSet {
		g.Nodes = append(g.Nodes, n)
	}
	sortStrings(g.Nodes)
	sortFrameworkEdges(g.Edges)
}

// goUnitFromPackage normalizes a parsed Go package into the language-neutral SyntacticUnit the framework
// readers consume. It is scoped to what a declarative-graph reader needs (imports + builder call sites);
// Go's node/edge extraction keeps using the richer go/ast path, which has real import resolution.
//
// The unit is built per PACKAGE (not per file, as the tree-sitter frontends do) because a Go package is
// the unit a graph is declared in, and SubgraphID is derived from PkgPath.
func goUnitFromPackage(pkg *Package) SyntacticUnit {
	unit := SyntacticUnit{
		PkgPath:     pkg.PkgPath,
		Imports:     map[string]string{},
		ImportPaths: map[string]bool{},
	}
	for _, f := range pkg.Files {
		if unit.RelPath == "" {
			unit.RelPath = f.RelPath
		}
		for p := range f.importPaths {
			unit.ImportPaths[p] = true
		}
		for name, p := range f.Imports {
			unit.Imports[name] = p
		}
		ast.Inspect(f.AST, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			root, rootIdent, chain := goCallTarget(ce)
			if len(chain) == 0 {
				return true
			}
			unit.CallSites = append(unit.CallSites, RawCallSite{
				Root: root, RootIdent: rootIdent, Chain: chain,
				EnclosingSymbol: "<pkg>",
				// 🔴 Go populates NO argument spans, here or anywhere, and that is a decision rather
				// than an omission. Two independent reasons, either sufficient:
				//
				//  1. Go's DETECTION path never builds a RawCallSite. It builds DetectedCallSite
				//     directly, carrying the *ast.CallExpr and its FileSet — real positions, real
				//     import resolution, unwrap-through-`anthropic.F`, field-path and option-ctor
				//     locators. ADR-003 ratifies keeping it: "Go keeps its go/ast path: it has real
				//     type information and is strictly stronger than a syntactic one." Spans here
				//     would be a second, weaker way to locate the same argument — a split
				//     source-of-truth whose two halves can disagree about which byte is the model.
				//  2. This RawCallSite is built by goUnitFromPackage, which exists ONLY to feed the
				//     framework graph readers. Those read node and edge NAMES; nothing rewrites
				//     through them. Worse, this unit is assembled per PACKAGE from many files and
				//     carries no single source buffer, so a byte offset here would be an offset into
				//     nothing well-defined — not merely unused, but actively wrong.
				//
				// The absence is TOTAL (empty map, nil insert), so no caller can read a zero-valued
				// span and splice at offset 0. go_framework_unit_populates_no_spans_test holds this.
				Keywords:          map[string]ArgValue{},
				KeywordInsert:     nil,
				PositionalStrings: goPositionalStrings(ce),
			})
			return true
		})
	}
	return unit
}

// goCallTarget builds the selector chain of a call: `g.AddNode(...)` -> ("g", true, ["AddNode"]).
func goCallTarget(ce *ast.CallExpr) (root string, rootIdent bool, chain []string) {
	sel, ok := ce.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false, nil
	}
	chain = []string{sel.Sel.Name}
	x := sel.X
	for {
		switch v := x.(type) {
		case *ast.SelectorExpr:
			chain = append([]string{v.Sel.Name}, chain...)
			x = v.X
		case *ast.Ident:
			return v.Name, true, chain
		default:
			return "", false, chain
		}
	}
}

// goPositionalStrings returns a call's string-literal args in source order, flattening routing-map VALUES
// (`map[string]string{"faq":"answer"}` -> "answer") and never the label KEYS. This mirrors Python's
// pyPositionalStrings exactly, so one shared reader walk yields identical graphs across languages.
func goPositionalStrings(ce *ast.CallExpr) []string {
	var out []string
	var collect func(e ast.Expr)
	collect = func(e ast.Expr) {
		switch v := unwrapExpr(e).(type) {
		case *ast.BasicLit:
			if s, ok := stringLiteralOf(v); ok {
				out = append(out, s)
			}
		case *ast.CompositeLit: // a routing map: collect VALUES, never KEYS
			for _, el := range v.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					collect(kv.Value)
					continue
				}
				collect(el)
			}
		}
	}
	for _, a := range ce.Args {
		collect(a)
	}
	return out
}

func stringLiteralOf(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func sanitizeID(s string) string {
	repl := strings.NewReplacer("/", "_", ".", "_", "-", "_")
	return repl.Replace(s)
}
