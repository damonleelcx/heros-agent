package transform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Tests for the language-neutral (tree-sitter) half of the engine — ADR-003 decision 1.
//
// Everything in this file goes through the REAL Generate against a REAL fixture repo, discovered with
// the REAL shipped registry. Nothing hand-builds a call site to feed a rewriter (with one labeled
// exception at the bottom, which uses the real analyzer and a real registry too). That is deliberate:
// the failure this whole change could plausibly ship is "the rewriter is fine but the span points at the
// wrong bytes", and a test that supplies its own spans cannot see it.
//
// The load-bearing assertion is always the REWRITTEN SOURCE, not "an edit was produced".

// spanTarget writes one source file into a temp dir and returns the root.
func spanTarget(t *testing.T, name, src string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return root
}

// spanSites indexes a fixture the way Generate does, and fails loudly on a fixture that discovers
// nothing — a fixture with no call sites would make every assertion below pass for the wrong reason.
func spanSites(t *testing.T, root, language string) map[string]discovery.SpanCallSite {
	t.Helper()
	sites, err := discovery.IndexSpanCallSites(root, language, nil)
	if err != nil {
		t.Fatalf("IndexSpanCallSites(%s): %v", language, err)
	}
	if len(sites) == 0 {
		t.Fatalf("discovery found no %s call sites in the fixture; the codemod has nothing to anchor on", language)
	}
	return sites
}

// onlyNode returns the fixture's single node id, asserting there is exactly one so a test can never
// silently assert against a different call site than it meant.
func onlyNode(t *testing.T, root, language string) string {
	t.Helper()
	sites := spanSites(t, root, language)
	if len(sites) != 1 {
		t.Fatalf("fixture has %d %s call sites, want exactly 1", len(sites), language)
	}
	for id := range sites {
		return id
	}
	return ""
}

func resolvedIn(language string, overrides map[string]variantspec.ResolvedOverride) *variantspec.Resolved {
	return &variantspec.Resolved{
		ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1", Language: language,
		Overrides: overrides,
	}
}

// refusalFor runs Generate expecting a refusal, and returns the message so the caller can assert WHICH
// refusal it was.
//
// 🔴 Asserting the reason, not just that it errored, is the point. Every refusal in this engine is a
// sentence a user acts on, and "it refused" is satisfied equally by refusing for the wrong reason —
// which is how a real limitation gets mistaken for a bug, or a bug for a limitation.
func refusalFor(t *testing.T, r *variantspec.Resolved, root string) string {
	t.Helper()
	_, err := Generate(r, root)
	if err == nil {
		t.Fatal("Generate succeeded; want a refusal")
	}
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("want ErrUnsafeRewrite, got %v", err)
	}
	return err.Error()
}

func mustContain(t *testing.T, got, want, why string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("the refusal does not say %s.\nwant substring: %q\ngot: %s", why, want, got)
	}
}

// ── Python ───────────────────────────────────────────────────────────────────────────────────────

const pyModelSrc = `import anthropic

client = anthropic.Anthropic()


def classify(ticket):
    return client.messages.create(
        model="claude-opus-4-6",
        max_tokens=1024,
        messages=[{"role": "user", "content": "Classify this ticket"}],
    )
`

func TestGenerate_Python_ModelOverrideRewritesTheModelArgument(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyModelSrc)
	id := onlyNode(t, root, "python")

	p, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("anthropic", "claude-sonnet-5")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	out := string(p.Files["pipeline.py"])
	if !strings.Contains(out, `model="claude-sonnet-5",`) {
		t.Errorf("the model argument was not rewritten:\n%s", out)
	}
	if strings.Contains(out, "claude-opus-4-6") {
		t.Error("the original model id survived the rewrite")
	}
	// Everything else is byte-identical: the splice model, not a re-print.
	if !strings.Contains(out, `messages=[{"role": "user", "content": "Classify this ticket"}],`) {
		t.Errorf("an untargeted line was altered:\n%s", out)
	}
	if !strings.Contains(out, "max_tokens=1024,") {
		t.Errorf("a sibling argument was altered:\n%s", out)
	}
	// The diff a human reviews.
	diff := string(p.Diff)
	if !strings.Contains(diff, `-        model="claude-opus-4-6",`) ||
		!strings.Contains(diff, `+        model="claude-sonnet-5",`) {
		t.Errorf("the diff does not show the model swap:\n%s", diff)
	}
}

const pyNoModelSrc = `import anthropic

client = anthropic.Anthropic()


def summarize(text):
    return client.messages.create(messages=[{"role": "user", "content": "Summarize"}])
`

// A call site that never pinned a model still HAS one (the SDK's default), so it is overridable — the
// same judgment the Go engine makes for an options struct with no Model field. Without this, most real
// Python call sites would be un-overridable.
func TestGenerate_Python_ModelInsertedWhenTheCallSiteNeverSetOne(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyNoModelSrc)
	id := onlyNode(t, root, "python")

	p, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("anthropic", "claude-sonnet-5")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(p.Files["pipeline.py"])
	// At the END of the argument list. Python rejects a keyword argument before a positional one, and
	// appending is valid after positionals and in an empty call.
	//
	// 🔴 CORRECTED. This comment used to end "…, after **kwargs, and in an empty call", and that clause
	// was WRONG — not imprecise, wrong, and it was the whole bug. Appending after `**kwargs` is valid
	// SYNTAX and invalid SEMANTICS: `create(**kwargs, model="x")` parses, passes py_compile, and raises
	// TypeError at runtime whenever kwargs already carries "model". The sentence was true about the
	// grammar and was being relied on as though it were true about the call, and no fixture here
	// contained a splat to contradict it. See TestGenerate_Python_ModelInsertIsRefusedUnderKwargsSplat.
	//
	// The clause is removed, not the assertion: this fixture has no splat, `create(messages=[…])` really
	// is insertable, and that behavior is unchanged and still asserted below. Deleting a false claim
	// that no test was checking is a correction; the coverage only grew.
	want := `create(messages=[{"role": "user", "content": "Summarize"}], model="claude-sonnet-5")`
	if !strings.Contains(out, want) {
		t.Errorf("the model argument was not inserted at the end of the call.\nwant: %s\ngot:  %s", want, out)
	}
	// And the result is real Python, not just plausible text.
	if err := discovery.ParseCheck("python", "pipeline.py", []byte(out)); err != nil {
		t.Errorf("the rewritten file does not parse: %v", err)
	}
}

