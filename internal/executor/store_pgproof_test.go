//go:build pgproof

// Live-Postgres proof for run/node_execution (tasks 3.9 run half, 5.2).
package executor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/lib/pq"
)

var testDB *sql.DB

const cfgHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestMain(m *testing.M) {
	db, err := pgtest.Open("proof_executor")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testDB = db
	for _, f := range []string{"0001_p0_lineage.up.sql", "0002_p2_registries.up.sql",
		"0003_p2_variant_spec.up.sql", "0004_p2_transform.up.sql",
		"0007_p2_verification_strength.up.sql", "0005_p2_run.up.sql"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", f))
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

// seedLineage creates the chain 0005's FKs require: workflow -> variant -> config -> variant_spec ->
// transform.
func seedLineage(t *testing.T) {
	t.Helper()
	stmts := []string{
		`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		 VALUES ('wf','local://t','abc','go','1.0.0') ON CONFLICT DO NOTHING`,
		`INSERT INTO variant (variant_id, workflow_id, label) VALUES ('v','wf','base') ON CONFLICT DO NOTHING`,
		`INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
		 VALUES ('` + cfgHash + `','v','wf','1.0.0','{}') ON CONFLICT DO NOTHING`,
		`INSERT INTO variant_spec (config_hash, source_revision, spec_json)
		 VALUES ('` + cfgHash + `','rev1','{}') ON CONFLICT DO NOTHING`,
		`INSERT INTO transform (config_hash, source_revision, build_status, verification_strength)
		 VALUES ('` + cfgHash + `','rev1','built','type-checked') ON CONFLICT DO NOTHING`,
	}
	for _, q := range stmts {
		if _, err := testDB.Exec(q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func sqlState(err error) string {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code)
	}
	return ""
}

func TestPG_StartFinishAndGetRoundTripARun(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	s := NewStore(testDB)

	if err := s.Start(ctx, "run_ok", cfgHash, "rev1", 42); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.RecordNode(ctx, "run_ok", NodeResult{
		NodeID: "n_a", AttemptGroup: 0, Status: StatusSucceeded,
		IdempotencyKey: IdempotencyKey("run_ok", "n_a", 0),
	}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	if err := s.Finish(ctx, &Run{RunID: "run_ok", Status: StatusSucceeded}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	got, err := s.Get(ctx, "run_ok")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusSucceeded || got.Seed != 42 || got.ConfigHash != cfgHash {
		t.Errorf("run did not round-trip: %+v", got)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].IdempotencyKey != "run_ok:n_a:0" {
		t.Errorf("node executions did not round-trip: %+v", got.Nodes)
	}
}

// Task 5.2, the headline: "a double-write race is a caught conflict, not a duplicate row."
//
// Two executors racing the same node — an at-least-once queue delivering twice — must not both write
// a result. Without the primary key, P4 scores the same invocation twice and the duplicate is
// invisible, because both rows look correct.
func TestPG_DoubleWriteOfOneNodeExecutionIsCaught(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	s := NewStore(testDB)
	if err := s.Start(ctx, "run_dup", cfgHash, "rev1", 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	n := NodeResult{NodeID: "n_a", AttemptGroup: 0, Status: StatusSucceeded,
		IdempotencyKey: IdempotencyKey("run_dup", "n_a", 0)}

	if err := s.RecordNode(ctx, "run_dup", n); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	// The identical redelivery is idempotent — the same work, reported twice.
	if err := s.RecordNode(ctx, "run_dup", n); err != nil {
		t.Fatalf("an identical redelivery must be a no-op, got: %v", err)
	}
	var count int
	if err := testDB.QueryRowContext(ctx,
		`SELECT count(*) FROM node_execution WHERE run_id='run_dup'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("a retried node wrote %d rows, want 1 — FR17 says a retry must not double-write", count)
	}

	// A CONFLICTING write under the same coordinates is loud: it means attempt_group was reused for a
	// genuinely different invocation, which the provider would bill twice while we recorded one.
	n2 := n
	n2.IdempotencyKey = "run_dup:n_a:999"
	if err := s.RecordNode(ctx, "run_dup", n2); !errors.Is(err, ErrDuplicateNodeExecution) {
		t.Errorf("want ErrDuplicateNodeExecution for a conflicting key, got %v", err)
	}

	// The raw INSERT the DB itself must refuse — the last line, when application code forgets.
	_, err := testDB.ExecContext(ctx,
		`INSERT INTO node_execution (run_id, node_id, attempt_group, status, idempotency_key)
		 VALUES ('run_dup','n_a',0,'succeeded','k')`)
	if err == nil {
		t.Fatal("a duplicate node_execution was accepted by the database")
	}
	if got := sqlState(err); got != "23505" {
		t.Errorf("rejected by %s, want the primary key (23505): %v", got, err)
	}
}

// A retry keeps its attempt_group and therefore its key: one logical call, one charge. A new
// invocation gets a new group: a different call, a different charge. Both must be storable.
func TestPG_RetryAndNewInvocationAreDistinguishable(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	s := NewStore(testDB)
	if err := s.Start(ctx, "run_groups", cfgHash, "rev1", 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, g := range []int{0, 1} {
		if err := s.RecordNode(ctx, "run_groups", NodeResult{
			NodeID: "n_a", AttemptGroup: g, Status: StatusSucceeded,
			IdempotencyKey: IdempotencyKey("run_groups", "n_a", g),
		}); err != nil {
			t.Fatalf("RecordNode group %d: %v", g, err)
		}
	}
	got, err := s.Get(ctx, "run_groups")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("recorded %d executions, want 2 (two invocations of one node)", len(got.Nodes))
	}
	if got.Nodes[0].IdempotencyKey == got.Nodes[1].IdempotencyKey {
		t.Error("two invocations share a key; the provider would collapse two real calls into one charge")
	}
}

// A halt is recorded with the node that caused it — PRD §9's unhappy path.
func TestPG_HaltIsRecordedWithTheNodeThatCausedIt(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	s := NewStore(testDB)
	if err := s.Start(ctx, "run_halt", cfgHash, "rev1", 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Finish(ctx, &Run{RunID: "run_halt", Status: StatusHalted,
		HaltedNodeID: "n_a", HaltedReason: "output violates io_contract"}); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	got, err := s.Get(ctx, "run_halt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusHalted || got.HaltedNodeID != "n_a" || got.HaltedReason == "" {
		t.Errorf("the halt did not round-trip: %+v", got)
	}
}

// A halt with no node names nothing, and a non-halted run with a halt reason is a lie. The DB refuses
// both.
func TestPG_HaltStatusAndHaltedNodeMustAgree(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	for _, tc := range []struct{ name, q string }{
		{"halted with no node", `INSERT INTO run (run_id, config_hash, source_revision, seed, status, finished_at)
		  VALUES ('r_bad1','` + cfgHash + `','rev1',1,'halted', now())`},
		{"succeeded but names a halted node", `INSERT INTO run (run_id, config_hash, source_revision, seed, status, halted_node_id, finished_at)
		  VALUES ('r_bad2','` + cfgHash + `','rev1',1,'succeeded','n_a', now())`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := testDB.ExecContext(ctx, tc.q); err == nil {
				t.Fatalf("accepted: %s", tc.name)
			} else if got := sqlState(err); got != "23514" {
				t.Errorf("rejected by %s, want the CHECK (23514): %v", got, err)
			}
		})
	}
}

// "Finished" and "still going" must not become indistinguishable after a crash, or the UI's loading
// state never resolves.
func TestPG_TerminalRunMustHaveAFinishedAt(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	_, err := testDB.ExecContext(ctx,
		`INSERT INTO run (run_id, config_hash, source_revision, seed, status)
		 VALUES ('r_nofin','`+cfgHash+`','rev1',1,'succeeded')`)
	if err == nil {
		t.Fatal("a terminal run with no finished_at was accepted")
	}
	if got := sqlState(err); got != "23514" {
		t.Errorf("rejected by %s, want the CHECK (23514): %v", got, err)
	}
}

// An at-least-once queue can deliver one run twice. The second must not rewrite the first's verdict.
func TestPG_Finish_CannotOverwriteATerminalVerdict(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	s := NewStore(testDB)
	if err := s.Start(ctx, "run_twice", cfgHash, "rev1", 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.Finish(ctx, &Run{RunID: "run_twice", Status: StatusHalted, HaltedNodeID: "n_a", HaltedReason: "boom"}); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	// The identical redelivery is a no-op.
	if err := s.Finish(ctx, &Run{RunID: "run_twice", Status: StatusHalted, HaltedNodeID: "n_a", HaltedReason: "boom"}); err != nil {
		t.Errorf("an identical re-finish must be a no-op, got %v", err)
	}
	// A DIFFERENT verdict is refused: a halted run must not become succeeded.
	err := s.Finish(ctx, &Run{RunID: "run_twice", Status: StatusSucceeded})
	if err == nil {
		t.Fatal("a terminal run's verdict was overwritten")
	}
	if !strings.Contains(err.Error(), "already terminal") {
		t.Errorf("want an already-terminal error, got %v", err)
	}
	got, err := s.Get(ctx, "run_twice")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusHalted {
		t.Errorf("the first verdict was lost: %q", got.Status)
	}
}

// A run of a configuration that was never transformed is unrepresentable.
func TestPG_RunCannotReferenceAnAbsentTransform(t *testing.T) {
	seedLineage(t)
	_, err := testDB.Exec(
		`INSERT INTO run (run_id, config_hash, source_revision, seed, status)
		 VALUES ('r_ghost','` + cfgHash + `','no-such-rev',1,'running')`)
	if err == nil {
		t.Fatal("a run citing a non-existent transform was accepted")
	}
	if got := sqlState(err); got != "23503" {
		t.Errorf("rejected by %s, want the FK (23503): %v", got, err)
	}
}

func TestPG_Finish_WithoutStartIsErrRunNotFound(t *testing.T) {
	err := NewStore(testDB).Finish(context.Background(), &Run{RunID: "never_started", Status: StatusSucceeded})
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("want ErrRunNotFound, got %v", err)
	}
}

// A node execution is a measurement; an edited one is a falsified measurement.
func TestPG_NodeExecution_IsImmutable(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	s := NewStore(testDB)
	if err := s.Start(ctx, "run_immut", cfgHash, "rev1", 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.RecordNode(ctx, "run_immut", NodeResult{NodeID: "n_a", Status: StatusSucceeded,
		IdempotencyKey: "run_immut:n_a:0"}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	for _, tc := range []struct{ name, q string }{
		{"update", `UPDATE node_execution SET status='failed' WHERE run_id='run_immut'`},
		{"delete", `DELETE FROM node_execution WHERE run_id='run_immut'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := testDB.ExecContext(ctx, tc.q); err == nil {
				t.Fatalf("a recorded node execution was mutated by %s", tc.name)
			} else if got := sqlState(err); got != "HR001" {
				t.Errorf("rejected by %s, want the immutability guard (HR001): %v", got, err)
			}
		})
	}
}

// The regression for the bug the live UI harness found: a bare FSBlobStore writes bytes but does not
// CATALOG them, so node_execution's FK to blob(content_hash) rejects the write.
//
// Every other test in this file passed while that was broken, because they all record nodes with NO
// I/O — the FK never fired. The first run with real per-node I/O failed instantly. This records a
// node the way the executor actually does, with blob references, so the gap cannot reopen.
func TestPG_RecordNodeWithRealBlobReferencesSatisfiesTheFK(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	s := NewStore(testDB)

	fs, err := registry.NewFSBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	// A CATALOGING store, as production must use: storing the bytes and making them referenceable are
	// one operation, not two.
	blobs := registry.NewCatalogingBlobStore(testDB, fs, "application/json")

	in, err := blobs.Put(ctx, []byte(`{"query":"hi"}`))
	if err != nil {
		t.Fatalf("put input: %v", err)
	}
	out, err := blobs.Put(ctx, []byte(`{"answer":"hello"}`))
	if err != nil {
		t.Fatalf("put output: %v", err)
	}

	if err := s.Start(ctx, "run_blobs", cfgHash, "rev1", 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := s.RecordNode(ctx, "run_blobs", NodeResult{
		NodeID: "n_a", AttemptGroup: 0, Status: StatusSucceeded,
		InputBlobHash: in, OutputBlobHash: out,
		IdempotencyKey: IdempotencyKey("run_blobs", "n_a", 0),
	}); err != nil {
		t.Fatalf("recording a node with real blob references failed — the blobs were not catalogued: %v", err)
	}

	got, err := s.Get(ctx, "run_blobs")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].InputBlobHash != in || got.Nodes[0].OutputBlobHash != out {
		t.Errorf("the blob references did not round-trip: %+v", got.Nodes)
	}
}

// An UNCATALOGUED blob reference must be rejected: the FK is what stops a run record from citing I/O
// nobody can read.
func TestPG_RecordNodeWithAnUncataloguedBlobIsRejected(t *testing.T) {
	ctx := context.Background()
	seedLineage(t)
	s := NewStore(testDB)

	fs, err := registry.NewFSBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	// The BARE store: bytes written, nothing catalogued — exactly the bug.
	orphan, err := fs.Put(ctx, []byte(`{"answer":"orphaned"}`))
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	if err := s.Start(ctx, "run_orphan", cfgHash, "rev1", 1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	err = s.RecordNode(ctx, "run_orphan", NodeResult{
		NodeID: "n_a", AttemptGroup: 0, Status: StatusSucceeded,
		OutputBlobHash: orphan, IdempotencyKey: IdempotencyKey("run_orphan", "n_a", 0),
	})
	if err == nil {
		t.Fatal("a node citing an uncatalogued blob was recorded; the reference would resolve to nothing")
	}
	if got := sqlState(err); got != "23503" {
		t.Errorf("rejected by %s, want the FK (23503): %v", got, err)
	}
}

func TestPG_ZZ_DownMigrationRemovesRunTablesOnly(t *testing.T) {
	ctx := context.Background()
	down, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", "0005_p2_run.down.sql"))
	if err != nil {
		t.Fatalf("read down: %v", err)
	}
	if _, err := testDB.ExecContext(ctx, string(down)); err != nil {
		t.Fatalf("apply down: %v", err)
	}
	for _, gone := range []string{"run", "node_execution"} {
		var exists bool
		if err := testDB.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, gone).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", gone, err)
		}
		if exists {
			t.Errorf("%s survived the down migration", gone)
		}
	}
	// Expand-only: everything below 0005 survives, including 0002's guard function.
	for _, obj := range []string{"transform", "variant_spec", "config"} {
		var ok bool
		if err := testDB.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, obj).Scan(&ok); err != nil {
			t.Fatalf("check %s: %v", obj, err)
		}
		if !ok {
			t.Errorf("the 0005 rollback removed %s, which it does not own", obj)
		}
	}
}
