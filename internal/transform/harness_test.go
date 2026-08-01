package transform

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P18 §5 — the interim refusal (decisions.md D-4, narrowed per cell by D-11). The hard part of the phase,
// and the one whose failure mode is not "a feature is missing" but "a measurement is false".
//
// Every test here runs the REAL Generate against a REAL fixture repo. Nothing hand-feeds a rewriter: the
// bug this file exists to catch is a harness override that reaches the end of the pipeline without being
// refused, and a test that called refuseHarness directly could not see that.

// defaultHarnessParams are schema-valid params per strategy. Empty params are not a simpler fixture — the
// registry rejects them, so an empty-params fixture would test the rejection rather than the refusal.
var defaultHarnessParams = map[string]string{
	"single-shot":  `{}`,
	"react-loop":   `{"max_turns":6,"stop_condition":"no-tool-call"}`,
	"plan-execute": `{"max_turns":4,"stop_condition":"plan-complete"}`,
	"reflexion":    `{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"find the error"}`,
	"critic-loop":  `{"max_turns":3,"critic_model_ref":"` + "deadbeef" + `"}`,
}

// harnessEntry builds a resolved harness entry the way the loader would — through the real builtin
// vocabulary, so a test can never assert against a strategy the registry would reject.
func harnessEntry(t *testing.T, strategy string) *registry.HarnessEntry {
	t.Helper()
	st := registry.HarnessStrategyNamed(strategy)
	if st == nil {
		t.Fatalf("harnessEntry: %q is not a builtin strategy", strategy)
	}
	params, ok := defaultHarnessParams[strategy]
	if !ok {
		t.Fatalf("no default params for strategy %q", strategy)
	}
	return &registry.HarnessEntry{
		VersionID: strings.Repeat("h", 64), Name: "h",
		Spec: registry.HarnessSpec{Strategy: strategy, Params: json.RawMessage(params)}, Strategy: st,
	}
}

func harnessOverride(t *testing.T, strategy string) variantspec.ResolvedOverride {
	t.Helper()
	return variantspec.ResolvedOverride{Harness: harnessEntry(t, strategy)}
}

// multiTurnStrategies is every builtin except the identity — the ones that MUST NOT silently succeed.
func multiTurnStrategies(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, st := range registry.BuiltinHarnessStrategies() {
		if st.Name() != registry.StrategySingleShot {
			out = append(out, st.Name())
		}
	}
	if len(out) == 0 {
		t.Fatal("the builtin set contains nothing but the identity; this suite would assert nothing")
	}
	return out
}

// TestHarnessRefusedAtTransform — task 5.1 🔴. Both engines, every multi-turn strategy, with a REAL
// Generate. The refusal must be typed, must name the node and the dimension, and must carry a cause class
// a consumer can branch on without reading prose.
func TestHarnessRefusedAtTransform(t *testing.T) {
	t.Run("go engine", func(t *testing.T) {
		root := newTarget(t)
		ids := nodeIDs(t, root)
		for _, strategy := range multiTurnStrategies(t) {
			t.Run(strategy, func(t *testing.T) {
				p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
					ids["summarize"]: harnessOverride(t, strategy),
				}), root)
				assertHarnessRefusal(t, p, err, ids["summarize"], strategy)
			})
		}
	})

	t.Run("span engine", func(t *testing.T) {
		const src = `import openai

client = openai.OpenAI()


def chat(question):
    resp = client.chat.completions.create(
        model="gpt-4o",
        messages=[{"role": "user", "content": question}],
    )
    return resp
`
		root := spanTarget(t, "pipeline.py", src)
		id := onlyNode(t, root, "python")
		for _, strategy := range multiTurnStrategies(t) {
			t.Run(strategy, func(t *testing.T) {
				if HarnessStrategyMaterializesIn("python", strategy) {
					t.Skipf("python materializes %s; its refusal is asserted per-cell by the coverage suite", strategy)
				}
				p, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
					id: harnessOverride(t, strategy),
				}), root)
				assertHarnessRefusal(t, p, err, id, strategy)
			})
		}
	})
}

