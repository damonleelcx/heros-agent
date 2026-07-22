package evalharness

import (
	"context"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// registryWithPatternMetrics builds a registry carrying one pattern-scoped metric per pattern the
// fixture uses, so the selection test has something to select between.
func registryWithPatternMetrics(t *testing.T) *Registry {
	t.Helper()
	r := NewBuiltinRegistry()
	must := func(name, metric string, p patternclassifier.Pattern) {
		t.Helper()
		if err := r.RegisterMetric(name, metric, UnitRange(), []patternclassifier.Pattern{p},
			func(_ context.Context, tr Trace, c Case, tgt Target) (float64, error) { return 1, nil }); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	must("misroute_rate", "misroute_rate", patternclassifier.Routing)
	must("relevance_at_k", "relevance_at_k", patternclassifier.RetrievalRAG)
	must("tool_call_success_rate", "tool_call_success_rate", patternclassifier.ToolUse)
	must("convergence", "convergence", patternclassifier.Reflection)
	return r
}

// Task 1.4 + task 8.6 — the router is scored with misroute metrics, NOT relevance@k; the RAG node is
// scored with relevance@k, NOT misroute. This is the whole point of pattern-driven selection.
func TestPatternDrivenMetricSetSelection(t *testing.T) {
	ir := fxMultiPatternIR()
	reg := registryWithPatternMetrics(t)
	plan := BuildPlan(ir, reg)

	byNode := map[string]NodePlan{}
	for _, np := range plan.Nodes {
		byNode[np.Target.NodeID] = np
	}

	router, ok := byNode["router"]
	if !ok {
		t.Fatal("router has no plan")
	}
	if router.Target.Pattern != patternclassifier.Routing {
		t.Fatalf("router pattern: want Routing got %q", router.Target.Pattern)
	}
	assertHas(t, router.Evaluators, "misroute_rate")
	assertLacks(t, router.Evaluators, "relevance_at_k")
	assertRefusalNames(t, router.Refusals, "relevance_at_k")
	if router.Primary != "misroute_rate" {
		t.Fatalf("router primary metric: want misroute_rate got %q", router.Primary)
	}

	rag, ok := byNode["branch_b"]
	if !ok {
		t.Fatal("branch_b has no plan")
	}
	assertHas(t, rag.Evaluators, "relevance_at_k")
	assertLacks(t, rag.Evaluators, "misroute_rate")
	if rag.Primary != "relevance_at_k" {
		t.Fatalf("RAG primary metric: want relevance_at_k got %q", rag.Primary)
	}

	tool := byNode["branch_a"]
	assertHas(t, tool.Evaluators, "tool_call_success_rate")
	assertLacks(t, tool.Evaluators, "relevance_at_k")

	reflect := byNode["reflect"]
	assertHas(t, reflect.Evaluators, "convergence")

	// Pattern-agnostic built-ins are admissible everywhere; pattern-scoped ones are refused at run
	// scope, and the refusal is RECORDED so "why is there no misroute_rate on the run row?" is
	// answerable without re-running anything.
	assertHas(t, plan.Run.Evaluators, EvaluatorExactMatch)
	assertLacks(t, plan.Run.Evaluators, "misroute_rate")
	assertRefusalNames(t, plan.Run.Refusals, "misroute_rate")
}

// A node with no pattern label gets only the pattern-agnostic metrics — never a guessed label.
func TestUnlabeledNodeGetsOnlyPatternAgnosticMetrics(t *testing.T) {
	ir := fxMultiPatternIR()
	ir.Nodes[0].PatternLabels = nil // strip the router's label
	reg := registryWithPatternMetrics(t)
	plan := BuildPlan(ir, reg)

	for _, np := range plan.Nodes {
		if np.Target.NodeID != "router" {
			continue
		}
		if np.Target.Pattern != "" {
			t.Fatalf("an unlabeled node must not be assigned a pattern, got %q", np.Target.Pattern)
		}
		assertLacks(t, np.Evaluators, "misroute_rate")
		assertHas(t, np.Evaluators, EvaluatorExactMatch)
		return
	}
	t.Fatal("router plan missing")
}

// A confirmed label beats a behavioral CANDIDATE at equal confidence: dispatching measurement off a
// hypothesis is how a node gets scored on metrics its region does not implement.
func TestConfirmedLabelBeatsCandidate(t *testing.T) {
	ir := fxMultiPatternIR()
	ir.Nodes[0].PatternLabels = append(ir.Nodes[0].PatternLabels, ir.Nodes[0].PatternLabels[0])
	ir.Nodes[0].PatternLabels[1].Pattern = string(patternclassifier.RetrievalRAG)
	ir.Nodes[0].PatternLabels[1].Candidate = true
	ir.Nodes[0].PatternLabels[1].Confidence = patternclassifier.ConfidenceTopologyDetermined

	got, _ := NodePattern(ir, "router")
	if got != patternclassifier.Routing {
		t.Fatalf("confirmed label must win over a candidate, got %q", got)
	}
}

func assertHas(t *testing.T, names []string, want string) {
	t.Helper()
	for _, n := range names {
		if n == want {
			return
		}
	}
	t.Fatalf("want %q among %v", want, names)
}

func assertLacks(t *testing.T, names []string, unwanted string) {
	t.Helper()
	for _, n := range names {
		if n == unwanted {
			t.Fatalf("%q must NOT be computed here (got %v)", unwanted, names)
		}
	}
}

func assertRefusalNames(t *testing.T, refusals []Refusal, want string) {
	t.Helper()
	for _, r := range refusals {
		if r.Evaluator == want {
			if !strings.Contains(r.Reason, "admissible") {
				t.Fatalf("refusal for %q must say why: %q", want, r.Reason)
			}
			return
		}
	}
	t.Fatalf("want a recorded refusal for %q, got %+v", want, refusals)
}
