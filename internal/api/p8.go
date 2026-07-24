package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// p8.go is the operator console's API — and it is deliberately NOT mounted on Server.
//
// # Why this is a separate handler rather than a route group
//
// P8 design Decision 11: the operator console is a separate application on a separate ORIGIN, so that
// the isolation between a tenant session and a cross-tenant capability is enforced by the browser
// (separate cookie jars, separate bundles) rather than by our routing being correct. That decision
// only holds if the SERVER side is separate too — an admin route group inside the customer server
// would put both behind one origin and give the routing back its power to be wrong.
//
// So: NewAdminAPI returns its own http.Handler, served on its own listener, and nothing in server.go
// references it. A customer request cannot reach an admin route because the admin routes are not in
// the customer server's mux at all.
//
// # Two credentials, two purposes
//
// Every admin request presents BOTH:
//
//   - the PLATFORM CREDENTIAL, proving the caller is the admin BFF. It lives server-side in the BFF
//     and never reaches a browser, a bundle, a URL or a log.
//   - the ADMIN SESSION token, proving WHICH admin principal is acting. The browser holds this one, in
//     an HttpOnly SameSite cookie the BFF sets; page script cannot read it.
//
// Neither alone is sufficient. The platform credential without a session identifies no actor, so
// nothing can be audited to anybody; a session without the platform credential means the request did
// not come through the admin BFF at all.

// Admin API header names. Constants, because a header spelled two ways is an auth check that never
// fires.
const (
	// HeaderPlatformCredential carries the BFF's server-side platform credential.
	HeaderPlatformCredential = "X-Admin-Platform-Credential"
	// HeaderAdminSession carries the admin session token the BFF holds in its HttpOnly cookie.
	HeaderAdminSession = "X-Admin-Session"
)

// AdminDeps is everything the admin API serves. Every field is required except where noted: an admin
// API missing a service would 404 a capability the console renders, which is the screen and the gate
// disagreeing (FR22).
type AdminDeps struct {
	// PlatformCredential is the shared secret the BFF presents. Sourced from the secrets manager by
	// the caller; this struct holds it only for the duration of the process.
	PlatformCredential string

	Authenticator *adminidentity.Authenticator
	Sessions      *adminidentity.SessionStore
	Gate          *adminrbac.Gate
	Executor      *adminops.Executor

	Tenants       *adminops.TenantService
	Entitlements  *adminops.EntitlementService
	Billing       *adminops.BillingService
	Registry      *adminops.RegistryService
	Jobs          *adminops.JobService
	KillSwitch    *adminops.KillSwitchService
	Impersonation *adminops.ImpersonationService
	CrossTenant   *adminops.CrossTenantService
	Audit         *adminops.AuditService
	GDPR          *adminops.GDPRService

	// TestModeIdP, when set, is the fixture admin IdP the sign-in page's test-mode control uses. It is
	// present ONLY in a test-mode deployment (rollout 8a). Its assertions are signed with the real
	// admin IdP keys and verified by the real verifier — including MFA — so it is a test-mode ISSUER,
	// never an MFA bypass. A production deployment leaves it nil and the /testmode/assert route 404s.
	TestModeIdP *adminidentity.IdPFixture

	// Rollout is the P8 rollout state, reported on the readiness surface. Nil serves everything (a
	// self-hosted deployment with no waved rollout); a wired one reports which waves are live.
	Rollout *adminops.Rollout
	// Secrets names the live secrets source, reported on /readyz — the DOOR, never a credential.
	Secrets providergateway.Secrets

	// Now supplies the clock for period resolution. Nil uses time.Now().UTC().
	Now func() time.Time
}

// AdminAPI is the operator console's HTTP surface.
type AdminAPI struct {
	deps AdminDeps
	mux  *http.ServeMux
	// Handler is the composed handler, credential gate included.
	Handler http.Handler
}

