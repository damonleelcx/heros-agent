package discovery

import (
	"go/ast"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// FrameworkReader derives nodes/edges from a framework's DECLARATIVE graph definition rather than
// inferring topology from call order (§4.4, FR5, design doc 07). Readers are versioned and isolated:
// an unrecognized version degrades to a flagged, best-effort subgraph — never a crash, never silent
// mis-inference (D7). A reader NEVER executes target code (I1); it reads the builder calls statically.
type FrameworkReader interface {
	Name() string
	// Detect reports whether the package uses the framework and whether this reader recognizes the
	// version. recognized=false => ReadDAG still runs but the subgraph is marked degraded.
	Detect(pkg *Package) (version string, present bool, recognized bool)
	ReadDAG(pkg *Package) (FrameworkGraph, []Diagnostic)
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

// goGraphBuilderReader is the P1 Go-native reader (design doc 07 §4): it recognizes the declarative
// state-graph builder API shared by langgraphgo / langchaingo-style graphs — AddNode / AddEdge /
// AddConditionalEdges / SetEntryPoint — by method name, within a package importing a known framework
// module. It is deliberately NOT a Python LangGraph/CrewAI reader (that is the multi-language phase).
type goGraphBuilderReader struct{}

// NewGoGraphBuilderReader returns the P1 framework reader.
func NewGoGraphBuilderReader() FrameworkReader { return goGraphBuilderReader{} }

func (goGraphBuilderReader) Name() string { return "go-graph-builder" }

// knownFrameworkModules maps an import-path substring to the version this reader is validated against.
// A match with an unexpected version => recognized=false => degrade-to-flag.
var knownFrameworkModules = map[string]string{
	"langgraphgo":        "v0",
	"langchaingo/graph":  "v0",
	"langchaingo/agents": "v0",
}

func (r goGraphBuilderReader) Detect(pkg *Package) (string, bool, bool) {
	for _, f := range pkg.Files {
		for path := range f.importPaths {
			for mod, ver := range knownFrameworkModules {
				if strings.Contains(path, mod) {
					// P1 recognizes the v0 line only; anything else degrades (doc 07 §3.2).
					return ver, true, true
				}
			}
		}
	}
	return "", false, false
}

// builderMethods are the declarative-graph builder calls this reader reads.
const (
	mAddNode        = "AddNode"
	mAddEdge        = "AddEdge"
	mAddConditional = "AddConditionalEdges"
	mSetEntryPoint  = "SetEntryPoint"
)

func (r goGraphBuilderReader) ReadDAG(pkg *Package) (FrameworkGraph, []Diagnostic) {
	ver, present, recognized := r.Detect(pkg)
	g := FrameworkGraph{
		FrameworkSource: r.Name(),
		Version:         ver,
		Recognized:      recognized,
		Degraded:        present && !recognized,
		SubgraphID:      "sg_" + sanitizeID(pkg.PkgPath),
	}
	if !present {
		return g, nil
	}
	var diags []Diagnostic
	nodeSet := map[string]bool{}
	edgeSeen := map[FrameworkEdge]bool{}

	for _, f := range pkg.Files {
		ast.Inspect(f.AST, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := ce.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case mAddNode, mSetEntryPoint:
				if name, ok := stringArg(ce, 0); ok {
					nodeSet[name] = true
				}
			case mAddEdge:
				from, ok1 := stringArg(ce, 0)
				to, ok2 := stringArg(ce, 1)
				if ok1 && ok2 {
					addFrameworkEdge(&g, edgeSeen, FrameworkEdge{From: from, To: to, Kind: "data"})
					nodeSet[from], nodeSet[to] = true, true
				}
			case mAddConditional:
				from, ok := stringArg(ce, 0)
				if !ok {
					return true
				}
				nodeSet[from] = true
				// Control edges to each routing-map TARGET (the map values), not the route labels (keys).
				for _, target := range conditionalTargets(ce) {
					if target == from {
						continue
					}
					addFrameworkEdge(&g, edgeSeen, FrameworkEdge{From: from, To: target, Kind: "control"})
					nodeSet[target] = true
				}
			}
			return true
		})
	}

	for name := range nodeSet {
		g.Nodes = append(g.Nodes, name)
	}
	sort.Strings(g.Nodes)
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		return g.Edges[i].To < g.Edges[j].To
	})

	if g.Degraded {
		diags = append(diags, Diagnostic{
			Code: CodeFrameworkVersionDrift, Severity: SeverityWarn, Symbol: g.SubgraphID,
			Message: "framework version not recognized; subgraph read best-effort and flagged (degrade-to-flag)",
		})
	}
	return g, diags
}

func addFrameworkEdge(g *FrameworkGraph, seen map[FrameworkEdge]bool, e FrameworkEdge) {
	if seen[e] {
		return
	}
	seen[e] = true
	g.Edges = append(g.Edges, e)
}

func stringArg(ce *ast.CallExpr, idx int) (string, bool) {
	if idx < 0 || idx >= len(ce.Args) {
		return "", false
	}
	return stringLiteralOf(ce.Args[idx])
}

// conditionalTargets returns the target node names from an AddConditionalEdges routing map — the map
// VALUES (e.g. {"faq":"answer"} -> "answer"), not the route-label keys. Falls back to any string args
// after the source when no map literal is present.
func conditionalTargets(ce *ast.CallExpr) []string {
	var out []string
	for _, a := range ce.Args {
		if cl, ok := unwrapExpr(a).(*ast.CompositeLit); ok {
			for _, el := range cl.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if s, ok := stringLiteralOf(kv.Value); ok {
						out = append(out, s)
					}
				}
			}
		}
	}
	if len(out) == 0 { // no map literal — take remaining string-literal args after the source.
		for i := 1; i < len(ce.Args); i++ {
			if s, ok := stringLiteralOf(ce.Args[i]); ok {
				out = append(out, s)
			}
		}
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