// ── F1: an unpacking breaks the insertion's premise ──────────────────────────────────────────────
//
// The insert branch rests on "the argument is absent, therefore the SDK default applies, therefore
// inserting it is an override". Under `**kwargs` the first step is false: the key may be in the dict
// at runtime and the syntactic floor cannot prove otherwise. These fixtures are the ones that were
// missing — every previous Python fixture wrote out every argument it passed, so nothing here ever
// tested the premise itself.

// pyKwargsSplatSrc is the REAL shape, reduced from hermes-agent agent/auxiliary_client.py (node
// n_3ac4435908c48539) — including the `kwargs["model"] = …` two lines up that makes the runtime
// TypeError certain rather than hypothetical.
const pyKwargsSplatSrc = `import openai

client = openai.OpenAI()


def call_llm(refreshed_model, task, **kwargs):
    if refreshed_model and refreshed_model != kwargs.get("model"):
        kwargs["model"] = refreshed_model
    return client.chat.completions.create(**kwargs)
`

// 🔴 THE LOAD-BEARING TEST. This is the exact transform the engine really emitted against the real
// repository, recorded `built`, and showed a reviewer behind a green badge:
//
//   - client.chat.completions.create(**kwargs)
//   - client.chat.completions.create(**kwargs, model="gpt-4o-2024-11-20")
//
// py_compile passes on that. Running it raises
// `TypeError: got multiple values for keyword argument 'model'` (verified against CPython 3.14, not
// recalled). ADR-001 names "a bad codemod can break a build or subtly change behavior" as its top
// risk, and ADR-003 records that Python's gate proves syntax only — so this is precisely the failure
// with no downstream net.
//
// It must now be REFUSED, and refused for the RIGHT reason: not "no place to insert" (there is a
// perfectly good argument list) but "the model may already be coming through the splat and we cannot
// prove otherwise".
func TestGenerate_Python_ModelInsertIsRefusedUnderKwargsSplat(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyKwargsSplatSrc)
	id := onlyNode(t, root, "python")

	msg := refusalFor(t, resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-4o-2024-11-20")},
	}), root)

	mustContain(t, msg, "**kwargs", "which unpacking it cannot see into (the line a human must go read)")
	mustContain(t, msg, "may ALREADY be among them", "that the premise for inserting is what failed")
	mustContain(t, msg, "got multiple values for keyword argument", "what would actually happen at runtime")
	mustContain(t, msg, "still PARSES", "why the build gate would NOT have caught this")

	// 🚫 And it must NOT refuse with the generic sentence, which would be false about this call site and
	// would send a reader looking for an argument list that is right there.
	if strings.Contains(msg, "no argument list or options object") {
		t.Errorf("refused with the generic no-container reason, but this call site HAS an argument "+
			"list — the reason is the splat inside it.\ngot: %s", msg)
	}
}

// `*args` breaks the same premise by the positional route: `create(*args, model="x")` raises
// "got multiple values for argument 'model'" when args reaches `model` positionally (verified against
// CPython 3.14). The syntactic floor cannot see the callee's signature to know whether it can, which
// is the reason to fail closed rather than play the odds.
func TestGenerate_Python_ModelInsertIsRefusedUnderArgsSplat(t *testing.T) {
	root := spanTarget(t, "pipeline.py", `import openai

client = openai.OpenAI()


def call_llm(*args):
    return client.chat.completions.create(*args)
`)
	id := onlyNode(t, root, "python")

	msg := refusalFor(t, resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-4o-2024-11-20")},
	}), root)
	mustContain(t, msg, "*args", "which unpacking it cannot see into")
	mustContain(t, msg, "may ALREADY be among them", "that the premise for inserting is what failed")
}

// A splat does NOT block REPLACING a model the call site actually wrote, and this test exists to stop
// the fix from over-reaching into a refusal it was never entitled to.
//
// `create(**kwargs, model="old")` already raises today if kwargs carries "model" — that breakage is
// the target's own, not something we introduce — and where it does not, replacing `"old"` is exactly
// the override that was asked for. Only the INSERTION's premise breaks, so only the insertion is
// refused (pattern-violation-minimal-fix: the fix's granularity is the unit that was violated).
func TestGenerate_Python_ModelReplacementStillWorksAlongsideAKwargsSplat(t *testing.T) {
	root := spanTarget(t, "pipeline.py", `import openai

client = openai.OpenAI()


def call_llm(**kwargs):
    return client.chat.completions.create(**kwargs, model="gpt-4o-mini")
`)
	id := onlyNode(t, root, "python")

	p, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-4o-2024-11-20")},
	}), root)
	if err != nil {
		t.Fatalf("replacing a WRITTEN model argument must still work next to a splat; refusing it would "+
			"over-reach past the defect: %v", err)
	}
	want := `create(**kwargs, model="gpt-4o-2024-11-20")`
	if !strings.Contains(string(p.Files["pipeline.py"]), want) {
		t.Errorf("want the written model replaced: %s\ngot: %s", want, p.Files["pipeline.py"])
	}
}

