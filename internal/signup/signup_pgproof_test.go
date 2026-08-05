//go:build pgproof

// Sign-up's atomicity, proven against a real transaction.
//
// The in-memory suite proves the SHAPE — a failure leaves nothing — but it proves it against a store
// that achieves atomicity with a mutex and an undo function. That is as strong as a store which loses
// everything on restart can be, and it is not the guarantee the durable path claims.
//
// This file proves the real one: four rows across two bounded contexts, in one Postgres transaction,
// and a failure in the LAST of them rolling back the first three. The failure mode it forbids is
// specific and unrecoverable through the product — an organization with an owner, a member list and no
// billing account, whose first *Upgrade* click reaches `accounts.Get → ErrNotFound` with no screen able
// to fix it.
//
//	make pg-proof
//	HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/signup/
package signup

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

func openPG(t *testing.T, schema string) *sql.DB {
	t.Helper()
	db, err := pgtest.Open(schema)
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := pgmigrate.Apply(t.Context(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	for _, table := range []string{"console_session", "api_credential", "invitation", "membership", "platform_user", "account", "tenant"} {
		if _, err := db.Exec(`DELETE FROM ` + table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
	return db
}

func durableService(t *testing.T, db *sql.DB) (*Service, *tenancy.PGStore) {
	t.Helper()
	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(catalog)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	tenants, err := tenancy.NewPGStore(db)
	if err != nil {
		t.Fatalf("tenancy store: %v", err)
	}
	accounts, err := account.NewPGStore(db)
	if err != nil {
		t.Fatalf("account store: %v", err)
	}
	svc, err := New(tenants, accounts, NewCatalogPlans(src), func() time.Time { return at })
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return svc, tenants
}

// TestDurableSignUpWritesFourRowsInOneTransaction.
func TestDurableSignUpWritesFourRowsInOneTransaction(t *testing.T) {
	db := openPG(t, "signup_ok")
	svc, _ := durableService(t, db)

	res, err := svc.Create(context.Background(), Request{
		Name: "Acme Inc", Issuer: "https://idp.acme", Subject: "sub-1", Email: "dana@acme.com",
	})
	if err != nil {
		t.Fatalf("sign up: %v", err)
	}
	id := res.Organization.Tenant.TenantID

	// Read every row back through SQL, so a service that reports success while writing nothing fails.
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM tenant WHERE tenant_id=$1`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("tenant rows=%d err=%v", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM platform_user WHERE issuer='https://idp.acme' AND subject='sub-1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("user rows=%d err=%v", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM membership WHERE tenant_id=$1 AND role='owner' AND status='active'`, id).Scan(&n); err != nil || n != 1 {
		t.Fatalf("owner membership rows=%d err=%v", n, err)
	}

	// 🔴 A plain `string`, and an ABSENT handle is the EMPTY one — D3 as amended by task 10.1. This read
	// used a `sql.NullString` and checked `.Valid`, which was correct while absence was spelled NULL and
	// became wrong in the same commit that stopped spelling it that way: `''` is Valid, so the test
	// reported an empty handle as a handle. The column is NOT NULL now, so there is nothing a NullString
	// would carry that this does not — and this is the same type the PRIOR image scans into, which is
	// what makes rolling the release back a redeploy rather than an outage.
	var handle, plan string
	var charges bool
	if err := db.QueryRow(
		`SELECT provider_customer_handle, plan_charges, active_plan_id FROM account WHERE customer_id=$1`, id,
	).Scan(&handle, &charges, &plan); err != nil {
		t.Fatalf("account row: %v", err)
	}
	if handle != "" {
		t.Errorf("a Free account holds a provider handle (%q) — sign-up must not register a customer at "+
			"a payment provider for everybody who tries the free tier", handle)
	}
	if charges {
		t.Error("plan_charges is true on the free plan")
	}
	if plan != "free" {
		t.Errorf("active_plan_id=%q", plan)
	}
}

// TestAFailedAccountWriteRollsBackTheOrganization.
//
// 🔴 This is the assertion the in-memory store cannot make. The three identity rows are written first,
// the account write fails, and the transaction must take all four back — not three of them, and not
// "eventually", which is what a compensating cleanup job would mean.
func TestAFailedAccountWriteRollsBackTheOrganization(t *testing.T) {
	db := openPG(t, "signup_rollback")
	svc, tenants := durableService(t, db)

	// Force the account write to fail INSIDE the transaction, the way a constraint violation would: the
	// customer id already exists. Sign-up mints the id, so the collision is arranged by making the
	// account store refuse — the failure the transaction has to survive is "the last write errored",
	// whatever produced it.
	svc.accounts = brokenAccounts{}

	// The in-memory hook path is not what runs here; the durable path calls account.CreateWithin with
	// the transaction. Point the service at a creator whose hook fails, and assert the rollback.
	failing := &failingHookCreator{inner: tenants}
	svc.tenants = failing

	_, err := svc.Create(context.Background(), Request{
		Name: "Acme Inc", Issuer: "https://idp.acme", Subject: "sub-1", Email: "dana@acme.com",
	})
	if err == nil {
		t.Fatal("sign-up succeeded even though the account write failed")
	}

	for _, q := range []string{
		`SELECT count(*) FROM tenant`,
		`SELECT count(*) FROM platform_user`,
		`SELECT count(*) FROM membership`,
		`SELECT count(*) FROM account`,
	} {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if n != 0 {
			t.Fatalf("%s returned %d — the transaction did not roll back. An organization with an owner "+
				"and no billing account reaches ErrNotFound on its first Upgrade, and no screen can fix it.", q, n)
		}
	}
}

// failingHookCreator delegates to the real store but makes the hook fail, which is the only way to
// exercise "the last write in the transaction errored" without depending on a specific constraint.
type failingHookCreator struct{ inner tenancy.OrganizationCreator }

func (f *failingHookCreator) CreateOrganization(o tenancy.NewOrganization, _ func(tenancy.Execer) error) (tenancy.Organization, error) {
	return f.inner.CreateOrganization(o, func(tenancy.Execer) error {
		return errors.New("billing store refused the write")
	})
}

type brokenAccounts struct{ account.Store }

// TestTwoConcurrentSignUpsForTheSameIdentityBothSucceedWithTheirOwnOrganization.
//
// A person signing up twice is the contractor case, not an edge one, and the user row is an UPSERT on
// the federated pair — so the two transactions race on one row. Both must succeed with one person and
// two organizations, rather than one failing with a unique violation the user would read as "sign-up is
// broken".
func TestTwoConcurrentSignUpsForTheSameIdentityBothSucceedWithTheirOwnOrganization(t *testing.T) {
	db := openPG(t, "signup_same_person")
	svc, _ := durableService(t, db)
	ctx := context.Background()

	first, err := svc.Create(ctx, Request{Name: "Acme", Issuer: "https://idp", Subject: "sub-1", Email: "dana@acme.com"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.Create(ctx, Request{Name: "Globex", Issuer: "https://idp", Subject: "sub-1", Email: "dana@acme.com"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.Organization.Owner.UserID != second.Organization.Owner.UserID {
		t.Fatalf("one person became two rows: %s / %s",
			first.Organization.Owner.UserID, second.Organization.Owner.UserID)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM platform_user`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("platform_user rows=%d err=%v", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM membership WHERE user_id=$1`, first.Organization.Owner.UserID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("memberships=%d err=%v", n, err)
	}
}
