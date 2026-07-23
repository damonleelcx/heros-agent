// Command p5hermes drives the whole P5 pipeline against a REAL repository (built for
// github.com/nousresearch/hermes-agent): discover the IR, validate its ordering through the typed
// contract, attempt a re-arrangement, surface which nodes still carry permissive schemas, then simulate
// a traced run and reconcile it. It NEVER executes the target — it parses it (invariant I1) — so it is
// safe to point at any checkout.
//
//	go run ./cmd/p5hermes -repo /path/to/hermes-agent [-scope agent]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/heros-foreal/agentd/internal/behavioral"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/dynamictracing"
	"github.com/heros-foreal/agentd/internal/irwriteback"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/reconcile"
	"github.com/heros-foreal/agentd/internal/typedcontract"
)

func main() {
	repo := flag.String("repo", ".", "path to the hermes-agent checkout (read-only)")
	out := flag.String("out", "", "if set, write the discovered IR JSON here and exit")
	flag.Parse()

	res, err := discovery.Run(discovery.Options{
		Repo:      *repo,
		CommitSHA: "de5ece994415276d215976836161f871f1d6d8f5",
		RepoURL:   "https://github.com/nousresearch/hermes-agent",
		Frontends: []discovery.LanguageFrontend{discovery.NewPythonFrontend()},
	})
	if err != nil {
		log.Fatalf("discovery: %v", err)
	}
	ir := &res.IR

	if *out != "" {
		b, err := discovery.MarshalIR(*ir)
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(*out, b, 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote IR (%d nodes) to %s\n", len(ir.Nodes), *out)
		return
	}

	fmt.Printf("═══ P5 on hermes-agent ═══\n")
	fmt.Printf("language=%s  nodes=%d  edges=%d\n\n", ir.Workflow.Language, len(ir.Nodes), len(ir.Edges))
	if len(ir.Nodes) == 0 {
		fmt.Println("no LLM call sites discovered in this scope.")
		return
	}

	// ── §1 Typed contracts: validate the discovered ordering ──────────────────
	order := make([]string, len(ir.Nodes))
	for i, n := range ir.Nodes {
		order[i] = n.NodeID
	}
	dataEdges := 0
	edges := make([]typedcontract.Edge, 0, len(ir.Edges))
	for _, e := range ir.Edges {
		edges = append(edges, typedcontract.Edge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind})
		if e.Kind == "data" {
			dataEdges++
		}
	}
	ordering := typedcontract.Ordering{Order: order, Edges: edges}
	v := typedcontract.ValidateOrdering(ir, ordering, typedcontract.DefaultCatalog())
	fmt.Printf("§1 ValidateOrdering(discovered): %s   (data edges=%d)\n", v.Kind, dataEdges)

	// ── §1 Re-arrangement: reverse the order, re-validate ─────────────────────
	if len(order) >= 2 {
		rev := make([]string, len(order))
		for i := range order {
			rev[i] = order[len(order)-1-i]
		}
		rv := typedcontract.ValidateOrdering(ir, typedcontract.Ordering{Order: rev, Edges: edges}, typedcontract.DefaultCatalog())
		fmt.Printf("   ValidateOrdering(reversed):   %s", rv.Kind)
		if rv.Kind == typedcontract.VerdictRejected && len(rv.Diagnostics) > 0 {
			fmt.Printf("  ← e.g. %s→%s %v", rv.Diagnostics[0].FromNodeID[:min(12, len(rv.Diagnostics[0].FromNodeID))],
				rv.Diagnostics[0].ToNodeID[:min(12, len(rv.Diagnostics[0].ToNodeID))], rv.Diagnostics[0].Fields)
		}
		fmt.Println()
	}

	// ── §8.3 Schema refinement: which nodes are still permissive ──────────────
	_, rep := irwriteback.RefineSchemas(ir, nil)
	fmt.Printf("\n§8.3 permissive io_contract nodes (coherence unverified until refined): %d/%d\n",
		len(rep.StillPermissive), len(ir.Nodes))
	fmt.Printf("     → every discovered edge is trivially 'coherent' against a permissive schema; P5\n")
	fmt.Printf("       surfaces this rather than pretending the reorder is proven safe.\n")

	// ── §4/§5 Dynamic tracing + reconciliation on a simulated run ─────────────
	// We do NOT run hermes-agent; we synthesise a trace over its discovered node ids to exercise the
	// interceptor + reconciler end to end on the real graph shape.
	blobs, sink := newMemBlobs(), &memSink{}
	interceptor := dynamictracing.New(blobs, sink)
	traceNodes := order
	if len(traceNodes) > 8 {
		traceNodes = traceNodes[:8]
	}
	tags := dynamictracing.Tags{VariantID: "hermes-v1", RunID: "hermes-run-1", ConfigHash: "cfg", Seed: 1}
	for idx, n := range traceNodes {
		t := tags
		t.NodeID = n
		interceptor.Observe(context.Background(), t, dynamictracing.LLMCall{
			Provider: "openrouter", ModelID: "hermes-3", Inputs: []byte(`{"messages":[]}`), InvocationIndex: 0})
		_ = idx
	}
	// Simulate a loop: the first node fires 3 more times (a reflection-style self-loop).
	for k := 1; k <= 3; k++ {
		t := tags
		t.NodeID = traceNodes[0]
		interceptor.Observe(context.Background(), t, dynamictracing.LLMCall{
			Provider: "openrouter", ModelID: "hermes-3", Inputs: []byte(`{"messages":[]}`), InvocationIndex: k})
	}
	interceptor.Flush()
	obs, _, providerCalls := interceptor.Stats()
	fmt.Printf("\n§4 interceptor: observed=%d calls, added provider calls=%d (passive), inputs content-hashed+redacted\n", obs, providerCalls)

	recRep := reconcile.Reconcile(ir, sink.all())
	confirmed, unconfirmed := 0, 0
	for _, n := range recRep.Nodes {
		switch n.Status {
		case reconcile.StatusConfirmed:
			confirmed++
		case reconcile.StatusUnconfirmed:
			unconfirmed++
		}
	}
	fmt.Printf("§5 reconcile: confirmed=%d unconfirmed=%d runtime-only-edges=%d  report_hash=%s\n",
		confirmed, unconfirmed, len(recRep.RuntimeOnlyEdges()), recRep.ContentHash[:12])
	loopNode := traceNodes[0]
	fmt.Printf("   loop node %q: 1 definition ↔ %d invocations (not %d nodes)\n",
		shortID(loopNode), len(recRep.Invocations[loopNode]), len(recRep.Invocations[loopNode]))

	// ── §6 Behavioral confirmation on the simulated loop ──────────────────────
	// Treat the looping node as a Reflection structural candidate and confirm it from the trace.
	cand := patternclassifier.Label{
		Pattern: patternclassifier.Reflection, Confidence: 0.5, Source: patternclassifier.SourceRule,
		SubgraphRef: loopNode, DetectorID: "structural", TaxonomyVersion: patternclassifier.TaxonomyVersion, Candidate: true,
	}
	// Give the loop a self-edge in the reconciled view so the rule can fire.
	recRep.Edges = append(recRep.Edges, reconcile.ReconciledEdge{FromNodeID: loopNode, ToNodeID: loopNode, Origin: reconcile.OriginRuntimeOnly})
	bres := behavioral.Confirm(ir, []patternclassifier.Label{cand}, behavioral.Evidence{
		Report:  recRep,
		Quality: map[string][]float64{loopNode: {0.6, 0.6, 0.6, 0.6}}, // never improves → anti-pattern
	})
	fmt.Printf("\n§6 behavioral: confirmed labels=%d", len(bres.Confirmed))
	if len(bres.Confirmed) > 0 {
		ms := bres.MetricSets[loopNode]
		sort.Strings(ms.Metrics)
		fmt.Printf(" (Reflection → metrics %v)", ms.Metrics)
	}
	fmt.Printf("  anti-patterns=%d", len(bres.AntiPatterns))
	if len(bres.AntiPatterns) > 0 {
		fmt.Printf(" [%s]", bres.AntiPatterns[0].Kind)
	}
	fmt.Println()
	fmt.Printf("\n✓ P5 pipeline ran end-to-end on the real hermes-agent graph (parsed, never executed).\n")
}

func shortID(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ── in-memory trace sinks ──────────────────────────────────────────────────
type memBlobs struct{ data map[string][]byte }

func newMemBlobs() *memBlobs { return &memBlobs{data: map[string][]byte{}} }

func (m *memBlobs) Put(_ context.Context, b []byte) (string, error) {
	h := fmt.Sprintf("%x", len(b)) + fmt.Sprintf("%p", &b)
	m.data[h] = b
	return h, nil
}

type memSink struct{ calls []dynamictracing.TracedCall }

func (s *memSink) Record(_ context.Context, c dynamictracing.TracedCall) error {
	s.calls = append(s.calls, c)
	return nil
}
func (s *memSink) all() []dynamictracing.TracedCall { return s.calls }
