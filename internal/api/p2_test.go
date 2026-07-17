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
	return newTestServerWith(t, P2Stores{}) // no stores: these tests never reach one
}

// newTestServerWith mounts a given set of stores. MountP2 registers routes on the mux, so it may be
// called exactly once per server — a second call panics on a duplicate pattern.
func newTestServerWith(t *testing.T, st P2Stores) *Server {
	t.Helper()
	s := New(nil, config.Config{})
	s.MountP2(st)
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
	w := do(t, s, "POST", "/api/p2/specs/resolve", body)

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
	w := do(t, s, "POST", "/api/p2/specs/resolve", body)

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
	w := do(t, s, "POST", "/api/p2/specs/resolve", body)

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
	w := do(t, s, "POST", "/api/p2/specs/resolve", `{"order":`)
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
	w := do(t, s, "POST", "/api/p2/specs/resolve", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a spec inlining a model definition was accepted (status %d)", w.Code)
	}
}

// An unmounted P2 store must say so, not 404. "This deployment has no Postgres" and "that run does
// not exist" send an operator to completely different places.
func TestP2_UnmountedStoreIsServiceUnavailableNotNotFound(t *testing.T) {
	s := newTestServer(t)
	for _, path := range []string{"/api/p2/runs/r1", "/api/p2/transforms/abc/rev1"} {
		w := do(t, s, "GET", path, "")
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s = %d, want 503 when the store is not mounted", path, w.Code)
		}
	}
	// Submit is mounted the same way and answers the same way. 503 and not 404: the route exists, the
	// deployment simply has no transform target — and an operator who sees 404 goes looking for a
	// routing bug that is not there.
	w := do(t, s, "POST", "/api/p2/specs/submit", `{"variant_id":"v","spec":{}}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("POST /api/p2/specs/submit = %d, want 503 when no submit path is mounted", w.Code)
	}
}

// variant_id is required and must never be defaulted. One variant_id maps to many config_hash values
// across a human's edit history (PRD §8.4); inventing one per submission would make every edit look
// like a brand-new variant and silently destroy that history.
//
// It is rejected BEFORE the service is reached, which is what lets this be proved without a database:
// the check belongs to the request's shape, not to the transform.
func TestSubmitSpec_VariantIDIsRequiredAndNotDefaulted(t *testing.T) {
	s := newTestServerWith(t, P2Stores{Submit: &submit.Service{}}) // non-nil: get past the mounted check
	w := do(t, s, "POST", "/api/p2/specs/submit",
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
	s := newTestServerWith(t, P2Stores{Submit: &submit.Service{}}) // non-nil: get past the mounted check
	w := do(t, s, "POST", "/api/p2/specs/submit", `{"spec":`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not valid JSON") {
		t.Errorf("the error should say the body is malformed, got: %s", w.Body)
	}
}

// The submit page must carry the user from a submission into the two views WITHOUT retyping an
// identifier. That is the whole of task 7.2's "submit → review → watch" being one flow rather than
// three tools, and it is the part that was missing while 7.2 was marked done.
//
// Asserted on the markup only because the wiring IS the markup here: the page must read the
// submission's own config_hash and run_id back into the panels' inputs. The behaviour is proved for
// real against live Postgres in internal/submit.
func TestP2_UICarriesASubmissionIntoTheDiffAndRunViews(t *testing.T) {
	s := newTestServer(t)
	body := do(t, s, "GET", "/p2", "").Body.String()

	if !strings.Contains(body, "/api/p2/specs/submit") {
		t.Fatal("the page has no submit; a user cannot get a config_hash without hand-pasting one")
	}
	// The response's coordinates are fed into panel 2's and panel 3's inputs.
	for _, wiring := range []string{`$("cfg").value = j.config_hash`, `$("rev").value = j.source_revision`,
		`$("run").value = j.run_id`} {
		if !strings.Contains(body, wiring) {
			t.Errorf("the page does not carry the submission into the next view: missing %q", wiring)
		}
	}
	// A build-rejected submission must stay a DISTINCT, legible state rather than collapsing into the
	// generic failure path (task 7.3).
	if !strings.Contains(body, `j.transform_status === "build-rejected"`) {
		t.Error("submit does not treat build-rejected as its own state")
	}
	// variant_id and seed must be their own inputs. Folding either into the spec textarea would fold
	// them into config_hash and break both the variant's edit history and multi-seed rollup.
	for _, id := range []string{`id="variant-id"`, `id="seed"`} {
		if !strings.Contains(body, id) {
			t.Errorf("the page has no %s input", id)
		}
	}
}

// F3: the strength must survive the build-rejected path.
//
// The rejected branch of load-transform used to `return` before the code that renders the strength
// badge, so a rejected transform showed "build-rejected" and nothing about what gate rejected it —
// even though the DB column and the API response both carry it on that path.
//
// That silence is a claim, and the wrong one. verify.go: "WHICH gate rejected it is information". The
// two rejections mean genuinely different things and send a reviewer to different places:
//
//	type-checked   + rejected -> a COMPILER read this rewrite and refused it
//	syntax-checked + rejected -> the rewrite does not even PARSE — much cruder, much worse
//
// ADR-003 decision 3 ("a reviewer must be able to see, without asking, whether a compiler stood
// behind it") has no exception for the unhappy path.
//
// Asserted on the markup because the wiring IS the markup here, the same way this file already
// asserts the submit→review flow. The API's half of it is proved against live Postgres in
// p2_transform_pgproof_test.go.
func TestP2_UIRendersVerificationStrengthOnTheBuildRejectedPath(t *testing.T) {
	s := newTestServer(t)
	body := do(t, s, "GET", "/p2", "").Body.String()

	// Isolate the rejected branch of load-transform: from the status test to the `return` that ends it.
	const marker = `if (t.status === "build-rejected") {`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("the transform view no longer has a build-rejected branch (%q); this test is testing "+
			"nothing and must be updated rather than deleted", marker)
	}
	rest := body[i:]
	end := strings.Index(rest, "return;")
	if end < 0 {
		t.Fatal("the build-rejected branch has no return; cannot delimit it")
	}
	branch := rest[:end]

	if !strings.Contains(branch, "t.verification_strength") {
		t.Errorf("the build-rejected branch renders no verification_strength, so a reviewer cannot tell "+
			"a compiler's rejection from a diff that does not parse. The record and the API both carry "+
			"it on this path; only the UI drops it.\nbranch:\n%s", branch)
	}
	// Reused anchors, not invented ones (🚫 禁止即兴定样式). Both classes already exist in the stylesheet
	// and are what the built path uses.
	if !strings.Contains(branch, "state-${esc(t.verification_strength)}") {
		t.Errorf("the strength is not rendered with the existing state-* chip the built path uses; a "+
			"second spelling of the same badge is a second thing to keep in sync.\nbranch:\n%s", branch)
	}
	for _, cls := range []string{".state-type-checked", ".state-syntax-checked"} {
		if !strings.Contains(body, cls) {
			t.Errorf("the page has no %s rule, so the badge this branch now renders would be unstyled", cls)
		}
	}
}

func TestP2_UIIsServed(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "GET", "/p2", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /p2 = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	// The page reads run records that reference prompts and completions; it is not something to leave
	// in a shared cache.
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	body := w.Body.String()
	// The states task 7.3 requires be first-class and DISTINCT. If a state has no style rule, it
	// renders identically to its neighbours and the requirement is unmet — a user cannot tell a halt
	// from a failure.
	for _, state := range []string{"state-running", "state-succeeded", "state-failed",
		"state-halted", "state-build-rejected"} {
		if !strings.Contains(body, "."+state) {
			t.Errorf("the page has no distinct style for %q; it would look like every other state", state)
		}
	}
	if !strings.Contains(body, "MountP2") && !strings.Contains(body, "/api/p2/runs/") {
		t.Error("the page does not call the run endpoint")
	}
}

// The UI must never derive a status. A run whose nodes all succeeded but which was HALTED is exactly
// what a derived status gets wrong, and it is the case that matters most (task 7.3).
func TestP2_UIDoesNotDeriveTerminalStatusFromTheNodeList(t *testing.T) {
	s := newTestServer(t)
	body := do(t, s, "GET", "/p2", "").Body.String()

	// The status shown must come from the record's field.
	if !strings.Contains(body, "run.status") {
		t.Error("the page does not render run.status from the record")
	}
	// Heuristics that would derive it. Any of these appearing means the page decided a status for
	// itself, which is what 7.3 forbids.
	for _, derived := range []string{
		"nodes.every(", "nodes.some(", ".filter(n => n.status", "allSucceeded", "hasFailed",
	} {
		if strings.Contains(body, derived) {
			t.Errorf("the page derives a status with %q instead of reading the record", derived)
		}
	}
}
