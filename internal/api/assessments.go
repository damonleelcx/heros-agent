package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/auth"
)

// assessments.go is the P33 surface: run an assessment, and read one back.
//
// # Two routes, and why not more
//
// `careful-api-creation` asks for the alternatives before a new endpoint. The console needs exactly
// two things — start one, and read the latest for a workflow — and everything else it might want is a
// field on the response rather than a route:
//
//   - "list the findings" is the response's `findings` array;
//   - "show the eval cases" is `findings[].eval_set.cases`, carried on the finding because FR13's
//     enumeration must not be a second fetch a renderer can skip;
//   - "where is the evidence" is `findings[].evidence_path`, DERIVED from the reference so no route
//     shape is stored anywhere.
//
// A third route was considered and refused: `GET /api/v1/assessment-findings?axis=…`. It would be a
// second way to obtain a subset of one document, and the subset it returns cannot be rendered
// honestly on its own — an axis read out of its report has lost the report's `partial` flag and its
// tally, which are exactly the two facts that say how to read it.
//
// # 🔴 Both are FLAT
//
// `/api/v1/workflows/{id}/assessment` is the natural shape and it cannot be published: an `Exact`
// ingress rule cannot match a variable segment, and the only rule that could is a `Prefix` under
// `/api/v1/workflows/`, which would publish every sibling anybody adds under that head. That is the
// P29 lesson applied before the 404 rather than after it, and P31 and P32 both applied it the same way.
//
// # What the browser is NOT allowed to derive
//
// The ORDER. FR5 ranks findings by evidence strength, and a console that sorted them itself would
// eventually sort them by a severity somebody guessed — which is the one ordering the requirement
// names as forbidden. So `rank` arrives as a field and the array arrives already sorted, for the same
// reason `evalboard` computes ties server-side: a second implementation in JavaScript drifts.

// AssessmentRunner is the P33 dependency: produce an assessment for a workflow at a revision.
//
// An interface rather than the concrete `*assessment.Runner` so a deployment can mount the READ
// surface over a store it does not let this process write to, and so the handler tests can drive every
// refusal without a database or a provider.
type AssessmentRunner interface {
	// Run assesses a workflow at the tenant's current revision and persists the result.
	Run(ctx context.Context, tenantID, workflowID string) (assessment.Assessment, error)
	// Latest returns the newest assessment of a workflow. ok=false is "none yet", never an error:
	// a workflow nobody has assessed and a workflow whose assessment failed are different states and
	// the console says different things about them.
	Latest(ctx context.Context, tenantID, workflowID string) (assessment.Assessment, bool, error)
	// Reinfer ignores the pin, assesses again, and returns the result WITH a diff against the previous
	// one (FR9). A nil diff means there was no previous assessment — which the console renders as
	// "first assessment", never as "no changes".
	Reinfer(ctx context.Context, tenantID, workflowID string) (assessment.Assessment, []assessment.AxisDiff, error)
}

// ── Console view types (registered in consoletypes.go; rendered by web/console) ──

// AssessmentView is one assessment as the console renders it.
type AssessmentView struct {
	AssessmentID   string `json:"assessment_id"`
	WorkflowID     string `json:"workflow_id"`
	SourceRevision string `json:"source_revision"`
	// AgentConfigHash and AgentConfigHashShort — rendered short, carried full, exactly as the eval
	// board carries a variant's config hash. A finding must stay attributable to an exact
	// configuration, and a truncated hash is not attributable.
	AgentConfigHash      string `json:"agent_config_hash"`
	AgentConfigHashShort string `json:"agent_config_hash_short"`

	StartedAt   int64 `json:"started_at_ms"`
	CompletedAt int64 `json:"completed_at_ms"`

	SpendUSD    float64 `json:"spend_usd"`
	SpendCapUSD float64 `json:"spend_cap_usd"`

	// Findings are the nine, ALREADY ORDERED by evidence strength (FR5).
	Findings []FindingView `json:"findings"`

	// Tally is the distribution the manager gets INSTEAD of a score (PRD §4, ruling R4). Nine numbers
	// that sum to nine, from which no ordering of one repository against another can be computed.
	Tally assessment.Tally `json:"tally"`

	// Partial says the assessment stopped early because it reached its spend cap. 🔴 A field rather
	// than something the console infers from a `budget_exhausted` finding: §7.3's requirement is that
	// a partial report is not presented as complete, and "not presented as complete" has to be a fact
	// the report carries, not one a reader notices.
	Partial bool `json:"partial"`
	// AllNotMeasured is true when every one of the nine came back `not_measured`. The console says
	// something different in that case, because a report of nine absences is not nine findings — it
	// is one finding about us.
	AllNotMeasured bool `json:"all_not_measured"`

	// Reinferred says this response is the result of an explicit re-inference (FR9).
	Reinferred bool `json:"reinferred,omitempty"`
	// Diff is the change against the previous assessment, ONLY the axes that changed.
	//
	// 🔴 Each entry names WHICH INPUT MOVED — the source, our configuration, or the provider's model.
	// That attribution is computed server-side for `evalboard`'s reason: "did this get worse because of
	// us or because of them" has exactly one correct answer, and a second implementation in JavaScript
	// would drift from this one.
	Diff []assessment.AxisDiff `json:"diff,omitempty"`
}

