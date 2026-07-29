package transform

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P18 §9 — the memory import edit class, and the end-to-end materialization it unblocks.
//
// This file is where the phase's claim becomes checkable: a memory strategy is materialized through the
// REAL Generate, the resulting tree is EXECUTED by python3, and the agent is observed actually
// remembering across calls. Everything before this was a component working in isolation.

// TestMemoryMaterializesEndToEnd — task 9.2. The whole path: Generate → files → run it.
func TestMemoryMaterializesEndToEnd(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyMemorySrc)
	id := onlyNode(t, root, "python")

	p, err := Generate(resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: memoryOverride(t, "scratchpad"),
	}), root)
	if err != nil {
		t.Fatalf("a complete memory cell must materialize: %v", err)
	}

	after := string(p.Files["pipeline.py"])
	t.Run("the call site reads and writes memory", func(t *testing.T) {
		for _, want := range []string{"import agentmem", "messages=agentmem.recall(", "agentmem.record("} {
			if !strings.Contains(after, want) {
				t.Errorf("the materialized source is missing %q:\n%s", want, after)
			}
		}
	})

	t.Run("the artifact ships in the SAME patch", func(t *testing.T) {
		// One revert restores everything. A module that shipped separately would leave the rewritten call
		// site importing something that is not there.
		for _, want := range []string{pyMemoryModulePath, memoryDocPath} {
			if _, ok := p.Files[want]; !ok {
				t.Errorf("the patch does not carry %s. Files: %v", want, fileNames(p.Files))
			}
		}
	})

	t.Run("attribution shifted with the import", func(t *testing.T) {
		// 🔴 The import adds a line, so every recorded line below it moves down by one in the TRANSFORMED
		// file — which is the file the build gate compiles. If this did not shift, a compiler diagnostic
		// would be attributed to the wrong node, which is one of the two things the line-count invariant
		// existed to protect.
		var line int
		for _, td := range p.Touched {
			if td.NodeID == id && td.Dim == string(variantspec.DimMemory) {
				line = td.Line
			}
		}
		if line == 0 {
			t.Fatal("no touched entry for the memory dimension")
		}
		lines := strings.Split(after, "\n")
		if line < 1 || line > len(lines) {
			t.Fatalf("recorded line %d is outside the transformed file", line)
		}
		if !strings.Contains(lines[line-1], "client.chat.completions.create") {
			t.Errorf("recorded line %d of the TRANSFORMED file is %q, which is not the call site; a "+
				"compiler error would be blamed on the wrong node", line, lines[line-1])
		}
	})

	// 🔴 THE test: run the materialized agent and watch it remember.
	t.Run("the materialized agent actually remembers", func(t *testing.T) {
		if _, err := exec.LookPath("python3"); err != nil {
			t.Skipf("python3 not available: %v", err)
		}
		dir := t.TempDir()
		for path, body := range p.Files {
			full := filepath.Join(dir, path)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, body, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		// A stub `openai` module: the point is what the REWRITTEN CALL SITE passes and records, not what a
		// provider returns. It echoes the messages it was given so the harness can see memory accumulate.
		stub := `
class _Completions:
    def create(self, model=None, messages=None):
        return {"role": "assistant", "content": "saw %d message(s)" % len(messages)}
class _Chat:
    def __init__(self): self.completions = _Completions()
class OpenAI:
    def __init__(self): self.chat = _Chat()
`
		if err := os.WriteFile(filepath.Join(dir, "openai.py"), []byte(stub), 0o600); err != nil {
			t.Fatal(err)
		}

		const harness = `
import sys
sys.path.insert(0, sys.argv[1])
import agentmem, pipeline
agentmem.set_session("s1")
print(pipeline.chat("first")["content"])
print(pipeline.chat("second")["content"])
print(pipeline.chat("third")["content"])
`
		out, err := exec.Command("python3", "-c", harness, dir).CombinedOutput()
		if err != nil {
			t.Fatalf("the materialized agent does not run: %v\n%s\n--- pipeline.py ---\n%s", err, out, after)
		}
		got := strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", " "))

		// Call 1 sends 1 message (no memory yet). Call 2 sends 3 (2 recorded + 1 new). Call 3 sends 5,
		// clamped to max_entries=... — the fixture uses scratchpad, whose bound is asserted separately.
		// What matters HERE is that the count GROWS: the store is being written and read.
		var counts []string
		for i, f := range got {
			if f == "saw" && i+1 < len(got) {
				counts = append(counts, got[i+1])
			}
		}
		if len(counts) != 3 {
			t.Fatalf("expected three calls, parsed %v from %q", counts, out)
		}
		if counts[0] != "1" {
			t.Errorf("the first call sent %s message(s), want 1 — an empty store recalls nothing", counts[0])
		}
		if counts[1] == "1" || counts[2] == "1" {
			t.Fatalf("the agent is not remembering: every call sent one message (%v).\nThe recall is "+
				"reading a store the record never filled — which is exactly the half-materialization "+
				"decisions.md D2 forbids, reached at RUN TIME instead of at codemod time.", counts)
		}
	})
}

