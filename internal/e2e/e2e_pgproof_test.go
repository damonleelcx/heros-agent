//go:build pgproof

// Package e2e is P2's end-to-end integration test (tasks 8.1, 8.2).
//
// # Why this exists when every package already has its own suite
//
// Because the per-package suites cannot see the seams between them, and the seams are where P2's real
// bugs have been. The blob-catalog bug is the case in point: internal/registry's tests passed (its
// blob store stored bytes), internal/executor's tests passed (they recorded nodes with no I/O), and
// the two were incompatible in a way that made the FIRST real run fail instantly. Nothing short of
// running the whole chain against real infrastructure could have caught it.
//
// So this wires the real thing:
//
//	real Postgres        the five migrations, the real stores
//	real object store    a filesystem blob store, catalogued
//	real git worktrees   a bare clone, a variant branch, a real commit
//	real `go build`      against a fixture that genuinely compiles
//	stubbed providers    an httptest server — the ONLY stub, because PRD §12 says so and because
//	                     spending money to run a test is not a design
//
// # The fixture (task 8.1)
//
// testdata/target is a 3-node graph in a buildable Go module pinned at a source_revision. It compiles
// against a local SDK stub typed the way the real SDK is, which is what makes the build gate's tests
// meaningful rather than decorative:
//
//   - Model is a NAMED STRING TYPE — rewriting it to an undefined identifier must fail to compile, and
//     rewriting it to a string literal must succeed.
//   - Messages are built the way the real SDK builds them: the ROLE lives in a function
//     (`NewUserMessage`) and the text in a block constructor (`NewTextBlock`), never in bare string
//     fields. That is the property the prompt rewriter's boundary rests on — a single-turn
//     construction therefore contains exactly ONE string expression — so a stub shaped any other way
//     would make every prompt override refuse for a reason no real SDK produces.
//
// The three nodes cover the prompt boundary end to end: `classify` is rewritable (single-turn, with a
// runtime operand a slot binds to), `resolve` is a two-turn list (refused as ambiguous), and
// `summarize` passes no Messages at all (refused as absent) while also being the model-INSERT case.
package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
	"github.com/heros-foreal/agentd/internal/worktree"
)

var testDB *sql.DB

