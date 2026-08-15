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
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/herosagent"
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

// ── The placement seed on the DURABLE path ──────────────────────────────────────────────────────────
//
// 🔴 Why this file and not signup_test.go. The in-memory tests exercise the `ex == nil` branch, so a
// mutation that removed the seed from the DURABLE branch left them green — the branch that actually
// runs in production had no coverage at all. Found by drilling, which is the only reason it is here.

func TestDurableSignUpSeedsThePlacementInsideTheTransaction(t *testing.T) {
	db := openPG(t, "signup_placement_seed")
	svc, _ := durableService(t, db)
	if _, err := db.Exec(`DELETE FROM heros_tenant_placement`); err != nil {
		t.Fatalf("clear placements: %v", err)
	}

	svc = svc.WithPlacementSeed(func(ctx context.Context, ex tenancy.Execer, tenantID string, at time.Time) error {
		// 🔴 The REAL writer, through the transaction handle sign-up passes. A test that wrote through
		// `db` here would pass while the production seam was broken: it would prove a row can be
		// written, never that it is written inside the creation transaction.
		return herosagent.SetPlacementWithin(ctx, ex, herosagent.TenantPlacement{
			TenantID: tenantID, Placement: herosagent.PlacementPlatform,
			Reason: "created under this deployment's signup policy", SetBy: "signup-policy:test",
			UpdatedAtMS: at.UnixMilli(),
		})
	})

	res, err := svc.Create(context.Background(), Request{
		Name: "Acme Inc", Issuer: "https://idp.acme", Subject: "sub-1", Email: "dana@acme.com",
	})
	if err != nil {
		t.Fatalf("sign up: %v", err)
	}

	var placement, setBy, reason string
	err = db.QueryRow(`SELECT placement, set_by, reason FROM heros_tenant_placement WHERE tenant_id = $1`,
		res.Organization.Tenant.TenantID).Scan(&placement, &setBy, &reason)
	if err != nil {
		t.Fatalf("the new organization has no placement row: %v", err)
	}
	if placement != string(herosagent.PlacementPlatform) {
		t.Errorf("placement = %q, want %q", placement, herosagent.PlacementPlatform)
	}
	// The row must be attributable to the POLICY, not to a person: nobody clicked anything, and a
	// set_by that read like an operator would be the audit trail lying about who decided.
	if !strings.Contains(setBy, "signup-policy") {
		t.Errorf("set_by = %q — a seeded row must name the mechanism, never an operator", setBy)
	}
	if strings.TrimSpace(reason) == "" {
		t.Error("the seeded row carries no reason")
	}
}

// 🔴 A FAILING SEED ROLLS THE WHOLE ORGANIZATION BACK.
//
// The transaction is the entire justification for seeding at creation rather than after it. If the
// seed can fail and leave the organization committed, the deployment ends up with exactly the record
// its policy says should not exist — and nothing downstream would ever notice.
func TestDurableSignUpRollsBackWhenTheSeedFails(t *testing.T) {
	db := openPG(t, "signup_placement_rollback")
	svc, _ := durableService(t, db)

	svc = svc.WithPlacementSeed(func(context.Context, tenancy.Execer, string, time.Time) error {
		return errors.New("placement store is unreachable")
	})
	if _, err := svc.Create(context.Background(), Request{
		Name: "Acme Inc", Issuer: "https://idp.acme", Subject: "sub-1", Email: "dana@acme.com",
	}); err == nil {
		t.Fatal("a sign-up whose placement seed failed was reported as successful")
	}

	for _, table := range []string{"tenant", "platform_user", "membership", "account"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s kept %d row(s) after the seed failed — the organization survived a failure "+
				"that was supposed to roll it back", table, n)
		}
	}
}
