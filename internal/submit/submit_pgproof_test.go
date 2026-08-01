//go:build pgproof

// The live proof of the submit path (task 7.2).
//
// # Why every assertion here re-reads
//
// "HTTP 200 is not evidence of a write." Neither is a Go function returning nil. Every test below
// asserts by reading the row back through the SAME path the product reads it through — the stores the
// UI's GET handlers call, and SQL against the tables themselves for the absence checks. A test that
// stopped at Submit's return value would prove Submit returns, which is not the claim.
//
// Real everything, because the seams are where P2's bugs have been: real Postgres, a real bare clone,
// a real `go build`, a real discovery run over a real checkout. The only thing not exercised here is a
// provider call, and nothing in submit makes one — the run is enqueued, not executed.
package submit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/runqueue"
	"github.com/heros-foreal/agentd/internal/variantspec"
	"github.com/heros-foreal/agentd/internal/worktree"
)

var testDB *sql.DB

func TestMain(m *testing.M) {
	db, err := pgtest.Open("proof_submit")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1) // fail, never skip: a proof that skips reports green for something it never checked
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

// harness is a Service wired the way production wires it, over a real target repo.
type harness struct {
	svc   *Service
	repo  string // the user's repo — submit must never mutate it
	rev   string
	regs  *registry.Store
	specs *variantspec.Store
	trans *worktree.Store
	runs  *executor.Store
	queue *runqueue.Queue
	nodes map[string]string // enclosing func -> node_id
	// verifierLangs records every language the Service asked for a gate for, in order. It is what
	// TestSubmit_UsesTheVerifierForTheDiscoveredLanguage asserts on: the ONLY way to see that the
	// language reaching worktree.VerifierFor is the one Discovery reported, rather than a constant.
	verifierLangs []string
}

// newHarness builds a real target repo, a real pool over it, and a real Service.
//
// The target is internal/e2e's fixture, reused rather than re-invented: it is task 8.1's pinned
// 3-node buildable graph, and a second fixture would be a second definition of what a target repo
// looks like — with its own drift.
func newHarness(t *testing.T) *harness {
	t.Helper()
	goBin, _ := exec.LookPath("go")
	return newHarnessWith(t, worktree.GoVerifier{GoBin: goBin, Env: buildEnv()})
}

// rejectingVerifier is a build gate that refuses, reporting what a compiler reports.
//
// It is here because a build rejection is UNREACHABLE through the real toolchain from this fixture,
// and that is by design rather than an oversight: the transform engine's minimality gate refuses to
// emit anything that does not parse, and the SDK stub's Model is a named string type, so every model
// override the engine WILL emit happens to compile. internal/e2e reaches the state by hand-corrupting
// a generated patch — it has to, because it is testing the gate itself.
//
// This test is not testing the gate. It is testing what SUBMIT does when the gate says no: record the
// transform, attribute it, and enqueue nothing. Verifier is an interface precisely because the gate is
// the target's business (a Go repo runs `go build`; the other six languages do not), so supplying one
// that fails is using the seam, not faking the subject. The real GoVerifier runs in every other test
// in this file.
//
// It reports type-checked, which is what a REAL Go rejection reports: `go build` type-checked the
// program and the program was not well-typed. A rejection still says which gate rejected it (ADR-003).
//
// 🔴 It rejects the TRANSFORMED tree only, by looking for the value the spec injects — and that is the
// whole point, not a detail. An earlier version rejected unconditionally, which quietly made it a model
// of something else entirely: a gate that cannot read this repository at all. Since ErrBaselineFails,
// Apply asks the pristine tree before blaming a diff, and an always-rejecting gate now correctly
// answers "this worker cannot judge anything here" instead of "your transform does not build". The stub
// was fine while nobody asked it the second question; it became a lie the moment somebody did.
//
// A real gate is a function of the tree it is handed. So is this one.
type rejectingVerifier struct {
	log string
	// rejectIfPresent is the value the Variant Spec writes into the source. Present ⇒ this is the
	// transformed tree ⇒ reject. Absent ⇒ this is the pristine tree ⇒ pass, exactly as `go build` would.
	rejectIfPresent string
}

