package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/mailer"
	"github.com/heros-foreal/agentd/internal/seats"
	"github.com/heros-foreal/agentd/internal/signup"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

// accounts.go is P27's HTTP surface: sign-up, members, invitations and credentials.
//
// # Scope comes from the credential, and there is no parameter that could change it
//
// Every route below reads the tenant from `auth.PrincipalFrom`. None of them takes an organization in a
// path, a query or a body — not because a rule says so, but because there is no parameter to get wrong.
// The one exception is sign-up, which creates an organization and therefore cannot be scoped to one; it
// is discussed under `handleCreateOrganization`.
//
// # Every refusal carries a `reason_code`
//
// The console has to distinguish *seat limit reached* from *last owner* from *invitation expired* in
// order to say three different things and offer three different next actions. Branching on the error
// PROSE would put that decision in two places and let a copy edit change behaviour, so the machine-
// readable name travels beside the sentence — the same shape P21's `collection_not_configured` already
// uses.
//
// # 🔴 A credential's plaintext is returned exactly once
//
// `handleCreateCredential` is the only response in this file that carries a secret, and it carries it
// because that is the only moment it exists. Nothing stores it, no listing returns it, and no other
// handler has a field it would fit in.

// Reason codes. Closed set, because the console switches on them.
const (
	ReasonSelfServeDisabled  = "self_serve_disabled"
	ReasonSeatLimitReached   = "seat_limit_reached"
	ReasonLastOwner          = "last_owner"
	ReasonInvitationExpired  = "invitation_expired"
	ReasonInvitationMismatch = "invitation_identity_mismatch"
	ReasonNotAMember         = "not_a_member"
	ReasonForbiddenRole      = "insufficient_role"
	ReasonOrgSuspended       = "organization_suspended"
	ReasonAccountSystemOff   = "account_system_not_mounted"
)

// AccountSurface is everything this file needs from the domain. One interface rather than four fields,
// so a deployment either mounts the account system or does not — a half-mounted one would answer some
// routes and 503 others, which reads to a user as an intermittent product.
type AccountSurface interface {
	Store() tenancy.Store
	// SignUp is nil when this deployment does not permit self-serve organization creation.
	SignUp() *signup.Service
	// SelfServeEnabled is the DECLARED posture, not an inference from SignUp being non-nil.
	SelfServeEnabled() bool
	// SeatsAllowed returns the plan's seat allowance for an organization, and whether one is set. An
	// absent allowance is UNLIMITED, never zero — see plancfg's Limits comment.
	SeatsAllowed(tenantID string) (float64, bool, error)
	// ObserveSeats re-derives and records the period's seat PEAK after a membership change.
	//
	// It is called after every change and it is not an increment: `internal/seats` recomputes from the
	// membership timeline, so a duplicated call and a missed one both end at the correct number. A
	// deployment with no metering implements it as a no-op — refusing a membership change because a
	// meter is absent would take an identity operation down over a billing dependency.
	ObserveSeats(tenantID string)
	Now() time.Time
	// Mailer delivers confirmation and reset links (P28). It may be nil, in which case this layer builds the
	// operator-visible fallback itself — 🔴 there is deliberately no path on which a message is dropped, so
	// "no mailer" means "held for the operator", never "discarded".
	Mailer() mailer.Mailer
	// ConsoleURL is the origin the links in those messages point at. Empty produces relative links, which is
	// wrong in an email and is why the deployment declares it; the readiness surface reports it unset.
	ConsoleURL() string
}

// MountAccounts registers P27's routes. Nothing is registered when the surface is absent, so an
// unmounted deployment answers 404 for a route it does not have rather than 503 for one it does.
func (s *Server) MountAccounts(a AccountSurface) {
	s.accounts = a
	s.Mux.HandleFunc("POST /api/v1/organizations", s.handleCreateOrganization)
	s.Mux.HandleFunc("GET /api/v1/organization", s.handleGetOrganization)
	s.Mux.HandleFunc("GET /api/v1/organization/members", s.handleListMembers)
	s.Mux.HandleFunc("POST /api/v1/organization/members/{user_id}/role", s.handleSetRole)
	s.Mux.HandleFunc("GET /api/v1/organization/members/{user_id}/removal-preview", s.handleRemovalPreview)
	s.Mux.HandleFunc("DELETE /api/v1/organization/members/{user_id}", s.handleRemoveMember)
	s.Mux.HandleFunc("GET /api/v1/organization/invitations", s.handleListInvitations)
	s.Mux.HandleFunc("POST /api/v1/organization/invitations", s.handleCreateInvitation)
	s.Mux.HandleFunc("POST /api/v1/organization/invitations/{invitation_id}/accept", s.handleAcceptInvitation)
	s.Mux.HandleFunc("DELETE /api/v1/organization/invitations/{invitation_id}", s.handleRevokeInvitation)
	s.Mux.HandleFunc("GET /api/v1/organization/credentials", s.handleListCredentials)
	s.Mux.HandleFunc("POST /api/v1/organization/credentials", s.handleCreateCredential)
	s.Mux.HandleFunc("DELETE /api/v1/organization/credentials/{credential_id}", s.handleRevokeCredential)
	s.Mux.HandleFunc("POST /api/v1/token-exchange", s.handleTokenExchange)
	s.Mux.HandleFunc("POST /api/v1/organization/close", s.handleCloseAccount)
	// The console's own session store, made durable. Three routes, all authenticated by the console's
	// platform credential — a browser never reaches them.
	s.Mux.HandleFunc("POST /api/v1/users/resolve", s.handleResolveUser)
	s.Mux.HandleFunc("POST /api/v1/console-sessions", s.handleCreateConsoleSession)
	s.Mux.HandleFunc("POST /api/v1/console-sessions/resolve", s.handleResolveConsoleSession)
	s.Mux.HandleFunc("POST /api/v1/console-sessions/revoke", s.handleRevokeConsoleSession)
	// The terminal's way in (task 13.1). Mounted here, with the account system, because a deployment
	// with no memberships has nothing for an approval to select.
	s.mountDeviceAuth()
	// A person's own way in (P28). Mounted here for the same reason: every route writes a person, a
	// membership or a credential, so without an account system they could only ever refuse.
	s.mountPasswordAuth()
}

