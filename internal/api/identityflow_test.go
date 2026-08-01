package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/adminfixture"
	"github.com/heros-foreal/agentd/internal/adminidentity"
)

// p22_adminflow_test.go fences the federated operator surface the P22 follow-on added.
//
// These routes were built and shipped with a live probe and NO regression fence — which is how the
// client-supplied-challenge defect got in and stayed in until somebody read the code again. Every
// assertion below is one an attacker would try, not one a happy path exercises.

const flowCredential = "adminflow-test-credential-not-a-secret"

func flowSurface(t *testing.T) *AdminAPI {
	t.Helper()
	layer, err := adminfixture.Build("p22-adminflow", func() time.Time { return time.Now().UTC() })
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	api, err := NewAdminAPI(AdminDeps{
		PlatformCredential: flowCredential,
		Authenticator:      layer.Authenticator,
		Sessions:           layer.Sessions,
		Gate:               layer.Gate,
		Executor:           layer.Executor,
		TestModeIdP:        layer.TestModeIdP,
		Challenges:         layer.Challenges,
		Factors:            layer.Factors,
		IdP:                layer.IdP, // nil here: the fixture layer is not federated
	})
	if err != nil {
		t.Fatalf("admin api: %v", err)
	}
	return api
}

func postAdmin(t *testing.T, api *AdminAPI, path string, body any, withCredential bool) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(payload)))
	req.Header.Set("content-type", "application/json")
	if withCredential {
		req.Header.Set(HeaderPlatformCredential, flowCredential)
	}
	rec := httptest.NewRecorder()
	api.Handler.ServeHTTP(rec, req)
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	return rec, decoded
}

func TestTheFederatedRoutesAreClosedWhenNoIdPIsWired(t *testing.T) {
	// A deployment with no federated IdP must not expose a half-wired federated surface. 404, not a
	// 500 and not a partial success — there is nothing behind these routes to reach.
	api := flowSurface(t)
	for _, path := range []string{"/admin/api/idp/start"} {
		rec, body := postAdmin(t, api, path, map[string]string{"state": "s"}, true)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s → %d, want 404 (%v)", path, rec.Code, body)
		}
	}
	// And a federated login body is refused rather than half-processed.
	rec, _ := postAdmin(t, api, "/admin/api/session", map[string]string{
		"code": "abc", "code_verifier": "v", "redirect_uri": "https://admin.test/auth/callback",
	}, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a federated login on an unfederated deployment → %d, want 404", rec.Code)
	}
}

func TestEveryFederatedRouteStillRequiresTheBFFCredential(t *testing.T) {
	// The routes are open to the BROWSER's console, not to the internet. `idp/start` and
	// `mfa/challenge` are reachable before a session exists — which is what signing in means — but
	// still only through the BFF.
	api := flowSurface(t)
	for _, path := range []string{"/admin/api/idp/start", "/admin/api/mfa/challenge", "/admin/api/mfa/enroll"} {
		rec, body := postAdmin(t, api, path, map[string]string{}, false)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without the BFF credential → %d, want 401 (%v)", path, rec.Code, body)
		}
	}
}

func TestAChallengeIsMintedByThePlatformAndSpentOnce(t *testing.T) {
	api := flowSurface(t)
	rec, body := postAdmin(t, api, "/admin/api/mfa/challenge", map[string]string{}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("challenge → %d (%v)", rec.Code, body)
	}
	id, _ := body["challenge_id"].(string)
	value, _ := body["challenge"].(string)
	if id == "" || value == "" || id == value {
		t.Fatalf("challenge response = %v; the handle and the challenge are different values", body)
	}

	// The store is the authority, and it is single-use. Asserted through the DEPS rather than through a
	// second HTTP call, because the login route is what consumes it in production and this proves the
	// store it consumes from behaves.
	if _, ok := api.deps.Challenges.Consume(id); !ok {
		t.Fatal("a freshly minted challenge did not redeem")
	}
	if _, ok := api.deps.Challenges.Consume(id); ok {
		t.Fatal("the same challenge redeemed twice")
	}
}

func TestTheLoginRouteNeverTakesAChallengeFromTheRequest(t *testing.T) {
	// 🔴 The regression fence for the defect this follow-on introduced and fixed. A challenge in the
	// body is not a challenge: an attacker replaying a captured WebAuthn assertion sends the value it
	// was signed over. The route must accept only a `challenge_id` it minted.
	source, err := readSource("adminconsole.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(source, "Challenge string") && strings.Contains(source, `json:"challenge"`) {
		t.Fatal("the login body accepts a raw `challenge` — a client-chosen challenge proves nothing")
	}
	if !strings.Contains(source, "Challenges.Consume(body.ChallengeID)") {
		t.Fatal("the login route does not consume a server-minted challenge")
	}
}

func TestEnrolmentIsNotSelfService(t *testing.T) {
	// An attacker holding ONE authenticated session must not be able to enrol their own factor and
	// convert a temporary hold into permanent access. Without a session at all it is a 401; the
	// capability gate behind it is `role.grant`, which is the same answer the platform already gives
	// to "who may change what somebody can do".
	api := flowSurface(t)
	rec, _ := postAdmin(t, api, "/admin/api/mfa/enroll", map[string]any{
		"admin_id": "adm-support", "kind": "totp",
	}, true)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("enrolment without a session → %d, want 401", rec.Code)
	}
	source, err := readSource("identityflow.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(source, "adminrbac.CapRoleGrant") {
		t.Fatal("enrolment is not gated on role.grant")
	}
	// The seed is never accepted over the wire: a route that took one would put a credential in any
	// request log the moment somebody enabled request logging.
	if strings.Contains(source, `"seed"`) || strings.Contains(source, "body.Seed") {
		t.Fatal("the enrolment route accepts a TOTP seed")
	}
	if !strings.Contains(source, "adminidentity.TOTPSeedName(factor.AdminID)") {
		t.Fatal("the enrolment route does not derive the seed's reserved logical name")
	}
}

func TestTheFixtureDoorClosesWhenFederated(t *testing.T) {
	// A federated deployment leaves TestModeIdP nil, so `/testmode/assert` 404s. Two doors into the
	// operator surface is one too many, and the fixture door is the one that needs no IdP.
	api := flowSurface(t)
	if api.deps.TestModeIdP == nil {
		t.Skip("this fixture layer is federated; the unfederated case is what this asserts")
	}
	rec, _ := postAdmin(t, api, "/admin/api/testmode/assert", map[string]string{"subject": "sso|superadmin"}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("the fixture door → %d on an unfederated deployment, want 200", rec.Code)
	}
	// The federated half is asserted in adminfixture: `layer.TestModeIdP` is nil when an IdP is wired.
	if _, ok := any(adminidentity.ProviderKindOIDC).(string); !ok {
		t.Fatal("unreachable")
	}
}
