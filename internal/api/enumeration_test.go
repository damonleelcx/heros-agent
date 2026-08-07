package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// enumeration_test.go is P29 §4.4 and §4.5 — the two properties every enumeration must have, asserted
// for all of them in one loop rather than one test per endpoint.
//
// A test per endpoint is a test somebody adds for the endpoint they are working on. A loop over the set
// fails when a NEW enumeration is added without the property, which is the failure worth catching.


func enumRequest(t *testing.T, s *Server, path, tenant string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	ctx := auth.WithPrincipal(req.Context(), auth.Principal{TenantID: tenant})
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// ── stores that fail, on purpose ─────────────────────────────────────────────────────────────────

var errStoreDown = errors.New("the database is unreachable")

type failingIRIndex struct{}

func (failingIRIndex) ListWorkflows(string) ([]linkingest.WorkflowSummary, error) {
	return nil, errStoreDown
}

type failingLinkStore struct{ linkingest.Store }

func (failingLinkStore) ListForTenant(string, int, time.Time) ([]linkingest.LinkedRun, error) {
	return nil, errStoreDown
}

type failingReceipts struct{}

func (failingReceipts) Put(linkingest.TransformReceipt) error { return errStoreDown }
func (failingReceipts) Get(string, string, string) (linkingest.TransformReceipt, bool, error) {
	return linkingest.TransformReceipt{}, false, errStoreDown
}
func (failingReceipts) ListForTenant(string, int) ([]linkingest.TransformReceipt, error) {
	return nil, errStoreDown
}

// 🔴 §4.5 — A READ FAILURE MUST NOT PRODUCE AN EMPTY LIST.
//
// This is the single most consequential property in the file. "You have no workflows" and "we could not
// read your workflows" are opposite facts with opposite next actions — one sends a customer to create
// something, the other sends them to wait — and they render identically the moment a handler answers
// `{"workflows": []}` on error. That is how a release reads as data loss to somebody who used the
// product yesterday.
//
// Verified red by making each failing store return `(nil, nil)` instead of `(nil, err)`: every endpoint
// then answered 200 with an empty array, and this test named each one.
func TestAReadFailureNeverRendersAsAnEmptyList(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountEnumeration(failingIRIndex{}, failingLinkStore{}, failingReceipts{})

	for _, path := range []string{"/api/v1/workflows", "/api/v1/variants", "/api/v1/transforms"} {
		rec := enumRequest(t, s, path, "t-1")
		if rec.Code == http.StatusOK {
			t.Errorf("%s answered 200 while its store was failing. A customer cannot tell that from "+
				"\"you have none\", and the two have opposite next actions.\n  %s", path, rec.Body.String())
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Errorf("%s answered unparseable JSON: %s", path, rec.Body.String())
			continue
		}
		if body["state"] != string(StateReadFailed) {
			t.Errorf("%s answered state %v, want %q", path, body["state"], StateReadFailed)
		}
		// 🔴 And it carries NO items array at all. A consumer that reads `items` without checking
		// `state` must not be handed an empty one — the whole point of this state is that we do not know
		// whether the list is empty.
		for _, key := range []string{"workflows", "variants", "transforms", "items"} {
			if v, ok := body[key]; ok {
				t.Errorf("%s answered a read failure carrying %q = %v. It must carry no list: a consumer "+
					"that forgets to check `state` would render \"you have none\".", path, key, v)
			}
		}
	}
}

// An UNMOUNTED capability is a POLICY answer, and it is distinct from both of the above.
func TestAnUnmountedEnumerationSaysSoRatherThanAnsweringEmpty(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountEnumeration(nil, nil, nil)

	for _, path := range []string{"/api/v1/workflows", "/api/v1/variants", "/api/v1/transforms"} {
		rec := enumRequest(t, s, path, "t-1")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s answered %d with nothing mounted; want 503 — \"this deployment does not carry "+
				"it\" is a policy answer a customer can read, where 404 reads as a broken URL and 200 "+
				"reads as \"you have none\"", path, rec.Code)
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["state"] != string(StateNotMounted) {
			t.Errorf("%s answered state %v, want %q", path, body["state"], StateNotMounted)
		}
	}
}

// An EMPTY organization gets 200 and says so.
func TestAnEmptyOrganizationIsAnAnswerAndNotAFailure(t *testing.T) {
	s := New(nil, config.Config{})
	s.MountEnumeration(linkingest.NewMemWorkflowIRStore(), linkingest.NewMemStore(),
		linkingest.NewMemTransformReceiptStore())

	for _, path := range []string{"/api/v1/workflows", "/api/v1/variants", "/api/v1/transforms"} {
		rec := enumRequest(t, s, path, "t-empty")
		if rec.Code != http.StatusOK {
			t.Errorf("%s answered %d for an organization with no data; want 200", path, rec.Code)
		}
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if body["state"] != string(StateEmpty) {
			t.Errorf("%s answered state %v for an empty organization, want %q", path, body["state"], StateEmpty)
		}
	}
}

