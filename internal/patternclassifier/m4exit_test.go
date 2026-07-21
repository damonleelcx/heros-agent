package patternclassifier

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// The remaining spec scenarios that no earlier test covers, plus the M4 exit checklist (PRD §13)
// asserted end to end rather than ticked by hand.

// Spec: "Purely behavioral patterns are not asserted from structure." The rule layer ships detectors
// for eight patterns and Reflection-as-candidate; it must never emit Planning, Memory Management,
// Human-in-the-Loop, or any other ⏳ row from topology alone, at any confidence.
func TestRuleLayerNeverEmitsAPurelyBehavioralPattern(t *testing.T) {
	allowed := map[Pattern]bool{Reflection: true}
	for _, p := range StructuralPatterns() {
		allowed[p] = true
	}
	// Every fixture, plus a few shapes deliberately built to look like the behavioral patterns.
	fixtures := append(allFixtures(),
		fixture{ // looks like a plan: one node emitting a list into several consumers
			name: "behavioral_bait/plan_like_fanout",
			ir: buildIR(
				[]discovery.IRNode{node("n_plan", withPrompt("produce a task list")), node("n_step1"), node("n_step2")},
				[]discovery.IREdge{dataEdge("n_plan", "n_step1"), dataEdge("n_plan", "n_step2")},
			),
		},
		fixture{ // looks like human-in-the-loop: an approval-shaped node in the middle
			name: "behavioral_bait/approval_like_chain",
			ir: buildIR(
				[]discovery.IRNode{node("n_draft"), node("n_human_approval", withPrompt("await approval")), node("n_send")},
				[]discovery.IREdge{dataEdge("n_draft", "n_human_approval"), dataEdge("n_human_approval", "n_send")},
			),
		},
		fixture{ // looks like memory: a node whose policy is a full-history window
			name: "behavioral_bait/memory_like_policy",
			ir:   buildIR([]discovery.IRNode{node("n_chat", withPolicy("full-history", "everything so far"))}, nil),
		},
	)
	for _, f := range fixtures {
		res, err := Classify(context.Background(), f.ir, f.opts())
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		for _, l := range res.Labels {
			if l.Source != SourceRule {
				continue
			}
			if !allowed[l.Pattern] {
				t.Errorf("%s: the rule layer emitted %q, which has no structural detector in P3.5 — "+
					"confirming it needs runtime traces (P5)", f.name, l.Pattern)
			}
			// And any behavioral pattern that IS emitted must be an unconfirmed, capped candidate.
			if IsBehavioral(l.Pattern) && (!l.Candidate || l.Confidence > BehavioralCandidateCap) {
				t.Errorf("%s: %q asserted as confirmed from structure: candidate=%v conf=%.2f",
					f.name, l.Pattern, l.Candidate, l.Confidence)
			}
		}
	}
}

// Spec: "a label with no subgraph_ref is invalid" — asserted at the level of a whole classification,
// not just on a hand-built label: EVERY label a real run produces names its region.
func TestEveryEmittedLabelNamesItsRegion(t *testing.T) {
	for _, f := range allFixtures() {
		res, err := Classify(context.Background(), f.ir, f.opts())
		if err != nil {
			t.Fatal(err)
		}
		known := map[string]bool{}
		for _, sg := range res.Subgraphs {
			known[sg.SubgraphID] = true
		}
		for _, n := range f.ir.Nodes {
			known[n.NodeID] = true
		}
		for _, l := range res.Labels {
			if l.SubgraphRef == "" {
				t.Errorf("%s: %q has no subgraph_ref", f.name, l.Pattern)
			}
			if !known[l.SubgraphRef] {
				t.Errorf("%s: %q references %q, which is neither a defined subgraph nor a node",
					f.name, l.Pattern, l.SubgraphRef)
			}
			if l.TaxonomyVersion != TaxonomyVersion {
				t.Errorf("%s: %q does not pin the taxonomy version", f.name, l.Pattern)
			}
		}
	}
}

// No classification, on any fixture, may produce a single label spanning the entire workflow. That is
// the failure mode the whole per-subgraph design exists to prevent, so it is asserted globally rather
// than only on the composite fixture.
func TestNoClassificationCollapsesToAWholeWorkflowLabel(t *testing.T) {
	for _, f := range allFixtures() {
		if len(f.ir.Nodes) < 3 {
			continue // a two-node workflow legitimately has one region
		}
		res, err := Classify(context.Background(), f.ir, f.opts())
		if err != nil {
			t.Fatal(err)
		}
		for _, sg := range res.Subgraphs {
			if len(sg.NodeIDs) == len(f.ir.Nodes) && len(f.ir.Nodes) > 4 {
				t.Errorf("%s: subgraph %s spans the whole %d-node workflow", f.name, sg.SubgraphID, len(f.ir.Nodes))
			}
		}
	}
}

