package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/seats"
	"github.com/heros-foreal/agentd/internal/signup"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

var apiAt = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

const testCatalog = `{
  "version": "cfg-p27-api",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,"features":["cli","discovery"],
     "limits":{"seats":2},"price_refs":{}},
    {"plan_id":"team","display_name":"Team","rank":1,"features":["cli","discovery","dashboard"],
     "limits":{"seats":5},"price_refs":{"subscription":"price_ref_team"}}
  ]
}`

// testSurface is the same composition `internal/launch` builds, wired against in-memory stores.
type testSurface struct {
	store     *tenancy.MemStore
	accounts  account.Store
	plans     *signup.CatalogPlans
	signUp    *signup.Service
	selfServe bool
	// usage is where the seat PEAK lands, so a test can assert the observation happened rather than
	// assume it. `observed` counts the calls, because "the number is right" and "the write ran" are two
	// facts and only one of them survives a refactor that stops calling.
	usage    *metering.MemUsageStore
	meter    *metering.Meter
	observed int
	// clock advances so a membership can be created and later removed at DIFFERENT instants, which is
	// what real usage does. A frozen clock produces zero-duration memberships, and a zero-duration
	// membership correctly contributes nothing to a seat peak — so a test built on one asserts against
	// the fixture rather than against the behaviour.
	clock time.Time
}

func (s *testSurface) Store() tenancy.Store    { return s.store }
func (s *testSurface) SignUp() *signup.Service { return s.signUp }
func (s *testSurface) SelfServeEnabled() bool  { return s.selfServe }
func (s *testSurface) Now() time.Time {
	if s.clock.IsZero() {
		return apiAt
	}
	return s.clock
}
func (s *testSurface) ObserveSeats(tenantID string) {
	s.observed++
	if s.meter == nil {
		return
	}
	if _, err := seats.Observe(s.store, s.meter, tenantID, metering.MonthPeriod(s.Now())); err != nil {
		panic("observe seats: " + err.Error())
	}
}
func (s *testSurface) SeatsAllowed(tenantID string) (float64, bool, error) {
	acct, err := s.accounts.Get(tenantID)
	if err != nil {
		return 0, false, err
	}
	if v, ok := acct.QuotaOverride(string(plancfg.LimitSeats)); ok {
		return v, true, nil
	}
	plan, err := s.plans.Resolve(acct.ActivePlanID)
	if err != nil {
		return 0, false, err
	}
	v, ok := plan.Limit(plancfg.LimitSeats)
	return v, ok, nil
}

func newSurface(t *testing.T, selfServe bool) (*Server, *testSurface) {
	t.Helper()
	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(testCatalog)); err != nil {
		t.Fatalf("catalog: %v", err)
	}
	plans := signup.NewCatalogPlans(src)
	store := tenancy.NewMemStore()
	accounts := account.NewMemStore()
	svc, err := signup.New(store, accounts, plans, func() time.Time { return apiAt })
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	usage := metering.NewMemUsageStore()
	meter := metering.NewMeter(metering.NewMemCostEvents(), usage)
	meter.SetClock(func() time.Time { return apiAt })
	surface := &testSurface{
		store: store, accounts: accounts, plans: plans, selfServe: selfServe,
		usage: usage, meter: meter,
	}
	if selfServe {
		surface.signUp = svc
	}
	s := New(nil, config.Config{})
	s.MountAccounts(surface)
	return s, surface
}

// as builds a request carrying an authenticated principal, which is what the middleware would have put
// in the context. The handlers read the tenant from there and from nowhere else.
func asPrincipal(t *testing.T, method, path string, body any, p auth.Principal) *http.Request {
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
	return r.WithContext(auth.WithPrincipal(r.Context(), p))
}

func doReq(t *testing.T, s *Server, r *http.Request) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, r)
	var body map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v — %s", r.URL.Path, err, rec.Body.String())
		}
	}
	return rec.Code, body
}

