package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/legal"
)

// p23.go is the consent surface: two endpoints, and no more (task 11.4).
//
//	POST /api/v1/legal/acceptances   record an acceptance — persist-then-acknowledge
//	GET  /api/v1/legal/acceptances   the CALLER'S OWN tenant history, plus what is pending
//
// # 🔴 What this surface deliberately does not have
//
// There is **no cross-tenant read on this path at all** — not a guarded one, not an admin one. The
// tenant and the principal come from the authenticated session on every call and are never read from the
// request body or a query parameter. A tenant id a caller can type is a tenant id a caller can change.
//
// Operator-side access to consent records stays in the P8 console behind its existing RBAC and
// append-only audit. That separation is the reason this file is short.
//
// # 🔴 The request body is exactly three fields
//
// `document_kind`, `document_version`, `content_hash` — plus `method`, which says which commitment
// moment produced the acceptance. Anything else is rejected, because anything else is a field this
// system has no column for and no business recording.
//
// # Persist-then-acknowledge
//
// The 201 is written after the row is committed. Never before. An acknowledged consent with no row is
// indistinguishable from consent that never happened, and the direction of that error is what decides
// whether a customer is told the truth.

// ConsentSource is the consent dependency.
type ConsentSource interface {
	Record(ctx context.Context, tenantID, principalID string, req legal.Request) (legal.Acceptance, bool, error)
	Read(ctx context.Context, tenantID, principalID string) (legal.History, error)
}

// ── Console view types ────────────────────────────────────────────────────────

// AcceptanceView is one recorded acceptance, as the account surface renders it.
//
// It carries the DOCUMENT IDENTITY and a route to the exact archived text (task 10.1). A history entry
// that named a document without linking the version a customer actually accepted would be a list of
// dates — the link to the immutable text is the whole evidentiary point.
type AcceptanceView struct {
	DocumentKind    string `json:"document_kind"`
	DocumentVersion string `json:"document_version"`
	ContentHash     string `json:"content_hash"`
	AcceptedAt      string `json:"accepted_at"`
	Method          string `json:"method"`
	// PrincipalID is the opaque principal that accepted. Never an email, never a name.
	PrincipalID string `json:"principal_id"`
	// ArchivedRoute opens the exact text that was accepted, at its permanent address.
	ArchivedRoute string `json:"archived_route"`
	// Superseded is true when a material later version has been published since.
	Superseded bool `json:"superseded"`
}

// PendingView is a document a principal has not yet accepted at its current version.
type PendingView struct {
	DocumentKind    string `json:"document_kind"`
	DocumentVersion string `json:"document_version"`
	ContentHash     string `json:"content_hash"`
	EffectiveDate   string `json:"effective_date"`
	Route           string `json:"route"`
	// Material is the declared materiality of the version being asked for.
	Material bool `json:"material"`
}

// LegalAcceptanceView is the GET response.
type LegalAcceptanceView struct {
	Accepted []AcceptanceView `json:"accepted"`
	Pending  []PendingView    `json:"pending"`
	// PendingUnknown is true when the manifest could not be read.
	//
	// 🔴 It exists so an empty `pending` cannot be mistaken for "nothing is outstanding". Reading is
	// fail-OPEN (Decision 4) — the history is still returned — but a gate that silently cleared itself
	// because an upstream was down would be fail-open on the COMMITMENT, which is the opposite rule.
	PendingUnknown bool `json:"pending_unknown,omitempty"`
}

// AcceptanceResult is the POST response.
type AcceptanceResult struct {
	Recorded bool `json:"recorded"`
	// Created distinguishes a first acceptance from a repeat. Both are successes; only one made a row.
	Created         bool   `json:"created"`
	DocumentKind    string `json:"document_kind"`
	DocumentVersion string `json:"document_version"`
	ContentHash     string `json:"content_hash"`
	AcceptedAt      string `json:"accepted_at"`
	ArchivedRoute   string `json:"archived_route"`
}

// acceptanceRequest is the wire shape. `DisallowUnknownFields` on the decoder makes the three-field rule
// enforced rather than documented.
type acceptanceRequest struct {
	DocumentKind    string `json:"document_kind"`
	DocumentVersion string `json:"document_version"`
	ContentHash     string `json:"content_hash"`
	Method          string `json:"method"`
}

// RegisterConsent mounts the consent surface.
func (s *Server) RegisterConsent(src ConsentSource) {
	s.consent = src
	s.Mux.HandleFunc("POST /api/v1/legal/acceptances", s.handleConsentRecord)
	s.Mux.HandleFunc("GET /api/v1/legal/acceptances", s.handleConsentRead)
}

func archivedRoute(kind, version string) string {
	return "/legal/" + kind + "/v/" + version
}