// ── F1 horizontal check: JS/TS object spread is NOT the same bug ─────────────────────────────────
//
// The horizontal question (根因分析 #5) is whether the same root cause bites the other span languages.
// For JS/TS the answer is NO, and the reason is worth pinning rather than re-deriving: object spread
// resolves a duplicate key instead of raising, and it resolves it LAST-WINS — so an appended
// `model:` IS the override, exactly what ArgInsert's premise claims. Blocking it would refuse correct
// rewrites for a danger this language does not have.
//
// 🔴 That immunity depends entirely on appending at the END. `{ model: "x", ...opts }` is the
// opposite program — opts wins and the override silently does nothing. This test is what keeps that
// property from being refactored away, because nothing about the code's shape announces it.
func TestGenerate_JS_ModelInsertAfterObjectSpreadOverridesRatherThanRaising(t *testing.T) {
	root := spanTarget(t, "run.js", `import OpenAI from "openai";

const client = new OpenAI();

export async function run(opts) {
  return client.chat.completions.create({ ...opts });
}
`)
	id := onlyNode(t, root, "javascript")

	p, err := Generate(resolvedIn("javascript", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-4o-2024-11-20")},
	}), root)
	if err != nil {
		t.Fatalf("a JS object spread is not Python's **kwargs — last-key-wins makes the appended key the "+
			"override, so this must NOT be refused: %v", err)
	}
	// The property is ORDER, not spelling: the key must land after the spread. Asserted as order rather
	// than as an exact substring so the test tracks the thing that is load-bearing — a whitespace change
	// in the splice is harmless, and `{ model: …, ...opts }` is a silent bug.
	out := string(p.Files["run.js"])
	spread, key := strings.Index(out, "...opts"), strings.Index(out, `model: "gpt-4o-2024-11-20"`)
	if key < 0 {
		t.Fatalf("the model key was not inserted at all:\n%s", out)
	}
	if spread > key {
		t.Errorf("the model key must be appended AFTER the spread (last key wins); before it, the spread "+
			"would override US and the diff would look applied while doing nothing.\ngot: %s", out)
	}
}

// Task 2.3's contract: same {config_hash, source_revision} -> byte-identical diff. Asserted on the
// hash AND the bytes, across two independent Generate calls (each re-walks the tree and re-parses).
func TestGenerate_Python_IsDeterministic(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyModelSrc)
	id := onlyNode(t, root, "python")
	r := resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("anthropic", "claude-sonnet-5")},
	})

	first, err := Generate(r, root)
	if err != nil {
		t.Fatalf("Generate #1: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := Generate(r, root)
		if err != nil {
			t.Fatalf("Generate #%d: %v", i+2, err)
		}
		if again.DiffHash != first.DiffHash {
			t.Fatalf("diff hash differs between runs: %s vs %s", first.DiffHash, again.DiffHash)
		}
		if string(again.Diff) != string(first.Diff) {
			t.Fatalf("diff bytes differ between runs:\n%s\n---\n%s", first.Diff, again.Diff)
		}
	}
}

// 🔴 The refusal this engine exists to get right. See rewrite_span.go's header: in Python the message
// role is a bare string, so a "count the strings" rule finds TWO here and would be misleading in
// exactly the language it would have been written for.
func TestGenerate_Python_PromptRefusesAMessageList(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyModelSrc)
	id := onlyNode(t, root, "python")

	msg := refusalFor(t, resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Prompt: promptEntry(t, "triage", "Triage it")},
	}), root)

	mustContain(t, msg, `dimension "prompt"`, "which dimension it refused")
	mustContain(t, msg, "not a static string literal", "that the value is not a literal")
	mustContain(t, msg, "the message role is a bare string", "WHY a message list cannot be resolved here")
	mustContain(t, msg, "indistinguishable from prompt text", "that guessing would be a guess")
	// It must NOT blame the language or the registry — the row DOES point at `messages`.
	if strings.Contains(msg, "declares no prompt locator") {
		t.Errorf("refused for the wrong reason: the row does declare a prompt locator.\ngot: %s", msg)
	}
}

const pyFStringSrc = `from langchain_openai import ChatOpenAI

chain = ChatOpenAI()


def ask(ticket):
    return chain.invoke(input=f"Triage this ticket: {ticket}")
`

// An f-string is string-SHAPED and carries runtime data. Replacing it with a fixed string would drop
// `ticket` — a diff that parses, passes a syntax-checked gate, and silently corrupts every eval run
// under it. This is the case ArgKind exists to name.
func TestGenerate_Python_PromptRefusesAnFString(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyFStringSrc)
	id := onlyNode(t, root, "python")

	msg := refusalFor(t, resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Prompt: promptEntry(t, "triage", "Triage it")},
	}), root)

	mustContain(t, msg, `dimension "prompt"`, "which dimension it refused")
	mustContain(t, msg, "interpolated string", "that the value interpolates")
	mustContain(t, msg, "silently DROP", "what the harm would be")
	// Specifically NOT the message-list refusal: these are different facts and a user fixes them
	// differently.
	if strings.Contains(msg, "the message role is a bare string") {
		t.Errorf("an f-string was refused with the message-list reason.\ngot: %s", msg)
	}
}

const pyLangchainSrc = `from langchain_openai import ChatOpenAI

chain = ChatOpenAI()


def ask():
    return chain.invoke(input="Tell me a joke")
`

