package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/evalharness"
)

// assessments_test.go covers the P33 surface: what it refuses, what it renders, and the two things it
// must never let the browser decide.

// ── A stub runner ────────────────────────────────────────────────────────────────────────────────

type stubAssessments struct {
	report assessment.Assessment
	found  bool
	runErr error
	runs   int
	diff   []assessment.AxisDiff
}

func (s *stubAssessments) Run(_ context.Context, _, _ string) (assessment.Assessment, error) {
	s.runs++
	if s.runErr != nil {
		return assessment.Assessment{}, s.runErr
	}
	return s.report, nil
}

func (s *stubAssessments) Latest(_ context.Context, _, _ string) (assessment.Assessment, bool, error) {
	return s.report, s.found, nil
}

func (s *stubAssessments) Reinfer(_ context.Context, _, _ string) (assessment.Assessment, []assessment.AxisDiff, error) {
	s.runs++
	if s.runErr != nil {
		return assessment.Assessment{}, nil, s.runErr
	}
	return s.report, s.diff, nil
}

// nineFindings builds a complete report exercising every legal cell, so the view test is a worked
// example rather than a happy path.
func nineFindings(t *testing.T) assessment.Assessment {
	t.Helper()
	graph := assessment.EvidenceRef{Surface: assessment.SurfaceGraph, Locator: "wf-1"}
	board := assessment.EvidenceRef{Surface: assessment.SurfaceBoard, Locator: "wf-1", Fragment: "set-1"}
	mk := func(f assessment.Finding, err error) assessment.Finding {
		t.Helper()
		if err != nil {
			t.Fatalf("building a finding: %v", err)
		}
		return f
	}
	report := assessment.EvalSetReport{
		EvalSetHash: "set-1",
		Score:       assessment.Interval{Mean: 0.94, Low: 0.90, High: 0.98, NSeeds: 5},
		NCases:      2, CoverageMeasured: true, OracleCoverage: 0, NIndecisive: 2,
		Cases: []assessment.CaseView{
			{CaseID: "c1", Oracle: oracleCannotFail()},
			{CaseID: "c2", Oracle: oracleCannotFail()},
		},
	}
	return assessment.Assessment{
		AssessmentID: "as-1", TenantID: "tn-1", WorkflowID: "wf-1",
		SourceRevision: "rev-1", AgentConfigHash: "0123456789abcdef0123",
		StartedAtMS: 1000, CompletedAtMS: 2000, SpendUSD: 0.2, SpendCapUSD: 1,
		Findings: []assessment.Finding{
			mk(assessment.NotMeasured(assessment.AxisModel, assessment.MissingUnresolvedField, "a", graph)),
			mk(assessment.Measured(assessment.AxisPrompt, "scores 0.94", board, report)),
			mk(assessment.Observed(assessment.AxisSkills, "no skills bound", graph)),
			mk(assessment.Observed(assessment.AxisContext, "system + last three turns", graph)),
			mk(assessment.NotMeasured(assessment.AxisTools, assessment.MissingUnresolvedField, "b", graph)),
			mk(assessment.Inferred(assessment.AxisMemory, "a per-session store", graph, "claude-opus-5", "sha256:1")),
			mk(assessment.NotMeasured(assessment.AxisHarness, assessment.MissingBudgetExhausted, "c", graph)),
			mk(assessment.Refused(assessment.AxisLoop, assessment.RefusalAnalysis, "P34", graph)),
			mk(assessment.Refused(assessment.AxisGraph, assessment.RefusalFrontend, "python emits no edges", graph)),
		},
	}
}

func assessmentServer(t *testing.T, src AssessmentRunner) *Server {
	t.Helper()
	s := New(nil, config.Config{})
	s.MountAssessments(src)
	return s
}

// ── Refusals ─────────────────────────────────────────────────────────────────────────────────────

// TestAnUnmountedAssessmentSurfaceAnswers503 is P9's distinction, which this phase needs more than
// most: 404 reads to a customer as "no such workflow" and would render over a workflow that plainly
// exists.
func TestAnUnmountedAssessmentSurfaceAnswers503(t *testing.T) {
	s := assessmentServer(t, nil)
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/assessments?workflow_id=wf-1", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unmounted surface answered %d, want 503", rec.Code)
	}
}

func TestAssessingRequiresAnAuthenticatedTenant(t *testing.T) {
	s := assessmentServer(t, &stubAssessments{})
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/assessments"},
		{http.MethodGet, "/api/v1/assessments?workflow_id=wf-1"},
	} {
		rec := httptest.NewRecorder()
		s.Mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"workflow_id":"wf-1"}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s answered %d with no principal, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// TestTheRunPayloadRefusesAnUnratifiedKey is `careful-api-creation` at the wire, and the key it is
