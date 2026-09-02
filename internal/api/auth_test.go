package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/heros-foreal/heros/db/migrations"
	"github.com/heros-foreal/heros/internal/auth"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/tenancy"
)

func testServer(t *testing.T) (*Server, string, string) {
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
	if err := a.CreateTenant(context.Background(), tenant, "Test"); err != nil {
		t.Fatalf("tenant: %v", err)
	}
	email := "person@" + tenant + ".invalid"
	const password = "a-sufficiently-long-password"
	if _, err := a.CreateUser(context.Background(), tenant, email, password, tenancy.Owner); err != nil {
		t.Fatalf("user: %v", err)
	}

	s := NewServer()
	s.Root = store.NewMemory()
	s.Auth = a
	s.DefaultTenant = tenant
	return s, tenant, email
}

var suffixSeq int

func randSuffix() string {
	suffixSeq++
	return fmt.Sprintf("%d%d", time.Now().UnixNano(), suffixSeq)
}

// TestEveryApiRouteRequiresAuthenticationByDefault.
//
// 🔴 THE fence for a default-deny. A check inside each handler is a check a new handler can be written
// without — routes get added under deadline, and the one that forgets looks exactly like the ones that
// did not, until somebody notices it serves everybody's data.
//
// This walks the real mux and asserts that every registered path either refuses an unauthenticated
// request or is on the short, explicit public list.
func TestEveryApiRouteRequiresAuthenticationByDefault(t *testing.T) {
	s, _, _ := testServer(t)
	h := s.Handler(nil)

	// 🔴 Walks `apiRoutes` — the same table the mux is built from — rather than a list written out here.
	// The previous version of this test enumerated routes by hand, which means a route added to the
	// server and not to the list is a route this fence silently never checks. A hand-maintained mirror of
	// "everything we serve" fails in the direction where the gap is invisible.
	for _, r := range apiRoutes {
		if r.Public {
			continue
		}
		req := httptest.NewRequest(r.Method, concretePath(r.Path), strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s returned %d without a credential; it must be 401",
				r.Method, r.Path, rec.Code)
		}
	}

	// And the public ones genuinely are reachable, or nobody could sign in, accept an invitation, or ask
	// for a password reset — every one of which is done by somebody with no credential by definition.
	//
	// # 🔴 Why the body is deliberately MALFORMED
	//
	// A status code does not say which layer produced it. `POST /api/auth/login` with an empty body
	// answers 401 from the HANDLER, because no such account matched — and a fence reading "401 means the
	// gate refused" reports a route as unreachable when it is served correctly, which is a false alarm
	// that gets the fence weakened rather than the bug fixed.
	//
	// Unparseable JSON is the discriminator that depends on no error prose: 400 means the handler ran and
	// rejected the body, 401 means the request never reached it.
	for _, r := range apiRoutes {
		if !r.Public {
			continue
		}
		req := httptest.NewRequest(r.Method, concretePath(r.Path), strings.NewReader(`{ not json`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized {
			t.Errorf("%s %s is marked public but answered 401, so the credential gate refused it before "+
				"the handler saw a malformed body", r.Method, r.Path)
		}
	}
}

// concretePath fills in a wildcard so a pattern can be requested.
func concretePath(pattern string) string {
	if i := strings.Index(pattern, "{"); i >= 0 {
		j := strings.Index(pattern[i:], "}")
		return pattern[:i] + "x" + pattern[i+j+1:]
	}
	return pattern
}

// TestNoPathIsHalfPublic.
//
// 🔴 `authenticate` runs BEFORE the mux has matched a pattern, so it can only key its exemptions on the
// URL path — not on the method. That approximation is exact as long as no path is public under one
// method and protected under another. If one ever is, the protected method is silently exempted too, and
// nothing else in the codebase would notice.
func TestNoPathIsHalfPublic(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range apiRoutes {
		if prior, ok := seen[r.Path]; ok && prior != r.Public {
			t.Errorf("%s is public under one method and protected under another; `authenticate` cannot "+
				"tell them apart and would exempt both", r.Path)
		}
		seen[r.Path] = r.Public
	}
}

// TestEveryRouteDeclaresWhatItNeeds.
//
// A protected route with no capability is reachable by anyone signed in, which is sometimes right — the
// member list is — but must be a decision rather than an omission. This asserts the ones that write, run
// or spend name a capability; the reader routes are listed as deliberate blanks.
func TestEveryRouteDeclaresWhatItNeeds(t *testing.T) {
	// Routes that intentionally need nothing beyond a valid session.
	deliberatelyOpen := map[string]bool{
		"POST /api/auth/logout":       true,
		"POST /api/auth/email/resend": true,
		"GET /api/members":            true,
		"GET /api/autonomy":           true,
		// 🔴 Your OWN profile — your name, what you do, your standing instructions, and the language you
		// want to be answered in. There is no role that should be unable to read or change its own
		// settings: gating it behind RunGoals would mean a viewer cannot ask to be answered in their own
		// language, which is not a permission anybody set out to model.
		//
		// It is safe to leave open because the handler takes the identity from the authenticated
		// principal and never from the request — there is no parameter with which to address somebody
		// else's rows. See handleGetProfile / handleSetProfile in console.go.
		"GET /api/profile":  true,
		"POST /api/profile": true,
	}
	for _, r := range apiRoutes {
		if r.Public || r.Needs != "" {
			continue
		}
		key := r.Method + " " + r.Path
		if !deliberatelyOpen[key] {
			t.Errorf("%s names no capability and is not in the deliberately-open list. Either give it "+
				"one, or add it here so the decision is written down", key)
		}
	}
}

// TestLoginIssuesAnHttpOnlySessionCookie.
func TestLoginIssuesAnHttpOnlySessionCookie(t *testing.T) {
	s, tenant, email := testServer(t)
	h := s.Handler(nil)

	body := `{"tenant":"` + tenant + `","email":"` + email + `","password":"a-sufficiently-long-password"}`
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login returned %d: %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	var session *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatal("login issued no session cookie")
	}
	if !session.HttpOnly {
		t.Error("the session cookie is readable by script; an injected script could exfiltrate it")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Error("the session cookie has no SameSite protection; another site could cause an " +
			"authenticated request from the browser")
	}
	if session.Value == "" {
		t.Fatal("the session cookie is empty")
	}

	// 🔴 The token must not appear in the response BODY. A credential in a JSON payload gets logged by
	// proxies, stored by clients, and pasted into bug reports.
	if strings.Contains(rec.Body.String(), session.Value) {
		t.Error("the session token appears in the response body")
	}

	// And it works.
	req2 := httptest.NewRequest("GET", "/api/subject", nil)
	req2.AddCookie(session)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code == http.StatusUnauthorized {
		t.Fatal("the session cookie issued by login does not authenticate")
	}
}

