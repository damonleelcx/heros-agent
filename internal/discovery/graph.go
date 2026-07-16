package discovery

import (
	"go/ast"
	"sort"
)

// GraphEdge is a typed edge between two nodes (frozen IR: kind ∈ {data, control}).
type GraphEdge struct {
	From string `json:"from_node_id"`
	To   string `json:"to_node_id"`
	Kind string `json:"kind"` // "data" | "control"
}

// Graph is the discovered call graph: nodes = LLM-invoking call sites, edges = data/control flow (§4.3).
type Graph struct {
	Nodes []ExtractedNode
	Edges []GraphEdge
}

// BuildGraph extracts metadata for all detected sites (grouped per file) and builds intra-procedural
// data edges: when node A's result is bound to a variable that feeds node B's arguments in the same
// function, an A→B data edge is emitted (FR4). Edge inference is best-effort and intra-procedural;
// what static analysis cannot see is simply not an edge (it is never guessed).
func BuildGraph(files []*ParsedFile, sites []DetectedCallSite) Graph {
	byFile := map[*ParsedFile][]DetectedCallSite{}
	for _, s := range sites {
		byFile[s.File] = append(byFile[s.File], s)
	}
	var g Graph
	for _, f := range files {
		fs := byFile[f]
		if len(fs) == 0 {
			continue
		}
		g.Nodes = append(g.Nodes, ExtractFile(f, fs)...)
		g.Edges = append(g.Edges, dataEdges(f, fs)...)
	}
	sortGraph(&g)
	return g
}

// dataEdges finds `x := <nodeA-call>` then a later `<nodeB-call>(... x ...)` in the same function.
func dataEdges(f *ParsedFile, sites []DetectedCallSite) []GraphEdge {
	// Map each detected call expression to its node id and enclosing symbol.
	callID := map[*ast.CallExpr]string{}
	callSym := map[*ast.CallExpr]string{}
	for _, s := range sites {
		callID[s.Call] = s.NodeID
		callSym[s.Call] = s.Identity.EnclosingSymbolFQN
	}

	// producer[(symbol,varName)] = nodeID whose result is bound to varName in that function.
	producer := map[[2]string]string{}
	ast.Inspect(f.AST, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range as.Rhs {
			ce, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			id, ok := callID[ce]
			if !ok || i >= len(as.Lhs) {
				continue
			}
			if lhsID, ok := as.Lhs[i].(*ast.Ident); ok && lhsID.Name != "_" {
				producer[[2]string{callSym[ce], lhsID.Name}] = id
			}
		}
		return true
	})
	if len(producer) == 0 {
		return nil
	}

	seen := map[GraphEdge]bool{}
	var edges []GraphEdge
	for _, s := range sites {
		sym := s.Identity.EnclosingSymbolFQN
		for _, arg := range s.Call.Args {
			ast.Inspect(arg, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				if from, ok := producer[[2]string{sym, id.Name}]; ok && from != s.NodeID {
					e := GraphEdge{From: from, To: s.NodeID, Kind: "data"}
					if !seen[e] {
						seen[e] = true
						edges = append(edges, e)
					}
				}
				return true
			})
		}
	}
	return edges
}

func sortGraph(g *Graph) {
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].NodeID < g.Nodes[j].NodeID })
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		if g.Edges[i].To != g.Edges[j].To {
			return g.Edges[i].To < g.Edges[j].To
		}
		return g.Edges[i].Kind < g.Edges[j].Kind
	})
}
