package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// fakeAuthoring lets each test choose exactly one outcome, so a status-code assertion is about the
// mapping rather than about a chain of real subsystems.
type fakeAuthoring struct {
	result authoring.Result
	sub    authoring.Submission
	rev    authoring.Reversal
	err    error
	// gotDraft records what the handler passed down, so a test can prove the actor came from the
	// session and not from the request body.
	gotDraft authoring.Draft
}

func (f *fakeAuthoring) Preflight(_ context.Context, d authoring.Draft) (authoring.Result, error) {
	f.gotDraft = d
	return f.result, f.err
}
func (f *fakeAuthoring) Submit(_ context.Context, d authoring.Draft) (authoring.Submission, error) {
	f.gotDraft = d
	return f.sub, f.err
}
func (f *fakeAuthoring) Revert(context.Context, string, authoring.Actor) (authoring.Reversal, error) {
	return f.rev, f.err
}
func (f *fakeAuthoring) History(context.Context, string, string) ([]authoring.Entry, error) {
	return nil, f.err
}
func (f *fakeAuthoring) Parent(context.Context, string, string) (*variantspec.VariantSpec, error) {
	return &variantspec.VariantSpec{}, f.err
}

func authoringServer(t *testing.T, src AuthoringSource) *Server {
	t.Helper()
	s := &Server{Mux: http.NewServeMux()}
	if src != nil {
		s.MountAuthoring(src)
	}
	return s
}

func authoringRequest(t *testing.T, s *Server, method, path, body string, principal *auth.Principal) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if principal != nil {
		req = req.WithContext(auth.WithPrincipal(req.Context(), *principal))
	}
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	return rec
}

const draftBody = `{"workflow_id":"wf1","parent_variant_id":"p1","concurrency_token":"p1",
	"edits":{"n1":{"model_ref":"m-new"}}}`

var member = auth.Principal{TenantID: "t1", Role: "member", APIKeyID: "key-7"}

// TestFailureClassesDistinguishable is task 9.8 / NFR18: six outcomes, six messages.
//
// The failure this guards is the one that costs a user the most time — an error handler that maps
// everything it does not recognise onto one generic 500, so "your plan does not include this",
// "somebody else edited this", and "the database is down" all read as "something went wrong" and send
// the user to the wrong person.
func TestFailureClassesDistinguishable(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{"not entitled is a purchase, not an administrator", authoring.ErrNotEntitled, http.StatusPaymentRequired},
		{"not permitted is an administrator, not a purchase", authoring.ErrNotPermitted, http.StatusForbidden},
		{"a moved parent is a conflict to rebase, not a failure", authoring.ErrStaleDraft, http.StatusConflict},
		{"submitting past a refusal is unprocessable, not malformed", authoring.ErrNotAdmissible, http.StatusUnprocessableEntity},
		{"an unreachable record is upstream, not our logic", authoring.ErrRecordUnreachable, http.StatusBadGateway},
	}
	seen := map[int]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := authoringServer(t, &fakeAuthoring{err: tc.err})
			rec := authoringRequest(t, s, "POST", "/api/v1/authoring/submit", draftBody, &member)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			var body specError
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Error == "" {
				t.Error("the response carries no message — the class is distinguishable but the cause is lost")
			}
			if prev, dup := seen[tc.wantCode]; dup {
				t.Errorf("status %d is shared with %q — the classes collapsed", tc.wantCode, prev)
			}
			seen[tc.wantCode] = tc.name
		})
	}

	t.Run("not mounted is 503, not 404", func(t *testing.T) {
		// A missing subsystem and a missing resource are different facts. Mapping the first onto 404
		// tells an operator their data is gone when the truth is the component was never wired.
		s := authoringServer(t, nil)
		s.Mux.HandleFunc("POST /api/v1/authoring/submit", func(w http.ResponseWriter, r *http.Request) {
			s.handleAuthoringSubmit(w, r)
		})
		rec := authoringRequest(t, s, "POST", "/api/v1/authoring/submit", draftBody, &member)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", rec.Code)
		}
	})

	t.Run("no principal is 401", func(t *testing.T) {
		s := authoringServer(t, &fakeAuthoring{})
		rec := authoringRequest(t, s, "POST", "/api/v1/authoring/submit", draftBody, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})
}

