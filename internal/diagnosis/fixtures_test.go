package diagnosis

import (
	"encoding/json"
	"time"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// fixtures_test.go builds the multi-pattern workflow (Routing → Tool Use → Reflection) and the
// per-fault traces the diagnosis tests drive. Every span is emitted the way the P2.5 instrument
// would, so the detectors are exercised over real trace shapes, not a bespoke stub.

func patNode(id string, p patternclassifier.Pattern, outSchema map[string]any) discovery.IRNode {
	return discovery.IRNode{
		NodeID: id, Kind: "static_definition",
		PatternLabels: []discovery.IRPatternLabel{{
			Pattern:         string(p),
			Confidence:      patternclassifier.ConfidenceTopologyDetermined,
			Source:          string(patternclassifier.SourceRule),
			DetectorID:      "fixture",
			TaxonomyVersion: patternclassifier.TaxonomyVersion,
		}},
		IOContract: discovery.IRIOContract{
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: outSchema,
		},
	}
}

// diagIR is the fixture: router (Routing) → node3 (Tool Use, output-contracted) → reflect (Reflection).
func diagIR() *discovery.IR {
	contract := map[string]any{
		"type": "object", "required": []any{"a"},
		"properties": map[string]any{"a": map[string]any{"type": "string"}},
	}
	return &discovery.IR{
		IRVersion: discovery.IRVersionPatternLabels,
		Workflow:  discovery.IRWorkflow{ID: "wf-diag", Language: "python"},
		Nodes: []discovery.IRNode{
			patNode("router", patternclassifier.Routing, contract),
			patNode("node3", patternclassifier.ToolUse, contract),
			patNode("reflect", patternclassifier.Reflection, contract),
		},
		Edges: []discovery.IREdge{
			{FromNodeID: "router", ToNodeID: "node3", Kind: "control"},
			{FromNodeID: "node3", ToNodeID: "reflect", Kind: "data"},
		},
	}
}

func span(caseID, node string, i int, attrs map[string]any, failed bool) telemetry.Span {
	base := time.Unix(1_700_000_000, 0).Add(time.Duration(i) * time.Second)
	status := telemetry.SpanStatusOK
	if failed {
		status = telemetry.SpanStatusError
	}
	a := map[string]any{telemetry.AttrNodeID: node, telemetry.AttrCostUSD: 0.002, telemetry.AttrLatencyMS: 120.0, telemetry.AttrNodeFailed: failed}
	for k, v := range attrs {
		a[k] = v
	}
	return telemetry.Span{
		TraceID: telemetry.TraceID(caseID), SpanID: telemetry.NodeSpanID(caseID + ":" + node),
		Kind: telemetry.SpanKindNode, Name: "chat " + node,
		StartTime: base, EndTime: base.Add(120 * time.Millisecond), Status: status, Attributes: a,
	}
}

func toolChild(caseID, node, tool, reason string, failed bool) telemetry.Span {
	base := time.Unix(1_700_000_100, 0)
	status := telemetry.SpanStatusOK
	if failed {
		status = telemetry.SpanStatusError
	}
	return telemetry.Span{
		TraceID: telemetry.TraceID(caseID), SpanID: telemetry.ToolSpanID(caseID+":"+node, tool, 0),
		Kind: telemetry.SpanKindTool, Name: tool, StartTime: base, EndTime: base.Add(30 * time.Millisecond),
		Status:     status,
		Attributes: map[string]any{telemetry.AttrNodeID: node, telemetry.AttrToolName: tool, telemetry.AttrToolReason: reason},
	}
}

// promptDriftCase: node3 drops its output contract (emits schema-invalid output).
func promptDriftCase(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{
		"router":  json.RawMessage(`{"a":"branch"}`),
		"node3":   json.RawMessage(`{"junk":"no a"}`),
		"reflect": json.RawMessage(`{"a":"wrong"}`),
	}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		span(id, "router", 0, nil, false),
		span(id, "node3", 1, nil, false),
		span(id, "reflect", 2, nil, false),
	}}
	tr.Output = json.RawMessage(`{"a":"wrong"}`)
	tr.Failed = true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-diag"}, Trace: tr}
}

