package api

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/mailer"
	"github.com/heros-foreal/agentd/internal/password"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

// passwordauth_test.go exercises the seven routes against the REAL stores and the REAL argon2id verifier.
//
// Nothing here is faked except the clock and the mail transport — and the mail "fake" is `OperatorMailer`,
// which is the code a deployment with no SMTP actually runs, so reading `Undelivered()` asserts against the
// production path rather than against a test double written to agree with the test.

const goodPassword = "a reasonable passphrase"

func newPasswordSurface(t *testing.T, selfServe bool) (*Server, *testSurface, *mailer.OperatorMailer) {
	t.Helper()
	s, surf := newSurface(t, selfServe)
	// A logger into nowhere: the WARN lines are asserted in internal/mailer's own tests, and repeating
	// them here would couple this file to that wording.
	op := mailer.NewOperatorMailer(log.New(&bytes.Buffer{}, "", 0))
	surf.mail = op
	return s, surf, op
}

func pwPost(t *testing.T, s *Server, path string, body any) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return doReq(t, s, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b)))
}

// linkFrom pulls the token out of the one message the operator record holds for a purpose.
func linkFrom(t *testing.T, op *mailer.OperatorMailer, purpose mailer.Purpose) string {
	t.Helper()
	for _, rec := range op.Undelivered() {
		if rec.Purpose != purpose {
			continue
		}
		i := strings.Index(rec.Body, "?t=")
		if i < 0 {
			t.Fatalf("a %s message carries no token:\n%s", purpose, rec.Body)
		}
		return strings.Fields(rec.Body[i+3:])[0]
	}
	t.Fatalf("no %s message was produced; held: %+v", purpose, op.Undelivered())
	return ""
}

func signUp(t *testing.T, s *Server, email string) (int, map[string]any) {
	t.Helper()
	return pwPost(t, s, "/api/v1/auth/password/signup", map[string]any{
		"name": "Acme Inc", "email": email, "password": goodPassword,
	})
}

// ── sign-up ─────────────────────────────────────────────────────────────────────────────────────────

// The whole point of the phase, asserted end to end: a person with nothing creates an account and can then
// sign in with it. No operator, no cluster access, no shared string.
func TestSignUpThenSignIn(t *testing.T) {
	s, surf, op := newPasswordSurface(t, true)

	code, body := signUp(t, s, "Priya@Example.com")
	if code != http.StatusCreated {
		t.Fatalf("sign-up status %d — %v", code, body)
	}
	tenantID, _ := body["tenant_id"].(string)
	userID, _ := body["user_id"].(string)
	if tenantID == "" || userID == "" {
		t.Fatalf("sign-up returned no organization or person: %v", body)
	}
	if body["email"] != "priya@example.com" {
		t.Errorf("the address was not normalised: %v", body["email"])
	}
	if body["email_verified"] != false {
		t.Error("a brand new address reported itself as confirmed")
	}
	// 🔴 The stored value is argon2id, never the minted-secret hash.
	rec, err := surf.store.GetPassword(userID)
	if err != nil {
		t.Fatalf("no password was stored: %v", err)
	}
	// 🔴 argon2id-tagged, which no SHA-256 hex string can be. An earlier version of this also asserted
	// `rec.Encoded != tenancy.HashSecret(goodPassword)` — and `internal/password`'s AST fence failed the
	// build for it, correctly: the fence bans passing a password-named value to the minted-secret hash, and
	// it cannot tell an assertion from a mistake. The assertion was redundant anyway (a SHA-256 hex string
	// does not start with `$argon2id$`), and adding an exemption to a fence in order to keep a redundant
	// line is how fences stop meaning anything. `password.TestParseRejectsForeignEncodings` covers the
	// specific case directly.
	if !strings.HasPrefix(rec.Encoded, "$argon2id$") {
		t.Fatalf("the stored password is not argon2id: %q", rec.Encoded)
	}

	code, in := pwPost(t, s, "/api/v1/auth/password/signin", map[string]any{
		"email": "priya@example.com", "password": goodPassword,
	})
	if code != http.StatusOK {
		t.Fatalf("sign-in status %d — %v", code, in)
	}
	if in["tenant_id"] != tenantID || in["user_id"] != userID {
		t.Fatalf("sign-in resolved a different principal: %v", in)
	}
	if in["role"] != string(tenancy.RoleOwner) {
		t.Errorf("the person who created the organization is not its owner: %v", in["role"])
	}
	// No credential was asked for, so none was minted — the console's case.
	if _, minted := in["credential"]; minted {
		t.Error("a credential was minted for a caller that did not ask for one")
	}

	// The confirmation went out, and confirming it works.
	token := linkFrom(t, op, mailer.PurposeVerifyEmail)
	code, v := pwPost(t, s, "/api/v1/auth/password/verify", map[string]any{"token": token})
	if code != http.StatusOK || v["email_verified"] != true {
		t.Fatalf("confirmation failed: %d %v", code, v)
	}
	// Single use.
	if code, _ := pwPost(t, s, "/api/v1/auth/password/verify", map[string]any{"token": token}); code == http.StatusOK {
		t.Fatal("a confirmation link worked twice")
	}
}

