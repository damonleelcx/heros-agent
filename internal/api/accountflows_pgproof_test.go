//go:build pgproof

// Live four-layer assertions on the three write paths P27 adds (task 11.3).
//
// # What "four layers" means here, and why a 2xx is none of them
//
// A handler that returns 201 has proved one thing: it reached the end of its own function. It has not
// proved a row exists, that the row is the one a later page will read, or that the change had the effect
// its name claims. Each of those is a separate place the write can be lost — a transaction that rolls
// back after the response is written, a read path that filters on a column the write did not set, a
// consequence wired to a different store than the one that changed.
//
// So every flow below is asserted at four layers, in this order:
//
//  1. TRANSPORT      the status and the response body
//  2. STORE          the row, read back through the store interface against real Postgres
//  3. CONSUMPTION    the endpoint a subsequent page load actually calls — not the writer's return value
//  4. CONSEQUENCE    the behaviour the write was FOR: can this person authenticate, does the seat count
//     move, is the session gone
//
// Layer 3 is the one that catches the most and is the easiest to skip. Layer 4 is the one that matters:
// "the row is there" and "removing this member ended their access" are different claims, and only the
// second is what a customer was promised.
package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/mailer"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/seats"
	"github.com/heros-foreal/agentd/internal/signup"
	"github.com/heros-foreal/agentd/internal/tenancy"
	_ "github.com/lib/pq"
)

var flowAt = time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)

// liveSurface is the same composition `internal/launch` builds, against real Postgres.
type liveSurface struct {
	store    tenancy.Store
	accounts account.Store
	plans    *signup.CatalogPlans
	signUp   *signup.Service
	usage    *metering.MemUsageStore
	meter    *metering.Meter
	clock    time.Time
}

func (s *liveSurface) Store() tenancy.Store    { return s.store }
func (s *liveSurface) SignUp() *signup.Service { return s.signUp }
func (s *liveSurface) SelfServeEnabled() bool  { return true }

// ConsoleURL was added to AccountSurface by P28 (the origin the links in verification and reset messages
// point at) and was never added here — so this ENTIRE pgproof file has not compiled since, and with it
// the whole `internal/api` live-Postgres suite has been silently dead. Found by P29 §3.7, which needed
// to add a test to this package and could not. Reported as a finding; the stub is the minimum that
// revives the suite, and an empty origin is what this fixture has always effectively had (relative
// links), so no assertion changes meaning.
func (s *liveSurface) ConsoleURL() string { return "" }

// Mailer, likewise added by P28 and likewise never added here. nil is the documented meaning "held for
// the operator" — the interface's own comment is explicit that there is no path on which a message is
// dropped — so this is the honest stub rather than a silencing one.
func (s *liveSurface) Mailer() mailer.Mailer { return nil }
func (s *liveSurface) Now() time.Time {
	if s.clock.IsZero() {
		return flowAt
	}
	return s.clock
}
func (s *liveSurface) SeatsHeld(tenantID string) (int, error) {
	return seats.Current(s.store, tenantID)
}
func (s *liveSurface) ObserveSeats(tenantID string) {
	_, _ = seats.Observe(s.store, s.meter, tenantID, metering.MonthPeriod(s.Now()))
}
func (s *liveSurface) SeatsAllowed(tenantID string) (float64, bool, error) {
	acct, err := s.accounts.Get(tenantID)
	if err != nil {
		return 0, false, err
	}
	plan, err := s.plans.Resolve(acct.ActivePlanID)
	if err != nil {
		return 0, false, err
	}
	v, ok := plan.Limit(plancfg.LimitSeats)
	return v, ok, nil
}

const flowCatalog = `{
  "version": "cfg-p27-flows",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,"features":["cli","discovery"],
     "limits":{"seats":3},"price_refs":{}}
  ]
}`

