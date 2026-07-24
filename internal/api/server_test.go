package api

import (
	"context"
	"encoding/json"
	"errors"
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

// ── P9 · console component aggregation (FR25, task 4.4) ──────────────────────
//
// The requirement is that a healthy Go service in front of an UNREACHABLE console does not report
// ready, and that the degraded component is NAMED on a machine-readable endpoint. Both halves matter:
// "not ready" without a subject sends an operator to read three dashboards to learn what the response
// already knew.

// stubProbe is a ComponentProbe whose answer the test dictates.
type stubProbe struct {
	name string
	err  error
}

func (p stubProbe) Name() string                { return p.name }
func (p stubProbe) Probe(context.Context) error { return p.err }

func TestReadyz_NoComponentsKeyWhenNoConsoleIsWired(t *testing.T) {
	// A deployment that ships no console has no console component. An empty `components` map would
	// assert that it HAS components and they are all fine — a different claim from having none, and the
	// same reasoning that keeps `secrets_source` absent rather than "unknown".
	s := New(nil, config.Config{})
	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("readyz body not JSON: %v", err)
	}
	if _, ok := body["components"]; ok {
		t.Errorf("readyz reported a components map with no component wired: %v", body["components"])
	}
}

func TestReadyz_ReportsReadyWhenTheConsoleIsReachable(t *testing.T) {
	s := New(nil, config.Config{})
	s.SetConsoleProbe(stubProbe{name: "console"})
	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("readyz body not JSON: %v", err)
	}
	components, ok := body["components"].(map[string]any)
	if !ok {
		t.Fatalf("readyz did not report a components map: %v", body)
	}
	console, ok := components["console"].(map[string]any)
	if !ok || console["status"] != "ready" {
		t.Errorf("console component = %v, want status ready", components["console"])
	}
}

func TestReadyz_NotReadyAndNamesTheConsoleWhenItIsUnreachable(t *testing.T) {
	s := New(nil, config.Config{})
	s.SetConsoleProbe(stubProbe{name: "console", err: errors.New("unreachable: connection refused")})
	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	// The Go service itself is perfectly healthy here. That is precisely the case a readiness signal
	// gets wrong: it reports on itself while the surface users actually reach is dead.
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz status = %d, want 503 — a healthy service with a dead console is not ready", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("readyz body not JSON: %v", err)
	}
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", body["status"])
	}
	degraded, ok := body["degraded_components"].([]any)
	if !ok || len(degraded) != 1 || degraded[0] != "console" {
		t.Fatalf("degraded_components = %v, want [console] — the degraded component must be NAMED", body["degraded_components"])
	}
	components := body["components"].(map[string]any)
	console := components["console"].(map[string]any)
	if console["status"] != "degraded" {
		t.Errorf("console component status = %v, want degraded", console["status"])
	}
	if !strings.Contains(console["detail"].(string), "connection refused") {
		t.Errorf("console detail = %v, want the probe's reason carried through", console["detail"])
	}
}

func TestHTTPComponentProbe_ReportsUnreachableRatherThanHanging(t *testing.T) {
	// A probe against a closed port must return promptly with a reason. A readiness probe that can
	// hang is not a readiness probe — the orchestrator's own deadline fires first and the operator
	// learns "timeout" instead of "the console is unreachable".
	probe := NewHTTPComponentProbe("console", "http://127.0.0.1:1/api/health")
	err := probe.Probe(context.Background())
	if err == nil {
		t.Fatal("probe against a closed port returned nil")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("probe error = %v, want it to say unreachable", err)
	}
}

func TestHTTPComponentProbe_ReportsANonOKHealthEndpoint(t *testing.T) {
	// A console that answers but is unhealthy is degraded, not ready. A probe that only checked
	// reachability would report ready for a process that is up and broken.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer down.Close()
	probe := NewHTTPComponentProbe("console", down.URL)
	if err := probe.Probe(context.Background()); err == nil {
		t.Fatal("probe against a 503 health endpoint returned nil")
	}
}
