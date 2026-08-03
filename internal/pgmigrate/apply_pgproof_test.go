//go:build pgproof

// Live-Postgres proof that the EMBEDDED MIGRATION SET APPLIES.
//
// # 🔴 Why this did not exist, and what it cost
//
// Nothing in CI applied a migration past ~0009. `prove_constraints.py` and `prove_slices.py` both stop
// at `0001_p0_lineage`; the pgproof tests in internal/registry, internal/executor and their neighbours
// each hand-list the handful of files their own tables need; and internal/pgmigrate — the package whose
// entire job is applying the set — was not in the CI package list at all.
//
// So every migration from 0010 onwards was, in CI terms, a text file. Migration 0024 shipped running
// `CREATE TABLE billing_event` against a schema where 0013 had already created it: `42P07` on the first
// real deployment, and because pgmigrate runs at BOOT, the platform would not have started. The whole
// suite was green. It was caught by reading 0013 by hand, which is not a control.
//
// This test is the control. It applies the real embedded set, in order, to a real Postgres, exactly as
// a booting deployment does.
//
// # What it asserts beyond "no error"
//
// Applying cleanly is necessary and not sufficient, so three further properties are checked:
//
//   - IDEMPOTENCE. A second Apply must be a no-op reporting zero applied. The DDL is bare `CREATE
//     TABLE`, so idempotence comes entirely from the ledger being READ — a runner that re-applied
//     everything would fail the second boot of every deployment.
//
//   - LEDGER COMPLETENESS. Every embedded migration's id must be present in `schema_migrations`
//     afterwards. A file that applies but forgets to record itself is re-applied on the next boot and
//     fails there — turning an authoring slip into an outage on somebody else's upgrade.
//
//   - FRESH-INSTALL PARITY. A fresh database must end at the same ledger as an incremental one. This
//     is what makes "upgrade preserves user state" checkable rather than asserted: the two paths a
//     deployment can take must converge.
//
//     make pg-proof
//     HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/pgmigrate/
package pgmigrate

import (
	"context"
	"database/sql"
	"testing"

	"github.com/heros-foreal/agentd/internal/pgtest"
)

