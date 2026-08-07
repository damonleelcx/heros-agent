package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/transform"
)

// transformreceipt.go is the authenticated ingest for the THIRD opt-in payload: what a transform the
// customer generated on their own machine actually did.
//
// # What it accepts and what it structurally cannot
//
// Counts and closed-set outcomes. `runlink.TransformReceipt` carries three integers where a diff would
// go, `DisallowUnknownFields` rejects any key outside the ratified list, and the decode target has no
// field a hunk could land in. A future CLI cannot start sending diffs here by adding a key: it would be
// refused at the boundary with the key named.
//
// # Idempotent by (tenant, config_hash, source_revision)
//
// A re-run of `heros apply --link-receipt` for the same configuration at the same revision leaves ONE
// row. That is not a convenience: the grain is what makes `/app/transforms/{config_hash}/{source_revision}`
// resolvable at all, and appending would make "which transform is this" unanswerable — the same reason
// `workflow_ir` upserts rather than appends.

// TransformReceiptSource stores and reads transmitted transform receipts.
//
// Separate from `WorkflowIRSource` because a deployment can accept structure and refuse receipts. They
// are different consents, and collapsing them into one mount would make the narrower policy
// unexpressible.
type TransformReceiptSource interface {
	Put(r linkingest.TransformReceipt) error
	Get(tenantID, configHash, sourceRevision string) (linkingest.TransformReceipt, bool, error)
	ListForTenant(tenantID string, limit int) ([]linkingest.TransformReceipt, error)
}

// MountTransformReceipts registers the receipt ingest. Call after New. Optional, like every mount:
// unmounted it answers 503 — "this deployment does not accept transform receipts" is a policy answer a
// customer can read, where a 404 would read as a broken URL.
func (s *Server) MountTransformReceipts(src TransformReceiptSource) {
	s.transformReceipts = src
	s.Mux.HandleFunc("POST /api/v1/transform-receipts", s.handleTransformReceiptIngest)
}

func (s *Server) handleTransformReceiptIngest(w http.ResponseWriter, r *http.Request) {
	if s.transformReceipts == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "this deployment does not accept transform receipts"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "transmitting a transform receipt requires an authenticated tenant"})
		return
	}

	var payload runlink.TransformReceipt
	// 1<<20: this payload is a dozen scalars and two short lists. A generous limit on an endpoint with a
	// fixed shape only buys somebody room to send something else — which on THIS endpoint means a diff.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the transform receipt is not valid: " + err.Error()})
		return
	}
	if payload.ContractVersion != runlink.TransformReceiptContractVersion {
		writeJSON(w, http.StatusUpgradeRequired, map[string]any{
			"error":            "transform-receipt contract mismatch",
			"contract_version": runlink.TransformReceiptContractVersion,
		})
		return
	}
	if payload.ConfigHash == "" || payload.SourceRevision == "" {
		writeJSON(w, http.StatusBadRequest, specError{
			Error: "a transform receipt needs both a config hash and a source revision — they are its identity, " +
				"and a receipt without one cannot be addressed by the surface that renders it",
		})
		return
	}
	for _, o := range payload.NodeOutcomes {
		if !knownOutcome(o.Outcome) {
			writeJSON(w, http.StatusBadRequest, specError{
				Error: "unknown node outcome " + o.Outcome + " (applied, refused)",
			})
			return
		}
		if o.Outcome == runlink.OutcomeRefused && !knownCauseClass(o.Cause) {
			// An unrecognised cause is REFUSED rather than stored. The console branches on this identifier
			// to choose which sentence to render and who it names as the owner; an unknown value would
			// fall through to whatever the default branch is, on a paid surface, permanently.
			writeJSON(w, http.StatusBadRequest, specError{
				Error: "unknown refusal cause " + o.Cause + " — the console renders its own copy from this " +
					"identifier, so a value it does not carry has no sentence to show",
			})
			return
		}
	}
	if payload.FilesChanged < 0 || payload.LinesAdded < 0 || payload.LinesRemoved < 0 {
		writeJSON(w, http.StatusBadRequest, specError{Error: "a diffstat count cannot be negative"})
		return
	}

	// Scope is the AUTHENTICATED tenant, never anything in the body — the payload has no tenant field at
	// all, which is the strongest form of "this cannot widen scope".
	rec := linkingest.TransformReceipt{
		TenantID: principal.TenantID, ConfigHash: payload.ConfigHash,
		SourceRevision: payload.SourceRevision, WorkflowID: payload.WorkflowID,
		ToolVersion: payload.ToolVersion, CoverageVersion: payload.CoverageVersion,
		Status: payload.Status, ReceivedAt: time.Now().UTC(),
		NodeOutcomes: payload.NodeOutcomes,
		FilesChanged: payload.FilesChanged, LinesAdded: payload.LinesAdded, LinesRemoved: payload.LinesRemoved,
	}
	if err := s.transformReceipts.Put(rec); err != nil {
		writeJSON(w, http.StatusBadGateway, specError{Error: "could not store the transform receipt: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"accepted":    true,
		"config_hash": payload.ConfigHash,
		"transform_url": runlink.PlatformBaseURL + "/app/transforms/" +
			url.PathEscape(payload.ConfigHash) + "/" + url.PathEscape(payload.SourceRevision),
	})
}

// knownOutcome reports whether a reported outcome is one of the two the engine can produce.
func knownOutcome(s string) bool {
	for _, v := range runlink.OutcomeStatuses() {
		if s == v {
			return true
		}
	}
	return false
}

// knownCauseClass reports whether a reported refusal cause is one of the three classes.
//
// Spelled from `transform.CauseClasses()` rather than from a literal list, so the boundary check and the
// engine cannot disagree about what a valid cause is — a check with its own copy of a closed set is the
// copy that goes stale.
func knownCauseClass(c string) bool {
	for _, k := range transform.CauseClasses() {
		if string(k) == c {
			return true
		}
	}
	return false
}
