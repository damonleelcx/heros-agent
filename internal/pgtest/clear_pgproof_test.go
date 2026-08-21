//go:build pgproof

// clear_pgproof_test.go proves `Clear` against the real schema.
//
// # Why this needs a live Postgres rather than a fixture
//
// The whole value of `Clear` is that it reads the foreign keys THAT EXIST rather than a list somebody
// maintains. A test over a hand-built fixture graph would prove the topological sort works and would
// prove nothing about the property that matters — that the expansion tracks the migrations. So the
// graph under test is the one the shipped migration set produces.
//
// # 🔴 The assertion that would have caught the original defect
//
// `TestClearingPlatformUserReachesItsPasswordRows` names `user_password` and `identity_token`
// explicitly. They are the two children migration 0041 added and that three separate hand-written lists
// missed, and naming them is the difference between "the sort works" and "the omission cannot recur".
package pgtest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/heros-foreal/agentd/internal/pgmigrate"
)

func clearDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open("pgtest_clear")
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

func indexOf(order []string, table string) int {
	for i, t := range order {
		if t == table {
			return i
		}
	}
	return -1
}

// TestClearingPlatformUserReachesItsPasswordRows is the defect, as a test.
func TestClearingPlatformUserReachesItsPasswordRows(t *testing.T) {
	db := clearDB(t)
	order, err := ClearedTables(db, "platform_user", "tenant")
	if err != nil {
		t.Fatalf("ClearOrder: %v", err)
	}

	// The two children migration 0041 added, which every hand-written list missed.
	for _, child := range []string{"user_password", "identity_token"} {
		i := indexOf(order, child)
		if i < 0 {
			t.Fatalf("clearing platform_user does not reach %q. That omission is the whole reason this "+
				"function exists: three suites listed the identity tables by hand, migration 0041 added "+
				"this child, and none of the lists learned about it.\n  order: %v", child, order)
		}
		if parent := indexOf(order, "platform_user"); i > parent {
			t.Errorf("%q is deleted AFTER platform_user (positions %d and %d); the foreign key would "+
				"refuse it.\n  order: %v", child, i, parent, order)
		}
	}

	// And the four that WERE listed, so a regression cannot pass by dropping them.
	for _, child := range []string{"api_credential", "console_session", "device_authorization", "membership"} {
		if i := indexOf(order, child); i < 0 {
			t.Errorf("clearing platform_user no longer reaches %q", child)
		} else if i > indexOf(order, "platform_user") {
			t.Errorf("%q is deleted after platform_user", child)
		}
	}
}

// TestEveryForeignKeyChildPrecedesItsParent is the general property.
//
// 🔴 Derived from the catalog on BOTH sides — the order comes from `pg_constraint` and so does the
// check. That is deliberate: an assertion written against a remembered list of edges would be the same
// artefact that failed, one level up.
func TestEveryForeignKeyChildPrecedesItsParent(t *testing.T) {
	db := clearDB(t)

	// Every root any suite in this repository clears, so the property is checked over the real
	// domains rather than over one convenient one.
	for _, roots := range [][]string{
		{"platform_user", "tenant"},
		{"platform_user", "account", "tenant"},
		{"run"},
		{"workflow"},
		{"variant"},
		{"source_connection"},
	} {
		order, err := ClearedTables(db, roots...)
		if err != nil {
			t.Fatalf("ClearOrder(%v): %v", roots, err)
		}
		if len(order) == 0 {
			t.Fatalf("ClearOrder(%v) returned nothing", roots)
		}
		children, err := foreignKeyChildren(db)
		if err != nil {
			t.Fatalf("read foreign keys: %v", err)
		}
		for parent, kids := range children {
			pi := indexOf(order, parent)
			if pi < 0 {
				continue // not in this domain
			}
			for _, child := range kids {
				if child == parent {
					continue // a self-edge is cleared by the single DELETE
				}
				ci := indexOf(order, child)
				if ci < 0 {
					t.Errorf("%v: %q references %q and is not cleared — the DELETE would be refused",
						roots, child, parent)
					continue
				}
				if ci > pi {
					t.Errorf("%v: %q (position %d) is deleted after its parent %q (position %d)",
						roots, child, ci, parent, pi)
				}
			}
		}
	}
}

// TestClearActuallyEmptiesTheRowsItReaches is the behavioural half.
//
// The two tests above read an ORDER. This one writes a row into the child that was being missed, runs
// the real `Clear`, and reads back — because an order that is correct and a `DELETE` that is never
// issued are indistinguishable from a list.
func TestClearActuallyEmptiesTheRowsItReaches(t *testing.T) {
	db := clearDB(t)
	ctx := context.Background()

	// The columns are the schema's own — `created_at` and `updated_at` are TIMESTAMPTZ with defaults,
	// so they are omitted rather than supplied. The first draft of this fixture invented
	// `created_at_ms`, which is the shape several NEWER tables use, and Postgres refused it: worth
	// leaving as a note, because a fixture that guesses a column is a fixture that can also guess one
	// that happens to exist and mean something else.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO platform_user (user_id, issuer, subject, email)
		 VALUES ('u-clear', 'issuer-clear', 'sub-clear', 'clear@example.com')`); err != nil {
		t.Fatalf("insert platform_user: %v", err)
	}
	// 🔴 The child that made the original failure loud. Without this row, `DELETE FROM platform_user`
	// succeeds whether or not the child is cleared — which is exactly why two of the three broken
	// helpers stayed green.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO user_password (user_id, encoded)
		 VALUES ('u-clear', '$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA')`); err != nil {
		t.Fatalf("insert user_password: %v", err)
	}

	Clear(t, db, "platform_user", "tenant")

	for _, table := range []string{"platform_user", "user_password"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdent(table)).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s still holds %d row(s) after Clear", table, n)
		}
	}
}

// TestClearRefusesAnEmptyRequest.
//
// A reset that silently does nothing is worse than one that fails: the next test measures the previous
// test's fixture and attributes the result to its own behaviour.
func TestClearRefusesAnEmptyRequest(t *testing.T) {
	db := clearDB(t)
	if _, err := ClearOrder(db); err == nil {
		t.Error("ClearOrder with no tables returned no error; a reset that clears nothing must say so")
	}
}
