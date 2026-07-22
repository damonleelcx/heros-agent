package evalharness

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/heros-foreal/agentd/internal/telemetry"
)

func valuesByMetric(vs []MetricValue) map[string]MetricValue {
	m := map[string]MetricValue{}
	for _, v := range vs {
		m[v.Metric] = v
	}
	return m
}

// Task 1.5 — the standard family is computed from the traces with no per-workflow configuration.
func TestStandardFamilyFromTrace(t *testing.T) {
	reg := NewBuiltinRegistry()
	tr := newTrace("run-1").
		node("router", 0.010, 100, 100, 50, false).
		node("answer", 0.030, 300, 400, 200, false).
		tool("answer", "search", true).
		tool("answer", "fetch", false).
		output(`{"a":"world"}`).
		build()

	vs, err := RunMetrics(context.Background(), reg, tr, baseCase())
	if err != nil {
		t.Fatalf("RunMetrics: %v", err)
	}
	got := valuesByMetric(vs)

	for _, m := range StandardFamily {
		if _, ok := got[m]; !ok {
			t.Fatalf("standard family metric %q missing from %v", m, keys(got))
		}
	}
	if got[MetricRunCostUSD].Value != 0.04 {
		t.Fatalf("cost: want 0.04 got %v", got[MetricRunCostUSD].Value)
	}
	if got[MetricRunTokens].Value != 750 {
		t.Fatalf("tokens: want 750 got %v", got[MetricRunTokens].Value)
	}
	if got[MetricRunLatencyMS].Value != 400 {
		t.Fatalf("latency: want the 400ms span envelope, got %v", got[MetricRunLatencyMS].Value)
	}
	if got[MetricToolErrorRate].Value != 0.5 {
		t.Fatalf("tool error rate: want 0.5 (1 of 2), got %v", got[MetricToolErrorRate].Value)
	}
	if got[MetricReliability].Value != 0 {
		t.Fatal("a run with a failed tool call is not reliable")
	}
	if got[MetricTaskSuccess].Value != 1 {
		t.Fatalf("task_success: want 1 got %v", got[MetricTaskSuccess].Value)
	}
	if got[MetricRunCostUSD].Unit != telemetry.UnitUSD {
		t.Fatalf("cost unit: want %q got %q", telemetry.UnitUSD, got[MetricRunCostUSD].Unit)
	}
	// Every family row is attributable to a producer — no empty evaluator_name.
	for _, v := range vs {
		if v.Evaluator == "" {
			t.Fatalf("metric %q has no evaluator attribution", v.Metric)
		}
		if v.NodeID == "" {
			t.Fatalf("metric %q has no node_id (the seven-tag contract requires one)", v.Metric)
		}
	}
}

// The success oracle is picked per case, and an explicitly named oracle wins over the precedence —
// a case carrying both a reference and a rubric is genuinely ambiguous.
func TestSuccessOraclePrecedenceAndOverride(t *testing.T) {
	reg := NewBuiltinRegistry()
	judge, err := NewLLMJudge(EvaluatorLLMJudge, MetricJudgeScore, &stubJudge{score: 5},
		JudgeConfig{Model: "stub", ScaleMax: 5}, calibratedStanding(MetricJudgeScore))
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if err := reg.Register(judge); err != nil {
		t.Fatalf("register judge: %v", err)
	}

	c := baseCase()
	c.Rubric = "rubric"
	e, err := SuccessOracleFor(reg, c)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	if e.Name() != EvaluatorExactMatch {
		t.Fatalf("a deterministic oracle must be preferred by default, got %q", e.Name())
	}

	c.SuccessOracle = EvaluatorLLMJudge
	e, err = SuccessOracleFor(reg, c)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	if e.Name() != EvaluatorLLMJudge {
		t.Fatalf("an explicitly named oracle must win, got %q", e.Name())
	}

	c.SuccessOracle = "no_such_evaluator"
	if _, err := SuccessOracleFor(reg, c); err == nil {
		t.Fatal("naming an unregistered oracle must fail loudly, not fall back")
	}
}

// A case with no oracle at all yields NO task_success row — never a zero. "Could not measure" and
// "measured and failed" are different facts.
func TestReferenceFreeCaseYieldsNoTaskSuccessRow(t *testing.T) {
	reg := NewBuiltinRegistry()
	c := baseCase()
	c.Reference = nil
	c.Label = LabelNone
	tr := newTrace("run-1").node("n", 0.01, 10, 1, 1, false).output(`{"a":"x"}`).build()

	vs, err := RunMetrics(context.Background(), reg, tr, c)
	if err != nil {
		t.Fatalf("RunMetrics: %v", err)
	}
	if _, ok := valuesByMetric(vs)[MetricTaskSuccess]; ok {
		t.Fatal("an unmeasurable success must be absent, not zero")
	}
	// The trace-derived family is still computed: cost/latency/tokens do not need a reference.
	if _, ok := valuesByMetric(vs)[MetricRunCostUSD]; !ok {
		t.Fatal("cost must still be measured for a reference-free case")
	}
}

// A run that halted has no output: success is 0 (measured), reliability is 0.
func TestFailedRunIsMeasuredAsFailure(t *testing.T) {
	reg := NewBuiltinRegistry()
	tr := newTrace("run-1").node("n", 0.01, 10, 1, 1, true).failed("contract violation").build()
	vs, err := RunMetrics(context.Background(), reg, tr, baseCase())
	if err != nil {
		t.Fatalf("RunMetrics: %v", err)
	}
	got := valuesByMetric(vs)
	if got[MetricTaskSuccess].Value != 0 {
		t.Fatalf("a halted run has not succeeded, got %v", got[MetricTaskSuccess].Value)
	}
	if got[MetricReliability].Value != 0 {
		t.Fatal("a halted run is not reliable")
	}
}

// Token counts emitted as ints must not read as zero (the map[string]any trap).
func TestTokenAttributesAreReadRegardlessOfNumericType(t *testing.T) {
	tr := newTrace("run-1").node("n", 0, 10, 7, 3, false).build()
	tr.Spans[0].Attributes[telemetry.AttrGenAIUsageInput] = float64(7)
	if got := aggregate(tr).tokens; got != 10 {
		t.Fatalf("want 10 tokens across int and float64 attributes, got %v", got)
	}
}

func keys(m map[string]MetricValue) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

var _ = json.RawMessage(nil)
