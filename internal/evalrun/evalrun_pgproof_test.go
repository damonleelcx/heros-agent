//go:build pgproof

// Live-Postgres proof for the P4 eval data path (tasks 6.1–6.3, 6.5).
//
// The claims worth proving here are properties of the SCHEMA, not of Go code: that an under-tagged
// result cannot be written, that the natural key collapses a redelivered measurement instead of
// duplicating it, that a mislabeled reference is refused by a CHECK, and that a covered obligation
// cannot also be marked unreachable. A mocked store would only prove the mock agrees with itself.
package evalrun

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

var testDB *sql.DB

const (
	pgConfigHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pgSetHash    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestMain(m *testing.M) {
	db, err := pgtest.Open("proof_evalrun")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testDB = db
	for _, f := range []string{
		"0001_p0_lineage.up.sql",
		"0002_p2_registries.up.sql",
		"0003_p2_variant_spec.up.sql",
		"0004_p2_transform.up.sql",
		"0007_p2_verification_strength.up.sql",
		"0005_p2_run.up.sql",
		"0006_p2_run_queue.up.sql",
		"0008_p25_eval_results.up.sql",
		"0009_p4_eval_harness.up.sql",
	} {
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

// seedLineage installs the FK targets every eval_result row needs.
func seedLineage(t *testing.T) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		 VALUES ('wf','local://t','abc','go','1.1.0') ON CONFLICT DO NOTHING`,
		`INSERT INTO variant (variant_id, workflow_id, label)
		 VALUES ('v-a','wf','A') ON CONFLICT DO NOTHING`,
		`INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
		 VALUES ('` + pgConfigHash + `','v-a','wf','1.1.0','{}'::jsonb) ON CONFLICT DO NOTHING`,
		`INSERT INTO node (workflow_id, node_id) VALUES ('wf','` + telemetry.NodeIDRun + `')
		 ON CONFLICT DO NOTHING`,
		`INSERT INTO node (workflow_id, node_id) VALUES ('wf','router') ON CONFLICT DO NOTHING`,
		`INSERT INTO eval_set (eval_set_hash, workflow_id, version)
		 VALUES ('` + pgSetHash + `','wf','` + EvalSetVersion + `') ON CONFLICT DO NOTHING`,
		`INSERT INTO eval_case (case_id, workflow_id, eval_set_hash, reference_label)
		 VALUES ('c-1','wf','` + pgSetHash + `','gold') ON CONFLICT DO NOTHING`,
		`INSERT INTO eval_case (case_id, workflow_id, eval_set_hash, reference_label)
		 VALUES ('c-2','wf','` + pgSetHash + `','weak') ON CONFLICT DO NOTHING`,
	} {
		if _, err := testDB.Exec(q); err != nil {
			t.Fatalf("seed: %v\nquery: %s", err, q)
		}
	}
}

func pgRow(caseID, nodeID, metric string, seed int64, value float64) Result {
	return Result{
		VariantID: "v-a", RunID: RunIDFor(pgConfigHash, "rev-1", caseID, seed),
		NodeID: nodeID, CaseID: caseID, Seed: seed,
		Timestamp: time.Unix(1_800_000_000, 0).UTC(), ConfigHash: pgConfigHash,
		WorkflowID: "wf", MetricName: metric, Value: value, Unit: telemetry.UnitRatio,
		EvaluatorName: "exact_match", EvalSetHash: pgSetHash,
		ReferenceLabel: evalharness.LabelGold,
	}
}

// Task 6.2 — a fully-tagged row round-trips, and the statistics layer reads its input shape back out.
func TestPGResultsRoundTripFullyTagged(t *testing.T) {
	seedLineage(t)
	s := NewPGStore(testDB)

	var rows []Result
	for seed := int64(0); seed < 5; seed++ {
		rows = append(rows, pgRow("c-1", telemetry.NodeIDRun, evalharness.MetricTaskSuccess, seed, 0.5+float64(seed)/10))
	}
	if err := s.PutResults(context.Background(), rows); err != nil {
		t.Fatalf("put: %v", err)
	}

	series, err := s.SeriesFor(context.Background(), pgSetHash, "v-a", evalharness.MetricTaskSuccess)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(series.Seeds()) != 5 {
		t.Fatalf("want 5 seeds back from Postgres, got %d", len(series.Seeds()))
	}

	// Every persisted row carries all seven tags NON-NULL. The DB is the last line; this asserts it.
	var undertagged int
	if err := testDB.QueryRow(`
		SELECT count(*) FROM eval_result
		 WHERE eval_set_hash = $1
		   AND (variant_id IS NULL OR run_id IS NULL OR node_id IS NULL OR case_id IS NULL
		        OR seed IS NULL OR ts IS NULL OR config_hash IS NULL OR eval_set_hash = '')`,
		pgSetHash).Scan(&undertagged); err != nil {
		t.Fatalf("tag-completeness query: %v", err)
	}
	if undertagged != 0 {
		t.Fatalf("%d under-tagged rows reached the table", undertagged)
	}
}

// Task 6.1 — a redelivered measurement collapses on the natural key rather than duplicating.
func TestPGRedeliveredResultCollapsesOnTheNaturalKey(t *testing.T) {
	seedLineage(t)
	s := NewPGStore(testDB)
	row := pgRow("c-2", telemetry.NodeIDRun, "redelivery_probe", 0, 1)

	for i := 0; i < 3; i++ {
		if err := s.PutResults(context.Background(), []Result{row}); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	var n int
	if err := testDB.QueryRow(
		`SELECT count(*) FROM eval_result WHERE metric_name = 'redelivery_probe'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("three deliveries of one measurement must be one row, got %d", n)
	}
}

// Task 6.2 — every leaderboard slice is a QUERY over the tags.
func TestPGEveryLeaderboardSliceIsAQuery(t *testing.T) {
	seedLineage(t)
	s := NewPGStore(testDB)

	var rows []Result
	for seed := int64(0); seed < 5; seed++ {
		r := pgRow("c-1", "router", "misroute_rate", seed, 0.1)
		r.Pattern = "Routing"
		r.EvaluatorName = "misroute_rate"
		rows = append(rows, r)

		weak := pgRow("c-2", telemetry.NodeIDRun, evalharness.MetricTaskSuccess, seed, 0.9)
		weak.ReferenceLabel = evalharness.LabelWeak
		rows = append(rows, weak)
	}
	if err := s.PutResults(context.Background(), rows); err != nil {
		t.Fatalf("put: %v", err)
	}

	for _, tc := range []struct {
		name  string
		query string
		args  []any
		want  int
	}{
		{"per pattern", `SELECT count(*) FROM eval_result WHERE eval_set_hash=$1 AND pattern=$2`,
			[]any{pgSetHash, "Routing"}, 5},
		{"per node", `SELECT count(*) FROM eval_result WHERE eval_set_hash=$1 AND node_id=$2`,
			[]any{pgSetHash, "router"}, 5},
		{"per seed", `SELECT count(*) FROM eval_result WHERE eval_set_hash=$1 AND seed=$2 AND node_id=$3`,
			[]any{pgSetHash, 3, "router"}, 1},
		{"exclude weak", `SELECT count(*) FROM eval_result WHERE eval_set_hash=$1 AND reference_label='weak'`,
			[]any{pgSetHash}, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var n int
			if err := testDB.QueryRow(tc.query, tc.args...).Scan(&n); err != nil {
				t.Fatalf("query: %v", err)
			}
			if n != tc.want {
				t.Fatalf("slice %q: want %d, got %d", tc.name, tc.want, n)
			}
		})
	}
}

