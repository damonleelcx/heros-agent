package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/legal"
)

// p23_test.go drives the two consent endpoints through the MUX.
//
// # 🔴 Why this file exists, and what its absence had meant
//
// It was written after noticing that `RegisterP23` had **no caller anywhere** and no test — so
// `handleP23Record` and `handleP23Read` had never been executed. The domain underneath was thoroughly
// covered, including against real Postgres, and the structural tests in the console read this file's
// SOURCE. None of that runs a request.
//
// That is exactly the failure this phase exists to name: a green suite over code nobody ran. Everything
// below goes through `s.Handler`, so the routing, the principal derivation, the status mapping and the
// JSON shape are all exercised rather than inferred.

// consentStub stands in for the legal service. The api package takes an interface, so the test does too.
type consentStub struct {
	recordErr error
	readErr   error
	stored    legal.Acceptance
	created   bool
	history   legal.History
	// seen records what the handler passed down, so the test can assert the tenant and the principal
	// came from the SESSION rather than from the request body.
	seenTenant, seenPrincipal string
	seenRequest               legal.Request
}

func (c *consentStub) Record(_ context.Context, tenantID, principalID string, req legal.Request) (legal.Acceptance, bool, error) {
	c.seenTenant, c.seenPrincipal, c.seenRequest = tenantID, principalID, req
	if c.recordErr != nil {
		return legal.Acceptance{}, false, c.recordErr
	}
	return c.stored, c.created, nil
}

func (c *consentStub) Read(_ context.Context, tenantID, principalID string) (legal.History, error) {
	c.seenTenant, c.seenPrincipal = tenantID, principalID
	return c.history, c.readErr
}

func consentServer(t *testing.T, src P23Source) *Server {
	t.Helper()
	s := New(nil, config.Config{})
	s.RegisterP23(src)
	return s
}

