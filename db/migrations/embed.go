// Package migrations embeds the SQL so a binary carries its own schema.
//
// 🔴 Embedded rather than read from disk at runtime: a server that loads migrations from a path is a
// server that behaves differently depending on what is next to it on the filesystem, and the failure
// appears at a customer's site rather than in CI.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed postgres/*.sql
var postgresFS embed.FS

// Apply runs every Postgres migration in filename order.
//
// # Why every migration runs every time, and why that is safe
//
// There is no "current version" query and no tracking table consulted before deciding what to run. Each
// statement is written to be idempotent (IF NOT EXISTS, or a guard), so the model is "run them all, let
// idempotence absorb the repeats". A tracking table that says a migration was applied when it was not —
// or the reverse — is a source of truth that disagrees with the database, and the database wins every
// argument it is not asked to participate in.
//
// 🔴 Statements are executed one at a time and NOT wrapped in a single transaction. Postgres permits
// DDL in a transaction, but one failing statement in a long chain would roll back work that had already
// succeeded, turning a partial upgrade into a total one. Idempotence is what makes the retry safe.
//
// # 🔴 Why the whole run is serialised by an advisory lock
//
// Idempotence makes a repeat safe. It does NOT make a CONCURRENT repeat safe, and the difference had
// already shipped: Postgres has no `ADD CONSTRAINT IF NOT EXISTS`, so the idiom here is
// `DROP CONSTRAINT IF EXISTS` followed by `ADD CONSTRAINT`. Two processes interleaving those four
// statements — both drop, both add — leaves the second one failing with "constraint already exists" and
// the process it belongs to refusing to start.
//
// That is not a hypothetical. It is what a second replica rolling out does, and what two test packages
// migrating the same database do; the race is wide enough that the suite hit it under `-race`, which is
// slow enough to open the window. The loser waits, then re-runs the chain from the top and lets
// idempotence absorb it.
//
// 🔴 The lock is TRANSACTION-scoped, held by a transaction that does nothing else. The obvious version —
// `pg_advisory_lock` on a reserved connection, released by closing it — is wrong in a way that is
// invisible until it is catastrophic: `sql.Conn.Close` returns the connection to the POOL, it does not
// end the session, so the lock is still held by a connection now serving unrelated queries and no later
// migration in this process can ever acquire it. Observed exactly that way: one session waiting on the
// lock, another idle in the pool holding it, forever.
//
// `pg_advisory_xact_lock` has no such path. Postgres releases it on commit, on rollback, and on the
// connection dying — so a panic, a cancelled context and a killed process all release it, and there is
// no line of code that has to remember to.
func Apply(ctx context.Context, db *sql.DB) error {
	entries, err := postgresFS.ReadDir("postgres")
	if err != nil {
		return fmt.Errorf("migrations: read dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	// A transaction whose only purpose is to hold the lock. It takes no table locks, so the DDL below —
	// which runs on the pool, one statement at a time, outside any transaction — is unaffected by it.
	gate, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrations: begin: %w", err)
	}
	defer func() { _ = gate.Rollback() }()
	if _, err := gate.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("migrations: waiting for another process to finish migrating: %w", err)
	}

	for _, name := range names {
		body, err := postgresFS.ReadFile("postgres/" + name)
		if err != nil {
			return fmt.Errorf("migrations: read %s: %w", name, err)
		}
		for i, stmt := range splitStatements(string(body)) {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("migrations: %s statement %d: %w", name, i+1, err)
			}
		}
	}
	return nil
}

// migrationLockKey identifies this lock among all advisory locks in the database.
//
// An arbitrary constant, fixed forever: two builds using different keys would not exclude each other,
// which is the whole point. Nothing else in this product takes an advisory lock, and the number is
// written out rather than computed so that a change to it is visible in a diff.
const migrationLockKey int64 = 4_183_027_641_100_871

// splitStatements splits on semicolons at the end of a line, which is the form this repository's
// migrations are written in. It strips `--` comments first so a semicolon inside one cannot split a
// statement in half.
//
// 🚫 Deliberately not a general SQL parser. The constraint is enforced by convention and by the fact
// that every statement here is DDL; a migration needing dollar-quoted function bodies must add a real
// splitter rather than hope this one copes.
func splitStatements(body string) []string {
	var clean []string
	for _, line := range strings.Split(body, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		clean = append(clean, line)
	}
	var out []string
	for _, stmt := range strings.Split(strings.Join(clean, "\n"), ";") {
		if s := strings.TrimSpace(stmt); s != "" {
			out = append(out, s)
		}
	}
	return out
}
