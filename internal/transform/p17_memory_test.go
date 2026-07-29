package transform

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P17 §7 — the interim refusal (decisions.md D4). The hard part of the phase, and the one whose failure
// mode is not "a feature is missing" but "a measurement is false".
//
// Every test here runs the REAL Generate against a REAL fixture repo. Nothing hand-feeds a rewriter: the
// bug this file exists to catch is a memory override that reaches the end of the pipeline without being
// refused, and a test that calls refuseMemory directly cannot see that.

// memoryEntry builds a resolved memory entry the way the loader would.
func memoryEntry(t *testing.T, strategy string) *registry.MemoryEntry {
	t.Helper()
	st := registry.MemoryStrategyNamed(strategy)
	if st == nil {
		t.Fatalf("memoryEntry: %q is not a builtin strategy", strategy)
	}
	return &registry.MemoryEntry{
		VersionID: strings.Repeat("e", 64), Name: "m",
		Spec: registry.MemorySpec{Strategy: strategy}, Strategy: st,
	}
}

// realStrategies is every builtin except the identity — the ones that MUST refuse.
func realStrategies(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, st := range registry.BuiltinMemoryStrategies() {
		if st.Name() != registry.StrategyNone {
			out = append(out, st.Name())
		}
	}
	if len(out) == 0 {
		t.Fatal("the builtin set contains nothing but the identity strategy; this suite would assert nothing")
	}
	return out
}

// TestMemoryOverrideRefusedInASTEngine — task 7.1 🔴, as P18 §4 leaves it.
//
// 🔴 P17 asserted one cause for every Go cell, because Go had no materializer and the gap was uniform.
// P18 gave Go a content-blind runtime and a verified anthropic response conversion, so its causes are now
// per-strategy — and collapsing them back to one would tell three different readers the same wrong thing:
//
//	a content-reading strategy → CauseNotAtCallSite. Permanent: a Go message is the customer's SDK type,
//	                             so its text is not readable without importing their SDK.
//	a content-blind strategy   → CauseCallSiteShape on this fixture, whose call is a bare statement with
//	                             no variable to record the response from. The author can change that.
//
// What did NOT change: no memory override is silently dropped, and a refusal never produces a diff.
func TestMemoryOverrideRefusedInASTEngine(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	for _, strategy := range realStrategies(t) {
		t.Run(strategy, func(t *testing.T) {
			p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
				ids["summarize"]: memoryOverride(t, strategy),
			}), root)
			if err == nil {
				t.Fatalf("the Go engine APPLIED a %s memory override at a call site with no variable to "+
					"record the response from", strategy)
			}
			if p != nil {
				t.Error("a refused memory override produced a patch; a refusal must produce NO diff")
			}
			if !errors.Is(err, ErrUnsafeRewrite) {
				t.Fatalf("err = %v, want ErrUnsafeRewrite", err)
			}
			var re *RewriteError
			if !errors.As(err, &re) {
				t.Fatalf("the refusal is not a *RewriteError: %v", err)
			}
			if re.NodeID != ids["summarize"] {
				t.Errorf("the refusal names node %q, want %q", re.NodeID, ids["summarize"])
			}
			if re.Dim != string(variantspec.DimMemory) {
				t.Errorf("the refusal names dimension %q, want memory", re.Dim)
			}

			want := CauseCallSiteShape
			if !memoryContentBlindStrategies[strategy] {
				want = CauseNotAtCallSite
			}
			if re.Cause != want {
				t.Errorf("cause = %q, want %q for strategy %q", re.Cause, want, strategy)
			}
			if !strings.Contains(err.Error(), strategy) {
				t.Errorf("the refusal does not name the strategy that was refused: %v", err)
			}
		})
	}
}

