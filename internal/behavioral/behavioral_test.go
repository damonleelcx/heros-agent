package behavioral

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/dynamictracing"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/reconcile"
)

func candidate(p patternclassifier.Pattern, subgraphRef string) patternclassifier.Label {
	return patternclassifier.Label{
		Pattern: p, Confidence: 0.5, Source: patternclassifier.SourceRule,
		SubgraphRef: subgraphRef, DetectorID: "structural", TaxonomyVersion: patternclassifier.TaxonomyVersion,
		Candidate: patternclassifier.IsBehavioral(p),
	}
}

func reportFor(ir *discovery.IR, calls []dynamictracing.TracedCall) reconcile.Report {
	return reconcile.Reconcile(ir, calls)
}

func tcall(node string, idx int) dynamictracing.TracedCall {
	return dynamictracing.TracedCall{Tags: dynamictracing.Tags{RunID: "r", ConfigHash: "c", NodeID: node}, InvocationIndex: idx}
}

func irNode(id string) discovery.IRNode {
	return discovery.IRNode{NodeID: id, Kind: "static_definition",
		IOContract: discovery.IRIOContract{InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}}}
}

// TASK 6.6: a self-edge iterating > 1 → confirmed Reflection with the Reflection metric-set.
func TestConfirm_ReflectionConfirmedWithMetricSet(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion,
		Nodes: []discovery.IRNode{irNode("critic")},
		Edges: []discovery.IREdge{{FromNodeID: "critic", ToNodeID: "critic", Kind: "data"}}}
	trace := []dynamictracing.TracedCall{tcall("critic", 0), tcall("critic", 1), tcall("critic", 2)}
	ev := Evidence{Report: reportFor(ir, trace)}

	res := Confirm(ir, []patternclassifier.Label{candidate(patternclassifier.Reflection, "critic")}, ev)
	if len(res.Confirmed) != 1 {
		t.Fatalf("Reflection with 3 iterations must be confirmed, got %+v", res.Confirmed)
	}
	c := res.Confirmed[0]
	if c.Pattern != patternclassifier.Reflection || c.Source != patternclassifier.SourceBehavioral {
		t.Fatalf("confirmed label wrong: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("confirmed behavioral label must be valid (uncapped, not candidate): %v", err)
	}
	ms, ok := res.MetricSets["critic"]
	if !ok || !contains(ms.Metrics, "iteration_count") || !contains(ms.Metrics, "convergence") {
		t.Fatalf("confirmed Reflection must select its metric-set, got %+v", ms)
	}
}

// TASK 6.6 / 6.2: a one-shot self-edge is NOT confirmed Reflection, and no metric-set is selected.
func TestConfirm_OneShotSelfEdgeNotReflection(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion,
		Nodes: []discovery.IRNode{irNode("critic")},
		Edges: []discovery.IREdge{{FromNodeID: "critic", ToNodeID: "critic", Kind: "data"}}}
	trace := []dynamictracing.TracedCall{tcall("critic", 0)} // executed exactly once
	ev := Evidence{Report: reportFor(ir, trace)}

	res := Confirm(ir, []patternclassifier.Label{candidate(patternclassifier.Reflection, "critic")}, ev)
	if len(res.Confirmed) != 0 {
		t.Fatalf("a one-shot self-edge must NOT confirm Reflection, got %+v", res.Confirmed)
	}
	if _, ok := res.MetricSets["critic"]; ok {
		t.Fatal("no metric-set may be selected for an unconfirmed Reflection")
	}
}

// TASK 6.6: a never-improving Reflection loop → typed anti-pattern with per-iteration quality evidence.
func TestConfirm_NeverImprovingLoopAntiPattern(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion,
		Nodes: []discovery.IRNode{irNode("critic")},
		Edges: []discovery.IREdge{{FromNodeID: "critic", ToNodeID: "critic", Kind: "data"}}}
	trace := []dynamictracing.TracedCall{tcall("critic", 0), tcall("critic", 1), tcall("critic", 2)}
	ev := Evidence{Report: reportFor(ir, trace), Quality: map[string][]float64{"critic": {0.7, 0.7, 0.69}}}

	res := Confirm(ir, []patternclassifier.Label{candidate(patternclassifier.Reflection, "critic")}, ev)
	if len(res.AntiPatterns) != 1 || res.AntiPatterns[0].Kind != ReflectionNoImprove {
		t.Fatalf("want a reflection_no_improve anti-pattern, got %+v", res.AntiPatterns)
	}
	q, ok := res.AntiPatterns[0].Evidence["per_iteration_quality"].([]float64)
	if !ok || len(q) != 3 {
		t.Fatalf("anti-pattern must attach per-iteration quality evidence, got %+v", res.AntiPatterns[0].Evidence)
	}
}

