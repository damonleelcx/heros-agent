package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
)

// authgate_fence_test.go drives the COMPOSED handler — `s.Handler`, not `s.Mux`.
//
// # 🔴 Why that distinction is the whole point of this file
//
// `auth.Compose` gates everything under `/api/` when `auth_mode=required`, which is every real deployment.
// It wraps `s.Handler` one layer OUTSIDE the mux. So a test that calls `s.Mux.ServeHTTP` is calling
// underneath the gate: it exercises the handler perfectly and proves nothing about whether a request can
// reach it.
//
// This has now cost the same bug twice.
//
//   - P27: `heros login` could not start a device flow on any real deployment — a flat 401 from the
//     middleware, handler never reached. Every device test passed, because they all drove `s.Mux`. It was
//     found by running the actual CLI against a real deployment.
//   - P28: `POST /api/v1/auth/password/signin` — the route the CLI uses to sign in with an email and a
//     password — was missing from `auth.PublicPaths` for exactly the same reason, and all twelve tests in
//     `passwordauth_test.go` passed, because they all drive `s.Mux`. Found by curling a locally-running
//     `agentd` with the gate on: `device/authorize` answered 201 and `auth/password/signin` answered 401.
//
// A comment describing a trap did not stop the second one. This test does, because it is the only place in
// the package where the two layers are stacked the way a deployment stacks them.

// credentialFreeRoutes are the routes a caller reaches BEFORE it holds any credential.
//
// Three, and each is safe unauthenticated for a reason written down at its handler:
//   - `device/authorize` accepts one display label that binds nothing and mints its own secrets;
//   - `device/token` is guarded by the 32 random bytes it was handed;
//   - `auth/password/signin` is guarded by argon2id verification plus a per-account lockout.
//
// 🚫 Everything else on those two families is deliberately absent. `device/approve` mints a credential
// naming a person; `password/signup|forgot|reset|verify|resend` are called by the console's server side,
// which holds the BFF's platform credential and passes the gate normally. Publishing them would widen the
// unauthenticated surface for no caller that needs it.
var credentialFreeRoutes = []struct {
	method, path, why string
}{
	{http.MethodPost, "/api/v1/device/authorize", "the CLI has no credential yet — that is the point of the flow"},
	{http.MethodPost, "/api/v1/device/token", "the CLI polls holding only a 256-bit device code"},
	{http.MethodPost, "/api/v1/auth/password/signin", "`heros login` signs in with an email and a password"},
}

// gatedServer builds a server with the gate ON, exactly as a real deployment composes it.
//
// 🔴 `required` is the value that turns the gate on, and it is what every real deployment sets. A fence
// that left it unset would compose no middleware and pass unconditionally — the failure mode this whole
// file exists to close, reproduced inside the fence itself. So it is asserted rather than assumed below.
func gatedServer(t *testing.T) *Server {
	t.Helper()
	s, _ := newSurfaceWith(t, true, config.Config{AuthMode: "required"})
	if s.Handler == nil {
		t.Fatal("the server composed no handler")
	}
	// Prove the gate is actually on before relying on it: an unauthenticated tenant route must refuse.
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/organization", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("the auth gate is NOT composed (an unauthenticated tenant route answered %d, want 401) — "+
			"every assertion in this file would pass for the wrong reason", rec.Code)
	}
	return s
}

// 🔴 The fence: a credential-free route must reach its handler with NO credential presented.
//
// # Why the body is deliberately malformed
//
// "Did the handler run?" cannot be answered by the status alone. `handlePasswordSignIn` legitimately
// answers 401 for an unknown address — so a fence that treated any 401 as "the gate refused" would report
// a defect that is not there, and would fail on correct code. The first version of this test did exactly
// that, and it took a discriminating request against a running server to tell the two apart.
//
// Malformed JSON is the discriminator: every one of these handlers decodes first and answers 400. So
//
//	400  → the request reached the handler (what we require)
//	401  → the gate refused before routing (the defect)
//
// It depends on no error prose, which is the other way this could have been written and the way that rots.
func TestCredentialFreeRoutesReachTheirHandler(t *testing.T) {
	s := gatedServer(t)
	for _, route := range credentialFreeRoutes {
		t.Run(route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, bytes.NewReader([]byte("not json")))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			// 🔴 s.Handler, NOT s.Mux. See the file header.
			s.Handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("%s %s answered 401 to a MALFORMED body, so the gate refused it before the handler "+
					"ran (a handler would have answered 400).\n  %s.\n"+
					"  Add it to auth.PublicPaths. Every test that drives s.Mux will keep passing while this "+
					"route is unreachable on every deployment with auth_mode=required — which is all of them.",
					route.method, route.path, route.why)
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s %s answered %d to a malformed body, want 400. The discriminator this fence "+
					"relies on no longer holds, so it can no longer tell a gate refusal from a handler "+
					"refusal — fix the fence before trusting it.", route.method, route.path, rec.Code)
			}
		})
	}
}

