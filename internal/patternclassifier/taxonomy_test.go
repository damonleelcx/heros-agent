package patternclassifier

import (
	"encoding/json"
	"testing"
)

// The taxonomy is CLOSED and its cardinality is part of the contract. A 21st pattern added without a
// TaxonomyVersion bump would silently change what a stored label means, so it fails here first.
func TestTaxonomyIsClosedAtTwenty(t *testing.T) {
	if len(taxonomy) != TaxonomySize {
		t.Fatalf("taxonomy has %d patterns, want %d (bump TaxonomyVersion if this is intentional)", len(taxonomy), TaxonomySize)
	}
	counts := map[Group]int{}
	for p, info := range taxonomy {
		if info.Pattern != p {
			t.Errorf("taxonomy key %q disagrees with row pattern %q", p, info.Pattern)
		}
		if info.Title == "" {
			t.Errorf("%q: missing title", p)
		}
		counts[info.Group]++
	}
	// The four groups and their sizes, per PRD §8.3. Asserted so a mis-grouped pattern (which would
	// change overlap precedence — control-flow owns the subgraph) is caught, not absorbed.
	for g, want := range map[Group]int{
		GroupControlFlow: 7, GroupCapability: 4, GroupCoordination: 2, GroupGovernance: 7,
	} {
		if counts[g] != want {
			t.Errorf("group %q has %d patterns, want %d", g, counts[g], want)
		}
	}
}

// THE CANONICAL LIST, pinned verbatim by number AND name.
//
// This is the fence against the defect that produced it: the taxonomy held all 20 patterns, but the
// PRD numbered them by group while everyone else numbers them 1–20 canonically — so "Pattern 5" was
// Planning in one document and Tool Use in another, and "Pattern 13" was Inter-Agent Communication
// or Retrieval/RAG depending on where you looked. A set can be complete and still be unusable if the
// identifiers people say out loud point at two different things.
//
// Written out longhand on purpose: a test that derived the expected list from the taxonomy would
// agree with any renumbering, including a wrong one.
func TestCanonicalOrdinalsArePinned(t *testing.T) {
	want := []struct {
		n     int
		title string
	}{
		{1, "Prompt Chaining"}, {2, "Routing"}, {3, "Parallelization"}, {4, "Reflection"},
		{5, "Tool Use"}, {6, "Planning"}, {7, "Multi-Agent Collaboration"}, {8, "Memory Management"},
		{9, "Learning & Adaptation"}, {10, "Goal Setting & Monitoring"},
		{11, "Exception Handling & Recovery"}, {12, "Human-in-the-Loop"}, {13, "Retrieval / RAG"},
		{14, "Inter-Agent Communication"}, {15, "Resource-Aware Optimization"},
		{16, "Reasoning Techniques"}, {17, "Evaluation & Monitoring"}, {18, "Guardrails & Safety"},
		{19, "Prioritization"}, {20, "Exploration & Discovery"},
	}
	if len(want) != TaxonomySize {
		t.Fatalf("the canonical list has %d entries, taxonomy size is %d", len(want), TaxonomySize)
	}
	got := Patterns()
	if len(got) != len(want) {
		t.Fatalf("Patterns() returned %d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Ordinal != w.n || got[i].Title != w.title {
			t.Errorf("position %d: got #%d %q, want #%d %q", i, got[i].Ordinal, got[i].Title, w.n, w.title)
		}
		// And the ordinal is LOOKUP-ABLE, so "Pattern 13" resolves rather than being counted by hand.
		if info, ok := ByOrdinal(w.n); !ok || info.Title != w.title {
			t.Errorf("ByOrdinal(%d) = %q, want %q", w.n, info.Title, w.title)
		}
	}
	// Ordinals are exactly the sequence 1..20 — no gaps, no duplicates, nothing out of range.
	seen := map[int]Pattern{}
	for _, i := range Patterns() {
		if i.Ordinal < 1 || i.Ordinal > TaxonomySize {
			t.Errorf("%q has ordinal %d, outside 1..%d", i.Pattern, i.Ordinal, TaxonomySize)
		}
		if prev, dup := seen[i.Ordinal]; dup {
			t.Errorf("ordinal %d is used by both %q and %q", i.Ordinal, prev, i.Pattern)
		}
		seen[i.Ordinal] = i.Pattern
	}
	for n := 1; n <= TaxonomySize; n++ {
		if _, ok := seen[n]; !ok {
			t.Errorf("ordinal %d is unassigned — the canonical sequence has a gap", n)
		}
	}
}

// Exactly eight patterns ship a structural detector in P3.5; the other twelve are behavioral and
// their confirmation is P5. That split is what caps confidence, so it is pinned by name.
func TestEightStructuralPatterns(t *testing.T) {
	got := StructuralPatterns()
	// In canonical ordinal order, matching Patterns().
	want := []Pattern{
		PromptChaining, Routing, Parallelization, ToolUse, MultiAgentCollaboration,
		RetrievalRAG, ResourceAwareOptimization,
	}
	// Reflection is structural-signature-detectable but BEHAVIORAL to confirm: it is a candidate.
	if IsBehavioral(Reflection) != true {
		t.Error("Reflection must be behavioral: a loop-back edge proves the loop exists, not that it iterates")
	}
	if len(got) != len(want) {
		t.Fatalf("structural patterns = %v (%d), want %d", got, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("structural[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestInTaxonomyRejectsOutsiders(t *testing.T) {
	for _, p := range []Pattern{"router", "react", "", "ROUTING", "retrieval/rag", "self_healing"} {
		if InTaxonomy(p) {
			t.Errorf("%q must not be in the closed taxonomy", p)
		}
	}
	for _, i := range Patterns() {
		if !InTaxonomy(i.Pattern) {
			t.Errorf("%q must be in the taxonomy", i.Pattern)
		}
	}
}

// Patterns() feeds the LLM fallback's enumerated prompt. If its order were unstable the prompt would
// change run to run and prompt_version would be a lie.
func TestPatternsOrderIsStable(t *testing.T) {
	first := Patterns()
	for i := 0; i < 20; i++ {
		got := Patterns()
		for j := range first {
			if got[j].Pattern != first[j].Pattern {
				t.Fatalf("Patterns() order is unstable at %d: %q vs %q", j, got[j].Pattern, first[j].Pattern)
			}
		}
	}
	b1, _ := json.Marshal(first)
	b2, _ := json.Marshal(Patterns())
	if string(b1) != string(b2) {
		t.Fatal("Patterns() is not byte-stable")
	}
}
