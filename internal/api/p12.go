package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
)

// p12.go is the P12 forge-delivery surface: the console's delivery READ model (state per delivery,
// linked to its proposal, and the reported route condition) and the CI-mediated fetch/report legs.
//
// It is a thin pass-through in the P9 sense (design Decision 3): it authenticates, scopes by the
// authenticated principal's tenant, forwards to the forgedelivery.Service, and maps to the console view
// shape. It computes no business rule — the route condition, the gate, the entitlement, the halt all
// live below it. It never reads a forge credential; the CI fetch returns credential-free Prepared
// values and the report carries none.

// P12Source is the P12 dependency: the forge-delivery service.
type P12Source interface {
	// ListDeliveries returns the tenant's delivery heads, newest first.
	ListDeliveries(ctx context.Context, tenantID string) ([]forgedelivery.DeliveryHead, error)
	// RouteConditionFor reports the tenant's delivery-route condition (configured/no_route/degraded/revoked).
	RouteConditionFor(ctx context.Context, tenantID string) (forgedelivery.RouteCondition, error)
	// Pending serves the CI fetch: verified, deliverable Prepared proposals for a target, scoped to the tenant.
	Pending(ctx context.Context, tenantID string, target forgedelivery.Target) ([]forgedelivery.Prepared, error)
	// RecordReport records a CI-opened delivery.
	RecordReport(ctx context.Context, tenantID string, prep forgedelivery.Prepared, r forgedelivery.Report) (forgedelivery.Result, error)
}

// ── Console view types (registered in consoletypes.go; rendered by web/console) ──

// DeliveryView is one delivery's current state and a link back to the proposal that produced it
// (task 6.3). State is one of open|updated|merged|closed|superseded|reverted.
type DeliveryView struct {
	DeliveryID     string `json:"delivery_id"`
	State          string `json:"state"`
	ConfigHash     string `json:"config_hash"`
	SourceRevision string `json:"source_revision"`
	Target         string `json:"target"`
	Mode           string `json:"mode"`
	ForgeRef       string `json:"forge_ref"`
	Reason         string `json:"reason,omitempty"`
	MergeCommit    string `json:"merge_commit,omitempty"`
	// ProposalRef is the canonical console route to the originating proposal's evidence (P9's rules), so
	// the loop from proposal to outcome is one click, not a search (task 6.3).
	ProposalRef string `json:"proposal_ref"`
}

// RouteConditionView is the reported delivery-route condition, rendered as a condition WITH a next
// action — never an empty list (tasks 6.1, 6.2).
type RouteConditionView struct {
	Kind       string   `json:"kind"`
	Detail     string   `json:"detail,omitempty"`
	NextAction string   `json:"next_action,omitempty"`
	Targets    []string `json:"targets,omitempty"`
}

// DeliveriesView is the console's delivery page read model: the deliveries and the route condition.
type DeliveriesView struct {
	Deliveries []DeliveryView     `json:"deliveries"`
	Route      RouteConditionView `json:"route"`
}

// MountP12 registers the delivery routes. Optional, like every other mount.
func (s *Server) MountP12(src P12Source) {
	s.p12 = src
	s.Mux.HandleFunc("GET /api/v1/deliveries", s.handleP12Deliveries)
	s.Mux.HandleFunc("GET /api/v1/ci/pending", s.handleP12CIPending)
	s.Mux.HandleFunc("POST /api/v1/ci/report", s.handleP12CIReport)
}

func (s *Server) p12Tenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "delivery requires an authenticated tenant"})
		return "", false
	}
	return principal.TenantID, true
}

// handleP12Deliveries serves the console delivery read model (tasks 6.1–6.3).
func (s *Server) handleP12Deliveries(w http.ResponseWriter, r *http.Request) {
	if s.p12 == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "forge delivery is not mounted on this server"})
		return
	}
	tenant, ok := s.p12Tenant(w, r)
	if !ok {
		return
	}
	heads, err := s.p12.ListDeliveries(r.Context(), tenant)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, specError{Error: "reading deliveries: " + err.Error()})
		return
	}
	cond, err := s.p12.RouteConditionFor(r.Context(), tenant)
	if err != nil {
		// A route condition that cannot be read is reported as such, never silently rendered "configured".
		writeJSON(w, http.StatusBadGateway, specError{Error: "reading the delivery route condition: " + err.Error()})
		return
	}
	view := DeliveriesView{
		Deliveries: make([]DeliveryView, 0, len(heads)),
		Route: RouteConditionView{
			Kind: string(cond.Kind), Detail: cond.Detail, NextAction: cond.NextAction, Targets: cond.Targets,
		},
	}
	for _, h := range heads {
		view.Deliveries = append(view.Deliveries, DeliveryView{
			DeliveryID: h.DeliveryID, State: string(h.State), ConfigHash: h.ConfigHash,
			SourceRevision: h.SourceRevision, Target: h.Target, Mode: string(h.Mode),
			ForgeRef: h.ForgeRef, Reason: h.Reason, MergeCommit: h.MergeCommit,
			ProposalRef: forgedelivery.ConsoleEvidencePath(h.ConfigHash, h.SourceRevision),
		})
	}
	writeJSON(w, http.StatusOK, view)
}

