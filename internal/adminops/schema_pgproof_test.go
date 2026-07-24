//go:build pgproof

// Live-Postgres proof of the P8 schema (migration 0014): the constraints that make admin-identity
// separation, append-only role grants, short-lived revocable sessions, and — the load-bearing one —
// the tamper-evident, no-mutate-path audit log true BY CONSTRUCTION rather than by application care.
//
// It runs the REAL migration chain against a REAL server, applying 0001 then 0014. No inlined CREATE
// TABLE anywhere: a test that builds its own approximation of the production schema proves only that
// the approximation behaves, which is exactly how a missing constraint reaches production with CI green.
//
// Every assertion is of the form "the database REJECTS this" or "this survives a re-run": a constraint
// nobody has watched reject anything is a constraint that may not exist.
package adminops_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/heros-foreal/agentd/internal/pgtest"
)

var testDB *sql.DB

// chain is the ordered migration chain P8 needs: the lineage bookkeeping table (0001) then P8 (0014).
// 0014 depends only on schema_migrations, which 0001 creates — P8 owns its own tables and reaches
// everything else through the subsystem that owns it, so it does not need the P2–P7 chain.
var chain = []string{"0001_p0_lineage.up.sql", "0014_p8_admin_console.up.sql"}

func TestMain(m *testing.M) {
	db, err := pgtest.Open("proof_p8_admin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testDB = db
	for _, f := range chain {
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

// TestMigrationIsIdempotent: the second run must succeed — what lets a new binary self-heal an older
// database.
func TestMigrationIsIdempotent(t *testing.T) {
	if err := apply(testDB, "0014_p8_admin_console.up.sql"); err != nil {
		t.Fatalf("re-applying 0014 must be a no-op, got: %v", err)
	}
	var n int
	if err := testDB.QueryRow(`SELECT count(*) FROM schema_migrations WHERE id = 14`).Scan(&n); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if n != 1 {
		t.Errorf("schema_migrations rows for id=14 = %d, want 1", n)
	}
}

// TestAuditEntryRefusesMutation is the load-bearing one: the database itself refuses UPDATE and DELETE
// on audit_entry, so there is no mutate/delete path for any role including the most privileged (FR15).
func TestAuditEntryRefusesMutation(t *testing.T) {
	if _, err := testDB.Exec(`INSERT INTO audit_entry (seq, prev_hash, entry_hash, actor_admin_id, target, action, result)
		VALUES (1, 'genesis', 'h1', 'adm-1', 'tenant:acme', 'admin.tenant.suspend', 'applied')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := testDB.Exec(`UPDATE audit_entry SET reason = 'rewritten' WHERE seq = 1`); err == nil {
		t.Fatal("the database allowed an UPDATE on audit_entry — a mutate path exists")
	}
	if _, err := testDB.Exec(`DELETE FROM audit_entry WHERE seq = 1`); err == nil {
		t.Fatal("the database allowed a DELETE on audit_entry — a delete path exists")
	}
	// The row is still there, unaltered — the write-ahead entry had no reason, and it still has none.
	var reason sql.NullString
	if err := testDB.QueryRow(`SELECT reason FROM audit_entry WHERE seq = 1`).Scan(&reason); err != nil {
		t.Fatalf("select: %v", err)
	}
	if reason.Valid && reason.String == "rewritten" {
		t.Error("the refused UPDATE nonetheless altered the row")
	}
}

// TestRoleGrantIsAppendOnly: a grant row cannot be updated or deleted (FR5).
func TestRoleGrantIsAppendOnly(t *testing.T) {
	if _, err := testDB.Exec(`INSERT INTO admin_principal (admin_id, sso_subject, status) VALUES ('adm-2', 'sso|adm-2', 'active')`); err != nil {
		t.Fatalf("insert principal: %v", err)
	}
	if _, err := testDB.Exec(`INSERT INTO admin_role_grant (grant_id, admin_id, role, action, granted_by, reason, granted_at)
		VALUES ('g1', 'adm-2', 'support', 'grant', 'adm-super', 'onboarding', now())`); err != nil {
		t.Fatalf("insert grant: %v", err)
	}
	if _, err := testDB.Exec(`UPDATE admin_role_grant SET role = 'superadmin' WHERE grant_id = 'g1'`); err == nil {
		t.Fatal("a role grant was mutable — a grant must be withdrawn by a new revoke row, never edited")
	}
	if _, err := testDB.Exec(`DELETE FROM admin_role_grant WHERE grant_id = 'g1'`); err == nil {
		t.Fatal("a role grant was deletable — the grant history must survive")
	}
}

// TestSessionTTLConstraint: a session with expires_at at or before issued_at is rejected — a
// "short-lived" session cannot be created already-expired-backwards (FR2).
func TestSessionTTLConstraint(t *testing.T) {
	if _, err := testDB.Exec(`INSERT INTO admin_principal (admin_id, sso_subject, status) VALUES ('adm-3', 'sso|adm-3', 'active')`); err != nil {
		t.Fatalf("insert principal: %v", err)
	}
	if _, err := testDB.Exec(`INSERT INTO admin_session (session_id, admin_id, issued_at, expires_at)
		VALUES ('s1', 'adm-3', now(), now())`); err == nil {
		t.Fatal("a session with no positive TTL was accepted")
	}
}

// TestGrantActionTimeConstraint: a grant row must carry granted_at (not revoked_at) and vice versa.
func TestGrantActionTimeConstraint(t *testing.T) {
	if _, err := testDB.Exec(`INSERT INTO admin_principal (admin_id, sso_subject, status) VALUES ('adm-4', 'sso|adm-4', 'active')`); err != nil {
		t.Fatalf("insert principal: %v", err)
	}
	// A 'grant' with revoked_at set is incoherent and rejected.
	if _, err := testDB.Exec(`INSERT INTO admin_role_grant (grant_id, admin_id, role, action, granted_by, reason, revoked_at)
		VALUES ('g2', 'adm-4', 'support', 'grant', 'adm-super', 'x', now())`); err == nil {
		t.Fatal("a 'grant' row with revoked_at and no granted_at was accepted")
	}
}

// TestDownMigrationRemovesTheP8TablesOnly: the reversal drops the eight P8 tables and leaves the
// lineage bookkeeping it did not create.
func TestDownMigrationRemovesTheP8TablesOnly(t *testing.T) {
	if err := apply(testDB, "0014_p8_admin_console.down.sql"); err != nil {
		t.Fatalf("down migration: %v", err)
	}
	for _, table := range []string{"admin_principal", "audit_entry", "kill_switch_state", "gdpr_request"} {
		var exists bool
		if err := testDB.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if exists {
			t.Errorf("%s survived the down migration", table)
		}
	}
	// schema_migrations — which 0001 owns — remains.
	var exists bool
	if err := testDB.QueryRow(`SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("check schema_migrations: %v", err)
	}
	if !exists {
		t.Error("the down migration removed schema_migrations, which it does not own")
	}
	// Re-apply so a later run of this suite starts clean.
	if err := apply(testDB, "0014_p8_admin_console.up.sql"); err != nil {
		t.Fatalf("re-apply after down: %v", err)
	}
}