func TestMain(m *testing.M) {
	db, err := pgtest.Open("proof_e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testDB = db
	for _, f := range []string{"0001_p0_lineage", "0002_p2_registries", "0003_p2_variant_spec",
		"0004_p2_transform", "0005_p2_run", "0006_p2_run_queue",
		"0007_p2_verification_strength"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", f+".up.sql"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", f, err)
			os.Exit(1)
		}
		if _, err := db.Exec(string(b)); err != nil {
			fmt.Fprintf(os.Stderr, "apply %s: %v\n", f, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// ── the fixture ──────────────────────────────────────────────────────────────────────────────────

type fixture struct {
	Root string // the user's repo — this must never be mutated
	Rev  string // the pinned source_revision
	IR   *discovery.IR
	// Nodes maps enclosing function -> node_id, so a test names a call site by what it is.
	Nodes map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sdkstub"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for src, dst := range map[string]string{
		"testdata/target/go.mod.txt":         "go.mod",
		"testdata/target/pipeline.go.txt":    "pipeline.go",
		"testdata/target/sdkstub/go.mod.txt": "sdkstub/go.mod",
		"testdata/target/sdkstub/sdk.go.txt": "sdkstub/sdk.go",
	} {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(root, dst), b, 0o600); err != nil {
			t.Fatalf("write %s: %v", dst, err)
		}
	}
	// A per-test marker, so this fixture's commit sha — and therefore its source_revision — is unique.
	//
	// Without it the fixture is byte-identical across tests, and a git sha is a hash of the tree AND
	// the commit timestamp: two tests committing in the same second produce the SAME sha. Every
	// assertion scoped by source_revision then sees its neighbours' rows, and the tests pass or fail
	// on how fast the machine is. TestE2E_DanglingRefFailsClosedWithNoSideEffects is the one that
	// catches it — it asserts on the ABSENCE of rows, which is exactly what a neighbour's rows break.
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
	sites, err := discovery.IndexGoCallSites(root, nil)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	// The fixture is only a fixture if discovery actually finds its call sites. A fixture that
	// discovers zero nodes would make every assertion below pass for the wrong reason.
	if len(sites) != 3 {
		t.Fatalf("discovery found %d call sites in the fixture, want 3", len(sites))
	}
	src, err := os.ReadFile(filepath.Join(root, "pipeline.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Split(string(src), "\n")
	// workflow.language is what a real discovery.Run emits for this Go fixture, and the Transform
	// Engine dispatches on it (ADR-003 decision 1). The fixture omitted it while there was only one
	// engine and the field went unread; it is not optional now, and there is deliberately no default —
	// a Resolved with no language fails loudly rather than silently taking the Go path.
	f.IR = &discovery.IR{
		IRVersion: "1.0.0", Workflow: discovery.IRWorkflow{Language: "go"},
		Nodes: []discovery.IRNode{}, Edges: []discovery.IREdge{},
	}
	for id, s := range sites {
		fn := enclosingFunc(lines, s.LineStart)
		f.Nodes[fn] = id
		f.IR.Nodes = append(f.IR.Nodes, discovery.IRNode{
			NodeID: id, Kind: "static_definition",
			CallSite:        discovery.IRCallSite{File: s.FileRel, Symbol: fn, LineStart: s.LineStart, LineEnd: s.LineEnd},
			Model:           discovery.IRModel{Provider: "anthropic", ModelID: "unresolved", Params: map[string]any{}},
			Prompt:          discovery.IRPrompt{Inline: "", Variables: []string{}},
			ContextAssembly: discovery.IRContextAssembly{Policy: "inline_messages"},
			IOContract: discovery.IRIOContract{
				InputSchema: map[string]any{"type": "object"},
				OutputSchema: map[string]any{
					"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}},
					"required": []any{"answer"},
				},
			},
		})
	}
	return f
}

func enclosingFunc(lines []string, line int) string {
	for i := line - 1; i >= 0 && i < len(lines); i-- {
		if strings.HasPrefix(lines[i], "func ") {
			n := strings.TrimPrefix(lines[i], "func ")
			if p := strings.IndexByte(n, '('); p > 0 {
				return n[:p]
			}
		}
	}
	return ""
}

func gitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+os.TempDir())
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// stack is the whole chain, wired the way production would be.
type stack struct {
	fx      *fixture
	blobs   registry.BlobStore
	regs    *registry.Store
	specs   *variantspec.Store
	applier *worktree.Applier
	tstore  *worktree.Store
	runs    *executor.Store
}

func newStack(t *testing.T, fx *fixture) *stack {
	t.Helper()
	root := t.TempDir()
	fs, err := registry.NewFSBlobStore(filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}
	// A CATALOGING store, as production must use: this is the seam the browser found broken.
	blobs := registry.NewCatalogingBlobStore(testDB, fs, "application/json")

	pool, err := worktree.NewPool(context.Background(), fx.Root, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	cache, err := worktree.NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	goBin, _ := exec.LookPath("go")
	return &stack{
		fx: fx, blobs: blobs,
		regs:    registry.NewStore(testDB, blobs),
		specs:   variantspec.NewStore(testDB),
		applier: worktree.NewApplier(pool, worktree.GoVerifier{GoBin: goBin, Env: buildEnv()}, cache),
		tstore:  worktree.NewStore(testDB, blobs),
		runs:    executor.NewStore(testDB),
	}
}

// buildEnv pins the build's environment. PRD §7 requires a deterministic build; inheriting the
// operator's GOFLAGS or GOPROXY would make two runs of one config_hash build differently.
func buildEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"), // the module cache lives here
		"GOFLAGS=-mod=mod",
		"GOPROXY=off", // the fixture resolves entirely through its local replace; nothing may be fetched
		"GOCACHE=" + os.Getenv("HOME") + "/Library/Caches/go-build",
	}
}

func seedWorkflow(t *testing.T, fx *fixture) {
	t.Helper()
	if _, err := testDB.Exec(
		`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		 VALUES ('wf', $1, $2, 'go', '1.0.0') ON CONFLICT DO NOTHING`, fx.Root, fx.Rev); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
}

// registerModel publishes a model entry and returns its version_id.
func (s *stack) registerModel(t *testing.T, name, provider, modelID string) string {
	t.Helper()
	maxTok := 256
	id, err := s.regs.RegisterModel(context.Background(), name, registry.ModelSpec{
		Provider: provider, ModelID: modelID,
		Params: registry.ModelParams{MaxTokens: &maxTok},
	})
	if err != nil {
		t.Fatalf("RegisterModel: %v", err)
	}
	return id
}

// spec builds a Variant Spec over the fixture's three nodes.
func (s *stack) spec(overrides map[string]variantspec.NodeOverride) *variantspec.VariantSpec {
	order := []string{s.fx.Nodes["classify"], s.fx.Nodes["resolve"], s.fx.Nodes["summarize"]}
	if overrides == nil {
		overrides = map[string]variantspec.NodeOverride{}
	}
	return &variantspec.VariantSpec{
		WorkflowID: "wf", SourceRevision: s.fx.Rev, Order: order, Nodes: overrides,
		Edges: []variantspec.Edge{
			{FromNodeID: order[0], ToNodeID: order[1], Kind: "data"},
			{FromNodeID: order[1], ToNodeID: order[2], Kind: "data"},
		},
	}
}

// pipeline runs the whole chain: resolve -> hash -> persist -> generate -> apply -> build -> record.
func (s *stack) pipeline(t *testing.T, spec *variantspec.VariantSpec, label string) (*variantspec.Resolved, *transform.Patch, *worktree.Applied, error) {
	t.Helper()
	ctx := context.Background()
	r, err := variantspec.Resolve(ctx, spec, s.fx.IR, s.regs)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := s.specs.Put(ctx, r, spec, "v_"+label, label); err != nil {
		t.Fatalf("persist spec: %v", err)
	}
	p, err := transform.Generate(r, s.fx.Root)
	if err != nil {
		return r, nil, nil, err
	}
	applied, applyErr := s.applier.Apply(ctx, p)
	if applied != nil {
		var rej *worktree.BuildRejection
		_ = errors.As(applyErr, &rej)
		if err := s.tstore.Put(ctx, applied, rej); err != nil {
			t.Fatalf("record transform: %v", err)
		}
	}
	return r, p, applied, applyErr
}

// ── 8.2 · the ten integration assertions ─────────────────────────────────────────────────────────

// 8.2 (1) deterministic diff · (3) behavior-preserving minimal diff · (4) isolated application.
//
// The known-good model-override variant (task 8.1), through the real chain.
func TestE2E_ModelOverride_MinimalDeterministicDiffOnAnIsolatedWorktree(t *testing.T) {
	fx := newFixture(t)
	seedWorkflow(t, fx)
	s := newStack(t, fx)
	before := hashTree(t, fx.Root)

	m := s.registerModel(t, "sonnet", "anthropic", "claude-sonnet-5")
	spec := s.spec(map[string]variantspec.NodeOverride{fx.Nodes["classify"]: {ModelRef: m}})

	r, p, applied, err := s.pipeline(t, spec, "model")
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if applied.Status != worktree.StatusBuilt {
		t.Fatalf("status = %q, want built:\n%s", applied.Status, applied.BuildLog)
	}

	// (3) minimal: exactly one line changed, and it is the model argument.
	minus, plus := countDiffLines(string(p.Diff))
	if minus != 1 || plus != 1 {
		t.Errorf("a model-only override changed %d/-%d/+ lines, want 1/1:\n%s", minus, plus, p.Diff)
	}
	if !strings.Contains(string(p.Diff), `"claude-sonnet-5"`) {
		t.Errorf("the diff does not carry the override:\n%s", p.Diff)
	}
	// The decoys must not be CHANGED. Checked against the changed lines only, not the whole diff: the
	// decoy sits three lines from the rewrite, so it legitimately appears as an unchanged CONTEXT line
	// — which is the diff being correct, not the transform being wrong.
	for _, l := range changedLines(string(p.Diff)) {
		for _, decoy := range []string{"appears here but must never be rewritten", "ModelClaudeSonnet4"} {
			if strings.Contains(l, decoy) {
				t.Errorf("the transform changed a line it must not touch (%q):\n%s", decoy, p.Diff)
			}
		}
	}
	// And the transformed FILE still contains both decoys, untouched.
	got := string(p.Files["pipeline.go"])
	if !strings.Contains(got, `_ = "claude-opus-4-6 appears here but must never be rewritten"`) {
		t.Error("the unrelated string literal was rewritten; the transform matched text, not AST")
	}
	if !strings.Contains(got, "anthropic.ModelClaudeSonnet4") {
		t.Error("a model-only override on ONE node edited another node's call site")
	}

	// (4) isolated: the user's tree is byte-for-byte unchanged after generate+apply+build.
	if after := hashTree(t, fx.Root); after != before {
		t.Error("the user's working tree changed")
	}

	// (1) deterministic: regenerate from a cold pool/cache and get the identical patch.
	s2 := newStack(t, fx)
	r2, err := variantspec.Resolve(context.Background(), spec, fx.IR, s2.regs)
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if r2.ConfigHash != r.ConfigHash {
		t.Errorf("config_hash is not deterministic: %s vs %s", r2.ConfigHash, r.ConfigHash)
	}
	p2, err := transform.Generate(r2, fx.Root)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if string(p2.Diff) != string(p.Diff) || p2.DiffHash != p.DiffHash {
		t.Errorf("the same {config_hash, source_revision} produced a different diff:\n%s\n---\n%s", p.Diff, p2.Diff)
	}
}

// M2 exit criterion 2b: a PROMPT override produces a minimal targeted diff that TAKES EFFECT —
// deterministic, buildable, and runnable, ending in a terminal `succeeded` status in Postgres.
//
// This is the criterion's headline test, and it is deliberately shaped like the model-override one:
// the two dimensions differ in what they rewrite, not in what they must prove.
//
// The `classify` node's prompt is the shape every real Go SDK call site has — a message CONSTRUCTION
// (`[]anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("…" + ticket))}`), not a
// bare string. The engine descends into it and replaces only the string expression inside, so the
// message slice, the role helper and the SDK types survive byte-for-byte. The slot `{{ticket}}` binds
// to the call site's OWN `ticket` expression — verified against the call site, never guessed.
//
// The proof that it took effect is read from the BUILT WORKTREE ON DISK, not from Generate's return
// value: the bytes that compile and run are the only ones that can be said to have taken effect.
func TestE2E_PromptOverride_TakesEffectAsAMinimalDeterministicDiffThatBuildsAndRuns(t *testing.T) {
	fx := newFixture(t)
	seedWorkflow(t, fx)
	s := newStack(t, fx)
	ctx := context.Background()

	// A template whose slot is spelled exactly like the call site's runtime operand (`ticket`), so the
	// binding is verified rather than invented. The literal is deliberately unlike the fixture's own
	// text, so "the new prompt is in the built tree" cannot pass by accident.
	pid, err := s.regs.RegisterPrompt(ctx, "triage/route",
		"You are a support router. Classify this ticket in one word:\n{{ticket}}")
	if err != nil {
		t.Fatalf("RegisterPrompt: %v", err)
	}
	spec := s.spec(map[string]variantspec.NodeOverride{fx.Nodes["classify"]: {PromptRef: pid}})

	r, p, applied, err := s.pipeline(t, spec, "prompteffect")
	if err != nil {
		t.Fatalf("pipeline: %v", err) // a refusal here IS the criterion failing
	}
	if applied.Status != worktree.StatusBuilt {
		t.Fatalf("the rewritten prompt does not compile:\n%s\n--- diff ---\n%s", applied.BuildLog, p.Diff)
	}

	// 1 · minimal + targeted: exactly one line changed, and it is the prompt text.
	minus, plus := countDiffLines(string(p.Diff))
	if minus != 1 || plus != 1 {
		t.Errorf("a prompt-only override changed %d-/%d+ lines, want 1/1:\n%s", minus, plus, p.Diff)
	}
	// The rewritten expression: template literal + the call site's own operand, concatenated. This is
	// the assertion that the slot bound to real code rather than being rendered as a hole.
	const wantExpr = `anthropic.NewTextBlock("You are a support router. Classify this ticket in one word:\n" + ticket)`
	built, err := os.ReadFile(filepath.Join(applied.Dir, "pipeline.go"))
	if err != nil {
		t.Fatalf("read built pipeline.go: %v", err)
	}
	if !strings.Contains(string(built), wantExpr) {
		t.Errorf("the built tree does not carry the rewritten prompt.\nwant: %s\n--- diff ---\n%s", wantExpr, p.Diff)
	}
	// No slot survived unrendered. A `{{ticket}}` reaching a provider looks like a bad model day, not a bug.
	if strings.Contains(string(built), "{{") {
		t.Errorf("an unrendered slot survived into the built tree:\n%s", p.Diff)
	}
	// The construction around the text is untouched, which is what makes this rewrite SDK-agnostic.
	if !strings.Contains(string(built), `Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(`) {
		t.Errorf("the message construction was not preserved byte-for-byte:\n%s", p.Diff)
	}
	// Targeted: the OTHER nodes' prompts are untouched, and so is the model dimension of this one.
	for _, untouched := range []string{`anthropic.NewTextBlock("First question.")`,
		`anthropic.NewTextBlock("Second question.")`, `Model:     anthropic.ModelClaudeOpus4_6,`} {
		if !strings.Contains(string(built), untouched) {
			t.Errorf("a prompt override edited something it must not touch (%q):\n%s", untouched, p.Diff)
		}
	}
	// FR2 independence: a prompt override records ONLY the prompt dimension as touched.
	for _, td := range p.Touched {
		if td.Dim != "prompt" || td.NodeID != fx.Nodes["classify"] {
			t.Errorf("a prompt-only override touched node=%s dim=%s", td.NodeID, td.Dim)
		}
	}

	// 2 · deterministic: re-resolve and regenerate from scratch — byte-identical diff.
	//
	// Unlike the model-override determinism check, this re-resolves through the SAME registry store
	// rather than a cold one. That is not a weaker test, it is the only correct one: a prompt's BODY is
	// a blob, and newStack gives each stack its own filesystem blob store, so a "cold" registry here
	// would not be re-resolving the same prompt — it would be failing to find it at all. Production has
	// one blob store; two is the test harness's artifact, not a scenario. Determinism is a property of
	// {config_hash, source_revision} -> diff, and both are re-derived below.
	r2, err := variantspec.Resolve(ctx, spec, fx.IR, s.regs)
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if r2.ConfigHash != r.ConfigHash {
		t.Errorf("config_hash is not deterministic: %s vs %s", r2.ConfigHash, r.ConfigHash)
	}
	p2, err := transform.Generate(r2, fx.Root)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if string(p2.Diff) != string(p.Diff) || p2.DiffHash != p.DiffHash {
		t.Errorf("the same {config_hash, source_revision} produced a different prompt diff:\n%s\n---\n%s",
			p.Diff, p2.Diff)
	}

	// 3 · it RUNS: drive the built, transformed copy to a terminal status.
	sh := filepath.Join(applied.Dir, "run.sh")
	var body strings.Builder
	body.WriteString("#!/bin/sh\nset -e\n")
	for i, node := range []string{fx.Nodes["classify"], fx.Nodes["resolve"], fx.Nodes["summarize"]} {
		rec := fmt.Sprintf(`{"invocation_id":"i%d","node_id":%q,"run_id":"run_prompt","invocation_index":0,`+
			`"input":{"q":"ticket %d"},"output":{"answer":"routed %d"}}`, i, node, i, i)
		body.WriteString("printf '%s\\n' '" + rec + "' >> \"$HEROS_INVOCATION_LOG\"\n")
	}
	if err := os.WriteFile(sh, []byte(body.String()), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}
	e := executor.New(s.blobs)
	run, err := e.Run(ctx, executor.Options{
		Dir: applied.Dir, Cmd: []string{"/bin/sh", sh}, RunID: "run_prompt", Seed: 7, IR: fx.IR,
	})
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, run.Stderr)
	}

	// 4 · assert via the DOWNSTREAM READ PATH, not the function's return: persist, then read back the
	// way the UI does. A run that only exists in memory is not a run.
	if err := s.runs.Start(ctx, "run_prompt", r.ConfigHash, fx.Rev, 7); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, n := range run.Nodes {
		if err := s.runs.RecordNode(ctx, "run_prompt", n); err != nil {
			t.Fatalf("RecordNode %s: %v", n.NodeID, err)
		}
	}
	if err := s.runs.Finish(ctx, run); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	got, err := s.runs.Get(ctx, "run_prompt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != executor.StatusSucceeded {
		t.Errorf("persisted terminal status = %q, want succeeded", got.Status)
	}
	// The run is attributed to the PROMPT-overridden configuration — this is what ties the terminal
	// status to the override rather than to some other build.
	if got.ConfigHash != r.ConfigHash || got.Seed != 7 {
		t.Errorf("the run is not attributed to the prompt-overridden config: %+v", got)
	}
	if len(got.Nodes) != 3 {
		t.Fatalf("persisted %d node executions, want 3", len(got.Nodes))
	}
	for _, n := range got.Nodes {
		if len(n.InputBlobHash) != 64 || len(n.OutputBlobHash) != 64 {
			t.Errorf("node %s has no per-node I/O: in=%q out=%q", n.NodeID, n.InputBlobHash, n.OutputBlobHash)
		}
		if _, err := s.blobs.Get(ctx, n.OutputBlobHash); err != nil {
			t.Errorf("node %s's output blob does not resolve: %v", n.NodeID, err)
		}
	}
	// And the prompt transform is on record as the reviewable artifact §13 asks for.
	rec, diff, err := s.tstore.Get(ctx, r.ConfigHash, fx.Rev)
	if err != nil {
		t.Fatalf("transform Get: %v", err)
	}
	if rec.Status != worktree.StatusBuilt || rec.Commit == "" {
		t.Errorf("the prompt transform record is not a complete review artifact: %+v", rec)
	}
	if !strings.Contains(string(diff), "You are a support router.") {
		t.Errorf("the recorded review diff does not carry the prompt override:\n%s", diff)
	}
}

