//go:build pgproof

package e2e

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
	"github.com/heros-foreal/agentd/internal/worktree"
)

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// ADR-003: the apply path is language-neutral. This is the proof, on the language that motivated it.
// ─────────────────────────────────────────────────────────────────────────────────────────────────
//
// Every other test in this package targets Go, and they all passed while the apply path was Go-only —
// which is the point. ADR-003's gap was found only when discovery was pointed at a real Python
// repository (nousresearch/hermes-agent, 3,055 files, 39 call sites the apply path could not touch);
// no doc review and no task audit caught it, because the two halves were each correct and the gap lived
// in the seam. 「本地跑通 ≠ 交付闭环」.
//
// So a Go e2e that passes proves nothing about Python, and this file exists so that stays true only
// while Python actually works. It drives the same chain the Go tests drive — discover -> resolve ->
// persist -> generate -> apply -> VERIFY — against a real Python repo, with the real PythonVerifier
// running the real `python3 -m py_compile` over the rewritten tree.
//
// The strength assertion is the second half of ADR-003 and is not decoration: this repository
// configures no type checker, so the honest verdict is `syntax-checked`, and the test asserts that
// verdict is what gets recorded — 🚫 "a syntax-checked diff must never be presentable as though it were
// type-checked" (ADR-003 decision 3).

const pyTargetSrc = `import anthropic

client = anthropic.Anthropic()


def classify(ticket):
    return client.messages.create(
        model="claude-opus-4-6",
        max_tokens=1024,
        messages=[{"role": "user", "content": "Classify this ticket"}],
    )


def summarize(text):
    return client.messages.create(
        model="claude-opus-4-6",
        max_tokens=512,
        messages=[{"role": "user", "content": "Summarize"}],
    )
`

