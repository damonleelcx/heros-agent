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
	s.MountRunLinking(ing)
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

// ── the READ side ────────────────────────────────────────────────────────────────────────────────
//
// These exist because linking had none. A run went in, was stored durably, answered `409 already_linked`
// on a re-link — and could not be read back by anything except a coverage COUNT. The console asked
// `/api/v1/runs/{id}`, which reads the EXECUTOR's table, and told the user **no such run** about a run
// the platform was holding. Every assertion below is one of the four answers that endpoint must be able
// to give, kept distinguishable from the other three.

func TestLinkedRunIsReadableAfterItIsLinked(t *testing.T) {
	s, _, _ := p11Server(t)
	rec := postLink(t, s, "acme", p11PayloadWithScores("run-read-1"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("link: want 201, got %d: %s", rec.Code, rec.Body.String())
	}

	got := getLinkedRun(t, s, "acme", "run-read-1")
	if got.Code != http.StatusOK {
		t.Fatalf("read back a linked run: want 200, got %d: %s", got.Code, got.Body.String())
	}
	var view LinkedRunView
	if err := json.Unmarshal(got.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.RunID != "run-read-1" || view.WorkflowID != "wf" {
		t.Fatalf("wrong subject: %+v", view)
	}
	if view.ConfigHash12 != strings.Repeat("a", 12) {
		t.Fatalf("config hash display should be the first 12 chars, got %q", view.ConfigHash12)
	}
	// 🔴 The scores are the point. A linked run whose numbers do not come back is a link that recorded
	// nothing a human can use — which is exactly the state this endpoint was written to end.
	if len(view.Scores) != 2 {
		t.Fatalf("want the 2 recorded scores, got %d: %+v", len(view.Scores), view.Scores)
	}
	if view.Scores[0].Metric != "quality" || view.Scores[0].Value != 0.8039 {
		t.Fatalf("scores must come back VERBATIM, not recomputed: %+v", view.Scores[0])
	}
	if view.Scores[0].CILow != 0.75 || view.Scores[0].CIHigh != 0.8618 {
		t.Fatalf("the interval must survive the round trip — a value without one is a claim we do not make: %+v",
			view.Scores[0])
	}
}

func TestAnUnlinkedRunIs404AndNotAnEmptySuccess(t *testing.T) {
	s, _, _ := p11Server(t)
	got := getLinkedRun(t, s, "acme", "run-never-linked")
	if got.Code != http.StatusNotFound {
		t.Fatalf("want 404 for a run nobody linked, got %d: %s", got.Code, got.Body.String())
	}
}

// A run linked by ANOTHER tenant must read as not-linked, never as data. A run id is guessable, so the
// scope is the authenticated principal and nothing else.
func TestALinkedRunIsNotReadableByAnotherTenant(t *testing.T) {
	s, _, _ := p11Server(t)
	if rec := postLink(t, s, "acme", p11PayloadWithScores("run-tenant-scoped")); rec.Code != http.StatusCreated {
		t.Fatalf("link: %d", rec.Code)
	}
	got := getLinkedRun(t, s, "other-corp", "run-tenant-scoped")
	if got.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant read must be 404, got %d: %s", got.Code, got.Body.String())
	}
}

// An unmounted capability answers 503, never 404. The console renders those two as different sentences
// with different next actions, and collapsing them is the failure this whole surface is careful about.
func TestReadingALinkedRunWithoutTheCapabilityIs503(t *testing.T) {
	s := New(nil, config.Config{})
	s.Mux.HandleFunc("GET /api/v1/runs/{run_id}/link", s.handleLinkedRun)
	got := getLinkedRun(t, s, "acme", "run-x")
	if got.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when run-linking is not mounted, got %d: %s", got.Code, got.Body.String())
	}
}

func TestReadingALinkedRunUnauthenticatedIs401(t *testing.T) {
	s, _, _ := p11Server(t)
	req := httptest.NewRequest("GET", "/api/v1/runs/run-x/link", nil)
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 with no principal, got %d: %s", rec.Code, rec.Body.String())
	}
}

func p11PayloadWithScores(runID string) []byte {
	p := runlink.BuildPayload(runlink.RunRecord{
		RunID: runID, WorkflowID: "wf", ConfigHash: strings.Repeat("a", 64),
		SourceRevision: "rev1", Timestamp: "2026-07-25T00:00:00Z", Seeds: []int64{1000},
		ToolVersion: "0.11.0",
		Metrics:     runlink.Metrics{CostUSD: 0.5, LatencyMS: 200, TokensIn: 100, TokensOut: 40},
		IR:          runlink.IRStructure{NodeIDs: []string{"n_1"}},
		Scores: []runlink.Score{
			{Metric: "quality", Value: 0.8039, CILow: 0.75, CIHigh: 0.8618},
			{Metric: "cost_usd", Value: 0.2097, CILow: 0.1941, CIHigh: 0.2260},
		},
		RunsReported: 2,
	})
	b, _ := json.Marshal(p)
	return b
}

func getLinkedRun(t *testing.T, s *Server, tenant, runID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/runs/"+runID+"/link", nil)
	if tenant != "" {
		req = req.WithContext(auth.WithPrincipal(req.Context(),
			auth.Principal{TenantID: tenant, Role: "member", APIKeyID: "k"}))
	}
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	return rec
}
