package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// localpairing.go is Mode 3's console↔agent pairing (P32 §4).
//
// # Three routes, and why the middle one is different from the other two
//
//	POST /api/v1/local-pairings        the CONSOLE asks for a code, on behalf of a signed-in person
//	POST /api/v1/local-pairing-claims  the AGENT claims it, from the customer's own machine
//	GET  /api/v1/local-pairings        the CONSOLE polls, and lists what is paired
//
// The claim is the only one reached from outside the cluster, and it is the only one that names no
// tenant: the agent authenticates with the person's own credential and the CODE identifies the pairing.
// Asking the agent to also name a tenant would be a field it could get wrong, and a field a caller can
// name is a field a caller can change.
//
// # 🚫 What no route here can carry
//
// A path, a file, or anything from the tree. The claim payload has three fields — the code, the machine
// name and a revision id — and `DisallowUnknownFields` refuses a fourth. That refusal is a privacy
// control rather than hygiene: the field somebody would add here is `repo_path`, and a local filesystem
// path is the customer's own layout arriving on our side for no reason anybody could name.

// PairingSource is the P32 §4 dependency.
type PairingSource interface {
	Start(ctx context.Context, tenantID, workflowID string) (sourceingest.Pairing, error)
	Claim(ctx context.Context, userCode, machineName, revision string) (sourceingest.Pairing, error)
	List(ctx context.Context, tenantID string) ([]sourceingest.Pairing, error)
}

// PairingView is one pairing as the console renders it.
type PairingView struct {
	PairingID  string                    `json:"pairing_id"`
	WorkflowID string                    `json:"workflow_id"`
	State      sourceingest.PairingState `json:"state"`
	// UserCode is the code to type. Served on the START response and on a PENDING row only — see
	// pairingViewOf: a claimed pairing's code is spent, and continuing to render it invites somebody
	// to type it again and be told it does not exist.
	UserCode string `json:"user_code,omitempty"`
	// MachineName is what the agent called itself. Customer-supplied text; React escapes it and
	// nothing interpolates it.
	MachineName string `json:"machine_name,omitempty"`
	Revision    string `json:"revision,omitempty"`
	CreatedAt   int64  `json:"created_at_ms"`
	ClaimedAt   int64  `json:"claimed_at_ms,omitempty"`
	ExpiresAt   int64  `json:"expires_at_ms"`
}

// LocalPairingsView is the local-mode surface's read model.
type LocalPairingsView struct {
	Pairings []PairingView `json:"pairings"`
	// Availability states which deployments the local mode works against, and whether THIS one is
	// among them (FR15). Served with the list so the console can state the limit BEFORE the flow
	// starts rather than failing at its last step.
	Availability sourceingest.LocalModeAvailability `json:"availability"`
	// Command is the exact command to run, with nothing left for the reader to substitute. It is
	// built here rather than in the console for FR5's reason, applied to Mode 3: a command a person
	// has to edit before it works is a command that will be run wrong.
	Command string `json:"command"`
}

// MountLocalPairing registers the pairing routes. Optional, like every other mount.
func (s *Server) MountLocalPairing(src PairingSource) {
	s.pairings = src
	s.Mux.HandleFunc("POST /api/v1/local-pairings", s.handlePairingStart)
	s.Mux.HandleFunc("GET /api/v1/local-pairings", s.handlePairingList)
	s.Mux.HandleFunc("POST /api/v1/local-pairing-claims", s.handlePairingClaim)
}

// ThisDeploymentURL is the public address this deployment answers on, or "" when it has not been told.
//
// 🔴 "" is reported as UNAVAILABLE-with-a-reason rather than assumed to match. A deployment that does
// not know its own address cannot tell whether the local bridge can reach it, and guessing yes is how
// the flow fails at its last step — which is precisely what FR15 exists to prevent.
func (s *Server) SetDeploymentURL(u string) { s.deploymentURL = u }

func (s *Server) pairingPrincipal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	if s.pairings == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{
			Error: "this deployment does not offer the local-repository bridge",
		})
		return auth.Principal{}, false
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "pairing requires an authenticated tenant"})
		return auth.Principal{}, false
	}
	return principal, true
}

