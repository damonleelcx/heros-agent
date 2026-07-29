package discovery

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── P14 11.11 / 11.12 🔴 — the frontend RECORDS the split; the pruner never infers it ────────────

// Every registered language with a list splitter records a node's tools with the location of each
// declaration. That recording is the whole of what tool pruning was blocked on — deleting a written
// element needs no rewriter work — so this is the assertion that turns the coverage cell into a fact.
func TestEveryFrontendRecordsTheToolSplit(t *testing.T) {
	cases := []struct {
		lang, file, src, wantFirst string
	}{
		{"python", "pipeline.py", `import anthropic

client = anthropic.Anthropic()


def chat():
    return client.messages.create(
        model="claude-opus-4-6",
        messages=[{"role": "user", "content": "hi"}],
        tools=[{"name": "search"}, {"name": "weather"}],
    )
`, `{"name": "search"}`},
		{"typescript", "pipeline.ts", `import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic();

export async function chat() {
  return client.messages.create({
    model: "claude-opus-4-6",
    messages: [{ role: "user", content: "hi" }],
    tools: [{ name: "search" }, { name: "weather" }],
  });
}
`, `{ name: "search" }`},
	}

	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			root := writeRepo(t, tc.file, tc.src)
			nodes := discoverNodes(t, root, tc.lang)
			if len(nodes) != 1 {
				t.Fatalf("want exactly one node, got %d", len(nodes))
			}
			n := nodes[0]
			if len(n.Tools) != 2 {
				t.Fatalf("want two recorded tools, got %d (%v)", len(n.Tools), toolNames(n.Tools))
			}
			if n.Tools[0].Name != tc.wantFirst {
				t.Errorf("first tool recorded as %q, want %q", n.Tools[0].Name, tc.wantFirst)
			}
			for _, tool := range n.Tools {
				if !tool.Locatable() {
					t.Errorf("tool %q was recorded WITHOUT a declaration location; a prune over it would have "+
						"nothing to point at", tool.Name)
					continue
				}
				if tool.DeclaredAt.Line <= 0 {
					t.Errorf("tool %q has a non-positive declaration line", tool.Name)
				}
			}
			// 🔴 The frozen field is untouched: ToolsSkills' emptiness is part of the bytes a pre-P14 IR
			// reproduces, and the split fields sit beside it rather than replacing it.
			if len(n.ToolsSkills) != 0 {
				t.Errorf("the frozen ToolsSkills slice was repurposed: %v", n.ToolsSkills)
			}
		})
	}
}

// 🚫 A tool set assembled at run time is recorded as ONE entry with NO location — explicitly, not by
// omission. "This node offers tools we cannot address" and "this node offers no tools" are different
// facts, and only the first may make a prune refuse; omitting it would make a prune report "no such
// tool" about a set that is plainly right there in the source.
func TestUnlocatableToolIsRecordedNotOmitted(t *testing.T) {
	root := writeRepo(t, "pipeline.py", `import anthropic

client = anthropic.Anthropic()


def chat(ctx):
    return client.messages.create(
        model="claude-opus-4-6",
        messages=[{"role": "user", "content": "hi"}],
        tools=build_tools(ctx),
    )
`)
	nodes := discoverNodes(t, root, "python")
	if len(nodes) != 1 {
		t.Fatalf("want exactly one node, got %d", len(nodes))
	}
	tools := nodes[0].Tools
	if len(tools) != 1 {
		t.Fatalf("a run-time-assembled tool set must be recorded as one entry, got %d (%v)",
			len(tools), toolNames(tools))
	}
	if tools[0].Locatable() {
		t.Errorf("a run-time-assembled set was recorded WITH a location; a prune would then delete a span "+
			"nobody wrote: %+v", tools[0])
	}
	if !strings.Contains(tools[0].Name, "build_tools") {
		t.Errorf("the recorded entry does not name what the call site wrote, so a refusal cannot quote it: %q",
			tools[0].Name)
	}
}

