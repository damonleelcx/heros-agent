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

// TestMemoryOverrideRefusedInASTEngine — task 7.1 🔴.
func TestMemoryOverrideRefusedInASTEngine(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	for _, strategy := range realStrategies(t) {
		t.Run(strategy, func(t *testing.T) {
			p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
				ids["classify"]: {Memory: memoryEntry(t, strategy)},
			}), root)
			if err == nil {
				t.Fatalf("the Go engine APPLIED a %s memory override. This is the worst outcome the system "+
					"can produce: the diff would be filed under a config_hash claiming a memory strategy the "+
					"source never had, and the eval would score a configuration that never ran", strategy)
			}
			if !errors.Is(err, ErrUnsafeRewrite) {
				t.Fatalf("err = %v, want ErrUnsafeRewrite", err)
			}
			if p != nil {
				t.Error("a refused memory override produced a patch; a refusal must produce NO diff")
			}

			var re *RewriteError
			if !errors.As(err, &re) {
				t.Fatalf("the refusal is not a *RewriteError: %v", err)
			}
			if re.NodeID != ids["classify"] {
				t.Errorf("the refusal names node %q, want %q", re.NodeID, ids["classify"])
			}
			if re.Dim != string(variantspec.DimMemory) {
				t.Errorf("the refusal names dimension %q, want memory", re.Dim)
			}
			// 🔴 CauseNoMaterializer, not CauseCallSiteShape. The user's call site is fine; the PLATFORM has
			// not built the artifact. Getting this backwards tells them to change code that is not the
			// problem.
			if re.Cause != CauseNoMaterializer {
				t.Errorf("the refusal's cause is %q, want %q: the call site is not at fault, the missing "+
					"memory runtime is", re.Cause, CauseNoMaterializer)
			}
			if !strings.Contains(err.Error(), strategy) {
				t.Errorf("the refusal does not name the strategy that was refused: %v", err)
			}
		})
	}
}

