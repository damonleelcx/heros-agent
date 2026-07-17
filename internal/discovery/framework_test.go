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
	g, diags, present := r.Read(goUnitFromPackage(pkg))
	if !present || !g.Recognized || g.Version == "" {
		t.Fatalf("Read: want present+recognized with version, got present=%v recognized=%v ver=%q", present, g.Recognized, g.Version)
	}
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
	_, _, present := NewGoGraphBuilderReader().Read(goUnitFromPackage(pkg))
	if present {
		t.Fatal("no framework import -> should not be present")
	}
}

// 🟠 KNOWN LIMITATION PIN (10.11) — this test does NOT bless the behaviour it asserts; it makes it
// visible and forces a red test if anyone changes it without deciding to.
//
// RawCallSite.PositionalStrings records a call's string LITERALS in source order and drops non-literal
// args entirely, so positional index is lost. A builder call whose name arg is a variable but which has a
// later string literal therefore adopts that literal as the node name — a guess, which I5 (never guess)
// says should instead be skipped. This is a PRE-EXISTING property of the syntactic floor (Python/TS/JS
// have always had it via langGraphReader); converging Go onto the shared floor means Go now shares it too.
// Go's deleted *Package reader was positionally strict here and would have emitted no node.
//
// It is not reachable through a valid langgraphgo/LangGraph call — AddNode(name, fn)'s second arg is a
// function, never a string — which is why the whole fixture corpus is byte-identical across the
// convergence. Fixing it properly means giving PositionalStrings positional fidelity across all six
// analyzers, which is a substrate change beyond this task's scope and is reported for a decision.
func TestBuilderFloorDropsPositionalFidelity_KnownLimitation(t *testing.T) {
	src := `package agent
import "github.com/example/langgraphgo/graph"
func build(nodeName string) {
	g := graph.NewStateGraph()
	g.AddNode(nodeName, "notaname")
}`
	pf, err := parseSingle("example.com/app/agent", "agent.go", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg := &Package{PkgPath: "example.com/app/agent", Files: []*ParsedFile{pf}}
	g, _, present := NewGoGraphBuilderReader().Read(goUnitFromPackage(pkg))
	if !present {
		t.Fatal("framework import present -> reader must report present")
	}
	if len(g.Nodes) != 1 || g.Nodes[0] != "notaname" {
		t.Fatalf("KNOWN-LIMITATION PIN CHANGED: the syntactic floor previously took the first string "+
			"literal (%q) as the node name. Got %v. If this changed deliberately (e.g. PositionalStrings "+
			"gained positional fidelity so a non-literal name arg is honestly skipped), that is an "+
			"IMPROVEMENT — update this pin and the Python/TS readers together.", "notaname", g.Nodes)
	}
}

// 10.11 — the framework-reader interface is language-neutral: exactly ONE reader contract exists, and the
// per-language table is the single source of truth for which languages have declarative-framework support.
// This guard goes red if a second, parallel reader interface is reintroduced for a new frontend.
func TestFrameworkReaderRegistryIsLanguageScoped(t *testing.T) {
	// Every reader in the table satisfies the one FrameworkReader contract (compile-time via the map type)
	// and every language that claims framework support must actually register a reader.
	for lang, readers := range frameworkReadersByLanguage {
		if len(readers) == 0 {
			t.Fatalf("language %q is in the framework table with no readers — remove the row instead of "+
				"claiming support it does not have", lang)
		}
		for _, r := range readers {
			if r.Name() == "" {
				t.Fatalf("language %q has a reader with no Name() — framework_source would be empty in the report", lang)
			}
		}
	}
	// The Go reader must be reachable through the table, not only through NewGoGraphBuilderReader().
	var sawGo bool
	for _, r := range frameworkReadersByLanguage["go"] {
		if r.Name() == "go-graph-builder" {
			sawGo = true
		}
	}
	if !sawGo {
		t.Fatal("go-graph-builder must be registered for language go in frameworkReadersByLanguage")
	}
	// crewai is a Python-only framework: it must NOT be registered for a language that cannot use it.
	for _, lang := range []string{"go", "typescript", "javascript", "rust", "java"} {
		for _, r := range frameworkReadersByLanguage[lang] {
			if r.Name() == "crewai" {
				t.Fatalf("crewai reader registered for %q, but CrewAI is a Python-only framework", lang)
			}
		}
	}
}
