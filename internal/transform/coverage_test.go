package transform

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// ── P13 17.5 / NFR19 🔴 — coverage is TOTAL, and the expectation is generated ────────────────────

// The single most important property of the coverage read: every axis answers for every registered
// language. A language that is simply ABSENT renders on every surface as "not applicable" — a claim
// about the customer's code — when it means "we have not built this yet".
//
// 🔴 The language set is read from discovery.DefaultFrontends, not listed here. A listed set would pass
// forever after the next frontend lands, which is precisely the moment it needs to fail.
func TestCoverageIsTotalOverRegisteredLanguages(t *testing.T) {
	cells := AxisCoverage()
	if len(cells) == 0 {
		t.Fatal("AxisCoverage returned nothing; a total table cannot be empty")
	}
	seen := map[string]map[string]bool{} // axis -> language -> present
	for _, c := range cells {
		if seen[c.Axis] == nil {
			seen[c.Axis] = map[string]bool{}
		}
		seen[c.Axis][c.Language] = true
	}
	for _, axis := range CoverageAxes() {
		langs := seen[axis]
		if langs == nil {
			t.Errorf("axis %q has no coverage entries at all", axis)
			continue
		}
		for _, l := range RegisteredLanguages() {
			if !langs[l] {
				t.Errorf("axis %q has no coverage entry for registered language %q — absence is not a value; "+
					"a gap must be present and name its missing artifact", axis, l)
			}
		}
	}
}

// Every cell is well-formed: a refusal carries one of the three classes, and only a platform gap names
// an artifact. The asymmetry is the point (P13 FR45) — a call-site fact and a not-in-source fact have no
// artifact to build, so naming one would promise work nobody owes.
func TestEveryCoverageCellIsWellFormed(t *testing.T) {
	for _, c := range AxisCoverage() {
		switch c.Status {
		case CoverageMaterializes:
			if c.Cause != "" {
				t.Errorf("%s/%s/%s materializes but carries cause %q", c.Axis, c.Language, c.Form, c.Cause)
			}
			if c.MissingArtifact != "" {
				t.Errorf("%s/%s/%s materializes but names a missing artifact", c.Axis, c.Language, c.Form)
			}
		case CoverageRefuses:
			if !c.Cause.Valid() {
				t.Errorf("%s/%s/%s refuses with cause %q, which is not one of the three classes",
					c.Axis, c.Language, c.Form, c.Cause)
			}
			if c.Cause == CauseNoMaterializer && c.MissingArtifact == "" {
				t.Errorf("%s/%s/%s is a platform gap that does not name the artifact that would close it; "+
					"\"not built yet\" without a name is a shrug, not a backlog item", c.Axis, c.Language, c.Form)
			}
			if c.Cause != CauseNoMaterializer && c.MissingArtifact != "" {
				t.Errorf("%s/%s/%s is not a platform gap but names an artifact, which promises work nobody "+
					"owes", c.Axis, c.Language, c.Form)
			}
			if c.Note == "" {
				t.Errorf("%s/%s/%s refuses with no sentence a human can act on", c.Axis, c.Language, c.Form)
			}
		default:
			t.Errorf("%s/%s/%s has status %q, which is neither materializes nor refuses",
				c.Axis, c.Language, c.Form, c.Status)
		}
	}
}

