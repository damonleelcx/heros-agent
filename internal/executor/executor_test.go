package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// memBlobs is an in-memory BlobStore.
type memBlobs struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemBlobs() *memBlobs { return &memBlobs{data: map[string][]byte{}} }

func (m *memBlobs) Put(_ context.Context, b []byte) (string, error) {
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:])
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[h] = append([]byte(nil), b...)
	return h, nil
}

// testIR gives node n_a an output contract requiring {"answer": string}.
func testIR() *discovery.IR {
	return &discovery.IR{
		IRVersion: "1.0.0",
		Nodes: []discovery.IRNode{{
			NodeID: "n_a", Kind: "static_definition",
			IOContract: discovery.IRIOContract{
				InputSchema: map[string]any{"type": "object"},
				OutputSchema: map[string]any{
					"type":                 "object",
					"properties":           map[string]any{"answer": map[string]any{"type": "string"}},
					"required":             []any{"answer"},
					"additionalProperties": false,
				},
			},
		}},
	}
}

// script writes an executable shell script that emits invocation records, standing in for a built,
// transformed workflow. A real Go binary would prove nothing extra here — what is under test is our
// side of the contract: that we read what it emits, check it, and act.
func script(t *testing.T, dir, body string) []string {
	t.Helper()
	p := filepath.Join(dir, "workflow.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nset -e\n"+body), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return []string{"/bin/sh", p}
}

func emit(rec string) string {
	return fmt.Sprintf("printf '%%s\\n' '%s' >> \"$HEROS_INVOCATION_LOG\"\n", rec)
}

func baseOpts(t *testing.T, dir string, cmd []string) Options {
	t.Helper()
	return Options{Dir: dir, Cmd: cmd, RunID: "run1", Seed: 42, IR: testIR(), Timeout: 20 * time.Second}
}

// ── 5.1: the idempotency key ─────────────────────────────────────────────────────────────────────

// PRD OQ1 pins the unit at {run_id, node_id, attempt_group}. The key must be DERIVED: a random key
// regenerated on retry is not an idempotency key, it is a second request wearing a hat.
func TestIdempotencyKey_IsDerivedFromRunNodeAndAttemptGroup(t *testing.T) {
	a := IdempotencyKey("run1", "n_a", 0)

	if got := IdempotencyKey("run1", "n_a", 0); got != a {
		t.Errorf("the key is not deterministic: %q vs %q", got, a)
	}
	// Two executors racing the same node — an at-least-once queue delivering twice — independently
	// compute the same key, so the provider collapses them even though neither knew about the other.
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"a different run is a different charge", IdempotencyKey("run2", "n_a", 0)},
		{"a different node is a different charge", IdempotencyKey("run1", "n_b", 0)},
		{"a new attempt group is a new invocation, so a new charge", IdempotencyKey("run1", "n_a", 1)},
	} {
		if tc.key == a {
			t.Errorf("%s: got the same key %q", tc.name, a)
		}
	}
}

// ── 3.7: running the built copy ──────────────────────────────────────────────────────────────────

func TestRun_ExecutesTheCopyAndRecordsEachNodeInvocation(t *testing.T) {
	dir := t.TempDir()
	cmd := script(t, dir, emit(`{"invocation_id":"i1","node_id":"n_a","run_id":"run1","invocation_index":0,"input":{"q":"hi"},"output":{"answer":"hello"}}`))
	e := New(newMemBlobs())

	run, err := e.Run(context.Background(), baseOpts(t, dir, cmd))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != StatusSucceeded {
		t.Fatalf("Status = %q, want succeeded. stderr: %s", run.Status, run.Stderr)
	}
	if len(run.Nodes) != 1 {
		t.Fatalf("recorded %d node executions, want 1: %+v", len(run.Nodes), run.Nodes)
	}
	n := run.Nodes[0]
	if n.NodeID != "n_a" || n.Status != StatusSucceeded {
		t.Errorf("node = %+v", n)
	}
	if n.IdempotencyKey != IdempotencyKey("run1", "n_a", 0) {
		t.Errorf("IdempotencyKey = %q", n.IdempotencyKey)
	}
	// PRD §7: node I/O is content-hashed and referenced, never inline — it is prompts and completions.
	if len(n.InputBlobHash) != 64 || len(n.OutputBlobHash) != 64 {
		t.Errorf("node I/O should be blob references, got in=%q out=%q", n.InputBlobHash, n.OutputBlobHash)
	}
}