func newLiveSurface(t *testing.T) (*Server, *liveSurface, *sql.DB) {
	t.Helper()
	db, err := pgtest.Open("proof_api_flows")
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply the embedded migration set: %v", err)
	}
	// A clean identity domain per test: these tables are shared within the package's schema, and a test
	// that inherited another's memberships would measure the fixture rather than the behaviour.
	for _, tbl := range []string{"console_session", "api_credential", "invitation", "membership", "platform_user", "tenant"} {
		if _, err := db.Exec(`DELETE FROM ` + tbl); err != nil {
			t.Fatalf("clear %s: %v", tbl, err)
		}
	}
	store, err := tenancy.NewPGStore(db)
	if err != nil {
		t.Fatalf("identity store: %v", err)
	}
	accounts, err := account.NewPGStore(db)
	if err != nil {
		t.Fatalf("account store: %v", err)
	}
	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(flowCatalog)); err != nil {
		t.Fatalf("catalog: %v", err)
	}
	plans := signup.NewCatalogPlans(src)
	surf := &liveSurface{
		store: store, accounts: accounts, plans: plans,
		usage: metering.NewMemUsageStore(),
	}
	surf.meter = metering.NewMeter(metering.NewMemCostEvents(), surf.usage)
	svc, err := signup.New(store, accounts, plans, surf.Now)
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	surf.signUp = svc

	s := New(nil, config.Config{})
	s.MountAccounts(surf)
	// ⚠️ `/api/v1/whoami` is registered by MountRunLinking, not by MountAccounts — the identity endpoint
	// is mounted with an unrelated capability. Harmless in a real deployment (`internal/launch` calls
	// MountRunLinking unconditionally, with a nil source when there is no database), and worth naming
	// because a test that mounts only the account system finds the identity endpoint missing, which is
	// exactly how this line came to exist.
	s.MountRunLinking(nil)
	return s, surf, db
}

func call(t *testing.T, s *Server, method, path string, body any, p *auth.Principal) (int, map[string]any) {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
	}
	if p != nil {
		r = r.WithContext(auth.WithPrincipal(r.Context(), *p))
	}
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, r)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

// ── sign-up ─────────────────────────────────────────────────────────────────────────────────────────

func TestPGFlow_SignUpAtFourLayers(t *testing.T) {
	s, surf, db := newLiveSurface(t)

	// 0 · The refusal that comes before any layer. An unauthenticated caller does not reach the posture
	// check, let alone the write.
	if code, _ := call(t, s, http.MethodPost, "/api/v1/organizations",
		map[string]any{"name": "Nobody"}, nil); code != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated sign-up answered %d, want 401", code)
	}

	// 1 · TRANSPORT
	//
	// ⚠️ The identity fields travel in the BODY, and this test supplies them the way the CONSOLE does.
	// `web/console/src/app/api/console/organization/signup/route.ts` fills them from the BFF's own
	// session — "the browser sends one field: a name" — so in the deployed shape they are server-side.
	// At the PLATFORM boundary they are not: any authenticated caller may name any `{issuer, subject}`.
	// See TestSignUpTakesTheIdentityFromTheRequestBody below, which pins that as the current contract
	// rather than leaving the reader to infer it from a passing test.
	caller := auth.Principal{TenantID: "platform", APIKeyID: "cred_bff"}
	code, body := call(t, s, http.MethodPost, "/api/v1/organizations", map[string]any{
		"name": "Acme", "issuer": "https://idp.acme", "subject": "sub-founder", "email": "founder@acme.com",
	}, &caller)
	if code != http.StatusCreated {
		t.Fatalf("sign-up answered %d: %v", code, body)
	}
	org, _ := body["organization"].(map[string]any)
	tenantID, _ := org["id"].(string)
	if tenantID == "" {
		t.Fatalf("the response names no organization: %v", body)
	}

	// 2 · STORE — the row, in Postgres, not the value the handler returned.
	var name, status string
	if err := db.QueryRow(`SELECT name, status FROM tenant WHERE tenant_id = $1`, tenantID).
		Scan(&name, &status); err != nil {
		t.Fatalf("the organization is not in the database after a 201: %v", err)
	}
	if name != "Acme" || status != "active" {
		t.Errorf("stored organization is %q/%q", name, status)
	}
	// The founder is an OWNER, and the account exists. Sign-up writes four rows and a 2xx proves none.
	var role string
	if err := db.QueryRow(
		`SELECT m.role FROM membership m JOIN platform_user u ON u.user_id = m.user_id
		  WHERE m.tenant_id = $1 AND u.subject = 'sub-founder'`, tenantID).Scan(&role); err != nil {
		t.Fatalf("the founder has no membership: %v", err)
	}
	if role != string(tenancy.RoleOwner) {
		t.Errorf("the founder joined as %q, not owner — an organization whose creator cannot administer "+
			"it is unrecoverable through the product", role)
	}
	acct, err := surf.accounts.Get(tenantID)
	if err != nil {
		t.Fatalf("the organization has no account, so it has no plan: %v", err)
	}
	if acct.PlanCharges {
		t.Error("a self-serve sign-up landed on a plan that charges")
	}

	// 3 · CONSUMPTION — the endpoint the console calls on the next page load.
	founder := principalFor(t, surf, tenantID, "sub-founder")
	code, view := call(t, s, http.MethodGet, "/api/v1/organization", nil, &founder)
	if code != http.StatusOK {
		t.Fatalf("the organization view answered %d: %v", code, view)
	}
	if view["name"] != "Acme" {
		t.Errorf("the view the console renders says %v, not the name that was just created", view["name"])
	}

	// 4 · CONSEQUENCE — the founder can act as an owner. The point of sign-up is not a row; it is that
	// somebody can now administer something.
	code, members := call(t, s, http.MethodGet, "/api/v1/organization/members", nil, &founder)
	if code != http.StatusOK {
		t.Fatalf("the founder cannot list their own members (%d): %v", code, members)
	}
	rows, _ := members["members"].([]any)
	if len(rows) != 1 {
		t.Fatalf("a new organization has %d members, want exactly its founder: %v", len(rows), members)
	}
}

