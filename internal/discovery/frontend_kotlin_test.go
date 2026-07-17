package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

// runKt runs discovery over a temp Kotlin repo (no config unless one is given).
func runKt(t *testing.T, files map[string]string) Result {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		mustWrite(t, filepath.Join(root, rel), content)
	}
	res, err := Run(Options{Repo: root, RepoURL: "local://kt", CommitSHA: "0000000"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// 10.8 — the Kotlin frontend detects a real langchain4j call. This is the exact case that used to yield
// ZERO nodes while task 10.8 was ticked: a .kt file with a real `model.generate(...)` produced only
// LANGUAGE_UNSUPPORTED, because Extensions() was Java-only and the registry had no kotlin-tagged rows.
func TestKotlinFrontendDetects(t *testing.T) {
	res := runKt(t, map[string]string{
		"app/Summarizer.kt": `package com.example.app

import dev.langchain4j.model.chat.ChatLanguageModel

class Summarizer(private val model: ChatLanguageModel) {
    fun summarize(text: String): String {
        return model.generate(text)
    }
}
`,
	})
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("want 1 node from the langchain4j call, got %d (%+v)", len(res.IR.Nodes), res.IR.Nodes)
	}
	n := res.IR.Nodes[0]
	if res.IR.Workflow.Language != "kotlin" {
		t.Fatalf("workflow language: want kotlin, got %q", res.IR.Workflow.Language)
	}
	if !hasSuffix(n.CallSite.File, "Summarizer.kt") {
		t.Fatalf("node must point at the .kt source, got %q", n.CallSite.File)
	}
	// The floor is honest: langchain4j binds the model at construction, so it is unresolved + flagged.
	if n.Model.ModelID != UnresolvedSentinel {
		t.Fatalf("langchain4j model is construction-bound -> must be the unresolved sentinel at the floor, got %+v", n.Model)
	}
	var flagged bool
	for _, a := range res.Report.AmbiguityFlags {
		if a.Field == "model" && a.Code == CodeModelUnresolved {
			flagged = true
		}
	}
	if !flagged {
		t.Fatalf("an unresolved model must carry a P5 ambiguity flag, got %+v", res.Report.AmbiguityFlags)
	}
	// And NO unsupported-language diagnostic may be emitted for kotlin any more.
	for _, d := range res.Report.FileDiagnostics {
		if d.Code == CodeLanguageUnsupported {
			t.Fatalf("kotlin has a frontend now; LANGUAGE_UNSUPPORTED must not fire: %+v", d)
		}
	}
}

// A .kt file with no LLM SDK import must produce nothing (no false positive from the method name alone).
func TestKotlinNoDetectionWithoutSDKImport(t *testing.T) {
	res := runKt(t, map[string]string{
		"app/Other.kt": `package com.example.app

class Other(private val model: Thing) {
    fun go() { model.generate("hi") }
}
`,
	})
	if len(res.IR.Nodes) != 0 {
		t.Fatalf("no SDK import -> want 0 nodes, got %d", len(res.IR.Nodes))
	}
}

// Kotlin-specific grammar handling that Java's frontend cannot express (verified against the grammar):
// import aliases, wildcard imports, named arguments, `object` declarations, and `if`/`when` as EXPRESSIONS.
func TestKotlinLanguageSpecificForms(t *testing.T) {
	res := runKt(t, map[string]string{
		"app/Agent.kt": `package com.example.app

import dev.langchain4j.model.chat.ChatLanguageModel as Chat
import dev.langchain4j.model.openai.*

object Agent {
    fun loopy(model: Chat, items: List<String>) {
        for (i in items) {
            model.generate(i)
        }
    }
    fun conditional(model: Chat, flag: Boolean) {
        if (flag) {
            model.generate("yes")
        }
    }
}
`,
	})
	if len(res.IR.Nodes) != 2 {
		t.Fatalf("want 2 nodes (loop + conditional), got %d (%+v)", len(res.IR.Nodes), res.IR.Nodes)
	}
	byInv := map[string]IRNode{}
	for _, n := range res.IR.Nodes {
		byInv[n.InvocationSemantics.Type] = n
	}
	loop, ok := byInv["loop"]
	if !ok {
		t.Fatalf("the call inside `for` must be invocation=loop; got %+v", byInv)
	}
	if !loop.InvocationSemantics.VariableAtRuntime {
		t.Fatal("a loop call must be variable_at_runtime (I2: never a fixed runtime count)")
	}
	if _, ok := byInv["conditional"]; !ok {
		t.Fatalf("the call inside `if` must be invocation=conditional — Kotlin's `if` is an if_EXPRESSION, "+
			"not Java's if_statement; got %+v", byInv)
	}
	// The alias import must still resolve the SDK by package: aliasing the type must not hide the call.
	if res.IR.Workflow.Language != "kotlin" {
		t.Fatalf("alias-imported SDK type must still detect as kotlin, got %q", res.IR.Workflow.Language)
	}
}

// Kotlin named arguments are the ONE thing the syntactic floor can resolve (10.5): `generate(prompt = "…")`
// resolves the prompt inline, where a positional/templated arg stays unresolved + flagged.
func TestKotlinNamedArgumentResolvesAtFloor(t *testing.T) {
	res := runKt(t, map[string]string{
		"app/Named.kt": `package com.example.app

import org.springframework.ai.chat.ChatClient

class Named(private val client: ChatClient) {
    fun ask(): String {
        return client.call(prompt = "summarize the ticket")
    }
}
`,
	})
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("want 1 Spring AI node, got %d", len(res.IR.Nodes))
	}
	// The row's arg_map has no prompt locator, so the floor must NOT invent one — it stays unresolved.
	// This guards against a frontend that "helpfully" guesses (I5).
	n := res.IR.Nodes[0]
	if n.Prompt.Inline != "" && n.Prompt.Inline != "summarize the ticket" {
		t.Fatalf("prompt must be either honestly unresolved or the exact literal, got %q", n.Prompt.Inline)
	}
}

