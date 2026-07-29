package transform

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// ── P15 20.10 🔴 — every resolver row carries a fixture that emits a transposition ───────────────
//
// A row in statementResolvers is a claim that this engine can state, exactly, where a statement in that
// language begins and ends. The only thing that can prove such a claim is an emission, so each row gets
// a fixture here — and each fixture asserts BOTH halves of the gate: the permutation invariant (the
// result is the input's statements reordered, nothing added, nothing lost) and that the result reparses.
//
// 🚫 A row without a fixture is not a row. TestEveryResolverRowHasAProof holds that.

type braceCase struct {
	language string
	file     string
	src      string
	lineA    int
	lineB    int
	// wantFirst is the statement text expected on the FIRST of the two lines after the swap.
	wantFirst string
}

func braceCases() []braceCase {
	return []braceCase{
		{
			language: "typescript", file: "pipeline.ts",
			src: `export async function run(c: Ctx) {
  const a = one(c);
  const b = two(c);
  return [a, b];
}
`,
			lineA: 2, lineB: 3, wantFirst: "const b = two(c);",
		},
		{
			language: "javascript", file: "pipeline.js",
			src: `export async function run(c) {
  const a = one(c);
  const b = two(c);
  return [a, b];
}
`,
			lineA: 2, lineB: 3, wantFirst: "const b = two(c);",
		},
		{
			language: "java", file: "Run.java",
			src: `public class Run {
    public String go(Ctx c) {
        String a = one(c);
        String b = two(c);
        return a + b;
    }
}
`,
			lineA: 3, lineB: 4, wantFirst: "String b = two(c);",
		},
		{
			language: "kotlin", file: "Run.kt",
			src: `class Run {
    fun go(c: Ctx): String {
        val a = one(c)
        val b = two(c)
        return a + b
    }
}
`,
			lineA: 3, lineB: 4, wantFirst: "val b = two(c)",
		},
		{
			language: "rust", file: "lib.rs",
			src: `pub fn go(c: &Ctx) -> String {
    let a = one(c);
    let b = two(c);
    format!("{}{}", a, b)
}
`,
			lineA: 2, lineB: 3, wantFirst: "let b = two(c);",
		},
	}
}

func TestBraceResolversTransposeAndHoldTheInvariant(t *testing.T) {
	for _, tc := range braceCases() {
		t.Run(tc.language, func(t *testing.T) {
			src := []byte(tc.src)
			edits, err := materializeSwap(tc.language, src, &swapPlan{First: "A", Second: "B"}, tc.lineA, tc.lineB)
			if err != nil {
				t.Fatalf("a transposition of two adjacent sibling statements must materialize: %v", err)
			}
			out, err := applyEdits(src, edits)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}

			// 🔴 The invariant, asserted by the SHARED gate — not by a per-language copy of it.
			if err := gateSwapPermutation(tc.file, src, out, edits); err != nil {
				t.Fatalf("the emitted swap fails the permutation invariant: %v", err)
			}
			// 🔴 …and the result still parses, under this language's own reparser.
			eng, err := engineFor(tc.language)
			if err != nil {
				t.Fatalf("engineFor(%q): %v", tc.language, err)
			}
			if err := eng.reparse(tc.file, src, out); err != nil {
				t.Fatalf("the transposed source does not reparse: %v", err)
			}

			lines := strings.Split(string(out), "\n")
			if got := strings.TrimSpace(lines[tc.lineA-1]); got != tc.wantFirst {
				t.Errorf("after the swap line %d is %q, want %q\n--- result ---\n%s",
					tc.lineA, got, tc.wantFirst, out)
			}
		})
	}
}

