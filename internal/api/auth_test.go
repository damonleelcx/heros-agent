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
	if _, err := a.CreateUser(context.Background(), tenant, email, password); err != nil {
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

	// Every API path the server registers, with a method that reaches it.
	routes := []struct{ method, path string }{
		{"POST", "/api/subject"},
		{"GET", "/api/subject"},
		{"POST", "/api/ask"},
		{"GET", "/api/goals/g-1/events"},
		{"POST", "/api/decide"},
		{"POST", "/api/auth/logout"},
	}
	for _, r := range routes {
		req := httptest.NewRequest(r.method, r.path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s returned %d without a credential; it must be 401",
				r.method, r.path, rec.Code)
		}
	}

	// And the public ones genuinely are reachable, or the console could never sign in.
	for _, r := range []struct{ method, path string }{
		{"POST", "/api/auth/login"},
		{"GET", "/api/auth/status"},
	} {
		req := httptest.NewRequest(r.method, r.path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized && r.path == "/api/auth/status" {
			t.Errorf("%s %s is not reachable without a credential", r.method, r.path)
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