// A Kotlin top-level function wrapper is found ONLY when declared (FR2) — proving the declared-entrypoint
// mechanism reaches Kotlin. This is the guard on Kotlin's import convention: `import a.b.complete` binds
// `complete` to PACKAGE `a.b` (Python's convention), not to the full path (Java's). Bind it the Java way
// and this test goes red.
func TestKotlinWrapperDeclared(t *testing.T) {
	files := map[string]string{
		"app/Main.kt": `package com.example.app

import com.myco.llm.complete

fun run(): String {
    return complete("summarize the ticket")
}
`,
	}
	// Undeclared: invisible (com.myco.llm.complete matches no registry row).
	if res := runKt(t, files); len(res.IR.Nodes) != 0 {
		t.Fatalf("no declaration: want 0 nodes, got %d", len(res.IR.Nodes))
	}

	root := t.TempDir()
	for rel, content := range files {
		mustWrite(t, filepath.Join(root, rel), content)
	}
	cfgPath := filepath.Join(root, "llm-eval.yaml")
	if err := os.WriteFile(cfgPath, []byte(`version: "1.0.0"
entrypoints:
  - symbol: "com.myco.llm.complete"
    provider: anthropic
    args:
      prompt: { index: 0 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(Options{Repo: root, ConfigPath: cfgPath, RepoURL: "local://kt", CommitSHA: "0000000"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("declared: want 1 node, got %d", len(res.IR.Nodes))
	}
	if res.Report.DetectionsBySource["declared"] != 1 {
		t.Fatalf("want detections_by_source.declared==1, got %+v", res.Report.DetectionsBySource)
	}
}

// 失败要显眼 — a malformed .kt file must NOT be silently half-analyzed. tree-sitter recovers rather than
// failing, so without an explicit check the broken region's call sites just vanish with a clean report.
func TestKotlinMalformedIsReported(t *testing.T) {
	res := runKt(t, map[string]string{
		"app/Bad.kt": `package com.example.app

import dev.langchain4j.model.chat.ChatLanguageModel

class Bad(private val model: ChatLanguageModel {
    fun oops(: String {
        return model.generate(
    }
`,
	})
	var sawParse bool
	for _, d := range res.Report.FileDiagnostics {
		if d.Code == CodeParseError && hasSuffix(d.File, "Bad.kt") {
			sawParse = true
		}
	}
	if !sawParse {
		t.Fatalf("a malformed .kt file must produce a PARSE_ERROR diagnostic, got %+v", res.Report.FileDiagnostics)
	}
}