func assertHarnessRefusal(t *testing.T, p *Patch, err error, nodeID, strategy string) {
	t.Helper()
	if err == nil {
		t.Fatalf("the engine APPLIED a %s harness override; a scaffold change that produced a diff without "+
			"a loop would be scored as if the loop ran", strategy)
	}
	if p != nil {
		t.Error("a refused harness override produced a patch; a refusal must produce NO diff")
	}
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("err = %v, want ErrUnsafeRewrite", err)
	}
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("err is not a *RewriteError: %v", err)
	}
	if re.NodeID != nodeID {
		t.Errorf("the refusal names node %q, want %q", re.NodeID, nodeID)
	}
	if re.Dim != string(variantspec.DimHarness) {
		t.Errorf("the refusal names dimension %q, want harness", re.Dim)
	}
	if !re.Cause.Valid() {
		t.Errorf("the refusal carries cause %q, which is not one of the three classes; a consumer's switch "+
			"would fall through", re.Cause)
	}
	if !strings.Contains(re.Detail, strategy) {
		t.Errorf("the refusal does not name the strategy that was asked for (%q): %s", strategy, re.Detail)
	}
}

// TestHarnessRefusedNotSilentlyDropped — task 5.2 🔴. The test that must fail if the override is silently
// dropped. It asserts the two halves that make a drop detectable: the resolved config STILL CARRIES the
// override, and the transform refuses rather than emitting a diff.
func TestHarnessRefusedNotSilentlyDropped(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	r := resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["summarize"]: harnessOverride(t, "react-loop"),
	})

	p, err := Generate(r, root)
	if err == nil {
		t.Fatal("Generate SUCCEEDED with a react-loop harness override. That is the failure this test " +
			"exists for: the diff would rewrite nothing about the scaffold, the build would pass, the eval " +
			"would run, and the score would be attributed to a config_hash claiming a loop the source " +
			"never had — a false measurement, not a missing feature")
	}
	if p != nil {
		t.Fatal("a refused harness override produced a patch")
	}

	// 🔴 The override is still THERE. A rewriter that "handled" the refusal by clearing the override would
	// make the refusal disappear on a second pass, and the axis would silently become a no-op.
	got := r.Overrides[ids["summarize"]]
	if got.Harness == nil {
		t.Fatal("the resolved override no longer carries the harness entry after Generate; refuse means " +
			"refuse, never drop — an override the transform erased cannot be re-materialized when a " +
			"rewriter lands, and nothing downstream can tell it was ever asked for")
	}
	if got.Harness.Spec.Strategy != "react-loop" {
		t.Fatalf("the carried strategy changed to %q", got.Harness.Spec.Strategy)
	}
	// And the dimension is still reported, so a later pass dispatches it again.
	found := false
	for _, d := range got.Dimensions() {
		if d == variantspec.DimHarness {
			found = true
		}
	}
	if !found {
		t.Fatal("Dimensions() no longer reports harness for the refused override")
	}
}

// TestSingleShotHarnessIsANoOpAtTransform — the other half of refuse-never-drop, and the one a
// blanket refusal would get wrong. The identity emits nothing and refuses nothing: one turn IS the
// un-rewritten call site, so a user selecting it must never be told their no-op was refused.
func TestSingleShotHarnessIsANoOpAtTransform(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["summarize"]: harnessOverride(t, registry.StrategySingleShot),
	}), root)
	if err != nil {
		t.Fatalf("an explicit single-shot harness was REFUSED: %v\nOne turn is exactly what the call site "+
			"already does; refusing it would tell a user their deliberate no-op failed", err)
	}
	if p == nil {
		t.Fatal("Generate returned no patch and no error")
	}
	if len(p.Files) != 0 {
		names := make([]string, 0, len(p.Files))
		for f := range p.Files {
			names = append(names, f)
		}
		t.Fatalf("the identity strategy emitted file changes: %v", names)
	}
}

