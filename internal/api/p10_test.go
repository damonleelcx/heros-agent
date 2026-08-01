package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/registry"
)

// fakeP10 records the names it was called with, so a test can assert the handler scoped by the
// authenticated tenant and never by anything in the request body.
type fakeP10 struct {
	registeredName string
	registeredBody string
	timelineName   string
	timeline       []registry.PromptTimelineEntry
	registerErr    error
}

func (f *fakeP10) RegisterPrompt(_ context.Context, name, body string) (string, error) {
	f.registeredName, f.registeredBody = name, body
	if f.registerErr != nil {
		return "", f.registerErr
	}
	return "deadbeef", nil
}
func (f *fakeP10) PromptNames(_ context.Context, prefix string) ([]string, error) {
	return []string{prefix + "triage"}, nil
}
func (f *fakeP10) PromptTimeline(_ context.Context, name string) ([]registry.PromptTimelineEntry, error) {
	f.timelineName = name
	return f.timeline, nil
}
func (f *fakeP10) DiffPromptVersions(_ context.Context, a, b string) (*registry.PromptVersionDiff, error) {
	return &registry.PromptVersionDiff{VersionA: a, VersionB: b}, nil
}
func (f *fakeP10) StudioRender(_ context.Context, versionID string, bindings map[string]string) (string, error) {
	return "rendered:" + versionID + ":" + bindings["x"], nil
}

func serveP10(t *testing.T, store P10Store, tenant, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	s := &Server{Mux: http.NewServeMux()}
	s.MountP10(store)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if tenant != "" {
		req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{TenantID: tenant, Role: "member"}))
	}
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	return rec
}

func TestPublishPrompt_ScopesByAuthenticatedTenantNotBody(t *testing.T) {
	f := &fakeP10{}
	// The body carries a bogus tenant field; it must be ignored. Scope comes from the principal.
	rec := serveP10(t, f, "tenant-A", "POST", "/api/v1/prompts/publish",
		`{"name":"triage","body":"Hi {{x}}","tenant_id":"tenant-EVIL"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if f.registeredName != "t:tenant-A/triage" {
		t.Fatalf("stored name = %q, want tenant-A scoped; a client tenant must not widen scope", f.registeredName)
	}
}

func TestPublishPrompt_UnauthenticatedIsRefused(t *testing.T) {
	f := &fakeP10{}
	rec := serveP10(t, f, "", "POST", "/api/v1/prompts/publish", `{"name":"triage","body":"hi"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if f.registeredName != "" {
		t.Fatalf("no version may be created for an unauthenticated request")
	}
}

func TestPublishPrompt_MalformedTemplateIs400(t *testing.T) {
	// RegisterPrompt returns an ErrInvalidEntry for a body that does not parse; the handler surfaces it
	// as a 400 (author's mistake), not a 500 (ours).
	f := &fakeP10{registerErr: registry.ErrInvalidEntry}
	rec := serveP10(t, f, "tenant-A", "POST", "/api/v1/prompts/publish", `{"name":"triage","body":"broken {{"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a malformed template", rec.Code)
	}
}

func TestPromptTimeline_ScopesLookupByTenantAndStripsPrefix(t *testing.T) {
	f := &fakeP10{timeline: []registry.PromptTimelineEntry{{VersionID: "v1", Name: "t:tenant-A/triage", Slots: []string{"x"}}}}
	rec := serveP10(t, f, "tenant-A", "GET", "/api/v1/prompts/triage/timeline", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if f.timelineName != "t:tenant-A/triage" {
		t.Fatalf("timeline lookup name = %q, want tenant-scoped", f.timelineName)
	}
	var out struct {
		Versions []registry.PromptTimelineEntry `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Versions) != 1 || out.Versions[0].Name != "triage" {
		t.Fatalf("display name should have the tenant prefix stripped, got %+v", out.Versions)
	}
}

func TestPromptDiff_RequiresBothIds(t *testing.T) {
	rec := serveP10(t, &fakeP10{}, "tenant-A", "GET", "/api/v1/prompts/diff?a=x", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when b is missing", rec.Code)
	}
}

func TestPromptImpact_ReportsBlockedNode(t *testing.T) {
	body := `{"proposed_body":"Triage {{ticket}} for {{tier}}","nodes":[{"NodeID":"n","CallSiteExprs":["ticket"],"Analyzable":true}]}`
	rec := serveP10(t, &fakeP10{}, "tenant-A", "POST", "/api/v1/prompts/impact", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out registry.PromptImpactAnalysis
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Blocked) != 1 || out.Blocked[0].NodeID != "n" {
		t.Fatalf("expected node n blocked for unbindable {{tier}}, got %+v", out)
	}
}

func TestMountP10_NotMountedIs503(t *testing.T) {
	s := &Server{Mux: http.NewServeMux()}
	s.MountP10(nil)
	req := httptest.NewRequest("POST", "/api/v1/prompts/publish",
		strings.NewReader(`{"name":"x","body":"y"}`))
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{TenantID: "t"}))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when the registry is not mounted", rec.Code)
	}
}
