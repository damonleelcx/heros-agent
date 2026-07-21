package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

type stubPatterns struct {
	views map[string]patternclassifier.GraphView
}

func (s stubPatterns) GraphView(id string) (patternclassifier.GraphView, bool) {
	v, ok := s.views[id]
	return v, ok
}

func p35Server(views map[string]patternclassifier.GraphView) *Server {
	s := New(nil, config.Config{})
	s.MountP35(stubPatterns{views: views})
	return s
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
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/p35/workflows/wf/graph", nil))
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

	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/p35/workflows/nope/graph", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("a missing workflow must 404, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/p35/workflows/wf/graph", nil))
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
	s.Mux.HandleFunc("GET /api/p35/workflows/{workflow_id}/graph", s.handleP35Graph)
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/p35/workflows/wf/graph", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("an unmounted classifier must be 503, not an empty 200: got %d", rec.Code)
	}
}

// The page is served self-contained: no external fetches, so a strict environment renders it the same
// as a permissive one.
func TestP35UIIsSelfContained(t *testing.T) {
	s := p35Server(nil)
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p35/graph", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, bad := range []string{"http://", "https://cdn", "<script src="} {
		if strings.Contains(body, bad) {
			t.Errorf("the page reaches outside itself (%q)", bad)
		}
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("the view must not be cached")
	}
	// The three states the page owes the reader must all be expressible in it.
	for _, want := range []string{"not yet classified", "chip-llm", "chip-rule", "CANDIDATE", "dispatches"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page cannot render %q", want)
		}
	}
}

// REGRESSION (found by looking at the 404 page, not by a test): on an error, the "Subgraph labels"
// card was left standing with an empty body. An empty label card under an error message reads as
// "this workflow has no labels" — a claim about the workflow, made at the exact moment we know
// nothing about the workflow. The page must be able to hide it.
func TestP35UICanHideTheLabelCardOnError(t *testing.T) {
	s := p35Server(nil)
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p35/graph", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `id="labelcard"`) {
		t.Error("the label card has no id, so an error state cannot hide it")
	}
	if !strings.Contains(body, `$("labelcard").hidden = true`) {
		t.Error("the error path does not hide the label card")
	}
}
