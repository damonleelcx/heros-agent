//go:build pgproof

// Live-Postgres proof for P29's two migrations (§3.3).
//
// 🔴 A SQLite-only run does not close this task, and the rule is not a formality. The last time a dialect
// went unrun in this product family, `SELECT d.*` in a view froze its column list at CREATE VIEW on
// PostgreSQL and re-expanded per query on SQLite — so new columns reached development and never reached a
// customer, with every migration green and only the view's readers coming up empty. **An unrun dialect is
// not missing coverage; it is cover for the failures already in it.**
//
//	make pg-proof
//	HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/pgmigrate/ -run P29
package pgmigrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// applyDownThenUp runs a migration's `.down.sql` and then its `.up.sql` again.
//
// The down files are deliberately NOT embedded (see db/migrations/embed.go: a binary that can drop the
// customer's tables on some code path is a binary that eventually does), so they are read from disk here
// — which is the only place they are ever executed, by a test, against a throwaway schema.
func applyDownThenUp(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	dir := filepath.Join("..", "..", "db", "migrations", "postgres")
	for _, suffix := range []string{".down.sql", ".up.sql"} {
		b, err := os.ReadFile(filepath.Join(dir, name+suffix))
		if err != nil {
			t.Fatalf("read %s%s: %v", name, suffix, err)
		}
		if _, err := db.ExecContext(context.Background(), string(b)); err != nil {
			t.Fatalf("%s%s failed: %v", name, suffix, err)
		}
	}
}

// 🔴 §3.3 — both migrations on a REAL Postgres: apply, RE-apply, down, up again.
//
// The re-apply is the half that catches the common defect. `ADD COLUMN IF NOT EXISTS` and
// `CREATE TABLE IF NOT EXISTS` make the DDL safe on a second run; the DO blocks that verify the
// DEFINITION do not have that protection for free, and a definition check written carelessly fails on
// exactly the run where nothing is wrong.
func TestP29MigrationsApplyReapplyAndSurviveADownAndUp(t *testing.T) {
	db := openSchema(t, "p29_migrations")
	ctx := context.Background()

	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("the embedded set does not apply: %v", err)
	}
	// A second Apply is the ledger doing its job; the two DO blocks run again inside it.
	res, err := Apply(ctx, db)
	if err != nil {
		t.Fatalf("a SECOND apply failed — this is every deployment's second boot: %v", err)
	}
	if len(res.Applied) != 0 {
		t.Fatalf("a second apply re-ran %d migration(s); it must be a no-op", len(res.Applied))
	}

	// Down, then up, for each of the two — independently, because they touch different objects and a
	// rollback may take only one of them.
	applyDownThenUp(t, db, "0042_p29_workflow_ir_coverage_version")
	applyDownThenUp(t, db, "0043_p29_linked_transform")

	// And the ledger is intact afterwards.
	for _, id := range []int{42, 43} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatalf("read ledger: %v", err)
		}
		if n != 1 {
			t.Errorf("schema_migrations has %d row(s) for %d after a down-and-up; want exactly 1", n, id)
		}
	}
}

