package transform

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// newTarget materializes the fixture repo into a temp dir and returns its root.
//
// The fixture's sources are committed as .txt and copied to .go here, because a directory of real
// .go files importing an SDK this module does not depend on would break `go build ./...` for the
// whole repo. testdata is excluded from the discovery loader's walk (skipDir), so the fixture has to
// live somewhere the loader will actually visit — a temp dir.
func newTarget(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for src, dst := range map[string]string{
		"testdata/target/go.mod.txt":      "go.mod",
		"testdata/target/pipeline.go.txt": "pipeline.go",
		"testdata/target/wiring.go.txt":   "wiring.go",
	} {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read fixture %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(root, dst), b, 0o600); err != nil {
			t.Fatalf("write %s: %v", dst, err)
		}
	}
	return root
}

func ptrF(f float64) *float64 { return &f }

func modelEntry(provider, modelID string) *registry.ModelEntry {
	return &registry.ModelEntry{VersionID: strings.Repeat("a", 64), Name: "m",
		Spec: registry.ModelSpec{Provider: provider, ModelID: modelID,
			Params: registry.ModelParams{Temperature: ptrF(0)}}}
}

// promptEntry builds a resolved prompt entry the way the Loader would: through the registry's real
// ParseTemplate, so a test can never assert against a template shape the registry would reject.
func promptEntry(t *testing.T, name, body string) *registry.PromptEntry {
	t.Helper()
	tmpl, err := registry.ParseTemplate(body)
	if err != nil {
		t.Fatalf("ParseTemplate(%q): %v", body, err)
	}
	return &registry.PromptEntry{
		VersionID: strings.Repeat("p", 64), Name: name, Template: tmpl,
		Spec: registry.PromptSpec{BodyBlobHash: strings.Repeat("b", 64), Slots: tmpl.Slots()},
	}
}

// nodeIDs returns the fixture's discovered node ids keyed by enclosing symbol, so tests can name a
// call site by what it is rather than by an opaque hash.
func nodeIDs(t *testing.T, root string) map[string]string {
	t.Helper()
	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		t.Fatalf("IndexGoCallSites: %v", err)
	}
	if len(sites) == 0 {
		t.Fatal("discovery found no call sites in the fixture; the codemod has nothing to anchor on")
	}
	out := map[string]string{}
	for id, s := range sites {
		fn := enclosingSymbol(t, root, s)
		out[fn] = id
	}
	return out
}

func enclosingSymbol(t *testing.T, root string, s discovery.GoCallSite) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, s.FileRel))
	if err != nil {
		t.Fatalf("read %s: %v", s.FileRel, err)
	}
	// The fixture has one call per function; the nearest preceding `func` names it.
	lines := strings.Split(string(b), "\n")
	for i := s.LineStart - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "func ") {
			name := strings.TrimPrefix(lines[i], "func ")
			if p := strings.IndexByte(name, '('); p > 0 {
				return name[:p]
			}
		}
	}
	return ""
}

// resolvedWith builds the Go fixture's resolved spec.
//
// Language is "go" because that is what variantspec.Resolve reads off this fixture's IR
// (discovery.IRWorkflow.Language). It is not a default and there is none: a Resolved with no language
// fails engineFor loudly rather than silently taking the Go path — see
// TestGenerate_RefusesAResolvedSpecWithNoLanguage.
func resolvedWith(overrides map[string]variantspec.ResolvedOverride) *variantspec.Resolved {
	return &variantspec.Resolved{
		ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1", Language: "go", Overrides: overrides,
	}
}

// ── 2.2: the rewrite happens, at the right place ─────────────────────────────────────────────────

func TestGenerate_ModelOverrideRewritesTheModelArgument(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("anthropic", "claude-sonnet-5")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	out := string(p.Files["pipeline.go"])
	if !strings.Contains(out, `MessageNewParams{Model: "claude-sonnet-5"}`) {
		t.Errorf("the model argument was not rewritten:\n%s", out)
	}
	if strings.Contains(out, "anthropic.ModelClaudeOpus4_6") {
		t.Error("the original model constant survived the rewrite")
	}
}

