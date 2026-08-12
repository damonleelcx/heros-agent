package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// p11.go is the P11 run-linking ingest surface — the authenticated endpoint the CLI's `link` command
// transmits to. It is the server counterpart of the egress boundary:
//
//   - the tenant is derived from the authenticated PRINCIPAL, never from the request body (FR/NFR11);
//   - the payload is ingested through internal/linkingest, which lands events in the EXISTING P2.5
//     substrate and is idempotent by run identity (FR14/FR15);
//   - a re-link reports "already linked" (HTTP 409) rather than double-counting;
//   - a run in a closed period is rejected distinctly;
//   - the response carries the console route the CLI prints on success (FR18).
//
// It never reads a provider credential — the allowlist has no such field — so there is nothing here to
// leak into a log or a response.

// LinkIngestSource is the P11 ingest dependency: it ingests a payload for a tenant, answers coverage,
// and reads back a single linked run.
//
// 🔴 `LinkedRun` is the READ side, and it was missing for as long as linking existed. A run the CLI
// transmitted was accepted, stored durably, and counted — and then the only surface that could see it
// again was a coverage COUNT on the billing page. Asked for the run itself, the console answered
// **"no such run"**, because `/api/v1/runs/{run_id}` reads the EXECUTOR's `run` table and a linked run
// lands in `run_link`. Two different tables, one identifier, and nothing joining them.
type LinkIngestSource interface {
	Ingest(tenantID string, p runlink.Payload) (linkingest.Result, error)
	Coverage(tenantID string) (linkingest.LinkCoverage, error)
	LinkedRun(tenantID, runID string) (linkingest.LinkedRun, bool, error)
}

// linkCoverageFor maps the ingest store's coverage into the billing view shape. It is how the P7 billing
// read model learns how complete its SUM figure is (FR17) without the metering code depending on the
// linking code — the api layer joins the two read models.
func (s *Server) linkCoverageFor(tenantID string) *LinkCoverageView {
	if s.runLinking == nil {
		return nil // coverage UNKNOWN — the console renders that distinctly, never as complete
	}
	c, err := s.runLinking.Coverage(tenantID)
	if err != nil {
		// 🔴 nil is UNKNOWN coverage, and that is the correct answer to a read failure — the same answer
		// an unmounted source gives. It is emphatically NOT zero and NOT complete: the console renders
		// unknown distinctly, and a spend figure at 100%% and one whose denominator could not be read
		// look identical as a number and mean opposite things.
		return nil
	}
	return &LinkCoverageView{
		RunsLinked: c.RunsLinked, RunsReported: c.RunsReported, Known: c.Known, Complete: c.Complete,
	}
}

// MountRunLinking registers the run-linking ingest routes. Call after New. Optional, like every other mount.
func (s *Server) MountRunLinking(src LinkIngestSource) {
	s.runLinking = src
	s.Mux.HandleFunc("POST /api/v1/run-links", s.handleRunLink)
	s.Mux.HandleFunc("GET /api/v1/runs/{run_id}/link", s.handleLinkedRun)
	s.Mux.HandleFunc("GET /api/v1/whoami", s.handleWhoAmI)
}

// LinkedRunView is one linked run, as the console renders it.
//
// It is deliberately NOT `RunView`. A `RunView` is an EXECUTOR run: attempt groups, per-node input and
// output blobs, a terminal status. A linked run has none of that and never will — the CLI transmits an
// allowlisted summary, so there is no input, no output, and no per-node status to show. Rendering one
// as the other would mean filling those columns with something, and the only somethings available are
// invented. What a linked run does carry is what the developer actually saw: the scores, with their
// intervals, computed by their own harness on their own machine.
type LinkedRunView struct {
	RunID          string `json:"run_id"`
	WorkflowID     string `json:"workflow_id"`
	ConfigHash     string `json:"config_hash"`
	ConfigHash12   string `json:"config_hash_display"`
	SourceRevision string `json:"source_revision"`
	// ToolVersion is the CLI build that produced the measurement. It is shown because a score is only
	// comparable to another score from the same harness, and this is the only place that fact is recorded.
	ToolVersion string `json:"tool_version"`
	LinkedAt    string `json:"linked_at"`
	// Scores are stored verbatim from the link, NOT recomputed here (FR: parity is a stored fact). The
	// number in the console is the number the developer read in their terminal, or it is a bug.
	Scores []LinkedScoreView `json:"scores"`
}