// 🔴 Registration must not be an oracle for "does this address have an account here". The response is
// byte-identical; the information goes to the address instead.
func TestSignUpDoesNotDiscloseAnExistingAccount(t *testing.T) {
	s, surf, op := newPasswordSurface(t, true)
	if code, body := signUp(t, s, "priya@example.com"); code != http.StatusCreated {
		t.Fatalf("first sign-up: %d %v", code, body)
	}
	before, _ := surf.store.ListTenants()

	code, second := signUp(t, s, "priya@example.com")
	if code != http.StatusCreated {
		t.Fatalf("a duplicate sign-up answered %d — it must be indistinguishable from the first", code)
	}
	if _, leaks := second["tenant_id"]; leaks {
		t.Errorf("the duplicate response carries an organization id, which the first also carries — "+
			"they must not differ in shape either: %v", second)
	}
	after, _ := surf.store.ListTenants()
	if len(after) != len(before) {
		t.Fatalf("a duplicate sign-up created %d extra organization(s)", len(after)-len(before))
	}
	// The one party entitled to know was told.
	found := false
	for _, rec := range op.Undelivered() {
		if rec.Purpose == mailer.PurposeSignupAttempt && rec.To == "priya@example.com" {
			found = true
		}
	}
	if !found {
		t.Fatal("nobody was told that somebody tried to register their address")
	}
}

func TestSignUpIsRefusedWhereSelfServeIsOff(t *testing.T) {
	s, _, _ := newPasswordSurface(t, false)
	code, body := signUp(t, s, "priya@example.com")
	if code != http.StatusForbidden || body["reason_code"] != ReasonSelfServeDisabled {
		t.Fatalf("status %d reason %v, want 403 %s", code, body["reason_code"], ReasonSelfServeDisabled)
	}
}

// The posture is checked BEFORE the address is looked up, so an install with sign-up off does not become a
// way to ask whether an address is registered.
func TestSignUpOffDoesNotBecomeAnExistenceOracle(t *testing.T) {
	s, surf, _ := newPasswordSurface(t, true)
	if code, _ := signUp(t, s, "priya@example.com"); code != http.StatusCreated {
		t.Fatal("setup sign-up failed")
	}
	surf.selfServe = false
	known, _ := signUp(t, s, "priya@example.com")
	unknown, _ := signUp(t, s, "nobody@example.com")
	if known != unknown {
		t.Fatalf("a known address answered %d and an unknown one %d", known, unknown)
	}
}

func TestSignUpRefusesAWeakPassword(t *testing.T) {
	s, _, _ := newPasswordSurface(t, true)
	for _, pw := range []string{"short", "priya-is-here-ok", "aaaaaaaaaaaaaaa"} {
		code, body := pwPost(t, s, "/api/v1/auth/password/signup", map[string]any{
			"name": "Acme", "email": "priya@example.com", "password": pw,
		})
		if code != http.StatusBadRequest || body["reason_code"] != ReasonWeakPassword {
			t.Errorf("password %q: status %d reason %v, want 400 %s", pw, code, body["reason_code"], ReasonWeakPassword)
		}
	}
}

// ── sign-in ─────────────────────────────────────────────────────────────────────────────────────────

