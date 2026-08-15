package signup

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

var at = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

const catalog = `{
  "version": "cfg-p27-1",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,
     "features":["cli","discovery"],
     "limits":{"sum_band":25,"seats":1,"retention_days":7,"eval_compute":10},
     "price_refs":{}},
    {"plan_id":"team","display_name":"Team","rank":1,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":300,"seats":5,"retention_days":30,"eval_compute":100},
     "price_refs":{"subscription":"price_ref_team_sub"}}
  ]
}`

func newService(t *testing.T, catalogJSON string) (*Service, *tenancy.MemStore, *account.MemStore) {
	t.Helper()
	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(catalogJSON)); err != nil {
		t.Fatalf("publish catalog: %v", err)
	}
	tenants := tenancy.NewMemStore()
	accounts := account.NewMemStore()
	svc, err := New(tenants, accounts, NewCatalogPlans(src), func() time.Time { return at })
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return svc, tenants, accounts
}

// TestSignUpCreatesEverythingThePaidPathAssumes is the whole point of the phase, in one test.
//
// The failing sentence it replaces: `StartCheckout → accounts.Get → ErrNotFound`, which is what every
// real customer reaches today because nothing in non-test code calls `accounts.Create`.
func TestSignUpCreatesEverythingThePaidPathAssumes(t *testing.T) {
	svc, tenants, accounts := newService(t, catalog)

	res, err := svc.Create(context.Background(), Request{
		Name: "Acme Inc", Issuer: "https://idp.acme", Subject: "sub-1", Email: "Dana@Acme.com",
	})
	if err != nil {
		t.Fatalf("sign up: %v", err)
	}

	// The organization.
	if res.Organization.Tenant.Name != "Acme Inc" {
		t.Errorf("name did not survive: %q", res.Organization.Tenant.Name)
	}
	if !res.Organization.Tenant.Active() {
		t.Error("a new organization should be active")
	}
	// The person, and their OWNERSHIP of it.
	if res.Organization.Membership.Role != tenancy.RoleOwner {
		t.Errorf("the creator must be the owner, got %q", res.Organization.Membership.Role)
	}
	if res.Organization.Owner.Email != "dana@acme.com" {
		t.Errorf("the address should be normalised: %q", res.Organization.Owner.Email)
	}

	// 🔴 Read every row back through the STORE, not from the return value. A service that reports
	// success while writing nothing passes an assertion on its own output.
	if _, err := tenants.GetTenant(res.Organization.Tenant.TenantID); err != nil {
		t.Fatalf("the organization was not persisted: %v", err)
	}
	m, err := tenants.GetMembership(res.Organization.Owner.UserID, res.Organization.Tenant.TenantID)
	if err != nil || !m.Active() || m.Role != tenancy.RoleOwner {
		t.Fatalf("the owner membership was not persisted: %v / %+v", err, m)
	}

	// And the account the paid path opens with.
	acct, err := accounts.Get(res.Organization.Tenant.TenantID)
	if err != nil {
		t.Fatalf("accounts.Get on a freshly signed-up organization failed — this is the exact call "+
			"StartCheckout opens with, and the exact failure P27 exists to close: %v", err)
	}
	if acct.ActivePlanID != "free" {
		t.Errorf("a new organization should start on the free plan, got %q", acct.ActivePlanID)
	}
	if acct.PlanConfigVersion != "cfg-p27-1" {
		t.Errorf("the plan's config version was not pinned: %q", acct.PlanConfigVersion)
	}
	if acct.ProviderCustomerHandle != "" {
		t.Errorf("a Free account must hold NO billing-provider customer — registering one for everybody "+
			"who tries the free tier is a data-minimisation problem we cannot undo. got %q",
			acct.ProviderCustomerHandle)
	}
	if acct.PlanCharges {
		t.Error("the free plan was recorded as charging")
	}
	// The customer id IS the tenant id, which is what `scope.ts` already assumes.
	if acct.CustomerID != res.Organization.Tenant.TenantID {
		t.Errorf("customer id %q != tenant id %q", acct.CustomerID, res.Organization.Tenant.TenantID)
	}
}