// An improving loop is confirmed Reflection but is NOT flagged as an anti-pattern.
func TestConfirm_ImprovingLoopNoAntiPattern(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion,
		Nodes: []discovery.IRNode{irNode("critic")},
		Edges: []discovery.IREdge{{FromNodeID: "critic", ToNodeID: "critic", Kind: "data"}}}
	trace := []dynamictracing.TracedCall{tcall("critic", 0), tcall("critic", 1)}
	ev := Evidence{Report: reportFor(ir, trace), Quality: map[string][]float64{"critic": {0.5, 0.8}}}
	res := Confirm(ir, []patternclassifier.Label{candidate(patternclassifier.Reflection, "critic")}, ev)
	if len(res.Confirmed) != 1 {
		t.Fatal("an improving loop is still confirmed Reflection")
	}
	if len(res.AntiPatterns) != 0 {
		t.Fatalf("an improving loop must not be flagged, got %+v", res.AntiPatterns)
	}
}

// Planning: a planning node whose task list is consumed downstream → confirmed.
func TestConfirm_Planning(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion,
		Nodes: []discovery.IRNode{irNode("planner"), irNode("worker")},
		Edges: []discovery.IREdge{{FromNodeID: "planner", ToNodeID: "worker", Kind: "data"}}}
	trace := []dynamictracing.TracedCall{tcall("planner", 0), tcall("worker", 0)}
	res := Confirm(ir, []patternclassifier.Label{candidate(patternclassifier.Planning, "planner")}, Evidence{Report: reportFor(ir, trace)})
	if len(res.Confirmed) != 1 || res.Confirmed[0].Pattern != patternclassifier.Planning {
		t.Fatalf("Planning must confirm when its list is consumed downstream, got %+v", res.Confirmed)
	}
}

// Memory Management: a read AND a write against a store → confirmed.
func TestConfirm_MemoryManagement(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Nodes: []discovery.IRNode{irNode("mem")}}
	trace := []dynamictracing.TracedCall{tcall("mem", 0), tcall("mem", 1)}
	ev := Evidence{Report: reportFor(ir, trace), MemoryOps: map[string][]string{"mem": {"read", "write"}}}
	res := Confirm(ir, []patternclassifier.Label{candidate(patternclassifier.MemoryManagement, "mem")}, ev)
	if len(res.Confirmed) != 1 {
		t.Fatalf("Memory Management must confirm on read+write, got %+v", res.Confirmed)
	}
	// Read-only must NOT confirm.
	ev2 := Evidence{Report: reportFor(ir, trace), MemoryOps: map[string][]string{"mem": {"read", "read"}}}
	if res2 := Confirm(ir, []patternclassifier.Label{candidate(patternclassifier.MemoryManagement, "mem")}, ev2); len(res2.Confirmed) != 0 {
		t.Fatal("read-only memory access must not confirm Memory Management")
	}
}

// HITL: a human-approval pause → confirmed.
func TestConfirm_HITL(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Nodes: []discovery.IRNode{irNode("approve")}}
	trace := []dynamictracing.TracedCall{tcall("approve", 0)}
	ev := Evidence{Report: reportFor(ir, trace), HumanPauses: map[string]bool{"approve": true}}
	res := Confirm(ir, []patternclassifier.Label{candidate(patternclassifier.HumanInTheLoop, "approve")}, ev)
	if len(res.Confirmed) != 1 {
		t.Fatalf("HITL must confirm on a human-approval pause, got %+v", res.Confirmed)
	}
}

// Anti-pattern: a router sending (nearly) all traffic one way.
func TestConfirm_RouterOneWay(t *testing.T) {
	ir := &discovery.IR{IRVersion: discovery.IRVersion, Nodes: []discovery.IRNode{irNode("router")}}
	trace := []dynamictracing.TracedCall{tcall("router", 0)}
	ev := Evidence{Report: reportFor(ir, trace), BranchTraffic: map[string]map[string]int{"router": {"a": 98, "b": 2}}}
	res := Confirm(ir, []patternclassifier.Label{candidate(patternclassifier.Routing, "router")}, ev)
	if len(res.AntiPatterns) != 1 || res.AntiPatterns[0].Kind != RouterOneWay {
		t.Fatalf("want router_one_way, got %+v", res.AntiPatterns)
	}
}

// TASK 6.4: the LLM classifier handles only the residue and never overrides a confirmed label.
func TestClassifyResidue_NeverOverridesConfirmed(t *testing.T) {
	clf := stubClassifier{pattern: patternclassifier.Planning, confidence: 0.9}
	ambiguous := map[string]map[string]any{"critic": {"x": 1}, "unknown": {"y": 2}}
	resolved := map[string]bool{"critic": true} // already confirmed by a rule
	labels := ClassifyResidue(clf, ambiguous, resolved)
	for _, l := range labels {
		if l.SubgraphRef == "critic" {
			t.Fatal("the LLM classifier must NOT relabel a rule-confirmed subgraph")
		}
	}
	if len(labels) != 1 || labels[0].SubgraphRef != "unknown" {
		t.Fatalf("only the ambiguous residue should be classified, got %+v", labels)
	}
	// A behavioral LLM guess is capped and marked candidate (never a confident confirmation).
	if labels[0].Confidence > patternclassifier.BehavioralCandidateCap || !labels[0].Candidate {
		t.Fatalf("LLM behavioral guess must be capped + candidate, got %+v", labels[0])
	}
}

type stubClassifier struct {
	pattern    patternclassifier.Pattern
	confidence float64
}

func (s stubClassifier) Classify(string, map[string]any) (patternclassifier.Pattern, float64, bool) {
	return s.pattern, s.confidence, true
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
