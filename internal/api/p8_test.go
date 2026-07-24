package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/plancfg"
)

// p8_test.go covers task 12.5 — the admin-console-surface SECURITY ASSERTIONS at the HTTP boundary,
// exercised against a real handler and real backend services:
//
//	no admin capability is reachable without the platform credential (the BFF boundary)
//	an unauthenticated route is refused (FR20)
//	a revoked admin session is denied at the next request (FR2)
//	a tenant session reaches no admin capability (FR21)
//
// The "no credential in the shipped bundle" assertion lives in the console's build-time scan
// (scripts/scan-bundle.mjs), which is where a bundle can actually be inspected.

const platformCred = "test-platform-credential-value-1234567890"
const adminIssuer = "https://admin-idp.test.heros.internal"

type adminStack struct {
	api      *api.AdminAPI
	sessions *adminidentity.SessionStore
	idp      *adminidentity.IdPFixture
	clk      func() time.Time
}

func newAdminStack(t *testing.T) *adminStack {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC) }
	audit := adminaudit.NewMemoryStore(now)

	grants := adminrbac.NewGrantStore(now)
	for _, r := range adminrbac.Roles {
		if _, err := grants.Seed("adm-"+string(r), r, "fixture"); err != nil {
			t.Fatalf("Seed: %v", err)
		}
	}
	gate, err := adminrbac.NewGate(grants, audit, now)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	exec, err := adminops.NewExecutor(gate, audit, nil, now)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	secrets, err := adminidentity.FixtureSecrets("sso-k", "mfa-k", "session-k")
	if err != nil {
		t.Fatalf("FixtureSecrets: %v", err)
	}
	provider, err := adminidentity.NewHMACProvider(adminidentity.HMACProviderConfig{Issuer: adminIssuer, Secrets: secrets, Now: now, TestMode: true})
	if err != nil {
		t.Fatalf("NewHMACProvider: %v", err)
	}
	sessions, err := adminidentity.NewSessionStore(adminidentity.SessionConfig{Now: now, Secrets: secrets})
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	principals := adminidentity.NewPrincipalStore()
	for _, r := range adminrbac.Roles {
		if err := principals.Put(adminidentity.Principal{
			AdminID: "adm-" + string(r), SSOSubject: "sso|" + string(r), MFAEnrolled: true,
			Status: adminidentity.StatusActive, CreatedAt: now(),
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	idp, err := adminidentity.NewIdPFixture(adminIssuer, secrets, now)
	if err != nil {
		t.Fatalf("NewIdPFixture: %v", err)
	}
	authn, err := adminidentity.NewAuthenticator(provider, principals, sessions, nil)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}

	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(`{"version":"v1","plans":[{"plan_id":"team","display_name":"Team","rank":1,"features":["cli"],"limits":{},"price_refs":{}}]}`)); err != nil {
		t.Fatalf("PublishJSON: %v", err)
	}
	resolver := plancfg.NewResolver(src, plancfg.NewMemAudit())
	resolver.SetClock(now)
	if _, err := resolver.Reload("fixture"); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	accounts := account.NewMemStore()
	if _, err := accounts.Create(account.Account{CustomerID: "tenant-acme", ProviderCustomerHandle: "cus_acme", ActivePlanID: "team", PlanConfigVersion: resolver.Version(), CreatedAt: now()}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	admission, err := adminops.NewAdmission(accounts, nil)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	tenants, err := adminops.NewTenantService(exec, accounts, resolver, admission)
	if err != nil {
		t.Fatalf("NewTenantService: %v", err)
	}

	adminAPI, err := api.NewAdminAPI(api.AdminDeps{
		PlatformCredential: platformCred, Authenticator: authn, Sessions: sessions, Gate: gate,
		Executor: exec, Tenants: tenants, TestModeIdP: idp, Now: now,
	})
	if err != nil {
		t.Fatalf("NewAdminAPI: %v", err)
	}
	return &adminStack{api: adminAPI, sessions: sessions, idp: idp, clk: now}
}

// login returns a live admin session token for a role, via the real SSO+MFA exchange.
func (s *adminStack) login(t *testing.T, role adminrbac.Role) string {
	t.Helper()
	assertion, err := s.idp.Assert(context.Background(), "sso|"+string(role), "webauthn")
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	body, _ := json.Marshal(map[string]any{"assertion": assertion})
	rec := s.do(t, http.MethodPost, "/admin/api/session", body, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		SessionToken string `json:"session_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("login decode: %v", err)
	}
	return out.SessionToken
}

// do issues a request through the composed handler, attaching credentials as given.
func (s *adminStack) do(t *testing.T, method, path string, body []byte, cred, session string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
	}
	// The platform credential defaults to the real one for every call except where a test overrides it,
	// because the BFF always presents it.
	if cred == "" {
		cred = platformCred
	}
	if cred != "-" {
		r.Header.Set(api.HeaderPlatformCredential, cred)
	}
	if session != "" {
		r.Header.Set(api.HeaderAdminSession, session)
	}
	rec := httptest.NewRecorder()
	s.api.Handler.ServeHTTP(rec, r)
	return rec
}

// TestPlatformCredentialGuardsEverything: without the BFF credential, no route is reachable — and the
// refusal comes before routing, so it does not confirm which paths exist.
func TestPlatformCredentialGuardsEverything(t *testing.T) {
	s := newAdminStack(t)
	for _, path := range []string{"/admin/api/me", "/admin/api/tenants", "/admin/api/session", "/admin/api/does-not-exist"} {
		rec := s.do(t, http.MethodGet, path, nil, "-", "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without the platform credential: status %d, want 401", path, rec.Code)
		}
		rec = s.do(t, http.MethodGet, path, nil, "wrong-credential", "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s with a wrong credential: status %d, want 401", path, rec.Code)
		}
	}
	// Health is the one open path, so a load balancer can probe the BFF.
	if rec := s.do(t, http.MethodGet, "/admin/api/healthz", nil, "-", ""); rec.Code != http.StatusOK {
		t.Errorf("healthz without credential: status %d, want 200", rec.Code)
	}
}

// TestUnauthenticatedRouteIsRefused: with the platform credential but no session, a capability route
// is refused (the server half of FR20's redirect — the BFF turns this 401 into a sign-in redirect).
func TestUnauthenticatedRouteIsRefused(t *testing.T) {
	s := newAdminStack(t)
	rec := s.do(t, http.MethodGet, "/admin/api/tenants", nil, "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tenants with no session: status %d, want 401", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["kind"] != "auth" {
		t.Errorf("error kind = %v, want auth (so the BFF redirects rather than rendering)", body["kind"])
	}
}

// TestRevokedSessionIsDeniedAtTheNextRequest is FR2 at the HTTP boundary.
func TestRevokedSessionIsDeniedAtTheNextRequest(t *testing.T) {
	s := newAdminStack(t)
	token := s.login(t, adminrbac.RolePlatformSRE)

	// The session works.
	if rec := s.do(t, http.MethodGet, "/admin/api/tenants", nil, "", token); rec.Code != http.StatusOK {
		t.Fatalf("tenants with a live session: status %d", rec.Code)
	}
	// Revoke it, then the very next request is denied.
	if rec := s.do(t, http.MethodDelete, "/admin/api/session", nil, "", token); rec.Code != http.StatusOK {
		t.Fatalf("logout: status %d", rec.Code)
	}
	rec := s.do(t, http.MethodGet, "/admin/api/tenants", nil, "", token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tenants after revocation: status %d, want 401", rec.Code)
	}
}

// TestTenantSessionReachesNoAdminCapability is FR21: a customer session presented to the admin BFF is
// refused. A tenant session is not even the same KIND of token — it does not verify against the admin
// signing key — so it lands in the unauthenticated branch.
func TestTenantSessionReachesNoAdminCapability(t *testing.T) {
	s := newAdminStack(t)
	// Whatever a tenant session looks like, it is not an admin session token. A plausible-looking
	// bearer string stands in for one.
	tenantish := "tenant-session|tenant-acme|role=admin"
	rec := s.do(t, http.MethodGet, "/admin/api/tenants", nil, "", tenantish)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a tenant session reached the admin surface: status %d, want 401", rec.Code)
	}
	// And the type system agrees: an auth.Principal is not an adminidentity.Session, so even placed on a
	// context it authorizes nothing here.
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-acme", Role: "admin"})
	if _, ok := adminidentity.SessionFrom(ctx); ok {
		t.Fatal("a tenant principal was visible as an admin session")
	}
}

// TestNoMFAIsRefusedAtLogin: FR1 at the HTTP boundary — an SSO-only assertion issues no session.
func TestNoMFAIsRefusedAtLogin(t *testing.T) {
	s := newAdminStack(t)
	assertion, err := s.idp.AssertWithoutMFA(context.Background(), "sso|platform_sre")
	if err != nil {
		t.Fatalf("AssertWithoutMFA: %v", err)
	}
	body, _ := json.Marshal(map[string]any{"assertion": assertion})
	rec := s.do(t, http.MethodPost, "/admin/api/session", body, "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login without MFA: status %d, want 401", rec.Code)
	}
}

// TestMeServesTheSamePermissionMapTheGateEnforces: the console renders from what /me returns, so it
// must carry the whole permission map and the friction classification (FR22).
func TestMeServesTheSamePermissionMapTheGateEnforces(t *testing.T) {
	s := newAdminStack(t)
	token := s.login(t, adminrbac.RoleSupport)
	rec := s.do(t, http.MethodGet, "/admin/api/me", nil, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: status %d", rec.Code)
	}
	var view api.AdminIdentityView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.AdminID != "adm-support" {
		t.Errorf("me admin_id = %q", view.AdminID)
	}
	if len(view.PermissionMap) != len(adminrbac.Roles) {
		t.Errorf("permission map has %d roles, want %d — the console renders from this map", len(view.PermissionMap), len(adminrbac.Roles))
	}
	if len(view.Friction) != len(adminrbac.Capabilities) {
		t.Errorf("friction has %d entries, want %d capabilities", len(view.Friction), len(adminrbac.Capabilities))
	}
	// Support's rendered capabilities never include a destructive one.
	for _, c := range view.Capabilities {
		if c == adminrbac.CapKillSwitch || c == adminrbac.CapBillingCorrect {
			t.Errorf("Support's /me offered destructive capability %q", c)
		}
	}
	if view.Console == "" {
		t.Error("me does not identify the console for the operator chrome")
	}
}

// TestSuspendThroughTheHTTPSurface: a full command round-trip, including the four-state error mapping.
func TestSuspendThroughTheHTTPSurface(t *testing.T) {
	s := newAdminStack(t)
	token := s.login(t, adminrbac.RolePlatformSRE)

	// No reason ⇒ friction (428), not a 500 and not an empty 200.
	body, _ := json.Marshal(map[string]any{"confirmed": true})
	rec := s.do(t, http.MethodPost, "/admin/api/tenants/tenant-acme/suspend", body, "", token)
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("suspend with no reason: status %d, want 428", rec.Code)
	}

	// A denied action for the wrong role ⇒ 403 with the escalation path.
	supportToken := s.login(t, adminrbac.RoleSupport)
	body, _ = json.Marshal(map[string]any{"reason": "x", "confirmed": true})
	rec = s.do(t, http.MethodPost, "/admin/api/tenants/tenant-acme/suspend", body, "", supportToken)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Support suspend: status %d, want 403", rec.Code)
	}
	var denied map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &denied)
	if denied["kind"] != "denied" {
		t.Errorf("denied kind = %v, want denied", denied["kind"])
	}

	// The real thing ⇒ 200 with a receipt.
	body, _ = json.Marshal(map[string]any{"reason": "incident INC-1: regressions", "confirmed": true})
	rec = s.do(t, http.MethodPost, "/admin/api/tenants/tenant-acme/suspend", body, "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("suspend: status %d: %s", rec.Code, rec.Body.String())
	}
}

// TestAdminAPIRequiresACredential: an admin API built with no platform credential is refused at
// construction — an unauthenticated operator surface is the anti-pattern P8 removes.
func TestAdminAPIRequiresACredential(t *testing.T) {
	if _, err := api.NewAdminAPI(api.AdminDeps{}); err == nil {
		t.Fatal("an admin API was built with no platform credential")
	}
}
