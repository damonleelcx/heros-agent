//go:build pgproof

// Live-Postgres proof of the P12 delivery schema (migration 0015): the constraints that make
// append-only, one-open-PR-per-delivery, mode-recorded, and close-is-not-merge true BY CONSTRUCTION
// rather than by application care — and the proof that delivery leaves `transform` immutability intact
// (task 4.2).
//
// It runs the REAL migration chain against a REAL server — no inlined CREATE TABLE — because a test
// that builds its own approximation of the production schema proves only that the approximation
// behaves, which is exactly how a missing constraint reaches a customer with CI green.
//
// Every assertion is of the form "the database REJECTS this": a constraint nobody has watched reject
// anything is a constraint that may not exist.
package deliveryrecord

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/pgtest"
)

var testDB *sql.DB

// migrationChain is the whole ordered chain up to 0015. Applying the CHAIN (not just 0015) is the
// point: 0015 sits on top of schema_migrations (0001) and must coexist with the transform immutability
// triggers (0002/0004), which this proof also exercises.
var migrationChain = []string{
	"0001_p0_lineage.up.sql",
	"0002_p2_registries.up.sql",
	"0003_p2_variant_spec.up.sql",
	"0004_p2_transform.up.sql",
	"0015_p12_delivery.up.sql",
}

