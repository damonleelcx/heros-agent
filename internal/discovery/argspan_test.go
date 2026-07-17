package discovery

import (
	"testing"
)

// Argument-span tests for the language-neutral apply path (ADR-003).
//
// The load-bearing assertion in this file is the same one in every case: SLICE THE SOURCE WITH THE
// SPAN AND COMPARE THE BYTES. That is what cannot be faked. A test that asserted "Keywords has a
// `model` entry" would pass against a span pointing at the wrong argument, at the keyword's NAME
// instead of its value, or off by the width of a quote — which are precisely the ways a codemod
// silently corrupts a file. Nothing here trusts an offset it did not slice with.

// analyzeOne parses one source through an analyzer and returns the call site whose selector leaf is
// `leaf`, plus the source, so a test can slice spans out of the exact bytes the analyzer saw.
func analyzeOne(t *testing.T, a LanguageAnalyzer, rel, src, leaf string) (RawCallSite, []byte) {
	t.Helper()
	unit, diags := a.Analyze(rel, []byte(src))
	for _, d := range diags {
		if d.Severity == SeverityError {
			t.Fatalf("%s: analyzer error diagnostic: %s", a.Language(), d.Message)
		}
	}
	for _, cs := range unit.CallSites {
		if builderLeaf(cs) == leaf {
			return cs, []byte(src)
		}
	}
	t.Fatalf("%s: no call site with leaf %q in %d sites", a.Language(), leaf, len(unit.CallSites))
	return RawCallSite{}, nil
}

// slice returns the bytes a span points at, failing loudly rather than panicking on a span that is
// outside the source (which is itself the bug this file exists to catch).
func slice(t *testing.T, src []byte, s ArgSpan) string {
	t.Helper()
	if s.Start < 0 || s.End > len(src) || s.Start > s.End {
		t.Fatalf("span [%d,%d) is outside the %d-byte source", s.Start, s.End, len(src))
	}
	return string(src[s.Start:s.End])
}

// wantArg is one expected located argument: the exact source text its span must cover, and its kind.
type wantArg struct {
	name string
	text string  // exact bytes the span must slice out of the source
	kind ArgKind // the rewrite classification
	read string  // the text the resolution floor reads ("" => reads none)
}

func checkArgs(t *testing.T, lang string, cs RawCallSite, src []byte, want []wantArg) {
	t.Helper()
	for _, w := range want {
		got, ok := cs.Keywords[w.name]
		if !ok {
			t.Errorf("%s: keyword %q not located; have %v", lang, w.name, keysOf(cs.Keywords))
			continue
		}
		if s := slice(t, src, got.Value); s != w.text {
			t.Errorf("%s: keyword %q span [%d,%d) slices %q, want %q",
				lang, w.name, got.Value.Start, got.Value.End, s, w.text)
		}
		if got.Kind != w.kind {
			t.Errorf("%s: keyword %q kind = %q, want %q", lang, w.name, got.Kind, w.kind)
		}
		if got.Text != w.read {
			t.Errorf("%s: keyword %q floor text = %q, want %q", lang, w.name, got.Text, w.read)
		}
	}
}