func (b rejectingVerifier) Verify(_ context.Context, dir string) (worktree.Verification, error) {
	src, err := os.ReadFile(filepath.Join(dir, "pipeline.go"))
	if err != nil {
		return worktree.Verification{}, fmt.Errorf("rejectingVerifier: read the tree it is judging: %w", err)
	}
	if b.rejectIfPresent == "" || !strings.Contains(string(src), b.rejectIfPresent) {
		return worktree.Verification{
			Strength: worktree.StrengthTypeChecked, Tool: "go build ./...",
		}, nil
	}
	return worktree.Verification{
		Strength: worktree.StrengthTypeChecked, Tool: "go build ./...", Log: b.log,
	}, errors.New("exit status 1")
}

func newHarnessWith(t *testing.T, builder worktree.Verifier) *harness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err)
	}
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "sdkstub"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for src, dst := range map[string]string{
		"../e2e/testdata/target/go.mod.txt":         "go.mod",
		"../e2e/testdata/target/pipeline.go.txt":    "pipeline.go",
		"../e2e/testdata/target/sdkstub/go.mod.txt": "sdkstub/go.mod",
		"../e2e/testdata/target/sdkstub/sdk.go.txt": "sdkstub/sdk.go",
	} {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read %s: %v", src, err)
		}
		if err := os.WriteFile(filepath.Join(repo, dst), b, 0o600); err != nil {
			t.Fatalf("write %s: %v", dst, err)
		}
	}
	// A per-test marker so this fixture's commit sha — and therefore its source_revision — is unique.
	// Without it two tests committing in the same second get the SAME sha, and every assertion scoped
	// by source_revision would see its neighbours' rows. The fail-closed test, which asserts on the
	// ABSENCE of rows, is exactly the one that would break. (Same reasoning as internal/e2e.)
	if err := os.WriteFile(filepath.Join(repo, "fixture_id.txt"), []byte(t.Name()), 0o600); err != nil {
		t.Fatalf("write fixture id: %v", err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "base"}} {
		if out, err := gitCmd(repo, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	rev, err := gitCmd(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}

	root := t.TempDir()
	fs, err := registry.NewFSBlobStore(filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}
	// A CATALOGING store, as production must use: node_execution's FK to blob(content_hash) requires
	// every referenced blob to have a catalog row.
	blobs := registry.NewCatalogingBlobStore(testDB, fs, "application/json")
	pool, err := worktree.NewPool(context.Background(), repo, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	cache, err := worktree.NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	h := &harness{
		repo: repo, rev: strings.TrimSpace(rev),
		regs: registry.NewStore(testDB, blobs), specs: variantspec.NewStore(testDB),
		trans: worktree.NewStore(testDB, blobs), runs: executor.NewStore(testDB),
		queue: runqueue.New(testDB), nodes: map[string]string{},
	}
	svc, err := New(Deps{
		Pool: pool, Registries: h.regs, Specs: h.specs, Cache: cache,
		// The harness pins ONE gate for every language, which is what these tests want: they exercise
		// submit's orchestration against a Go fixture, and a test's verifier is chosen by the test, not
		// by a table. The per-language table itself is asserted in internal/worktree and by
		// TestSubmit_UsesTheVerifierForTheDiscoveredLanguage below.
		Verifiers: func(lang string) (worktree.Verifier, error) {
			h.verifierLangs = append(h.verifierLangs, lang)
			return builder, nil
		},
		Transforms: h.trans, Runs: h.runs, Queue: h.queue,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.svc = svc

	// Name the call sites by what they are, from the same indexer discovery uses.
	sites, err := indexNodes(repo)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	h.nodes = sites
	if len(h.nodes) != 3 {
		t.Fatalf("the fixture discovered %d call sites, want 3", len(h.nodes))
	}
	return h
}

// buildEnv pins the build's environment: inheriting the operator's GOFLAGS or GOPROXY would make two
// runs of one config_hash build differently.
func buildEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"), // the module cache lives here
		"GOFLAGS=-mod=mod",
		"GOPROXY=off", // the fixture resolves through its local replace; nothing may be fetched
		"GOCACHE=" + os.Getenv("HOME") + "/Library/Caches/go-build",
	}
}

func gitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+os.TempDir())
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// spec builds a Variant Spec over the fixture's three nodes, in a stable order.
func (h *harness) spec(overrides map[string]variantspec.NodeOverride) *variantspec.VariantSpec {
	order := []string{h.nodes["classify"], h.nodes["resolve"], h.nodes["summarize"]}
	if overrides == nil {
		overrides = map[string]variantspec.NodeOverride{}
	}
	return &variantspec.VariantSpec{
		WorkflowID: "wf_submit", SourceRevision: h.rev, Order: order, Nodes: overrides,
		Edges: []variantspec.Edge{
			{FromNodeID: order[0], ToNodeID: order[1], Kind: "data"},
			{FromNodeID: order[1], ToNodeID: order[2], Kind: "data"},
		},
	}
}

func (h *harness) registerModel(t *testing.T, name, provider, modelID string) string {
	t.Helper()
	maxTok := 256
	id, err := h.regs.RegisterModel(context.Background(), name, registry.ModelSpec{
		Provider: provider, ModelID: modelID, Params: registry.ModelParams{MaxTokens: &maxTok},
	})
	if err != nil {
		t.Fatalf("RegisterModel: %v", err)
	}
	return id
}

// count is the absence check's read path: SQL against the table itself.
func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := testDB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// ── the proofs ───────────────────────────────────────────────────────────────────────────────────

// The headline: a submitted spec becomes a persisted spec, a generated transform, AND a queued run —
// each proved by reading it back through the path the product consumes it through.
//
// This is what task 7.2 claimed and what did not exist: before submit, nothing outside a test helper
// wrote any of these three rows.
func TestPGSubmit_PersistsTheSpecTheTransformAndTheQueuedRun(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	before := hashTree(t, h.repo)

	m := h.registerModel(t, "submit-sonnet", "anthropic", "claude-sonnet-5")
	spec := h.spec(map[string]variantspec.NodeOverride{h.nodes["classify"]: {ModelRef: m}})

	out, err := h.svc.Submit(ctx, Request{Spec: spec, VariantID: "v_submit", Label: "submit", Seed: 7})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.TransformStatus != worktree.StatusBuilt {
		t.Fatalf("transform status = %q, want built", out.TransformStatus)
	}
	if out.RunID == "" {
		t.Fatal("a built transform was not enqueued: no run_id")
	}

	// 1 · the SPEC row — read back through variantspec.Store.Get, which re-verifies that the stored
	// lineage still hashes to the config_hash it is filed under.
	gotSpec, lineage, err := h.specs.Get(ctx, out.ConfigHash, h.rev)
	if err != nil {
		t.Fatalf("the spec was not persisted: %v", err)
	}
	if gotSpec.SourceRevision != h.rev || len(gotSpec.Order) != 3 {
		t.Errorf("the stored spec is not the one submitted: %+v", gotSpec)
	}
	if len(lineage.Nodes) != 3 {
		t.Errorf("the stored lineage has %d nodes, want 3", len(lineage.Nodes))
	}
	// The override actually made it into the hashed configuration — otherwise config_hash names a
	// configuration that is not the one the user asked for.
	var found bool
	for _, n := range lineage.Nodes {
		if n.NodeID == h.nodes["classify"] && strings.Contains(n.ModelRef, "claude-sonnet-5") {
			found = true
		}
	}
	if !found {
		t.Errorf("the submitted override is not in the stored lineage: %+v", lineage.Nodes)
	}

	// 2 · the TRANSFORM row — read back through the store the UI's GET /api/v1/transforms handler uses.
	rec, diff, err := h.trans.Get(ctx, out.ConfigHash, h.rev)
	if err != nil {
		t.Fatalf("the transform was not recorded: %v", err)
	}
	if rec.Status != worktree.StatusBuilt {
		t.Errorf("recorded transform status = %q, want built", rec.Status)
	}
	if len(diff) == 0 {
		t.Error("the transform has no reviewable diff — the diff IS the product's output (ADR-001)")
	}
	if !strings.Contains(string(diff), `"claude-sonnet-5"`) {
		t.Errorf("the diff does not carry the override:\n%s", diff)
	}
	if rec.Commit == "" || rec.Branch == "" {
		t.Errorf("the transform is not a reviewable change: %+v", rec)
	}

	// 3 · the RUN row — read back through the store the UI's GET /api/v1/runs handler uses. It must be
	// watchable the instant submit returns, or a successful submit looks like a broken one.
	run, err := h.runs.Get(ctx, out.RunID)
	if err != nil {
		t.Fatalf("the run is not readable at the id submit returned: %v", err)
	}
	if run.Status != executor.StatusRunning {
		t.Errorf("run status = %q, want running", run.Status)
	}
	if run.ConfigHash != out.ConfigHash || run.SourceRevision != h.rev || run.Seed != 7 {
		t.Errorf("the run is not attributed to what was submitted: %+v", run)
	}

	// 4 · the QUEUE row — the run is actually dispatchable, proved by dequeuing it. Reading run_queue
	// with SQL would prove a row exists; dequeuing proves the thing the queue is FOR.
	item, err := h.queue.Dequeue(ctx, "proof-worker")
	if err != nil {
		t.Fatalf("the run was not dispatchable: %v", err)
	}
	if item.RunID != out.RunID || item.ConfigHash != out.ConfigHash || item.Seed != 7 {
		t.Errorf("the queued item is not the submitted run: %+v", item)
	}

	// 5 · the user's tree is byte-for-byte untouched. Submit checks out, discovers, generates, and
	// builds — all of it must happen somewhere else (ADR-001's absolute rule).
	if after := hashTree(t, h.repo); after != before {
		t.Error("submit mutated the user's working tree")
	}
}

// FR11, fail-closed: a dangling ref aborts with NO side effects — no spec, no transform, no run, no
// queue item — and names the node and the dimension (task 7.4).
//
// The absence is the assertion. This is the test that would catch a submit that wrote the spec first
// and resolved afterwards, which is the natural way to get this wrong.
func TestPGSubmit_DanglingRefFailsClosedAndWritesNothing(t *testing.T) {
	h := newHarness(t)

	spec := h.spec(map[string]variantspec.NodeOverride{
		h.nodes["classify"]: {ModelRef: strings.Repeat("0", 64)}, // resolves to nothing
	})
	_, err := h.svc.Submit(context.Background(), Request{Spec: spec, VariantID: "v_dangle", Seed: 0})
	if !errors.Is(err, variantspec.ErrUnresolvedRef) {
		t.Fatalf("want ErrUnresolvedRef, got %v", err)
	}
	// Task 7.4: WHICH node and WHICH dimension. "Invalid spec" is not actionable.
	var se *variantspec.SpecError
	if !errors.As(err, &se) {
		t.Fatalf("the rejection is not a *SpecError: %T", err)
	}
	if se.NodeID != h.nodes["classify"] || se.Dim != variantspec.DimModel {
		t.Errorf("the rejection names node=%q dim=%q, want the classify node / model", se.NodeID, se.Dim)
	}
	if se.Ref == "" {
		t.Error("the rejection does not say WHICH ref dangled")
	}

	// Nothing, anywhere. Scoped by this fixture's unique source_revision.
	for _, q := range []struct{ what, sql string }{
		{"variant_spec", `SELECT count(*) FROM variant_spec WHERE source_revision=$1`},
		{"transform", `SELECT count(*) FROM transform WHERE source_revision=$1`},
		{"run", `SELECT count(*) FROM run WHERE source_revision=$1`},
		{"run_queue", `SELECT count(*) FROM run_queue WHERE source_revision=$1`},
	} {
		if n := count(t, q.sql, h.rev); n != 0 {
			t.Errorf("a dangling ref left %d %s row(s); submit must abort before ANY side effect", n, q.what)
		}
	}
	// And no variant row either: a submission that failed closed must not leave a variant behind that
	// a user would then see in a list, pointing at nothing.
	if n := count(t, `SELECT count(*) FROM variant WHERE variant_id=$1`, "v_dangle"); n != 0 {
		t.Errorf("a failed-closed submission created %d variant row(s)", n)
	}
}

// Submitting the same spec twice with the same seed is IDEMPOTENT: no duplicate rows anywhere, and
// the second submit returns the same coordinates as the first.
//
// # The semantics, and why they are these
//
// A run is identified by {config_hash, source_revision, seed} — 0005's own comment calls that the
// reproducibility unit, and config-hash-spec §5 keeps seed out of config_hash precisely so that seeds
// of one configuration roll up together. Two submissions agreeing on all three describe the SAME
// experiment. The platform is deterministic by construction, so executing it again could only
// reproduce the first answer at the cost of a second build and a second provider bill — so the second
// submit collapses onto the first run rather than forking a new one.
//
// The converse is proved below: a different seed is a different experiment and MUST get its own run.
// Collapsing those would break multi-seed variance measurement, which is the reason seed exists.
func TestPGSubmit_ResubmittingTheSameSpecIsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	m := h.registerModel(t, "submit-idem", "anthropic", "claude-idem-5")
	spec := h.spec(map[string]variantspec.NodeOverride{h.nodes["classify"]: {ModelRef: m}})
	req := Request{Spec: spec, VariantID: "v_idem", Label: "idem", Seed: 3}

	first, err := h.svc.Submit(ctx, req)
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	second, err := h.svc.Submit(ctx, req)
	if err != nil {
		t.Fatalf("re-submitting an identical spec failed instead of collapsing onto the first: %v", err)
	}

	if second.ConfigHash != first.ConfigHash {
		t.Errorf("config_hash is not deterministic: %s vs %s", first.ConfigHash, second.ConfigHash)
	}
	if second.RunID != first.RunID {
		t.Errorf("a re-submit forked a new run (%s vs %s); the same experiment would be built and "+
			"billed twice", first.RunID, second.RunID)
	}

	// One row each. This is the assertion that matters: the returned ids agreeing proves the derivation
	// is stable, but only counting rows proves nothing was written twice.
	if n := count(t, `SELECT count(*) FROM variant_spec WHERE config_hash=$1 AND source_revision=$2`,
		first.ConfigHash, h.rev); n != 1 {
		t.Errorf("variant_spec has %d rows for one configuration, want 1", n)
	}
	if n := count(t, `SELECT count(*) FROM config WHERE config_hash=$1`, first.ConfigHash); n != 1 {
		t.Errorf("config has %d rows for one config_hash, want 1", n)
	}
	if n := count(t, `SELECT count(*) FROM transform WHERE config_hash=$1 AND source_revision=$2`,
		first.ConfigHash, h.rev); n != 1 {
		t.Errorf("transform has %d rows for one configuration, want 1", n)
	}
	if n := count(t, `SELECT count(*) FROM run WHERE config_hash=$1 AND seed=3`, first.ConfigHash); n != 1 {
		t.Errorf("run has %d rows for one experiment, want 1", n)
	}
	if n := count(t, `SELECT count(*) FROM run_queue WHERE run_id=$1`, first.RunID); n != 1 {
		t.Errorf("run_queue has %d items for one run, want 1", n)
	}
	// And exactly ONE dispatch: a second queue item would mean the same run executed twice.
	if _, err := h.queue.Dequeue(ctx, "w1"); err != nil {
		t.Fatalf("the run is not dispatchable: %v", err)
	}
	if _, err := h.queue.Dequeue(ctx, "w2"); !errors.Is(err, runqueue.ErrEmpty) {
		t.Errorf("a re-submit left a second dispatchable item; the run would execute twice (got %v)", err)
	}
}