// The config-layer spec's "AST anchoring, not text matching" scenario: the discovered model literal
// also appears in a comment and in an unrelated string literal in the same file, and neither may move.
func TestGenerate_AnchorsOnTheASTNotOnText(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("anthropic", "claude-sonnet-5")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(p.Files["pipeline.go"])

	if !strings.Contains(out, "// The literal \"claude-opus-4-8\" also appears in this comment") {
		t.Error("the comment mentioning the model was rewritten; the transform matched text, not AST")
	}
	if !strings.Contains(out, `_ = "claude-opus-4-8 is mentioned here but must never be rewritten"`) {
		t.Error("an unrelated string literal was rewritten; the transform matched text, not AST")
	}
}

// A call site that never pinned a model still has one (the SDK default), so overriding it must INSERT
// the field rather than refuse.
func TestGenerate_ModelOverrideInsertsAnAbsentField(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["agent"]: {Model: modelEntry("anthropic", "claude-haiku-4-5")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(p.Files["pipeline.go"])
	if !strings.Contains(out, `MessageNewParams{Model: "claude-haiku-4-5"}`) {
		t.Errorf("the absent model field was not inserted:\n%s", out)
	}
}

// ── 2.4: behavior-preserving except the intended change ──────────────────────────────────────────

// The headline minimality property: everything outside the one rewritten expression is byte-identical
// to what was checked out. This is what byte-splicing buys that go/printer cannot.
func TestGenerate_EverythingOutsideTheCallSiteIsByteIdentical(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	before, err := os.ReadFile(filepath.Join(root, "pipeline.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("anthropic", "claude-sonnet-5")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	beforeLines := strings.Split(string(before), "\n")
	afterLines := strings.Split(string(p.Files["pipeline.go"]), "\n")
	if len(beforeLines) != len(afterLines) {
		t.Fatalf("the line count changed (%d -> %d); the rewrite reflowed the file",
			len(beforeLines), len(afterLines))
	}
	changed := 0
	for i := range beforeLines {
		if beforeLines[i] != afterLines[i] {
			changed++
			if !strings.Contains(afterLines[i], "claude-sonnet-5") {
				t.Errorf("line %d changed but is not the model argument: %q", i+1, afterLines[i])
			}
		}
	}
	if changed != 1 {
		t.Errorf("%d lines changed; a model-only override must touch exactly one", changed)
	}
}

// FR2 at the file level: overriding one node must not edit another node's call site.
func TestGenerate_DoesNotTouchUnoverriddenNodes(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("anthropic", "claude-sonnet-5")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(p.Files["pipeline.go"])
	// agent's call site had an empty params literal and must still have one.
	if !strings.Contains(out, "MessageNewParams{})") {
		t.Errorf("the un-overridden node's call site was edited:\n%s", out)
	}
	if len(p.Touched) != 1 || p.Touched[0].Dim != "model" {
		t.Errorf("Touched = %+v, want exactly one model rewrite", p.Touched)
	}
}

// A spec with no overrides is the baseline P4 compares against, not an error.
func TestGenerate_NoOverridesProducesAnEmptyPatch(t *testing.T) {
	root := newTarget(t)
	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !p.IsEmpty() || len(p.Files) != 0 {
		t.Errorf("a spec with no overrides produced a patch: %s", p.Diff)
	}
}

// ── 2.3: determinism ─────────────────────────────────────────────────────────────────────────────

// Task 2.3: same {config_hash, source_revision} -> byte-identical diff, and an identical content hash.
func TestGenerate_IsDeterministic(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	over := map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("anthropic", "claude-sonnet-5")},
		ids["agent"]:    {Model: modelEntry("anthropic", "claude-haiku-4-5")},
	}

	first, err := Generate(resolvedWith(over), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Repeat enough times that any map-iteration order dependence in edit collection would surface.
	for i := 0; i < 25; i++ {
		got, err := Generate(resolvedWith(over), root)
		if err != nil {
			t.Fatalf("Generate %d: %v", i, err)
		}
		if string(got.Diff) != string(first.Diff) {
			t.Fatalf("generation %d produced a different diff:\n--- first ---\n%s\n--- got ---\n%s",
				i, first.Diff, got.Diff)
		}
		if got.DiffHash != first.DiffHash {
			t.Fatalf("generation %d hashed to %s, want %s", i, got.DiffHash, first.DiffHash)
		}
	}
	if first.DiffHash == "" || len(first.DiffHash) != 64 {
		t.Errorf("DiffHash = %q, want 64 hex chars", first.DiffHash)
	}
}

func TestGenerate_DiffIsAReadableUnifiedDiff(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("anthropic", "claude-sonnet-5")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	d := string(p.Diff)
	for _, want := range []string{"--- a/pipeline.go", "+++ b/pipeline.go", "@@ ", "-\t", "+\t"} {
		if !strings.Contains(d, want) {
			t.Errorf("the diff is missing %q — a reviewer has to be able to read this:\n%s", want, d)
		}
	}
	// Exactly one removed and one added line: the review artifact ADR-001 promises.
	minus, plus := 0, 0
	for _, l := range strings.Split(d, "\n") {
		if strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---") {
			minus++
		}
		if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
			plus++
		}
	}
	if minus != 1 || plus != 1 {
		t.Errorf("diff has %d removed / %d added lines, want 1/1:\n%s", minus, plus, d)
	}
}

