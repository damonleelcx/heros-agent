// Command grapheditor brings up the P5 interactive graph editor against the REAL hermes-agent graph,
// adapted for a NON-GRAPH agent (the message the whole demo answers): hermes-agent's `call_llm`
// fallback chain declares no framework graph, so Discovery emits nodes with ZERO edges. This demo:
//
//  1. loads the discovered hermes IR (real node ids / files),
//  2. scopes to the real `agent/auxiliary_client.py::call_llm` cluster (a fallback chain of LLM calls),
//  3. RECOVERS topology with the P4.5 machinery (internal/linkage via irwriteback.RecoverTopology) —
//     edges inferred from the shared conversation object, carried with provenance + confidence,
//  4. applies representative trace-refined io_contracts (§8.3) so the recovered chain has real data
//     dependencies the validator can check,
//  5. serves the editor so the coherent / adapter-inserted / rejected states are visible ON REAL
//     hermes nodes, with the recovered edges rendered as hypotheses (never framework-certain).
//
// It never executes hermes-agent — it reads the cached IR. Not a shipped service: a demo harness.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/irwriteback"
)

func main() {
	irPath := flag.String("ir", "", "path to the cached hermes IR JSON (from `p5hermes -out`)")
	addr := flag.String("addr", "127.0.0.1:8481", "listen address")
	max := flag.Int("max", 5, "max call_llm nodes to include")
	flag.Parse()
	if *irPath == "" {
		log.Fatal("-ir is required (run `p5hermes -repo <hermes> -out hermes-ir.json` first)")
	}

	raw, err := os.ReadFile(*irPath)
	if err != nil {
		log.Fatal(err)
	}
	var full discovery.IR
	if err := json.Unmarshal(raw, &full); err != nil {
		log.Fatal(err)
	}

	ir := scopeToCallLLM(&full, *max)
	fmt.Printf("scoped to %d real hermes `call_llm` nodes (non-graph: %v)\n", len(ir.Nodes), irwriteback.IsNonGraph(ir))

	// 1. Recover topology (P4.5 linkage) — the non-graph adaptation.
	ir, added := irwriteback.RecoverTopology(ir)
	fmt.Printf("recovered %d inferred edges from shared conversation state\n", added)

	// 2. Apply representative trace-refined contracts so the recovered chain has real dependencies.
	refineContracts(ir)

	s := api.New(nil, config.Config{})
	s.MountP5(src{ir: ir})
	fmt.Printf("\np5 editor (hermes-agent, recovered topology):\n  http://%s/p5/editor?workflow_id=hermes\n", *addr)
	fmt.Printf("  reorder a later call ahead of its producer → REJECTED on real hermes nodes.\n")
	log.Fatal(http.ListenAndServe(*addr, s.Handler))
}

type src struct{ ir *discovery.IR }

func (s src) IR(id string) (*discovery.IR, bool) {
	if id != "hermes" {
		return nil, false
	}
	return s.ir, true
}

// scopeToCallLLM keeps the real `call_llm` (non-async) nodes, capped, ordered by source line so the
// recovered chain follows the code's fallback order.
func scopeToCallLLM(full *discovery.IR, max int) *discovery.IR {
	var nodes []discovery.IRNode
	for _, n := range full.Nodes {
		if strings.HasSuffix(n.CallSite.Symbol, "call_llm") && !strings.Contains(n.CallSite.Symbol, "async") {
			nodes = append(nodes, n)
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].CallSite.LineStart < nodes[j].CallSite.LineStart })
	if len(nodes) > max {
		nodes = nodes[:max]
	}
	return &discovery.IR{IRVersion: full.IRVersion, Workflow: full.Workflow, Nodes: nodes}
}

// refineContracts stands in for §8.3 trace-driven schema refinement: the fallback chain threads the
// conversation `messages` object (which is exactly the shared-state signal the edges were recovered
// from), so each call requires `messages` and produces the next `messages`. One fallback candidate is
// given a differently-named output (`reply`) to demonstrate the rename ADAPTER on a real edge.
func refineContracts(ir *discovery.IR) {
	str := map[string]any{"type": "string"}
	arr := map[string]any{"type": "array"}
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
	for i := range ir.Nodes {
		n := &ir.Nodes[i]
		// Every call consumes the running conversation.
		n.IOContract.InputSchema = obj(map[string]any{"messages": arr, "model": str}, "messages")
		// Every call appends its completion to the conversation and emits it forward.
		n.IOContract.OutputSchema = obj(map[string]any{"messages": arr, "response": str})
	}
	// One fallback candidate returns `reply` instead of `messages` → the edge into the next call needs a
	// rename adapter (reply→messages). A realistic "this provider names the field differently" case.
	if len(ir.Nodes) >= 3 {
		ir.Nodes[1].IOContract.OutputSchema = map[string]any{
			"type": "object", "properties": map[string]any{"reply": map[string]any{"type": "array"}, "response": str}}
	}
}
