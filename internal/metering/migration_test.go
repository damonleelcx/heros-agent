package metering

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// migration_test.go is task 8.2's fence: migration 0013 is EXPAND-ONLY, idempotent, and carries no plan
// configuration.
//
// It is a STATIC test on purpose. The live-Postgres proof (schema_pgproof_test.go) needs Docker and
// runs behind a build tag; this one runs unconditionally in `make go`, so a destructive statement or a
// committed price is caught on every PR rather than only when somebody remembers to run the proof. A
// check that can be skipped is a check that eventually is.

const migrationName = "0013_p7_billing_metering"

func migrationSQL(t *testing.T, suffix string) string {
	t.Helper()
	path := filepath.Join("..", "..", "db", "migrations", "postgres", migrationName+"."+suffix+".sql")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(b) < 500 {
		t.Fatalf("%s is only %d bytes — the fence is not seeing the migration", path, len(b))
	}
	return string(b)
}

// stripComments removes `--` comments so a rule stated in prose is not mistaken for a statement.
func stripComments(sql string) string {
	var out []string
	for _, line := range strings.Split(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// TestMigrationIsExpandOnly: the UP migration adds and never removes or rewrites. A destructive
// statement in an up-migration is how a deploy loses a customer's billing history.
func TestMigrationIsExpandOnly(t *testing.T) {
	sql := stripComments(migrationSQL(t, "up"))

	forbidden := map[string]*regexp.Regexp{
		"DROP TABLE":       regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`),
		"DROP COLUMN":      regexp.MustCompile(`(?i)\bDROP\s+COLUMN\b`),
		"ALTER COLUMN":     regexp.MustCompile(`(?i)\bALTER\s+COLUMN\b`),
		"RENAME":           regexp.MustCompile(`(?i)\bRENAME\s+(TO|COLUMN)\b`),
		"TRUNCATE":         regexp.MustCompile(`(?i)\bTRUNCATE\b`),
		"a blanket UPDATE": regexp.MustCompile(`(?i)\bUPDATE\s+\w+\s+SET\b`),
	}
	for name, re := range forbidden {
		if m := re.FindString(sql); m != "" {
			t.Errorf("the up-migration contains %s (%q) — it must be expand-only", name, m)
		}
	}

	// DELETE is checked by scan rather than by regexp: Go's RE2 has no lookahead, and the ONE legitimate
	// delete (the down-migration's own schema_migrations marker) has to be distinguishable from a data
	// delete without pretending a negative lookahead exists.
	deleteFrom := regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+(\w+)`)
	for _, m := range deleteFrom.FindAllStringSubmatch(sql, -1) {
		if !strings.EqualFold(m[1], "schema_migrations") {
			t.Errorf("the up-migration deletes from %s — it must be expand-only", m[1])
		}
	}

	// Every one of the five tables is created, and every CREATE is guarded so a second run is a no-op
	// (the migration-script rule's first requirement, and what lets a new binary self-heal an old DB).
	for _, table := range []string{"account", "usage_record", "billable_savings", "billing_event", "webhook_delivery"} {
		want := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+` + table + `\b`)
		if !want.MatchString(sql) {
			t.Errorf("table %s is not created with an IF NOT EXISTS guard", table)
		}
	}
	// Same reason: scan every CREATE TABLE/INDEX and require the guard, rather than reach for a
	// lookahead the engine does not have.
	creates := regexp.MustCompile(`(?i)CREATE\s+(?:UNIQUE\s+)?(TABLE|INDEX)\s+(\S+)`)
	found := 0
	for _, m := range creates.FindAllStringSubmatch(sql, -1) {
		found++
		if !strings.EqualFold(m[2], "IF") {
			t.Errorf("CREATE %s %s is unguarded — it would fail on a re-run, and every migration must be "+
				"safe to run twice", strings.ToUpper(m[1]), m[2])
		}
	}
	if found == 0 {
		t.Fatal("no CREATE statements found — the guard check would be vacuously true")
	}
	// The marker insert is idempotent too.
	if !strings.Contains(sql, "ON CONFLICT (id) DO NOTHING") {
		t.Error("the schema_migrations marker insert is not idempotent")
	}
}

// TestMigrationCarriesNoPlanConfiguration is design Decision 3 at the migration layer: a plan or price
// change must need NO migration, which is only true if no plan definition ever lives in one.
func TestMigrationCarriesNoPlanConfiguration(t *testing.T) {
	sql := stripComments(migrationSQL(t, "up"))

	// A plan catalog in a migration would look like an INSERT into a plans table, or a priced default.
	forbidden := map[string]*regexp.Regexp{
		"a plan table":            regexp.MustCompile(`(?i)CREATE\s+TABLE[^;]*\bplan(s)?\s*\(`),
		"a seeded plan row":       regexp.MustCompile(`(?i)INSERT\s+INTO\s+plan`),
		"a priced default":        regexp.MustCompile(`(?i)\b(price|amount|rate|fee)[a-z_]*\s+\w+[^,;]*DEFAULT\s+-?\d`),
		"a hardcoded SUM band":    regexp.MustCompile(`(?i)\bsum_band\b`),
		"a hardcoded seat limit":  regexp.MustCompile(`(?i)\bseat_limit\b`),
		"a hardcoded gainshare %": regexp.MustCompile(`(?i)\bgainshare_(rate|percent|pct)\b`),
	}
	for name, re := range forbidden {
		if m := re.FindString(sql); m != "" {
			t.Errorf("the migration contains %s (%q) — plan definitions ship through the CONFIG STORE, "+
				"never a migration (design Decision 3)", name, m)
		}
	}

	// The positive half: the account row points at a plan by ID + CONFIG VERSION, which is the whole
	// mechanism that keeps packaging out of the database.
	for _, want := range []string{"active_plan_id", "plan_config_version"} {
		if !strings.Contains(sql, want) {
			t.Errorf("the account table has no %s — without it, a closed period cannot be explained "+
				"after the next config publish", want)
		}
	}
}

// TestMigrationStoresNoMoney is design Decision 10 at the migration layer: the platform holds provider
// HANDLES, never amounts. An amount never stored is an amount that cannot leak from a row into a log.
func TestMigrationStoresNoMoney(t *testing.T) {
	sql := stripComments(migrationSQL(t, "up"))

	// A money column would be NUMERIC/DECIMAL/MONEY, or a column named for an amount with a numeric type.
	moneyType := regexp.MustCompile(`(?i)\b\w*(amount|price|cost|total|charge)\w*\s+(NUMERIC|DECIMAL|MONEY|BIGINT|INTEGER|DOUBLE\s+PRECISION)`)
	if m := moneyType.FindString(sql); m != "" {
		t.Errorf("the migration declares a money column (%q) — the platform stores provider handles, "+
			"never amounts (design Decision 10)", strings.TrimSpace(m))
	}
	// The refs that stand in for amounts are TEXT handles.
	for _, want := range []string{"amount_ref", "provider_ref", "provider_customer_handle", "provider_usage_ref"} {
		if !strings.Contains(sql, want) {
			t.Errorf("the migration has no %s column — there is then nothing to point at an amount with", want)
		}
	}
}

// TestDownMigrationReversesExactlyWhatUpCreated: a migration nobody can reverse is a migration nobody
// can deploy with confidence, and a down that misses a table leaves the next up unable to run.
func TestDownMigrationReversesExactlyWhatUpCreated(t *testing.T) {
	down := stripComments(migrationSQL(t, "down"))
	for _, table := range []string{"account", "usage_record", "billable_savings", "billing_event", "webhook_delivery"} {
		want := regexp.MustCompile(`(?i)DROP\s+TABLE\s+IF\s+EXISTS\s+` + table + `\b`)
		if !want.MatchString(down) {
			t.Errorf("the down-migration does not drop %s", table)
		}
	}
	if !strings.Contains(down, "DELETE FROM schema_migrations WHERE id = 13") {
		t.Error("the down-migration does not remove its schema_migrations marker")
	}
	// The append-only trigger and its function must go too, or a re-apply hits an existing function.
	for _, want := range []string{"DROP TRIGGER IF EXISTS billing_event_append_only", "DROP FUNCTION IF EXISTS billing_event_reject_mutation"} {
		if !strings.Contains(down, want) {
			t.Errorf("the down-migration does not remove %q", want)
		}
	}
	// Order matters: dependents before the parent, or the FKs block the drop.
	iAccount := strings.Index(down, "DROP TABLE IF EXISTS account")
	for _, dependent := range []string{"usage_record", "billing_event", "billable_savings"} {
		if i := strings.Index(down, "DROP TABLE IF EXISTS "+dependent); i < 0 || i > iAccount {
			t.Errorf("%s is dropped after its parent account — the foreign key would block it", dependent)
		}
	}
}

// TestExpandOnlyFenceGoesRed proves the fence can FAIL. A guard nobody has watched reject anything is
// decoration.
func TestExpandOnlyFenceGoesRed(t *testing.T) {
	cases := map[string]struct {
		sql string
		re  *regexp.Regexp
	}{
		"drop table":     {`DROP TABLE usage_record;`, regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`)},
		"alter column":   {`ALTER TABLE account ALTER COLUMN customer_id TYPE UUID;`, regexp.MustCompile(`(?i)\bALTER\s+COLUMN\b`)},
		"blanket update": {`UPDATE account SET active_plan_id = 'free';`, regexp.MustCompile(`(?i)\bUPDATE\s+\w+\s+SET\b`)},
		"money column":   {`price_cents BIGINT NOT NULL`, regexp.MustCompile(`(?i)\b\w*(amount|price|cost|total|charge)\w*\s+(NUMERIC|DECIMAL|MONEY|BIGINT|INTEGER|DOUBLE\s+PRECISION)`)},
		"seeded plans":   {`INSERT INTO plan (id) VALUES ('team');`, regexp.MustCompile(`(?i)INSERT\s+INTO\s+plan`)},
	}
	for name, c := range cases {
		if !c.re.MatchString(c.sql) {
			t.Errorf("the fence missed %s", name)
		}
	}
	// And it must NOT fire on the legitimate shapes the real migration uses.
	legit := `CREATE TABLE IF NOT EXISTS usage_record (quantity DOUBLE PRECISION NOT NULL, amount_ref TEXT NULL);`
	if regexp.MustCompile(`(?i)\b\w*(amount|price|cost|total|charge)\w*\s+(NUMERIC|DECIMAL|MONEY|BIGINT|INTEGER|DOUBLE\s+PRECISION)`).MatchString(legit) {
		t.Error("the money fence false-positived on an opaque TEXT amount_ref")
	}
}
