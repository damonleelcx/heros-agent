package transform

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P18 §11 — the Python harness call-site rewriter.
//
// Everything here goes through the REAL discovery against a REAL fixture. The bug this phase could
// plausibly ship is a fixed-N loop wearing a strategy's name, and a test that fed a rewriter its own
// spans could not see it.

// pyHarnessSrc is the shape that materializes: a written message list inside a locatable call.
const pyHarnessSrc = `import openai

client = openai.OpenAI()


def chat(question):
    resp = client.chat.completions.create(
        model="gpt-4o",
        messages=[{"role": "user", "content": question}],
    )
    return resp
`

// pyHarnessKwargsSrc assembles its request elsewhere. There is no written list for the loop's
// continuation to append to — the DECIDE half's precondition at a call site.
const pyHarnessKwargsSrc = `import openai

client = openai.OpenAI()


def chat(**kwargs):
    resp = client.chat.completions.create(**kwargs)
    return resp
`

// pyHarnessNonListSrc writes a `messages=` argument that is not a list literal.
const pyHarnessNonListSrc = `import openai

client = openai.OpenAI()


def chat(built):
    resp = client.chat.completions.create(model="gpt-4o", messages=built)
    return resp
`

// materializeHarnessAt runs the harness materializer against a REAL discovered call site.
func materializeHarnessAt(t *testing.T, src, strategy string) ([]edit, error) {
	t.Helper()
	root := spanTarget(t, "pipeline.py", src)
	sites := spanSites(t, root, "python")
	if len(sites) != 1 {
		t.Fatalf("fixture has %d call sites, want 1", len(sites))
	}
	var site discovery.SpanCallSite
	for _, s := range sites {
		site = s
	}
	return spanMaterializeHarness(site, []byte(src), harnessOverride(t, strategy))
}