// NewAdminAPI builds the admin surface.
func NewAdminAPI(deps AdminDeps) (*AdminAPI, error) {
	if strings.TrimSpace(deps.PlatformCredential) == "" {
		return nil, errors.New("api: the admin API requires a platform credential — an unauthenticated operator surface is the prod-shell anti-pattern P8 exists to remove")
	}
	if deps.Authenticator == nil || deps.Sessions == nil || deps.Gate == nil || deps.Executor == nil {
		return nil, errors.New("api: the admin API requires the identity, authorization and command layers")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	a := &AdminAPI{deps: deps, mux: http.NewServeMux()}
	a.routes()
	a.Handler = a.requirePlatformCredential(a.mux)
	return a, nil
}

// requirePlatformCredential refuses anything that did not come through the admin BFF.
//
// It runs BEFORE routing, so an unknown path from an unauthenticated caller is refused rather than
// 404'd — a 404 would confirm which admin paths exist.
func (a *AdminAPI) requirePlatformCredential(next http.Handler) http.Handler {
	want := []byte(a.deps.PlatformCredential)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/api/healthz" {
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
			return
		}
		if r.URL.Path == "/admin/api/readyz" {
			// The readiness surface: which waves are live, which secrets source is in use, whether the
			// admin IdP is in test mode. Words only — never a credential (task 13.2). Open like healthz so
			// a load balancer can probe it.
			body := map[string]any{"status": "ready", "surface": "p8-admin-console"}
			body["admin_idp"] = a.deps.Authenticator.Describe()
			if a.deps.Rollout != nil {
				body["rollout"] = a.deps.Rollout.Describe()
			}
			if a.deps.Secrets != nil {
				body["secrets_source"] = a.deps.Secrets.Describe()
			}
			writeJSON(w, http.StatusOK, body)
			return
		}
		got := []byte(r.Header.Get(HeaderPlatformCredential))
		if len(got) == 0 || subtle.ConstantTimeCompare(got, want) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":  "admin_bff_credential_required",
				"detail": "this request did not come through the admin backend-for-frontend",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *AdminAPI) routes() {
	m := a.mux
	m.HandleFunc("POST /admin/api/session", a.handleLogin)
	m.HandleFunc("DELETE /admin/api/session", a.handleLogout)
	m.HandleFunc("POST /admin/api/testmode/assert", a.handleTestModeAssert)
	m.HandleFunc("GET /admin/api/me", a.session(a.handleMe))

	m.HandleFunc("GET /admin/api/tenants", a.session(a.handleTenants))
	m.HandleFunc("GET /admin/api/tenants/{id}", a.session(a.handleTenant))
	m.HandleFunc("POST /admin/api/tenants/{id}/suspend", a.session(a.handleSuspend))
	m.HandleFunc("POST /admin/api/tenants/{id}/reactivate", a.session(a.handleReactivate))
	m.HandleFunc("POST /admin/api/tenants/{id}/quota", a.session(a.handleQuota))
	m.HandleFunc("POST /admin/api/tenants/{id}/entitlement", a.session(a.handleOverride))

	m.HandleFunc("GET /admin/api/plans", a.session(a.handlePlans))
	m.HandleFunc("GET /admin/api/billing/{tenant}", a.session(a.handleBilling))
	m.HandleFunc("POST /admin/api/billing/{tenant}/credit", a.session(a.handleCredit))
	m.HandleFunc("POST /admin/api/billing/{tenant}/refund", a.session(a.handleRefund))

	m.HandleFunc("GET /admin/api/registry", a.session(a.handleRegistry))
	m.HandleFunc("POST /admin/api/registry/models", a.session(a.handleAddModel))
	m.HandleFunc("POST /admin/api/registry/models/{id}/deprecate", a.session(a.handleDeprecateModel))
	m.HandleFunc("POST /admin/api/registry/models/{id}/price-ref", a.session(a.handleRepointPrice))

	m.HandleFunc("GET /admin/api/jobs", a.session(a.handleJobs))
	m.HandleFunc("GET /admin/api/fleet", a.session(a.handleFleet))
	m.HandleFunc("POST /admin/api/jobs/{id}/retry", a.session(a.handleJobRetry))
	m.HandleFunc("POST /admin/api/jobs/{id}/cancel", a.session(a.handleJobCancel))

	m.HandleFunc("GET /admin/api/killswitch", a.session(a.handleKillSwitchState))
	m.HandleFunc("POST /admin/api/killswitch/arm", a.session(a.handleArm))
	m.HandleFunc("POST /admin/api/killswitch/disarm", a.session(a.handleDisarm))

	m.HandleFunc("GET /admin/api/impersonation", a.session(a.handleImpersonations))
	m.HandleFunc("POST /admin/api/impersonation", a.session(a.handleImpersonateStart))
	m.HandleFunc("POST /admin/api/impersonation/{id}/elevate", a.session(a.handleImpersonateElevate))
	m.HandleFunc("POST /admin/api/impersonation/{id}/end", a.session(a.handleImpersonateEnd))

	m.HandleFunc("GET /admin/api/crosstenant/{aggregate}", a.session(a.handleCrossTenant))
	m.HandleFunc("GET /admin/api/crosstenant/{aggregate}/{tenant}", a.session(a.handleDrillDown))

	m.HandleFunc("GET /admin/api/audit", a.session(a.handleAudit))
	m.HandleFunc("GET /admin/api/audit/verify", a.session(a.handleAuditVerify))

	m.HandleFunc("GET /admin/api/gdpr", a.session(a.handleGDPRList))
	m.HandleFunc("POST /admin/api/gdpr", a.session(a.handleGDPRRecord))
	m.HandleFunc("POST /admin/api/gdpr/execute", a.session(a.handleGDPRExecute))
	m.HandleFunc("GET /admin/api/gdpr/{id}/verify", a.session(a.handleGDPRVerify))
}