// FindingView is one axis as the console renders it.
type FindingView struct {
	Axis   assessment.Axis   `json:"axis"`
	State  assessment.State  `json:"state"`
	Origin assessment.Origin `json:"origin"`
	Claim  string            `json:"claim"`

	// Rank is the finding's position on the evidence-strength ladder, 0 strongest. Carried so the
	// console can group without re-deriving an ordering it must not invent (FR5).
	Rank int `json:"rank"`

	EvidenceSurface assessment.Surface `json:"evidence_surface"`
	// EvidenceLocator is the SUBJECT the evidence is about — a workflow id, or a variant id.
	//
	// 🔴 Carried as a field so the console builds its own link from the subject rather than parsing one
	// out of `evidence_path`. The live run against `nousresearch/hermes-agent` is why: that workflow's
	// id is `github.com/nousresearch/hermes-agent`, so a console splitting the platform path on `/` and
	// taking the fourth segment linked to a workflow called `github.com`. Nothing errored; the link
	// just went somewhere else, which is the quietest kind of wrong.
	EvidenceLocator string `json:"evidence_locator"`
	// EvidencePath is the platform route that serves the evidence, derived at render time from the
	// reference. Never stored: a persisted URL is a copy of the router, and the copy is what serves a
	// 404 to a reader following a two-month-old finding.
	EvidencePath string `json:"evidence_path"`

	MissingInput assessment.MissingInput `json:"missing_input,omitempty"`
	RefusalCause assessment.RefusalCause `json:"refusal_cause,omitempty"`

	ProviderModelVersion string `json:"provider_model_version,omitempty"`
	InferenceAddress     string `json:"inference_address,omitempty"`

	// EvalSet is the decisiveness report, present only on a measured finding.
	EvalSet *assessment.EvalSetReport `json:"eval_set,omitempty"`
	// EvalSetCannotFail is `eval-set-decisiveness`'s sharpest statement, computed server-side from the
	// CASE LIST rather than from `n_indecisive == n_cases`, so the claim and the enumeration behind it
	// cannot disagree. When true the console states that the set cannot fail and does not present the
	// score as evidence of quality.
	EvalSetCannotFail bool `json:"eval_set_cannot_fail,omitempty"`
}

// MountAssessments registers the P33 routes. Call after New.
func (s *Server) MountAssessments(src AssessmentRunner) {
	s.assessments = src
	s.Mux.HandleFunc("POST /api/v1/assessments", s.handleRunAssessment)
	s.Mux.HandleFunc("GET /api/v1/assessments", s.handleReadAssessment)
}

type runAssessmentRequest struct {
	// WorkflowID is required. `DisallowUnknownFields` keeps the payload to these two keys: the field a
	// future client would add here is `axes: [...]` — a request to assess a subset — and accepting one
	// silently would produce a report with fewer than nine findings, which FR1 exists to forbid.
	// 🚫 There is deliberately no `tenant_id`: the tenant comes from the credential, which is the
	// strongest available form of "this request cannot widen its own scope".
	WorkflowID string `json:"workflow_id"`
	// Reinfer ignores the pin and assesses again (FR9).
	//
	// 🔴 A FIELD rather than a third route, and that is `careful-api-creation`'s alternative taken
	// rather than dismissed: it is the same act on the same resource, and the result is the same
	// document with one extra key. What it is NOT is a default — `false` is the ordinary call, which
	// is free and idempotent on a pinned key, and a caller must say `true` to spend money. That is why
	// the flag lives in the BODY and not in a query string: a retry loop repeats a body it was given,
	// and a URL is the thing somebody pastes into a browser.
	Reinfer bool `json:"reinfer,omitempty"`
}

