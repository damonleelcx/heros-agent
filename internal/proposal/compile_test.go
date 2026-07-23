package proposal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// materialize copies a testdata fixture (`.txt` sources) into a temp dir as real files and returns the
// root. Fixtures ship as `.txt` so a directory of SDK-importing `.go` files does not break the module
// build, mirroring the transform package's own fixture handling.
func materialize(t *testing.T, fixture string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for src, dst := range files {
		b, err := os.ReadFile(filepath.Join("testdata", fixture, src))
		if err != nil {
			t.Fatalf("read fixture %s/%s: %v", fixture, src, err)
		}
		if err := os.WriteFile(filepath.Join(root, dst), b, 0o600); err != nil {
			t.Fatalf("write %s: %v", dst, err)
		}
	}
	return root
}

func targetRoot(t *testing.T) string {
	return materialize(t, "target", map[string]string{
		"go.mod.txt": "go.mod", "pipeline.go.txt": "pipeline.go"})
}

func buildmodRoot(t *testing.T) string {
	return materialize(t, "buildmod", map[string]string{
		"go.mod.txt": "go.mod", "main.go.txt": "main.go"})
}

// fixedResolver returns a hand-built Resolved (like the transform tests), so the codemod can be driven
// without a live registry. It ignores the spec — the determinism it proves is the codemod's, not the
// resolver's.
type fixedResolver struct{ r *variantspec.Resolved }

func (f fixedResolver) Resolve(*variantspec.VariantSpec) (*variantspec.Resolved, error) {
	return f.r, nil
}

func modelEntry(id string) *registry.ModelEntry {
	zero := 0.0
	return &registry.ModelEntry{VersionID: strings.Repeat("a", 64), Name: "m",
		Spec: registry.ModelSpec{Provider: "anthropic", ModelID: id,
			Params: registry.ModelParams{Temperature: &zero}}}
}

// §1b.3 / §7.2: re-compiling the same candidate against the same source yields a BYTE-IDENTICAL diff
// (content-hashed to the same value). This drives the real codemod (transform.Generate).
func TestCompile_CodemodIsDeterministic(t *testing.T) {
	root := targetRoot(t)
	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		t.Fatalf("IndexGoCallSites: %v", err)
	}
	var classifyID string
	for id, s := range sites {
		if strings.Contains(readSymbol(t, root, s), "classify") {
			classifyID = id
		}
	}
	if classifyID == "" {
		t.Fatal("fixture node `classify` not found")
	}

	resolved := &variantspec.Resolved{
		ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1", Language: "go",
		Overrides: map[string]variantspec.ResolvedOverride{classifyID: {Model: modelEntry("claude-sonnet-5")}},
	}
	comp := Compiler{Resolver: fixedResolver{resolved}, Root: root, Build: okBuild{}}

	cand := Candidate{Operator: OpModelUpgrade, NodeID: classifyID, Spec: baseSpec()}
	a, err := comp.Compile(context.Background(), cand)
	if err != nil {
		t.Fatalf("compile 1: %v", err)
	}
	b, err := comp.Compile(context.Background(), cand)
	if err != nil {
		t.Fatalf("compile 2: %v", err)
	}
	if a.DiffHash == "" {
		t.Fatal("empty diff — the codemod rewrote nothing")
	}
	if a.DiffHash != b.DiffHash {
		t.Errorf("codemod is non-deterministic: %s != %s", a.DiffHash, b.DiffHash)
	}
	if string(a.Patch.Diff) != string(b.Patch.Diff) {
		t.Error("the diff bytes are not identical across recompiles")
	}
	// The diff changes only the targeted call site's model argument.
	if !strings.Contains(string(a.Patch.Diff), "claude-sonnet-5") {
		t.Errorf("the diff did not rewrite the model argument:\n%s", a.Patch.Diff)
	}
}