// TestMemoryOverrideRefusedInSpanEngine — task 7.2 🔴, as P18 leaves it.
//
// 🔴 This test's CLAIM changed when P18 landed, and the change is the point rather than a maintenance
// chore. P17 asserted that every span-engine cell refuses with CauseNoMaterializer, because no language
// had one. Python now does. Keeping the old assertion would have meant asserting a falsehood; deleting
// the test would have dropped the guarantee. So it splits along the line P18 actually drew:
//
//	an UNCOVERED language      → still CauseNoMaterializer. Nothing the author writes could help.
//	a COVERED language whose
//	call site cannot carry it  → CauseCallSiteShape. The author CAN act on it.
//
// What did NOT change, and is re-asserted below: no memory override is ever silently dropped, and a
// refusal never produces a diff.
func TestMemoryOverrideRefusedInSpanEngine(t *testing.T) {
	t.Run("an uncovered language refuses about the platform", func(t *testing.T) {
		root := spanTarget(t, "pipeline.ts", tsMultiTurnSrc)
		id := onlyNode(t, root, "typescript")

		for _, strategy := range realStrategies(t) {
			p, err := Generate(resolvedIn("typescript", map[string]variantspec.ResolvedOverride{
				id: memoryOverride(t, strategy),
			}), root)
			if err == nil {
				t.Fatalf("a language with no emitted memory module APPLIED a %s override", strategy)
			}
			if p != nil {
				t.Error("a refused memory override produced a patch")
			}
			var re *RewriteError
			if !errors.As(err, &re) || re.Dim != string(variantspec.DimMemory) || re.Cause != CauseNoMaterializer {
				t.Fatalf("refusal = %#v, want a memory/no-materializer RewriteError", err)
			}
		}
	})

	t.Run("a covered language refuses about the call site", func(t *testing.T) {
		// pyMultiTurnSrc RETURNS its call, so the record half has no name to record from. Python has a
		// materializer, so blaming the platform here would send the author to wait for something that has
		// already landed.
		root := spanTarget(t, "pipeline.py", pyMultiTurnSrc)
		id := onlyNode(t, root, "python")

		for _, strategy := range realStrategies(t) {
			p, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
				id: memoryOverride(t, strategy),
			}), root)
			if err == nil {
				t.Fatalf("a call site that cannot carry the RECORD half materialized a %s override; that "+
					"emits a memory which reads a store nothing fills", strategy)
			}
			if p != nil {
				t.Error("a refused memory override produced a patch")
			}
			var re *RewriteError
			if !errors.As(err, &re) || re.Dim != string(variantspec.DimMemory) {
				t.Fatalf("refusal = %#v, want a memory RewriteError", err)
			}
			if re.Cause != CauseCallSiteShape {
				t.Errorf("cause = %q, want %q: Python HAS a materializer, so the honest cause is this "+
					"call site's shape — which the author can change", re.Cause, CauseCallSiteShape)
			}
		}
	})
}

// TestMemoryRefusalTypedAndProducesNoDiff — task 7.3 🔴. The refusal must be DISTINGUISHABLE from
// "you asked for something that does not exist", because those are answered by different people.
func TestMemoryRefusalTypedAndProducesNoDiff(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Memory: memoryEntry(t, "summary-buffer")},
	}), root)
	if err == nil {
		t.Fatal("a memory override was applied")
	}
	if p != nil {
		t.Fatal("a refused memory override produced a patch")
	}

	// It IS ErrUnsafeRewrite …
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("err = %v, want ErrUnsafeRewrite", err)
	}
	// … and it is NOT any of the "that does not exist" sentinels. A caller branching on these must be
	// able to tell "we will not do this YET" from "you named something that isn't there": the first is
	// our backlog, the second is the author's typo, and one message cannot serve both.
	for _, wrong := range []struct {
		err  error
		name string
	}{
		{ErrNodeNotFound, "ErrNodeNotFound"},
		{variantspec.ErrUnknownNode, "ErrUnknownNode"},
		{variantspec.ErrUnresolvedRef, "ErrUnresolvedRef"},
		{variantspec.ErrInvalidSpec, "ErrInvalidSpec"},
	} {
		if errors.Is(err, wrong.err) {
			t.Errorf("the memory refusal also matches %s; a caller cannot then tell a deferred capability "+
				"from a bad reference", wrong.name)
		}
	}

	// And it is not ErrOverlappingEdits either — that would mean a rewriter produced edits.
	if errors.Is(err, ErrOverlappingEdits) {
		t.Error("the memory refusal reports overlapping edits; the rewriter must produce no edits at all")
	}
}

