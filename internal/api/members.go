package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/heros-foreal/heros/internal/auth"
	"github.com/heros-foreal/heros/internal/autonomy"
	"github.com/heros-foreal/heros/internal/mailer"
	"github.com/heros-foreal/heros/internal/tenancy"
)

// members.go serves the organization: who is in it, who has been asked, and what each may do.
//
// The capability needed to reach each of these is declared in routes.go and applied at registration. The
// FINER rule — that an admin may not act on an owner, that the last owner cannot be demoted — lives in
// the store, inside the transaction that makes the change, because it depends on rows a handler would
// have to read separately and could then be raced.

type membersResp struct {
	Members []auth.Member `json:"members"`
	// You is the caller's own id, so the console can mark their row and avoid offering them a menu that
	// would lock them out of their own organization.
	You string `json:"you"`
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	members, err := s.Auth.ListMembers(r.Context(), p.Tenant)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, membersResp{Members: members, You: p.UserID})
}

type inviteReq struct {
	Email string       `json:"email"`
	Role  tenancy.Role `json:"role"`
}

// handleInvite creates an invitation and mails it.
//
// # 🔴 Why a send failure fails the whole request
//
// The invitation is useless without the mail: the token is returned once, is never stored in a readable
// form, and is not recoverable. Recording the row and reporting success would leave the inviter believing
// somebody had been invited, and that person waiting for a mail that was never sent — the failure would
// surface days later as "did you get my invite?".
//
// So a send failure withdraws the invitation and says what went wrong. Contrast the password-reset path,
// which deliberately cannot report anything, for a reason set out there.
func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	var req inviteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "unreadable request")
		return
	}
	if !tenancy.ValidRole(string(req.Role)) {
		badRequest(w, "Choose a role: admin, member, or viewer.")
		return
	}
	if err := s.mailAvailable(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}

	token, inv, err := s.Auth.CreateInvitation(r.Context(), p, req.Email, req.Role)
	if err != nil {
		writeAuthErr(w, err)
		return
	}
	org, _ := s.Auth.OrgName(r.Context(), p.Tenant)
	msg := mailer.Invitation(inv.Email, orDefault(org, p.Tenant), p.Subject,
		s.Links.Invitation(token), auth.InvitationLifetime)
	if err := s.Mail.Send(r.Context(), msg); err != nil {
		// 🔴 Withdraw what cannot be delivered, so the console does not list an invitation whose only
		// copy of the token has been discarded. Best-effort: if the withdrawal also fails the row is
		// harmless — it expires, and re-inviting replaces it.
		_ = s.Auth.RevokeInvitation(r.Context(), p.Tenant, inv.ID)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "The invitation could not be sent, so it has not been created: " + err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitation": inv})
}

type setRoleReq struct {
	UserID string       `json:"user_id"`
	Role   tenancy.Role `json:"role"`
}

func (s *Server) handleSetRole(w http.ResponseWriter, r *http.Request) {
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	var req setRoleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "unreadable request")
		return
	}
	if err := s.Auth.SetRole(r.Context(), p, req.UserID, req.Role); err != nil {
		writeAuthErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "role changed"})
}

type removeReq struct {
	UserID string `json:"user_id"`
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	var req removeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "unreadable request")
		return
	}
	if err := s.Auth.RemoveMember(r.Context(), p, req.UserID); err != nil {
		writeAuthErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Server) handleListInvitations(w http.ResponseWriter, r *http.Request) {
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	invs, err := s.Auth.ListInvitations(r.Context(), p.Tenant)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": invs})
}

type revokeReq struct {
	ID string `json:"id"`
}

func (s *Server) handleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	var req revokeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "unreadable request")
		return
	}
	if err := s.Auth.RevokeInvitation(r.Context(), p.Tenant, req.ID); err != nil {
		writeAuthErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "withdrawn"})
}

// ── shared replies ───────────────────────────────────────────────────────────────────────────────

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
}