// TestRefusalIsAVerdictNotAnError: a refused preflight is a 200 carrying the named cause.
//
// Returning 4xx here would be the single most damaging mistake on this surface: every client's generic
// error path would swallow the node, the field, and the reason — the three things preflight exists to
// deliver — into "request failed".
func TestRefusalIsAVerdictNotAnError(t *testing.T) {
	src := &fakeAuthoring{result: authoring.Result{
		Verdict: authoring.VerdictRefused,
		Refusal: authoring.Refusal{Cause: "inline node cannot carry a temperature override",
			NodeID: "n1", Field: "provider_params"},
	}}
	s := authoringServer(t, src)
	rec := authoringRequest(t, s, "POST", "/api/v1/authoring/preflight", draftBody, &member)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a refusal is a verdict the user asked for", rec.Code)
	}
	var view AuthoringPreflightView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Verdict != string(authoring.VerdictRefused) {
		t.Errorf("verdict = %q, want refused", view.Verdict)
	}
	if view.NodeID != "n1" || view.Field != "provider_params" || view.Cause == "" {
		t.Errorf("the refusal lost its names: %+v", view)
	}
}

// TestPreflightThreeVerdictsSurviveTheWire: the third verdict must reach the client as its own value,
// not as a refusal with an odd message. A surface that receives two values can only render two states.
func TestPreflightThreeVerdictsSurviveTheWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  authoring.Result
		want string
	}{
		{"admissible", authoring.Result{Verdict: authoring.VerdictAdmissible, ConfigHash: "abc"}, "admissible"},
		{"refused", authoring.Result{Verdict: authoring.VerdictRefused,
			Refusal: authoring.Refusal{Cause: "no", NodeID: "n1"}}, "refused"},
		{"not_yet_measurable", authoring.Result{Verdict: authoring.VerdictNotYetMeasurable,
			Missing: authoring.MissingInput{Kind: "context_drop_ratio", Subject: "summarization"}}, "not_yet_measurable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := authoringServer(t, &fakeAuthoring{result: tc.res})
			rec := authoringRequest(t, s, "POST", "/api/v1/authoring/preflight", draftBody, &member)
			var view AuthoringPreflightView
			if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if view.Verdict != tc.want {
				t.Fatalf("verdict = %q, want %q", view.Verdict, tc.want)
			}
			if tc.want == "not_yet_measurable" && view.MissingKind == "" {
				t.Error("the third verdict arrived without naming the missing measurement — a dead end")
			}
		})
	}
}

// TestAuthoringActorComesFromTheSession: a client cannot author as somebody else by saying so.
func TestAuthoringActorComesFromTheSession(t *testing.T) {
	src := &fakeAuthoring{result: authoring.Result{Verdict: authoring.VerdictAdmissible}}
	s := authoringServer(t, src)
	// The body tries to claim another tenant and another actor. Both must be ignored.
	body := `{"workflow_id":"wf1","parent_variant_id":"p1","tenant_id":"other-tenant",
		"actor":{"id":"someone-else","tenant_id":"other-tenant"},"edits":{"n1":{"model_ref":"m"}}}`
	authoringRequest(t, s, "POST", "/api/v1/authoring/preflight", body, &member)

	if src.gotDraft.Actor.TenantID != "t1" {
		t.Errorf("tenant = %q, want t1 — request scope must never come from the body", src.gotDraft.Actor.TenantID)
	}
	if src.gotDraft.Actor.ID != "key-7" {
		t.Errorf("actor = %q, want the session's identity", src.gotDraft.Actor.ID)
	}
}

// TestSubmitViewCarriesUnverified: the label travels with the change over the wire, always.
func TestSubmitViewCarriesUnverified(t *testing.T) {
	src := &fakeAuthoring{sub: authoring.Submission{
		ChangeID: "ac_1", ConfigHash: "hash1",
		Entry: authoring.Entry{VerificationState: authoring.StateUnverified,
			Origin: string(authoring.OriginUser), Axis: "model", DiffRef: "d1", ActorID: "key-7"},
	}}
	s := authoringServer(t, src)
	rec := authoringRequest(t, s, "POST", "/api/v1/authoring/submit", draftBody, &member)

	var view AuthoringChangeView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.VerificationState != string(authoring.StateUnverified) {
		t.Errorf("verification_state = %q, want unverified", view.VerificationState)
	}
	// And it must be a required field on the wire, not an omitempty that vanishes and lets a client
	// default it to something friendlier.
	if !strings.Contains(rec.Body.String(), `"verification_state"`) {
		t.Error("verification_state is absent from the JSON — a change without it reads as verified")
	}
}

// TestMalformedDraftIsBadRequestNotServerError keeps "you sent nonsense" separate from "we broke".
func TestMalformedDraftIsBadRequestNotServerError(t *testing.T) {
	s := authoringServer(t, &fakeAuthoring{})
	for _, body := range []string{`{`, `{"workflow_id":""}`, `{"parent_variant_id":"p1"}`} {
		rec := authoringRequest(t, s, "POST", "/api/v1/authoring/preflight", body, &member)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q → status %d, want 400", body, rec.Code)
		}
	}
}