// 🔴 One answer for an unknown address and a wrong password. Byte-identical: status, reason code and prose.
func TestSignInDoesNotDiscloseWhetherAnAddressIsRegistered(t *testing.T) {
	s, _, _ := newPasswordSurface(t, true)
	if code, _ := signUp(t, s, "priya@example.com"); code != http.StatusCreated {
		t.Fatal("setup sign-up failed")
	}
	unknownCode, unknown := pwPost(t, s, "/api/v1/auth/password/signin", map[string]any{
		"email": "nobody@example.com", "password": goodPassword,
	})
	wrongCode, wrong := pwPost(t, s, "/api/v1/auth/password/signin", map[string]any{
		"email": "priya@example.com", "password": "a different passphrase",
	})
	if unknownCode != wrongCode {
		t.Fatalf("an unknown address answered %d and a wrong password %d", unknownCode, wrongCode)
	}
	if unknown["error"] != wrong["error"] || unknown["reason_code"] != wrong["reason_code"] {
		t.Fatalf("the two refusals differ:\n unknown: %v\n wrong:   %v", unknown, wrong)
	}
	if wrong["reason_code"] != ReasonBadCredentials {
		t.Errorf("reason_code=%v, want %q", wrong["reason_code"], ReasonBadCredentials)
	}
}

// The CLI's path: ask for a credential and get a PERSONAL one, which is what makes offboarding true in a
// terminal.
func TestSignInMintsAPersonalCredentialWhenALabelIsGiven(t *testing.T) {
	s, surf, _ := newPasswordSurface(t, true)
	code, up := signUp(t, s, "priya@example.com")
	if code != http.StatusCreated {
		t.Fatalf("sign-up: %v", up)
	}
	userID := up["user_id"].(string)

	code, in := pwPost(t, s, "/api/v1/auth/password/signin", map[string]any{
		"email": "priya@example.com", "password": goodPassword, "device_label": "priya@laptop (darwin/arm64)",
	})
	if code != http.StatusOK {
		t.Fatalf("sign-in: %d %v", code, in)
	}
	cred, _ := in["credential"].(map[string]any)
	if cred == nil {
		t.Fatal("no credential was minted for a caller that named a device")
	}
	if cred["kind"] != "personal" {
		t.Fatalf("credential kind = %v, want personal — a machine credential survives offboarding", cred["kind"])
	}
	token, _ := cred["token"].(string)
	if token == "" {
		t.Fatal("no plaintext was returned; it exists only in this response")
	}
	// It authenticates, and it names the person.
	stored, err := surf.store.ResolveCredential(tenancy.HashSecret(token))
	if err != nil {
		t.Fatalf("the minted credential does not resolve: %v", err)
	}
	if stored.UserID != userID || stored.Label != "priya@laptop (darwin/arm64)" {
		t.Fatalf("the credential does not name the person or the device: %+v", stored)
	}
}