// ── resolving a verified identity to a person ───────────────────────────────────────────────────────

type resolveUserRequest struct {
	// The VERIFIED claims, forwarded by the console's server side after it verified the assertion and
	// resolved the organization through its own mapping.
	Issuer   string `json:"issuer"`
	Subject  string `json:"subject"`
	Email    string `json:"email"`
	TenantID string `json:"tenant_id"`
}

// handleResolveUser turns a verified federated identity into the platform's own person, and makes sure
// that person is a member of the organization the console's mapping placed them in.
//
// # Why the membership is ensured here rather than left to an invitation
//
// The console's mapping has ALREADY decided this identity belongs to this organization — that is what
// `resolveTenant` does, by verified email domain, by per-issuer registration, or by an explicit JIT
// allow rule. Signing somebody in and then telling them they are not a member would be the platform
// disagreeing with its own configuration, and every member-scoped surface would refuse them with a
// reason they cannot act on.
//
// 🔴 A membership created this way DOES occupy a seat, and it is deliberately not refused when the
// organization is over its allowance. Refusing would lock out somebody the deployment's own mapping
// admits; going over is instead VISIBLE — on the members page, and through the entitlement gate that
// already denies the dashboard over the seat limit. A deployment that maps a whole domain into one
// organization has chosen that, and the honest response is to show it the number rather than to bar the
// door at sign-in.
func (s *Server) handleResolveUser(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff, "this deployment does not mount the account system")
		return
	}
	if _, ok := auth.PrincipalFrom(r.Context()); !ok {
		refuse(w, http.StatusUnauthorized, "", "authentication required")
		return
	}
	var req resolveUserRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		refuse(w, http.StatusBadRequest, "", "the request is not valid")
		return
	}
	if strings.TrimSpace(req.Issuer) == "" || strings.TrimSpace(req.Subject) == "" || strings.TrimSpace(req.TenantID) == "" {
		refuse(w, http.StatusBadRequest, "", "resolving a person needs a verified identity and an organization")
		return
	}
	store := s.accounts.Store()
	if _, err := store.GetTenant(req.TenantID); err != nil {
		refuse(w, http.StatusForbidden, "", "no person can be resolved for that organization")
		return
	}
	now := s.accounts.Now()
	user, err := store.UpsertUser(tenancy.User{
		Issuer: strings.TrimSpace(req.Issuer), Subject: strings.TrimSpace(req.Subject),
		Email: tenancy.NormalizeEmail(req.Email), CreatedAt: now,
	})
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the person could not be recorded")
		return
	}

	existing, merr := store.GetMembership(user.UserID, req.TenantID)
	switch {
	case merr == nil && existing.Active():
		// Already a member. Nothing to do, and in particular the ROLE is not reset — a sign-in must not
		// demote an owner back to the default.
	case merr == nil:
		// A REMOVED membership. Signing in does not reinstate it: removal is an administrative decision,
		// and letting a sign-in undo it would make offboarding depend on the person not trying again.
		writeJSON(w, http.StatusOK, map[string]any{"user_id": user.UserID, "member": false, "reason_code": ReasonNotAMember})
		return
	default:
		if _, err := store.PutMembership(tenancy.Membership{
			UserID: user.UserID, TenantID: req.TenantID, Role: tenancy.RoleMember,
			Status: tenancy.MemberActive, JoinedAt: now,
		}); err != nil {
			refuse(w, http.StatusInternalServerError, "", "the membership could not be recorded")
			return
		}
		// A new member is a seat taken; re-derive the period's peak from the timeline it just joined.
		s.accounts.ObserveSeats(req.TenantID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": user.UserID, "member": true})
}

// ── the console's durable session store ─────────────────────────────────────────────────────────────
//
// # What moved, and what deliberately did not
//
// The console's sessions lived in a `globalThis` Map. Its own comment called that honest for the
// one-container deployment ADR-006 describes and named the two consequences: a console restart ends
// every session, and a horizontally-scaled console needs a shared store. P19's Kubernetes overlay
// declares `replicas: 2`, under which a user signs in against one pod and is signed out by the next
// request that lands on the other — intermittently, which is the worst failure mode to diagnose.
//
// So the STORE moves here and nothing else does. The TTL, the cookie flags, revocation-at-the-next-
// request-with-no-grace and the fail-closed middleware are the console's and are untouched.
//
// # 🔴 The platform never sees a console session token
//
// The console mints the token, hashes it, and sends only the HASH. There is no field on any of these
// three requests a plaintext could arrive in. That is stricter than it needs to be and it is free: the
// console already holds the token, so nothing is gained by the platform holding it too, and a token in
// a request body is a token in an access log.

type createConsoleSessionRequest struct {
	// TokenHash is what the console computed. Never the token.
	TokenHash string `json:"token_hash"`
	SessionID string `json:"session_id"`
	TenantID  string `json:"tenant_id"`
	UserID    string `json:"user_id"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
}

func (s *Server) handleCreateConsoleSession(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff, "this deployment does not mount the account system")
		return
	}
	if _, ok := auth.PrincipalFrom(r.Context()); !ok {
		refuse(w, http.StatusUnauthorized, "", "authentication required")
		return
	}
	var req createConsoleSessionRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		refuse(w, http.StatusBadRequest, "", "the request is not valid")
		return
	}
	if strings.TrimSpace(req.TokenHash) == "" || strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.TenantID) == "" {
		refuse(w, http.StatusBadRequest, "", "a session needs a token hash, an id and an organization")
		return
	}
	store := s.accounts.Store()
	if _, err := store.GetTenant(req.TenantID); err != nil {
		refuse(w, http.StatusForbidden, "", "no session can be issued for that organization")
		return
	}
	sess, err := store.CreateSession(tenancy.Session{
		TokenHash: req.TokenHash,
		SessionID: req.SessionID,
		TenantID:  req.TenantID,
		UserID:    strings.TrimSpace(req.UserID),
		IssuedAt:  req.IssuedAt,
		ExpiresAt: req.ExpiresAt,
		// 🔴 A browser cookie, NOT an API credential. `auth` refuses this purpose.
		Purpose: tenancy.PurposeConsole,
	})
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the session could not be recorded")
		return
	}
	writeJSON(w, http.StatusCreated, consoleSessionView(sess))
}

type consoleSessionLookup struct {
	TokenHash string `json:"token_hash"`
}

// handleResolveConsoleSession answers "is this session live, and whose is it?".
//
// It is a POST rather than a GET so the hash travels in a body rather than in a path — a path is logged
// by every proxy in front of it, and a hash in an access log is a hash somebody can test against a
// stolen cookie offline.
func (s *Server) handleResolveConsoleSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.consoleSessionFromBody(w, r)
	if !ok {
		return
	}
	// Expiry and revocation are checked HERE, on every read, which is what keeps revocation effective at
	// the next request with no grace period — the property the in-memory store had and that a durable
	// one must not lose.
	if !sess.Live(s.accounts.Now().UnixMilli()) {
		refuse(w, http.StatusNotFound, "", "no such session")
		return
	}
	if t, err := s.accounts.Store().GetTenant(sess.TenantID); err != nil || t.Status.Suspended() {
		// A suspended organization's sessions stop resolving. Otherwise closing an account would leave
		// everybody signed in until their cookie expired.
		refuse(w, http.StatusNotFound, "", "no such session")
		return
	}
	writeJSON(w, http.StatusOK, consoleSessionView(sess))
}

func (s *Server) handleRevokeConsoleSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.consoleSessionFromBody(w, r)
	if !ok {
		return
	}
	if err := s.accounts.Store().RevokeSession(sess.TokenHash, s.accounts.Now().UnixMilli()); err != nil {
		refuse(w, http.StatusInternalServerError, "", "the session could not be revoked")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

// consoleSessionFromBody is the shared preamble: authenticate the caller, read a hash, find the row.
func (s *Server) consoleSessionFromBody(w http.ResponseWriter, r *http.Request) (tenancy.Session, bool) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff, "this deployment does not mount the account system")
		return tenancy.Session{}, false
	}
	if _, ok := auth.PrincipalFrom(r.Context()); !ok {
		refuse(w, http.StatusUnauthorized, "", "authentication required")
		return tenancy.Session{}, false
	}
	var req consoleSessionLookup
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || strings.TrimSpace(req.TokenHash) == "" {
		refuse(w, http.StatusBadRequest, "", "the request is not valid")
		return tenancy.Session{}, false
	}
	sess, err := s.accounts.Store().ResolveSession(strings.TrimSpace(req.TokenHash))
	if err != nil || sess.Purpose != tenancy.PurposeConsole {
		// An UPSTREAM token presented here is not a console session, and saying so would tell a caller
		// which kind they hold. One answer for both.
		refuse(w, http.StatusNotFound, "", "no such session")
		return tenancy.Session{}, false
	}
	return sess, true
}

// consoleSessionView carries what the console needs to rebuild its own Session value. No token, no hash.
func consoleSessionView(sess tenancy.Session) map[string]any {
	view := map[string]any{
		"session_id": sess.SessionID,
		"tenant_id":  sess.TenantID,
		"issued_at":  sess.IssuedAt,
		"expires_at": sess.ExpiresAt,
	}
	if sess.UserID != "" {
		// 🔴 ABSENT, not empty, when there is no person. A placeholder here would put a name on an
		// action nobody took.
		view["user_id"] = sess.UserID
	}
	if sess.RevokedAt != 0 {
		view["revoked_at"] = sess.RevokedAt
	}
	return view
}

// ErasureMechanism names the process that actually erases data, so a closure surface can point at it
// instead of implying it did the erasing.
//
// 🔴 It is a NAME, not a promise. Closing an account suspends the organization and stops accrual; it
// does not delete history, and a surface that says "closed" without saying that is a surface a customer
// reads as deletion. The distinction is regulatory as well as honest: a customer who hears "we keep it"
// without "you can ask us to erase it" hears a retention problem, and one who hears "deleted" without
// "closure is not deletion" hears a promise we do not keep.
const ErasureMechanism = "gdpr_request"

// handleCloseAccount suspends the organization and stops metered accrual. It erases nothing.
func (s *Server) handleCloseAccount(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	// Owner only. Closing is the mirror of upgrading, and both are financial commitments.
	if _, ok := s.actingMember(w, p, store, func(role tenancy.Role) bool { return role == tenancy.RoleOwner }); !ok {
		return
	}

	t, err := store.SetTenantStatus(p.TenantID, tenancy.StatusSuspended, s.accounts.Now())
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the organization could not be closed")
		return
	}
	// The seat peak is re-derived one last time, so the period the closure falls in bills what was
	// actually held rather than what happened to be left at the moment somebody clicked.
	s.accounts.ObserveSeats(p.TenantID)

	logSecurity("organization_closed", p.TenantID)
	writeJSON(w, http.StatusOK, map[string]any{
		"organization_id": t.TenantID,
		"status":          string(t.Status),
		// Both halves, always. What closing DID and what it did not.
		"billing_stopped": true,
		"data_erased":     false,
		"erasure_note": "closing stops billing and suspends the organization. It does not erase your " +
			"history — erasure is a separate, audited request.",
		"erasure_mechanism": ErasureMechanism,
	})
}

// ScopedTokenTTL bounds a console token's life.
//
// Ten minutes: long enough that a page render and its follow-up fetches share one token, short enough
// that a token captured from a log or a proxy is worthless by the time anybody reads it. It is NOT the
// session's TTL — the browser session is eight hours and lives in the console; this is the thing the
// console holds on the browser's behalf for one burst of work.
const ScopedTokenTTL = 10 * time.Minute

type tokenExchangeRequest struct {
	// TenantID and UserID are what the console's server side proved when it verified the assertion and
	// issued its own session. This endpoint is the ONE place that assertion is trusted.
	TenantID string `json:"tenant_id"`
	UserID   string `json:"user_id"`
}

// handleTokenExchange mints a short-lived token scoped to one organization.
//
// # What this replaces, and the claim it can honestly make
//
// Before P27 the console sent one process-wide credential on every request plus an `X-Console-Tenant`
// header the platform never read. Two things were wrong with that and only one of them was the header:
// the platform had no scope at all, and the console's single credential could reach everything.
//
// 🔴 What this does NOT change: the console's server side is still the authority on which organization a
// browser belongs to. It verified the assertion; nothing else can. Pretending otherwise would be an
// overclaim, and the design says so.
//
// What it DOES change is everything downstream of that one moment. The assertion of scope happens
// **once**, here, where it can be logged and rate-limited — instead of on every request, where it was
// ignored. The token that comes back is bound to that organization, expires in minutes, is revocable,
// and is revoked automatically when the member is removed (because it is a session row, and
// `RemoveMember` revokes sessions). A bug in a console route can forward the token it holds; it cannot
// mint one for a different organization without coming back here.
//
// The header is deleted rather than left inert, because an ignored header that names authority is read
// by the next person as the mechanism.
func (s *Server) handleTokenExchange(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff,
			"this deployment does not mount the account system")
		return
	}
	if _, ok := auth.PrincipalFrom(r.Context()); !ok {
		refuse(w, http.StatusUnauthorized, "", "authentication required")
		return
	}
	var req tokenExchangeRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		refuse(w, http.StatusBadRequest, "", "the request is not valid")
		return
	}
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		refuse(w, http.StatusBadRequest, "", "a scoped token needs an organization")
		return
	}
	store := s.accounts.Store()
	tenant, err := store.GetTenant(tenantID)
	if err != nil {
		// An unknown organization and a suspended one are one answer here, for the reason every other
		// refusal in this file is: the caller learns nothing it did not already know.
		refuse(w, http.StatusForbidden, "", "no token can be issued for that organization")
		return
	}
	if tenant.Status.Suspended() {
		refuse(w, http.StatusForbidden, ReasonOrgSuspended, "no token can be issued for that organization")
		return
	}
	userID := strings.TrimSpace(req.UserID)
	if userID != "" {
		// A token naming a person must name one who is actually a member. Otherwise the console could
		// mint a token attributing actions to somebody who left.
		m, merr := store.GetMembership(userID, tenantID)
		if merr != nil || !m.Active() {
			refuse(w, http.StatusForbidden, ReasonNotAMember, "no token can be issued for that person")
			return
		}
	}

	secret, err := tenancy.NewCredentialSecret()
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the token could not be issued")
		return
	}
	now := s.accounts.Now()
	sess, err := store.CreateSession(tenancy.Session{
		TokenHash: tenancy.HashSecret(secret),
		SessionID: tenancy.NewID("tok"),
		TenantID:  tenantID,
		UserID:    userID,
		IssuedAt:  now.UnixMilli(),
		ExpiresAt: now.Add(ScopedTokenTTL).UnixMilli(),
	})
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the token could not be issued")
		return
	}
	// The token, once. The response carries no organization name, no member list and no plan — a caller
	// that needs those asks for them with the token it just received.
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      secret,
		"token_id":   sess.SessionID,
		"expires_at": now.Add(ScopedTokenTTL).UTC(),
		"expires_in": int(ScopedTokenTTL.Seconds()),
	})
}

// refuse writes an error with its machine-readable reason beside it.
func refuse(w http.ResponseWriter, code int, reason, message string) {
	writeJSON(w, code, map[string]any{"error": message, "reason_code": reason})
}

// principalAndStore is the preamble every scoped handler shares: an authenticated tenant and a mounted
// account system, or a refusal that says which is missing.
func (s *Server) principalAndStore(w http.ResponseWriter, r *http.Request) (auth.Principal, tenancy.Store, bool) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff,
			"this deployment does not mount the account system")
		return auth.Principal{}, nil, false
	}
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || p.TenantID == "" {
		refuse(w, http.StatusUnauthorized, "", "authentication required")
		return auth.Principal{}, nil, false
	}
	return p, s.accounts.Store(), true
}

// actingMember resolves the caller's membership and checks a role requirement.
//
// 🔴 A MACHINE credential has no membership and therefore no authority to administer people. That is
// deliberate: a CI key that could remove a colleague is a CI key that becomes an offboarding tool, and
// the audit entry for it would name nobody.
func (s *Server) actingMember(w http.ResponseWriter, p auth.Principal, store tenancy.Store, need func(tenancy.Role) bool) (tenancy.Membership, bool) {
	if p.UserID == "" {
		refuse(w, http.StatusForbidden, ReasonNotAMember,
			"this action needs a signed-in person; a machine credential names nobody")
		return tenancy.Membership{}, false
	}
	m, err := store.GetMembership(p.UserID, p.TenantID)
	if err != nil || !m.Active() {
		refuse(w, http.StatusForbidden, ReasonNotAMember, "you are not an active member of this organization")
		return tenancy.Membership{}, false
	}
	if need != nil && !need(m.Role) {
		refuse(w, http.StatusForbidden, ReasonForbiddenRole, "your role does not permit this")
		return tenancy.Membership{}, false
	}
	return m, true
}

// ── sign-up ─────────────────────────────────────────────────────────────────────────────────────────

type createOrganizationRequest struct {
	// Name is the ONLY field a person types.
	Name string `json:"name"`
	// Issuer, Subject and Email are the VERIFIED assertion's claims, forwarded by the console's server
	// side after it verified them.
	//
	// 🔴 The trust grant here is narrow and worth naming. A caller who can reach this endpoint can create
	// a NEW organization for an identity it asserts — it cannot reach an existing one, cannot name a
	// tenant, and cannot widen anything: the tenant id is minted server-side and the caller never
	// supplies it. That is a different grant from the one ADR-008 Rule 2 forbids, which is a request
	// naming an authority it already wants. Narrowing it further to a dedicated credential is a
	// follow-up, and it is recorded rather than assumed away.
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
	Email   string `json:"email"`
}

func (s *Server) handleCreateOrganization(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff,
			"this deployment does not mount the account system")
		return
	}
	if _, ok := auth.PrincipalFrom(r.Context()); !ok {
		refuse(w, http.StatusUnauthorized, "", "authentication required")
		return
	}
	// 🔴 The posture is checked BEFORE the body is read. A deployment with self-serve off must behave as
	// though the capability does not exist, and parsing first would leak that it does through timing and
	// through validation errors.
	if !s.accounts.SelfServeEnabled() || s.accounts.SignUp() == nil {
		refuse(w, http.StatusForbidden, ReasonSelfServeDisabled,
			"this deployment does not create organizations on request")
		return
	}

	var req createOrganizationRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		refuse(w, http.StatusBadRequest, "", "the request is not valid: "+err.Error())
		return
	}

	res, err := s.accounts.SignUp().Create(r.Context(), signup.Request{
		Name: req.Name, Issuer: req.Issuer, Subject: req.Subject, Email: req.Email,
	})
	switch {
	case errors.Is(err, signup.ErrNameRequired):
		refuse(w, http.StatusBadRequest, "", "an organization needs a name")
		return
	case errors.Is(err, signup.ErrNoFreePlan):
		// A configuration mistake on our side, said plainly rather than as a 500: the deployment's
		// catalog has no plan that charges nothing, and a new customer cannot be started on one that does.
		refuse(w, http.StatusServiceUnavailable, "", "this deployment has no free plan to start on")
		return
	case err != nil:
		refuse(w, http.StatusInternalServerError, "", "the organization could not be created")
		return
	}
	writeJSON(w, http.StatusCreated, organizationView(res))
}

func organizationView(res signup.Result) map[string]any {
	return map[string]any{
		"organization": map[string]any{
			"id":         res.Organization.Tenant.TenantID,
			"name":       res.Organization.Tenant.Name,
			"created_at": res.Organization.Tenant.CreatedAt,
		},
		"owner": map[string]any{
			"user_id": res.Organization.Owner.UserID,
			"email":   res.Organization.Owner.Email,
			"role":    string(res.Organization.Membership.Role),
		},
		"plan": map[string]any{
			"id":      res.Account.ActivePlanID,
			"charges": res.Account.PlanCharges,
		},
	}
}

// ── the organization the caller is acting as ────────────────────────────────────────────────────────

func (s *Server) handleGetOrganization(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	t, err := store.GetTenant(p.TenantID)
	if err != nil {
		refuse(w, http.StatusNotFound, "", "no such organization")
		return
	}
	current, err := seatsCurrent(store, p.TenantID)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the member list could not be read")
		return
	}
	allowed, hasLimit, err := s.accounts.SeatsAllowed(p.TenantID)
	if err != nil {
		// A plan that cannot be resolved is not a seat count of zero. Saying so keeps the two apart.
		hasLimit = false
	}
	body := map[string]any{
		"id":         t.TenantID,
		"name":       t.Name,
		"status":     string(t.Status),
		"created_at": t.CreatedAt,
		// 🔴 The VIEWER's own role, so a surface can decide which controls to render rather than
		// rendering them all and letting the platform refuse.
		//
		// A control that is visible, pressable and always refused is a silent dead write: the person
		// learns the product is broken rather than that the action is not theirs. The platform still
		// refuses — this is what the screen needs to avoid ASKING.
		//
		// Absent for a machine credential, which has no role in an organization. Absent rather than
		// "member": a default here would show a CI key the member controls and let it try them.
		"your_role": viewerRole(store, p),
		// 🔴 Two seat numbers, two names, always labelled. `seats_current` is a STATE read from
		// membership; the period's billed peak is a different question with a different answer and it is
		// deliberately not in this response.
		"seats_current": current,
	}
	if hasLimit {
		body["seats_allowed"] = allowed
	} else {
		// Absent rather than a large number: "unlimited" and "a big allowance" are different answers and
		// a renderer must not have to guess which it received.
		body["seats_unlimited"] = true
	}
	writeJSON(w, http.StatusOK, body)
}

// seatsCurrent counts the seats an organization holds now.
//
// It delegates to `internal/seats`, which owns the definition — including the one undecided part of it
// (whether a member who never opens the console occupies a seat, PRD Open Question 3). Counting here
// too would be a second definition, and the two would answer differently the moment the question is
// settled in one of them.
func seatsCurrent(store tenancy.Store, tenantID string) (int, error) {
	return seats.Current(store, tenantID)
}

// ── members ─────────────────────────────────────────────────────────────────────────────────────────

func memberView(store tenancy.Store, m tenancy.Membership) map[string]any {
	view := map[string]any{
		"user_id":   m.UserID,
		"role":      string(m.Role),
		"status":    string(m.Status),
		"joined_at": m.JoinedAt,
	}
	if u, err := store.GetUser(m.UserID); err == nil {
		view["email"] = u.Email
	}
	return view
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	if _, ok := s.actingMember(w, p, store, nil); !ok {
		return
	}
	members, err := store.ListMembers(p.TenantID)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the member list could not be read")
		return
	}
	out := make([]map[string]any, 0, len(members))
	for _, m := range members {
		out = append(out, memberView(store, m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (s *Server) handleSetRole(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	acting, ok := s.actingMember(w, p, store, tenancy.Role.CanAdminister)
	if !ok {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		refuse(w, http.StatusBadRequest, "", "the request is not valid")
		return
	}
	role := tenancy.Role(strings.TrimSpace(req.Role))
	if !tenancy.KnownRole(role) {
		refuse(w, http.StatusBadRequest, "", "unknown role")
		return
	}
	target := r.PathValue("user_id")
	// An admin may not promote somebody to owner, and may not touch an owner. Ownership is a financial
	// authority — the owner is who may upgrade — so it moves only by an owner's hand.
	if acting.Role != tenancy.RoleOwner {
		existing, err := store.GetMembership(target, p.TenantID)
		if err == nil && existing.Role == tenancy.RoleOwner {
			refuse(w, http.StatusForbidden, ReasonForbiddenRole, "only an owner may change an owner's role")
			return
		}
		if role == tenancy.RoleOwner {
			refuse(w, http.StatusForbidden, ReasonForbiddenRole, "only an owner may make somebody an owner")
			return
		}
	}
	m, err := store.SetRole(target, p.TenantID, role)
	switch {
	case errors.Is(err, tenancy.ErrLastOwner):
		refuse(w, http.StatusConflict, ReasonLastOwner,
			"this organization would be left with no owner, and nobody could restore it")
		return
	case errors.Is(err, tenancy.ErrNotFound):
		refuse(w, http.StatusNotFound, "", "no such member")
		return
	case err != nil:
		refuse(w, http.StatusInternalServerError, "", "the role could not be changed")
		return
	}
	writeJSON(w, http.StatusOK, memberView(store, m))
}

func (s *Server) handleRemovalPreview(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	if _, ok := s.actingMember(w, p, store, tenancy.Role.CanAdminister); !ok {
		return
	}
	prev, err := store.PreviewRemoval(r.PathValue("user_id"), p.TenantID)
	if err != nil {
		refuse(w, http.StatusNotFound, "", "no such member")
		return
	}
	writeJSON(w, http.StatusOK, previewView(prev))
}

// previewView renders BOTH halves of a removal. The second half is the one that matters: an offboarding
// screen listing what it revokes and hiding what it leaves running is worse than no screen, because the
// person confirming it signs an attestation that is wrong.
func previewView(prev tenancy.RemovalPreview) map[string]any {
	return map[string]any{
		"user_id":              prev.UserID,
		"email":                prev.Email,
		"last_owner":           prev.LastOwner,
		"sessions_to_revoke":   prev.Sessions,
		"credentials_revoked":  credentialViews(prev.PersonalCredentials),
		"credentials_retained": credentialViews(prev.MachineCredentials),
	}
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	acting, ok := s.actingMember(w, p, store, tenancy.Role.CanAdminister)
	if !ok {
		return
	}
	target := r.PathValue("user_id")
	if acting.Role != tenancy.RoleOwner {
		if existing, err := store.GetMembership(target, p.TenantID); err == nil && existing.Role == tenancy.RoleOwner {
			refuse(w, http.StatusForbidden, ReasonForbiddenRole, "only an owner may remove an owner")
			return
		}
	}
	res, err := store.RemoveMember(target, p.TenantID, s.accounts.Now())
	switch {
	case errors.Is(err, tenancy.ErrLastOwner):
		refuse(w, http.StatusConflict, ReasonLastOwner,
			"this organization would be left with no owner, and nobody could restore it")
		return
	case errors.Is(err, tenancy.ErrNotFound):
		refuse(w, http.StatusNotFound, "", "no such member")
		return
	case err != nil:
		refuse(w, http.StatusInternalServerError, "", "the member could not be removed")
		return
	}
	// 🔴 Attribution, and the boundary it stops at.
	//
	// The acting person is recorded here and in the structured log. It is deliberately NOT written to
	// `audit_entry`: that chain's actor column is `actor_admin_id`, an OPERATOR principal, and P8 FR1
	// makes the two identity domains categorically disjoint — an operator is not a user and a user must
	// not appear in the operator's chain. Writing one there would be the first join between the two
	// halves that migration 0038's header says must never exist.
	//
	// A durable, customer-facing audit trail is therefore a NAMED FOLLOW-UP rather than something this
	// phase quietly borrowed a table for. What P27 delivers is attribution at the point of action, which
	// is what makes "who removed this person" answerable at all — before P27 the platform could not name
	// the actor, because there were no users.
	// The membership change is the event; re-deriving the period's peak is the reconciliation read.
	// It runs AFTER the removal committed, so a peak recorded here can never describe a removal that
	// did not happen.
	s.accounts.ObserveSeats(p.TenantID)

	logMemberRemoval(p.TenantID, p.UserID, target, res.SessionsRevoked, res.CredentialsRevoked)

	// The two numbers are reported rather than "removed": an operator reading a log needs to know how
	// much actually stopped working, and "we removed them" is compatible with revoking nothing.
	writeJSON(w, http.StatusOK, map[string]any{
		"removed_by":                     p.UserID,
		"user_id":                        res.Membership.UserID,
		"status":                         string(res.Membership.Status),
		"sessions_revoked":               res.SessionsRevoked,
		"credentials_revoked":            res.CredentialsRevoked,
		"machine_credentials_unaffected": res.MachineCredsUnknown,
	})
}

// ── invitations ─────────────────────────────────────────────────────────────────────────────────────

// InvitationTTL bounds how long an invitation can be accepted for.
//
// Seven days: long enough to survive a holiday, short enough that a link forwarded into a group chat
// stops working before anybody has forgotten it is there. A standing offer in an inbox is a way in that
// nobody is tracking.
const InvitationTTL = 7 * 24 * time.Hour

func invitationView(i tenancy.Invitation, now time.Time) map[string]any {
	state := "pending"
	switch {
	case i.AcceptedAt != nil:
		state = "accepted"
	case i.RevokedAt != nil:
		state = "revoked"
	case !now.Before(i.ExpiresAt):
		// 🔴 Expired is its OWN state, not a variant of pending. They need different copy and different
		// next actions — one says "waiting for them", the other says "send it again".
		state = "expired"
	}
	return map[string]any{
		"invitation_id": i.InvitationID,
		"email":         i.Email,
		"role":          string(i.Role),
		"state":         state,
		"created_at":    i.CreatedAt,
		"expires_at":    i.ExpiresAt,
	}
}

func (s *Server) handleListInvitations(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	if _, ok := s.actingMember(w, p, store, tenancy.Role.CanInvite); !ok {
		return
	}
	list, err := store.ListInvitations(p.TenantID)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the invitations could not be read")
		return
	}
	now := s.accounts.Now()
	out := make([]map[string]any, 0, len(list))
	for _, i := range list {
		out = append(out, invitationView(i, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"invitations": out})
}

func (s *Server) handleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	acting, ok := s.actingMember(w, p, store, tenancy.Role.CanInvite)
	if !ok {
		return
	}
	// 🔴 P28: an unconfirmed address may not send mail to a third party under our name.
	//
	// This is one of exactly TWO actions confirmation gates (the other is moving to a plan that charges);
	// see ADR-012 Decision 7 for why sign-in itself is not one of them. Both gates exist because both spend
	// something the account has not proved it owns — an invitation puts a message in somebody else's inbox
	// with our SPF record on it, which is what an unverified account would otherwise turn this product into.
	if !s.confirmedAddress(w, store, p.UserID, "inviting people") {
		return
	}
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		refuse(w, http.StatusBadRequest, "", "the request is not valid")
		return
	}
	role := tenancy.Role(strings.TrimSpace(req.Role))
	if role == "" {
		role = tenancy.RoleMember
	}
	if !tenancy.KnownRole(role) {
		refuse(w, http.StatusBadRequest, "", "unknown role")
		return
	}
	if acting.Role != tenancy.RoleOwner && role == tenancy.RoleOwner {
		refuse(w, http.StatusForbidden, ReasonForbiddenRole, "only an owner may invite an owner")
		return
	}
	email := tenancy.NormalizeEmail(req.Email)
	if email == "" || !strings.Contains(email, "@") {
		refuse(w, http.StatusBadRequest, "", "an invitation needs an email address")
		return
	}

	// 🔴 The seat check happens HERE as well as at acceptance, and pending invitations count.
	//
	// Checking only at acceptance means the owner sends five invitations against one free seat and four
	// colleagues get an error that is the owner's fault. The person who can fix the problem should be
	// the person who sees it.
	if code, msg, over := s.overSeatLimit(store, p.TenantID, true); over {
		refuse(w, http.StatusConflict, code, msg)
		return
	}

	now := s.accounts.Now()
	inv, err := store.CreateInvitation(tenancy.Invitation{
		InvitationID: tenancy.NewID("inv"),
		TenantID:     p.TenantID,
		Email:        email,
		Role:         role,
		InvitedBy:    p.UserID,
		CreatedAt:    now,
		ExpiresAt:    now.Add(InvitationTTL),
	})
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the invitation could not be created")
		return
	}
	writeJSON(w, http.StatusCreated, invitationView(inv, now))
}

// overSeatLimit reports whether one more seat would exceed the plan's allowance.
//
// 🔴 The message names BOTH numbers. "Seat limit reached" invites a support ticket asking what the limit
// is, and we already have the answer on the screen where the person can act on it.
func (s *Server) overSeatLimit(store tenancy.Store, tenantID string, countPending bool) (string, string, bool) {
	allowed, hasLimit, err := s.accounts.SeatsAllowed(tenantID)
	if err != nil || !hasLimit {
		// An unset allowance is UNLIMITED, never zero. Treating a resolution failure as zero would deny
		// every invitation on a deployment whose plan config is briefly unavailable.
		return "", "", false
	}
	current, err := seatsCurrent(store, tenantID)
	if err != nil {
		return "", "", false
	}
	used := current
	if countPending {
		invites, err := store.ListInvitations(tenantID)
		if err == nil {
			now := s.accounts.Now()
			for _, i := range invites {
				if i.Pending(now) {
					used++
				}
			}
		}
	}
	if float64(used) < allowed {
		return "", "", false
	}
	return ReasonSeatLimitReached, seatMessage(used, allowed), true
}

func seatMessage(used int, allowed float64) string {
	return "your plan includes " + trimFloat(allowed) + " seats and " + itoa(used) +
		" are in use — upgrade, or remove a member first"
}

// ── invitation acceptance ───────────────────────────────────────────────────────────────────────────

// handleAcceptInvitation creates a membership only when a VERIFIED identity matches the invitation.
//
// # 🔴 The acting person comes from the CREDENTIAL, and the request carries no identity at all
//
// The first version of this endpoint took `{issuer, subject, email}` in the body, forwarded by the
// console after it verified an assertion. That worked and it was a trust grant: the platform believed a
// body about who was acting.
//
// It is no longer needed, and removing it is a strengthening rather than a convenience. Signing in
// already resolves a person (`/api/v1/users/resolve`), and the console now presents a token scoped to
// that person — so the platform can read the actor from the thing it verified itself. The address the
// invitation is matched against is the one recorded on that person at sign-in, which came from an
// assertion; it is the same verified address, read from a row instead of from a request.
//
// What that buys: there is no field on this request an address could arrive in, so "the address in the
// request is never the address that matters" stops being a rule and becomes a shape.
//
// # The link is still not a credential
//
// Somebody holding an invitation id whose verified address is different gets nothing — no membership,
// and a recorded security event. That is the whole difference between "pre-filled" and "admitted".
func (s *Server) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		refuse(w, http.StatusServiceUnavailable, ReasonAccountSystemOff, "this deployment does not mount the account system")
		return
	}
	p, ok := auth.PrincipalFrom(r.Context())
	if !ok || p.TenantID == "" {
		refuse(w, http.StatusUnauthorized, "", "authentication required")
		return
	}
	if p.UserID == "" {
		// A MACHINE credential cannot accept an invitation on anybody's behalf. An invitation admits a
		// PERSON, and a credential that names none has nobody to admit.
		refuse(w, http.StatusForbidden, ReasonNotAMember,
			"accepting an invitation needs a signed-in person; a machine credential names nobody")
		return
	}
	store := s.accounts.Store()

	user, err := store.GetUser(p.UserID)
	if err != nil {
		refuse(w, http.StatusForbidden, ReasonNotAMember, "no such person")
		return
	}

	id := r.PathValue("invitation_id")
	inv, err := store.GetInvitation(id)
	if err != nil {
		refuse(w, http.StatusNotFound, "", "no such invitation")
		return
	}
	now := s.accounts.Now()
	if !inv.Pending(now) {
		refuse(w, http.StatusConflict, ReasonInvitationExpired, "that invitation is no longer valid")
		return
	}
	if tenancy.NormalizeEmail(user.Email) != inv.Email {
		// A security event, recorded server-side. The response says the invitation is not for this
		// account and nothing about whose it is.
		s.logSecurityEvent(r.Context(), "invitation_identity_mismatch", inv.TenantID)
		refuse(w, http.StatusForbidden, ReasonInvitationMismatch,
			"that invitation was issued to a different account")
		return
	}
	if code, msg, over := s.overSeatLimit(store, inv.TenantID, false); over {
		refuse(w, http.StatusConflict, code, msg)
		return
	}

	// Stamp the invitation FIRST. It is single-use at the store, so if two acceptances race, exactly one
	// gets past this line and the other is refused — whereas creating the membership first would let
	// both create one.
	if _, err := store.AcceptInvitation(id, now); err != nil {
		refuse(w, http.StatusConflict, ReasonInvitationExpired, "that invitation is no longer valid")
		return
	}
	m, err := store.PutMembership(tenancy.Membership{
		UserID: user.UserID, TenantID: inv.TenantID, Role: inv.Role,
		Status: tenancy.MemberActive, InvitedBy: inv.InvitedBy, JoinedAt: now,
	})
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the membership could not be created")
		return
	}

	// A new member is a seat taken. Re-derive the period's peak from the timeline the membership just
	// joined.
	s.accounts.ObserveSeats(inv.TenantID)

	writeJSON(w, http.StatusOK, map[string]any{
		"organization_id": inv.TenantID,
		"user_id":         user.UserID,
		"role":            string(m.Role),
	})
}

func (s *Server) handleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	if _, ok := s.actingMember(w, p, store, tenancy.Role.CanInvite); !ok {
		return
	}
	inv, err := store.GetInvitation(r.PathValue("invitation_id"))
	if err != nil || inv.TenantID != p.TenantID {
		// Another organization's invitation is indistinguishable from one that does not exist.
		refuse(w, http.StatusNotFound, "", "no such invitation")
		return
	}
	out, err := store.RevokeInvitation(inv.InvitationID, s.accounts.Now())
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the invitation could not be revoked")
		return
	}
	writeJSON(w, http.StatusOK, invitationView(out, s.accounts.Now()))
}

// ── credentials ─────────────────────────────────────────────────────────────────────────────────────

func credentialViews(cs []tenancy.Credential) []map[string]any {
	out := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		view := map[string]any{
			"credential_id": c.CredentialID,
			"label":         c.Label,
			// 🔴 "personal" or "machine", as a WORD. The difference decides what member removal covers,
			// and a reader must not have to infer it from a blank column.
			"kind":       c.Kind(),
			"role":       string(c.Role),
			"created_at": c.CreatedAt,
			"revoked":    c.Revoked(),
		}
		if c.Personal() {
			view["user_id"] = c.UserID
		}
		out = append(out, view)
	}
	return out
}

func (s *Server) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	if _, ok := s.actingMember(w, p, store, tenancy.Role.CanAdminister); !ok {
		return
	}
	creds, err := store.ListCredentials(p.TenantID)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the credentials could not be read")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": credentialViews(creds)})
}

// handleCreateCredential is the ONLY response in this package that carries a secret.
func (s *Server) handleCreateCredential(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	acting, ok := s.actingMember(w, p, store, tenancy.Role.CanAdminister)
	if !ok {
		return
	}
	var req struct {
		Label string `json:"label"`
		// Kind is "personal" or "machine". It is asked for rather than inferred, because the difference
		// decides whether removing this person revokes the key — and a default would make that decision
		// silently.
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		refuse(w, http.StatusBadRequest, "", "the request is not valid")
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		refuse(w, http.StatusBadRequest, "", "a credential needs a label — a list of opaque ids is a list where the wrong key gets revoked")
		return
	}
	userID := p.UserID
	if strings.EqualFold(strings.TrimSpace(req.Kind), "machine") {
		userID = ""
	}

	secret, err := tenancy.NewCredentialSecret()
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the credential could not be created")
		return
	}
	c, err := store.CreateCredential(tenancy.Credential{
		CredentialID: tenancy.NewID("cred"),
		TenantID:     p.TenantID,
		UserID:       userID,
		Label:        label,
		Role:         acting.Role,
		Hash:         tenancy.HashSecret(secret),
		CreatedAt:    s.accounts.Now(),
	})
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the credential could not be created")
		return
	}
	view := credentialViews([]tenancy.Credential{c})[0]
	// Once. This is the only moment the plaintext exists anywhere.
	view["secret"] = secret
	view["secret_shown_once"] = true
	writeJSON(w, http.StatusCreated, view)
}

func (s *Server) handleRevokeCredential(w http.ResponseWriter, r *http.Request) {
	p, store, ok := s.principalAndStore(w, r)
	if !ok {
		return
	}
	if _, ok := s.actingMember(w, p, store, tenancy.Role.CanAdminister); !ok {
		return
	}
	creds, err := store.ListCredentials(p.TenantID)
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the credentials could not be read")
		return
	}
	id := r.PathValue("credential_id")
	found := false
	for _, c := range creds {
		if c.CredentialID == id {
			found = true
			break
		}
	}
	if !found {
		// Another organization's credential is indistinguishable from one that does not exist.
		refuse(w, http.StatusNotFound, "", "no such credential")
		return
	}
	c, err := store.RevokeCredential(id, s.accounts.Now())
	if err != nil {
		refuse(w, http.StatusInternalServerError, "", "the credential could not be revoked")
		return
	}
	writeJSON(w, http.StatusOK, credentialViews([]tenancy.Credential{c})[0])
}

// logSecurityEvent records a refusal an operator should be able to read. It carries the ORGANIZATION and
// the event name and nothing about the identity that was presented — the cause belongs in a log an
// operator can read, and an attacker must not learn which half they got wrong.
func (s *Server) logSecurityEvent(_ context.Context, event, tenantID string) {
	logSecurity(event, tenantID)
}

// viewerRole is the acting person's role in this organization, or "" for a machine credential.
//
// It is read from MEMBERSHIP rather than taken from the principal's `Role` field. Those are two
// different things and only one of them is authoritative: a credential's role is what it was minted
// with, and a membership's role is what the person is NOW. An owner demoted this morning must not still
// see owner controls because their key remembers otherwise.
func viewerRole(store tenancy.Store, p auth.Principal) string {
	if p.UserID == "" {
		return ""
	}
	m, err := store.GetMembership(p.UserID, p.TenantID)
	if err != nil || !m.Active() {
		return ""
	}
	return string(m.Role)
}
