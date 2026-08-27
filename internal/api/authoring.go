package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/errorcode"
	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/telemetry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// The AUTHORING surface (P13 13c task 9.8).
//
// Three routes — preflight, submit, revert — and one discipline: SIX failure classes stay six.
//
//	503 not mounted        the subsystem is absent from this deployment
//	401 not authenticated  no principal
//	402 not entitled       the plan does not carry authoring          → the remedy is a purchase
//	403 not permitted      this identity may not author               → the remedy is an administrator
//	404 not found          no such workflow / change
//	409 conflict           the parent advanced                        → the remedy is to rebase the edit
//	200 refused            the engine said no, by name                → NOT an error: a verdict
//	502 upstream failure   we could not reach something we needed
//
// The one that matters most is the last pair. A REFUSAL IS NOT AN ERROR. It is a verdict the user asked
// for and is entitled to read, and returning it as a 4xx/5xx would make every client's error handler
// swallow the named cause into a generic "something went wrong" — which is precisely the outcome
// preflight exists to prevent. So a refused preflight is a 200 carrying `verdict: "refused"`.

// AuthoringSource is the authoring surface this server mounts. It is an interface for the reason
// every other mount is: the server needs the ANSWERS, not the packages that compute them.
type AuthoringSource interface {
	// Preflight evaluates a draft without spending anything.
	Preflight(ctx context.Context, d authoring.Draft) (authoring.Result, error)
	// Submit turns an admissible draft into a recorded, compiled authored change.
	Submit(ctx context.Context, d authoring.Draft) (authoring.Submission, error)
	// Revert undoes an authored change by re-deriving its recorded parent.
	Revert(ctx context.Context, changeID string, actor authoring.Actor) (authoring.Reversal, error)
	// History returns one change's append-only rows, in order.
	History(ctx context.Context, tenantID, changeID string) ([]authoring.Entry, error)
	// Parent returns the spec a draft is edited against, so a client need not carry it.
	Parent(ctx context.Context, workflowID, variantID string) (*variantspec.VariantSpec, error)
}

// AuthoringPreflightView is the console's read model for a preflight verdict.
//
// It carries all three verdicts as ONE closed field rather than a boolean plus an optional reason,
// because a boolean forces the surface to collapse "we refuse this" and "we have not measured this"
// into one disabled control — and those two lead a user to opposite actions.
type AuthoringPreflightView struct {
	Verdict string `json:"verdict"` // "admissible" | "refused" | "not_yet_measurable"
	// Cause / NodeID / Field / Shape are populated on a refusal, rendered verbatim from the engine.
	Cause  string `json:"cause,omitempty"`
	NodeID string `json:"node_id,omitempty"`
	Field  string `json:"field,omitempty"`
	Shape  string `json:"shape,omitempty"`
	// MissingKind / MissingSubject are populated on not_yet_measurable, and name what would resolve it.
	MissingKind    string `json:"missing_kind,omitempty"`
	MissingSubject string `json:"missing_subject,omitempty"`
	// ConfigHash is the hash the change WOULD have, on an admissible verdict.
	ConfigHash string   `json:"config_hash,omitempty"`
	Dimensions []string `json:"dimensions,omitempty"`
	Nodes      []string `json:"nodes,omitempty"`
	// Adapters are the adapter nodes an ordering change would insert, shown BEFORE submission so the
	// author agrees to them with their eyes open — never a component that appears only in the diff.
	Adapters []string `json:"adapters,omitempty"`
}

// AuthoringChangeView is the read model for a submitted authored change.
type AuthoringChangeView struct {
	ChangeID   string `json:"change_id"`
	ConfigHash string `json:"config_hash"`
	// VerificationState is "unverified" until the harness runs. It travels with the change everywhere,
	// because a change displayed beside a verified delta without it reads as one.
	VerificationState string `json:"verification_state"`
	Origin            string `json:"origin"`
	Axis              string `json:"axis"`
	DiffRef           string `json:"diff_ref,omitempty"`
	ForkedFrom        string `json:"forked_from_proposal,omitempty"`
	ActorID           string `json:"actor_id,omitempty"`
}

// MountAuthoring registers the authoring routes. Optional, like every other mount.
func (s *Server) MountAuthoring(src AuthoringSource) {
	s.authoring = src
	s.Mux.HandleFunc("POST /api/v1/authoring/preflight", s.handleAuthoringPreflight)
	s.Mux.HandleFunc("POST /api/v1/authoring/submit", s.handleAuthoringSubmit)
	s.Mux.HandleFunc("POST /api/v1/authoring/revert", s.handleAuthoringRevert)
	s.Mux.HandleFunc("GET /api/v1/authoring/history", s.handleAuthoringHistory)
}

