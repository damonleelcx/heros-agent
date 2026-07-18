//go:build pgproof

// Live-Postgres proof for the eval-results store + the 0008 expand-only migration (task 5.3).
package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/lib/pq"
)

var testDB *sql.DB

const evalCfgHash = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

func TestMain(m *testing.M) {
	db, err := pgtest.Open("proof_telemetry_eval")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testDB = db
	for _, f := range []string{"0001_p0_lineage.up.sql", "0008_p25_eval_results.up.sql"} {
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

func seedLineage(t *testing.T) {
	t.Helper()
	stmts := []string{
		`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		 VALUES ('wf','local://t','abc','go','1.0.0') ON CONFLICT DO NOTHING`,
		`INSERT INTO variant (variant_id, workflow_id, label) VALUES ('v','wf','base') ON CONFLICT DO NOTHING`,
		`INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
		 VALUES ('` + evalCfgHash + `','v','wf','1.0.0','{}') ON CONFLICT DO NOTHING`,
		`INSERT INTO node (workflow_id, node_id) VALUES ('wf','n_a') ON CONFLICT DO NOTHING`,
		`INSERT INTO eval_case (case_id, workflow_id) VALUES ('case_1','wf') ON CONFLICT DO NOTHING`,
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

func qevent(metric string, value float64, evaluator string) QualityMetricEvent {
	seed := int64(3)
	v := value
	return QualityMetricEvent{
		Event: metricevent.Event{
			SchemaVersion: metricevent.SchemaVersion,
			VariantID:     "v", RunID: "run_1", NodeID: "n_a", CaseID: "case_1",
			Seed: &seed, Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			ConfigHash: evalCfgHash, MetricName: metric, Value: &v, Unit: UnitCount,
		},
		EvaluatorName: evaluator,
	}
}

// Task 5.4 (comparison store): a quality event round-trips and is queryable by config_hash.
func TestPG_EvalStore_WriteAndQueryByConfigHash(t *testing.T) {
	seedLineage(t)
	s := NewPGEvalStore(testDB)
	ctx := context.Background()

	if err := s.PutEval(ctx, qevent("quality_score", 0.9, "reference")); err != nil {
		t.Fatalf("PutEval: %v", err)
	}
	rows, err := s.ByConfigHash(ctx, evalCfgHash)
	if err != nil {
		t.Fatalf("ByConfigHash: %v", err)
	}
	if len(rows) != 1 || rows[0].MetricName != "quality_score" || rows[0].EvaluatorName != "reference" {
		t.Fatalf("row did not round-trip: %+v", rows)
	}
	if rows[0].ConfigHash != evalCfgHash {
		t.Errorf("row not keyed by config_hash: %q", rows[0].ConfigHash)
	}
}

// Task 2.2 second layer: the DB refuses an under-tagged row even if application code bypasses the gate.
func TestPG_EvalStore_DBRefusesUntaggedRow(t *testing.T) {
	seedLineage(t)
	// A raw insert omitting seed — the NOT NULL column is the last line of defense.
	_, err := testDB.Exec(
		`INSERT INTO eval_result (config_hash, variant_id, run_id, node_id, case_id, ts, workflow_id, metric_name, value, unit, evaluator_name)
		 VALUES ('` + evalCfgHash + `','v','run_1','n_a','case_1', now(), 'wf','m',1,'count','reference')`)
	if err == nil {
		t.Fatal("a row missing seed was accepted")
	}
	if got := sqlState(err); got != "23502" { // not_null_violation
		t.Errorf("rejected by %s, want NOT NULL (23502): %v", got, err)
	}
}

// The widened natural key: two DIFFERENT evaluators emitting the same metric for the same coordinates
// are TWO rows (not a collision); the SAME evaluator re-emitting is idempotent (one row).
func TestPG_EvalStore_WidenedNaturalKeyDistinguishesEvaluators(t *testing.T) {
	seedLineage(t)
	s := NewPGEvalStore(testDB)
	ctx := context.Background()

	if err := s.PutEval(ctx, qevent("score", 1, "eval_a")); err != nil {
		t.Fatalf("PutEval a: %v", err)
	}
	if err := s.PutEval(ctx, qevent("score", 1, "eval_a")); err != nil {
		t.Fatalf("idempotent re-put must be a no-op: %v", err)
	}
	if err := s.PutEval(ctx, qevent("score", 2, "eval_b")); err != nil {
		t.Fatalf("PutEval b: %v", err)
	}

	var n int
	if err := testDB.QueryRowContext(ctx,
		`SELECT count(*) FROM eval_result WHERE run_id='run_1' AND metric_name='score'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("two evaluators produced %d rows, want 2 (widened natural key); a re-put of eval_a must not add a third", n)
	}
}

// evaluator_name is required — an unnamed evaluator's output is unattributable.
func TestPG_EvalStore_EvaluatorNameRequired(t *testing.T) {
	seedLineage(t)
	err := NewPGEvalStore(testDB).PutEval(context.Background(), qevent("score", 1, ""))
	if err == nil {
		t.Fatal("an event with no evaluator_name was accepted")
	}
}

// A row for a config_hash that was never recorded is refused (unattributable), not silently dropped.
func TestPG_EvalStore_UnknownConfigHashIsLoud(t *testing.T) {
	seedLineage(t)
	ev := qevent("score", 1, "reference")
	ev.ConfigHash = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	err := NewPGEvalStore(testDB).PutEval(context.Background(), ev)
	if err == nil {
		t.Fatal("a row for an unrecorded config_hash was accepted")
	}
}