// session verifies the admin session token and installs it on the request context.
//
// Every capability route goes through it, so "no admin capability is reachable without a live admin
// session" is a property of the routing table rather than of each handler remembering.
func (a *AdminAPI) session(next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get(HeaderAdminSession)
		if token == "" {
			writeAdminError(w, http.StatusUnauthorized, "admin_session_required",
				"no admin session was presented", nil)
			return
		}
		sess, err := a.deps.Sessions.Authorize(r.Context(), token)
		if err != nil {
			// A tenant session presented here lands in exactly this branch: it does not verify against
			// the admin signing key and names no admin session, so it is refused (FR21).
			writeAdminError(w, http.StatusUnauthorized, "admin_session_invalid", err.Error(), nil)
			return
		}
		ctx := adminidentity.WithSession(r.Context(), sess)
		if impID := r.Header.Get("X-Admin-Impersonation"); impID != "" {
			ctx = adminops.WithImpersonation(ctx, impID)
		}
		next(w, r.WithContext(ctx))
	}
}

// ── Session ─────────────────────────────────────────────────────────────────────────────────────

func (a *AdminAPI) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Assertion adminidentity.Assertion `json:"assertion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	sess, token, err := a.deps.Authenticator.Authenticate(r.Context(), body.Assertion)
	if err != nil {
		// One status for every authentication failure: probing must not learn whether the subject
		// exists, whether MFA was the problem, or whether the principal is disabled.
		writeAdminError(w, http.StatusUnauthorized, "authentication_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_token": token,
		"admin_id":      sess.AdminID,
		"expires_at":    sess.ExpiresAt.UTC().Format(time.RFC3339),
		"mfa_factor":    sess.MFAFactor,
	})
}

// handleTestModeAssert mints a fixture SSO+MFA assertion for the sign-in page's test-mode control.
//
// Present only when a test-mode IdP is wired. It never issues a session — it produces the SAME signed
// assertion the real IdP would, which the caller then exchanges through the ordinary login path, MFA
// verification and all. A production deployment leaves TestModeIdP nil and this route 404s, so there
// is no test-mode surface in production to reach.
func (a *AdminAPI) handleTestModeAssert(w http.ResponseWriter, r *http.Request) {
	if a.deps.TestModeIdP == nil {
		writeAdminError(w, http.StatusNotFound, "not_test_mode",
			"this deployment's admin IdP is not in test mode — sign in through the identity provider", nil)
		return
	}
	var body struct {
		Subject string `json:"subject"`
		Factor  string `json:"factor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return
	}
	if body.Factor == "" {
		body.Factor = "webauthn"
	}
	assertion, err := a.deps.TestModeIdP.Assert(r.Context(), body.Subject, body.Factor)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "assert_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, assertion)
}

