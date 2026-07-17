package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// TestHealthz verifies the liveness endpoint is public and returns ok.
func TestHealthz(t *testing.T) {
	s := New(nil, config.Config{})
	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("healthz body not JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("healthz status field = %v, want ok", body["status"])
	}
}

// TestReadyzNilDB tolerates a nil ledger (reports ready when no DB is wired yet).
func TestReadyzNilDB(t *testing.T) {
	s := New(nil, config.Config{})
	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", rr.Code)
	}
}

// TestHealthPublicUnderRequiredAuth confirms health stays reachable even when
// auth_mode=required (auth gates only /api/*).
func TestHealthPublicUnderRequiredAuth(t *testing.T) {
	s := New(nil, config.Config{AuthMode: "required"})
	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz under required auth = %d, want 200 (health is public)", rr.Code)
	}
}

// The health-signal-surface requirement for task 6.1: an operator must be able to tell WHICH secrets
// source is live without reading logs or source. A boot-time log line does not satisfy this — it
// cannot be checked now, on the box that is misbehaving, by a monitor.
func TestReadyz_ReportsWhichSecretsSourceIsLive(t *testing.T) {
	s := New(nil, config.Config{})
	s.SetSecretsSource(providergateway.EnvSecrets{})

	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", rr.Code)
	}
	var body struct {
		Status  string                     `json:"status"`
		Secrets providergateway.SourceInfo `json:"secrets_source"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("readyz body not JSON: %v", err)
	}
	if body.Secrets.Kind != providergateway.SourceKindEnv {
		t.Errorf("secrets_source.kind = %q, want %q — an operator cannot tell what is live",
			body.Secrets.Kind, providergateway.SourceKindEnv)
	}
}

// Swapping the source must actually change what /readyz says. Without this, the field could be a
// constant and the test above would still pass — a health signal that always reports the same thing
// is decoration.
func TestReadyz_TheReportedSourceTracksTheConfiguredOne(t *testing.T) {
	s := New(nil, config.Config{})
	s.SetSecretsSource(providergateway.StaticSecrets{})

	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body struct {
		Secrets providergateway.SourceInfo `json:"secrets_source"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("readyz body not JSON: %v", err)
	}
	if body.Secrets.Kind != providergateway.SourceKindStatic {
		t.Errorf("secrets_source.kind = %q, want %q", body.Secrets.Kind, providergateway.SourceKindStatic)
	}
}

// /readyz is unauthenticated. It must name the door, never what is behind it.
func TestReadyz_NeverCarriesACredential(t *testing.T) {
	const theKey = "sk-readyz-must-not-print-this-9a8b7c"
	s := New(nil, config.Config{})
	s.SetSecretsSource(providergateway.StaticSecrets{
		providergateway.ProviderOpenAI: {APIKey: theKey},
	})

	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if strings.Contains(rr.Body.String(), theKey) {
		t.Fatalf("a credential reached an unauthenticated health endpoint: %s", rr.Body.String())
	}
}

// A deployment that never wired a source says so by omission rather than inventing a status for it.
func TestReadyz_OmitsTheSourceWhenNoneIsWired(t *testing.T) {
	s := New(nil, config.Config{})
	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("readyz body not JSON: %v", err)
	}
	if _, ok := body["secrets_source"]; ok {
		t.Errorf("secrets_source is present with no source wired: %v", body)
	}
}