// actually guarding against is named in the handler: `axes: [...]`. A request to assess a SUBSET,
// accepted silently, produces a report with fewer than nine findings — which FR1 exists to forbid.
func TestTheRunPayloadRefusesAnUnratifiedKey(t *testing.T) {
	src := &stubAssessments{report: nineFindings(t)}
	s := assessmentServer(t, src)
	req := asPrincipal(t, http.MethodPost, "/api/v1/assessments",
		map[string]any{"workflow_id": "wf-1", "axes": []string{"model"}},
		auth.Principal{TenantID: "tn-1"})
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a request naming a subset of axes answered %d, want 400", rec.Code)
	}
	if src.runs != 0 {
		t.Fatal("the assessment ran anyway — and an assessment that ran is provider money spent")
	}
}

// TestAWorkflowNeverAssessedIs404AndSaysSo separates the empty state from the honest one.
func TestAWorkflowNeverAssessedIs404AndSaysSo(t *testing.T) {
	s := assessmentServer(t, &stubAssessments{found: false})
	req := asPrincipal(t, http.MethodGet, "/api/v1/assessments?workflow_id=wf-1", nil, auth.Principal{TenantID: "tn-1"})
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("answered %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not been assessed") {
		t.Fatalf("the 404 does not distinguish \"nobody has assessed this\" from \"no such workflow\": %s", rec.Body.String())
	}
}

func TestAnIncompleteReportIs422NotAGeneric500(t *testing.T) {
	s := assessmentServer(t, &stubAssessments{runErr: assessment.ErrIncompleteAssessment})
	req := asPrincipal(t, http.MethodPost, "/api/v1/assessments", map[string]any{"workflow_id": "wf-1"},
		auth.Principal{TenantID: "tn-1"})
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("answered %d, want 422", rec.Code)
	}
}

// ── The view ─────────────────────────────────────────────────────────────────────────────────────

func readView(t *testing.T, s *Server) AssessmentView {
	t.Helper()
	req := asPrincipal(t, http.MethodGet, "/api/v1/assessments?workflow_id=wf-1", nil, auth.Principal{TenantID: "tn-1"})
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", rec.Code, rec.Body.String())
	}
	var v AssessmentView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding the view: %v", err)
	}
	return v
}

// TestTheViewShipsNineFindingsAlreadyOrdered is FR5 where it matters: the browser must not sort. A
// console that ordered them itself would eventually order them by a severity somebody guessed, which
// is the one ordering the requirement names as forbidden.
func TestTheViewShipsNineFindingsAlreadyOrdered(t *testing.T) {
	s := assessmentServer(t, &stubAssessments{report: nineFindings(t), found: true})
	v := readView(t, s)
	if len(v.Findings) != 9 {
		t.Fatalf("got %d findings, want 9", len(v.Findings))
	}
	for i, f := range v.Findings {
		if f.Rank != i {
			t.Fatalf("finding %d carries rank %d — the array is not in rank order, so a console reading "+
				"it top to bottom is reading a different ordering than the one the server computed", i, f.Rank)
		}
	}
	if v.Findings[0].State != assessment.StateMeasured {
		t.Fatalf("the strongest finding is %s, want measured", v.Findings[0].State)
	}
	if last := v.Findings[len(v.Findings)-1]; last.State != assessment.StateRefused {
		t.Fatalf("the weakest finding is %s, want refused", last.State)
	}
}

// TestTheViewCarriesPartialAndCannotFailAsFacts is the pair of claims the console must not derive.
//
// `partial` is §7.3 — a partial report is not presented as complete, and that has to be a fact the
// report carries rather than one a reader notices. `eval_set_cannot_fail` is
// `eval-set-decisiveness`'s sharpest statement, and it is computed from the CASE LIST rather than from
// `n_indecisive == n_cases` so the claim and the enumeration cannot disagree.
func TestTheViewCarriesPartialAndCannotFailAsFacts(t *testing.T) {
	s := assessmentServer(t, &stubAssessments{report: nineFindings(t), found: true})
	v := readView(t, s)
	if !v.Partial {
		t.Fatal("a report carrying a budget_exhausted finding does not report itself partial")
	}
	var measured *FindingView
	for i := range v.Findings {
		if v.Findings[i].State == assessment.StateMeasured {
			measured = &v.Findings[i]
		}
	}
	if measured == nil {
		t.Fatal("no measured finding in the view")
	}
	if !measured.EvalSetCannotFail {
		t.Fatal("an eval set whose every oracle is indecisive does not report that it cannot fail — so a " +
			"score of 0.94 renders as a strong result")
	}
	if measured.EvalSet == nil || len(measured.EvalSet.Cases) != 2 {
		t.Fatal("the cases are not enumerated on the finding, so FR13's \"a count is not a case list\" " +
			"would need a second fetch a renderer can skip")
	}
}

