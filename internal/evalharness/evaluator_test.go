package evalharness

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

func baseCase() Case {
	return Case{
		CaseID:     "case-1",
		WorkflowID: "wf-multipattern",
		Suite:      "default",
		Input:      json.RawMessage(`{"q":"hello"}`),
		Reference:  json.RawMessage(`{"a":"world"}`),
		Label:      LabelGold,
		Origin:     OriginHandAuthored,
	}
}

// Task 1.1 — an evaluator declares its output range and its admissible patterns, and both are
// enforced. A range that cannot be honest is refused at registration.
func TestRangeValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		rng  Range
		ok   bool
	}{
		{"unit", UnitRange(), true},
		{"count", Range{Min: 0, Max: 100}, true},
		{"inverted", Range{Min: 1, Max: 0}, false},
		{"degenerate", Range{Min: 1, Max: 1}, false},
		{"infinite", Range{Min: 0, Max: math.Inf(1)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.rng.Validate()
			if tc.ok && err != nil {
				t.Fatalf("range %v: want valid, got %v", tc.rng, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("range %v: want rejected, got valid", tc.rng)
			}
		})
	}
}

// Task 1.3 — a custom metric registers by name exactly like a skill, and its declared range is
// validated at registration.
func TestRegisterCustomMetric(t *testing.T) {
	r := NewBuiltinRegistry()
	err := r.RegisterMetric("domain_accuracy", "domain_accuracy", UnitRange(), nil,
		func(_ context.Context, tr Trace, c Case, tgt Target) (float64, error) { return 0.75, nil })
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, ok := r.Get("domain_accuracy"); !ok {
		t.Fatal("custom metric is not retrievable after registration")
	}

	// Duplicate name is rejected, not silently overwritten.
	if err := r.RegisterMetric("domain_accuracy", "x", UnitRange(), nil,
		func(_ context.Context, tr Trace, c Case, tgt Target) (float64, error) { return 0, nil }); err == nil {
		t.Fatal("duplicate registration must be rejected")
	}

	// An invalid range is refused at registration, before any value exists.
	if err := r.RegisterMetric("bad_range", "bad_range", Range{Min: 1, Max: 0}, nil,
		func(_ context.Context, tr Trace, c Case, tgt Target) (float64, error) { return 0, nil }); err == nil {
		t.Fatal("registration with an inverted range must be rejected")
	}

	// A nil scoring function is refused: a registered name with no implementation is a metric that
	// reports nothing while looking present.
	if err := r.RegisterMetric("nil_fn", "nil_fn", UnitRange(), nil, nil); err == nil {
		t.Fatal("registration with a nil function must be rejected")
	}
}

// Spec scenario: "Custom metric is rejected if its value escapes its declared range" — the harness
// flags the result invalid rather than recording an out-of-range score.
func TestOutOfRangeValueIsFlaggedNotRecorded(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterMetric("escapes", "escapes", UnitRange(), nil,
		func(_ context.Context, tr Trace, c Case, tgt Target) (float64, error) { return 4.2, nil }); err != nil {
		t.Fatalf("register: %v", err)
	}
	e, _ := r.Get("escapes")
	tr := newTrace("run-1").node("n", 0.01, 100, 10, 20, false).output(`{"a":"world"}`).build()

	mv, err := Compute(context.Background(), e, tr, baseCase(), RunTarget())
	if !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("want ErrOutOfRange, got %v (value %v)", err, mv.Value)
	}
	if mv.Value != 0 || mv.Metric != "" {
		t.Fatalf("an out-of-range result must not be returned as a value, got %+v", mv)
	}
}

// A NaN is never in range: a NaN score would poison every mean, CI and weighted sum downstream.
func TestNaNIsNeverInRange(t *testing.T) {
	if UnitRange().Contains(nan()) {
		t.Fatal("NaN must never be considered in range")
	}
}

func nan() float64 { return math.NaN() }

// Task 1.4 / 8.6 — pattern admissibility is enforced in BOTH directions, and a plug-in that
// disagrees with the P3.5 metric-set table is refused at registration.
func TestAdmissibilityIsEnforcedAgainstThePatternTable(t *testing.T) {
	r := NewRegistry()

	// relevance@k is in the RAG metric-set, so registering it for RetrievalRAG succeeds...
	if err := r.RegisterMetric("relevance_at_k", "relevance_at_k", UnitRange(),
		[]patternclassifier.Pattern{patternclassifier.RetrievalRAG},
		func(_ context.Context, tr Trace, c Case, tgt Target) (float64, error) { return 0.9, nil }); err != nil {
		t.Fatalf("registering relevance_at_k for RAG must succeed: %v", err)
	}

	// ...and registering the SAME metric for Routing fails, because Routing's metric-set does not
	// contain it. This is the drift check: the table is the source of truth, the plug-in's claim is
	// not enough.
	err := r.RegisterMetric("relevance_on_router", "relevance_at_k", UnitRange(),
		[]patternclassifier.Pattern{patternclassifier.Routing},
		func(_ context.Context, tr Trace, c Case, tgt Target) (float64, error) { return 0.9, nil })
	if err == nil {
		t.Fatal("declaring relevance_at_k admissible for Routing must be refused at registration")
	}
	if !strings.Contains(err.Error(), "metric-set") {
		t.Fatalf("refusal must name the metric-set mismatch, got %v", err)
	}

	// At compute time the same rule holds: relevance@k on a Routing node is ErrInadmissible.
	e, _ := r.Get("relevance_at_k")
	tr := newTrace("run-1").node("router", 0.01, 10, 1, 1, false).build()
	if _, err := Compute(context.Background(), e, tr, baseCase(), NodeTarget("router", patternclassifier.Routing)); !errors.Is(err, ErrInadmissible) {
		t.Fatalf("relevance@k on a Routing node must be inadmissible, got %v", err)
	}
	if _, err := Compute(context.Background(), e, tr, baseCase(), NodeTarget("rag", patternclassifier.RetrievalRAG)); err != nil {
		t.Fatalf("relevance@k on a RAG node must be admissible, got %v", err)
	}
}

