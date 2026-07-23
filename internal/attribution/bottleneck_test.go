package attribution

import (
	"encoding/json"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// paretoCase builds a run where one node dominates COST and a different node dominates LATENCY:
//   - router: cheap and fast
//   - "costly": the majority of end-to-end cost
//   - "slow":   the latency critical path
func paretoCase(id string) FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		nodeSpan(id, "router", 0, 0.001, 50, false),
		nodeSpan(id, "costly", 1, 0.100, 100, false), // dominates cost
		nodeSpan(id, "slow", 2, 0.002, 800, false),   // dominates latency
	}}
	tr.Output = json.RawMessage(`{"a":"ok"}`)
	return FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-x"}, Trace: tr}
}

// Task 4.3: the flag names the cost-dominating node and the latency-critical-path node, each tagged
// with its dimension.
func TestBottleneck_NamesCostAndLatencyDominators(t *testing.T) {
	v := testVariant()
	flags := Bottleneck(v, []FailingCase{paretoCase("c1"), paretoCase("c2")}, BottleneckConfig{})

	byDim := map[Dimension][]BottleneckFlag{}
	for _, f := range flags {
		byDim[f.Dimension] = append(byDim[f.Dimension], f)
	}

	if len(byDim[DimCost]) != 1 || byDim[DimCost][0].NodeID != "costly" {
		t.Fatalf("cost flags = %+v, want single flag on 'costly'", byDim[DimCost])
	}
	if len(byDim[DimLatency]) != 1 || byDim[DimLatency][0].NodeID != "slow" {
		t.Fatalf("latency flags = %+v, want single flag on 'slow'", byDim[DimLatency])
	}
	// Dominance shares must be the real majority.
	if byDim[DimCost][0].Dominance < 0.5 {
		t.Errorf("cost dominance = %.3f, want majority", byDim[DimCost][0].Dominance)
	}
	if byDim[DimLatency][0].Dominance < 0.5 {
		t.Errorf("latency dominance = %.3f, want majority", byDim[DimLatency][0].Dominance)
	}
}

// A co-dominant split flags both nodes on the Pareto frontier.
func TestBottleneck_CoDominantSplit(t *testing.T) {
	mk := func(id string) FailingCase {
		tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{}}
		tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
			nodeSpan(id, "a", 0, 0.05, 100, false),
			nodeSpan(id, "b", 1, 0.05, 100, false),
			nodeSpan(id, "c", 2, 0.001, 5, false),
		}}
		return FailingCase{Case: evalharness.Case{CaseID: id}, Trace: tr}
	}
	flags := Bottleneck(testVariant(), []FailingCase{mk("c1")}, BottleneckConfig{})
	costNodes := map[string]bool{}
	for _, f := range flags {
		if f.Dimension == DimCost {
			costNodes[f.NodeID] = true
		}
	}
	if !costNodes["a"] || !costNodes["b"] {
		t.Fatalf("co-dominant cost split should flag both a and b; got %v", costNodes)
	}
}