// TestMemoryOverrideRefusedInSpanEngine — task 7.2 🔴. The other engine, so no target language applies a
// memory change through the path the first test does not cover.
func TestMemoryOverrideRefusedInSpanEngine(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyMultiTurnSrc)
	id := onlyNode(t, root, "python")

	for _, strategy := range realStrategies(t) {
		t.Run(strategy, func(t *testing.T) {
			p, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
				id: {Memory: memoryEntry(t, strategy)},
			}), root)
			if err == nil {
				t.Fatalf("the span engine APPLIED a %s memory override; the Go engine's refusal would then "+
					"be a formality that every tree-sitter language routes around", strategy)
			}
			if !errors.Is(err, ErrUnsafeRewrite) {
				t.Fatalf("err = %v, want ErrUnsafeRewrite", err)
			}
			if p != nil {
				t.Error("a refused memory override produced a patch")
			}
			var re *RewriteError
			if !errors.As(err, &re) || re.Dim != string(variantspec.DimMemory) || re.Cause != CauseNoMaterializer {
				t.Fatalf("refusal = %#v, want a memory/no-materializer RewriteError", err)
			}
		})
	}
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
				nodeID: {Memory: memoryEntry(t, strategy)},
			}), root)
			if err == nil {
				t.Errorf("[%s/%s] the override was NOT refused; some path drops it or emits an edit", lang, strategy)
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

// TestMemoryCoverageIsUniformAndRefuses — the coverage table is what the AUTHORING surface reads to state
// the boundary before a user chooses (P17 D7). If it disagreed with the engine, the surface would promise
// something the transform then refuses — or vice versa.
func TestMemoryCoverageIsUniformAndRefuses(t *testing.T) {
	cells := CoverageFor(string(variantspec.DimMemory))
	if len(cells) == 0 {
		t.Fatal("the memory axis has no coverage cells; the authoring surface would have nothing to read " +
			"and would have to hard-code a second sentence")
	}

	wantCells := len(RegisteredLanguages()) * registry.MemoryStrategySetSize
	if len(cells) != wantCells {
		t.Errorf("memory coverage has %d cells, want %d (every language × every strategy); coverage on this "+
			"axis is a TOTAL function, and a missing cell renders as \"not applicable\"", len(cells), wantCells)
	}

	byForm := map[string]map[string]CoverageCell{}
	for _, c := range cells {
		if byForm[c.Form] == nil {
			byForm[c.Form] = map[string]CoverageCell{}
		}
		byForm[c.Form][c.Language] = c
	}

	for _, st := range registry.BuiltinMemoryStrategies() {
		langs, ok := byForm[st.Name()]
		if !ok {
			t.Errorf("strategy %q has no coverage cells", st.Name())
			continue
		}
		for lang, c := range langs {
			if st.Name() == registry.StrategyNone {
				if c.Status != CoverageMaterializes {
					t.Errorf("[%s/none] status is %q, want materializes: the identity strategy is equivalent "+
						"to the un-rewritten call site, and a user selecting it must not be told it was refused",
						lang, c.Status)
				}
				continue
			}
			if c.Status != CoverageRefuses {
				t.Errorf("[%s/%s] coverage claims %q while the engine refuses; the authoring surface would "+
					"promise a change the transform then declines", lang, st.Name(), c.Status)
			}
			if c.Cause != CauseNoMaterializer {
				t.Errorf("[%s/%s] cause is %q, want %q — the platform owes this artifact, not the customer",
					lang, st.Name(), c.Cause, CauseNoMaterializer)
			}
			if c.MissingArtifact == "" {
				t.Errorf("[%s/%s] a no-materializer refusal names no missing artifact, so nobody can tell "+
					"what would close it", lang, st.Name())
			}
		}
	}

	// 🔴 UNIFORMITY. Unlike skills and context, memory must not vary by language: what is missing is a
	// memory runtime, not a per-language rewriter, and a per-language cell would tell a Rust user to wait
	// for a Rust artifact that is not the blocker.
	for form, langs := range byForm {
		var first *CoverageCell
		for lang, c := range langs {
			if first == nil {
				cc := c
				first = &cc
				continue
			}
			if c.Status != first.Status || c.Cause != first.Cause || c.MissingArtifact != first.MissingArtifact {
				t.Errorf("strategy %q answers differently for %s than for %s; the memory axis is uniform "+
					"across languages because the missing artifact is a runtime, not a rewriter",
					form, lang, first.Language)
			}
			// And no cell may name a language as the blocker.
			if strings.Contains(strings.ToLower(c.Note), strings.ToLower(lang)) && lang != "" {
				t.Errorf("[%s/%s] the coverage note names the language as the reason: %q. That implies some "+
					"other language's rewriter has landed, which is false", lang, form, c.Note)
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
	t.Run("totality canary", TestMemoryRefusalTotalityCanary)
	t.Run("none is a no-op", TestMemoryNoneAppliesAsNoOp)

	t.Run("the refusal does not blame the language", func(t *testing.T) {
		root := spanTarget(t, "pipeline.py", pyMultiTurnSrc)
		id := onlyNode(t, root, "python")
		_, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
			id: {Memory: memoryEntry(t, "vector-recall")},
		}), root)
		if err == nil {
			t.Fatal("not refused")
		}
		msg := strings.ToLower(err.Error())
		// It must say the gap is everywhere, not here. "the python materializer is still being built"
		// would send a user to wait for the wrong thing.
		if !strings.Contains(msg, "any language") && !strings.Contains(msg, "every language") {
			t.Errorf("the refusal does not say the gap is language-independent, so a reader will assume "+
				"another language works: %v", err)
		}
		// And it must not leave the user thinking their configuration was thrown away.
		if !strings.Contains(msg, "hashes") && !strings.Contains(msg, "resolves") {
			t.Errorf("the refusal does not tell the user what their change DID accomplish (it resolves and "+
				"hashes), which is the difference between a refusal and a dead end: %v", err)
		}
	})
}
