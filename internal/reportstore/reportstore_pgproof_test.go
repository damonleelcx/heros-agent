//go:build pgproof

// Live-Postgres proof for the P4.5 read-only report schema (task 8.2 / 8.4). The claims worth proving
// here are properties of the SCHEMA, not of Go code: an ablation row cannot be stored as non-ephemeral
// (it can never masquerade as a user variant), a diagnosis cannot be stored evidence-free (never a
// bare label), and a rule-sourced diagnosis cannot carry analyst trust fields. A mocked store would
// only prove the mock agrees with itself.
package reportstore

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/heros-foreal/agentd/internal/pgtest"
)

var testDB *sql.DB

const (
	pgVariant = "v-pg"
	pgConfig  = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	pgSet     = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	pgWF      = "wf-pg"
	pgCase    = "case-pg"
)

func TestMain(m *testing.M) {
	db, err := pgtest.Open("proof_reportstore")
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
		"0010_p45_attribution_diagnosis.up.sql",
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
	seed(db)
	os.Exit(m.Run())
}

// seed inserts the FK targets the report rows reference (read-side integrity), so the tests exercise
// the report CHECKs rather than tripping on a missing parent.
func seed(db *sql.DB) {
	must := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			fmt.Fprintf(os.Stderr, "seed: %v\n", err)
			os.Exit(1)
		}
	}
	must(`INSERT INTO workflow (workflow_id) VALUES ($1) ON CONFLICT DO NOTHING`, pgWF)
	must(`INSERT INTO config (config_hash, lineage_json) VALUES ($1, '{}') ON CONFLICT DO NOTHING`, pgConfig)
	must(`INSERT INTO variant (variant_id, workflow_id, config_hash) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, pgVariant, pgWF, pgConfig)
	must(`INSERT INTO eval_set (eval_set_hash, workflow_id, version) VALUES ($1,$2,'v1') ON CONFLICT DO NOTHING`, pgSet, pgWF)
	must(`INSERT INTO eval_case (case_id, workflow_id, suite) VALUES ($1,$2,'s') ON CONFLICT DO NOTHING`, pgCase, pgWF)
}

// Task 8.4: an ablation row CANNOT be stored as non-ephemeral — the DB refuses to let a measurement
// variant masquerade as a user variant.
func TestPG_AblationMustBeEphemeral(t *testing.T) {
	_, err := testDB.Exec(`INSERT INTO ablation_result
		(ablation_id, variant_id, eval_set_hash, config_hash, node_id, swapped_config_ref, metric,
		 delta_mean, ci_low, ci_high, n_seeds, verdict, ephemeral)
		VALUES ('abl-1',$1,$2,$3,'node3','ref','task_success', 0.1, 0.05, 0.15, 5, 'bottleneck', FALSE)`,
		pgVariant, pgSet, pgConfig)
	if err == nil {
		t.Fatal("expected the ephemeral CHECK to refuse a non-ephemeral ablation row")
	}
}

// Task 6.x: a diagnosis cannot be stored evidence-free — never a bare label.
func TestPG_DiagnosisMustCarryEvidence(t *testing.T) {
	_, err := testDB.Exec(`INSERT INTO diagnosis
		(diag_id, variant_id, eval_set_hash, config_hash, node_id, taxonomy_code, taxonomy_version,
		 source, confidence, evidence_case_ids_json)
		VALUES ('d-empty',$1,$2,$3,'node3','prompt_format_drift','heros.p45.taxonomy.v1','rule',1.0,'[]')`,
		pgVariant, pgSet, pgConfig)
	if err == nil {
		t.Fatal("expected the evidence CHECK to refuse an evidence-free diagnosis")
	}
}

// A rule-sourced diagnosis cannot carry analyst trust fields.
func TestPG_RuleDiagnosisHasNoAnalystFields(t *testing.T) {
	_, err := testDB.Exec(`INSERT INTO diagnosis
		(diag_id, variant_id, eval_set_hash, config_hash, node_id, taxonomy_code, taxonomy_version,
		 source, confidence, evidence_case_ids_json, agreement, n_human, analyst_flagged)
		VALUES ('d-rule-bad',$1,$2,$3,'node3','prompt_format_drift','heros.p45.taxonomy.v1','rule',1.0,
		        '["case-pg"]', 0.8, 5, TRUE)`,
		pgVariant, pgSet, pgConfig)
	if err == nil {
		t.Fatal("expected the source CHECK to refuse analyst trust fields on a rule diagnosis")
	}
}

// A valid analyst diagnosis with evidence is accepted.
func TestPG_ValidAnalystDiagnosisAccepted(t *testing.T) {
	_, err := testDB.Exec(`INSERT INTO diagnosis
		(diag_id, variant_id, eval_set_hash, config_hash, node_id, taxonomy_code, taxonomy_version,
		 source, confidence, evidence_case_ids_json, agreement, n_human, calibrated, analyst_flagged, low_confidence)
		VALUES ('d-analyst',$1,$2,$3,'reflect','non_convergence','heros.p45.taxonomy.v1','analyst',0.7,
		        '["case-pg"]', 0.62, 20, TRUE, FALSE, FALSE)
		ON CONFLICT (diag_id) DO NOTHING`,
		pgVariant, pgSet, pgConfig)
	if err != nil {
		t.Fatalf("valid analyst diagnosis rejected: %v", err)
	}
}