// LinkedScoreView is one metric with the interval that qualifies it. The interval is not optional
// decoration: a value without one is a claim this platform does not make.
type LinkedScoreView struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	CILow  float64 `json:"ci_low"`
	CIHigh float64 `json:"ci_high"`
}

// handleLinkedRun answers with a linked run, or says precisely which of the three things is true:
// the capability is absent (503), the caller is not authenticated (401), or that run is not linked (404).
func (s *Server) handleLinkedRun(w http.ResponseWriter, r *http.Request) {
	if s.runLinking == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "run-linking ingest is not mounted"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "reading a linked run requires an authenticated tenant"})
		return
	}
	runID := r.PathValue("run_id")

	// Scoped to the AUTHENTICATED tenant, exactly as ingest is. A run id is guessable, so scoping it
	// anywhere else would make this a cross-tenant read of somebody's cost figures.
	lr, found, err := s.runLinking.LinkedRun(principal.TenantID, runID)
	if err != nil {
		// A read failure is NOT "not linked". Reporting it as 404 would tell a user their run was never
		// transmitted, on a day the database was merely unreachable.
		writeJSON(w, http.StatusBadGateway, specError{Error: "could not read the linked run: " + err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, specError{Error: "no such linked run"})
		return
	}

	view := LinkedRunView{
		RunID: lr.RunID, WorkflowID: lr.WorkflowID,
		ConfigHash: lr.ConfigHash, ConfigHash12: shortHash12(lr.ConfigHash),
		SourceRevision: lr.SourceRevision, ToolVersion: lr.ToolVersion,
		LinkedAt: lr.LinkedAt.UTC().Format(time.RFC3339),
		Scores:   make([]LinkedScoreView, 0, len(lr.Scores)),
	}
	for _, sc := range lr.Scores {
		view.Scores = append(view.Scores, LinkedScoreView{
			Metric: sc.Metric, Value: sc.Value, CILow: sc.CILow, CIHigh: sc.CIHigh,
		})
	}
	writeJSON(w, http.StatusOK, view)
}

// shortHash12 is the display form of a config hash — the same twelve characters the rest of the console
// shows, so one configuration reads identically on every surface.
func shortHash12(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

// handleWhoAmI answers `login`'s token validation: it returns the identity the authenticated token
// resolves to. Behind auth.Compose, so an unknown token is already a 401 before this runs.
func (s *Server) handleWhoAmI(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "authentication required"})
		return
	}
	// Signing in is an authenticated act too, so a person who has signed in and never linked anything
	// still has a billing surface rather than an absent one.
	s.provisionAccount(principal.TenantID)

	// 🔴 ADDITIVE. `identity` keeps its name, its meaning and its value — both existing callers (the
	// CLI's `Validate` and the console's platform-token seam) read only that field, and P27 must not
	// break either. What is added beside it is who and where.
	body := map[string]any{
		"identity": principal.TenantID,
		// The organization, under its own name, so a caller does not have to know that `identity`
		// happens to be an organization id.
		"organization_id": principal.TenantID,
		// "personal" or "machine". The difference decides what member removal covers, and a caller that
		// has to infer it from a blank field will infer it wrong.
		"credential_kind": credentialKind(principal),
	}
	if principal.UserID != "" {
		// ABSENT for a machine credential, never a placeholder: a placeholder here would let a caller
		// attribute an action to a person who did not take it.
		body["user_id"] = principal.UserID
	}
	if s.accounts != nil {
		if t, err := s.accounts.Store().GetTenant(principal.TenantID); err == nil {
			body["organization_name"] = t.Name
		}
		// 🔴 `email_verified`, and its ABSENCE was a shipped defect rather than an omission of
		// something nobody wanted. The console's account page reads exactly this field
		// (`Boolean(who.data.email_verified)`), so a body that never carries it renders `undefined` as
		// FALSE — and the banner "Confirm your address to unlock inviting people and paid plans" was
		// shown to a person whose address WAS confirmed, permanently, with no action that could clear
		// it. Observed in production: `platform_user.email_verified_at` was set the moment the link was
		// clicked, and the banner never moved.
		//
		// Read from the store, not from the principal: verification happens AFTER a session is issued,
		// so anything carried on the credential is a snapshot that is stale exactly when it matters.
		// This is the same rule the session store follows for revocation — read it every time, because
		// a cached "was true a moment ago" is a window in which the answer is wrong.
		//
		// Only for a person. A machine credential has no address, so the field stays ABSENT rather than
		// false — the two are different facts and a caller must be able to tell them apart.
		if principal.UserID != "" {
			if u, err := s.accounts.Store().GetUser(principal.UserID); err == nil {
				body["email_verified"] = u.EmailVerified()
				if u.Email != "" {
					body["email"] = u.Email
				}
			}
			// A read failure leaves the field absent, deliberately: absent means "we could not say",
			// and the console is required to treat that as unknown rather than as unconfirmed.
		}
	}
	writeJSON(w, http.StatusOK, body)
}