// TestPythonHarnessMaterializes — task 11.3. One edit, and the author's own call survives verbatim inside
// it: a harness WRAPS a call, it does not rewrite what the author asked for.
func TestPythonHarnessMaterializes(t *testing.T) {
	edits, err := materializeHarnessAt(t, pyHarnessSrc, "reflexion")
	if err != nil {
		t.Fatalf("a complete harness cell must materialize: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("got %d edit(s), want exactly 1 — a harness is one wrap of one call", len(edits))
	}
	after := applyTo(t, pyHarnessSrc, edits)

	if !strings.Contains(after, `agentharness.run(`) {
		t.Fatalf("the loop is missing:\n%s", after)
	}
	if !strings.Contains(after, "lambda "+harnessLoopParam+":") {
		t.Errorf("the call was not passed as a re-invocable thunk:\n%s", after)
	}
	// The author's model argument survives byte-for-byte; only the message argument became the loop's
	// parameter, and the written list moved out to be the loop's input.
	if !strings.Contains(after, `model="gpt-4o"`) {
		t.Errorf("the author's own arguments did not survive the wrap:\n%s", after)
	}
	if !strings.Contains(after, "messages="+harnessLoopParam) {
		t.Errorf("the message argument was not replaced by the loop's parameter:\n%s", after)
	}
	if !strings.Contains(after, `[{"role": "user", "content": question}]`) {
		t.Errorf("the author's written message list was not passed to the loop:\n%s", after)
	}
	// 🔴 And the wrapped call is still a single expression — the statement's own shape is untouched, so
	// `return f(...)` would work exactly as `resp = f(...)` does. That is the difference from memory,
	// whose record half needs a name to hold the response.
	if !strings.Contains(after, "resp = agentharness.run(") {
		t.Errorf("the wrap did not replace the call expression in place:\n%s", after)
	}
}

// TestPythonHarnessEditIsMinimalAndReparses — task 11.3 🔴. The line-count invariant, and the result
// actually parsing. The wrap moves the message text from inside the call to after it, so the newline
// count is preserved by arithmetic; this asserts the arithmetic rather than trusting it.
func TestPythonHarnessEditIsMinimalAndReparses(t *testing.T) {
	edits, err := materializeHarnessAt(t, pyHarnessSrc, "reflexion")
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	after := applyTo(t, pyHarnessSrc, edits)

	beforeLines := strings.Count(pyHarnessSrc, "\n")
	afterLines := strings.Count(after, "\n")
	if beforeLines != afterLines {
		t.Fatalf("the wrap changed the file's line count (%d -> %d); every line below would shift and "+
			"attribution would break", beforeLines, afterLines)
	}

	// Only the call site's own lines changed.
	b, a := strings.Split(pyHarnessSrc, "\n"), strings.Split(after, "\n")
	for i := range b {
		if b[i] == a[i] {
			continue
		}
		if i+1 < 7 || i+1 > 10 {
			t.Errorf("line %d changed and is outside the call site: %q -> %q", i+1, b[i], a[i])
		}
	}

	// And it parses, checked by the engine's own reparser rather than by eye.
	if err := discovery.ParseCheck("python", "pipeline.py", []byte(after)); err != nil {
		t.Fatalf("the rewritten source does not parse: %v\n%s", err, after)
	}
	// Executed through python3 when one is available, because a tree-sitter parse is a weaker claim than
	// the language's own.
	if py, lookErr := exec.LookPath("python3"); lookErr == nil {
		dir := t.TempDir()
		path := filepath.Join(dir, "pipeline.py")
		if err := os.WriteFile(path, []byte(after), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if out, err := exec.Command(py, "-m", "py_compile", path).CombinedOutput(); err != nil {
			t.Fatalf("python3 rejected the rewritten source: %v\n%s\n%s", err, out, after)
		}
	}
}

// TestHalfMaterializableHarnessRefusedWhole — task 11.4 🔴. A call site that can be re-invoked but whose
// message list is assembled elsewhere is REFUSED, not given a loop that re-asks the identical question.
func TestHalfMaterializableHarnessRefusedWhole(t *testing.T) {
	for _, c := range []struct{ name, src, wants string }{
		{"kwargs", pyHarnessKwargsSrc, "**kwargs"},
		{"non-list", pyHarnessNonListSrc, "rather than a written list"},
	} {
		t.Run(c.name, func(t *testing.T) {
			edits, err := materializeHarnessAt(t, c.src, "reflexion")
			if err == nil {
				t.Fatalf("a call site with no written message list materialized %d edit(s). The DRIVE half "+
					"works here — the call can be wrapped — and emitting it alone would produce a loop that "+
					"re-asks the identical question N times: a single shot at N times the price, running "+
					"under a multi-turn config_hash", len(edits))
			}
			if len(edits) != 0 {
				t.Error("a refusal returned edits; both halves are resolved before the first edit is emitted")
			}
			var re *RewriteError
			if !errors.As(err, &re) {
				t.Fatalf("err is not a *RewriteError: %v", err)
			}
			// 🔴 The CALL-SITE cause, not the platform one. The reason is their code, it is actionable, and
			// it stays true after every rewriter lands.
			if re.Cause != CauseCallSiteShape {
				t.Errorf("cause = %q, want %q — telling this author to wait for a rewriter would send them "+
					"to wait for something that would refuse them too", re.Cause, CauseCallSiteShape)
			}
			if !strings.Contains(re.Detail, c.wants) {
				t.Errorf("the refusal does not name the real reason (%q): %s", c.wants, re.Detail)
			}
		})
	}
}

// TestHostServiceRefusedAtPythonCallSite — task 11.5. The three strategies needing a second actor refuse
// even in the covered language, because a call site has nowhere to inject one.
func TestHostServiceRefusedAtPythonCallSite(t *testing.T) {
	for _, strategy := range []string{"react-loop", "plan-execute", "critic-loop"} {
		t.Run(strategy, func(t *testing.T) {
			_, err := materializeHarnessAt(t, pyHarnessSrc, strategy)
			if err == nil {
				t.Fatalf("%s materialized at a Python call site; the loop it emitted could not run its tool, "+
					"plan its steps, or call its critic, so it would be a different strategy", strategy)
			}
			var re *RewriteError
			if !errors.As(err, &re) {
				t.Fatalf("err is not a *RewriteError: %v", err)
			}
			if re.Cause != CauseNotAtCallSite {
				t.Errorf("cause = %q, want %q — there is nothing for the platform to build here",
					re.Cause, CauseNotAtCallSite)
			}
		})
	}
}

// TestSingleShotEmitsNothingInPython — the identity, in the covered language. It must not be refused and
// must not emit: one turn IS the un-rewritten call site.
func TestSingleShotEmitsNothingInPython(t *testing.T) {
	edits, err := materializeHarnessAt(t, pyHarnessSrc, registry.StrategySingleShot)
	if err != nil {
		t.Fatalf("the identity strategy was refused: %v", err)
	}
	if len(edits) != 0 {
		t.Fatalf("the identity strategy emitted %d edit(s)", len(edits))
	}
}

// TestGoHarnessIdentityOnlyAndRefusalIsPermanent — task 11.6. Go materializes the identity and refuses
// every multi-turn strategy with a PERMANENT cause and NO missing artifact.
func TestGoHarnessIdentityOnlyAndRefusalIsPermanent(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	// The identity: no refusal, no change.
	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["summarize"]: harnessOverride(t, registry.StrategySingleShot),
	}), root)
	if err != nil || p == nil || len(p.Files) != 0 {
		t.Fatalf("Go refused or changed something for the identity strategy: err=%v", err)
	}

	// `reflexion` — the one whose refusal is about Go rather than about host services.
	_, err = Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["summarize"]: harnessOverride(t, "reflexion"),
	}), root)
	if err == nil {
		t.Fatal("Go materialized reflexion; deciding whether to take another turn means reading the " +
			"answer's TEXT, and a Go response is the customer's SDK type")
	}
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("err is not a *RewriteError: %v", err)
	}
	// 🔴 Permanent, not owed. Naming a missing artifact would promise work that would not help.
	if re.Cause != CauseNotAtCallSite {
		t.Errorf("cause = %q, want %q — there is nothing to build: a generated Go module cannot read a "+
			"field off the customer's SDK type without importing their SDK", re.Cause, CauseNotAtCallSite)
	}
	for _, c := range CoverageFor(string(variantspec.DimHarness)) {
		if c.Language != "go" || c.Form != "reflexion" {
			continue
		}
		if c.MissingArtifact != "" {
			t.Errorf("the go/reflexion cell names a missing artifact %q; the asymmetry with Python is a "+
				"fact about static typing, not a backlog item", c.MissingArtifact)
		}
	}
}

