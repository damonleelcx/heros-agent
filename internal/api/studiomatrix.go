package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/studio"
)

// P10 studio MATRIX surface (M-series): the node × model grid's read/compute routes over the shipped
// P10 capabilities. Models are the rows, a workflow's nodes are the columns, a cell test-runs and binds.
// No ranking, no new runtime path (see openspec/changes/archive/2026-08-01-p10-studio-matrix).

// StudioModelStore is the registry surface the matrix needs (rows + per-cell render). *registry.Store
// satisfies it; an interface for testability.
type StudioModelStore interface {
	ModelCatalog(ctx context.Context) ([]registry.ModelCatalogEntry, error)
	ResolveModel(ctx context.Context, versionID string) (*registry.ModelEntry, error)
	StudioRender(ctx context.Context, versionID string, bindings map[string]string) (string, error)
}

// StudioMatrix bundles the matrix's extra sources beyond the P10 store.
type StudioMatrix struct {
	Store     StudioModelStore
	Workflows *studio.WorkflowCatalog
	Binds     *studio.BindStore
	Runner    *studio.Runner
}

// MountStudioMatrix registers the matrix routes. Call after MountPromptRegistry.
func (s *Server) MountStudioMatrix(m StudioMatrix) {
	s.studioMatrix = m
	// 🔴 `GET /api/v1/workflows` is NO LONGER registered here (P29 §4.1). It answered from
	// `studio.WorkflowCatalog`, a PROCESS-LOCAL map filled only by `cmd/demo` and `cmd/proof` — so on
	// every real deployment it returned an empty list, permanently, and the studio's workflow picker had
	// nothing in it for a reason no screen stated. `MountEnumeration` now serves it from the reported
	// structure store, which is durable and scoped to the authenticated organization.
	//
	// The catalog itself stays: the demo binaries still fill it and still read it through
	// `handleWorkflowNodes` below. What changed is that it is off the CONSOLE-FACING path.
	s.Mux.HandleFunc("GET /api/v1/models", s.handleModelCatalog)
	s.Mux.HandleFunc("GET /api/v1/workflows/{id}/nodes", s.handleWorkflowNodes)
	s.Mux.HandleFunc("GET /api/v1/workflows/{id}/bindings", s.handleWorkflowBindings)
	s.Mux.HandleFunc("POST /api/v1/studio/run", s.handleStudioRun)
	s.Mux.HandleFunc("POST /api/v1/studio/bind", s.handleStudioBind)
}

func (s *Server) matrixReady(w http.ResponseWriter) (auth.Principal, bool) {
	if s.studioMatrix.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "the studio matrix is not mounted"})
		return auth.Principal{}, false
	}
	return auth.Principal{}, true
}

func matrixPrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || p.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "the studio matrix requires an authenticated tenant"})
		return auth.Principal{}, false
	}
	return p, true
}