// The refusal boundary, at the e2e level, against a REAL compiler and the real chain.
//
// This is the other half of criterion 2b and it is not a consolation prize: the value of a codemod on
// customer source is bounded by what it declines to guess at. Each case below is a call site where a
// prompt entry does not determine a unique rewrite, so the only alternatives are "refuse" and "guess",
// and a guess that COMPILES is the failure mode with no downstream net — it would quietly corrupt
// every eval run beneath it.
//
// Each case pins the REASON, not just the refusal. That matters here specifically: before the fixture
// carried real message constructions, this test passed because no node set `Messages` AT ALL — it
// refused with "the prompt argument is not present" and never reached the boundary it claimed to
// prove. A refusal test that does not assert WHY can pass for a reason that has nothing to do with
// the guarantee, which is exactly what happened.
func TestE2E_PromptOverride_UnrewritableCallSitesAreRefusedBeforeAnythingIsApplied(t *testing.T) {
	fx := newFixture(t)
	seedWorkflow(t, fx)
	s := newStack(t, fx)
	ctx := context.Background()

	cases := []struct {
		name, node, prompt, body string
		wantReason               string
	}{
		{
			// The call site feeds `ticket` into its prompt; a fixed string would silently drop it.
			name: "slotless template at a call site feeding a runtime value",
			node: "classify", prompt: "triage/fixed", body: "Classify this ticket. Be brief.",
			wantReason: "declares no slots",
		},
		{
			// Two turns, two text literals: a prompt entry names ONE text and cannot say which it replaces.
			name: "multi-turn message list",
			node: "resolve", prompt: "triage/multiturn", body: "Resolve it.",
			wantReason: "separate string expressions",
		},
		{
			// The call site never passes Messages, so there is no prompt expression to rewrite.
			name: "call site with no prompt argument at all",
			node: "summarize", prompt: "triage/absent", body: "Summarize it.",
			wantReason: "not present",
		},
		{
			// A slot spelled unlike anything the call site supplies. Binding it by POSITION would compile
			// and silently put the wrong value in the hole; the engine will not guess.
			name: "slot matching no call-site operand",
			node: "classify", prompt: "triage/unbound", body: "Classify {{unrelated_variable}} now.",
			wantReason: "matches 0 of this call site's runtime value(s)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pid, err := s.regs.RegisterPrompt(ctx, tc.prompt, tc.body)
			if err != nil {
				t.Fatalf("RegisterPrompt: %v", err)
			}
			spec := s.spec(map[string]variantspec.NodeOverride{fx.Nodes[tc.node]: {PromptRef: pid}})

			_, _, applied, err := s.pipeline(t, spec, "refuse_"+tc.prompt)
			if !errors.Is(err, transform.ErrUnsafeRewrite) {
				t.Fatalf("want ErrUnsafeRewrite, got %v", err)
			}
			var re *transform.RewriteError
			if !errors.As(err, &re) {
				t.Fatalf("want a *RewriteError, got %T", err)
			}
			if re.NodeID != fx.Nodes[tc.node] || re.Dim != "prompt" {
				t.Errorf("the refusal names node=%q dim=%q, want the %s node / prompt", re.NodeID, re.Dim, tc.node)
			}
			// The REASON, not merely that it refused. Without this the test cannot tell the boundary
			// firing from an unrelated refusal — the exact bug this test previously had.
			if !strings.Contains(re.Detail, tc.wantReason) {
				t.Errorf("the refusal reason does not name the boundary that fired.\nwant substring: %q\ngot: %s",
					tc.wantReason, re.Detail)
			}
			// FR5: rejected BEFORE any transform is applied — no worktree, no build, no record.
			if applied != nil {
				t.Error("a refused override still produced an applied transform")
			}
		})
	}
}