// TestMemoryImportClassAdmitsOnlyItsOwnImport — task 9.1 🔴. The class must not become a general
// "insert arbitrary lines" escape hatch, which is the thing a future contributor would most naturally
// reach for it as.
func TestMemoryImportClassAdmitsOnlyItsOwnImport(t *testing.T) {
	src := []byte("import openai\n\nx = 1\n")
	allowed := map[int]bool{1: true}

	t.Run("a well-formed import is admitted", func(t *testing.T) {
		e := edit{Start: 0, End: 0, New: "import agentmem\n", NodeID: "n", Dim: "memory", Import: true}
		out, err := applyEdits(src, []edit{e})
		if err != nil {
			t.Fatal(err)
		}
		if err := gateMemoryImport("f.py", src, out, []edit{e}, allowed); err != nil {
			t.Fatalf("a single import line was rejected: %v", err)
		}
	})

	t.Run("arbitrary inserted text is refused", func(t *testing.T) {
		// 🚫 The escape-hatch case. Without the pattern check this class would let any rewriter insert any
		// code anywhere, past the untargeted-line rule.
		for _, bad := range []string{
			"import agentmem\nos.system('rm -rf /')\n",
			"x = evil()\n",
			"import agentmem; os.system('x')\n",
			"from agentmem import *\n",
		} {
			e := edit{Start: 0, End: 0, New: bad, NodeID: "n", Dim: "memory", Import: true}
			out, _ := applyEdits(src, []edit{e})
			if err := gateMemoryImport("f.py", src, out, []edit{e}, allowed); err == nil {
				t.Errorf("the gate admitted %q; this class exists to add ONE import, and anything looser "+
					"makes it the insert-anything hatch the untargeted-line rule exists to prevent", bad)
			}
		}
	})

	t.Run("a replacement disguised as an import is refused", func(t *testing.T) {
		e := edit{Start: 0, End: 5, New: "import agentmem\n", NodeID: "n", Dim: "memory", Import: true}
		out, _ := applyEdits(src, []edit{e})
		if err := gateMemoryImport("f.py", src, out, []edit{e}, allowed); err == nil {
			t.Error("the gate admitted an edit that REPLACES bytes while claiming to insert an import; " +
				"this class may add a line and may not remove one")
		}
	})

	t.Run("two imports are refused", func(t *testing.T) {
		e := edit{Start: 0, End: 0, New: "import agentmem\n", NodeID: "n", Dim: "memory", Import: true}
		out, _ := applyEdits(src, []edit{e, e})
		if err := gateMemoryImport("f.py", src, out, []edit{e, e}, allowed); err == nil {
			t.Error("the gate admitted two import edits; the file would gain a duplicate import")
		}
	})

	t.Run("an untargeted change riding along is refused", func(t *testing.T) {
		// 🔴 The core assertion: adding an import does not license editing anything else. Here the import
		// is well-formed, but a second edit changes a line nobody targeted.
		imp := edit{Start: 0, End: 0, New: "import agentmem\n", NodeID: "n", Dim: "memory", Import: true}
		sneak := edit{Start: 16, End: 21, New: "y = 2", NodeID: "n", Dim: "memory"}
		out, err := applyEdits(src, []edit{imp, sneak})
		if err != nil {
			t.Fatal(err)
		}
		if err := gateMemoryImport("f.py", src, out, []edit{imp, sneak}, allowed); err == nil {
			t.Errorf("the gate admitted an untargeted edit alongside the import:\n%s", out)
		}
	})
}