// handleModelCatalog serves the matrix ROWS.
func (s *Server) handleModelCatalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.matrixReady(w); !ok {
		return
	}
	models, err := s.studioMatrix.Store.ModelCatalog(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, specError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// handleWorkflowNodes serves the matrix COLUMNS for a workflow (P29 §6.1).
//
// 🔴 The columns are THE TENANT'S OWN NODES, read from the reported structure. They used to come from
// `studio.WorkflowCatalog` — a process-local map filled only by `cmd/demo` and `cmd/proof` — so on every
// real deployment this answered 404 "no such workflow is loaded" for every workflow a customer had, and
// the matrix had no columns at all.
//
// Each column carries the node's SYMBOL and its CURRENT MODEL, because a matrix whose columns are opaque
// hashes is a matrix nobody can use: the whole question it answers is "which of MY call sites should
// this model go to", and a hash does not tell a reader which call site they are looking at.
//
// The catalog is still consulted FIRST, so the demo binaries keep working unchanged — and, more to the
// point, so a deployment that has loaded a workflow the platform-side way is not overridden by a
// reported one. Reported structure is the FALLBACK, which is the direction that cannot regress anything.
func (s *Server) handleWorkflowNodes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.matrixReady(w); !ok {
		return
	}
	workflowID := r.PathValue("id")
	if s.studioMatrix.Workflows != nil {
		if nodes, ok := s.studioMatrix.Workflows.Nodes(workflowID); ok {
			writeJSON(w, http.StatusOK, map[string]any{"workflow_id": workflowID, "nodes": nodes})
			return
		}
	}

	if s.workflowIR == nil {
		// No reported-structure store to fall back to. The catalog's own answer stands, unchanged: on a
		// catalog-only deployment "no such workflow is loaded" was true before this change and is true
		// after it, and widening it to 503 would tell an operator their deployment is broken when it is
		// doing exactly what it is configured to do.
		if s.studioMatrix.Workflows != nil {
			writeJSON(w, http.StatusNotFound, specError{Error: "no such workflow is loaded"})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, specError{
			Error: "this deployment neither loads workflows nor accepts reported structure, so the matrix has no columns",
		})
		return
	}
	principal, ok := matrixPrincipal(w, r)
	if !ok {
		return
	}
	ir, found, err := s.workflowIR.Latest(principal.TenantID, workflowID)
	if err != nil {
		// A read failure is NOT "you reported nothing" and NOT "no such workflow". Three states, three
		// next actions — see the enumeration's header.
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"state": StateReadFailed,
			"error": "could not read this workflow's reported structure: " + err.Error(),
		})
		return
	}
	if !found {
		// 🔴 200, not 404. The workflow may well exist — the platform has simply never been told its
		// shape, and 404 would read as "no such workflow" and send the reader to check an id that is
		// correct. The response names the command that would fill it.
		writeJSON(w, http.StatusOK, map[string]any{
			"state": "not-reported", "workflow_id": workflowID, "nodes": []studioColumn{},
			"detail": "This organization has not reported this workflow's structure, so the matrix has no " +
				"columns to draw. The platform does not invent the nodes it was not told about.",
			"fill_with": "heros link --with-ir",
		})
		return
	}

	cols := make([]studioColumn, 0, len(ir.Nodes))
	for _, n := range ir.Nodes {
		cols = append(cols, studioColumn{
			NodeID: n.NodeID, Symbol: n.Symbol, File: n.File,
			Provider: n.Provider, ModelID: n.ModelID, Language: n.Language,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"state": "ok", "workflow_id": workflowID, "nodes": cols,
		"source_revision": ir.SourceRevision,
	})
}