// 8.2 (2) build-preserving rejection: a spec whose transform would break the build is rejected
// BEFORE running, as build-rejected, naming the node and dimension.
//
// The break is real: the SDK stub's Model is a named string type, and this override rewrites it to a
// value the compiler rejects. This is the codemod hazard ADR-001 names as the top risk, made concrete.
func TestE2E_BuildBreakingTransformIsRejectedBeforeItRuns(t *testing.T) {
	fx := newFixture(t)
	seedWorkflow(t, fx)
	s := newStack(t, fx)

	// A model id nothing else in this package registers. Two tests registering identical content get
	// the same version_id and therefore the same config_hash — and the store then (correctly) refuses
	// to record a second, conflicting verdict for that pair. That guard is right; this test needs a
	// configuration of its own.
	m := s.registerModel(t, "breaker", "anthropic", "claude-breaks-the-build")
	spec := s.spec(map[string]variantspec.NodeOverride{fx.Nodes["classify"]: {ModelRef: m}})
	r, err := variantspec.Resolve(context.Background(), spec, fx.IR, s.regs)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := s.specs.Put(context.Background(), r, spec, "v_broken", "broken"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	p, err := transform.Generate(r, fx.Root)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Corrupt the generated patch the way a bad codemod would: syntactically valid Go that does not
	// compile. Injected here because the engine (correctly) will not produce one — the gate has to be
	// provable even when the thing it guards against is prevented upstream.
	p.Files["pipeline.go"] = []byte(strings.Replace(string(p.Files["pipeline.go"]),
		`Model:     "claude-breaks-the-build",`, `Model:     undefinedIdentifier,`, 1))

	applied, err := s.applier.Apply(context.Background(), p)
	if !errors.Is(err, worktree.ErrBuildRejected) {
		t.Fatalf("want ErrBuildRejected, got %v", err)
	}
	if applied.Status != worktree.StatusBuildRejected {
		t.Errorf("status = %q, want build-rejected", applied.Status)
	}
	if !strings.Contains(applied.BuildLog, "undefinedIdentifier") {
		t.Errorf("the build log lost the compiler's reason:\n%s", applied.BuildLog)
	}
	var rej *worktree.BuildRejection
	if errors.As(err, &rej) && rej.NodeID == "" {
		t.Log("note: the compiler blamed a line outside a rewritten one, so attribution is (correctly) empty")
	}
	// It is recorded as build-rejected, so the UI renders it and P4 does not re-attempt it.
	if err := s.tstore.Put(context.Background(), applied, rej); err != nil {
		t.Fatalf("record: %v", err)
	}
	rec, _, err := s.tstore.Get(context.Background(), p.ConfigHash, p.SourceRevision)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Status != worktree.StatusBuildRejected {
		t.Errorf("recorded status = %q, want build-rejected", rec.Status)
	}
	// And crucially: no run may exist for it.
	var runs int
	if err := testDB.QueryRow(`SELECT count(*) FROM run WHERE config_hash=$1`, p.ConfigHash).Scan(&runs); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runs != 0 {
		t.Errorf("a build-rejected transform has %d run(s); it must never run", runs)
	}
}

// 8.2 (5) reproducibility: the same {config_hash, source_revision} applies and builds to the same
// tree, from a cold pool and cache each time.
func TestE2E_Reproducibility_SamePairBuildsTheSameTree(t *testing.T) {
	fx := newFixture(t)
	seedWorkflow(t, fx)

	var trees []string
	var hashes []string
	for i := 0; i < 2; i++ {
		s := newStack(t, fx) // cold: a cache hit would return the first run's answer and prove nothing
		m := s.registerModel(t, "repro", "anthropic", "claude-repro-5")
		spec := s.spec(map[string]variantspec.NodeOverride{fx.Nodes["classify"]: {ModelRef: m}})
		r, _, applied, err := s.pipeline(t, spec, "repro")
		if err != nil {
			t.Fatalf("pipeline %d: %v", i, err)
		}
		if applied.CacheHit {
			t.Fatalf("iteration %d hit a cache", i)
		}
		out, err := gitCmd(applied.Dir, "rev-parse", "HEAD^{tree}")
		if err != nil {
			t.Fatalf("rev-parse: %v", err)
		}
		trees = append(trees, strings.TrimSpace(out))
		hashes = append(hashes, r.ConfigHash)
	}
	if hashes[0] != hashes[1] {
		t.Errorf("config_hash differs across runs: %s vs %s", hashes[0], hashes[1])
	}
	if trees[0] != trees[1] {
		t.Errorf("the same pair built different trees: %s vs %s", trees[0], trees[1])
	}
}

// 8.2 (8) fail-closed on a dangling ref: aborts before ANY side effect — no diff, no worktree, no
// run — and names the node and dimension.
func TestE2E_DanglingRefFailsClosedWithNoSideEffects(t *testing.T) {
	fx := newFixture(t)
	seedWorkflow(t, fx)
	s := newStack(t, fx)

	spec := s.spec(map[string]variantspec.NodeOverride{
		fx.Nodes["classify"]: {ModelRef: strings.Repeat("0", 64)}, // resolves to nothing
	})
	_, err := variantspec.Resolve(context.Background(), spec, fx.IR, s.regs)
	if !errors.Is(err, variantspec.ErrUnresolvedRef) {
		t.Fatalf("want ErrUnresolvedRef, got %v", err)
	}
	var se *variantspec.SpecError
	if !errors.As(err, &se) || se.NodeID != fx.Nodes["classify"] || se.Dim != variantspec.DimModel {
		t.Errorf("the rejection must name node and dimension, got %v", err)
	}
	// No side effects anywhere.
	for _, q := range []struct{ what, sql string }{
		{"variant_spec", `SELECT count(*) FROM variant_spec WHERE source_revision=$1`},
		{"transform", `SELECT count(*) FROM transform WHERE source_revision=$1`},
		{"run", `SELECT count(*) FROM run WHERE source_revision=$1`},
	} {
		var n int
		if err := testDB.QueryRow(q.sql, fx.Rev).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", q.what, err)
		}
		if n != 0 {
			t.Errorf("a dangling ref left %d %s row(s); resolution must abort before any side effect", n, q.what)
		}
	}
}

