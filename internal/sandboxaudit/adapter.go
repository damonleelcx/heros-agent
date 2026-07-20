// Package sandboxaudit bridges the sandbox and broker audit streams into the P2.5 telemetry substrate
// (P3 tasks 5.2, 5.3). Every denied action (egress block, credential-read attempt, resource breach,
// filesystem-scope violation), every isolate lifecycle transition, every brokered-call decision, and
// every skill-contract failure becomes a metric event carrying the P0 tag set — so a run's
// sandbox-denial rate, tool-error rate, and context-window utilization all land in the same collector
// P2.5 already fans out to the three stores.
//
// It keeps the low-level sandbox and broker packages free of a telemetry dependency: they emit typed
// audit values; this adapter turns them into tagged events. No secret value is ever placed on an event
// — sandbox/broker records are secret-free by construction, and the collector's scrubber is the final
// backstop.
package sandboxaudit

import (
	"context"
	"time"

	"github.com/heros-foreal/agentd/internal/broker"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/sandbox"
	"github.com/heros-foreal/agentd/internal/skillgate"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// Emitter is the collector subset this adapter needs. *telemetry.Collector satisfies it.
type Emitter interface {
	EmitMetric(ctx context.Context, ev metricevent.Event)
}

// Adapter carries the run's P0 context (variant_id, config_hash, seed, case_id) and stamps it onto
// every audit-derived event. node_id and run_id come from the individual event so a per-node denial is
// attributable to its node.
type Adapter struct {
	col  Emitter
	base telemetry.P0Tags
}

// New builds an Adapter. base supplies the tags an audit event does not carry itself (variant_id,
// config_hash, seed, case_id, and a default run_id).
func New(col Emitter, base telemetry.P0Tags) *Adapter {
	return &Adapter{col: col, base: base}
}

// SandboxSink returns a sandbox.AuditSink that emits denial + lifecycle events.
func (a *Adapter) SandboxSink() sandbox.AuditSink { return sandboxSink{a} }

// BrokerAuditor returns a broker.Auditor that emits a denial event for every denied brokered call.
func (a *Adapter) BrokerAuditor() broker.Auditor { return brokerAuditor{a} }

// RecordSkillFailure emits a tool-error event for a skill-contract failure (task 5.3: tool-error rate).
func (a *Adapter) RecordSkillFailure(nodeID string, ce *skillgate.ContractError) {
	if ce == nil {
		return
	}
	a.emit(nodeID, telemetry.MetricToolError, 1, telemetry.UnitCount, map[string]any{
		telemetry.AttrToolReason: string(ce.Kind),
		telemetry.AttrSkillName:  ce.Skill,
	})
}

type sandboxSink struct{ a *Adapter }

func (s sandboxSink) Record(e sandbox.Event) {
	switch e.Kind {
	case sandbox.EventDenial:
		s.a.emit(e.NodeID, telemetry.MetricSandboxDenial, 1, telemetry.UnitCount, map[string]any{
			telemetry.AttrDenialKind: string(e.Denial),
		})
	case sandbox.EventLifecycle:
		s.a.emit(e.NodeID, telemetry.MetricSandboxLifecycle, 1, telemetry.UnitCount, map[string]any{
			telemetry.AttrPhase: string(e.Phase),
		})
	}
}

type brokerAuditor struct{ a *Adapter }

func (b brokerAuditor) Record(r broker.AuditRecord) {
	// A denied brokered call is a sandbox denial (broker egress bypass attempt); an allowed call is not
	// an anomaly and does not inflate the denial-rate series.
	if !r.Allowed {
		b.a.emit(r.NodeID, telemetry.MetricSandboxDenial, 1, telemetry.UnitCount, map[string]any{
			telemetry.AttrDenialKind: string(sandbox.DenyBroker),
		})
	}
}

// emit builds a P0-tagged metric event and hands it to the collector. node_id falls back to the
// run-scoped sentinel when an audit event is not attributable to a single node (e.g. a broker call with
// no node context), so the seven-tag contract's minLength-1 node_id still holds.
func (a *Adapter) emit(nodeID, name string, value float64, unit string, dims map[string]any) {
	if a.col == nil {
		return
	}
	if nodeID == "" {
		nodeID = telemetry.NodeIDRun
	}
	runID := a.base.RunID
	if runID == "" {
		runID = telemetry.NodeIDRun
	}
	ts := a.base.Timestamp
	if ts == "" {
		ts = time.Now().UTC().Format(time.RFC3339Nano)
	}
	seed := a.base.Seed
	v := value
	a.col.EmitMetric(context.Background(), metricevent.Event{
		SchemaVersion: metricevent.SchemaVersion,
		VariantID:     a.base.VariantID,
		RunID:         runID,
		NodeID:        nodeID,
		CaseID:        a.base.CaseID,
		Seed:          &seed,
		Timestamp:     ts,
		ConfigHash:    a.base.ConfigHash,
		MetricName:    name,
		Value:         &v,
		Unit:          unit,
		Dimensions:    dims,
	})
}