// TestGroupHarnessRefusedNamingEdgeSet — task 5.3. A group harness spans several calls, so the refusal
// has to name the EDGE SET: a reader with two harnesses cannot otherwise tell which one was refused.
func TestGroupHarnessRefusedNamingEdgeSet(t *testing.T) {
	root := newTarget(t)
	r := resolvedWith(nil)
	r.HarnessGroups = []variantspec.ResolvedGroupOverride{{
		Entry: harnessEntry(t, "plan-execute"),
		Edges: []variantspec.ResolvedEdge{
			{FromNodeID: "n_a", ToNodeID: "n_b", Kind: "data"},
			{FromNodeID: "n_b", ToNodeID: "n_c", Kind: "data"},
		},
	}}

	p, err := Generate(r, root)
	if err == nil {
		t.Fatal("a group harness was APPLIED; wrapping several calls in one loop is control-flow surgery " +
			"across statements and files, and a diff that did not do it would be scored as if it had")
	}
	if p != nil {
		t.Error("a refused group harness produced a patch")
	}
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("err is not a *RewriteError: %v", err)
	}
	if re.Dim != string(variantspec.DimHarness) {
		t.Errorf("the refusal names dimension %q, want harness", re.Dim)
	}
	for _, want := range []string{"n_a->n_b", "n_b->n_c", "plan-execute"} {
		if !strings.Contains(re.Detail, want) {
			t.Errorf("the refusal does not name %q — the edge set IS the group's identity, and a reader "+
				"with two group harnesses cannot tell which was refused without it:\n%s", want, re.Detail)
		}
	}
	// 🚫 And it must not read as a refusal to REWIRE. The edge set is the one the spec declares; a harness
	// never reorders (decisions.md D-5, P15 owns wiring).
	if !strings.Contains(re.Detail, "P15") {
		t.Errorf("the refusal does not distinguish itself from a wiring refusal; a reader would go looking "+
			"for a reorder that was never asked for:\n%s", re.Detail)
	}
}

// TestHarnessRefusalTotalityCanary — task 11.8 🔴, asserted from §5 so it can never regress silently.
//
// Every (language, strategy) cell the engine does NOT materialize must return a typed refusal carrying a
// cause class. The canary is the whole guarantee: a coverage table that claimed a cell works while the
// engine refuses it — or the reverse — is the drift the derived read exists to prevent.
func TestHarnessRefusalTotalityCanary(t *testing.T) {
	cells := CoverageFor(string(variantspec.DimHarness))
	if len(cells) == 0 {
		t.Fatal("the harness axis reports no coverage cells at all; the totality read would assert nothing")
	}
	langs := map[string]bool{}
	strategies := map[string]bool{}
	for _, c := range cells {
		langs[c.Language] = true
		strategies[c.Form] = true

		// The claim and the engine must agree, cell by cell.
		wantMaterializes := HarnessStrategyMaterializesIn(c.Language, c.Form)
		if got := c.Status == CoverageMaterializes; got != wantMaterializes {
			t.Errorf("coverage says %s/%s materializes=%v but the engine's own dispatch says %v; a claim "+
				"that does not match the engine is wrong in one direction or the other, and both are the "+
				"same defect", c.Language, c.Form, got, wantMaterializes)
		}
		if c.Status == CoverageRefuses {
			if !c.Cause.Valid() {
				t.Errorf("%s/%s refuses with cause %q, which is not one of the three classes",
					c.Language, c.Form, c.Cause)
			}
			// 🔴 Only CauseNoMaterializer may name work we owe. A permanent language or call-site fact
			// wearing that label tells a user to wait for something that will not help them.
			if c.Cause != CauseNoMaterializer && c.MissingArtifact != "" {
				t.Errorf("%s/%s refuses with cause %q but names a missing artifact %q; only the "+
					"no-materializer class may name work the platform owes",
					c.Language, c.Form, c.Cause, c.MissingArtifact)
			}
			if c.Cause == CauseNoMaterializer && c.MissingArtifact == "" {
				t.Errorf("%s/%s claims the platform owes work but names no artifact", c.Language, c.Form)
			}
		}
		if c.Note == "" {
			t.Errorf("%s/%s carries no note; the cause is the machine half and the note is the human half, "+
				"and a cell with only the first is unreadable", c.Language, c.Form)
		}
	}

	// Totality: every registered language and every builtin strategy has an answer.
	for _, lang := range RegisteredLanguages() {
		if !langs[lang] {
			t.Errorf("the harness axis has no cell for %s; absence renders as 'not applicable' on every "+
				"surface, which is a claim about the customer's code", lang)
		}
	}
	for _, st := range registry.BuiltinHarnessStrategies() {
		if !strategies[st.Name()] {
			t.Errorf("the harness axis has no cell for strategy %s", st.Name())
		}
	}

	// 🔴 The identity is never refused, anywhere.
	for _, c := range cells {
		if c.Form == registry.StrategySingleShot && c.Status != CoverageMaterializes {
			t.Errorf("%s reports the identity strategy as refused; one turn is the un-rewritten call site, "+
				"so there is nothing to refuse", c.Language)
		}
	}
}