// seedOrg signs an organization up through the real service and returns the ids.
func seedOrg(t *testing.T, s *Server, surf *testSurface, name, subject, email string) (string, string) {
	t.Helper()
	svc := surf.signUp
	if svc == nil {
		var err error
		svc, err = signup.New(surf.store, surf.accounts, surf.plans, func() time.Time { return apiAt })
		if err != nil {
			t.Fatalf("signup: %v", err)
		}
	}
	res, err := svc.Create(t.Context(), signup.Request{
		Name: name, Issuer: "https://idp", Subject: subject, Email: email,
	})
	if err != nil {
		t.Fatalf("seed org: %v", err)
	}
	return res.Organization.Tenant.TenantID, res.Organization.Owner.UserID
}

func owner(tenantID, userID string) auth.Principal {
	return auth.Principal{TenantID: tenantID, UserID: userID, Role: string(tenancy.RoleOwner), APIKeyID: "cred_test"}
}

// ── sign-up ─────────────────────────────────────────────────────────────────────────────────────────

func TestSignUpIsRefusedWhenSelfServeIsOff(t *testing.T) {
	s, _ := newSurface(t, false)
	code, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organizations", map[string]any{
		"name": "Acme", "issuer": "https://idp", "subject": "sub-1", "email": "a@acme.com",
	}, auth.Principal{TenantID: "platform"}))
	if code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", code)
	}
	if body["reason_code"] != ReasonSelfServeDisabled {
		t.Errorf("reason_code=%v, want %q", body["reason_code"], ReasonSelfServeDisabled)
	}
}

func TestSignUpCreatesAnOrganizationOnAFreePlan(t *testing.T) {
	s, surf := newSurface(t, true)
	code, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organizations", map[string]any{
		"name": "Acme Inc", "issuer": "https://idp", "subject": "sub-1", "email": "dana@acme.com",
	}, auth.Principal{TenantID: "platform"}))
	if code != http.StatusCreated {
		t.Fatalf("status %d — %v", code, body)
	}
	org, _ := body["organization"].(map[string]any)
	id, _ := org["id"].(string)
	if id == "" {
		t.Fatal("no organization id in the response")
	}
	// The account the paid path opens with must exist.
	acct, err := surf.accounts.Get(id)
	if err != nil {
		t.Fatalf("accounts.Get after sign-up: %v", err)
	}
	if acct.PlanCharges || acct.ProviderCustomerHandle != "" {
		t.Errorf("a new organization must start on a plan that charges nothing, with no billing customer: %+v", acct)
	}
	plan, _ := body["plan"].(map[string]any)
	if plan["charges"] != false {
		t.Errorf("plan.charges=%v, want false", plan["charges"])
	}
}

// ── members ─────────────────────────────────────────────────────────────────────────────────────────

func TestAMachineCredentialMayNotAdministerPeople(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, _ := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")

	// A principal with no user is a machine credential.
	machine := auth.Principal{TenantID: tenantID, Role: string(tenancy.RoleAdmin), APIKeyID: "cred_ci"}
	code, body := doReq(t, s, asPrincipal(t, http.MethodGet, "/api/v1/organization/members", nil, machine))
	if code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 — a CI key that can remove a colleague becomes an offboarding tool", code)
	}
	if body["reason_code"] != ReasonNotAMember {
		t.Errorf("reason_code=%v", body["reason_code"])
	}
}

func TestTheLastOwnerCannotBeRemovedThroughTheAPI(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, userID := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")

	code, body := doReq(t, s, asPrincipal(t, http.MethodDelete, "/api/v1/organization/members/"+userID, nil, owner(tenantID, userID)))
	if code != http.StatusConflict {
		t.Fatalf("status %d, want 409 — %v", code, body)
	}
	if body["reason_code"] != ReasonLastOwner {
		t.Errorf("reason_code=%v, want %q — a generic denial does not tell the owner what to do next",
			body["reason_code"], ReasonLastOwner)
	}
}