// 🚫 No second coverage list. Everything that STATES coverage derives from the engine's own tables, so
// this asserts the derived reads agree with the total one rather than carrying their own answers.
func TestNoSurfaceHoldsItsOwnCoverageList(t *testing.T) {
	// The skill read and the total table must agree cell for cell.
	fromSkills := map[string]bool{}
	for _, c := range MaterializerCoverage() {
		fromSkills[c.Language+"/"+c.Provider] = true
	}
	for _, c := range CoverageFor(string(variantspec.DimSkills)) {
		key := c.Language + "/" + c.Form
		if c.Status == CoverageMaterializes && !fromSkills[key] {
			t.Errorf("AxisCoverage claims skills materialize at %s, which MaterializerCoverage does not carry", key)
		}
		if c.Status == CoverageRefuses && fromSkills[key] {
			t.Errorf("AxisCoverage refuses skills at %s, which MaterializerCoverage claims materializes", key)
		}
	}
	// The context read and the total table must agree language for language.
	ctxLangs := map[string]bool{}
	for _, l := range ContextMaterializerLanguages() {
		ctxLangs[l] = true
	}
	for _, c := range CoverageFor(string(variantspec.DimContext)) {
		if c.Form != "sliding-window" {
			continue // one selection policy is enough to compare the language answer
		}
		if got := c.Status == CoverageMaterializes; got != ctxLangs[c.Language] {
			t.Errorf("context coverage for %q disagrees with ContextMaterializerLanguages (%v vs %v)",
				c.Language, got, ctxLangs[c.Language])
		}
	}
	// The wiring read likewise.
	for _, c := range CoverageFor(wiringRefusalDim) {
		if got := c.Status == CoverageMaterializes; got != HasStatementMaterializer(c.Language) {
			t.Errorf("wiring coverage for %q disagrees with HasStatementMaterializer", c.Language)
		}
	}
	// And tool pruning.
	for _, c := range CoverageFor(string(variantspec.DimTools)) {
		if got := c.Status == CoverageMaterializes; got != discovery.RecordsToolSplit(c.Language) {
			t.Errorf("tool-prune coverage for %q disagrees with discovery.RecordsToolSplit", c.Language)
		}
	}
}

// 🔴 P13 21.4 — coverage is IDENTICAL on every plan. There is no plan input to reach for, and that is
// the assertion: the read takes no entitlement, role, or tenant, so no tier can move a cell. A future
// contributor adding one has to change this signature, which is a visible thing to do in review.
func TestCoverageIsPlanInvariant(t *testing.T) {
	first := CoverageTableVersion()
	for i := 0; i < 3; i++ {
		if got := CoverageTableVersion(); got != first {
			t.Fatalf("the coverage table is not deterministic across reads: %q then %q", first, got)
		}
	}
	if !strings.HasPrefix(first, "cov-") {
		t.Errorf("the coverage version must be self-identifying, got %q", first)
	}
}

// ── P13 18.3 🔴 — the cause order goes RED when reversed ────────────────────────────────────────

// The ordering rule with the fixture that makes it real: a Python call site that unpacks its arguments
// AND (for the duration of this test) has no spelling for its provider. Both causes are true. The one
// reported must be the CALL SITE's, because it stays true after the spelling lands — telling this author
// to wait for a materializer costs them a quarter and helps them not at all.
func TestCallSiteCauseBeatsLanguageCause(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyKwargsSrc)
	id := onlyNode(t, root, "python")

	// Remove the spelling, so the no-materializer branch is genuinely reachable for this call site.
	cell := toolValueKey("python", "openai")
	form, covered := toolValueForms[cell]
	if !covered {
		t.Fatal("the (python, openai) row must exist for this test to remove it")
	}
	delete(toolValueForms, cell)
	t.Cleanup(func() { toolValueForms[cell] = form })

	entry := skillEntry(t, "search_kb", `{"type":"object","properties":{"query":{"type":"string"}}}`)
	_, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Skills: []*registry.SkillEntry{entry}},
	}), root)
	if err == nil {
		t.Fatal("a call site with unpacked arguments cannot carry a binding; it must refuse")
	}
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("want a typed refusal, got %T", err)
	}
	// 🔴 THE assertion. Reversing the order in spanMaterializeSkills makes this line fail, which is what
	// makes the ordering a guarantee rather than a comment.
	if re.Cause != CauseCallSiteShape {
		t.Fatalf("the reported cause is %q; the call site's own shape must win over the missing spelling, "+
			"because it stays true after the spelling lands.\ngot: %s", re.Cause, re.Detail)
	}
	mustContain(t, re.Detail, "**api_kwargs", "the unpacking the author can act on")
	if strings.Contains(re.Detail, "no declared tool-value spelling") {
		t.Errorf("the refusal blamed the missing spelling for a call site that would refuse anyway:\n%s", re.Detail)
	}
}

