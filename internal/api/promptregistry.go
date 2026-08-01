package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/registry"
)

// P10 prompt-authoring write surface (tasks 2.1–2.6) — the platform API's FIRST write route.
//
// # Tenant scoping is server-side, and structural
//
// The registry is content-addressed and global by construction: version_id = sha256({kind,name,spec}).
// To isolate tenants without a new column (the standing constraint forbids a new registry table), the
// stored NAME is namespaced by the authenticated tenant — `t:<tenant>/<name>`. Two tenants publishing
// the byte-identical body get DIFFERENT version_ids because the tenant is part of the hashed envelope,
// so a tenant can never enumerate or collide with another's prompts. The tenant is read only from the
// authenticated principal; a client-supplied tenant identifier in the body is ignored, so it cannot
// widen scope (spec: "cannot be widened by a client-supplied identifier").
//
// # No mutation surface (task 2.2)
//
// These routes are publish + three reads. There is deliberately no update, no delete, no soft-delete
// route — an edit publishes a new version and the prior one stays resolvable. The DB trigger in 0002
// remains the last line of defence, not the first; immutability does not depend on callers choosing
// not to attempt mutation, because no route expresses it.

// PromptStore is the read/write surface the prompt-authoring routes need. *registry.Store satisfies it;
// an interface so these handlers can be tested without Postgres.
type PromptStore interface {
	RegisterPrompt(ctx context.Context, name, body string) (string, error)
	PromptNames(ctx context.Context, prefix string) ([]string, error)
	PromptTimeline(ctx context.Context, name string) ([]registry.PromptTimelineEntry, error)
	DiffPromptVersions(ctx context.Context, a, b string) (*registry.PromptVersionDiff, error)
	StudioRender(ctx context.Context, versionID string, bindings map[string]string) (string, error)
}

// MountPromptRegistry registers the prompt-authoring routes. Call after New. Optional, like the other mounts:
// a deployment without a prompt registry simply does not mount it and the routes answer 503.
func (s *Server) MountPromptRegistry(store PromptStore) {
	s.promptRegistry = store
	s.Mux.HandleFunc("POST /api/v1/prompts/publish", s.handlePublishPrompt)
	s.Mux.HandleFunc("GET /api/v1/prompts", s.handlePromptNames)
	s.Mux.HandleFunc("GET /api/v1/prompts/{name}/timeline", s.handlePromptTimeline)
	s.Mux.HandleFunc("GET /api/v1/prompts/diff", s.handlePromptDiff)
	s.Mux.HandleFunc("POST /api/v1/prompts/impact", s.handlePromptImpact)
	s.Mux.HandleFunc("POST /api/v1/studio/preview", s.handleStudioPreview)
}

// scopedName namespaces a user-facing prompt name under a tenant. Stripping is by known prefix length,
// not by splitting on "/", so a user name that itself contains "/" round-trips unambiguously.
func scopedName(tenantID, name string) string { return "t:" + tenantID + "/" + name }

func unscopeName(tenantID, stored string) string {
	return strings.TrimPrefix(stored, "t:"+tenantID+"/")
}

type publishPromptRequest struct {
	Name string `json:"name"`
	Body string `json:"body"`
	// Any "tenant"/"tenant_id" field a client sends is intentionally NOT part of this struct: scope
	// comes from the authenticated principal, never the body.
}

type publishPromptResult struct {
	VersionID string `json:"version_id"`
	Name      string `json:"name"`
}

