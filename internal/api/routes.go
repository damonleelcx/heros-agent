package api

import (
	"net/http"

	"github.com/heros-foreal/heros/internal/tenancy"
)

// routes.go is the one list of what this server serves, who may reach it, and what they must be.
//
// # 🔴 Why authorization is declared beside the route and not written inside the handler
//
// It is the same argument that made authentication a wrapper. A capability check inside a handler is a
// check the next handler can be written without, and the one that forgets is indistinguishable from the
// ones that did not until somebody notices a viewer approved a change. Declaring it here means the check
// is applied by REGISTRATION: a route reaches its handler only through the wrapper its row asked for,
// and a row with no answer in the `Needs` column is a row a reviewer can see is unguarded.
//
// # 🔴 Why the public list and the mux come from the same table
//
// They were two lists — a `map[string]bool` of public paths beside a series of `m.HandleFunc` calls —
// and two hand-maintained lists of the same thing drift. The failure is silent in the dangerous
// direction: a route added to the mux and not to the fence's list is simply never checked. Everything
// below is derived from `apiRoutes`, so the fence walks exactly what the server serves.

// route is one endpoint.
type route struct {
	Method string
	Path   string
	// Public means the route is served WITHOUT a credential. Four of these exist and each one is a
	// deliberate hole: somebody who has forgotten their password, or is holding an invitation, has no
	// credential by definition.
	Public bool
	// Needs is the capability the caller must hold. Empty means any authenticated member of the tenant —
	// used for routes that only read what everybody in an organization can already see.
	//
	// Ignored on a public route, where there is nobody to hold anything.
	Needs   tenancy.Capability
	Handler func(*Server) http.HandlerFunc
}

// apiRoutes is every route this server has.
var apiRoutes = []route{
	// ── signing in, and the three ways in without a password ──────────────────────────────────────
	{Method: "POST", Path: "/api/auth/login", Public: true,
		Handler: func(s *Server) http.HandlerFunc { return s.handleLogin }},
	{Method: "GET", Path: "/api/auth/status", Public: true,
		Handler: func(s *Server) http.HandlerFunc { return s.handleAuthStatus }},
	{Method: "POST", Path: "/api/auth/logout",
		Handler: func(s *Server) http.HandlerFunc { return s.handleLogout }},

	// Public because the person using them has no account yet, or cannot get into the one they have.
	// Each is rate-limited by its own token being single-use and short-lived rather than by a counter.
	{Method: "POST", Path: "/api/auth/password/forgot", Public: true,
		Handler: func(s *Server) http.HandlerFunc { return s.handleForgotPassword }},
	{Method: "POST", Path: "/api/auth/password/reset", Public: true,
		Handler: func(s *Server) http.HandlerFunc { return s.handleResetPassword }},
	{Method: "GET", Path: "/api/auth/invitation", Public: true,
		Handler: func(s *Server) http.HandlerFunc { return s.handleLookupInvitation }},
	{Method: "POST", Path: "/api/auth/invitation/accept", Public: true,
		Handler: func(s *Server) http.HandlerFunc { return s.handleAcceptInvitation }},
	{Method: "POST", Path: "/api/auth/email/verify", Public: true,
		Handler: func(s *Server) http.HandlerFunc { return s.handleVerifyEmail }},

	// Resending your OWN confirmation needs a session — there is nothing to resend to somebody who
	// cannot say who they are, and a public version would mail anybody's address on request.
	{Method: "POST", Path: "/api/auth/email/resend",
		Handler: func(s *Server) http.HandlerFunc { return s.handleResendVerification }},

	// ── the organization ──────────────────────────────────────────────────────────────────────────
	// Listing colleagues needs no capability: everybody in an organization can already see who else is
	// in it, and hiding it would only stop the console rendering the page that says so.
	{Method: "GET", Path: "/api/members",
		Handler: func(s *Server) http.HandlerFunc { return s.handleListMembers }},
	{Method: "POST", Path: "/api/members/invite", Needs: tenancy.InviteMember,
		Handler: func(s *Server) http.HandlerFunc { return s.handleInvite }},
	{Method: "POST", Path: "/api/members/role", Needs: tenancy.ManageMembers,
		Handler: func(s *Server) http.HandlerFunc { return s.handleSetRole }},
	{Method: "POST", Path: "/api/members/remove", Needs: tenancy.ManageMembers,
		Handler: func(s *Server) http.HandlerFunc { return s.handleRemoveMember }},
	{Method: "GET", Path: "/api/invitations", Needs: tenancy.InviteMember,
		Handler: func(s *Server) http.HandlerFunc { return s.handleListInvitations }},
	{Method: "POST", Path: "/api/invitations/revoke", Needs: tenancy.InviteMember,
		Handler: func(s *Server) http.HandlerFunc { return s.handleRevokeInvitation }},

	// ── the work ──────────────────────────────────────────────────────────────────────────────────
	{Method: "POST", Path: "/api/subject", Needs: tenancy.LoadSubject,
		Handler: func(s *Server) http.HandlerFunc { return s.handleSubject }},
	{Method: "GET", Path: "/api/subject", Needs: tenancy.ReadGoals,
		Handler: func(s *Server) http.HandlerFunc { return s.handleGetSubject }},
	{Method: "POST", Path: "/api/ask", Needs: tenancy.RunGoals,
		Handler: func(s *Server) http.HandlerFunc { return s.handleAsk }},
	{Method: "GET", Path: "/api/goals/{id}/events", Needs: tenancy.ReadGoals,
		Handler: func(s *Server) http.HandlerFunc { return s.handleEvents }},
	// The events stream is what is happening NOW; the timeline is what happened, why, and what the run
	// is waiting for. Both take a goal id from the URL, so both check ownership before reading anything.
	{Method: "GET", Path: "/api/goals/{id}/timeline", Needs: tenancy.ReadGoals,
		Handler: func(s *Server) http.HandlerFunc { return s.handleTimeline }},
	// 🔴 The sharpest route in the product: approving a Tier-C change writes to the customer's own
	// repository. A viewer must not reach it, and neither must the system principal.
	{Method: "POST", Path: "/api/decide", Needs: tenancy.ApproveChange,
		Handler: func(s *Server) http.HandlerFunc { return s.handleDecideRequest }},
}

