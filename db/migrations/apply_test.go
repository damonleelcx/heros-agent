package migrations

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("HEROS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HEROS_TEST_DATABASE_URL unset")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestConcurrentApplyAllSucceed.
//
// # 🔴 The failure this exists for
//
// Idempotence makes a REPEAT safe; it does not make a CONCURRENT repeat safe, and the two are easy to
// confuse because the migrations read as though they were the same thing. Postgres has no
// `ADD CONSTRAINT IF NOT EXISTS`, so the idiom here is `DROP CONSTRAINT IF EXISTS` then `ADD CONSTRAINT`
// — and two processes interleaving those four statements leaves the second failing with "constraint
// already exists" and refusing to start.
//
// That is what a second replica rolling out does. This was observed as a real failure in the test suite
// before the advisory lock was added.
func TestConcurrentApplyAllSucceed(t *testing.T) {
	db := testDB(t)
	const racers = 8
	var wg sync.WaitGroup
	errs := make([]error, racers)
	start := make(chan struct{})
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // 🔴 released together, so they collide rather than queueing politely
			errs[i] = Apply(context.Background(), db)
		}()
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent migration %d failed: %v", i, err)
		}
	}
}

// TestNoAdvisoryLockSurvivesApply.
//
// # 🔴 The bug this caught, which was worse than the one it was fixing
//
// The first version took a session-scoped `pg_advisory_lock` on a connection from `db.Conn`, and relied
// on `conn.Close()` to release it. `sql.Conn.Close` returns the connection to the POOL — it does not end
// the session — so the lock stayed held by a connection that was now serving unrelated queries, and no
// later migration in the process could ever acquire it. Observed exactly that way in a live database:
// one session blocked on the lock, another idle in the pool holding it, indefinitely.
//
// ⚠️ The first version of THIS test was vacuous. It ran Apply three times and failed if one hung — and
// it passed against the leaking code, because advisory locks are re-entrant within a session and the
// pool handed the second call the same connection that already held it. A test written from the SYMPTOM
// (it hangs) missed the defect; one written from the INVARIANT does not.
//
// The invariant: when Apply returns, nothing holds this lock. `pg_locks` answers that directly, in one
// query, with no timing and no dependence on which connection the pool happened to choose.
func TestNoAdvisoryLockSurvivesApply(t *testing.T) {
	db := testDB(t)
	if err := Apply(context.Background(), db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var held int
	// An advisory lock's bigint key is split across classid (high 32 bits) and objid (low 32); objsubid
	// is 1 for the single-argument form.
	err := db.QueryRow(`
		SELECT count(*) FROM pg_locks
		WHERE locktype = 'advisory'
		  AND ((classid::bigint << 32) | objid::bigint) = $1
		  AND objsubid = 1`, migrationLockKey).Scan(&held)
	if err != nil {
		t.Fatalf("reading pg_locks: %v", err)
	}
	if held != 0 {
		t.Fatalf("%d session(s) still hold the migration lock after Apply returned. It has leaked onto a "+
			"pooled connection, and no later migration in this process — or in any other — can acquire "+
			"it", held)
	}
}
