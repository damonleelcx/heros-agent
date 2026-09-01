package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/heros/internal/tenancy"
)

// account_test.go covers the three links that stand in for a password.

// invite sends an invitation and returns the token out of the real mail.
func (hz *harness) invite(t *testing.T, as *http.Cookie, email string, role tenancy.Role) string {
	t.Helper()
	rec := hz.do(t, "POST", "/api/members/invite",
		`{"email":"`+email+`","role":"`+string(role)+`"}`, as)
	if rec.Code != http.StatusOK {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body.String())
	}
	return tokenFromLink(t, hz.mail.last(t), "invite")
}

// TestAnInvitationCarriesTheRoleTheInviterChose.
//
// # 🔴 The property being defended
//
// The person accepting sends a token and a password. Everything else about the account they are about to
// get — which organization, which address, which role — comes from the row the inviter created. If the
// role came from the request body, the first thing anybody would try is "owner", and the second is
// somebody else's organization.
//
// The request below sends both, and is expected to be ignored rather than refused: `acceptReq` has no
// field for either, so there is nothing to forget to validate.
func TestAnInvitationCarriesTheRoleTheInviterChose(t *testing.T) {
	hz := newHarness(t)
	owner, _ := hz.user(t, tenancy.Owner)
	invited := "newcomer-" + randSuffix() + "@example.test"
	token := hz.invite(t, owner, invited, tenancy.Viewer)

	rec := hz.do(t, "POST", "/api/auth/invitation/accept",
		`{"token":"`+token+`","password":"`+testPassword+`","role":"owner","email":"someone@else.test"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["role"] != "viewer" {
		t.Errorf("the accepted account is a %v; the invitation said viewer", body["role"])
	}
	if body["email"] != invited {
		t.Errorf("the account was created for %v, not the invited address %q", body["email"], invited)
	}
	if body["tenant"] != hz.tenant {
		t.Errorf("the account landed in %v, not the inviting organization", body["tenant"])
	}
}

// TestAnInvitationCannotMintAnOwner, at both layers that could stop it.
//
// 🔴 Checked in the store AND enforced by a database CHECK constraint, and both are asserted here. The
// store check is what produces a sentence a person can read; the constraint is what remains true if a
// future handler, a migration script or a support query gets it wrong. A rule with one enforcement point
// is a rule for as long as that one line survives refactoring.
func TestAnInvitationCannotMintAnOwner(t *testing.T) {
	hz := newHarness(t)
	owner, _ := hz.user(t, tenancy.Owner)

	rec := hz.do(t, "POST", "/api/members/invite",
		`{"email":"usurper@example.test","role":"owner"}`, owner)
	if rec.Code != http.StatusForbidden {
		t.Errorf("an owner could invite somebody straight to owner: %d %s", rec.Code, rec.Body.String())
	}

	// And the database refuses it even with the handler bypassed entirely.
	_, err := hz.db.ExecContext(context.Background(), `
		INSERT INTO invitations (id, token_hash, tenant, email, role, created_at, expires_at)
		VALUES ($1,$2,$3,$4,'owner', now(), now() + interval '1 day')`,
		"inv_fence"+randSuffix(), "hash"+randSuffix(), hz.tenant, "usurper@example.test")
	if err == nil {
		t.Error("the database accepted an invitation to the owner role; the CHECK constraint is missing")
	}
}

// TestAnInvitationTokenWorksOnce.
//
// A link in a mailbox is forwarded, synced to a phone, and scanned by a mail client that follows URLs.
// A second acceptance must not create a second account, and must not be possible for whoever else ends
// up holding the mail.
func TestAnInvitationTokenWorksOnce(t *testing.T) {
	hz := newHarness(t)
	owner, _ := hz.user(t, tenancy.Owner)
	token := hz.invite(t, owner, "once-"+randSuffix()+"@example.test", tenancy.Member)

	body := `{"token":"` + token + `","password":"` + testPassword + `"}`
	if rec := hz.do(t, "POST", "/api/auth/invitation/accept", body, nil); rec.Code != http.StatusOK {
		t.Fatalf("first acceptance failed: %d %s", rec.Code, rec.Body.String())
	}
	rec := hz.do(t, "POST", "/api/auth/invitation/accept", body, nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("the same invitation was accepted twice: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAcceptingAnInvitationProvesTheAddress.
//
// 🔴 The token arrived in a mail sent to that address and the person is holding it. Receipt IS the
// proof, so asking them to confirm the address that just confirmed itself would be a step whose only
// lesson is that confirmation steps are noise.
func TestAcceptingAnInvitationProvesTheAddress(t *testing.T) {
	hz := newHarness(t)
	owner, _ := hz.user(t, tenancy.Owner)
	invited := "proven-" + randSuffix() + "@example.test"
	token := hz.invite(t, owner, invited, tenancy.Member)

	if rec := hz.do(t, "POST", "/api/auth/invitation/accept",
		`{"token":"`+token+`","password":"`+testPassword+`"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body.String())
	}
	members, err := hz.Auth.ListMembers(context.Background(), hz.tenant)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members {
		if strings.EqualFold(m.Email, invited) {
			if !m.EmailVerified {
				t.Error("an address that received and used an invitation is still marked unconfirmed")
			}
			return
		}
	}
	t.Fatal("the invited account does not appear in the member list")
}

