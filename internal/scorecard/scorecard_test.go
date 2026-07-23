package scorecard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/reportstore"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

func scIR() *discovery.IR {
	contract := map[string]any{"type": "object", "required": []any{"a"},
		"properties": map[string]any{"a": map[string]any{"type": "string"}}}
	node := func(id string, p patternclassifier.Pattern) discovery.IRNode {
		return discovery.IRNode{NodeID: id, Kind: "static_definition",
			PatternLabels: []discovery.IRPatternLabel{{Pattern: string(p),
				Confidence: patternclassifier.ConfidenceTopologyDetermined, Source: string(patternclassifier.SourceRule),
				DetectorID: "sc", TaxonomyVersion: patternclassifier.TaxonomyVersion}},
			IOContract: discovery.IRIOContract{InputSchema: map[string]any{"type": "object"}, OutputSchema: contract}}
	}
	return &discovery.IR{IRVersion: discovery.IRVersionPatternLabels,
		Workflow: discovery.IRWorkflow{ID: "wf-sc"},
		Nodes:    []discovery.IRNode{node("router", patternclassifier.Routing), node("node3", patternclassifier.ToolUse), node("reflect", patternclassifier.Reflection)}}
}

func scSpan(caseID, node string, i int, cost, lat float64) telemetry.Span {
	base := time.Unix(1_700_000_000, 0).Add(time.Duration(i) * time.Second)
	return telemetry.Span{TraceID: telemetry.TraceID(caseID), SpanID: telemetry.NodeSpanID(caseID + node),
		Kind: telemetry.SpanKindNode, Name: node, StartTime: base, EndTime: base.Add(time.Duration(lat) * time.Millisecond),
		Status:     telemetry.SpanStatusOK,
		Attributes: map[string]any{telemetry.AttrNodeID: node, telemetry.AttrCostUSD: cost, telemetry.AttrLatencyMS: lat, telemetry.AttrNodeFailed: false}}
}

func scFaulty(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{
		"router": json.RawMessage(`{"a":"x"}`), "node3": json.RawMessage(`{"junk":1}`), "reflect": json.RawMessage(`{"a":"w"}`)}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		scSpan(id, "router", 0, 0.001, 50), scSpan(id, "node3", 1, 0.100, 100), scSpan(id, "reflect", 2, 0.002, 800)}}
	tr.Output = json.RawMessage(`{"a":"w"}`)
	tr.Failed = true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-sc"}, Trace: tr}
}

func scVariant() attribution.Variant {
	return attribution.Variant{VariantID: "v-sc", ConfigHash: "cfg", EvalSetHash: "es", WorkflowID: "wf-sc"}
}