// 0042's column exists, is text, and is NULLABLE — the last of which is the one that carries meaning.
func TestP29CoverageVersionIsNullableText(t *testing.T) {
	db := openSchema(t, "p29_shape")
	if _, err := Apply(context.Background(), db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var dataType, isNullable, colDefault sql.NullString
	err := db.QueryRow(`SELECT data_type, is_nullable, column_default FROM information_schema.columns
	                     WHERE table_name = 'workflow_ir' AND column_name = 'coverage_version'`).
		Scan(&dataType, &isNullable, &colDefault)
	if err != nil {
		t.Fatalf("workflow_ir.coverage_version is absent: %v", err)
	}
	if dataType.String != "text" {
		t.Errorf("data_type = %q, want text", dataType.String)
	}
	if isNullable.String != "YES" {
		t.Errorf("is_nullable = %q, want YES — NULL is the only honest value for a row written before "+
			"this change, and it is what the projection reads as `not reported`", isNullable.String)
	}
	if colDefault.Valid {
		t.Errorf("column_default = %q. A DEFAULT would silently backfill every future insert that omits "+
			"the column, which is the fabrication the nullability exists to prevent.", colDefault.String)
	}
}

// 🔴 §3.5 — a row written BEFORE this change reads back as `not reported`, never as a default.
//
// This is asserted by writing the pre-change row the pre-change way — an INSERT that names the old column
// list — and then reading it through the CURRENT store. Simulating it by writing ” through the new
// store would test the wrong thing entirely: it would prove the store round-trips an empty string.
func TestP29ARowWrittenBeforeThisChangeReadsAsNotReported(t *testing.T) {
	db := openSchema(t, "p29_absent")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO workflow_ir (tenant_id, workflow_id, source_revision, ir_version, received_at,
		                          nodes_json, edges_json)
		 VALUES ('t-old','wf-old','rev-old','v1',$1,'[]'::jsonb,'[]'::jsonb)`, time.Now().UTC())
	if err != nil {
		t.Fatalf("a pre-change INSERT (old column list) failed — an older image would not be able to "+
			"write at all: %v", err)
	}
	var coverage sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT coverage_version FROM workflow_ir WHERE tenant_id='t-old'`).Scan(&coverage); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if coverage.Valid {
		t.Errorf("a row written without a coverage version read back as %q. It must be NULL: the platform "+
			"was never told which table those verdicts came from, and substituting one would date somebody "+
			"else's structure to a table it was never computed against.", coverage.String)
	}
}

// 🔴 §3.6 — two transmissions of the same receipt leave ONE row.
func TestP29ATransformReceiptUpsertsRatherThanAppends(t *testing.T) {
	db := openSchema(t, "p29_upsert")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	ins := func(status string, files int) {
		t.Helper()
		_, err := db.ExecContext(ctx,
			`INSERT INTO linked_transform (tenant_id, config_hash, source_revision, workflow_id,
			     tool_version, coverage_version, status, received_at, node_outcomes_json,
			     files_changed, lines_added, lines_removed)
			 VALUES ('t','h','r','wf','0.1',NULL,$1,$2,'[]'::jsonb,$3,0,0)
			 ON CONFLICT (tenant_id, config_hash, source_revision) DO UPDATE
			   SET status = EXCLUDED.status, files_changed = EXCLUDED.files_changed`,
			status, time.Now().UTC(), files)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	ins("applied", 3)
	ins("applied", 5)

	var rows, files int
	var status string
	if err := db.QueryRowContext(ctx,
		`SELECT count(*), max(files_changed), max(status) FROM linked_transform WHERE tenant_id='t'`).
		Scan(&rows, &files, &status); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if rows != 1 {
		t.Errorf("two transmissions of one receipt left %d row(s). The grain IS the key: a second row "+
			"would make \"which transform is this\" depend on insertion order.", rows)
	}
	if files != 5 {
		t.Errorf("the later transmission did not replace the earlier (files_changed = %d, want 5)", files)
	}
}

// The diffstat cannot be negative, checked in the DATABASE and not only at the handler — the two fail
// independently, and a future writer that bypasses the handler still cannot store a receipt that renders
// as "-3 files changed" on a paid surface.
func TestP29TheDiffstatCheckFiresInTheDatabase(t *testing.T) {
	db := openSchema(t, "p29_check")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO linked_transform (tenant_id, config_hash, source_revision, workflow_id,
		     tool_version, status, received_at, node_outcomes_json, files_changed, lines_added, lines_removed)
		 VALUES ('t','h2','r','wf','0.1','applied',$1,'[]'::jsonb,-1,0,0)`, time.Now().UTC())
	if err == nil {
		t.Fatal("a negative files_changed was accepted by the database")
	}
	if !strings.Contains(err.Error(), "linked_transform_counts_nonneg") {
		t.Errorf("the insert failed for the wrong reason: %v", err)
	}
}
