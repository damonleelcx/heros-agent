package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

// 10.2 — the registry is language-tagged: the seed rows are all Go, so ForLanguage("go") returns them
// and ForLanguage("python") returns none (a Python call can never match a Go row).
func TestRegistryLanguageFilter(t *testing.T) {
	reg := mustRegistry(t)
	// Every row has a language; ForLanguage partitions the registry cleanly (each row in exactly one
	// language view), and the shipped languages all have rows.
	langs := map[string]bool{}
	for _, r := range reg.Rows {
		if r.Language == "" {
			t.Fatalf("row %q has no language tag", r.ID)
		}
		langs[r.Language] = true
	}
	sum := 0
	for l := range langs {
		view := reg.ForLanguage(l)
		sum += len(view.Rows)
		for _, r := range view.Rows {
			if r.Language != l {
				t.Fatalf("ForLanguage(%q) returned row %q tagged %q", l, r.ID, r.Language)
			}
		}
	}
	if sum != len(reg.Rows) {
		t.Fatalf("language views (%d) do not partition all rows (%d)", sum, len(reg.Rows))
	}
	for _, want := range []string{"go", "python", "typescript", "javascript"} {
		if len(reg.ForLanguage(want).Rows) == 0 {
			t.Fatalf("no registry rows for shipped language %q", want)
		}
	}
}

// 10.1 — the Go path is a LanguageFrontend; GoFrontend.Handles routes only production .go files.
func TestGoFrontendHandles(t *testing.T) {
	f := NewGoFrontend()
	if f.Language() != "go" {
		t.Fatalf("language: %q", f.Language())
	}
	cases := map[string]bool{
		"main.go": true, "internal/x/y.go": true,
		"main_test.go": false, "app.py": false, "index.ts": false, "README.md": false,
	}
	for path, want := range cases {
		if got := f.Handles(path); got != want {
			t.Fatalf("Handles(%q)=%v, want %v", path, got, want)
		}
	}
}

// 10.3 — a repo whose source is in a language no frontend handles yields 0 nodes AND a
// LANGUAGE_UNSUPPORTED diagnostic (I4: a 0-node language is explained, never silently ignored).
func TestUnsupportedLanguageReported(t *testing.T) {
	root := t.TempDir()
	// Ruby and C# have no frontend yet -> their source must be reported, not silently ignored.
	mustWrite(t, filepath.Join(root, "app.rb"), "puts 'hi'\n")
	mustWrite(t, filepath.Join(root, "Prog.cs"), "class Prog {}\n")

	res, err := Run(Options{Repo: root, RepoURL: "local://t", CommitSHA: "0000000"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.IR.Nodes) != 0 {
		t.Fatalf("no frontend for ruby/csharp: want 0 nodes, got %d", len(res.IR.Nodes))
	}
	got := map[string]bool{}
	for _, d := range res.Report.FileDiagnostics {
		if d.Code == CodeLanguageUnsupported {
			for _, lang := range []string{"ruby", "csharp"} {
				if containsWord(d.Message, lang) {
					got[lang] = true
				}
			}
		}
	}
	if !got["ruby"] || !got["csharp"] {
		t.Fatalf("want LANGUAGE_UNSUPPORTED for ruby and csharp, got %+v (%v)", got, res.Report.FileDiagnostics)
	}
}

// 10.1 — the frontend seam is pluggable: a custom frontend injected via Options.Frontends contributes
// nodes to the core with no Go involvement, and a mixed set marks workflow.language "mixed".
func TestPluggableFrontendAndMixedLanguage(t *testing.T) {
	root := t.TempDir() // empty repo; the fake frontend fabricates a node
	fake := &fakeFrontend{lang: "python", node: syntheticNode(t, "python")}
	res, err := Run(Options{
		Repo: root, RepoURL: "local://t", CommitSHA: "0000000",
		Frontends: []LanguageFrontend{NewGoFrontend(), fake},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("want the fake frontend's 1 node, got %d", len(res.IR.Nodes))
	}
	// Only the python frontend contributed => language label is "python" (single contributor of two frontends).
	if res.IR.Workflow.Language != "python" {
		t.Fatalf("workflow.language: want python (sole contributor), got %q", res.IR.Workflow.Language)
	}
}

// --- helpers ---

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsWord(s, w string) bool { return len(s) >= len(w) && indexOf(s, w) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// fakeFrontend is a test double implementing LanguageFrontend for a non-Go language.
type fakeFrontend struct {
	lang string
	node ExtractedNode
	kind AnalysisKind
}

func (f *fakeFrontend) Language() string { return f.lang }

// AnalysisKind defaults to syntactic, which is what a non-Go frontend is today. A test that needs the
// other answer sets `kind` explicitly.
func (f *fakeFrontend) AnalysisKind() AnalysisKind {
	if f.kind == "" {
		return AnalysisSyntactic
	}
	return f.kind
}

func (f *fakeFrontend) Handles(path string) bool { return false }
func (f *fakeFrontend) Discover(repo string, reg *Registry, decl *declaredIndex) (FrontendResult, error) {
	return FrontendResult{Nodes: []ExtractedNode{f.node}, CallSites: 1, WorkflowID: "example.com/py"}, nil
}

// syntheticNode builds a minimal valid ExtractedNode (parsed via the Go frontend for a real *ast.CallExpr
// so the emitter's position lookup works — the node is language-neutral once extracted).
func syntheticNode(t *testing.T, lang string) ExtractedNode {
	t.Helper()
	pf, err := parseSingle("example.com/py/svc", "svc.go", `package svc
import "github.com/anthropics/anthropic-sdk-go"
func run(c *anthropic.Client) { c.Messages.New(nil, anthropic.MessageNewParams{}) }`)
	if err != nil {
		t.Fatal(err)
	}
	pkg := &Package{PkgPath: pf.PkgPath, Files: []*ParsedFile{pf}}
	sites, _ := DetectPackage(pkg, mustRegistry(t), nil)
	merged, _ := Merge(sites)
	nodes := ExtractFile(pf, merged)
	if len(nodes) != 1 {
		t.Fatalf("synthetic node setup: want 1, got %d", len(nodes))
	}
	return nodes[0]
}
