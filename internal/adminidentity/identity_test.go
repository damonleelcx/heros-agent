package adminidentity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/auth"
)

// identity_test.go covers task 1.3 — the FR1/FR2 authentication and session invariants:
//
//	no MFA                     ⇒ denied, no session, logged
//	SSO + MFA                  ⇒ session issued for an ADMIN principal
//	customer-auth principal    ⇒ reaches no admin capability
//	expired session            ⇒ denied at the NEXT request
//	revoked session            ⇒ denied at the NEXT request, no grace, logged

const (
	testIssuer  = "https://admin-idp.test.heros.internal"
	otherIssuer = "https://customer-idp.test.heros.internal"
)

// clock is a hand-advanced clock. Tests advance it rather than sleeping — a test that sleeps for a
// session TTL is a test that gets deleted.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// stack is the wired identity layer under test.
type stack struct {
	clk        *clock
	secrets    adminidentity.Secrets
	idp        *adminidentity.IdPFixture
	principals *adminidentity.PrincipalStore
	sessions   *adminidentity.SessionStore
	authn      *adminidentity.Authenticator
	observer   *adminidentity.MemoryObserver
}

func newStack(t *testing.T, ttl time.Duration) *stack {
	t.Helper()
	clk := &clock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
	secrets, err := adminidentity.FixtureSecrets("sso-signing-key", "mfa-signing-key", "session-signing-key")
	if err != nil {
		t.Fatalf("FixtureSecrets: %v", err)
	}
	obs := adminidentity.NewMemoryObserver()
	provider, err := adminidentity.NewHMACProvider(adminidentity.HMACProviderConfig{
		Issuer: testIssuer, Secrets: secrets, Now: clk.now, TestMode: true,
	})
	if err != nil {
		t.Fatalf("NewHMACProvider: %v", err)
	}
	sessions, err := adminidentity.NewSessionStore(adminidentity.SessionConfig{
		TTL: ttl, Now: clk.now, Secrets: secrets, Observer: obs,
	})
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	principals := adminidentity.NewPrincipalStore()
	if err := principals.Put(adminidentity.Principal{
		AdminID: "adm-support-1", SSOSubject: "sso|support-1", MFAEnrolled: true,
		Status: adminidentity.StatusActive, CreatedAt: clk.now(),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	idp, err := adminidentity.NewIdPFixture(testIssuer, secrets, clk.now)
	if err != nil {
		t.Fatalf("NewIdPFixture: %v", err)
	}
	authn, err := adminidentity.NewAuthenticator(provider, principals, sessions, obs)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return &stack{clk: clk, secrets: secrets, idp: idp, principals: principals, sessions: sessions, authn: authn, observer: obs}
}

// TestSSOWithoutMFAIssuesNoSession is FR1's load-bearing denial: a VALID SSO assertion, presented
// without a verified factor, must issue nothing.
func TestSSOWithoutMFAIssuesNoSession(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, adminidentity.DefaultTTL)

	assertion, err := s.idp.AssertWithoutMFA(ctx, "sso|support-1")
	if err != nil {
		t.Fatalf("AssertWithoutMFA: %v", err)
	}
	sess, token, err := s.authn.Authenticate(ctx, assertion)
	if !errors.Is(err, adminidentity.ErrMFARequired) {
		t.Fatalf("Authenticate without MFA: err = %v, want ErrMFARequired", err)
	}
	if token != "" || sess.SessionID != "" {
		t.Fatalf("a session was issued without MFA: %+v token=%q", sess, token)
	}
	if got := s.observer.Count(adminidentity.EventLoginDeniedNoMFA); got != 1 {
		t.Errorf("no-MFA denials logged = %d, want 1", got)
	}

	// And no admin capability is reachable: the empty token authorizes nothing.
	if _, err := s.sessions.Authorize(ctx, token); err == nil {
		t.Fatal("an empty token authorized a session")
	}
}

// TestSSOPlusMFAIssuesAnAdminSession is the positive half of FR1 — and asserts the principal is an
// ADMIN principal, with no tenant identity anywhere on it.
func TestSSOPlusMFAIssuesAnAdminSession(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, adminidentity.DefaultTTL)

	assertion, err := s.idp.Assert(ctx, "sso|support-1", "webauthn")
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	sess, token, err := s.authn.Authenticate(ctx, assertion)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if sess.AdminID != "adm-support-1" {
		t.Errorf("session admin_id = %q, want adm-support-1", sess.AdminID)
	}
	if sess.MFAFactor != "webauthn" {
		t.Errorf("session mfa factor = %q, want webauthn", sess.MFAFactor)
	}
	if !sess.ExpiresAt.After(sess.IssuedAt) {
		t.Error("session has no positive lifetime")
	}
	live, err := s.sessions.Authorize(ctx, token)
	if err != nil {
		t.Fatalf("Authorize a fresh session: %v", err)
	}
	if live.SessionID != sess.SessionID {
		t.Errorf("authorized a different session: %q vs %q", live.SessionID, sess.SessionID)
	}

	// The principal behind it is an admin principal — the store that resolved it holds no tenant.
	p, ok := s.principals.ByID(sess.AdminID)
	if !ok || p.SSOSubject != "sso|support-1" {
		t.Fatalf("admin principal not resolvable from the session: %+v ok=%v", p, ok)
	}
}

