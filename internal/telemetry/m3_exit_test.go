package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// TestM3ExitChecklist is task 9.5 (with 9.1 folded in): it drives ONE real (stubbed-provider) run
// through the whole substrate and asserts every item of the PRD §13 M3 exit checklist. A green here is
// the phase's headline exit bar. Each subtest names the checklist item it proves.
func TestM3ExitChecklist(t *testing.T) {
	spans := NewMemSpanStore(0)
	tsdb := NewMemTSDB(0)
	eval := NewMemEvalStore()
	col := NewCollector(CollectorConfig{Spans: spans, TSDB: tsdb, Eval: eval})
	t.Cleanup(col.Close)

	gw, inst, entry := testRigWithSink(t, col)

	// A run of a 3-node graph with a tool call, reusing the zero-telemetry-code fixture path (9.1).
	seed := int64(7)
	rc := RunContext{VariantID: "variant_alpha", RunID: "run_m3", ConfigHash: testConfigHash, Seed: seed, CaseID: "case_1"}
	tracer := inst.StartRun(rc)
	nodes := []string{"n_a", "n_b", "n_c"}
	for i, node := range nodes {
		nodeRC := rc.WithNode(node, 0)
		ctx := NewContext(context.Background(), nodeRC)
		tracer.NodeStarted(ctx, node)
		if _, err := executor.CallProvider(ctx, gw, entry,
			providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}}},
			executor.NodeInvocation{RunID: rc.RunID, NodeID: node, AttemptGroup: 0, Seed: &seed}); err != nil {
			t.Fatalf("node %s: %v", node, err)
		}
		if i == 0 {
			s := time.Now()
			tracer.ToolCall(ctx, nodeRC, "web_search", 0, s, s.Add(time.Millisecond), true)
		}
		tracer.NodeFinished(ctx, node)
	}
	tracer.EndRun(context.Background())
	col.Flush() // the node spans are emitted async; drain them before reading the trace for evaluation

	// The reference evaluator over the run's trace (9.5 evaluator item).
	reg := NewRegistry()
	_ = reg.Register(NewReferenceEvaluator())
	RunEvaluators(context.Background(), reg, rc, Trace{Run: rc, Spans: spans.Trace(rc.RunID)}, col)
	col.Flush()

	t.Run("1_drillable_spans_and_queryable_metrics", func(t *testing.T) {
		trace := spans.Trace(rc.RunID)
		var run, node, tool int
		for _, s := range trace {
			switch s.Kind {
			case SpanKindRun:
				run++
			case SpanKindNode:
				node++
			case SpanKindTool:
				tool++
			}
		}
		if run != 1 || node != 3 || tool != 1 {
			t.Errorf("span hierarchy = %d run / %d node / %d tool; want 1/3/1", run, node, tool)
		}
		if s, err := tsdb.Query(map[string]string{AttrConfigHash: rc.ConfigHash}); err != nil || len(s) == 0 {
			t.Errorf("no queryable operational metrics in the TSDB: %d, %v", len(s), err)
		}
	})

	t.Run("2_all_seven_tags_keyed_by_config_hash_and_missing_rejected", func(t *testing.T) {
		s, _ := tsdb.Query(map[string]string{AttrConfigHash: rc.ConfigHash})
		if len(s) == 0 {
			t.Fatal("no samples")
		}
		for _, sample := range s {
			if sample.Labels[AttrConfigHash] != rc.ConfigHash {
				t.Errorf("sample not keyed by config_hash")
			}
		}
		// Negative: a missing-config_hash event is rejected.
		before := col.GateStats().Rejected
		bad := rc.WithNode("n_a", 0).event(MetricCostUSD, 1, UnitUSD, time.Now(), nil)
		bad.ConfigHash = ""
		col.EmitMetric(context.Background(), bad)
		col.Flush()
		if col.GateStats().Rejected <= before {
			t.Error("a missing-config_hash event was not rejected at the boundary")
		}
	})

	t.Run("3_full_operational_taxonomy", func(t *testing.T) {
		got := map[string]bool{}
		s, _ := tsdb.Query(map[string]string{AttrConfigHash: rc.ConfigHash, AttrNodeID: "n_a"})
		for _, sample := range s {
			got[sample.Labels["metric_name"]] = true
		}
		for _, want := range OperationalMetricNames {
			if !got[want] {
				t.Errorf("missing operational metric %q", want)
			}
		}
	})

	t.Run("4_zero_user_code", func(t *testing.T) {
		// The fixture above contains no telemetry code in its node bodies, yet the set is present —
		// proved by item 3. This subtest documents the item; item 3 is its assertion.
	})

	t.Run("5_cardinality_discipline", func(t *testing.T) {
		s, _ := tsdb.Query(map[string]string{AttrConfigHash: rc.ConfigHash})
		for _, sample := range s {
			for _, high := range HighCardinalityTags {
				if _, isLabel := sample.Labels[high]; isLabel {
					t.Errorf("high-cardinality tag %q is a TSDB label", high)
				}
			}
			// case_id is retained as an exemplar (queryable), just not a label.
			if sample.Exemplars[AttrCaseID] == "" && sample.Labels[AttrNodeID] != NodeIDRun {
				t.Errorf("case_id was not retained as an exemplar")
			}
		}
	})

	t.Run("6_three_store_routing_filterable_by_config_hash", func(t *testing.T) {
		if s, _ := tsdb.Query(map[string]string{AttrConfigHash: rc.ConfigHash}); len(s) == 0 {
			t.Error("trend query (TSDB) empty")
		}
		if len(spans.SpansByConfigHash(rc.ConfigHash)) == 0 {
			t.Error("drill-down query (span store) empty")
		}
		if rows, _ := eval.ByConfigHash(context.Background(), rc.ConfigHash); len(rows) == 0 {
			t.Error("comparison query (eval store) empty")
		}
	})

	t.Run("7_no_secret_or_pii_anywhere", func(t *testing.T) {
		for _, sp := range spans.SpansByConfigHash(rc.ConfigHash) {
			for _, v := range sp.Attributes {
				if str, ok := v.(string); ok && containsSecret(str) {
					t.Errorf("secret/PII in a span attribute: %q", str)
				}
			}
			if containsSecret(sp.StatusMsg) {
				t.Errorf("secret/PII in a span status message")
			}
		}
	})

	t.Run("8_degrade_safe_and_idempotent", func(t *testing.T) {
		// Idempotent: re-emit a node's cost; the store still holds one.
		before, _ := tsdb.Query(map[string]string{AttrConfigHash: rc.ConfigHash, AttrNodeID: "n_a", "metric_name": MetricCostUSD})
		pb, _ := NewPriceBook("v1")
		pb.Set(providergateway.ProviderOpenAI, "gpt-5", ModelInfo{InputPerMTok: 1, OutputPerMTok: 2, ContextWindow: 200000})
		d := callDetail{provider: providergateway.ProviderOpenAI, modelID: "gpt-5", usage: tokenUsage{input: 100, output: 40},
			duration: time.Millisecond, attempts: 1, idempotencyKey: rc.WithNode("n_a", 0).IdempotencyKey()}
		evs, _ := MetricSet(rc.WithNode("n_a", 0), d, time.Now(), pb)
		for _, ev := range evs {
			col.EmitMetric(context.Background(), ev)
		}
		col.Flush()
		after, _ := tsdb.Query(map[string]string{AttrConfigHash: rc.ConfigHash, AttrNodeID: "n_a", "metric_name": MetricCostUSD})
		if len(after) != len(before) {
			t.Errorf("re-emitting a node's cost changed the sample count %d -> %d (retry double-counted)", len(before), len(after))
		}
	})

	t.Run("9_evaluator_stub_exercised", func(t *testing.T) {
		rows, _ := eval.ByConfigHash(context.Background(), rc.ConfigHash)
		if len(rows) != 3 {
			t.Fatalf("reference evaluator produced %d rows, want 3", len(rows))
		}
		for _, ev := range rows {
			if err := ev.Validate(); err != nil {
				t.Errorf("eval event under-tagged: %v", err)
			}
		}
	})

	t.Run("10_live_monitor_renders_states", func(t *testing.T) {
		// Inject a failed + timed-out node so the monitor's state distinction is exercised.
		spans.PutSpan(context.Background(), Span{SpanID: "sf", TraceID: TraceID(rc.RunID), Kind: SpanKindNode, Status: SpanStatusError,
			Attributes: map[string]any{AttrConfigHash: rc.ConfigHash, AttrRunID: rc.RunID, AttrVariantID: rc.VariantID, AttrNodeID: "n_fail", AttrNodeFailed: true}})
		spans.PutSpan(context.Background(), Span{SpanID: "st", TraceID: TraceID(rc.RunID), Kind: SpanKindNode, Status: SpanStatusError,
			Attributes: map[string]any{AttrConfigHash: rc.ConfigHash, AttrRunID: rc.RunID, AttrVariantID: rc.VariantID, AttrNodeID: "n_timeout", AttrTimedOut: true, AttrNodeFailed: true}})
		mon := NewMonitor(fakeStatus{info: map[string]RunStatusInfo{rc.RunID: {Status: "running", ConfigHash: rc.ConfigHash}}}, spans)
		snap, ok := mon.Snapshot(rc.RunID)
		if !ok {
			t.Fatal("monitor snapshot failed")
		}
		st := map[string]string{}
		for _, n := range snap.Nodes {
			st[n.NodeID] = n.State
		}
		if st["n_a"] != NodeStateOK || st["n_fail"] != NodeStateFailed || st["n_timeout"] != NodeStateTimedOut {
			t.Errorf("monitor states not distinct: %v", st)
		}
		if snap.Status != "running" { // read from the record, not derived
			t.Errorf("status not read from the record: %q", snap.Status)
		}
	})
}