// TestMemoryNoneAppliesAsNoOp — the mirror of every refusal above, and the reason the boundary is drawn
// on "would anything have to change" rather than "was memory mentioned".
//
// An explicit `none` is the identity strategy: nothing to materialize, so nothing to refuse. If this
// went red, `none` would be unusable — a user could never state "this node deliberately carries nothing",
// and could never back out of an authored memory change.
func TestMemoryNoneAppliesAsNoOp(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	t.Run("go engine", func(t *testing.T) {
		p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
			ids["classify"]: {Memory: memoryEntry(t, registry.StrategyNone)},
		}), root)
		if err != nil {
			t.Fatalf("an explicit `none` was REFUSED: %v.\nThe identity strategy changes nothing, so there "+
				"is nothing to refuse; refusing it would make `none` unusable and would draw the boundary on "+
				"the wrong fact", err)
		}
		if p != nil && len(p.Diff) != 0 {
			t.Errorf("an explicit `none` produced a diff:\n%s\n`none` ≡ absent, so it must be observably "+
				"identical to no override at all", p.Diff)
		}
	})

	t.Run("span engine", func(t *testing.T) {
		pyRoot := spanTarget(t, "pipeline.py", pyMultiTurnSrc)
		id := onlyNode(t, pyRoot, "python")
		p, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
			id: {Memory: memoryEntry(t, registry.StrategyNone)},
		}), pyRoot)
		if err != nil {
			t.Fatalf("an explicit `none` was refused in the span engine: %v", err)
		}
		if p != nil && len(p.Diff) != 0 {
			t.Errorf("an explicit `none` produced a diff:\n%s", p.Diff)
		}
	})
}

