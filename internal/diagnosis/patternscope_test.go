package diagnosis

import (
	"context"
	"testing"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

func contains(codes []TaxonomyCode, c TaxonomyCode) bool {
	for _, x := range codes {
		if x == c {
			return true
		}
	}
	return false
}

// Task 7.3: a Routing node is checked for misroutes and NOT for a RAG failure mode (retrieval_miss);
// a Reflection node is checked for non-convergence / degradation-on-revision.
func TestAdmissibleCodes_PatternScoped(t *testing.T) {
	routing := AdmissibleCodes(patternclassifier.Routing)
	if !contains(routing, CauseMisroute) {
		t.Errorf("Routing must admit misroute; got %v", routing)
	}
	if contains(routing, CauseRetrievalMiss) {
		t.Errorf("Routing must NOT admit retrieval_miss (a RAG failure mode); got %v", routing)
	}

	reflection := AdmissibleCodes(patternclassifier.Reflection)
	if !contains(reflection, CauseNonConvergence) {
		t.Errorf("Reflection must admit non_convergence; got %v", reflection)
	}
	if !contains(reflection, CauseDegradationOnRevisit) {
		t.Errorf("Reflection must admit degradation_on_revision; got %v", reflection)
	}
	if contains(reflection, CauseRetrievalMiss) {
		t.Errorf("Reflection must NOT admit retrieval_miss; got %v", reflection)
	}

	// RAG admits retrieval_miss; Routing does not — the dispatcher is the pattern label.
	if !AdmissibleOn(CauseRetrievalMiss, patternclassifier.RetrievalRAG) {
		t.Errorf("retrieval_miss must be admissible on RAG")
	}
	if AdmissibleOn(CauseRetrievalMiss, patternclassifier.Routing) {
		t.Errorf("retrieval_miss must be inadmissible on Routing")
	}
}

// Task 7.2: the detector path refuses to diagnose a node with a mode its pattern cannot exhibit. Even
// if a retrieval-miss SIGNAL is present on a Routing node, no retrieval_miss cause is emitted for it.
func TestDetect_RefusesInadmissibleModeOnPattern(t *testing.T) {
	// A router node with a (nonsensical) retrieval-miss signal: zero retrieved chunks attribute.
	fc := routingWithRetrievalSignal("c1")
	got := Detect(routingOnlyIR(), fc)
	for _, c := range got {
		if c.Code == CauseRetrievalMiss {
			t.Fatalf("retrieval_miss must be refused on a Routing node; got %+v", got)
		}
	}
}

// Task 7.2 (analyst path): the analyst cannot record an inadmissible code either — a valid taxonomy
// code that the node's pattern does not admit is rejected.
func TestAnalyze_RefusesInadmissibleAnalystCode(t *testing.T) {
	ir := routingOnlyIR()
	// A residue case whose target is the router; analyst proposes retrieval_miss (valid code, wrong
	// pattern).
	residue := routingResidueCase("r1")
	analyst := &scriptedAnalyst{responses: map[string]AnalystResponse{
		"r1": {Code: string(CauseRetrievalMiss), Confidence: 0.9},
	}}
	_, rejects, err := Analyze(context.Background(), analyst, ir, []attribution.FailingCase{residue})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(rejects) != 1 || rejects[0].Reason != "code inadmissible on node pattern" {
		t.Fatalf("inadmissible analyst code must be rejected; got %+v", rejects)
	}
}
