package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/improvementrun"
)

// improvementServer mounts the P35 surface over a stub.
func improvementServer(t *testing.T, r ImprovementRunner) *Server {
	t.Helper()
	s := New(nil, config.Config{})
	s.MountImprovementRuns(r)
	return s
}

// testPrincipal is the authenticated session every non-refusal test runs under.
var testPrincipal = auth.Principal{TenantID: "tn-1", UserID: "person@example.com"}

// P35 tasks 3.6 — the improvement-run routes are addressed by FLAT paths, those paths are published
// `Exact` on every substrate, and deleting an ingress rule turns this file red.
//
// # Why this exists beside the derived fence in ingress_fence_test.go
//
// That fence derives its path set from `internal/runlink/transport`, which is the only code speaking to
// the platform from outside the cluster. These routes have no transport caller yet — the console's BFF
// reaches them in-cluster — so the derived fence cannot see them, and "cannot see it" is exactly how the
// P27 device routes came to be patched into a running cluster by hand.
//
// P30's finding is the precedent and the cost: the proposal generate route was mounted, had no button,
// and was not published. A button would have 404'd against production behind an entirely green build.
// This file is that lesson applied in the same change that mounts the handler rather than after a
// customer meets the 404.

// improvementPaths are the routes this file is about. Written once so a rename fails here loudly rather
// than silently narrowing every assertion below to routes that no longer exist.
var improvementPaths = []string{
	"/api/v1/improvement-plans",
	"/api/v1/improvement-runs",
	"/api/v1/improvement-decisions",
}

func TestImprovementRunRoutesAreRegisteredFlat(t *testing.T) {
	registered := registeredRoutes(t)
	for _, p := range improvementPaths {
		if !registered[p] {
			t.Errorf("%s is not registered. It is the shape that can be published Exact; the natural "+
				"shape (/api/v1/improvement-runs/{id}) can only be published by a Prefix rule, which "+
				"publishes every sibling anybody adds under that head.", p)
		}
	}
}

// 🔴 THE FENCE, and the one that must go red when an ingress rule is deleted.
//
// Nothing else in the tree notices a missing ingress rule: the deployment is healthy, the handler is
// registered, the build is green, and the only symptom is a 404 at the edge that reads to a customer as
// "wrong URL" and to us as "works on my machine".
func TestEveryImprovementRunRouteIsPublishedExact(t *testing.T) {
	declared := map[string]bool{}
	for _, r := range PublicRoutes() {
		declared[r] = true
	}
	ingress := ingressPaths(t)
	compose := setOf(composePlatformPaths(t))

	for _, p := range improvementPaths {
		if !declared[p] {
			t.Errorf("publicroutes.go does not declare %s public, so no substrate is required to "+
				"publish it and every assertion below is vacuous for it", p)
			continue
		}
		pathType, routed := ingress[p]
		switch {
		case !routed:
			t.Errorf("deploy/k8s/overlays/prod/ingress.yaml does not route %s to agentd.\n"+
				"  It answers 404 in production the moment that manifest is applied. Add:\n"+
				"          - path: %s\n            pathType: Exact\n"+
				"            backend: { service: { name: agentd, port: { number: 4321 } } }", p, p)
		case pathType != "Exact":
			t.Errorf("%s is published with pathType %q. Exact, never Prefix: a Prefix rule publishes "+
				"every route beneath it, which is the whole reason this path is flat.", p, pathType)
		}
		if !compose[p] {
			t.Errorf("deploy/scripts/bootstrap-vm.sh does not publish %s.\n"+
				"  The product ships on two substrates and a route published on one of them is a route "+
				"that 404s for every self-hosting customer.", p)
		}
	}
}

// ── the three controls that make publishing a paid route safe ────────────────────────────────────

type stubRunner struct {
	plan improvementrun.Plan
	run  improvementrun.Run
	err  error
}