// TestMemoryImportPlacement — 🔴 the two placements that would be silently wrong.
func TestMemoryImportPlacement(t *testing.T) {
	t.Run("it goes below a module docstring", func(t *testing.T) {
		src := []byte("\"\"\"The module doc.\"\"\"\n\nimport openai\n")
		e, err := memoryImportEdit(src, "n", "memory", "python", "")
		if err != nil {
			t.Fatalf("memoryImportEdit: %v", err)
		}
		out, err := applyEdits(src, []edit{e})
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(out), "\n")
		if !strings.HasPrefix(lines[0], `"""`) {
			t.Fatalf("the import landed ABOVE the module docstring:\n%s\nA docstring is only __doc__ when "+
				"it is the first statement; an import above it silently makes it a bare string expression "+
				"and the module loses its documentation with no error anywhere.", out)
		}
	})

	t.Run("it goes below a __future__ import", func(t *testing.T) {
		src := []byte("from __future__ import annotations\n\nimport openai\n")
		e, err := memoryImportEdit(src, "n", "memory", "python", "")
		if err != nil {
			t.Fatalf("memoryImportEdit: %v", err)
		}
		out, err := applyEdits(src, []edit{e})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(out), "from __future__") {
			t.Fatalf("the import landed ABOVE a __future__ import:\n%s\nA future import must be the first "+
				"statement; anything above it is a SyntaxError.", out)
		}
		assertPythonParses(t, string(out))
	})

	t.Run("a file with no import is refused rather than guessed at", func(t *testing.T) {
		src := []byte("x = 1\ny = 2\n")
		if _, err := memoryImportEdit(src, "n", "memory", "python", ""); err == nil {
			t.Error("a file with no top-level import got an import inserted at a guessed position")
		} else if !errors.Is(err, ErrUnsafeRewrite) {
			t.Errorf("err = %v, want ErrUnsafeRewrite", err)
		}
	})
}

// TestMemoryImportIsNotDuplicatedPerNode — two memory nodes in ONE file get ONE import.
func TestMemoryImportIsNotDuplicatedPerNode(t *testing.T) {
	const twoNodes = `import openai

client = openai.OpenAI()


def first(q):
    a = client.chat.completions.create(
        model="gpt-4o",
        messages=[{"role": "user", "content": q}],
    )
    return a


def second(q):
    b = client.chat.completions.create(
        model="gpt-4o",
        messages=[{"role": "user", "content": q}],
    )
    return b
`
	root := spanTarget(t, "pipeline.py", twoNodes)
	sites := spanSites(t, root, "python")
	if len(sites) != 2 {
		t.Fatalf("fixture has %d call sites, want 2", len(sites))
	}
	overrides := map[string]variantspec.ResolvedOverride{}
	for id := range sites {
		overrides[id] = memoryOverride(t, "scratchpad")
	}

	p, err := Generate(resolvedIn("python", overrides), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	after := string(p.Files["pipeline.py"])
	if n := strings.Count(after, "import agentmem"); n != 1 {
		t.Fatalf("the file carries %d `import agentmem` lines, want exactly 1:\n%s", n, after)
	}
	assertPythonParses(t, after)
}