// A pattern-scoped evaluator cannot score the run as a whole: the run has no pattern label, and
// silently allowing it would put a node metric on the end-to-end row.
func TestPatternScopedEvaluatorCannotScoreTheRun(t *testing.T) {
	r := NewRegistry()
	_ = r.RegisterMetric("misroute_rate", "misroute_rate", UnitRange(),
		[]patternclassifier.Pattern{patternclassifier.Routing},
		func(_ context.Context, tr Trace, c Case, tgt Target) (float64, error) { return 0.1, nil })
	e, _ := r.Get("misroute_rate")
	tr := newTrace("run-1").node("router", 0, 1, 0, 0, false).build()
	if _, err := Compute(context.Background(), e, tr, baseCase(), RunTarget()); !errors.Is(err, ErrInadmissible) {
		t.Fatalf("want ErrInadmissible at run scope, got %v", err)
	}
}

// An out-of-taxonomy pattern is refused rather than treated as a new pattern nobody defined.
func TestUnknownPatternIsRefused(t *testing.T) {
	r := NewRegistry()
	err := r.RegisterMetric("x", "x", UnitRange(), []patternclassifier.Pattern{"NotAPattern"},
		func(_ context.Context, tr Trace, c Case, tgt Target) (float64, error) { return 0, nil })
	if err == nil {
		t.Fatal("an out-of-taxonomy pattern must be refused at registration")
	}
}

// Every value carries its evaluator name and the case's reference label — a score computed against a
// weak reference cannot be separated from that fact anywhere downstream.
func TestComputeStampsAttributionAndReferenceLabel(t *testing.T) {
	r := NewBuiltinRegistry()
	e, _ := r.Get(EvaluatorExactMatch)
	c := baseCase()
	c.Label = LabelWeak
	tr := newTrace("run-1").node("n", 0, 1, 0, 0, false).output(`{"a":"world"}`).build()

	mv, err := Compute(context.Background(), e, tr, c, RunTarget())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if mv.Evaluator != EvaluatorExactMatch {
		t.Fatalf("evaluator attribution missing, got %q", mv.Evaluator)
	}
	if mv.ReferenceLabel != LabelWeak {
		t.Fatalf("reference label must ride on the value, got %q", mv.ReferenceLabel)
	}
}

// A NODE-scoped target scores that node's own output, not the run's.
//
// This test exists because the linter found `traceBuilder.nodeOutput` unused and the honest reading
// was not "dead helper" but "untested path": Trace.OutputFor branches on scope, and only the run
// branch had ever been asserted. A node evaluator silently scoring the end-to-end output would
// attribute a downstream node's answer to an upstream one, and nothing would have caught it.
func TestNodeScopedTargetReadsThatNodesOutput(t *testing.T) {
	tr := newTrace("run-1").
		node("router", 0.01, 10, 1, 1, false).
		node("answer", 0.02, 20, 1, 1, false).
		nodeOutput("router", `{"a":"routed"}`).
		nodeOutput("answer", `{"a":"world"}`).
		output(`{"a":"end-to-end"}`).
		build()

	for _, tc := range []struct {
		name   string
		target Target
		want   string
	}{
		{"run scope reads the end-to-end output", RunTarget(), `{"a":"end-to-end"}`},
		{"node scope reads that node's output", NodeTarget("answer", ""), `{"a":"world"}`},
		{"a different node reads its own", NodeTarget("router", ""), `{"a":"routed"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tr.OutputFor(tc.target)
			if !ok {
				t.Fatal("want an output")
			}
			if string(got) != tc.want {
				t.Fatalf("want %s, got %s", tc.want, got)
			}
		})
	}

	// A node that produced no output is absent, NOT the run's output under another name.
	if _, ok := tr.OutputFor(NodeTarget("never-ran", "")); ok {
		t.Fatal("a node with no output must report absent, never fall back to the run's")
	}

	// And an evaluator scoring a node scores that node: exact-match against the router's answer
	// passes on the router and fails on the node that answered differently.
	e := NewExactMatch()
	c := baseCase()
	c.Reference = json.RawMessage(`{"a":"routed"}`)
	for _, tc := range []struct {
		node string
		want float64
	}{{"router", 1}, {"answer", 0}} {
		got, err := e.Evaluate(context.Background(), tr, c, NodeTarget(tc.node, ""))
		if err != nil {
			t.Fatalf("%s: %v", tc.node, err)
		}
		if got != tc.want {
			t.Fatalf("node %s: want %v got %v", tc.node, tc.want, got)
		}
	}
}