// ── 2.6 / FR5: refusals ──────────────────────────────────────────────────────────────────────────

func TestGenerate_RejectsUnknownNode(t *testing.T) {
	root := newTarget(t)
	_, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		"n_nonexistent": {Model: modelEntry("anthropic", "claude-sonnet-5")},
	}), root)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("want ErrNodeNotFound, got %v", err)
	}
	var re *RewriteError
	if !errors.As(err, &re) || re.NodeID != "n_nonexistent" {
		t.Errorf("the rejection must name the node, got: %v", err)
	}
}

// A provider swap is not a call-site argument rewrite: changing the model string on an anthropic SDK
// call does not make it an OpenAI call. Refusing loudly is what stops a diff that compiles and then
// talks to the wrong provider.
func TestGenerate_RejectsProviderSwapAsACallSiteRewrite(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	_, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("openai", "gpt-5")},
	}), root)
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("want ErrUnsafeRewrite for a provider swap, got %v", err)
	}
	var re *RewriteError
	if !errors.As(err, &re) || re.Dim != "model" {
		t.Errorf("the rejection must name the dimension, got: %v", err)
	}
	if !strings.Contains(err.Error(), "openai") || !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("the rejection should name both providers, got: %v", err)
	}
}

// The dimensions this engine will not rewrite refuse with a reason, naming node and dimension — FR5's
// "a call site the transform cannot rewrite safely", rejected before anything is applied.
//
// 🔴 `skills` USED to be listed here and is not any more: P14 task 2.1 replaced the Go refusal with real
// materialization from the skill's sealed schema (skillbind.go, decisions.md D-14.4). It has not become
// unconditional — a Go call site whose provider has no declared tool-value form, and one whose tool set
// is assembled at runtime, both still refuse — but those refusals are per (provider, call site) rather
// than per dimension, so they live with the materializer's own tests in skillbind_test.go. Leaving a
// blanket "skills always refuses" case here would have kept passing (an entry with no sealed schema
// refuses for a different reason) while asserting a contract that is no longer true.
//
// 🔴 `context` under a SELECTION policy has moved the same way: P16 task 2.2 replaced the Go refusal
// with real materialization (contextmaterialize.go), so `full` no longer refuses here — it is the
// identity policy, and a call site that writes its messages out already assembles exactly that. What
// still refuses, and what this case now pins, is a policy that assembles at RUN TIME: a summarizer's
// output is not in the source to select, so materializing it would mean writing a model's answer into a
// diff. Leaving the `full` case here would have kept asserting a contract P16 deliberately retired.
func TestGenerate_RefusesConstructionDimensionsWithAReason(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	cases := []struct {
		name     string
		override variantspec.ResolvedOverride
		wantDim  string
	}{
		{"context-host-calling", variantspec.ResolvedOverride{
			Context: &registry.ContextEntry{VersionID: strings.Repeat("x", 64), Name: "c",
				Spec: registry.ContextSpec{Policy: "summarization"}}}, "context"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
				ids["classify"]: tc.override,
			}), root)
			if !errors.Is(err, ErrUnsafeRewrite) {
				t.Fatalf("want ErrUnsafeRewrite, got %v", err)
			}
			var re *RewriteError
			if !errors.As(err, &re) {
				t.Fatalf("want a *RewriteError, got %T", err)
			}
			if re.NodeID != ids["classify"] || re.Dim != tc.wantDim {
				t.Errorf("rejection names node=%q dim=%q, want node=%q dim=%q",
					re.NodeID, re.Dim, ids["classify"], tc.wantDim)
			}
			if re.Detail == "" {
				t.Error("a refusal must say why; the user has to decide what to do about it")
			}
		})
	}
}

