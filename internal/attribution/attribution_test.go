package attribution

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fixtures: the multi-pattern workflow (Routing → Tool Use → Reflection) with a
// fault injected at exactly ONE node (task 1.1 fixture / task 11.1). Node "node3"
// (a Tool Use node) drops its output contract — it emits {"junk":...} where its
// schema requires {"a": string} — which causes a downstream parse failure. The
// fault is at node3; first-divergence MUST name node3, not the node that then
// fails to parse it.
// ─────────────────────────────────────────────────────────────────────────────

const faultyNodeID = "node3"

// faultIR is the task-1.1 fixture IR: router → node3 (Tool Use, output-contracted) → reflect.
func faultIR() *discovery.IR {
	node := func(id string, p patternclassifier.Pattern, outSchema map[string]any) discovery.IRNode {
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
	contract := map[string]any{
		"type": "object", "required": []any{"a"},
		"properties": map[string]any{"a": map[string]any{"type": "string"}},
	}
	return &discovery.IR{
		IRVersion: discovery.IRVersionPatternLabels,
		Workflow:  discovery.IRWorkflow{ID: "wf-fault", Language: "python"},
		Nodes: []discovery.IRNode{
			node("router", patternclassifier.Routing, contract),
			node(faultyNodeID, patternclassifier.ToolUse, contract),
			node("reflect", patternclassifier.Reflection, contract),
		},
		Edges: []discovery.IREdge{
			{FromNodeID: "router", ToNodeID: faultyNodeID, Kind: "control"},
			{FromNodeID: faultyNodeID, ToNodeID: "reflect", Kind: "data"},
		},
	}
}

// nodeSpan builds one node span at ordinal i with the given per-node figures.
func nodeSpan(caseID, node string, i int, cost, latency float64, failed bool) telemetry.Span {
	base := time.Unix(1_700_000_000, 0)
	start := base.Add(time.Duration(i) * time.Second)
	status := telemetry.SpanStatusOK
	if failed {
		status = telemetry.SpanStatusError
	}
	return telemetry.Span{
		TraceID:   telemetry.TraceID(caseID),
		SpanID:    telemetry.NodeSpanID(caseID + ":" + node),
		Kind:      telemetry.SpanKindNode,
		Name:      "chat " + node,
		StartTime: start,
		EndTime:   start.Add(time.Duration(latency) * time.Millisecond),
		Status:    status,
		Attributes: map[string]any{
			telemetry.AttrNodeID:     node,
			telemetry.AttrCostUSD:    cost,
			telemetry.AttrLatencyMS:  latency,
			telemetry.AttrNodeFailed: failed,
		},
	}
}

// faultyCase builds a failing case whose node3 drops its output contract (emits schema-invalid
// output), and whose downstream reflect node then produces the wrong end-to-end answer. Every span
// is status OK — the fault is a CONTENT contract violation, exactly the "silent" AI failure the
// discipline warns about, and it must still localize to node3.
func faultyCase(id string) FailingCase {
	tr := evalharness.Trace{
		NodeOutputs: map[string]json.RawMessage{
			"router":     json.RawMessage(`{"a":"branch"}`),
			faultyNodeID: json.RawMessage(`{"junk":"no a field"}`), // ← contract violation
			"reflect":    json.RawMessage(`{"a":"wrong"}`),
		},
	}
	tr.Trace = telemetry.Trace{
		Run: telemetry.RunContext{CaseID: id},
		Spans: []telemetry.Span{
			nodeSpan(id, "router", 0, 0.002, 100, false),
			nodeSpan(id, faultyNodeID, 1, 0.004, 200, false),
			nodeSpan(id, "reflect", 2, 0.003, 150, false),
		},
	}
	tr.Output = json.RawMessage(`{"a":"wrong"}`)
	tr.Failed = true
	return FailingCase{
		Case:  evalharness.Case{CaseID: id, WorkflowID: "wf-fault", Label: evalharness.LabelNone},
		Trace: tr,
	}
}

func testVariant() Variant {
	return Variant{
		VariantID:   "v-fault",
		ConfigHash:  "cfg" + hex60("fault"),
		EvalSetHash: hex60("evalset")[:64],
		WorkflowID:  "wf-fault",
	}
}

func hex60(seed string) string {
	const hexd = "0123456789abcdef"
	out := make([]byte, 64)
	for i := range out {
		out[i] = hexd[(int(seed[i%len(seed)])*7+i*3)%16]
	}
	return string(out)
}

// Task 1.4: on a workflow with a fault injected at exactly one node, per-node contribution names
// that node as the first-divergence node on the failing cases.
func TestAttribute_FirstDivergenceNamesInjectedFaultNode(t *testing.T) {
	ir := faultIR()
	cases := []FailingCase{faultyCase("c1"), faultyCase("c2"), faultyCase("c3")}

	got := Attribute(ir, testVariant(), cases)

	if got.NFailing != 3 {
		t.Fatalf("NFailing = %d, want 3", got.NFailing)
	}
	for _, ca := range got.Cases {
		if ca.FirstDivergenceNode != faultyNodeID {
			t.Errorf("case %s first-divergence = %q, want %q", ca.CaseID, ca.FirstDivergenceNode, faultyNodeID)
		}
		if ca.DivergenceKind != DivergenceContract {
			t.Errorf("case %s divergence kind = %q, want %q", ca.CaseID, ca.DivergenceKind, DivergenceContract)
		}
	}

	// The per-node aggregate must credit node3 with 100% of the first-divergence, and no other node.
	byNode := map[string]NodeAttribution{}
	for _, n := range got.Nodes {
		byNode[n.NodeID] = n
	}
	if fd := byNode[faultyNodeID].FirstDivergenceCount; fd != 3 {
		t.Errorf("node3 first-divergence count = %d, want 3", fd)
	}
	if fs := byNode[faultyNodeID].FailureShare; fs != 1.0 {
		t.Errorf("node3 failure share = %v, want 1.0", fs)
	}
	for _, other := range []string{"router", "reflect"} {
		if fd := byNode[other].FirstDivergenceCount; fd != 0 {
			t.Errorf("node %s first-divergence count = %d, want 0", other, fd)
		}
	}
}

// Task 1.3: attribution rows are keyed {variant, eval_set, config, node, case} and queryable per node
// / per case without re-running.
func TestAttribute_RowsKeyedAndQueryable(t *testing.T) {
	ir := faultIR()
	v := testVariant()
	cases := []FailingCase{faultyCase("c1"), faultyCase("c2")}
	got := Attribute(ir, v, cases)

	// Every row carries the full key.
	for _, r := range got.Rows {
		if r.VariantID != v.VariantID || r.EvalSetHash != v.EvalSetHash || r.ConfigHash != v.ConfigHash {
			t.Fatalf("row missing variant/eval-set/config key: %+v", r)
		}
		if r.NodeID == "" || r.CaseID == "" {
			t.Fatalf("row missing node/case key: %+v", r)
		}
	}

	// "Per-case, per-node attribution is a query" — filter rows in memory rather than re-running.
	query := func(node, caseID string) (AttributionRow, bool) {
		for _, r := range got.Rows {
			if r.NodeID == node && r.CaseID == caseID {
				return r, true
			}
		}
		return AttributionRow{}, false
	}
	r, ok := query(faultyNodeID, "c1")
	if !ok || !r.FirstDivergence || r.ContribSuccess != 1 {
		t.Fatalf("query(node3, c1) = %+v, ok=%v; want first-divergence with ContribSuccess=1", r, ok)
	}
	r, ok = query("router", "c1")
	if !ok || r.FirstDivergence || r.ContribSuccess != 0 {
		t.Fatalf("query(router, c1) = %+v, ok=%v; want non-divergence with ContribSuccess=0", r, ok)
	}
}

// Determinism: the same failing set yields byte-identical rows regardless of input order.
func TestAttribute_Deterministic(t *testing.T) {
	ir := faultIR()
	v := testVariant()
	a := Attribute(ir, v, []FailingCase{faultyCase("c1"), faultyCase("c2"), faultyCase("c3")})
	b := Attribute(ir, v, []FailingCase{faultyCase("c3"), faultyCase("c1"), faultyCase("c2")})
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("attribution not deterministic under input reordering:\n a=%s\n b=%s", ja, jb)
	}
}

