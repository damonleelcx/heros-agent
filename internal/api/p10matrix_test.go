package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/studio"
)

type fakeMatrixStore struct{}

func (fakeMatrixStore) ModelCatalog(_ context.Context) ([]registry.ModelCatalogEntry, error) {
	return []registry.ModelCatalogEntry{
		{VersionID: "m1", Name: "sonnet", Provider: "anthropic", ModelID: "claude-sonnet-5"},
		{VersionID: "m2", Name: "gpt", Provider: "openai", ModelID: "gpt-5"},
	}, nil
}
func (fakeMatrixStore) ResolveModel(_ context.Context, versionID string) (*registry.ModelEntry, error) {
	if versionID == "m1" {
		return &registry.ModelEntry{VersionID: "m1", Name: "sonnet", Spec: registry.ModelSpec{Provider: "anthropic", ModelID: "claude-sonnet-5"}}, nil
	}
	return nil, registry.ErrNotFound
}
func (fakeMatrixStore) StudioRender(_ context.Context, _ string, bindings map[string]string) (string, error) {
	return "rendered:" + bindings["ticket"], nil
}

func matrixServer(t *testing.T) *Server {
	t.Helper()
	s := &Server{Mux: http.NewServeMux()}
	wf := studio.NewWorkflowCatalog()
	wf.Load("wf1", &discovery.IR{Nodes: []discovery.IRNode{
		{NodeID: "n_triage", CallSite: discovery.IRCallSite{Symbol: "triage", File: "t.py"}, Model: discovery.IRModel{Provider: "anthropic", ModelID: "claude"}},
	}})
	runner := studio.NewRunner(studio.EchoCompleter{}, studio.NewSpendMeter(studio.Cap{}), studio.FlatPricer(1.0), func() time.Time { return time.Unix(0, 0) })
	s.MountP10Matrix(P10Matrix{Store: fakeMatrixStore{}, Workflows: wf, Binds: studio.NewBindStore(), Runner: runner})
	return s
}

func serveMatrix(t *testing.T, s *Server, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(auth.WithPrincipal(req.Context(), auth.Principal{TenantID: "demo", Role: "member", APIKeyID: "k"}))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	return rec
}

func TestMatrix_ModelCatalogListsRows(t *testing.T) {
	rec := serveMatrix(t, matrixServer(t), "GET", "/api/p10/models", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Models []registry.ModelCatalogEntry `json:"models"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Models) != 2 {
		t.Fatalf("expected 2 model rows, got %d", len(out.Models))
	}
}

func TestMatrix_WorkflowNodesAreColumns(t *testing.T) {
	rec := serveMatrix(t, matrixServer(t), "GET", "/api/p10/workflows/wf1/nodes", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var out struct {
		Nodes []studio.NodeSummary `json:"nodes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Nodes) != 1 || out.Nodes[0].NodeID != "n_triage" || out.Nodes[0].PromptName != "node/n_triage" {
		t.Fatalf("unexpected columns: %+v", out.Nodes)
	}
}

func TestMatrix_MissingWorkflowIs404(t *testing.T) {
	rec := serveMatrix(t, matrixServer(t), "GET", "/api/p10/workflows/nope/nodes", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

func TestMatrix_RunReturnsFigures(t *testing.T) {
	body := `{"model_version_id":"m1","prompt_version_id":"p1","bindings":{"ticket":"T-1"}}`
	rec := serveMatrix(t, matrixServer(t), "POST", "/api/p10/studio/run", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var res studio.Result
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.Output == "" || res.CostUSD <= 0 || res.Kind != "exploratory" {
		t.Fatalf("run must return exploratory output+cost: %+v", res)
	}
}

func TestMatrix_RunUnknownModelIs400(t *testing.T) {
	body := `{"model_version_id":"nope","prompt_version_id":"p1","bindings":{"ticket":"T"}}`
	rec := serveMatrix(t, matrixServer(t), "POST", "/api/p10/studio/run", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestMatrix_BindIsUnverifiedAndReplacesPrior(t *testing.T) {
	s := matrixServer(t)
	b1 := `{"workflow_id":"wf1","node_id":"n_triage","model_version_id":"m1","model_id":"anthropic/claude-sonnet-5","prompt_name":"node/n_triage","prompt_version_id":"p1"}`
	rec := serveMatrix(t, s, "POST", "/api/p10/studio/bind", b1)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Verified bool `json:"verified"`
		InForce  bool `json:"in_force"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Verified {
		t.Fatal("a studio bind must be unverified")
	}
	if !out.InForce {
		t.Fatal("a bind must be in force")
	}
	// Bind a second cell for the same node — replaces the first.
	b2 := `{"workflow_id":"wf1","node_id":"n_triage","model_version_id":"m2","model_id":"openai/gpt-5","prompt_name":"node/n_triage","prompt_version_id":"p2"}`
	serveMatrix(t, s, "POST", "/api/p10/studio/bind", b2)
	rec = serveMatrix(t, s, "GET", "/api/p10/workflows/wf1/bindings", "")
	var bindings struct {
		Bindings map[string]studio.Binding `json:"bindings"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &bindings)
	if len(bindings.Bindings) != 1 {
		t.Fatalf("one bound cell per column: expected 1 node bound, got %d", len(bindings.Bindings))
	}
	if bindings.Bindings["n_triage"].ModelID != "openai/gpt-5" {
		t.Fatalf("the second bind must replace the first: %+v", bindings.Bindings["n_triage"])
	}
}

func TestMatrix_UnauthenticatedRunRefused(t *testing.T) {
	s := matrixServer(t)
	req := httptest.NewRequest("POST", "/api/p10/studio/run", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}