// The narrow thing that DOES work: a keyword argument whose value is one static string literal. This is
// langchain's `input:` and the Vercel AI SDK's `prompt:` — the shapes the registry rows already point
// at, and the only shape a type-free parse can stand behind.
func TestGenerate_Python_PromptRewritesAStaticStringKeyword(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyLangchainSrc)
	id := onlyNode(t, root, "python")

	p, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Prompt: promptEntry(t, "joke", "Tell me a better joke")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(p.Files["pipeline.py"])
	if !strings.Contains(out, `chain.invoke(input="Tell me a better joke")`) {
		t.Errorf("the prompt literal was not rewritten:\n%s", out)
	}
	if err := discovery.ParseCheck("python", "pipeline.py", []byte(out)); err != nil {
		t.Errorf("the rewritten file does not parse: %v", err)
	}
}

// ADR-002 holds identically on the syntactic path: a call site's SDK is not something a model string
// can change. A rule that held only in Go would make one spec mean two things.
func TestGenerate_Python_ProviderSwapIsRefused(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyModelSrc)
	id := onlyNode(t, root, "python")

	msg := refusalFor(t, resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-4o")},
	}), root)
	mustContain(t, msg, "swapping providers", "that a provider swap is what was refused")
	mustContain(t, msg, "ADR-002", "the decision it is refusing under")
}

// ── TypeScript / JavaScript ──────────────────────────────────────────────────────────────────────

const tsModelSrc = `import OpenAI from "openai";

const client = new OpenAI();

export async function classify(ticket: string) {
  return client.chat.completions.create({
    model: "gpt-4o",
    messages: [{ role: "user", content: "Classify this ticket" }],
  });
}
`