func (s *Server) handlePublishPrompt(w http.ResponseWriter, r *http.Request) {
	if s.promptRegistry == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "the prompt registry is not mounted"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		// Belt-and-braces: the auth middleware already gates /api/*, but the write path re-checks so a
		// misconfigured mount can never publish untenanted.
		writeJSON(w, http.StatusUnauthorized, specError{Error: "publishing a prompt requires an authenticated tenant"})
		return
	}
	var req publishPromptRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the request is not valid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "name is required"})
		return
	}

	versionID, err := s.promptRegistry.RegisterPrompt(r.Context(), scopedName(principal.TenantID, req.Name), req.Body)
	if err != nil {
		// A malformed template is the author's mistake and is rejected AT PUBLISH, naming the offending
		// position (task 2.3) — ParseTemplate's error carries the byte offset. Classify it as a 400 so a
		// Postgres outage is never reported to the user as "your template is bad."
		if errors.Is(err, registry.ErrInvalidEntry) {
			writeJSON(w, http.StatusBadRequest, specError{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, specError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, publishPromptResult{VersionID: versionID, Name: req.Name})
}

func (s *Server) handlePromptNames(w http.ResponseWriter, r *http.Request) {
	if s.promptRegistry == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "the prompt registry is not mounted"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "listing prompts requires an authenticated tenant"})
		return
	}
	names, err := s.promptRegistry.PromptNames(r.Context(), "t:"+principal.TenantID+"/")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, specError{Error: err.Error()})
		return
	}
	view := make([]string, 0, len(names))
	for _, n := range names {
		view = append(view, unscopeName(principal.TenantID, n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"names": view})
}

func (s *Server) handlePromptTimeline(w http.ResponseWriter, r *http.Request) {
	if s.promptRegistry == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "the prompt registry is not mounted"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "reading a prompt timeline requires an authenticated tenant"})
		return
	}
	name := r.PathValue("name")
	entries, err := s.promptRegistry.PromptTimeline(r.Context(), scopedName(principal.TenantID, name))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, specError{Error: err.Error()})
		return
	}
	// Strip the tenant prefix back off for display. Never nil: an empty timeline is a legitimate result
	// the UI renders as its empty state, distinguishable from a retrieval failure (which is a 500).
	view := make([]registry.PromptTimelineEntry, 0, len(entries))
	for _, e := range entries {
		e.Name = unscopeName(principal.TenantID, e.Name)
		view = append(view, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "versions": view})
}

func (s *Server) handlePromptDiff(w http.ResponseWriter, r *http.Request) {
	if s.promptRegistry == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "the prompt registry is not mounted"})
		return
	}
	a := r.URL.Query().Get("a")
	b := r.URL.Query().Get("b")
	if a == "" || b == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "both version ids a and b are required"})
		return
	}
	diff, err := s.promptRegistry.DiffPromptVersions(r.Context(), a, b)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, specError{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, specError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, diff)
}

type impactRequest struct {
	ProposedBody string                `json:"proposed_body"`
	Nodes        []registry.ImpactNode `json:"nodes"`
}

func (s *Server) handlePromptImpact(w http.ResponseWriter, r *http.Request) {
	var req impactRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the request is not valid JSON: " + err.Error()})
		return
	}
	analysis, err := registry.AnalyzeImpact(req.ProposedBody, req.Nodes)
	if err != nil {
		// A malformed proposed body has no slot set to analyze against; report it as the author's error,
		// naming the offending position, exactly as publish would.
		writeJSON(w, http.StatusBadRequest, specError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, analysis)
}

type studioPreviewRequest struct {
	VersionID string            `json:"version_id"`
	Bindings  map[string]string `json:"bindings"`
}

// handleStudioPreview renders a prompt version with supplied bindings and returns the exact string a
// run would send (tasks 4.6, 6.1). It carries NO score, rank, or judgement — a preview is exploratory.
// A missing or unknown binding is a 400 naming the offending slot, not a partially-rendered string.
func (s *Server) handleStudioPreview(w http.ResponseWriter, r *http.Request) {
	if s.promptRegistry == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "the prompt registry is not mounted"})
		return
	}
	var req studioPreviewRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the request is not valid JSON: " + err.Error()})
		return
	}
	rendered, err := s.promptRegistry.StudioRender(r.Context(), req.VersionID, req.Bindings)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, specError{Error: err.Error()})
			return
		}
		// A missing/unknown binding is the author's error (Render's ErrInvalidEntry), reported as a 400
		// naming the slot — never a partial render.
		writeJSON(w, http.StatusBadRequest, specError{Error: err.Error()})
		return
	}
	// "unranked": the preview is a string and its metadata, never a verdict. The label lives on the
	// result so a client cannot render it as anything else.
	writeJSON(w, http.StatusOK, map[string]any{
		"rendered": rendered,
		"kind":     "exploratory",
	})
}