// 8.2 (10) clean rollback: a single `git revert` restores source_revision byte-for-byte, no residue.
func TestE2E_RollbackIsASingleGitRevertWithNoResidue(t *testing.T) {
	fx := newFixture(t)
	seedWorkflow(t, fx)
	s := newStack(t, fx)

	m := s.registerModel(t, "revertme", "anthropic", "claude-revert-5")
	spec := s.spec(map[string]variantspec.NodeOverride{fx.Nodes["classify"]: {ModelRef: m}})
	_, _, applied, err := s.pipeline(t, spec, "revert")
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}

	if err := s.applier.Revert(context.Background(), applied); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	orig, err := os.ReadFile(filepath.Join(fx.Root, "pipeline.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	back, err := os.ReadFile(filepath.Join(applied.Dir, "pipeline.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(back) != string(orig) {
		t.Error("the revert did not restore the original bytes")
	}
	if out, err := gitCmd(applied.Dir, "status", "--porcelain"); err != nil || strings.TrimSpace(out) != "" {
		t.Errorf("residual edits after revert: %q", out)
	}
	out, err := gitCmd(applied.Dir, "diff", fx.Rev, "HEAD", "--stat")
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("the reverted branch still differs from source_revision:\n%s", out)
	}
}

// 8.2 (6) idempotency + (7) provider swap + seed propagation, through the REAL gateway against a
// stubbed provider.
//
// Provider swap here is the gateway's half (FR12): the same request against two providers returns the
// same normalized shape. The codemod's half deliberately REFUSES a call-site provider swap (an
// anthropic SDK call does not become an OpenAI call by rewriting a string), so the two halves
// together are the whole requirement — see TestE2E_ProviderSwapAtTheCallSiteIsRefused.
func TestE2E_IdempotencyProviderSwapAndSeedThroughTheRealGateway(t *testing.T) {
	fx := newFixture(t)
	seedWorkflow(t, fx)
	s := newStack(t, fx)
	ctx := context.Background()

	// Build a real transform + run so the FKs have targets.
	m := s.registerModel(t, "gw", "anthropic", "claude-gw-5")
	spec := s.spec(map[string]variantspec.NodeOverride{fx.Nodes["classify"]: {ModelRef: m}})
	r, _, applied, err := s.pipeline(t, spec, "gw")
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if applied.Status != worktree.StatusBuilt {
		t.Fatalf("build: %s", applied.BuildLog)
	}

	// A stubbed provider that bills the way a real one does, and fails twice first.
	var mu sync.Mutex
	seen := map[string]bool{}
	charges, requests, failNext := 0, 0, 2
	var gotSeed any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		requests++
		key := req.Header.Get("Idempotency-Key")
		if key == "" || !seen[key] {
			charges++
			seen[key] = true
		}
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		if v, ok := body["seed"]; ok {
			gotSeed = v
		}
		fail := failNext > 0
		if fail {
			failNext--
		}
		mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"ok"}}],
			"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	t.Cleanup(srv.Close)

	gw := providergateway.New(
		providergateway.StaticSecrets{providergateway.ProviderOpenAI: {APIKey: "sk-e2e-secret-value-xyz"}},
		providergateway.WithBaseURL(providergateway.ProviderOpenAI, srv.URL))

	entry, err := s.regs.ResolveModel(ctx, s.registerModel(t, "gwmodel", "openai", "gpt-5"))
	if err != nil {
		t.Fatalf("ResolveModel: %v", err)
	}
	seed := int64(4242)
	resp, err := executor.CallProvider(ctx, gw, entry,
		providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: "hi"}}},
		executor.NodeInvocation{RunID: "run_e2e", NodeID: fx.Nodes["classify"], AttemptGroup: 0, Seed: &seed})
	if err != nil {
		t.Fatalf("CallProvider: %v", err)
	}

	// (6) idempotency: three requests, ONE charge.
	mu.Lock()
	gotReq, gotCharges, seedSeen := requests, charges, gotSeed
	mu.Unlock()
	if gotReq != 3 {
		t.Fatalf("the provider saw %d requests, want 3", gotReq)
	}
	if gotCharges != 1 {
		t.Errorf("a retried call billed %d times, want 1 (PRD §11)", gotCharges)
	}
	// seed propagation (task 5.3) reached the provider.
	if seedSeen != float64(4242) {
		t.Errorf("the run's seed did not reach the provider: %v", seedSeen)
	}
	// (7) provider swap: the response is normalized regardless of who answered.
	if resp.Content != "ok" || resp.StopReason != providergateway.StopEndTurn || resp.Provider != "openai" {
		t.Errorf("the response was not normalized: %+v", resp)
	}

	// ONE node_execution row for the retried invocation (task 5.2).
	if err := s.runs.Start(ctx, "run_e2e", r.ConfigHash, fx.Rev, seed); err != nil {
		t.Fatalf("Start: %v", err)
	}
	in, _ := s.blobs.Put(ctx, []byte(`{"q":"hi"}`))
	out, _ := s.blobs.Put(ctx, []byte(`{"answer":"ok"}`))
	n := executor.NodeResult{
		NodeID: fx.Nodes["classify"], AttemptGroup: 0, Status: executor.StatusSucceeded,
		InputBlobHash: in, OutputBlobHash: out,
		IdempotencyKey: executor.IdempotencyKey("run_e2e", fx.Nodes["classify"], 0),
	}
	if err := s.runs.RecordNode(ctx, "run_e2e", n); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	if err := s.runs.RecordNode(ctx, "run_e2e", n); err != nil { // the redelivery
		t.Fatalf("re-RecordNode: %v", err)
	}
	var rows int
	if err := testDB.QueryRow(`SELECT count(*) FROM node_execution WHERE run_id='run_e2e'`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 1 {
		t.Errorf("a retried node wrote %d rows, want 1", rows)
	}
	if err := s.runs.Finish(ctx, &executor.Run{RunID: "run_e2e", Status: executor.StatusSucceeded}); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}

