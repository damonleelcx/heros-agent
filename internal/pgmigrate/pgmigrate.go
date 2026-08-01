// Package pgmigrate applies the platform's Postgres schema at boot, idempotently.
//
// # Why the ledger is READ, not just written
//
// Every migration file already ends by inserting its own row into `schema_migrations`. That makes the
// ledger a WRITTEN record — and a written-only ledger cannot decide anything. The decision this package
// makes is the opposite direction: read the ledger FIRST, and apply only what is missing from it.
//
// The distinction is not academic here. The DDL is bare `CREATE TABLE`, not `CREATE TABLE IF NOT
// EXISTS`, so a re-run is an error rather than a no-op — idempotence has to come from the ledger,
// because it cannot be borrowed from the DDL's tolerance. A runner that applied everything on every
// boot would fail the second boot of every deployment with `42P07 relation "workflow" already exists`.
//
// # Why each file is executed whole
//
// Each migration is already wrapped in its own `BEGIN; … COMMIT;`, so it is executed as one statement
// batch and its atomicity is the file's own. Splitting on semicolons would break the first function
// body or dollar-quoted string anyone adds, and would buy nothing the file does not already give.
//
// # Why a missing ledger row after a successful apply is fatal
//
// After applying, this verifies the migration's row is actually in the ledger. A migration that ran but
// forgot to record itself would be re-applied on the next boot and fail there — turning a silent
// authoring mistake into an outage on somebody else's upgrade, at the least convenient moment. Proving
// the row is present converts that into a failure on the boot that caused it.
package pgmigrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"

	migrations "github.com/heros-foreal/agentd/db/migrations"
)

// Migration is one embedded `.up.sql` file, identified by the numeric prefix it carries.
type Migration struct {
	ID   int64  // 1 for 0001_p0_lineage.up.sql
	Name string // "0001_p0_lineage"
	SQL  string
}

// Result reports what a run did, so a caller can log the difference between "brought the schema up" and
// "found it already current" instead of logging the same line either way.
type Result struct {
	Applied []string // names applied by THIS run, in order
	Already []string // names the ledger already held
}

// FreshInstall reports whether this run applied every migration — i.e. the database held none.
func (r Result) FreshInstall() bool { return len(r.Already) == 0 && len(r.Applied) > 0 }

var namePattern = regexp.MustCompile(`^(\d+)_([a-z0-9_]+)\.up\.sql$`)

// Load returns the embedded migrations in application order.
//
// Order is by numeric ID, not by string sort: `0010` sorting before `0002` is the classic way a
// migration set applies in an order nobody reviewed, and it stays latent until the tenth migration.
func Load() ([]Migration, error) {
	entries, err := fs.ReadDir(migrations.Postgres, "postgres")
	if err != nil {
		return nil, fmt.Errorf("pgmigrate: read embedded migrations: %w", err)
	}
	out := make([]Migration, 0, len(entries))
	seen := map[int64]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := namePattern.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("pgmigrate: %q does not match NNNN_name.up.sql — an unrecognised file is "+
				"either a migration that would be skipped silently or a typo, and both need a human", e.Name())
		}
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("pgmigrate: %q: %w", e.Name(), err)
		}
		if prev, dup := seen[id]; dup {
			return nil, fmt.Errorf("pgmigrate: migrations %s and %s share id %d — the ledger is keyed by id, "+
				"so one of them would be recorded as the other", prev, e.Name(), id)
		}
		seen[id] = e.Name()
		body, err := fs.ReadFile(migrations.Postgres, path.Join("postgres", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("pgmigrate: read %s: %w", e.Name(), err)
		}
		out = append(out, Migration{ID: id, Name: m[1] + "_" + m[2], SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Apply brings the database up to the embedded schema and returns what it did.
//
// It is safe to call on every boot: a database already at the embedded version has every id in its
// ledger, so nothing is executed and Result.Applied is empty.
func Apply(ctx context.Context, db *sql.DB) (Result, error) {
	all, err := Load()
	if err != nil {
		return Result{}, err
	}
	applied, err := AppliedIDs(ctx, db)
	if err != nil {
		return Result{}, err
	}

	var res Result
	for _, m := range all {
		if applied[m.ID] {
			res.Already = append(res.Already, m.Name)
			continue
		}
		if _, err := db.ExecContext(ctx, m.SQL); err != nil {
			// Named, with the migration that failed and how far the run got. "migration failed" without a
			// subject sends an operator to read nineteen files to learn what this error already knew.
			return res, fmt.Errorf("pgmigrate: applying %s (after %d applied, %d already present): %w",
				m.Name, len(res.Applied), len(res.Already), err)
		}
		ok, err := ledgerHas(ctx, db, m.ID)
		if err != nil {
			return res, err
		}
		if !ok {
			return res, fmt.Errorf("pgmigrate: %s applied but did not record id %d in schema_migrations — "+
				"the next boot would apply it again and fail there instead of here; add the ledger INSERT "+
				"to the migration", m.Name, m.ID)
		}
		res.Applied = append(res.Applied, m.Name)
	}
	return res, nil
}

// AppliedIDs reads the ledger. A database with no `schema_migrations` table has applied nothing — that
// is a fresh install, not an error, and it is the only state in which the set is legitimately empty.
func AppliedIDs(ctx context.Context, db *sql.DB) (map[int64]bool, error) {
	var present sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('schema_migrations')::text`).Scan(&present); err != nil {
		return nil, fmt.Errorf("pgmigrate: look for the migration ledger: %w", err)
	}
	out := map[int64]bool{}
	if !present.Valid {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT id FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("pgmigrate: read the migration ledger: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("pgmigrate: read the migration ledger: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgmigrate: read the migration ledger: %w", err)
	}
	return out, nil
}

func ledgerHas(ctx context.Context, db *sql.DB, id int64) (bool, error) {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE id = $1`, id).Scan(&n); err != nil {
		return false, fmt.Errorf("pgmigrate: verify ledger row %d: %w", id, err)
	}
	return n > 0, nil
}