func TestGenerate_TypeScript_ModelOverrideRewritesTheModelArgument(t *testing.T) {
	root := spanTarget(t, "pipeline.ts", tsModelSrc)
	id := onlyNode(t, root, "typescript")

	p, err := Generate(resolvedIn("typescript", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-5-mini")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(p.Files["pipeline.ts"])
	if !strings.Contains(out, `model: "gpt-5-mini",`) {
		t.Errorf("the model key was not rewritten:\n%s", out)
	}
	if strings.Contains(out, `"gpt-4o"`) {
		t.Error("the original model id survived the rewrite")
	}
	if !strings.Contains(out, `messages: [{ role: "user", content: "Classify this ticket" }],`) {
		t.Errorf("an untargeted line was altered:\n%s", out)
	}
}

const jsNoModelSrc = `import OpenAI from "openai";

const client = new OpenAI();

export async function summarize() {
  return client.chat.completions.create({ messages: [{ role: "user", content: "Sum" }] });
}
`

func TestGenerate_JavaScript_ModelInsertedWhenTheCallSiteNeverSetOne(t *testing.T) {
	root := spanTarget(t, "pipeline.js", jsNoModelSrc)
	id := onlyNode(t, root, "javascript")

	p, err := Generate(resolvedIn("javascript", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-5-mini")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(p.Files["pipeline.js"])
	// `: ` is the JS object-literal spelling, supplied by the analyzer (ArgInsert.Assign) — not a table
	// on this side.
	if !strings.Contains(out, `model: "gpt-5-mini"`) {
		t.Errorf("the model key was not inserted:\n%s", out)
	}
	if err := discovery.ParseCheck("javascript", "pipeline.js", []byte(out)); err != nil {
		t.Errorf("the rewritten file does not parse: %v", err)
	}
}

func TestGenerate_TypeScript_IsDeterministic(t *testing.T) {
	root := spanTarget(t, "pipeline.ts", tsModelSrc)
	id := onlyNode(t, root, "typescript")
	r := resolvedIn("typescript", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-5-mini")},
	})
	first, err := Generate(r, root)
	if err != nil {
		t.Fatalf("Generate #1: %v", err)
	}
	again, err := Generate(r, root)
	if err != nil {
		t.Fatalf("Generate #2: %v", err)
	}
	if first.DiffHash != again.DiffHash || string(first.Diff) != string(again.Diff) {
		t.Fatalf("diff is not byte-identical across runs")
	}
}

func TestGenerate_TypeScript_PromptRefusesAMessageList(t *testing.T) {
	root := spanTarget(t, "pipeline.ts", tsModelSrc)
	id := onlyNode(t, root, "typescript")

	msg := refusalFor(t, resolvedIn("typescript", map[string]variantspec.ResolvedOverride{
		id: {Prompt: promptEntry(t, "triage", "Triage it")},
	}), root)
	mustContain(t, msg, "not a static string literal", "that the value is not a literal")
	mustContain(t, msg, "the message role is a bare string", "why a TS message list cannot be resolved")
	mustContain(t, msg, "TypeScript", "which language it is talking about")
}

const jsTemplateSrc = `import { generateText } from "ai";

export async function ask(ticket) {
  return generateText({ prompt: ` + "`Triage this ticket: ${ticket}`" + ` });
}
`

// A JS template literal WITH a substitution is the same hazard as an f-string, and the analyzer marks
// it interpolated. (A substitution-free template literal is a different case: it is replaceable, and it
// has no Text — which is exactly why ArgKind and ArgValue.Text are independent.)
func TestGenerate_JavaScript_PromptRefusesATemplateLiteralWithASubstitution(t *testing.T) {
	root := spanTarget(t, "pipeline.js", jsTemplateSrc)
	id := onlyNode(t, root, "javascript")

	msg := refusalFor(t, resolvedIn("javascript", map[string]variantspec.ResolvedOverride{
		id: {Prompt: promptEntry(t, "triage", "Triage it")},
	}), root)
	mustContain(t, msg, "interpolated string", "that the value interpolates")
	mustContain(t, msg, "silently DROP", "what the harm would be")
}

const jsVercelSrc = `import { generateText } from "ai";

export async function ask() {
  return generateText({ prompt: "Tell me a joke" });
}
`

func TestGenerate_JavaScript_PromptRewritesAStaticStringKey(t *testing.T) {
	root := spanTarget(t, "pipeline.js", jsVercelSrc)
	id := onlyNode(t, root, "javascript")

	p, err := Generate(resolvedIn("javascript", map[string]variantspec.ResolvedOverride{
		id: {Prompt: promptEntry(t, "joke", "Tell me a better joke")},
	}), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := string(p.Files["pipeline.js"])
	if !strings.Contains(out, `generateText({ prompt: "Tell me a better joke" })`) {
		t.Errorf("the prompt was not rewritten:\n%s", out)
	}
}

// The Vercel row points at no model, because `generateText({model: openai("gpt-4o")})` takes a model
// OBJECT, not a string. The refusal must say so at the ROW level and must NOT imply JavaScript is the
// problem — JS object keys are perfectly rewritable, as the test above shows.
func TestGenerate_JavaScript_ModelRefusedWhenTheRowNamesNoModelArgument(t *testing.T) {
	root := spanTarget(t, "pipeline.js", jsVercelSrc)
	id := onlyNode(t, root, "javascript")

	msg := refusalFor(t, resolvedIn("javascript", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-5-mini")},
	}), root)
	mustContain(t, msg, "declares no model locator", "that the ROW has no model locator")
	if strings.Contains(msg, "has no named-argument form") {
		t.Errorf("JavaScript was blamed for having no named arguments, which is false.\ngot: %s", msg)
	}
}

// ── Java / Rust / Kotlin: the honest refusals ────────────────────────────────────────────────────

const javaSrc = `package com.example;

import dev.langchain4j.model.chat.ChatLanguageModel;

public class Triage {
    private final ChatLanguageModel model;

    public Triage(ChatLanguageModel model) { this.model = model; }

    public String classify(String ticket) {
        return model.generate("Classify this ticket");
    }
}
`

// 🔴 Java's call sites are DISCOVERED — Discovery finds this one and puts it in the IR — and are not
// rewritable, for a reason that is a fact about Java and about the registry rows, not a gap in this
// engine. The refusal must say which, because "unsupported" would send the reader nowhere.
// 🔴 Java's refusal, retargeted the same way Kotlin's was. The old assertions ("Java has no
// named-argument form", "no Java row declares an arg_map") were true sentences about the wrong fact:
// langchain4j binds the model on a BUILDER, which P13 FR52 now locates. What must still be true is that
// a file which does not WRITE that builder is refused with a sentence about the source.
func TestGenerate_Java_UnwrittenBindingRefusesAboutTheSource(t *testing.T) {
	root := spanTarget(t, "Triage.java", javaSrc)
	id := onlyNode(t, root, "java")

	msg := refusalFor(t, resolvedIn("java", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-5-mini")},
	}), root)

	mustContain(t, msg, "binds model at a builder-chain call", "the SDK's binding STYLE")
	mustContain(t, msg, "this file does not write one", "that the SOURCE is the limit")
	// Not a generic brush-off, and not a language claim.
	if strings.Contains(strings.ToLower(msg), "unsupported") {
		t.Errorf("Java was refused as 'unsupported', which is false — Discovery finds this call site.\ngot: %s", msg)
	}
	if strings.Contains(msg, "Java has no named-argument form") {
		t.Errorf("Java was blamed for lacking named arguments, which is no longer the operative fact — its "+
			"SDK binds on a builder and that binding IS rewritable.\ngot: %s", msg)
	}
}

// 🔴 Wave 13d turned all three of these around, and the turn is the record of the boundary moving.
//
// They used to assert that Java, Kotlin and Rust REFUSE, each for its own honest reason — Java and Rust
// for having no named-argument form, Kotlin for having no row with an arg_map. Every one of those
// sentences was true and none of them was the operative fact. The operative fact was that the engine
// only knew how to point at an ARGUMENT, while these SDKs bind the model on a BUILDER (langchain4j,
// Spring AI) or in a REQUEST VALUE (async-openai). P13 FR52 generalized the locator to a binding site,
// and all three became rewritable without a single new rewriter.
//
// What they assert now is the pair that matters: a file that WRITES the binding is materialized, and a
// file that does not is refused with a sentence about the SOURCE — never about the language.

const javaBoundSrc = `package com.example;

import dev.langchain4j.model.openai.OpenAiChatModel;

public class Triage {
    private final OpenAiChatModel model = OpenAiChatModel.builder()
        .apiKey(System.getenv("OPENAI_API_KEY"))
        .modelName("gpt-4o")
        .build();

    public String classify(String ticket) {
        return model.generate("Classify this ticket");
    }
}
`

func TestGenerate_Java_BuilderBoundModelMaterializes(t *testing.T) {
	root := spanTarget(t, "Triage.java", javaBoundSrc)
	id := onlyNode(t, root, "java")

	p, err := Generate(resolvedIn("java", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-5-mini")},
	}), root)
	if err != nil {
		t.Fatalf("a builder-bound Java model must materialize (P13 FR52): %v", err)
	}
	after := string(p.Files["Triage.java"])
	if !strings.Contains(after, `.modelName("gpt-5-mini")`) {
		t.Errorf("the builder call was not rewritten:\n%s", after)
	}
	if strings.Contains(after, `"gpt-4o"`) {
		t.Errorf("the previous model is still bound:\n%s", after)
	}
	// 🔴 Only the binding moved: the API-key line is a sibling builder call and must be untouched.
	if !strings.Contains(after, `.apiKey(System.getenv("OPENAI_API_KEY"))`) {
		t.Errorf("a sibling builder call was disturbed:\n%s", after)
	}
}

const kotlinBoundSrc = `package com.example

import dev.langchain4j.model.openai.OpenAiChatModel

class Triage {
    private val model = OpenAiChatModel.builder()
        .modelName("gpt-4o")
        .build()

    fun classify(ticket: String): String {
        return model.generate("Classify this ticket")
    }
}
`

func TestGenerate_Kotlin_BuilderBoundModelMaterializes(t *testing.T) {
	root := spanTarget(t, "Triage.kt", kotlinBoundSrc)
	id := onlyNode(t, root, "kotlin")

	p, err := Generate(resolvedIn("kotlin", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-5-mini")},
	}), root)
	if err != nil {
		t.Fatalf("a builder-bound Kotlin model must materialize (P13 FR52): %v", err)
	}
	if !strings.Contains(string(p.Files["Triage.kt"]), `.modelName("gpt-5-mini")`) {
		t.Errorf("the builder call was not rewritten:\n%s", p.Files["Triage.kt"])
	}
}

