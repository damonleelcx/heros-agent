package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalgen"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// realir.go loads a Workflow IR emitted by P1 discovery over an ACTUAL repository, classifies it
// with P3.5, and hands it to the board — so the thing being scored is a real workflow rather than a
// fixture.
//
// It exists because the fixture IR in main.go proves the measurement PATH and nothing about any real
// codebase. Running against a real repo immediately surfaced two facts a fixture never would: that
// P1 emits call sites with no edges (inter-node flow is P5), and that a coverage report over a
// zero-edge IR was reporting a vacuous 100%.

// loadRealIR reads an IR from disk and classifies it. It returns the IR, the classification result,
// and a human-readable account of what was actually found — which is the point: the account is
// usually more informative than the board.
func loadRealIR(ctx context.Context, path string) (*discovery.IR, patternclassifier.Result, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, patternclassifier.Result{}, "", err
	}
	var ir discovery.IR
	if err := json.Unmarshal(raw, &ir); err != nil {
		return nil, patternclassifier.Result{}, "", fmt.Errorf("parse IR %s: %w", path, err)
	}

	// The classifier needs a skill resolver to answer "does this tools_skills entry name a skill that
	// exists?". With no registry stood up, the honest answer for every name is NO — a resolver that
	// said yes would manufacture Tool Use labels out of unresolvable strings.
	res, err := patternclassifier.Classify(ctx, &ir, patternclassifier.Options{
		Skills: patternclassifier.NewStaticSkillResolver(),
	})
	if err != nil {
		return nil, patternclassifier.Result{}, "", fmt.Errorf("classify: %w", err)
	}

	labeled := map[string]int{}
	for _, l := range res.Labels {
		labeled[string(l.Pattern)]++
	}
	account := fmt.Sprintf(
		"workflow %q from %s@%.7s\n  %d nodes · %d edges · %d subgraphs\n  labels: %v · unclassified regions: %d · llm calls: %d",
		ir.Workflow.ID, ir.Workflow.Repo.URL, ir.Workflow.Repo.CommitSHA,
		len(ir.Nodes), len(ir.Edges), len(ir.Subgraphs), labeled, len(res.Residue), res.LLMCalls)

	if len(ir.Edges) == 0 {
		account += "\n  ⚠ ZERO EDGES: P1 static discovery finds call sites, not the flow between them.\n" +
			"    Path coverage is therefore NOT MEASURABLE on this IR — the board says so rather than\n" +
			"    reporting a vacuous 100%. Edges arrive with P5 dynamic tracing."
	}
	return &ir, res, account, nil
}

// writeBackLabels attaches the classifier's labels to the IR nodes, which is what makes the harness's
// pattern-driven metric selection dispatch off a REAL label rather than an asserted one.
func writeBackLabels(ir *discovery.IR, res patternclassifier.Result) {
	bySubgraph := map[string][]patternclassifier.Label{}
	for _, l := range res.Labels {
		bySubgraph[l.SubgraphRef] = append(bySubgraph[l.SubgraphRef], l)
	}
	for _, sg := range res.Subgraphs {
		labels := bySubgraph[sg.SubgraphID]
		if len(labels) == 0 {
			continue
		}
		irLabels := make([]discovery.IRPatternLabel, 0, len(labels))
		for _, l := range labels {
			irLabels = append(irLabels, discovery.IRPatternLabel{
				Pattern:         string(l.Pattern),
				Confidence:      l.Confidence,
				Source:          string(l.Source),
				SubgraphRef:     l.SubgraphRef,
				DetectorID:      l.DetectorID,
				LLMRunRef:       l.LLMRunRef,
				TaxonomyVersion: l.TaxonomyVersion,
				Candidate:       l.Candidate,
			})
		}
		members := map[string]bool{}
		for _, id := range sg.NodeIDs {
			members[id] = true
		}
		for i := range ir.Nodes {
			if members[ir.Nodes[i].NodeID] {
				ir.Nodes[i].PatternLabels = append(ir.Nodes[i].PatternLabels, irLabels...)
			}
		}
		ir.Subgraphs = append(ir.Subgraphs, discovery.IRSubgraph{
			SubgraphID:    sg.SubgraphID,
			NodeIDs:       sg.NodeIDs,
			PatternLabels: irLabels,
		})
	}
	ir.IRVersion = discovery.IRVersionPatternLabels
}

// ─────────────────────────────────────────────────────────────────────────────
// IR-driven runtime
//
// These replace the fixture's hardcoded topology when --ir is given. They are a SIMULATOR over the
// real IR, not the real workflow: the node ids, their order, their invocation semantics and their I/O
// contracts all come from what discovery actually found, but no line of the target repository runs.
// That distinction is the whole point of this file — it is the difference between "measured the real
// structure" and "measured the real system", and only the first is on offer here.
// ─────────────────────────────────────────────────────────────────────────────