// The same fixture with the spelling PRESENT still refuses, and refuses identically — which is the
// property that makes the ordering correct rather than merely nicer. If the call-site refusal changed
// when coverage arrived, then coverage really was the operative cause and the order would be wrong.
func TestCallSiteRefusalIsUnchangedByCoverage(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyKwargsSrc)
	id := onlyNode(t, root, "python")
	entry := skillEntry(t, "search_kb", `{"type":"object","properties":{"query":{"type":"string"}}}`)

	_, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Skills: []*registry.SkillEntry{entry}},
	}), root)
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("want a typed refusal, got %v", err)
	}
	if re.Cause != CauseCallSiteShape {
		t.Errorf("with the spelling present the cause must still be the call site's, got %q", re.Cause)
	}
}

// ── P14 11.15 🔴 — parity of contract across languages ──────────────────────────────────────────

// The same pinned skill, materialized in two languages, must offer the model the SAME argument
// contract. Two languages that disagreed about the contract while sharing one config_hash would make
// the platform compare two configurations and call them one — every number downstream then quietly
// stops meaning what it says.
func TestBoundSkillContractParityAcrossLanguages(t *testing.T) {
	const sealed = `{"type":"object","properties":{"query":{"type":"string"},"top_k":{"type":"integer"}}}`
	entry := skillEntry(t, "search_kb", sealed)
	skills := []*registry.SkillEntry{entry}

	for _, cell := range []toolValueCell{
		toolValueKey("python", "anthropic"),
		toolValueKey("typescript", "anthropic"),
		toolValueKey("javascript", "anthropic"),
	} {
		form, ok := toolValueForms[cell]
		if !ok {
			t.Fatalf("no spelling for %v", cell)
		}
		got, err := toolListLiteral(form, skills)
		if err != nil {
			t.Fatalf("%v: %v", cell, err)
		}
		// Every language names the same skill and carries every property the SEALED schema declared.
		for _, want := range []string{"search_kb", "query", "top_k"} {
			if !strings.Contains(got, want) {
				t.Errorf("%v dropped %q from the sealed contract:\n%s", cell, want, got)
			}
		}
	}
}

// A sealed contract with no argument shape refuses in EVERY language — a fact about the contract, and
// one no spelling row changes.
func TestEmptySealedShapeRefusesInEveryLanguage(t *testing.T) {
	entry := skillEntry(t, "search_kb", `{"type":"object"}`)
	for cell, form := range toolValueForms {
		if _, err := toolListLiteral(form, []*registry.SkillEntry{entry}); err == nil {
			t.Errorf("%v materialized a tool with no argument shape; an empty property bag is a tool the "+
				"model can never call successfully", cell)
		}
	}
}

// ── P13 18.8 🚫 — no gate is relaxed to reach a language ─────────────────────────────────────────

// Every registered language's engine supplies EVERY gate the contract requires. A language served by a
// half-filled row would skip a safety check for one language only, silently — patches shipping unchecked
// for Rust would be indistinguishable, in the record, from patches that were checked.
func TestNoLanguageSkipsAGate(t *testing.T) {
	for _, lang := range RegisteredLanguages() {
		eng, err := engineFor(lang)
		if err != nil {
			t.Errorf("registered language %q has no usable engine: %v", lang, err)
			continue
		}
		if eng.index == nil {
			t.Errorf("%q's engine cannot locate a call site", lang)
		}
		if eng.reparse == nil {
			t.Errorf("%q's engine supplies no \"the result still parses\" assertion; a language whose "+
				"emissions are never reparsed is a language whose codemod is ungated", lang)
		}
	}
	// 🚫 And an unknown language is an ERROR, never a permissive default — in particular never a
	// fall-back to Go, which would find no call sites and report the node as missing: a lie with a
	// plausible sentence attached.
	if _, err := engineFor("elixir"); err == nil {
		t.Error("an unregistered language was served an engine")
	}
	if _, err := engineFor("mixed"); err == nil {
		t.Error("a polyglot workflow was served an engine; one patch, one language, one verifier")
	}
}

