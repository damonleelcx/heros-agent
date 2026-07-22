package evalharness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// InterfaceVersion is the stable major a custom metric compiles against. It tracks
// telemetry.EvaluatorInterfaceVersion's promise one layer up: an evaluator built against this major
// keeps the same range/admissibility contract and the same tagging path.
const InterfaceVersion = "1.0.0"

// Sentinel errors. They are values, not strings, because every one of them is a decision a caller
// has to branch on: "the metric does not apply here" is not the same outcome as "the metric ran and
// produced nonsense", and collapsing the two is how an inadmissible score ends up on a board.
var (
	// ErrInadmissible means the evaluator is not admissible on the target's pattern. The harness
	// SKIPS, it does not score zero — a zero would be read as "measured and bad".
	ErrInadmissible = errors.New("evalharness: evaluator is not admissible for this pattern")
	// ErrNotApplicable means the evaluator is admissible but this case gives it nothing to score
	// (no reference, no rubric, no schema). Also a skip, never a zero.
	ErrNotApplicable = errors.New("evalharness: evaluator has nothing to score on this case")
	// ErrOutOfRange means the evaluator returned a value outside the range it declared. The result
	// is flagged invalid and discarded.
	ErrOutOfRange = errors.New("evalharness: metric value escapes its declared range")
	// ErrInvalidEvaluator is a registration-time rejection.
	ErrInvalidEvaluator = errors.New("evalharness: invalid evaluator")
)

// Range is an evaluator's declared output interval, inclusive at both ends. Declaring it is what
// makes "the custom metric returned 4.2 on a [0,1] metric" catchable at the boundary instead of
// three layers downstream in a weighted sum.
type Range struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// UnitRange is the [0,1] interval every quality metric that enters the composite score must use.
func UnitRange() Range { return Range{Min: 0, Max: 1} }

// Validate rejects a nonsensical range at registration time.
func (r Range) Validate() error {
	if math.IsNaN(r.Min) || math.IsNaN(r.Max) || math.IsInf(r.Min, 0) || math.IsInf(r.Max, 0) {
		return fmt.Errorf("%w: range bounds must be finite, got [%v,%v]", ErrInvalidEvaluator, r.Min, r.Max)
	}
	if r.Min >= r.Max {
		return fmt.Errorf("%w: range min (%v) must be below max (%v)", ErrInvalidEvaluator, r.Min, r.Max)
	}
	return nil
}

// Contains reports whether v lies inside the declared range. NaN is never contained: a NaN score
// would propagate silently through every mean, CI, and weighted sum it touches.
func (r Range) Contains(v float64) bool {
	if math.IsNaN(v) {
		return false
	}
	return v >= r.Min && v <= r.Max
}

// Normalize maps v onto [0,1] within the declared range. Used by the scoring layer so a metric with
// a non-unit range still enters the weighted sum comparably.
func (r Range) Normalize(v float64) float64 {
	if r.Max == r.Min {
		return 0
	}
	return (v - r.Min) / (r.Max - r.Min)
}

// Scope says whether an evaluator scores the run as a whole or one node of it.
type Scope string

const (
	// ScopeRun scores the workflow's end-to-end output (task success, end-to-end latency).
	ScopeRun Scope = "run"
	// ScopeNode scores one node, and is therefore subject to pattern admissibility.
	ScopeNode Scope = "node"
)

// Target is what an evaluator is pointed at. Pattern is the P3.5 label of the node, and it is the
// dispatcher: it decides which metrics are even allowed to be computed here.
type Target struct {
	Scope   Scope                     `json:"scope"`
	NodeID  string                    `json:"node_id,omitempty"`
	Pattern patternclassifier.Pattern `json:"pattern,omitempty"`
	Labels  []patternclassifier.Label `json:"-"`
}

// RunTarget is the target for end-to-end scoring.
func RunTarget() Target { return Target{Scope: ScopeRun, NodeID: telemetry.NodeIDRun} }

// NodeTarget is the target for one labeled node.
func NodeTarget(nodeID string, p patternclassifier.Pattern) Target {
	return Target{Scope: ScopeNode, NodeID: nodeID, Pattern: p}
}