// The converse of idempotency: a re-submit with a DIFFERENT seed is a different experiment and gets
// its own run against the same, already-built transform.
//
// This is what seed is for — multi-seed runs of one configuration are how variance is measured
// (config-hash-spec §5) — so collapsing them would be a bug, not a saving.
func TestPGSubmit_ADifferentSeedIsADifferentRunOnTheSameTransform(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	m := h.registerModel(t, "submit-seeds", "anthropic", "claude-seeds-5")
	spec := h.spec(map[string]variantspec.NodeOverride{h.nodes["classify"]: {ModelRef: m}})

	one, err := h.svc.Submit(ctx, Request{Spec: spec, VariantID: "v_seeds", Seed: 1})
	if err != nil {
		t.Fatalf("Submit seed 1: %v", err)
	}
	two, err := h.svc.Submit(ctx, Request{Spec: spec, VariantID: "v_seeds", Seed: 2})
	if err != nil {
		t.Fatalf("Submit seed 2: %v", err)
	}

	if one.ConfigHash != two.ConfigHash {
		t.Errorf("seed leaked into config_hash (%s vs %s); seeds of one configuration must share a "+
			"hash so their results roll up", one.ConfigHash, two.ConfigHash)
	}
	if one.RunID == two.RunID {
		t.Fatal("two seeds collapsed onto one run; variance across seeds would be unmeasurable")
	}
	// Two runs, ONE transform: the second seed reused the build rather than rebuilding it.
	if n := count(t, `SELECT count(*) FROM run WHERE config_hash=$1`, one.ConfigHash); n != 2 {
		t.Errorf("run has %d rows for two seeds, want 2", n)
	}
	if n := count(t, `SELECT count(*) FROM transform WHERE config_hash=$1`, one.ConfigHash); n != 1 {
		t.Errorf("transform has %d rows, want 1 — two seeds share one build", n)
	}
	for _, id := range []string{one.RunID, two.RunID} {
		run, err := h.runs.Get(ctx, id)
		if err != nil {
			t.Fatalf("run %s is not readable: %v", id, err)
		}
		if run.Status != executor.StatusRunning {
			t.Errorf("run %s status = %q, want running", id, run.Status)
		}
	}
}