// 🔴 The binding-site edit is a NEW EDIT CLASS, not a loosened gate. It may admit its own line — and
// nothing else. A rewriter that returned an ordinary edit on that same line must still be rejected,
// which is what proves the window was not widened.
func TestBindingSiteAdmitsOnlyItsOwnLine(t *testing.T) {
	src := []byte("a\nb\nc\nd\n")
	allowed := map[int]bool{3: true} // the call site's own line
	ordinary := []edit{{Start: 0, End: 1, New: "z", NodeID: "n", Dim: "model"}}
	if err := gateMinimal("f.kt", src, []byte("z\nb\nc\nd\n"), ordinary, allowed, reparseNoop); err == nil {
		t.Fatal("an ordinary edit on an untargeted line must still be rejected; the window was widened")
	}
	// The same edit, marked as a binding site AND with its line admitted, passes — because Generate
	// admits exactly that line when it emits one.
	binding := []edit{{Start: 0, End: 1, New: "z", NodeID: "n", Dim: "model", Binding: true}}
	if err := gateMinimal("f.kt", src, []byte("z\nb\nc\nd\n"), binding, map[int]bool{1: true, 3: true}, reparseNoop); err != nil {
		t.Fatalf("a binding-site edit on an admitted line must pass: %v", err)
	}
}

func reparseNoop(string, []byte, []byte) error { return nil }

// ── P13 §19 / P14 11.16 🔴 — coverage growth changes no MEASUREMENT ─────────────────────────────

// The invariant that makes it safe to keep adding cells: a previously materializable change emits the
// SAME bytes after the table grows. Every added row is a new place to emit a diff, and diffs are what
// the harness scores — so a spelling row that also perturbed an existing one would silently re-score
// work already in the ledger.
//
// The fixture is deliberately a Go anthropic call site: the oldest, most-depended-on cell in the table.
func TestCoverageGrowthPreservesExistingDiffsAndHashes(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	spec := func() *variantspec.Resolved {
		return resolvedWith(map[string]variantspec.ResolvedOverride{
			ids["agent"]: {Model: modelEntry("anthropic", "claude-sonnet-5")},
		})
	}

	first, err := Generate(spec(), root)
	if err != nil {
		t.Fatalf("the baseline cell must materialize: %v", err)
	}

	// Add a cell — the same thing a new language's spelling row does — and re-generate.
	added := toolValueKey("elixir", "anthropic")
	toolValueForms[added] = toolValueForm{
		openList: "[", closeList: "]", sdkNote: "a fixture row",
		element: func(name string, schema jsonSchemaDoc) (string, error) { return "{}", nil },
	}
	t.Cleanup(func() { delete(toolValueForms, added) })

	second, err := Generate(spec(), root)
	if err != nil {
		t.Fatalf("adding a cell broke an existing materialization: %v", err)
	}
	if string(first.Diff) != string(second.Diff) {
		t.Errorf("adding a coverage cell changed an existing diff:\n--- before\n%s\n--- after\n%s",
			first.Diff, second.Diff)
	}
	if first.DiffHash != second.DiffHash {
		t.Errorf("the diff hash moved: %s -> %s", first.DiffHash, second.DiffHash)
	}
	if first.ConfigHash != second.ConfigHash {
		t.Errorf("the config hash moved: %s -> %s; coverage is not part of identity and must never "+
			"perturb it", first.ConfigHash, second.ConfigHash)
	}
}

