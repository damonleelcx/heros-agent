package api

import (
	"io"

	"encoding/json"
	"github.com/heros-foreal/agentd/internal/auth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/evalboard"
)

type stubBoard struct {
	view     evalboard.View
	ok       bool
	profiles []string
}

func (s *stubBoard) Board(_, workflowID, profile string) (evalboard.View, bool) {
	s.profiles = append(s.profiles, profile)
	v := s.view
	v.WorkflowID = workflowID
	v.Profile = profile
	return v, s.ok
}

// THREE distinct answers for three distinct facts, and keeping them distinct is the point:
//
//	route absent   -> 404 from the mux   "this build does not serve P4 at all"
//	mounted, nil   -> 503                "P4 is wired but has no read model here"
//	no such board  -> 404 naming the id  "this server has no board for that workflow"
//
// Collapsing the middle case into a 404 sends an operator hunting a missing workflow when the
// deployment is what is wrong.
func TestP4MountedWithNoSourceIs503(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountEvalBoard(nil)
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, authedBoardReq(http.MethodGet, "/api/v1/workflows/wf/eval-board", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when mounted without a source, got %d", rec.Code)
	}
}

func TestP4NotMountedAtAllIsRouteAbsent(t *testing.T) {
	s := New(nil, config.Config{})
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, authedBoardReq(http.MethodGet, "/api/v1/workflows/wf/eval-board", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an unregistered route is a 404 from the mux, got %d", rec.Code)
	}
}

func TestP4UnknownWorkflowIs404(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountEvalBoard(&stubBoard{ok: false})
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, authedBoardReq(http.MethodGet, "/api/v1/workflows/nope/eval-board", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for an unknown workflow, got %d", rec.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !strings.Contains(body["error"], "nope") {
		t.Fatalf("the 404 must name the workflow, got %v", body)
	}
}

// The profile is a query parameter on a GET. That is what makes "switching profiles enqueues zero
// runs" structural rather than a promise: a GET cannot enqueue work.
func TestP4ProfileIsAReadParameter(t *testing.T) {
	src := &stubBoard{ok: true, view: evalboard.View{State: evalboard.StateComplete}}
	s := New(nil, config.Config{})
	s.MountEvalBoard(src)

	for _, profile := range []string{"", "cost-optimized", "quality-first"} {
		rec := httptest.NewRecorder()
		url := "/api/v1/workflows/wf/eval-board"
		if profile != "" {
			url += "?profile=" + profile
		}
		s.Handler.ServeHTTP(rec, authedBoardReq(http.MethodGet, url, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("profile %q: want 200, got %d", profile, rec.Code)
		}
		var v evalboard.View
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if v.RunsEnqueued != 0 {
			t.Fatalf("profile %q enqueued %d runs", profile, v.RunsEnqueued)
		}
	}
	if len(src.profiles) != 3 {
		t.Fatalf("want 3 reads, got %v", src.profiles)
	}
}

// A POST to the board endpoint is not routed: the read surface has no write verb.
func TestP4BoardHasNoWriteVerb(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountEvalBoard(&stubBoard{ok: true})
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, authedBoardReq(http.MethodPost, "/api/v1/workflows/wf/eval-board", nil))
	if rec.Code == http.StatusOK {
		t.Fatal("the board endpoint must not accept POST")
	}
}

// authedBoardReq builds a request carrying an authenticated tenant. The board and the scorecard are tenant-scoped
// reads — a variant id is a CONFIG HASH, identical across tenants running the same configuration — so
// every request in these tests carries a principal, exactly as a real one does after auth middleware.
func authedBoardReq(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	return req.WithContext(auth.WithPrincipal(req.Context(), boardTenant))
}

var boardTenant = auth.Principal{TenantID: "t1", Role: "member", APIKeyID: "key-1"}
