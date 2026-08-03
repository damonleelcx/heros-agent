package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// sourcepush_test.go covers the ingest that accepts something no other endpoint here does — the
// customer's source. The controls are different in kind from the allowlist the other ingests apply, so
// they are tested for directly: who may push, whose tenant the bundle lands under, and what a caller is
// told when the snapshot they are asking about was never sent.

// fakeSourceStore records what it was asked to store.
type fakeSourceStore struct {
	mu      sync.Mutex
	put     map[string][]byte
	deleted []sourceingest.Ref
	putErr  error
}

func newFakeSourceStore() *fakeSourceStore {
	return &fakeSourceStore{put: map[string][]byte{}}
}

func (f *fakeSourceStore) Put(_ context.Context, ref sourceingest.Ref, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	f.put[ref.String()] = append([]byte(nil), data...)
	return nil
}

func (f *fakeSourceStore) Delete(_ context.Context, ref sourceingest.Ref) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, ref)
	return nil
}

// fakeDiscovery returns a canned summary, or an error.
type fakeDiscovery struct {
	summary DiscoverySummary
	err     error
	calls   []sourceingest.Ref
}

func (f *fakeDiscovery) Discover(_ context.Context, ref sourceingest.Ref) (DiscoverySummary, error) {
	f.calls = append(f.calls, ref)
	if f.err != nil {
		return DiscoverySummary{}, f.err
	}
	return f.summary, nil
}

func sourceRequest(t *testing.T, s *Server, method, path, body string, principal *auth.Principal) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if principal != nil {
		req = req.WithContext(auth.WithPrincipal(req.Context(), *principal))
	}
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	return rec
}

var pusher = auth.Principal{TenantID: "t1", Role: "member", APIKeyID: "key-1"}

func TestSourcePushUnmountedAnswers503(t *testing.T) {
	s := New(nil, config.Config{})
	// Mounted with a nil store: the ROUTE must exist so the answer is "this deployment does not accept
	// source" rather than a 404 that reads as a broken URL.
	s.MountSourcePush(nil, nil)

	rec := sourceRequest(t, s, "PUT", "/api/v1/workflows/wf/source/rev", "bytes", &pusher)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestSourcePushRequiresAnAuthenticatedTenant(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountSourcePush(newFakeSourceStore(), nil)

	rec := sourceRequest(t, s, "PUT", "/api/v1/workflows/wf/source/rev", "bytes", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an unauthenticated push", rec.Code)
	}
}

// TestSourcePushLandsUnderThePrincipalsTenant is the cross-tenant guard. The tenant is taken from the
// authenticated principal and from nowhere else — a tenant id a caller can name is one a caller can
// change, and this endpoint stores their source under it.
func TestSourcePushLandsUnderThePrincipalsTenant(t *testing.T) {
	store := newFakeSourceStore()
	s := New(nil, config.Config{})
	s.MountSourcePush(store, nil)

	rec := sourceRequest(t, s, "PUT", "/api/v1/workflows/wf-a/source/rev-1", "archive-bytes", &pusher)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	want := sourceingest.Ref{TenantID: "t1", WorkflowID: "wf-a", SourceRevision: "rev-1"}
	if _, ok := store.put[want.String()]; !ok {
		t.Fatalf("bundle was not stored under %s; stored keys = %v", want, keysOf(store.put))
	}
}

// TestSourcePushAnswers202NotOK: the bundle is stored and discovery has NOT run. A 200 would tell a CLI
// the graph is ready, and its next request would 404 on a workflow the user was just told we accepted.
func TestSourcePushAnswers202NotOK(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountSourcePush(newFakeSourceStore(), nil)

	rec := sourceRequest(t, s, "PUT", "/api/v1/workflows/wf/source/rev", "bytes", &pusher)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 — storing a snapshot is not the same as having discovered it", rec.Code)
	}
}

func TestEmptySourcePushIsRefused(t *testing.T) {
	store := newFakeSourceStore()
	s := New(nil, config.Config{})
	s.MountSourcePush(store, nil)

	rec := sourceRequest(t, s, "PUT", "/api/v1/workflows/wf/source/rev", "", &pusher)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an empty bundle", rec.Code)
	}
	if len(store.put) != 0 {
		t.Error("an empty bundle was stored; a failed upload must not be recorded as a snapshot")
	}
}

// TestSourceDeleteIsTheRetraction: "stop holding my source" has to be something a customer can do.
func TestSourceDeleteRemovesTheSnapshot(t *testing.T) {
	store := newFakeSourceStore()
	s := New(nil, config.Config{})
	s.MountSourcePush(store, nil)

	rec := sourceRequest(t, s, "DELETE", "/api/v1/workflows/wf-a/source/rev-1", "", &pusher)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if len(store.deleted) != 1 || store.deleted[0].WorkflowID != "wf-a" || store.deleted[0].TenantID != "t1" {
		t.Fatalf("deleted = %+v, want one delete of t1/wf-a", store.deleted)
	}
}

// TestSourceDeleteOfAnAbsentSnapshotIs204: 404 here would let a caller probe which revisions a tenant
// has pushed, and the outcome the caller asked for happened either way.
func TestSourceDeleteOfAnAbsentSnapshotIs204(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountSourcePush(newFakeSourceStore(), nil)

	rec := sourceRequest(t, s, "DELETE", "/api/v1/workflows/never-pushed/source/rev", "", &pusher)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 even when nothing was there", rec.Code)
	}
}

func TestDiscoverWithoutARunnerAnswers503(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountSourcePush(newFakeSourceStore(), nil)

	rec := sourceRequest(t, s, "POST", "/api/v1/workflows/wf/source/rev/discover", "", &pusher)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when this deployment runs no discovery", rec.Code)
	}
}

// TestDiscoverWithoutPushedSourceAnswers409 is the distinction that decides what a user does next.
// "No snapshot for this revision" is not "no such workflow": the remedy is to push one, and a 404 would
// send them looking for a typo instead.
func TestDiscoverWithoutPushedSourceAnswers409(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountSourcePush(newFakeSourceStore(), &fakeDiscovery{
		err: fmt.Errorf("wrapped: %w", sourceingest.ErrNoSource),
	})

	rec := sourceRequest(t, s, "POST", "/api/v1/workflows/wf/source/rev/discover", "", &pusher)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "push a snapshot") {
		t.Errorf("body = %s, want it to name the remedy", rec.Body.String())
	}
}

func TestDiscoverReturnsTheSummary(t *testing.T) {
	disc := &fakeDiscovery{summary: DiscoverySummary{
		WorkflowID: "wf", SourceRevision: "rev", Nodes: 4, Edges: 3, Labelled: 1, Unclassified: 2,
	}}
	s := New(nil, config.Config{})
	s.MountSourcePush(newFakeSourceStore(), disc)

	rec := sourceRequest(t, s, "POST", "/api/v1/workflows/wf/source/rev/discover", "", &pusher)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Labelled and unclassified are reported SEPARATELY. A single coverage percentage would print the
	// same number for "we classified nothing" and "there was nothing to classify".
	for _, want := range []string{`"labelled_regions":1`, `"unclassified_regions":2`, `"llm_calls":0`} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %s, want it to contain %s", body, want)
		}
	}
	if len(disc.calls) != 1 || disc.calls[0].TenantID != "t1" {
		t.Fatalf("discovery was called with %+v, want one call scoped to tenant t1", disc.calls)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
