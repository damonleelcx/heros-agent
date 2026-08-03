package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

type stubPatterns struct {
	// keyed tenant\x00workflow, so this stub can express the cross-tenant case the interface now has to
	// survive. A stub that ignored the tenant would make the scoping test pass by construction.
	views map[string]patternclassifier.GraphView
	// tenant is the owner of every view in `views`, for the common single-tenant fixtures.
	tenant string
}

func (s stubPatterns) GraphView(tenantID, id string) (patternclassifier.GraphView, bool) {
	if s.tenant != "" && tenantID != s.tenant {
		return patternclassifier.GraphView{}, false
	}
	v, ok := s.views[id]
	return v, ok
}

func p35Server(views map[string]patternclassifier.GraphView) *Server {
	s := New(nil, config.Config{})
	s.MountPatternGraph(stubPatterns{views: views, tenant: "acme"})
	return s
}

// p35Get issues an AUTHENTICATED graph request. The endpoint is tenant-scoped now, so every fixture
// below has to carry a principal — an unauthenticated read is its own test, at the bottom.
func p35Get(s *Server, tenant, workflowID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+workflowID+"/pattern-graph", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(),
		auth.Principal{TenantID: tenant, Role: "member", APIKeyID: "k"}))
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, req)
	return rec
}

func TestP35GraphServesTheReadModel(t *testing.T) {
	want := patternclassifier.GraphView{
		WorkflowID: "wf", IRVersion: "1.1.0", TaxonomyVersion: patternclassifier.TaxonomyVersion,
		Regions: []patternclassifier.ViewRegion{{
			SubgraphID: "sg_a", NodeIDs: []string{"n_router"},
			Labels: []patternclassifier.ViewLabel{{Pattern: patternclassifier.Routing, Confidence: 0.95, Source: patternclassifier.SourceRule}},
		}},
	}
	s := p35Server(map[string]patternclassifier.GraphView{"wf": want})
	rec := p35Get(s, "acme", "wf")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got patternclassifier.GraphView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Regions) != 1 || got.Regions[0].Labels[0].Pattern != patternclassifier.Routing {
		t.Fatalf("read model did not survive the wire: %+v", got)
	}
	if got.Regions[0].Labels[0].Confidence != 0.95 || got.Regions[0].Labels[0].Source != patternclassifier.SourceRule {
		t.Errorf("confidence/source lost in transit: %+v", got.Regions[0].Labels[0])
	}
}

// "No such workflow" and "workflow exists but is unclassified" are DIFFERENT answers. Collapsing them
// is exactly how the empty state turns into a misleading blank.
func TestP35DistinguishesMissingWorkflowFromUnclassifiedOne(t *testing.T) {
	unclassified := patternclassifier.GraphView{
		WorkflowID: "wf", IRVersion: "1.0.0",
		Unclassified: []patternclassifier.ViewRegion{{SubgraphID: "sg_x", NodeIDs: []string{"n_a"}}},
	}
	s := p35Server(map[string]patternclassifier.GraphView{"wf": unclassified})

	rec := p35Get(s, "acme", "nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("a missing workflow must 404, got %d", rec.Code)
	}

	rec = p35Get(s, "acme", "wf")
	if rec.Code != http.StatusOK {
		t.Fatalf("an unclassified workflow exists and must 200, got %d", rec.Code)
	}
	var got patternclassifier.GraphView
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Unclassified) != 1 {
		t.Error("the unclassified region must be present in the payload as DATA, not as an absence")
	}
}

func TestP35NotMountedIsNotAnEmptyResult(t *testing.T) {
	s := New(nil, config.Config{})
	s.Mux.HandleFunc("GET /api/v1/workflows/{workflow_id}/pattern-graph", s.handlePatternGraph)
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/workflows/wf/pattern-graph", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("an unmounted classifier must be 503, not an empty 200: got %d", rec.Code)
	}
}

// 🔴 The reason the interface changed. Workflow ids are chosen by customers, they collide across
// tenants, and the caller supplies one straight from a URL — so a source keyed on the workflow alone
// serves tenant A's graph to tenant B, with nothing in the code looking wrong. This test is what makes
// the scope a property rather than a convention.
func TestP35DoesNotServeOneTenantsGraphToAnother(t *testing.T) {
	s := p35Server(map[string]patternclassifier.GraphView{"wf": {WorkflowID: "wf"}})

	if rec := p35Get(s, "acme", "wf"); rec.Code != http.StatusOK {
		t.Fatalf("the owning tenant must be served: %d", rec.Code)
	}
	if rec := p35Get(s, "other-corp", "wf"); rec.Code != http.StatusNotFound {
		t.Fatalf("another tenant asked for the SAME workflow id and got %d — a cross-tenant read of a "+
			"customer's workflow shape", rec.Code)
	}
}

func TestP35RefusesAnUnauthenticatedRead(t *testing.T) {
	s := p35Server(map[string]patternclassifier.GraphView{"wf": {WorkflowID: "wf"}})
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/workflows/wf/pattern-graph", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 with no principal, got %d", rec.Code)
	}
}