// TestCustomerPrincipalCannotReachAdminCapability walks FR1's third scenario. A tenant principal —
// with the most privileged tenant-side role string there is — is placed on the request context, and
// the admin identity gate still finds no admin session.
func TestCustomerPrincipalCannotReachAdminCapability(t *testing.T) {
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{
		TenantID: "tenant-enterprise-1", Role: "admin", APIKeyID: "key-1",
	})
	if p, ok := auth.PrincipalFrom(ctx); !ok || !p.IsAdmin() {
		t.Fatal("fixture is wrong: the tenant principal should be present and tenant-side admin")
	}
	if _, ok := adminidentity.SessionFrom(ctx); ok {
		t.Fatal("a customer-auth principal was visible as an admin session")
	}
	if _, err := adminidentity.RequireSession(ctx); !errors.Is(err, adminidentity.ErrNoAdminSession) {
		t.Fatalf("RequireSession on a tenant context: err = %v, want ErrNoAdminSession", err)
	}
}

// TestAssertionFromTheCustomerIdPIsRefused closes the other half of the separation: an assertion that
// verifies perfectly but was minted by a different issuer is not an admin login.
func TestAssertionFromTheCustomerIdPIsRefused(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, adminidentity.DefaultTTL)

	other, err := adminidentity.NewIdPFixture(otherIssuer, s.secrets, s.clk.now)
	if err != nil {
		t.Fatalf("NewIdPFixture: %v", err)
	}
	assertion, err := other.Assert(ctx, "sso|support-1", "totp")
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, _, err := s.authn.Authenticate(ctx, assertion); !errors.Is(err, adminidentity.ErrAssertionInvalid) {
		t.Fatalf("Authenticate with a foreign issuer: err = %v, want ErrAssertionInvalid", err)
	}
}

// TestExpiredSessionIsDeniedAtTheNextRequest is FR2's first scenario.
func TestExpiredSessionIsDeniedAtTheNextRequest(t *testing.T) {
	ctx := context.Background()
	ttl := 15 * time.Minute
	s := newStack(t, ttl)

	assertion, err := s.idp.Assert(ctx, "sso|support-1", "totp")
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	_, token, err := s.authn.Authenticate(ctx, assertion)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := s.sessions.Authorize(ctx, token); err != nil {
		t.Fatalf("the session should be live before its TTL elapses: %v", err)
	}

	s.clk.advance(ttl) // exactly at expiry: the boundary is closed, not open.
	if _, err := s.sessions.Authorize(ctx, token); !errors.Is(err, adminidentity.ErrSessionExpired) {
		t.Fatalf("Authorize at expiry: err = %v, want ErrSessionExpired", err)
	}
	if got := s.observer.Count(adminidentity.EventSessionDeniedExpired); got != 1 {
		t.Errorf("expiry denials logged = %d, want 1", got)
	}

	// Re-authentication (SSO + MFA) yields a fresh session — the recovery path the scenario names.
	fresh, err := s.idp.Assert(ctx, "sso|support-1", "totp")
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, newToken, err := s.authn.Authenticate(ctx, fresh); err != nil || newToken == "" {
		t.Fatalf("re-authentication failed: %v", err)
	}
}

