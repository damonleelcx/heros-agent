package herosagent

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// p30_activation_sentinel_test.go fences ONE property:
//
//	🔴 THE GO PREDICATE AND THE SQL PREDICATE ANSWER "IS THIS VERSION SERVING?" THE SAME WAY.
//
// # The defect this was written for
//
// The database says NULL means never activated:
//
//	activated_at_ms  BIGINT,          -- "NULL unless active"
//	CREATE UNIQUE INDEX ... ON heros_agent_version ((activated_at_ms IS NOT NULL))
//	                            WHERE activated_at_ms IS NOT NULL;
//
// and `PGVersionStore.Active` selects `WHERE activated_at_ms IS NOT NULL`. The Go side used to say
// `func (v Version) Active() bool { return v.ActivatedAtMS != 0 }`, over a field `scanVersion` filled
// with `activated.Int64` — discarding `activated.Valid`.
//
// So NULL and 0 arrived in Go as the same value, and a row stamped 0 satisfied the SQL predicate while
// failing the Go one. `Active()` returned `(v, true, nil)` for a version whose own `v.Active()` was
// false — the store handing back a row that reports itself as not the thing the store was asked for.
//
// # Why neither half was wrong on its own, which is why it survived review
//
// `activated_at_ms IS NOT NULL` is the right predicate for a nullable column. `ActivatedAtMS != 0` is
// the ordinary Go idiom for an unset int64. Each is correct in isolation and they are only wrong
// TOGETHER — and nothing compared them, because comparing them requires holding both in one test.
//
// # What it cost, and what it would have cost
//
// It could not fire in production: every caller passes `time.Now().UnixMilli()`. It was found by a
// proof binary using a constant-0 clock for determinism, where the rollback step silently did nothing.
//
// Had a 0 ever reached the column, the symptoms would have been four, none of them naming the cause:
// `/agent` showing `serving_config_hash` set while no row in the Versions table said `serving`; the
// serving version offered as a ROLLBACK TARGET (`passed && !v.Active()`); customer-side submissions
// REJECTED as "not the active definition" for the definition that was in fact serving; and
// `MemVersionStore.Activate` failing to stand the row down, because its clearing loop asks `Active()`.
//
// 🚫 The fix is NOT to make the Go predicate `>= 0` or to special-case zero. It is to stop discarding
// the distinction the database already draws — `ActivatedAtMS` is a `*int64`, and nil is the only way
// to say "never activated", exactly as `Abstention.Confidence` and `registry.Envelope.TurnCeiling`
// already do for the same reason.

// scanRow is a `Scan` that hands back a fixed row, so `scanVersion` can be driven with no database.
//
// 🔴 It drives the REAL `scanVersion` rather than reimplementing it. A test that built a `Version`
// itself would assert that the test can build a Version, which is not the question — the question is
// whether the function standing between a database row and a Go value preserves what the row said.
type scanRow struct {
	activated sql.NullInt64
}

func (r scanRow) Scan(dest ...any) error {
	if len(dest) != 8 {
		return fmt.Errorf("scanVersion asked for %d columns; this fake supplies 8. The column list "+
			"changed and this fence is now testing a shape that does not exist", len(dest))
	}
	*(dest[0].(*string)) = "cfg-1"                  // config_hash
	*(dest[1].(*string)) = `{"prompt_ref":"p"}`     // spec_json
	*(dest[2].(*string)) = "claude-opus-5"          // model_ref
	*(dest[3].(*string)) = "anthropic"              // credential_ref
	*(dest[4].(*string)) = string(RehearsalPassed)  // rehearsal_state
	*(dest[5].(*sql.NullString)) = sql.NullString{} // rehearsal_report
	*(dest[6].(*sql.NullInt64)) = r.activated       // activated_at_ms
	*(dest[7].(*int64)) = 1_700_000_000_000         // created_at_ms
	return nil
}