// The full engine run produces a ready scorecard with per-node breakdown, clusters, diagnosis cards
// with evidence, and bottleneck flags — and is read-only.
func TestEngine_GenerateReadOnlyScorecard(t *testing.T) {
	eng := &Engine{Store: reportstore.NewMemStore()}
	view, err := eng.Generate(context.Background(), GenerateInput{
		IR:           scIR(),
		Variant:      scVariant(),
		FailingCases: []attribution.FailingCase{scFaulty("c1"), scFaulty("c2"), scFaulty("c3")},
		Overall:      OverallMetrics{TaskSuccess: 0.4, NCases: 10, NFailing: 3, CostUSD: 0.3, LatencyMS: 950},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if view.State != StateReady {
		t.Fatalf("state = %q, want ready", view.State)
	}
	if !view.ReadOnly {
		t.Error("scorecard must be read-only")
	}
	if len(view.Nodes) == 0 {
		t.Error("per-node breakdown empty")
	}
	// node3 must be the top first-divergence hotspot.
	if view.Nodes[0].NodeID != "node3" {
		t.Errorf("top node = %q, want node3", view.Nodes[0].NodeID)
	}
	// Every diagnosis card carries evidence — never a bare label.
	if len(view.Diagnoses) == 0 {
		t.Fatal("no diagnosis cards")
	}
	for _, d := range view.Diagnoses {
		if len(d.EvidenceCaseIDs) == 0 {
			t.Errorf("diagnosis %s has no evidence", d.Code)
		}
		if d.Description == "" {
			t.Errorf("diagnosis %s has no description (bare label)", d.Code)
		}
	}
	// Bottleneck flags landed on the per-node rows: node3 dominates cost, reflect dominates latency.
	dims := map[string][]attribution.Dimension{}
	for _, n := range view.Nodes {
		dims[n.NodeID] = n.BottleneckDimensions
	}
	if !hasDim(dims["node3"], attribution.DimCost) {
		t.Errorf("node3 should carry a cost bottleneck flag; got %v", dims["node3"])
	}
	if !hasDim(dims["reflect"], attribution.DimLatency) {
		t.Errorf("reflect should carry a latency bottleneck flag; got %v", dims["reflect"])
	}
}

// Empty state: a variant with no failing cases renders `empty`, not a blank ready scorecard.
func TestEngine_EmptyState(t *testing.T) {
	eng := &Engine{Store: reportstore.NewMemStore()}
	view, err := eng.Generate(context.Background(), GenerateInput{
		IR: scIR(), Variant: scVariant(), FailingCases: nil,
		Overall: OverallMetrics{TaskSuccess: 1.0, NCases: 10, NFailing: 0},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if view.State != StateEmpty {
		t.Fatalf("state = %q, want empty", view.State)
	}
	if view.Message == "" {
		t.Error("empty state should carry a message")
	}
}

// Partial state: ablation candidates exist but no runner → the scorecard is partial, not silently
// complete.
func TestEngine_PartialWhenAblationPending(t *testing.T) {
	eng := &Engine{Store: reportstore.NewMemStore()} // no Runner
	view, err := eng.Generate(context.Background(), GenerateInput{
		IR: scIR(), Variant: scVariant(),
		FailingCases: []attribution.FailingCase{scFaulty("c1")},
		Overall:      OverallMetrics{NFailing: 1, NCases: 5},
		AblationTopN: 2, // request ablation but supply no runner
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !view.Partial {
		t.Error("scorecard should be partial when ablation is requested but not yet run")
	}
}

// Uncalibrated analyst surfaces on the view and is flagged.
func TestBuild_UncalibratedAnalystFlagged(t *testing.T) {
	view := Build(Input{
		Variant: scVariant(),
		Overall: OverallMetrics{NFailing: 1, NCases: 5},
		Diagnoses: []diagnosis.Diagnosis{{
			DiagID: "d1", NodeID: "reflect", TaxonomyCode: diagnosis.CauseNonConvergence,
			Source: diagnosis.SourceAnalyst, Confidence: 0.7, EvidenceCaseIDs: []string{"c1"},
			Agreement: 0.4, NHuman: 10, Calibrated: true, AnalystFlagged: true,
		}},
		Analyst: diagnosis.AnalystCalibration{AnalystMetric: "a", Agreement: 0.4, NHuman: 10, Calibrated: true, Floor: 0.6},
	})
	if !view.AnalystUncalibrated {
		t.Error("below-floor analyst should set AnalystUncalibrated")
	}
	if !view.Analyst.Present || !view.Analyst.Flagged {
		t.Errorf("analyst status should be present and flagged; got %+v", view.Analyst)
	}
}

// Inconclusive flag: an inconclusive ablation sets HasInconclusive.
func TestBuild_InconclusiveFlag(t *testing.T) {
	view := Build(Input{
		Variant: scVariant(), Overall: OverallMetrics{NFailing: 1},
		Ablations: []attribution.AblationResult{{NodeID: "router", Verdict: attribution.VerdictInconclusive, CILow: -0.1, CIHigh: 0.1}},
	})
	if !view.HasInconclusive {
		t.Error("inconclusive ablation should set HasInconclusive")
	}
}

// The view carries no apply affordance — a JSON dump must contain no apply/mutate field.
func TestView_NoApplyAffordance(t *testing.T) {
	view := Build(Input{Variant: scVariant(), Overall: OverallMetrics{NFailing: 1},
		Diagnoses: []diagnosis.Diagnosis{{DiagID: "d", NodeID: "n", TaxonomyCode: diagnosis.CausePromptFormatDrift,
			Source: diagnosis.SourceRule, Confidence: 1, EvidenceCaseIDs: []string{"c1"}}}})
	raw, _ := json.Marshal(view)
	for _, forbidden := range []string{"apply", "mutate", "proposal", "change_config", "patch"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Errorf("scorecard view leaks a mutation affordance: %q", forbidden)
		}
	}
}

func hasDim(dims []attribution.Dimension, d attribution.Dimension) bool {
	for _, x := range dims {
		if x == d {
			return true
		}
	}
	return false
}
