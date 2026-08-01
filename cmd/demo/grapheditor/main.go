// Command grapheditor serves the P5 interactive graph editor against a REAL IR through the REAL
// validate path (internal/typedcontract + internal/transform), so the unhappy-path UX can be driven in
// a browser.
//
// It exists because the first-class states — coherent / adapter-inserted (with a previewed source diff)
// / rejected (edge-anchored) — cannot be verified by asserting on markup: a test that greps the HTML
// for "Rejected" proves the string is the string. What has to be checked is that a real browser, fed a
// real verdict through the real API, renders three VISIBLY different, keyboard-operable,
// screen-reader-announced states. So this stands the whole path up: IR → validator → verdict → HTTP →
// page.
//
// Not a shipped service: a demo harness. It serves one hand-built fixture IR that exercises every
// verdict, and it seeds nothing a production process may.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/discovery"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8480", "listen address")
	flag.Parse()

	s := api.New(nil, config.Config{})
	s.MountP5(src{})
	fmt.Printf("p5 editor:\n")
	fmt.Printf("  %-14s http://%s/editor?workflow_id=%s\n", "demo", *addr, "wf")
	fmt.Printf("     A→B is a rename mismatch (answer→response): adapter-inserted state with a previewed diff.\n")
	fmt.Printf("     Move C above B (Alt+↑) to force a rejected, edge-anchored state.\n")
	log.Fatal(http.ListenAndServe(*addr, s.Handler))
}

type src struct{}

func (src) IR(id string) (*discovery.IR, bool) {
	if id != "wf" {
		return nil, false
	}
	return fixtureIR(), true
}

func fixtureIR() *discovery.IR {
	obj := func(props map[string]any, req ...string) map[string]any {
		r := make([]any, len(req))
		for i, x := range req {
			r[i] = x
		}
		m := map[string]any{"type": "object", "properties": props}
		if len(r) > 0 {
			m["required"] = r
		}
		return m
	}
	str := map[string]any{"type": "string"}
	return &discovery.IR{
		IRVersion: discovery.IRVersion, Workflow: discovery.IRWorkflow{ID: "wf", Language: "go"},
		Nodes: []discovery.IRNode{
			{NodeID: "retrieve", Kind: "static_definition", IOContract: discovery.IRIOContract{
				InputSchema: map[string]any{"type": "object"}, OutputSchema: obj(map[string]any{"answer": str})}},
			{NodeID: "summarize", Kind: "static_definition", IOContract: discovery.IRIOContract{
				InputSchema: obj(map[string]any{"response": str}, "response"), OutputSchema: obj(map[string]any{"summary": str})}},
			{NodeID: "format", Kind: "static_definition", IOContract: discovery.IRIOContract{
				InputSchema: obj(map[string]any{"summary": str}, "summary"), OutputSchema: map[string]any{"type": "object"}}},
		},
		Edges: []discovery.IREdge{
			{FromNodeID: "retrieve", ToNodeID: "summarize", Kind: "data"},
			{FromNodeID: "summarize", ToNodeID: "format", Kind: "data"},
		},
	}
}