// A list the splitter cannot prove the boundaries of — a spread — is recorded as unlocatable WHOLE,
// never as a half-split list. Recording half a list is the shape that deletes the wrong element.
func TestUnprovableListIsRecordedWholeAndUnlocatable(t *testing.T) {
	root := writeRepo(t, "pipeline.py", `import anthropic

client = anthropic.Anthropic()


def chat(extra):
    return client.messages.create(
        model="claude-opus-4-6",
        messages=[{"role": "user", "content": "hi"}],
        tools=[{"name": "search"}, *extra],
    )
`)
	nodes := discoverNodes(t, root, "python")
	if len(nodes) != 1 {
		t.Fatalf("want exactly one node, got %d", len(nodes))
	}
	tools := nodes[0].Tools
	if len(tools) != 1 || tools[0].Locatable() {
		t.Fatalf("a list carrying a spread must be recorded as ONE unlocatable entry, got %+v", tools)
	}
}

// ── the shared splitter itself ──────────────────────────────────────────────────────────────────

func TestSplitWrittenListAcrossLanguages(t *testing.T) {
	cases := []struct {
		lang, text string
		want       []string
	}{
		{"python", `[{"a": 1}, b]`, []string{`{"a": 1}`, "b"}},
		{"typescript", `[{ a: 1 }, b]`, []string{"{ a: 1 }", "b"}},
		{"javascript", `[a, b, c]`, []string{"a", "b", "c"}},
		{"kotlin", `listOf(a, b)`, []string{"a", "b"}},
		{"java", `List.of(a, b)`, []string{"a", "b"}},
		{"rust", `vec![a, b]`, []string{"a", "b"}},
		// A comma inside a string is not a separator.
		{"python", `["a, b", c]`, []string{`"a, b"`, "c"}},
	}
	for _, tc := range cases {
		got, err := SplitWrittenList(tc.lang, tc.text)
		if err != nil {
			t.Errorf("%s %q: %v", tc.lang, tc.text, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s %q: got %d elements, want %d", tc.lang, tc.text, len(got), len(tc.want))
			continue
		}
		for i := range got {
			if got[i].Text != tc.want[i] {
				t.Errorf("%s %q: element %d = %q, want %q", tc.lang, tc.text, i, got[i].Text, tc.want[i])
			}
			if tc.text[got[i].Start:got[i].End] != got[i].Text {
				t.Errorf("%s %q: element %d's span does not cover its text", tc.lang, tc.text, i)
			}
		}
	}
}

// 🔴 What the splitter REFUSES, in every language that has one. Each of these is a boundary the engine
// cannot prove, and a best-effort split of any of them is the shape that deletes the wrong element.
func TestSplitWrittenListRefusesUnprovableBoundaries(t *testing.T) {
	bad := map[string][]string{
		"python":     {`[a, *rest]`, "[a, # note\nb]", `[a, """x` + "\n" + `y"""]`, `[a, {b: 1]`, `[a, "x]`},
		"typescript": {`[a, ...rest]`, "[a, // note\nb]", `[a, /* note */ b]`, `[a, {b: 1]`},
		"kotlin":     {`listOf(a, *rest)`, "listOf(a, // note\nb)"},
		"rust":       {`vec![a, // note` + "\n" + `b]`},
	}
	for lang, texts := range bad {
		for _, text := range texts {
			if _, err := SplitWrittenList(lang, text); err == nil {
				t.Errorf("%s: SplitWrittenList(%q) succeeded; a boundary this engine cannot prove must refuse",
					lang, text)
			}
		}
	}
}

// A language with no splitter refuses by name rather than returning an empty split, which would read as
// "this list has no elements" — the silent answer that makes a prune delete nothing while claiming to.
func TestSplitWrittenListNamesTheMissingSplitter(t *testing.T) {
	_, err := SplitWrittenList("elixir", "[a, b]")
	if err == nil {
		t.Fatal("a language with no splitter must refuse")
	}
	if !strings.Contains(err.Error(), "splitter") {
		t.Errorf("the refusal must name the missing artifact, got %v", err)
	}
	if CanSplitWrittenList("elixir") || RecordsToolSplit("elixir") {
		t.Error("an unregistered language must not claim a splitter or a tool split")
	}
	// Go records the split from go/ast rather than from this table, and must still report true.
	if !RecordsToolSplit("go") {
		t.Error("Go records the tool split from go/ast; the read must say so")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────────────────────────

func writeRepo(t *testing.T, name, src string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func discoverNodes(t *testing.T, root, language string) []ExtractedNode {
	t.Helper()
	fe, err := syntacticFrontendFor(language)
	if err != nil {
		t.Fatalf("no syntactic frontend for %q: %v", language, err)
	}
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	res, err := fe.Discover(root, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Nodes
}

func toolNames(tools []IRTool) []string {
	out := make([]string, 0, len(tools))
	for _, x := range tools {
		out = append(out, x.Name)
	}
	return out
}

// ── P13 18.5 🔴 — the binding-site forms are ADDITIVE ────────────────────────────────────────────

// A row that declares a builder-chain or request-field binding site parses, and — the load-bearing half
// — every EXISTING row parses byte-identically to before the forms existed. The generalization that
// makes Kotlin/Java/Rust reachable must not move a single Go or Python locator.
func TestBindingFormIsAdditiveToExistingRows(t *testing.T) {
	reg, err := LoadRegistry([]byte(`
version: v1
rows:
  - { id: kt.example.generate, language: kotlin, import_path: dev.langchain4j, symbol_kind: client_method, selector: generate, provider_hint: openai, arg_map: { model: { builder: modelName } } }
  - { id: rs.example.create, language: rust, import_path: async-openai, symbol_kind: client_method, selector: create, provider_hint: openai, arg_map: { model: { request_field: model } } }
  - { id: py.example.create, language: python, import_path: anthropic, symbol_kind: client_method, selector: messages.create, provider_hint: anthropic, arg_map: { model: { name: model } } }
`))
	if err != nil {
		t.Fatalf("a registry declaring the new binding forms must load: %v", err)
	}
	want := map[string]LocatorForm{
		"kt.example.generate": LocBuilderCall,
		"rs.example.create":   LocRequestField,
		"py.example.create":   LocParamName,
	}
	for _, r := range reg.Rows {
		got := r.ArgMap.Model.Form
		if got != want[r.ID] {
			t.Errorf("row %q resolved to form %q, want %q", r.ID, got, want[r.ID])
		}
		if got.BindsBeforeTheCall() != (got == LocBuilderCall || got == LocRequestField) {
			t.Errorf("row %q: BindsBeforeTheCall disagrees with the form", r.ID)
		}
	}
	if reg.Rows[0].ArgMap.Model.Builder != "modelName" {
		t.Errorf("the builder method name was not carried: %+v", reg.Rows[0].ArgMap.Model)
	}
	if reg.Rows[1].ArgMap.Model.Request != "model" {
		t.Errorf("the request field name was not carried: %+v", reg.Rows[1].ArgMap.Model)
	}

	// 🔴 Additivity, over the SHIPPED registry. The claim is precise, and stating it loosely would make
	// this test either useless or wrong: rows that ALREADY addressed something inside the argument list
	// must be untouched, while rows that addressed nothing may now declare where their SDK binds. The
	// second is the whole change; only the first is what "additive" promises.
	def, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	// The languages whose SDKs bind at the call site. A locator here must never become a
	// binds-before-the-call form — that would be a CHANGED materialization, not an added one.
	callSiteBound := map[string]bool{"go": true, "python": true, "typescript": true, "javascript": true}
	for _, r := range def.Rows {
		for name, loc := range map[string]*ArgLocator{"model": r.ArgMap.Model, "prompt": r.ArgMap.Prompt, "tools": r.ArgMap.Tools} {
			if loc == nil {
				continue
			}
			if loc.Form == "" {
				t.Errorf("shipped row %q's %s locator lost its form", r.ID, name)
			}
			if callSiteBound[r.Language] && loc.Form.BindsBeforeTheCall() {
				t.Errorf("shipped row %q's %s locator was CONVERTED to a binds-before-the-call form; rows "+
					"that already addressed the argument list must be untouched", r.ID, name)
			}
			// A binds-before-the-call locator must carry the name it binds at, or it is unusable and the
			// rewriter would refuse with a blank quotation.
			if loc.Form.BindsBeforeTheCall() && loc.Builder == "" && loc.Request == "" {
				t.Errorf("row %q's %s locator declares a binding site with no method or field name", r.ID, name)
			}
		}
	}
}