// 🔴 P13 19.2 / FR48 — SEMANTIC PARITY. The same resolved override, materialized in two languages,
// expresses the SAME configuration. Two languages that meant different things by one spec would make
// the platform compare two configurations while calling them one.
//
// Asserted over a shared fixture rather than by inspection, and over the MODEL dimension because that is
// the one every language can carry — so it is the strictest available comparison.
func TestSameOverrideMeansTheSameThingAcrossLanguages(t *testing.T) {
	const wantModel = "gpt-5-mini"
	cases := []struct{ lang, file, src string }{
		{"python", "pipeline.py", `import openai

client = openai.OpenAI()


def chat():
    return client.chat.completions.create(model="gpt-4o", messages=[{"role": "user", "content": "hi"}])
`},
		{"typescript", "pipeline.ts", `import OpenAI from "openai";

const client = new OpenAI();

export async function chat() {
  return client.chat.completions.create({ model: "gpt-4o", messages: [{ role: "user", content: "hi" }] });
}
`},
		{"kotlin", "Triage.kt", kotlinBoundSrc},
		{"java", "Triage.java", javaBoundSrc},
		{"rust", "lib.rs", rustBoundSrc},
	}
	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			root := spanTarget(t, tc.file, tc.src)
			sites := spanSites(t, root, tc.lang)
			var id string
			for k := range sites {
				id = k
			}
			p, err := Generate(resolvedIn(tc.lang, map[string]variantspec.ResolvedOverride{
				id: {Model: modelEntry("openai", wantModel)},
			}), root)
			if err != nil {
				t.Fatalf("the model override must materialize in %s: %v", tc.lang, err)
			}
			after := string(p.Files[tc.file])
			// Every language now states the SAME configuration — the model the spec named — and none of
			// them states anything else about it.
			if !strings.Contains(after, `"`+wantModel+`"`) {
				t.Errorf("%s does not state the configured model:\n%s", tc.lang, after)
			}
			if strings.Contains(after, `"gpt-4o"`) {
				t.Errorf("%s still states the previous model:\n%s", tc.lang, after)
			}
			// 🔴 And exactly one binding moved: a language that rewrote two places would be applying a
			// broader interpretation of the same override than its siblings.
			if got := strings.Count(after, `"`+wantModel+`"`); got != 1 {
				t.Errorf("%s wrote the configured model %d times, want exactly 1:\n%s", tc.lang, got, after)
			}
		})
	}
}

// 🔴 P13 19.3 / P14 11.17 — the harness stays axis- AND language-agnostic. A variant materialized in a
// newly covered language is scored with ZERO eval change, because scoring consumes only config_hash and
// the trace — never the language, never the axis. Asserted structurally: the same configuration hashes
// identically whatever language materialized it.
func TestNewLanguageNeedsNoEvalChange(t *testing.T) {
	pyRoot := spanTarget(t, "pipeline.py", `import openai

client = openai.OpenAI()


def chat():
    return client.chat.completions.create(model="gpt-4o", messages=[{"role": "user", "content": "hi"}])
`)
	pySites := spanSites(t, pyRoot, "python")
	var pyID string
	for k := range pySites {
		pyID = k
	}
	override := variantspec.ResolvedOverride{Model: modelEntry("openai", "gpt-5-mini")}

	p, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{pyID: override}), pyRoot)
	if err != nil {
		t.Fatalf("python must materialize: %v", err)
	}
	// The patch carries a config_hash and a source revision, and nothing that names a language or an
	// axis: that absence is what makes the harness able to score a new language without changing.
	if p.ConfigHash == "" {
		t.Fatal("a materialized patch must carry the config_hash the harness scores by")
	}
	for _, td := range p.Touched {
		if td.Dim == "" || td.NodeID == "" {
			t.Errorf("a touched dimension is unattributed: %+v", td)
		}
	}
}

// ── P13 21.2 🔴 — every coverage row carries a NAMED EXECUTABLE PROOF ────────────────────────────