// Trace is what an evaluator sees: the P2.5 spans plus the resolved I/O the spans reference by hash.
// Spans carry timing, cost, tokens and status; they deliberately do NOT carry payloads (payloads are
// content-hashed blobs), so the harness resolves them once and hands both to every evaluator rather
// than letting each evaluator reach into a blob store.
type Trace struct {
	telemetry.Trace
	// Output is the workflow's end-to-end output.
	Output json.RawMessage
	// NodeOutputs / NodeInputs are per-node payloads keyed by node_id.
	NodeOutputs map[string]json.RawMessage
	NodeInputs  map[string]json.RawMessage
	// Failed reports that the run terminated without producing a valid end-to-end output.
	Failed bool
	// FailureReason is the halt/contract-violation reason, if any.
	FailureReason string
}

// OutputFor returns the payload the given target scores over.
func (t Trace) OutputFor(tgt Target) (json.RawMessage, bool) {
	if tgt.Scope == ScopeRun {
		return t.Output, len(t.Output) > 0
	}
	out, ok := t.NodeOutputs[tgt.NodeID]
	return out, ok && len(out) > 0
}

// MetricValue is one scored number, with everything needed to know whether to believe it.
type MetricValue struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
	NodeID string  `json:"node_id"`

	// Evaluator is stamped by the harness, never by the evaluator itself — the emitter, not the
	// plugin, is the authority on attribution.
	Evaluator string `json:"evaluator"`
	// ReferenceLabel is copied from the case, so a score computed against a weak reference cannot
	// be separated from that fact anywhere downstream.
	ReferenceLabel ReferenceLabel `json:"reference_label"`
	// Judge carries the judge's calibration standing when this value came from an LLM judge. It is
	// non-nil for exactly the judge metrics, which is how a consumer knows a gate is forbidden.
	Judge *JudgeStanding `json:"judge,omitempty"`
}

// Evaluator is the plug-in contract: a function over a trace and its case that declares what it
// outputs and where it is allowed to run.
type Evaluator interface {
	// Name is the registry key and the eval_result.evaluator_name value.
	Name() string
	// Metric is the metric_name the value is recorded under. Two evaluators may compute the same
	// metric (a rubric judge and an exact-match oracle both produce task_success); the name is what
	// keeps their rows distinguishable.
	Metric() string
	// Range is the declared output interval, enforced on every value.
	Range() Range
	// AdmissiblePatterns is the set of P3.5 patterns this evaluator may be computed on. An EMPTY
	// set means pattern-agnostic (cost, latency, exact-match): admissible everywhere. A non-empty
	// set is a hard restriction — the harness refuses the evaluator on any other pattern.
	AdmissiblePatterns() []patternclassifier.Pattern
	// Evaluate scores the trace. It returns ErrNotApplicable when the case gives it nothing to
	// score; it must never invent a value to avoid returning an error. ctx is carried because a
	// judge evaluator makes a provider call — an evaluator that cannot be cancelled is an eval run
	// that cannot be capped.
	Evaluate(ctx context.Context, tr Trace, c Case, tgt Target) (float64, error)
}

