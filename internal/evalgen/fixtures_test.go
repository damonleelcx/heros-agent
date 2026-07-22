package evalgen

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// fxRouterIR is the task 8.1 fixture: Routing -> per-branch Tool Use -> Reflection (a self-edge
// loop). The router chooses a branch from the input's "route" field, which is what makes the
// simulating prober BELOW a real router rather than a coin flip.
func fxRouterIR() *discovery.IR {
	node := func(id string, p patternclassifier.Pattern) discovery.IRNode {
		return discovery.IRNode{
			NodeID: id,
			Kind:   "static_definition",
			PatternLabels: []discovery.IRPatternLabel{{
				Pattern:         string(p),
				Confidence:      patternclassifier.ConfidenceTopologyDetermined,
				Source:          string(patternclassifier.SourceRule),
				TaxonomyVersion: patternclassifier.TaxonomyVersion,
			}},
			IOContract: discovery.IRIOContract{
				InputSchema: map[string]any{
					"type":     "object",
					"required": []any{"route", "q"},
					"properties": map[string]any{
						"route": map[string]any{"type": "string"},
						"q":     map[string]any{"type": "string"},
						"turns": map[string]any{"type": "integer"},
					},
				},
				OutputSchema: map[string]any{
					"type":       "object",
					"required":   []any{"a"},
					"properties": map[string]any{"a": map[string]any{"type": "string"}},
				},
			},
		}
	}
	return &discovery.IR{
		IRVersion: discovery.IRVersionPatternLabels,
		Workflow:  discovery.IRWorkflow{ID: "wf-router", Language: "python"},
		Nodes: []discovery.IRNode{
			node("router", patternclassifier.Routing),
			node("branch_a", patternclassifier.ToolUse),
			node("branch_b", patternclassifier.ToolUse),
			node("reflect", patternclassifier.Reflection),
		},
		Edges: []discovery.IREdge{
			{FromNodeID: "router", ToNodeID: "branch_a", Kind: "control"},
			{FromNodeID: "router", ToNodeID: "branch_b", Kind: "control"},
			{FromNodeID: "branch_a", ToNodeID: "reflect", Kind: "data"},
			{FromNodeID: "branch_b", ToNodeID: "reflect", Kind: "data"},
			{FromNodeID: "reflect", ToNodeID: "reflect", Kind: "control"},
		},
	}
}

// fxUnreachableIR adds a branch outcome NO input can select: the router only ever routes to a or b,
// and `branch_dead` is reachable in the graph but not in behaviour. This is the fixture for "the
// loop terminates at the bound and reports the residual instead of a false 100%".
func fxUnreachableIR() *discovery.IR {
	ir := fxRouterIR()
	ir.Nodes = append(ir.Nodes, discovery.IRNode{
		NodeID: "branch_dead",
		Kind:   "static_definition",
		IOContract: discovery.IRIOContract{
			InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
			OutputSchema: map[string]any{"type": "object"},
		},
	})
	ir.Edges = append(ir.Edges, discovery.IREdge{FromNodeID: "router", ToNodeID: "branch_dead", Kind: "control"})
	return ir
}

// simProber is a REAL execution simulator, not a stub that hands back the answer: it interprets the
// case input the way the fixture workflow would, produces telemetry spans, and coverage is then
// derived from those spans by the production TraceEvidence path. Nothing in the test asserts
// coverage directly from a case's declared PathTags.
type simProber struct {
	// reachable limits which routes the workflow can actually take, modelling a router whose
	// condition no input satisfies for the excluded branches.
	reachable map[string]bool
	probes    int
}

func newSimProber(routes ...string) *simProber {
	m := map[string]bool{}
	for _, r := range routes {
		m[r] = true
	}
	return &simProber{reachable: m}
}

