//go:build pgproof

// The down-migration proof is DESTRUCTIVE — it drops the columns 0008 added — so it must run LAST in
// the whole package, after every test that relies on those columns (the integration test writes to
// evaluator_name). Go runs tests in source order with files ordered alphabetically, so this file's
// `zz_` prefix guarantees it sorts after evalstore_/integration_/m3_. Keeping it in its own file makes
// that ordering intent explicit rather than an accident of where a function happened to sit.
package telemetry

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The down migration removes ONLY what 0008 added (expand-only in reverse).
func TestPG_ZZ_DownMigrationIsExpandOnly(t *testing.T) {
	ctx := context.Background()
	// Narrowing the natural key back requires data that satisfies the NARROW key. Earlier tests wrote
	// rows that differ only by evaluator_name (legal under the wide key, colliding under the narrow one)
	// — a real rollback with such data would fail loudly, which is correct. This test proves the
	// STRUCTURAL rollback, so clear the rows first to isolate the schema change from the data caveat.
	if _, err := testDB.ExecContext(ctx, `DELETE FROM eval_result`); err != nil {
		t.Fatalf("clear eval_result: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", "0008_p25_eval_results.down.sql"))
	if err != nil {
		t.Fatalf("read down: %v", err)
	}
	if _, err := testDB.ExecContext(ctx, string(down)); err != nil {
		t.Fatalf("apply down: %v", err)
	}
	// evaluator_name is gone; eval_result and its seven tag columns survive.
	var hasEvaluator bool
	if err := testDB.QueryRowContext(ctx,
		// current_schema(): pgtest gives each proof its own schema, and more than one of them now has
		// an `eval_result` — an unfiltered information_schema query answers about someone else's
		// table. Without the filter this test went green only for as long as no other package
		// created that table, and turned red the moment P4's proof did. (The same filter is on
		// worktree's equivalent check, for the same reason.)
		`SELECT EXISTS(SELECT 1 FROM information_schema.columns
		 WHERE table_schema = current_schema()
		   AND table_name='eval_result' AND column_name='evaluator_name')`).Scan(&hasEvaluator); err != nil {
		t.Fatalf("column check: %v", err)
	}
	if hasEvaluator {
		t.Error("evaluator_name survived the down migration")
	}
	var hasEvalResult bool
	if err := testDB.QueryRowContext(ctx, `SELECT to_regclass('eval_result') IS NOT NULL`).Scan(&hasEvalResult); err != nil {
		t.Fatalf("table check: %v", err)
	}
	if !hasEvalResult {
		t.Error("the 0008 rollback removed eval_result, which it does not own")
	}
}