func (a *AdminAPI) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get(HeaderAdminSession)
	if token == "" {
		writeAdminError(w, http.StatusBadRequest, "bad_request", "no session to revoke", nil)
		return
	}
	sess, err := a.deps.Sessions.Authorize(r.Context(), token)
	if err != nil {
		// Already expired or revoked: idempotent success, because the caller's goal is "this session is
		// not usable", and it is not.
		writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
		return
	}
	if err := a.deps.Sessions.Revoke(sess.SessionID, sess.AdminID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "revoke_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

// AdminIdentityView is what the console renders its chrome and its role-scoped surface from.
type AdminIdentityView struct {
	AdminID string `json:"admin_id"`
	// Roles and Capabilities are resolved LIVE from the gate, so a revoked role disappears from the
	// screen on the next request rather than at the next login.
	Roles        []adminrbac.Role       `json:"roles"`
	Capabilities []adminrbac.Capability `json:"capabilities"`
	// PermissionMap is the WHOLE map the backend enforces. The console renders from it, so screen and
	// gate cannot disagree (FR22) — and a denial can name who holds a capability without a second call.
	PermissionMap adminrbac.PermissionMap `json:"permission_map"`
	// Capabilities metadata the console needs to scale friction without hardcoding it.
	Friction []CapabilityFriction `json:"friction"`
	// SessionExpiresAt lets the chrome show how long the operator has.
	SessionExpiresAt string `json:"session_expires_at"`
	// Console identifies this application in the operator chrome (FR23).
	Console string `json:"console"`
	// ActiveImpersonation is the operator's live impersonation session, if any — the persistent banner.
	ActiveImpersonation *ImpersonationView `json:"active_impersonation,omitempty"`
	// KillSwitchTwoPersonDisarm tells the console why the global control is higher-friction.
	KillSwitchTwoPersonDisarm bool `json:"kill_switch_two_person_disarm"`
	// MinimumCohort explains a suppressed aggregate without the client hardcoding the number.
	MinimumCohort int `json:"minimum_cohort"`
}

// CapabilityFriction is one capability's friction classification, served so the client cannot invent
// its own idea of which actions need a typed target.
type CapabilityFriction struct {
	Capability   adminrbac.Capability `json:"capability"`
	Description  string               `json:"description"`
	ReadOnly     bool                 `json:"read_only"`
	Irreversible bool                 `json:"irreversible"`
	TypedTarget  bool                 `json:"typed_target"`
	HeldBy       []adminrbac.Role     `json:"held_by"`
}

// ImpersonationView is the banner's model.
type ImpersonationView struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenant_id"`
	Scope            string `json:"scope"`
	Reason           string `json:"reason"`
	ExpiresAt        string `json:"expires_at"`
	RemainingSeconds int    `json:"remaining_seconds"`
	Banner           string `json:"banner"`
}

func (a *AdminAPI) handleMe(w http.ResponseWriter, r *http.Request) {
	sess, err := adminidentity.RequireSession(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusUnauthorized, "admin_session_required", err.Error(), nil)
		return
	}
	view := AdminIdentityView{
		AdminID:          sess.AdminID,
		Roles:            a.deps.Gate.LiveRoles(sess.AdminID),
		Capabilities:     a.deps.Gate.CapabilitiesFor(sess.AdminID),
		PermissionMap:    adminrbac.FullPermissionMap(),
		SessionExpiresAt: sess.ExpiresAt.UTC().Format(time.RFC3339),
		Console:          "Operator Console",
	}
	for _, c := range adminrbac.Capabilities {
		view.Friction = append(view.Friction, CapabilityFriction{
			Capability: c, Description: c.Description(), ReadOnly: c.ReadOnly(),
			Irreversible: c.Irreversible(), TypedTarget: c.RequiresTypedTarget(), HeldBy: adminrbac.HoldersOf(c),
		})
	}
	if a.deps.KillSwitch != nil {
		view.KillSwitchTwoPersonDisarm = a.deps.KillSwitch.Policy().GlobalDisarmRequiresTwoPerson
	}
	if a.deps.CrossTenant != nil {
		view.MinimumCohort = a.deps.CrossTenant.MinimumCohort()
	}
	if a.deps.Impersonation != nil {
		if active, err := a.deps.Impersonation.Active(r.Context()); err == nil && len(active) > 0 {
			s := active[0]
			now := a.deps.Now()
			view.ActiveImpersonation = &ImpersonationView{
				ID: s.ID, TenantID: s.TenantID, Scope: string(s.Scope), Reason: s.Reason,
				ExpiresAt:        s.ExpiresAt.UTC().Format(time.RFC3339),
				RemainingSeconds: s.RemainingSeconds(now), Banner: s.BannerText(now),
			}
		}
	}
	writeJSON(w, http.StatusOK, view)
}