func (s stubRunner) Plan(context.Context, string, string, improvementrun.RunOrigin) (improvementrun.Plan, error) {
	return s.plan, s.err
}
func (s stubRunner) Acknowledge(context.Context, improvementrun.Acknowledgement) error { return nil }
func (s stubRunner) Propose(context.Context, improvementrun.Plan) (improvementrun.Run, error) {
	return s.run, s.err
}
func (s stubRunner) Run(context.Context, string, string) (improvementrun.Run, bool, error) {
	return s.run, s.run.RunID != "", nil
}
func (s stubRunner) Decide(context.Context, string, string, string, improvementrun.DecisionKind, string) (
	improvementrun.Run, improvementrun.Decision, error) {
	return s.run, improvementrun.Decision{}, s.err
}

// TestTheImprovementRoutesRefuseAnUnauthenticatedRequest is the first of the three controls
// `publicroutes.go` names, and the one publishing a PAID route depends on most.
func TestTheImprovementRoutesRefuseAnUnauthenticatedRequest(t *testing.T) {
	s := improvementServer(t, stubRunner{})
	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/v1/improvement-plans", `{"question":"fix it"}`},
		{http.MethodPost, "/api/v1/improvement-runs", `{"plan_id":"p","question":"fix it"}`},
		{http.MethodGet, "/api/v1/improvement-runs?run_id=r", ""},
		{http.MethodPost, "/api/v1/improvement-decisions", `{"run_id":"r","proposal_id":"p","decision":"approve"}`},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		s.Mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s answered %d without a credential. This route spends the platform's own "+
				"provider money; the auth gate is the first of three controls that make publishing it safe",
				tc.method, tc.path, rec.Code)
		}
	}
}