// handleP12CIPending serves the authenticated CI fetch: verified, deliverable Prepared proposals for a
// target, scoped server-side to the caller's tenant (task 5.3). The target comes from the query; the
// tenant NEVER does.
func (s *Server) handleP12CIPending(w http.ResponseWriter, r *http.Request) {
	if s.p12 == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "forge delivery is not mounted on this server"})
		return
	}
	tenant, ok := s.p12Tenant(w, r)
	if !ok {
		return
	}
	target, err := parseTargetQuery(r.URL.Query().Get("target"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: err.Error()})
		return
	}
	prepared, err := s.p12.Pending(r.Context(), tenant, target)
	if err != nil {
		if errors.Is(err, forgedelivery.ErrNoRoute) {
			// A reported condition, not a fault — the CI action distinguishes it from "nothing to deliver".
			writeJSON(w, http.StatusOK, map[string]any{"prepared": []any{}, "route": "no_route"})
			return
		}
		writeJSON(w, http.StatusBadGateway, specError{Error: "preparing deliveries: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prepared": prepared})
}

// ciReportBody is what the CI runner POSTs after opening a pull request. It echoes the identity from the
// Prepared it was served so the platform can record it; the platform re-derives the delivery id from
// (config_hash, source_revision, target) and rejects a mismatch, so a runner cannot fabricate identity.
type ciReportBody struct {
	DeliveryID     string `json:"delivery_id"`
	ConfigHash     string `json:"config_hash"`
	SourceRevision string `json:"source_revision"`
	Target         string `json:"target"`
	Mode           string `json:"mode"`
	ProposalID     string `json:"proposal_id"`
	ForgeRef       string `json:"forge_ref"`
	ForgeURL       string `json:"forge_url"`
	Created        bool   `json:"created"`
	Merged         bool   `json:"merged"`
	MergeCommit    string `json:"merge_commit"`
}

// handleP12CIReport records a CI-opened delivery (task 5.1 report leg).
func (s *Server) handleP12CIReport(w http.ResponseWriter, r *http.Request) {
	if s.p12 == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "forge delivery is not mounted on this server"})
		return
	}
	tenant, ok := s.p12Tenant(w, r)
	if !ok {
		return
	}
	var body ciReportBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the report payload is not valid: " + err.Error()})
		return
	}
	target, err := parseTargetQuery(body.Target)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: err.Error()})
		return
	}
	// Re-derive the delivery id server-side; a mismatch means the runner's identity claim is inconsistent.
	want := forgedelivery.DeliveryID(body.ConfigHash, body.SourceRevision, target.Key())
	if body.DeliveryID != want {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the reported delivery id does not match its identity"})
		return
	}
	mode := forgedelivery.Mode(body.Mode)
	if !mode.Valid() {
		writeJSON(w, http.StatusBadRequest, specError{Error: "unknown delivery mode"})
		return
	}
	prep := forgedelivery.Prepared{
		DeliveryID: want, TenantID: tenant, ProposalID: body.ProposalID,
		ConfigHash: body.ConfigHash, SourceRevision: body.SourceRevision, Target: target, Mode: mode,
	}
	report := forgedelivery.Report{
		DeliveryID: want, ForgeRef: body.ForgeRef, ForgeURL: body.ForgeURL,
		Created: body.Created, Merged: body.Merged, MergeCommit: body.MergeCommit,
	}
	res, err := s.p12.RecordReport(r.Context(), tenant, prep, report)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, specError{Error: "recording the delivery: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"delivery_id": res.DeliveryID, "forge_ref": res.PR.Ref, "superseded": res.Superseded,
	})
}

// parseTargetQuery parses "owner/repo" or "owner/repo#workflow" into a Target. Base is not part of the
// key, so it is left empty here (it is only needed when opening, which already happened).
func parseTargetQuery(s string) (forgedelivery.Target, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return forgedelivery.Target{}, errors.New("a target (owner/repo[#workflow]) is required")
	}
	var wf string
	if i := strings.IndexByte(s, '#'); i >= 0 {
		wf = s[i+1:]
		s = s[:i]
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return forgedelivery.Target{}, errors.New("target must be owner/repo[#workflow]")
	}
	return forgedelivery.Target{Owner: parts[0], Repo: parts[1], Workflow: wf}, nil
}
