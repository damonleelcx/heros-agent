package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/heros-foreal/heros/db/migrations"
	"github.com/heros-foreal/heros/internal/auth"
	"github.com/heros-foreal/heros/internal/mailer"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/tenancy"
)

// authz_test.go is the fence for what each role may reach.

const testPassword = "a-sufficiently-long-password"

// captureMailer records what would have been sent, so a test can read the link out of it.
//
// 🔴 It implements the same interface the daemon uses, and the templates it is handed are the real ones.
// A test that built its own link string would prove the handler works with a link the product never
// produces — which is how a broken URL ships past a green suite.
type captureMailer struct {
	mu   sync.Mutex
	sent []mailer.Message
	// fail makes every send fail, for the paths that must react to that.
	fail error
}

func (c *captureMailer) Describe() string { return "capture (test)" }

func (c *captureMailer) Send(_ context.Context, m mailer.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail != nil {
		return c.fail
	}
	c.sent = append(c.sent, m)
	return nil
}

func (c *captureMailer) last(t *testing.T) mailer.Message {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sent) == 0 {
		t.Fatal("no mail was sent")
	}
	return c.sent[len(c.sent)-1]
}

func (c *captureMailer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sent)
}

// tokenFromLink pulls a token out of a link the real templates produced.
func tokenFromLink(t *testing.T, m mailer.Message, param string) string {
	t.Helper()
	// The plain-text part prints the URL in full; parsing that is also a check that it is there.
	for _, line := range strings.Split(m.Text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "https://") && !strings.HasPrefix(line, "http://") {
			continue
		}
		if i := strings.Index(line, param+"="); i >= 0 {
			return line[i+len(param)+1:]
		}
	}
	t.Fatalf("no %s link in the message:\n%s", param, m.Text)
	return ""
}

// harness is a server on a real database with real identity.
type harness struct {
	*Server
	h      http.Handler
	tenant string
	mail   *captureMailer
	db     *sql.DB
}

func newHarness(t *testing.T) *harness {
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
	a := auth.NewStore(db)
	tenant := "t-" + randSuffix()
	if err := a.CreateTenant(context.Background(), tenant, "Acme"); err != nil {
		t.Fatalf("tenant: %v", err)
	}
	links, err := mailer.NewLinks("https://console.example.test")
	if err != nil {
		t.Fatalf("links: %v", err)
	}
	m := &captureMailer{}
	s := NewServer()
	s.Root = store.NewMemory()
	s.Auth = a
	s.DefaultTenant = tenant
	s.Mail = m
	s.Links = links
	return &harness{Server: s, h: s.Handler(nil), tenant: tenant, mail: m, db: db}
}

// user creates somebody in a role and signs them in, returning their cookie and id.
func (hz *harness) user(t *testing.T, role tenancy.Role) (*http.Cookie, string) {
	t.Helper()
	email := string(role) + "-" + randSuffix() + "@example.test"
	id, err := hz.Auth.CreateUser(context.Background(), hz.tenant, email, testPassword, role)
	if err != nil {
		t.Fatalf("create %s: %v", role, err)
	}
	return hz.signIn(t, email), id
}

func (hz *harness) signIn(t *testing.T, email string) *http.Cookie {
	t.Helper()
	body := `{"tenant":"` + hz.tenant + `","email":"` + email + `","password":"` + testPassword + `"}`
	rec := hz.do(t, "POST", "/api/auth/login", body, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign in as %s: %d %s", email, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatalf("no session cookie for %s", email)
	return nil
}

func (hz *harness) do(t *testing.T, method, path, body string, as *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if as != nil {
		req.AddCookie(as)
	}
	rec := httptest.NewRecorder()
	hz.h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("undecodable response %q: %v", rec.Body.String(), err)
	}
	return out
}