// TestTheRequestBodyCannotWidenItsOwnRun is the second control: `DisallowUnknownFields` refuses the
// exact fields a future client would add — a budget, an axis list, an origin — each of which would let
// a caller widen its own bound or select the write-credential delivery path.
func TestTheRequestBodyCannotWidenItsOwnRun(t *testing.T) {
	s := improvementServer(t, stubRunner{})
	for _, body := range []string{
		`{"question":"fix it","budget_usd":1000}`,
		`{"question":"fix it","axes":["model"]}`,
		`{"question":"fix it","origin":"console"}`,
		`{"question":"fix it","tenant_id":"someone-else"}`,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/improvement-plans", strings.NewReader(body))
		s.Mux.ServeHTTP(rec, req.WithContext(auth.WithPrincipal(req.Context(), testPrincipal)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("the body %s was accepted (%d). Each of these fields would let a caller widen its "+
				"own bound or claim the console's write-credential delivery path", body, rec.Code)
		}
	}
}

// TestAnUnboundableQuestionRendersItsCauseAndNextAction is FR3 at the transport: a refusal is a
// condition a person can act on, not a 500 with a sentence.
func TestAnUnboundableQuestionRendersItsCauseAndNextAction(t *testing.T) {
	s := improvementServer(t, stubRunner{err: &improvementrun.Refusal{
		Cause: improvementrun.RefusedUnboundedRequested, Detail: "d", NextAction: "n",
	}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/improvement-plans", strings.NewReader(`{"question":"no limit"}`))
	s.Mux.ServeHTTP(rec, req.WithContext(auth.WithPrincipal(req.Context(), testPrincipal)))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unboundable question answered %d, want 422", rec.Code)
	}
	var view ImprovementRefusalView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Cause == "" || view.NextAction == "" {
		t.Fatalf("the refusal carries no cause or next action (%+v). The entire argument for refusing "+
			"rather than defaulting is that the person can act on the refusal", view)
	}
}

// TestOriginIsReadFromTheTransportNotTheBody is a security boundary, not a style preference: the origin
// selects the delivery mode (R3) and decides whether the run may deliver at all (D-35.3).
func TestOriginIsReadFromTheTransportNotTheBody(t *testing.T) {
	plain := httptest.NewRequest(http.MethodPost, "/api/v1/improvement-plans", nil)
	if got := originFor(plain); got != improvementrun.OriginCLI {
		t.Fatalf("a request with no console user agent got origin %q, want %q. The conservative "+
			"direction matters: mistaking a console request for a CLI one withholds delivery with a "+
			"stated reason; the other way reaches for a write credential", got, improvementrun.OriginCLI)
	}
	console := httptest.NewRequest(http.MethodPost, "/api/v1/improvement-plans", nil)
	console.Header.Set("User-Agent", "HEROS-Console/1.0")
	if got := originFor(console); got != improvementrun.OriginConsole {
		t.Fatalf("the console's own user agent got origin %q, want %q", got, improvementrun.OriginConsole)
	}
}

// TestAMovedPlanIsRefusedRatherThanRun is FR2's teeth at the transport: the plan somebody saw and the
// plan that runs must be the same plan.
func TestAMovedPlanIsRefusedRatherThanRun(t *testing.T) {
	s := improvementServer(t, stubRunner{plan: improvementrun.Plan{PlanID: "plan_current"}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/improvement-runs",
		strings.NewReader(`{"plan_id":"plan_the_person_saw","question":"fix it"}`))
	s.Mux.ServeHTTP(rec, req.WithContext(auth.WithPrincipal(req.Context(), testPrincipal)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a stale plan id ran anyway (%d). The person agreed to a scope and a budget; running "+
			"the current plan instead spends under bounds nobody was shown", rec.Code)
	}
}

// TestOnlyAPersonMayApprove is the control that makes publishing the decision route safe: approving a
// change authorizes a write to a customer's repository, and `internal/approval` refuses an empty actor
// for the reason this refusal exists one layer up — a row that records a decision and cannot say who
// made it is worse than no row, because it is believed.
func TestOnlyAPersonMayApprove(t *testing.T) {
	s := improvementServer(t, stubRunner{})
	machine := auth.Principal{TenantID: "tn-1"} // a machine credential: no UserID
	req := httptest.NewRequest(http.MethodPost, "/api/v1/improvement-decisions",
		strings.NewReader(`{"run_id":"r","proposal_id":"p","decision":"approve"}`))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req.WithContext(auth.WithPrincipal(req.Context(), machine)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a machine credential approved a change (%d). Publishing this route is safe only "+
			"because a machine cannot walk it to a pull request", rec.Code)
	}
}

// TestTheDecisionRouteHasNoPluralForm is design D4 at the wire. The pressure to add `proposal_ids`
// arrives in a future phase under delivery pressure, and a comment does not stop it.
func TestTheDecisionRouteHasNoPluralForm(t *testing.T) {
	rt := reflect.TypeOf(improvementDecisionRequest{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Type.Kind() == reflect.Slice {
			t.Fatalf("improvementDecisionRequest.%s is a %s. A bundle approval is one click that means "+
				"several things, and the person will read the first item and accept the rest", f.Name, f.Type)
		}
	}
	// … and the wire refuses one even so.
	s := improvementServer(t, stubRunner{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/improvement-decisions",
		strings.NewReader(`{"run_id":"r","proposal_ids":["a","b"],"decision":"approve"}`))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req.WithContext(auth.WithPrincipal(req.Context(), testPrincipal)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a plural payload was accepted (%d)", rec.Code)
	}
}

// TestAVoidApprovalAnswers409WithTheRunAttached — a bare error would leave the console rendering a
// stale card with an approve button that will fail again.
func TestAVoidApprovalAnswers409WithTheRunAttached(t *testing.T) {
	s := improvementServer(t, stubRunner{
		run: improvementrun.Run{RunID: "run_1"},
		err: improvementrun.ErrApprovalVoid,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/improvement-decisions",
		strings.NewReader(`{"run_id":"run_1","proposal_id":"p","decision":"approve"}`))
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req.WithContext(auth.WithPrincipal(req.Context(), testPrincipal)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("a void approval answered %d, want 409", rec.Code)
	}
	var view ImprovementDecisionView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Run.RunID != "run_1" {
		t.Fatal("the 409 does not carry the run, so the console cannot re-render the re-requested proposal")
	}
}
