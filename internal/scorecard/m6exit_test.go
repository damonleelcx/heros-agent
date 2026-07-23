package scorecard_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/attrengine"
	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/reportstore"
	"github.com/heros-foreal/agentd/internal/scorecard"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// m6exit_test.go is task 11.6: drive the WHOLE P4.5 path once and confirm the M6 exit checklist
// (PRD §13) is green, item by item. It is the integration counterpart to the per-package unit tests —
// one run of the real engines over the multi-pattern fixture, asserting every acceptance criterion.

// ── task 11.1 fixtures: the multi-pattern workflow with a known one-node fault ──

func m6IR() *discovery.IR {
	contract := map[string]any{"type": "object", "required": []any{"a"},
		"properties": map[string]any{"a": map[string]any{"type": "string"}}}
	node := func(id string, p patternclassifier.Pattern) discovery.IRNode {
		return discovery.IRNode{NodeID: id, Kind: "static_definition",
			PatternLabels: []discovery.IRPatternLabel{{Pattern: string(p),
				Confidence: patternclassifier.ConfidenceTopologyDetermined, Source: string(patternclassifier.SourceRule),
				DetectorID: "m6", TaxonomyVersion: patternclassifier.TaxonomyVersion}},
			IOContract: discovery.IRIOContract{InputSchema: map[string]any{"type": "object"}, OutputSchema: contract}}
	}
	return &discovery.IR{IRVersion: discovery.IRVersionPatternLabels,
		Workflow: discovery.IRWorkflow{ID: "wf-m6"},
		Nodes: []discovery.IRNode{
			node("router", patternclassifier.Routing),
			node("node3", patternclassifier.ToolUse),
			node("reflect", patternclassifier.Reflection),
		}}
}

func m6span(caseID, node string, i int, cost, lat float64, attrs map[string]any) telemetry.Span {
	base := time.Unix(1_700_000_000, 0).Add(time.Duration(i) * time.Second)
	a := map[string]any{telemetry.AttrNodeID: node, telemetry.AttrCostUSD: cost, telemetry.AttrLatencyMS: lat, telemetry.AttrNodeFailed: false}
	for k, v := range attrs {
		a[k] = v
	}
	return telemetry.Span{TraceID: telemetry.TraceID(caseID), SpanID: telemetry.NodeSpanID(caseID + node),
		Kind: telemetry.SpanKindNode, Name: node, StartTime: base, EndTime: base.Add(time.Duration(lat) * time.Millisecond),
		Status: telemetry.SpanStatusOK, Attributes: a}
}

func m6PromptDrift(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{
		"router": json.RawMessage(`{"a":"x"}`), "node3": json.RawMessage(`{"junk":1}`), "reflect": json.RawMessage(`{"a":"w"}`)}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		m6span(id, "router", 0, 0.001, 50, nil), m6span(id, "node3", 1, 0.100, 100, nil), m6span(id, "reflect", 2, 0.002, 800, nil)}}
	tr.Output, tr.Failed = json.RawMessage(`{"a":"w"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-m6"}, Trace: tr}
}

func m6ToolEmpty(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{"router": json.RawMessage(`{"a":"x"}`), "node3": json.RawMessage(`{"a":"ok"}`)}}
	tool := telemetry.Span{TraceID: telemetry.TraceID(id), SpanID: telemetry.ToolSpanID(id+"node3", "search", 0),
		Kind: telemetry.SpanKindTool, Name: "search", Status: telemetry.SpanStatusOK,
		StartTime: time.Unix(1_700_000_100, 0), EndTime: time.Unix(1_700_000_100, 0).Add(30 * time.Millisecond),
		Attributes: map[string]any{telemetry.AttrNodeID: "node3", telemetry.AttrToolReason: "empty"}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		m6span(id, "router", 0, 0.001, 50, nil), m6span(id, "node3", 1, 0.002, 100, nil), tool}}
	tr.Output, tr.Failed = json.RawMessage(`{"a":"w"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-m6"}, Trace: tr}
}

func m6MultiHop(id string) attribution.FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{
		"router": json.RawMessage(`{"a":"x"}`), "node3": json.RawMessage(`{"a":"ok"}`), "reflect": json.RawMessage(`{"a":"w"}`)}}
	var spans []telemetry.Span
	for i, n := range []string{"router", "node3", "reflect", "reflect", "reflect"} {
		spans = append(spans, m6span(id, n, i, 0.002, 120, nil))
	}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: spans}
	tr.Output, tr.Failed = json.RawMessage(`{"a":"w"}`), true
	return attribution.FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-m6"}, Trace: tr}
}