// A loop fires one NODE many times. Each invocation is a new attempt_group — a new charge, because it
// is one — and therefore a distinct idempotency key and a distinct node_execution row.
func TestRun_LoopInvocationsGetDistinctAttemptGroups(t *testing.T) {
	dir := t.TempDir()
	body := emit(`{"invocation_id":"i1","node_id":"n_a","run_id":"run1","invocation_index":0,"output":{"answer":"one"}}`) +
		emit(`{"invocation_id":"i2","node_id":"n_a","run_id":"run1","invocation_index":1,"output":{"answer":"two"}}`)
	e := New(newMemBlobs())

	run, err := e.Run(context.Background(), baseOpts(t, dir, script(t, dir, body)))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(run.Nodes) != 2 {
		t.Fatalf("recorded %d executions, want 2", len(run.Nodes))
	}
	if run.Nodes[0].AttemptGroup != 0 || run.Nodes[1].AttemptGroup != 1 {
		t.Errorf("attempt groups = %d, %d; want 0, 1", run.Nodes[0].AttemptGroup, run.Nodes[1].AttemptGroup)
	}
	if run.Nodes[0].IdempotencyKey == run.Nodes[1].IdempotencyKey {
		t.Error("two loop iterations share an idempotency key; the provider would collapse two real calls into one")
	}
}

func TestRun_NonZeroExitIsFailedNotSucceeded(t *testing.T) {
	dir := t.TempDir()
	e := New(newMemBlobs())
	run, err := e.Run(context.Background(), baseOpts(t, dir, script(t, dir, "exit 3\n")))
	if !errors.Is(err, ErrRunFailed) {
		t.Fatalf("want ErrRunFailed, got %v", err)
	}
	if run.Status != StatusFailed {
		t.Errorf("Status = %q, want failed", run.Status)
	}
}

// PRD §7: a workflow that hangs must not exhaust the executor.
func TestRun_HangingWorkflowTimesOut(t *testing.T) {
	dir := t.TempDir()
	e := New(newMemBlobs())
	opts := baseOpts(t, dir, script(t, dir, "sleep 60\n"))
	opts.Timeout = 500 * time.Millisecond

	start := time.Now()
	run, err := e.Run(context.Background(), opts)
	if err == nil {
		t.Fatal("a hanging workflow ran to completion")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("want a timeout error, got %v", err)
	}
	if run.Status != StatusFailed {
		t.Errorf("Status = %q, want failed", run.Status)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("the timeout took %v to fire", elapsed)
	}
}

// ── 3.8: the contract halt ───────────────────────────────────────────────────────────────────────

// FR15: "halt the run with a typed error when a node's I/O violates the typed contract, rather than
// passing malformed data downstream."
func TestRun_ContractViolationHaltsTheRunNamingTheNode(t *testing.T) {
	dir := t.TempDir()
	// The contract requires {"answer": string}; this emits a number.
	cmd := script(t, dir, emit(`{"invocation_id":"i1","node_id":"n_a","run_id":"run1","invocation_index":0,"output":{"answer":42}}`)+"sleep 5\n")
	e := New(newMemBlobs())

	run, err := e.Run(context.Background(), baseOpts(t, dir, cmd))
	if !errors.Is(err, ErrContractViolation) {
		t.Fatalf("want ErrContractViolation, got %v", err)
	}
	var h *HaltError
	if !errors.As(err, &h) {
		t.Fatalf("want a *HaltError, got %T", err)
	}
	if h.NodeID != "n_a" {
		t.Errorf("the halt must name the node, got %q", h.NodeID)
	}
	if h.Reason == "" {
		t.Error("a halt with no reason leaves the user nothing to act on")
	}
	// `halted` is NOT `failed`: the workflow did not break, we stopped it. Reporting the killed
	// process's non-zero exit as `failed` would send the user to debug working code.
	if run.Status != StatusHalted {
		t.Errorf("Status = %q, want halted", run.Status)
	}
	if run.HaltedNodeID != "n_a" || run.HaltedReason == "" {
		t.Errorf("the run record must carry the halt: node=%q reason=%q", run.HaltedNodeID, run.HaltedReason)
	}
}