// The codemod's half of provider swap: refused, because rewriting a model string does not turn an
// anthropic SDK call into an OpenAI one. FR12 makes swaps transparent through the GATEWAY.
func TestE2E_ProviderSwapAtTheCallSiteIsRefused(t *testing.T) {
	fx := newFixture(t)
	seedWorkflow(t, fx)
	s := newStack(t, fx)

	m := s.registerModel(t, "openai-swap", "openai", "gpt-5")
	spec := s.spec(map[string]variantspec.NodeOverride{fx.Nodes["classify"]: {ModelRef: m}})
	_, _, applied, err := s.pipeline(t, spec, "swap")
	if !errors.Is(err, transform.ErrUnsafeRewrite) {
		t.Fatalf("want ErrUnsafeRewrite for a call-site provider swap, got %v", err)
	}
	if applied != nil {
		t.Error("a refused swap still produced an applied transform")
	}
	if !strings.Contains(err.Error(), "openai") || !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("the refusal should name both providers: %v", err)
	}
}

// 8.2 (9) contract halt: a node whose output violates its io_contract halts the run, naming the node,
// and the run's terminal status is `halted` — not `failed`.
func TestE2E_ContractViolationHaltsTheRunAndIsRecordedAsHalted(t *testing.T) {
	fx := newFixture(t)
	seedWorkflow(t, fx)
	s := newStack(t, fx)
	ctx := context.Background()

	spec := s.spec(nil) // the baseline: it builds, so there is something to run
	r, _, applied, err := s.pipeline(t, spec, "halt")
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if applied.Status != worktree.StatusBuilt {
		t.Fatalf("build: %s", applied.BuildLog)
	}

	// A workflow that emits an output violating the contract (answer must be a string).
	node := fx.Nodes["classify"]
	sh := filepath.Join(applied.Dir, "emit.sh")
	rec := fmt.Sprintf(`{"invocation_id":"i1","node_id":%q,"run_id":"run_halt_e2e","invocation_index":0,"output":{"answer":42}}`, node)
	if err := os.WriteFile(sh, []byte("#!/bin/sh\nprintf '%s\\n' '"+rec+"' >> \"$HEROS_INVOCATION_LOG\"\nsleep 5\n"), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}

	e := executor.New(s.blobs)
	run, err := e.Run(ctx, executor.Options{
		Dir: applied.Dir, Cmd: []string{"/bin/sh", sh}, RunID: "run_halt_e2e", Seed: 1, IR: fx.IR,
	})
	if !errors.Is(err, executor.ErrContractViolation) {
		t.Fatalf("want ErrContractViolation, got %v", err)
	}
	if run.Status != executor.StatusHalted {
		t.Errorf("status = %q, want halted — the workflow did not break, we stopped it", run.Status)
	}
	if run.HaltedNodeID != node {
		t.Errorf("the halt names %q, want %q", run.HaltedNodeID, node)
	}

	// Persisted as `halted`, with the node — which is what the UI renders.
	if err := s.runs.Start(ctx, "run_halt_e2e", r.ConfigHash, fx.Rev, 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, n := range run.Nodes {
		if err := s.runs.RecordNode(ctx, "run_halt_e2e", n); err != nil {
			t.Fatalf("RecordNode: %v", err)
		}
	}
	if err := s.runs.Finish(ctx, run); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	got, err := s.runs.Get(ctx, "run_halt_e2e")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != executor.StatusHalted || got.HaltedNodeID != node || got.HaltedReason == "" {
		t.Errorf("the halt was not recorded legibly: %+v", got)
	}
}

// An override that INSERTS an absent field, end to end through a real build. The `summarize` node
// never sets a model, and the rewrite must still compile.
func TestE2E_OverridingANodeThatNeverSetAModelInsertsTheFieldAndBuilds(t *testing.T) {
	fx := newFixture(t)
	seedWorkflow(t, fx)
	s := newStack(t, fx)

	m := s.registerModel(t, "insert", "anthropic", "claude-haiku-4-5")
	spec := s.spec(map[string]variantspec.NodeOverride{fx.Nodes["summarize"]: {ModelRef: m}})
	_, p, applied, err := s.pipeline(t, spec, "insert")
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if applied.Status != worktree.StatusBuilt {
		t.Fatalf("the inserted field does not compile:\n%s\n--- diff ---\n%s", applied.BuildLog, p.Diff)
	}
	if !strings.Contains(string(p.Diff), `"claude-haiku-4-5"`) {
		t.Errorf("the field was not inserted:\n%s", p.Diff)
	}
}

// PRD §13's HEADLINE exit criterion: "A hardcoded graph is transformed, built, and run end to end
// from the transformed working copy, producing per-node I/O and a terminal run status."
//
// Every other test here proves a link. This one walks the entire chain in one go and ends with a
// terminal status and per-node I/O in Postgres — which is the sentence the exit checklist actually
// asks someone to sign.
//
// It runs the BUILT copy (executor.Options.Dir is applied.Dir), which is the whole point of ADR-001:
// the code under measurement is the code that would ship.
func TestE2E_M2_HardcodedGraphIsTransformedBuiltAndRunEndToEnd(t *testing.T) {
	fx := newFixture(t)
	seedWorkflow(t, fx)
	s := newStack(t, fx)
	ctx := context.Background()

	// 1 · configure: override ONE node's model.
	m := s.registerModel(t, "m2exit", "anthropic", "claude-m2-exit")
	spec := s.spec(map[string]variantspec.NodeOverride{fx.Nodes["classify"]: {ModelRef: m}})

	// 2 · resolve → hash → persist → generate → apply → build.
	r, p, applied, err := s.pipeline(t, spec, "m2exit")
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if applied.Status != worktree.StatusBuilt {
		t.Fatalf("the transform did not build:\n%s", applied.BuildLog)
	}
	if len(p.Diff) == 0 {
		t.Fatal("no reviewable diff was produced")
	}

	// 3 · run the BUILT, TRANSFORMED copy. The workflow reports one invocation per node, each
	// conforming to its io_contract.
	sh := filepath.Join(applied.Dir, "run.sh")
	var body strings.Builder
	body.WriteString("#!/bin/sh\nset -e\n")
	order := []string{fx.Nodes["classify"], fx.Nodes["resolve"], fx.Nodes["summarize"]}
	for i, node := range order {
		rec := fmt.Sprintf(`{"invocation_id":"i%d","node_id":%q,"run_id":"run_m2","invocation_index":0,`+
			`"input":{"q":"ticket %d"},"output":{"answer":"triaged %d"}}`, i, node, i, i)
		body.WriteString("printf '%s\\n' '" + rec + "' >> \"$HEROS_INVOCATION_LOG\"\n")
	}
	if err := os.WriteFile(sh, []byte(body.String()), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}

	e := executor.New(s.blobs)
	run, err := e.Run(ctx, executor.Options{
		Dir: applied.Dir, Cmd: []string{"/bin/sh", sh}, RunID: "run_m2", Seed: 99, IR: fx.IR,
	})
	if err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, run.Stderr)
	}

	// 4 · a terminal run status, and per-node I/O.
	if run.Status != executor.StatusSucceeded {
		t.Fatalf("terminal status = %q, want succeeded", run.Status)
	}
	if len(run.Nodes) != 3 {
		t.Fatalf("recorded %d node executions, want 3 (one per node in the graph)", len(run.Nodes))
	}

	// 5 · persist it, and read it back the way the UI does.
	if err := s.runs.Start(ctx, "run_m2", r.ConfigHash, fx.Rev, 99); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, n := range run.Nodes {
		if err := s.runs.RecordNode(ctx, "run_m2", n); err != nil {
			t.Fatalf("RecordNode %s: %v", n.NodeID, err)
		}
	}
	if err := s.runs.Finish(ctx, run); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	got, err := s.runs.Get(ctx, "run_m2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != executor.StatusSucceeded {
		t.Errorf("persisted status = %q, want succeeded", got.Status)
	}
	if got.Seed != 99 || got.ConfigHash != r.ConfigHash {
		t.Errorf("the run is not attributed to its configuration: %+v", got)
	}
	if len(got.Nodes) != 3 {
		t.Fatalf("persisted %d node executions, want 3", len(got.Nodes))
	}
	// Per-node I/O: every node has BOTH input and output as content-addressed blob references, and
	// each carries its idempotency key. This is what the inspect view renders.
	for _, n := range got.Nodes {
		if len(n.InputBlobHash) != 64 || len(n.OutputBlobHash) != 64 {
			t.Errorf("node %s has no per-node I/O: in=%q out=%q", n.NodeID, n.InputBlobHash, n.OutputBlobHash)
		}
		if n.IdempotencyKey == "" {
			t.Errorf("node %s has no idempotency key", n.NodeID)
		}
		// The blobs are readable — a reference that resolves to nothing is not I/O.
		if _, err := s.blobs.Get(ctx, n.OutputBlobHash); err != nil {
			t.Errorf("node %s's output blob does not resolve: %v", n.NodeID, err)
		}
	}
	// And the transform is on record as the reviewable artifact it is.
	rec, diff, err := s.tstore.Get(ctx, r.ConfigHash, fx.Rev)
	if err != nil {
		t.Fatalf("transform Get: %v", err)
	}
	if rec.Status != worktree.StatusBuilt || len(diff) == 0 || rec.Commit == "" {
		t.Errorf("the transform record is not a complete review artifact: %+v", rec)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

// changedLines returns only the +/- lines of a diff — the lines the transform actually edited.
// Context lines are unchanged code and asserting on them confuses "the diff shows it" with "the
// transform touched it".
func changedLines(d string) []string {
	var out []string
	for _, l := range strings.Split(d, "\n") {
		switch {
		case strings.HasPrefix(l, "---"), strings.HasPrefix(l, "+++"):
		case strings.HasPrefix(l, "-"), strings.HasPrefix(l, "+"):
			out = append(out, l)
		}
	}
	return out
}

func countDiffLines(d string) (minus, plus int) {
	for _, l := range strings.Split(d, "\n") {
		switch {
		case strings.HasPrefix(l, "---"), strings.HasPrefix(l, "+++"):
		case strings.HasPrefix(l, "-"):
			minus++
		case strings.HasPrefix(l, "+"):
			plus++
		}
	}
	return
}

// hashTree hashes every file under dir except .git, whose bytes legitimately churn.
func hashTree(t *testing.T, dir string) string {
	t.Helper()
	h := newHasher()
	var files []string
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sortStrings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		rel, _ := filepath.Rel(dir, f)
		h.add(rel, b)
	}
	return h.sum()
}
