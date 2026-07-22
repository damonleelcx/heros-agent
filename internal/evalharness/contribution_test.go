package evalharness

import (
	"math"
	"testing"
)

// Task 1.6 — per-node contribution decomposes the run's end-to-end cost/latency/success.

func TestPerNodeContributionDecomposesTheRun(t *testing.T) {
	tr := newTrace("run-1").
		node("cheap", 0.010, 100, 10, 10, false).
		node("expensive", 0.090, 300, 10, 10, false).
		output(`{"a":"world"}`).
		build()

	cs := Contributions(tr)
	if len(cs) != 2 {
		t.Fatalf("want 2 node contributions, got %d", len(cs))
	}
	byNode := map[string]NodeContribution{}
	for _, c := range cs {
		byNode[c.NodeID] = c
	}
	if got := byNode["expensive"].CostShare; math.Abs(got-0.9) > 1e-9 {
		t.Fatalf("expensive cost share: want 0.9 got %v", got)
	}
	if got := byNode["cheap"].CostShare; math.Abs(got-0.1) > 1e-9 {
		t.Fatalf("cheap cost share: want 0.1 got %v", got)
	}
	if got := byNode["expensive"].LatencyShare; math.Abs(got-0.75) > 1e-9 {
		t.Fatalf("expensive latency share: want 0.75 got %v", got)
	}

	// Shares sum to 1 — a decomposition that does not is not a decomposition.
	var cost, lat float64
	for _, c := range cs {
		cost += c.CostShare
		lat += c.LatencyShare
	}
	if math.Abs(cost-1) > 1e-9 || math.Abs(lat-1) > 1e-9 {
		t.Fatalf("shares must sum to 1, got cost=%v latency=%v", cost, lat)
	}
}

// Only the FIRST failing node in execution order carries the failure: crediting every downstream
// node would multiply one root cause into a cluster and send attribution chasing symptoms.
func TestOnlyFirstFailingNodeCarriesSuccessImpact(t *testing.T) {
	tr := newTrace("run-1").
		node("a", 0.01, 100, 1, 1, false).
		node("b", 0.01, 100, 1, 1, true).
		node("c", 0.01, 100, 1, 1, true).
		failed("halted at b").
		build()

	var blamed []string
	for _, c := range Contributions(tr) {
		if c.SuccessImpact == 1 {
			blamed = append(blamed, c.NodeID)
		}
	}
	if len(blamed) != 1 || blamed[0] != "b" {
		t.Fatalf("want exactly node b blamed, got %v", blamed)
	}
}

// A failed tool call is attributed to the node that made it.
func TestToolErrorsAttributeToTheirNode(t *testing.T) {
	tr := newTrace("run-1").
		node("a", 0.01, 100, 1, 1, false).
		node("b", 0.01, 100, 1, 1, false).
		tool("b", "search", false).
		tool("b", "fetch", false).
		build()

	for _, c := range Contributions(tr) {
		switch c.NodeID {
		case "b":
			if c.ToolErrors != 2 {
				t.Fatalf("node b: want 2 tool errors got %v", c.ToolErrors)
			}
			if c.SuccessImpact != 1 {
				t.Fatal("a node whose tool calls failed is a failing node")
			}
		case "a":
			if c.ToolErrors != 0 {
				t.Fatalf("node a: want 0 tool errors got %v", c.ToolErrors)
			}
		}
	}
}

// The decomposition is persisted as tagged rows, so it is queryable per case and per node rather
// than recomputed by whoever wants to read it.
func TestContributionMetricsAreTaggedRows(t *testing.T) {
	tr := newTrace("run-1").
		node("a", 0.01, 100, 1, 1, false).
		node("b", 0.03, 300, 1, 1, false).
		build()

	vs := ContributionMetrics(tr, baseCase())
	if len(vs) != 2*len(ContributionFamily) {
		t.Fatalf("want %d rows (2 nodes x %d metrics), got %d", 2*len(ContributionFamily), len(ContributionFamily), len(vs))
	}
	for _, v := range vs {
		if v.NodeID == "" {
			t.Fatalf("contribution row %q has no node_id", v.Metric)
		}
		if v.Evaluator == "" {
			t.Fatalf("contribution row %q has no evaluator attribution", v.Metric)
		}
	}
}

// A trace with no spans decomposes to nothing rather than dividing by zero.
func TestEmptyTraceDecomposesToNothing(t *testing.T) {
	tr := newTrace("run-1").build()
	if got := Contributions(tr); len(got) != 0 {
		t.Fatalf("want no contributions from an empty trace, got %v", got)
	}
}
