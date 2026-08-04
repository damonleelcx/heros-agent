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

// An idempotency guard must ask about the object it guards, in THIS schema.
//
// 🔴 `pg_constraint`, `pg_class` and `pg_indexes` are DATABASE-WIDE catalogs, and constraint and index
// names are unique per table rather than per database. So
//
//	IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'account_status_known')
//
// is satisfied as soon as ANY schema in the database has a constraint by that name — it asks "does this
// name exist anywhere" and means "does this table have it". internal/pgtest gives every test package its
// own schema in one shared database and calls that isolation structural; a guard like this reaches
// straight through it. The first package to apply the migration creates the constraint, every other
// schema skips it, and `go test` runs packages concurrently — so which one wins changes run to run, and
// any proof asserting that CHECK becomes a coin flip.
//
// It came up tails: internal/deliveryroute's proof that delivery_route refuses a misspelled forge failed
// only when run alongside internal/pgmigrate, and passed alone. 0026 was corrected in place (unreleased)
// and 0024 by migration 0028, which the runner's read-the-ledger design made necessary — editing an
// applied file changes nothing on a database that already ran it.
//
// The fence is textual on purpose. The alternative — asserting the constraints exist, in a proof — is
// the thing this defect already defeated once.
func TestCatalogGuardsAreScopedToTheCurrentSchema(t *testing.T) {
	ms, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// An APPLIED migration's text is not editable — it has to keep describing what actually ran on a
	// customer's database — so a defect in one is repaired by a later migration, and the file keeps the
	// wrong SQL forever. That is the only exemption, and it is not a bare name: the repair must exist and
	// must name what it repairs, which is checked below. An exemption nobody can add without shipping a
	// fix is a different thing from a grandfather clause.
	repairedBy := map[string]string{
		"0024_billing_durable": "0028_repair_account_constraint_guards",
	}
	byName := map[string]string{}
	for _, m := range ms {
		byName[m.Name] = m.SQL
	}
	for broken, repair := range repairedBy {
		sql, ok := byName[repair]
		if !ok {
			t.Errorf("%s is exempted as repaired by %s, which does not exist", broken, repair)
			continue
		}
		if !strings.Contains(sql, broken) {
			t.Errorf("%s is exempted as repaired by %s, but %s never mentions it — an exemption whose "+
				"repair does not name what it repairs is a silence with a citation", broken, repair, repair)
		}
	}

	// A catalog query in a migration is fine; an UNSCOPED one is not. current_schema() is what makes it
	// a question about this database's own schema rather than about every schema in the cluster.
	catalogs := []string{"pg_constraint", "pg_class", "pg_indexes", "pg_index", "pg_attribute"}
	for _, m := range ms {
		if _, exempt := repairedBy[m.Name]; exempt {
			continue
		}
		for _, cat := range catalogs {
			if !strings.Contains(m.SQL, cat) {
				continue
			}
			if strings.Contains(m.SQL, "current_schema()") {
				continue
			}
			t.Errorf("%s queries %s without scoping to current_schema(): the catalog is database-wide, "+
				"so the guard is satisfied by an object of the same name in ANOTHER schema — including "+
				"another test package's. Join through pg_class/pg_namespace and match "+
				"n.nspname = current_schema(), as 0026 and 0028 do.", m.Name, cat)
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