// stub analyst + sandboxed executor (the only stubs).

type m6Analyst struct{ calls []string }

func (a *m6Analyst) Analyze(_ context.Context, fc attribution.FailingCase, r diagnosis.Rubric) (diagnosis.AnalystResponse, error) {
	a.calls = append(a.calls, fc.Case.CaseID)
	return diagnosis.AnalystResponse{Code: string(diagnosis.CauseNonConvergence), Confidence: 0.55}, nil
}

type m6Exec struct{}

func (m6Exec) Sandboxed() bool { return true }
func (m6Exec) Execute(_ context.Context, unit attrengine.AblationUnit, v attribution.Variant, metric string) (attrengine.UnitResult, error) {
	mean := 0.40
	if !unit.Baseline && unit.Node == "node3" {
		mean = 0.90
	}
	var obs []evalstats.Observation
	for ci := 0; ci < 10; ci++ {
		val := mean + 0.01*float64((ci*7+int(unit.Seed)*3)%5)
		if val > 1 {
			val = 1
		}
		obs = append(obs, evalstats.Observation{CaseID: "c" + string(rune('a'+ci)), Seed: unit.Seed, Value: val})
	}
	return attrengine.UnitResult{Obs: obs, CostUSD: 0.001}, nil
}

func TestM6ExitChecklist_Green(t *testing.T) {
	ctx := context.Background()
	ir := m6IR()
	v := attribution.Variant{VariantID: "v-m6", ConfigHash: "cfg-m6", EvalSetHash: "es-m6", WorkflowID: "wf-m6"}
	cases := []attribution.FailingCase{
		m6PromptDrift("pd-1"), m6PromptDrift("pd-2"), m6PromptDrift("pd-3"),
		m6ToolEmpty("te-1"), m6ToolEmpty("te-2"),
		m6MultiHop("mh-1"), m6MultiHop("mh-2"), // residue → analyst
	}

	// Human-labeled calibration subset (task 11.1) → below-floor, so the flag path is exercised.
	cal := diagnosis.Calibrate("m6_analyst",
		map[string]diagnosis.TaxonomyCode{"h1": diagnosis.CauseNonConvergence, "h2": diagnosis.CauseRetrievalMiss, "h3": diagnosis.CauseMisroute, "h4": diagnosis.CauseContextOverflow},
		map[string]diagnosis.TaxonomyCode{"h1": diagnosis.CauseNonConvergence, "h2": diagnosis.CauseContextOverflow, "h3": diagnosis.CauseContextOverflow, "h4": diagnosis.CauseContextOverflow},
		diagnosis.DefaultAnalystFloor)

	analyst := &m6Analyst{}
	runner, err := attrengine.NewFanoutAblationRunner(m6Exec{}, nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	store := reportstore.NewMemStore()
	eng := &scorecard.Engine{Store: store, Runner: runner, Analyst: analyst, Cal: cal}

	view, err := eng.Generate(ctx, scorecard.GenerateInput{
		IR: ir, Variant: v, FailingCases: cases,
		Overall:          scorecard.OverallMetrics{TaskSuccess: 0.6, NCases: 20, NFailing: len(cases), CostUSD: 0.8, LatencyMS: 4000},
		AblationTopN:     3,
		SwappedConfigRef: func(node string) string { return "swap-" + node },
		AblationConfig:   attribution.AblationConfig{Metric: "task_success", Direction: evalstats.HigherIsBetter, Seeds: []int64{0, 1, 2, 3, 4}, Stats: evalstats.DefaultConfig()},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// ✅ names the responsible node (first-divergence node3).
	if view.Nodes[0].NodeID != "node3" || view.Nodes[0].FirstDivergenceCount == 0 {
		t.Errorf("[names responsible node] top node = %+v, want node3 with first-divergence", view.Nodes[0])
	}

	// ✅ clusters into named categories with sizes + representatives.
	if len(view.Clusters) < 3 {
		t.Errorf("[clustering] want ≥3 named clusters, got %d", len(view.Clusters))
	}
	for _, c := range view.Clusters {
		if c.Label == "" || c.Size == 0 || c.RepresentativeCaseID == "" {
			t.Errorf("[clustering] cluster missing label/size/representative: %+v", c)
		}
	}

	// ✅ ablation isolates node3 (bottleneck, CI excludes zero); non-faulty → inconclusive.
	var node3Abl, otherAbl *scorecard.AblationCard
	for i := range view.Ablations {
		if view.Ablations[i].NodeID == "node3" {
			node3Abl = &view.Ablations[i]
		} else {
			otherAbl = &view.Ablations[i]
		}
	}
	if node3Abl == nil || node3Abl.Verdict != attribution.VerdictBottleneck {
		t.Errorf("[ablation] node3 should be bottleneck; got %+v", node3Abl)
	} else if node3Abl.CILow <= 0 && node3Abl.CIHigh >= 0 {
		t.Errorf("[ablation] bottleneck CI must exclude zero; got [%v,%v]", node3Abl.CILow, node3Abl.CIHigh)
	}
	if otherAbl == nil || otherAbl.Verdict != attribution.VerdictInconclusive {
		t.Errorf("[ablation] a non-faulty swap should be inconclusive; got %+v", otherAbl)
	}

	// ✅ cost/latency bottleneck flags name the dominating node.
	dims := map[string][]attribution.Dimension{}
	for _, n := range view.Nodes {
		dims[n.NodeID] = n.BottleneckDimensions
	}
	if !containsDim(dims["node3"], attribution.DimCost) {
		t.Errorf("[bottleneck] node3 should carry cost flag; got %v", dims["node3"])
	}
	if !containsDim(dims["reflect"], attribution.DimLatency) {
		t.Errorf("[bottleneck] reflect should carry latency flag; got %v", dims["reflect"])
	}

	// ✅ rule detectors emit typed causes deterministically; analyst runs ONLY on the rule residue.
	// The three prompt-drift cases are rule-explained; the residue is the two tool-empty cases (no
	// tool-empty taxonomy *code* — that is a cluster category) plus the two multi-hop cases: 4 cases.
	// The analyst must be called on exactly those, and NEVER on a rule-explained pd-* case.
	if len(analyst.calls) != 4 {
		t.Errorf("[analyst residue] analyst called %d times, want 4 (residue only); calls=%v", len(analyst.calls), analyst.calls)
	}
	for _, c := range analyst.calls {
		if strings.HasPrefix(c, "pd-") {
			t.Errorf("[analyst residue] analyst called on rule-explained case %q — must run on residue only", c)
		}
	}

	// ✅ agreement reported alongside every analyst diagnosis + below-floor flagged; ✅ evidence on every card.
	sawAnalyst := false
	for _, d := range view.Diagnoses {
		if len(d.EvidenceCaseIDs) == 0 {
			t.Errorf("[evidence] diagnosis %s carries no evidence", d.Code)
		}
		if d.Source == diagnosis.SourceAnalyst {
			sawAnalyst = true
			if d.NHuman == 0 {
				t.Errorf("[agreement] analyst diagnosis missing agreement/n_human")
			}
			if !d.AnalystFlagged {
				t.Errorf("[flag] below-floor analyst diagnosis must be flagged")
			}
		}
	}
	if !sawAnalyst {
		t.Error("[analyst] expected at least one analyst diagnosis on the residue")
	}
	if !view.AnalystUncalibrated {
		t.Error("[uncalibrated] below-floor analyst should set AnalystUncalibrated on the scorecard")
	}

	// ✅ per-run scorecard shows overall + per-node + clusters; ✅ read-only, no apply affordance.
	if view.State != scorecard.StateReady || !view.ReadOnly {
		t.Errorf("[scorecard] want ready+read-only; got state=%q readOnly=%v", view.State, view.ReadOnly)
	}
	raw, _ := json.Marshal(view)
	for _, forbidden := range []string{"apply", "proposal", "mutate", "patch"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Errorf("[read-only] scorecard leaks a mutation affordance: %q", forbidden)
		}
	}

	// ✅ read-only proven: the ablations persisted are ephemeral; no user variant was created.
	key := reportstore.ReportKey{VariantID: v.VariantID, EvalSetHash: v.EvalSetHash, ConfigHash: v.ConfigHash}
	for _, abl := range store.Ablation(ctx, key) {
		if !abl.Ephemeral {
			t.Errorf("[read-only] persisted ablation is not ephemeral: %+v", abl)
		}
	}
}

func containsDim(dims []attribution.Dimension, d attribution.Dimension) bool {
	for _, x := range dims {
		if x == d {
			return true
		}
	}
	return false
}