// An unsafe rewrite (FR5) is refused BEFORE anything is applied, naming the node and the dimension —
// and it leaves no transform and no run.
//
// The fixture's prompt is a Messages construction, not a value expression, so a prompt override is
// exactly the call site the engine (correctly) refuses. The spec IS persisted by this point, which is
// deliberate and matches the existing chain: the configuration resolved and is a real, hashable
// configuration — it simply cannot be expressed as a codemod against this tree.
func TestPGSubmit_UnsafeRewriteIsRefusedWithNoTransformAndNoRun(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	pid, err := h.regs.RegisterPrompt(ctx, "submit/classify", "Classify this ticket. Be brief.")
	if err != nil {
		t.Fatalf("RegisterPrompt: %v", err)
	}
	spec := h.spec(map[string]variantspec.NodeOverride{h.nodes["classify"]: {PromptRef: pid}})

	_, err = h.svc.Submit(ctx, Request{Spec: spec, VariantID: "v_unsafe", Seed: 0})
	if err == nil {
		t.Fatal("an unsafe rewrite was accepted")
	}
	// It must be attributable, or the user cannot act on it. The API maps exactly this into the same
	// {node, dimension} wire shape a dangling ref produces.
	if !strings.Contains(err.Error(), "prompt") {
		t.Errorf("the refusal does not name the dimension: %v", err)
	}

	// Nothing was applied and nothing runs.
	if n := count(t, `SELECT count(*) FROM transform WHERE source_revision=$1`, h.rev); n != 0 {
		t.Errorf("a refused rewrite produced %d transform row(s)", n)
	}
	if n := count(t, `SELECT count(*) FROM run WHERE source_revision=$1`, h.rev); n != 0 {
		t.Errorf("a refused rewrite produced %d run(s)", n)
	}
	if n := count(t, `SELECT count(*) FROM run_queue WHERE source_revision=$1`, h.rev); n != 0 {
		t.Errorf("a refused rewrite queued %d run(s)", n)
	}
}

