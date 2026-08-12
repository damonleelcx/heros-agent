package patternclassifier

import (
	"context"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// P30 workstream 1 — surface honesty. Every assertion here is on a RENDERED STRING, because the defect
// these tasks fix was never a wrong number: it was a true number under a false sentence.

// syntacticReport is the discovery report a Python repository actually produces today: one contributing
// frontend, syntactic, nodes and no edges.
func syntacticReport(lang string, nodes int) discovery.DiscoveryReport {
	return discovery.DiscoveryReport{Frontends: []discovery.FrontendRun{
		{Language: lang, AnalysisKind: discovery.AnalysisSyntactic, Nodes: nodes, Edges: 0},
	}}
}

// edgelessIR is nodes with no edges — the shape every non-Go repository has.
func edgelessIR() *discovery.IR {
	return buildIR([]discovery.IRNode{node("n_a"), node("n_b"), node("n_c")}, nil)
}

func viewFor(t *testing.T, ir *discovery.IR, opts Options, rep discovery.DiscoveryReport) GraphView {
	t.Helper()
	res, err := Classify(context.Background(), ir, opts)
	if err != nil {
		t.Fatal(err)
	}
	return BuildGraphView(ir, res, rep)
}

// Task 1.1 — an edgeless graph carries a `no_topology` reason, and the reason NAMES the frontend and its
// analysis kind. The names come from the report; nothing in this package knows Python is syntactic.
func TestEdgelessGraphCarriesNoTopologyReasonFromTheDiscoveryReport(t *testing.T) {
	gv := viewFor(t, edgelessIR(), Options{Skills: NewStaticSkillResolver()}, syntacticReport("python", 3))

	if gv.Topology == nil {
		t.Fatal("a graph with nodes and zero edges carries no topology explanation at all")
	}
	if gv.Topology.Reason != ReasonNoTopology {
		t.Errorf("reason = %q, want %q", gv.Topology.Reason, ReasonNoTopology)
	}
	if len(gv.Topology.Frontends) != 1 || gv.Topology.Frontends[0].Language != "python" ||
		gv.Topology.Frontends[0].AnalysisKind != "syntactic" {
		t.Fatalf("the frontends did not reach the view: %+v", gv.Topology.Frontends)
	}
	for _, want := range []string{"python", "syntactic", "none has been looked for"} {
		if !strings.Contains(gv.Topology.Sentence, want) {
			t.Errorf("the sentence does not contain %q:\n  %s", want, gv.Topology.Sentence)
		}
	}
}

// 🔴 The half of 1.1 that makes it a fence rather than a string: the sentence must be DERIVED. Hand the
// same edgeless IR a report naming a TYPED frontend and the explanation must reverse — an absence of
// edges from a typed frontend is a finding about the code, not a limit of the analysis. A hand-written
// sentence in the view would pass the test above and fail this one.
func TestNoTopologySentenceReversesWhenTheFrontendIsTyped(t *testing.T) {
	rep := discovery.DiscoveryReport{Frontends: []discovery.FrontendRun{
		{Language: "go", AnalysisKind: discovery.AnalysisTyped, Nodes: 3, Edges: 0},
	}}
	gv := viewFor(t, edgelessIR(), Options{Skills: NewStaticSkillResolver()}, rep)

	if gv.Topology == nil {
		t.Fatal("no topology explanation")
	}
	if strings.Contains(gv.Topology.Sentence, "syntactic") {
		t.Errorf("a typed frontend is described as syntactic:\n  %s", gv.Topology.Sentence)
	}
	if !strings.Contains(gv.Topology.Sentence, "Nothing connects these call sites") {
		t.Errorf("a typed frontend's empty edge list is not reported as a finding:\n  %s", gv.Topology.Sentence)
	}
}

// A report with no frontends says so, rather than asserting that nothing is syntactic.
func TestNoTopologyWithNoReportedFrontendSaysSo(t *testing.T) {
	gv := viewFor(t, edgelessIR(), Options{Skills: NewStaticSkillResolver()}, discovery.DiscoveryReport{})
	if gv.Topology == nil {
		t.Fatal("no topology explanation")
	}
	if !strings.Contains(gv.Topology.Sentence, "recorded no contributing frontend") {
		t.Errorf("an unreported analysis is not reported as unknown:\n  %s", gv.Topology.Sentence)
	}
}

// A graph WITH edges has nothing to explain, and must not carry the explanation anyway.
func TestGraphWithEdgesCarriesNoTopologyExplanation(t *testing.T) {
	f := fxPromptChaining()
	gv := viewFor(t, f.ir, f.opts(), syntacticReport("python", 3))
	if gv.Topology != nil {
		t.Errorf("a graph with edges carries a no-topology explanation: %+v", gv.Topology)
	}
}

// Task 1.3 — the `llm_calls` copy. Three cases, three sentences, asserted as strings.
func TestLLMCallsNoteDistinguishesThreeCases(t *testing.T) {
	t.Run("zero labels is not full coverage", func(t *testing.T) {
		gv := viewFor(t, edgelessIR(), Options{Skills: NewStaticSkillResolver()}, syntacticReport("python", 3))
		if gv.LLMCalls != 0 {
			t.Fatalf("llm_calls = %d, want 0", gv.LLMCalls)
		}
		if strings.Contains(gv.LLMCallsNote, "Fully rule-covered") {
			t.Errorf("a graph with no labels at all is reported as fully rule-covered:\n  %s", gv.LLMCallsNote)
		}
		if !strings.Contains(gv.LLMCallsNote, "nothing looked") {
			t.Errorf("the zero-label case does not say nothing looked:\n  %s", gv.LLMCallsNote)
		}
	})

	t.Run("full coverage", func(t *testing.T) {
		f := fxPromptChaining()
		gv := viewFor(t, f.ir, f.opts(), discovery.DiscoveryReport{})
		if !strings.Contains(gv.LLMCallsNote, "Fully rule-covered") {
			t.Errorf("a fully covered graph is not reported as such:\n  %s", gv.LLMCallsNote)
		}
	})

	t.Run("partial coverage", func(t *testing.T) {
		// A chain the rules cover, plus fxAmbiguous's single control edge, which by construction matches
		// no signature. 🚫 Not a skip if the graph comes out unmixed: a skipped assertion is the third
		// case going unproved, which is the whole defect this task fixes.
		ir := buildIR(
			[]discovery.IRNode{
				node("n_extract"), node("n_summarize"), node("n_draft"),
				node("n_guard", withPrompt("check the request against policy")),
				node("n_solo", withPrompt("answer"), withSemantics("conditional", false)),
			},
			[]discovery.IREdge{
				dataEdge("n_extract", "n_summarize"), dataEdge("n_summarize", "n_draft"),
				controlEdge("n_guard", "n_solo"),
			},
		)
		gv := viewFor(t, ir, Options{Skills: NewStaticSkillResolver()}, discovery.DiscoveryReport{})
		if len(gv.Regions) == 0 || len(gv.Unclassified) == 0 {
			t.Fatalf("the fixture is not a mixed graph, so the third case is untested: "+
				"%d labelled, %d unlabelled", len(gv.Regions), len(gv.Unclassified))
		}
		if !strings.Contains(gv.LLMCallsNote, "Partly rule-covered") {
			t.Errorf("a partly covered graph is not reported as such:\n  %s", gv.LLMCallsNote)
		}
	})
}

// Task 1.4 — the four causes. Each is a distinct value AND a distinct sentence.
func TestFourUnclassifiedCausesAreDistinct(t *testing.T) {
	seen := map[string]UnclassifiedReason{}
	for _, r := range []UnclassifiedReason{
		ReasonNoTopologyToMatch, ReasonNoSignatureMatched, ReasonModelNotConsulted, ReasonModelAbstained,
	} {
		s := SentenceFor(r)
		if s == "" {
			t.Fatalf("cause %q has no sentence", r)
		}
		if prev, dup := seen[s]; dup {
			t.Errorf("causes %q and %q render the SAME sentence — the distinction is invisible", prev, r)
		}
		seen[s] = r
	}
	if len(seen) != 4 {
		t.Fatalf("want four distinct sentences, got %d", len(seen))
	}
	// 🚫 An unrecognised cause must not fall back to a plausible paragraph.
	if got := SentenceFor(UnclassifiedReason("invented")); got != "" {
		t.Errorf("an unknown cause renders a sentence anyway: %q", got)
	}
}

func TestUnclassifiedReasonPrecedence(t *testing.T) {
	cases := []struct {
		name                                      string
		noTopology, fallbackConfigured, consulted bool
		want                                      UnclassifiedReason
	}{
		{"no topology wins over everything", true, true, true, ReasonNoTopologyToMatch},
		{"consulted and unlabelled is an abstention", false, true, true, ReasonModelAbstained},
		{"no model configured", false, false, false, ReasonModelNotConsulted},
		{"model configured, this region not reached", false, true, false, ReasonNoSignatureMatched},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := unclassifiedReason(c.noTopology, c.fallbackConfigured, c.consulted); got != c.want {
				t.Errorf("reason = %q, want %q", got, c.want)
			}
		})
	}
}

