package hostedcompile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/proposalstore"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/sourceingest"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// endtoend_test.go compiles a real proposal over a real Go tree with the real codemod, and asserts the
// bytes that come out are a diff a human could review.
//
// Everything before this file tests the pieces. This is the only place the claim "the platform compiles
// proposals into diffs" is actually checked, and it is checked the way it would fail: a store row and a
// source tree in, a unified diff out, with the model argument at the named call site rewritten.

// target materializes the transform engine's own Go fixture — a two-file module with real LLM call
// sites — as the customer's pushed snapshot.
func target(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for src, dst := range map[string]string{
		"../transform/testdata/target/go.mod.txt":      "go.mod",
		"../transform/testdata/target/pipeline.go.txt": "pipeline.go",
		"../transform/testdata/target/wiring.go.txt":   "wiring.go",
	} {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read fixture %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(root, dst), b, 0o600); err != nil {
			t.Fatalf("write %s: %v", dst, err)
		}
	}
	return root
}

// treeRunner serves a fixed tree and derives its IR the way the platform does — through discovery, so
// the node ids the codemod anchors on are the ones discovery produces rather than ones a test invented.
type treeRunner struct{ root string }

func (r treeRunner) WithSource(_ context.Context, ref sourceingest.Ref, fn func(string, *discovery.IR) error) error {
	out, err := discovery.Run(discovery.Options{
		Repo: r.root, WorkflowID: ref.WorkflowID, CommitSHA: ref.SourceRevision,
	})
	if err != nil {
		return err
	}
	ir := out.IR
	return fn(r.root, &ir)
}

// fixedRegistries resolves the one model ref the candidate selects. It is the seam variantspec.Resolve
// reads through; a real deployment passes registry.Store.
type fixedRegistries struct{ model *registry.ModelEntry }

func (f fixedRegistries) ResolveModel(context.Context, string) (*registry.ModelEntry, error) {
	return f.model, nil
}
func (fixedRegistries) ResolvePrompt(context.Context, string) (*registry.PromptEntry, error) {
	return nil, nil
}
func (fixedRegistries) ResolveSkill(context.Context, string) (*registry.SkillEntry, error) {
	return nil, nil
}
func (fixedRegistries) ResolveContextPolicy(context.Context, string) (*registry.ContextEntry, error) {
	return nil, nil
}
func (fixedRegistries) ResolveMemory(context.Context, string) (*registry.MemoryEntry, error) {
	return nil, nil
}
func (fixedRegistries) ResolveHarness(context.Context, string) (*registry.HarnessEntry, error) {
	return nil, nil
}

// nodeIDFor returns the discovered node id of the call site in the named enclosing function.
func nodeIDFor(t *testing.T, root, symbol string) string {
	t.Helper()
	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		t.Fatalf("IndexGoCallSites: %v", err)
	}
	for id, s := range sites {
		b, err := os.ReadFile(filepath.Join(root, s.FileRel))
		if err != nil {
			t.Fatalf("read %s: %v", s.FileRel, err)
		}
		lines := strings.Split(string(b), "\n")
		for i := s.LineStart - 1; i >= 0; i-- {
			if strings.HasPrefix(lines[i], "func ") {
				if strings.HasPrefix(strings.TrimPrefix(lines[i], "func "), symbol+"(") {
					return id
				}
				break
			}
		}
	}
	t.Fatalf("no discovered call site inside func %s", symbol)
	return ""
}

