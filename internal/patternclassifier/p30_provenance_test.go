package patternclassifier

import (
	"context"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// P30 workstream 2 — every fact records who authored it.

// Task 2.2 — a detector's label is stamped `detector`, in the ONE place labels are minted, with no
// edit to any of the eight shipped detectors.
func TestARuleLabelIsAuthoredByADetector(t *testing.T) {
	f := fxPromptChaining()
	res, err := Classify(context.Background(), f.ir, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Labels) == 0 {
		t.Fatal("the fixture produced no labels, so this proves nothing")
	}
	for _, l := range res.Labels {
		if l.Author != discovery.AuthorDetector {
			t.Errorf("label %q author = %q, want %q", l.Pattern, l.Author, discovery.AuthorDetector)
		}
	}
}

// 🔴 The author comes from the PROPOSAL, not from a constant at the mint site. P30's agent emits its
// labels as RegionProposals so they pass the same partitioner and the same precedence rule (D3) — so a
// hardcoded `detector` here would silently re-attribute every one of them to a rule that never fired.
// This is the assertion that a hardcode would fail.
func TestAProposalsAuthorSurvivesToItsLabel(t *testing.T) {
	p := RegionProposal{Author: discovery.AuthorHEROS}
	if got := p.AuthorOrDetector(); got != discovery.AuthorHEROS {
		t.Errorf("AuthorOrDetector() = %q, want %q", got, discovery.AuthorHEROS)
	}
	// And the default is `detector`, which is what makes the eight shipped detectors need no edit.
	if got := (RegionProposal{}).AuthorOrDetector(); got != discovery.AuthorDetector {
		t.Errorf("an unstamped proposal defaults to %q, want %q", got, discovery.AuthorDetector)
	}
}

// 🔴 TASK 2.8 — THE WRITER'S OWN CHECK. A fact written without an author is indistinguishable from a
// pre-P30 `legacy` fact the moment it is stored, and that is the only moment it is recoverable.
//
// Proved red by deleting the ValidAuthor guard in WriteBack: an unauthored label is then written, and
// reading it back reports `legacy` for a fact this build produced.
func TestWriteBackRefusesAFactWithNoAuthor(t *testing.T) {
	f := fxPromptChaining()
	res, err := Classify(context.Background(), f.ir, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	// Strip the author from one label — exactly what a future mint site that forgets would produce.
	res.Labels[0].Author = ""

	if _, err := WriteBack(f.ir, res); err == nil {
		t.Fatal("WriteBack accepted a label with no author. Once stored it reads as `legacy`, which is " +
			"the one value a writer may never produce — and nothing afterwards can tell it from a fact " +
			"written before authorship existed.")
	} else if !strings.Contains(err.Error(), "who authored it") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

// 🚫 `legacy` is a READING, never a value a writer may supply. A writer stamping it is claiming not to
// know who it is.
func TestLegacyIsNotAWritableAuthor(t *testing.T) {
	if discovery.ValidAuthor(discovery.AuthorLegacy) {
		t.Error("`legacy` is accepted as a writable author. It is what an ABSENT author reads as; a " +
			"writer that stamps it has recorded ignorance as a fact.")
	}
	for _, a := range []discovery.FactAuthor{
		discovery.AuthorFrontend, discovery.AuthorDetector, discovery.AuthorHEROS, discovery.AuthorOperator,
	} {
		if !discovery.ValidAuthor(a) {
			t.Errorf("%q is not a writable author", a)
		}
	}
}

// Task 2.3 — the READER maps an absent author to `legacy`, and `legacy` is distinguishable from
// `frontend` in the result. This is the half that makes "which of these did the model write?"
// answerable on a mixed store.
func TestAnUnauthoredStoredLabelReadsAsLegacyAndNotAsFrontend(t *testing.T) {
	f := fxPromptChaining()
	res, err := Classify(context.Background(), f.ir, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	labelled, err := WriteBack(f.ir, res)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a document written before P30: clear the stored author on the wire records.
	for i := range labelled.Subgraphs {
		for j := range labelled.Subgraphs[i].PatternLabels {
			labelled.Subgraphs[i].PatternLabels[j].Author = ""
		}
	}
	for i := range labelled.Nodes {
		for j := range labelled.Nodes[i].PatternLabels {
			labelled.Nodes[i].PatternLabels[j].Author = ""
		}
	}

	back, _, err := ReadLabels(labelled)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) == 0 {
		t.Fatal("no labels were read back")
	}
	for _, l := range back {
		if l.Author != discovery.AuthorLegacy {
			t.Errorf("an unauthored stored label read as %q, want %q", l.Author, discovery.AuthorLegacy)
		}
		if l.Author == discovery.AuthorFrontend {
			t.Error("a pre-P30 label read as `frontend` — that is the back-fill the design refuses, " +
				"arriving through the reader instead of through a migration")
		}
	}
}

// Task 2.2 — a frontend edge is stamped in BuildIR, the one place edges are minted, so no language
// frontend can forget.
func TestAFrontendEdgeIsAuthoredInBuildIR(t *testing.T) {
	f := fxPromptChaining()
	if len(f.ir.Edges) == 0 {
		t.Fatal("the fixture has no edges")
	}
	for _, e := range f.ir.Edges {
		if discovery.AuthorOf(e.Author) != discovery.AuthorFrontend {
			t.Errorf("edge %s→%s author = %q, want %q",
				e.FromNodeID, e.ToNodeID, e.Author, discovery.AuthorFrontend)
		}
	}
}

// The author reaches the VIEW, on both edges and labels. The customer surface has to draw an inferred
// edge differently from a measured one, and it cannot tell them apart from anything else on the struct.
func TestTheAuthorReachesTheView(t *testing.T) {
	f := fxPromptChaining()
	gv := viewFor(t, f.ir, f.opts(), discovery.DiscoveryReport{})
	for _, e := range gv.Edges {
		if e.Author == "" {
			t.Errorf("view edge %s→%s carries no author", e.From, e.To)
		}
	}
	var sawLabel bool
	for _, r := range gv.Regions {
		for _, l := range r.Labels {
			sawLabel = true
			if l.Author == "" {
				t.Errorf("view label %q carries no author", l.Pattern)
			}
		}
	}
	if !sawLabel {
		t.Fatal("no labelled region reached the view, so the label half proves nothing")
	}
}