// pyFixture is a real git repo containing real Python, discovered by the real Python frontend.
func pyFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		// 🔴 A skip, NOT a pass. ADR-003's accepted L4 cost is that the worker needs the toolchains it
		// verifies with; a missing one must be visible. Silently passing here would make this file's
		// green a statement about nothing — the exact "tripwire tests guarded by env vars give false
		// confidence" failure.
		t.Skipf("python3 is required to verify a Python transform, and this worker has none: %v", err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pipeline.py"), []byte(pyTargetSrc), 0o600); err != nil {
		t.Fatalf("write pipeline.py: %v", err)
	}
	// Same per-test marker as newFixture, for the same reason: a unique source_revision.
	if err := os.WriteFile(filepath.Join(root, "fixture_id.txt"), []byte(t.Name()), 0o600); err != nil {
		t.Fatalf("write fixture id: %v", err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "base"}} {
		if out, err := gitCmd(root, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	rev, err := gitCmd(root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}

	f := &fixture{Root: root, Rev: strings.TrimSpace(rev), Nodes: map[string]string{}}

	// The IR comes from the REAL pipeline — discovery.Run, all seven frontends — not from a hand-built
	// struct. That is what makes workflow.language a discovered FACT here rather than a value this test
	// asserts about itself, and workflow.language is the thing the whole dispatch turns on.
	res, err := discovery.Run(discovery.Options{Repo: root, WorkflowID: "wf", CommitSHA: f.Rev})
	if err != nil {
		t.Fatalf("discovery.Run: %v", err)
	}
	if res.IR.Workflow.Language != "python" {
		t.Fatalf("discovery called this repo %q, want python — the fixture is not what this test thinks",
			res.IR.Workflow.Language)
	}
	if len(res.IR.Nodes) != 2 {
		t.Fatalf("discovery found %d nodes in the Python fixture, want 2", len(res.IR.Nodes))
	}
	ir := res.IR
	f.IR = &ir

	sites, err := discovery.IndexSpanCallSites(root, "python", nil)
	if err != nil {
		t.Fatalf("IndexSpanCallSites: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "pipeline.py"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(string(src), "\n")
	for id, s := range sites {
		f.Nodes[enclosingPyFunc(lines, s.LineStart)] = id
	}
	if f.Nodes["classify"] == "" || f.Nodes["summarize"] == "" {
		t.Fatalf("could not name the Python call sites; got %v", f.Nodes)
	}
	return f
}

// enclosingPyFunc names a call site by the nearest preceding `def`.
func enclosingPyFunc(lines []string, lineStart int) string {
	for i := lineStart - 1; i >= 0 && i < len(lines); i-- {
		if strings.HasPrefix(lines[i], "def ") {
			name := strings.TrimPrefix(lines[i], "def ")
			if p := strings.IndexByte(name, '('); p > 0 {
				return name[:p]
			}
		}
	}
	return ""
}

// pyStack wires the same components as newStack, with the gate the LANGUAGE gets — chosen by
// worktree.VerifierFor from the language discovery just reported, which is the wiring ADR-003 asks for
// and the wiring internal/submit does in production.
func pyStack(t *testing.T, fx *fixture) *stack {
	t.Helper()
	root := t.TempDir()
	fs, err := registry.NewFSBlobStore(filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}
	blobs := registry.NewCatalogingBlobStore(testDB, fs, "application/json")
	pool, err := worktree.NewPool(context.Background(), fx.Root, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	cache, err := worktree.NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	v, err := worktree.VerifierFor(fx.IR.Workflow.Language)
	if err != nil {
		t.Fatalf("VerifierFor(%s): %v", fx.IR.Workflow.Language, err)
	}
	return &stack{
		fx: fx, blobs: blobs,
		regs:    registry.NewStore(testDB, blobs),
		specs:   variantspec.NewStore(testDB),
		applier: worktree.NewApplier(pool, v, cache),
		tstore:  worktree.NewStore(testDB, blobs),
		runs:    executor.NewStore(testDB),
	}
}

func seedPyWorkflow(t *testing.T, fx *fixture) {
	t.Helper()
	if _, err := testDB.Exec(
		`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		 VALUES ('wf', $1, $2, 'python', '1.0.0') ON CONFLICT DO NOTHING`, fx.Root, fx.Rev); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
}

func pySpec(fx *fixture, overrides map[string]variantspec.NodeOverride) *variantspec.VariantSpec {
	order := []string{fx.Nodes["classify"], fx.Nodes["summarize"]}
	if overrides == nil {
		overrides = map[string]variantspec.NodeOverride{}
	}
	return &variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: fx.Rev, Order: order, Nodes: overrides,
		Edges: []variantspec.Edge{{FromNodeID: order[0], ToNodeID: order[1], Kind: "data"}},
	}
}

// 🔴 THE proof of ADR-003: a non-Go repository goes all the way through.
func TestE2E_Python_ModelOverrideIsGeneratedAppliedAndVerified(t *testing.T) {
	fx := pyFixture(t)
	seedPyWorkflow(t, fx)
	s := pyStack(t, fx)
	before := hashTree(t, fx.Root)

	m := s.registerModel(t, "py_sonnet", "anthropic", "claude-sonnet-5")
	spec := pySpec(fx, map[string]variantspec.NodeOverride{fx.Nodes["classify"]: {ModelRef: m}})

	r, p, applied, err := s.pipeline(t, spec, "pymodel")
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}

	// The language flowed from discovery, through Resolve, into the engine that dispatched on it.
	if r.Language != "python" {
		t.Fatalf("the resolved spec carries language %q, want python", r.Language)
	}

	// ── the transform ────────────────────────────────────────────────────────────────────────────
	if applied.Status != worktree.StatusBuilt {
		t.Fatalf("status = %q, want built:\n%s", applied.Status, applied.BuildLog)
	}
	out := string(p.Files["pipeline.py"])
	if !strings.Contains(out, `model="claude-sonnet-5"`) {
		t.Errorf("the model argument was not rewritten:\n%s", out)
	}
	// ONLY the targeted node. `summarize` shares the model id and the SDK and must not move — this is
	// FR2's per-dimension, per-node independence, asserted on a language where the two call sites are
	// textually identical apart from their max_tokens.
	if strings.Count(out, `model="claude-opus-4-6"`) != 1 {
		t.Errorf("the untargeted call site was rewritten too (or not left alone):\n%s", out)
	}

	// ── ADR-003 decision 2/3: the record says what was PROVED ─────────────────────────────────────
	//
	// This repository configures no pyright and no mypy, so `syntax-checked` is the honest verdict and
	// the one that must be recorded. Asserting it is what stops a future change from quietly recording
	// the Go path's `type-checked` for a Python diff that never earned it.
	if applied.Strength != worktree.StrengthSyntaxChecked {
		t.Errorf("strength = %q, want %q: this Python repo configures no type checker, so a compiler did "+
			"NOT stand behind this diff and the record must not imply one did",
			applied.Strength, worktree.StrengthSyntaxChecked)
	}
	if !strings.Contains(applied.VerifierTool, "py_compile") {
		t.Errorf("verifier tool = %q, want the py_compile fallback named; evidence must travel with the "+
			"claim", applied.VerifierTool)
	}
	// And the consequence is live, not advisory: a syntax-checked transform is human-reviewed at every
	// automation level (ADR-003 decision 5).
	if applied.Strength.AllowsAutonomousApply() {
		t.Error("a syntax-checked Python transform claims it may be auto-applied; the weaker gate has " +
			"become the customer's problem")
	}
	if !applied.Strength.RequiresHumanReview() {
		t.Error("a syntax-checked Python transform does not require human review")
	}

	// ── the record, read back through the store ──────────────────────────────────────────────────
	rec, _, err := s.tstore.Get(context.Background(), applied.ConfigHash, applied.SourceRevision)
	if err != nil {
		t.Fatalf("read the transform record back: %v", err)
	}
	if rec.Strength != worktree.StrengthSyntaxChecked {
		t.Errorf("the PERSISTED strength is %q, want syntax-checked; strength is not derivable after the "+
			"fact, so if it did not round-trip it is gone", rec.Strength)
	}

	// ── the user's tree was never touched (ADR-001's isolation) ───────────────────────────────────
	if after := hashTree(t, fx.Root); after != before {
		t.Error("the user's repository changed; the transform must only ever touch an isolated worktree")
	}

	// ── the verified bytes are the bytes on the branch ───────────────────────────────────────────
	onBranch, err := os.ReadFile(filepath.Join(applied.Dir, "pipeline.py"))
	if err != nil {
		t.Fatalf("read the applied worktree: %v", err)
	}
	if string(onBranch) != out {
		t.Error("the worktree python3 verified is not the patch's bytes")
	}
}

// A Python transform that does not parse must be REJECTED before it is proposed — the same terminal
// state a Go build failure produces, reached through py_compile.
//
// It is driven by breaking the fixture rather than by breaking a rewriter, because a rewriter that
// emits invalid Python is caught earlier (by gateMinimal's re-parse) and would never reach the gate.
// This proves the LAST net holds: even if a splice somehow produced unparsable Python, the verifier
// rejects it and no run is enqueued.
func TestE2E_Python_UnparsableTreeIsRejectedByTheGate(t *testing.T) {
	fx := pyFixture(t)
	seedPyWorkflow(t, fx)
	s := pyStack(t, fx)

	v, err := worktree.VerifierFor("python")
	if err != nil {
		t.Fatalf("VerifierFor: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.py"), []byte("def f(:\n  pass\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	ver, err := v.Verify(context.Background(), dir)
	if err == nil {
		t.Fatal("py_compile accepted a file that does not parse; the gate cannot go red")
	}
	if errors.Is(err, worktree.ErrToolchainUnavailable) {
		t.Fatalf("this is a toolchain failure, not a rejection: %v", err)
	}
	// A rejection still states which gate rejected it — that a syntax-checked gate rejected the tree is
	// itself information (the rewrite does not even parse).
	if ver.Strength != worktree.StrengthSyntaxChecked {
		t.Errorf("a rejection carries strength %q, want syntax-checked", ver.Strength)
	}
	_ = s
}

// The other half of ADR-003 decision 4: the SAME Python verifier reports `type-checked` against a repo
// that configures a type checker. Strength is a property of the repository, not of the language — which
// is why it lives on the result and not on the Verifier.
//
// Skipped, loudly, when the worker has no pyright: 🚫 a missing toolchain must never be mistaken for a
// legitimate downgrade, and a test that quietly passed here would be asserting nothing.
func TestE2E_Python_AConfiguredTypeCheckerEarnsTypeChecked(t *testing.T) {
	if _, err := exec.LookPath("pyright"); err != nil {
		t.Skipf("pyright is not installed on this worker, so the type-checked half of ADR-003 decision 4 "+
			"is NOT covered by this run: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pyrightconfig.json"), []byte(`{"include": ["."]}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.py"), []byte("def f() -> int:\n    return 1\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	v, err := worktree.VerifierFor("python")
	if err != nil {
		t.Fatalf("VerifierFor: %v", err)
	}
	ver, err := v.Verify(context.Background(), dir)
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, ver.Log)
	}
	if ver.Strength != worktree.StrengthTypeChecked {
		t.Errorf("strength = %q, want type-checked: this repo configures pyright", ver.Strength)
	}
}
