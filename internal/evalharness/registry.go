package evalharness

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// Registry holds built-in and user-registered evaluators. It mirrors the skill/context-policy
// registry pattern already in the tree: a name -> implementation map with duplicate-name rejection
// and validation AT REGISTRATION, so a metric that cannot possibly produce an honest number is
// refused before it can appear on a board rather than after.
type Registry struct {
	mu    sync.RWMutex
	evals map[string]Evaluator
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry { return &Registry{evals: map[string]Evaluator{}} }

// NewBuiltinRegistry builds a registry pre-loaded with the four built-in oracle evaluators
// (exact-match, JSON-schema validity, regex) plus the standard trace-derived family. The LLM judge
// is NOT included: it needs a model and a calibration record, both of which are caller-supplied, and
// silently registering an uncalibrated judge is precisely what Decision 3 forbids.
func NewBuiltinRegistry() *Registry {
	r := NewRegistry()
	for _, e := range Builtins() {
		if err := r.Register(e); err != nil {
			// Builtins() returns constructed-by-us evaluators; a failure here is a programming
			// error in this package, not a runtime condition.
			panic(fmt.Sprintf("evalharness: built-in evaluator rejected: %v", err))
		}
	}
	return r
}

// Register adds an evaluator after validating everything that can be validated statically:
// non-empty name and metric, a sane declared range, and — for a pattern-scoped evaluator — that its
// metric actually belongs to every pattern's metric-set it claims. That last check is the one that
// matters: it makes the P3.5 metricSets table the single source of truth and turns a plug-in that
// disagrees with it into a registration failure instead of a wrong number on a leaderboard.
func (r *Registry) Register(e Evaluator) error {
	if e == nil {
		return fmt.Errorf("%w: Register(nil)", ErrInvalidEvaluator)
	}
	name := strings.TrimSpace(e.Name())
	if name == "" {
		return fmt.Errorf("%w: an evaluator must have a name", ErrInvalidEvaluator)
	}
	if strings.TrimSpace(e.Metric()) == "" {
		return fmt.Errorf("%w: evaluator %q must declare a metric name", ErrInvalidEvaluator, name)
	}
	if err := e.Range().Validate(); err != nil {
		return fmt.Errorf("evaluator %q: %w", name, err)
	}
	for _, p := range e.AdmissiblePatterns() {
		if !patternclassifier.InTaxonomy(p) {
			return fmt.Errorf("%w: evaluator %q declares pattern %q which is not in taxonomy %s",
				ErrInvalidEvaluator, name, p, patternclassifier.TaxonomyVersion)
		}
		if err := metricInPatternSet(e.Metric(), p); err != nil {
			return fmt.Errorf("%w: evaluator %q: %v", ErrInvalidEvaluator, name, err)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.evals[name]; dup {
		return fmt.Errorf("%w: evaluator %q is already registered", ErrInvalidEvaluator, name)
	}
	r.evals[name] = e
	return nil
}

// MetricFunc is the shape a user-defined custom metric is written as — the same signature the
// built-ins satisfy, so a custom metric is not a second-class citizen with a different code path.
type MetricFunc func(ctx context.Context, tr Trace, c Case, tgt Target) (float64, error)

// RegisterMetric registers a scoring function by name, the same way a skill is registered: the
// declared range and pattern admissibility are validated at registration and enforced on every value
// thereafter. This is the whole of what a user needs to add a domain metric — no harness change.
func (r *Registry) RegisterMetric(name, metric string, rng Range, patterns []patternclassifier.Pattern, fn MetricFunc) error {
	if fn == nil {
		return fmt.Errorf("%w: custom metric %q has no scoring function", ErrInvalidEvaluator, name)
	}
	return r.Register(&funcEvaluator{
		name:     name,
		metric:   metric,
		rng:      rng,
		patterns: append([]patternclassifier.Pattern(nil), patterns...),
		fn:       fn,
	})
}

// Get returns a registered evaluator by name.
func (r *Registry) Get(name string) (Evaluator, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.evals[name]
	return e, ok
}

// Evaluators returns every registered evaluator in a STABLE name order. Order is stable because the
// harness's output is compared byte-for-byte across runs in the reproducibility tests; a map-order
// walk would make an identical measurement look like a change.
func (r *Registry) Evaluators() []Evaluator {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.evals))
	for n := range r.evals {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Evaluator, 0, len(names))
	for _, n := range names {
		out = append(out, r.evals[n])
	}
	return out
}

// Describe returns every registered evaluator's descriptor, in name order.
func (r *Registry) Describe() []Descriptor {
	evs := r.Evaluators()
	out := make([]Descriptor, 0, len(evs))
	for _, e := range evs {
		out = append(out, Describe(e))
	}
	return out
}

// funcEvaluator adapts a MetricFunc to the Evaluator interface.
type funcEvaluator struct {
	name     string
	metric   string
	rng      Range
	patterns []patternclassifier.Pattern
	fn       MetricFunc
}

func (e *funcEvaluator) Name() string                                    { return e.name }
func (e *funcEvaluator) Metric() string                                  { return e.metric }
func (e *funcEvaluator) Range() Range                                    { return e.rng }
func (e *funcEvaluator) AdmissiblePatterns() []patternclassifier.Pattern { return e.patterns }
func (e *funcEvaluator) Evaluate(ctx context.Context, tr Trace, c Case, tgt Target) (float64, error) {
	return e.fn(ctx, tr, c, tgt)
}

// ─────────────────────────────────────────────────────────────────────────────
// Adapter onto the P2.5 evaluator seam.
// ─────────────────────────────────────────────────────────────────────────────

// telemetryAdapter carries a P4 evaluator onto telemetry.Evaluator so a P4 metric flows through the
// SAME collector path (gate -> scrub -> eval store) P2.5 already built, rather than opening a second
// emission path that would have to re-derive the tagging discipline.
type telemetryAdapter struct {
	ev    Evaluator
	trace func(telemetry.Trace) Trace
	kase  Case
	tgt   Target
}

// AsTelemetryEvaluator adapts a P4 evaluator for a fixed case/target onto the P2.5 seam. resolve
// lifts a raw telemetry.Trace into the richer P4 Trace (attaching resolved payloads); it is supplied
// by the harness, which owns the blob store.
func AsTelemetryEvaluator(e Evaluator, c Case, tgt Target, resolve func(telemetry.Trace) Trace) telemetry.Evaluator {
	return &telemetryAdapter{ev: e, trace: resolve, kase: c, tgt: tgt}
}

func (a *telemetryAdapter) Name() string { return a.ev.Name() }

func (a *telemetryAdapter) Evaluate(rc telemetry.RunContext, tr telemetry.Trace) []telemetry.QualityMetricEvent {
	mv, err := Compute(context.Background(), a.ev, a.trace(tr), a.kase, a.tgt)
	if err != nil {
		// A skip (inadmissible / not-applicable) and an invalid value both emit NOTHING. Emitting a
		// zero would be indistinguishable from a measured failure.
		return nil
	}
	nodeRC := rc
	if a.tgt.Scope == ScopeNode {
		nodeRC = rc.WithNode(a.tgt.NodeID, 0)
	} else {
		nodeRC = rc.WithNode(telemetry.NodeIDRun, 0)
	}
	nodeRC.CaseID = a.kase.CaseID
	return []telemetry.QualityMetricEvent{
		nodeRC.Quality(a.ev.Name(), mv.Metric, mv.Value, mv.Unit, nowFunc()),
	}
}