// TestRevertNotFoundIsNotAServerError: undoing something that is not there is a 404 with a remedy.
func TestRevertNotFoundIsNotAServerError(t *testing.T) {
	s := authoringServer(t, &fakeAuthoring{err: authoring.ErrNothingToRevert})
	rec := authoringRequest(t, s, "POST", "/api/v1/authoring/revert", `{"change_id":"ac_missing"}`, &member)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if errors.Is(authoring.ErrNothingToRevert, authoring.ErrStaleDraft) {
		t.Error("not-found and conflict collapsed into one error")
	}
}

// TestAuthoringPayloadAllowlisted (P13 13c task 12.2, FR32).
//
// Authoring must widen no egress. The failure this guards is specific and tempting: preflight is the
// natural place for an implementation to post the draft — or worse, the resolved prompt — somewhere to
// be checked, and it would work perfectly while quietly shipping customer content across the boundary.
//
// The assertion is over what the wire SHAPES can hold, not over one recorded request. A test that
// inspected a single payload would prove only that this payload was clean.
func TestAuthoringPayloadAllowlisted(t *testing.T) {
	t.Run("every wire field is an allowlisted identifier, reference or verdict", func(t *testing.T) {
		// 🔴 An earlier version of this check banned SUBSTRINGS ("token", "body", "env", …) and flagged
		// `ConcurrencyToken` — an opaque marker, not a credential. A fence that cries wolf is a fence
		// somebody switches off. So this is an allowlist per type: adding a field fails here and forces a
		// human to answer "is this a reference or a payload?", which is the question actually worth asking.
		allowed := map[string]map[string]bool{
			"authoringDraftRequest": {
				"WorkflowID": true, "ParentVariantID": true, "ConcurrencyToken": true,
				"ForkedFromProposal": true, "Edits": true,
			},
			"AuthoringPreflightView": {
				"Verdict": true, "Cause": true, "NodeID": true, "Field": true, "Shape": true,
				"MissingKind": true, "MissingSubject": true, "ConfigHash": true,
				"Dimensions": true, "Nodes": true, "Adapters": true,
			},
			"AuthoringChangeView": {
				"ChangeID": true, "ConfigHash": true, "VerificationState": true, "Origin": true,
				"Axis": true, "DiffRef": true, "ForkedFrom": true, "ActorID": true,
			},
		}
		for _, shape := range []any{authoringDraftRequest{}, AuthoringPreflightView{}, AuthoringChangeView{}} {
			rt := reflect.TypeOf(shape)
			ok := allowed[rt.Name()]
			if ok == nil {
				t.Fatalf("no allowlist for %s", rt.Name())
			}
			for i := 0; i < rt.NumField(); i++ {
				if !ok[rt.Field(i).Name] {
					t.Errorf("%s gained field %q — authoring carries identifiers, references and verdicts. "+
						"If this is a reference, allowlist it; if it is a prompt body, a source excerpt, a "+
						"diff, an environment value or a credential, it must not cross the boundary.",
						rt.Name(), rt.Field(i).Name)
				}
			}
		}
		// DiffRef is a REFERENCE to a content-addressed transform, never the diff itself. Pinned by name
		// because "DiffRef" and "Diff" differ by three characters and by everything that matters.
		if _, hasDiff := reflect.TypeOf(AuthoringChangeView{}).FieldByName("Diff"); hasDiff {
			t.Error("AuthoringChangeView carries the diff itself rather than a reference to it")
		}
	})

	t.Run("a refusal's verbatim cause is the only free text, and it is the engine's own", func(t *testing.T) {
		// The cause IS rendered verbatim — that is deliberate and is the one string a user must see. What
		// matters is that it is the platform's sentence travelling outward to the user, not customer
		// content travelling inward to us. This pins that the console never SENDS free text.
		rt := reflect.TypeOf(authoringDraftRequest{})
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			switch f.Name {
			case "WorkflowID", "ParentVariantID", "ConcurrencyToken", "ForkedFromProposal", "Edits":
				// identifiers and a map of refs — fine
			default:
				t.Errorf("the draft request gained %q; a request carries identifiers and references only", f.Name)
			}
		}
	})

	t.Run("the actor is never accepted from the request", func(t *testing.T) {
		// Request scope comes from the session. A tenant field here would let a client author as another.
		rt := reflect.TypeOf(authoringDraftRequest{})
		for i := 0; i < rt.NumField(); i++ {
			name := strings.ToLower(rt.Field(i).Name)
			if strings.Contains(name, "tenant") || strings.Contains(name, "actor") {
				t.Errorf("the draft request accepts %q from the body — scope must come from the session",
					rt.Field(i).Name)
			}
		}
	})
}