// authoringActor resolves the acting identity SERVER-SIDE. Request scope never comes from a
// client-supplied tenant id — the browser holds a session, not a claim about who it is.
func (s *Server) authoringActor(w http.ResponseWriter, r *http.Request) (authoring.Actor, bool) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{Error: "authoring requires an authenticated tenant"})
		return authoring.Actor{}, false
	}
	// APIKeyID is the identity label the platform already resolved for this credential. It is what
	// an audit row can be traced back to; falling back to the tenant would make every change in a
	// tenant look like one author.
	id := principal.APIKeyID
	if id == "" {
		id = principal.TenantID + "/" + principal.Role
	}
	return authoring.Actor{ID: id, TenantID: principal.TenantID}, true
}

func (s *Server) authoringMounted(w http.ResponseWriter) bool {
	if s.authoring == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{Error: "authoring is not mounted on this server"})
		return false
	}
	return true
}

// authoringDraftRequest is the wire shape of a draft. The actor is deliberately ABSENT: it is resolved
// from the session, so a client cannot author as somebody else by saying so.
type authoringDraftRequest struct {
	WorkflowID         string                    `json:"workflow_id"`
	ParentVariantID    string                    `json:"parent_variant_id"`
	ConcurrencyToken   string                    `json:"concurrency_token"`
	ForkedFromProposal string                    `json:"forked_from_proposal,omitempty"`
	Edits              map[string]authoring.Edit `json:"edits"`
}

func (s *Server) readDraft(w http.ResponseWriter, r *http.Request, actor authoring.Actor) (authoring.Draft, bool) {
	var req authoringDraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "malformed draft: " + err.Error()})
		return authoring.Draft{}, false
	}
	if req.WorkflowID == "" || req.ParentVariantID == "" {
		writeJSON(w, http.StatusBadRequest,
			specError{Error: "a draft must name the workflow and the parent variant it edits"})
		return authoring.Draft{}, false
	}
	return authoring.Draft{
		WorkflowID: req.WorkflowID, ParentVariantID: req.ParentVariantID,
		ConcurrencyToken: req.ConcurrencyToken, ForkedFromProposal: req.ForkedFromProposal,
		Edits: req.Edits, Actor: actor,
	}, true
}

func (s *Server) handleAuthoringPreflight(w http.ResponseWriter, r *http.Request) {
	if !s.authoringMounted(w) {
		return
	}
	actor, ok := s.authoringActor(w, r)
	if !ok {
		return
	}
	draft, ok := s.readDraft(w, r, actor)
	if !ok {
		return
	}
	res, err := s.authoring.Preflight(r.Context(), draft)
	if err != nil {
		s.writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preflightView(res))
}

func (s *Server) handleAuthoringSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.authoringMounted(w) {
		return
	}
	actor, ok := s.authoringActor(w, r)
	if !ok {
		return
	}
	draft, ok := s.readDraft(w, r, actor)
	if !ok {
		return
	}
	sub, err := s.authoring.Submit(r.Context(), draft)
	if err != nil {
		// 🔴 P37 §5.6 — the refusal is COUNTED, with the cause as an attribute. One name for every
		// refusal reason (`ConversationRefused`'s argument): a separate name per cause would make "how
		// often does this surface say no" a question requiring the operator to know the list in advance.
		axisSaveEvent(r, eventname.ConsoleAxisSaveRefused, slog.LevelWarn,
			"an axis change was refused at save",
			"workflow_id", draft.WorkflowID, "cause", err.Error(),
			"error_code", errorcode.RequestInvalid.String())
		s.writeAuthoringError(w, err)
		return
	}
	// 🔴 Emitted AFTER the write returns, never before. An event on the way in counts intent rather than
	// effect, and a 200 is not evidence of a write (P37 §9.3, §6.6 proves the row exists).
	axisSaveEvent(r, eventname.ConsoleAxisSaved, slog.LevelInfo, "an axis change was written",
		"workflow_id", draft.WorkflowID, "change_id", sub.ChangeID, "config_hash", sub.ConfigHash,
		"axis", sub.Entry.Axis, "verification_state", string(sub.Entry.VerificationState))
	writeJSON(w, http.StatusOK, AuthoringChangeView{
		ChangeID: sub.ChangeID, ConfigHash: sub.ConfigHash,
		VerificationState: string(sub.Entry.VerificationState),
		Origin:            sub.Entry.Origin, Axis: sub.Entry.Axis,
		DiffRef: sub.Entry.DiffRef, ForkedFrom: sub.Entry.ForkedFromProposal,
		ActorID: sub.Entry.ActorID,
	})
}