// publicPaths is derived from the table, so it cannot fall behind it.
//
// Keyed by path rather than by method, because `authenticate` runs before the mux has matched a pattern
// and only knows the URL. TestNoPathIsHalfPublic asserts that no path mixes public and protected
// methods, which is what would make that approximation wrong.
var publicPaths = func() map[string]bool {
	m := make(map[string]bool, len(apiRoutes))
	for _, r := range apiRoutes {
		if r.Public {
			m[r.Path] = true
		}
	}
	return m
}()

// requireCapability refuses a principal that does not hold one.
//
// # 🔴 Why 403 and not 404
//
// Elsewhere in this codebase a refusal is disguised as "not found", because confirming that another
// customer's goal id is real turns a guess into an enumeration. Here the object belongs to the caller's
// own organization and they already know it exists — they can see the button. Answering 404 would tell
// a viewer that "approve" does not exist rather than that they may not use it, which is a support ticket
// rather than a security property.
func requireCapability(need tenancy.Capability, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := tenancy.From(r.Context())
		if err != nil {
			unauthorized(w, "You are not signed in.")
			return
		}
		if !p.May(need) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": refusalFor(need, p.Role),
				"role":  string(p.Role),
			})
			return
		}
		next(w, r)
	}
}

// refusalFor explains a refusal in the words somebody would use to ask for what they need.
//
// 🔴 A table, not a format string. "You need goal.run" makes the reader learn this codebase's vocabulary
// to understand a sentence about their own account; naming the role that does hold it tells them exactly
// what to ask an administrator for.
func refusalFor(need tenancy.Capability, have tenancy.Role) string {
	switch need {
	case tenancy.RunGoals:
		return "Your account can read this organization's work but not start new runs. " +
			"An owner or an administrator can change your role from viewer to member."
	case tenancy.LoadSubject:
		return "Your account cannot point the console at a repository. " +
			"An owner or an administrator can change your role from viewer to member."
	case tenancy.ApproveChange:
		return "Your account cannot approve a change to the repository. " +
			"Approving is what actually writes the code, so it is limited to members and above."
	case tenancy.InviteMember:
		return "Only owners and administrators can invite people to this organization."
	case tenancy.ManageMembers:
		return "Only owners and administrators can change roles or remove people."
	case tenancy.TransferOwnership:
		return "Only an owner can make somebody else an owner."
	default:
		if have == "" {
			return "This request carries no role, so it holds no permissions."
		}
		return "Your role does not permit that."
	}
}