// The schema refuses a mislabeled reference: a typo that promoted `weak` to `gold` would let an
// unreviewed synthetic reference drive a gate.
func TestPGRefusesAnInvalidReferenceLabel(t *testing.T) {
	seedLineage(t)
	_, err := testDB.Exec(
		`INSERT INTO eval_case (case_id, workflow_id, eval_set_hash, reference_label)
		 VALUES ('c-bad','wf',$1,'golden')`, pgSetHash)
	if err == nil {
		t.Fatal("an out-of-vocabulary reference label must be refused by the schema")
	}
	if !strings.Contains(err.Error(), "reference_label") {
		t.Fatalf("the constraint must name the column, got %v", err)
	}
}

// The schema refuses an out-of-taxonomy edge-case kind: a generator cannot invent a slot the
// coverage report does not track.
func TestPGRefusesAnOutOfTaxonomyEdgeCaseKind(t *testing.T) {
	seedLineage(t)
	_, err := testDB.Exec(
		`INSERT INTO eval_case (case_id, workflow_id, eval_set_hash, edge_case_kind)
		 VALUES ('c-bad-edge','wf',$1,'made_up_kind')`, pgSetHash)
	if err == nil {
		t.Fatal("an out-of-taxonomy edge-case kind must be refused")
	}
}