func keysOf(m map[string]ArgValue) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// TestArgSpansPointAtTheValueExpression is the core proof, for every language that HAS named
// arguments: each located span slices exactly the argument's value expression out of the source —
// not the name, not the `name=value` pair, not the quotes-minus-one.
//
// It covers literal / interpolated / expression in each language in one pass, because the three are
// produced by the same code path and a table that only ever fed it literals would prove the least
// interesting third of it.
func TestArgSpansPointAtTheValueExpression(t *testing.T) {
	tests := []struct {
		lang string
		a    LanguageAnalyzer
		rel  string
		src  string
		leaf string
		want []wantArg
	}{
		{
			lang: "python",
			a:    &pythonAnalyzer{},
			rel:  "svc/triage.py",
			src: `import anthropic
client = anthropic.Anthropic()
MODEL_ID = "claude-sonnet-4-5"
ticket = "t"
client.messages.create(model="claude-sonnet-4-5", fallback=MODEL_ID, note=f"triage {ticket}", messages=[{"role": "user", "content": ticket}])
`,
			leaf: "create",
			want: []wantArg{
				// a static literal: replaceable, and the floor reads it
				{name: "model", text: `"claude-sonnet-4-5"`, kind: ArgLiteralString, read: "claude-sonnet-4-5"},
				// a runtime expression: present (so it has a span to replace) but no static text
				{name: "fallback", text: `MODEL_ID`, kind: ArgExpression, read: ""},
				// an f-string: the floor has always read its first segment; it is NOT replaceable
				{name: "note", text: `f"triage {ticket}"`, kind: ArgInterpolatedString, read: "triage "},
				// a construction: the whole list is the span
				{name: "messages", text: `[{"role": "user", "content": ticket}]`, kind: ArgExpression, read: ""},
			},
		},
		{
			lang: "typescript",
			a:    &jsAnalyzer{lang: "typescript", exts: []string{".ts"}},
			rel:  "src/agent.ts",
			src: `import OpenAI from "openai";
const client = new OpenAI();
const q = "hi";
const r = await client.chat.completions.create({ model: "gpt-4o", fallback: MODEL_ID, sys: ` + "`you are ${q}`" + `, pinned: ` + "`gpt-4o-mini`" + `, messages: [{ role: "user", content: q }] });
`,
			leaf: "create",
			want: []wantArg{
				{name: "model", text: `"gpt-4o"`, kind: ArgLiteralString, read: "gpt-4o"},
				{name: "fallback", text: `MODEL_ID`, kind: ArgExpression, read: ""},
				{name: "sys", text: "`you are ${q}`", kind: ArgInterpolatedString, read: ""},
				// a substitution-free template literal IS replaceable, and yet the floor reads nothing
				// from it — the case that proves Kind is not derived from Text.
				{name: "pinned", text: "`gpt-4o-mini`", kind: ArgLiteralString, read: ""},
				{name: "messages", text: `[{ role: "user", content: q }]`, kind: ArgExpression, read: ""},
			},
		},
		{
			lang: "javascript",
			a:    &jsAnalyzer{lang: "javascript", exts: []string{".js"}},
			rel:  "src/agent.js",
			src: `const OpenAI = require("openai");
const client = new OpenAI();
const r = await client.chat.completions.create({ model: "gpt-4o", messages: [] });
`,
			leaf: "create",
			want: []wantArg{
				{name: "model", text: `"gpt-4o"`, kind: ArgLiteralString, read: "gpt-4o"},
				{name: "messages", text: `[]`, kind: ArgExpression, read: ""},
			},
		},
		{
			lang: "kotlin",
			a:    &kotlinAnalyzer{},
			rel:  "src/Agent.kt",
			src: `import dev.langchain4j.model.chat.ChatLanguageModel

fun run(model: ChatLanguageModel, name: String) {
    model.generate(prompt = "hello there", greeting = "hi $name", other = MODEL_ID)
}
`,
			leaf: "generate",
			want: []wantArg{
				{name: "prompt", text: `"hello there"`, kind: ArgLiteralString, read: "hello there"},
				{name: "greeting", text: `"hi $name"`, kind: ArgInterpolatedString, read: "hi "},
				{name: "other", text: `MODEL_ID`, kind: ArgExpression, read: ""},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.lang, func(t *testing.T) {
			cs, src := analyzeOne(t, tc.a, tc.rel, tc.src, tc.leaf)
			checkArgs(t, tc.lang, cs, src, tc.want)
		})
	}
}

// TestArgSpansAreAlwaysRealSpans holds the invariant every consumer depends on: an ArgValue exists
// only because an argument was LOCATED, so its span is always a real, in-bounds, non-empty range.
// Without this, a caller could read a zero-valued ArgSpan out of a map miss and splice at offset 0 —
// the exact magic-zero failure a nil *ArgInsert is shaped to prevent for insertions.
func TestArgSpansAreAlwaysRealSpans(t *testing.T) {
	src := `import anthropic
client = anthropic.Anthropic()
client.messages.create(model="m", empty="", expr=x, messages=[])
`
	cs, b := analyzeOne(t, &pythonAnalyzer{}, "a.py", src, "create")
	if len(cs.Keywords) != 4 {
		t.Fatalf("want 4 located keywords, got %d (%v)", len(cs.Keywords), keysOf(cs.Keywords))
	}
	for name, v := range cs.Keywords {
		if v.Value.Len() <= 0 {
			t.Errorf("keyword %q: empty span [%d,%d)", name, v.Value.Start, v.Value.End)
		}
		if v.Value.Start < 0 || v.Value.End > len(b) {
			t.Errorf("keyword %q: span [%d,%d) outside the %d-byte source", name, v.Value.Start, v.Value.End, len(b))
		}
		if v.Kind == "" {
			t.Errorf("keyword %q: unclassified kind", name)
		}
	}
	// `empty=""` is a static literal the floor reads nothing from — it must still be replaceable, and
	// it must still be unresolved in the IR. Both, not either.
	if got := cs.Keywords["empty"]; got.Kind != ArgLiteralString || got.Text != "" {
		t.Errorf(`empty="": kind=%q text=%q, want literal_string + ""`, got.Kind, got.Text)
	}
}

// TestKeywordInsertSplicesValidSource is the absent-argument proof, and it does not stop at
// "KeywordInsert is non-nil". It performs the splice the rewriter would perform and asserts the
// RESULTING SOURCE, then re-parses it and asserts the parser accepts it and now sees the argument.
//
// 🔴 The `create(ticket)` case is the one that matters. Python rejects a keyword argument written
// before a positional one, so an insertion point "just inside the opening paren" — the obvious
// implementation, and the one Go's rewriteModel legitimately uses for a struct literal — produces
// `create(model="x", ticket)`, a SyntaxError. Only re-parsing catches that; asserting an offset
// never would.
func TestKeywordInsertSplicesValidSource(t *testing.T) {
	tests := []struct {
		lang     string
		a        LanguageAnalyzer
		rel      string
		src      string
		leaf     string
		insert   string // the value expression to bind
		wantCall string // the call's source text after the splice
	}{
		{
			lang:     "python/empty-args",
			a:        &pythonAnalyzer{},
			rel:      "a.py",
			src:      "import anthropic\nclient.messages.create()\n",
			leaf:     "create",
			insert:   `"claude-sonnet-4-5"`,
			wantCall: `client.messages.create(model="claude-sonnet-4-5")`,
		},
		{
			lang:     "python/existing-keyword",
			a:        &pythonAnalyzer{},
			rel:      "a.py",
			src:      "import anthropic\nclient.messages.create(messages=[])\n",
			leaf:     "create",
			insert:   `"claude-sonnet-4-5"`,
			wantCall: `client.messages.create(messages=[], model="claude-sonnet-4-5")`,
		},
		{
			// the SyntaxError case: appending is valid, prepending would not be.
			lang:     "python/positional-arg",
			a:        &pythonAnalyzer{},
			rel:      "a.py",
			src:      "import anthropic\nclient.messages.create(ticket)\n",
			leaf:     "create",
			insert:   `"claude-sonnet-4-5"`,
			wantCall: `client.messages.create(ticket, model="claude-sonnet-4-5")`,
		},
		{
			lang:     "python/trailing-comma",
			a:        &pythonAnalyzer{},
			rel:      "a.py",
			src:      "import anthropic\nclient.messages.create(messages=[],)\n",
			leaf:     "create",
			insert:   `"m"`,
			wantCall: `client.messages.create(messages=[],model="m")`,
		},
		{
			lang:   "typescript",
			a:      &jsAnalyzer{lang: "typescript", exts: []string{".ts"}},
			rel:    "a.ts",
			src:    "import OpenAI from \"openai\";\nawait client.chat.completions.create({ messages: [] });\n",
			leaf:   "create",
			insert: `"gpt-4o"`,
			// The inserted pair lands just inside the closing brace, so the object's original inner
			// space precedes the separator. Valid JS, and cosmetics are not this engine's call: the
			// target repo's own formatter (prettier/gofmt-equivalent) is what judges layout.
			wantCall: `await client.chat.completions.create({ messages: [] , model: "gpt-4o"})`,
		},
		{
			lang:     "javascript/empty-object",
			a:        &jsAnalyzer{lang: "javascript", exts: []string{".js"}},
			rel:      "a.js",
			src:      "const OpenAI = require(\"openai\");\nawait client.chat.completions.create({});\n",
			leaf:     "create",
			insert:   `"gpt-4o"`,
			wantCall: `await client.chat.completions.create({model: "gpt-4o"})`,
		},
		{
			lang:     "kotlin",
			a:        &kotlinAnalyzer{},
			rel:      "A.kt",
			src:      "import dev.langchain4j.model.chat.ChatLanguageModel\nfun run(m: ChatLanguageModel) {\n    m.generate(\"hi\")\n}\n",
			leaf:     "generate",
			insert:   `"gpt-4o"`,
			wantCall: `m.generate("hi", model = "gpt-4o")`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.lang, func(t *testing.T) {
			cs, src := analyzeOne(t, tc.a, tc.rel, tc.src, tc.leaf)
			if cs.KeywordInsert == nil {
				t.Fatalf("%s: no KeywordInsert — an absent model argument could not be added", tc.lang)
			}
			ins := cs.KeywordInsert
			if ins.At < 0 || ins.At > len(src) {
				t.Fatalf("%s: insert offset %d outside the %d-byte source", tc.lang, ins.At, len(src))
			}

			// Splice exactly the way the rewriter is documented to: Prefix + name + Assign + value.
			add := ins.Prefix + "model" + ins.Assign + tc.insert
			out := string(src[:ins.At]) + add + string(src[ins.At:])

			// The call's own text must be what we expect, byte for byte.
			after, _ := analyzeOne(t, tc.a, tc.rel, out, tc.leaf)
			got := sliceLines(out, after.LineStart, after.LineEnd)
			if got != tc.wantCall {
				t.Errorf("%s: after splice the call reads\n  %q\nwant\n  %q", tc.lang, got, tc.wantCall)
			}

			// And the result must PARSE. This is the assertion that catches an insertion point which
			// is arithmetically fine and syntactically illegal.
			unit, diags := tc.a.Analyze(tc.rel, []byte(out))
			for _, d := range diags {
				t.Errorf("%s: spliced source no longer parses cleanly: [%s] %s", tc.lang, d.Severity, d.Message)
			}
			// ...and the parser must now SEE the argument we added, sliced back out of the new source.
			var found bool
			for _, c := range unit.CallSites {
				if builderLeaf(c) != tc.leaf {
					continue
				}
				v, ok := c.Keywords["model"]
				if !ok {
					continue
				}
				found = true
				if s := string([]byte(out)[v.Value.Start:v.Value.End]); s != tc.insert {
					t.Errorf("%s: re-read model span slices %q, want %q", tc.lang, s, tc.insert)
				}
			}
			if !found {
				t.Errorf("%s: the spliced `model` argument is not located on re-parse", tc.lang)
			}
		})
	}
}

// sliceLines returns lines [start,end] (1-based) of s, trimmed — enough to compare one call's text.
func sliceLines(s string, start, end int) string {
	lines := splitLines(s)
	if start < 1 || end > len(lines) || start > end {
		return ""
	}
	out := ""
	for i := start - 1; i < end; i++ {
		if i > start-1 {
			out += "\n"
		}
		out += lines[i]
	}
	return trimSpace(out)
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\n' || s[j-1] == ';') {
		j--
	}
	return s[i:j]
}