// Generate must not write anything: application to an isolated worktree is task 3.4's job, and the
// user's tree is never mutated in place (FR5d).
func TestGenerate_WritesNothingToTheTree(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	before, err := os.ReadFile(filepath.Join(root, "pipeline.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if _, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("anthropic", "claude-sonnet-5")},
	}), root); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(root, "pipeline.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("Generate mutated the tree it read; it must be a pure function of bytes in -> patch out")
	}
}

// Regression: two edits in one file must not collapse into one over-reporting hunk.
//
// The first cut of unifiedDiff trimmed a common prefix/suffix and called everything between them
// changed. With two rewrites five lines apart, it rendered the four untouched lines between them —
// a closing brace, a blank line, a comment, a func signature — as remove+add pairs, claiming six
// changed lines when two changed. The diff IS the product under ADR-001, so a diff that over-reports
// what it touched is a correctness bug, not a formatting one.
func TestGenerate_MultipleEditsDoNotOverReportChangedLines(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("anthropic", "claude-sonnet-5")},
		ids["agent"]:    {Model: modelEntry("anthropic", "claude-haiku-4-5")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	d := string(p.Diff)

	minus, plus := 0, 0
	for _, l := range strings.Split(d, "\n") {
		switch {
		case strings.HasPrefix(l, "---"), strings.HasPrefix(l, "+++"):
		case strings.HasPrefix(l, "-"):
			minus++
		case strings.HasPrefix(l, "+"):
			plus++
		}
	}
	if minus != 2 || plus != 2 {
		t.Errorf("two rewrites produced %d removed / %d added lines, want 2/2 — the diff is "+
			"over-reporting untouched lines:\n%s", minus, plus, d)
	}
	// The lines between the two call sites are unchanged and must appear as context.
	for _, ctx := range []string{" }", " // agent never sets a model at all"} {
		if !strings.Contains(d, ctx+"") && !strings.Contains(d, ctx) {
			t.Errorf("expected unchanged line %q to render as context:\n%s", ctx, d)
		}
	}
}