// writeAuthErr maps a store error to a status code.
//
// 🔴 A table over sentinel errors, not string matching on the message. Matching on prose means changing
// an error message silently changes a status code, and the direction that breaks is a refusal that
// starts answering 500 — which reads as "the server is broken" rather than "you may not do that", and
// sends somebody to the wrong logs.
func writeAuthErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrRefused):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, auth.ErrLastOwner):
		// 409: the request is well-formed and permitted, and conflicts with the state of the
		// organization. A 403 would suggest asking somebody with more authority, and there is nobody with
		// more authority than the last owner.
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, auth.ErrNoSuchMember):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, auth.ErrAlreadyMember), errors.Is(err, auth.ErrEmailTaken),
		errors.Is(err, auth.ErrAlreadyVerified):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, auth.ErrBadRole):
		badRequest(w, err.Error())
	case errors.Is(err, auth.ErrWeakPassword):
		badRequest(w, err.Error())
	case errors.Is(err, auth.ErrBadToken):
		// 410 Gone, not 404: the link WAS real and is finished with. It tells the person to ask for a new
		// one, which is the actual next step.
		writeJSON(w, http.StatusGone, map[string]string{"error": err.Error()})
	case errors.Is(err, mailer.ErrNotConfigured):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case errors.Is(err, auth.ErrBusy):
		// 🔴 Never folded into a 4xx. This is the server saying it is overloaded, on a path — accepting an
		// invitation, choosing a new password — where a 4xx would read as "your link is no longer valid"
		// and send somebody to ask for another one they do not need.
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "The server is busy and could not process that just now. Your link is still valid — " +
				"try again in a few seconds.",
		})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// ── autonomy ─────────────────────────────────────────────────────────────────────────────────────

type autonomyLevelOut struct {
	Level     string `json:"level"`
	Describes string `json:"describes"`
}

type autonomyResp struct {
	Level string `json:"level"`
	// Describes says in a sentence what the current level means, so the console does not carry a second
	// copy of the explanation that would drift from the one the worker records.
	Describes string `json:"describes"`
	// Choices is every level with its description, for rendering the menu.
	Choices []autonomyLevelOut `json:"choices"`
	// MayChange tells the console whether to render the control at all. The server refuses either way;
	// this only decides what is worth showing.
	MayChange bool `json:"may_change"`
}

func (s *Server) handleGetAutonomy(w http.ResponseWriter, r *http.Request) {
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	level, err := s.Auth.AutonomyFor(p.Tenant)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := autonomyResp{
		Level: string(level), Describes: autonomy.Describe(level),
		MayChange: p.May(tenancy.SetAutonomy),
	}
	for _, l := range autonomy.Levels {
		out.Choices = append(out.Choices, autonomyLevelOut{Level: string(l), Describes: autonomy.Describe(l)})
	}
	writeJSON(w, http.StatusOK, out)
}

type setAutonomyReq struct {
	Level string `json:"level"`
}

func (s *Server) handleSetAutonomy(w http.ResponseWriter, r *http.Request) {
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	var req setAutonomyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "unreadable request")
		return
	}
	if !autonomy.Valid(req.Level) {
		badRequest(w, "Choose a level: supervised, assisted, or autonomous.")
		return
	}
	if err := s.Auth.SetAutonomy(r.Context(), p.Tenant, autonomy.Level(req.Level)); err != nil {
		writeAuthErr(w, err)
		return
	}
	// 🔴 Logged, at the level of a real event. Widening what a run may do without a person is the single
	// most consequential setting in the product, and "when did this change, and who changed it" must be
	// answerable from the record rather than from somebody's memory.
	log.Printf("autonomy.changed tenant=%s by=%s level=%s", p.Tenant, p.Subject, req.Level)
	writeJSON(w, http.StatusOK, map[string]string{
		"level": req.Level, "describes": autonomy.Describe(autonomy.Level(req.Level)),
	})
}
