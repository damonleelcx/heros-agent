package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/adminfixture"
)

// p22_crossorigin_test.go is P22 task 8.6's last clause: the operator surface is UNREACHABLE from the
// customer console's origin.
//
// # Why a cross-origin test rather than a routing test
//
// P8 Decision 11 / ADR-006 / P19 Decision 5 all say the same thing: the isolation between the two
// identity domains is the BROWSER'S ORIGIN BOUNDARY, not routing correctness. A test that only checked
// "the admin route rejects a tenant principal" would be testing the wrong half — it would still pass in
// a deployment that had helpfully added `Access-Control-Allow-Origin` to make a dashboard work, at
// which point a script on the customer console could read operator responses using the operator's own
// browser session.
//
// So this asserts the three things that make the boundary real, in the order a browser applies them.

func adminSurface(t *testing.T) http.Handler {
	t.Helper()
	layer, err := adminfixture.Build("p22-crossorigin", func() time.Time { return time.Now().UTC() })
	if err != nil {
		t.Fatalf("admin fixture: %v", err)
	}
	api, err := NewAdminAPI(AdminDeps{
		PlatformCredential: "admin-bff-credential-not-a-secret",
		Authenticator:      layer.Authenticator,
		Sessions:           layer.Sessions,
		Gate:               layer.Gate,
		Executor:           layer.Executor,
	})
	if err != nil {
		t.Fatalf("admin api: %v", err)
	}
	return api.Handler
}

func TestOperatorSurfaceIsUnreachableFromTheCustomerOrigin(t *testing.T) {
	handler := adminSurface(t)

	t.Run("a request carrying the customer console's session cookie is refused", func(t *testing.T) {
		// The customer session cookie is the strongest thing a customer-origin script could hold. It
		// authorises nothing here: the two consoles are on different origins with disjoint cookie jars,
		// and the admin surface reads a platform credential header the browser cannot forge.
		req := httptest.NewRequest(http.MethodGet, "/admin/api/me", nil)
		req.Header.Set("cookie", "heros_console_session=a-perfectly-valid-customer-session")
		req.Header.Set("origin", "https://console.heros.test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d; a customer session must authorise no admin capability", rec.Code)
		}
	})

	t.Run("no CORS header lets a customer-origin script read a response", func(t *testing.T) {
		// Even the OPEN endpoints. `/admin/api/readyz` is deliberately unauthenticated so a load
		// balancer can probe it — and a script on the customer origin still must not be able to read
		// it, because the browser is what enforces that and the browser only enforces what the response
		// tells it.
		for _, path := range []string{"/admin/api/healthz", "/admin/api/readyz", "/admin/api/me"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("origin", "https://console.heros.test")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			for _, header := range []string{
				"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Headers",
			} {
				if v := rec.Header().Get(header); v != "" {
					t.Fatalf("%s answered %s: %q — the operator surface must be opaque to another origin", path, header, v)
				}
			}
		}
	})

	t.Run("a preflight is not answered", func(t *testing.T) {
		// Without a preflight answer a browser will not even send the real cross-origin request, which
		// is the boundary doing its job one step earlier.
		req := httptest.NewRequest(http.MethodOptions, "/admin/api/me", nil)
		req.Header.Set("origin", "https://console.heros.test")
		req.Header.Set("access-control-request-method", "GET")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("the operator surface answered a cross-origin preflight")
		}
	})

	t.Run("an unknown admin path is refused rather than 404'd", func(t *testing.T) {
		// A 404 confirms which admin paths exist, which is reconnaissance handed to an unauthenticated
		// caller. The credential check runs BEFORE routing so every path answers identically.
		req := httptest.NewRequest(http.MethodGet, "/admin/api/there-is-no-such-thing", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d; an unauthenticated caller must not learn which admin paths exist", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "there-is-no-such-thing") {
			t.Fatal("the refusal echoed the requested path back")
		}
	})
}
