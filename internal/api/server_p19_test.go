package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
)

// P19 readiness aggregation (deployment-topology task 3.1, admin-console-deploy, platform-llm-access).
// The deployment ships MORE than one dependent component, and /readyz must aggregate EVERY one it
// ships and NAME the degraded one — a partial aggregation is the lying signal, one store below the fold.

func TestReadyz_AggregatesEveryComponentAndNamesTheDegradedOne(t *testing.T) {
	s := New(nil, config.Config{})
	// A realistic platform set: customer console, operator console, object store, queue — with the
	// operator console the one that is down.
	s.AddComponentProbe(stubProbe{name: "customer_console"})
	s.AddComponentProbe(stubProbe{name: "admin_console", err: errors.New("unreachable: connection refused")})
	s.AddComponentProbe(stubProbe{name: "object_store"})
	s.AddComponentProbe(stubProbe{name: "queue"})

	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — one dead component makes the whole deployment not-ready", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	comps, ok := body["components"].(map[string]any)
	if !ok {
		t.Fatalf("no components map: %v", body)
	}
	// Every wired component is reported — the healthy ones too, so the aggregation is not partial.
	for _, name := range []string{"customer_console", "admin_console", "object_store", "queue"} {
		if _, ok := comps[name].(map[string]any); !ok {
			t.Errorf("component %q not aggregated into /readyz: %v", name, comps)
		}
	}
	// The degraded one is NAMED, so a monitor alerts on admin_console, not on "not ready".
	degraded, ok := body["degraded_components"].([]any)
	if !ok || len(degraded) != 1 || degraded[0] != "admin_console" {
		t.Fatalf("degraded_components = %v, want [admin_console]", body["degraded_components"])
	}
}

// platform-llm-access task 3.2 / 6.4: a wired secret source whose store is unreachable turns /readyz
// not-ready and NAMES secrets_source — fail-closed, and (for the air-gapped on-prem gateway)
// degraded-not-available rather than a crash.
func TestReadyz_FailsClosedWhenTheSecretSourceIsUnreachable(t *testing.T) {
	s := New(nil, config.Config{})
	s.AddComponentProbe(stubProbe{name: "secrets_source", err: errors.New("unreachable: on-prem gateway down")})
	// The rest of the platform is fine.
	s.AddComponentProbe(stubProbe{name: "object_store"})

	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — an unreachable secret source must fail closed, not report ready", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	degraded, _ := body["degraded_components"].([]any)
	found := false
	for _, d := range degraded {
		if d == "secrets_source" {
			found = true
		}
	}
	if !found {
		t.Fatalf("secrets_source not named as degraded: %v — the fail-closed signal must name the source", degraded)
	}
}

// Every stub-probe path stays green when all components are reachable — the whole deployment is ready
// only when every aggregated component is.
func TestReadyz_ReadyOnlyWhenEveryComponentIsReachable(t *testing.T) {
	s := New(nil, config.Config{})
	for _, n := range []string{"customer_console", "admin_console", "object_store", "queue", "vector_store", "graph_store"} {
		s.AddComponentProbe(stubProbe{name: n})
	}
	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — every component reachable is a ready deployment; body=%s", rr.Code, rr.Body.String())
	}
}

// AddComponentProbe ignores a nil probe, so a launch path can wire a component conditionally without a
// guard at every call site.
func TestAddComponentProbe_IgnoresNil(t *testing.T) {
	s := New(nil, config.Config{})
	s.AddComponentProbe(nil)
	rr := httptest.NewRecorder()
	s.Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a nil probe must not create a degraded phantom component", rr.Code)
	}
}