// TestHostServiceStrategiesRefusedByName — task 11.5, asserted from §5 because it is the one refusal that
// is TRUE IN EVERY LANGUAGE and must therefore never be reported as a language gap.
func TestHostServiceStrategiesRefusedByName(t *testing.T) {
	for _, strategy := range []string{"react-loop", "plan-execute", "critic-loop"} {
		if harnessHostService(strategy) == "" {
			t.Fatalf("%s declares no host service; it would then be reported as materializable wherever a "+
				"rewriter exists, and a loop that never runs its tool is not that loop", strategy)
		}
		for _, lang := range RegisteredLanguages() {
			if HarnessStrategyMaterializesIn(lang, strategy) {
				t.Errorf("%s reports %s as materializable; a call site offers no injection point for a tool "+
					"executor, a planner or a critic in ANY language", lang, strategy)
			}
			for _, c := range CoverageFor(string(variantspec.DimHarness)) {
				if c.Language != lang || c.Form != strategy {
					continue
				}
				if c.Cause != CauseNotAtCallSite {
					t.Errorf("%s/%s refuses with cause %q, want %q — this is a permanent fact about call "+
						"sites, not a platform backlog item", lang, strategy, c.Cause, CauseNotAtCallSite)
				}
				if c.MissingArtifact != "" {
					t.Errorf("%s/%s names a missing artifact %q; there is nothing to build, so naming one "+
						"promises work that would not help", lang, strategy, c.MissingArtifact)
				}
			}
		}
	}

	// And the two that need nothing declare nothing, or the check above would be vacuous.
	for _, strategy := range []string{registry.StrategySingleShot, "reflexion"} {
		if svc := harnessHostService(strategy); svc != "" {
			t.Errorf("%s declares host service %q; it needs none — reflexion's critique is another turn of "+
				"the SAME call, which is exactly why it is the multi-turn strategy that can be materialized",
				strategy, svc)
		}
	}
}

// TestRefusalSuite — task 8.2 🔴. The acceptance gate's refusal half, stated as one suite because the
// three facts only mean something together: a typed refusal on both engines, an override that is still
// there afterwards, and a group harness that names its edge set.
//
// 🚫 It deliberately does NOT re-run the unit tests above. What it adds is the CONJUNCTION — a build
// where each of the three passes individually but the transform quietly drops the override on the second
// pass would satisfy every one of them and fail this.
func TestRefusalSuite(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	node := ids["summarize"]

	r := resolvedWith(map[string]variantspec.ResolvedOverride{node: harnessOverride(t, "react-loop")})

	// Twice through the same Resolved. A rewriter that "handled" the refusal by clearing the override
	// would refuse the first time and SUCCEED the second — the axis silently becoming a no-op, which no
	// single-pass test can see.
	for pass := 1; pass <= 2; pass++ {
		p, err := Generate(r, root)
		if err == nil {
			t.Fatalf("pass %d: Generate succeeded with a react-loop override", pass)
		}
		if p != nil {
			t.Fatalf("pass %d: a refusal produced a patch", pass)
		}
		var re *RewriteError
		if !errors.As(err, &re) || re.Dim != string(variantspec.DimHarness) || !re.Cause.Valid() {
			t.Fatalf("pass %d: refusal is not typed and classified: %v", pass, err)
		}
		if r.Overrides[node].Harness == nil {
			t.Fatalf("pass %d: the override was dropped; refuse means refuse, never drop", pass)
		}
	}

	// The group form, on the same tree, naming its edge set.
	g := resolvedWith(nil)
	g.HarnessGroups = []variantspec.ResolvedGroupOverride{{
		Entry: harnessEntry(t, "critic-loop"),
		Edges: []variantspec.ResolvedEdge{{FromNodeID: "n_x", ToNodeID: "n_y", Kind: "data"}},
	}}
	_, err := Generate(g, root)
	if err == nil {
		t.Fatal("a group harness was applied")
	}
	var re *RewriteError
	if !errors.As(err, &re) || !strings.Contains(re.Detail, "n_x->n_y") {
		t.Fatalf("the group refusal does not name its edge set: %v", err)
	}

	// And the identity is still not refused, on the same tree, in the same suite — because a refusal
	// suite that passed by refusing EVERYTHING would be the opposite defect.
	ok, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		node: harnessOverride(t, registry.StrategySingleShot),
	}), root)
	if err != nil {
		t.Fatalf("the identity strategy was refused: %v", err)
	}
	if len(ok.Files) != 0 {
		t.Error("the identity strategy emitted a change")
	}
}

