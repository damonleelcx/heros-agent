//go:build pgproof

// The live half of the activation-sentinel fence: the Go predicate and the SQL predicate answer
// "is this version serving?" the same way, against a REAL Postgres with the REAL migration applied.
//
// # Why the tag-free half is not enough on its own
//
// `p30_activation_sentinel_test.go` drives `scanVersion` with a fake `Scan`, which proves the mapping
// from a column value to a Go value. It cannot prove what the DATABASE does with that value — that a 0
// is accepted by the column, satisfies `IS NOT NULL`, takes the partial unique index's one active slot,
// and comes back out of `PGVersionStore.Active` as the active row.
//
// That chain is the thing that made the defect invisible: each link is obviously correct and the
// disagreement only exists across all of them. So this test walks the whole chain, on a real database,
// and asserts the two answers at the end.
//
// 🚫 It writes the 0 with raw SQL rather than through `Activate`, deliberately. `Activate` takes a
// clock and every production caller passes `time.Now().UnixMilli()`, so going through it could not
// produce the row under test — and a fence that can only construct the healthy case is not a fence.
// The question is what the READ does with a row the column permits.
package herosagent

import (
	"context"
	"database/sql"
	"testing"
)

// TestOnARealDatabaseTheGoAndSQLAnswersAgree is the live fence.
func TestOnARealDatabaseTheGoAndSQLAnswersAgree(t *testing.T) {
	ctx := context.Background()
	db := agentDB(t, "pgproof_activation_sentinel")
	store, err := NewPGVersionStore(db)
	if err != nil {
		t.Fatal(err)
	}

	d := goodDefinition()
	hash, err := d.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, Version{
		ConfigHash: hash, Definition: d, ModelRef: "claude-opus-5", CredentialRef: "anthropic",
		RehearsalState: RehearsalPassed, CreatedAtMS: 1_700_000_000_000,
	}); err != nil {
		t.Fatal(err)
	}

	// 🔴 STAMP IT AT EPOCH 0, with raw SQL. The column is `BIGINT` and nullable; 0 is a value it
	// accepts, and the CHECK only requires `rehearsal_state = 'passed'`, which this row satisfies.
	if _, err := db.ExecContext(ctx,
		`UPDATE heros_agent_version SET activated_at_ms = 0 WHERE config_hash = $1`, hash); err != nil {
		t.Fatalf("the column refused a 0, which would make this fence unnecessary: %v", err)
	}

	// What the DATABASE says, asked in its own words — the predicate `PGVersionStore.Active` selects on
	// and the one the partial unique index is built over.
	var sqlSaysServing bool
	if err := db.QueryRowContext(ctx,
		`SELECT activated_at_ms IS NOT NULL FROM heros_agent_version WHERE config_hash = $1`,
		hash).Scan(&sqlSaysServing); err != nil {
		t.Fatal(err)
	}
	if !sqlSaysServing {
		t.Fatal("a row stamped 0 does not satisfy `activated_at_ms IS NOT NULL`, so this fence is " +
			"testing a row the defect was never about")
	}

	// What the STORE hands back, through the real read path.
	got, ok, err := store.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("`PGVersionStore.Active` returned nothing for a row its own query matches")
	}
	if got.ConfigHash != hash {
		t.Fatalf("the store returned %s, want %s", got.ConfigHash, hash)
	}

	// 🔴 THE ASSERTION. The store selected this row BECAUSE the SQL predicate was true; the value it
	// returned must not say otherwise.
	if got.Active() != sqlSaysServing {
		t.Errorf("the store returned a row as THE ACTIVE VERSION and the row reports itself as not "+
			"active.\n  SQL (`activated_at_ms IS NOT NULL`): %v\n  Go  (`Version.Active()`):          %v\n\n"+
			"  These are two answers to one question about one row, and every surface downstream picks "+
			"one of them: `/agent` reads the store's `ok` for `serving_config_hash` and reads "+
			"`v.Active()` for the Versions table's `serving` column, so it would show a hash that is "+
			"serving and no row that is.", sqlSaysServing, got.Active())
	}
	if at, has := got.ActivatedAt(); !has || at != 0 {
		t.Errorf("the activation timestamp came back as (%d, %v), want (0, true) — the row says 0 and "+
			"the value must say the same", at, has)
	}

	// And `Get` agrees with `Active` about the same row: two read paths, one answer.
	byHash, ok, err := store.Get(ctx, hash)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if byHash.Active() != got.Active() {
		t.Errorf("`Get` says active=%v and `Active` says active=%v for the same row",
			byHash.Active(), got.Active())
	}
}

// 🔴 THE PARTIAL UNIQUE INDEX AND THE GO PREDICATE AGREE ABOUT "AT MOST ONE".
//
// The index forbids two rows with a non-NULL `activated_at_ms`. If the Go side thought a 0-stamped row
// were inactive, an activation path that stands the previous version down by asking `Active()` would
// leave it stamped — and the next activation would hit the index rather than succeed.
//
// `PGVersionStore.Activate` stands the previous row down with SQL (`WHERE activated_at_ms IS NOT
// NULL`), so it was always safe; this asserts that, and asserts the Go view of the result matches.
func TestActivatingOverAZeroStampedRowStandsItDownOnARealDatabase(t *testing.T) {
	ctx := context.Background()
	db := agentDB(t, "pgproof_activation_standdown")
	store, err := NewPGVersionStore(db)
	if err != nil {
		t.Fatal(err)
	}

	first := goodDefinition()
	firstHash, err := first.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	second := goodDefinition()
	second.Nodes[0].ContextRef = "ctx-v2"
	secondHash, err := second.ConfigHash()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		hash string
		def  Definition
	}{{firstHash, first}, {secondHash, second}} {
		if err := store.Put(ctx, Version{
			ConfigHash: p.hash, Definition: p.def, ModelRef: "claude-opus-5", CredentialRef: "anthropic",
			RehearsalState: RehearsalPassed, CreatedAtMS: 1_700_000_000_000,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE heros_agent_version SET activated_at_ms = 0 WHERE config_hash = $1`, firstHash); err != nil {
		t.Fatal(err)
	}

	// Activating the second must stand the first down — or the partial unique index refuses the write.
	if err := store.Activate(ctx, secondHash, 1_700_000_000_001); err != nil {
		t.Fatalf("activating over a 0-stamped row failed. If this is a unique-index violation, the "+
			"stand-down predicate no longer matches the index's: %v", err)
	}

	var active int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM heros_agent_version WHERE activated_at_ms IS NOT NULL`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("%d row(s) are non-NULL after a second activation; the index permits one", active)
	}
	got, ok, err := store.Active(ctx)
	if err != nil || !ok {
		t.Fatalf("Active: ok=%v err=%v", ok, err)
	}
	if got.ConfigHash != secondHash {
		t.Errorf("%s is serving, want %s", got.ConfigHash, secondHash)
	}
	// And the row that was stood down reports itself that way in Go, not merely in SQL.
	stood, ok, err := store.Get(ctx, firstHash)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if stood.Active() {
		t.Error("the stood-down version still reports itself as active")
	}
	if _, has := stood.ActivatedAt(); has {
		t.Error("the stood-down version still carries an activation timestamp; `SET activated_at_ms = " +
			"NULL` means it has none, and a 0 there would be 1 January 1970 on an operator surface")
	}
	_ = sql.ErrNoRows
}