// studioColumn is one matrix column: a node the customer reported, named so a reader can tell which of
// their call sites it is.
type studioColumn struct {
	NodeID string `json:"node_id"`
	Symbol string `json:"symbol,omitempty"`
	File   string `json:"file,omitempty"`
	// Provider and ModelID are the node's CURRENT binding as discovered — what the call site does today,
	// which is the baseline every cell in that column is a change FROM.
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"model_id,omitempty"`
	Language string `json:"language,omitempty"`
}

// handleWorkflowBindings serves the current in-force selection per node (one bound cell per column).
func (s *Server) handleWorkflowBindings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.matrixReady(w); !ok {
		return
	}
	principal, ok := matrixPrincipal(w, r)
	if !ok {
		return
	}
	if s.studioMatrix.Binds == nil {
		writeJSON(w, http.StatusOK, map[string]any{"bindings": map[string]studio.Binding{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bindings": s.studioMatrix.Binds.Bindings(principal.TenantID, r.PathValue("id")),
	})
}

type studioRunRequest struct {
	ModelVersionID  string            `json:"model_version_id"`
	PromptVersionID string            `json:"prompt_version_id"`
	Bindings        map[string]string `json:"bindings"`
}

// handleStudioRun test-runs a cell: render the node's prompt, execute against the cell's model through
// the studio runner, and return output + cost + latency + tokens (metered under the studio spend kind).
// It carries NO score, rank, or judgement — the result is exploratory.
func (s *Server) handleStudioRun(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.matrixReady(w); !ok {
		return
	}
	principal, ok := matrixPrincipal(w, r)
	if !ok {
		return
	}
	if s.studioMatrix.Runner == nil {
		// 🔴 P29 §6.2 — REFUSED BY NAME, and the name is the whole message.
		//
		// "studio test-run is not available on this deployment" was the old sentence, and it is the
		// shape of answer that produces a support ticket: it reads as a capability the platform has and
		// has switched off, so the reader's next move is to ask which plan turns it on. There is no such
		// plan. A test-run calls a MODEL PROVIDER, that call needs a provider credential, and **the
		// platform holds no customer provider credential and will not** — which is a boundary the
		// product is built on rather than a gap in this deployment.
		//
		// So the refusal says which thing is missing, says nobody can supply it here, names the local
		// command that does the same work with the customer's own key, and — explicitly — does not imply
		// a plan would change it. `reason_code` is the machine half; the console branches on that and
		// renders its own copy.
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"reason_code": "no_customer_provider_credential",
			"error": "A test-run calls your model provider, and this platform holds no provider " +
				"credential of yours. It never will: your keys stay in your environment, which is the " +
				"boundary the whole product rests on — no plan, role or flag changes it. Run the same " +
				"comparison locally with `heros author --model <ref>` (or `heros eval`), where your own " +
				"key is already configured.",
			"local_command":  "heros author --model <model_ref>",
			"plan_would_fix": false,
		})
		return
	}
	var req studioRunRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the request is not valid JSON: " + err.Error()})
		return
	}
	rendered, err := s.studioMatrix.Store.StudioRender(r.Context(), req.PromptVersionID, req.Bindings)
	if err != nil {
		// A missing/unknown binding or unknown prompt is the author's error — a 400 naming the slot,
		// rendered as a failure distinct from an empty result (FR28 unhappy path).
		writeJSON(w, http.StatusBadRequest, specError{Error: err.Error()})
		return
	}
	modelEntry, err := s.studioMatrix.Store.ResolveModel(r.Context(), req.ModelVersionID)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeJSON(w, http.StatusBadRequest, specError{Error: "no such model version"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, specError{Error: err.Error()})
		return
	}
	caller := studio.Caller{TenantID: principal.TenantID, UserID: principalUser(principal)}
	gwReq := providergateway.Request{Messages: []providergateway.Message{{Role: providergateway.RoleUser, Content: rendered}}}
	result, err := s.studioMatrix.Runner.Run(r.Context(), caller, modelEntry, gwReq, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, specError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type studioBindRequest struct {
	WorkflowID      string `json:"workflow_id"`
	NodeID          string `json:"node_id"`
	ModelVersionID  string `json:"model_version_id"`
	ModelID         string `json:"model_id"`
	PromptName      string `json:"prompt_name"`
	PromptVersionID string `json:"prompt_version_id"`
}

// handleStudioBind binds a node to a cell's (model, prompt version) — bound apply mode. The binding is
// marked UNVERIFIED (a studio selection is not a proof) and replaces any prior binding for the node
// (one bound cell per column). It offers no promotion path and asserts no ranking.
func (s *Server) handleStudioBind(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.matrixReady(w); !ok {
		return
	}
	principal, ok := matrixPrincipal(w, r)
	if !ok {
		return
	}
	if s.studioMatrix.Binds == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "binding is not available on this deployment"})
		return
	}
	var req studioBindRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the request is not valid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.NodeID) == "" || strings.TrimSpace(req.WorkflowID) == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "workflow_id and node_id are required"})
		return
	}
	binding := studio.Binding{
		NodeID:          req.NodeID,
		ModelVersionID:  req.ModelVersionID,
		ModelID:         req.ModelID,
		PromptName:      req.PromptName,
		PromptVersionID: req.PromptVersionID,
		Verified:        false, // a studio bind is never verified — a selection is not a proof.
	}
	s.studioMatrix.Binds.Bind(principal.TenantID, req.WorkflowID, binding)
	writeJSON(w, http.StatusOK, map[string]any{
		"binding": binding,
		// The honest state: bound and in force, but unverified. Never "promoted" or "best".
		"in_force": true,
		"verified": false,
		"note":     "in force — unverified. A studio selection is not a proof; run a multi-seed evaluation to establish a claim.",
	})
}

// principalUser derives a per-user cap key from the principal. The API-key id when present, else the
// tenant (a single-key tenant caps at the tenant level, which is correct).
func principalUser(p auth.Principal) string {
	if p.APIKeyID != "" {
		return p.APIKeyID
	}
	return p.TenantID
}