// A build-rejected transform is a legible TERMINAL state, and it is never enqueued (FR5b).
//
// This is the unhappy path task 7.1 asks to be designed first, and the one the UI renders as its own
// distinct state. Submit must NOT treat it as an error — a rejection is an answer — so the assertions
// are: no Go error, a recorded transform carrying the compiler's reason, and nothing in the queue.
func TestPGSubmit_ABuildRejectedTransformIsRecordedAndNeverEnqueued(t *testing.T) {
	const compilerSaid = "./pipeline.go:8:62: cannot use \"claude-rejected-5\" (untyped string " +
		"constant) as anthropic.Model value"
	h := newHarnessWith(t, rejectingVerifier{log: compilerSaid, rejectIfPresent: "claude-rejected-5"})
	ctx := context.Background()

	m := h.registerModel(t, "submit-reject", "anthropic", "claude-rejected-5")
	spec := h.spec(map[string]variantspec.NodeOverride{h.nodes["classify"]: {ModelRef: m}})

	out, err := h.svc.Submit(ctx, Request{Spec: spec, VariantID: "v_rej", Seed: 0})
	// NOT an error. A transform that does not build is a product state, and modelling it as a failure
	// is how it becomes indistinguishable from the server falling over.
	if err != nil {
		t.Fatalf("a build rejection was reported as an error rather than an outcome: %v", err)
	}
	if out.TransformStatus != worktree.StatusBuildRejected {
		t.Fatalf("transform status = %q, want build-rejected", out.TransformStatus)
	}
	if out.RunID != "" {
		t.Errorf("a build-rejected transform was given run %q; it must never run", out.RunID)
	}

	// The transform IS recorded, read back through the store the UI's GET handler uses — a rejection
	// nobody can inspect is a dead end for whoever has to fix the spec.
	rec, _, err := h.trans.Get(ctx, out.ConfigHash, h.rev)
	if err != nil {
		t.Fatalf("the rejection was not recorded: %v", err)
	}
	if rec.Status != worktree.StatusBuildRejected {
		t.Errorf("recorded status = %q, want build-rejected", rec.Status)
	}
	if !strings.Contains(rec.BuildLog, "claude-rejected-5") {
		t.Errorf("the record lost the compiler's reason, which is the only actionable part:\n%s", rec.BuildLog)
	}

	// No run, and nothing dispatchable.
	if n := count(t, `SELECT count(*) FROM run WHERE config_hash=$1`, out.ConfigHash); n != 0 {
		t.Errorf("a build-rejected transform has %d run(s); it must never run", n)
	}
	if n := count(t, `SELECT count(*) FROM run_queue WHERE config_hash=$1`, out.ConfigHash); n != 0 {
		t.Errorf("a build-rejected transform queued %d run(s)", n)
	}
	// NOT asserted by dequeuing: Dequeue takes the queue's head regardless of configuration, and these
	// proofs share one schema, so it would happily return a NEIGHBOURING test's run and fail this one
	// for something it did not do. The scoped counts above are the honest read.

	// The system-wide invariant, read from the records rather than from this call: every queued run
	// points at a transform that BUILT.
	if n := count(t, `SELECT count(*) FROM run_queue q JOIN transform t
	                  ON q.config_hash = t.config_hash AND q.source_revision = t.source_revision
	                  WHERE t.build_status <> 'built'`); n != 0 {
		t.Errorf("%d queued run(s) point at a transform that did not build", n)
	}
}