// The diff has to be a real patch, not plausible-looking text.
//
// Everything else here asserts our own rendering against our own expectations, which cannot catch an
// off-by-one in a hunk header — the counts would be wrong and every string assertion would still
// pass. `git apply` is an independent implementation of the unified-diff format, so handing it the
// artifact and requiring the result be byte-identical to the transformed bytes is the check that the
// header arithmetic is actually right. ADR-001 promises a patch a human can apply; this proves one.
func TestGenerate_DiffAppliesCleanlyWithGitApply(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required for this test: %v", err)
	}
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["classify"]: {Model: modelEntry("anthropic", "claude-sonnet-5")},
		ids["agent"]:    {Model: modelEntry("anthropic", "claude-haiku-4-5")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "base"}} {
		if out, err := run(args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	patchPath := filepath.Join(t.TempDir(), "variant.patch")
	if err := os.WriteFile(patchPath, p.Diff, 0o600); err != nil {
		t.Fatalf("write patch: %v", err)
	}
	if out, err := run("apply", "--check", patchPath); err != nil {
		t.Fatalf("git apply --check rejected the generated diff: %v\n%s\n--- diff ---\n%s", err, out, p.Diff)
	}
	if out, err := run("apply", patchPath); err != nil {
		t.Fatalf("git apply failed: %v\n%s", err, out)
	}

	// Applying the diff must reproduce the transformed bytes exactly. If it does not, the diff and
	// the Files map disagree — and the reviewer would be approving something other than what runs.
	applied, err := os.ReadFile(filepath.Join(root, "pipeline.go"))
	if err != nil {
		t.Fatalf("read applied: %v", err)
	}
	if string(applied) != string(p.Files["pipeline.go"]) {
		t.Errorf("applying the diff produced different bytes than the patch's Files:\n--- applied ---\n%s\n--- Files ---\n%s",
			applied, p.Files["pipeline.go"])
	}
}

// ── 2.2: the prompt dimension ────────────────────────────────────────────────────────────────────
//
// These are the tests the prompt rewriter's boundary is actually made of. The boundary is stated in
// rewrite.go; each case below pins one side of it, and every refusal case is paired with a rewrite
// case that would break if the refusal were simply "refuse everything".

// The headline: a prompt override rewrites the text INSIDE the message construction, and the
// construction itself survives byte-for-byte. Nothing SDK-shaped is synthesized.
func TestGenerate_PromptOverrideRewritesTheTextInsideTheMessageConstruction(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["summarize"]: {Prompt: promptEntry(t, "triage/summarize", "Summarize the ticket in one line.")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(p.Files["pipeline.go"])

	// The new text is in, the old text is gone...
	if !strings.Contains(out, `anthropic.NewTextBlock("Summarize the ticket in one line.")`) {
		t.Errorf("the prompt text was not rewritten:\n%s", out)
	}
	if strings.Contains(out, `"Summarize the ticket."`) {
		t.Error("the original prompt text survived the rewrite")
	}
	// ...and the construction around it is untouched. This is the property that makes the rewrite
	// safe on any SDK: we never had to know what a MessageParam is.
	if !strings.Contains(out, `Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(`) {
		t.Errorf("the message construction was not preserved verbatim:\n%s", out)
	}
	if len(p.Touched) != 1 || p.Touched[0].Dim != "prompt" {
		t.Errorf("Touched = %+v, want exactly one prompt rewrite", p.Touched)
	}
}

// A template WITH slots, at a call site that supplies the slot's value as a Go expression. The slot
// binds to that expression — the rewrite has to produce code, not a string with a hole in it.
func TestGenerate_PromptOverrideWithSlotsBindsThemToTheCallSitesExpressions(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	before, err := os.ReadFile(filepath.Join(root, "pipeline.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["triage"]: {Prompt: promptEntry(t, "triage/route",
			"You are a support router.\nClassify the following ticket:\n{{ticket}}\nAnswer with one word.")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(p.Files["pipeline.go"])

	// The slot became the call site's own `ticket` expression, spliced between the literal runs.
	want := `anthropic.NewTextBlock("You are a support router.\nClassify the following ticket:\n" + ticket + "\nAnswer with one word.")`
	if !strings.Contains(out, want) {
		t.Errorf("the slotted template did not render into a Go expression.\nwant substring:\n%s\ngot:\n%s", want, out)
	}
	// The runtime value is still wired in — the whole point of binding rather than rendering.
	if !strings.Contains(out, "+ ticket +") {
		t.Error("the call site's runtime `ticket` expression was dropped by the rewrite")
	}
	// No unrendered slot reached the CODE. Scoped to the rewritten call site on purpose: the fixture's
	// own comment says "{{ticket}}", and a whole-file search would match that — which would be the
	// test failing for the one reason that is actually correct behavior (comments are never rewritten).
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "anthropic.NewTextBlock(") && strings.Contains(l, "{{") {
			t.Errorf("an unrendered slot reached the source; the codemod emitted a prompt with a hole in it: %q", l)
		}
	}
	if strings.Contains(out, "Triage this ticket: ") {
		t.Error("the original prompt text survived the rewrite")
	}

	// And it is still a one-line, one-place edit (2.4).
	beforeLines := strings.Split(string(before), "\n")
	afterLines := strings.Split(out, "\n")
	if len(beforeLines) != len(afterLines) {
		t.Fatalf("line count changed (%d -> %d)", len(beforeLines), len(afterLines))
	}
	changed := 0
	for i := range beforeLines {
		if beforeLines[i] != afterLines[i] {
			changed++
		}
	}
	if changed != 1 {
		t.Errorf("%d lines changed; a prompt-only override must touch exactly one", changed)
	}
}

// The diff is the product (ADR-001). Assert the review artifact itself, not just the bytes.
func TestGenerate_PromptOverrideProducesAReadableOneLineDiff(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["triage"]: {Prompt: promptEntry(t, "triage/route", "Classify: {{ticket}}")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	d := string(p.Diff)
	minus, plus := 0, 0
	for _, l := range strings.Split(d, "\n") {
		switch {
		case strings.HasPrefix(l, "---"), strings.HasPrefix(l, "+++"):
		case strings.HasPrefix(l, "-"):
			minus++
		case strings.HasPrefix(l, "+"):
			plus++
		}
	}
	if minus != 1 || plus != 1 {
		t.Errorf("prompt diff has %d removed / %d added lines, want 1/1:\n%s", minus, plus, d)
	}
	if !strings.Contains(d, `+`) || !strings.Contains(d, `"Classify: " + ticket`) {
		t.Errorf("the diff does not show the rewritten prompt expression:\n%s", d)
	}
}

// 2.3 on the new path: the prompt rewriter must be as deterministic as the model one. It walks
// template segments and call-site operands, and if either ever came off a map this goes red.
func TestGenerate_PromptRewriteIsDeterministic(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	over := map[string]variantspec.ResolvedOverride{
		ids["summarize"]: {Prompt: promptEntry(t, "p/sum", "Summarize this, briefly.")},
		ids["triage"]: {Prompt: promptEntry(t, "p/triage",
			"Route {{ticket}} to a queue. Consider {{ticket}} carefully.")},
	}

	first, err := Generate(resolvedWith(over), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for i := 0; i < 25; i++ {
		got, err := Generate(resolvedWith(over), root)
		if err != nil {
			t.Fatalf("Generate %d: %v", i, err)
		}
		if string(got.Diff) != string(first.Diff) {
			t.Fatalf("generation %d produced a different diff:\n--- first ---\n%s\n--- got ---\n%s",
				i, first.Diff, got.Diff)
		}
		if got.DiffHash != first.DiffHash {
			t.Fatalf("generation %d hashed to %s, want %s", i, got.DiffHash, first.DiffHash)
		}
	}
	// A slot used twice binds twice — proof the segment walk follows the BODY's order, not the
	// sorted slot set.
	if n := strings.Count(string(first.Files["pipeline.go"]), "+ ticket +"); n != 2 {
		t.Errorf("the twice-used slot bound %d times, want 2:\n%s", n, first.Files["pipeline.go"])
	}
}

// FR2: a prompt override must not touch the model, and vice versa. Both dimensions on one node.
func TestGenerate_PromptAndModelOverridesOnOneNodeAreIndependent(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["summarize"]: {
			Model:  modelEntry("anthropic", "claude-haiku-4-5"),
			Prompt: promptEntry(t, "p/sum", "Be terse."),
		},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(p.Files["pipeline.go"])
	if !strings.Contains(out, `Model:    "claude-haiku-4-5"`) {
		t.Errorf("the model was not rewritten:\n%s", out)
	}
	if !strings.Contains(out, `anthropic.NewTextBlock("Be terse.")`) {
		t.Errorf("the prompt was not rewritten:\n%s", out)
	}
	if len(p.Touched) != 2 {
		t.Errorf("Touched = %+v, want one model and one prompt rewrite", p.Touched)
	}

	// Two edits on ADJACENT lines of one call site is a shape only this combination reaches, and
	// unifiedDiff renders it as `-,+,-,+` rather than grouping the removals. git apply accepts that;
	// this pins it, because "the reviewer's artifact is a real patch" is a promise ADR-001 makes and
	// our own rendering assertions structurally cannot check.
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required for this test: %v", err)
	}
	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "base"}} {
		if out, err := run(args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	patchPath := filepath.Join(t.TempDir(), "both.patch")
	if err := os.WriteFile(patchPath, p.Diff, 0o600); err != nil {
		t.Fatalf("write patch: %v", err)
	}
	if out, err := run("apply", "--check", patchPath); err != nil {
		t.Fatalf("git apply --check rejected two adjacent edits on one call site: %v\n%s\n--- diff ---\n%s",
			err, out, p.Diff)
	}
}

// ── the refusal boundary: what it must still refuse, and why ─────────────────────────────────────

func TestGenerate_PromptRefusalBoundary(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	cases := []struct {
		name string
		node string
		body string
		// wantDetail is the phrase that proves the refusal is the RIGHT one. Without it a test only
		// proves "something was refused", and every one of these would pass against a rewriter that
		// refused unconditionally — which is exactly the state this work replaced.
		wantDetail string
	}{
		// Two text literals: a prompt entry names one text and cannot say which turn it replaces.
		// Picking one would compile and quietly rewrite the wrong turn.
		{"multi-turn message list is ambiguous", "chat", "Only one question now.",
			"multi-turn"},
		// The call site feeds `ticket` into its prompt; a slotless template would drop it and still
		// return a plausible, scoreable completion. Silent eval corruption.
		{"slotless template would discard a runtime value", "triage", "Classify this ticket.",
			"silently discard"},
		// The template wants a value the call site has no expression for.
		{"slot with nothing at the call site to bind", "summarize", "Summarize {{ticket}}.",
			"no runtime value to bind"},
		// The slot's name matches no call-site operand. Binding by position instead would silently
		// put the wrong value in the hole.
		{"slot name matches no call-site expression", "triage", "Classify {{issue}}.",
			"will not\nguess"},
		// The call site never wrote a prompt argument at all.
		{"no prompt argument at the call site", "classify", "Anything.",
			"not present"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
				ids[tc.node]: {Prompt: promptEntry(t, "p/x", tc.body)},
			}), root)
			if !errors.Is(err, ErrUnsafeRewrite) {
				t.Fatalf("want ErrUnsafeRewrite, got %v", err)
			}
			var re *RewriteError
			if !errors.As(err, &re) {
				t.Fatalf("want a *RewriteError, got %T", err)
			}
			if re.NodeID != ids[tc.node] || re.Dim != "prompt" {
				t.Errorf("refusal names node=%q dim=%q, want node=%q dim=prompt", re.NodeID, re.Dim, ids[tc.node])
			}
			// Compare with whitespace collapsed: the message wraps across source lines.
			flat := strings.Join(strings.Fields(re.Detail), " ")
			want := strings.Join(strings.Fields(tc.wantDetail), " ")
			if !strings.Contains(flat, want) {
				t.Errorf("the refusal does not explain itself.\nwant substring: %q\ngot: %q", want, flat)
			}
		})
	}
}