// TestAWrongPasswordAndAnUnknownEmailAreIndistinguishable.
//
// 🔴 Distinguishing them tells an attacker which emails are real, which is the first half of the work.
func TestAWrongPasswordAndAnUnknownEmailAreIndistinguishable(t *testing.T) {
	s, tenant, email := testServer(t)
	h := s.Handler(nil)

	responses := map[string]string{}
	for name, body := range map[string]string{
		"wrong password": `{"tenant":"` + tenant + `","email":"` + email + `","password":"wrong-but-long-enough"}`,
		"unknown email":  `{"tenant":"` + tenant + `","email":"nobody@nowhere.invalid","password":"wrong-but-long-enough"}`,
	} {
		req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s returned %d, want 401", name, rec.Code)
		}
		responses[name] = rec.Body.String()
	}
	if responses["wrong password"] != responses["unknown email"] {
		t.Errorf("the two failures are distinguishable:\n  wrong password: %s\n  unknown email:  %s",
			responses["wrong password"], responses["unknown email"])
	}
}

// TestAGarbageTokenIsRefused, with the same message as an expired one.
func TestAGarbageTokenIsRefused(t *testing.T) {
	s, _, _ := testServer(t)
	h := s.Handler(nil)
	for _, tok := range []string{"nonsense", "", strings.Repeat("A", 64)} {
		req := httptest.NewRequest("GET", "/api/subject", nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q returned %d", tok, rec.Code)
		}
	}
}

// TestLogoutEndsTheSession.
func TestLogoutEndsTheSession(t *testing.T) {
	s, tenant, email := testServer(t)
	h := s.Handler(nil)

	body := `{"tenant":"` + tenant + `","email":"` + email + `","password":"a-sufficiently-long-password"}`
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no session cookie")
	}

	out := httptest.NewRequest("POST", "/api/auth/logout", nil)
	out.AddCookie(session)
	h.ServeHTTP(httptest.NewRecorder(), out)

	after := httptest.NewRequest("GET", "/api/subject", nil)
	after.AddCookie(session)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, after)
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("a logged-out session still authenticates (%d)", rec3.Code)
	}
}

// TestOneTenantsSubjectIsNotVisibleToAnother.
//
// 🔴 The loaded repository used to be a SINGLE FIELD on the server, replaced by whoever loaded last. One
// customer's question was answered about another customer's repository — with real file:line references,
// confidently, about code they have never seen. A cross-tenant data leak wearing the shape of a cache.
func TestOneTenantsSubjectIsNotVisibleToAnother(t *testing.T) {
	s, _, _ := testServer(t)
	s.setSubject("tenant-a", &subjectState{})
	if s.subjectFor("tenant-b") != nil {
		t.Fatal("a repository loaded by one tenant is visible to another")
	}
	if s.subjectFor("tenant-a") == nil {
		t.Fatal("a tenant cannot see the repository it loaded")
	}
}