// TestAPartialSignUpLeavesNothing.
//
// 🔴 Every partial state here is unrecoverable through the product: a tenant with no owner has nobody
// who can invite one, and a tenant with no account reaches ErrNotFound on its first paid action. So a
// failure must leave none of them, and the assertion is that the identity rows are gone too — not just
// that the call returned an error.
func TestAPartialSignUpLeavesNothing(t *testing.T) {
	svc, tenants, accounts := newService(t, catalog)

	// Make the account write fail by claiming the customer id first. The id is minted inside Create, so
	// instead we let the first sign-up succeed and then force a collision on the SECOND by pre-creating
	// its account — which is not reachable from outside. The reachable equivalent: a store that refuses.
	svc.accounts = refusingAccounts{accounts}

	_, err := svc.Create(context.Background(), Request{
		Name: "Acme Inc", Issuer: "https://idp.acme", Subject: "sub-1", Email: "dana@acme.com",
	})
	if err == nil {
		t.Fatal("sign-up succeeded even though the account write failed")
	}

	list, lerr := tenants.ListTenants()
	if lerr != nil {
		t.Fatalf("list: %v", lerr)
	}
	if len(list) != 0 {
		t.Fatalf("a failed sign-up left %d organization(s) behind: %+v\n"+
			"A tenant with no account reaches ErrNotFound on its first paid action, and no screen can fix it.", len(list), list)
	}
	if _, err := tenants.FindUser("https://idp.acme", "sub-1"); err == nil {
		t.Error("a failed sign-up left the person behind")
	}
}

type refusingAccounts struct{ account.Store }

func (refusingAccounts) Create(account.Account) (account.Account, error) {
	return account.Account{}, errors.New("billing store is down")
}

// TestSignUpRefusesWithoutAName. It is the one field the person types, and deriving it from the email
// domain produces "Gmail" for every independent developer.
func TestSignUpRefusesWithoutAName(t *testing.T) {
	svc, _, _ := newService(t, catalog)
	if _, err := svc.Create(context.Background(), Request{
		Name: "   ", Issuer: "https://idp", Subject: "sub-1", Email: "a@b.com",
	}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("want ErrNameRequired, got %v", err)
	}
}

// TestSignUpRefusesWithoutAVerifiedIdentity: the issuer and subject come from the assertion, and there
// is no path that derives them from anything a client sent.
func TestSignUpRefusesWithoutAVerifiedIdentity(t *testing.T) {
	svc, _, _ := newService(t, catalog)
	if _, err := svc.Create(context.Background(), Request{Name: "Acme"}); err == nil {
		t.Fatal("sign-up succeeded with no verified identity")
	}
}

// TestACatalogWithNoFreePlanRefusesRatherThanFallingBack.
//
// Falling back to the cheapest paid plan would demand a payment method from somebody who has not seen
// the product yet — and it would succeed, so nobody would notice until a customer complained.
func TestACatalogWithNoFreePlanRefusesRatherThanFallingBack(t *testing.T) {
	paidOnly := strings.Replace(catalog, `"price_refs":{}}`, `"price_refs":{"subscription":"price_ref_free_sub"}}`, 1)
	svc, tenants, _ := newService(t, paidOnly)

	_, err := svc.Create(context.Background(), Request{
		Name: "Acme", Issuer: "https://idp", Subject: "sub-1", Email: "a@acme.com",
	})
	if !errors.Is(err, ErrNoFreePlan) {
		t.Fatalf("want ErrNoFreePlan, got %v", err)
	}
	if list, _ := tenants.ListTenants(); len(list) != 0 {
		t.Error("a refused sign-up created an organization anyway")
	}
}

// TestTheFreePlanIsTheLowestRankedPlanThatChargesNothing — derived from the catalog, so it cannot
// disagree with it.
func TestTheFreePlanIsTheLowestRankedPlanThatChargesNothing(t *testing.T) {
	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(catalog)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	plans := NewCatalogPlans(src)
	if got := plans.FreePlanID(); got != "free" {
		t.Errorf("free plan is %q, want free", got)
	}

	// A catalog whose entry tier is called something else must still work.
	renamed := strings.ReplaceAll(catalog, `"plan_id":"free"`, `"plan_id":"starter"`)
	src2 := plancfg.NewMemSource()
	if err := src2.PublishJSON([]byte(renamed)); err != nil {
		t.Fatalf("publish renamed: %v", err)
	}
	if got := NewCatalogPlans(src2).FreePlanID(); got != "starter" {
		t.Errorf("a deployment that names its entry tier differently got %q; the free plan is DERIVED "+
			"from the catalog (lowest rank, no price references), never hardcoded", got)
	}
}

// TestTheResolvedPlanCarriesTheSnapshotVersion: an account pins the version so a closed period stays
// explainable after the next publish, and a plan resolved without one would pin the empty string.
func TestTheResolvedPlanCarriesTheSnapshotVersion(t *testing.T) {
	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(catalog)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	p, err := NewCatalogPlans(src).Resolve("team")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if p.Version != "cfg-p27-1" {
		t.Errorf("the version did not travel with the plan: %q", p.Version)
	}
	if !p.Charges() {
		t.Error("a plan with a subscription price reference must report that it charges")
	}
}