// TestLanguagesWithoutNamedArgumentsLocateNothing holds the abstentions, so that "Java has no spans"
// stays a recorded decision that a future change has to argue with, rather than a gap someone fills
// in by accident and nobody notices.
//
// Java and Rust have no named-argument form AT ALL, so there is nothing to locate and nowhere to
// insert. The absence must be TOTAL — a nil KeywordInsert, not a zero-valued one — because a
// zero-valued insert splices at offset 0, i.e. corrupts the head of the file.
func TestLanguagesWithoutNamedArgumentsLocateNothing(t *testing.T) {
	tests := []struct {
		lang string
		a    LanguageAnalyzer
		rel  string
		src  string
		leaf string
	}{
		{
			lang: "java",
			a:    &javaAnalyzer{},
			rel:  "Agent.java",
			src: `import dev.langchain4j.model.chat.ChatLanguageModel;
class Agent {
  String run(ChatLanguageModel model) { return model.generate("Hello"); }
}
`,
			leaf: "generate",
		},
		{
			lang: "rust",
			a:    &rustAnalyzer{},
			rel:  "src/main.rs",
			src: `use async_openai::Client;
async fn run(client: &Client) {
    let _ = client.chat().create(req).await;
}
`,
			leaf: "create",
		},
	}
	for _, tc := range tests {
		t.Run(tc.lang, func(t *testing.T) {
			cs, _ := analyzeOne(t, tc.a, tc.rel, tc.src, tc.leaf)
			if len(cs.Keywords) != 0 {
				t.Errorf("%s has no named arguments, but %d keyword(s) were located: %v — if the language "+
					"gained one, say so here; if this is a positional argument, it does not belong in "+
					"Keywords (see the Java/Rust comments in the analyzers)", tc.lang, len(cs.Keywords), keysOf(cs.Keywords))
			}
			if cs.KeywordInsert != nil {
				t.Errorf("%s: KeywordInsert = %+v, want nil — there is no legal place to write a named "+
					"argument in this language, and a non-nil insert would have a rewriter splice one in",
					tc.lang, *cs.KeywordInsert)
			}
		})
	}
}

