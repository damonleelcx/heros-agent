//go:build pgproof

// Live-Postgres proof for the consent record (tasks 9.1, 9.3, 9.8 · QA task 12.3).
//
// # 🔴 Why this cannot be a unit test
//
// The idempotency guarantee IS the unique constraint. Asserting it against an in-memory fake asserts a
// property of the fake — and the fake was written by the same person, on the same assumption, in the
// same hour. The claim "a double-clicked button produces one row" is only worth anything against a real
// database applying a real constraint under real concurrency.
//
// The same goes for append-only: the trigger is the guarantee, and a Go map cannot refuse an UPDATE.
//
// It is behind the `pgproof` build tag rather than an env-var skip, following this repository's existing
// rule: a test that quietly skips when its dependency is missing reports green for something it never
// checked. This is either compiled and run by `make pg-proof` — where a missing database is a FAILURE —
// or it is not part of the run at all.
//
//	make pg-proof
//	HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/legal/
package legal

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/pgtest"
	_ "github.com/lib/pq"
)

func migrationPath(name string) string {
	return filepath.Join("..", "..", "db", "migrations", "postgres", name)
}

// applyMigrations runs the REAL .sql files, not an inline copy.
//
// 🔴 `test-fixture-real-schema`: a test that builds its own CREATE TABLE proves the invariants of a
// table that does not ship. The whole point of this file is that the constraints under test are the ones
// the customer's database will have.
func applyMigrations(db *sql.DB) error {
	for _, f := range []string{"0001_p0_lineage.up.sql", "0019_p23_legal_acceptance.up.sql"} {
		sqlBytes, err := os.ReadFile(migrationPath(f))
		if err != nil {
			return fmt.Errorf("read %s: %w", f, err)
		}
		if _, err := db.Exec(string(sqlBytes)); err != nil {
			return fmt.Errorf("apply %s: %w", f, err)
		}
	}
	return nil
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := pgtest.Open("p23legal")
	if err != nil {
		t.Fatalf("open postgres (set %s): %v", pgtest.EnvURL, err)
	}
	if err := applyMigrations(db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func row(tenant, principal, version string) Acceptance {
	return Acceptance{
		TenantID: tenant, PrincipalID: principal,
		DocumentKind: KindTerms, DocumentVersion: version,
		ContentHash: strings.Repeat("a", 64),
		AcceptedAt:  time.Now().UTC().Truncate(time.Microsecond),
		Method:      MethodSignIn,
	}
}

// ── 12.3 · double-submit produces ONE row, under real concurrency ─────────────

func TestDoubleSubmitProducesExactlyOneRow(t *testing.T) {
	db := openDB(t)
	store := NewPGStore(db)
	ctx := context.Background()

	// Twelve concurrent inserts of the same acceptance, each with its own uuid — which is exactly what a
	// double-clicked button behind a retrying proxy looks like. "Check then insert" loses this race; the
	// unique constraint does not.
	const attempts = 12
	var wg sync.WaitGroup
	created := make([]bool, attempts)
	errs := make([]error, attempts)
	start := make(chan struct{})

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := row("tenant-race", "principal-race", "1.0.0")
			a.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
			<-start // release them together, so this is a real race rather than a sequence
			_, c, err := store.Insert(ctx, a)
			created[i], errs[i] = c, err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("attempt %d errored — a repeat must SUCCEED, not error: %v", i, err)
		}
	}

	var rows int
	if err := db.QueryRow(
		`SELECT count(*) FROM legal_acceptance WHERE tenant_id = $1 AND principal_id = $2`,
		"tenant-race", "principal-race").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("%d rows after %d concurrent submits — idempotency is not in the schema", rows, attempts)
	}

	wins := 0
	for _, c := range created {
		if c {
			wins++
		}
	}
	if wins != 1 {
		t.Errorf("%d attempts reported created=true; exactly one insert may claim the row", wins)
	}
}

// ── Append-only, enforced by the store rather than by care ───────────────────

func TestTheRecordRefusesADelete(t *testing.T) {
	db := openDB(t)
	store := NewPGStore(db)
	a := row("tenant-del", "principal-del", "1.0.0")
	a.ID = "00000000-0000-4000-8000-000000000101"
	if _, _, err := store.Insert(context.Background(), a); err != nil {
		t.Fatal(err)
	}

	// 🔴 A consent record is EVIDENCE. Its value in a dispute is precisely that nobody can have removed
	// it, including us.
	_, err := db.Exec(`DELETE FROM legal_acceptance WHERE tenant_id = 'tenant-del'`)
	if err == nil {
		t.Fatal("a DELETE succeeded — the append-only guard is not attached")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("the refusal did not explain itself: %v", err)
	}
}

func TestTheRecordRefusesToRewriteWhatWasAgreed(t *testing.T) {
	db := openDB(t)
	store := NewPGStore(db)
	a := row("tenant-upd", "principal-upd", "1.0.0")
	a.ID = "00000000-0000-4000-8000-000000000102"
	if _, _, err := store.Insert(context.Background(), a); err != nil {
		t.Fatal(err)
	}

	// Changing the hash would make the record cite text the customer never saw. That is the single most
	// damaging edit possible to this table, and it is refused at the database.
	_, err := db.Exec(
		`UPDATE legal_acceptance SET content_hash = $1 WHERE tenant_id = 'tenant-upd'`,
		strings.Repeat("f", 64))
	if err == nil {
		t.Fatal("the content hash was rewritten — the evidence is editable")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("the refusal did not explain itself: %v", err)
	}
}