// TestHarnessCoverageReflectsMaterializers — task 11.7. Coverage stops being uniform and becomes a READ of
// the materializer table, so the claim and the behaviour cannot disagree.
func TestHarnessCoverageReflectsMaterializers(t *testing.T) {
	got := map[string]map[string]CoverageStatus{}
	for _, c := range CoverageFor(string(variantspec.DimHarness)) {
		if got[c.Language] == nil {
			got[c.Language] = map[string]CoverageStatus{}
		}
		got[c.Language][c.Form] = c.Status
	}
	if len(got) < 2 {
		t.Fatalf("coverage reports %d language(s); the read would be uniform by accident", len(got))
	}

	// Python materializes reflexion; every other language refuses it. If BOTH were true everywhere the
	// table would be uniform again — which was P17's honest state for memory and is not P18's here.
	if got["python"]["reflexion"] != CoverageMaterializes {
		t.Errorf("python/reflexion = %q, want materializes", got["python"]["reflexion"])
	}
	uniform := true
	for lang, cells := range got {
		if lang == "python" {
			continue
		}
		if cells["reflexion"] == CoverageMaterializes {
			t.Errorf("%s/reflexion claims to materialize, but only Python has an emitted module", lang)
		}
		if cells["reflexion"] != got["python"]["reflexion"] {
			uniform = false
		}
	}
	if uniform {
		t.Error("every language reports the same verdict for reflexion; the read is uniform, which would " +
			"mean it is not derived from the materializer table")
	}

	// And the identity materializes everywhere, in every language.
	for lang, cells := range got {
		if cells[registry.StrategySingleShot] != CoverageMaterializes {
			t.Errorf("%s reports the identity as %q", lang, cells[registry.StrategySingleShot])
		}
	}
}