// 🔴 THE CLAIM. A stored proposal plus a pushed snapshot produces a reviewable unified diff, and the
// resolved config hash replaces the candidate identity the generator recorded.
func TestAStoredProposalCompilesIntoAReviewableDiff(t *testing.T) {
	root := target(t)
	node := nodeIDFor(t, root, "classify")
	const modelRef = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	blobs := newBlobs()
	spec := variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev1",
		// NO ORDER, exactly as proposalgen stores it. The compiler fills it from the IR — see
		// compileOne. A spec that supplies its own is proposing a rewiring, which this one is not.
		Nodes: map[string]variantspec.NodeOverride{node: {ModelRef: modelRef}},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	specHash, err := blobs.Put(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}

	sc := scored("p1", specHash, "", proposalstore.BuildUnbuilt)
	sc.NodeID = node
	store := &memStore{scored: []proposalstore.Scored{sc}}

	c := &Compiler{
		Runner: treeRunner{root: root},
		Store:  store,
		Blobs:  blobs,
		Registries: fixedRegistries{model: &registry.ModelEntry{
			VersionID: modelRef, Name: "cheap",
			Spec: registry.ModelSpec{Provider: "anthropic", ModelID: "claude-haiku-4-5"},
		}},
	}

	res, err := c.Compile(context.Background(), "t1", "wf")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if res.State != StateCompiled {
		t.Fatalf("state = %q (%s)", res.State, res.Detail)
	}
	if len(res.Outcomes) != 1 {
		t.Fatalf("outcomes = %+v", res.Outcomes)
	}
	out := res.Outcomes[0]

	// A diff exists, is content-addressed, and is a UNIFIED DIFF a human can read.
	if out.DiffHash == "" {
		t.Fatalf("no diff was produced: %+v", out)
	}
	diff := string(blobs.data[out.DiffHash])
	if !strings.Contains(diff, "---") || !strings.Contains(diff, "+++") {
		t.Fatalf("the stored artifact is not a unified diff:\n%s", diff)
	}
	if !strings.Contains(diff, "claude-haiku-4-5") {
		t.Fatalf("the diff does not carry the proposed model:\n%s", diff)
	}
	// It rewrites the call site rather than appending: the old value leaves on a `-` line.
	if !strings.Contains(diff, "-") || !strings.Contains(diff, "pipeline.go") {
		t.Errorf("the diff does not read as a rewrite of the discovered file:\n%s", diff)
	}

	// 🔴 The RESOLVED config hash replaces the generator's candidate identity. Until it is recorded, a
	// customer's CI has nothing it can report a verdict against — the ingest is keyed by this hash.
	if len(store.put) != 1 {
		t.Fatalf("recorded %d rows", len(store.put))
	}
	rec := store.put[0]
	if rec.CandidateConfigHash == sc.CandidateConfigHash {
		t.Error("the candidate identity survived; the resolved config hash is what the verdict ingest " +
			"is keyed by")
	}
	if len(rec.CandidateConfigHash) != 64 {
		t.Errorf("config hash = %q, want a 64-hex content address", rec.CandidateConfigHash)
	}
	if rec.SourceDiffBlobHash != out.DiffHash {
		t.Errorf("the row does not point at the stored diff: %q vs %q", rec.SourceDiffBlobHash, out.DiffHash)
	}

	// ⚠️ And it is UNBUILT, not built: the gate parsed the output and no compiler ran. The whole point
	// of the three-valued build result is that this does not silently become `built`.
	if rec.BuildStatus != proposalstore.BuildUnbuilt {
		t.Errorf("build status = %q; a parse is not a build, and `built` is what delivery reads",
			rec.BuildStatus)
	}
	if !strings.Contains(out.Detail, "PARSES") {
		t.Errorf("the outcome must say what the gate did and did not prove, got %q", out.Detail)
	}
	if rec.RefusalReason != "" {
		t.Errorf("a compiled proposal was marked refused: %q", rec.RefusalReason)
	}
}

// Compiling is DETERMINISTIC: the same proposal against the same tree yields a byte-identical diff.
// The claim §1b.3 makes, and what lets a regeneration be proved identical without re-reading it.
func TestCompilingTwiceYieldsTheSameDiff(t *testing.T) {
	root := target(t)
	node := nodeIDFor(t, root, "classify")
	const modelRef = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	run := func() string {
		blobs := newBlobs()
		spec := variantspec.VariantSpec{
			WorkflowID: "wf", SourceRevision: "rev1",
			Nodes: map[string]variantspec.NodeOverride{node: {ModelRef: modelRef}},
		}
		raw, _ := json.Marshal(spec)
		specHash, _ := blobs.Put(context.Background(), raw)
		sc := scored("p1", specHash, "", proposalstore.BuildUnbuilt)
		sc.NodeID = node
		c := &Compiler{
			Runner: treeRunner{root: root},
			Store:  &memStore{scored: []proposalstore.Scored{sc}},
			Blobs:  blobs,
			Registries: fixedRegistries{model: &registry.ModelEntry{
				VersionID: modelRef, Name: "cheap",
				Spec: registry.ModelSpec{Provider: "anthropic", ModelID: "claude-haiku-4-5"},
			}},
		}
		res, err := c.Compile(context.Background(), "t1", "wf")
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
		return res.Outcomes[0].DiffHash
	}

	if a, b := run(), run(); a != b {
		t.Errorf("two compiles of one proposal produced different diffs (%s vs %s) — the artifact is "+
			"content-addressed, so a non-deterministic codemod makes every regeneration look like a change", a, b)
	}
}