// TestTheRemovalPreviewNamesWhatItWillNotRevoke is the offboarding honesty requirement, at the API.
func TestTheRemovalPreviewNamesWhatItWillNotRevoke(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, ownerID := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")

	// A second person, and one credential of each kind.
	leaver, err := surf.store.UpsertUser(tenancy.User{Issuer: "https://idp", Subject: "sub-2", Email: "sam@acme.com", CreatedAt: apiAt})
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := surf.store.PutMembership(tenancy.Membership{
		UserID: leaver.UserID, TenantID: tenantID, Role: tenancy.RoleMember, JoinedAt: apiAt,
	}); err != nil {
		t.Fatalf("membership: %v", err)
	}
	mustCred := func(userID, label string) {
		secret, _ := tenancy.NewCredentialSecret()
		if _, err := surf.store.CreateCredential(tenancy.Credential{
			CredentialID: tenancy.NewID("cred"), TenantID: tenantID, UserID: userID,
			Label: label, Role: tenancy.RoleMember, Hash: tenancy.HashSecret(secret), CreatedAt: apiAt,
		}); err != nil {
			t.Fatalf("credential %s: %v", label, err)
		}
	}
	mustCred(leaver.UserID, "sam's laptop")
	mustCred("", "CI deploy key")

	code, body := doReq(t, s, asPrincipal(t, http.MethodGet,
		"/api/v1/organization/members/"+leaver.UserID+"/removal-preview", nil, owner(tenantID, ownerID)))
	if code != http.StatusOK {
		t.Fatalf("status %d — %v", code, body)
	}
	revoked, _ := body["credentials_revoked"].([]any)
	retained, _ := body["credentials_retained"].([]any)
	if len(revoked) != 1 {
		t.Errorf("credentials_revoked=%v, want one personal credential", revoked)
	}
	if len(retained) != 1 {
		t.Fatalf("credentials_retained=%v — an offboarding screen that hides the CI key it leaves "+
			"running is worse than none: the person confirming it signs an attestation that is wrong", retained)
	}
	kept, _ := retained[0].(map[string]any)
	if kept["label"] != "CI deploy key" {
		t.Errorf("the retained credential is not named: %v", kept)
	}
	if kept["kind"] != "machine" {
		t.Errorf("kind=%v, want machine — the word decides what removal covers", kept["kind"])
	}
}

// ── invitations ─────────────────────────────────────────────────────────────────────────────────────

func TestAnInvitationLinkIsNotACredential(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, ownerID := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")

	code, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/invitations", map[string]any{
		"email": "New.Hire@Acme.com", "role": "member",
	}, owner(tenantID, ownerID)))
	if code != http.StatusCreated {
		t.Fatalf("create invitation: %d — %v", code, body)
	}
	id, _ := body["invitation_id"].(string)

	// A stranger: a real person, signed in, whose verified address is not the invited one.
	stranger, err := surf.store.UpsertUser(tenancy.User{
		Issuer: "https://idp", Subject: "sub-999", Email: "stranger@elsewhere.com", CreatedAt: apiAt,
	})
	if err != nil {
		t.Fatalf("stranger: %v", err)
	}
	invited, err := surf.store.UpsertUser(tenancy.User{
		Issuer: "https://idp", Subject: "sub-2", Email: "new.hire@acme.com", CreatedAt: apiAt,
	})
	if err != nil {
		t.Fatalf("invited: %v", err)
	}

	// 🔴 Somebody else's verified identity opening the same link creates nothing.
	code, body = doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/invitations/"+id+"/accept", nil,
		auth.Principal{TenantID: tenantID, UserID: stranger.UserID}))
	if code != http.StatusForbidden {
		t.Fatalf("a forwarded invitation was accepted by the wrong identity: %d — %v", code, body)
	}
	if body["reason_code"] != ReasonInvitationMismatch {
		t.Errorf("reason_code=%v", body["reason_code"])
	}
	members, _ := surf.store.ListMembers(tenantID)
	if len(members) != 1 {
		t.Fatalf("the refused acceptance created a membership: %+v", members)
	}

	// The invited person joins.
	code, body = doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/invitations/"+id+"/accept", nil,
		auth.Principal{TenantID: tenantID, UserID: invited.UserID}))
	if code != http.StatusOK {
		t.Fatalf("the invited person was refused: %d — %v", code, body)
	}
	members, _ = surf.store.ListMembers(tenantID)
	if len(members) != 2 {
		t.Fatalf("acceptance did not create a membership: %+v", members)
	}

	// Single-use.
	code, body = doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/invitations/"+id+"/accept", nil,
		auth.Principal{TenantID: tenantID, UserID: invited.UserID}))
	if code != http.StatusConflict || body["reason_code"] != ReasonInvitationExpired {
		t.Errorf("a second acceptance was not refused: %d — %v", code, body)
	}
}

