package patternclassifier

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The headline detector test: every hand-labeled fixture gets exactly the labels it was hand-labeled
// with, against exactly the right region — and none of the patterns it must not get.
//
// It runs the WHOLE pipeline (partition → detect → arbitrate → validate → emit), not each predicate
// in isolation, because a detector that is right on its own and wrong after arbitration is still
// wrong, and only the end-to-end path shows that.
func TestDetectorsOnHandLabeledFixtures(t *testing.T) {
	for _, f := range allFixtures() {
		t.Run(f.name, func(t *testing.T) {
			res, err := Classify(context.Background(), f.ir, f.opts())
			if err != nil {
				t.Fatalf("classify: %v", err)
			}
			for _, w := range f.want {
				ref := w.ref()
				var found *Label
				for i := range res.Labels {
					if res.Labels[i].Pattern == w.pattern && res.Labels[i].SubgraphRef == ref {
						found = &res.Labels[i]
					}
				}
				if found == nil {
					t.Errorf("missing %q on region %s (%v)\ngot: %s", w.pattern, ref, w.nodeIDs, dumpLabels(res))
					continue
				}
				if found.Source != SourceRule {
					t.Errorf("%q: source = %q, want rule", w.pattern, found.Source)
				}
				if found.Candidate != w.candidate {
					t.Errorf("%q: candidate = %v, want %v", w.pattern, found.Candidate, w.candidate)
				}
			}
			for _, bad := range f.mustNot {
				for _, l := range res.Labels {
					if l.Pattern == bad {
						t.Errorf("near-miss violated: %q was emitted on %s\ngot: %s", bad, l.SubgraphRef, dumpLabels(res))
					}
				}
			}
		})
	}
}

func dumpLabels(r Result) string {
	b, _ := json.MarshalIndent(r.Labels, "", "  ")
	return string(b)
}

// Task 3.9: a detector is a PURE function of the IR. Same input, identical output — byte for byte,
// every time. Anything less and the labels are not diffable and the "zero LLM calls" guarantee is
// not checkable.
func TestClassificationIsBytePureAcrossRuns(t *testing.T) {
	for _, f := range allFixtures() {
		first, err := Classify(context.Background(), f.ir, f.opts())
		if err != nil {
			t.Fatalf("%s: %v", f.name, err)
		}
		want, _ := json.Marshal(first)
		for i := 0; i < 25; i++ {
			got, err := Classify(context.Background(), f.ir, f.opts())
			if err != nil {
				t.Fatalf("%s: %v", f.name, err)
			}
			b, _ := json.Marshal(got)
			if string(b) != string(want) {
				t.Fatalf("%s: run %d differs from run 0\nwant: %s\ngot:  %s", f.name, i+1, want, b)
			}
		}
	}
}

// Task 3.5's near-miss is not just "no label": a stale tool binding is a real finding and must be
// VISIBLE. A node that will fail at runtime must not be reported as a clean node.
func TestUnresolvableToolBindingIsDiagnosedNotSilent(t *testing.T) {
	f := fxNearMissUnresolvableToolIsNotToolUse()
	res, err := Classify(context.Background(), f.ir, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diagnostics) == 0 {
		t.Fatal("a tool binding that does not resolve must be diagnosed, not silently ignored")
	}
	if !strings.Contains(res.Diagnostics[0].Reason, "deleted_skill") {
		t.Errorf("the diagnostic must name the unresolvable skill: %s", res.Diagnostics[0])
	}
}

// Classify refuses to run without a registry snapshot rather than reporting "no tool use". An
// unavailable registry is an outage, not a workflow property.
func TestClassifyRefusesWithoutASkillResolver(t *testing.T) {
	if _, err := Classify(context.Background(), fxToolUse().ir, Options{}); err == nil {
		t.Fatal("classifying without a Skills snapshot must fail loudly")
	}
	if _, err := Classify(context.Background(), nil, Options{Skills: NewStaticSkillResolver()}); err == nil {
		t.Fatal("a nil IR must fail loudly")
	}
}

