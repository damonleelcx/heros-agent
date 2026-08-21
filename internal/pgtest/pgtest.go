// Package pgtest wires a live-Postgres proof to its own isolated schema.
//
// It exists only for the `pgproof`-tagged tests (internal/registry, internal/variantspec); nothing in
// agentd imports it. It is a normal package rather than a _test.go helper because two different test
// packages need the same wiring, and duplicating it is how the two quietly drift apart.
//
// # Why each proof needs its own schema
//
// The proofs share one server — one Docker container locally, one service container in CI — and
// `go test` runs packages CONCURRENTLY. Pointed at the same schema they race: both apply
// 0001_p0_lineage, whose DDL is bare `CREATE TABLE` (not `IF NOT EXISTS`), so the loser dies with
// `42P07: relation "workflow" already exists`. Worse, they would see each other's rows, and a proof
// that "the registry contains exactly one row" is worthless when a neighbour is inserting too.
//
// A schema per package makes the isolation structural instead of a rule about test naming.
package pgtest

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

// EnvURL is the connection string a proof runs against. `make pg-proof` sets it from an ephemeral
// container; CI sets it from the db-proof job's Postgres service.
const EnvURL = "HEROS_TEST_POSTGRES_URL"

// Open returns a pool bound to its own schema, creating the schema if needed.
//
// It FAILS rather than skips when the database is unreachable. A proof that skips itself when its
// dependency is missing reports green for something it never checked — the failure mode this whole
// tag exists to avoid — so every error here is fatal to the run.
//
// The schema is bound via the DSN, never by `Exec("SET search_path …")` after opening. search_path is
// PER-CONNECTION state: an Exec sets it on whichever pooled connection happens to serve that call,
// and every other connection in the pool keeps the default. The result is a suite that passes or
// fails depending on pool scheduling, writing half its rows into `public`. Putting it in the DSN
// means libpq applies it to every connection the pool ever opens.
func Open(schema string) (*sql.DB, error) {
	raw := os.Getenv(EnvURL)
	if raw == "" {
		return nil, fmt.Errorf("%s is not set: this proof requires a live Postgres — run `make pg-proof`", EnvURL)
	}
	if !validSchemaName(schema) {
		return nil, fmt.Errorf("pgtest: %q is not a usable schema name", schema)
	}

	// A first, unbound connection just to create the schema — it cannot be bound to a schema that
	// does not exist yet.
	admin, err := sql.Open("postgres", raw)
	if err != nil {
		return nil, fmt.Errorf("pgtest: open %s: %w", EnvURL, err)
	}
	defer func() { _ = admin.Close() }()
	if err := admin.Ping(); err != nil {
		return nil, fmt.Errorf("pgtest: cannot reach Postgres at %s: %w", EnvURL, err)
	}
	// Dropped and recreated so a re-run starts from a known-empty schema rather than inheriting rows
	// from the last one — `-count=1` reruns and a shared CI service both depend on this.
	if _, err := admin.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`); err != nil {
		return nil, fmt.Errorf("pgtest: drop schema %s: %w", schema, err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		return nil, fmt.Errorf("pgtest: create schema %s: %w", schema, err)
	}

	bound, err := withSearchPath(raw, schema)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", bound)
	if err != nil {
		return nil, fmt.Errorf("pgtest: open bound: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pgtest: ping bound: %w", err)
	}

	// Loud post-open check: prove the DSN actually bound, rather than trusting it. A silently
	// unbound pool would run the whole proof against `public` and still pass — the tests would be
	// green and testing the wrong schema.
	var got string
	if err := db.QueryRow(`SELECT current_schema()`).Scan(&got); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pgtest: read current_schema: %w", err)
	}
	if got != schema {
		_ = db.Close()
		return nil, fmt.Errorf("pgtest: search_path did not bind: current_schema() is %q, want %q", got, schema)
	}
	return db, nil
}

// withSearchPath adds `options=-c search_path=<schema>` to a libpq URL, preserving what is there.
func withSearchPath(raw, schema string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("pgtest: parse %s: %w", EnvURL, err)
	}
	q := u.Query()
	q.Set("options", "-c search_path="+schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// validSchemaName keeps the identifier interpolation above safe. Schema names are package-chosen
// constants, never user input, but an identifier cannot be a bound parameter, so the check is here
// rather than left implicit.
func validSchemaName(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return !strings.HasPrefix(s, "pg_")
}

// Clear empties the named tables and every table that has a foreign key into them, in an order no
// constraint can refuse.
//
// # 🔴 Why the order is DERIVED and not written down
//
// Three suites in this repository cleared the identity domain from a hand-written list, and all three
// lists fell behind migration 0041 (P28), which gave `platform_user` two new children — `user_password`
// and `identity_token`. Neither appeared in any list.
//
// Only ONE of the three went red, and that is the part worth understanding. `internal/tenancy` writes a
// password row, so its `DELETE FROM platform_user` hit the foreign key and failed loudly. The other two
// do not write one YET: their lists were equally wrong and equally silent, and they would have gone red
// on the day somebody added a password to a fixture — a failure with no apparent connection to the
// change that triggered it.
//
// The lists were not in the wrong ORDER. Each was correctly children-first for the tables it knew
// about. They were INCOMPLETE, and a list that must be extended whenever a migration adds a child is a
// list that will be incomplete again. Editing the three by hand fixes today's instance and re-arms the
// mechanism.
//
// So the graph is read from `pg_constraint` at run time — the same catalog the constraint itself lives
// in. A migration that adds a child is picked up with nothing to remember.
//
// # What it deletes, which is MORE than what it was asked for
//
// Every FK descendant of the named tables, transitively. That is not a widening — it is what the
// callers already meant. A `DELETE FROM platform_user` cannot succeed while a row references it, so
// "clear the identity domain" has always implied its children; the lists were an attempt to enumerate
// them. The expansion is reported by `ClearedTables` so a caller can assert what a reset actually
// touched rather than assume.
//
// # 🚫 Why not TRUNCATE ... CASCADE
//
// It would handle the order and the cycles for free, and it is refused for two reasons. Several tables
// in this schema carry append-only guards that reject TRUNCATE by design (`model_entry`, `delivery`,
// `billing_event`, `audit_entry`) — a reset helper that works everywhere except the tables somebody
// deliberately protected is a helper with a surprise in it. And CASCADE reaches tables the caller never
// named and cannot see, which is exactly the silence this function exists to end.
func Clear(t *testing.T, db *sql.DB, tables ...string) {
	t.Helper()
	ordered, err := ClearOrder(db, tables...)
	if err != nil {
		t.Fatalf("pgtest: %v", err)
	}
	for _, table := range ordered {
		if _, err := db.Exec(`DELETE FROM ` + quoteIdent(table)); err != nil {
			t.Fatalf("pgtest: clear %s (order: %v): %v", table, ordered, err)
		}
	}
}

// ClearedTables reports what Clear would delete, in the order it would delete it.
//
// Exported so a suite can ASSERT the expansion rather than trust it — `internal/pgtest`'s own test uses
// it to prove that clearing `platform_user` reaches `user_password`, which is the specific omission
// that produced this function.
func ClearedTables(db *sql.DB, tables ...string) ([]string, error) { return ClearOrder(db, tables...) }

// ClearOrder computes the delete order: every requested table plus its transitive FK descendants,
// children before parents.
func ClearOrder(db *sql.DB, tables ...string) ([]string, error) {
	if len(tables) == 0 {
		return nil, fmt.Errorf("Clear was called with no tables")
	}
	childrenOf, err := foreignKeyChildren(db)
	if err != nil {
		return nil, err
	}

	// The transitive closure of "things that reference this", starting from what was asked for.
	want := map[string]bool{}
	var expand func(string)
	expand = func(table string) {
		if want[table] {
			return
		}
		want[table] = true
		for _, child := range childrenOf[table] {
			expand(child)
		}
	}
	for _, table := range tables {
		expand(table)
	}

	// Depth-first post-order over the FK edges gives children before parents. `visiting` catches a real
	// cycle; a SELF-edge is skipped rather than reported, because a single DELETE clears the whole
	// table regardless of a row referencing another row in it (`legal_acceptance` supersedes itself
	// this way, and it is not a problem to solve here).
	var out []string
	done, visiting := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(table string) error {
		if done[table] {
			return nil
		}
		if visiting[table] {
			return fmt.Errorf("the foreign keys among %v form a cycle through %q, so no delete order "+
				"exists. Break it with a deferrable constraint, or clear the cycle's tables explicitly",
				tables, table)
		}
		visiting[table] = true
		for _, child := range childrenOf[table] {
			if child == table || !want[child] {
				continue
			}
			if err := visit(child); err != nil {
				return err
			}
		}
		visiting[table] = false
		done[table] = true
		out = append(out, table)
		return nil
	}
	// Sorted, so the order is stable between runs and a failure message names the same sequence twice.
	roots := make([]string, 0, len(want))
	for table := range want {
		roots = append(roots, table)
	}
	sort.Strings(roots)
	for _, table := range roots {
		if err := visit(table); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// foreignKeyChildren maps each table to the tables holding a foreign key into it, in THIS schema.
//
// Scoped to `current_schema()` deliberately: the proofs share one server and one schema per package,
// and a catalog query without the scope would return another package's tables — which is the same
// cross-suite bleed the per-schema binding exists to prevent.
func foreignKeyChildren(db *sql.DB) (map[string][]string, error) {
	rows, err := db.Query(`
		SELECT c.relname AS child, p.relname AS parent
		  FROM pg_constraint con
		  JOIN pg_class     c  ON c.oid  = con.conrelid
		  JOIN pg_class     p  ON p.oid  = con.confrelid
		  JOIN pg_namespace ns ON ns.oid = c.relnamespace
		 WHERE con.contype = 'f'
		   AND ns.nspname = current_schema()`)
	if err != nil {
		return nil, fmt.Errorf("read foreign keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string][]string{}
	seen := map[[2]string]bool{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, fmt.Errorf("scan foreign key: %w", err)
		}
		// A table may reference another through several constraints; one edge is enough.
		if seen[[2]string{child, parent}] {
			continue
		}
		seen[[2]string{child, parent}] = true
		out[parent] = append(out[parent], child)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read foreign keys: %w", err)
	}
	for parent := range out {
		sort.Strings(out[parent])
	}
	return out, nil
}

// quoteIdent quotes a table name for interpolation.
//
// The names are package-chosen constants and never user input, but an identifier cannot be a bound
// parameter — so the quoting is here rather than left implicit, and a table named like a keyword works.
func quoteIdent(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}