// 🔴 THE FENCE. For every value the column can hold, the Go predicate must agree with the SQL one.
//
// The SQL predicate is `activated_at_ms IS NOT NULL`, which in Go is `sql.NullInt64.Valid`. So the
// assertion is exactly: `scanVersion(row).Active() == row.Valid`, for every row shape.
func TestTheGoAndSQLAnswersToIsThisVersionServingAgree(t *testing.T) {
	for _, c := range []struct {
		name string
		col  sql.NullInt64
	}{
		{"NULL — never activated", sql.NullInt64{}},
		{"a real timestamp", sql.NullInt64{Int64: 1_700_000_000_000, Valid: true}},
		{
			// 🔴 THE ROW THE DEFECT WAS ABOUT. `IS NOT NULL` is TRUE for it, so the database considers
			// this the serving version and the partial unique index has already given it the one active
			// slot. Any Go predicate that disagrees is describing a different row than the one the
			// database returned.
			"epoch 0 — non-NULL, so the DATABASE says this row is serving",
			sql.NullInt64{Int64: 0, Valid: true},
		},
		{"a negative timestamp — nonsense, and still non-NULL", sql.NullInt64{Int64: -1, Valid: true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, err := scanVersion(scanRow{activated: c.col})
			if err != nil {
				t.Fatalf("scanVersion: %v", err)
			}
			// `IS NOT NULL` in SQL is `.Valid` in Go. That equivalence is the whole property.
			sqlSaysServing := c.col.Valid
			if v.Active() != sqlSaysServing {
				t.Errorf("the two halves disagree about the SAME ROW.\n"+
					"  activated_at_ms = %s\n"+
					"  SQL  (`activated_at_ms IS NOT NULL`, and the partial unique index): serving = %v\n"+
					"  Go   (`Version.Active()`):                                          serving = %v\n\n"+
					"  `PGVersionStore.Active` selects on the SQL predicate, so it returns this row as "+
					"THE active version while the value it returns reports itself as not active. "+
					"Downstream that is four different wrong answers — a serving hash with no serving "+
					"row, the live definition offered as a rollback target, customer submissions "+
					"rejected against the definition that is serving, and a previous version never "+
					"stood down — and none of them names this.",
					describe(c.col), sqlSaysServing, v.Active())
			}
		})
	}
}

// 🔴 AND THE TIMESTAMP SURVIVES. A fix that made `Active()` agree by throwing the number away would
// pass the fence above and break `ServingSinceMS`, which is the only thing that answers "since when".
func TestAnActiveVersionStillCarriesWhenItWasActivated(t *testing.T) {
	const at = 1_700_000_000_123
	v, err := scanVersion(scanRow{activated: sql.NullInt64{Int64: at, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Active() {
		t.Fatal("a version activated at a real timestamp does not report itself as active")
	}
	got, ok := v.ActivatedAt()
	if !ok || got != at {
		t.Errorf("the activation timestamp is (%d, %v), want (%d, true). `ServingSinceMS` reads it, "+
			"and a surface that says a definition is serving without saying since when cannot answer "+
			"the first question an incident asks.", got, ok, at)
	}
	// A version that was never activated has no timestamp to give, and says so rather than answering 0.
	never, err := scanVersion(scanRow{activated: sql.NullInt64{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := never.ActivatedAt(); ok {
		t.Error("a version that was never activated reports an activation time. Zero is a real instant " +
			"— 1 January 1970 — and rendering it beside `serving` would be a date somebody could act on")
	}
}

// 🔴 THE IN-MEMORY STORE STANDS THE PREVIOUS VERSION DOWN, whatever it was stamped with.
//
// `MemVersionStore.Activate` clears the previous active row by asking `other.Active()`. Under the old
// predicate a row stamped 0 answered false, so it was never cleared — and the in-memory store, which is
// what most tests run against, would then hold TWO rows the database's partial unique index forbids.
//
// A test passing against a store that permits two is a test passing on behaviour the database refuses,
// which is the exact failure `MemVersionStore.Activate`'s own comment says it exists to prevent.
func TestActivatingStandsDownAPreviousVersionStampedAtEpochZero(t *testing.T) {
	ctx := context.Background()
	store := NewMemVersionStore()

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
		if err := store.Put(ctx, Version{ConfigHash: p.hash, Definition: p.def,
			RehearsalState: RehearsalPassed}); err != nil {
			t.Fatal(err)
		}
	}

	// 🔴 Activated AT EPOCH 0 — the value the database accepts and the old Go predicate read as "not
	// active".
	if err := store.Activate(ctx, firstHash, 0); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := store.Active(ctx); err != nil || !ok || got.ConfigHash != firstHash {
		t.Fatalf("after activating at epoch 0 the store reports ok=%v hash=%q err=%v; the database "+
			"would report this row as serving, because its column is non-NULL", ok, got.ConfigHash, err)
	}

	// Now activate the other one. The first must be stood down.
	if err := store.Activate(ctx, secondHash, 1_700_000_000_000); err != nil {
		t.Fatal(err)
	}
	all, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var active []string
	for _, v := range all {
		if v.Active() {
			active = append(active, v.ConfigHash)
		}
	}
	if len(active) != 1 || active[0] != secondHash {
		t.Errorf("%d version(s) are active after a second activation (%v), want exactly 1 (%s). The "+
			"database's partial unique index makes two impossible; an in-memory store that permits two "+
			"lets a test pass on behaviour the database refuses.",
			len(active), active, secondHash)
	}
}

func describe(c sql.NullInt64) string {
	if !c.Valid {
		return "NULL"
	}
	return fmt.Sprintf("%d", c.Int64)
}