// The end-to-end shape: an unlabelled region on an edgeless graph reports `no_topology`, not the old
// undifferentiated "not yet classified".
func TestUnclassifiedRegionOnAnEdgelessGraphReportsNoTopology(t *testing.T) {
	gv := viewFor(t, edgelessIR(), Options{Skills: NewStaticSkillResolver()}, syntacticReport("python", 3))
	if len(gv.Unclassified) == 0 {
		t.Fatal("an edgeless graph produced no unclassified regions")
	}
	for _, r := range gv.Unclassified {
		if r.Reason != ReasonNoTopologyToMatch {
			t.Errorf("region %s reason = %q, want %q", r.SubgraphID, r.Reason, ReasonNoTopologyToMatch)
		}
		if r.ReasonSentence != SentenceFor(ReasonNoTopologyToMatch) {
			t.Errorf("region %s sentence does not match its reason: %q", r.SubgraphID, r.ReasonSentence)
		}
	}
}

// Every unclassified region carries a reason. A region with an empty reason renders as the old
// undifferentiated state, which is the thing being removed.
func TestEveryUnclassifiedRegionCarriesAReason(t *testing.T) {
	f := fxAmbiguous()
	gv := viewFor(t, f.ir, f.opts(), discovery.DiscoveryReport{})
	if len(gv.Unclassified) == 0 {
		t.Skip("fixture produced no unclassified regions")
	}
	for _, r := range gv.Unclassified {
		if r.Reason == "" || r.ReasonSentence == "" {
			t.Errorf("region %s is unlabelled with no stated cause: %+v", r.SubgraphID, r)
		}
	}
}