func openSchema(t *testing.T, schema string) *sql.DB {
	t.Helper()
	db, err := pgtest.Open(schema)
	if err != nil {
		// FAILS rather than skips: a proof that skips itself when its dependency is missing reports
		// green for something it never checked, which is the failure this whole tag exists to avoid.
		t.Fatalf("live Postgres required: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestTheEmbeddedSetAppliesToARealDatabase is the assertion whose absence let 0024 through.
func TestTheEmbeddedSetAppliesToARealDatabase(t *testing.T) {
	db := openSchema(t, "pgmigrate_full")
	ctx := context.Background()

	res, err := Apply(ctx, db)
	if err != nil {
		t.Fatalf("the embedded migration set does not apply to an empty database: %v\n\n"+
			"This is what a deployment does at BOOT. A failure here is a platform that does not start.", err)
	}
	if !res.FreshInstall() {
		t.Fatalf("expected a fresh install, got applied=%d already=%d", len(res.Applied), len(res.Already))
	}

	all, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(res.Applied) != len(all) {
		t.Fatalf("applied %d of %d embedded migrations", len(res.Applied), len(all))
	}
	t.Logf("applied %d migrations cleanly", len(res.Applied))
}

// TestASecondApplyIsANoOp: idempotence comes from READING the ledger, not from tolerant DDL.
func TestASecondApplyIsANoOp(t *testing.T) {
	db := openSchema(t, "pgmigrate_twice")
	ctx := context.Background()

	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	res, err := Apply(ctx, db)
	if err != nil {
		t.Fatalf("a second apply failed — every deployment's SECOND boot would fail this way: %v", err)
	}
	if len(res.Applied) != 0 {
		t.Fatalf("the second apply re-ran %d migration(s): %v", len(res.Applied), res.Applied)
	}
}

// TestEveryMigrationRecordsItself: a migration that applies but does not write its ledger row is
// re-applied on the next boot and fails there, on somebody else's upgrade.
func TestEveryMigrationRecordsItself(t *testing.T) {
	db := openSchema(t, "pgmigrate_ledger")
	ctx := context.Background()

	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	applied, err := AppliedIDs(ctx, db)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	all, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, m := range all {
		if !applied[m.ID] {
			t.Errorf("%s applied but did not record id %d in schema_migrations", m.Name, m.ID)
		}
	}
	if len(applied) != len(all) {
		t.Errorf("ledger holds %d ids, the embedded set has %d", len(applied), len(all))
	}
}

// TestTheSchemaCarriesWhatTheStoresQuery is the narrower version of the same gap.
//
// A migration set can apply cleanly and still not carry the columns the Go stores SELECT — which is the
// other half of what 0024 got wrong: it targeted `billing_account`, a table that does not exist, while
// the real one is `account`. Listing the (table, column) pairs the durable stores actually name means a
// store pointed at a table nobody created fails HERE rather than on a customer's first billing page.
func TestTheSchemaCarriesWhatTheStoresQuery(t *testing.T) {
	db := openSchema(t, "pgmigrate_columns")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// One row per column a durable store reads or writes by name. Deliberately hand-written: deriving
	// it from the schema would compare the schema to itself and pass on anything.
	want := map[string][]string{
		"account": {
			"customer_id", "provider_customer_handle", "active_plan_id", "plan_config_version",
			"gainshare_consent", "consented_at", "created_at",
			// The four P8 columns migration 0024 adds. Their absence is what it was written for.
			"status", "suspension_reason", "suspended_at", "quota_overrides",
		},
		"billing_event": {
			"event_id", "customer_id", "period", "type", "kind", "idempotency_key", "provider_ref",
			"amount_ref", "caused_by", "reason", "quantity", "status", "evidence_json",
			"created_at", "settled_at",
		},
		"run_link": {
			"tenant_id", "run_id", "workflow_id", "config_hash", "source_revision", "tool_version",
			"linked_at", "scores_json",
			// Migration 0023's eval evidence.
			"eval_case_count", "eval_seed_count", "eval_gate_outcome", "eval_gate_failures",
			"eval_single_seed", "per_node_json",
		},
		// 0025's scope columns. Their ABSENCE is what made api.ProposalsSource.Surface(workflowID)
		// unanswerable: 0012 built these tables single-tenant and workflow-implicit.
		"proposal": {"proposal_id", "diagnosis_id", "operator", "base_variant_id", "candidate_config_hash", "status", "tenant_id", "workflow_id"},
		"verdict":  {"proposal_id", "metric", "delta", "ci_low", "ci_high", "gate_result"},
		// `base_ref` is here twice over: the store names it, AND it is the column 0026 dropped and 0027
		// restored. A column list alone would not have caught that — the store written against the same
		// misreading would have named the same wrong set — which is why the real fence for it is the
		// round trip in internal/deliveryroute. This row is the cheap half.
		"delivery_route":          {"tenant_id", "target", "base_ref", "forge", "mode", "capability_kind", "capability_detail"},
		"workflow_ir":             {"tenant_id", "workflow_id", "source_revision", "ir_version", "received_at", "nodes_json", "edges_json"},
		"source_bundle":           {"tenant_id", "workflow_id", "source_revision", "content_hash", "size_bytes", "received_at"},
		"platform_workflow_graph": {"tenant_id", "workflow_id", "source_revision", "ir_version", "taxonomy_version", "discovered_at", "llm_calls", "view_json"},
	}

	for table, columns := range want {
		for _, col := range columns {
			var n int
			err := db.QueryRowContext(ctx,
				`SELECT count(*) FROM information_schema.columns
				  WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`,
				table, col).Scan(&n)
			if err != nil {
				t.Fatalf("introspect %s.%s: %v", table, col, err)
			}
			if n != 1 {
				t.Errorf("%s.%s is missing — a durable store names this column, so the store would fail "+
					"on its first real query", table, col)
			}
		}
	}
}