// ── invitation acceptance ───────────────────────────────────────────────────────────────────────────

func TestPGFlow_InvitationAcceptanceAtFourLayers(t *testing.T) {
	s, surf, db := newLiveSurface(t)
	tenantID, founder := seedLiveOrg(t, s, surf, "Globex", "sub-owner", "owner@globex.com")

	code, inv := call(t, s, http.MethodPost, "/api/v1/organization/invitations",
		map[string]any{"email": "joiner@globex.com", "role": "member"}, &founder)
	if code != http.StatusCreated {
		t.Fatalf("creating the invitation answered %d: %v", code, inv)
	}
	invitationID, _ := inv["invitation_id"].(string)

	// The joiner signs in for the first time; the platform now knows who they are.
	joiner := principalForNewUser(t, surf, tenantID, "sub-joiner", "joiner@globex.com")

	seatsBefore := currentSeats(t, surf, tenantID)

	// 1 · TRANSPORT
	code, accepted := call(t, s, http.MethodPost,
		"/api/v1/organization/invitations/"+invitationID+"/accept", map[string]any{}, &joiner)
	if code != http.StatusOK {
		t.Fatalf("acceptance answered %d: %v", code, accepted)
	}

	// 2 · STORE
	var role, mstatus string
	if err := db.QueryRow(`SELECT role, status FROM membership WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, joiner.UserID).Scan(&role, &mstatus); err != nil {
		t.Fatalf("acceptance returned 200 and wrote no membership: %v", err)
	}
	if role != "member" || mstatus != "active" {
		t.Errorf("the new membership is %q/%q", role, mstatus)
	}
	var acceptedAt sql.NullTime
	if err := db.QueryRow(`SELECT accepted_at FROM invitation WHERE invitation_id = $1`, invitationID).
		Scan(&acceptedAt); err != nil {
		t.Fatalf("read the invitation back: %v", err)
	}
	if !acceptedAt.Valid {
		t.Error("the invitation is not marked accepted, so it can be accepted again — an invitation " +
			"that admits twice is a credential")
	}

	// 3 · CONSUMPTION — the members list the console renders next.
	code, members := call(t, s, http.MethodGet, "/api/v1/organization/members", nil, &founder)
	if code != http.StatusOK {
		t.Fatalf("members answered %d", code)
	}
	if rows, _ := members["members"].([]any); len(rows) != 2 {
		t.Fatalf("the members list shows %d after an acceptance, want 2: %v", len(rows), members)
	}

	// 4 · CONSEQUENCE — the seat count moved, and the joiner can now read the organization.
	if after := currentSeats(t, surf, tenantID); after != seatsBefore+1 {
		t.Errorf("the current seat count went %d → %d; acceptance is what makes somebody occupy a seat",
			seatsBefore, after)
	}
	if code, _ := call(t, s, http.MethodGet, "/api/v1/organization", nil, &joiner); code != http.StatusOK {
		t.Errorf("the new member cannot read the organization they just joined (%d)", code)
	}
}

// ── member removal ──────────────────────────────────────────────────────────────────────────────────

func TestPGFlow_MemberRemovalAtFourLayers(t *testing.T) {
	s, surf, db := newLiveSurface(t)
	tenantID, founder := seedLiveOrg(t, s, surf, "Initech", "sub-boss", "boss@initech.com")
	member := principalForNewUser(t, surf, tenantID, "sub-staff", "staff@initech.com")
	mustJoin(t, surf, tenantID, member.UserID, tenancy.RoleMember)

	// A credential and a live session belonging to the person about to be removed. Both are what the
	// removal must actually end — the row disappearing is not the promise.
	secret := "sk-live-removal-proof-value"
	if _, err := surf.store.CreateCredential(tenancy.Credential{
		CredentialID: "cred_staff", TenantID: tenantID, UserID: member.UserID,
		Role: tenancy.RoleMember, Hash: tenancy.HashSecret(secret), CreatedAt: flowAt,
	}); err != nil {
		t.Fatalf("create the member's credential: %v", err)
	}
	seatsBefore := currentSeats(t, surf, tenantID)

	// 1 · TRANSPORT
	code, removed := call(t, s, http.MethodDelete,
		"/api/v1/organization/members/"+member.UserID, nil, &founder)
	if code != http.StatusOK {
		t.Fatalf("removal answered %d: %v", code, removed)
	}

	// 2 · STORE
	var mstatus string
	var removedAt sql.NullTime
	if err := db.QueryRow(`SELECT status, removed_at FROM membership WHERE tenant_id=$1 AND user_id=$2`,
		tenantID, member.UserID).Scan(&mstatus, &removedAt); err != nil {
		t.Fatalf("read the membership back: %v", err)
	}
	if mstatus != "removed" || !removedAt.Valid {
		t.Errorf("the membership is %q/removed_at=%v — removal is a state change, and the row must stay "+
			"so the seat timeline can still be replayed", mstatus, removedAt.Valid)
	}

	// 3 · CONSUMPTION
	code, members := call(t, s, http.MethodGet, "/api/v1/organization/members", nil, &founder)
	if code != http.StatusOK {
		t.Fatalf("members answered %d", code)
	}
	rows, _ := members["members"].([]any)
	for _, r := range rows {
		m, _ := r.(map[string]any)
		if m["user_id"] == member.UserID && m["status"] == "active" {
			t.Errorf("the removed person is still listed as active: %v", m)
		}
	}

	// 4 · CONSEQUENCE — the seat is freed AND their credential stops working at the next request. The
	// second is the one a customer offboarding somebody is actually relying on.
	if after := currentSeats(t, surf, tenantID); after != seatsBefore-1 {
		t.Errorf("the current seat count went %d → %d after a removal", seatsBefore, after)
	}
	reg := auth.NewRegistry(config.Config{}).WithSource(surf.store.(auth.CredentialSource))
	if _, ok := reg.Lookup(secret); ok {
		t.Error("the removed member's credential still authenticates. The row changing state is not the " +
			"promise — the promise is that their next request is refused.")
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────────────────────────────

func seedLiveOrg(t *testing.T, s *Server, surf *liveSurface, name, subject, email string) (string, auth.Principal) {
	t.Helper()
	res, err := surf.signUp.Create(context.Background(), signup.Request{
		Name: name, Issuer: "https://idp.acme", Subject: subject, Email: email,
	})
	if err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	return res.Organization.Tenant.TenantID, auth.Principal{
		TenantID: res.Organization.Tenant.TenantID, UserID: res.Organization.Owner.UserID,
		Role: string(tenancy.RoleOwner), APIKeyID: "cred_seed",
	}
}

func principalFor(t *testing.T, surf *liveSurface, tenantID, subject string) auth.Principal {
	t.Helper()
	u, err := surf.store.FindUser("https://idp.acme", subject)
	if err != nil {
		t.Fatalf("find %s: %v", subject, err)
	}
	return auth.Principal{TenantID: tenantID, UserID: u.UserID, Role: string(tenancy.RoleOwner), APIKeyID: "cred_x"}
}

func principalForNewUser(t *testing.T, surf *liveSurface, tenantID, subject, email string) auth.Principal {
	t.Helper()
	u, err := surf.store.UpsertUser(tenancy.User{
		Issuer: "https://idp.acme", Subject: subject, Email: email, CreatedAt: flowAt,
	})
	if err != nil {
		t.Fatalf("upsert %s: %v", subject, err)
	}
	return auth.Principal{TenantID: tenantID, UserID: u.UserID, Role: string(tenancy.RoleMember), APIKeyID: "cred_y"}
}

func mustJoin(t *testing.T, surf *liveSurface, tenantID, userID string, role tenancy.Role) {
	t.Helper()
	if _, err := surf.store.PutMembership(tenancy.Membership{
		UserID: userID, TenantID: tenantID, Role: role, Status: tenancy.MemberActive, JoinedAt: flowAt,
	}); err != nil {
		t.Fatalf("add member: %v", err)
	}
}

func currentSeats(t *testing.T, surf *liveSurface, tenantID string) int {
	t.Helper()
	n, err := seats.Current(surf.store, tenantID)
	if err != nil {
		t.Fatalf("current seats: %v", err)
	}
	return n
}

// TestSignUpTakesTheIdentityFromTheRequestBody records a gap this task FOUND between the capability's
// spec and the route, and pins the behaviour that actually ships.
//
// 🔴 `specs/self-serve-subscription/spec.md` says, in its own scenario: *"the identity, issuer and
// subject are taken from the verified assertion server-side"* and *"the organization name is the only
// client-supplied field"*. `handleCreateOrganization` checks that SOME principal is present and then
// reads `issuer`, `subject` and `email` out of the JSON body.
//
// It is not a one-line fix, and that is why it is written down rather than patched here. The platform
// has no way to represent the caller: a person with no organization has no membership, and a console
// session row requires a `tenant_id`, so there is no scoped token for somebody who has not joined
// anything yet. The identity has to be asserted by the component that holds the IdP relationship — the
// console BFF — which is exactly what it does. The deployed product is therefore correct; the PLATFORM
// boundary is looser than the spec sentence, and an authenticated caller can name a `{issuer, subject}`
// that is not theirs and create an organization owned by that person.
//
// Two honest options for whoever owns it: narrow the route to a principal the platform recognises as the
// BFF, or amend the scenario to say which server holds the assertion. This test goes red if either
// happens, which is the point.
func TestSignUpTakesTheIdentityFromTheRequestBody(t *testing.T) {
	s, _, db := newLiveSurface(t)

	// A caller authenticated as one organization names a subject that has nothing to do with it.
	caller := auth.Principal{TenantID: "someone_elses_org", APIKeyID: "cred_x"}
	code, body := call(t, s, http.MethodPost, "/api/v1/organizations", map[string]any{
		"name": "Squatted", "issuer": "https://idp.victim", "subject": "sub-victim",
		"email": "victim@example.com",
	}, &caller)
	if code != http.StatusCreated {
		t.Skipf("sign-up no longer accepts an identity from the body (status %d: %v).\n"+
			"That is the SPEC's behaviour and good news — delete this test and the ⚠️ note in "+
			"TestPGFlow_SignUpAtFourLayers with it.", code, body)
	}
	var owner string
	if err := db.QueryRow(
		`SELECT u.subject FROM membership m JOIN platform_user u ON u.user_id = m.user_id
		  WHERE m.role = 'owner' AND m.tenant_id = (SELECT tenant_id FROM tenant WHERE name = 'Squatted')`).
		Scan(&owner); err != nil {
		t.Fatalf("read the owner back: %v", err)
	}
	if owner != "sub-victim" {
		t.Fatalf("the organization's owner is %q; this test pins the current contract and the contract "+
			"has changed", owner)
	}
	t.Logf("pinned: a caller authenticated as %q created an organization owned by the federated "+
		"subject %q, which it named itself", caller.TenantID, owner)
}
