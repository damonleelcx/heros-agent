package hostdiscovery

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// P30 task 2.1/2.3 — the row-level provenance index.

func viewWith(edges []patternclassifier.ViewEdge, labels []patternclassifier.ViewLabel) patternclassifier.GraphView {
	return patternclassifier.GraphView{
		Edges:   edges,
		Regions: []patternclassifier.ViewRegion{{SubgraphID: "sg_1", Labels: labels}},
	}
}

func edge(author string) patternclassifier.ViewEdge {
	return patternclassifier.ViewEdge{From: "a", To: "b", Kind: "data", Author: author}
}

func label(author discovery.FactAuthor) patternclassifier.ViewLabel {
	return patternclassifier.ViewLabel{Pattern: "PromptChaining", Author: author}
}

func TestTheProvenanceIndexIsTheSortedSetOfAuthorsPresent(t *testing.T) {
	cases := map[string]struct {
		view patternclassifier.GraphView
		want string
	}{
		// Sorted, so a re-derivation compared against a stored value cannot fail on map order.
		"mixed": {
			viewWith([]patternclassifier.ViewEdge{edge("heros"), edge("frontend")},
				[]patternclassifier.ViewLabel{label(discovery.AuthorDetector)}),
			"detector,frontend,heros",
		},
		"one author, once": {
			viewWith([]patternclassifier.ViewEdge{edge("frontend"), edge("frontend")}, nil),
			"frontend",
		},
		// 🔴 A view whose facts carry NO author is a pre-P30 graph. Empty — stored as SQL NULL, read
		// back as `legacy`. Claiming `frontend` here would be the back-fill the design refuses.
		"unauthored facts": {
			viewWith([]patternclassifier.ViewEdge{edge("")}, []patternclassifier.ViewLabel{label("")}),
			"",
		},
		// A view with no facts at all attributes nothing, rather than attributing an empty set.
		"no facts": {patternclassifier.GraphView{}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ProvenanceOf(tc.view); got != tc.want {
				t.Errorf("ProvenanceOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// 🔴 THE NO-DRIFT FENCE. The stored index is DERIVED from the document at write time, in one place, so
// it cannot disagree with what it indexes. A summary that can drift is worse than no summary, because
// it is believed — so this asserts that re-deriving from the written view reproduces the stored value.
//
// It is the property the PGStore relies on: Put never accepts a caller-supplied Provenance.
func TestTheStoredProvenanceIndexCannotDriftFromTheDocument(t *testing.T) {
	v := viewWith([]patternclassifier.ViewEdge{edge("heros"), edge("frontend")},
		[]patternclassifier.ViewLabel{label(discovery.AuthorDetector)})

	// A caller trying to understate what the graph contains — the exact drift this prevents.
	g := Graph{TenantID: "t1", WorkflowID: "wf", SourceRevision: "rev", View: v, Provenance: "frontend"}

	// What the store will actually write is derived, not taken from g.
	if got := ProvenanceOf(g.View); got == g.Provenance {
		t.Fatal("the fixture does not model drift, so this test proves nothing")
	}
	if got, want := ProvenanceOf(g.View), "detector,frontend,heros"; got != want {
		t.Errorf("the derived index is %q, want %q — a caller's claim must not reach the column", got, want)
	}
}

func TestHasAuthorMatchesWholeMembersNotSubstrings(t *testing.T) {
	const stored = "detector,frontend"
	if !HasAuthor(stored, discovery.AuthorFrontend) {
		t.Error("frontend is not found in a set that contains it")
	}
	if HasAuthor(stored, discovery.AuthorHEROS) {
		t.Error("heros is found in a set that does not contain it")
	}
	// A hypothetical future author whose name CONTAINS an existing one must not match it.
	if HasAuthor("heros-lite", discovery.AuthorHEROS) {
		t.Error("membership is a substring test — `heros-lite` matched `heros`, which is how a future " +
			"author silently inherits another's incident queries")
	}
	if HasAuthor("", discovery.AuthorFrontend) {
		t.Error("an empty index reports membership")
	}
}