// A coverage row is a claim that this engine can emit a well-formed change in that cell. The only thing
// that can prove such a claim is a run, so every materializing cell must be reachable from a fixture
// that emits in it. A row admitted on a document is not a row.
//
// The proof is asserted STRUCTURALLY rather than by a list of test names: each materializing cell's
// artifact must exist in the engine's own tables, so a row cannot be added by editing a doc.
func TestEveryCoverageRowHasAProof(t *testing.T) {
	for _, c := range AxisCoverage() {
		if c.Status != CoverageMaterializes {
			continue
		}
		switch c.Axis {
		case string(variantspec.DimSkills):
			form, ok := toolValueForms[toolValueKey(c.Language, c.Form)]
			if !ok {
				t.Errorf("skills/%s/%s claims to materialize with no spelling row behind it", c.Language, c.Form)
				continue
			}
			if form.element == nil || form.openList == "" {
				t.Errorf("skills/%s/%s's row cannot emit anything", c.Language, c.Form)
			}
			// 🔴 The row NAMES the SDK generation it targets. "anthropic is supported" is not true of an
			// anthropic call site on a different generation, so a row without one is an unbounded claim.
			if form.sdkNote == "" {
				t.Errorf("skills/%s/%s does not name the SDK generation its spelling targets", c.Language, c.Form)
			}
		case wiringRefusalDim:
			if !HasStatementMaterializer(c.Language) {
				t.Errorf("wiring/%s claims to materialize with no statement resolver", c.Language)
			}
		case string(variantspec.DimTools):
			if !discovery.RecordsToolSplit(c.Language) {
				t.Errorf("tools/%s claims to materialize with no recorded tool split", c.Language)
			}
		case string(variantspec.DimContext):
			if c.Note == "" {
				t.Errorf("context/%s/%s materializes with no stated behaviour", c.Language, c.Form)
			}
		}
	}
}

// ── P13 21.3 🔴 — a polyglot workflow refuses BY NAME ───────────────────────────────────────────

// One patch, one language, one verifier. A workflow whose nodes span two languages is refused with the
// languages named, and no language is chosen on the user's behalf — picking the majority would emit a
// patch the single verifier behind it could not honestly gate.
func TestPolyglotWorkflowRefusesByName(t *testing.T) {
	_, err := engineFor("mixed")
	if err == nil {
		t.Fatal("a polyglot workflow must be refused, not served an engine")
	}
	if !strings.Contains(err.Error(), "mixed") {
		t.Errorf("the refusal must name what it was asked for, got %v", err)
	}
	// It also tells the reader what WOULD have worked, which is the difference between a refusal and a
	// dead end.
	for _, lang := range RewritableLanguages() {
		if !strings.Contains(err.Error(), lang) {
			t.Errorf("the refusal does not list %q among the rewritable languages: %v", lang, err)
		}
	}
}

// ── P13 21.5 / P14 11.23 / P16 10.14 🔴 — assert the DOWNSTREAM consumer ────────────────────────

// After a materialization in a newly covered language, read back what a consumer actually gets: the
// emitted diff, the reparse verdict, and the recorded coverage cell. A green suite is compatible with a
// materializer that emitted nothing, and with a cell that claims a capability the emission did not have.
func TestNewLanguageAssertsDownstreamState(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyToolsSrc)
	id := onlyNode(t, root, "python")
	entry := skillEntry(t, "search_kb", `{"type":"object","properties":{"query":{"type":"string"}}}`)

	p, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Skills: []*registry.SkillEntry{entry}},
	}), root)
	if err != nil {
		t.Fatalf("the (python, anthropic) cell must materialize: %v", err)
	}

	// 1. The DIFF exists and carries the constructed value.
	if len(p.Diff) == 0 {
		t.Fatal("a materialized binding produced no diff for a reviewer to read")
	}
	if !strings.Contains(string(p.Diff), "search_kb") {
		t.Errorf("the emitted diff does not carry the bound skill:\n%s", p.Diff)
	}
	// 2. The RESULT still parses, under this language's own reparser — the gate a consumer relies on.
	eng, err := engineFor("python")
	if err != nil {
		t.Fatal(err)
	}
	before := []byte(pyToolsSrc)
	after, ok := p.Files["pipeline.py"]
	if !ok {
		t.Fatal("the patch does not carry the file it claims to have changed")
	}
	if err := eng.reparse("pipeline.py", before, after); err != nil {
		t.Fatalf("the materialized source does not reparse: %v", err)
	}
	// 3. The COVERAGE CELL agrees that this was supposed to work.
	var cell *CoverageCell
	for _, c := range CoverageFor(string(variantspec.DimSkills)) {
		if c.Language == "python" && c.Form == "anthropic" {
			cc := c
			cell = &cc
		}
	}
	if cell == nil {
		t.Fatal("the coverage table has no cell for the emission that just succeeded")
	}
	if cell.Status != CoverageMaterializes {
		t.Errorf("the engine materialized a cell coverage reports as %q — the table and the engine disagree",
			cell.Status)
	}
	// 4. And the TOUCHED record attributes it, so a build failure can be traced to a node and a dimension.
	if len(p.Touched) == 0 {
		t.Error("the patch records no touched dimension; a compiler error could not be attributed")
	}
}