// §1b.2 / §1b.4 / §7.2: a candidate whose diff fails to build is rejected before surfacing (build
// gate). A building candidate proceeds. This runs a REAL `go build` on an isolated copy.
func TestBuildGate_RejectsNonBuildingDiff(t *testing.T) {
	root := buildmodRoot(t)
	checker := GoBuildChecker{Root: root}

	good := &transform.Patch{ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1",
		Files: map[string][]byte{"main.go": []byte(goodMain)}}
	bad := &transform.Patch{ConfigHash: strings.Repeat("d", 64), SourceRevision: "rev1",
		Files: map[string][]byte{"main.go": []byte(badMain)}}

	gr, err := checker.Check(context.Background(), good)
	if err != nil {
		t.Fatalf("check good: %v", err)
	}
	if !gr.Builds {
		t.Fatalf("a valid-Go diff was reported as non-building:\n%s", gr.Log)
	}
	br, err := checker.Check(context.Background(), bad)
	if err != nil {
		t.Fatalf("check bad: %v", err)
	}
	if br.Builds {
		t.Fatal("a type-erroring diff was reported as building")
	}
	if !strings.Contains(br.Log, "cannot use") && !strings.Contains(br.Log, "mismatched") && br.Log == "" {
		t.Errorf("build failure log is empty; a rejection must carry the compiler diagnostic")
	}
}

// §1b.2: a build_failed compiled candidate is not surfaceable — the ranker/verification gate read this.
func TestCompile_BuildFailedIsNotSurfaceable(t *testing.T) {
	root := buildmodRoot(t)
	sites, _ := discovery.IndexGoCallSites(root, nil) // buildmod has no SDK call sites; that's fine here
	_ = sites
	resolved := &variantspec.Resolved{ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1", Language: "go"}
	comp := Compiler{Resolver: fixedResolver{resolved}, Root: root, Build: failBuild{log: "type error"}}
	got, err := comp.Compile(context.Background(), Candidate{Operator: OpModelUpgrade, NodeID: "x", Spec: baseSpec()})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got.BuildStatus != BuildFailed {
		t.Errorf("want build_failed, got %s", got.BuildStatus)
	}
	if got.Surfaceable() {
		t.Error("a build_failed candidate must not be surfaceable")
	}
}

// §1b.1: the build check operates on an isolated copy — the user's working tree is never mutated.
func TestBuildGate_DoesNotMutateUserTree(t *testing.T) {
	root := buildmodRoot(t)
	before, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	checker := GoBuildChecker{Root: root}
	patch := &transform.Patch{ConfigHash: strings.Repeat("c", 64), SourceRevision: "rev1",
		Files: map[string][]byte{"main.go": []byte(goodMain)}}
	if _, err := checker.Check(context.Background(), patch); err != nil {
		t.Fatalf("check: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the build check mutated the user's working tree in place")
	}
}

// ── build-gate stubs and fixture sources ─────────────────────────────────────────────────────────

// okBuild passes every diff — used where the codemod (not the compiler) is under test.
type okBuild struct{}

func (okBuild) Check(context.Context, *transform.Patch) (BuildResult, error) {
	return BuildResult{Builds: true}, nil
}

type failBuild struct{ log string }

func (f failBuild) Check(context.Context, *transform.Patch) (BuildResult, error) {
	return BuildResult{Builds: false, Log: f.log}, nil
}

const goodMain = `package main

func classify() string { return "opus" }

func main() { _ = classify() }
`

// badMain assigns a string to an int — a genuine type error the Go compiler rejects.
const badMain = `package main

func classify() int { return "opus" }

func main() { _ = classify() }
`

func readSymbol(t *testing.T, root string, s discovery.GoCallSite) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, s.FileRel))
	if err != nil {
		t.Fatalf("read %s: %v", s.FileRel, err)
	}
	lines := strings.Split(string(b), "\n")
	for i := s.LineStart - 1; i >= 0 && i < len(lines); i-- {
		if strings.HasPrefix(lines[i], "func ") {
			return lines[i]
		}
	}
	return ""
}
