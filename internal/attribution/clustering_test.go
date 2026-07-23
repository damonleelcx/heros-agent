package attribution

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// toolSpan builds a tool child span under a node, with a fail-closed reason.
func toolSpan(caseID, node, tool, reason string, failed bool) telemetry.Span {
	base := time.Unix(1_700_000_500, 0)
	status := telemetry.SpanStatusOK
	if failed {
		status = telemetry.SpanStatusError
	}
	return telemetry.Span{
		TraceID:   telemetry.TraceID(caseID),
		SpanID:    telemetry.ToolSpanID(caseID+":"+node, tool, 0),
		Kind:      telemetry.SpanKindTool,
		Name:      tool,
		StartTime: base,
		EndTime:   base.Add(50 * time.Millisecond),
		Status:    status,
		Attributes: map[string]any{
			telemetry.AttrNodeID:     node,
			telemetry.AttrToolName:   tool,
			telemetry.AttrToolReason: reason,
		},
	}
}

// multiHopCase is a deep chain (≥4 node executions) with no tool/retrieval/overflow signal — a
// multi-hop reasoning failure.
func multiHopCase(id string) FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{}}
	var spans []telemetry.Span
	for i, n := range []string{"plan", "hop1", "hop2", "hop3", "answer"} {
		spans = append(spans, nodeSpan(id, n, i, 0.002, 120, false))
	}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: spans}
	tr.Output = json.RawMessage(`{"a":"wrong"}`)
	tr.Failed = true
	return FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-x"}, Trace: tr}
}

// toolEmptyCase is a short run whose tool returned empty — a tool-returns-empty failure.
func toolEmptyCase(id string) FailingCase {
	tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{}}
	tr.Trace = telemetry.Trace{Run: telemetry.RunContext{CaseID: id}, Spans: []telemetry.Span{
		nodeSpan(id, "router", 0, 0.001, 80, false),
		nodeSpan(id, "lookup", 1, 0.002, 100, false),
		toolSpan(id, "lookup", "search", "empty", false),
	}}
	tr.Output = json.RawMessage(`{"a":"wrong"}`)
	tr.Failed = true
	return FailingCase{Case: evalharness.Case{CaseID: id, WorkflowID: "wf-x"}, Trace: tr}
}

// Task 2.4: a multi-hop fault cluster and a tool-returns-empty fault cluster yield two distinct named
// clusters with correct sizes.
func TestCluster_TwoDistinctNamedClusters(t *testing.T) {
	v := testVariant()
	cases := []FailingCase{
		multiHopCase("mh-1"), multiHopCase("mh-2"), multiHopCase("mh-3"),
		toolEmptyCase("te-1"), toolEmptyCase("te-2"),
	}
	clusters := Cluster(nil, v, cases)

	if len(clusters) != 2 {
		t.Fatalf("got %d clusters, want 2: %+v", len(clusters), clusters)
	}
	bySig := map[FailureSignature]FailureCluster{}
	for _, c := range clusters {
		bySig[c.Signature] = c
	}

	mh, ok := bySig[SigMultiHop]
	if !ok {
		t.Fatalf("no multi-hop cluster; got %+v", clusters)
	}
	if mh.Size != 3 {
		t.Errorf("multi-hop size = %d, want 3", mh.Size)
	}
	if mh.Label != "fails on multi-hop reasoning" {
		t.Errorf("multi-hop label = %q", mh.Label)
	}
	if mh.RepresentativeCaseID != "mh-1" {
		t.Errorf("multi-hop representative = %q, want mh-1", mh.RepresentativeCaseID)
	}

	te, ok := bySig[SigToolReturnsEmpty]
	if !ok {
		t.Fatalf("no tool-returns-empty cluster; got %+v", clusters)
	}
	if te.Size != 2 {
		t.Errorf("tool-empty size = %d, want 2", te.Size)
	}
	if te.Label != "fails when a tool returns empty" {
		t.Errorf("tool-empty label = %q", te.Label)
	}

	// Clusters are size-ordered: the larger multi-hop cluster comes first.
	if clusters[0].Signature != SigMultiHop {
		t.Errorf("clusters not size-ordered: first is %q", clusters[0].Signature)
	}
}

// Determinism + append-only stability: the same failing set yields identical cluster ids and members
// regardless of input order.
func TestCluster_DeterministicAndStableIDs(t *testing.T) {
	v := testVariant()
	a := Cluster(nil, v, []FailingCase{multiHopCase("mh-1"), toolEmptyCase("te-1"), multiHopCase("mh-2")})
	b := Cluster(nil, v, []FailingCase{toolEmptyCase("te-1"), multiHopCase("mh-2"), multiHopCase("mh-1")})
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("clustering not deterministic:\n a=%s\n b=%s", ja, jb)
	}
}
