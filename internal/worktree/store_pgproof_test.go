//go:build pgproof

// Live-Postgres proof for the transform record (task 3.9, transform half).
//
// Behind the `pgproof` tag like the other proofs: `make go` does not compile it, `make pg-proof` runs
// it, and with no reachable database it FAILS rather than skips.
package worktree

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

func TestMain(m *testing.M) {
	db, err := pgtest.Open("proof_worktree")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testDB = db
	for _, f := range []string{"0001_p0_lineage.up.sql", "0002_p2_registries.up.sql",
		"0003_p2_variant_spec.up.sql", "0004_p2_transform.up.sql",
		"0007_p2_verification_strength.up.sql"} {
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

// seedSpec creates the lineage chain 0004's FK requires: workflow -> variant -> config -> variant_spec.
func seedSpec(t *testing.T, configHash, rev string) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		  VALUES ('wf','local://t','abc','go','1.0.0') ON CONFLICT DO NOTHING`, nil},
		{`INSERT INTO variant (variant_id, workflow_id, label) VALUES ('v','wf','base')
		  ON CONFLICT DO NOTHING`, nil},
		{`INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
		  VALUES ($1,'v','wf','1.0.0','{}') ON CONFLICT DO NOTHING`, []any{configHash}},
		{`INSERT INTO variant_spec (config_hash, source_revision, spec_json) VALUES ($1,$2,'{}')
		  ON CONFLICT DO NOTHING`, []any{configHash, rev}},
	} {
		if _, err := testDB.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func newStore(t *testing.T) *Store {
	t.Helper()
	blobs, err := registry.NewFSBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	return NewStore(testDB, blobs)
}

func TestPG_Put_RecordsABuiltTransformAndItsDiff(t *testing.T) {
	ctx := context.Background()
	seedSpec(t, hashA, "rev1")
	s := newStore(t)

	applied := &Applied{
		ConfigHash: hashA, SourceRevision: "rev1", Dir: "/wt/a", Branch: "variant/aaa",
		Commit: "deadbeef", Diff: []byte("--- a/x.go\n+++ b/x.go\n-a\n+b\n"), Status: StatusBuilt,
		Strength: StrengthTypeChecked, VerifierTool: "go build ./...",
	}
	if err := s.Put(ctx, applied, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rec, diff, err := s.Get(ctx, hashA, "rev1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Status != StatusBuilt || rec.Commit != "deadbeef" || rec.Branch != "variant/aaa" {
		t.Errorf("record did not round-trip: %+v", rec)
	}
	if string(diff) != string(applied.Diff) {
		t.Errorf("the diff did not round-trip:\n got %q\nwant %q", diff, applied.Diff)
	}

	// PRD §7: the diff is content-hashed in the object store and REFERENCED — the row must not carry
	// the user's source code inline.
	var stored sql.NullString
	if err := testDB.QueryRowContext(ctx,
		`SELECT diff_blob_hash FROM transform WHERE config_hash = $1`, hashA).Scan(&stored); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !stored.Valid || len(stored.String) != 64 {
		t.Errorf("diff_blob_hash = %v, want a content hash", stored)
	}
	var n int
	if err := testDB.QueryRowContext(ctx,
		`SELECT count(*) FROM blob WHERE content_hash = $1`, stored.String).Scan(&n); err != nil {
		t.Fatalf("count blob: %v", err)
	}
	if n != 1 {
		t.Error("the diff blob was not catalogued; the FK would have nothing to point at")
	}
}

// A build-rejected transform is a terminal state to record, not a failure to swallow: the UI renders
// it and P4 must not re-attempt it (FR18).
func TestPG_Put_RecordsARejectionWithItsReasonAndAttribution(t *testing.T) {
	ctx := context.Background()
	seedSpec(t, hashB, "rev1")
	s := newStore(t)

	applied := &Applied{
		ConfigHash: hashB, SourceRevision: "rev1", Dir: "/wt/b", Branch: "variant/bbb",
		Commit: "cafe", Status: StatusBuildRejected, BuildLog: "./pipeline.go:4:15: undefined: x",
		Strength: StrengthTypeChecked,
	}
	rej := &BuildRejection{ConfigHash: hashB, NodeID: "n_a", Dim: "model", Log: applied.BuildLog}
	if err := s.Put(ctx, applied, rej); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rec, _, err := s.Get(ctx, hashB, "rev1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Status != StatusBuildRejected {
		t.Errorf("Status = %q, want build-rejected", rec.Status)
	}
	if rec.RejectedNodeID != "n_a" || rec.RejectedDim != "model" {
		t.Errorf("attribution did not round-trip: node=%q dim=%q", rec.RejectedNodeID, rec.RejectedDim)
	}
	if !strings.Contains(rec.BuildLog, "undefined: x") {
		t.Errorf("the compiler's reason was lost: %q", rec.BuildLog)
	}
}

// A rejection with no reason is useless to the one person who has to fix the spec, so the DB refuses
// to store one.
func TestPG_RejectionWithoutAReasonIsRejectedByTheDatabase(t *testing.T) {
	ctx := context.Background()
	seedSpec(t, hashC, "rev1")
	_, err := testDB.ExecContext(ctx,
		`INSERT INTO transform (config_hash, source_revision, build_status, verification_strength, build_log)
		 VALUES ($1, 'rev1', 'build-rejected', 'type-checked', '')`, hashC)
	if err == nil {
		t.Fatal("a build-rejected transform with no log was accepted")
	}
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || pqErr.Constraint != "transform_rejection_has_a_reason" {
		t.Errorf("rejected, but not by the reason CHECK: %v", err)
	}
}

// The build_status vocabulary is closed: a typo must not invent a third state the executor's switch
// silently treats as runnable.
func TestPG_BuildStatusVocabularyIsClosed(t *testing.T) {
	ctx := context.Background()
	seedSpec(t, hashD, "rev1")
	_, err := testDB.ExecContext(ctx,
		`INSERT INTO transform (config_hash, source_revision, build_status, verification_strength)
		 VALUES ($1,'rev1','probably-fine','type-checked')`, hashD)
	if err == nil {
		t.Fatal("an invented build_status was accepted")
	}
	if got := sqlState(err); got != "23514" {
		t.Errorf("rejected by %s, want the CHECK (23514): %v", got, err)
	}
}

// A transform of a configuration nobody authored must be unrepresentable.
func TestPG_TransformCannotReferenceAnUnauthoredSpec(t *testing.T) {
	_, err := testDB.Exec(
		`INSERT INTO transform (config_hash, source_revision, build_status, verification_strength)
		 VALUES ($1,'nope','built','type-checked')`, strings.Repeat("e", 64))
	if err == nil {
		t.Fatal("a transform citing a non-existent variant_spec was accepted")
	}
	if got := sqlState(err); got != "23503" {
		t.Errorf("rejected by %s, want the FK (23503): %v", got, err)
	}
}

// The transform is a pure function of (config_hash, source_revision), so a re-record is a no-op.
func TestPG_Put_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	seedSpec(t, hashE, "rev1")
	s := newStore(t)
	applied := &Applied{ConfigHash: hashE, SourceRevision: "rev1", Dir: "/wt/e",
		Diff: []byte("d"), Status: StatusBuilt, Strength: StrengthTypeChecked}

	if err := s.Put(ctx, applied, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put(ctx, applied, nil); err != nil {
		t.Fatalf("re-Put must be a no-op: %v", err)
	}
	var n int
	if err := testDB.QueryRowContext(ctx,
		`SELECT count(*) FROM transform WHERE config_hash = $1`, hashE).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("re-recording stored %d rows, want 1", n)
	}
}

// The same pair reaching two verdicts means the transform was not a pure function of it — a drifting
// toolchain, or a worktree that was not at source_revision. Silently keeping the first would let P4
// score against a build nobody can reproduce.
func TestPG_Put_ConflictingVerdictForTheSamePairIsLoud(t *testing.T) {
	ctx := context.Background()
	seedSpec(t, hashF, "rev1")
	s := newStore(t)

	if err := s.Put(ctx, &Applied{ConfigHash: hashF, SourceRevision: "rev1", Status: StatusBuilt,
		Strength: StrengthTypeChecked}, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	err := s.Put(ctx, &Applied{ConfigHash: hashF, SourceRevision: "rev1",
		Status: StatusBuildRejected, BuildLog: "boom", Strength: StrengthTypeChecked}, nil)
	if err == nil {
		t.Fatal("the same pair recorded two different verdicts without complaint")
	}
	if !strings.Contains(err.Error(), "must build the same way") {
		t.Errorf("the error should explain why this cannot happen, got: %v", err)
	}
}

// A transform is as immutable as the pair identifying it.
func TestPG_Transform_IsImmutable(t *testing.T) {
	ctx := context.Background()
	seedSpec(t, hashG, "rev1")
	s := newStore(t)
	if err := s.Put(ctx, &Applied{ConfigHash: hashG, SourceRevision: "rev1", Status: StatusBuilt,
		Strength: StrengthTypeChecked}, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	for _, tc := range []struct{ name, q string }{
		{"update", `UPDATE transform SET build_status = 'build-rejected' WHERE config_hash = $1`},
		{"delete", `DELETE FROM transform WHERE config_hash = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := testDB.ExecContext(ctx, tc.q, hashG); err == nil {
				t.Fatalf("a recorded transform was mutated by %s", tc.name)
			} else if got := sqlState(err); got != "HR001" {
				t.Errorf("rejected by %s, want the immutability guard (HR001): %v", got, err)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────
// ADR-003 decision 3 — the strength is PERSISTED and read back through the path that consumes it
// ─────────────────────────────────────────────────────────────────────────────────────────────────

// The strength survives the round trip, for BOTH values, through the same Get the API serves from.
//
// "HTTP 200 != 入库证据": the assertion that matters is not that Put returned nil, it is that a
// separate read — the one the diff-review UI actually performs — comes back with the same claim. A
// strength that Put accepts and Get loses would leave every diff looking equally verified, which is
// the exact state ADR-003 exists to end.
func TestPG_Put_PersistsTheVerificationStrengthAndItReadsBack(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	for _, tc := range []struct {
		hash     string
		strength Strength
		// autonomous is what ADR-003 decision 5 says this strength earns, asserted on the value that
		// came BACK FROM POSTGRES — not on the one we sent. That is the whole point: the automation gate
		// reads the record, so the record is what has to be right.
		autonomous bool
	}{
		{hashH, StrengthTypeChecked, true},
		{hashI, StrengthSyntaxChecked, false},
	} {
		t.Run(string(tc.strength), func(t *testing.T) {
			seedSpec(t, tc.hash, "rev1")
			if err := s.Put(ctx, &Applied{
				ConfigHash: tc.hash, SourceRevision: "rev1", Dir: "/wt/x", Branch: "variant/x",
				Commit: "c0ffee", Diff: []byte("--- a/x\n+++ b/x\n"), Status: StatusBuilt,
				Strength: tc.strength, VerifierTool: "some tool",
			}, nil); err != nil {
				t.Fatalf("Put: %v", err)
			}

			rec, _, err := s.Get(ctx, tc.hash, "rev1")
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if rec.Strength != tc.strength {
				t.Fatalf("strength did not round-trip: got %q, want %q", rec.Strength, tc.strength)
			}
			if rec.Strength.AllowsAutonomousApply() != tc.autonomous {
				t.Errorf("the record read back from Postgres says autonomous=%v, want %v",
					rec.Strength.AllowsAutonomousApply(), tc.autonomous)
			}
			// And it is a real column, not something Get inferred from build_status — both rows here say
			// `built`, so an inference would have to give them the same answer, and they differ.
			var raw string
			if err := testDB.QueryRowContext(ctx,
				`SELECT verification_strength FROM transform WHERE config_hash = $1`, tc.hash).Scan(&raw); err != nil {
				t.Fatalf("read column: %v", err)
			}
			if raw != string(tc.strength) {
				t.Errorf("the column holds %q, want %q", raw, tc.strength)
			}
		})
	}
}

// The vocabulary is closed at the DB, like build_status. A typo must not invent a third claim that
// Strength.AllowsAutonomousApply has never heard of.
func TestPG_VerificationStrengthVocabularyIsClosed(t *testing.T) {
	ctx := context.Background()
	seedSpec(t, hashJ, "rev1")
	_, err := testDB.ExecContext(ctx,
		`INSERT INTO transform (config_hash, source_revision, build_status, verification_strength)
		 VALUES ($1,'rev1','built','probably-checked')`, hashJ)
	if err == nil {
		t.Fatal("an invented verification_strength was accepted")
	}
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || pqErr.Constraint != "transform_verification_strength_known" {
		t.Errorf("rejected, but not by the strength CHECK: %v", err)
	}
}

// 🔴 The most important schema assertion in this migration.
//
// A row that does not say what its gate proved must be UNWRITABLE. If the column had a default, this
// INSERT would succeed and quietly claim whatever that default was — and if the default were
// 'type-checked' (the obvious, convenient choice) every code path that forgot to set a strength would
// present its diff as though a compiler had stood behind it. That is ADR-003's rejected option B
// reintroduced through a DDL convenience.
//
// So: NOT NULL, and no default. Stating what you proved is mandatory, and forgetting is loud.
func TestPG_ATransformThatDoesNotSayWhatItProvedIsUnwritable(t *testing.T) {
	ctx := context.Background()
	seedSpec(t, hashK, "rev1")
	_, err := testDB.ExecContext(ctx,
		`INSERT INTO transform (config_hash, source_revision, build_status) VALUES ($1,'rev1','built')`,
		hashK)
	if err == nil {
		t.Fatal("a transform with no verification strength was accepted; the column has a default, and " +
			"any code path that forgets to state what it proved now silently claims that default")
	}
	if got := sqlState(err); got != "23502" {
		t.Errorf("rejected by %s, want NOT NULL (23502): %v", got, err)
	}
	// Belt and braces: prove the absence of the default directly, because THAT is the property. A
	// future ALTER adding one back would keep the test above green only if it were nullable — this
	// one goes red either way.
	var def sql.NullString
	if err := testDB.QueryRowContext(ctx,
		`SELECT column_default FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = 'transform'
		   AND column_name = 'verification_strength'`).Scan(&def); err != nil {
		t.Fatalf("read column_default: %v", err)
	}
	if def.Valid {
		t.Errorf("verification_strength has DEFAULT %q. 0007 adds a default to backfill pre-ADR-003 "+
			"rows and DROPs it in the next statement, precisely so that a new row cannot inherit a "+
			"claim nobody made.", def.String)
	}
}

// The store refuses before Postgres does, so the caller gets a sentence instead of a constraint name.
func TestPG_Put_RefusesATransformWithNoStrength(t *testing.T) {
	ctx := context.Background()
	seedSpec(t, hashL, "rev1")
	err := newStore(t).Put(ctx, &Applied{
		ConfigHash: hashL, SourceRevision: "rev1", Status: StatusBuilt,
	}, nil)
	if err == nil {
		t.Fatal("a transform with no strength was recorded")
	}
	if !strings.Contains(err.Error(), "state what its gate PROVED") {
		t.Errorf("the error should explain what is missing and why, got: %v", err)
	}
}

// 0007 is expand-only: it must not have relaxed 0004's immutability trigger to backfill.
//
// This is the trap the migration was written around. The natural way to fill a new NOT NULL column is
// an UPDATE — and `transform` rejects every UPDATE by trigger, so a migration written that way would
// either fail outright or be "fixed" by disabling the trigger, which is a one-way door on the table's
// core guarantee. 0007 uses PostgreSQL 11+'s ADD COLUMN ... DEFAULT fast-path instead, which fills
// existing rows without firing row triggers. This asserts the guarantee it had to work around is
// still standing afterwards.
func TestPG_Transform_IsStillImmutableAfter0007(t *testing.T) {
	ctx := context.Background()
	seedSpec(t, hashM, "rev1")
	s := newStore(t)
	if err := s.Put(ctx, &Applied{ConfigHash: hashM, SourceRevision: "rev1", Status: StatusBuilt,
		Strength: StrengthSyntaxChecked}, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Upgrading a recorded claim after the fact is exactly what must be impossible: it would let a
	// syntax-checked diff become auto-appliable without anything re-verifying it.
	_, err := testDB.ExecContext(ctx,
		`UPDATE transform SET verification_strength = 'type-checked' WHERE config_hash = $1`, hashM)
	if err == nil {
		t.Fatal("a recorded verification strength was UPGRADED in place; nothing re-verified anything")
	}
	if got := sqlState(err); got != "HR001" {
		t.Errorf("rejected by %s, want the immutability guard (HR001): %v", got, err)
	}
}

func TestPG_Get_UnknownTransformIsErrTransformNotFound(t *testing.T) {
	_, _, err := newStore(t).Get(context.Background(), strings.Repeat("9", 64), "nope")
	if !errors.Is(err, ErrTransformNotFound) {
		t.Fatalf("want ErrTransformNotFound, got %v", err)
	}
}

// 0007 rolls back to exactly 0004's world: the column and its CHECK go, `transform` stays.
func TestPG_ZY_Down0007RemovesTheColumnOnly(t *testing.T) {
	ctx := context.Background()
	down, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres",
		"0007_p2_verification_strength.down.sql"))
	if err != nil {
		t.Fatalf("read down: %v", err)
	}
	if _, err := testDB.ExecContext(ctx, string(down)); err != nil {
		t.Fatalf("apply 0007 down: %v", err)
	}
	var col bool
	if err := testDB.QueryRowContext(ctx,
		// current_schema(): pgtest gives each proof its own schema, and every one of them has a
		// `transform` — an unfiltered information_schema query answers about someone else's table.
		`SELECT count(*) > 0 FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = 'transform'
		   AND column_name = 'verification_strength'`).Scan(&col); err != nil {
		t.Fatalf("check column: %v", err)
	}
	if col {
		t.Error("verification_strength survived the 0007 rollback")
	}
	// Expand-only: 0007 owns the column, NOT the table it sits on.
	var tbl bool
	if err := testDB.QueryRowContext(ctx, `SELECT to_regclass('transform') IS NOT NULL`).Scan(&tbl); err != nil {
		t.Fatalf("check table: %v", err)
	}
	if !tbl {
		t.Error("the 0007 rollback dropped `transform`, which 0004 owns")
	}
}

func TestPG_ZZ_DownMigrationRemovesTransformOnly(t *testing.T) {
	ctx := context.Background()
	down, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", "0004_p2_transform.down.sql"))
	if err != nil {
		t.Fatalf("read down: %v", err)
	}
	if _, err := testDB.ExecContext(ctx, string(down)); err != nil {
		t.Fatalf("apply down: %v", err)
	}
	var exists bool
	if err := testDB.QueryRowContext(ctx, `SELECT to_regclass('transform') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("check: %v", err)
	}
	if exists {
		t.Error("transform survived the down migration")
	}
	// Expand-only: 0001–0003's objects, and 0002's guard function, must survive.
	for _, obj := range []string{"variant_spec", "config", "model_entry"} {
		var ok bool
		if err := testDB.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, obj).Scan(&ok); err != nil {
			t.Fatalf("check %s: %v", obj, err)
		}
		if !ok {
			t.Errorf("the 0004 rollback removed %s, which it does not own", obj)
		}
	}
	var fn bool
	if err := testDB.QueryRowContext(ctx,
		// 🔴 Scoped to THIS schema. `pg_proc` is database-wide and `internal/pgtest` gives every test
		// package its own schema, so an unscoped `count(*) > 0` is satisfied by ANOTHER package's copy
		// of the function — and this assertion is that the rollback did NOT drop ours. Unscoped it is a
		// FALSE GREEN: 0004 could drop the function in this schema and the test would still pass,
		// because somebody else's exists. (The `to_regclass` check above is already search_path-scoped,
		// which is why it never had this problem.)
		`SELECT count(*) > 0 FROM pg_proc
		  WHERE proname = 'registry_reject_mutation'
		    AND pronamespace = current_schema()::regnamespace`).Scan(&fn); err != nil {
		t.Fatalf("check function: %v", err)
	}
	if !fn {
		t.Error("the 0004 rollback dropped registry_reject_mutation(), which 0002 owns")
	}
}

func sqlState(err error) string {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code)
	}
	return ""
}

const (
	hashD = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	hashE = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	hashF = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	hashG = "1111111111111111111111111111111111111111111111111111111111111111"
	hashH = "2222222222222222222222222222222222222222222222222222222222222222"
	hashI = "3333333333333333333333333333333333333333333333333333333333333333"
	hashJ = "4444444444444444444444444444444444444444444444444444444444444444"
	hashK = "5555555555555555555555555555555555555555555555555555555555555555"
	hashL = "6666666666666666666666666666666666666666666666666666666666666666"
	hashM = "7777777777777777777777777777777777777777777777777777777777777777"
)