func TestTheRecordPermitsExactlyTheTwoBookkeepingColumns(t *testing.T) {
	db := openDB(t)
	store := NewPGStore(db)
	a := row("tenant-book", "principal-book", "1.0.0")
	a.ID = "00000000-0000-4000-8000-000000000103"
	if _, _, err := store.Insert(context.Background(), a); err != nil {
		t.Fatal(err)
	}

	// Supersession is bookkeeping, not a rewrite of what was agreed — so it is allowed.
	if _, err := store.MarkSuperseded(context.Background(), KindTerms, "1.1.0", ""); err != nil {
		t.Fatalf("supersession was refused: %v", err)
	}
	// So is the erasure tombstone.
	if _, err := store.EraseSubject(context.Background(), "tenant-book", "principal-book", time.Now().UTC()); err != nil {
		t.Fatalf("erasure was refused: %v", err)
	}
}

// ── 9.8 · erasure keeps the evidence ─────────────────────────────────────────

func TestErasureTombstonesTheSubjectAndTheEvidenceSurvives(t *testing.T) {
	db := openDB(t)
	store := NewPGStore(db)
	ctx := context.Background()
	a := row("tenant-erase", "principal-erase", "1.0.0")
	a.ID = "00000000-0000-4000-8000-000000000104"
	if _, _, err := store.Insert(ctx, a); err != nil {
		t.Fatal(err)
	}

	n, err := store.EraseSubject(ctx, "tenant-erase", "principal-erase", time.Now().UTC())
	if err != nil || n != 1 {
		t.Fatalf("erase: n=%d err=%v", n, err)
	}

	rows, err := store.ListForPrincipal(ctx, "tenant-erase", "principal-erase")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("erasure removed the row; it must TOMBSTONE the subject and keep the evidence (%d rows)", len(rows))
	}
	got := rows[0]
	if !got.Erased() {
		t.Error("the subject was not tombstoned")
	}
	if got.DocumentVersion != "1.0.0" || got.ContentHash != strings.Repeat("a", 64) || got.AcceptedAt.IsZero() {
		t.Errorf("erasure destroyed evidence: %+v", got)
	}
}

// ── The schema refuses to become personal data ───────────────────────────────

func TestThePrincipalIdCannotBeAnEmailAddress(t *testing.T) {
	db := openDB(t)
	// A mis-wired integration passing an email address must fail at the DATABASE, not quietly make this
	// table personal data. This is the constraint doing the job the code review might not.
	_, err := db.Exec(`
INSERT INTO legal_acceptance (id, tenant_id, principal_id, document_kind, document_version, content_hash, method)
VALUES ('00000000-0000-4000-8000-000000000105', 't', 'someone@example.com', 'terms', '1.0.0', $1, 'signin')`,
		strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("an email address was accepted as a principal id")
	}
	if !strings.Contains(err.Error(), "legal_acceptance_principal_is_opaque") {
		t.Errorf("refused for the wrong reason: %v", err)
	}
}

func TestTheClosedVocabulariesAreEnforced(t *testing.T) {
	db := openDB(t)
	for _, tc := range []struct{ name, kind, version, method, constraint string }{
		{"an invented document kind", "marketing", "1.0.0", "signin", "document_kind"},
		{"a non-semver version", "terms", "v1", "signin", "document_version"},
		{"an invented method", "terms", "1.0.0", "telepathy", "method"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Exec(`
INSERT INTO legal_acceptance (id, tenant_id, principal_id, document_kind, document_version, content_hash, method)
VALUES (gen_random_uuid(), 't', 'p', $1, $2, $3, $4)`,
				tc.kind, tc.version, strings.Repeat("a", 64), tc.method)
			if err == nil {
				t.Fatalf("%s was accepted — a typo can invent a value a consumer's switch mishandles", tc.name)
			}
			if !strings.Contains(err.Error(), tc.constraint) {
				t.Errorf("refused for the wrong reason: %v", err)
			}
		})
	}
}

// ── 9.7 · the retention job, against a real trigger ──────────────────────────

func TestRetentionCanDeleteDespiteTheAppendOnlyGuard(t *testing.T) {
	// The append-only trigger refuses DELETE, so the retention job needs the privileged path. This proves
	// the path works — and, by contrast with TestTheRecordRefusesADelete above, that it is the ONLY way.
	db := openDB(t)
	store := NewPGStore(db)
	old := row("tenant-ret", "principal-ret", "1.0.0")
	old.ID = "00000000-0000-4000-8000-000000000106"
	old.AcceptedAt = time.Now().UTC().Add(-10 * 365 * 24 * time.Hour)
	if _, _, err := store.Insert(context.Background(), old); err != nil {
		t.Fatal(err)
	}

	removed, err := store.DeleteOlderThan(context.Background(), time.Now().UTC().Add(-7*365*24*time.Hour))
	if err != nil {
		t.Fatalf("the retention job could not delete: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed %d rows, want 1", removed)
	}
}