// Task 3.4 / 8.6: a Reflection loop is a CANDIDATE at the capped band, never a confirmed fact.
func TestReflectionIsACappedCandidate(t *testing.T) {
	f := fxReflection()
	res, err := Classify(context.Background(), f.ir, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	labels := res.LabelsFor(SubgraphIDFor([]string{"n_generate", "n_critique"}))
	if len(labels) != 1 || labels[0].Pattern != Reflection {
		t.Fatalf("want exactly one Reflection label, got %s", dumpLabels(res))
	}
	l := labels[0]
	if !l.Candidate {
		t.Error("Reflection from structure alone must be marked candidate")
	}
	if l.Confidence > BehavioralCandidateCap {
		t.Errorf("confidence %.2f exceeds the candidate cap %.2f", l.Confidence, BehavioralCandidateCap)
	}
}

// The composite fixture is the per-subgraph proof (task 8.3): two patterns, two DIFFERENT regions,
// neither collapsing into a whole-workflow label.
func TestCompositeEmitsBothLabelsAgainstTheirOwnSubgraphs(t *testing.T) {
	f := fxComposite()
	res, err := Classify(context.Background(), f.ir, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	routing := SubgraphIDFor([]string{"n_router", "n_billing", "n_tech"})
	rag := SubgraphIDFor([]string{"n_embed", "n_retrieve", "n_rerank", "n_answer"})
	if routing == rag {
		t.Fatal("the two regions must have different ids")
	}
	if got := res.LabelsFor(routing); len(got) != 1 || got[0].Pattern != Routing {
		t.Errorf("subgraph A: want exactly Routing, got %+v", got)
	}
	if got := res.LabelsFor(rag); len(got) != 1 || got[0].Pattern != RetrievalRAG {
		t.Errorf("subgraph B: want exactly Retrieval/RAG, got %+v", got)
	}
	// Tool Use on n_tech co-exists with the Routing region that owns it — both, not a contest.
	if got := res.LabelsFor("n_tech"); len(got) != 1 || got[0].Pattern != ToolUse {
		t.Errorf("n_tech: want Tool Use co-existing inside the routing branch, got %+v", got)
	}
	// No label spans the whole workflow.
	for _, sg := range res.Subgraphs {
		if len(sg.NodeIDs) == len(f.ir.Nodes) {
			t.Errorf("a label spans the whole workflow: %s", sg.SubgraphID)
		}
	}
}

// The ambiguous fixture must be left for the fallback — the rules must NOT invent a label for it.
func TestAmbiguousIRProducesNoRuleLabelAndIsAllResidue(t *testing.T) {
	f := fxAmbiguous()
	res, err := Classify(context.Background(), f.ir, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Labels) != 0 {
		t.Fatalf("no structural signature matches; rules must not guess: %s", dumpLabels(res))
	}
	if len(res.Residue) != 1 || len(res.Residue[0].NodeIDs) != 2 {
		t.Fatalf("the whole IR should be residue, got %+v", res.Residue)
	}
}

func TestCyclesFindsSelfEdgesAndMultiNodeLoops(t *testing.T) {
	g := newGraph(fxReflection().ir)
	if got := g.cycles(); len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("two-node loop: got %v", got)
	}
	self := buildIR(nil, nil)
	self.Nodes = fxToolUse().ir.Nodes
	self.Edges = append(self.Edges, dataEdge("n_agent", "n_agent"))
	if got := newGraph(self).cycles(); len(got) != 1 || got[0][0] != "n_agent" {
		t.Fatalf("self-edge: got %v", got)
	}
	// A plain chain has no cycle.
	if got := newGraph(fxPromptChaining().ir).cycles(); len(got) != 0 {
		t.Fatalf("a linear chain has no cycle: %v", got)
	}
}
