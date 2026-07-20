package sandboxaudit

import (
	"context"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/broker"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/sandbox"
	"github.com/heros-foreal/agentd/internal/skillgate"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

type recEmitter struct {
	mu sync.Mutex
	ev []metricevent.Event
}

func (r *recEmitter) EmitMetric(_ context.Context, e metricevent.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ev = append(r.ev, e)
}
func (r *recEmitter) byName(name string) *metricevent.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.ev {
		if r.ev[i].MetricName == name {
			return &r.ev[i]
		}
	}
	return nil
}

func base() telemetry.P0Tags {
	return telemetry.P0Tags{
		VariantID:  "var_1",
		RunID:      "run_1",
		CaseID:     "case_1",
		Seed:       3,
		ConfigHash: "b6d81b360a5672d80c27430f39153e2c04e9a1a5b3d3a1c0e2b8f7a6c5d4e3f2",
	}
}

// Task 5.2: a denial event is emitted with the full P0 tag set and a denial_kind dimension, secret-free.
func TestSandboxDenialEmittedWithP0Tags(t *testing.T) {
	em := &recEmitter{}
	a := New(em, base())
	a.SandboxSink().Record(sandbox.Event{Kind: sandbox.EventDenial, NodeID: "node_1", Denial: sandbox.DenyEgress, Reason: "blocked"})

	ev := em.byName(telemetry.MetricSandboxDenial)
	if ev == nil {
		t.Fatal("no sandbox_denial metric emitted")
	}
	if err := ev.Validate(); err != nil {
		t.Errorf("denial event fails the P0 tag contract: %v", err)
	}
	if ev.Dimensions[telemetry.AttrDenialKind] != string(sandbox.DenyEgress) {
		t.Errorf("denial_kind dimension wrong: %+v", ev.Dimensions)
	}
	if ev.NodeID != "node_1" {
		t.Errorf("node_id = %q", ev.NodeID)
	}
}

// Task 5.2: lifecycle transitions are audited as metric events.
func TestLifecycleEmitted(t *testing.T) {
	em := &recEmitter{}
	a := New(em, base())
	a.SandboxSink().Record(sandbox.Event{Kind: sandbox.EventLifecycle, NodeID: "node_1", Phase: sandbox.PhaseCreateFailed})
	ev := em.byName(telemetry.MetricSandboxLifecycle)
	if ev == nil || ev.Dimensions[telemetry.AttrPhase] != string(sandbox.PhaseCreateFailed) {
		t.Fatalf("lifecycle event missing/incorrect: %+v", ev)
	}
}

// Task 5.3: a denied brokered call inflates the sandbox-denial series; an allowed one does not.
func TestBrokerDenialEmitted(t *testing.T) {
	em := &recEmitter{}
	a := New(em, base())
	ba := a.BrokerAuditor()
	ba.Record(broker.AuditRecord{NodeID: "node_1", Op: "http", Allowed: false, Reason: "not allowlisted"})
	ba.Record(broker.AuditRecord{NodeID: "node_1", Op: "http", Allowed: true, Reason: "ok"})

	em.mu.Lock()
	n := 0
	for _, e := range em.ev {
		if e.MetricName == telemetry.MetricSandboxDenial {
			n++
		}
	}
	em.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected exactly one denial from the denied call, got %d", n)
	}
}

// Task 5.3: a skill-contract failure emits a tool-error event tagged with the skill + reason.
func TestSkillFailureEmitsToolError(t *testing.T) {
	em := &recEmitter{}
	a := New(em, base())
	a.RecordSkillFailure("node_1", &skillgate.ContractError{Skill: "search", Kind: skillgate.FailureInputSchema, Field: "top_k"})
	ev := em.byName(telemetry.MetricToolError)
	if ev == nil {
		t.Fatal("no tool_error metric emitted")
	}
	if ev.Dimensions[telemetry.AttrSkillName] != "search" || ev.Dimensions[telemetry.AttrToolReason] != string(skillgate.FailureInputSchema) {
		t.Errorf("tool_error dimensions wrong: %+v", ev.Dimensions)
	}
}

// End-to-end through the real collector: the events pass the emission-boundary gate (proving the P0
// contract) and land in the TSDB.
func TestFlowsThroughRealCollector(t *testing.T) {
	tsdb := telemetry.NewMemTSDB(0)
	col := telemetry.NewCollector(telemetry.CollectorConfig{Spans: telemetry.NewMemSpanStore(0), TSDB: tsdb, Eval: telemetry.NewMemEvalStore()})
	defer col.Close()

	a := New(col, base())
	a.SandboxSink().Record(sandbox.Event{Kind: sandbox.EventDenial, NodeID: "node_1", Denial: sandbox.DenyResource, Reason: "cpu"})
	col.Flush()

	samples, err := tsdb.Query(map[string]string{"metric_name": telemetry.MetricSandboxDenial})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(samples) == 0 {
		t.Fatal("denial event did not reach the TSDB via the collector (gate may have rejected it)")
	}
}
