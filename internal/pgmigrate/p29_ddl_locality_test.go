package pgmigrate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// p29_ddl_locality_test.go is P29 §3.4 — "the dialect-symmetry lint covers both migrations. If any DDL is
// moved into a Go hook, it must still be covered — a hook is not an exemption."
//
// # 🔴 What that task assumed, and what is actually true here — reported, not quietly reinterpreted
//
// The task is written for a product with TWO dialects, where symmetry means "the same DDL exists under
// both, and a statement moved into a Go hook escapes the comparison". **This repository has one dialect
// for this table family, and the asymmetry is deliberate rather than an omission.**
// `db/migrations/README.md` says it in the first paragraph: *two dialects are two semantics*, a migration
// lives under the dialect it targets, and the SQLite store in `internal/db/db.go` is the **dev ledger** —
// a different database holding registries, memory and API keys. It has never carried `workflow_ir`, and
// `linkingest` opens Postgres and only Postgres.
//
// Writing a SQLite copy of these two tables to satisfy the word "dual-dialect" would create a second
// schema that nothing reads and nothing migrates — which is precisely the drift that README exists to
// prevent, wearing symmetry's clothes.
//
// # So what IS enforced here, because the underlying hazard is real
//
// The hazard the dialect-symmetry lint actually guards is **DDL that escapes the migration files**. A
// `CREATE TABLE` or `ALTER TABLE` executed from Go — at boot, in a store's constructor, behind an
// `ensureX()` helper — is schema that no migration ledger records, no down migration reverses, no proof
// script applies and no reviewer reading `db/migrations/` can see. That failure mode does not need a
// second dialect to bite, and this repository has already had a version of it: the schema was applied
// only by `cmd/demo/configui` from a relative path, so it ran when someone started a demo from the
// repository root and on no deployment ever.
//
// This test therefore asserts the invariant that survives translation: **every DDL statement touching
// P29's objects lives in `db/migrations/postgres/`, and nothing in `internal/` executes any.**

// p29Objects are the schema objects this change owns.
var p29Objects = []string{"linked_transform", "coverage_version", "idx_linked_transform_tenant"}

// 🔴 No Go code executes DDL against P29's objects. A hook is not an exemption.
func TestNoGoCodeExecutesDDLForP29Objects(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	ddl := regexp.MustCompile(`(?is)\b(CREATE|ALTER|DROP)\s+(TABLE|INDEX|COLUMN|VIEW|CONSTRAINT)\b`)

	checked := 0
	err := filepath.Walk(root, func(path string, fi os.FileInfo, werr error) error {
		if werr != nil || fi.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Test files may create scratch tables in a throwaway schema; that is a fixture, not deployed
		// schema, and forbidding it would only push fixtures into non-test files.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				return true
			}
			if !ddl.MatchString(v) {
				return true
			}
			for _, obj := range p29Objects {
				if strings.Contains(strings.ToLower(v), obj) {
					t.Errorf("%s executes DDL touching %q from Go:\n  %s\n\n"+
						"  Schema that lives in Go is schema no migration ledger records, no down "+
						"migration reverses, no proof script applies, and no reviewer reading "+
						"db/migrations/ can see. Move it into a migration file.",
						fset.Position(lit.Pos()), obj, strings.TrimSpace(v))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 100 {
		t.Fatalf("the scan read only %d Go file(s) under %s — it is not reading the tree, so this fence "+
			"would pass for the wrong reason", checked, root)
	}
}

// The two migrations exist as an up/down PAIR, and the up file records itself in the ledger.
//
// A migration that applies and forgets to record itself is re-applied on the next boot and fails there —
// turning an authoring slip into an outage on somebody else's upgrade, which is 0024's exact history.
func TestP29MigrationsAreCompleteAndSelfRecording(t *testing.T) {
	dir := filepath.Join("..", "..", "db", "migrations", "postgres")
	for id, name := range map[int]string{
		42: "0042_p29_workflow_ir_coverage_version",
		43: "0043_p29_linked_transform",
	} {
		up, err := os.ReadFile(filepath.Join(dir, name+".up.sql"))
		if err != nil {
			t.Fatalf("%s.up.sql: %v", name, err)
		}
		if _, err := os.ReadFile(filepath.Join(dir, name+".down.sql")); err != nil {
			t.Errorf("%s has no down migration. A rollback with no down file is a rollback nobody has "+
				"thought about: %v", name, err)
		}
		want := "INSERT INTO schema_migrations (id, name) VALUES (" + strconv.Itoa(id)
		if !strings.Contains(string(up), want) {
			t.Errorf("%s.up.sql does not record itself as id %d in schema_migrations", name, id)
		}
		if !strings.Contains(string(up), "ON CONFLICT (id) DO NOTHING") {
			t.Errorf("%s.up.sql's ledger insert is not idempotent", name)
		}
		// The guard-by-definition requirement: an `IF NOT EXISTS` on its own is a NAME guard.
		if !strings.Contains(string(up), "RAISE EXCEPTION") {
			t.Errorf("%s.up.sql has no definition check. `IF NOT EXISTS` is satisfied by an object of the "+
				"right NAME and any shape at all, which is how a migration silently agrees with a schema "+
				"it does not describe.", name)
		}
	}
}
