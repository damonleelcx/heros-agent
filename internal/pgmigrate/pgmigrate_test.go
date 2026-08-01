package pgmigrate

import (
	"strings"
	"testing"
)

// The tests here run without a database on purpose. What can go wrong in this package WITHOUT a server
// — an unordered set, a duplicate id, a migration that never records itself — is exactly what a live
// test would not catch until the tenth migration or the second boot. The against-Postgres half is the
// `pgproof` tag's job (internal/pgtest), and internal/legal's store_pgproof_test.go is its shape.

func TestLoadReturnsEveryMigrationInNumericOrder(t *testing.T) {
	ms, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(ms) == 0 {
		t.Fatal("Load returned no migrations — the embed did not reach db/migrations/postgres, and a " +
			"runner with an empty set applies nothing while reporting success")
	}
	for i, m := range ms {
		if i > 0 && ms[i-1].ID >= m.ID {
			t.Fatalf("migrations out of order at %d: %s (id %d) follows %s (id %d)",
				i, m.Name, m.ID, ms[i-1].Name, ms[i-1].ID)
		}
		if strings.TrimSpace(m.SQL) == "" {
			t.Fatalf("%s embedded empty", m.Name)
		}
	}
	// The order is NUMERIC, not lexical. A string sort puts 0010 before 0002 — latent until the tenth
	// migration, and then it applies a schema in an order nobody reviewed.
	if ms[0].ID != 1 {
		t.Fatalf("first migration is id %d (%s), want 1", ms[0].ID, ms[0].Name)
	}
}

// Every migration must record itself in the ledger it is decided by. Apply verifies this at runtime and
// fails the boot; this fails the build instead, which is the cheaper of the two places to find out.
func TestEveryMigrationRecordsItselfInTheLedger(t *testing.T) {
	ms, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, m := range ms {
		if !strings.Contains(m.SQL, "INSERT INTO schema_migrations") {
			t.Errorf("%s does not INSERT INTO schema_migrations: the runner reads that ledger to decide "+
				"what to apply, so this migration would be re-applied on every boot — and its DDL is bare "+
				"CREATE TABLE, so the second boot would fail rather than no-op", m.Name)
		}
	}
}

// Each migration carries its own transaction. The runner executes a file as one batch and does not add
// one, so a file without BEGIN/COMMIT would apply half a schema and leave it there.
func TestEveryMigrationIsSelfTransactional(t *testing.T) {
	ms, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, m := range ms {
		if !strings.Contains(m.SQL, "BEGIN;") || !strings.Contains(m.SQL, "COMMIT;") {
			t.Errorf("%s is not wrapped in BEGIN;/COMMIT; — the runner executes the file as one batch and "+
				"adds no transaction of its own, so a failure part-way would leave a half-applied schema", m.Name)
		}
	}
}

// The `.down.sql` files must NOT be reachable from the deployed binary. Rollback is re-applying the
// prior package (Decision 7); a binary that can drop the customer's tables on some code path is a
// binary that eventually does.
func TestDownMigrationsAreNotEmbedded(t *testing.T) {
	ms, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, m := range ms {
		if strings.Contains(m.Name, "down") {
			t.Errorf("%s looks like a down migration and is embedded", m.Name)
		}
		if strings.Contains(m.SQL, "DROP TABLE") && !strings.Contains(m.SQL, "DROP TABLE IF EXISTS") {
			t.Errorf("%s contains an unconditional DROP TABLE — up migrations do not drop", m.Name)
		}
	}
}

func TestFreshInstallDistinguishesAnEmptyDatabaseFromACurrentOne(t *testing.T) {
	// A fresh install applied everything and found nothing; a current database applied nothing. Both are
	// success, and the boot log says different things about them — so the distinction is worth a test.
	if !(Result{Applied: []string{"0001_x"}}).FreshInstall() {
		t.Error("a run that applied migrations to an empty ledger is a fresh install")
	}
	if (Result{Already: []string{"0001_x"}}).FreshInstall() {
		t.Error("a run that applied nothing to an existing ledger is not a fresh install")
	}
	if (Result{Applied: []string{"0002_y"}, Already: []string{"0001_x"}}).FreshInstall() {
		t.Error("an upgrade over an existing ledger is not a fresh install")
	}
	if (Result{}).FreshInstall() {
		t.Error("an empty result is not a fresh install")
	}
}
