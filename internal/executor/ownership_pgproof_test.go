//go:build pgproof

// P27 run ownership, against a real database.
//
// Three properties, and the second is the one that has to be written carefully:
//
//   - a run records the organization that created it, and a listing returns only that organization's;
//
//   - the cross-organization probe is issued as TWO DIFFERENT organizations. A version of this test that
//     runs both probes as the same organization passes against an implementation with no isolation at
//     all, which is exactly the trap it exists to avoid;
//
//   - a pre-ownership run (NULL owner) is excluded from every listing and counted separately, so a
//     surface can say "runs exist that predate ownership" instead of showing the empty table it would
//     show a brand-new customer.
//
//     make pg-proof
//     HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/executor/
package executor

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// openOwnershipDB reuses the package's database — TestMain applies the whole embedded set — and clears
// the tables these cases write, so each starts from a known state without a second schema to keep in
// sync with the first.
func openOwnershipDB(t *testing.T) *sql.DB {
	t.Helper()
	for _, table := range []string{"node_execution", "run"} {
		if _, err := testDB.Exec(`DELETE FROM ` + table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
	return testDB
}

// seedConfig writes the lineage `run.config_hash` requires. The foreign keys are 0001's and 0005's and
// predate P27; a test that skipped them would be testing a schema this platform does not have. It reuses
// the package's own `seedLineage` rather than restating the chain, so the two cannot drift.
func seedConfig(t *testing.T, _ *sql.DB, _ string) {
	t.Helper()
	seedLineage(t)
}

func TestARunRecordsItsOwnerAndAListingIsScopedToIt(t *testing.T) {
	db := openOwnershipDB(t)
	store := NewStore(db)
	ctx := context.Background()
	hash := cfgHash
	seedConfig(t, db, hash)

	if err := store.Start(ctx, "run-acme-1", hash, "rev1", 1, "acme"); err != nil {
		t.Fatalf("start acme run: %v", err)
	}
	if err := store.Start(ctx, "run-globex-1", hash, "rev1", 2, "globex"); err != nil {
		t.Fatalf("start globex run: %v", err)
	}

	acme, err := store.ListForTenant(ctx, "acme", 50, time.Time{})
	if err != nil {
		t.Fatalf("list acme: %v", err)
	}
	if len(acme) != 1 || acme[0].RunID != "run-acme-1" {
		t.Fatalf("acme's listing is %+v — it must contain exactly its own run", acme)
	}

	// 🔴 The second probe runs as a DIFFERENT organization. Written as one organization asking twice,
	// this test passes against an implementation with no isolation at all.
	globex, err := store.ListForTenant(ctx, "globex", 50, time.Time{})
	if err != nil {
		t.Fatalf("list globex: %v", err)
	}
	if len(globex) != 1 || globex[0].RunID != "run-globex-1" {
		t.Fatalf("globex's listing is %+v", globex)
	}

	// And the detail read carries the owner, which is what the API's 404 decision is made from.
	rec, err := store.Get(ctx, "run-globex-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.TenantID != "globex" {
		t.Fatalf("the run's owner is %q", rec.TenantID)
	}
}

// TestAPreOwnershipRunIsExcludedFromListingsAndCountedSeparately is D6, at the store.
func TestAPreOwnershipRunIsExcludedFromListingsAndCountedSeparately(t *testing.T) {
	db := openOwnershipDB(t)
	store := NewStore(db)
	ctx := context.Background()
	hash := cfgHash
	seedConfig(t, db, hash)

	// A run created before P27: the column exists, the value was never written.
	if err := store.Start(ctx, "run-legacy", hash, "rev1", 1, ""); err != nil {
		t.Fatalf("start legacy run: %v", err)
	}
	if err := store.Start(ctx, "run-owned", hash, "rev1", 2, "acme"); err != nil {
		t.Fatalf("start owned run: %v", err)
	}

	var owner sql.NullString
	if err := db.QueryRow(`SELECT tenant_id FROM run WHERE run_id='run-legacy'`).Scan(&owner); err != nil {
		t.Fatalf("read legacy owner: %v", err)
	}
	if owner.Valid {
		t.Fatalf("an unspecified owner was stored as %q rather than NULL — a guessed owner on billed "+
			"usage is unfalsifiable after the fact", owner.String)
	}

	list, err := store.ListForTenant(ctx, "acme", 50, time.Time{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].RunID != "run-owned" {
		t.Fatalf("a pre-ownership run appeared in an organization's listing: %+v", list)
	}

	n, err := store.PreOwnedCount(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("pre-ownership count is %d, want 1 — a surface needs this to say \"runs exist that "+
			"predate ownership\" instead of showing the empty table a brand-new customer sees", n)
	}
}

// TestListingRefusesWithoutAnOrganization: "the caller forgot the scope" must never resolve to "here is
// everything". An empty tenant is an error, not a wildcard.
func TestListingRefusesWithoutAnOrganization(t *testing.T) {
	db := openOwnershipDB(t)
	store := NewStore(db)
	if _, err := store.ListForTenant(context.Background(), "", 50, time.Time{}); err == nil {
		t.Fatal("a listing with no organization succeeded")
	}
}

// TestTheListingIsPagedAndOrderedNewestFirst.
func TestTheListingIsPagedAndOrderedNewestFirst(t *testing.T) {
	db := openOwnershipDB(t)
	store := NewStore(db)
	ctx := context.Background()
	hash := cfgHash
	seedConfig(t, db, hash)

	for i := 0; i < 5; i++ {
		if err := store.Start(ctx, "run-"+string(rune('a'+i)), hash, "rev1", int64(i), "acme"); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		// started_at defaults to now(); nudge it so the ordering is deterministic rather than a race.
		if _, err := db.Exec(`UPDATE run SET started_at = now() - ($1 || ' minutes')::interval WHERE run_id = $2`,
			5-i, "run-"+string(rune('a'+i))); err != nil {
			t.Fatalf("stamp %d: %v", i, err)
		}
	}

	page, err := store.ListForTenant(ctx, "acme", 2, time.Time{})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("limit ignored: got %d", len(page))
	}
	if !page[0].StartedAt.After(page[1].StartedAt) {
		t.Errorf("the listing is not newest-first: %v then %v", page[0].StartedAt, page[1].StartedAt)
	}

	// The cursor is a timestamp, not an offset: an offset page shifts under a concurrent write and
	// silently skips or repeats a row.
	next, err := store.ListForTenant(ctx, "acme", 2, page[1].StartedAt)
	if err != nil {
		t.Fatalf("next page: %v", err)
	}
	for _, r := range next {
		if r.RunID == page[0].RunID || r.RunID == page[1].RunID {
			t.Errorf("the second page repeats %s", r.RunID)
		}
	}
}
