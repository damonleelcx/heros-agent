package transform

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P34 task 5.8 / FR16 / QA fence 9.9 — a topology this language cannot carry is REFUSED BY NAME, and
// the override is not dropped.
//
// 🔴 The second half is the one that matters and the one a careless implementation gets wrong. A
// dropped override produces a diff that builds, passes review, runs, and is scored — against source
// that was never rewired, under a config_hash that already records the new topology. The number is
// wrong and looks exactly like a number that is right.

// graphResolved builds a Resolved carrying a topology declaration, in the hashed shape the refusal
// reads. It uses the PROJECTION rather than an authored spec because that is what `checkGraphTopology`
// reads, and because the projection is what a measurement is filed under.
func graphResolved(language string, groups []variantspec.ResolvedGraphGroup, edges []variantspec.ResolvedEdge) *variantspec.Resolved {
	r := resolvedIn(language, map[string]variantspec.ResolvedOverride{})
	r.Config.GraphGroups = groups
	r.Config.Edges = edges
	return r
}

func concurrentGroup() []variantspec.ResolvedGraphGroup {
	return []variantspec.ResolvedGraphGroup{{Nodes: []string{"n_b", "n_c"}, Concurrent: true}}
}

func mergedGroup() []variantspec.ResolvedGraphGroup {
	return []variantspec.ResolvedGraphGroup{{
		Nodes: []string{"n_b", "n_c"},
		Merge: &variantspec.ResolvedMerge{Into: "n_d", Strategy: "all-fields", OnNodeFailure: "fail-fast"},
	}}
}

func predicateEdges() []variantspec.ResolvedEdge {
	return []variantspec.ResolvedEdge{
		{FromNodeID: "n_a", ToNodeID: "n_b", Kind: variantspec.EdgeKindPredicate, Predicate: "route"},
	}
}

// TestEveryLanguageRefusesTopologyByName — no language ships a topology rewriter, so every one of them
// must refuse, and each refusal must name the AXIS and the FORM.
//
// 🚫 The alternative that must not be reachable is silence. If `Generate` returned a patch here, the
// declaration would have been dropped.
func TestEveryLanguageRefusesTopologyByName(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyHarnessSrc)

	for _, tc := range []struct {
		name   string
		build  func(string) *variantspec.Resolved
		form   string
		anchor string
	}{
		{"concurrent group", func(l string) *variantspec.Resolved { return graphResolved(l, concurrentGroup(), nil) },
			"concurrent group", "n_b"},
		{"merge", func(l string) *variantspec.Resolved { return graphResolved(l, mergedGroup(), nil) },
			"merge", "n_b"},
		{"conditional edge", func(l string) *variantspec.Resolved { return graphResolved(l, nil, predicateEdges()) },
			"conditional edge", "n_a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, lang := range RegisteredLanguages() {
				r := tc.build(lang)
				_, err := Generate(r, root)
				if err == nil {
					t.Fatalf("%s/%s: Generate produced a patch for a topology no rewriter materializes. "+
						"The declaration is already in this configuration's config_hash, so applying it as a "+
						"no-op lets that hash be scored against source that still runs the old topology",
						lang, tc.name)
				}
				if !errors.Is(err, ErrUnsafeRewrite) {
					t.Fatalf("%s/%s: want a typed ErrUnsafeRewrite, got %v", lang, tc.name, err)
				}
				var re *RewriteError
				if !errors.As(err, &re) {
					t.Fatalf("%s/%s: the refusal is not a RewriteError: %v", lang, tc.name, err)
				}
				if re.Dim != graphRefusalDim {
					t.Errorf("%s/%s: the refusal names axis %q, want %q — a reader has to know WHICH axis "+
						"was refused", lang, tc.name, re.Dim, graphRefusalDim)
				}
				if re.NodeID != tc.anchor {
					t.Errorf("%s/%s: the refusal anchors to node %q, want %q — a RewriteError with no node "+
						"is one a reader cannot navigate to", lang, tc.name, re.NodeID, tc.anchor)
				}
				if !strings.Contains(err.Error(), tc.form) {
					t.Errorf("%s/%s: the refusal does not name the form: %v", lang, tc.name, err)
				}
				// 🔴 The load-bearing sentence: it must say the declaration was NOT dropped, because a
				// reader who believes it was silently ignored will go looking for their change in the diff
				// and conclude the platform is broken.
				if !strings.Contains(err.Error(), "not dropped") {
					t.Errorf("%s/%s: the refusal does not say the declaration was not dropped: %v",
						lang, tc.name, err)
				}
				// And it must name the missing artifact — this is our backlog, not a permanent fact.
				if re.Cause != CauseNoMaterializer {
					t.Errorf("%s/%s: the refusal carries cause %q, want %q. Concurrency and conditional "+
						"routing ARE expressible at a call site — that is what makes them a codemod rather "+
						"than a deployment policy — so calling this permanent would tell a reader that a "+
						"thing which is merely unbuilt can never be built",
						lang, tc.name, re.Cause, CauseNoMaterializer)
				}
			}
		})
	}
}

