package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// TASK 2.6: an inserted adapter (a GenerateTransform patch) is applied to an ISOLATED worktree, builds
// there, and is revertible by a single `git revert` — never touching the user's working tree in place.
// This ties the P5 adapter-materialisation flow to the existing ADR-001 apply/build/revert machinery.
func TestApply_InsertedAdapterBuildsInIsolationAndReverts(t *testing.T) {
	src, rev := newSourceRepo(t)
	a := newApplier(t, src)
	ctx := context.Background()
	before := hashTree(t, src)

	resolved := &variantspec.Resolved{Language: "go", SourceRevision: rev, ConfigHash: hashA}
	spec := &variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: rev,
		InsertedAdapters: []variantspec.InsertedAdapter{{
			AdapterNodeID: "adapter:rename:A->B", FromNodeID: "A", ToNodeID: "B", CatalogKind: "rename",
			Params:   map[string]any{"renames": []map[string]any{{"from": "answer", "to": "response"}}},
			InSchema: map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}},
			OutSchema: map[string]any{"type": "object",
				"properties": map[string]any{"response": map[string]any{"type": "string"}}, "required": []any{"response"}},
		}},
	}
	patch, err := transform.GenerateTransform(resolved, spec, src)
	if err != nil {
		t.Fatalf("GenerateTransform: %v", err)
	}

	got, err := a.Apply(ctx, patch)
	if err != nil {
		t.Fatalf("Apply: %v\n%s", err, func() string {
			if got != nil {
				return got.BuildLog
			}
			return ""
		}())
	}
	if got.Status != StatusBuilt {
		t.Fatalf("adapter patch must build, got %q\n%s", got.Status, got.BuildLog)
	}
	if got.Dir == src {
		t.Fatal("adapter was applied to the user's own tree")
	}
	// The generated adapter file exists in the isolated worktree and compiles as part of the module.
	adapterPath := filepath.Join(got.Dir, transform.AdapterPackageDir, "adapter_A_B_rename.go")
	if b, err := os.ReadFile(adapterPath); err != nil || !strings.Contains(string(b), "response") {
		t.Fatalf("adapter source missing from worktree: %v", err)
	}
	// The user's tree is byte-for-byte unchanged.
	if after := hashTree(t, src); after != before {
		t.Fatalf("the user's working tree changed")
	}

	// Revertible by a single git revert.
	if err := a.Revert(ctx, got); err != nil {
		t.Fatalf("Revert: %v", err)
	}
}
