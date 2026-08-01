package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
)

// fakeP12 is a ForgeDeliverySource that returns fixed heads + a route condition, for the served-rendering test.
type fakeP12 struct {
	heads []fd.DeliveryHead
	cond  fd.RouteCondition
}

func (f *fakeP12) ListDeliveries(context.Context, string) ([]fd.DeliveryHead, error) {
	return f.heads, nil
}
func (f *fakeP12) RouteConditionFor(context.Context, string) (fd.RouteCondition, error) {
	return f.cond, nil
}
func (f *fakeP12) Pending(context.Context, string, fd.Target) ([]fd.Prepared, error) { return nil, nil }
func (f *fakeP12) RecordReport(context.Context, string, fd.Prepared, fd.Report) (fd.Result, error) {
	return fd.Result{}, nil
}

func p12Server(src ForgeDeliverySource) *Server {
	cfg := config.Config{AuthMode: "required", TenantCredentials: []config.TenantCredential{
		{TenantID: "t1", APIKey: "k1", Role: "member"},
	}}
	s := New(nil, cfg)
	s.MountForgeDelivery(src)
	return s
}

// 7.6 — visibility asserts as a RENDERING the console consumes: the served JSON carries the route
// condition (kind + next action) and each delivery's state + a proposal link, not just a log line.
func TestP12Deliveries_RendersConditionAndStates(t *testing.T) {
	src := &fakeP12{
		heads: []fd.DeliveryHead{
			{DeliveryID: "d1", TenantID: "t1", ConfigHash: "c1", SourceRevision: "r1", Target: "o/r", Mode: fd.ModeCI, State: fd.StateMerged, MergeCommit: "abc"},
		},
		cond: fd.RouteCondition{Kind: fd.RouteAbsent, Detail: "no route", NextAction: "configure a route"},
	}
	s := p12Server(src)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/deliveries", nil)
	req.Header.Set("X-API-Key", "k1")
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var view DeliveriesView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Route.Kind != "no_route" || view.Route.NextAction == "" {
		t.Errorf("route condition not rendered with a next action: %+v", view.Route)
	}
	if len(view.Deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(view.Deliveries))
	}
	d := view.Deliveries[0]
	if d.State != "merged" || d.ProposalRef == "" {
		t.Errorf("delivery not rendered with state + proposal link: %+v", d)
	}
}

// The delivery surface requires an authenticated tenant — an unauthenticated request is refused, never
// served another tenant's deliveries.
func TestP12Deliveries_RequiresAuth(t *testing.T) {
	s := p12Server(&fakeP12{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/deliveries", nil) // no key
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated delivery read = %d, want 401", rec.Code)
	}
}

// The CI report handler rejects a reported delivery id that does not match its claimed identity — a
// runner cannot fabricate a delivery id for arbitrary identity.
func TestP12CIReport_RejectsInconsistentIdentity(t *testing.T) {
	s := p12Server(&fakeP12{})
	body := `{"delivery_id":"deadbeef","config_hash":"c1","source_revision":"r1","target":"o/r","mode":"ci","forge_ref":"o/r#1","created":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ci/report", strings.NewReader(body))
	req.Header.Set("X-API-Key", "k1")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("inconsistent identity report = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
