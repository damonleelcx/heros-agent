package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heros-foreal/heros/internal/auth"
	"github.com/heros-foreal/heros/internal/tenancy"
)

// middleware.go is the only place a principal is created from a request.
//
// # 🔴 Why authentication is a wrapper and not a check inside each handler
//
// A check inside a handler is a check a new handler can be written without. Routes get added under
// deadline, and the one that forgets is indistinguishable from the ones that did not — until somebody
// notices it serves everybody's data.
//
// So the mux is wrapped, every route is authenticated by default, and the exemptions are a short
// explicit list that a reviewer can read in one sitting. Adding a route makes it protected; exposing one
// takes a deliberate `Public: true` in the route table.
//
// What a principal may DO once authenticated is decided one layer in, at registration — see routes.go.

// sessionCookie is the browser's credential.
const sessionCookie = "heros_session"

// 🔴 The set of public paths is a closed list derived from `apiRoutes`, not a prefix rule. `/api/auth/`
// as a prefix would silently expose anything a later phase files under it — and this feature files four
// new endpoints there, three of which hand out sessions. The whole point of a default-deny is that
// widening it is one visible `Public: true` in a diff.

// authenticate wraps a handler so every request carries an identity, or is refused.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Static assets are served by the file server, not the API, and carry no data.
		if !strings.HasPrefix(r.URL.Path, "/api/") || publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		token := credentialFrom(r)
		if token == "" {
			unauthorized(w, "You are not signed in.")
			return
		}
		p, err := s.Auth.Authenticate(r.Context(), token)
		if err != nil {
			// 🔴 One message for every failure. Distinguishing "no such session" from "expired" tells the
			// holder of a token whether it was ever real, which is the first half of guessing one.
			unauthorized(w, "Your session is not valid. Sign in again.")
			return
		}
		next.ServeHTTP(w, r.WithContext(tenancy.With(r.Context(), p)))
	})
}

// credentialFrom reads the session token from a cookie or a bearer header.
//
// The cookie is for the console; the header is for a terminal. Both are the same credential, so there is
// one verification path rather than two that could disagree.
func credentialFrom(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

// qq quotes a phrase the customer sees on screen, so a message naming a button reads as naming a button.
func qq(s string) string { return "\u201c" + s + "\u201d" }

func unauthorized(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": msg})
}

// ── the auth routes ──────────────────────────────────────────────────────────────────────────────

type loginReq struct {
	Tenant   string `json:"tenant"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable request"})
		return
	}
	if req.Tenant == "" {
		req.Tenant = s.DefaultTenant
	}

	// 🔴 Spent BEFORE the lookup, and the limiter is told nothing about what the lookup found.
	//
	// The same rule as the password-reset limit next door, for the same reason. This endpoint already
	// answers identically for a wrong password and an unknown address, in message and in timing; a limit
	// that only counted attempts against accounts that EXIST would undo it, because then being refused at
	// all would mean the address is real.
	key := loginKey(req.Tenant, req.Email)
	if ok, wait := s.LoginLimit.Allow(key); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "Too many sign-in attempts for that account. Try again in " + humanWait(wait) +
				". If this was not you, somebody is guessing at the password — it has not been " +
				"changed, and choosing a new one from " + qq("Forgotten your password?") +
				" will end the attempt.",
		})
		return
	}

	token, p, err := s.Auth.Login(r.Context(), req.Tenant, req.Email, req.Password)
	if err != nil {
		// 🔴 One message, and a deliberate pause is NOT added here — the constant-time password path in
		// auth.Login already makes a missing user cost what a wrong password costs.
		unauthorized(w, "That email and password do not match.")
		return
	}

	// 🔴 A CORRECT password costs nothing. This is what keeps the limit from being a weapon: charged for
	// every attempt, anybody could lock anybody out of their own account by failing to sign in as them
	// often enough — and with the reset endpoint also limited per address, both ways in would close at
	// once.
	//
	// Charging only for failures does not make the account unblockable, and the claim is worth stating
	// exactly: an attacker who keeps the bucket at zero still gets the owner refused, because the refusal
	// happens before the password is looked at. What it removes is the RATCHET. Every refilled token is a
	// fresh chance, the owner needs to win exactly one of those races, and the attempt that wins costs
	// nothing — so the owner gets in after some retries instead of being shut out until somebody
	// intervenes.
	//
	// It is spend-then-restore rather than look-then-charge because the check and the spend have to be
	// one atomic step; two concurrent attempts that merely LOOK at the last token both proceed.
	s.LoginLimit.Restore(key)

	setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant": p.Tenant, "email": p.Subject, "role": p.Role,
		"expires_in_hours": int(auth.SessionLifetime.Hours()),
	})
}

// setSessionCookie is the ONE place a session reaches a browser.
//
// 🔴 Shared by signing in and by accepting an invitation, which both hand out a session. Two copies of
// these flags is two places for `HttpOnly` to be true, and the one that is missing it is the one that
// gets read by a script on the page.
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:  sessionCookie,
		Value: token,
		Path:  "/",
		// 🔴 HttpOnly: script on the page cannot read it, so an injected script cannot exfiltrate the
		// session. SameSite=Lax: another site cannot cause an authenticated request from the browser.
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure is set when the request arrived over TLS. Setting it unconditionally would make the
		// cookie unusable on a local http console, and hard-coding false would send it in clear text in
		// production — so it follows the connection it was issued on.
		Secure:  r.TLS != nil,
		Expires: time.Now().Add(auth.SessionLifetime),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if tok := credentialFrom(r); tok != "" {
		_ = s.Auth.Logout(r.Context(), tok)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

// handleAuthStatus says whether the caller is signed in. Public, because the console asks it before it
// has a credential — and it reveals nothing except whether the request it just made carried one.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	tok := credentialFrom(r)
	if tok == "" {
		writeJSON(w, http.StatusOK, map[string]any{"signed_in": false})
		return
	}
	p, err := s.Auth.Authenticate(r.Context(), tok)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"signed_in": false})
		return
	}
	body := map[string]any{
		"signed_in": true, "tenant": p.Tenant, "email": p.Subject, "role": p.Role,
		// 🔴 The console renders its menus from THIS, not from its own copy of the rules. A second
		// implementation of the capability table in JavaScript would disagree with the real one sooner or
		// later, and the direction it disagrees in is a button that appears and then refuses.
		"can": capabilitiesOf(p),
	}
	// email_verified needs one more read, and only the status call makes it — everything else that
	// matters is on the principal. It gates nothing; the console shows a banner offering to resend.
	if m, err := s.Auth.Member(r.Context(), p.Tenant, p.UserID); err == nil {
		body["email_verified"] = m.EmailVerified
	}
	body["mail_configured"] = s.mailReady()
	writeJSON(w, http.StatusOK, body)
}

// capabilitiesOf renders what this principal may do, as the console needs it.
func capabilitiesOf(p tenancy.Principal) map[string]bool {
	out := make(map[string]bool, len(tenancy.Capabilities))
	for _, c := range tenancy.Capabilities {
		out[string(c)] = p.May(c)
	}
	return out
}