// contextOverflowCase: node3's context was truncated (drop ratio > 0) before it ran.
func contextOverflowCase(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{
		"router":  json.RawMessage(`{"a":"branch"}`),
		"node3":   json.RawMessage(`{"a":"ok"}`), // schema-valid → no prompt-drift, isolates overflow
		"reflect": json.RawMessage(`{"a":"wrong"}`),
	}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		span(id, "router", 0, nil, false),
		span(id, "node3", 1, map[string]any{telemetry.MetricContextDropRatio: 0.4}, false),
		span(id, "reflect", 2, nil, false),
	}}
	tr.Output = json.RawMessage(`{"a":"wrong"}`)
	tr.Failed = true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-diag"}, Trace: tr}
}

// toolSchemaCase: node3's tool fails repeatedly with a schema mismatch.
func toolSchemaCase(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{
		"router": json.RawMessage(`{"a":"branch"}`),
		"node3":  json.RawMessage(`{"a":"ok"}`),
	}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		span(id, "router", 0, nil, false),
		span(id, "node3", 1, nil, false),
		toolChild(id, "node3", "search", "schema_mismatch", true),
	}}
	tr.Output = json.RawMessage(`{"a":"wrong"}`)
	tr.Failed = true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-diag"}, Trace: tr}
}

// unexplainedCase: nothing structural fired and no contract was violated — the fuzzy residue the
// analyst runs on.
func unexplainedCase(id string) attribution.FailingCase {
	// An IR with no output schema so there is no contract to violate.
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{"router": json.RawMessage(`{"a":"x"}`)}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		span(id, "router", 0, nil, false),
	}}
	tr.Output = json.RawMessage(`{"a":"wrong"}`)
	tr.Failed = true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-diag"}, Trace: tr}
}

// cleanButFailingCase: the end-to-end answer is wrong, but every node's output is schema-valid and no
// structural signal (overflow, tool, retrieval) fired — the fuzzy residue only the analyst can judge.
func cleanButFailingCase(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{
		"router":  json.RawMessage(`{"a":"branch"}`),
		"node3":   json.RawMessage(`{"a":"ok"}`),
		"reflect": json.RawMessage(`{"a":"plausible-but-wrong"}`),
	}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		span(id, "router", 0, nil, false),
		span(id, "node3", 1, nil, false),
		span(id, "reflect", 2, nil, false),
	}}
	tr.Output = json.RawMessage(`{"a":"plausible-but-wrong"}`)
	tr.Failed = true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-diag"}, Trace: tr}
}

// routingOnlyIR is a single Routing node with no output contract — used to test that a RAG failure
// mode is refused on a router.
func routingOnlyIR() *discovery.IR {
	return &discovery.IR{
		IRVersion: discovery.IRVersionPatternLabels,
		Workflow:  discovery.IRWorkflow{ID: "wf-route"},
		Nodes:     []discovery.IRNode{patNode("router", patternclassifier.Routing, nil)},
	}
}

// routingWithRetrievalSignal is a failing case whose router node carries a (nonsensical) zero-chunk
// retrieval signal — the detector must refuse to emit retrieval_miss on a Routing node.
func routingWithRetrievalSignal(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{"router": json.RawMessage(`{"a":"x"}`)}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		span(id, "router", 0, map[string]any{telemetry.MetricContextRetrievedChunks: 0.0}, false),
	}}
	tr.Output = json.RawMessage(`{"a":"wrong"}`)
	tr.Failed = true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-route"}, Trace: tr}
}

// routingResidueCase is a failing case whose only node is the router and that trips no rule.
func routingResidueCase(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{"router": json.RawMessage(`{"a":"x"}`)}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		span(id, "router", 0, nil, false),
	}}
	tr.Output = json.RawMessage(`{"a":"wrong"}`)
	tr.Failed = true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-route"}, Trace: tr}
}

// noSchemaIR is diagIR without output contracts — used so a residue case trips no rule.
func noSchemaIR() *discovery.IR {
	return &discovery.IR{
		IRVersion: discovery.IRVersionPatternLabels,
		Workflow:  discovery.IRWorkflow{ID: "wf-diag"},
		Nodes: []discovery.IRNode{
			patNode("router", patternclassifier.Routing, nil),
		},
	}
}