// TestTheSeatRefusalNamesBothNumbers.
func TestTheSeatRefusalNamesBothNumbers(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, ownerID := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")
	// The free plan allows 2 seats. One member exists; one pending invitation makes two.
	code, _ := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/invitations", map[string]any{
		"email": "second@acme.com", "role": "member",
	}, owner(tenantID, ownerID)))
	if code != http.StatusCreated {
		t.Fatalf("first invitation: %d", code)
	}

	code, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/invitations", map[string]any{
		"email": "third@acme.com", "role": "member",
	}, owner(tenantID, ownerID)))
	if code != http.StatusConflict {
		t.Fatalf("the third seat was allowed on a 2-seat plan: %d — %v", code, body)
	}
	if body["reason_code"] != ReasonSeatLimitReached {
		t.Fatalf("reason_code=%v", body["reason_code"])
	}
	msg, _ := body["error"].(string)
	if !bytes.Contains([]byte(msg), []byte("2")) {
		t.Errorf("the refusal does not name the plan's allowance and the current count: %q\n"+
			"A limit the user cannot check is a limit they distrust.", msg)
	}
}

// ── credentials ─────────────────────────────────────────────────────────────────────────────────────

func TestACredentialSecretIsReturnedOnceAndNeverListed(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, ownerID := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")

	code, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/credentials", map[string]any{
		"label": "dana's laptop", "kind": "personal",
	}, owner(tenantID, ownerID)))
	if code != http.StatusCreated {
		t.Fatalf("create credential: %d — %v", code, body)
	}
	secret, _ := body["secret"].(string)
	if secret == "" {
		t.Fatal("no secret in the creation response — that is the only moment it exists")
	}
	if body["kind"] != "personal" {
		t.Errorf("kind=%v", body["kind"])
	}

	code, list := doReq(t, s, asPrincipal(t, http.MethodGet, "/api/v1/organization/credentials", nil, owner(tenantID, ownerID)))
	if code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("the credential listing carries the plaintext")
	}
	// And it authenticates, which is what proves the hash round-tripped.
	reg := auth.NewRegistry(config.Config{}).WithSource(surf.store)
	p, ok := reg.Lookup(secret)
	if !ok || p.TenantID != tenantID || p.UserID != ownerID {
		t.Fatalf("the issued credential does not authenticate: %+v ok=%v", p, ok)
	}
}

func TestAMachineCredentialSurvivesRemovingThePersonWhoMadeIt(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, ownerID := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")
	// A second owner so the first can be removed.
	second, err := surf.store.UpsertUser(tenancy.User{Issuer: "https://idp", Subject: "sub-2", Email: "sam@acme.com", CreatedAt: apiAt})
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := surf.store.PutMembership(tenancy.Membership{
		UserID: second.UserID, TenantID: tenantID, Role: tenancy.RoleOwner, JoinedAt: apiAt,
	}); err != nil {
		t.Fatalf("membership: %v", err)
	}

	mk := func(kind string) string {
		code, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/credentials", map[string]any{
			"label": kind + " key", "kind": kind,
		}, owner(tenantID, ownerID)))
		if code != http.StatusCreated {
			t.Fatalf("create %s: %d — %v", kind, code, body)
		}
		secret, _ := body["secret"].(string)
		return secret
	}
	personal := mk("personal")
	machine := mk("machine")

	code, body := doReq(t, s, asPrincipal(t, http.MethodDelete, "/api/v1/organization/members/"+ownerID, nil,
		auth.Principal{TenantID: tenantID, UserID: second.UserID, Role: string(tenancy.RoleOwner)}))
	if code != http.StatusOK {
		t.Fatalf("remove: %d — %v", code, body)
	}

	reg := auth.NewRegistry(config.Config{}).WithSource(surf.store)
	if _, ok := reg.Lookup(personal); ok {
		t.Error("the removed person's credential still authenticates")
	}
	if _, ok := reg.Lookup(machine); !ok {
		t.Error("removing a person revoked the organization's machine credential — that breaks the " +
			"customer's build, and the preview promised it would not")
	}
	if body["machine_credentials_unaffected"] == nil {
		t.Error("the removal result does not report what it left running")
	}
}

