package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/heros-foreal/heros/internal/auth"
	"github.com/heros-foreal/heros/internal/mailer"
	"github.com/heros-foreal/heros/internal/tenancy"
)

// account.go serves the four things somebody does about their own account, three of them without being
// able to sign in: forgetting a password, choosing a new one, accepting an invitation, and confirming an
// address.

// mailReady reports whether this deployment can actually send.
func (s *Server) mailReady() bool {
	if s.Mail == nil {
		return false
	}
	_, off := s.Mail.(mailer.Unconfigured)
	return !off && s.Links.Origin() != ""
}

// mailAvailable is the check made BEFORE an operation that needs mail does anything else.
//
// 🔴 Checked up front rather than at the send. Creating an invitation and then discovering there is no
// way to deliver it means unwinding a write; refusing first means the customer is told the truth about
// the deployment before anything happens.
func (s *Server) mailAvailable() error {
	if !s.mailReady() {
		return fmt.Errorf("this deployment cannot send mail, so invitations, password resets and " +
			"address confirmations are unavailable. An operator can set HEROS_SMTP_HOST, " +
			"HEROS_SMTP_USERNAME, HEROS_SMTP_PASSWORD, HEROS_MAIL_FROM and HEROS_PUBLIC_URL and restart")
	}
	return nil
}

// ── forgetting a password ────────────────────────────────────────────────────────────────────────

type forgotReq struct {
	Tenant string `json:"tenant"`
	Email  string `json:"email"`
}

// handleForgotPassword issues a reset link.
//
// # 🔴 Why the answer is the same whether or not the account exists
//
// "I forgot my password" is a request anybody can make about anybody's address, without a credential and
// without a rate limit that would help. If the reply differs — a different message, a different status,
// or merely a different amount of TIME — then this endpoint answers the question "does this person have
// an account with you", for any address, at whatever rate the network allows. For a product that reads
// customers' private source code, the customer list is itself worth having.
//
// So the body is a constant, and the mail is sent on a background goroutine so that the response time
// carries no signal either: the request returns before an SMTP conversation could have finished.
//
// # 🔴 Why a send failure is not reported to the requester
//
// It cannot be, without answering the question the constant reply exists to refuse. The failure is
// logged, at WARN, with the recipient and the reason and never the token — the operator can see it, and
// the person asking cannot use it to learn anything. This is the opposite choice from an invitation,
// where the requester is inside the organization and there is no secret to keep.
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "unreadable request")
		return
	}
	if req.Tenant == "" {
		req.Tenant = s.DefaultTenant
	}
	// The constant answer, decided before anything is looked up so no branch can forget to give it.
	const answer = "If that address has an account here, a link to choose a new password is on its way. " +
		"It works once and expires in an hour."

	if s.mailReady() {
		token, to, org, err := s.Auth.CreatePasswordReset(r.Context(), req.Tenant, req.Email)
		switch {
		case err == nil:
			s.sendInBackground("password reset", to,
				mailer.ResetPassword(to, orDefault(org, req.Tenant), s.Links.Reset(token),
					auth.PasswordResetLifetime))
		case errors.Is(err, auth.ErrNoSuchUser):
			// Nothing to send. Deliberately silent — not even a log line naming the address, which would
			// build the list this endpoint refuses to give out.
		default:
			log.Printf("WARN auth.reset.create_failed tenant=%s: %v", req.Tenant, err)
		}
	} else {
		log.Printf("WARN auth.reset.mail_unconfigured tenant=%s — a password reset was requested and "+
			"this deployment cannot send mail", req.Tenant)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": answer})
}

// sendInBackground sends without the caller waiting, and without the request's context.
//
// 🔴 NOT r.Context(). That context is cancelled the moment the response is written, so a send started
// with it would be aborted a few microseconds later — the mail would never leave, the log would show a
// cancellation, and the endpoint would report success. Its own timeout, on its own deadline.
func (s *Server) sendInBackground(what, to string, m mailer.Message) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := s.Mail.Send(ctx, m); err != nil {
			// Recipient and reason, never the token — a log line carrying a reset link is a reset link
			// that has leaked to everybody who can read logs.
			log.Printf("WARN mail.send_failed purpose=%q to=%s: %v", what, to, err)
		}
	}()
}

type resetReq struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "unreadable request")
		return
	}
	if err := s.Auth.ResetPassword(r.Context(), req.Token, req.Password); err != nil {
		writeAuthErr(w, err)
		return
	}
	// 🚫 No session is issued here, deliberately. Somebody holding a reset link has proven they can read
	// one mailbox, and signing them straight in would make a link forwarded by accident into a session.
	// They sign in with the password they just chose, which proves they know it.
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "Your password has been changed and every other session has been signed out. " +
			"Sign in with the new password.",
	})
}

// ── invitations, from the other side ─────────────────────────────────────────────────────────────

// handleLookupInvitation renders what somebody is being invited to, before they accept.
func (s *Server) handleLookupInvitation(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		badRequest(w, "That link is missing its token.")
		return
	}
	inv, err := s.Auth.LookupInvitation(r.Context(), token)
	if err != nil {
		writeAuthErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"invitation": inv,
		// The password rule travels with the form, so the console does not carry a second copy of a
		// number the server enforces.
		"min_password_length": auth.MinPasswordLength,
	})
}

type acceptReq struct {
	Token    string `json:"token"`
	Password string `json:"password"`
	// 🚫 There is deliberately no Role field, and no Email field. Both come from the invitation row: a
	// role in this body is a role the recipient picks, and an address in it is an account they create
	// for somebody else.
}

func (s *Server) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	var req acceptReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "unreadable request")
		return
	}
	token, p, err := s.Auth.AcceptInvitation(r.Context(), req.Token, req.Password)
	if err != nil {
		writeAuthErr(w, err)
		return
	}
	// Signed in immediately. Unlike a password reset, this is not a link that merely proves access to a
	// mailbox — they have just chosen a password, which is the same evidence signing in would ask for.
	setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant": p.Tenant, "email": p.Subject, "role": p.Role,
	})
}

// ── confirming an address ────────────────────────────────────────────────────────────────────────

type verifyReq struct {
	Token string `json:"token"`
}

func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req verifyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "unreadable request")
		return
	}
	email, err := s.Auth.VerifyEmail(r.Context(), req.Token)
	if err != nil {
		writeAuthErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"email": email, "status": "That address is confirmed.",
	})
}

// handleResendVerification sends a confirmation link to the CALLER's own address.
//
// 🔴 It takes no address argument. An endpoint that mails a link to an address in the request body is a
// way to send mail from this product to anybody, which is a spam relay with a nicer name. The recipient
// is whatever address the session's account holds.
func (s *Server) handleResendVerification(w http.ResponseWriter, r *http.Request) {
	p, err := tenancy.From(r.Context())
	if err != nil {
		unauthorized(w, "You are not signed in.")
		return
	}
	if err := s.mailAvailable(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	token, to, org, err := s.Auth.CreateEmailVerification(r.Context(), p.Tenant, p.UserID)
	if err != nil {
		writeAuthErr(w, err)
		return
	}
	msg := mailer.VerifyEmail(to, orDefault(org, p.Tenant), s.Links.Verify(token),
		auth.EmailVerificationLifetime)
	if err := s.Mail.Send(r.Context(), msg); err != nil {
		// Reported, because the caller is the recipient — there is no address here they do not already
		// know about.
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "The confirmation could not be sent: " + err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "A confirmation link is on its way to " + to + ". It expires in 24 hours.",
	})
}