func (p *simProber) Probe(_ context.Context, ir *discovery.IR, c evalharness.Case) (Evidence, error) {
	p.probes++
	var in map[string]any
	_ = json.Unmarshal(c.Input, &in)

	route, _ := in["route"].(string)
	if !p.reachable[route] {
		route = "branch_a" // the router's default: an unrecognised route never selects a dead branch
	}
	turns := 1
	switch t := in["turns"].(type) {
	case float64:
		turns = int(t)
	case int:
		turns = t
	}
	if turns < 1 {
		turns = 1
	}
	if turns > 8 {
		turns = 8
	}

	b := newTraceBuilder(c.CaseID)
	b.node("router")
	b.node(route)
	for i := 0; i < turns; i++ {
		b.node("reflect")
	}
	return TraceEvidence(ir, c.CaseID, b.build()), nil
}

// traceBuilder emits node spans in execution order, exactly as the P2.5 instrument would.
type traceBuilder struct {
	runID string
	spans []telemetry.Span
}

func newTraceBuilder(runID string) *traceBuilder { return &traceBuilder{runID: runID} }

func (b *traceBuilder) node(nodeID string) {
	b.spans = append(b.spans, telemetry.Span{
		TraceID:    telemetry.TraceID(b.runID),
		SpanID:     telemetry.NodeSpanID(b.runID + ":" + nodeID + ":" + itoa(len(b.spans))),
		Kind:       telemetry.SpanKindNode,
		Name:       "chat " + nodeID,
		Status:     telemetry.SpanStatusOK,
		Attributes: map[string]any{telemetry.AttrNodeID: nodeID},
	})
}

func (b *traceBuilder) build() telemetry.Trace {
	return telemetry.Trace{Run: telemetry.RunContext{RunID: b.runID}, Spans: b.spans}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

// scriptedCaseModel is the LLM-driven layer's model seam. It answers the TARGETS it is given, so a
// test can assert the layer was pointed at the residual rather than sprayed at the whole space.
type scriptedCaseModel struct {
	// answerable are the targets this model can actually produce a case for. A target outside this
	// set gets no case — the model honestly cannot reach it.
	answerable map[string]bool
	requests   []CaseRequest
}

func (m *scriptedCaseModel) GenerateCases(_ context.Context, req CaseRequest) ([]GeneratedCase, error) {
	m.requests = append(m.requests, req)
	var out []GeneratedCase
	for _, target := range req.Targets {
		if !m.answerable[target] {
			continue
		}
		route := ""
		if _, to, ok := ParseEdgeID(target); ok {
			route = to
		}
		if _, outcome, ok := ParseBranchID(target); ok {
			route = outcome
		}
		turns := 1
		if node, bound, ok := ParseLoopBoundID(target); ok && node == "reflect" {
			turns = DefaultLoopBounds[bound]
			route = "branch_a"
		}
		if route == "" {
			route = "branch_a"
		}
		input, _ := json.Marshal(map[string]any{
			"route": route,
			"q":     "targeted case for " + target,
			"turns": turns,
		})
		out = append(out, GeneratedCase{Input: input, Targets: []string{target}})
	}
	for _, k := range req.EdgeCases {
		input, _ := json.Marshal(map[string]any{
			"route": "branch_a",
			"q":     "edge case " + string(k),
			"turns": 1,
		})
		out = append(out, GeneratedCase{Input: input, EdgeCase: k, Targets: []string{string(k)}})
	}
	return out, nil
}

func handCase(id, route, q string, turns int) evalharness.Case {
	input, _ := json.Marshal(map[string]any{"route": route, "q": q, "turns": turns})
	return evalharness.Case{
		CaseID:     id,
		WorkflowID: "wf-router",
		Suite:      "hand",
		Input:      input,
		Label:      evalharness.LabelNone,
		Origin:     evalharness.OriginHandAuthored,
	}
}

func containsAll(hay []string, needles ...string) bool {
	set := map[string]bool{}
	for _, h := range hay {
		set[h] = true
	}
	for _, n := range needles {
		if !set[n] {
			return false
		}
	}
	return true
}

func joined(ss []string) string { return strings.Join(ss, ", ") }