// TestNoRouteAcceptsAnOrganizationParameter is FR16's shape, checked at the surface: scope comes from
// the credential, and there is no parameter that could change it.
func TestNoRouteAcceptsAnOrganizationParameter(t *testing.T) {
	s, surf := newSurface(t, true)
	acme, acmeOwner := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")
	globex, _ := seedOrg(t, s, surf, "Globex", "sub-2", "sam@globex.com")

	// Acme's owner asks for members, naming Globex every way a client could.
	r := asPrincipal(t, http.MethodGet, "/api/v1/organization/members?tenant="+globex+"&organization="+globex,
		nil, owner(acme, acmeOwner))
	r.Header.Set("X-Console-Tenant", globex)
	code, body := doReq(t, s, r)
	if code != http.StatusOK {
		t.Fatalf("status %d — %v", code, body)
	}
	members, _ := body["members"].([]any)
	if len(members) != 1 {
		t.Fatalf("expected only Acme's one member, got %d", len(members))
	}
	m, _ := members[0].(map[string]any)
	if m["email"] != "dana@acme.com" {
		t.Errorf("a client-supplied organization changed the result: %v", m)
	}
}

// TestAnUnmountedAccountSystemAnswers404RatherThan503 — a deployment that does not mount the surface
// registers no routes, so the answer is "no such route" rather than "the route is broken".
func TestAnUnmountedAccountSystemAnswers404RatherThan503(t *testing.T) {
	s := New(nil, config.Config{})
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/organization/members", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

// TestAcceptingAnInvitationRecordsThePeriodPeak is 6.3 at the surface: the membership change is the
// event, and the re-derived peak is the reconciliation read that follows it.
func TestAcceptingAnInvitationRecordsThePeriodPeak(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, ownerID := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")

	_, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/invitations", map[string]any{
		"email": "second@acme.com", "role": "member",
	}, owner(tenantID, ownerID)))
	id, _ := body["invitation_id"].(string)

	joiner, err := surf.store.UpsertUser(tenancy.User{
		Issuer: "https://idp", Subject: "sub-2", Email: "second@acme.com", CreatedAt: apiAt,
	})
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}
	code, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/invitations/"+id+"/accept", nil,
		auth.Principal{TenantID: tenantID, UserID: joiner.UserID}))
	if code != http.StatusOK {
		t.Fatalf("accept: %d — %v", code, body)
	}

	rec, err := surf.usage.Get(metering.Key{
		CustomerID: tenantID, Period: metering.MonthPeriod(apiAt).ID, Metric: metering.MetricSeats,
	})
	if err != nil {
		t.Fatalf("no seat observation was recorded after a membership change: %v\n"+
			"This is the write whose absence made LimitSeats decorative for the whole of P7.", err)
	}
	if rec.Quantity != 2 {
		t.Fatalf("recorded seat peak = %v, want 2", rec.Quantity)
	}
}