func (s *Server) handleRunAssessment(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.assessmentPrincipal(w, r)
	if !ok {
		return
	}
	var req runAssessmentRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "the request body must be {\"workflow_id\": \"…\"} and nothing else: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.WorkflowID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "workflow_id is required"})
		return
	}

	var (
		a    assessment.Assessment
		diff []assessment.AxisDiff
		err  error
	)
	if req.Reinfer {
		a, diff, err = s.assessments.Reinfer(r.Context(), principal.TenantID, req.WorkflowID)
	} else {
		a, err = s.assessments.Run(r.Context(), principal.TenantID, req.WorkflowID)
	}
	if err != nil {
		// 🔴 A refusal that names its cause, not a 500 with a generic sentence. The two failures a
		// caller can act on are distinguished: an incomplete report is our defect, and an unresolvable
		// evidence reference is a surface that is not mounted in this deployment.
		status := http.StatusInternalServerError
		if errors.Is(err, assessment.ErrIncompleteAssessment) || errors.Is(err, assessment.ErrInvalidFinding) {
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	view := assessmentView(a)
	if req.Reinfer {
		// 🔴 Set even when EMPTY, and the distinction is the point. `[]` means "we assessed again and
		// nothing changed" — a real and reassuring answer. `null` (the field absent) means there was no
		// previous assessment to compare against. Collapsing them would render a first assessment as
		// "no changes", which is false in the most misleading direction: everything is new.
		view.Diff = diff
		view.Reinferred = true
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s *Server) handleReadAssessment(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.assessmentPrincipal(w, r)
	if !ok {
		return
	}
	workflowID := strings.TrimSpace(r.URL.Query().Get("workflow_id"))
	if workflowID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "workflow_id is required"})
		return
	}
	a, found, err := s.assessments.Latest(r.Context(), principal.TenantID, workflowID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if !found {
		// 404 is DISTINCT from an assessment that exists and found nothing. The console's empty state
		// depends on telling "nobody has assessed this yet" apart from "we assessed it and could
		// measure nothing" — collapsing the two turns an honest report of nine absences into a page
		// that looks like a missing feature.
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "this workflow has not been assessed yet"})
		return
	}
	writeJSON(w, http.StatusOK, assessmentView(a))
}

func (s *Server) assessmentPrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	if s.assessments == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "surface assessment is not mounted in this deployment"})
		return auth.Principal{}, false
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "assessing a repository requires an authenticated tenant"})
		return auth.Principal{}, false
	}
	return principal, true
}

// AssessmentViewOf renders an assessment as the console receives it.
//
// 🔴 Exported for ONE caller — `cmd/proof/assessment`, which runs the whole pipeline against a real
// repository and needs to show what the console would show. The alternative is a second renderer in
// the proof binary, and a proof that renders its own version of the view proves nothing about the
// view: the ordering, the derived evidence path and the `cannot fail` computation are exactly the
// parts that could be wrong, and they are exactly the parts a copy would reimplement correctly.
//
// This is a deliberate, minimal widening of the package's surface — one function, no new type — and it
// is recorded here so a future reader does not read it as accidental. It is the same trade
// `consoletypes.go` makes and for the same reason.
func AssessmentViewOf(a assessment.Assessment) AssessmentView { return assessmentView(a) }

func assessmentView(a assessment.Assessment) AssessmentView {
	out := AssessmentView{
		AssessmentID:         a.AssessmentID,
		WorkflowID:           a.WorkflowID,
		SourceRevision:       a.SourceRevision,
		AgentConfigHash:      a.AgentConfigHash,
		AgentConfigHashShort: shortConfigHash(a.AgentConfigHash),
		StartedAt:            a.StartedAtMS,
		CompletedAt:          a.CompletedAtMS,
		SpendUSD:             a.SpendUSD,
		SpendCapUSD:          a.SpendCapUSD,
		Tally:                a.Tally(),
		Partial:              a.Partial(),
		AllNotMeasured:       a.AllNotMeasured(),
		Findings:             []FindingView{},
	}
	for rank, f := range a.Ordered() {
		v := FindingView{
			Axis:                 f.Axis(),
			State:                f.State(),
			Origin:               f.Origin(),
			Claim:                f.Claim(),
			Rank:                 rank,
			EvidenceSurface:      f.Evidence().Surface,
			EvidenceLocator:      f.Evidence().Locator,
			EvidencePath:         f.Evidence().Path(),
			MissingInput:         f.MissingInput(),
			RefusalCause:         f.RefusalCause(),
			ProviderModelVersion: f.ProviderModelVersion(),
			InferenceAddress:     f.InferenceAddress(),
			EvalSet:              f.Eval(),
		}
		if f.Eval() != nil {
			v.EvalSetCannotFail = f.Eval().CannotFail()
		}
		out.Findings = append(out.Findings, v)
	}
	return out
}

func shortConfigHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}
