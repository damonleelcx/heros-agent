package auth

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/heros-foreal/heros/db/migrations"
	"github.com/heros-foreal/heros/internal/tenancy"
	"strings"
)

// TestEmailKeyMatchesHowTheDatabaseCompares.
//
// # 🔴 Why this needs a database and not a table of strings
//
// `EmailKey` is a SECOND expression of a rule the SQL already states as `lower(email)`. Two statements
// of one rule drift, and the drift here is silent and exploitable in both directions: a key stricter
// than the query treats one account as two (a per-address rate limit becomes "three per capitalisation"),
// and a key looser than the query merges two accounts into one bucket.
//
// A unit test over strings would only assert that `EmailKey` does what `EmailKey` does. This asks the
// actual database, through the actual login path, whether it considers each spelling the same person —
// and requires `EmailKey` to have said the same thing.
func TestEmailKeyMatchesHowTheDatabaseCompares(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := NewStore(db)
	tenant := fmt.Sprintf("t-emailkey-%d", time.Now().UnixNano())
	if err := s.CreateTenant(ctx, tenant, "Acme"); err != nil {
		t.Fatalf("tenant: %v", err)
	}
	const password = "a-sufficiently-long-password"
	// 🔴 Unique per run, like the tenant above, and the uniqueness lives in the DOMAIN so that every
	// spelling below stays an exact variant of this one address. Addresses are unique across the whole
	// deployment since migration 0008; a fixed address here passes once and fails on every later run.
	domain := fmt.Sprintf("Example%d.Test", time.Now().UnixNano())
	stored := "Firstname.Lastname@" + domain
	if _, err := s.CreateUser(ctx, tenant, stored, password, tenancy.Owner); err != nil {
		t.Fatalf("user: %v", err)
	}

	for _, spelling := range []string{
		stored,
		strings.ToLower(stored),
		strings.ToUpper(stored),
		"  " + stored + "  ",
		// 🚫 A plus-suffix and a dotless local part are DIFFERENT people as far as this product is
		// concerned — those normalisations belong to one mail provider, and applying them everywhere
		// merges accounts that belong to different humans.
		"firstname.lastname+work@" + strings.ToLower(domain),
		"firstnamelastname@" + strings.ToLower(domain),
	} {
		_, _, loginErr := s.Login(ctx, tenant, spelling, password)
		databaseSaysSamePerson := loginErr == nil
		keySaysSamePerson := EmailKey(spelling) == EmailKey(stored)

		if databaseSaysSamePerson != keySaysSamePerson {
			t.Errorf("%q: the database says same-person=%v, EmailKey says %v.\n"+
				"  Anything keyed on EmailKey — the password-reset rate limit, for one — is now keyed "+
				"differently from the account it is protecting", spelling,
				databaseSaysSamePerson, keySaysSamePerson)
		}
	}

	// The stored form keeps its capitalisation, because that is how somebody writes their own name and
	// how it should be shown back to them. Only the COMPARISON is case-insensitive.
	m, err := s.ListMembers(ctx, tenant)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(m) != 1 || m[0].Email != stored {
		t.Errorf("the address was stored as %q, not as typed (%q)", m[0].Email, stored)
	}
}

// testDB opens the test database and brings its schema up to date.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("HEROS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HEROS_TEST_DATABASE_URL unset")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrations.Apply(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