// TestRemovingAMemberFreesTheCurrentSeatButNotTheBilledPeak — the two quantities moving in opposite
// directions in one operation is the whole reason they have two names.
func TestRemovingAMemberFreesTheCurrentSeatButNotTheBilledPeak(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, ownerID := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")

	second, err := surf.store.UpsertUser(tenancy.User{Issuer: "https://idp", Subject: "sub-2", Email: "sam@acme.com", CreatedAt: apiAt})
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	// Both members join at the same instant the organization was created; the removal happens a day
	// later. Real durations, deliberately: a membership stamped with ONE frozen clock joins and leaves
	// at the same instant, which is a zero-duration membership and correctly contributes nothing to the
	// peak — see `seats.PeakOf`. Writing the test that way would assert against the fixture.
	if _, err := surf.store.PutMembership(tenancy.Membership{
		UserID: second.UserID, TenantID: tenantID, Role: tenancy.RoleMember, JoinedAt: apiAt,
	}); err != nil {
		t.Fatalf("membership: %v", err)
	}
	surf.ObserveSeats(tenantID)
	surf.clock = apiAt.Add(24 * time.Hour)

	code, body := doReq(t, s, asPrincipal(t, http.MethodDelete,
		"/api/v1/organization/members/"+second.UserID, nil, owner(tenantID, ownerID)))
	if code != http.StatusOK {
		t.Fatalf("remove: %d — %v", code, body)
	}

	// The seat that gates the next invitation is freed immediately.
	code, org := doReq(t, s, asPrincipal(t, http.MethodGet, "/api/v1/organization", nil, owner(tenantID, ownerID)))
	if code != http.StatusOK {
		t.Fatalf("organization: %d", code)
	}
	if org["seats_current"] != float64(1) {
		t.Errorf("seats_current = %v, want 1", org["seats_current"])
	}

	// The billed peak is not.
	rec, err := surf.usage.Get(metering.Key{
		CustomerID: tenantID, Period: metering.MonthPeriod(apiAt).ID, Metric: metering.MetricSeats,
	})
	if err != nil {
		t.Fatalf("seat observation: %v", err)
	}
	if rec.Quantity != 2 {
		t.Fatalf("the billed peak fell to %v after a removal — removing people before the period closes "+
			"must not retroactively unbuy the month", rec.Quantity)
	}
}

// TestTheOrganizationViewLabelsWhichSeatQuantityItShows is FR22 at the surface: no unlabelled "seats".
func TestTheOrganizationViewLabelsWhichSeatQuantityItShows(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, ownerID := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")

	code, body := doReq(t, s, asPrincipal(t, http.MethodGet, "/api/v1/organization", nil, owner(tenantID, ownerID)))
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if _, bare := body["seats"]; bare {
		t.Fatal("the response carries an unlabelled `seats` field — a reader cannot tell the current " +
			"count from the period's billed peak, and they are different numbers")
	}
	if _, ok := body["seats_current"]; !ok {
		t.Error("seats_current is missing")
	}
	if _, ok := body["seats_allowed"]; !ok {
		t.Error("seats_allowed is missing on a plan that sets one")
	}
}

// TestClosingAnAccountStopsBillingAndSaysItErasedNothing is 6.6.
//
// 🔴 Both halves in one response. A closure surface that says "closed" without saying what it did NOT do
// is a surface a customer reads as deletion — and a customer who hears "we keep it" without "you can ask
// us to erase it" hears a retention problem. The mechanism is NAMED so the next question has an answer.
func TestClosingAnAccountStopsBillingAndSaysItErasedNothing(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, ownerID := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")

	code, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/close", nil, owner(tenantID, ownerID)))
	if code != http.StatusOK {
		t.Fatalf("close: %d — %v", code, body)
	}
	if body["status"] != string(tenancy.StatusSuspended) {
		t.Errorf("status = %v, want suspended", body["status"])
	}
	if body["billing_stopped"] != true {
		t.Error("closing did not report that billing stopped")
	}
	if body["data_erased"] != false {
		t.Error("closing reported that data was erased — it was not, and saying so is the one claim " +
			"here that a regulator would care about")
	}
	if body["erasure_mechanism"] != ErasureMechanism {
		t.Errorf("erasure_mechanism = %v, want %q — a closure surface must point at the process that "+
			"actually erases, not imply it did the erasing", body["erasure_mechanism"], ErasureMechanism)
	}

	// The organization is suspended in the store, which is what stops accrual and refuses its
	// credentials at authentication.
	tn, err := surf.store.GetTenant(tenantID)
	if err != nil {
		t.Fatalf("get tenant: %v", err)
	}
	if !tn.Status.Suspended() {
		t.Fatal("closing did not suspend the organization")
	}
	// And the history is still there.
	members, err := surf.store.ListMembers(tenantID)
	if err != nil || len(members) == 0 {
		t.Fatalf("closing erased the member list: %v / %+v", err, members)
	}
}