// TestReInvitingReplacesTheEarlierLink.
//
// 🔴 Re-inviting is what somebody does when the first mail went astray — and the reason it went astray
// might be that it went somewhere else. Leaving both links live means the one the inviter believes they
// replaced is still a way into the organization.
func TestReInvitingReplacesTheEarlierLink(t *testing.T) {
	hz := newHarness(t)
	owner, _ := hz.user(t, tenancy.Owner)
	email := "resend-" + randSuffix() + "@example.test"

	first := hz.invite(t, owner, email, tenancy.Member)
	second := hz.invite(t, owner, email, tenancy.Member)
	if first == second {
		t.Fatal("re-inviting reissued the same token")
	}
	rec := hz.do(t, "POST", "/api/auth/invitation/accept",
		`{"token":"`+first+`","password":"`+testPassword+`"}`, nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("the superseded invitation still works: %d %s", rec.Code, rec.Body.String())
	}
	if rec := hz.do(t, "POST", "/api/auth/invitation/accept",
		`{"token":"`+second+`","password":"`+testPassword+`"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("the replacement invitation does not work: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAnInvitationThatCannotBeSentIsNotCreated.
//
// 🔴 The token is returned once and never stored in usable form. An invitation whose mail failed is a
// row nobody can ever accept, listed in the console as pending — so the inviter believes somebody was
// invited and that person waits for a mail that was never sent. The failure has to surface where the
// person who clicked "invite" is looking.
func TestAnInvitationThatCannotBeSentIsNotCreated(t *testing.T) {
	hz := newHarness(t)
	owner, _ := hz.user(t, tenancy.Owner)
	hz.mail.fail = errors.New("relay refused the connection")

	email := "undeliverable-" + randSuffix() + "@example.test"
	rec := hz.do(t, "POST", "/api/members/invite", `{"email":"`+email+`","role":"member"}`, owner)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("a failed send reported %d; the inviter was not told: %s", rec.Code, rec.Body.String())
	}
	invs, err := hz.Auth.ListInvitations(context.Background(), hz.tenant)
	if err != nil {
		t.Fatal(err)
	}
	for _, inv := range invs {
		if strings.EqualFold(inv.Email, email) {
			t.Fatal("an invitation nobody can accept is listed as pending")
		}
	}
}

// ── forgetting a password ────────────────────────────────────────────────────────────────────────

// TestForgottenPasswordAnswersTheSameForAKnownAndAnUnknownAddress.
//
// # 🔴 What an unequal answer would be worth
//
// This endpoint takes an address, needs no credential, and can be called at whatever rate the network
// allows. If the reply differs between a real and an unknown address — different words, a different
// status, or merely a different amount of time — it answers "does this person have an account with you"
// for anybody. For a product that reads customers' private source code, the customer list is itself
// worth having.
func TestForgottenPasswordAnswersTheSameForAKnownAndAnUnknownAddress(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	known := "known-" + randSuffix() + "@example.test"
	if _, err := hz.Auth.CreateUser(context.Background(), hz.tenant, known, testPassword,
		tenancy.Member); err != nil {
		t.Fatal(err)
	}

	answers := map[string]string{}
	codes := map[string]int{}
	for name, addr := range map[string]string{
		"known":   known,
		"unknown": "nobody-" + randSuffix() + "@example.test",
	} {
		rec := hz.do(t, "POST", "/api/auth/password/forgot",
			`{"tenant":"`+hz.tenant+`","email":"`+addr+`"}`, nil)
		answers[name], codes[name] = rec.Body.String(), rec.Code
	}
	if answers["known"] != answers["unknown"] || codes["known"] != codes["unknown"] {
		t.Errorf("a known and an unknown address are distinguishable:\n  known:   %d %s\n  unknown: %d %s",
			codes["known"], answers["known"], codes["unknown"], answers["unknown"])
	}
	if codes["known"] != http.StatusOK {
		t.Errorf("the constant answer is %d, which is itself a signal", codes["known"])
	}
}

// TestAPasswordResetSignsOutEverySession.
//
// # 🔴 Why this is the point of the feature, not a detail of it
//
// The commonest reason to reset a password is that somebody else may have it. If existing sessions
// survive, the person who took the account keeps it: the reset changes the lock and leaves the intruder
// inside the house. Somebody resetting BECAUSE their laptop was stolen stays signed in on it.
//
// This product has shipped that exact bug before, in a configuration where sign-out worked and
// revocation did not — which is what made it dangerous, because two thirds of the sentence kept working.
// The reset mail makes this claim in writing, so it is asserted here rather than believed.
func TestAPasswordResetSignsOutEverySession(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	email := "reset-" + randSuffix() + "@example.test"
	if _, err := hz.Auth.CreateUser(context.Background(), hz.tenant, email, testPassword,
		tenancy.Member); err != nil {
		t.Fatal(err)
	}
	// Two sessions, as somebody with a laptop and a desktop would have.
	laptop := hz.signIn(t, email)
	desktop := hz.signIn(t, email)
	for _, c := range []*http.Cookie{laptop, desktop} {
		if rec := hz.do(t, "GET", "/api/members", "", c); rec.Code != http.StatusOK {
			t.Fatalf("a fresh session does not work: %d", rec.Code)
		}
	}

	before := hz.mail.count()
	if rec := hz.do(t, "POST", "/api/auth/password/forgot",
		`{"tenant":"`+hz.tenant+`","email":"`+email+`"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("forgot: %d", rec.Code)
	}
	// The mail is sent on a background goroutine, deliberately, so the response time carries no signal.
	waitForMail(t, hz, before+1)
	token := tokenFromLink(t, hz.mail.last(t), "reset")

	const newPassword = "an-entirely-different-password"
	if rec := hz.do(t, "POST", "/api/auth/password/reset",
		`{"token":"`+token+`","password":"`+newPassword+`"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}

	for name, c := range map[string]*http.Cookie{"laptop": laptop, "desktop": desktop} {
		if rec := hz.do(t, "GET", "/api/members", "", c); rec.Code != http.StatusUnauthorized {
			t.Errorf("the %s session survived a password reset (%d); whoever holds it still has the "+
				"account", name, rec.Code)
		}
	}
	// And the new password works, or the reset merely locked everybody out.
	rec := hz.do(t, "POST", "/api/auth/login",
		`{"tenant":"`+hz.tenant+`","email":"`+email+`","password":"`+newPassword+`"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the new password does not work: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAResetTokenWorksOnceAndIssuesNoSession.
//
// 🚫 No session is issued by a reset, deliberately. Somebody holding a reset link has proven they can
// read one mailbox; a link forwarded by accident would otherwise become a session. They sign in with the
// password they just chose, which proves they know it.
func TestAResetTokenWorksOnceAndIssuesNoSession(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	email := "once-reset-" + randSuffix() + "@example.test"
	if _, err := hz.Auth.CreateUser(context.Background(), hz.tenant, email, testPassword,
		tenancy.Member); err != nil {
		t.Fatal(err)
	}
	before := hz.mail.count()
	hz.do(t, "POST", "/api/auth/password/forgot", `{"tenant":"`+hz.tenant+`","email":"`+email+`"}`, nil)
	waitForMail(t, hz, before+1)
	token := tokenFromLink(t, hz.mail.last(t), "reset")

	rec := hz.do(t, "POST", "/api/auth/password/reset",
		`{"token":"`+token+`","password":"a-brand-new-long-password"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Error("a password reset issued a session; a forwarded link would become a login")
		}
	}
	again := hz.do(t, "POST", "/api/auth/password/reset",
		`{"token":"`+token+`","password":"yet-another-long-password"}`, nil)
	if again.Code != http.StatusGone {
		t.Fatalf("the same reset link worked twice: %d %s", again.Code, again.Body.String())
	}
}

// ── confirming an address ────────────────────────────────────────────────────────────────────────

// TestConfirmationTakesNoAddressFromTheRequest.
//
// 🔴 An endpoint that mails a link to an address in the request body is a way to send mail from this
// product to anybody who asks — a spam relay wearing the product's DKIM signature. The recipient is
// whatever address the session's own account holds, and the body is ignored.
func TestConfirmationTakesNoAddressFromTheRequest(t *testing.T) {
	hz := newHarness(t)
	owner, _ := hz.user(t, tenancy.Owner)
	// The owner created by the harness is unconfirmed, as an operator-typed address should be.
	rec := hz.do(t, "POST", "/api/auth/email/resend", `{"email":"victim@somewhere-else.test"}`, owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("resend: %d %s", rec.Code, rec.Body.String())
	}
	sent := hz.mail.last(t)
	if strings.Contains(sent.To, "somewhere-else.test") {
		t.Fatalf("the confirmation was addressed to a name from the request body: %s", sent.To)
	}
	// And confirming it actually marks the account.
	token := tokenFromLink(t, sent, "verify")
	if rec := hz.do(t, "POST", "/api/auth/email/verify", `{"token":"`+token+`"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", rec.Code, rec.Body.String())
	}
	status := decode(t, hz.do(t, "GET", "/api/auth/status", "", owner))
	if status["email_verified"] != true {
		t.Errorf("the address is still unconfirmed after confirming it: %v", status["email_verified"])
	}
	// Once. A confirmation link is worth little, but "worth little" is not "reusable".
	if rec := hz.do(t, "POST", "/api/auth/email/verify", `{"token":"`+token+`"}`, nil); rec.Code != http.StatusGone {
		t.Errorf("a confirmation link worked twice: %d", rec.Code)
	}
}

// TestAnUnconfirmedAddressBlocksNothing.
//
// # 🔴 A deliberate non-property, asserted so it is not quietly changed
//
// Mail is the least reliable component in this deployment. Making a session, a run or an approval depend
// on a confirmed address turns a mail outage into a lockout — including for the one account that could
// fix the mail. Confirmation is recorded and shown; the day there is a reason to gate something on it,
// that is a decision to make in the open, and this test failing is what makes it one.
func TestAnUnconfirmedAddressBlocksNothing(t *testing.T) {
	hz := newHarness(t)
	owner, _ := hz.user(t, tenancy.Owner)

	status := decode(t, hz.do(t, "GET", "/api/auth/status", "", owner))
	if status["email_verified"] == true {
		t.Fatal("the premise has changed: this account was expected to be unconfirmed")
	}
	for _, r := range apiRoutes {
		if r.Public || !tenancy.Can(tenancy.Owner, r.Needs) && r.Needs != "" {
			continue
		}
		rec := hz.do(t, r.Method, concretePath(r.Path), `{}`, owner)
		if rec.Code == http.StatusForbidden {
			t.Errorf("%s %s refused an owner whose address is merely unconfirmed: %s",
				r.Method, r.Path, rec.Body.String())
		}
	}
}

// TestTheStatusEndpointReportsCapabilitiesRatherThanLettingTheConsoleGuess.
//
// 🔴 The console renders its menus from this. A second copy of the capability table in JavaScript would
// disagree with the real one sooner or later, and the way it disagrees is a button that appears and then
// refuses — which reads as the product being broken rather than as the person lacking permission.
func TestTheStatusEndpointReportsCapabilitiesRatherThanLettingTheConsoleGuess(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	for _, role := range tenancy.Roles {
		c, _ := hz.user(t, role)
		status := decode(t, hz.do(t, "GET", "/api/auth/status", "", c))
		can, ok := status["can"].(map[string]any)
		if !ok {
			t.Fatalf("the status endpoint reports no capabilities for %s", role)
		}
		for _, cap := range tenancy.Capabilities {
			want := tenancy.Can(role, cap)
			if can[string(cap)] != want {
				t.Errorf("status says a %s may %q = %v; the table says %v",
					role, cap, can[string(cap)], want)
			}
		}
		if status["role"] != string(role) {
			t.Errorf("status reports role %v for a %s", status["role"], role)
		}
	}
}

// waitForMail waits for the background sender to have delivered n messages.
//
// 🔴 The password-reset path sends on a goroutine ON PURPOSE, so that the response time does not reveal
// whether the address exists. A test that read the mailbox immediately would be racing that decision and
// would fail intermittently — which is how a deliberate design gets "fixed" back into a timing oracle.
func waitForMail(t *testing.T, hz *harness, n int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if hz.mail.count() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waited for %d messages, saw %d", n, hz.mail.count())
}

// ── the reset limit ──────────────────────────────────────────────────────────────────────────────

// TestTheResetLimitIsIdenticalForAKnownAndAnUnknownAddress.
//
// # 🔴 The reason this test matters more than the limit does
//
// A rate limiter is the classic way an enumeration oracle is reopened after being closed. The natural
// implementation counts what it did — a mail sent, a token issued — and therefore only limits addresses
// that exist. Then 429-versus-200 answers, for anybody who cares to ask, exactly the question the
// constant reply was built to refuse: the addresses that can be rate-limited are the addresses that have
// accounts.
//
// So both addresses are pushed past the ceiling and compared at every step: the same status, the same
// body, and the same request number at which each trips.
func TestTheResetLimitIsIdenticalForAKnownAndAnUnknownAddress(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	known := "limited-known-" + randSuffix() + "@example.test"
	if _, err := hz.Auth.CreateUser(context.Background(), hz.tenant, known, testPassword,
		tenancy.Member); err != nil {
		t.Fatal(err)
	}
	unknown := "limited-unknown-" + randSuffix() + "@example.test"

	// One past the burst, so both must cross the boundary.
	const attempts = ResetBurst + 2
	type reply struct {
		code int
		body string
	}
	seen := map[string][]reply{}
	for _, addr := range []string{known, unknown} {
		for range attempts {
			rec := hz.do(t, "POST", "/api/auth/password/forgot",
				`{"tenant":"`+hz.tenant+`","email":"`+addr+`"}`, nil)
			seen[addr] = append(seen[addr], reply{rec.Code, rec.Body.String()})
		}
	}
	for i := range attempts {
		k, u := seen[known][i], seen[unknown][i]
		if k.code != u.code || k.body != u.body {
			t.Fatalf("request %d distinguishes a real address from an invented one:\n"+
				"  known:   %d %s\n  unknown: %d %s", i+1, k.code, k.body, u.code, u.body)
		}
	}
	// And the limit is actually doing something, or the test above compares two identical non-events.
	if seen[known][ResetBurst].code != http.StatusTooManyRequests {
		t.Fatalf("request %d was %d, not 429 — nothing is being limited",
			ResetBurst+1, seen[known][ResetBurst].code)
	}
	if seen[known][0].code != http.StatusOK {
		t.Fatalf("the first request was already refused (%d)", seen[known][0].code)
	}
}

// TestTheResetLimitCannotBeBypassedByChangingCase.
//
// 🔴 Identity here is case-insensitive — every query matches on `lower(email)`, so `Foo@x.test` and
// `foo@x.test` are one account and one inbox. A limiter keyed on the raw string would give each spelling
// its own allowance, and the ceiling would be "three per capitalisation" — which is not a ceiling.
func TestTheResetLimitCannotBeBypassedByChangingCase(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	addr := "MixedCase-" + randSuffix() + "@Example.Test"

	for i := range ResetBurst {
		if rec := hz.do(t, "POST", "/api/auth/password/forgot",
			`{"email":"`+addr+`"}`, nil); rec.Code != http.StatusOK {
			t.Fatalf("request %d of the burst was refused: %d", i+1, rec.Code)
		}
	}
	for _, spelling := range []string{
		strings.ToLower(addr), strings.ToUpper(addr), "  " + addr + "  ",
	} {
		rec := hz.do(t, "POST", "/api/auth/password/forgot",
			`{"email":"`+spelling+`"}`, nil)
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("respelling the address as %q got a fresh allowance (%d)", spelling, rec.Code)
		}
	}
}

// TestOneFloodedAddressDoesNotLockOutEverybodyElse.
//
// 🔴 A limit that is not per-address hands an attacker something better than what it prevents: flood one
// endpoint and nobody in the deployment can recover their account. The thing being protected is an inbox,
// so the bucket belongs to the address.
func TestOneFloodedAddressDoesNotLockOutEverybodyElse(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	victim := "flooded-" + randSuffix() + "@example.test"
	for range ResetBurst + 5 {
		hz.do(t, "POST", "/api/auth/password/forgot", `{"email":"`+victim+`"}`, nil)
	}
	rec := hz.do(t, "POST", "/api/auth/password/forgot",
		`{"email":"bystander-`+randSuffix()+`@example.test"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("flooding one address refused a different one (%d): %s", rec.Code, rec.Body.String())
	}
}

// TestARefusedResetSendsNoMailAndSaysWhenToReturn.
func TestARefusedResetSendsNoMailAndSaysWhenToReturn(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	addr := "quiet-" + randSuffix() + "@example.test"
	if _, err := hz.Auth.CreateUser(context.Background(), hz.tenant, addr, testPassword,
		tenancy.Member); err != nil {
		t.Fatal(err)
	}
	for range ResetBurst {
		hz.do(t, "POST", "/api/auth/password/forgot", `{"email":"`+addr+`"}`, nil)
	}
	waitForMail(t, hz, ResetBurst)
	before := hz.mail.count()

	rec := hz.do(t, "POST", "/api/auth/password/forgot", `{"email":"`+addr+`"}`, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After is %q; a client cannot tell when to come back", ra)
	}
	// The refusal must be a refusal to SEND, not merely a refusal to reply. Mail is dispatched on a
	// goroutine, so give it the same chance to arrive that a successful request would have.
	time.Sleep(200 * time.Millisecond)
	if hz.mail.count() != before {
		t.Errorf("a rate-limited request still sent mail: %d messages became %d", before, hz.mail.count())
	}
}

// ── the login limit ──────────────────────────────────────────────────────────────────────────────

// signInAttempt makes one login request and returns the recorder, without asserting anything.
func (hz *harness) signInAttempt(t *testing.T, tenant, email, password string) *httptest.ResponseRecorder {
	t.Helper()
	return hz.do(t, "POST", "/api/auth/login",
		`{"tenant":"`+tenant+`","email":"`+email+`","password":"`+password+`"}`, nil)
}

// TestTheLoginLimitIsIdenticalForAKnownAndAnUnknownAddress.
//
// 🔴 The same fence as the reset endpoint's, for the same reason. Login already answers identically for
// a wrong password and an address with no account — in message and in timing, which is why auth.Login
// verifies against a decoy hash when nothing matches. A limit that only counted attempts against
// accounts that exist would give all of that away again: being refused at all would mean the address is
// real.
func TestTheLoginLimitIsIdenticalForAKnownAndAnUnknownAddress(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	known := "login-known-" + randSuffix() + "@example.test"
	if _, err := hz.Auth.CreateUser(context.Background(), hz.tenant, known, testPassword,
		tenancy.Member); err != nil {
		t.Fatal(err)
	}
	unknown := "login-unknown-" + randSuffix() + "@example.test"

	const attempts = LoginBurst + 2
	type reply struct {
		code int
		body string
	}
	seen := map[string][]reply{}
	for _, addr := range []string{known, unknown} {
		for range attempts {
			rec := hz.signInAttempt(t, hz.tenant, addr, "a-wrong-password-of-ample-length")
			seen[addr] = append(seen[addr], reply{rec.Code, rec.Body.String()})
		}
	}
	for i := range attempts {
		k, u := seen[known][i], seen[unknown][i]
		if k.code != u.code || k.body != u.body {
			t.Fatalf("attempt %d distinguishes a real account from an invented one:\n"+
				"  known:   %d %s\n  unknown: %d %s", i+1, k.code, k.body, u.code, u.body)
		}
	}
	if seen[known][LoginBurst].code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d was %d, not 429 — nothing is being limited",
			LoginBurst+1, seen[known][LoginBurst].code)
	}
	if seen[known][0].code != http.StatusUnauthorized {
		t.Fatalf("the first wrong password was %d, not 401", seen[known][0].code)
	}
}

// TestACorrectPasswordDoesNotSpendTheLoginBudget.
//
// # 🔴 What this is really protecting
//
// A per-account login limit charged for every attempt hands an attacker an account-lockout weapon:
// fail to sign in as somebody often enough and they cannot sign in either. With the reset endpoint also
// limited per address, both ways into the account close at once, for the price of a few dozen requests
// an hour.
//
// Charging only for FAILURES removes the ratchet. Somebody with several devices, or a script, can sign
// in successfully without limit; the budget is ten WRONG passwords, not ten sign-ins.
func TestACorrectPasswordDoesNotSpendTheLoginBudget(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	email := "budget-" + randSuffix() + "@example.test"
	if _, err := hz.Auth.CreateUser(context.Background(), hz.tenant, email, testPassword,
		tenancy.Member); err != nil {
		t.Fatal(err)
	}

	// Spend all but one token on wrong guesses.
	for i := range LoginBurst - 1 {
		if rec := hz.signInAttempt(t, hz.tenant, email, "wrong-but-long-enough-1"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong guess %d answered %d, want 401", i+1, rec.Code)
		}
	}
	// Now sign in correctly, many more times than there are tokens. Every one must work: each spends the
	// last token and hands it straight back.
	for i := range LoginBurst * 5 {
		rec := hz.signInAttempt(t, hz.tenant, email, testPassword)
		if rec.Code != http.StatusOK {
			t.Fatalf("correct sign-in %d was refused with %d — a correct password is consuming the "+
				"budget, so anybody with several devices is one busy morning from being locked out: %s",
				i+1, rec.Code, rec.Body.String())
		}
	}
	// And the last token is still there for one more wrong guess, after which the ceiling holds.
	if rec := hz.signInAttempt(t, hz.tenant, email, "wrong-but-long-enough-2"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the final token was missing: %d", rec.Code)
	}
	rec := hz.signInAttempt(t, hz.tenant, email, "wrong-but-long-enough-3")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("guessing continued past the ceiling: %d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After is %q; a client cannot tell when to come back", ra)
	}
}

// TestGuessingAtOneAccountDoesNotAffectAnother.
//
// Two axes at once, because the key has two halves. A different address in the same organization is a
// different account, and — the one that is easy to get wrong — the SAME address in a different
// organization is also a different account, with a different password. Keyed on the address alone,
// guessing at one customer's user would spend a different customer's user's budget, which is one tenant
// degrading another through an endpoint neither of them controls.
func TestGuessingAtOneAccountDoesNotAffectAnother(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	victim := "victim-" + randSuffix() + "@example.test"
	if _, err := hz.Auth.CreateUser(context.Background(), hz.tenant, victim, testPassword,
		tenancy.Member); err != nil {
		t.Fatal(err)
	}
	// A second organization holding the SAME address.
	other := "t-" + randSuffix()
	if err := hz.Auth.CreateTenant(context.Background(), other, "Other"); err != nil {
		t.Fatal(err)
	}
	if _, err := hz.Auth.CreateUser(context.Background(), other, victim, testPassword,
		tenancy.Owner); err != nil {
		t.Fatal(err)
	}
	bystander := "bystander-" + randSuffix() + "@example.test"
	if _, err := hz.Auth.CreateUser(context.Background(), hz.tenant, bystander, testPassword,
		tenancy.Member); err != nil {
		t.Fatal(err)
	}

	for range LoginBurst + 3 {
		hz.signInAttempt(t, hz.tenant, victim, "wrong-but-long-enough")
	}
	if rec := hz.signInAttempt(t, hz.tenant, victim, testPassword); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the flooded account is not limited (%d); the premise of this test is wrong", rec.Code)
	}
	if rec := hz.signInAttempt(t, hz.tenant, bystander, testPassword); rec.Code != http.StatusOK {
		t.Errorf("guessing at one account refused a different one in the same organization: %d %s",
			rec.Code, rec.Body.String())
	}
	if rec := hz.signInAttempt(t, other, victim, testPassword); rec.Code != http.StatusOK {
		t.Errorf("guessing at an account in one organization refused the same address in another: %d %s",
			rec.Code, rec.Body.String())
	}
}

// TestTheLoginLimitCannotBeBypassedByChangingCase.
func TestTheLoginLimitCannotBeBypassedByChangingCase(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	addr := "LoginCase-" + randSuffix() + "@Example.Test"
	if _, err := hz.Auth.CreateUser(context.Background(), hz.tenant, addr, testPassword,
		tenancy.Member); err != nil {
		t.Fatal(err)
	}
	for range LoginBurst {
		hz.signInAttempt(t, hz.tenant, addr, "wrong-but-long-enough")
	}
	for _, spelling := range []string{
		strings.ToLower(addr), strings.ToUpper(addr), "  " + addr + "  ",
	} {
		if rec := hz.signInAttempt(t, hz.tenant, spelling, "wrong-but-long-enough"); rec.Code != http.StatusTooManyRequests {
			t.Errorf("respelling the address as %q got a fresh allowance (%d)", spelling, rec.Code)
		}
	}
}