// TestEveryRouteRefusesExactlyTheRolesThatLackItsCapability.
//
// # 🔴 Why every route against every role, and not a sample
//
// The same argument as the tenant-isolation fence next door. A sample proves the sampled routes are
// guarded, and the route somebody forgot is by definition not in the sample. Sixteen routes and four
// roles is sixty-four requests, all of them cheap, and the table they check is the one the mux is built
// from — so a route added tomorrow is covered by this test today.
//
// The assertion is two-sided on purpose. That a viewer is refused is half the property; the other half
// is that a member is NOT refused, because a fence that only checks denials passes perfectly on a server
// that refuses everybody.
func TestEveryRouteRefusesExactlyTheRolesThatLackItsCapability(t *testing.T) {
	hz := newHarness(t)
	// One owner exists first, so nothing here trips the last-owner rule.
	ownerCookie, _ := hz.user(t, tenancy.Owner)
	_ = ownerCookie

	cookies := map[tenancy.Role]*http.Cookie{}
	for _, role := range tenancy.Roles {
		c, _ := hz.user(t, role)
		cookies[role] = c
	}

	for _, r := range apiRoutes {
		if r.Public || r.Needs == "" {
			continue
		}
		for _, role := range tenancy.Roles {
			body := `{}`
			rec := hz.do(t, r.Method, concretePath(r.Path), body, cookies[role])
			refused := rec.Code == http.StatusForbidden
			shouldRefuse := !tenancy.Can(role, r.Needs)
			switch {
			case shouldRefuse && !refused:
				t.Errorf("%s %s needs %q, which a %s does not hold, and it answered %d instead of 403",
					r.Method, r.Path, r.Needs, role, rec.Code)
			case !shouldRefuse && refused:
				t.Errorf("%s %s needs %q, which a %s DOES hold, and it was refused with 403: %s",
					r.Method, r.Path, r.Needs, role, rec.Body.String())
			}
		}
	}
}

// TestARefusalSaysWhatToAskFor.
//
// 🔴 An authorization failure is not an error the person can fix by retrying, so the message has to say
// who can fix it. "forbidden" sends somebody to support to find out what the product means; naming the
// role that holds the capability ends the conversation in one message.
func TestARefusalSaysWhatToAskFor(t *testing.T) {
	hz := newHarness(t)
	_, _ = hz.user(t, tenancy.Owner)
	viewer, _ := hz.user(t, tenancy.Viewer)

	rec := hz.do(t, "POST", "/api/decide", `{}`, viewer)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a viewer reached /api/decide: %d", rec.Code)
	}
	body := decode(t, rec)
	msg, _ := body["error"].(string)
	if !strings.Contains(strings.ToLower(msg), "approve") {
		t.Errorf("the refusal does not say what was refused: %q", msg)
	}
	if body["role"] != "viewer" {
		t.Errorf("the refusal does not report the role the caller holds: %v", body["role"])
	}
	// 🚫 And it must not leak the internal capability name, which means nothing outside this codebase.
	if strings.Contains(msg, "change.approve") {
		t.Errorf("the refusal quotes an internal identifier at the customer: %q", msg)
	}
}

// TestDemotionTakesEffectOnTheNextRequest.
//
// # 🔴 What this is really testing
//
// That authority is read from the user row per request, and not stamped onto the session at login. If it
// were on the session, somebody demoted this morning would keep this morning's access until their
// session expired — up to a fortnight of exactly the permission that was just taken away, with the
// console showing the change as done.
func TestDemotionTakesEffectOnTheNextRequest(t *testing.T) {
	hz := newHarness(t)
	ownerCookie, _ := hz.user(t, tenancy.Owner)
	victim, victimID := hz.user(t, tenancy.Member)

	// A member may approve, so this is not a 403.
	if rec := hz.do(t, "POST", "/api/decide", `{}`, victim); rec.Code == http.StatusForbidden {
		t.Fatalf("a member was refused /api/decide before any demotion: %s", rec.Body.String())
	}
	rec := hz.do(t, "POST", "/api/members/role",
		`{"user_id":"`+victimID+`","role":"viewer"}`, ownerCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("demotion failed: %d %s", rec.Code, rec.Body.String())
	}
	// SAME cookie, next request.
	if rec := hz.do(t, "POST", "/api/decide", `{}`, victim); rec.Code != http.StatusForbidden {
		t.Fatalf("a demoted member still reached /api/decide with their existing session (%d). "+
			"Authority is being cached in the credential", rec.Code)
	}
}

