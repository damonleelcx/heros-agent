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
	"strings"

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
