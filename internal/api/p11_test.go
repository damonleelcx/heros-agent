package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// p11_test.go proves the ingest endpoint attributes to the authenticated tenant server-side and is
// idempotent — the two properties the boundary's server half must guarantee (tasks 3.4, 3.6).

func p11Server(t *testing.T) (*Server, *metering.MemCostEvents, *linkingest.MemStore) {
	t.Helper()
	sub := metering.NewMemCostEvents()
	links := linkingest.NewMemStore()
	ing := linkingest.New(sub, links, func(tenant, run string) string {
		return "https://heros-agent.space/app/runs/" + run
	})
	s := New(nil, config.Config{})
	s.MountP11(ing)
	return s, sub, links
}

func p11Payload(runID string) []byte {
	p := runlink.BuildPayload(runlink.RunRecord{
		RunID: runID, WorkflowID: "wf", ConfigHash: strings.Repeat("a", 64),
		SourceRevision: "rev1", Timestamp: "2026-07-25T00:00:00Z", Seeds: []int64{1000},
		ToolVersion: "0.11.0", Metrics: runlink.Metrics{CostUSD: 0.5, LatencyMS: 200, TokensIn: 100, TokensOut: 40},
		IR: runlink.IRStructure{NodeIDs: []string{"n_1"}}, RunsReported: 2,
	})
	b, _ := json.Marshal(p)
	return b
}

func postLink(t *testing.T, s *Server, tenant string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/run-links", bytes.NewReader(body))
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{TenantID: tenant, Role: "member", APIKeyID: "k"}))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	return rec
}

func TestP11IngestAttributesServerSide(t *testing.T) {
	s, sub, _ := p11Server(t)
	rec := postLink(t, s, "tenantA", p11Payload("run-1"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("link status = %d, body %s", rec.Code, rec.Body.String())
	}
	period := metering.MonthPeriod(mustTime("2026-07-25T00:00:00Z"))
	a, _ := metering.DeriveSUM(sub, "tenantA", period)
	b, _ := metering.DeriveSUM(sub, "tenantB", period)
	if a.Quantity != 0.5 || b.Quantity != 0 {
		t.Errorf("attribution wrong: A=%v B=%v", a.Quantity, b.Quantity)
	}
}

func TestP11IngestIdempotent(t *testing.T) {
	s, sub, _ := p11Server(t)
	body := p11Payload("run-1")
	if rec := postLink(t, s, "tenantA", body); rec.Code != http.StatusCreated {
		t.Fatalf("first link: %d", rec.Code)
	}
	rec := postLink(t, s, "tenantA", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("re-link status = %d, want 409 (idempotent)", rec.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["already_linked"] != true {
		t.Errorf("re-link should report already_linked, got %v", resp)
	}
	sum, _ := metering.DeriveSUM(sub, "tenantA", metering.MonthPeriod(mustTime("2026-07-25T00:00:00Z")))
	if sum.Quantity != 0.5 {
		t.Errorf("SUM double-counted on re-link: %v", sum.Quantity)
	}
}

func TestP11WhoAmI(t *testing.T) {
	s, _, _ := p11Server(t)
	req := httptest.NewRequest("GET", "/api/v1/whoami", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{TenantID: "tenantA", Role: "member", APIKeyID: "k"}))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("whoami status %d", rec.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["identity"] != "tenantA" {
		t.Errorf("whoami identity = %v", resp["identity"])
	}
}

func mustTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