// TestHarnessMaterializationSuite — task 13.2 🔴. The wave-18c gate's materialization half.
//
// 🔴 The conjunction it adds: over the WHOLE vocabulary and BOTH engines, every cell either emits with
// its artifact beside it or refuses with a valid cause — and never both, and never neither. A build
// where one strategy silently emitted nothing and returned no error would pass every unit above.
func TestHarnessMaterializationSuite(t *testing.T) {
	pySrc := pyHarnessSrc
	pyRoot := spanTarget(t, "pipeline.py", pySrc)
	pyNode := onlyNode(t, pyRoot, "python")
	goRoot := newTarget(t)
	goNode := nodeIDs(t, goRoot)["summarize"]

	for _, st := range registry.BuiltinHarnessStrategies() {
		for _, c := range []struct {
			lang, root, node string
			resolved         func(map[string]variantspec.ResolvedOverride) *variantspec.Resolved
		}{
			{"python", pyRoot, pyNode, func(o map[string]variantspec.ResolvedOverride) *variantspec.Resolved {
				return resolvedIn("python", o)
			}},
			{"go", goRoot, goNode, resolvedWith},
		} {
			t.Run(st.Name()+"/"+c.lang, func(t *testing.T) {
				p, err := Generate(c.resolved(map[string]variantspec.ResolvedOverride{
					c.node: harnessOverride(t, st.Name()),
				}), c.root)

				covered := HarnessStrategyMaterializesIn(c.lang, st.Name())
				switch {
				case !covered:
					if err == nil {
						t.Fatalf("an uncovered cell produced a patch instead of a refusal")
					}
					var re *RewriteError
					if !errors.As(err, &re) || !re.Cause.Valid() {
						t.Fatalf("the refusal is not typed and classified: %v", err)
					}
					if p != nil {
						t.Error("a refusal produced a patch")
					}
				case st.Name() == registry.StrategySingleShot:
					// The identity: covered, and emits nothing. Both halves of that matter.
					if err != nil {
						t.Fatalf("the identity was refused: %v", err)
					}
					if len(p.Files) != 0 {
						t.Errorf("the identity emitted %d file(s)", len(p.Files))
					}
				default:
					if err != nil {
						t.Fatalf("a covered cell refused: %v", err)
					}
					// 🔴 The artifact ships in the SAME patch as the call-site edit.
					for _, want := range []string{pyHarnessModulePath, harnessDocPath} {
						if _, ok := p.Files[want]; !ok {
							t.Errorf("the patch is missing %s; a module without the call, or a call without "+
								"the module, is a broken repository either way", want)
						}
					}
					if got := string(p.Files["pipeline.py"]); !strings.Contains(got, harnessImportName+".run(") {
						t.Errorf("the call site was not wrapped:\n%s", got)
					}
				}
			})
		}
	}
}

// TestHarnessCoverageAgreesWithEngine — task 13.3 🔴. The coverage read and the transform must agree for
// every cell, and every uncovered cell must still return a typed `unsafeRewrite`.
//
// It is the canary's twin, aimed at the other direction: TestHarnessRefusalTotalityCanary asserts the
// table is total and classified; this asserts the ENGINE actually behaves the way the table says, by
// running it.
func TestHarnessCoverageAgreesWithEngine(t *testing.T) {
	pyRoot := spanTarget(t, "pipeline.py", pyHarnessSrc)
	pyNode := onlyNode(t, pyRoot, "python")

	checked := 0
	for _, c := range CoverageFor(string(variantspec.DimHarness)) {
		if c.Language != "python" {
			continue // the engine can only be RUN for the languages this fixture is written in
		}
		checked++
		_, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
			pyNode: harnessOverride(t, c.Form),
		}), pyRoot)
		if c.Status == CoverageMaterializes && err != nil {
			t.Errorf("coverage says python/%s materializes and the engine refused: %v", c.Form, err)
		}
		if c.Status == CoverageRefuses && err == nil {
			t.Errorf("coverage says python/%s refuses and the engine emitted a patch", c.Form)
		}
	}
	if checked != registry.HarnessStrategySetSize {
		t.Fatalf("checked %d cell(s), want %d — the read has drifted and this test is no longer reading it",
			checked, registry.HarnessStrategySetSize)
	}
}
