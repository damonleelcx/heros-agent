package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/proposalstore"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/verification"
)

// verdictingest.go is the P5.5 verdict ingest — the endpoint the customer's CI reports a measurement to.
//
// # Why the platform cannot produce what this accepts
//
// The verification gate runs the eval harness over a held-out split, which needs the eval cases, the run
// traces and a provider. All three stay in the customer's environment. So a hosted deployment can
// GENERATE a proposal and can never MEASURE one, and this is the only path by which
// `gate_result = 'pass'` becomes true of a stored verdict without the platform inventing it.
//
// 🔴 That makes this the most consequential write surface on the platform. A passing verdict is what
// lets a proposal be recommended AND what lets P12 open a pull request into a customer's repository —
// so an unauthenticated or unscoped write here turns into someone else's merged code. Three things
// stand in the way, and none of them is a check somebody has to remember:
//
//   - the tenant comes from the authenticated principal, never from the body (the payload has no tenant
//     field at all, which is the strongest form of "this cannot widen scope");
//   - proposalstore.PutVerdict verifies OWNERSHIP with a WHERE clause in the same transaction, so a
//     verdict for another tenant's proposal is a zero-row no-op reported as "no such proposal";
//   - `DisallowUnknownFields` rejects a payload carrying a key outside the wire contract, rather than
//     ignoring it — on this endpoint that is a privacy control, not hygiene, because the field a future
//     CLI would add is a case id or a reason.

// VerdictSink records a verdict a customer's CI reported. It is the write half of
// proposalstore.Store, named separately because a deployment that serves the recommendation surface
// read-only should not have to accept writes to get it.
//
// It takes a context, unlike the older ingest interfaces in this package. Those predate the stores
// having one and adapt in internal/launch; proposalstore.PGStore.PutVerdict already has exactly this
// signature, so matching it means the store IS the sink with no adapter in between — and an adapter is
// a place for a scope argument to be rewritten on its way through.
type VerdictSink interface {
	PutVerdict(ctx context.Context, tenantID string, v verification.Verdict) error
}

// MountVerdictIngest registers the verdict ingest route. Call after New. Optional, like every mount:
// unmounted it answers 503, which says "this deployment does not accept reported verdicts" rather than
// "that proposal does not exist".
func (s *Server) MountVerdictIngest(sink VerdictSink) {
	s.verdicts = sink
	s.Mux.HandleFunc("POST /api/v1/proposals/{proposal_id}/verdict", s.handleVerdictIngest)
}

func (s *Server) handleVerdictIngest(w http.ResponseWriter, r *http.Request) {
	if s.verdicts == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "this deployment does not accept reported verdicts"})
		return
	}
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "reporting a verdict requires an authenticated tenant"})
		return
	}

	var payload runlink.VerdictPayload
	// 1<<20 rather than the run link's 4<<20: this payload is sixteen scalars and has no list in it. A
	// generous limit on an endpoint with a fixed shape only buys somebody room to send something else.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the reported verdict is not valid: " + err.Error()})
		return
	}
	if payload.ContractVersion != runlink.VerdictContractVersion {
		writeJSON(w, http.StatusUpgradeRequired, map[string]any{
			"error":            "verdict contract mismatch",
			"contract_version": runlink.VerdictContractVersion,
		})
		return
	}
	// The path names the proposal and so does the body. They must agree: a mismatch means one of them
	// describes a different proposal, and choosing either would attach a measurement to the wrong change.
	if id := r.PathValue("proposal_id"); id != payload.ProposalID {
		writeJSON(w, http.StatusBadRequest, specError{
			Error: "the proposal id in the path and in the payload disagree — refusing rather than choosing one",
		})
		return
	}
	if payload.ProposalID == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "a verdict must name the proposal it measured"})
		return
	}
	if !knownGateResult(payload.GateResult) {
		// An unrecognised gate result is REFUSED rather than stored. verification.Verdict.Passed() tests
		// for `pass` exactly, so an unknown value would be stored as a permanent non-pass and read as
		// "measured and rejected" — which is a different thing from "we could not understand the report".
		writeJSON(w, http.StatusBadRequest, specError{
			Error: "unknown gate_result " + payload.GateResult +
				" (pass, fail_significance, fail_regression, fail_constraint, unrun)",
		})
		return
	}
	if payload.CasesFixedCount < 0 || payload.CasesBrokenCount < 0 {
		writeJSON(w, http.StatusBadRequest, specError{Error: "a case count cannot be negative"})
		return
	}

	// Built field by field, exactly as the egress side is. Note what is NOT set: CasesFixed and
	// CasesBroken stay nil because ids do not cross, and Reason stays empty because free text does not —
	// verification.Narrate says so where a reader will see it rather than leaving a sentence half
	// finished. Both absences are the contract, not omissions.
	v := verification.Verdict{
		ProposalID:     payload.ProposalID,
		ConfigHash:     payload.ConfigHash,
		DiffHash:       payload.DiffHash,
		Metric:         payload.Metric,
		Delta:          evalstats.Interval{Mean: payload.Delta, Low: payload.CILow, High: payload.CIHigh},
		Significant:    payload.Significant,
		HeldOut:        payload.HeldOut,
		CostDelta:      payload.CostDelta,
		LatencyDelta:   payload.LatencyDelta,
		RegressionPass: payload.RegressionPass,
		GateResult:     verification.GateResult(payload.GateResult),

		CasesFixedCount:  payload.CasesFixedCount,
		CasesBrokenCount: payload.CasesBrokenCount,
	}

	if err := s.verdicts.PutVerdict(r.Context(), principal.TenantID, v); err != nil {
		if errors.Is(err, proposalstore.ErrNotFound) {
			// Deliberately the same answer for "no such proposal" and "not yours". Distinguishing them
			// would tell an authenticated caller that a proposal id exists under some other tenant, which
			// is the one bit of information the ownership check is protecting.
			writeJSON(w, http.StatusNotFound, specError{Error: "no such proposal"})
			return
		}
		writeJSON(w, http.StatusBadGateway, specError{Error: "could not record the verdict: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"accepted":         true,
		"proposal_id":      payload.ProposalID,
		"gate_result":      payload.GateResult,
		"contract_version": runlink.VerdictContractVersion,
	})
}

// knownGateResult reports whether a reported value is one of the five the gate can produce. A closed
// set, checked at the boundary: everything downstream treats gate_result as a decided verdict.
func knownGateResult(s string) bool {
	switch verification.GateResult(s) {
	case verification.GatePass, verification.GateFailSig, verification.GateFailRegress,
		verification.GateFailConstrain, verification.GateUnrun:
		return true
	}
	return false
}