// TestMemoryRefusalTotalityCanary — task 7.4 🔴.
//
// The canary. A node carrying a real memory strategy must come back refused on EVERY path: every
// registered language, every non-identity strategy, through the real engine. There is no code path that
// drops the override or emits a memory edit.
//
// 🔴 This is the test that must be able to go RED. A refusal that cannot fail is decoration, so the
// second half deliberately proves the harness can detect an applied override — by running the same
// machinery against `none`, which MUST succeed. If both halves passed no matter what, the first half
// would be asserting nothing.
func TestMemoryRefusalTotalityCanary(t *testing.T) {
	// One fixture per registered language, so "every path" means every path a user can actually take.
	// These are the SAME fixtures the model/context suites use — deliberately, because a fixture written
	// for this test could be shaped (accidentally) so that discovery finds nothing, and the canary would
	// then pass by never reaching a rewriter at all.
	fixtures := map[string]struct{ file, src string }{
		"go":         {}, // handled by newTarget below — it needs a go.mod
		"python":     {"pipeline.py", pyMultiTurnSrc},
		"typescript": {"pipeline.ts", tsModelSrc},
		"javascript": {"pipeline.js", jsVercelSrc},
		"kotlin":     {"Triage.kt", kotlinBoundSrc},
		"java":       {"Triage.java", javaBoundSrc},
		"rust":       {"lib.rs", rustBoundSrc},
	}

	covered := 0
	for _, lang := range RegisteredLanguages() {
		fx, ok := fixtures[lang]
		if !ok {
			t.Fatalf("registered language %q has no canary fixture. A language added without one is a "+
				"language whose memory refusal is untested, which is exactly the gap this canary exists to "+
				"close — add a fixture rather than skipping it", lang)
		}

		var root, nodeID string
		if lang == "go" {
			fx.file = "" // the Go fixture is multi-file; the half-check below reads the span file only
			root = newTarget(t)
			nodeID = nodeIDs(t, root)["classify"]
		} else {
			root = spanTarget(t, fx.file, fx.src)
			sites, err := discovery.IndexSpanCallSites(root, lang, nil)
			if err != nil || len(sites) == 0 {
				// A fixture this language's frontend does not recognize proves nothing about the refusal,
				// and silently passing on it is how a canary stops being one.
				t.Fatalf("the %s canary fixture discovers no call site (err=%v); the assertion below would "+
					"pass for the wrong reason", lang, err)
			}
			for id := range sites {
				nodeID = id
				break
			}
		}

		for _, strategy := range realStrategies(t) {
			p, err := Generate(resolvedIn(lang, map[string]variantspec.ResolvedOverride{
				nodeID: memoryOverride(t, strategy),
			}), root)

			// 🔴 The canary's claim WIDENED when P18 landed, and this is the widening. P17 asserted every
			// override comes back refused, because nothing could materialize. Now some can — so the
			// invariant that actually matters is the one that was always underneath: an override is either
			// materialized COMPLETELY or refused with a typed cause. There is no third outcome, and in
			// particular there is no half.
			if err == nil {
				if p == nil {
					t.Errorf("[%s/%s] neither a patch nor a refusal — the override was DROPPED", lang, strategy)
					continue
				}
				after := string(p.Files[fx.file])
				hasRecall := strings.Contains(after, "agentmem.recall(")
				hasRecord := strings.Contains(after, "agentmem.record(")
				if hasRecall != hasRecord {
					t.Errorf("[%s/%s] a HALF-materialized memory shipped (recall=%v record=%v). A memory "+
						"that reads a store nothing fills — or fills one nothing reads — behaves as `none` "+
						"while its config_hash claims %s:\n%s", lang, strategy, hasRecall, hasRecord, strategy, after)
				}
				if !hasRecall {
					t.Errorf("[%s/%s] the patch carries neither half; the override reached the source as "+
						"nothing at all", lang, strategy)
				}
				covered++
				continue
			}

			var re *RewriteError
			if !errors.As(err, &re) || re.Dim != string(variantspec.DimMemory) {
				t.Errorf("[%s/%s] refused for the wrong reason: %v", lang, strategy, err)
				continue
			}
			if p != nil {
				t.Errorf("[%s/%s] a refused override produced a patch", lang, strategy)
			}
			covered++
		}
	}
	if covered == 0 {
		t.Fatal("the canary asserted nothing")
	}

	// 🔴 The canary's own canary: the same machinery MUST let `none` through. If this fails, every
	// assertion above is satisfied by a Generate that refuses everything, and the suite proves nothing.
	root := newTarget(t)
	if _, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		nodeIDs(t, root)["classify"]: {Memory: memoryEntry(t, registry.StrategyNone)},
	}), root); err != nil {
		t.Fatalf("the harness refuses even the identity strategy (%v); the refusals asserted above are "+
			"therefore not evidence of anything", err)
	}
}

