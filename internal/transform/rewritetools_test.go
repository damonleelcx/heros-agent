package transform

import (
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P14 §6 — tool pruning at the call site.

// prune resolves a tool selection at one fixture node and returns the rewritten file.
func prune(t *testing.T, root, nodeID string, kept ...string) (*Patch, error) {
	t.Helper()
	return Generate(resolvedWith(map[string]variantspec.ResolvedOverride{
		nodeID: {ToolSelection: kept},
	}), root)
}

// ── 6.1 a pruned tool is DELETED at the call site ────────────────────────────────────────────────

func TestPrunedToolDeletedAtCallSite(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	t.Run("a middle element", func(t *testing.T) {
		p, err := prune(t, root, ids["prunable"], "weatherTool", "searchTool")
		if err != nil {
			t.Fatalf("prune: %v", err)
		}
		got := string(p.Files["pipeline.go"])
		if !strings.Contains(got, "{weatherTool, searchTool}") {
			t.Errorf("want the surviving tools left as a well-formed list, got:\n%s", p.Diff)
		}
		if strings.Contains(got, "{weatherTool, sqlTool, searchTool}") {
			t.Errorf("the pruned tool is still declared at this call site:\n%s", p.Diff)
		}
		if len(p.Touched) != 1 || p.Touched[0].Dim != "tools" {
			t.Errorf("the patch must record the tools dimension as touched, got %+v", p.Touched)
		}
	})

	t.Run("the last element", func(t *testing.T) {
		// The separator to remove is the one BEFORE it — there is no trailing comma to take.
		p, err := prune(t, root, ids["prunable"], "weatherTool", "sqlTool")
		if err != nil {
			t.Fatalf("prune: %v", err)
		}
		if !strings.Contains(string(p.Files["pipeline.go"]), "{weatherTool, sqlTool}") {
			t.Errorf("dropping the last element left a malformed list:\n%s", p.Diff)
		}
	})

	t.Run("the edit is a DELETION, not a construction", func(t *testing.T) {
		p, err := prune(t, root, ids["prunable"], "weatherTool", "searchTool")
		if err != nil {
			t.Fatalf("prune: %v", err)
		}
		for _, line := range strings.Split(string(p.Diff), "\n") {
			if strings.HasPrefix(line, "+") && strings.Contains(line, "ToolParam") {
				t.Errorf("a prune constructed a tool value; it must only delete an element the call site "+
					"already wrote:\n%s", p.Diff)
			}
		}
	})
}

// A deletion must not change the file's LINE COUNT. TouchedDimension.Line has to stay valid in both
// files so the build gate can attribute a compiler error to the rewrite that caused it, and "only the
// targeted lines changed" is only checkable while the lines line up.
func TestPruneNeverChangesTheLineCount(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	for name, id := range map[string]string{"one line": ids["prunable"], "one tool per line": ids["stackedtools"]} {
		t.Run(name, func(t *testing.T) {
			kept := []string{"weatherTool"}
			if name == "one line" {
				kept = []string{"weatherTool", "searchTool"}
			}
			p, err := prune(t, root, id, kept...)
			if err != nil {
				t.Fatalf("prune: %v", err)
			}
			before := readFixture(t, root)
			after := string(p.Files["pipeline.go"])
			if lines(before) != lines(after) {
				t.Fatalf("the prune changed the file from %d to %d lines", lines(before), lines(after))
			}
			// The prune must have actually removed something — the fixture declares sqlTool at two
			// different call sites, so count rather than merely look for the name.
			if strings.Count(after, "sqlTool") != strings.Count(before, "sqlTool")-1 {
				t.Errorf("the prune did not delete exactly one sqlTool declaration:\n%s", p.Diff)
			}
		})
	}
}

// ── 6.2 🔴 a dynamically-assembled set is REFUSED ────────────────────────────────────────────────

func TestDynamicToolSetRefused(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)

	p, err := prune(t, root, ids["dynamictools"], "weatherTool")
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("a prune over a runtime-assembled tool set must refuse, got %v", err)
	}
	if p != nil {
		t.Fatal("a refusal must emit no diff — not even a partial one")
	}
	var re *RewriteError
	if !errors.As(err, &re) {
		t.Fatalf("want a *RewriteError, got %T", err)
	}
	if re.NodeID != ids["dynamictools"] || re.Dim != "tools" {
		t.Errorf("the refusal must name node and dimension, got node=%q dim=%q", re.NodeID, re.Dim)
	}
	if !strings.Contains(re.Detail, "runtime") {
		t.Errorf("the refusal must say WHY, so the user can act on it: %s", re.Detail)
	}
}

// The same refusal for every tree-sitter language, which records no tool split at all. A selection
// resolve accepted must not silently delete nothing here.
//
// It runs against a REAL Python fixture rather than a Go tree relabelled "python": a Go tree under a
// Python language label fails earlier, at "no Python call site with this node_id", which would let this
// test pass without the tools dimension ever being dispatched.
func TestSpanToolPruneRefuses(t *testing.T) {
	root := spanTarget(t, "pipeline.py", pyModelSrc)
	id := onlyNode(t, root, "python")

	msg := refusalFor(t, resolvedIn("python", map[string]variantspec.ResolvedOverride{
		id: {ToolSelection: []string{"weatherTool"}},
	}), root)
	mustContain(t, msg, "no tool split", "that the frontend, not the engine, is what is missing")
	mustContain(t, msg, "REFUSED rather than applied to nothing", "that this is a refusal, not a silent no-op")
}

// A selection the call site cannot satisfy is refused rather than partially applied: reaching here
// means the tree and the IR the selection was validated against disagree.
func TestPruneRefusesWhenTreeAndIRDisagree(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	_, err := prune(t, root, ids["prunable"], "phantomTool")
	if !errors.Is(err, ErrUnsafeRewrite) {
		t.Fatalf("keeping a tool the call site does not declare must refuse, got %v", err)
	}
	if !strings.Contains(err.Error(), "phantomTool") {
		t.Errorf("the refusal must name the tool it could not find: %v", err)
	}
}

// Determinism: the same selection against the same tree yields a byte-identical diff.
func TestToolPruneIsDeterministic(t *testing.T) {
	root := newTarget(t)
	ids := nodeIDs(t, root)
	var first string
	for i := 0; i < 5; i++ {
		p, err := prune(t, root, ids["prunable"], "searchTool", "weatherTool")
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if i == 0 {
			first = p.DiffHash
			continue
		}
		if p.DiffHash != first {
			t.Fatalf("run %d diff hash %s != run 0 %s", i, p.DiffHash, first)
		}
	}
}

func readFixture(t *testing.T, root string) string {
	t.Helper()
	b, err := readFile(root, "pipeline.go")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(b)
}

func lines(s string) int { return strings.Count(s, "\n") }