// ── Tenants ─────────────────────────────────────────────────────────────────────────────────────

func (a *AdminAPI) handleTenants(w http.ResponseWriter, r *http.Request) {
	views, err := a.deps.Tenants.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": views})
}

func (a *AdminAPI) handleTenant(w http.ResponseWriter, r *http.Request) {
	view, err := a.deps.Tenants.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// commandBody is the shape every state-changing admin request takes: a reason, a confirmation, and —
// for a typed-target action — what the operator typed.
type commandBody struct {
	Reason         string  `json:"reason"`
	Confirmed      bool    `json:"confirmed"`
	TypedTarget    string  `json:"typed_target,omitempty"`
	PlanRef        string  `json:"plan_ref,omitempty"`
	Limit          string  `json:"limit,omitempty"`
	Value          float64 `json:"value,omitempty"`
	ClearValue     bool    `json:"clear_value,omitempty"`
	AgainstEventID string  `json:"against_event_id,omitempty"`
	Scope          string  `json:"scope,omitempty"`
	SecondApprover string  `json:"second_approver,omitempty"`
	TenantID       string  `json:"tenant_id,omitempty"`
	TTLSeconds     int     `json:"ttl_seconds,omitempty"`
	SubjectRef     string  `json:"subject_ref,omitempty"`
	ModelID        string  `json:"model_id,omitempty"`
	Provider       string  `json:"provider,omitempty"`
	PriceRef       string  `json:"price_ref,omitempty"`
}

func (c commandBody) confirmation() adminops.Confirmation {
	return adminops.Confirmation{Confirmed: c.Confirmed, TypedTarget: c.TypedTarget}
}

func decodeCommand(w http.ResponseWriter, r *http.Request) (commandBody, bool) {
	var body commandBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAdminError(w, http.StatusBadRequest, "bad_request", err.Error(), nil)
		return commandBody{}, false
	}
	return body, true
}

func (a *AdminAPI) handleSuspend(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	receipt, err := a.deps.Tenants.Suspend(r.Context(), r.PathValue("id"), body.Reason, body.confirmation())
	writeReceipt(w, receipt, err)
}

func (a *AdminAPI) handleReactivate(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	receipt, err := a.deps.Tenants.Reactivate(r.Context(), r.PathValue("id"), body.Reason, body.confirmation())
	writeReceipt(w, receipt, err)
}

func (a *AdminAPI) handleQuota(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	value := body.Value
	if body.ClearValue {
		value = mathNaN()
	}
	receipt, err := a.deps.Tenants.SetQuota(r.Context(), r.PathValue("id"), plancfg.Limit(body.Limit),
		value, body.Reason, body.confirmation())
	writeReceipt(w, receipt, err)
}

func (a *AdminAPI) handleOverride(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	receipt, err := a.deps.Entitlements.Override(r.Context(), r.PathValue("id"), body.PlanRef, body.Reason, body.confirmation())
	writeReceipt(w, receipt, err)
}

func (a *AdminAPI) handlePlans(w http.ResponseWriter, r *http.Request) {
	plans, err := a.deps.Entitlements.Plans(r.Context())
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": plans})
}

// ── Billing ─────────────────────────────────────────────────────────────────────────────────────

func (a *AdminAPI) handleBilling(w http.ResponseWriter, r *http.Request) {
	view, err := a.deps.Billing.Oversight(r.Context(), r.PathValue("tenant"), a.period(r))
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *AdminAPI) handleCredit(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	receipt, err := a.deps.Billing.IssueCredit(r.Context(), r.PathValue("tenant"), body.AgainstEventID, body.Reason, body.confirmation())
	writeReceipt(w, receipt, err)
}

func (a *AdminAPI) handleRefund(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	receipt, err := a.deps.Billing.IssueRefund(r.Context(), r.PathValue("tenant"), body.AgainstEventID, body.Reason, body.confirmation())
	writeReceipt(w, receipt, err)
}