type pairingStartPayload struct {
	WorkflowID string `json:"workflow_id"`
}

func (s *Server) handlePairingStart(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.pairingPrincipal(w, r)
	if !ok {
		return
	}
	// 🔴 Availability is checked BEFORE a code is issued. Handing out a code on a deployment the
	// bridge cannot reach is the "fails at the end of the flow" outcome FR15 names — the person would
	// type it, wait, and be told nothing happened.
	avail := sourceingest.Availability(runlink.PlatformBaseURL, s.deploymentURL)
	if !avail.Available {
		writeJSON(w, http.StatusConflict, specError{Error: avail.Why})
		return
	}
	var p pairingStartPayload
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the pairing request could not be read"})
		return
	}
	pair, err := s.pairings.Start(r.Context(), principal.TenantID, p.WorkflowID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, pairingViewOf(pair))
}

func (s *Server) handlePairingList(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.pairingPrincipal(w, r)
	if !ok {
		return
	}
	pairs, err := s.pairings.List(r.Context(), principal.TenantID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, specError{Error: "the pairings could not be read"})
		return
	}
	view := LocalPairingsView{
		Pairings:     make([]PairingView, 0, len(pairs)),
		Availability: sourceingest.Availability(runlink.PlatformBaseURL, s.deploymentURL),
		Command:      "heros pair --code <the code above> --repo .",
	}
	for _, p := range pairs {
		view.Pairings = append(view.Pairings, pairingViewOf(p))
	}
	writeJSON(w, http.StatusOK, view)
}

// pairingClaimPayload is the agent's claim.
//
// 🚫 Three fields, and `DisallowUnknownFields` refuses a fourth. The field somebody would add is
// `repo_path`.
type pairingClaimPayload struct {
	UserCode string `json:"user_code"`
	// MachineName is what the agent calls itself, so the console can say WHICH machine.
	MachineName string `json:"machine_name"`
	// Revision is a commit id — never the code at it.
	Revision string `json:"revision,omitempty"`
}

func (s *Server) handlePairingClaim(w http.ResponseWriter, r *http.Request) {
	// The claim is authenticated but NOT tenant-scoped by the handler: the code resolves the pairing,
	// and the tenant on it is the one the console created it under. See this file's header.
	if s.pairings == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{
			Error: "this deployment does not offer the local-repository bridge",
		})
		return
	}
	if _, ok := auth.PrincipalFrom(r.Context()); !ok {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "claiming a pairing requires an authenticated credential — run `heros login` first"})
		return
	}
	var p pairingClaimPayload
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{
			Error: "the claim could not be read — it carries a field outside the contract, or is malformed",
		})
		return
	}
	pair, err := s.pairings.Claim(r.Context(), p.UserCode, p.MachineName, p.Revision)
	switch {
	case errors.Is(err, sourceingest.ErrPairingExpired):
		// 410, and DISTINCT from the 404 below. "Your code expired, start a new one" and "no such
		// code" send a person to two different places, and only one of them is where the problem is.
		writeJSON(w, http.StatusGone, specError{Error: err.Error()})
	case errors.Is(err, sourceingest.ErrNoPairing):
		writeJSON(w, http.StatusNotFound, specError{Error: "that pairing code is not waiting to be claimed — check it, or start a new one from the console"})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, specError{Error: err.Error()})
	default:
		writeJSON(w, http.StatusOK, pairingViewOf(pair))
	}
}

// pairingViewOf renders a pairing, withholding a spent code.
func pairingViewOf(p sourceingest.Pairing) PairingView {
	v := PairingView{
		PairingID: p.PairingID, WorkflowID: p.WorkflowID, State: p.State,
		MachineName: p.MachineName, Revision: p.Revision,
		CreatedAt: p.CreatedAtMS, ClaimedAt: p.ClaimedAtMS, ExpiresAt: p.ExpiresAtMS,
	}
	// 🔴 The code is served only while it can still be used. A claimed or expired pairing that still
	// renders its code invites somebody to type it and be told it does not exist — and it keeps a
	// single-use secret on a screen after its single use.
	if p.State == sourceingest.PairingPending {
		v.UserCode = p.UserCode
	}
	return v
}