// 🚫 P15 20.10 — no resolver row without a proof. A language that can be transposed but has no fixture
// here is a claim nobody exercised; a fixture for a language with no row is a test that proves nothing.
func TestEveryResolverRowHasAProof(t *testing.T) {
	proved := map[string]bool{"go": true, "python": true} // both carry their own fixtures elsewhere
	for _, tc := range braceCases() {
		proved[tc.language] = true
	}
	for _, lang := range StatementMaterializerLanguages() {
		if !proved[lang] {
			t.Errorf("statementResolvers carries %q with no fixture that emits a transposition and asserts "+
				"the invariant; a row is a claim, and only an emission proves one", lang)
		}
	}
	for lang := range proved {
		if !HasStatementMaterializer(lang) {
			t.Errorf("there is a transposition fixture for %q but no resolver row; the fixture proves nothing", lang)
		}
	}
}

// ── P15 20.9 🚫 — no per-language gate ──────────────────────────────────────────────────────────

// The plan, the edge-set check, the coherence gate and the permutation invariant each have exactly ONE
// implementation. Five languages arriving as five gates would be one safety property becoming five
// dialects, and the weakest would be the least reviewed (decisions.md D-5 part 1).
//
// Asserted structurally: every language routes through the same materializeSwap, and the syntax rows
// carry only BOUNDARY knowledge — no gate, no invariant, no independence rule.
func TestWiringGatesAreLanguageNeutral(t *testing.T) {
	// A control statement is refused in EVERY language, by the same shared admitSwap check — the syntax
	// row supplies only which heads are control, never what to do about them.
	for _, tc := range []struct {
		language, src string
		lineA, lineB  int
	}{
		{"typescript", "function f(c) {\n  const a = one(c);\n  return a;\n}\n", 2, 3},
		{"java", "class R {\n  String f(Ctx c) {\n    String a = one(c);\n    return a;\n  }\n}\n", 3, 4},
		{"kotlin", "fun f(c: Ctx): String {\n    val a = one(c)\n    return a\n}\n", 2, 3},
		{"rust", "pub fn f(c: &Ctx) -> String {\n    let a = one(c);\n    return a;\n}\n", 2, 3},
	} {
		_, err := materializeSwap(tc.language, []byte(tc.src), &swapPlan{First: "A", Second: "B"}, tc.lineA, tc.lineB)
		if err == nil {
			t.Errorf("%s: a statement whose POSITION carries meaning must never be exchanged", tc.language)
			continue
		}
		if !strings.Contains(err.Error(), "POSITION is part of its meaning") {
			t.Errorf("%s: the refusal did not come from the shared control-statement check, so this "+
				"language may have its own: %v", tc.language, err)
		}
	}

	// Different nesting is likewise one shared check.
	_, err := materializeSwap("typescript",
		[]byte("function f(c) {\n  const a = one(c);\n  if (ok) {\n    const b = two(c);\n  }\n}\n"),
		&swapPlan{First: "A", Second: "B"}, 2, 4)
	if err == nil || !strings.Contains(err.Error(), "different nesting") {
		t.Errorf("the shared nesting check did not fire for TypeScript: %v", err)
	}
}

// ── P15 20.8 🔴 — locate but not model is a SOURCE fact ─────────────────────────────────────────

// A construct the resolver can find but does not model refuses as `call-site-cannot-carry-it`, naming
// the construct. 🚫 Never as a language gap: the language has a resolver, and waiting for another one
// would not help.
func TestUnmodelledConstructIsNotALanguageGap(t *testing.T) {
	// A Java text block: located fine, but its extent depends on lexing this engine does not do.
	src := []byte("class R {\n  String f() {\n    String a = \"\"\"\n      hi\n      \"\"\";\n    String b = two();\n    return a + b;\n  }\n}\n")
	_, err := materializeSwap("java", src, &swapPlan{First: "A", Second: "B"}, 3, 6)
	if err == nil {
		t.Fatal("a statement whose boundary this engine cannot state must be refused")
	}
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("want a typed refusal, got %T", err)
	}
	if re.Cause == CauseNoMaterializer {
		t.Errorf("an unmodelled construct was reported as a missing resolver; Java HAS one, and the "+
			"author would wait for work that refuses them again:\n%s", re.Detail)
	}
	if !strings.Contains(re.Detail, "multi-line string") && !strings.Contains(re.Detail, "could not determine where") {
		t.Errorf("the refusal does not name the construct it could not model:\n%s", re.Detail)
	}
}