// as sends a request carrying an authenticated principal, the way the auth middleware would.
func as(s *Server, principal auth.Principal, method, target, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req = req.WithContext(auth.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	return rec
}

func acceptance() legal.Acceptance {
	return legal.Acceptance{
		ID: "id-1", TenantID: "cus_acme", PrincipalID: "cus_acme",
		DocumentKind: legal.KindTerms, DocumentVersion: "1.0.0",
		ContentHash: strings.Repeat("a", 64),
		AcceptedAt:  time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		Method:      legal.MethodSignIn,
	}
}

// ── The write path ────────────────────────────────────────────────────────────

func TestP23Record_A201IsWrittenOnlyAfterTheRowExists(t *testing.T) {
	src := &consentStub{stored: acceptance(), created: true}
	s := consentServer(t, src)

	rec := as(s, auth.Principal{TenantID: "cus_acme"}, http.MethodPost, "/v1/legal/acceptances",
		`{"document_kind":"terms","document_version":"1.0.0","content_hash":"`+strings.Repeat("a", 64)+`","method":"signin"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var got AcceptanceResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Recorded || !got.Created {
		t.Errorf("a committed row was not reported as recorded: %+v", got)
	}
	// The archived route is what the account surface links to — the exact text that was accepted.
	if got.ArchivedRoute != "/legal/terms/v/1.0.0" {
		t.Errorf("archived route %q, want the permanent version route", got.ArchivedRoute)
	}
}

func TestP23Record_ARepeatIs200AndSaysItCreatedNothing(t *testing.T) {
	// A double-clicked button. Both are successes; only one made a row, and the response says which —
	// so a client cannot report two decisions where a customer made one.
	src := &consentStub{stored: acceptance(), created: false}
	s := consentServer(t, src)

	rec := as(s, auth.Principal{TenantID: "cus_acme"}, http.MethodPost, "/v1/legal/acceptances",
		`{"document_kind":"terms","document_version":"1.0.0","content_hash":"`+strings.Repeat("a", 64)+`","method":"signin"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 for a repeat: %s", rec.Code, rec.Body.String())
	}
	var got AcceptanceResult
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if !got.Recorded || got.Created {
		t.Errorf("a repeat reported created=%v recorded=%v", got.Created, got.Recorded)
	}
}

func TestP23Record_TheTenantAndPrincipalComeFromTheSessionNotTheBody(t *testing.T) {
	// 🔴 A tenant id a caller can type is a tenant id a caller can change. The body below tries to name
	// another tenant; the handler must refuse the field outright AND, when it is absent, must use the
	// session's.
	src := &consentStub{stored: acceptance(), created: true}
	s := consentServer(t, src)

	// An unknown field is refused rather than ignored.
	rec := as(s, auth.Principal{TenantID: "cus_acme"}, http.MethodPost, "/v1/legal/acceptances",
		`{"document_kind":"terms","document_version":"1.0.0","content_hash":"`+strings.Repeat("a", 64)+`","method":"signin","tenant_id":"cus_victim"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a body carrying tenant_id was accepted (status %d): %s", rec.Code, rec.Body.String())
	}

	// And a well-formed request uses the session's tenant.
	src2 := &consentStub{stored: acceptance(), created: true}
	s2 := consentServer(t, src2)
	as(s2, auth.Principal{TenantID: "cus_acme", APIKeyID: "key-7"}, http.MethodPost, "/v1/legal/acceptances",
		`{"document_kind":"terms","document_version":"1.0.0","content_hash":"`+strings.Repeat("a", 64)+`","method":"signin"}`)
	if src2.seenTenant != "cus_acme" {
		t.Errorf("the service received tenant %q, want the session's", src2.seenTenant)
	}
	// ADR-008: the principal is the seam's, not an email and not a tenant when a principal exists.
	if src2.seenPrincipal != "key-7" {
		t.Errorf("the service received principal %q, want the session's principal", src2.seenPrincipal)
	}
}

func TestP23Record_NoSessionIsRefused(t *testing.T) {
	s := consentServer(t, &consentStub{})
	req := httptest.NewRequest(http.MethodPost, "/v1/legal/acceptances", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req) // deliberately no principal in the context
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("an unauthenticated write got %d, want 401", rec.Code)
	}
}

// TestP23Record_EachRefusalGetsItsOwnStatus is the mapping a client branches on.
func TestP23Record_EachRefusalGetsItsOwnStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		reason string
	}{
		// 🔴 A hash mismatch is 409, not 400: the request was well-formed and the CONFLICT is with what
		// the server publishes. That is what a stale tab produces, and the remedy is "reload the
		// document", not "fix your JSON".
		{"a hash the server never published", legal.ErrHashMismatch, http.StatusConflict, "content_hash_mismatch"},
		{"an unreadable manifest", legal.ErrManifestUnavailable, http.StatusServiceUnavailable, "manifest_unavailable"},
		{"an unknown version", legal.ErrUnknownVersion, http.StatusBadRequest, "unknown_version"},
		{"an unknown kind", legal.ErrUnknownKind, http.StatusBadRequest, "unknown_kind"},
		{"a store failure", errors.New("connection reset"), http.StatusInternalServerError, "write_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := consentServer(t, &consentStub{recordErr: tc.err})
			rec := as(s, auth.Principal{TenantID: "cus_acme"}, http.MethodPost, "/v1/legal/acceptances",
				`{"document_kind":"terms","document_version":"1.0.0","content_hash":"`+strings.Repeat("a", 64)+`","method":"signin"}`)

			if rec.Code != tc.status {
				t.Errorf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			var body map[string]string
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body["reason"] != tc.reason {
				t.Errorf("reason %q, want %q", body["reason"], tc.reason)
			}
			// 🔴 The sentence a customer sees. It says what did NOT happen — never a checkmark over an
			// unrecorded acceptance.
			if !strings.Contains(body["error"], "nothing has been agreed") {
				t.Errorf("a failed write did not say nothing has been agreed: %q", body["error"])
			}
			// And nothing in the response may read as success.
			if strings.Contains(rec.Body.String(), `"recorded":true`) {
				t.Error("a failed write reported recorded=true")
			}
		})
	}
}

// ── The read path ─────────────────────────────────────────────────────────────

func TestP23Read_ReturnsTheCallersOwnHistoryAndWhatIsPending(t *testing.T) {
	src := &consentStub{history: legal.History{
		Accepted: []legal.Acceptance{acceptance()},
		Pending: []legal.Version{{
			Version: "1.0.0", EffectiveDate: "2026-07-31", Hash: strings.Repeat("c", 64),
			Route: "/legal/privacy/v/1.0.0", Material: true,
		}},
	}}
	s := consentServer(t, src)

	rec := as(s, auth.Principal{TenantID: "cus_acme"}, http.MethodGet, "/v1/legal/acceptances", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got LegalAcceptanceView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Accepted) != 1 || got.Accepted[0].ArchivedRoute != "/legal/terms/v/1.0.0" {
		t.Fatalf("history did not render with its archived route: %+v", got.Accepted)
	}
	if len(got.Pending) != 1 || got.Pending[0].DocumentKind != "privacy" {
		t.Fatalf("pending did not render, or lost its kind: %+v", got.Pending)
	}
	if got.PendingUnknown {
		t.Error("pending_unknown was set on a successful read")
	}
	if src.seenTenant != "cus_acme" {
		t.Errorf("the service was asked for tenant %q", src.seenTenant)
	}
}

func TestP23Read_AManifestOutageIsFailOpenAndSaysPendingIsUnknown(t *testing.T) {
	// 🔴 Reading is fail-OPEN: the customer's own history is real and is returned. But an empty
	// `pending` must not read as "nothing is outstanding" — that would silently clear the gate.
	src := &consentStub{
		history: legal.History{Accepted: []legal.Acceptance{acceptance()}},
		readErr: legal.ErrManifestUnavailable,
	}
	s := consentServer(t, src)

	rec := as(s, auth.Principal{TenantID: "cus_acme"}, http.MethodGet, "/v1/legal/acceptances", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("a manifest outage hid the history (status %d)", rec.Code)
	}
	var got LegalAcceptanceView
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Accepted) != 1 {
		t.Error("the history was withheld during a manifest outage")
	}
	if !got.PendingUnknown {
		t.Error("pending_unknown was not set — an empty pending list would read as 'you owe nothing'")
	}
}

func TestP23Read_AStoreFailureIsNotFailOpen(t *testing.T) {
	// A manifest outage is fail-open; a STORE failure is not. Returning an empty history as though it
	// were the customer's real one would be the read-path version of an optimistic checkmark.
	s := consentServer(t, &consentStub{readErr: errors.New("connection reset")})
	rec := as(s, auth.Principal{TenantID: "cus_acme"}, http.MethodGet, "/v1/legal/acceptances", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502 — an empty history must not be served as a real one", rec.Code)
	}
}

// ── The surface is exactly two endpoints ─────────────────────────────────────

func TestP23_AnUnmountedDeploymentSaysSoRatherThanPanicking(t *testing.T) {
	// A deployment that never called RegisterP23 has no consent surface. It must answer, not crash — and
	// it must say WHICH thing is absent, because "503" alone sends an operator to the wrong system.
	s := New(nil, config.Config{})
	s.Mux.HandleFunc("POST /v1/legal/acceptances", s.handleP23Record)
	s.Mux.HandleFunc("GET /v1/legal/acceptances", s.handleP23Read)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := as(s, auth.Principal{TenantID: "cus_acme"}, method, "/v1/legal/acceptances", "{}")
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s on an unmounted surface got %d, want 503", method, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "not_mounted") {
			t.Errorf("%s did not name the reason: %s", method, rec.Body.String())
		}
	}
}
