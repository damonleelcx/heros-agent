package evalharness

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/telemetry"
)

// contribution.go is task 1.6: decompose the run's end-to-end success / cost / latency into a
// PER-NODE contribution, from the traces. This is the substrate P4.5 attribution consumes — "the
// workflow scores 60%" is not actionable, "node summarize contributes 71% of the cost and is the
// failing node in 34 of the 40 failures" is.

// NodeContribution is one node's share of one run.
type NodeContribution struct {
	NodeID string `json:"node_id"`
	// CostShare / LatencyShare are shares of the run total in [0,1]. Shares, not absolutes, because
	// the question attribution asks is "where did it go?", and a share is comparable across cases of
	// wildly different absolute size.
	CostShare    float64 `json:"cost_share"`
	LatencyShare float64 `json:"latency_share"`
	// SuccessImpact is 1 when this node is where the run failed, 0 otherwise. Only the FIRST failing
	// node in execution order carries the 1: everything after it failed because of it, and crediting
	// every downstream node with the failure would multiply one root cause into a cluster.
	SuccessImpact float64 `json:"success_impact"`
	// ToolErrors counts this node's failed tool calls.
	ToolErrors float64 `json:"tool_errors"`
	// CostUSD / LatencyMS are the absolute figures the shares came from, carried so a reader can
	// check the arithmetic rather than trust it.
	CostUSD   float64 `json:"cost_usd"`
	LatencyMS float64 `json:"latency_ms"`
}

// Contributions decomposes a trace per node, in stable node order.
//
// Latency share uses the node's OWN duration over the sum of node durations (not over wall-clock),
// because the question is "which node did the work?" — under a parallel fan-out the shares still
// answer that, whereas dividing by wall-clock would make the shares sum above 1 and read as broken.
func Contributions(tr Trace) []NodeContribution {
	type acc struct {
		cost, latency float64
		failed        bool
		start         int64
		toolErrors    float64
	}
	byNode := map[string]*acc{}
	order := []string{}

	for _, sp := range tr.NodeSpans() {
		id := attrString(sp.Attributes, telemetry.AttrNodeID)
		if id == "" {
			id = sp.Name
		}
		a, ok := byNode[id]
		if !ok {
			a = &acc{start: sp.StartTime.UnixNano()}
			byNode[id] = a
			order = append(order, id)
		}
		if sp.StartTime.UnixNano() < a.start {
			a.start = sp.StartTime.UnixNano()
		}
		a.cost += attrFloat(sp.Attributes, telemetry.AttrCostUSD)
		if ms := attrFloat(sp.Attributes, telemetry.AttrLatencyMS); ms > 0 {
			a.latency += ms
		} else if d := sp.Duration(); d > 0 {
			a.latency += float64(d.Milliseconds())
		}
		if sp.Status == telemetry.SpanStatusError {
			a.failed = true
		}
		if f, ok := sp.Attributes[telemetry.AttrNodeFailed].(bool); ok && f {
			a.failed = true
		}
	}

	for _, sp := range tr.Spans {
		if sp.Kind != telemetry.SpanKindTool || sp.Status != telemetry.SpanStatusError {
			continue
		}
		id := attrString(sp.Attributes, telemetry.AttrNodeID)
		if a, ok := byNode[id]; ok {
			a.toolErrors++
			a.failed = true
		}
	}

	var totalCost, totalLatency float64
	for _, a := range byNode {
		totalCost += a.cost
		totalLatency += a.latency
	}

	// The first failing node in execution order is the one credited with the failure.
	firstFailing := ""
	{
		failing := []string{}
		for id, a := range byNode {
			if a.failed {
				failing = append(failing, id)
			}
		}
		sort.Slice(failing, func(i, j int) bool {
			ai, aj := byNode[failing[i]], byNode[failing[j]]
			if ai.start != aj.start {
				return ai.start < aj.start
			}
			return failing[i] < failing[j]
		})
		if len(failing) > 0 {
			firstFailing = failing[0]
		}
	}

	sort.Strings(order)
	out := make([]NodeContribution, 0, len(order))
	for _, id := range order {
		a := byNode[id]
		nc := NodeContribution{
			NodeID:     id,
			CostUSD:    a.cost,
			LatencyMS:  a.latency,
			ToolErrors: a.toolErrors,
		}
		if totalCost > 0 {
			nc.CostShare = a.cost / totalCost
		}
		if totalLatency > 0 {
			nc.LatencyShare = a.latency / totalLatency
		}
		if id == firstFailing {
			nc.SuccessImpact = 1
		}
		out = append(out, nc)
	}
	return out
}

// ContributionMetrics renders the decomposition as MetricValues so it persists through exactly the
// same tagged path every other eval result takes — per-node contribution is queryable per case and
// per node because it is stored as rows, not computed in a report.
func ContributionMetrics(tr Trace, c Case) []MetricValue {
	var out []MetricValue
	for _, nc := range Contributions(tr) {
		for _, mv := range []MetricValue{
			{Metric: MetricNodeCostShare, Value: nc.CostShare, Unit: telemetry.UnitRatio},
			{Metric: MetricNodeLatencyShare, Value: nc.LatencyShare, Unit: telemetry.UnitRatio},
			{Metric: MetricNodeSuccessImpact, Value: nc.SuccessImpact, Unit: telemetry.UnitRatio},
			{Metric: MetricNodeToolErrorCount, Value: nc.ToolErrors, Unit: telemetry.UnitCount},
		} {
			mv.NodeID = nc.NodeID
			mv.Evaluator = contributionEvaluator
			mv.ReferenceLabel = c.Label
			out = append(out, mv)
		}
	}
	return out
}

// contributionEvaluator is the evaluator_name the decomposition rows are attributed to.
const contributionEvaluator = "node_contribution"
