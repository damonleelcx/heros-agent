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