// TestNoTopologyDeclarationIsNotRefused keeps the gate from becoming a blanket refusal. If this went
// red, every spec in the product would stop generating a diff.
func TestNoTopologyDeclarationIsNotRefused(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyHarnessSrc)
	r := graphResolved("python", nil, []variantspec.ResolvedEdge{
		{FromNodeID: "n_a", ToNodeID: "n_b", Kind: "data"},
	})
	if _, err := Generate(r, root); err != nil && errors.Is(err, ErrUnsafeRewrite) {
		var re *RewriteError
		if errors.As(err, &re) && re.Dim == graphRefusalDim {
			t.Fatalf("a spec declaring no topology was refused on the graph axis: %v", err)
		}
	}
}

// TestANonConcurrentGroupWithNoMergeIsNotRefused — a group that declares neither concurrency nor a
// merge asks the transform for nothing, so there is nothing to refuse. Refusing it would make the
// declaration unusable for the modelling and comparison it is entirely correct for.
func TestANonConcurrentGroupWithNoMergeIsNotRefused(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyHarnessSrc)
	r := graphResolved("python", []variantspec.ResolvedGraphGroup{{Nodes: []string{"n_b", "n_c"}}}, nil)
	_, err := Generate(r, root)
	var re *RewriteError
	if errors.As(err, &re) && re.Dim == graphRefusalDim {
		t.Fatalf("a group declaring neither concurrency nor a merge was refused: %v", err)
	}
}

// TestGraphCoverageIsTotalAndSaysWhichThingIsMissing is FR18: a refusal names which of the frontend,
// the analysis, or the language support is missing, and never a generic unsupported state.
func TestGraphCoverageIsTotalAndSaysWhichThingIsMissing(t *testing.T) {
	cells := CoverageFor(graphRefusalDim)
	want := len(RegisteredLanguages()) * len(GraphForms())
	if len(cells) != want {
		t.Fatalf("the graph axis has %d cells, want %d (%d languages × %d forms). Absence is not a value "+
			"here: a missing cell renders on every surface as \"not applicable\", which is a claim about "+
			"the customer's code",
			len(cells), want, len(RegisteredLanguages()), len(GraphForms()))
	}
	for _, c := range cells {
		if c.Status != CoverageRefuses {
			t.Errorf("%s/%s claims to materialize, but graphMaterializers is empty; a `materializes` cell "+
				"nothing backs is FR16's dropped override wearing a coverage badge", c.Language, c.Form)
			continue
		}
		if c.MissingArtifact == "" {
			t.Errorf("%s/%s refuses and names no missing artifact; the reader is told to wait with no "+
				"idea for what", c.Language, c.Form)
		}
		if c.Note == "" {
			t.Errorf("%s/%s refuses with no reason a person can read", c.Language, c.Form)
		}
		// 🔴 CauseNoMaterializer, never CauseNotAtCallSite. Borrowing the permanent cause would spend the
		// taxonomy's only irreversible word on a thing that is merely unbuilt.
		if c.Cause != CauseNoMaterializer {
			t.Errorf("%s/%s carries cause %q, want %q", c.Language, c.Form, c.Cause, CauseNoMaterializer)
		}
	}

	// FR18's three-way distinction, exercised rather than described: a syntactic frontend's cells must
	// name the ANALYSIS, not the rewriter, because a topology declaration is validated against edges and
	// typed contracts a syntactic analyser never produces.
	sawAnalysis, sawRewriter := false, false
	for _, c := range cells {
		switch {
		case strings.Contains(c.MissingArtifact, "typed"):
			sawAnalysis = true
		case strings.Contains(c.MissingArtifact, "topology rewriter"):
			sawRewriter = true
		}
	}
	if !sawAnalysis && !sawRewriter {
		t.Error("no cell names either a missing analysis or a missing rewriter; the read has collapsed " +
			"into one generic unsupported state, which FR18 forbids")
	}
}

// TestTheGraphAxisDeclaresItselfAbsent — the honest status while no language materializes anything.
// 🔴 ABSENT is NOT "not applicable": the axis exists and the engine applies none of it, which is a fact
// about the PLATFORM rather than about the customer's code.
func TestTheGraphAxisDeclaresItselfAbsent(t *testing.T) {
	if got := StatusFor(graphRefusalDim); got != StatusAbsent {
		t.Fatalf("the graph axis declares itself %q, want %q while graphMaterializers is empty", got, StatusAbsent)
	}
	// And the loop axis is PARTIAL, so this test is reading a derived value rather than a constant.
	if got := StatusFor(string(variantspec.DimLoop)); got != StatusPartial {
		t.Errorf("the loop axis declares itself %q, want %q — reflexion materializes where a language can "+
			"read an answer, three strategies refuse everywhere permanently", got, StatusPartial)
	}
}