// Admissible reports whether e may be computed on tgt, and why not if it may not.
//
// Two independent conditions must hold for a node-scoped metric, and they check different things:
//
//  1. The evaluator's OWN declaration must list the node's pattern. This is the plug-in's promise
//     about where its number means something.
//  2. The pattern's metric-set (patternclassifier.MetricSetFor — the single source of truth for
//     pattern -> metrics) must contain the evaluator's metric. This is the platform's promise, and
//     it is what stops a well-meaning plug-in from declaring itself admissible for Routing while
//     computing relevance@k.
//
// Requiring both means a drift between the plug-in and the table is a refusal, not a silent
// preference for one of them.
func Admissible(e Evaluator, tgt Target) error {
	declared := e.AdmissiblePatterns()
	if len(declared) == 0 {
		return nil // pattern-agnostic
	}
	if tgt.Scope == ScopeRun {
		return fmt.Errorf("%w: evaluator %q is pattern-scoped and cannot score the run as a whole",
			ErrInadmissible, e.Name())
	}
	if tgt.Pattern == "" {
		return fmt.Errorf("%w: evaluator %q needs a pattern label and node %q has none",
			ErrInadmissible, e.Name(), tgt.NodeID)
	}
	found := false
	for _, p := range declared {
		if p == tgt.Pattern {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("%w: evaluator %q declares %v, node %q is labeled %q",
			ErrInadmissible, e.Name(), declared, tgt.NodeID, tgt.Pattern)
	}
	if err := metricInPatternSet(e.Metric(), tgt.Pattern); err != nil {
		return err
	}
	return nil
}

// metricInPatternSet enforces condition (2) of Admissible against the P3.5 table.
func metricInPatternSet(metric string, p patternclassifier.Pattern) error {
	set, ok := patternclassifier.MetricSetFor(p)
	if !ok {
		return fmt.Errorf("%w: pattern %q has no metric-set", ErrInadmissible, p)
	}
	for _, m := range set.Metrics {
		if m == metric {
			return nil
		}
	}
	return fmt.Errorf("%w: metric %q is not in pattern %q's metric-set %v",
		ErrInadmissible, metric, p, set.Metrics)
}

// Compute is the ONLY sanctioned way to run an evaluator. It enforces admissibility before the
// evaluator executes (so an inadmissible judge never costs a provider call) and the declared range
// after it returns (so an out-of-range value is flagged invalid, not recorded).
func Compute(ctx context.Context, e Evaluator, tr Trace, c Case, tgt Target) (MetricValue, error) {
	if err := Admissible(e, tgt); err != nil {
		return MetricValue{}, err
	}
	v, err := e.Evaluate(ctx, tr, c, tgt)
	if err != nil {
		return MetricValue{}, err
	}
	rng := e.Range()
	if !rng.Contains(v) {
		return MetricValue{}, fmt.Errorf("%w: evaluator %q declared [%v,%v] and returned %v",
			ErrOutOfRange, e.Name(), rng.Min, rng.Max, v)
	}
	mv := MetricValue{
		Metric:         e.Metric(),
		Value:          v,
		Unit:           unitFor(e),
		NodeID:         tgt.NodeID,
		Evaluator:      e.Name(),
		ReferenceLabel: c.Label,
	}
	if j, ok := e.(JudgeEvaluator); ok {
		st := j.Standing()
		mv.Judge = &st
	}
	return mv, nil
}

// unitFor reports an evaluator's unit. An evaluator may declare one explicitly; otherwise a [0,1]
// range is a ratio and anything else is a count, which are the only two units a scored quality
// metric has ever needed.
func unitFor(e Evaluator) string {
	if u, ok := e.(interface{ Unit() string }); ok {
		return u.Unit()
	}
	if e.Range() == UnitRange() {
		return telemetry.UnitRatio
	}
	return telemetry.UnitCount
}

// Descriptor is an evaluator's self-description, used by the docs sync test and the UI so
// "which evaluators exist, over what range, admissible where" is read from code rather than from a
// hand-maintained list that drifts.
type Descriptor struct {
	Name       string                      `json:"name"`
	Metric     string                      `json:"metric"`
	Range      Range                       `json:"range"`
	Patterns   []patternclassifier.Pattern `json:"patterns"`
	Judge      bool                        `json:"judge"`
	Calibrated bool                        `json:"calibrated"`
}

// Describe builds an evaluator's descriptor.
func Describe(e Evaluator) Descriptor {
	pats := append([]patternclassifier.Pattern(nil), e.AdmissiblePatterns()...)
	sort.Slice(pats, func(i, j int) bool { return pats[i] < pats[j] })
	d := Descriptor{Name: e.Name(), Metric: e.Metric(), Range: e.Range(), Patterns: pats}
	if j, ok := e.(JudgeEvaluator); ok {
		d.Judge = true
		d.Calibrated = j.Standing().Calibrated
	}
	return d
}
