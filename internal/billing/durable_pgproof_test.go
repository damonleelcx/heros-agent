//go:build pgproof

// Live-Postgres proof for the three durable billing stores.
//
// # 🔴 Why an in-memory test would have proved nothing here
//
// Every guarantee these stores make is a CONSTRAINT, and a Go map cannot refuse anything. "Two retries
// produce one row" is the UNIQUE index. "A pending row cannot carry a receipt" is
// `billing_event_settled_has_refs`. "A gainshare charge names its evidence" is a CHECK. Asserting those
// against a fake asserts a property of the fake.
//
// This suite also corrected its own author. The claim it was written to demonstrate — that writing a
// pending row's provider_ref as "" instead of NULL would make 0013 refuse every Append — is FALSE.
// Breaking `nullStr` and re-running proved the write still succeeds: for a pending row the constraint's
// right-hand side is `TRUE AND FALSE` either way, because settled_at is NULL. Reasoning about a CHECK is
// not the same as running it, which is the argument for this file existing.
//
//	make pg-proof
//	HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/billing/
package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
)

func durableDB(t *testing.T, schema string) *sql.DB {
	t.Helper()
	db, err := pgtest.Open(schema)
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// The WHOLE embedded set, exactly as a boot does — not a hand-picked subset. Picking files is how
	// the rest of the suite stopped noticing anything past 0009.
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

// seedAccount inserts the account a billing_event's FOREIGN KEY requires.
func seedAccount(t *testing.T, db *sql.DB, customerID string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO account (customer_id, provider_customer_handle, active_plan_id, plan_config_version)
		 VALUES ($1, $2, 'plan_team', 'v1') ON CONFLICT DO NOTHING`,
		customerID, "cus_handle_"+customerID)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
}

func pendingCharge(customerID, key string) BillingEvent {
	return BillingEvent{
		CustomerID: customerID, Period: "2026-07", Type: TypeCharge, Kind: KindMetered,
		IdempotencyKey: key, CausedBy: "usage_record:" + customerID + "/2026-07/sum",
		Quantity: 12.5, CreatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	}
}

// TestAppendWritesAPendingRow: a pending row is accepted and carries no receipt.
func TestAppendWritesAPendingRow(t *testing.T) {
	db := durableDB(t, "billing_durable_append")
	seedAccount(t, db, "cus_a")
	l, err := NewPGLedger(db)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}

	ev, err := l.Append(pendingCharge("cus_a", "charge:cus_a/2026-07"))
	if err != nil {
		t.Fatalf("append a pending charge: %v\n\n"+
			"0013 constrains status against the provider refs and settled_at; this is the write path "+
			"every charge takes.", err)
	}
	if ev.Status != StatusPending {
		t.Errorf("status = %q, want pending", ev.Status)
	}
	if ev.EventID == "" {
		t.Error("Append did not mint an event id")
	}
	if ev.ProviderRef != "" || ev.SettledAt != nil {
		t.Errorf("a pending row came back carrying a receipt: ref=%q settled=%v", ev.ProviderRef, ev.SettledAt)
	}
}

// TestTheUniqueIndexIsTheNeverDoubleChargeGuarantee. The idempotency key is the SAME key handed to the
// provider, so a second row is a second CHARGE — and the database, not this code, is what refuses it.
func TestTheUniqueIndexIsTheNeverDoubleChargeGuarantee(t *testing.T) {
	db := durableDB(t, "billing_durable_dupe")
	seedAccount(t, db, "cus_b")
	l, _ := NewPGLedger(db)

	first, err := l.Append(pendingCharge("cus_b", "charge:cus_b/2026-07"))
	if err != nil {
		t.Fatalf("first append: %v", err)
	}
	second, err := l.Append(pendingCharge("cus_b", "charge:cus_b/2026-07"))
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("err = %v, want ErrDuplicateKey — a retry must not become a second charge", err)
	}
	if second.EventID != first.EventID {
		t.Fatalf("the duplicate path returned event %q, want the EXISTING row %q — the caller hands this "+
			"back to the provider as the original charge", second.EventID, first.EventID)
	}

	rows, err := l.Events("cus_b", "2026-07")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d rows for one idempotency key — that is a double charge", len(rows))
	}
}

// TestSettleCompletesOnceAndOnlyOnce: two recovery sweeps racing one pending row must not both stamp
// it, which would let the second provider call's receipt overwrite the first's.
func TestSettleCompletesOnceAndOnlyOnce(t *testing.T) {
	db := durableDB(t, "billing_durable_settle")
	seedAccount(t, db, "cus_c")
	l, _ := NewPGLedger(db)

	key := "charge:cus_c/2026-07"
	if _, err := l.Append(pendingCharge("cus_c", key)); err != nil {
		t.Fatalf("append: %v", err)
	}
	at := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)

	settled, err := l.Settle(key, "ch_provider_1", "amt_1", at)
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if settled.Status != StatusRecorded || settled.ProviderRef != "ch_provider_1" || settled.SettledAt == nil {
		t.Fatalf("settled row is incomplete: %+v", settled)
	}

	again, err := l.Settle(key, "ch_provider_2", "amt_2", at)
	if !errors.Is(err, ErrAlreadySettled) {
		t.Fatalf("err = %v, want ErrAlreadySettled", err)
	}
	if again.ProviderRef != "ch_provider_1" {
		t.Fatalf("provider_ref = %q — the second receipt overwrote the first", again.ProviderRef)
	}
}

// TestPendingIsTheOutageBuffer.
func TestPendingIsTheOutageBuffer(t *testing.T) {
	db := durableDB(t, "billing_durable_pending")
	seedAccount(t, db, "cus_d")
	l, _ := NewPGLedger(db)

	for _, k := range []string{"charge:cus_d/a", "charge:cus_d/b"} {
		if _, err := l.Append(pendingCharge("cus_d", k)); err != nil {
			t.Fatalf("append %s: %v", k, err)
		}
	}
	if _, err := l.Settle("charge:cus_d/a", "ch_1", "amt_1", time.Now()); err != nil {
		t.Fatalf("settle: %v", err)
	}

	pending, err := l.Pending()
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 || pending[0].IdempotencyKey != "charge:cus_d/b" {
		t.Fatalf("pending = %+v, want only the unsettled row", pending)
	}
}

// TestAGainshareChargeMustNameItsEvidence — 0013's constraint, exercised. A billed saving that cannot
// name its evidence is the confident guessing P7 exists to forbid.
func TestAGainshareChargeMustNameItsEvidence(t *testing.T) {
	db := durableDB(t, "billing_durable_evidence")
	seedAccount(t, db, "cus_e")
	l, _ := NewPGLedger(db)

	bare := BillingEvent{
		CustomerID: "cus_e", Period: "2026-07", Type: TypeGainshareCharge, Kind: KindGainshare,
		IdempotencyKey: "gainshare:cus_e/2026-07", CausedBy: "verified_delta:vd_1", Quantity: 3,
		CreatedAt: time.Now().UTC(),
	}
	if _, err := l.Append(bare); err == nil {
		t.Fatal("a gainshare charge with NO evidence was accepted — the database is supposed to refuse it")
	}

	withEvidence := bare
	withEvidence.Evidence = []string{"vd_1"}
	if _, err := l.Append(withEvidence); err != nil {
		t.Fatalf("a gainshare charge WITH evidence was refused: %v", err)
	}
}

// TestUsageUpsertIsANoOpOnAnIdenticalRederivation. Bumping updated_at on every re-derivation churns the
// reconciler and makes "when did this last change" unanswerable.
func TestUsageUpsertIsANoOpOnAnIdenticalRederivation(t *testing.T) {
	db := durableDB(t, "billing_durable_usage")
	seedAccount(t, db, "cus_f")
	us, err := metering.NewPGUsageStore(db)
	if err != nil {
		t.Fatalf("usage store: %v", err)
	}

	rec := usageRow("cus_f", 100)
	first, changed, err := us.Upsert(rec)
	if err != nil || !changed {
		t.Fatalf("first upsert: changed=%v err=%v", changed, err)
	}

	again, changed, err := us.Upsert(rec)
	if err != nil {
		t.Fatalf("identical re-derivation: %v", err)
	}
	if changed {
		t.Error("an identical re-derivation reported a change")
	}
	if !again.UpdatedAt.Equal(first.UpdatedAt) {
		t.Errorf("updated_at moved on a no-op: %v -> %v", first.UpdatedAt, again.UpdatedAt)
	}

	// A CHANGED quantity un-reports: the provider ref belongs to the quantity that was sent.
	if _, err := us.MarkReported(rec.Key(), "usage_ref_1"); err != nil {
		t.Fatalf("mark reported: %v", err)
	}
	moved := usageRow("cus_f", 250)
	after, changed, err := us.Upsert(moved)
	if err != nil || !changed {
		t.Fatalf("changed upsert: changed=%v err=%v", changed, err)
	}
	if after.ReportedToProvider || after.ProviderUsageRef != "" {
		t.Errorf("a changed quantity kept the old provider hand-off (%v/%q) — that marks a number as "+
			"sent when a different number was sent", after.ReportedToProvider, after.ProviderUsageRef)
	}
}

// usageRow builds a SUM record for the period the other tests use.
func usageRow(customerID string, qty float64) metering.UsageRecord {
	return metering.UsageRecord{
		CustomerID: customerID, Period: "2026-07", Metric: metering.MetricSUM,
		Quantity: qty, SourceDigest: "digest-" + customerID + "-" + itoaF(qty),
		UpdatedAt: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
	}
}

func itoaF(f float64) string { return fmt.Sprintf("%.0f", f) }
