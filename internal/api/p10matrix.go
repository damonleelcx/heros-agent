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
// No ranking, no new runtime path (see openspec/changes/p10-studio-matrix).

// MatrixStore is the registry surface the matrix needs (rows + per-cell render). *registry.Store
// satisfies it; an interface for testability.
type MatrixStore interface {
	ModelCatalog(ctx context.Context) ([]registry.ModelCatalogEntry, error)
	ResolveModel(ctx context.Context, versionID string) (*registry.ModelEntry, error)
	StudioRender(ctx context.Context, versionID string, bindings map[string]string) (string, error)
}

// P10Matrix bundles the matrix's extra sources beyond the P10 store.
type P10Matrix struct {
	Store     MatrixStore
	Workflows *studio.WorkflowCatalog
	Binds     *studio.BindStore
	Runner    *studio.Runner
}

// MountP10Matrix registers the matrix routes. Call after MountP10.
func (s *Server) MountP10Matrix(m P10Matrix) {
	s.p10matrix = m
	s.Mux.HandleFunc("GET /api/p10/workflows", s.handleWorkflows)
	s.Mux.HandleFunc("GET /api/p10/models", s.handleModelCatalog)
	s.Mux.HandleFunc("GET /api/p10/workflows/{id}/nodes", s.handleWorkflowNodes)
	s.Mux.HandleFunc("GET /api/p10/workflows/{id}/bindings", s.handleWorkflowBindings)
	s.Mux.HandleFunc("POST /api/p10/studio/run", s.handleStudioRun)
	s.Mux.HandleFunc("POST /api/p10/studio/bind", s.handleStudioBind)
}

func (s *Server) matrixReady(w http.ResponseWriter) (auth.Principal, bool) {
	if s.p10matrix.Store == nil {
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

// handleWorkflows lists the loaded workflow ids the matrix can be opened for.
func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.matrixReady(w); !ok {
		return
	}
	ids := []string{}
	if s.p10matrix.Workflows != nil {
		ids = s.p10matrix.Workflows.Workflows()
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": ids})
}

// handleModelCatalog serves the matrix ROWS.
func (s *Server) handleModelCatalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.matrixReady(w); !ok {
		return
	}
	models, err := s.p10matrix.Store.ModelCatalog(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, specError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// handleWorkflowNodes serves the matrix COLUMNS for a workflow.
func (s *Server) handleWorkflowNodes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.matrixReady(w); !ok {
		return
	}
	if s.p10matrix.Workflows == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "no workflows are loaded"})
		return
	}
	nodes, ok := s.p10matrix.Workflows.Nodes(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, specError{Error: "no such workflow is loaded"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow_id": r.PathValue("id"), "nodes": nodes})
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
	if s.p10matrix.Binds == nil {
		writeJSON(w, http.StatusOK, map[string]any{"bindings": map[string]studio.Binding{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bindings": s.p10matrix.Binds.Bindings(principal.TenantID, r.PathValue("id")),
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
	if s.p10matrix.Runner == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "studio test-run is not available on this deployment"})
		return
	}
	var req studioRunRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the request is not valid JSON: " + err.Error()})
		return
	}
	rendered, err := s.p10matrix.Store.StudioRender(r.Context(), req.PromptVersionID, req.Bindings)
	if err != nil {
		// A missing/unknown binding or unknown prompt is the author's error — a 400 naming the slot,
		// rendered as a failure distinct from an empty result (FR28 unhappy path).
		writeJSON(w, http.StatusBadRequest, specError{Error: err.Error()})
		return
	}
	modelEntry, err := s.p10matrix.Store.ResolveModel(r.Context(), req.ModelVersionID)
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
	result, err := s.p10matrix.Runner.Run(r.Context(), caller, modelEntry, gwReq, nil)
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
	if s.p10matrix.Binds == nil {
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
	s.p10matrix.Binds.Bind(principal.TenantID, req.WorkflowID, binding)
	writeJSON(w, http.StatusOK, map[string]any{
		"binding": binding,
		// The honest state: bound and in force, but unverified. Never "promoted" or "best".
		"in_force":   true,
		"verified":   false,
		"note":       "in force — unverified. A studio selection is not a proof; run a multi-seed evaluation to establish a claim.",
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