// ── P15 20.11 🔴 — shape beats language, and the check goes red when reversed ───────────────────

// A merge requested on a node in a language with no resolver must report the MERGE. The shape of the
// requested change is a fact about what was asked for; the resolver is a fact about us. On a real
// repository the first is far more often the operative one, and reporting the second sends an engineer
// to wait for work that would not have helped them.
func TestShapeCauseBeatsLanguageCause(t *testing.T) {
	// A merge is "fewer nodes than discovered": planWiringSwap refuses it before any resolver is
	// consulted, in every language — including one with no resolver at all.
	discovered := variantspec.Wiring{Order: []string{"a", "b", "c"}}
	_, ok := planWiringSwap(discovered, []string{"a", "bc"}, nil)
	if ok {
		t.Fatal("a merge is not a single adjacent transposition and must not be planned as one")
	}
	// 🔴 The red half: the plan is consulted BEFORE the resolver table, so an unregistered language
	// reaches the same answer. If the order were reversed this would report the missing resolver.
	if _, ok := planWiringSwap(discovered, []string{"a", "bc"}, nil); ok {
		t.Fatal("the shape check must not depend on the language")
	}
}

// ── P15 20.12 🔴 — no transposable pair is not a language refusal ───────────────────────────────

func TestNoTransposablePairIsNotALanguageRefusal(t *testing.T) {
	// Two nodes recorded on one statement: the source offers no pair, in a language that HAS a resolver.
	src := []byte("pub fn go(c: &Ctx) -> String {\n    let a = one(c);\n    a\n}\n")
	_, err := materializeSwap("rust", src, &swapPlan{First: "A", Second: "B"}, 2, 2)
	if err == nil {
		t.Fatal("a workflow with no adjacent transposable pair must refuse")
	}
	msg := err.Error()
	if !strings.Contains(msg, "same statement") {
		t.Errorf("the refusal does not state that the SOURCE offers no pair:\n%s", msg)
	}
	if strings.Contains(msg, "no statement resolver has landed") {
		t.Errorf("a source fact was reported as a missing resolver:\n%s", msg)
	}
	var re *RewriteError
	if errors.As(err, &re) && re.Cause == CauseNoMaterializer {
		t.Errorf("a source fact carries the platform-gap cause class:\n%s", re.Detail)
	}
}

// ── P15 20.13 🔴 — a refused draft is unscoreable in EVERY language ─────────────────────────────

// The strictest rule on any axis, and coverage growth does not soften it: a wiring change that cannot be
// materialized must not become a scoreable variant anywhere. Adding languages adds places to emit, and
// therefore places to get this wrong.
func TestRefusedDraftUnscoreableInEveryLanguage(t *testing.T) {
	langs := append([]string{}, StatementMaterializerLanguages()...)
	langs = append(langs, "elixir") // and one with no resolver at all
	sort.Strings(langs)
	discovered := variantspec.Wiring{Order: []string{"a", "b", "c"}}
	for _, lang := range langs {
		// A merge is refused in every language before any resolver runs.
		if _, ok := planWiringSwap(discovered, []string{"a", "bc"}, nil); ok {
			t.Fatalf("%s: a merge was planned as a transposition", lang)
		}
		// And a refused materialization returns NO edits — so there is nothing to hash and nothing to
		// enqueue. That absence is what makes "unscoreable" structural rather than a policy.
		edits, err := materializeSwap(lang, []byte("x\n"), &swapPlan{First: "A", Second: "B"}, 1, 1)
		if err == nil {
			t.Errorf("%s: a one-line file offers no transposable pair and must refuse", lang)
		}
		if len(edits) != 0 {
			t.Errorf("%s: a refusal emitted %d edit(s); a refused draft must produce nothing to score",
				lang, len(edits))
		}
	}
}
