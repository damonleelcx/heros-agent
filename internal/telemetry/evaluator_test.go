package telemetry

import (
	"context"
	"testing"
	"time"
)

// buildTraceFromRun runs the fixture and returns the run's trace (node spans) for an evaluator to score.
func buildTraceFromRun(t *testing.T) (*Collector, *MemEvalStore, RunContext, Trace) {
	t.Helper()
	spans := NewMemSpanStore(0)
	eval := NewMemEvalStore()
	col := NewCollector(CollectorConfig{Spans: spans, TSDB: NewMemTSDB(0), Eval: eval})
	t.Cleanup(col.Close)
	gw, inst, entry := testRigWithSink(t, col)
	rc := runFixture(t, gw, inst, entry, []string{"n_a", "n_b", "n_c"})
	col.Flush()
	trace := Trace{Run: rc, Spans: spans.Trace(rc.RunID)}
	return col, eval, rc, trace
}

// Task 7.2: the built-in reference evaluator runs over a completed run's trace and emits quality events
// that carry the full seven-tag set and land in the eval-results store, keyed by config_hash.
func TestSection7_ReferenceEvaluatorProvesTheSeam(t *testing.T) {
	col, eval, rc, trace := buildTraceFromRun(t)

	reg := NewRegistry()
	if err := reg.Register(NewReferenceEvaluator()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	RunEvaluators(context.Background(), reg, rc, trace, col)
	col.Flush()

	rows, err := eval.ByConfigHash(context.Background(), rc.ConfigHash)
	if err != nil {
		t.Fatalf("ByConfigHash: %v", err)
	}
	if len(rows) != 3 { // one per node
		t.Fatalf("reference evaluator produced %d eval rows, want 3 (one per node)", len(rows))
	}
	for _, ev := range rows {
		if err := ev.Validate(); err != nil {
			t.Errorf("eval event is not fully tagged: %v", err)
		}
		if ev.EvaluatorName != ReferenceEvaluatorName {
			t.Errorf("eval row not attributed to the reference evaluator: %q", ev.EvaluatorName)
		}
		if ev.ConfigHash != rc.ConfigHash {
			t.Errorf("eval row not keyed by config_hash")
		}
	}
}

// Task 7.1: the registry supports built-in + user-defined evaluators (Skill-Registry pattern) and
// rejects a duplicate name; a user-defined evaluator flows the same path without re-plumbing.
func TestSection7_RegistryBuiltInAndUserDefined(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(NewReferenceEvaluator()); err != nil {
		t.Fatalf("register built-in: %v", err)
	}
	// A user-defined evaluator registers through the same interface.
	if err := reg.Register(&constEvaluator{name: "user_scorer", metric: "user_score", value: 0.75}); err != nil {
		t.Fatalf("register user-defined: %v", err)
	}
	if len(reg.Evaluators()) != 2 {
		t.Errorf("registry has %d evaluators, want 2", len(reg.Evaluators()))
	}
	// A duplicate name is rejected, not silently overwritten.
	if err := reg.Register(&constEvaluator{name: "user_scorer"}); err == nil {
		t.Error("registering a duplicate evaluator name was allowed")
	}

	// The user-defined evaluator's events land in the same store via the same path.
	col, eval, rc, trace := buildTraceFromRun(t)
	RunEvaluators(context.Background(), reg, rc, trace, col)
	col.Flush()
	rows, _ := eval.ByConfigHash(context.Background(), rc.ConfigHash)
	var sawUser bool
	for _, ev := range rows {
		if ev.EvaluatorName == "user_scorer" {
			sawUser = true
			if err := ev.Validate(); err != nil {
				t.Errorf("user-defined evaluator emitted an under-tagged event: %v", err)
			}
		}
	}
	if !sawUser {
		t.Error("the user-defined evaluator's events did not flow through the substrate")
	}
}

// Task 7.1 (by-construction tagging): an evaluator that tries to emit an under-tagged event is rejected
// at the SAME emission boundary as operational metrics — it reaches no store.
func TestSection7_EvaluatorCannotEmitUnderTaggedEvent(t *testing.T) {
	col, eval, rc, trace := buildTraceFromRun(t)
	reg := NewRegistry()
	// A broken evaluator that strips config_hash from its events.
	_ = reg.Register(&brokenEvaluator{})
	RunEvaluators(context.Background(), reg, rc, trace, col)
	col.Flush()

	rows, _ := eval.ByConfigHash(context.Background(), rc.ConfigHash)
	for _, ev := range rows {
		if ev.EvaluatorName == "broken" {
			t.Error("an under-tagged evaluator event reached the store")
		}
	}
	if col.GateStats().Rejected == 0 {
		t.Error("the gate did not reject the broken evaluator's under-tagged events")
	}
}

// Task 7.3: the interface is versioned (stable major) so P4 binds without re-plumbing.
func TestSection7_InterfaceIsVersioned(t *testing.T) {
	if EvaluatorInterfaceVersion == "" {
		t.Fatal("the evaluator interface must be versioned")
	}
	// A trivial shape check: MAJOR.MINOR.PATCH.
	if EvaluatorInterfaceVersion[0] != '1' {
		t.Errorf("interface version = %q; P4 binds against major 1", EvaluatorInterfaceVersion)
	}
}

// Task 7.4: the tag set supports every P4/P4.5 slice — per-variant, per-node, per-case, per-seed. A
// missing tag here is an un-answerable question later, so this asserts each axis is present and
// group-able on the emitted eval events.
func TestSection7_TagSetSupportsAllSlices(t *testing.T) {
	col, eval, rc, trace := buildTraceFromRun(t)
	reg := NewRegistry()
	_ = reg.Register(NewReferenceEvaluator())
	RunEvaluators(context.Background(), reg, rc, trace, col)
	col.Flush()

	rows, _ := eval.ByConfigHash(context.Background(), rc.ConfigHash)
	if len(rows) == 0 {
		t.Fatal("no eval rows to slice")
	}
	// Each of the four downstream slice axes is answerable because each tag is present and non-empty.
	byVariant := map[string]int{}
	byNode := map[string]int{}
	byCase := map[string]int{}
	bySeed := map[int64]int{}
	for _, ev := range rows {
		if ev.VariantID == "" || ev.NodeID == "" || ev.CaseID == "" || ev.Seed == nil {
			t.Fatalf("an eval row is missing a slice tag: %+v", ev.Event)
		}
		byVariant[ev.VariantID]++
		byNode[ev.NodeID]++
		byCase[ev.CaseID]++
		bySeed[*ev.Seed]++
	}
	if len(byVariant) == 0 || len(byNode) < 3 || len(byCase) == 0 || len(bySeed) == 0 {
		t.Errorf("slice axes not fully populated: variants=%d nodes=%d cases=%d seeds=%d",
			len(byVariant), len(byNode), len(byCase), len(bySeed))
	}
}

// ── test evaluators ──

// constEvaluator emits one constant-valued quality event per node — a stand-in user-defined evaluator.
type constEvaluator struct {
	name   string
	metric string
	value  float64
}

func (c *constEvaluator) Name() string { return c.name }
func (c *constEvaluator) Evaluate(rc RunContext, trace Trace) []QualityMetricEvent {
	var out []QualityMetricEvent
	for _, ns := range trace.NodeSpans() {
		nodeID := attrStr(ns.Attributes, AttrNodeID)
		out = append(out, rc.WithNode(nodeID, 0).Quality(c.name, c.metric, c.value, UnitCount, time.Now()))
	}
	return out
}

// brokenEvaluator strips config_hash — the under-tagged event the gate must reject.
type brokenEvaluator struct{}

func (brokenEvaluator) Name() string { return "broken" }
func (brokenEvaluator) Evaluate(rc RunContext, trace Trace) []QualityMetricEvent {
	ev := rc.WithNode("n_a", 0).Quality("broken", "broken_metric", 1, UnitCount, time.Now())
	ev.ConfigHash = "" // sabotage the tag set
	return []QualityMetricEvent{ev}
}
