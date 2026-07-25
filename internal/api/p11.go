package api

import (
	"encoding/json"
	"errors"
	"net/http"

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

// LinkIngestSource is the P11 ingest dependency: it ingests a payload for a tenant and answers coverage.
type LinkIngestSource interface {
	Ingest(tenantID string, p runlink.Payload) (linkingest.Result, error)
	Coverage(tenantID string) linkingest.LinkCoverage
}

// linkCoverageFor maps the ingest store's coverage into the billing view shape. It is how the P7 billing
// read model learns how complete its SUM figure is (FR17) without the metering code depending on the
// linking code — the api layer joins the two read models.
func (s *Server) linkCoverageFor(tenantID string) *LinkCoverageView {
	if s.p11 == nil {
		return nil // coverage UNKNOWN — the console renders that distinctly, never as complete
	}
	c := s.p11.Coverage(tenantID)
	return &LinkCoverageView{
		RunsLinked: c.RunsLinked, RunsReported: c.RunsReported, Known: c.Known, Complete: c.Complete,
	}
}

// MountP11 registers the run-linking ingest routes. Call after New. Optional, like every other mount.
func (s *Server) MountP11(src LinkIngestSource) {
	s.p11 = src
	s.Mux.HandleFunc("POST /api/p11/link", s.handleP11Link)
	s.Mux.HandleFunc("GET /api/p11/whoami", s.handleP11WhoAmI)
}

// handleP11WhoAmI answers `login`'s token validation: it returns the identity the authenticated token
// resolves to. Behind auth.Compose, so an unknown token is already a 401 before this runs.
func (s *Server) handleP11WhoAmI(w http.ResponseWriter, r *http.Request) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "authentication required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"identity": principal.TenantID})
}

// handleP11Link ingests one linked run.
func (s *Server) handleP11Link(w http.ResponseWriter, r *http.Request) {
	if s.p11 == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "run-linking ingest is not mounted"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "linking a run requires an authenticated tenant"})
		return
	}

	var payload runlink.Payload
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	dec.DisallowUnknownFields() // a payload with a field outside the wire contract is rejected, not silently accepted
	if err := dec.Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the linked payload is not valid: " + err.Error()})
		return
	}

	// Scope is the authenticated tenant. Any tenant a client might try to smuggle is ignored — the
	// payload has no tenant field, and the ingester takes the identity from here.
	res, err := s.p11.Ingest(principal.TenantID, payload)
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
