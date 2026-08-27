package pgmigrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// P36 §3.9 — the definition store's migration: repeatable, idempotency guard named, and existing
// single-node rows read back byte-identically.
//
// 🔴 None of this needs a live Postgres, and that is deliberate — it follows p34_loop_registry_test.go's
// discipline for the same reason: these are the assertions that must hold on EVERY build, because the
// properties they check are the ones whose violation is silent.

const (
	p36Up   = "0052_p36_node_attribution.up.sql"
	p36Down = "0052_p36_node_attribution.down.sql"
)

// p36SQL returns the migration's STATEMENTS, with `--` comments stripped.
//
// Stripping is load-bearing, not tidying: this file's header quotes `spec_json` and `heros_agent_version`
// several times explaining why they are NOT touched, and a scan over the raw bytes reports every one of
// those as a violation. A fence that fires on a file's prose gets read as broken and then deleted,
// taking the real check with it.
func p36SQL(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	stripped := strings.Join(out, "\n")
	if strings.TrimSpace(stripped) == "" {
		t.Fatalf("%s is entirely comments; every fence below would pass on an empty file", name)
	}
	return stripped
}

// 🔴 REPEATABLE: every statement class carries a guard, so a second run returns success and changes
// nothing.
func TestP36MigrationIsRepeatable(t *testing.T) {
	up := p36SQL(t, p36Up)

	if !strings.Contains(up, "ADD COLUMN IF NOT EXISTS") {
		t.Error("the ALTER has no `IF NOT EXISTS` guard. A migration that fails on its second run is a " +
			"migration an operator cannot retry after a partial apply — and a partial apply is exactly " +
			"when they need to.")
	}
	if !strings.Contains(up, "ON CONFLICT (id) DO NOTHING") {
		t.Error("the schema_migrations marker has no ON CONFLICT guard, so a second run fails on the " +
			"primary key after the DDL has already succeeded")
	}
	// Every statement that CAN carry a guard must. A bare CREATE/ALTER slipping in later is what this
	// catches, and the guard names are the ones the commit body has to cite.
	for _, unguarded := range []string{"\nCREATE TABLE heros", "\nCREATE INDEX ", "\nALTER TABLE heros_inference\n    ADD COLUMN nodes"} {
		if strings.Contains(up, unguarded) {
			t.Errorf("an unguarded statement %q is not re-runnable", strings.TrimSpace(unguarded))
		}
	}
	down := p36SQL(t, p36Down)
	if !strings.Contains(down, "DROP COLUMN IF EXISTS") {
		t.Error("the down migration is not re-runnable either")
	}
}

// 🔴 THE DEFINITION STORE IS NOT REWRITTEN — task 3.9's real content, and decisions.md D-36.0's whole
// point.
//
// P36 changes the SHAPE of a definition, and the obvious migration is to rewrite every stored
// `spec_json` into the new nested form. That rewrite changes every definition's `config_hash`, which
// orphans every pinned inference filed under the old one. Nothing errors when that happens.
//
// It is also unnecessary: a single-node definition marshals to the pre-P36 bytes exactly, so an existing
// row is read back byte-identically by the new binary with NO migration at all.
func TestP36MigrationDoesNotRewriteStoredDefinitions(t *testing.T) {
	for _, name := range []string{p36Up, p36Down} {
		sql := p36SQL(t, name)
		for _, forbidden := range []string{
			"heros_agent_version", "spec_json", "config_hash",
		} {
			if strings.Contains(sql, forbidden) {
				t.Errorf("%s touches %q. Rewriting a stored definition changes its config_hash, which "+
					"orphans every inference pinned under the old one — the ADR-014 chain arriving through "+
					"the database instead of the seal path. decisions.md D-36.0 establishes that no such "+
					"rewrite is needed: the compatibility encoding reproduces the pre-P36 bytes.",
					name, forbidden)
			}
		}
		// And no UPDATE of the inference table's existing content either: a backfill of `nodes_json`
		// would synthesise a measurement nobody took.
		if strings.Contains(strings.ToUpper(sql), "UPDATE HEROS_INFERENCE") {
			t.Errorf("%s backfills nodes_json. NULL means NOT RECORDED; a synthesised entry would report "+
				"zero calls, zero tokens and zero latency as MEASUREMENTS — the same substitution "+
				"`unpriced` must not render as `0`, one table over.", name)
		}
	}
}

// 🔴 The new column is NULLABLE, and it is stated in the file rather than left to a default.
func TestP36NodeAttributionColumnIsNullable(t *testing.T) {
	up := p36SQL(t, p36Up)
	if !strings.Contains(up, "nodes_json JSONB") {
		t.Fatalf("the column is not declared as `nodes_json JSONB`:\n%s", up)
	}
	if strings.Contains(up, "nodes_json JSONB NOT NULL") {
		t.Error("`nodes_json` is NOT NULL. Every row written before this column existed has no per-node " +
			"record and never will — nobody observed which node produced those edges. NOT NULL forces a " +
			"backfill, and a backfilled entry reports a measurement nobody took.")
	}
	if !strings.Contains(up, "DEFAULT") {
		return // no default at all is the correct shape
	}
	t.Error("`nodes_json` carries a DEFAULT. A default is a backfill by another name: it makes every " +
		"existing row claim a per-node record it does not have.")
}

// 🔴 The migration's id and name are unique and follow the sequence. A duplicate id is invisible until
// two migrations silently mark each other applied.
func TestP36MigrationIdIsUniqueAcrossTheRegistry(t *testing.T) {
	dir := filepath.Join("..", "..", "db", "migrations", "postgres")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	var checked int
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		const marker = "INSERT INTO schema_migrations (id, name) VALUES ("
		i := strings.Index(string(b), marker)
		if i < 0 {
			continue
		}
		rest := string(b)[i+len(marker):]
		id := strings.TrimSpace(strings.SplitN(rest, ",", 2)[0])
		checked++
		if prev, dup := seen[id]; dup {
			t.Errorf("migration id %s is claimed by both %s and %s — whichever runs second marks itself "+
				"applied and does nothing, silently", id, prev, e.Name())
		}
		seen[id] = e.Name()
	}
	if checked < 40 {
		t.Errorf("only %d migration(s) were inspected — the scan is not reaching the directory, so its "+
			"clean report means nothing", checked)
	}
	if seen["52"] != p36Up {
		t.Errorf("migration id 52 is claimed by %q, want %q", seen["52"], p36Up)
	}
}