// "Do not pass malformed data downstream", once we are not the data path, means the process that
// would have made the next call no longer exists. This is that, observed: the workflow would have
// written `downstream-ran` a second later, and must not get the chance.
func TestRun_HaltActuallyStopsTheWorkflowBeforeItContinues(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "downstream-ran")
	body := emit(`{"invocation_id":"i1","node_id":"n_a","run_id":"run1","invocation_index":0,"output":{"answer":42}}`) +
		"sleep 3\n" + fmt.Sprintf("touch %q\n", marker)
	e := New(newMemBlobs())

	if _, err := e.Run(context.Background(), baseOpts(t, dir, script(t, dir, body))); !errors.Is(err, ErrContractViolation) {
		t.Fatalf("want ErrContractViolation, got %v", err)
	}
	// Give a surviving process time to reach the marker, so this fails loudly if the kill missed.
	time.Sleep(1 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the workflow kept running after the halt; malformed data reached the next step")
	}
}

// A conforming output must not halt — otherwise the gate is just "halt always" and proves nothing.
func TestRun_ConformingOutputDoesNotHalt(t *testing.T) {
	dir := t.TempDir()
	cmd := script(t, dir, emit(`{"invocation_id":"i1","node_id":"n_a","run_id":"run1","invocation_index":0,"output":{"answer":"fine"}}`))
	e := New(newMemBlobs())

	run, err := e.Run(context.Background(), baseOpts(t, dir, cmd))
	if err != nil {
		t.Fatalf("a conforming output halted the run: %v", err)
	}
	if run.Status != StatusSucceeded {
		t.Errorf("Status = %q, want succeeded", run.Status)
	}
}

// An invalid io_contract fails the run UP FRONT. Discovering at node 4 that node 5's contract does
// not compile would mean four nodes of provider spend before learning the run could never be checked.
func TestRun_InvalidIOContractFailsBeforeAnythingRuns(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	cmd := script(t, dir, fmt.Sprintf("touch %q\n", marker))
	opts := baseOpts(t, dir, cmd)
	opts.IR = &discovery.IR{IRVersion: "1.0.0", Nodes: []discovery.IRNode{{
		NodeID: "n_a", IOContract: discovery.IRIOContract{OutputSchema: map[string]any{"type": "nonsense"}},
	}}}

	if _, err := New(newMemBlobs()).Run(context.Background(), opts); err == nil {
		t.Fatal("an invalid io_contract was accepted")
	} else if !strings.Contains(err.Error(), "n_a") {
		t.Errorf("the error should name the node, got %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the workflow ran despite an uncheckable contract")
	}
}

// A remote $ref would make the contract check depend on a third party's uptime and on content nobody
// hashed — same stance as the Skill Registry.
func TestRun_IOContractWithARemoteRefIsRejected(t *testing.T) {
	dir := t.TempDir()
	opts := baseOpts(t, dir, script(t, dir, "true\n"))
	opts.IR = &discovery.IR{IRVersion: "1.0.0", Nodes: []discovery.IRNode{{
		NodeID: "n_a", IOContract: discovery.IRIOContract{
			OutputSchema: map[string]any{"$ref": "https://example.com/s.json"}},
	}}}
	if _, err := New(newMemBlobs()).Run(context.Background(), opts); err == nil {
		t.Fatal("a contract with a remote $ref was accepted")
	}
}

// ── 5.3: seed threading, and the sandbox ─────────────────────────────────────────────────────────

// Task 5.3: the seed is threaded from the Variant Spec through to the stochastic steps. This is the
// propagation half — PRD OQ2 asserts reproducibility on seed PROPAGATION, not on provider output.
func TestRun_SeedAndRunIDReachTheTransformedCopy(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "env.txt")
	cmd := script(t, dir, fmt.Sprintf("printf '%%s %%s' \"$HEROS_SEED\" \"$HEROS_RUN_ID\" > %q\n", out))
	e := New(newMemBlobs())

	opts := baseOpts(t, dir, cmd)
	opts.Seed = 12345
	if _, err := e.Run(context.Background(), opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != "12345 run1" {
		t.Errorf("the copy saw %q, want the run's seed and run_id", b)
	}
}