// THE M4 EXIT CHECKLIST (PRD §13), asserted rather than ticked. Each sub-test is one checklist line;
// `go test -run TestM4ExitChecklist -v` prints the checklist with its verdicts.
func TestM4ExitChecklist(t *testing.T) {
	ctx := context.Background()
	composite := fxComposite()
	res, err := Classify(ctx, composite.ir, composite.opts())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("subgraphs carry pattern labels with confidence, per-subgraph", func(t *testing.T) {
		if len(res.Labels) == 0 {
			t.Fatal("no labels")
		}
		for _, l := range res.Labels {
			if l.SubgraphRef == "" || l.Confidence <= 0 {
				t.Errorf("%q: ref=%q conf=%v", l.Pattern, l.SubgraphRef, l.Confidence)
			}
		}
		if len(res.Subgraphs) < 2 {
			t.Errorf("want at least two distinct regions, got %d", len(res.Subgraphs))
		}
	})

	t.Run("all eight structural detectors fire on their signatures and not on near-misses", func(t *testing.T) {
		fired := map[Pattern]bool{}
		for _, f := range allFixtures() {
			r, err := Classify(ctx, f.ir, f.opts())
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range r.Labels {
				fired[l.Pattern] = true
			}
			for _, bad := range f.mustNot {
				for _, l := range r.Labels {
					if l.Pattern == bad {
						t.Errorf("%s: near-miss violated by %q", f.name, bad)
					}
				}
			}
		}
		for _, p := range append(StructuralPatterns(), Reflection) {
			if !fired[p] {
				t.Errorf("detector for %q never fired on any fixture", p)
			}
		}
	})

	t.Run("a composite workflow emits both labels, each tied to its own subgraph", func(t *testing.T) {
		routing := SubgraphIDFor([]string{"n_router", "n_billing", "n_tech"})
		rag := SubgraphIDFor([]string{"n_embed", "n_retrieve", "n_rerank", "n_answer"})
		if got := res.LabelsFor(routing); len(got) != 1 || got[0].Pattern != Routing {
			t.Errorf("subgraph A: %+v", got)
		}
		if got := res.LabelsFor(rag); len(got) != 1 || got[0].Pattern != RetrievalRAG {
			t.Errorf("subgraph B: %+v", got)
		}
	})

	t.Run("the LLM fallback runs only on ambiguous subgraphs, constrained, returning confidence", func(t *testing.T) {
		m := &stubModel{reply: []RawLabel{
			{Pattern: string(GuardrailsSafety), Confidence: conf(0.5)},
			{Pattern: "invented_pattern", Confidence: conf(0.9)},
		}}
		f := fxAmbiguous()
		r, err := Classify(ctx, f.ir, fallbackOpts(f, m))
		if err != nil {
			t.Fatal(err)
		}
		if r.LLMCalls != 1 {
			t.Errorf("llm calls = %d, want 1", r.LLMCalls)
		}
		if len(r.Labels) != 1 || r.Labels[0].Pattern != GuardrailsSafety || r.Labels[0].Source != SourceLLM {
			t.Errorf("labels = %+v", r.Labels)
		}
		if len(r.Diagnostics) != 1 {
			t.Errorf("the out-of-taxonomy answer was not diagnosed: %v", r.Diagnostics)
		}
	})

	t.Run("a fully rule-covered IR is deterministic and makes zero LLM calls", func(t *testing.T) {
		m := &stubModel{reply: []RawLabel{{Pattern: string(Planning), Confidence: conf(0.9)}}}
		first, err := Classify(ctx, composite.ir, fallbackOpts(composite, m))
		if err != nil {
			t.Fatal(err)
		}
		if first.LLMCalls != 0 || len(m.calls) != 0 {
			t.Fatalf("llm calls = %d", first.LLMCalls)
		}
		want, _ := json.Marshal(first)
		for i := 0; i < 5; i++ {
			again, _ := Classify(ctx, composite.ir, fallbackOpts(composite, m))
			got, _ := json.Marshal(again)
			if string(got) != string(want) {
				t.Fatalf("run %d differs", i)
			}
		}
	})

	t.Run("pattern to metric-set dispatch works", func(t *testing.T) {
		byRegion := MetricSetsForLabels(res.Labels)
		router := byRegion[SubgraphIDFor([]string{"n_router", "n_billing", "n_tech"})]
		rag := byRegion[SubgraphIDFor([]string{"n_embed", "n_retrieve", "n_rerank", "n_answer"})]
		if len(router) != 1 || router[0].Primary != "misroute_rate" {
			t.Errorf("router: %+v", router)
		}
		if len(rag) != 1 || rag[0].Primary != "relevance_at_k" {
			t.Errorf("rag: %+v", rag)
		}
	})

	t.Run("behavioral patterns are not asserted as confirmed", func(t *testing.T) {
		f := fxReflection()
		r, err := Classify(ctx, f.ir, f.opts())
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Labels) != 1 || !r.Labels[0].Candidate || r.Labels[0].Confidence > BehavioralCandidateCap {
			t.Errorf("reflection: %+v", r.Labels)
		}
	})

	t.Run("labels are written back additively and surfaced on the graph UI", func(t *testing.T) {
		labelled, err := WriteBack(composite.ir, res)
		if err != nil {
			t.Fatal(err)
		}
		if majorOf(labelled.IRVersion) != majorOf(composite.ir.IRVersion) {
			t.Errorf("MAJOR bumped: %q -> %q", composite.ir.IRVersion, labelled.IRVersion)
		}
		gv := BuildGraphView(labelled, res)
		if len(gv.Regions) != 2 {
			t.Errorf("the view does not surface both regions: %d", len(gv.Regions))
		}
		for _, r := range gv.Regions {
			for _, l := range r.Labels {
				if l.Source == "" || l.Confidence == 0 {
					t.Errorf("the view drops source/confidence: %+v", l)
				}
			}
		}
		// The empty state is representable as data.
		f := fxAmbiguous()
		er, _ := Classify(ctx, f.ir, f.opts())
		ev := BuildGraphView(f.ir, er)
		if len(ev.Unclassified) != 1 {
			t.Errorf("the empty state is not first-class: %+v", ev)
		}
	})
}