const rustBoundSrc = `use async_openai::Client;
use async_openai::types::CreateChatCompletionRequestArgs;

pub async fn classify(client: &Client, ticket: &str) -> String {
    let request = CreateChatCompletionRequestArgs::default()
        .model("gpt-4o")
        .build()
        .unwrap();
    let response = client.chat().create(request).await.unwrap();
    response.to_string()
}
`

func TestGenerate_Rust_RequestFieldBoundModelMaterializes(t *testing.T) {
	root := spanTarget(t, "lib.rs", rustBoundSrc)
	sites := spanSites(t, root, "rust")
	var id string
	for k := range sites {
		id = k
	}

	p, err := Generate(resolvedIn("rust", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-5-mini")},
	}), root)
	if err != nil {
		t.Fatalf("a request-field-bound Rust model must materialize (P13 FR52): %v", err)
	}
	if !strings.Contains(string(p.Files["lib.rs"]), `.model("gpt-5-mini")`) {
		t.Errorf("the request field was not rewritten:\n%s", p.Files["lib.rs"])
	}
}

const kotlinSrc = `package com.example

import dev.langchain4j.model.chat.ChatLanguageModel

class Triage(private val model: ChatLanguageModel) {
    fun classify(ticket: String): String {
        return model.generate("Classify this ticket")
    }
}
`

// 🔴 P13 18.7 — a call site whose SDK binds nowhere THIS FILE writes refuses with a sentence about the
// SOURCE. The model here is injected, so the builder ran in some other file; there is no expression to
// replace, and that is a fact the author can act on. 🚫 It must not be reported as a language gap: the
// language is rewritable, as the three tests above prove.
func TestGenerate_Kotlin_UnwrittenBindingRefusesAboutTheSource(t *testing.T) {
	root := spanTarget(t, "Triage.kt", kotlinSrc)
	id := onlyNode(t, root, "kotlin")

	msg := refusalFor(t, resolvedIn("kotlin", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("anthropic", "claude-sonnet-5")},
	}), root)

	mustContain(t, msg, "binds model at a builder-chain call", "the SDK's binding STYLE")
	mustContain(t, msg, "this file does not write one", "that the SOURCE, not the platform, is the limit")
	mustContain(t, msg, "Bind it once in the same file", "a route the author can actually take")
	// 🚫 None of the old language-blaming sentences may come back.
	for _, wrong := range []string{
		"Kotlin has no named-argument form",
		"no materializer for this language",
		"no Kotlin row in the signature registry declares an arg_map",
	} {
		if strings.Contains(msg, wrong) {
			t.Errorf("the refusal blames the language or the registry for a source fact (%q):\n%s", wrong, msg)
		}
	}
}

// 🔴 P13 §14 Q11 — a SHARED builder refuses by name. One builder feeding two call sites has one model,
// so a per-node override would silently change the sibling node too: a false measurement wearing a
// change's clothes. The refusal quotes the count so the author can find both.
func TestGenerate_Kotlin_SharedBuilderRefusesNamingTheSharing(t *testing.T) {
	root := spanTarget(t, "Triage.kt", `package com.example

import dev.langchain4j.model.openai.OpenAiChatModel

class Triage {
    private val fast = OpenAiChatModel.builder().modelName("gpt-4o-mini").build()
    private val slow = OpenAiChatModel.builder().modelName("gpt-4o").build()

    fun classify(ticket: String): String {
        return slow.generate("Classify this ticket")
    }
}
`)
	id := onlyNode(t, root, "kotlin")

	msg := refusalFor(t, resolvedIn("kotlin", map[string]variantspec.ResolvedOverride{
		id: {Model: modelEntry("openai", "gpt-5-mini")},
	}), root)
	mustContain(t, msg, "SHARED", "that the builder feeds more than one node")
	mustContain(t, msg, "2 times", "how many bindings there are, so the author can find them")
	mustContain(t, msg, "false measurement", "why guessing one would be worse than refusing")
}

// ── skills still refuse everywhere; context now depends on the language ──────────────────────────

// 🔴 This test has now been turned around TWICE, and both turns are the record of a boundary moving.
// It first asserted that CONTEXT refuses in Python (Python's selection materializer landed, so that went);
// it then asserted that SKILLS refuse in Python (wave 14d's spelling rows landed, so that goes too).
// What it pins now is the refusal that survives both: a skill whose SEALED CONTRACT carries no argument
// shape refuses in EVERY language, because an empty property bag is a valid tool that accepts nothing —
// it parses, and then fails every call the model makes against it.
func TestGenerate_Python_SkillWithNoSealedShapeRefuses(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyModelSrc)
	id := onlyNode(t, root, "python")

	msg := refusalFor(t, resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Skills: []*registry.SkillEntry{{VersionID: strings.Repeat("s", 64), Name: "search_kb"}}},
	}), root)
	mustContain(t, msg, "search_kb", "the skill it refused")
	mustContain(t, msg, "sealed input schema", "that the CONTRACT is what is missing")
	// 🚫 And it must not blame the language: this refusal is identical in Go.
	if strings.Contains(msg, "no declared tool-value spelling") {
		t.Errorf("a contract-shaped refusal was reported as a coverage gap:\n%s", msg)
	}
}