// TestHarnessMaterializesEndToEnd — the whole path through Generate: the call-site edit, the import, the
// generated module, and the params document, all in ONE patch so one revert restores everything.
func TestHarnessMaterializesEndToEnd(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyHarnessSrc)
	id := onlyNode(t, root, "python")

	r := resolvedIn("python", map[string]variantspec.ResolvedOverride{id: harnessOverride(t, "reflexion")})
	p, err := Generate(r, root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	out := string(p.Files["pipeline.py"])
	if !strings.Contains(out, "import "+harnessImportName+"\n") {
		t.Errorf("the rewritten file does not import the generated module:\n%s", out)
	}
	if !strings.Contains(out, harnessImportName+".run(") {
		t.Errorf("the call site was not wrapped:\n%s", out)
	}

	// 🔴 The artifact ships in the SAME patch. A module in the tree without the call, or a call without
	// the module, is a broken repository either way — and one revert has to restore both.
	mod, ok := p.Files[pyHarnessModulePath]
	if !ok {
		t.Fatalf("the generated module is not in the patch (files: %v)", patchFileNames(p))
	}
	doc, ok := p.Files[harnessDocPath]
	if !ok {
		t.Fatalf("the params document is not in the patch (files: %v)", patchFileNames(p))
	}

	// 🚫 Dependency-free: standard library only.
	for _, line := range strings.Split(string(mod), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "import ") && !strings.HasPrefix(trimmed, "from ") {
			continue
		}
		name := strings.Fields(strings.TrimPrefix(strings.TrimPrefix(trimmed, "from "), "import "))[0]
		switch name {
		case "json", "os", "threading":
		default:
			t.Errorf("the generated module imports %q, which is outside the standard library", name)
		}
	}

	// The params travel as DATA, so retuning max_turns is a document change.
	var parsed HarnessDocument
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("the params document is not valid JSON: %v", err)
	}
	node, ok := parsed.Nodes[id]
	if !ok {
		t.Fatalf("the document has no entry for the rewritten node (%v)", parsed.Nodes)
	}
	if node.Strategy != "reflexion" || node.Params["max_turns"] == nil {
		t.Fatalf("the document lost the strategy or its params: %+v", node)
	}
	// 🔴 What "as data" means precisely: the module carries no NODE and no PARAM VALUE. It does branch on
	// strategy NAMES — that is the loop's definition, which is structure — but nothing about this
	// particular configuration is interpolated into it, so retuning max_turns changes only the document.
	if strings.Contains(string(mod), id) {
		t.Error("the generated module names the node it was generated for; the module must be a constant, " +
			"or two nodes in one tree would need two modules")
	}
	for _, baked := range []string{"max_turns = 3", "max_turns=3", `"max_turns": 3`} {
		if strings.Contains(string(mod), baked) {
			t.Errorf("the generated module bakes in the parameter value %q; retuning max_turns must be a "+
				"document change, not a code change", baked)
		}
	}
	// And the module is byte-identical to the constant — nothing was templated into it at all.
	if string(mod) != pythonHarnessModule {
		t.Error("the emitted module differs from the constant; it is templated, so regeneration cannot be " +
			"byte-identical for two configurations that differ only in params")
	}
}

func patchFileNames(p *Patch) []string {
	out := make([]string, 0, len(p.Files))
	for f := range p.Files {
		out = append(out, f)
	}
	sortStrings(out)
	return out
}

// TestHarnessArtifactRegeneratesByteIdentically — task 11.2 🔴. The same resolved configuration
// regenerates the artifact byte-for-byte, so a re-apply produces no spurious module diff.
func TestHarnessArtifactRegeneratesByteIdentically(t *testing.T) {
	r := resolvedIn("python", map[string]variantspec.ResolvedOverride{
		"n1": harnessOverride(t, "reflexion"),
		"n2": harnessOverride(t, "react-loop"),
	})
	first, err := GenerateHarnessArtifacts(r, "python")
	if err != nil {
		t.Fatalf("GenerateHarnessArtifacts: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("got %d artifact file(s), want 2", len(first))
	}
	for i := 0; i < 10; i++ {
		again, err := GenerateHarnessArtifacts(r, "python")
		if err != nil {
			t.Fatalf("regenerate: %v", err)
		}
		for path, want := range first {
			if string(again[path]) != string(want) {
				t.Fatalf("%s regenerated differently on run %d", path, i)
			}
		}
	}

	// 🚫 The identity generates nothing: a module nothing calls is dead code we put in someone's tree.
	empty, err := GenerateHarnessArtifacts(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		"n1": harnessOverride(t, registry.StrategySingleShot),
	}), "python")
	if err != nil {
		t.Fatalf("GenerateHarnessArtifacts (identity): %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("the identity strategy generated %d artifact file(s)", len(empty))
	}

	// 🚫 And no language without a module receives another language's.
	if _, err := GenerateHarnessArtifacts(r, "rust"); err == nil {
		t.Error("a language with no harness module was handed one")
	}
}

// TestHarnessModuleImplementsEverySealedStrategy — the conformance pin. A sixth strategy landing without
// a branch in the emitted module would produce a module that raises at run time for a configuration the
// registry happily seals.
func TestHarnessModuleImplementsEverySealedStrategy(t *testing.T) {
	got := harnessStrategyNamesInModule()
	want := registry.HarnessStrategyNames()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the emitted module implements %v but the sealed vocabulary is %v; a strategy the "+
			"registry seals and the module does not implement raises in the customer's process", got, want)
	}
}
