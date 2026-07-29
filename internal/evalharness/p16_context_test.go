package evalharness

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// P16 task 5.4 / 7.6 / NFR6 — a context reduction is legible through the harness that already exists.
//
// The claim P16 makes to the eval layer is that it needs NOTHING from it. A windowed or compacted
// context shows up as fewer `eval_tokens_total` at unchanged `task_success`, read through the standard
// family, with no context-specific scorer, no new metric, and no evaluator that knows the word
// "context". This file is where that claim can go red.

// ── the reduction is visible in the standard family ──────────────────────────────────────────────

func TestContextReductionLowersEvalTokensNoRegression(t *testing.T) {
	reg := NewBuiltinRegistry()

	// The SAME workflow, the SAME case, the SAME correct answer — twice. The only difference is how much
	// context each node was handed, which shows up as input tokens on the node spans.
	base := newTrace("run-base").
		node("router", 0.010, 100, 400, 50, false).
		node("answer", 0.030, 300, 1200, 200, false).
		output(`{"a":"world"}`).
		build()
	windowed := newTrace("run-windowed").
		node("router", 0.004, 100, 150, 50, false).
		node("answer", 0.012, 300, 400, 200, false).
		output(`{"a":"world"}`).
		build()

	// 🔴 ONE call, the same one every other axis is scored by. If P16 needed its own scorer, this line
	// would have to change — and that change is what the test exists to prevent.
	baseVals, err := RunMetrics(context.Background(), reg, base, baseCase())
	if err != nil {
		t.Fatalf("RunMetrics(base): %v", err)
	}
	winVals, err := RunMetrics(context.Background(), reg, windowed, baseCase())
	if err != nil {
		t.Fatalf("RunMetrics(windowed): %v", err)
	}
	b, w := valuesByMetric(baseVals), valuesByMetric(winVals)

	if !(w[MetricRunTokens].Value < b[MetricRunTokens].Value) {
		t.Errorf("a context reduction must show up as fewer %s: base=%v windowed=%v",
			MetricRunTokens, b[MetricRunTokens].Value, w[MetricRunTokens].Value)
	}
	if w[MetricTaskSuccess].Value < b[MetricTaskSuccess].Value {
		t.Errorf("task_success regressed (%v → %v); a reduction that loses the answer is not a win, and the "+
			"harness is what says so", b[MetricTaskSuccess].Value, w[MetricTaskSuccess].Value)
	}
	// The saving is real money, through the standard family's own cost metric — no context-specific
	// accounting anywhere.
	if !(w[MetricRunCostUSD].Value < b[MetricRunCostUSD].Value) {
		t.Errorf("the reduction did not lower %s: base=%v windowed=%v",
			MetricRunCostUSD, b[MetricRunCostUSD].Value, w[MetricRunCostUSD].Value)
	}

	// Both runs produced exactly the same metric NAMES. A context variant that introduced a metric its
	// baseline lacks could not be compared against it at all.
	if !reflect.DeepEqual(sortedNames(baseVals), sortedNames(winVals)) {
		t.Errorf("the two runs produced different metric families:\n%v\n%v",
			sortedNames(baseVals), sortedNames(winVals))
	}
}

// 🚫 No context-specific scorer exists, and none is needed. The standard family names no axis — this
// pins that for the context axis specifically, the way P13 pinned it for prompt/model.
func TestNoContextSpecificScorer(t *testing.T) {
	for _, m := range append(append([]string{}, StandardFamily...), ContributionFamily...) {
		low := strings.ToLower(m)
		for _, banned := range []string{"context", "drop", "window", "compaction", "retriev", "chunk"} {
			if strings.Contains(low, banned) {
				t.Errorf("harness metric %q names a context concept (%q); the loss is read from "+
					"context_drop_ratio in telemetry and the reduction from eval_tokens_total here — the "+
					"harness must not learn the axis", m, banned)
			}
		}
	}

	// And no registered evaluator claims to score context. An evaluator is how a scorer would enter.
	for _, d := range NewBuiltinRegistry().Describe() {
		low := strings.ToLower(d.Name + " " + d.Metric)
		if strings.Contains(low, "context") || strings.Contains(low, "drop") {
			t.Errorf("a builtin evaluator %q scores a context concept; scoring a context variant must "+
				"require no evaluator change", d.Name)
		}
	}
}

func sortedNames(vs []MetricValue) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range vs {
		if !seen[v.Metric] {
			seen[v.Metric] = true
			out = append(out, v.Metric)
		}
	}
	// Insertion order is deterministic (RunMetrics builds the slice in a fixed order), so no sort is
	// needed — and comparing the order too is stricter, which is what we want here.
	return out
}