// ── Registry ────────────────────────────────────────────────────────────────────────────────────

func (a *AdminAPI) handleRegistry(w http.ResponseWriter, r *http.Request) {
	models, err := a.deps.Registry.List(r.Context())
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (a *AdminAPI) handleAddModel(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	receipt, err := a.deps.Registry.AddModel(r.Context(), body.ModelID, body.Provider, body.PriceRef, body.Reason, body.confirmation())
	writeReceipt(w, receipt, err)
}

func (a *AdminAPI) handleDeprecateModel(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	receipt, err := a.deps.Registry.DeprecateModel(r.Context(), r.PathValue("id"), body.Reason, body.confirmation())
	writeReceipt(w, receipt, err)
}

func (a *AdminAPI) handleRepointPrice(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	receipt, err := a.deps.Registry.RepointPriceRef(r.Context(), r.PathValue("id"), body.PriceRef, body.Reason, body.confirmation())
	writeReceipt(w, receipt, err)
}

// ── Jobs & fleet ────────────────────────────────────────────────────────────────────────────────

func (a *AdminAPI) handleJobs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	jobs, err := a.deps.Jobs.List(r.Context(), limit)
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (a *AdminAPI) handleFleet(w http.ResponseWriter, r *http.Request) {
	health, err := a.deps.Jobs.Health(r.Context())
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, health)
}

func (a *AdminAPI) handleJobRetry(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	receipt, err := a.deps.Jobs.Retry(r.Context(), r.PathValue("id"), body.Reason, body.confirmation())
	writeReceipt(w, receipt, err)
}

func (a *AdminAPI) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	receipt, err := a.deps.Jobs.Cancel(r.Context(), r.PathValue("id"), body.Reason, body.confirmation())
	writeReceipt(w, receipt, err)
}

// ── Kill switch ─────────────────────────────────────────────────────────────────────────────────

func (a *AdminAPI) handleKillSwitchState(w http.ResponseWriter, r *http.Request) {
	states, err := a.deps.KillSwitch.States(r.Context())
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	global, gerr := a.deps.KillSwitch.State(r.Context(), adminops.ScopeGlobal)
	body := map[string]any{
		"states": states,
		"policy": map[string]any{
			"global_disarm_requires_two_person": a.deps.KillSwitch.Policy().GlobalDisarmRequiresTwoPerson,
		},
	}
	if gerr != nil {
		// Indeterminate state is reported as DEGRADED, never as "not armed" — the console must never
		// render a fleet as running when we cannot tell.
		body["degraded"] = true
		body["detail"] = gerr.Error()
	} else {
		body["global"] = global
	}
	writeJSON(w, http.StatusOK, body)
}

func (a *AdminAPI) handleArm(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	receipt, err := a.deps.KillSwitch.Arm(r.Context(), body.Scope, body.Reason, body.confirmation())
	writeReceipt(w, receipt, err)
}

func (a *AdminAPI) handleDisarm(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	receipt, err := a.deps.KillSwitch.Disarm(r.Context(), body.Scope, body.Reason, body.confirmation(), body.SecondApprover)
	writeReceipt(w, receipt, err)
}

// ── Impersonation ───────────────────────────────────────────────────────────────────────────────

