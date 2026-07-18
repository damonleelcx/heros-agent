package telemetry

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// evaluator.go is Decision 9: the evaluator-plugin seam, STUBBED here so P4 slots real quality metrics
// in without re-plumbing collection, tagging, or storage. The interface and the reference evaluator are
// the whole of what this phase ships; exact-match / schema / LLM-judge evaluators and the statistical
// layer are P4.

// EvaluatorInterfaceVersion is the stable major P4 binds against (task 7.3). A P4 evaluator compiled
// against this major is guaranteed the same collection/tagging/storage path. Bumping the MAJOR is the
// only signal that a re-plumb is required; MINOR/patch add without breaking.
const EvaluatorInterfaceVersion = "1.0.0"

// Trace is a completed run's telemetry as an evaluator consumes it: the spans (run -> node -> tool) and
// the run-scoped coordinates. An evaluator reads the trace and scores it; it never reaches into a store
// or the gateway.
type Trace struct {
	Run   RunContext
	Spans []Span
}

// NodeSpans returns the node-execution spans of the trace, in emission order — the unit most evaluators
// score (one quality event per node).
func (t Trace) NodeSpans() []Span {
	var out []Span
	for _, s := range t.Spans {
		if s.Kind == SpanKindNode {
			out = append(out, s)
		}
	}
	return out
}

// Evaluator is the plugin contract (design.md §8.4). Evaluate is handed the RunContext — which CARRIES
// the seven tags — so an evaluator's emitted events are fully tagged BY CONSTRUCTION: the evaluator
// supplies a metric name, value, and unit; it does not, and cannot, supply (or forget) the tags. This
// is the property the stub exists to freeze: "an evaluator cannot emit an under-tagged event."
type Evaluator interface {
	Name() string
	Evaluate(rc RunContext, trace Trace) []QualityMetricEvent
}

// Registry holds built-in and user-defined evaluators, mirroring the Skill Registry pattern (a name ->
// implementation map with duplicate-name rejection). A user-defined evaluator registered here flows
// through the exact same tagging/cardinality/storage path as a built-in one (task 7.1's third scenario).
type Registry struct {
	mu    sync.RWMutex
	evals map[string]Evaluator
}

// NewRegistry builds an empty evaluator registry.
func NewRegistry() *Registry { return &Registry{evals: map[string]Evaluator{}} }

// Register adds an evaluator. A duplicate name is rejected rather than silently overwritten — two
// evaluators answering to one name is exactly the ambiguity the Skill Registry's content-addressing
// avoids, and here the name is the key P4 and the eval store attribute rows by.
func (r *Registry) Register(e Evaluator) error {
	if e == nil {
		return fmt.Errorf("telemetry: Register(nil evaluator)")
	}
	name := e.Name()
	if name == "" {
		return fmt.Errorf("telemetry: an evaluator must have a name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.evals[name]; dup {
		return fmt.Errorf("telemetry: evaluator %q is already registered", name)
	}
	r.evals[name] = e
	return nil
}

// Evaluators returns the registered evaluators (order unspecified).
func (r *Registry) Evaluators() []Evaluator {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Evaluator, 0, len(r.evals))
	for _, e := range r.evals {
		out = append(out, e)
	}
	return out
}

// Get returns a registered evaluator by name.
func (r *Registry) Get(name string) (Evaluator, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.evals[name]
	return e, ok
}

// EvalEmitter is where evaluator output goes — the Collector's eval path (routes to Postgres). An
// interface so the harness does not depend on the concrete Collector.
type EvalEmitter interface {
	EmitEval(ctx context.Context, ev QualityMetricEvent)
}

// RunEvaluators runs every registered evaluator over a completed run's trace and emits their events
// through the SAME collector path operational metrics take (gate -> scrub -> eval store). This is the
// seam end to end: register -> evaluate -> tagged events -> eval-results store, with no evaluator able
// to bypass the tagging or storage discipline.
func RunEvaluators(ctx context.Context, reg *Registry, rc RunContext, trace Trace, sink EvalEmitter) {
	for _, e := range reg.Evaluators() {
		for _, ev := range e.Evaluate(rc, trace) {
			// Stamp the evaluator name if the evaluator did not (a belt-and-braces: the emitter, not the
			// plugin, is the authority on which evaluator produced a row).
			if ev.EvaluatorName == "" {
				ev.EvaluatorName = e.Name()
			}
			sink.EmitEval(ctx, ev)
		}
	}
}

// Quality builds a fully-tagged QualityMetricEvent from a RunContext — the ONLY intended way an
// evaluator produces an event. Because the tags come from the RunContext (not from the evaluator's
// arguments), an event built this way is tagged by construction; the gate rejects anything that somehow
// is not (defense in depth). Evaluators call rc.WithNode(...).Quality(...) for per-node attribution.
func (rc RunContext) Quality(evaluatorName, metricName string, value float64, unit string, now time.Time) QualityMetricEvent {
	return QualityMetricEvent{
		Event:         rc.event(metricName, value, unit, now, nil),
		EvaluatorName: evaluatorName,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Reference evaluator (task 7.2) — the trivial built-in that proves the seam.
// ─────────────────────────────────────────────────────────────────────────────

// ReferenceEvaluatorName is the built-in reference evaluator's name.
const ReferenceEvaluatorName = "reference"

// MetricReferenceNodePresent is the trivial quality metric the reference evaluator emits: 1 for every
// node that executed. It is intentionally content-free — its only job is to prove that a registered
// evaluator's events carry the seven tags and land in the eval store. Real quality metrics are P4.
const MetricReferenceNodePresent = "reference_node_present"

// ReferenceEvaluator is the built-in that exercises the plugin seam end to end. It emits one quality
// event per node span, each carrying the full seven-tag set from the RunContext.
type ReferenceEvaluator struct {
	now func() time.Time
}

// NewReferenceEvaluator builds the reference evaluator.
func NewReferenceEvaluator() *ReferenceEvaluator {
	return &ReferenceEvaluator{now: time.Now}
}

// Name identifies the evaluator (the eval_result.evaluator_name value).
func (e *ReferenceEvaluator) Name() string { return ReferenceEvaluatorName }

// Evaluate emits one reference quality event per node in the trace, fully tagged by construction.
func (e *ReferenceEvaluator) Evaluate(rc RunContext, trace Trace) []QualityMetricEvent {
	now := e.now()
	var out []QualityMetricEvent
	for _, ns := range trace.NodeSpans() {
		nodeID := attrStr(ns.Attributes, AttrNodeID)
		attempt := 0
		if a, ok := ns.Attributes[AttrAttemptGroup].(int); ok {
			attempt = a
		}
		nodeRC := rc.WithNode(nodeID, attempt)
		out = append(out, nodeRC.Quality(e.Name(), MetricReferenceNodePresent, 1, UnitCount, now))
	}
	return out
}