// TestOnePersonMaySignUpTwice — the contractor case, which is why membership is a join table.
func TestOnePersonMaySignUpTwice(t *testing.T) {
	svc, tenants, _ := newService(t, catalog)
	ctx := context.Background()
	req := Request{Name: "Acme", Issuer: "https://idp", Subject: "sub-1", Email: "dana@acme.com"}

	first, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	req.Name = "Globex"
	second, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	if first.Organization.Owner.UserID != second.Organization.Owner.UserID {
		t.Fatalf("the same person became two people: %s / %s",
			first.Organization.Owner.UserID, second.Organization.Owner.UserID)
	}
	if first.Organization.Tenant.TenantID == second.Organization.Tenant.TenantID {
		t.Fatal("two sign-ups produced one organization")
	}
	ms, err := tenants.ListMembershipsFor(first.Organization.Owner.UserID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ms) != 2 {
		t.Fatalf("expected two memberships, got %d", len(ms))
	}
}

// ── Creation-time placement seeding ─────────────────────────────────────────────────────────────────
//
// A deployment whose organizations are all its operator's own wants every new one enrolled for
// analysis. The dangerous way to give it that is to flip what an ABSENT placement row means, which
// enables every tenant at once, records nobody's decision, and removes the per-tenant off switch. The
// seed writes an explicit row per organization instead. These hold that distinction.

func TestWithoutASeedSignUpWritesNoPlacement(t *testing.T) {
	svc, _, _ := newService(t, catalog)
	// No WithPlacementSeed: the service must not invent a placement, because `disabled` by absence is
	// the Q2 default and a signup path that quietly enrolled everybody would be the change this design
	// exists to avoid.
	if svc.seed != nil {
		t.Fatal("a service built with no seed carries one")
	}
	if _, err := svc.Create(context.Background(), Request{
		Name: "Acme Inc", Issuer: "https://idp.acme", Subject: "sub-1", Email: "dana@acme.com",
	}); err != nil {
		t.Fatalf("sign up: %v", err)
	}
}

func TestTheSeedRunsForANewOrganisation(t *testing.T) {
	svc, _, _ := newService(t, catalog)
	var gotTenant string
	var calls int
	svc = svc.WithPlacementSeed(func(_ context.Context, _ tenancy.Execer, tenantID string, _ time.Time) error {
		calls++
		gotTenant = tenantID
		return nil
	})
	res, err := svc.Create(context.Background(), Request{
		Name: "Acme Inc", Issuer: "https://idp.acme", Subject: "sub-1", Email: "dana@acme.com",
	})
	if err != nil {
		t.Fatalf("sign up: %v", err)
	}
	if calls != 1 {
		t.Fatalf("the seed ran %d time(s), want 1", calls)
	}
	// The tenant the seed is told about must be the one that was created — a seed pointed at a
	// different id would enrol an organization nobody signed up.
	if gotTenant != res.Organization.Tenant.TenantID {
		t.Errorf("the seed was given %q, want the created tenant %q", gotTenant, res.Organization.Tenant.TenantID)
	}
}

// 🔴 A FAILING SEED FAILS THE SIGN-UP.
//
// This is the opposite of the bypass rule that governs the analysis itself, and deliberately: the seed
// runs INSIDE the creation transaction, so returning nil would commit an organization the deployment's
// own policy says should have been enrolled. A missing analysis is a missing enrichment; an
// organization created outside its deployment's policy is a record that is wrong.
func TestAFailingSeedFailsTheSignUp(t *testing.T) {
	svc, tenants, _ := newService(t, catalog)
	svc = svc.WithPlacementSeed(func(context.Context, tenancy.Execer, string, time.Time) error {
		return errors.New("placement store is unreachable")
	})
	_, err := svc.Create(context.Background(), Request{
		Name: "Acme Inc", Issuer: "https://idp.acme", Subject: "sub-1", Email: "dana@acme.com",
	})
	if err == nil {
		t.Fatal("a sign-up whose placement seed failed was reported as successful")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("the failure does not carry the seed's reason: %v", err)
	}
	// And the organization must not survive the failure: the identity store undoes its writes, so a
	// retry is a clean creation rather than a collision with a half-made org.
	list, lerr := tenants.ListTenants()
	if lerr != nil {
		t.Fatalf("list tenants: %v", lerr)
	}
	if len(list) != 0 {
		t.Errorf("%d organization(s) were left behind after the seed failed: %+v", len(list), list)
	}
}