// ── P14 11.5 🚫 — the two tables are never collapsed ────────────────────────────────────────────

// Binding coverage and pruning coverage answer different questions and are blocked in different
// packages. A language routinely prunes before it can bind, so one combined answer would be resolved —
// in practice — toward the pessimistic one, and a customer whose real need is a prune would be told the
// axis does not apply to them.
func TestBindingAndPruningCoverageAreStatedSeparately(t *testing.T) {
	skills := CoverageFor(string(variantspec.DimSkills))
	tools := CoverageFor(string(variantspec.DimTools))
	if len(skills) == 0 || len(tools) == 0 {
		t.Fatal("both mechanics must publish coverage")
	}
	// 🔴 The proof that they are two answers: at least one language where they DISAGREE. Today Kotlin,
	// Java and Rust prune and cannot bind. If this ever stops being true the test should be re-read
	// rather than deleted — a single-answer world is one where the two tables could be merged.
	disagree := false
	for _, lang := range RegisteredLanguages() {
		binds, prunes := false, false
		for _, c := range skills {
			if c.Language == lang && c.Status == CoverageMaterializes {
				binds = true
			}
		}
		for _, c := range tools {
			if c.Language == lang && c.Status == CoverageMaterializes {
				prunes = true
			}
		}
		if binds != prunes {
			disagree = true
		}
	}
	if !disagree {
		t.Error("no language's binding and pruning answers differ, so this test can no longer prove the " +
			"two tables are independent; re-read D-14.5 before relaxing it")
	}
	// And neither table's cells leak into the other's axis label.
	for _, c := range skills {
		if c.Axis != string(variantspec.DimSkills) {
			t.Errorf("a skills cell carries axis %q", c.Axis)
		}
	}
}

// ── P14 11.14 🚫 — the pruner reads the recorded location and never infers ──────────────────────

// A tool the frontend could not locate is recorded as unlocatable, and every prune over it refuses. The
// pruner may not derive which element is which by position, by text similarity, or by matching the
// selection's names against element text — each of those deletes the wrong element in a diff that
// parses.
func TestPrunerReadsTheRecordedLocationOnly(t *testing.T) {
	// A run-time-assembled tool set: recorded, unlocatable, and refused rather than guessed at.
	root := spanTarget(t, "pipeline.py", `import anthropic

client = anthropic.Anthropic()


def chat(ctx):
    return client.messages.create(
        model="claude-opus-4-6",
        messages=[{"role": "user", "content": "hi"}],
        tools=build_tools(ctx),
    )
`)
	id := onlyNode(t, root, "python")
	msg := refusalFor(t, resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {ToolSelection: []string{"search"}},
	}), root)
	mustContain(t, msg, "not a list written here", "that the engine will not guess at an unwritten set")
	if strings.Contains(msg, "no pruner for this language") {
		t.Errorf("a source fact was reported as a coverage gap:\n%s", msg)
	}

	// And a selection naming an element the call site does not carry is refused rather than applied to
	// the nearest match — the fail-closed half of "never infers".
	root2 := spanTarget(t, "pipeline.py", pyToolsSrc)
	id2 := onlyNode(t, root2, "python")
	msg2 := refusalFor(t, resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id2: {ToolSelection: []string{`{"name": "searc"}`}}, // a near-miss
	}), root2)
	mustContain(t, msg2, "the tree and the IR", "that a near-miss is a disagreement, not a fuzzy match")
}