func TestMain(m *testing.M) {
	db, err := pgtest.Open("proof_p12_delivery")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testDB = db
	for _, f := range migrationChain {
		if err := apply(db, f); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

func apply(db *sql.DB, name string) error {
	b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", name))
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if _, err := db.Exec(string(b)); err != nil {
		return fmt.Errorf("apply %s: %w", name, err)
	}
	return nil
}

func rejects(t *testing.T, label, query string, args ...any) {
	t.Helper()
	if _, err := testDB.Exec(query, args...); err == nil {
		t.Errorf("the database ACCEPTED %s — the constraint does not fire", label)
	} else {
		t.Logf("ok  rejected (as intended): %s -> %s", label, firstLine(err.Error()))
	}
}

func accepts(t *testing.T, label, query string, args ...any) {
	t.Helper()
	if _, err := testDB.Exec(query, args...); err != nil {
		t.Errorf("the database REJECTED a legitimate %s: %v", label, err)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

const ins = `INSERT INTO delivery (delivery_id,tenant_id,config_hash,source_revision,target,forge_ref,mode,state,actor,reason,merge_commit)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`

// TestMigrationIsIdempotent: the second run must succeed and leave one marker row.
func TestMigrationIsIdempotent(t *testing.T) {
	if err := apply(testDB, "0015_p12_delivery.up.sql"); err != nil {
		t.Fatalf("re-applying 0015 must be a no-op, got: %v", err)
	}
	var n int
	if err := testDB.QueryRow(`SELECT count(*) FROM schema_migrations WHERE id = 15`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("schema_migrations rows for id=15 = %d, want 1", n)
	}
}

// TestOneOpenPullRequestPerDelivery proves the partial unique index — the guarantee that makes
// idempotency hold under CONCURRENCY (task 2.3 / 7.1): two racing opens both pass a check-then-insert,
// and only one can insert here.
func TestOneOpenPullRequestPerDelivery(t *testing.T) {
	accepts(t, "first opened", ins, "d_open", "t1", "ch", "rev", "o/r", "o/r#1", "ci", "opened", "ci:p", nil, nil)
	rejects(t, "a SECOND opened for the same delivery", ins, "d_open", "t1", "ch", "rev", "o/r", "o/r#1", "ci", "opened", "ci:p2", nil, nil)
	// An 'updated' append is fine — the index is only on 'opened'.
	accepts(t, "an updated append", ins, "d_open", "t1", "ch", "rev", "o/r", "o/r#1", "ci", "updated", "ci:p", nil, nil)
}

// TestDeliveryIsAppendOnly proves the trigger refuses UPDATE and DELETE outright — a state change has
// no choice but to be a new row (task 4.1 / design Decision 4).
func TestDeliveryIsAppendOnly(t *testing.T) {
	accepts(t, "a delivery row", ins, "d_ap", "t1", "ch", "rev", "o/r", "o/r#9", "ci", "opened", "ci:p", nil, nil)
	rejects(t, "an UPDATE of a delivery row",
		`UPDATE delivery SET actor='x' WHERE delivery_id='d_ap'`)
	rejects(t, "a DELETE of a delivery row",
		`DELETE FROM delivery WHERE delivery_id='d_ap'`)
	rejects(t, "a TRUNCATE of the delivery table", `TRUNCATE delivery`)
}

// TestCloseIsNotAMerge proves the schema will not let a merged row exist without an observed commit,
// and forbids a merge_commit on any non-merged row — so a close can never be dressed up as a merge
// (task 4.4).
func TestCloseIsNotAMerge(t *testing.T) {
	rejects(t, "a merged row with no merge commit (an inference)",
		ins, "d_m1", "t1", "ch", "rev", "o/r", "o/r#2", "ci", "merged", "ci", nil, nil)
	rejects(t, "a closed row carrying a merge commit",
		ins, "d_m2", "t1", "ch", "rev", "o/r", "o/r#3", "ci", "closed", "webhook", nil, "abc123")
	accepts(t, "a merged row with its observed commit",
		ins, "d_m3", "t1", "ch", "rev", "o/r", "o/r#4", "ci", "merged", "ci", nil, "abc123")
	accepts(t, "a plain close with no merge commit",
		ins, "d_m4", "t1", "ch", "rev", "o/r", "o/r#5", "ci", "closed", "webhook", nil, nil)
}

// TestRejectsUnsoundRows: each of these would be an unexplainable record entry.
func TestRejectsUnsoundRows(t *testing.T) {
	rejects(t, "unknown mode",
		ins, "d_b1", "t1", "ch", "rev", "o/r", "o/r#6", "sftp", "opened", "ci", nil, nil)
	rejects(t, "unknown state",
		ins, "d_b2", "t1", "ch", "rev", "o/r", "o/r#6", "ci", "abandoned", "ci", nil, nil)
	rejects(t, "empty forge_ref",
		ins, "d_b3", "t1", "ch", "rev", "o/r", "", "ci", "opened", "ci", nil, nil)
	rejects(t, "a supersession with no reason",
		ins, "d_b4", "t1", "ch", "rev", "o/r", "o/r#7", "ci", "superseded", "supersession", nil, nil)
	accepts(t, "a supersession with its reason",
		ins, "d_b5", "t1", "ch", "rev", "o/r", "o/r#8", "ci", "superseded", "supersession", "superseded by newer", nil)
}

// TestTransformImmutabilityStillHolds proves task 4.2 at the storage layer: delivery being in the
// schema does NOT relax transform's immutability. Proven two ways:
//
//  1. The pre-existing immutability trigger is still present on `transform` after 0015 applied — a
//     catalog fact that does not depend on seeding the deep config→variant_spec→transform FK chain.
//  2. Seeded end to end (workflow→variant→config→variant_spec→transform), an UPDATE is still rejected.
//     The seed is the real chain; if any shape drifts the seed fails loudly rather than skipping.
func TestTransformImmutabilityStillHolds(t *testing.T) {
	// (1) The trigger survives delivery's migration.
	var n int
	if err := testDB.QueryRow(
		`SELECT count(*) FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
		 WHERE c.relname = 'transform' AND t.tgname = 'transform_immutable'`).Scan(&n); err != nil {
		t.Fatalf("pg_trigger lookup: %v", err)
	}
	if n != 1 {
		t.Fatalf("the transform_immutable trigger is not present after delivery installed (count=%d)", n)
	}

	// (2) End-to-end rejection through the real FK chain.
	const ch = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 64 hex
	seed := []string{
		`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		 VALUES ('wf_t','https://x/y','revt','python','1') ON CONFLICT DO NOTHING`,
		`INSERT INTO variant (variant_id, workflow_id, label) VALUES ('v_t','wf_t','v') ON CONFLICT DO NOTHING`,
		`INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
		 VALUES ('` + ch + `','v_t','wf_t','1','{}'::jsonb) ON CONFLICT DO NOTHING`,
		`INSERT INTO variant_spec (config_hash, source_revision, spec_json)
		 VALUES ('` + ch + `','revt','{}'::jsonb) ON CONFLICT DO NOTHING`,
		`INSERT INTO transform (config_hash, source_revision, build_status)
		 VALUES ('` + ch + `','revt','built') ON CONFLICT DO NOTHING`,
	}
	for _, q := range seed {
		if _, err := testDB.Exec(q); err != nil {
			t.Fatalf("seed transform FK chain (%s): %v", firstLine(q), err)
		}
	}
	rejects(t, "an UPDATE of an (immutable) transform row after delivery is installed",
		`UPDATE transform SET build_log='x' WHERE config_hash='`+ch+`' AND source_revision='revt'`)
}
