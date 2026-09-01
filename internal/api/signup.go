package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/heros-foreal/heros/internal/auth"
	"github.com/heros-foreal/heros/internal/mailer"
)

// The sign-up ceiling, keyed on the address being registered.
//
// Two back to back covers the real case — somebody mistypes their organization name and submits again —
// and refills slowly, because a person creates an organization approximately once.
//
// # 🔴 What this limit does NOT cover, stated because the gap is easy to mistake for coverage
//
// It is keyed on the ADDRESS. An attacker who varies the address is not slowed by it at all: a thousand
// sign-ups at one each trips nothing. That is the same per-caller gap the login and reset limits have,
// and it is not closed here because closing it needs a trustworthy client address, which means proxy
// trust configuration this deployment does not have — and an IP limit derived from an untrusted header
// is worse than none, because it can be aimed at somebody else.
//
// What DOES bound the damage today is the password-hashing gate: every sign-up costs one argon2id
// verification, the process runs a fixed number of those at once, and the rest are shed with an honest
// 503 rather than queued. So a flood becomes slow and loud instead of unbounded — but it can still
// create many organizations, and nothing here prevents that.
const (
	SignupBurst      = 2
	SignupRefill     = 10 * time.Minute
	SignupKeyCeiling = 50_000
)

type signupReq struct {
	Organization string `json:"organization"`
	Email        string `json:"email"`
	Password     string `json:"password"`
}

// handleSignup creates an organization and its first account.
//
// # 🔴 Why this answers HONESTLY that an address is taken, when login and reset refuse to
//
// The neighbouring endpoints go to real trouble to make "no such address" and "wrong password"
// indistinguishable, so this looks like an inconsistency. It is not, and the difference is worth
// stating because a future reader will otherwise "fix" it.
//
// A sign-up form cannot be vague. If an address is already registered and this endpoint says something
// non-committal, the person is left unable to sign up and unable to understand why — and the address is
// theirs, in the overwhelmingly common case. Every product that has tried the vague answer here ends up
// leaking it anyway through the next screen.
//
// The enumeration risk is real but it is bounded to "is this address registered", which the reset
// endpoint already refuses to confirm, so an attacker gains one bit they could also get by trying to
// sign up on any other service. Weighed against a form nobody can complete, it is the right trade —
// and the rate limit above keeps it from being a bulk oracle.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var req signupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "unreadable request")
		return
	}

	// Spent BEFORE the work, like every other limit in this package: a limit that only counts what
	// succeeded tells the caller which attempts were real.
	key := auth.EmailKey(req.Email)
	if ok, wait := s.SignupLimit.Allow(key); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "Too many sign-up attempts for that address. Try again in " + humanWait(wait) + ".",
		})
		return
	}

	token, p, err := s.Auth.SignUp(r.Context(), req.Organization, req.Email, req.Password)
	switch {
	case errors.Is(err, auth.ErrBusy):
		// 🔴 503 and a REFUND, matching sign-in. The attempt was never evaluated, so charging for it
		// would make a busy server quietly eat somebody's sign-up budget.
		s.SignupLimit.Restore(key)
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "The server is busy and could not finish creating your account. Nothing was " +
				"created. Try again in a few seconds.",
		})
		return
	case errors.Is(err, auth.ErrEmailTaken):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "That email address already has an account. Sign in instead, or use " +
				qq("Forgotten your password?") + " if you cannot get in.",
		})
		return
	case errors.Is(err, auth.ErrWeakPassword):
		badRequest(w, "Choose a password of at least "+strconv.Itoa(auth.MinPasswordLength)+" characters.")
		return
	case errors.Is(err, auth.ErrOrgNameRequired):
		badRequest(w, "Give your organization a name — it is what everyone you invite will see.")
		return
	case errors.Is(err, auth.ErrNoSuchUser):
		badRequest(w, "That does not look like an email address.")
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Your account could not be created. Nothing was saved; please try again.",
		})
		return
	}

	// 🔴 Confirmation mail is BEST EFFORT and explicitly not fatal, matching the bootstrap path. The
	// account works either way — a confirmed address gates nothing today — and refusing to complete a
	// sign-up because the relay is down would turn a mail outage into a product that cannot be joined.
	if s.mailReady() {
		if tok, to, org, mErr := s.Auth.CreateEmailVerification(r.Context(), p.Tenant, p.UserID); mErr == nil {
			s.sendInBackground("email verification", to,
				mailer.VerifyEmail(to, org, s.Links.Verify(tok), auth.EmailVerificationLifetime))
		}
	}

	setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant": p.Tenant, "email": p.Subject, "role": p.Role,
		"expires_in_hours": int(auth.SessionLifetime.Hours()),
	})
}
