package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

// runPy runs discovery over a temp repo of {relpath: content} python files (no go.mod needed).
func runPy(t *testing.T, files map[string]string) Result {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		mustWrite(t, filepath.Join(root, rel), content)
	}
	res, err := Run(Options{Repo: root, RepoURL: "local://py", CommitSHA: "0000000", WorkflowID: "pyrepo"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// The tree-sitter Python frontend detects SDK calls, resolves the model from a keyword string literal,
// flags loop calls variable_at_runtime, and emits schema-shaped nodes with workflow.language=python.
func TestPythonFrontendDetects(t *testing.T) {
	res := runPy(t, map[string]string{
		"svc/triage.py": `import anthropic
from openai import OpenAI

client = anthropic.Anthropic()

def classify(text):
    return client.messages.create(model="claude-sonnet-4-5", messages=[{"role": "user", "content": text}])

def agent(items):
    oai = OpenAI()
    for it in items:
        oai.chat.completions.create(model="gpt-4o", messages=[])
`,
	})
	if res.IR.Workflow.Language != "python" {
		t.Fatalf("workflow.language: want python, got %q", res.IR.Workflow.Language)
	}
	if len(res.IR.Nodes) != 2 {
		t.Fatalf("want 2 nodes (classify + agent), got %d", len(res.IR.Nodes))
	}

	byModel := map[string]IRNode{}
	for _, n := range res.IR.Nodes {
		byModel[n.Model.ModelID] = n
	}
	an, ok := byModel["claude-sonnet-4-5"]
	if !ok || an.Model.Provider != "anthropic" {
		t.Fatalf("anthropic node: want provider anthropic + model claude-sonnet-4-5, got %+v", byModel)
	}
	if an.InvocationSemantics.Type != "single" {
		t.Fatalf("classify should be single, got %q", an.InvocationSemantics.Type)
	}
	oa, ok := byModel["gpt-4o"]
	if !ok || oa.Model.Provider != "openai" {
		t.Fatalf("openai node: want provider openai + model gpt-4o")
	}
	if oa.InvocationSemantics.Type != "loop" || !oa.InvocationSemantics.VariableAtRuntime {
		t.Fatalf("agent (in a for loop) must be loop/variable_at_runtime, got %+v", oa.InvocationSemantics)
	}
}

// The floor is honest (10.5): a call whose model is NOT a string literal is unresolved + flagged, never
// guessed.
func TestPythonFloorUnresolvedModel(t *testing.T) {
	res := runPy(t, map[string]string{
		"m.py": `import anthropic
client = anthropic.Anthropic()
def f(chosen_model):
    client.messages.create(model=chosen_model, messages=[])
`,
	})
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(res.IR.Nodes))
	}
	if res.IR.Nodes[0].Model.ModelID != UnresolvedSentinel {
		t.Fatalf("runtime-variable model must be unresolved, got %q", res.IR.Nodes[0].Model.ModelID)
	}
	var flagged bool
	for _, a := range res.Report.AmbiguityFlags {
		if a.Field == "model" && a.Code == CodeModelUnresolved {
			flagged = true
		}
	}
	if !flagged {
		t.Fatalf("unresolved model must carry a P5 ambiguity flag, got %+v", res.Report.AmbiguityFlags)
	}
}

// A Python in-house wrapper is found ONLY via a user-declared entrypoint (FR2), proving the mechanism is
// language-neutral.
func TestPythonWrapperDeclared(t *testing.T) {
	files := map[string]string{
		"app/main.py": `from myco.llm import complete
def run():
    return complete("summarize this")
`,
	}
	// Without a declaration: the wrapper is invisible (myco.llm.complete matches no registry row).
	res := runPy(t, files)
	if len(res.IR.Nodes) != 0 {
		t.Fatalf("no declaration: want 0 nodes, got %d", len(res.IR.Nodes))
	}

	// Declare it -> the node appears, sourced from the declaration.
	root := t.TempDir()
	for rel, content := range files {
		mustWrite(t, filepath.Join(root, rel), content)
	}
	cfg := mustWrite2(t, root, "llm-eval.yaml", `
version: "1.0.0"
entrypoints:
  - symbol: "myco.llm.complete"
    provider: anthropic
    args:
      prompt: { index: 0 }
`)
	res2, err := Run(Options{Repo: root, ConfigPath: cfg, RepoURL: "local://py", CommitSHA: "0000000"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res2.IR.Nodes) != 1 {
		t.Fatalf("declared: want 1 node, got %d", len(res2.IR.Nodes))
	}
	if res2.Report.DetectionsBySource["declared"] != 1 {
		t.Fatalf("want detections_by_source.declared==1, got %+v", res2.Report.DetectionsBySource)
	}
}

func mustWrite2(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	mustWrite(t, p, content)
	return p
}

// I1 for tree-sitter: a Python file with a side-effectful top-level statement must NEVER run during
// discovery — tree-sitter only parses. This is the Python analogue of TestNoExecutionInitNeverFires.
func TestPythonNoExecution(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(t.TempDir(), "PY_SENTINEL_MUST_NOT_EXIST")
	mustWrite(t, filepath.Join(root, "evil.py"), "import os\nos.system('touch "+sentinel+"')\nopen('"+sentinel+"', 'w').close()\n")
	_ = runRepo2(t, root)
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("python top-level side effect FIRED — tree-sitter executed source (I1 violated)")
	}
}

func runRepo2(t *testing.T, root string) Result {
	t.Helper()
	res, err := Run(Options{Repo: root, RepoURL: "local://t", CommitSHA: "0000000"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}