// The lockout is the one distinguishable state, and it says how long. Both halves are asserted: an account
// that locks silently and an account that never locks are each wrong in their own way.
func TestRepeatedFailuresLockAndSaySo(t *testing.T) {
	s, surf, _ := newPasswordSurface(t, true)
	if code, _ := signUp(t, s, "priya@example.com"); code != http.StatusCreated {
		t.Fatal("setup sign-up failed")
	}
	var lastCode int
	var last map[string]any
	for i := 0; i < tenancy.DefaultLockout.Threshold; i++ {
		lastCode, last = pwPost(t, s, "/api/v1/auth/password/signin", map[string]any{
			"email": "priya@example.com", "password": "not the passphrase",
		})
	}
	if lastCode != http.StatusTooManyRequests || last["reason_code"] != ReasonAccountLocked {
		t.Fatalf("after %d failures: %d %v — the account did not lock",
			tenancy.DefaultLockout.Threshold, lastCode, last)
	}
	if msg, _ := last["error"].(string); !strings.Contains(msg, "minute") {
		t.Errorf("the lock message does not say how long: %q", msg)
	}
	// 🔴 And the CORRECT password is refused too — a lock that lets the real password through is not a lock.
	code, body := pwPost(t, s, "/api/v1/auth/password/signin", map[string]any{
		"email": "priya@example.com", "password": goodPassword,
	})
	if code != http.StatusTooManyRequests {
		t.Fatalf("the correct password was accepted while locked: %d %v", code, body)
	}
	// A reset clears it: the escape hatch the copy offers has to exist.
	userID, _ := surf.store.FindUserByEmail(tenancy.IssuerPassword, "priya@example.com")
	enc, _ := password.Hash("another reasonable passphrase")
	if _, err := surf.store.SetPassword(userID.UserID, enc, surf.Now()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if code, body := pwPost(t, s, "/api/v1/auth/password/signin", map[string]any{
		"email": "priya@example.com", "password": "another reasonable passphrase",
	}); code != http.StatusOK {
		t.Fatalf("a reset did not clear the lock: %d %v", code, body)
	}
}

// ── forgot / reset ──────────────────────────────────────────────────────────────────────────────────

func TestForgotAnswersIdenticallyForEveryAddress(t *testing.T) {
	s, _, op := newPasswordSurface(t, true)
	if code, _ := signUp(t, s, "priya@example.com"); code != http.StatusCreated {
		t.Fatal("setup sign-up failed")
	}
	knownCode, known := pwPost(t, s, "/api/v1/auth/password/forgot", map[string]any{"email": "priya@example.com"})
	unknownCode, unknown := pwPost(t, s, "/api/v1/auth/password/forgot", map[string]any{"email": "nobody@example.com"})
	garbageCode, garbage := pwPost(t, s, "/api/v1/auth/password/forgot", map[string]any{"email": "not an address"})
	if knownCode != unknownCode || knownCode != garbageCode {
		t.Fatalf("statuses differ: known=%d unknown=%d malformed=%d", knownCode, unknownCode, garbageCode)
	}
	if known["message"] != unknown["message"] || known["message"] != garbage["message"] {
		t.Fatalf("messages differ:\n %v\n %v\n %v", known, unknown, garbage)
	}
	// Only the known one caused mail.
	resets := 0
	for _, rec := range op.Undelivered() {
		if rec.Purpose == mailer.PurposeResetPassword {
			resets++
			if rec.To != "priya@example.com" {
				t.Errorf("a reset was sent to %s", rec.To)
			}
		}
	}
	if resets != 1 {
		t.Fatalf("%d reset messages were produced, want exactly 1", resets)
	}
}

// 🔴 The load-bearing assertion of the whole recovery path: a reset ends the sessions and personal
// credentials that person held, and DISCLOSES the machine credentials it left running.
func TestResetEndsEverythingThatPersonHeldAndSaysWhatItDidNot(t *testing.T) {
	s, surf, op := newPasswordSurface(t, true)
	code, up := signUp(t, s, "priya@example.com")
	if code != http.StatusCreated {
		t.Fatalf("sign-up: %v", up)
	}
	tenantID, userID := up["tenant_id"].(string), up["user_id"].(string)

	// Something she holds, and something the organization holds.
	personal, err := surf.store.CreateCredential(tenancy.Credential{
		CredentialID: "cred_personal", TenantID: tenantID, UserID: userID, Label: "old laptop",
		Role: tenancy.RoleOwner, Hash: tenancy.HashSecret("personal-secret"), CreatedAt: surf.Now(),
	})
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	if _, err := surf.store.CreateCredential(tenancy.Credential{
		CredentialID: "cred_ci", TenantID: tenantID, Label: "deploy key",
		Role: tenancy.RoleMember, Hash: tenancy.HashSecret("ci-secret"), CreatedAt: surf.Now(),
	}); err != nil {
		t.Fatalf("credential: %v", err)
	}
	if _, err := surf.store.CreateSession(tenancy.Session{
		TokenHash: tenancy.HashSecret("browser-token"), SessionID: "sess-1", TenantID: tenantID, UserID: userID,
		IssuedAt: surf.Now().UnixMilli(), ExpiresAt: surf.Now().Add(8 * time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatalf("session: %v", err)
	}

	if code, _ := pwPost(t, s, "/api/v1/auth/password/forgot", map[string]any{"email": "priya@example.com"}); code != http.StatusOK {
		t.Fatal("forgot failed")
	}
	token := linkFrom(t, op, mailer.PurposeResetPassword)

	code, out := pwPost(t, s, "/api/v1/auth/password/reset", map[string]any{
		"token": token, "password": "a different reasonable passphrase",
	})
	if code != http.StatusOK {
		t.Fatalf("reset: %d %v", code, out)
	}
	if out["sessions_revoked"].(float64) != 1 || out["credentials_revoked"].(float64) != 1 {
		t.Fatalf("the reset did not revoke what it claimed: %v", out)
	}
	left, _ := out["machine_credentials_untouched"].([]any)
	if len(left) != 1 {
		t.Fatalf("the machine credentials left running were not disclosed: %v", out)
	}
	if first, _ := left[0].(map[string]any); first["label"] != "deploy key" {
		t.Errorf("the disclosed credential is not named: %v", left[0])
	}

	// The old session and the old personal credential are dead at the NEXT request, with no grace period.
	sess, _ := surf.store.ResolveSession(tenancy.HashSecret("browser-token"))
	if sess.Live(surf.Now().UnixMilli()) {
		t.Error("a session survived the reset")
	}
	back, _ := surf.store.ResolveCredential(personal.Hash)
	if !back.Revoked() {
		t.Error("a personal credential survived the reset")
	}
	// The machine credential is untouched, which is what the disclosure promised.
	ci, _ := surf.store.ResolveCredential(tenancy.HashSecret("ci-secret"))
	if ci.Revoked() {
		t.Error("a machine credential was revoked — the disclosure was wrong in the safe direction, which is " +
			"still wrong: the customer's build breaks with no warning")
	}
	// A reset proves the address, so it confirms it.
	user, _ := surf.store.GetUser(userID)
	if !user.EmailVerified() {
		t.Error("completing a reset did not confirm the address it was delivered to")
	}
	// And the link is spent.
	if code, _ := pwPost(t, s, "/api/v1/auth/password/reset", map[string]any{
		"token": token, "password": "yet another reasonable passphrase",
	}); code == http.StatusOK {
		t.Fatal("a reset link worked twice")
	}
}

func TestResetRefusesAnUnusableLink(t *testing.T) {
	s, _, _ := newPasswordSurface(t, true)
	for _, tok := range []string{"", "never-existed", strings.Repeat("x", 64)} {
		code, body := pwPost(t, s, "/api/v1/auth/password/reset", map[string]any{
			"token": tok, "password": goodPassword,
		})
		if code != http.StatusBadRequest || body["reason_code"] != ReasonLinkUnusable {
			t.Errorf("token %q: %d %v, want 400 %s", tok, code, body["reason_code"], ReasonLinkUnusable)
		}
	}
}

// ── change ──────────────────────────────────────────────────────────────────────────────────────────

func TestChangeRequiresTheCurrentPasswordAndKeepsThisSession(t *testing.T) {
	s, surf, _ := newPasswordSurface(t, true)
	code, up := signUp(t, s, "priya@example.com")
	if code != http.StatusCreated {
		t.Fatalf("sign-up: %v", up)
	}
	tenantID, userID := up["tenant_id"].(string), up["user_id"].(string)

	mine, err := surf.store.CreateSession(tenancy.Session{
		TokenHash: tenancy.HashSecret("mine"), SessionID: "sess-mine", TenantID: tenantID, UserID: userID,
		IssuedAt: surf.Now().UnixMilli(), ExpiresAt: surf.Now().Add(8 * time.Hour).UnixMilli(),
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := surf.store.CreateSession(tenancy.Session{
		TokenHash: tenancy.HashSecret("other"), SessionID: "sess-other", TenantID: tenantID, UserID: userID,
		IssuedAt: surf.Now().UnixMilli(), ExpiresAt: surf.Now().Add(8 * time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatalf("session: %v", err)
	}

	// `APIKeyID` is the session id for a session-derived principal — see auth.principalFromSession.
	p := auth.Principal{TenantID: tenantID, UserID: userID, Role: string(tenancy.RoleOwner), APIKeyID: mine.SessionID}

	code, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/auth/password/change", map[string]any{
		"current_password": "wrong", "new_password": "a brand new passphrase",
	}, p))
	if code != http.StatusUnauthorized {
		t.Fatalf("a wrong current password was accepted: %d %v", code, body)
	}

	code, body = doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/auth/password/change", map[string]any{
		"current_password": goodPassword, "new_password": "a brand new passphrase",
	}, p))
	if code != http.StatusOK {
		t.Fatalf("change: %d %v", code, body)
	}
	if body["other_sessions_revoked"].(float64) != 1 {
		t.Fatalf("other sessions were not ended: %v", body)
	}
	// 🔴 This session survives. Signing somebody out of the page they just used teaches them nothing about
	// whether it worked.
	still, _ := surf.store.ResolveSession(tenancy.HashSecret("mine"))
	if !still.Live(surf.Now().UnixMilli()) {
		t.Error("the session that made the change was revoked by it")
	}
	gone, _ := surf.store.ResolveSession(tenancy.HashSecret("other"))
	if gone.Live(surf.Now().UnixMilli()) {
		t.Error("another session survived a password change")
	}
}

// A machine credential names nobody, so there is no person whose password could be changed.
func TestChangeRefusesAMachineCredential(t *testing.T) {
	s, _, _ := newPasswordSurface(t, true)
	code, up := signUp(t, s, "priya@example.com")
	if code != http.StatusCreated {
		t.Fatalf("sign-up: %v", up)
	}
	code, body := doReq(t, s, asPrincipal(t, http.MethodPost, "/api/v1/auth/password/change", map[string]any{
		"current_password": goodPassword, "new_password": "a brand new passphrase",
	}, auth.Principal{TenantID: up["tenant_id"].(string), Role: "member", APIKeyID: "cred_ci"}))
	if code != http.StatusForbidden || body["reason_code"] != ReasonNotAMember {
		t.Fatalf("%d %v, want 403 %s", code, body["reason_code"], ReasonNotAMember)
	}
}

// ── what never appears anywhere ─────────────────────────────────────────────────────────────────────

// 🔴 The submitted password must not reach a response body, on any route, on any path.
func TestNoResponseEverCarriesTheSubmittedPassword(t *testing.T) {
	s, _, op := newPasswordSurface(t, true)
	const secret = "a reasonable passphrase"

	bodies := []string{}
	record := func(code int, body map[string]any) {
		b, _ := json.Marshal(body)
		bodies = append(bodies, string(b))
	}
	record(signUp(t, s, "priya@example.com"))
	record(pwPost(t, s, "/api/v1/auth/password/signin", map[string]any{"email": "priya@example.com", "password": secret}))
	record(pwPost(t, s, "/api/v1/auth/password/signin", map[string]any{"email": "priya@example.com", "password": "wrong"}))
	record(pwPost(t, s, "/api/v1/auth/password/forgot", map[string]any{"email": "priya@example.com"}))
	record(pwPost(t, s, "/api/v1/auth/password/resend", map[string]any{"email": "priya@example.com"}))
	record(signUp(t, s, "priya@example.com"))

	for i, b := range bodies {
		if strings.Contains(b, secret) {
			t.Errorf("response %d carries the submitted password:\n%s", i, b)
		}
	}
	// And no message body carries it either.
	for _, rec := range op.Undelivered() {
		if strings.Contains(rec.Body, secret) {
			t.Errorf("a %s message carries the password:\n%s", rec.Purpose, rec.Body)
		}
	}
}

// A deployment with no mail configured still works, and never silently drops. This is the fence at the
// HTTP layer — `internal/mailer` holds the same one at the seam.
func TestAnUnconfiguredDeploymentStillSignsPeopleUpAndHoldsTheLinks(t *testing.T) {
	s, surf, op := newPasswordSurface(t, true)
	if op.Configured() {
		t.Fatal("the test surface reported itself able to send")
	}
	if code, body := signUp(t, s, "priya@example.com"); code != http.StatusCreated {
		t.Fatalf("sign-up failed on a deployment with no mail: %d %v", code, body)
	}
	if len(op.Undelivered()) == 0 {
		t.Fatal("the confirmation link was discarded rather than held for the operator")
	}
	_ = surf
}
