package patternclassifier

import (
	"context"
	"strings"
	"testing"
)

// The table must cover the WHOLE taxonomy (Decision 7): all 20 rows authored now, so P5 wires a
// behavioral label source into an existing table rather than redesigning dispatch.
func TestEveryTaxonomyPatternHasAMetricSet(t *testing.T) {
	for _, i := range Patterns() {
		ms, ok := MetricSetFor(i.Pattern)
		if !ok {
			t.Errorf("%q has no metric-set row", i.Pattern)
			continue
		}
		if len(ms.Metrics) == 0 {
			t.Errorf("%q: empty metric-set", i.Pattern)
		}
		if ms.Primary == "" {
			t.Errorf("%q: no primary metric named", i.Pattern)
		}
		found := false
		for _, m := range ms.Metrics {
			if m == ms.Primary {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: primary %q is not in the metric list %v", i.Pattern, ms.Primary, ms.Metrics)
		}
		if len(ms.FailureModes) == 0 {
			t.Errorf("%q: no failure modes scoped (P4.5 reads this row)", i.Pattern)
		}
	}
	// The eight structurally-labelled patterns must be marked available — those are the ones P4 can
	// key off immediately.
	for _, p := range append(StructuralPatterns(), Reflection) {
		if ms, _ := MetricSetFor(p); !ms.Available {
			t.Errorf("%q ships a label in P3.5 but its metric-set is marked unavailable", p)
		}
	}
}

// An unknown pattern gets NOTHING, not a plausible default. Computing "some metrics" for a pattern
// the table does not know produces numbers nobody can interpret.
func TestMetricSetForUnknownPatternReturnsNothing(t *testing.T) {
	if ms, ok := MetricSetFor("self_healing_swarm"); ok || len(ms.Metrics) != 0 {
		t.Fatalf("unknown pattern must yield no metric-set, got %+v", ms)
	}
}

// Task 5.3 / the M4 exit behaviour: a RAG subgraph selects retrieval metrics and NOT router metrics;
// a Routing subgraph selects misroute-rate and NOT relevance@k.
func TestDispatchRAGSelectsRetrievalAndRoutingSelectsMisroute(t *testing.T) {
	rag, ok := MetricSetFor(RetrievalRAG)
	if !ok {
		t.Fatal("no RAG metric-set")
	}
	for _, want := range []string{"relevance_at_k", "faithfulness", "recall", "rerank_gain"} {
		if !has(rag.Metrics, want) {
			t.Errorf("RAG metric-set missing %q: %v", want, rag.Metrics)
		}
	}
	if has(rag.Metrics, "misroute_rate") || has(rag.Metrics, "routing_accuracy") {
		t.Errorf("RAG must NOT select router metrics: %v", rag.Metrics)
	}

	rt, _ := MetricSetFor(Routing)
	if rt.Primary != "misroute_rate" {
		t.Errorf("Routing primary = %q, want misroute_rate", rt.Primary)
	}
	if !has(rt.Metrics, "routing_accuracy") {
		t.Errorf("Routing must select routing-accuracy: %v", rt.Metrics)
	}
	if has(rt.Metrics, "relevance_at_k") {
		t.Errorf("Routing must NOT select retrieval relevance@k: %v", rt.Metrics)
	}

	refl, _ := MetricSetFor(Reflection)
	for _, want := range []string{"iteration_count", "convergence", "quality_gain_per_revision"} {
		if !has(refl.Metrics, want) {
			t.Errorf("Reflection metric-set missing %q: %v", want, refl.Metrics)
		}
	}
}

// The end-to-end dispatch: classify the COMPOSITE fixture and confirm each region resolves to its own
// metric-set. This is the M4 exit behaviour on a real classification, not on a hand-built label.
func TestDispatchOnClassifiedCompositeIR(t *testing.T) {
	f := fxComposite()
	res, err := Classify(context.Background(), f.ir, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	byRegion := MetricSetsForLabels(res.Labels)

	routerRef := SubgraphIDFor([]string{"n_router", "n_billing", "n_tech"})
	ragRef := SubgraphIDFor([]string{"n_embed", "n_retrieve", "n_rerank", "n_answer"})

	router := byRegion[routerRef]
	if len(router) != 1 || router[0].Primary != "misroute_rate" {
		t.Fatalf("the router subgraph must select misroute-rate, got %+v", router)
	}
	rag := byRegion[ragRef]
	if len(rag) != 1 || rag[0].Primary != "relevance_at_k" {
		t.Fatalf("the RAG subgraph must select retrieval metrics, got %+v", rag)
	}
	// The two regions of ONE workflow get DIFFERENT metric-sets — the thing P3.5 exists to make true.
	if has(router[0].Metrics, "relevance_at_k") || has(rag[0].Metrics, "misroute_rate") {
		t.Error("the two regions' metric-sets leaked into each other")
	}
	// And the tool-bound node inside the routing branch dispatches tool metrics of its own.
	tool := byRegion["n_tech"]
	if len(tool) != 1 || tool[0].Primary != "tool_call_success_rate" {
		t.Fatalf("the tool-bound node must select tool metrics, got %+v", tool)
	}
}

// The returned set is a copy: a consumer mutating what it got must not corrupt the table for every
// later consumer.
func TestMetricSetIsNotMutableByCallers(t *testing.T) {
	a, _ := MetricSetFor(Routing)
	a.Metrics[0] = "corrupted"
	b, _ := MetricSetFor(Routing)
	if b.Metrics[0] == "corrupted" {
		t.Fatal("MetricSetFor handed out a reference into the source-of-truth table")
	}
}

// The dump shows the dispatch at the point of use, so a reviewer sees what a label DOES, not just
// that it exists.
func TestDumpShowsDispatch(t *testing.T) {
	out, err := Dump(context.Background(), fxComposite().ir, fxComposite().opts())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "dispatches metric-set: primary=misroute_rate") ||
		!strings.Contains(out, "dispatches metric-set: primary=relevance_at_k") {
		t.Errorf("dump does not show per-region dispatch:\n%s", out)
	}
}

func has(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