// The same seed propagates identically across runs — the assertion FR16 actually makes, since
// providers do not guarantee identical output even at a fixed seed (PRD OQ2).
func TestRun_SameSeedPropagatesIdenticallyAcrossRuns(t *testing.T) {
	e := New(newMemBlobs())
	var seen []string
	for i := 0; i < 3; i++ {
		dir := t.TempDir()
		out := filepath.Join(dir, "seed.txt")
		cmd := script(t, dir, fmt.Sprintf("printf '%%s' \"$HEROS_SEED\" > %q\n", out))
		opts := baseOpts(t, dir, cmd)
		opts.Seed = 777
		if _, err := e.Run(context.Background(), opts); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		seen = append(seen, string(b))
	}
	for i, s := range seen {
		if s != "777" {
			t.Errorf("run %d saw seed %q, want 777 — seed propagation is not reproducible", i, s)
		}
	}
}

// The executor's own process holds provider credentials. Handing os.Environ() to the target would
// export every one into a process whose code we rewrote but did not write — and PRD §7 says secrets
// never reach run records, of which the target's stdout is one.
func TestRun_TargetDoesNotInheritTheExecutorsEnvironment(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-should-never-be-inherited")
	t.Setenv("SOME_OPERATOR_SETTING", "leaky")

	dir := t.TempDir()
	cmd := script(t, dir, "env\n")
	e := New(newMemBlobs())

	run, err := e.Run(context.Background(), baseOpts(t, dir, cmd))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(run.Stdout, "sk-ant-should-never-be-inherited") {
		t.Error("the executor's provider credential was exported into the transformed copy")
	}
	if strings.Contains(run.Stdout, "leaky") {
		t.Error("the operator's environment leaked into the run; two runs of one config_hash could differ")
	}
	// HOME must not be the operator's: it holds credentials and git config.
	if strings.Contains(run.Stdout, "HOME="+os.Getenv("HOME")+"\n") {
		t.Error("the target inherited the operator's HOME")
	}
}

// A previous run's records must not be read as this one's — that would attribute another run's node
// I/O to this config_hash.
func TestRun_StaleInvocationLogFromAPriorRunIsNotRead(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, ".heros-invocations.jsonl")
	if err := os.WriteFile(stale,
		[]byte(`{"invocation_id":"old","node_id":"n_a","run_id":"OLD","invocation_index":0,"output":{"answer":"stale"}}`+"\n"),
		0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	cmd := script(t, dir, emit(`{"invocation_id":"i1","node_id":"n_a","run_id":"run1","invocation_index":0,"output":{"answer":"fresh"}}`))
	e := New(newMemBlobs())

	run, err := e.Run(context.Background(), baseOpts(t, dir, cmd))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(run.Nodes) != 1 {
		t.Errorf("recorded %d executions, want 1 — a prior run's records were read as this run's", len(run.Nodes))
	}
}

// Noise on our channel is not a contract violation. A workflow that writes a stray line must not be
// reported as having violated its contract — that would send the user to fix a schema that is fine.
func TestRun_MalformedRecordIsIgnoredNotTreatedAsAViolation(t *testing.T) {
	dir := t.TempDir()
	body := "printf 'not json\\n' >> \"$HEROS_INVOCATION_LOG\"\n" +
		emit(`{"invocation_id":"i1","node_id":"n_a","run_id":"run1","invocation_index":0,"output":{"answer":"ok"}}`)
	e := New(newMemBlobs())

	run, err := e.Run(context.Background(), baseOpts(t, dir, script(t, dir, body)))
	if err != nil {
		t.Fatalf("a malformed line failed the run: %v", err)
	}
	if run.Status != StatusSucceeded || len(run.Nodes) != 1 {
		t.Errorf("Status=%q nodes=%d, want succeeded/1", run.Status, len(run.Nodes))
	}
}

func TestRun_RequiresTheIRSoThereIsAlwaysAContractToCheck(t *testing.T) {
	dir := t.TempDir()
	opts := baseOpts(t, dir, script(t, dir, "true\n"))
	opts.IR = nil
	if _, err := New(newMemBlobs()).Run(context.Background(), opts); err == nil {
		t.Fatal("a run with no IR was accepted; there would be no typed contract to check")
	}
}