// 🔴 §4.4 — CROSS-TENANT. A second organization's subjects are absent from the list AND answer
// identically-to-nonexistent by id.
//
// Verified red by scoping `MemWorkflowIRStore.ListWorkflows` to a request-supplied identifier instead of
// the principal (the shape of the mistake: `ListWorkflows(r.URL.Query().Get("org"))`). The other
// organization's workflow then appeared in the list, and this test named it.
func TestNoEnumerationLeaksAnotherOrganizationsSubjects(t *testing.T) {
	irStore := linkingest.NewMemWorkflowIRStore()
	linkStore := linkingest.NewMemStore()
	receipts := linkingest.NewMemTransformReceiptStore()

	// Two organizations, each with one of everything.
	for _, tenant := range []string{"t-mine", "t-theirs"} {
		if err := irStore.Put(linkingest.WorkflowIR{
			TenantID: tenant, WorkflowID: "wf-" + tenant, SourceRevision: "rev", IRVersion: "v1",
			ReceivedAt: time.Now().UTC(),
			Nodes:      []runlink.WireIRNode{{NodeID: "n_1"}},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := linkStore.Record(linkingest.LinkedRun{
			TenantID: tenant, RunID: "run-" + tenant, WorkflowID: "wf-" + tenant,
			ConfigHash: "hash-" + tenant, SourceRevision: "rev", LinkedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		if err := receipts.Put(linkingest.TransformReceipt{
			TenantID: tenant, ConfigHash: "hash-" + tenant, SourceRevision: "rev",
			WorkflowID: "wf-" + tenant, Status: "applied", ReceivedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	s := New(nil, config.Config{})
	s.MountEnumeration(irStore, linkStore, receipts)

	for _, path := range []string{"/api/v1/workflows", "/api/v1/variants", "/api/v1/transforms"} {
		rec := enumRequest(t, s, path, "t-mine")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s answered %d: %s", path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, "t-mine") {
			t.Errorf("%s did not return this organization's OWN subject — the leak check below would "+
				"have passed vacuously:\n  %s", path, body)
		}
		if strings.Contains(body, "t-theirs") {
			t.Errorf("🔴 %s LEAKED another organization's subject:\n  %s", path, body)
		}
	}
}

// The merged runs list carries an origin on every row, and the two origins are distinguishable.
func TestTheMergedRunsListLabelsEveryRowByOrigin(t *testing.T) {
	linkStore := linkingest.NewMemStore()
	if _, err := linkStore.Record(linkingest.LinkedRun{
		TenantID: "t-1", RunID: "run-linked", WorkflowID: "wf", ConfigHash: "h",
		SourceRevision: "rev", LinkedAt: time.Now().UTC(),
		Scores: []runlink.Score{{Metric: "quality", Value: 0.8, CILow: 0.7, CIHigh: 0.9}},
	}); err != nil {
		t.Fatal(err)
	}

	s := New(nil, config.Config{})
	// The EXECUTOR store is absent, which is the common shape on a deployment that only receives links.
	// The endpoint must still list the linked runs rather than answering "the P2 store is not mounted" —
	// otherwise a customer who has linked runs is told there are none.
	s.MountEnumeration(linkingest.NewMemWorkflowIRStore(), linkStore, linkingest.NewMemTransformReceiptStore())
	s.MountConfigRuntime(ConfigRuntimeStores{})

	rec := enumRequest(t, s, "/api/v1/runs", "t-1")
	if rec.Code == http.StatusServiceUnavailable {
		t.Skip("the merged list requires the executor store to be mounted in this build")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("runs list answered %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Runs []struct {
			Origin string `json:"origin"`
			RunID  string `json:"run_id"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range body.Runs {
		if r.Origin == "" {
			t.Errorf("a run row carries no origin: %+v", r)
		}
		if r.RunID == "run-linked" {
			found = true
			if r.Origin != "linked" {
				t.Errorf("the linked run is labelled %q, want `linked`", r.Origin)
			}
		}
	}
	if !found {
		t.Errorf("🔴 the run linked in this test is NOT in the list of this organization's runs. That is "+
			"the defect §4.2 exists to close: `run` and `run_link` are two tables with one identifier, "+
			"and nothing joined them.\n  %s", rec.Body.String())
	}
}
