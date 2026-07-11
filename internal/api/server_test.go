package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
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
