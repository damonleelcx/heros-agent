package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/submit"
)

// These cover the half of the UI's contract that is testable without a database: the fail-closed
// rejection shape (task 7.4) and the page itself. The store-backed reads are proved in
// p2_pgproof_test.go against real records, and the rendered states are driven in a real browser —
// asserting on markup here would prove only that the strings I wrote are the strings I wrote.

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerWith(t, ConfigRuntimeStores{}) // no stores: these tests never reach one
}

// newTestServerWith mounts a given set of stores. MountConfigRuntime registers routes on the mux, so it may be
// called exactly once per server — a second call panics on a duplicate pattern.
func newTestServerWith(t *testing.T, st ConfigRuntimeStores) *Server {
	t.Helper()
	s := New(nil, config.Config{})
	s.MountConfigRuntime(st)
	return s
}

func do(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.Handler.ServeHTTP(w, r)
	return w
}

// Task 7.4: a fail-closed rejection names WHICH node and WHICH dimension. The UI can only show what
// the API tells it, so this is where that requirement actually lives.
func TestResolveSpec_RejectionNamesTheNodeAndDimension(t *testing.T) {
	s := newTestServer(t)
	// A spec whose node has overrides but is not in the ordering — a real authoring mistake, and one
	// whose error must point somewhere.
	body := `{"workflow_id":"wf","source_revision":"rev1","order":["n_a"],
	          "nodes":{"n_ghost":{"model_ref":"m1"}},"edges":[]}`
	w := do(t, s, "POST", "/api/v1/specs/resolve", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400. body: %s", w.Code, w.Body)
	}
	var got specError
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, w.Body)
	}
	if got.NodeID != "n_ghost" {
		t.Errorf("node_id = %q, want n_ghost — 'invalid spec' tells a user nothing they can act on", got.NodeID)
	}
	if got.Error == "" {
		t.Error("the rejection carries no message")
	}
}

// A rejection on a specific dimension carries that dimension.
func TestResolveSpec_DimensionIsSurfacedWhenTheFaultIsOnOne(t *testing.T) {
	s := newTestServer(t)
	body := `{"workflow_id":"wf","source_revision":"rev1","order":["n_a"],
	          "nodes":{"n_a":{"skill_refs":["s1","s1"]}},"edges":[]}`
	w := do(t, s, "POST", "/api/v1/specs/resolve", body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	var got specError
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.NodeID != "n_a" || got.Dimension != "skills" {
		t.Errorf("got node=%q dimension=%q, want n_a/skills", got.NodeID, got.Dimension)
	}
}

func TestResolveSpec_ValidSpecReportsItsRefs(t *testing.T) {
	s := newTestServer(t)
	body := `{"workflow_id":"wf","source_revision":"rev1","order":["n_a","n_b"],
	          "nodes":{"n_a":{"model_ref":"m1","prompt_ref":"p1"}},
	          "edges":[{"from_node_id":"n_a","to_node_id":"n_b","kind":"data"}]}`
	w := do(t, s, "POST", "/api/v1/specs/resolve", body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body)
	}
	var got struct {
		Valid bool     `json:"valid"`
		Refs  []string `json:"refs"`
		Nodes int      `json:"nodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Valid || got.Nodes != 2 {
		t.Errorf("got %+v", got)
	}
	// The refs are what the user pinned — showing them back is how they confirm they pinned what they
	// meant to, before anything is generated.
	if len(got.Refs) != 2 {
		t.Errorf("refs = %v, want the two pinned version_ids", got.Refs)
	}
}

// A malformed body is the author's mistake, and saying WHICH is the difference between a fixable
// message and "400".
func TestResolveSpec_MalformedJSONSaysWhatIsWrong(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "POST", "/api/v1/specs/resolve", `{"order":`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not valid JSON") {
		t.Errorf("the error should say the body is malformed, got: %s", w.Body)
	}
}

// FR3: refs are version IDs only. A spec that inlines a definition has content living outside every
// registry, so its config_hash could never resolve back to bytes.
func TestResolveSpec_InlinedDefinitionIsRejected(t *testing.T) {
	s := newTestServer(t)
	body := `{"workflow_id":"wf","source_revision":"rev1","order":["n_a"],
	          "nodes":{"n_a":{"model_ref":{"provider":"openai","model_id":"gpt-5"}}},"edges":[]}`
	w := do(t, s, "POST", "/api/v1/specs/resolve", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a spec inlining a model definition was accepted (status %d)", w.Code)
	}
}

// An unmounted P2 store must say so, not 404. "This deployment has no Postgres" and "that run does
// not exist" send an operator to completely different places.
func TestP2_UnmountedStoreIsServiceUnavailableNotNotFound(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/api/v1/runs/r1", "/api/v1/transforms/abc/rev1"} {
		w := do(t, s, "GET", path, "")
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503 when the store is not mounted", path, w.Code)
		}
	}
	// Submit is mounted the same way and answers the same way. 503 and not 404: the route exists, the
	// deployment simply has no transform target — and an operator who sees 404 goes looking for a
	// routing bug that is not there.
	w := do(t, s, "POST", "/api/v1/specs/submit", `{"variant_id":"v","spec":{}}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("POST /api/v1/specs/submit = %d, want 503 when no submit path is mounted", w.Code)
	}
}

// variant_id is required and must never be defaulted. One variant_id maps to many config_hash values
// across a human's edit history (PRD §8.4); inventing one per submission would make every edit look
// like a brand-new variant and silently destroy that history.
//
// It is rejected BEFORE the service is reached, which is what lets this be proved without a database:
// the check belongs to the request's shape, not to the transform.
func TestSubmitSpec_VariantIDIsRequiredAndNotDefaulted(t *testing.T) {
	s := newTestServerWith(t, ConfigRuntimeStores{Submit: &submit.Service{}}) // non-nil: get past the mounted check
	w := do(t, s, "POST", "/api/v1/specs/submit",
		`{"spec":{"workflow_id":"wf","source_revision":"rev1","order":["n_a"],"nodes":{},"edges":[]}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a submission with no variant_id", w.Code)
	}
	if !strings.Contains(w.Body.String(), "variant_id") {
		t.Errorf("the rejection does not name the missing field: %s", w.Body)
	}
}

// A malformed submission is the author's mistake, and saying WHICH is the difference between a
// fixable message and "400".
func TestSubmitSpec_MalformedJSONSaysWhatIsWrong(t *testing.T) {
	s := newTestServerWith(t, ConfigRuntimeStores{Submit: &submit.Service{}}) // non-nil: get past the mounted check
	w := do(t, s, "POST", "/api/v1/specs/submit", `{"spec":`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not valid JSON") {
		t.Errorf("the error should say the body is malformed, got: %s", w.Body)
	}
}

// The four `TestP2_UI…` tests that lived here were removed with `static/p2.html` in the P9 cutover.
// They asserted properties OF THAT PAGE — that it was served, that it carried a submission into the
// diff and run views, that it rendered verification strength on the build-rejected path, and that it
// read `run.status` from the record rather than deriving a terminal state from the node list.
//
// Every one of those invariants still holds and is still guarded; it moved with the surface. The
// console's `tests/inventory.test.mjs` carries them as **P2-11**, **P2-10**, **P2-14** and **P2-24**,
// one case each, named by inventory id. Deleting a page's tests along with the page is correct; losing
// what they protected would not be, which is why the mapping is written down rather than assumed.
