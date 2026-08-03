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
	// prepared/withheld are the two halves of the CI fetch. Both are fields so a test can assert the
	// handler emits `withheld` — the list that exists so a customer whose route names an unimplemented
	// forge is told, rather than served an empty array.
	prepared   []fd.Prepared
	withheld   []fd.Withheld
	pendingErr error
}

func (f *fakeP12) ListDeliveries(context.Context, string) ([]fd.DeliveryHead, error) {
	return f.heads, nil
}
func (f *fakeP12) RouteConditionFor(context.Context, string) (fd.RouteCondition, error) {
	return f.cond, nil
}
func (f *fakeP12) Pending(context.Context, string, fd.Target) ([]fd.Prepared, []fd.Withheld, error) {
	return f.prepared, f.withheld, f.pendingErr
}
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

// The CI fetch must SERVE the withheld list, and must serve it even when empty.
//
// 🔴 An absent key is the bug this whole field exists to end, one layer up: a CI runner that has not
// been taught about `withheld` reads a withheld proposal as one that does not exist, which is the same
// silence the empty `prepared` array used to produce.
func TestP12CIPending_ServesWithheldReasons(t *testing.T) {
	src := &fakeP12{withheld: []fd.Withheld{{
		ProposalID: "p1", Kind: fd.WithheldForgeNotImplemented,
		Detail:     "This repository's delivery route names a forge that is declared but not implemented.",
		NextAction: "Point this route at a GitHub repository.",
	}}}
	s := p12Server(src)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ci/pending?target=o/r", nil)
	req.Header.Set("X-API-Key", "k1")
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var got struct {
		Prepared []fd.Prepared `json:"prepared"`
		Withheld []fd.Withheld `json:"withheld"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	if len(got.Withheld) != 1 || got.Withheld[0].Kind != fd.WithheldForgeNotImplemented {
		t.Fatalf("the withheld reason did not reach the runner: %s", rec.Body)
	}
	if got.Withheld[0].NextAction == "" {
		t.Error("a reported condition arrived with no next action")
	}

	// ...and the key is present, not omitted, when nothing was withheld.
	s2 := p12Server(&fakeP12{})
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/ci/pending?target=o/r", nil)
	req2.Header.Set("X-API-Key", "k1")
	rec2 := httptest.NewRecorder()
	s2.Handler.ServeHTTP(rec2, req2)
	if !strings.Contains(rec2.Body.String(), `"withheld"`) {
		t.Errorf("the `withheld` key is omitted when empty — a runner cannot tell an old platform from "+
			"one that withheld nothing: %s", rec2.Body)
	}
}