// A judge cannot be stored as calibrated with no human labels — the state that would fake a
// calibration is unrepresentable.
func TestPGRefusesCalibratedJudgeWithNoHumanLabels(t *testing.T) {
	_, err := testDB.Exec(
		`INSERT INTO judge_calibration
		   (judge_metric_name, agreement, percent_agreement, n_human, floor, calibrated)
		 VALUES ('judge_score', 0.9, 0.95, 0, 0.6, TRUE)`)
	if err == nil {
		t.Fatal("calibrated with n_human = 0 must be refused")
	}

	if _, err := testDB.Exec(
		`INSERT INTO judge_calibration
		   (judge_metric_name, agreement, percent_agreement, n_human, floor, calibrated)
		 VALUES ('judge_score', 0.9, 0.95, 40, 0.6, TRUE) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("a calibrated judge with real labels must be storable: %v", err)
	}
}

// A coverage obligation cannot be both covered and unreachable — a report that contradicts itself
// is worse than no report.
func TestPGRefusesContradictoryCoverageItem(t *testing.T) {
	seedLineage(t)
	_, err := testDB.Exec(
		`INSERT INTO coverage_item (eval_set_hash, dimension, item_id, kind, covered, unreachable)
		 VALUES ($1,'path','router->branch_dead','edge',TRUE,TRUE)`, pgSetHash)
	if err == nil {
		t.Fatal("covered AND unreachable must be refused")
	}

	// The honest shape — uncovered and unreachable — IS storable, because that is what a residual is.
	if _, err := testDB.Exec(
		`INSERT INTO coverage_item (eval_set_hash, dimension, item_id, kind, covered, unreachable)
		 VALUES ($1,'path','router->branch_dead','edge',FALSE,TRUE) ON CONFLICT DO NOTHING`,
		pgSetHash); err != nil {
		t.Fatalf("a reported residual must be storable: %v", err)
	}
}

// A normalized value outside [0,1] cannot enter the score cache.
func TestPGRefusesOutOfRangeNormalizedValue(t *testing.T) {
	seedLineage(t)
	_, err := testDB.Exec(
		`INSERT INTO score_cache
		   (variant_id, eval_set_hash, metric_name, normalized_value, raw_mean, scale_min, scale_max)
		 VALUES ('v-a',$1,'task_success', 4.2, 0.9, 0, 1)`, pgSetHash)
	if err == nil {
		t.Fatal("a normalized value outside [0,1] must be refused")
	}
}

// An interval whose low exceeds its high is not an interval.
func TestPGRefusesInvertedConfidenceInterval(t *testing.T) {
	seedLineage(t)
	_, err := testDB.Exec(
		`INSERT INTO metric_stat
		   (eval_set_hash, variant_id, config_hash, metric_name, mean, ci_low, ci_high,
		    n_seeds, n_cases, n_obs, method, confidence)
		 VALUES ($1,'v-a',$2,'task_success', 0.8, 0.9, 0.7, 5, 20, 100, 'bootstrap', 0.95)`,
		pgSetHash, pgConfigHash)
	if err == nil {
		t.Fatal("an inverted confidence interval must be refused")
	}
}

// The rollback is proved, not assumed. A down migration nobody has run is a rollback plan that
// discovers its own ordering bug during an incident. This applies the whole chain up, rolls 0009
// back, and applies it again — in a schema of its own so it cannot disturb the other proofs.
func TestPGMigration0009RollsBackAndReapplies(t *testing.T) {
	db, err := pgtest.Open("proof_evalrun_rollback")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	apply := func(file string) error {
		b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", file))
		if err != nil {
			return err
		}
		_, err = db.Exec(string(b))
		return err
	}
	for _, f := range []string{
		"0001_p0_lineage.up.sql", "0002_p2_registries.up.sql", "0003_p2_variant_spec.up.sql",
		"0004_p2_transform.up.sql", "0007_p2_verification_strength.up.sql", "0005_p2_run.up.sql",
		"0006_p2_run_queue.up.sql", "0008_p25_eval_results.up.sql", "0009_p4_eval_harness.up.sql",
	} {
		if err := apply(f); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}

	if err := apply("0009_p4_eval_harness.down.sql"); err != nil {
		t.Fatalf("rollback 0009: %v", err)
	}
	// The P4 tables are gone and the P2.5 fact table survives — expand-only in reverse.
	for _, table := range []string{"eval_set", "metric_stat", "judge_calibration", "score_cache",
		"weight_profile", "gate_set", "eval_run", "coverage_item"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if exists {
			t.Fatalf("rollback left %s behind", table)
		}
	}
	var evalResultExists bool
	if err := db.QueryRow(`SELECT to_regclass('eval_result') IS NOT NULL`).Scan(&evalResultExists); err != nil {
		t.Fatalf("check eval_result: %v", err)
	}
	if !evalResultExists {
		t.Fatal("the rollback removed a table 0009 did not create")
	}
	var version int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE id = 9`).Scan(&version); err != nil {
		t.Fatalf("bookkeeping: %v", err)
	}
	if version != 0 {
		t.Fatal("the rollback must remove its own schema_migrations row")
	}

	// And it re-applies cleanly — the second run of a migration must succeed (idempotence).
	if err := apply("0009_p4_eval_harness.up.sql"); err != nil {
		t.Fatalf("re-apply 0009: %v", err)
	}
	if err := apply("0009_p4_eval_harness.up.sql"); err != nil {
		t.Fatalf("0009 must be idempotent; second apply failed: %v", err)
	}
}