// A context policy that assembles at RUN TIME refuses in Python for the POLICY's reason, and says so
// without promising a rewriter — no rewriter will ever write a model's answer into source.
func TestGenerate_Python_RunTimeContextPolicyRefusesOnThePolicy(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyModelSrc)
	id := onlyNode(t, root, "python")

	msg := refusalFor(t, resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {Context: &registry.ContextEntry{VersionID: strings.Repeat("x", 64), Name: "c",
			Spec: registry.ContextSpec{Policy: "rag-retrieval"}}},
	}), root)
	mustContain(t, msg, "RETRIEVING chunks", "the policy's own reason")
	mustContain(t, msg, "host-side where it belongs", "that the capability is not lost")
	if strings.Contains(msg, "materializer is still being built") {
		t.Errorf("a run-time policy was told to wait for a language rewriter that would refuse it too.\ngot: %s", msg)
	}
}

// ── the dispatch table itself ────────────────────────────────────────────────────────────────────

// 🔴 The table must cover every frontend Discovery ships. A language Discovery finds call sites in and
// the engine has never heard of is the ADR-003 gap re-opening: the IR would name nodes the apply path
// rejects as "no engine", with nothing to explain why.
func TestEnginesCoverEveryFrontend(t *testing.T) {
	for _, fe := range discovery.DefaultFrontends() {
		if _, err := engineFor(fe.Language()); err != nil {
			t.Errorf("discovery ships a %s frontend but the transform engine has no row for it: %v",
				fe.Language(), err)
		}
	}
	// And the other direction: an engine for a language nothing discovers would be dead code claiming
	// a capability.
	known := map[string]bool{}
	for _, fe := range discovery.DefaultFrontends() {
		known[fe.Language()] = true
	}
	for _, l := range RewritableLanguages() {
		if !known[l] {
			t.Errorf("the transform engine has a %q row but no frontend discovers that language", l)
		}
	}
}

// A Resolved that never went through variantspec.Resolve has no language. It must fail LOUDLY rather
// than default to Go — a Go rewriter on a Python tree would not crash, it would find no call sites and
// report "your node does not exist at source_revision", which is a lie with a plausible sentence.
func TestGenerate_RefusesAResolvedSpecWithNoLanguage(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyModelSrc)
	_, err := Generate(&variantspec.Resolved{
		ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1",
		Overrides: map[string]variantspec.ResolvedOverride{},
	}, root)
	if !errors.Is(err, ErrLanguageNotRewritable) {
		t.Fatalf("want ErrLanguageNotRewritable for a spec with no language, got %v", err)
	}
	mustContain(t, err.Error(), "variantspec.Resolve", "where the language was supposed to come from")
}

// A polyglot repository gets workflow.language == "mixed", and this engine rewrites one language per
// patch because the single verifier behind it can only honestly gate one. That refusal is a real
// product boundary and must be loud, not a silent pick-the-first-language.
func TestGenerate_RefusesAMixedLanguageWorkflow(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyModelSrc)
	_, err := Generate(resolvedIn("mixed", map[string]variantspec.ResolvedOverride{}), root)
	if !errors.Is(err, ErrLanguageNotRewritable) {
		t.Fatalf("want ErrLanguageNotRewritable for a mixed-language workflow, got %v", err)
	}
}

// ── The Kotlin rewriter, proved against the real analyzer ────────────────────────────────────────

// 🔴 What this test proves and — just as important — what it does NOT.
//
// It PROVES the span rewriter handles Kotlin correctly: real Kotlin source, parsed by the real
// kotlinAnalyzer, indexed by the real IndexSpanCallSites, spans sliced out of the real bytes, and the
// result re-parsed as real Kotlin. If a Kotlin registry row (or an llm-eval.yaml declared entrypoint)
// ever names a keyword argument, THIS is what will run, and it works.
//
// It does NOT prove Kotlin is applicable today, and nothing here should be read as claiming it is. The
// registry it uses is a fixture, written by this test, because the SHIPPED registry has no Kotlin row
// with an arg_map — see TestGenerate_Kotlin_RefusalBlamesTheRegistryNotTheLanguage, which is the truth
// about the product. The two tests are a matched pair on purpose: one says the engine is ready, the
// other says the data is not, and neither is allowed to imply the other.
//
// A rewriter existing ≠ a language being genuinely applicable. That distinction is why this comment is
// longer than the test.
const kotlinNamedArgSrc = `package com.example

import ai.example.chat.ChatClient

class Triage(private val client: ChatClient) {
    fun classify(ticket: String): String {
        return client.messages.create(model = "opus-4-6", prompt = "Classify it")
    }
}
`

const kotlinFixtureRegistry = `
version: "1.0.0"
default_language: go
rows:
  - id: kt.example.messages.create
    language: kotlin
    import_path: ai.example.chat
    symbol_kind: client_method
    selector: messages.create
    provider_hint: anthropic
    arg_map:
      model: { name: "model" }
      prompt: { name: "prompt" }
`

