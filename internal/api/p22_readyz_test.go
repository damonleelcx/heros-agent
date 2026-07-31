package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/config"
)

// p22_readyz_test.go fences the readiness half of P22 (tasks 7.1, 8.5).
//
// The property under test is not "the endpoint returns JSON". It is that an unreachable identity
// provider makes the whole signal NOT READY and NAMES ITSELF — because "not ready" without a subject
// sends an operator to read three dashboards to learn what the response already knew, and a health
// signal read from a UI is not a health signal at all (🔴 health-signal-surface).

type stubAdminIdentity struct{ info adminidentity.ProviderInfo }

func (s stubAdminIdentity) Describe() adminidentity.ProviderInfo { return s.info }

func readyz(t *testing.T, s *Server) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readyz body: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, body
}

func TestReadyzNamesAnUnreachableIdentityProvider(t *testing.T) {
	s := New(nil, config.Config{})
	s.SetAdminIdentity(stubAdminIdentity{info: adminidentity.ProviderInfo{
		Kind: adminidentity.ProviderKindOIDC, Issuer: "https://idp.heros.internal",
	}})

	// A console whose IdP cannot be reached. The console itself is fine — which is exactly the case
	// that a single "customer_console" component would report as healthy.
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"component": "console", "status": "ok",
			"identity_provider": map[string]any{
				"kind": "oidc", "issuer": "https://acme.okta.com", "reachable": false,
				"detail": "discovery returned 503",
			},
		})
	}))
	defer console.Close()
	s.SetIdentityProbe(NewHTTPIdentityProbe(console.URL))

	code, body := readyz(t, s)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; an unreachable IdP must make the deployment not ready", code)
	}
	degraded, _ := body["degraded_components"].([]any)
	found := false
	for _, d := range degraded {
		if d == "identity_provider" {
			found = true
		}
	}
	if !found {
		t.Fatalf("degraded_components = %v; the endpoint must NAME identity_provider", degraded)
	}
	components, _ := body["components"].(map[string]any)
	entry, _ := components["identity_provider"].(map[string]any)
	if entry["kind"] != "oidc" || entry["issuer"] != "https://acme.okta.com" || entry["reachable"] != false {
		t.Fatalf("identity_provider = %v; kind, issuer and reachability are the three things an operator needs", entry)
	}
	admin, _ := body["admin_idp"].(map[string]any)
	if admin["kind"] != adminidentity.ProviderKindOIDC || admin["test_mode"] != false {
		t.Fatalf("admin_idp = %v; the live operator IdP must be named", admin)
	}
	// Never a key, never a secret id — this endpoint is public by necessity.
	raw, _ := json.Marshal(body)
	for _, forbidden := range []string{"client_secret", "signing", "seed", "private"} {
		if json.Valid(raw) && containsFold(string(raw), forbidden) {
			t.Fatalf("/readyz mentions %q; it names the door, never anything behind it", forbidden)
		}
	}
}

func TestReadyzIsReadyWhenTheIdentityProviderAnswers(t *testing.T) {
	s := New(nil, config.Config{})
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"identity_provider": map[string]any{"kind": "configured", "issuer": "", "reachable": true},
		})
	}))
	defer console.Close()
	s.SetIdentityProbe(NewHTTPIdentityProbe(console.URL))

	code, body := readyz(t, s)
	if code != http.StatusOK {
		t.Fatalf("status = %d; a reachable IdP must not degrade the signal (%v)", code, body)
	}
}

func TestReadyzTreatsAnUnreportedIdentityProviderAsNotReady(t *testing.T) {
	// An unreported signal is not a green one. A console too old to report the block, or one that
	// cannot be reached at all, must not be read as healthy — that is the lying health signal.
	s := New(nil, config.Config{})
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"component": "console", "status": "ok"})
	}))
	defer console.Close()
	s.SetIdentityProbe(NewHTTPIdentityProbe(console.URL))

	if code, _ := readyz(t, s); code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d; a console that reports no identity block is not evidence of a healthy one", code)
	}
}

func TestIdentityProbeDoesNotHangOnADeadConsole(t *testing.T) {
	probe := NewHTTPIdentityProbe("http://127.0.0.1:1/health")
	status := probe.Identity(context.Background())
	if status.Reachable {
		t.Fatal("a probe against a closed port reported reachable")
	}
}

func containsFold(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		indexFold(haystack, needle) >= 0
}

func indexFold(s, sub string) int {
	lower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		ok := true
		for j := 0; j < len(sub); j++ {
			if lower(s[i+j]) != lower(sub[j]) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}