// Reference-mismatch path: a node with no output contract, whose output merely differs from a
// success trace, is localized as reference_mismatch (design Q1 second clause).
func TestFirstDivergence_ReferenceMismatch(t *testing.T) {
	// IR with no output schemas → no contract to violate.
	ir := &discovery.IR{
		Workflow: discovery.IRWorkflow{ID: "wf-ref"},
		Nodes: []discovery.IRNode{
			{NodeID: "a"}, {NodeID: "b"}, {NodeID: "c"},
		},
	}
	mk := func(a, b, c string, spansFailed bool) evalharness.Trace {
		tr := evalharness.Trace{NodeOutputs: map[string]json.RawMessage{
			"a": json.RawMessage(a), "b": json.RawMessage(b), "c": json.RawMessage(c),
		}}
		tr.Trace = telemetry.Trace{Spans: []telemetry.Span{
			nodeSpan("x", "a", 0, 0.001, 10, false),
			nodeSpan("x", "b", 1, 0.001, 10, spansFailed),
			nodeSpan("x", "c", 2, 0.001, 10, false),
		}}
		return tr
	}
	success := mk(`{"v":1}`, `{"v":2}`, `{"v":3}`, false)
	// node b's output diverges from the success trace; a matches.
	fail := mk(`{"v":1}`, `{"v":99}`, `{"v":3}`, false)
	fc := FailingCase{Case: evalharness.Case{CaseID: "x"}, Trace: fail, SuccessTrace: &success}

	node, kind := FirstDivergence(ir, fc)
	if node != "b" || kind != DivergenceReference {
		t.Fatalf("firstDivergence = (%q,%q), want (b, reference_mismatch)", node, kind)
	}
}