func TestSpanRewriter_KotlinNamedArgumentIsRewritable(t *testing.T) {
	root := spanTarget(t, "Triage.kt", kotlinNamedArgSrc)
	reg, err := discovery.LoadRegistry([]byte(kotlinFixtureRegistry))
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	sites, err := discovery.IndexSpanCallSites(root, "kotlin", reg)
	if err != nil {
		t.Fatalf("IndexSpanCallSites: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("indexed %d Kotlin call sites, want 1 — the fixture must actually be discovered", len(sites))
	}
	var site discovery.SpanCallSite
	for _, s := range sites {
		site = s
	}
	src := []byte(kotlinNamedArgSrc)

	edits, err := spanRewriteModel(site, src, variantspec.ResolvedOverride{
		Model: modelEntry("anthropic", "sonnet-5"),
	})
	if err != nil {
		t.Fatalf("spanRewriteModel: %v", err)
	}
	out, err := applyEdits(src, edits)
	if err != nil {
		t.Fatalf("applyEdits: %v", err)
	}
	if !strings.Contains(string(out), `model = "sonnet-5"`) {
		t.Errorf("the Kotlin named argument was not rewritten:\n%s", out)
	}
	if strings.Contains(string(out), "opus-4-6") {
		t.Error("the original model id survived the rewrite")
	}
	if err := discovery.ParseCheck("kotlin", "Triage.kt", out); err != nil {
		t.Errorf("the rewritten Kotlin does not parse: %v", err)
	}
}

// 🔴 `$` in a Kotlin string is a STRING TEMPLATE, and this is the bug that made a per-language speller
// necessary rather than reusing strconv.Quote. Spliced unescaped, `"you owe $total"` is a compile error
// when `total` is not in scope and — far worse — a silent interpolation of the wrong value when it is.
// A prompt body is arbitrary user text, so this is reachable with no warning.
func TestSpellString_KotlinEscapesTheTemplateDollar(t *testing.T) {
	got, err := spellString("kotlin", "you owe $total dollars")
	if err != nil {
		t.Fatalf("spellString: %v", err)
	}
	if want := `"you owe \$total dollars"`; got != want {
		t.Errorf("Kotlin's `$` was not escaped.\nwant: %s\ngot:  %s", want, got)
	}
	// And it is NOT escaped where `$` is an ordinary character — an escape that is wrong everywhere
	// else is not a fix, it is a different bug.
	for _, lang := range []string{"python", "javascript", "typescript"} {
		got, err := spellString(lang, "you owe $total dollars")
		if err != nil {
			t.Fatalf("spellString(%s): %v", lang, err)
		}
		if want := `"you owe $total dollars"`; got != want {
			t.Errorf("%s: `$` should be ordinary.\nwant: %s\ngot:  %s", lang, want, got)
		}
	}
}

// A control character's escape spelling differs per language (`\x00` is valid Python and JS and does
// not exist in Kotlin), so the speller refuses rather than improvising one.
func TestSpellString_RefusesAControlCharacterRatherThanGuess(t *testing.T) {
	if _, err := spellString("kotlin", "a\x00b"); err == nil {
		t.Fatal("spellString accepted a NUL; want a refusal naming it")
	} else if !strings.Contains(err.Error(), "U+0000") {
		t.Errorf("the refusal does not name the offending character: %v", err)
	}
	// A newline IS spellable, in every one of these languages, and must not be refused: it is escaped,
	// which also keeps gateMinimal's "no rewriter may emit a newline" rule satisfied by construction.
	got, err := spellString("python", "line one\nline two")
	if err != nil {
		t.Fatalf("spellString refused a newline it can spell: %v", err)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("the literal contains a RAW newline, which would shift every line below it: %q", got)
	}
	if want := `"line one\nline two"`; got != want {
		t.Errorf("want %s, got %s", want, got)
	}
}

// ── gateMinimal's re-parse half, for a non-Go language ───────────────────────────────────────────

// The Go gate re-parses with go/parser. The tree-sitter equivalent cannot be "tree-sitter parsed it",
// because tree-sitter ALWAYS parses — it recovers and returns a tree with ERROR nodes. So the real
// equivalent is "parsed with no ERROR/MISSING nodes", and this asserts it actually rejects.
func TestReparseSyntactic_RejectsASpliceThatBrokeTheFile(t *testing.T) {
	good := []byte("def f():\n    return g(model=\"a\")\n")
	if err := reparseSyntactic("python", "f.py", good, good); err != nil {
		t.Fatalf("reparseSyntactic rejected valid Python: %v", err)
	}
	broken := []byte("def f():\n    return g(model=\"a\"\n")
	err := reparseSyntactic("python", "f.py", good, broken)
	if err == nil {
		t.Fatal("reparseSyntactic accepted Python that does not parse; the gate cannot go red")
	}
	if !strings.Contains(err.Error(), "no longer parses as python") {
		t.Errorf("the gate blamed the wrong thing: %v", err)
	}
}

// 🔴 The asymmetry with Go. tree-sitter frontends analyze files go/parser would have refused outright,
// so a file that was ALREADY broken still reaches this gate — and blaming the rewrite for damage it did
// not do sends a user hunting a codemod bug that does not exist. The two cases are named apart.
func TestReparseSyntactic_BlamesTheOriginalWhenTheOriginalWasAlreadyBroken(t *testing.T) {
	alreadyBroken := []byte("def f():\n    return g(model=\"a\"\n")
	err := reparseSyntactic("python", "f.py", alreadyBroken, alreadyBroken)
	if err == nil {
		t.Fatal("reparseSyntactic accepted a file the parser only RECOVERED; it must refuse to codemod one")
	}
	if !strings.Contains(err.Error(), "already has syntax errors") {
		t.Errorf("want the original blamed, got: %v", err)
	}
	if strings.Contains(err.Error(), "no longer parses") {
		t.Errorf("the rewrite was blamed for damage that was already there: %v", err)
	}
}