// TestAnAdminCannotReachAnOwnerOverHttp.
//
// The unit test next door proves the RULE. This proves the rule is actually the one the endpoint
// applies — the two have to be checked separately, because a correct rule nobody calls is the most
// common way an authorization model is wrong.
func TestAnAdminCannotReachAnOwnerOverHttp(t *testing.T) {
	hz := newHarness(t)
	_, ownerID := hz.user(t, tenancy.Owner)
	admin, _ := hz.user(t, tenancy.Admin)

	for _, c := range []struct {
		what, path, body string
	}{
		{"demote the owner", "/api/members/role", `{"user_id":"` + ownerID + `","role":"member"}`},
		{"remove the owner", "/api/members/remove", `{"user_id":"` + ownerID + `"}`},
	} {
		rec := hz.do(t, "POST", c.path, c.body, admin)
		if rec.Code != http.StatusForbidden {
			t.Errorf("an admin could %s: %d %s", c.what, rec.Code, rec.Body.String())
		}
	}
	// And an admin cannot promote anybody to owner, which is the same escalation by the other door.
	_, memberID := hz.user(t, tenancy.Member)
	rec := hz.do(t, "POST", "/api/members/role", `{"user_id":"`+memberID+`","role":"owner"}`, admin)
	if rec.Code != http.StatusForbidden {
		t.Errorf("an admin promoted somebody to owner: %d %s", rec.Code, rec.Body.String())
	}
}

// TestTheLastOwnerCannotBeRemovedOrDemoted.
//
// 🔴 Without this an organization can be left with nobody who can invite, promote or remove anybody —
// a state recoverable only by somebody with database access, which for a customer means a support
// ticket and for us means a shell on their data.
func TestTheLastOwnerCannotBeRemovedOrDemoted(t *testing.T) {
	hz := newHarness(t)
	owner, ownerID := hz.user(t, tenancy.Owner)

	for _, c := range []struct{ what, path, body string }{
		{"demote themselves", "/api/members/role", `{"user_id":"` + ownerID + `","role":"member"}`},
		{"remove themselves", "/api/members/remove", `{"user_id":"` + ownerID + `"}`},
	} {
		rec := hz.do(t, "POST", c.path, c.body, owner)
		if rec.Code != http.StatusConflict {
			t.Errorf("the last owner could %s: %d %s", c.what, rec.Code, rec.Body.String())
		}
	}
	// With a second owner in place, the first may step down.
	_, secondID := hz.user(t, tenancy.Member)
	if rec := hz.do(t, "POST", "/api/members/role",
		`{"user_id":"`+secondID+`","role":"owner"}`, owner); rec.Code != http.StatusOK {
		t.Fatalf("an owner could not appoint another owner: %d %s", rec.Code, rec.Body.String())
	}
	if rec := hz.do(t, "POST", "/api/members/role",
		`{"user_id":"`+ownerID+`","role":"member"}`, owner); rec.Code != http.StatusOK {
		t.Fatalf("an owner could not step down after appointing a successor: %d %s",
			rec.Code, rec.Body.String())
	}
}

// TestMembersOfOneOrganizationAreInvisibleToAnother.
//
// 🔴 The isolation property, at the membership layer. A user id from another tenant must be
// indistinguishable from one that does not exist — otherwise "remove member" doubles as a probe for
// which account ids are real across every customer on the deployment.
func TestMembersOfOneOrganizationAreInvisibleToAnother(t *testing.T) {
	a := newHarness(t)
	_, _ = a.user(t, tenancy.Owner)
	_, strangerID := a.user(t, tenancy.Member)

	b := newHarness(t)
	bOwner, _ := b.user(t, tenancy.Owner)

	// Not in the list.
	rec := b.do(t, "GET", "/api/members", "", bOwner)
	if strings.Contains(rec.Body.String(), strangerID) {
		t.Fatal("one organization's member list contains another's member")
	}
	// And not reachable by id, with the same answer a nonexistent id gets.
	real := b.do(t, "POST", "/api/members/remove", `{"user_id":"`+strangerID+`"}`, bOwner)
	fake := b.do(t, "POST", "/api/members/remove", `{"user_id":"usr_definitely-not-real"}`, bOwner)
	if real.Code != http.StatusNotFound {
		t.Errorf("another tenant's member id answered %d, not 404: %s", real.Code, real.Body.String())
	}
	if real.Code != fake.Code || real.Body.String() != fake.Body.String() {
		t.Errorf("a real id from another tenant is distinguishable from a nonexistent one:\n"+
			"  real:  %d %s\n  fake:  %d %s", real.Code, real.Body.String(), fake.Code, fake.Body.String())
	}
	// The stranger is still there — the refusal was a refusal, not a silent success.
	if _, err := a.Auth.Member(context.Background(), a.tenant, strangerID); err != nil {
		t.Fatalf("the member was removed across a tenant boundary: %v", err)
	}
}