func (a *AdminAPI) handleImpersonations(w http.ResponseWriter, r *http.Request) {
	active, err := a.deps.Impersonation.Active(r.Context())
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	now := a.deps.Now()
	out := make([]ImpersonationView, 0, len(active))
	for _, s := range active {
		out = append(out, ImpersonationView{
			ID: s.ID, TenantID: s.TenantID, Scope: string(s.Scope), Reason: s.Reason,
			ExpiresAt:        s.ExpiresAt.UTC().Format(time.RFC3339),
			RemainingSeconds: s.RemainingSeconds(now), Banner: s.BannerText(now),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (a *AdminAPI) handleImpersonateStart(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	ttl := time.Duration(body.TTLSeconds) * time.Second
	sess, receipt, err := a.deps.Impersonation.Start(r.Context(), body.TenantID, body.Reason, ttl, body.confirmation())
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	now := a.deps.Now()
	writeJSON(w, http.StatusOK, map[string]any{
		"session": ImpersonationView{
			ID: sess.ID, TenantID: sess.TenantID, Scope: string(sess.Scope), Reason: sess.Reason,
			ExpiresAt:        sess.ExpiresAt.UTC().Format(time.RFC3339),
			RemainingSeconds: sess.RemainingSeconds(now), Banner: sess.BannerText(now),
		},
		"receipt": receipt,
	})
}

func (a *AdminAPI) handleImpersonateElevate(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	sess, receipt, err := a.deps.Impersonation.Elevate(r.Context(), r.PathValue("id"), body.Reason, body.confirmation())
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	now := a.deps.Now()
	writeJSON(w, http.StatusOK, map[string]any{
		"session": ImpersonationView{
			ID: sess.ID, TenantID: sess.TenantID, Scope: string(sess.Scope), Reason: sess.Reason,
			ExpiresAt:        sess.ExpiresAt.UTC().Format(time.RFC3339),
			RemainingSeconds: sess.RemainingSeconds(now), Banner: sess.BannerText(now),
		},
		"receipt": receipt,
	})
}

func (a *AdminAPI) handleImpersonateEnd(w http.ResponseWriter, r *http.Request) {
	body, _ := decodeCommand(w, r)
	receipt, err := a.deps.Impersonation.End(r.Context(), r.PathValue("id"), body.Reason)
	writeReceipt(w, receipt, err)
}

// ── Cross-tenant ────────────────────────────────────────────────────────────────────────────────

func (a *AdminAPI) handleCrossTenant(w http.ResponseWriter, r *http.Request) {
	model, err := a.deps.CrossTenant.View(r.Context(), adminops.Aggregate(r.PathValue("aggregate")), a.period(r))
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model)
}

func (a *AdminAPI) handleDrillDown(w http.ResponseWriter, r *http.Request) {
	model, err := a.deps.CrossTenant.DrillDown(r.Context(), r.PathValue("tenant"),
		adminops.Aggregate(r.PathValue("aggregate")), a.period(r))
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model)
}

// ── Audit ───────────────────────────────────────────────────────────────────────────────────────

func (a *AdminAPI) handleAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	view, err := a.deps.Audit.Entries(r.Context(), adminaudit.Filter{
		Actor: q.Get("actor"), Target: q.Get("target"), Action: adminaudit.Action(q.Get("action")),
	}, limit)
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *AdminAPI) handleAuditVerify(w http.ResponseWriter, r *http.Request) {
	v, err := a.deps.Audit.Verify(r.Context())
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// ── Compliance ──────────────────────────────────────────────────────────────────────────────────

func (a *AdminAPI) handleGDPRList(w http.ResponseWriter, r *http.Request) {
	reqs, err := a.deps.GDPR.List(r.Context())
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": reqs})
}

func (a *AdminAPI) handleGDPRRecord(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	req, err := a.deps.GDPR.Record(r.Context(), body.SubjectRef, body.Reason)
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (a *AdminAPI) handleGDPRExecute(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCommand(w, r)
	if !ok {
		return
	}
	req, receipt, err := a.deps.GDPR.Execute(r.Context(), body.SubjectRef, body.Reason, body.confirmation())
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": req, "receipt": receipt})
}

func (a *AdminAPI) handleGDPRVerify(w http.ResponseWriter, r *http.Request) {
	report, err := a.deps.GDPR.Verify(r.Context(), r.PathValue("id"))
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// ── Shared response shaping ─────────────────────────────────────────────────────────────────────

func (a *AdminAPI) period(r *http.Request) metering.Period {
	if id := r.URL.Query().Get("period"); id != "" {
		if t, err := time.Parse("2006-01", id); err == nil {
			return metering.MonthPeriod(t)
		}
	}
	return metering.MonthPeriod(a.deps.Now())
}

func writeReceipt(w http.ResponseWriter, receipt adminops.Receipt, err error) {
	if err != nil {
		writeCapabilityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

// adminErrorBody is the console's four-state contract in wire form.
//
// The KIND is what the client renders on: `denied` becomes a denial with an escalation path,
// `degraded` becomes a transport/subsystem failure, `friction` becomes "you still need a reason or a
// typed target". Collapsing them into one status code is exactly how a permission denial ends up
// rendered as an empty result (FR26).
type adminErrorBody struct {
	Error  string `json:"error"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	// HeldBy names the roles that hold the capability, for denied-with-escalation.
	HeldBy []string `json:"held_by,omitempty"`
}

// Error kinds. Central enum, matched by the client.
const (
	errKindDenied   = "denied"
	errKindFriction = "friction"
	errKindDegraded = "degraded"
	errKindAuth     = "auth"
	errKindRequest  = "request"
	// errKindNotFound is distinct from errKindRequest on purpose. "you sent something malformed" and
	// "the thing you named does not exist" send an operator to two different places, and collapsing
	// them into a 400 made a mistyped tenant id read on the console as a broken platform.
	errKindNotFound = "not_found"
)

func writeAdminError(w http.ResponseWriter, status int, code, detail string, heldBy []string) {
	kind := errKindDegraded
	switch status {
	case http.StatusUnauthorized:
		kind = errKindAuth
	case http.StatusForbidden:
		kind = errKindDenied
	case http.StatusBadRequest:
		kind = errKindRequest
	case http.StatusNotFound:
		kind = errKindNotFound
	case http.StatusPreconditionRequired:
		kind = errKindFriction
	}
	writeJSON(w, status, adminErrorBody{Error: code, Kind: kind, Detail: detail, HeldBy: heldBy})
}

// writeCapabilityError maps a service error onto the console's four states.
func writeCapabilityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, adminidentity.ErrNoAdminSession):
		writeAdminError(w, http.StatusUnauthorized, "admin_session_required", err.Error(), nil)
	case errors.Is(err, adminops.ErrDenied):
		writeAdminError(w, http.StatusForbidden, "denied", err.Error(), holdersFrom(err))
	case errors.Is(err, adminops.ErrNoReason),
		errors.Is(err, adminops.ErrNotConfirmed),
		errors.Is(err, adminops.ErrSecondConfirmation),
		errors.Is(err, adminops.ErrSecondApproverRequired),
		errors.Is(err, adminops.ErrImpersonationNoReason),
		errors.Is(err, adminops.ErrImpersonatedWrite):
		writeAdminError(w, http.StatusPreconditionRequired, "friction_required", err.Error(), nil)
	case errors.Is(err, account.ErrNotFound):
		// An identifier that does not resolve is a 404, never a 400. The console renders the two
		// differently — "check what you pasted" versus "the platform could not be reached" — and only
		// one of them is true here.
		writeAdminError(w, http.StatusNotFound, "not_found", err.Error(), nil)
	case errors.Is(err, adminaudit.ErrStoreUnavailable),
		errors.Is(err, adminops.ErrKillStateIndeterminate):
		// Fail-closed conditions are DEGRADED, not empty: the console says the subsystem is down.
		writeAdminError(w, http.StatusServiceUnavailable, "fail_closed", err.Error(), nil)
	default:
		writeAdminError(w, http.StatusBadRequest, "action_failed", err.Error(), nil)
	}
}

// holdersFrom extracts the escalation path a denial names, so the client renders who holds the
// capability rather than a bare refusal.
func holdersFrom(err error) []string {
	msg := err.Error()
	idx := strings.LastIndex(msg, "held by ")
	if idx < 0 {
		return nil
	}
	return strings.Split(msg[idx+len("held by "):], " or ")
}

// mathNaN returns a NaN without importing math into the handler bodies; the quota surface uses it to
// express "clear the override" rather than "set it to zero", which would mean an allowance of nothing.
func mathNaN() float64 {
	var zero float64
	return zero / zero
}

// Describe reports the admin API's live posture for an operator checking which surface is up. It
// names no credential.
func (a *AdminAPI) Describe() map[string]string {
	out := map[string]string{
		"surface":       "p8-admin-console",
		"admin_idp":     a.deps.Authenticator.Describe().Issuer,
		"idp_kind":      a.deps.Authenticator.Describe().Kind,
		"idp_test_mode": fmt.Sprint(a.deps.Authenticator.Describe().TestMode),
		"session_ttl":   a.deps.Sessions.TTL().String(),
	}
	return out
}

// AdminContextWithSession is a helper for in-process callers (the demo's headless mode) that need an
// authorized context without going through HTTP.
func AdminContextWithSession(ctx context.Context, sess adminidentity.Session) context.Context {
	return adminidentity.WithSession(ctx, sess)
}