// The minimality gate, on the new path.
//
// `wrapped`'s prompt expression spans two lines. The rewriter renders a one-line replacement, which
// would DELETE a line and shift every line below it — invalidating TouchedDimension.Line and making
// "only the targeted lines changed" uncheckable. The model rewriter could never trigger this (a
// model constant does not span lines), so this direction of the gate was untested until the prompt
// rewriter made it reachable.
//
// This test is the gate's proof of life: it is red if gateMinimal's span check is removed.
func TestGenerate_GateRejectsARewriteThatWouldShiftLines(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	_, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["wrapped"]: {Prompt: promptEntry(t, "p/w", "One line now: {{ticket}}")},
	}), root)
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("want ErrUnsafeRewrite for a line-count-changing rewrite, got %v", err)
	}
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("want a *RewriteError, got %T", err)
	}
	if re.Dim != "prompt" || re.NodeID != ids["wrapped"] {
		t.Errorf("the gate's rejection must name node and dimension, got node=%q dim=%q", re.NodeID, re.Dim)
	}
	if !strings.Contains(re.Detail, "spans") {
		t.Errorf("the rejection should say the expression spans lines, got: %q", re.Detail)
	}
}

// The control for every refusal above (and the reason this file is not just a wall of green
// assertions): the fixture's own text is byte-identical afterwards. A refused override leaves NO
// edit behind, not a partial one.
func TestGenerate_ARefusedPromptLeavesNoPartialEdit(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	before, err := os.ReadFile(filepath.Join(root, "pipeline.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// A spec whose FIRST node rewrites fine and whose second is refused: the good edit must not
	// survive the bad one. Generate is all-or-nothing.
	_, err = Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["summarize"]: {Prompt: promptEntry(t, "p/ok", "This one is fine.")},
		ids["chat"]:      {Prompt: promptEntry(t, "p/bad", "This one is ambiguous.")},
	}), root)
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("want ErrUnsafeRewrite, got %v", err)
	}
	after, err := os.ReadFile(filepath.Join(root, "pipeline.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("a refused generation mutated the tree")
	}
}