// TestRevokedSessionIsDeniedImmediately is FR2's second scenario: no grace period at all.
func TestRevokedSessionIsDeniedImmediately(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, adminidentity.DefaultTTL)

	assertion, err := s.idp.Assert(ctx, "sso|support-1", "totp")
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	sess, token, err := s.authn.Authenticate(ctx, assertion)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if err := s.sessions.Revoke(sess.SessionID, "adm-superadmin-1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// The very next request — the clock has not moved at all, so no grace window can hide behind time.
	if _, err := s.sessions.Authorize(ctx, token); !errors.Is(err, adminidentity.ErrSessionRevoked) {
		t.Fatalf("Authorize after revocation: err = %v, want ErrSessionRevoked", err)
	}
	if got := s.observer.Count(adminidentity.EventSessionDeniedRevoked); got != 1 {
		t.Errorf("revocation denials logged = %d, want 1", got)
	}
}

// TestForgedSessionTokenIsRefused: the session id is verified by signature, so a caller cannot mint
// one by guessing an id.
func TestForgedSessionTokenIsRefused(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, adminidentity.DefaultTTL)

	assertion, err := s.idp.Assert(ctx, "sso|support-1", "totp")
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	sess, _, err := s.authn.Authenticate(ctx, assertion)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	// The real session id, with a signature the caller made up.
	forged := sess.SessionID + ".0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := s.sessions.Authorize(ctx, forged); !errors.Is(err, adminidentity.ErrSessionUnknown) {
		t.Fatalf("Authorize a forged token: err = %v, want ErrSessionUnknown", err)
	}
}

// TestDisabledPrincipalCannotAuthenticate: offboarding is enforced at the login path, and revoking
// live sessions ends access that is already in flight.
func TestDisabledPrincipalCannotAuthenticate(t *testing.T) {
	ctx := context.Background()
	s := newStack(t, adminidentity.DefaultTTL)

	assertion, err := s.idp.Assert(ctx, "sso|support-1", "totp")
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	_, token, err := s.authn.Authenticate(ctx, assertion)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if err := s.principals.Disable("adm-support-1"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if n := s.sessions.RevokeAllFor("adm-support-1", "adm-superadmin-1"); n != 1 {
		t.Errorf("RevokeAllFor revoked %d sessions, want 1", n)
	}
	if _, err := s.sessions.Authorize(ctx, token); !errors.Is(err, adminidentity.ErrSessionRevoked) {
		t.Fatalf("an offboarded admin's live session survived: %v", err)
	}
	next, err := s.idp.Assert(ctx, "sso|support-1", "totp")
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if _, _, err := s.authn.Authenticate(ctx, next); !errors.Is(err, adminidentity.ErrPrincipalDisabled) {
		t.Fatalf("Authenticate as a disabled principal: err = %v, want ErrPrincipalDisabled", err)
	}
}

// TestSessionTTLIsBounded: a config typo cannot turn a "short-lived" session into a week.
func TestSessionTTLIsBounded(t *testing.T) {
	secrets, err := adminidentity.FixtureSecrets("a", "b", "c")
	if err != nil {
		t.Fatalf("FixtureSecrets: %v", err)
	}
	if _, err := adminidentity.NewSessionStore(adminidentity.SessionConfig{
		TTL: 7 * 24 * time.Hour, Secrets: secrets,
	}); err == nil {
		t.Fatal("a week-long admin session TTL was accepted")
	}
	if _, err := adminidentity.NewSessionStore(adminidentity.SessionConfig{Secrets: nil}); err == nil {
		t.Fatal("a session store was built with no secrets source")
	}
}

// TestSecretsComeFromTheManagerAndNeverAppearInDescribe: the readiness surface names the door, never
// what is behind it (task 13.2's rule, enforced at the identity layer where the keys live).
func TestSecretsComeFromTheManagerAndNeverAppearInDescribe(t *testing.T) {
	const sessionKey = "session-signing-key-value"
	secrets, err := adminidentity.FixtureSecrets("sso-k", "mfa-k", sessionKey)
	if err != nil {
		t.Fatalf("FixtureSecrets: %v", err)
	}
	info := secrets.Describe()
	if info.Kind == "" {
		t.Error("the secrets source does not name itself — /readyz cannot report which door is live")
	}
	for _, s := range []string{info.Kind, info.Detail} {
		if s == sessionKey {
			t.Fatalf("Describe leaked a signing key: %q", s)
		}
	}
	// And the keys really are reachable only by asking at the moment of use.
	got, err := secrets.SessionSigningKey(context.Background())
	if err != nil || string(got) != sessionKey {
		t.Fatalf("SessionSigningKey = %q, %v", got, err)
	}
}