// TestMemoryCoverageReflectsMaterializers — P18 §5.1, replacing P17's uniformity assertion.
//
// 🔴 P17 asserted this table was UNIFORM across languages. That was a true claim about a true state —
// nothing was per-language, because the memory RUNTIME was missing everywhere. P18 shipped it plus a
// Python materializer, so the axis became asymmetric, and a test still demanding uniformity would be
// demanding a lie. What survives, and is asserted harder: the table is TOTAL, it matches the ENGINE cell
// for cell, and no cell claims a capability the engine refuses.
func TestMemoryCoverageReflectsMaterializers(t *testing.T) {
	cells := CoverageFor(string(variantspec.DimMemory))
	wantCells := len(RegisteredLanguages()) * registry.MemoryStrategySetSize
	if len(cells) != wantCells {
		t.Fatalf("memory coverage has %d cells, want %d (every language × every strategy); coverage on this "+
			"axis is a TOTAL function, and a missing cell renders as \"not applicable\"", len(cells), wantCells)
	}

	for _, c := range cells {
		identity := c.Form == registry.StrategyNone
		wantMaterializes := identity || MemoryStrategyMaterializesIn(c.Language, c.Form)
		if got := c.Status == CoverageMaterializes; got != wantMaterializes {
			t.Errorf("[%s/%s] coverage says materializes=%v, the engine's materializer table says %v; the "+
				"claim and the behaviour must not be able to disagree", c.Language, c.Form, got, wantMaterializes)
		}
		if c.Status != CoverageRefuses {
			continue
		}
		// Two refusal shapes, and the difference is who (if anyone) can close it. A CauseNoMaterializer
		// cell owes an artifact and must name where the axis DOES work; a CauseNotAtCallSite cell owes
		// nothing, because there is nothing to build — and naming an artifact there would promise work
		// that would not help (P13 FR45).
		switch c.Cause {
		case CauseNoMaterializer:
			if c.MissingArtifact == "" {
				t.Errorf("[%s/%s] a no-materializer refusal names no missing artifact", c.Language, c.Form)
			}
			for _, lang := range MemoryMaterializerLanguages() {
				if !strings.Contains(c.Note, lang) {
					t.Errorf("[%s/%s] the refusal note does not name %q among the covered languages: %q",
						c.Language, c.Form, lang, c.Note)
				}
			}
		case CauseNotAtCallSite:
			if c.MissingArtifact != "" {
				t.Errorf("[%s/%s] a permanent refusal names a missing artifact (%q); the asymmetry between "+
					"the cause classes is the point", c.Language, c.Form, c.MissingArtifact)
			}
		default:
			t.Errorf("[%s/%s] refuses with cause %q, which is neither of the two shapes this axis produces",
				c.Language, c.Form, c.Cause)
		}
	}

	// 🚫 A materializing cell must state its PRECONDITIONS. It is a claim about a (language, strategy)
	// pair, not a promise about every call site in it — and a reader who discovers the record half's
	// requirement at apply time has been told half the truth.
	for _, c := range cells {
		if c.Status != CoverageMaterializes || c.Form == registry.StrategyNone {
			continue
		}
		for _, must := range []string{"BOTH halves", "session"} {
			if !strings.Contains(c.Note, must) {
				t.Errorf("[%s/%s] the materializing note omits %q; a precondition a reader meets at apply "+
					"time instead of at read time is a precondition we hid: %q", c.Language, c.Form, must, c.Note)
			}
		}
	}
}

// TestMemoryRefusalSuite — task 11.1, the QA acceptance gate's refusal suite. It runs the parts together
// rather than restating them, and adds the one assertion none of them makes alone: the refusal text does
// not tell the user to wait for a per-language artifact.
func TestMemoryRefusalSuite(t *testing.T) {
	t.Run("AST engine", TestMemoryOverrideRefusedInASTEngine)
	t.Run("span engine", TestMemoryOverrideRefusedInSpanEngine)
	t.Run("typed and no diff", TestMemoryRefusalTypedAndProducesNoDiff)
	t.Run("coverage matches the engine", TestMemoryCoverageReflectsMaterializers)
	t.Run("totality canary", TestMemoryRefusalTotalityCanary)
	t.Run("none is a no-op", TestMemoryNoneAppliesAsNoOp)
	t.Run("an uncovered language's refusal names what IS covered", func(t *testing.T) {
		// 🔴 P17 asserted here that the refusal says the gap is language-INDEPENDENT. P18 made that false
		// for Python, so the assertion moved to what is still true and still load-bearing: a refusal must
		// tell the reader which languages DO work, and must not leave them thinking their configuration
		// was thrown away. That is the difference between a refusal and a dead end.
		root := spanTarget(t, "pipeline.ts", tsMultiTurnSrc)
		id := onlyNode(t, root, "typescript")
		_, err := Generate(resolvedIn("typescript", map[string]variantspec.ResolvedOverride{
			id: memoryOverride(t, "vector-recall"),
		}), root)
		if err == nil {
			t.Fatal("not refused")
		}
		msg := strings.ToLower(err.Error())
		for _, covered := range MemoryMaterializerLanguages() {
			if !strings.Contains(msg, covered) {
				t.Errorf("the refusal does not name %q among the covered languages, so a reader cannot tell "+
					"whether the axis works anywhere: %v", covered, err)
			}
		}
		if !strings.Contains(msg, "refused rather than") {
			t.Errorf("the refusal does not say the override was refused rather than dropped: %v", err)
		}
	})
}