// TestAReinferenceRendersAsADiffNamingTheCause is FR9 at the wire, and the assertion that matters is
// the CAUSE: a changed report raises exactly one question, and the answer must not be left to the
// reader.
func TestAReinferenceRendersAsADiffNamingTheCause(t *testing.T) {
	src := &stubAssessments{report: nineFindings(t), found: true, diff: []assessment.AxisDiff{{
		Axis:                       assessment.AxisMemory,
		BeforeState:                assessment.StateObserved,
		AfterState:                 assessment.StateObserved,
		BeforeOrigin:               assessment.OriginInferred,
		AfterOrigin:                assessment.OriginInferred,
		BeforeClaim:                "a per-session store",
		AfterClaim:                 "a per-session store pruned after twenty turns",
		BeforeProviderModelVersion: "anthropic/claude-opus-5-20260501",
		AfterProviderModelVersion:  "anthropic/claude-opus-5-20260812",
		Cause:                      assessment.CauseProviderModel,
		Why:                        "the model behind this finding changed",
	}}}
	s := assessmentServer(t, src)
	req := asPrincipal(t, http.MethodPost, "/api/v1/assessments",
		map[string]any{"workflow_id": "wf-1", "reinfer": true}, auth.Principal{TenantID: "tn-1"})
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("answered %d: %s", rec.Code, rec.Body.String())
	}
	var v AssessmentView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !v.Reinferred {
		t.Fatal("the response does not say it is a re-inference")
	}
	if len(v.Diff) != 1 || v.Diff[0].Cause != assessment.CauseProviderModel {
		t.Fatalf("the diff does not attribute the change to the provider: %+v", v.Diff)
	}
}

// TestAnOrdinaryRunCarriesNoDiff — the field is absent, not empty. `[]` would say "we compared and
// nothing changed", which is a claim this call did not make.
func TestAnOrdinaryRunCarriesNoDiff(t *testing.T) {
	s := assessmentServer(t, &stubAssessments{report: nineFindings(t), found: true})
	req := asPrincipal(t, http.MethodPost, "/api/v1/assessments",
		map[string]any{"workflow_id": "wf-1"}, auth.Principal{TenantID: "tn-1"})
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), `"diff"`) {
		t.Fatalf("an ordinary run shipped a diff key: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"reinferred"`) {
		t.Fatalf("an ordinary run claimed to be a re-inference: %s", rec.Body.String())
	}
}

// TestEvidencePathsAreDerivedAndResolveToRealRoutes keeps design D5's promise checkable: a finding's
// evidence must be a route this package actually serves.
func TestEvidencePathsAreDerivedAndResolveToRealRoutes(t *testing.T) {
	s := assessmentServer(t, &stubAssessments{report: nineFindings(t), found: true})
	v := readView(t, s)
	want := map[assessment.Surface]string{
		assessment.SurfaceGraph: "/api/v1/workflows/wf-1/pattern-graph",
		assessment.SurfaceBoard: "/api/v1/workflows/wf-1/eval-board",
	}
	for _, f := range v.Findings {
		if got := want[f.EvidenceSurface]; got != "" && f.EvidencePath != got {
			t.Fatalf("%s points at %q, want %q", f.Axis, f.EvidencePath, got)
		}
		if f.EvidencePath == "" {
			t.Fatalf("%s carries no evidence path", f.Axis)
		}
	}
}

// TestNoViewFieldCanCarryAComposite is ruling R4 at the LAST place it could arrive — the wire.
//
// 🔴 Read by reflection rather than by reading the struct, so a field added later is caught by the
// fence and not by review. This is the same shape as P32's credential fence and it is here for the
// same reason: the refusal has to be self-defending.
func TestNoViewFieldCanCarryAComposite(t *testing.T) {
	banned := []string{"score", "grade", "level", "rating", "maturity", "overall", "rank"}
	// `Rank` on a FINDING is the evidence-strength position, which is the opposite of a composite: it
	// is per-finding and ordinal, and it exists precisely so nobody re-derives an ordering. Named as an
	// exception rather than dropped from the word list, so the list keeps guarding the type it guards.
	allowed := map[string]bool{"FindingView.Rank": true}

	var walk func(t reflect.Type, path string)
	seen := map[reflect.Type]bool{}
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Ptr || rt.Kind() == reflect.Slice {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			key := rt.Name() + "." + f.Name
			// An eval set's own score is legitimate: it is a measurement with an interval and a case
			// list behind it, which is the one number in this phase that is not a composite.
			if !strings.HasPrefix(key, "EvalSetReport.") && !strings.HasPrefix(key, "Interval.") && !allowed[key] {
				lower := strings.ToLower(f.Name)
				for _, w := range banned {
					if strings.Contains(lower, w) {
						t.Fatalf("%s carries %q. Nine axes do not reduce to one number, and the wire is "+
							"the last place that refusal can be enforced before it is a screenshot.", key, w)
					}
				}
			}
			walk(f.Type, key)
		}
	}
	walk(reflect.TypeOf(AssessmentView{}), "AssessmentView")
}

// oracleCannotFail is a REAL `evalharness.OracleVerdict` for a schema that constrains nothing —
// the "most misleading case in the set", built from the type the product actually uses rather than a
// look-alike, so the fields it must carry are the fields it is checked for.
func oracleCannotFail() evalharness.OracleVerdict {
	return evalharness.OracleVerdict{
		Kind:   "schema",
		Reason: "the output schema constrains nothing: it accepts every JSON value",
	}
}