func (s *Server) handleConsentRecord(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no session"})
		return
	}
	if s.consent == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":  "the consent surface is not mounted on this deployment",
			"reason": "not_mounted",
		})
		return
	}

	// 🔴 Unknown fields are REFUSED, not ignored. A client sending `tenant_id` or `email` is a client
	// with a misunderstanding about this endpoint, and silently dropping the field would let that
	// misunderstanding ship.
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	var body acceptanceRequest
	if err := decoder.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "the request body must be exactly document_kind, document_version, content_hash and method",
		})
		return
	}

	stored, created, err := s.consent.Record(r.Context(), principal.TenantID, principalIDOf(principal), legal.Request{
		DocumentKind:    legal.Kind(body.DocumentKind),
		DocumentVersion: body.DocumentVersion,
		ContentHash:     body.ContentHash,
		Method:          legal.Method(body.Method),
	})
	if err != nil {
		writeJSON(w, statusForConsentError(err), map[string]string{
			// 🔴 The sentence a customer sees on a failed write. It says what did NOT happen, because the
			// one thing that must never appear is a checkmark over an unrecorded acceptance.
			"error":  "the acceptance was not recorded; nothing has been agreed",
			"reason": reasonForConsentError(err),
			"detail": err.Error(),
		})
		return
	}

	// Only here — after the store returned a committed row — is success reported.
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, AcceptanceResult{
		Recorded:        true,
		Created:         created,
		DocumentKind:    string(stored.DocumentKind),
		DocumentVersion: stored.DocumentVersion,
		ContentHash:     stored.ContentHash,
		AcceptedAt:      stored.AcceptedAt.Format(time.RFC3339),
		ArchivedRoute:   archivedRoute(string(stored.DocumentKind), stored.DocumentVersion),
	})
}

func (s *Server) handleConsentRead(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no session"})
		return
	}
	if s.consent == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error":  "the consent surface is not mounted on this deployment",
			"reason": "not_mounted",
		})
		return
	}

	history, err := s.consent.Read(r.Context(), principal.TenantID, principalIDOf(principal))
	view := LegalAcceptanceView{Accepted: []AcceptanceView{}, Pending: []PendingView{}}
	if err != nil {
		// Reading is fail-OPEN. A manifest outage must not hide a customer's own history from them — and
		// `pending_unknown` is what stops the empty list reading as "you owe nothing".
		if !errors.Is(err, legal.ErrManifestUnavailable) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		view.PendingUnknown = true
	}

	for _, a := range history.Accepted {
		view.Accepted = append(view.Accepted, AcceptanceView{
			DocumentKind:    string(a.DocumentKind),
			DocumentVersion: a.DocumentVersion,
			ContentHash:     a.ContentHash,
			AcceptedAt:      a.AcceptedAt.Format(time.RFC3339),
			Method:          string(a.Method),
			PrincipalID:     a.PrincipalID,
			ArchivedRoute:   archivedRoute(string(a.DocumentKind), a.DocumentVersion),
			Superseded:      a.SupersededBy != "",
		})
	}
	for _, p := range history.Pending {
		view.Pending = append(view.Pending, PendingView{
			DocumentKind:    kindOfRoute(p.Route),
			DocumentVersion: p.Version,
			ContentHash:     p.Hash,
			EffectiveDate:   p.EffectiveDate,
			Route:           p.Route,
			Material:        p.Material,
		})
	}
	writeJSON(w, http.StatusOK, view)
}

// statusForConsentError maps a domain refusal to a status a client can branch on.
//
// The distinctions are the point. A hash mismatch is a 409 rather than a 400: the request was
// well-formed and the CONFLICT is with what the server publishes — which is exactly the situation a
// stale tab produces, and the client's remedy is to reload the document rather than to fix its JSON.
func statusForConsentError(err error) int {
	switch {
	case errors.Is(err, legal.ErrManifestUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, legal.ErrHashMismatch):
		return http.StatusConflict
	case errors.Is(err, legal.ErrUnknownKind),
		errors.Is(err, legal.ErrUnknownVersion),
		errors.Is(err, legal.ErrInvalidMethod),
		errors.Is(err, legal.ErrNoPublishedVersion):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func reasonForConsentError(err error) string {
	switch {
	case errors.Is(err, legal.ErrManifestUnavailable):
		return "manifest_unavailable"
	case errors.Is(err, legal.ErrHashMismatch):
		return "content_hash_mismatch"
	case errors.Is(err, legal.ErrUnknownKind):
		return "unknown_kind"
	case errors.Is(err, legal.ErrUnknownVersion):
		return "unknown_version"
	case errors.Is(err, legal.ErrInvalidMethod):
		return "invalid_method"
	case errors.Is(err, legal.ErrNoPublishedVersion):
		return "no_published_version"
	default:
		return "write_failed"
	}
}

// principalIDOf derives the opaque principal id from the authenticated caller (ADR-008, task 9.5).
//
// Today the seam carries a tenant and an optional key label. Binding the consent record to THIS function
// rather than to a field means P22 making identity real is a change here and nowhere else — no
// migration, no rewrite of stored rows, and no column whose meaning quietly changed.
func principalIDOf(p auth.Principal) string {
	if p.APIKeyID != "" {
		return p.APIKeyID
	}
	// A deployment with no per-principal identity yet records the tenant as the principal. That is
	// honest — it says "this tenant accepted" rather than inventing a user the platform cannot prove —
	// and it is exactly the gap ADR-008 exists to close later without a migration.
	return p.TenantID
}

// kindOfRoute reads the kind back out of a permanent route (`/legal/{kind}/v/{version}`).
func kindOfRoute(route string) string {
	const prefix = "/legal/"
	if len(route) <= len(prefix) {
		return ""
	}
	rest := route[len(prefix):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			return rest[:i]
		}
	}
	return rest
}
