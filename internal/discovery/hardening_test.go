package discovery

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tempRepo writes a set of files (rel path -> content) into a fresh temp repo with a go.mod and returns
// the repo root.
func tempRepo(t *testing.T, module string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module "+module+"\n\ngo 1.22\n")
	for rel, content := range files {
		write(rel, content)
	}
	return root
}

func runRepo(t *testing.T, root string) Result {
	t.Helper()
	res, err := Run(Options{Repo: root, RepoURL: "local://t", CommitSHA: "0000000"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// 7.1 — No-execution: a target file with an init() side effect must NEVER fire during discovery
// (discovery only parses text; it never compiles or runs the repo). NFR1 / invariant I1.
func TestNoExecutionInitNeverFires(t *testing.T) {
	sentinel := filepath.Join(t.TempDir(), "SENTINEL_MUST_NOT_EXIST")
	root := tempRepo(t, "example.com/evil", map[string]string{
		"main.go": `package main
import "os"
func init() { _ = os.WriteFile(` + "`" + sentinel + "`" + `, []byte("fired"), 0644) }
func main() {}
`,
	})
	_ = runRepo(t, root)
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("init() side effect FIRED — discovery executed target code (I1 violated)")
	}
}

// 7.2 — Least privilege: discovery is read-only; it must not create/modify/delete any file in the repo.
func TestReadOnlyNoRepoMutation(t *testing.T) {
	root := tempRepo(t, "example.com/ro", map[string]string{
		"main.go": `package main
import "github.com/anthropics/anthropic-sdk-go"
func main() { var c *anthropic.Client; c.Messages.New(nil, anthropic.MessageNewParams{}) }
`,
	})
	before := snapshotDir(t, root)
	_ = runRepo(t, root)
	after := snapshotDir(t, root)
	if before != after {
		t.Fatalf("discovery mutated the repo (read-only violated):\nbefore=%s\nafter =%s", before, after)
	}
}

// snapshotDir returns a stable digest of a directory's file paths + contents.
func snapshotDir(t *testing.T, root string) string {
	t.Helper()
	h := sha256.New()
	var paths []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// stable order
	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			if paths[j] < paths[i] {
				paths[i], paths[j] = paths[j], paths[i]
			}
		}
	}
	for _, p := range paths {
		b, _ := os.ReadFile(p)
		h.Write([]byte(p))
		h.Write(b)
	}
	return string(h.Sum(nil))
}

// 7.3 — Robustness: a deeply-nested expression must degrade to a per-file diagnostic (or bounded walk),
// never a crash; sibling files are still discovered.
func TestDeepNestingNoCrash(t *testing.T) {
	deep := "package p\nvar x = " + strings.Repeat("(", 60000) + "1" + strings.Repeat(")", 60000) + "\n"
	root := tempRepo(t, "example.com/deep", map[string]string{
		"hostile.go": deep,
		"good.go": `package p
import "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
func good(c *bedrockruntime.Client) { c.Converse(nil, &bedrockruntime.ConverseInput{}) }
`,
	})
	res := runRepo(t, root) // must not panic / overflow
	// The good file is still discovered.
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("want the good Converse node discovered, got %d nodes", len(res.IR.Nodes))
	}
	// The hostile file produced a diagnostic (parse error or bounded-walk), not a silent success.
	var flagged bool
	for _, d := range res.Report.FileDiagnostics {
		if strings.Contains(d.File, "hostile.go") {
			flagged = true
		}
	}
	if !flagged {
		t.Fatalf("deeply-nested file should be flagged in diagnostics, got %+v", res.Report.FileDiagnostics)
	}
}

// 7.3 — Robustness: a huge string literal is handled without hanging or exhausting resources.
func TestHugeLiteralBounded(t *testing.T) {
	big := strings.Repeat("A", 2_000_000)
	root := tempRepo(t, "example.com/big", map[string]string{
		"main.go": `package main
import "github.com/anthropics/anthropic-sdk-go"
func main() { var c *anthropic.Client; c.Messages.New(nil, anthropic.MessageNewParams{Model: "` + big + `"}) }
`,
	})
	res := runRepo(t, root)
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(res.IR.Nodes))
	}
}

// 7.3 — Robustness: a symlink cycle must not be followed (no infinite loop) and is reported.
func TestSymlinkCycleSkipped(t *testing.T) {
	root := tempRepo(t, "example.com/sym", map[string]string{
		"main.go": `package main
import "github.com/anthropics/anthropic-sdk-go"
func main() { var c *anthropic.Client; c.Messages.New(nil, anthropic.MessageNewParams{}) }
`,
	})
	// Create a symlink cycle: <root>/loop -> <root>.
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	res := runRepo(t, root) // must terminate, not hang
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("want the real node discovered despite the symlink cycle, got %d", len(res.IR.Nodes))
	}
}

// 7.4 — Adversarial: a FrameworkReader that panics is recovered into a diagnostic; the run completes and
// the non-framework nodes are still emitted (I7 / doc 08 F10). This is the guard the review added.
type panickingReader struct{}

func (panickingReader) Name() string                         { return "boom" }
func (panickingReader) Detect(*Package) (string, bool, bool) { return "v0", true, true }
func (panickingReader) ReadDAG(*Package) (FrameworkGraph, []Diagnostic) {
	panic("reader blew up on drifted input")
}

func TestFrameworkReaderPanicRecovered(t *testing.T) {
	root := tempRepo(t, "example.com/fw", map[string]string{
		"main.go": `package main
import "github.com/anthropics/anthropic-sdk-go"
func main() { var c *anthropic.Client; c.Messages.New(nil, anthropic.MessageNewParams{}) }
`,
	})
	res, err := Run(Options{Repo: root, RepoURL: "local://t", CommitSHA: "0000000", Frameworks: []FrameworkReader{panickingReader{}}})
	if err != nil {
		t.Fatalf("a panicking reader must not fail the run: %v", err)
	}
	if len(res.IR.Nodes) != 1 {
		t.Fatalf("non-framework node must still be emitted, got %d", len(res.IR.Nodes))
	}
	var recovered bool
	for _, d := range res.Report.FileDiagnostics {
		if d.Code == CodeFrameworkReaderErr {
			recovered = true
		}
	}
	if !recovered {
		t.Fatalf("want a FRAMEWORK_READER_ERROR diagnostic from the recover guard, got %+v", res.Report.FileDiagnostics)
	}
}