// nodeTargetKey is the input field naming the node a case exercises. It is explicit because on an
// edge-less IR there is no flow to infer an order from: discovery found 40 call sites and nothing
// about how control reaches them, so a case names its target rather than pretending to route.
const nodeTargetKey = "__node__"

// irWalk returns the nodes a case executes on a real IR.
//
// With edges present it would follow them. With NONE — which is what P1's static pass yields — a case
// exercises exactly the node it names, repeated for its invocation semantics. Inventing an order over
// disconnected call sites would be fabricating the very flow P5 exists to observe.
func irWalk(ir *discovery.IR) func(evalharness.Case) []string {
	semantics := map[string]string{}
	for _, n := range ir.Nodes {
		semantics[n.NodeID] = n.InvocationSemantics.Type
	}
	return func(c evalharness.Case) []string {
		var in map[string]any
		_ = json.Unmarshal(c.Input, &in)
		node, _ := in[nodeTargetKey].(string)
		if node == "" {
			return nil
		}
		turns := 1
		if t, ok := in["turns"].(float64); ok && int(t) > 0 {
			turns = int(t)
		}
		// A call site discovery marked `loop` really does execute more than once; honouring that is
		// what makes the loop-bound obligations mean anything.
		if semantics[node] != "loop" {
			turns = 1
		}
		out := make([]string, 0, turns)
		for i := 0; i < turns; i++ {
			out = append(out, node)
		}
		return out
	}
}

// irProber derives coverage evidence by running irWalk through the production TraceEvidence path.
type irProber struct {
	walk func(evalharness.Case) []string
}

func (p irProber) Probe(_ context.Context, ir *discovery.IR, c evalharness.Case) (evalgen.Evidence, error) {
	var spans []telemetry.Span
	for i, node := range p.walk(c) {
		spans = append(spans, telemetry.Span{
			TraceID:    telemetry.TraceID(c.CaseID),
			SpanID:     telemetry.NodeSpanID(c.CaseID + node + fmt.Sprint(i)),
			Kind:       telemetry.SpanKindNode,
			Status:     telemetry.SpanStatusOK,
			Attributes: map[string]any{telemetry.AttrNodeID: node},
		})
	}
	return evalgen.TraceEvidence(ir, c.CaseID, telemetry.Trace{Spans: spans}), nil
}

// irSeedCases is the hand-authored starting set for a real IR: one case per node for the first few
// nodes, leaving the rest as a genuine coverage gap for the loop to close.
func irSeedCases(ir *discovery.IR, n int) []evalharness.Case {
	out := make([]evalharness.Case, 0, n)
	for i, node := range ir.Nodes {
		if i >= n {
			break
		}
		in, _ := json.Marshal(map[string]any{nodeTargetKey: node.NodeID, "turns": 1})
		out = append(out, evalharness.Case{
			CaseID:     "hand-" + node.NodeID,
			WorkflowID: ir.Workflow.ID,
			Suite:      "hand",
			Input:      in,
			Label:      evalharness.LabelNone,
			Origin:     evalharness.OriginHandAuthored,
			PathTags:   []string{node.NodeID},
		})
	}
	return out
}

// irCaseGenerator answers the coverage gap for a real IR: it emits a case for each uncovered node
// obligation the report names. It writes NO reference — nothing here knows what the right answer is
// for a node in someone else's repository, and inventing one would be the weak-reference failure with
// extra steps.
type irCaseGenerator struct{ ir *discovery.IR }

func (g irCaseGenerator) GenerateCases(_ context.Context, req evalgen.CaseRequest) ([]evalgen.GeneratedCase, error) {
	known := map[string]bool{}
	for _, n := range g.ir.Nodes {
		known[n.NodeID] = true
	}
	var out []evalgen.GeneratedCase
	for _, target := range req.Targets {
		node := target
		if n, _, ok := evalgen.ParseLoopBoundID(target); ok {
			node = n
		}
		if !known[node] {
			continue // an obligation this generator honestly cannot discharge
		}
		turns := 1
		if _, bound, ok := evalgen.ParseLoopBoundID(target); ok {
			turns = evalgen.DefaultLoopBounds[bound]
		}
		in, _ := json.Marshal(map[string]any{nodeTargetKey: node, "turns": turns})
		out = append(out, evalgen.GeneratedCase{Input: in, Targets: []string{target}})
	}
	for _, k := range req.EdgeCases {
		// An edge-case probe still has to enter a node to probe anything.
		if len(g.ir.Nodes) == 0 {
			break
		}
		in, _ := json.Marshal(map[string]any{
			nodeTargetKey: g.ir.Nodes[0].NodeID, "turns": 1, "edge_case": string(k)})
		out = append(out, evalgen.GeneratedCase{Input: in, EdgeCase: k, Targets: []string{string(k)}})
	}
	return out, nil
}