// TestGoFrameworkUnitPopulatesNoSpans holds Go's abstention (see framework.go's comment).
//
// Go keeps its go/ast path (ADR-003), and the only RawCallSite Go ever builds is the one
// goUnitFromPackage assembles to feed the framework graph readers — per PACKAGE, from many files, with
// no single source buffer. A byte offset there is an offset into nothing. This test is what makes that
// absence checkable rather than merely commented.
func TestGoFrameworkUnitPopulatesNoSpans(t *testing.T) {
	pf, err := parseSingle("example.com/app/agent", "agent.go", `package agent
import "github.com/example/langgraphgo/graph"
func build() {
	g := graph.NewStateGraph()
	g.AddNode("classify", nil)
	g.AddEdge("classify", "answer")
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	unit := goUnitFromPackage(&Package{PkgPath: "example.com/app/agent", Files: []*ParsedFile{pf}})
	if len(unit.CallSites) == 0 {
		t.Fatal("no call sites: the fixture stopped exercising goUnitFromPackage")
	}
	for _, cs := range unit.CallSites {
		if len(cs.Keywords) != 0 {
			t.Errorf("go: %s located %d keyword span(s) %v — Go's rewrite path is go/ast "+
				"(ADR-003), and a span assembled per-package has no source buffer to be an offset into",
				builderLeaf(cs), len(cs.Keywords), keysOf(cs.Keywords))
		}
		if cs.KeywordInsert != nil {
			t.Errorf("go: %s has KeywordInsert = %+v, want nil", builderLeaf(cs), *cs.KeywordInsert)
		}
	}
}

// TestFloorIsUnchangedByWideningTheKeywordMap is the non-regression assertion in code, next to the
// change it guards, rather than only in the fixture harness.
//
// The Keywords map now carries arguments the floor cannot read (they are there for their spans). This
// asserts the thing that makes that safe: keywordFor's answer is decided by Text alone, so an
// argument that is PRESENT-but-unreadable resolves exactly as it did when it was absent from the map
// altogether — unresolved. If someone "helpfully" makes keywordFor consult Kind, this goes red.
func TestFloorIsUnchangedByWideningTheKeywordMap(t *testing.T) {
	site := DetectedCallSite{Keywords: map[string]ArgValue{
		"literal":      {Value: ArgSpan{0, 3}, Kind: ArgLiteralString, Text: "gpt-4o"},
		"expr":         {Value: ArgSpan{4, 8}, Kind: ArgExpression},     // present, unreadable
		"tmpl":         {Value: ArgSpan{9, 20}, Kind: ArgLiteralString}, // replaceable, unread
		"interpolated": {Value: ArgSpan{21, 30}, Kind: ArgInterpolatedString, Text: "triage "},
	}}
	cases := []struct {
		name    string
		loc     *ArgLocator
		wantVal string
		wantOK  bool
	}{
		{"literal resolves", &ArgLocator{Form: LocParamName, Name: "literal"}, "gpt-4o", true},
		{"runtime expression stays unresolved", &ArgLocator{Form: LocParamName, Name: "expr"}, "", false},
		{"replaceable-but-unread stays unresolved", &ArgLocator{Form: LocParamName, Name: "tmpl"}, "", false},
		{"absent stays unresolved", &ArgLocator{Form: LocParamName, Name: "nope"}, "", false},
		{"non-name locator stays unresolved", &ArgLocator{Form: LocFieldPath, Field: "literal"}, "", false},
		{"nil locator stays unresolved", nil, "", false},
		// The floor has ALWAYS read an f-string's first static segment. That is a pre-existing floor
		// inaccuracy; it is recorded here because it is IR-visible, and Kind is what keeps the REWRITE
		// path from inheriting it. Changing this line changes the emitted IR.
		{"f-string still reads its first segment", &ArgLocator{Form: LocParamName, Name: "interpolated"}, "triage ", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := keywordFor(site, c.loc)
			if got != c.wantVal || ok != c.wantOK {
				t.Errorf("keywordFor = (%q,%v), want (%q,%v)", got, ok, c.wantVal, c.wantOK)
			}
		})
	}
}

// TestArgInsertRefusesRecoveredSource: tree-sitter never fails a parse — it RECOVERS, returning a
// tree with ERROR/MISSING nodes (see syntaxErrorDiagnostics). `create(model="m", ,)` is the case that
// makes this more than theory: it recovers into an ordinary-looking argument_list, with a REAL
// closing paren and an ERROR node in the middle, and the call site is emitted normally. A
// "splice before the closing delimiter" rule that only checked for the delimiter would hand back a
// confident offset into a region the parser was guessing at.
//
// The source is already broken here, so the splice could not have shipped either way — the verifier
// would reject it (ADR-003). That is exactly why this is worth refusing at the source: an engine that
// emits a diff into source it did not understand, and leans on the gate to notice, has moved a
// correctness property into a place where a weaker gate (`node --check`, `py_compile`) may not hold
// it. 失败要显眼: refuse where the knowledge is, not where the cleanup is.
func TestArgInsertRefusesRecoveredSource(t *testing.T) {
	src := "import anthropic\nclient.messages.create(model=\"m\", ,)\n"
	unit, diags := (&pythonAnalyzer{}).Analyze("a.py", []byte(src))
	if len(diags) == 0 {
		t.Fatal("malformed source produced no diagnostic: the fixture is no longer malformed")
	}
	var seen bool
	for _, cs := range unit.CallSites {
		if builderLeaf(cs) != "create" {
			continue
		}
		seen = true
		if cs.KeywordInsert != nil {
			t.Errorf("KeywordInsert = %+v on a call whose argument list was RECOVERED, not parsed; "+
				"want nil", *cs.KeywordInsert)
		}
	}
	// No t.Skip: if tree-sitter stops recovering this call, the test has stopped testing what it
	// claims to and must say so, not quietly pass.
	if !seen {
		t.Fatal("tree-sitter no longer recovers a `create` call from this source; the fixture no longer " +
			"exercises the recovered-container path — replace it with one that does")
	}
}