func (s *Server) handleAuthoringRevert(w http.ResponseWriter, r *http.Request) {
	if !s.authoringMounted(w) {
		return
	}
	actor, ok := s.authoringActor(w, r)
	if !ok {
		return
	}
	var req struct {
		ChangeID string `json:"change_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChangeID == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "revert requires a change_id"})
		return
	}
	rev, err := s.authoring.Revert(r.Context(), req.ChangeID, actor)
	if err != nil {
		s.writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, AuthoringChangeView{
		ChangeID: rev.ChangeID, ConfigHash: rev.ConfigHash,
		VerificationState: string(rev.Entry.VerificationState),
		Origin:            rev.Entry.Origin, Axis: rev.Entry.Axis, ActorID: rev.Entry.ActorID,
	})
}

func (s *Server) handleAuthoringHistory(w http.ResponseWriter, r *http.Request) {
	if !s.authoringMounted(w) {
		return
	}
	actor, ok := s.authoringActor(w, r)
	if !ok {
		return
	}
	changeID := r.URL.Query().Get("change_id")
	if changeID == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "history requires a change_id"})
		return
	}
	entries, err := s.authoring.History(r.Context(), actor.TenantID, changeID)
	if err != nil {
		s.writeAuthoringError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// writeAuthoringError keeps the failure classes distinguishable (NFR18).
//
// 🚫 It deliberately does NOT have a default that maps everything unrecognised onto 500 with a generic
// message. An unmapped error keeps its own sentence, because "something went wrong" is the message that
// costs a user the most time.
func (s *Server) writeAuthoringError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, authoring.ErrNotEntitled):
		// 402: the remedy is a plan change, not an administrator. Kept distinct from 403 for that reason.
		writeJSON(w, http.StatusPaymentRequired, specError{Error: err.Error()})
	case errors.Is(err, authoring.ErrNotPermitted):
		writeJSON(w, http.StatusForbidden, specError{Error: err.Error()})
	case errors.Is(err, authoring.ErrStaleDraft):
		// 409: the parent advanced. The body carries which parent, so the client can rebase rather than
		// guess. 🚫 Never resolved by overwriting.
		writeJSON(w, http.StatusConflict, specError{Error: err.Error()})
	case errors.Is(err, authoring.ErrNothingToRevert):
		writeJSON(w, http.StatusNotFound, specError{Error: err.Error()})
	case errors.Is(err, authoring.ErrNotAdmissible):
		// A submit past a refusal. 422 rather than 400: the request was well-formed, the change was not
		// admissible — and the two are different mistakes.
		writeJSON(w, http.StatusUnprocessableEntity, specError{Error: err.Error()})
	case errors.Is(err, authoring.ErrRecordUnreachable):
		writeJSON(w, http.StatusBadGateway, specError{Error: err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, specError{Error: err.Error()})
	}
}

func preflightView(r authoring.Result) AuthoringPreflightView {
	return AuthoringPreflightView{
		Verdict: string(r.Verdict),
		Cause:   r.Refusal.Cause, NodeID: r.Refusal.NodeID, Field: r.Refusal.Field, Shape: r.Refusal.Shape,
		MissingKind: r.Missing.Kind, MissingSubject: r.Missing.Subject,
		ConfigHash: r.ConfigHash, Dimensions: r.Dimensions, Nodes: r.Nodes, Adapters: r.Adapters,
	}
}

// axisSaveEvent records one save-path event with the three correlation identities P37 §5.5 requires.
//
// # Why a helper rather than two call sites
//
// The three ids are the whole point, and the failure they prevent is an operator holding a `change_id`
// with no way to reach the request that produced it. Two hand-written call sites is one chance to omit
// one, and an omission is invisible: the line still logs, still reads correctly, and only fails when
// somebody needs it during an incident.
//
// 🔴 The event NAME comes from the central enum, never from a literal. `internal/eventname`'s header
// states the reason: an invented name is a free-text field on the far side of a boundary.
func axisSaveEvent(r *http.Request, name eventname.Name, level slog.Level, msg string, attrs ...any) {
	traceID := traceIDFor(r)
	base := []any{
		"event", name.String(),
		"request_id", telemetry.TraceIDFromContext(r.Context()),
		"trace_id", traceID,
		"span_id", telemetry.RequestSpanID(traceID),
	}
	slog.Default().Log(r.Context(), level, msg, append(base, attrs...)...)
}