// The other direction, and it is not symmetry for its own sake: if `PublicPaths` ever grew a prefix match or
// somebody added the whole family, these would open silently and nothing would fail.
func TestTheRestOfThoseFamiliesStaysGated(t *testing.T) {
	s := gatedServer(t)
	mustBeGated := []struct{ method, path, why string }{
		{http.MethodPost, "/api/v1/device/approve", "it mints a credential naming a person"},
		// 🔴 P32 · the connection routes, added the day they were PUBLISHED on the ingress.
		//
		// Before that they were reachable only console→agentd inside the cluster, and the auth gate was
		// the second line of defence behind the network. It is now the FIRST and only one: the ingress
		// carries `POST /api/v1/repo-connections` to the internet, and that request body contains a
		// forge credential and creates a standing read grant over a customer's repository.
		//
		// So "these are gated" stopped being a property somebody could reason about from the topology
		// and became one that has to be asserted. This is that assertion.
		{http.MethodPost, "/api/v1/repo-connections", "it stores a forge credential and creates a standing read grant"},
		{http.MethodGet, "/api/v1/repo-connections", "it lists which repositories a tenant has granted us"},
		{http.MethodPost, "/api/v1/repo-connection-revocations", "it DELETES a grant, a credential and every tree derived from it"},
		{http.MethodGet, "/api/v1/repo-connection-reads", "it reads the ledger of when a customer's repository was read"},
		// P32 §4 · the console half of the pairing flow. The CLAIM is credential-free by design and is
		// in the list above; these two are not, and publishing the claim must not have widened them.
		{http.MethodPost, "/api/v1/local-pairings", "it issues a pairing code against the caller's tenant"},
		{http.MethodGet, "/api/v1/local-pairings", "it lists which machines read this tenant's repositories"},
		{http.MethodPost, "/api/v1/auth/password/signup", "only the console's server side calls it, holding the BFF credential"},
		{http.MethodPost, "/api/v1/auth/password/forgot", "same — and unauthenticated it is a mail-sending surface for anybody"},
		{http.MethodPost, "/api/v1/auth/password/reset", "same"},
		{http.MethodPost, "/api/v1/auth/password/verify", "same"},
		{http.MethodPost, "/api/v1/auth/password/resend", "same"},
		{http.MethodPost, "/api/v1/auth/password/change", "it changes a credential and is authenticated by design"},
		{http.MethodGet, "/api/v1/organization", "an ordinary tenant-scoped read"},
	}
	for _, route := range mustBeGated {
		t.Run(route.path, func(t *testing.T) {
			// The same malformed body, so a route that is gated answers 401 (the gate, before routing)
			// rather than 400 (its handler). One request shape across both directions of the fence.
			req := httptest.NewRequest(route.method, route.path, bytes.NewReader([]byte("not json")))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s answered %d with NO credential — it must be 401.\n  %s.\n"+
					"  Nothing breaks when this is wrong; the route simply becomes reachable by anybody who "+
					"can reach the port, and stays that way.",
					route.method, route.path, rec.Code, route.why)
			}
		})
	}
}

// The list in `auth` and the list here must not drift: this file is the one that proves reachability, and a
// path added to `auth.PublicPaths` without a case here is a route nothing checks.
func TestEveryPublicPathIsFencedHere(t *testing.T) {
	fenced := map[string]bool{}
	for _, r := range credentialFreeRoutes {
		fenced[r.path] = true
	}
	for _, p := range auth.PublicPaths {
		if !fenced[p] {
			t.Errorf("auth.PublicPaths contains %s and this fence does not exercise it — so nothing proves "+
				"it is reachable, which is the property it was added for", p)
		}
	}
}
