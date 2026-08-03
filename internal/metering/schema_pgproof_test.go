//go:build pgproof

// Live-Postgres proof of the P7 schema (migration 0013): the constraints that make
// never-double-count, never-double-charge, append-only corrections, no-card-data and
// gainshare-traces-to-its-evidence true BY CONSTRUCTION rather than by application care.
//
// This runs the REAL migration chain against a REAL server — no inlined CREATE TABLE anywhere, because
// a test that builds its own approximation of the production schema proves only that the approximation
// behaves, which is exactly how a missing column reaches a customer with CI green.
//
// Every assertion below is of the form "the database REJECTS this": a constraint nobody has watched
// reject anything is a constraint that may not exist.
package metering

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

// migrationChain is the whole ordered chain up to 0013. Applying the CHAIN (not just 0013) is the
// point: 0013's foreign keys and its `schema_migrations` row only make sense on top of what came
// before, and an upgrade path is what a customer actually runs.
var migrationChain = []string{
	"0001_p0_lineage.up.sql",
	"0013_p7_billing_metering.up.sql",
}

func TestMain(m *testing.M) {
	db, err := pgtest.Open("proof_p7_billing")
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

// TestMigrationIsIdempotent is the migration-script rule's first requirement: the second run must
// succeed. It is what lets a new binary self-heal an older database and lets an installer be re-run.
func TestMigrationIsIdempotent(t *testing.T) {
	if err := apply(testDB, "0013_p7_billing_metering.up.sql"); err != nil {
		t.Fatalf("re-applying 0013 must be a no-op, got: %v", err)
	}
	var n int
	if err := testDB.QueryRow(`SELECT count(*) FROM schema_migrations WHERE id = 13`).Scan(&n); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if n != 1 {
		t.Errorf("schema_migrations rows for id=13 = %d, want 1", n)
	}
}

func seedAccount(t *testing.T, id string) {
	t.Helper()
	_, err := testDB.Exec(`INSERT INTO account
		(customer_id, provider_customer_handle, active_plan_id, plan_config_version)
		VALUES ($1, 'prov_cus_'||$1, 'team', 'cfg-a') ON CONFLICT DO NOTHING`, id)
	if err != nil {
		t.Fatalf("seed account %s: %v", id, err)
	}
}

func rejects(t *testing.T, label, query string, args ...any) {
	t.Helper()
	_, err := testDB.Exec(query, args...)
	if err == nil {
		t.Errorf("the database ACCEPTED %s — the constraint does not fire", label)
		return
	}
	t.Logf("ok  rejected (as intended): %s -> %s", label, firstLine(err.Error()))
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

// TestAccountRefusesCardData proves design Decision 10 at the storage layer: a PAN cannot be stored in
// the provider-handle column, so a mis-wired integration fails at the database instead of putting the
// platform in PCI scope.
func TestAccountRefusesCardData(t *testing.T) {
	for _, pan := range []string{"4242424242424242", "4242 4242 4242 4242", "4242-4242-4242-4242", "378282246310005"} {
		rejects(t, "card-shaped provider handle "+pan,
			`INSERT INTO account (customer_id, provider_customer_handle, active_plan_id, plan_config_version)
			 VALUES ('cus_pan_'||md5($1), $1, 'free', 'cfg-a')`, pan)
	}
	accepts(t, "opaque provider handle",
		`INSERT INTO account (customer_id, provider_customer_handle, active_plan_id, plan_config_version)
		 VALUES ('cus_ok', 'prov_cus_9f3a21', 'free', 'cfg-a')`)
}

// TestAccountConsentIsTimestamped: consent and its timestamp move together, so a revocation cannot
// leave a stale consented_at that an auditor reads as still-consented.
func TestAccountConsentIsTimestamped(t *testing.T) {
	seedAccount(t, "cus_consent")
	rejects(t, "consent with no timestamp",
		`UPDATE account SET gainshare_consent = TRUE, consented_at = NULL WHERE customer_id = 'cus_consent'`)
	rejects(t, "timestamp with no consent",
		`UPDATE account SET gainshare_consent = FALSE, consented_at = now() WHERE customer_id = 'cus_consent'`)
	accepts(t, "consent with its timestamp",
		`UPDATE account SET gainshare_consent = TRUE, consented_at = now() WHERE customer_id = 'cus_consent'`)
	accepts(t, "revocation clearing the timestamp",
		`UPDATE account SET gainshare_consent = FALSE, consented_at = NULL WHERE customer_id = 'cus_consent'`)
}

// TestUsageRecordCannotDoubleCount is the never-double-count guarantee as a SCHEMA property (task 2.2 /
// design Decision 2): the primary key makes a second charge-bearing row for one
// {customer, period, metric} physically impossible, and the upsert path updates the single row.
func TestUsageRecordCannotDoubleCount(t *testing.T) {
	seedAccount(t, "cus_meter")

	ins := `INSERT INTO usage_record (customer_id, period, metric, quantity, source_digest)
	        VALUES ('cus_meter', '2026-07', 'sum', $1, $2)`
	accepts(t, "first usage record", ins, 2.0, "digest-1")
	rejects(t, "a SECOND row for the same {customer, period, metric}", ins, 2.0, "digest-1")

	// The upsert path — the one RecordUsage uses — updates the one row in place.
	for i := 0; i < 5; i++ {
		accepts(t, "replayed upsert",
			`INSERT INTO usage_record (customer_id, period, metric, quantity, source_digest)
			 VALUES ('cus_meter', '2026-07', 'sum', 2.0, 'digest-1')
			 ON CONFLICT (customer_id, period, metric)
			 DO UPDATE SET quantity = EXCLUDED.quantity, source_digest = EXCLUDED.source_digest`)
	}
	var rows int
	var qty float64
	if err := testDB.QueryRow(
		`SELECT count(*), max(quantity) FROM usage_record
		 WHERE customer_id='cus_meter' AND period='2026-07' AND metric='sum'`).Scan(&rows, &qty); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if rows != 1 {
		t.Errorf("rows after 5 replays = %d, want 1", rows)
	}
	if qty != 2.0 {
		t.Errorf("quantity after 5 replays = %v, want 2 (the period once, not multiplied)", qty)
	}
}

// TestUsageRecordRejectsUnsoundRows: each of these would become an unexplainable invoice line.
func TestUsageRecordRejectsUnsoundRows(t *testing.T) {
	seedAccount(t, "cus_unsound")
	base := `INSERT INTO usage_record (customer_id, period, metric, quantity, source_digest, reported_to_provider, provider_usage_ref)
	         VALUES ($1,$2,$3,$4,$5,$6,$7)`

	rejects(t, "unknown metric", base, "cus_unsound", "2026-07", "bandwidth", 1.0, "d", false, nil)
	rejects(t, "malformed period", base, "cus_unsound", "July 2026", "sum", 1.0, "d", false, nil)
	rejects(t, "negative quantity", base, "cus_unsound", "2026-07", "seats", -1.0, "d", false, nil)
	rejects(t, "empty source digest", base, "cus_unsound", "2026-07", "seats", 1.0, "", false, nil)
	rejects(t, "reported with no provider ref", base, "cus_unsound", "2026-07", "seats", 1.0, "d", true, nil)
	rejects(t, "usage for an unknown customer", base, "cus_ghost", "2026-07", "seats", 1.0, "d", false, nil)

	accepts(t, "all four meters",
		`INSERT INTO usage_record (customer_id, period, metric, quantity, source_digest)
		 VALUES ('cus_unsound','2026-07','sum',1,'d'),
		        ('cus_unsound','2026-07','seats',4,'d'),
		        ('cus_unsound','2026-07','retention',30,'d'),
		        ('cus_unsound','2026-07','eval_compute',128,'d')`)
}

// TestDownMigrationRemovesEverything: a migration that cannot be reversed is a migration nobody can
// deploy with confidence. Run last (zz prefix ordering is not available across files, so it re-applies
// the chain afterwards to leave the database usable for any later run).
func TestZZDownMigrationRemovesEverything(t *testing.T) {
	if err := apply(testDB, "0013_p7_billing_metering.down.sql"); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	for _, tbl := range []string{"account", "usage_record", "billable_savings", "billing_event", "webhook_delivery"} {
		var n int
		// current_schema(): pgtest gives each proof its own schema in ONE database, and
		// information_schema is database-wide. Without the filter this asks "does any schema anywhere
		// still have an `account`" — and internal/billing, internal/launch and internal/proposalstore all
		// create one. It was a race rather than a constant failure: `go test` runs packages concurrently,
		// so this passed when metering happened to reach the down migration before its neighbours applied
		// theirs, and failed when it did not. Adding a seventh package to the proof list was enough to
		// flip it. (telemetry and worktree carry the same filter, for the same reason.)
		if err := testDB.QueryRow(
			`SELECT count(*) FROM information_schema.tables
			  WHERE table_schema = current_schema() AND table_name = $1`, tbl).Scan(&n); err != nil {
			t.Fatalf("information_schema: %v", err)
		}
		if n != 0 {
			t.Errorf("table %s survived the down migration", tbl)
		}
	}
	var n int
	if err := testDB.QueryRow(`SELECT count(*) FROM schema_migrations WHERE id = 13`).Scan(&n); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if n != 0 {
		t.Errorf("the schema_migrations marker for 13 survived the down migration")
	}
	// Re-apply so the database is left in the state the chain describes.
	if err := apply(testDB, "0013_p7_billing_metering.up.sql"); err != nil {
		t.Fatalf("re-apply after down: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// billing_event — never-double-charge and append-only, at the storage layer
// ─────────────────────────────────────────────────────────────────────────────

// TestBillingEventCannotDoubleCharge proves the UNIQUE idempotency key fires (task 3.2 / FR10). This
// is the layer that closes the check-then-insert race application-level dedupe leaves open: two
// concurrent callers can both pass a "does this key exist" check, and only one of them can insert.
func TestBillingEventCannotDoubleCharge(t *testing.T) {
	seedAccount(t, "cus_charge")
	ins := `INSERT INTO billing_event (event_id, customer_id, period, type, kind, idempotency_key, caused_by, quantity)
	        VALUES ($1, 'cus_charge', '2026-07', 'charge', 'metered', $2, 'usage_record:cus_charge/2026-07/sum', 3.0)`

	accepts(t, "first charge", ins, "be_1", "charge_metered:cus_charge:2026-07:sum")
	rejects(t, "a SECOND charge under the same idempotency key", ins, "be_2", "charge_metered:cus_charge:2026-07:sum")

	var n int
	if err := testDB.QueryRow(`SELECT count(*) FROM billing_event WHERE customer_id='cus_charge'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("billing_event rows = %d, want 1", n)
	}
}

// TestBillingEventIsAppendOnly proves design Decision 6 at the storage layer: a correction has no
// choice but to be a new row, because revision and deletion are rejected by the database.
func TestBillingEventIsAppendOnly(t *testing.T) {
	seedAccount(t, "cus_append")
	accepts(t, "the original charge",
		`INSERT INTO billing_event (event_id, customer_id, period, type, kind, idempotency_key, caused_by, quantity)
		 VALUES ('be_ap_1','cus_append','2026-07','charge','metered','charge_metered:cus_append:2026-07:sum',
		         'usage_record:cus_append/2026-07/sum', 9.0)`)

	rejects(t, "DELETE of a billing event",
		`DELETE FROM billing_event WHERE event_id = 'be_ap_1'`)
	rejects(t, "revising the quantity",
		`UPDATE billing_event SET quantity = 1.0 WHERE event_id = 'be_ap_1'`)
	rejects(t, "revising the customer",
		`UPDATE billing_event SET customer_id = 'cus_charge' WHERE event_id = 'be_ap_1'`)
	rejects(t, "revising the idempotency key",
		`UPDATE billing_event SET idempotency_key = 'something_else' WHERE event_id = 'be_ap_1'`)
	rejects(t, "revising what caused it",
		`UPDATE billing_event SET caused_by = 'nothing' WHERE event_id = 'be_ap_1'`)
	rejects(t, "revising the period",
		`UPDATE billing_event SET period = '2026-08' WHERE event_id = 'be_ap_1'`)

	// The ONE permitted transition: completing the write-ahead row with the provider's receipt.
	accepts(t, "settling the write-ahead row with the provider receipt",
		`UPDATE billing_event SET status='recorded', provider_ref='prov_ch_1', amount_ref='prov_amt_1',
		        settled_at=now() WHERE event_id = 'be_ap_1'`)
	rejects(t, "a SECOND provider receipt overwriting the first",
		`UPDATE billing_event SET provider_ref='prov_ch_2', amount_ref='prov_amt_2' WHERE event_id = 'be_ap_1'`)

	var ref string
	if err := testDB.QueryRow(`SELECT provider_ref FROM billing_event WHERE event_id='be_ap_1'`).Scan(&ref); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if ref != "prov_ch_1" {
		t.Errorf("provider_ref = %q, want the first receipt to have survived", ref)
	}

	// A correction is a NEW row, and it is accepted.
	accepts(t, "an additive credit as a new row",
		`INSERT INTO billing_event (event_id, customer_id, period, type, idempotency_key, caused_by, reason, status, provider_ref, settled_at)
		 VALUES ('be_ap_2','cus_append','2026-07','credit','credit:be_ap_1:overcharged',
		         'billing_event:be_ap_1','overcharged: the period was re-derived after a late cost event',
		         'recorded','prov_cr_1', now())`)
}

// TestBillingEventRejectsUnsoundRows: each of these would be a charge nobody can explain at month-end.
func TestBillingEventRejectsUnsoundRows(t *testing.T) {
	seedAccount(t, "cus_bad")
	rejects(t, "unknown event type",
		`INSERT INTO billing_event (event_id, customer_id, type, idempotency_key, caused_by)
		 VALUES ('be_b1','cus_bad','invoice_void','k1','x')`)
	rejects(t, "a resold-token charge kind",
		`INSERT INTO billing_event (event_id, customer_id, type, kind, idempotency_key, caused_by)
		 VALUES ('be_b2','cus_bad','charge','provider_tokens','k2','x')`)
	rejects(t, "a credit with no reason",
		`INSERT INTO billing_event (event_id, customer_id, type, idempotency_key, caused_by, status, provider_ref, settled_at)
		 VALUES ('be_b3','cus_bad','credit','k3','x','recorded','r', now())`)
	rejects(t, "an empty idempotency key",
		`INSERT INTO billing_event (event_id, customer_id, type, idempotency_key, caused_by)
		 VALUES ('be_b4','cus_bad','charge','','x')`)
	rejects(t, "a row that names no cause",
		`INSERT INTO billing_event (event_id, customer_id, type, idempotency_key, caused_by)
		 VALUES ('be_b5','cus_bad','charge','k5','')`)
	rejects(t, "a 'recorded' row with no provider receipt",
		`INSERT INTO billing_event (event_id, customer_id, type, idempotency_key, caused_by, status)
		 VALUES ('be_b6','cus_bad','charge','k6','x','recorded')`)
	rejects(t, "a gainshare charge with no evidence",
		`INSERT INTO billing_event (event_id, customer_id, type, kind, idempotency_key, caused_by)
		 VALUES ('be_b7','cus_bad','gainshare_charge','gainshare','k7','billable_savings:cus_bad/2026-07')`)
	accepts(t, "a gainshare charge that traces to its evidence",
		`INSERT INTO billing_event (event_id, customer_id, period, type, kind, idempotency_key, caused_by, evidence_json)
		 VALUES ('be_b8','cus_bad','2026-07','gainshare_charge','gainshare','charge_gainshare:cus_bad:2026-07',
		         'billable_savings:cus_bad/2026-07', '["verdict:vd-1","merge:abc123"]'::jsonb)`)
}

// TestWebhookDeliveryIsProcessedOnce proves the webhook dedupe key fires (task 4.1 / FR14). The
// PRIMARY KEY is what makes "exactly once" survive two CONCURRENT redeliveries — both would pass a
// check-then-insert in application code, and only one can insert here.
func TestWebhookDeliveryIsProcessedOnce(t *testing.T) {
	ins := `INSERT INTO webhook_delivery (provider_event_id, type) VALUES ($1, $2)`
	accepts(t, "first delivery", ins, "evt_001", "invoice.payment_failed")
	rejects(t, "a redelivery of the same provider event", ins, "evt_001", "invoice.payment_failed")
	rejects(t, "a delivery with no provider event id", ins, "", "invoice.paid")

	// The claim path an application uses: ON CONFLICT DO NOTHING, whose row count is the claim verdict.
	var claimed int64
	res, err := testDB.Exec(ins+` ON CONFLICT (provider_event_id) DO NOTHING`, "evt_001", "invoice.payment_failed")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed, err = res.RowsAffected(); err != nil {
		t.Fatalf("rows affected: %v", err)
	}
	if claimed != 0 {
		t.Errorf("the redelivery claimed the delivery (%d rows) — it would have been processed twice", claimed)
	}

	var n int
	if err := testDB.QueryRow(`SELECT count(*) FROM webhook_delivery WHERE provider_event_id='evt_001'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("webhook_delivery rows = %d, want 1", n)
	}
}

// TestBillableSavingsMustTraceToItsEvidence proves design Decision 8 at the storage layer (task 6.2): a
// non-zero billed saving that cannot name the verification and the merge behind it is not storable.
func TestBillableSavingsMustTraceToItsEvidence(t *testing.T) {
	seedAccount(t, "cus_gain")
	ins := `INSERT INTO billable_savings
	        (customer_id, period, baseline_sum, optimized_sum, savings, verified_delta_refs, merge_commits)
	        VALUES ('cus_gain', $1, $2, $3, $4, $5::jsonb, $6::jsonb)`

	rejects(t, "a billed saving with no evidence at all",
		ins, "2026-07", 100.0, 60.0, 40.0, `[]`, `[]`)
	rejects(t, "a billed saving with verified deltas but no merges",
		ins, "2026-07", 100.0, 60.0, 40.0, `["vd1"]`, `[]`)
	rejects(t, "a billed saving with merges but no verified deltas",
		ins, "2026-07", 100.0, 60.0, 40.0, `[]`, `["abc123"]`)
	rejects(t, "a saving whose arithmetic does not reconstruct",
		ins, "2026-07", 100.0, 60.0, 99.0, `["vd1"]`, `["abc123"]`)
	rejects(t, "a negative saving",
		ins, "2026-07", 60.0, 100.0, -40.0, `["vd1"]`, `["abc123"]`)

	accepts(t, "a verified, merged saving that traces to its evidence",
		ins, "2026-07", 100.0, 60.0, 40.0, `["vd1","vd2"]`, `["abc123","def456"]`)
	// A ZERO saving needs no evidence — there is nothing to justify.
	accepts(t, "a zero saving with no evidence",
		ins, "2026-08", 100.0, 100.0, 0.0, `[]`, `[]`)

	rejects(t, "a SECOND savings row for the same {customer, period}",
		ins, "2026-07", 100.0, 60.0, 40.0, `["vd1"]`, `["abc123"]`)
}