// handleRunLink ingests one linked run.
func (s *Server) handleRunLink(w http.ResponseWriter, r *http.Request) {
	if s.runLinking == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "run-linking ingest is not mounted"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "linking a run requires an authenticated tenant"})
		return
	}

	// 🔴 The first authenticated act provisions the account (P29 §7.1). It is create-if-absent and it
	// never corrects an existing one — a "reconciliation" here would move a paying customer back to Free
	// on their next link, silently.
	s.provisionAccount(principal.TenantID)

	var payload runlink.Payload
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	dec.DisallowUnknownFields() // a payload with a field outside the wire contract is rejected, not silently accepted
	if err := dec.Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the linked payload is not valid: " + err.Error()})
		return
	}

	// Scope is the authenticated tenant. Any tenant a client might try to smuggle is ignored — the
	// payload has no tenant field, and the ingester takes the identity from here.
	res, err := s.runLinking.Ingest(principal.TenantID, payload)
	if err != nil {
		var mismatch *linkingest.ContractMismatch
		var closed *linkingest.ClosedPeriod
		switch {
		case errors.As(err, &mismatch):
			writeJSON(w, http.StatusUpgradeRequired, map[string]any{
				"error": mismatch.Error(), "contract_version": mismatch.Want,
			})
		case errors.As(err, &closed):
			// A closed period is a legible terminal state, not a server fault: the CLI reports it and the
			// run stays valid locally. 409 distinguishes it from a 400 malformed payload.
			writeJSON(w, http.StatusConflict, map[string]any{"error": closed.Error(), "closed_period": true})
		default:
			writeJSON(w, http.StatusBadRequest, specError{Error: err.Error()})
		}
		return
	}

	code := http.StatusCreated
	if res.AlreadyLinked {
		// Idempotency surfaced as 409: the CLI reads this as "counted once, not again".
		code = http.StatusConflict
	}
	writeJSON(w, code, map[string]any{
		"accepted":         res.Accepted,
		"already_linked":   res.AlreadyLinked,
		"run_url":          res.RunURL,
		"contract_version": res.ContractVersion,
	})
}

// ── the OPT-IN workflow structure ────────────────────────────────────────────────────────────────

// WorkflowIRSource stores and reads the opt-in structure. Separate from LinkIngestSource because a
// deployment can accept runs and refuse structure — accepting more than a customer's policy allows is
// exactly the kind of thing that should require a second, explicit mount.
type WorkflowIRSource interface {
	Put(ir linkingest.WorkflowIR) error
	Latest(tenantID, workflowID string) (linkingest.WorkflowIR, bool, error)
}

// MountWorkflowIR registers the opt-in structure ingest. Call after New. Optional, like every mount:
// unmounted, the route answers 503 and `heros link --with-ir` reports that this deployment does not
// accept structure — which is a policy answer, not a failure.
// P29 · the FLAT route is the one the CLI addresses, and the parameterised one is kept registered for
// exactly one release so a deployed CLI reaching it from inside the cluster still works. See
// publicroutes.go: the flat one is ExposurePublic, the parameterised one is ExposureInternal and is never
// published. The identifier moved into the payload, where `WorkflowIRPayload.WorkflowID` already carried
// it — the URL segment was duplicating the body, and that duplication is what made the path unpublishable
// as an `Exact` ingress rule.
func (s *Server) MountWorkflowIR(src WorkflowIRSource) {
	s.workflowIR = src
	s.Mux.HandleFunc("POST /api/v1/workflow-ir", s.handleWorkflowIRIngest)
	s.Mux.HandleFunc("POST /api/v1/workflows/{workflow_id}/ir", s.handleWorkflowIRIngest)
}

