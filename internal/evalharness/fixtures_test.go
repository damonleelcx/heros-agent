package evalharness

import (
	"context"
	"encoding/json"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// fixtures_test.go builds traces and IRs as Go constructor funcs (the convention
// internal/patternclassifier/fixtures_test.go established): a fixture that is code can be read
// beside the assertion it feeds, and cannot drift out of sync with the types it populates.

var fixedNow = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

// traceBuilder assembles a P4 Trace span by span.
type traceBuilder struct {
	rc    telemetry.RunContext
	spans []telemetry.Span
	tr    Trace
	t0    time.Time
}

func newTrace(runID string) *traceBuilder {
	rc := telemetry.RunContext{
		VariantID:  "v-a",
		RunID:      runID,
		ConfigHash: "aa" + repeat("0", 62),
		Seed:       0,
		CaseID:     "case-1",
	}
	return &traceBuilder{rc: rc, t0: fixedNow, tr: Trace{
		NodeOutputs: map[string]json.RawMessage{},
		NodeInputs:  map[string]json.RawMessage{},
	}}
}

func repeat(s string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, s[0])
	}
	return string(out)
}

// node adds a node span with cost, latency, tokens and a pass/fail status.
func (b *traceBuilder) node(nodeID string, costUSD, latencyMS float64, inTok, outTok int, failed bool) *traceBuilder {
	status := telemetry.SpanStatusOK
	if failed {
		status = telemetry.SpanStatusError
	}
	start := b.t0
	b.t0 = b.t0.Add(time.Duration(latencyMS) * time.Millisecond)
	b.spans = append(b.spans, telemetry.Span{
		TraceID:      telemetry.TraceID(b.rc.RunID),
		SpanID:       telemetry.NodeSpanID(b.rc.RunID + ":" + nodeID),
		ParentSpanID: telemetry.RunSpanID(b.rc.RunID),
		Name:         "chat " + nodeID,
		Kind:         telemetry.SpanKindNode,
		StartTime:    start,
		EndTime:      b.t0,
		Status:       status,
		Attributes: map[string]any{
			telemetry.AttrNodeID:           nodeID,
			telemetry.AttrRunID:            b.rc.RunID,
			telemetry.AttrVariantID:        b.rc.VariantID,
			telemetry.AttrConfigHash:       b.rc.ConfigHash,
			telemetry.AttrCaseID:           b.rc.CaseID,
			telemetry.AttrSeed:             b.rc.Seed,
			telemetry.AttrCostUSD:          costUSD,
			telemetry.AttrLatencyMS:        latencyMS,
			telemetry.AttrNodeFailed:       failed,
			telemetry.AttrGenAIUsageInput:  inTok,
			telemetry.AttrGenAIUsageOutput: outTok,
		},
	})
	return b
}

// tool adds a tool-call child span under a node.
func (b *traceBuilder) tool(nodeID, toolName string, ok bool) *traceBuilder {
	status := telemetry.SpanStatusOK
	if !ok {
		status = telemetry.SpanStatusError
	}
	b.spans = append(b.spans, telemetry.Span{
		TraceID:   telemetry.TraceID(b.rc.RunID),
		SpanID:    telemetry.ToolSpanID(b.rc.RunID+":"+nodeID, toolName, 0),
		Kind:      telemetry.SpanKindTool,
		Name:      toolName,
		StartTime: b.t0,
		EndTime:   b.t0.Add(5 * time.Millisecond),
		Status:    status,
		Attributes: map[string]any{
			telemetry.AttrNodeID:   nodeID,
			telemetry.AttrToolName: toolName,
		},
	})
	return b
}

// output sets the run's end-to-end output.
func (b *traceBuilder) output(raw string) *traceBuilder {
	b.tr.Output = json.RawMessage(raw)
	return b
}

// nodeOutput sets one node's output payload.
func (b *traceBuilder) nodeOutput(nodeID, raw string) *traceBuilder {
	b.tr.NodeOutputs[nodeID] = json.RawMessage(raw)
	return b
}

func (b *traceBuilder) failed(reason string) *traceBuilder {
	b.tr.Failed = true
	b.tr.FailureReason = reason
	return b
}

func (b *traceBuilder) build() Trace {
	b.tr.Trace = telemetry.Trace{Run: b.rc, Spans: b.spans}
	return b.tr
}

// ─────────────────────────────────────────────────────────────────────────────
// IR fixture: Routing -> per-branch Tool Use -> Reflection (the task 8.1 fixture).
// ─────────────────────────────────────────────────────────────────────────────

func fxMultiPatternIR() *discovery.IR {
	label := func(p patternclassifier.Pattern, ref string) discovery.IRPatternLabel {
		return discovery.IRPatternLabel{
			Pattern:         string(p),
			Confidence:      patternclassifier.ConfidenceTopologyDetermined,
			Source:          string(patternclassifier.SourceRule),
			SubgraphRef:     ref,
			DetectorID:      "fixture",
			TaxonomyVersion: patternclassifier.TaxonomyVersion,
		}
	}
	node := func(id string, labels ...discovery.IRPatternLabel) discovery.IRNode {
		return discovery.IRNode{
			NodeID:        id,
			Kind:          "static_definition",
			PatternLabels: labels,
			IOContract: discovery.IRIOContract{
				InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}, "required": []any{"q"}},
				OutputSchema: map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "required": []any{"a"}},
			},
		}
	}
	return &discovery.IR{
		IRVersion: discovery.IRVersionPatternLabels,
		Workflow:  discovery.IRWorkflow{ID: "wf-multipattern", Language: "python"},
		Nodes: []discovery.IRNode{
			node("router", label(patternclassifier.Routing, "sg-route")),
			node("branch_a", label(patternclassifier.ToolUse, "sg-tool-a")),
			node("branch_b", label(patternclassifier.RetrievalRAG, "sg-rag-b")),
			node("reflect", label(patternclassifier.Reflection, "sg-reflect")),
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

// ─────────────────────────────────────────────────────────────────────────────
// Stub judge model. Deterministic on purpose: a judge stub that varied per call would make every
// statistical assertion in this package flaky for reasons unrelated to the statistics.
// ─────────────────────────────────────────────────────────────────────────────

type stubJudge struct {
	score float64
	err   error
	calls int
	// omitScore reproduces a model that answers without a score — the contract violation the
	// pointer-typed RawVerdict.Score exists to make representable.
	omitScore bool
}

func (s *stubJudge) Judge(_ context.Context, _ JudgeRequest) (RawVerdict, error) {
	s.calls++
	if s.err != nil {
		return RawVerdict{}, s.err
	}
	if s.omitScore {
		return RawVerdict{Rationale: "no score"}, nil
	}
	v := s.score
	return RawVerdict{Score: &v, Rationale: "stub"}, nil
}

func calibratedStanding(metric string) JudgeStanding {
	return JudgeStanding{Metric: metric, Agreement: 0.82, PercentAgreement: 0.9, NHuman: 40, Floor: 0.6, Calibrated: true}
}

func uncalibratedStanding(metric string) JudgeStanding {
	return JudgeStanding{Metric: metric, Floor: 0.6}
}
