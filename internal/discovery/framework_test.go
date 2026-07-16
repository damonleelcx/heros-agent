package discovery

import "testing"

// §4.4: the framework reader derives nodes/edges from the DECLARATIVE graph (AddNode/AddEdge/
// AddConditionalEdges), tags the framework source, and reads control edges from the routing map.
func TestFrameworkReaderReadsDeclarativeGraph(t *testing.T) {
	src := `package agent
import "github.com/example/langgraphgo/graph"
func build() {
	g := graph.NewStateGraph()
	g.AddNode("classify", nil)
	g.AddNode("answer", nil)
	g.AddNode("escalate", nil)
	g.AddEdge("classify", "route")
	g.AddConditionalEdges("route", nil, map[string]string{"faq": "answer", "esc": "escalate"})
	g.SetEntryPoint("classify")
}`
	pf, err := parseSingle("example.com/app/agent", "agent.go", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg := &Package{PkgPath: "example.com/app/agent", Files: []*ParsedFile{pf}}

	r := NewGoGraphBuilderReader()
	ver, present, recognized := r.Detect(pkg)
	if !present || !recognized || ver == "" {
		t.Fatalf("Detect: want present+recognized with version, got present=%v recognized=%v ver=%q", present, recognized, ver)
	}

	g, diags := r.ReadDAG(pkg)
	if g.FrameworkSource != "go-graph-builder" {
		t.Fatalf("framework source: %q", g.FrameworkSource)
	}
	if len(diags) != 0 {
		t.Fatalf("recognized version should produce no drift diagnostic, got %v", diags)
	}
	// Nodes: classify, answer, escalate, route.
	wantNodes := map[string]bool{"classify": true, "answer": true, "escalate": true, "route": true}
	if len(g.Nodes) != len(wantNodes) {
		t.Fatalf("nodes: want %d, got %v", len(wantNodes), g.Nodes)
	}
	for _, n := range g.Nodes {
		if !wantNodes[n] {
			t.Fatalf("unexpected node %q in %v", n, g.Nodes)
		}
	}
	// Edges: classify->route (data); route->answer, route->escalate (control). Route labels faq/esc must
	// NOT become targets.
	var data, control int
	for _, e := range g.Edges {
		switch e.Kind {
		case "data":
			data++
			if e.From != "classify" || e.To != "route" {
				t.Fatalf("unexpected data edge %+v", e)
			}
		case "control":
			control++
			if e.To != "answer" && e.To != "escalate" {
				t.Fatalf("control edge target should be a routing-map value, got %+v", e)
			}
		}
	}
	if data != 1 || control != 2 {
		t.Fatalf("want 1 data + 2 control edges, got %d data / %d control: %+v", data, control, g.Edges)
	}
}

// A package that does not use a framework is simply not present (no false subgraph).
func TestFrameworkReaderAbsent(t *testing.T) {
	pf, _ := parseSingle("example.com/app/svc", "svc.go", "package svc\nfunc x() {}")
	pkg := &Package{PkgPath: "example.com/app/svc", Files: []*ParsedFile{pf}}
	_, present, _ := NewGoGraphBuilderReader().Detect(pkg)
	if present {
		t.Fatal("no framework import -> should not be present")
	}
}