func (s *Server) handleWorkflowIRIngest(w http.ResponseWriter, r *http.Request) {
	if s.workflowIR == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "this deployment does not accept workflow structure"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "transmitting workflow structure requires an authenticated tenant"})
		return
	}

	var payload runlink.WorkflowIRPayload
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	// 🔴 As with the run link: a field outside the wire contract is REJECTED, not ignored. On this
	// endpoint that is a privacy control, not a hygiene one — silently accepting an unknown key is how a
	// future CLI starts sending prompt text to a platform that never agreed to hold it.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the workflow structure is not valid: " + err.Error()})
		return
	}
	if payload.ContractVersion != runlink.WorkflowIRContractVersion {
		writeJSON(w, http.StatusUpgradeRequired, map[string]any{
			"error":            "workflow-structure contract mismatch",
			"contract_version": runlink.WorkflowIRContractVersion,
		})
		return
	}
	// On the parameterised route the path names the workflow and so does the body. They must agree: a
	// mismatch means one of them is describing something else, and guessing which would attach a
	// structure to the wrong subject. On the FLAT route there is no path identifier — `PathValue` returns
	// "" — and the body is the only statement of which workflow this is, which is the point of the shape.
	//
	// 🔴 The empty check is `!= ""`, not "if this is the flat route". A precedence rule ("the path wins",
	// "the body wins") is what turns a disagreement into a silent mis-attribution, so there is none.
	if id := r.PathValue("workflow_id"); id != "" && id != payload.WorkflowID {
		writeJSON(w, http.StatusBadRequest, specError{
			Error: "the workflow id in the path and in the payload disagree — refusing rather than choosing one",
		})
		return
	}
	if payload.WorkflowID == "" || payload.SourceRevision == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "a workflow structure needs both a workflow id and a source revision"})
		return
	}

	// 🔴 P30 task 7.3 — THE HEROS RESULT RIDES THIS PAYLOAD. No second transport, no second route, no
	// second allowlist: a customer-placed tenant's inference arrives on the ingest that already carries
	// its workflow's shape, under the contract that already versions it.
	//
	// It runs BEFORE the structure is stored, and a refusal fails the whole request. Storing the
	// structure and dropping the inference would answer 201 to a submission whose most important half
	// was discarded — and the customer's console would then show a graph with no inferred edges and no
	// statement that any had been refused, which is indistinguishable from an agent that found nothing.
	if !s.acceptAgentFacts(w, r, principal.TenantID, payload) {
		return
	}

	// Scope is the AUTHENTICATED tenant, never anything in the body — the payload has no tenant field
	// at all, which is the strongest form of "this cannot widen scope".
	if err := s.workflowIR.Put(linkingest.WorkflowIR{
		TenantID: principal.TenantID, WorkflowID: payload.WorkflowID,
		SourceRevision: payload.SourceRevision, IRVersion: payload.IRVersion,
		ReceivedAt: time.Now().UTC(), Nodes: payload.Nodes, Edges: payload.Edges,
		// 🔴 Carried through EXACTLY as sent, including when it is absent. The platform never substitutes
		// its own `transform.CoverageTableVersion()`: that would date the customer's verdicts to a table
		// they were never computed against, and it is precisely the STALE label that would be suppressed.
		CoverageVersion: payload.CoverageVersion,
	}); err != nil {
		writeJSON(w, http.StatusBadGateway, specError{Error: "could not store the workflow structure: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"accepted":    true,
		"workflow_id": payload.WorkflowID,
		"nodes":       len(payload.Nodes),
		"edges":       len(payload.Edges),
		"graph_url":   runlink.PlatformBaseURL + "/app/workflows/" + url.PathEscape(payload.WorkflowID) + "/graph",
	})
}