// The prompt diff has to be a real patch too — same independent check as the model path.
func TestGenerate_PromptDiffAppliesCleanlyWithGitApply(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required for this test: %v", err)
	}
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["summarize"]: {Prompt: promptEntry(t, "p/sum", "Summarize, briefly.")},
		ids["triage"]:    {Prompt: promptEntry(t, "p/triage", "Route {{ticket}} now.")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "base"}} {
		if out, err := run(args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	patchPath := filepath.Join(t.TempDir(), "prompt.patch")
	if err := os.WriteFile(patchPath, p.Diff, 0o600); err != nil {
		t.Fatalf("write patch: %v", err)
	}
	if out, err := run("apply", patchPath); err != nil {
		t.Fatalf("git apply rejected the generated prompt diff: %v\n%s\n--- diff ---\n%s", err, out, p.Diff)
	}
	applied, err := os.ReadFile(filepath.Join(root, "pipeline.go"))
	if err != nil {
		t.Fatalf("read applied: %v", err)
	}
	if string(applied) != string(p.Files["pipeline.go"]) {
		t.Errorf("applying the prompt diff produced different bytes than the patch's Files")
	}
}

// The rewritten source must still be valid Go. gateMinimal re-parses, so this passing at all is
// already evidence — but a prompt expression is the first rewrite that emits an OPERATOR, and a
// mis-joined concatenation is the way that goes wrong.
func TestGenerate_RewrittenPromptSourceStillParses(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		ids["triage"]: {Prompt: promptEntry(t, "p/t", "{{ticket}} is the ticket; classify it.")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "pipeline.go", p.Files["pipeline.go"], parser.ParseComments); err != nil {
		t.Fatalf("the rewritten source does not parse: %v\n%s", err, p.Files["pipeline.go"])
	}
	// A leading slot must not produce a stray `+` at the start of the expression.
	if !strings.Contains(string(p.Files["pipeline.go"]), `anthropic.NewTextBlock(ticket + " is the ticket; classify it.")`) {
		t.Errorf("a leading slot did not render correctly:\n%s", p.Files["pipeline.go"])
	}
}