// A Service missing a collaborator refuses to be built, rather than discovering it after a compile.
func TestPGSubmit_AMisconfiguredServiceRefusesToStart(t *testing.T) {
	_, err := New(Deps{}) // nothing wired
	if err == nil {
		t.Fatal("a Service with no collaborators was constructed; it would fail after building a transform")
	}
	for _, want := range []string{"Pool", "Queue", "Registries"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name the missing %s: %v", want, err)
		}
	}
}

// ── ADR-003: the gate is chosen by the language Discovery reported ───────────────────────────────

// 🔴 The wiring ADR-003 decision 1/2 turns on: Discovery states the workflow's language, and THAT is
// what selects the verifier. Before this, the Service held one Applier — and therefore one gate — for
// every submission, which is the Go-only shape the ADR exists to remove.
//
// Asserting the language that reached the seam, not just that a gate ran: a Service that hard-coded
// "go" would pass every other test in this file, because every fixture here is Go. That is exactly how
// the original gap survived — 「本地跑通 ≠ 交付闭环」.
// 🔴 The fixture here is PYTHON, and that is the entire point.
//
// The first version of this test used the Go fixture and asserted the Service asked for a "go" gate.
// It passed — and it also passed when the Service was mutated to hard-code `applierFor("go")` and
// ignore the discovered language entirely. A test that cannot go red is not a fence; it is decoration
// that reports green about a property it never checked. Found by running exactly that mutation.
//
// With a Python target the assertion has teeth: a Service that hard-codes "go", or that reads the
// language from anywhere other than the IR discovery just produced, fails here.
func newPythonHarness(t *testing.T) *harness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 is required to verify a Python transform, and this worker has none: %v", err)
	}
	repo := t.TempDir()
	src := "import anthropic\n\nclient = anthropic.Anthropic()\n\n\n" +
		"def classify(ticket):\n    return client.messages.create(\n" +
		"        model=\"claude-opus-4-6\",\n" +
		"        messages=[{\"role\": \"user\", \"content\": \"Classify\"}],\n    )\n"
	if err := os.WriteFile(filepath.Join(repo, "pipeline.py"), []byte(src), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "fixture_id.txt"), []byte(t.Name()), 0o600); err != nil {
		t.Fatalf("write fixture id: %v", err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "base"}} {
		if out, err := gitCmd(repo, args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	rev, err := gitCmd(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}

	root := t.TempDir()
	fs, err := registry.NewFSBlobStore(filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatalf("blobs: %v", err)
	}
	blobs := registry.NewCatalogingBlobStore(testDB, fs, "application/json")
	pool, err := worktree.NewPool(context.Background(), repo, filepath.Join(root, "pool"))
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	cache, err := worktree.NewCache(filepath.Join(root, "cache"))
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	h := &harness{
		repo: repo, rev: strings.TrimSpace(rev),
		regs: registry.NewStore(testDB, blobs), specs: variantspec.NewStore(testDB),
		trans: worktree.NewStore(testDB, blobs), runs: executor.NewStore(testDB),
		queue: runqueue.New(testDB), nodes: map[string]string{},
	}
	// The REAL table — worktree.VerifierFor — so this test proves the production wiring end to end and
	// not a stand-in the test wrote itself. It only records which language was asked for.
	svc, err := New(Deps{
		Pool: pool, Registries: h.regs, Specs: h.specs, Cache: cache,
		Verifiers: func(lang string) (worktree.Verifier, error) {
			h.verifierLangs = append(h.verifierLangs, lang)
			return worktree.VerifierFor(lang)
		},
		Transforms: h.trans, Runs: h.runs, Queue: h.queue,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.svc = svc

	sites, err := discovery.IndexSpanCallSites(repo, "python", nil)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("the Python fixture discovered %d call sites, want 1", len(sites))
	}
	for id := range sites {
		h.nodes["classify"] = id
	}
	if _, err := testDB.Exec(
		`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		 VALUES ('wf_submit_py', $1, $2, 'python', '1.0.0') ON CONFLICT DO NOTHING`, repo, h.rev); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	return h
}

func TestSubmit_UsesTheVerifierForTheDiscoveredLanguage(t *testing.T) {
	h := newPythonHarness(t)
	m := h.registerModel(t, "py_sonnet", "anthropic", "claude-sonnet-5")

	out, err := h.svc.Submit(context.Background(), Request{
		Spec: &variantspec.VariantSpec{
			WorkflowID: "wf_submit_py", SourceRevision: h.rev,
			Order: []string{h.nodes["classify"]},
			Nodes: map[string]variantspec.NodeOverride{h.nodes["classify"]: {ModelRef: m}},
		},
		VariantID: "v_lang", Seed: 1,
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if out.TransformStatus != worktree.StatusBuilt {
		t.Fatalf("status = %q, want built", out.TransformStatus)
	}
	if len(h.verifierLangs) != 1 {
		t.Fatalf("the Service asked for %d verifiers, want exactly 1: %v", len(h.verifierLangs), h.verifierLangs)
	}
	if h.verifierLangs[0] != "python" {
		t.Errorf("the Service asked for a %q gate for a PYTHON workflow; the gate must follow the "+
			"language Discovery reported, not a constant", h.verifierLangs[0])
	}
	// And the whole point of ADR-003 landed: a non-Go repo went submit -> transform -> verify -> queued.
	if out.RunID == "" {
		t.Error("a built Python transform enqueued no run")
	}
}

// A language this platform has no gate for stops the submission DEAD, before anything is persisted or
// applied. It must: applying a codemod and only then discovering nothing can verify it would leave a
// branch and a worktree carrying an unverified change — and the one thing this pipeline promises is
// that an unverified transform is never proposed.
func TestSubmit_AnUnverifiableLanguageIsRefusedBeforeAnythingIsApplied(t *testing.T) {
	h := newHarness(t)
	// Stand in for a language the table has no row for. The Service must surface it, not shrug.
	h.svc.verifierFor = func(lang string) (worktree.Verifier, error) {
		return nil, worktree.ErrLanguageNotVerifiable
	}
	m := h.registerModel(t, "sonnet", "anthropic", "claude-sonnet-5")

	_, err := h.svc.Submit(context.Background(), Request{
		Spec:      h.spec(map[string]variantspec.NodeOverride{h.nodes["classify"]: {ModelRef: m}}),
		VariantID: "v_unverifiable", Seed: 1,
	})
	if !errors.Is(err, worktree.ErrLanguageNotVerifiable) {
		t.Fatalf("want ErrLanguageNotVerifiable, got %v", err)
	}
	// Nothing was written: the refusal happens above step 3, which is what makes "no partial state"
	// structural rather than a discipline.
	var n int
	if err := testDB.QueryRow(
		`SELECT COUNT(*) FROM transform WHERE source_revision = $1`, h.rev).Scan(&n); err != nil {
		t.Fatalf("count transforms: %v", err)
	}
	if n != 0 {
		t.Errorf("%d transform row(s) were written for a submission that could never be verified", n)
	}
}