// TestOnlyAnOwnerMayCloseAnAccount — closing is the mirror of upgrading, and both are financial.
func TestOnlyAnOwnerMayCloseAnAccount(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, _ := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")

	admin, err := surf.store.UpsertUser(tenancy.User{Issuer: "https://idp", Subject: "sub-admin", Email: "adm@acme.com", CreatedAt: apiAt})
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := surf.store.PutMembership(tenancy.Membership{
		UserID: admin.UserID, TenantID: tenantID, Role: tenancy.RoleAdmin, JoinedAt: apiAt,
	}); err != nil {
		t.Fatalf("membership: %v", err)
	}

	code, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/close", nil,
		auth.Principal{TenantID: tenantID, UserID: admin.UserID, Role: string(tenancy.RoleAdmin)}))
	if code != http.StatusForbidden {
		t.Fatalf("an admin closed the account: %d — %v", code, body)
	}
	if body["reason_code"] != ReasonForbiddenRole {
		t.Errorf("reason_code = %v", body["reason_code"])
	}
}

// TestAMachineCredentialCannotAcceptAnInvitation.
//
// An invitation admits a PERSON. A credential that names none has nobody to admit — and letting one
// accept would mean a CI key could add itself to an organization it was invited to by mistake.
func TestAMachineCredentialCannotAcceptAnInvitation(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, ownerID := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")

	_, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/invitations", map[string]any{
		"email": "new@acme.com", "role": "member",
	}, owner(tenantID, ownerID)))
	id, _ := body["invitation_id"].(string)

	code, refusal := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/invitations/"+id+"/accept", nil,
		auth.Principal{TenantID: tenantID, Role: string(tenancy.RoleAdmin), APIKeyID: "cred_ci"}))
	if code != http.StatusForbidden {
		t.Fatalf("a machine credential accepted an invitation: %d — %v", code, refusal)
	}
	if refusal["reason_code"] != ReasonNotAMember {
		t.Errorf("reason_code = %v", refusal["reason_code"])
	}
}

// TestTheAcceptanceRequestCarriesNoIdentityAtAll is the shape the rule became.
//
// It used to take `{issuer, subject, email}` in the body — the console asserting who was acting. The
// actor now comes from the credential the platform verified, so there is no field an address could
// arrive in, and "the address in the request is never the address that matters" is structural.
func TestTheAcceptanceRequestCarriesNoIdentityAtAll(t *testing.T) {
	s, surf := newSurface(t, true)
	tenantID, ownerID := seedOrg(t, s, surf, "Acme", "sub-1", "dana@acme.com")

	_, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/invitations", map[string]any{
		"email": "new@acme.com", "role": "member",
	}, owner(tenantID, ownerID)))
	id, _ := body["invitation_id"].(string)

	joiner, err := surf.store.UpsertUser(tenancy.User{
		Issuer: "https://idp", Subject: "sub-9", Email: "new@acme.com", CreatedAt: apiAt,
	})
	if err != nil {
		t.Fatalf("joiner: %v", err)
	}

	// A body claiming a DIFFERENT address changes nothing: it is not read.
	code, _ := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/organization/invitations/"+id+"/accept",
		map[string]any{"email": "somebody-else@elsewhere.com"},
		auth.Principal{TenantID: tenantID, UserID: joiner.UserID}))
	if code != http.StatusOK {
		t.Fatalf("the invited person was refused because of a body they did not control: %d", code)
	}
}
