// Package adminrbac is P8's authorization gate: four least-privileged operator roles, a
// DENY-BY-DEFAULT capability map, and Superadmin-only, append-only, audited role grants
// (design Decision 2, FR3/FR4/FR5).
//
// # Deny by default is a structural property, not a habit
//
// The permission table below lists (role, capability) pairs that are ALLOWED. Anything absent is
// denied. That inverts the usual failure: adding a capability and forgetting to grant it makes the
// capability unreachable — annoying, and safe — rather than reachable by everyone. Capabilities is
// the complete enumeration, and the matrix test iterates THAT list rather than a hand-copied one, so
// a capability added without a considered grant is a failing test, not a silent hole.
//
// # Why the screen reads this same map
//
// The console renders capability from the map this gate enforces (FR22). Two copies of "who may do
// what" is the classic way a UI offers a button the backend refuses — or worse, hides one it would
// have allowed and nobody notices the gate is off. `PermissionMapFor(role)` is what the BFF serves to
// the browser, so screen and gate cannot disagree.
//
// # Why roles are resolved live on every call
//
// Grants are folded from the append-only grant log at authorization time, never cached on the session.
// A role revoked at 10:00 is gone at 10:00, not at the holder's next login — which is what makes
// revocation an incident response rather than a scheduled event.
package adminrbac

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
)

// Role is one operator role. Four, partitioning capability so no single persona can move money AND
// halt the fleet AND erase the record.
type Role string

const (
	// RoleSupport answers customer tickets: read tenants, read jobs/fleet, read-scoped impersonation.
	// It can neither bill nor destroy — the load-bearing least-privilege invariant (FR4).
	RoleSupport Role = "support"
	// RoleBillingOps runs the money surface: invoices, dunning, reconciliation, additive credits and
	// refunds, entitlement/plan overrides, and tenant suspension for non-payment.
	RoleBillingOps Role = "billing_ops"
	// RolePlatformSRE runs the machinery: jobs, the worker fleet, the model registry, and the kill
	// switch that halts autonomous merges.
	RolePlatformSRE Role = "platform_sre"
	// RoleSuperadmin holds everything, plus the two capabilities nobody else has: granting roles and
	// executing a GDPR erasure.
	RoleSuperadmin Role = "superadmin"
)

// Roles is every role, least-privileged first. Enumerated so the matrix test and the console's role
// picker read the real set.
var Roles = []Role{RoleSupport, RoleBillingOps, RolePlatformSRE, RoleSuperadmin}

// Valid reports whether r is a known role. An unknown role grants nothing — a typo in a grant denies
// rather than escalates, the direction a mistake must fail in here.
func (r Role) Valid() bool {
	for _, k := range Roles {
		if k == r {
			return true
		}
	}
	return false
}

// DisplayName is the operator-facing role name. English, Title Case, matching the console's chrome.
func (r Role) DisplayName() string {
	switch r {
	case RoleSupport:
		return "Support"
	case RoleBillingOps:
		return "Billing-Ops"
	case RolePlatformSRE:
		return "Platform-SRE"
	case RoleSuperadmin:
		return "Superadmin"
	}
	return string(r)
}

// Capability is one gated admin action. Named constants, never literals at call sites: a capability
// spelled two ways is a gate that never fires.
type Capability string

const (
	// ── Read surfaces every role holds (design Decision 2, row 1) ──

	// CapTenantRead is searching and viewing tenants.
	CapTenantRead Capability = "tenant.read"
	// CapJobRead is reading jobs and worker-fleet health.
	CapJobRead Capability = "job.read"
	// CapImpersonateRead is starting a read-scoped, time-bounded impersonation session.
	CapImpersonateRead Capability = "impersonate.read"

	// ── Billing-Ops (row 3) ──

	// CapBillingRead is invoices, dunning, reconciliation and gainshare oversight.
	CapBillingRead Capability = "billing.read"
	// CapBillingCorrect is issuing an additive credit or refund.
	CapBillingCorrect Capability = "billing.correct"
	// CapEntitlementOverride is overriding a tenant's plan/entitlement.
	CapEntitlementOverride Capability = "entitlement.override"

	// ── Platform-SRE (row 4) ──

	// CapJobRetry is retrying a job on the existing P4/P6 queue.
	CapJobRetry Capability = "job.retry"
	// CapJobCancel is cancelling a running job — destructive, so confirm + reason + audit.
	CapJobCancel Capability = "job.cancel"
	// CapRegistryAdmin is administering models and their price references.
	CapRegistryAdmin Capability = "registry.admin"
	// CapKillSwitch is arming or disarming the autonomous-merge kill switch, global or per tenant.
	CapKillSwitch Capability = "killswitch.operate"

	// ── Shared destructive tenant lifecycle (row 5) ──

	// CapTenantSuspend is suspending or reactivating a tenant.
	CapTenantSuspend Capability = "tenant.suspend"
	// CapTenantQuota is adjusting a tenant's quota.
	CapTenantQuota Capability = "tenant.quota"

	// ── Superadmin only (row 6) ──

	// CapRoleGrant is granting or revoking an admin role.
	CapRoleGrant Capability = "role.grant"
	// CapGDPRExecute is executing a data-deletion request.
	CapGDPRExecute Capability = "gdpr.execute"

	// ── Capabilities the design table does not enumerate, decided here ──

	// CapCrossTenantRead is reading a cross-tenant aggregate (usage/SUM, COGS, revenue/ops, top
	// consumers, anomalies).
	//
	// NOT granted to Support. Decision 7 makes any cross-tenant access a privacy event, and Support's
	// legitimate need is a SINGLE tenant — the one on the ticket — which CapTenantRead and read-scoped
	// impersonation already serve. Granting Support the fleet-wide aggregate would hand the widest
	// audience the broadest read for no answered question.
	CapCrossTenantRead Capability = "crosstenant.read"
	// CapAuditRead is reading the audit log.
	//
	// NOT granted to Support, for the same reason: the chain records actions across every tenant, so
	// reading it IS a cross-tenant read. Support's denial names Platform-SRE and Superadmin, which is
	// what "denied with escalation" (FR22) is for.
	CapAuditRead Capability = "audit.read"
	// CapImpersonateElevate is elevating an impersonation session to write scope.
	//
	// NOT granted to Support (design Open Q3's proposal): Support can SEE what a tenant sees but never
	// ACT as them. Keeping "see" and "act as" on different roles is the whole reason impersonation is
	// safer than sharing a credential.
	CapImpersonateElevate Capability = "impersonate.elevate"

	// ── P26. Three capabilities that PARTITION rather than widen (design D8) ──
	//
	// Each governs a new read-only oversight surface. None is folded into an existing capability, and
	// no existing role gained anything: the roles below hold these because somebody decided they
	// should, and `TestNoExistingRoleWidened` proves the pre-P26 capabilities' holders are unchanged.

	// CapDeliveryRead is reading delivery records, their observed merge state, and the change-delivery
	// rollout picture.
	//
	// GRANTED TO SUPPORT, and that grant is the point of the capability existing. "Did my change reach
	// my repository, and if not, why not" is a support question, and today the only tool that can
	// answer it is a reason-required impersonation session into the customer's own console. Folding
	// this into CapCrossTenantRead would have handed Support fleet-wide spend and usage to answer a
	// question about a pull request.
	CapDeliveryRead Capability = "delivery.read"

	// CapReleaseRead is reading published releases per install channel, artefact verification,
	// post-publish smoke, and signing-key STATE — identifier and fingerprint, never key material.
	//
	// NOT granted to Support or Billing-Ops. A release engineer is a fifth persona this console does not
	// have a role for, and until it does, the nearest holder is Platform-SRE. Signing-key state is not
	// something a support queue needs, and the P20 leak is the reason that judgement is written down
	// rather than assumed.
	CapReleaseRead Capability = "release.read"

	// CapAxisRead is reading per-axis adoption, refusal counts by stable typed cause, and the coverage
	// matrix.
	//
	// It lets an axis owner see refusals WITHOUT seeing usage or spend, which is the partition
	// CapCrossTenantRead could not express: that capability grants the money aggregates in the same
	// breath, and the question "which materializer would unblock the most refused nodes" needs none of
	// them.
	CapAxisRead Capability = "axis.read"

	// ── P30. Two capabilities, and the split is the point (design D5, task 6.6) ──

	// CapAgentRead is reading HEROS's definition, its rehearsal report, its per-tenant placement and
	// its spend.
	//
	// NOT granted to Support or Billing-Ops. The agent's definition names a MODEL and a CREDENTIAL
	// REFERENCE, and its spend is a fleet-wide money aggregate — the same reasoning that keeps
	// CapCrossTenantRead off Support. An axis owner reading refusals needs none of it.
	CapAgentRead Capability = "agent.read"

	// CapAgentAdmin is publishing a definition, activating one, editing a cap, and setting a tenant's
	// placement.
	//
	// 🔴 SEPARATE FROM CapAgentRead, and NOT folded into CapRegistryAdmin, because the blast radius is
	// different in kind. Administering a model repoints a price reference; publishing an agent
	// definition changes what the platform INFERS about every customer's source, and setting a
	// placement to `platform` makes the platform read that source under a platform-held credential.
	// Q2 made the default `disabled` precisely so that is a deliberate act, and a deliberate act needs
	// a capability somebody was granted on purpose.
	CapAgentAdmin Capability = "agent.admin"
)

// Capabilities is every gated capability. The matrix test iterates this, so a capability added
// without a considered grant fails a test rather than shipping ungated or unreachable.
var Capabilities = []Capability{
	CapTenantRead, CapJobRead, CapImpersonateRead,
	CapBillingRead, CapBillingCorrect, CapEntitlementOverride,
	CapJobRetry, CapJobCancel, CapRegistryAdmin, CapKillSwitch,
	CapTenantSuspend, CapTenantQuota,
	CapRoleGrant, CapGDPRExecute,
	CapCrossTenantRead, CapAuditRead, CapImpersonateElevate,
	CapDeliveryRead, CapReleaseRead, CapAxisRead,
	CapAgentRead, CapAgentAdmin,
}

// Description is the operator-facing explanation of a capability, used by the console's
// denied-with-escalation copy. English, sentence case, no numbers.
func (c Capability) Description() string {
	if d, ok := capabilityDescriptions[c]; ok {
		return d
	}
	return string(c)
}

var capabilityDescriptions = map[Capability]string{
	CapTenantRead:          "Search and view tenants",
	CapJobRead:             "Read jobs and worker-fleet health",
	CapImpersonateRead:     "Start a read-scoped, time-bounded impersonation session",
	CapBillingRead:         "View invoices, dunning, reconciliation and gainshare evidence",
	CapBillingCorrect:      "Issue an additive credit or refund",
	CapEntitlementOverride: "Override a tenant's plan or entitlement",
	CapJobRetry:            "Retry a job",
	CapJobCancel:           "Cancel a running job",
	CapRegistryAdmin:       "Administer models and their price references",
	CapKillSwitch:          "Arm or disarm the autonomous-merge kill switch",
	CapTenantSuspend:       "Suspend or reactivate a tenant",
	CapTenantQuota:         "Adjust a tenant's quota",
	CapRoleGrant:           "Grant or revoke an admin role",
	CapGDPRExecute:         "Execute a data-deletion request",
	CapCrossTenantRead:     "Read cross-tenant aggregates",
	CapAuditRead:           "Read the audit log",
	CapImpersonateElevate:  "Elevate an impersonation session to write scope",
	CapDeliveryRead:        "Read delivery records and the change-delivery rollout picture",
	CapReleaseRead:         "Read published releases, artefact verification and signing-key state",
	CapAxisRead:            "Read per-axis adoption, refusals and coverage",
	CapAgentRead:           "Read the platform analysis agent's definition, rehearsal and spend",
	CapAgentAdmin:          "Publish and activate an agent definition, and set caps and placements",
}

// RequiresConfirmation reports whether a capability must go through the confirm + recorded reason +
// audit path. FR6 covers "destructive OR privileged", which is why starting an impersonation session
// is in scope even though it destroys nothing: it opens a window onto a tenant's data, and that must
// be justified and on the record.
//
// It is derived from a read-only table rather than an if/else chain at each call site, so adding a
// capability picks up the friction automatically instead of when somebody remembers.
func (c Capability) RequiresConfirmation() bool { return !readOnly[c] }

// ReadOnly reports whether a capability only reads. The complement of RequiresConfirmation, named
// positively so call sites read as intent rather than as a negation.
func (c Capability) ReadOnly() bool { return readOnly[c] }

var readOnly = map[Capability]bool{
	CapTenantRead:      true,
	CapJobRead:         true,
	CapBillingRead:     true,
	CapCrossTenantRead: true,
	CapAuditRead:       true,
	// The three P26 oversight surfaces. Read-only by construction — no control on any of them opens,
	// closes, retries or merges a delivery, halts a channel, unpublishes an artefact, or changes a
	// coverage answer.
	CapDeliveryRead: true,
	CapReleaseRead:  true,
	CapAxisRead:     true,
}

// Irreversible reports whether a capability is a one-way door — an action no counterpart can undo.
// It is a SEMANTIC property, used by the console to say so and by the friction table below.
func (c Capability) Irreversible() bool { return irreversible[c] }

// RequiresTypedTarget reports whether a capability requires the operator to TYPE the target
// identifier as a second confirmation.
//
// Every irreversible capability does, which is FR6's rule. Elevating an impersonation session to
// write scope also does, and it is not irreversible: the reason is that "see a tenant" and "act as a
// tenant" must not be one click apart (FR13/FR25). Keeping the two predicates separate means the
// console can still label the GDPR erasure as the only one-way door without implying that write
// elevation is unrecoverable.
func (c Capability) RequiresTypedTarget() bool { return irreversible[c] || typedTarget[c] }

var typedTarget = map[Capability]bool{
	CapImpersonateElevate: true,
}

var irreversible = map[Capability]bool{
	// A GDPR erasure destroys content that cannot be restored. Suspension, kill-switch arming, quota
	// changes, overrides and credits are all reversible by their counterpart action, which is why they
	// take one confirmation and this takes two.
	CapGDPRExecute: true,
}

// permissions is the ALLOW list. Deny by default: an unlisted (role, capability) pair is denied.
//
// It mirrors design Decision 2's table exactly for the rows that table names, and documents its own
// reasoning for the three capabilities the table does not enumerate (see their constants above).
var permissions = map[Role]map[Capability]bool{
	RoleSupport: {
		CapTenantRead:      true,
		CapJobRead:         true,
		CapImpersonateRead: true,
		// 🔴 Support holds delivery.read, and that is the whole argument for the capability. The
		// question it answers — "did this tenant's change reach their repository, and if not, why" — is
		// today answered by opening an impersonation session, which is a data-protection cost paid for
		// a missing aggregate. It does NOT bring spend or usage with it, which folding this into
		// crosstenant.read would have.
		CapDeliveryRead: true,
	},
	RoleBillingOps: {
		CapTenantRead:          true,
		CapJobRead:             true,
		CapImpersonateRead:     true,
		CapImpersonateElevate:  true,
		CapBillingRead:         true,
		CapBillingCorrect:      true,
		CapEntitlementOverride: true,
		CapTenantSuspend:       true,
		CapTenantQuota:         true,
		CapCrossTenantRead:     true,
		CapAuditRead:           true,
		// Delivery, because a gainshare dispute is a question about which changes actually merged.
		// NOT release or axis: neither is a money question, and Billing-Ops is already the broadest
		// non-superadmin role.
		CapDeliveryRead: true,
	},
	RolePlatformSRE: {
		CapTenantRead:         true,
		CapJobRead:            true,
		CapImpersonateRead:    true,
		CapImpersonateElevate: true,
		CapJobRetry:           true,
		CapJobCancel:          true,
		CapRegistryAdmin:      true,
		CapKillSwitch:         true,
		CapTenantSuspend:      true,
		CapTenantQuota:        true,
		CapCrossTenantRead:    true,
		CapAuditRead:          true,
		// All three. Platform-SRE runs the machinery, and until a release-engineer role exists it is
		// the nearest holder of signing-key state — a judgement recorded here rather than assumed,
		// because the alternative was widening registry.admin to cover keys.
		CapDeliveryRead: true,
		CapReleaseRead:  true,
		CapAxisRead:     true,
		// P30 read, and NOT P30 admin. Platform-SRE runs the machinery and must be able to SEE what the
		// analysis agent is doing and what it costs. Publishing a definition changes what the platform
		// infers about every customer's source; that stays with Superadmin until somebody decides
		// otherwise on purpose, which is the same judgement CapReleaseRead records one field up.
		CapAgentRead: true,
	},
	RoleSuperadmin: superadminGrants(),
}

// superadminGrants gives Superadmin every capability.
//
// Derived from Capabilities rather than hand-listed on purpose: a hand-listed Superadmin is the one
// place a forgotten entry produces a capability NO role can reach, and the resulting bug ("nobody can
// action a GDPR request") surfaces during a compliance deadline rather than in review.
func superadminGrants() map[Capability]bool {
	m := make(map[Capability]bool, len(Capabilities))
	for _, c := range Capabilities {
		m[c] = true
	}
	return m
}

// Grants reports the capabilities a role holds, sorted. This is what the BFF serves to the console so
// the screen renders from the same map the gate enforces (FR22).
func Grants(r Role) []Capability {
	m := permissions[r]
	out := make([]Capability, 0, len(m))
	for c, ok := range m {
		if ok {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// HoldersOf reports which roles grant a capability, least-privileged first. It is what a denial
// message names — "Billing-Ops or Superadmin holds this" — so a refusal tells an operator how to
// escalate rather than being a bare 403 (FR22).
func HoldersOf(c Capability) []Role {
	var out []Role
	for _, r := range Roles {
		if permissions[r][c] {
			out = append(out, r)
		}
	}
	return out
}

// PermissionMap is the whole allow list, shaped for transport to the console.
type PermissionMap map[Role][]Capability

// FullPermissionMap renders every role's grants. Served to the console so it never keeps a second copy.
func FullPermissionMap() PermissionMap {
	out := make(PermissionMap, len(Roles))
	for _, r := range Roles {
		out[r] = Grants(r)
	}
	return out
}

// ── Grant log ───────────────────────────────────────────────────────────────────────────────────

// GrantAction is what a grant-log row records.
type GrantAction string

const (
	// GrantActionGrant adds a role.
	GrantActionGrant GrantAction = "grant"
	// GrantActionRevoke removes one. A revoke is a NEW ROW, never an edit of the grant row: the log is
	// append-only, so "when did this admin stop holding Platform-SRE" stays answerable years later.
	GrantActionRevoke GrantAction = "revoke"
)

// RoleGrant is one append-only row of `admin_role_grant`.
type RoleGrant struct {
	GrantID   string      `json:"grant_id"`
	AdminID   string      `json:"admin_id"`
	Role      Role        `json:"role"`
	Action    GrantAction `json:"action"`
	GrantedBy string      `json:"granted_by"`
	// GrantedAt is when a grant row was written; RevokedAt is when a revoke row was. Exactly one is
	// set per row — the design's two columns, kept append-only by writing a row rather than editing one.
	GrantedAt time.Time `json:"granted_at,omitempty"`
	RevokedAt time.Time `json:"revoked_at,omitempty"`
	// Revokes names the grant this row withdraws, so the pair is reconstructable without inference.
	Revokes string `json:"revokes,omitempty"`
	Reason  string `json:"reason"`
}

// At returns the row's effective timestamp regardless of which column carries it.
func (g RoleGrant) At() time.Time {
	if g.Action == GrantActionRevoke {
		return g.RevokedAt
	}
	return g.GrantedAt
}

var (
	// ErrNotSuperadmin is the FR5 denial: only a Superadmin grants or revokes a role.
	ErrNotSuperadmin = errors.New("adminrbac: granting or revoking an admin role requires Superadmin")
	// ErrUnknownRole rejects a typo rather than creating a role nothing grants.
	ErrUnknownRole = errors.New("adminrbac: unknown admin role")
	// ErrNoReason rejects a grant with no recorded justification.
	ErrNoReason = errors.New("adminrbac: a role grant requires a recorded reason")
)

// GrantStore is the append-only role-grant log.
type GrantStore struct {
	mu   sync.RWMutex
	rows []RoleGrant
	now  func() time.Time
	seq  int
	// writer is the optional durable backing (durable.go). Nil means the log lives only as long as the
	// process, which on a folded log means every operator holds no role after a restart.
	writer GrantWriter
}

// NewGrantStore builds an empty log.
func NewGrantStore(now func() time.Time) *GrantStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &GrantStore{now: now}
}

// Seed writes an initial grant with no Superadmin gate, for bootstrapping the FIRST Superadmin and for
// fixtures.
//
// It is separate from Grant rather than a flag on it: bootstrapping is a deployment-time act with no
// acting admin to authorize it (there is not one yet), and conflating it with the runtime path is how
// "gated to Superadmin" quietly acquires a bypass parameter. It is not reachable from the HTTP surface.
func (s *GrantStore) Seed(adminID string, role Role, reason string) (RoleGrant, error) {
	if !role.Valid() {
		return RoleGrant{}, fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	return s.append(adminID, role, GrantActionGrant, "bootstrap", reason, "")
}

// Live returns the roles an admin holds right now, folded from the append-only log.
func (s *GrantStore) Live(adminID string) []Role {
	s.mu.RLock()
	defer s.mu.RUnlock()
	held := map[Role]bool{}
	for _, g := range s.rows {
		if g.AdminID != adminID {
			continue
		}
		held[g.Role] = g.Action == GrantActionGrant
	}
	var out []Role
	for _, r := range Roles {
		if held[r] {
			out = append(out, r)
		}
	}
	return out
}

// Rows returns the whole append-only log (a copy).
func (s *GrantStore) Rows() []RoleGrant {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RoleGrant, len(s.rows))
	copy(out, s.rows)
	return out
}

func (s *GrantStore) append(adminID string, role Role, action GrantAction, by, reason, revokes string) (RoleGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	row := RoleGrant{
		GrantID: fmt.Sprintf("grant-%04d", s.seq), AdminID: adminID, Role: role, Action: action,
		GrantedBy: by, Reason: reason, Revokes: revokes,
	}
	if action == GrantActionRevoke {
		row.RevokedAt = s.now()
	} else {
		row.GrantedAt = s.now()
	}
	// Durable first: a revoke that persisted nowhere is an operator whose authority comes back at the
	// next restart, and nobody re-reads a revoke they already performed.
	//
	// The counter is NOT rolled back on failure. Reusing a burnt id would let a later row take the id an
	// earlier failed attempt may already have written, and the log is append-only precisely so no id ever
	// means two things. A gap in the sequence costs nothing; a reused id costs the audit trail.
	if s.writer != nil {
		if err := s.writer.AppendGrant(row); err != nil {
			return RoleGrant{}, fmt.Errorf("adminrbac: persist %s of %s to %s: %w", action, role, adminID, err)
		}
	}
	s.rows = append(s.rows, row)
	return row, nil
}

// liveGrantID finds the grant row a revoke withdraws, so the pair is linked rather than inferred.
func (s *GrantStore) liveGrantID(adminID string, role Role) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id := ""
	for _, g := range s.rows {
		if g.AdminID == adminID && g.Role == role {
			if g.Action == GrantActionGrant {
				id = g.GrantID
			} else {
				id = ""
			}
		}
	}
	return id
}

// ── Denial observation ──────────────────────────────────────────────────────────────────────────

// Decision is an authorization outcome.
type Decision struct {
	Allowed bool `json:"allowed"`
	// Capability and Target restate what was asked, so a Decision is self-describing in a log line.
	Capability Capability `json:"capability"`
	Target     string     `json:"target"`
	// Reason explains a denial in operator language.
	Reason string `json:"reason,omitempty"`
	// HeldBy names the roles that DO hold the capability — the escalation path a denial owes the
	// operator instead of a bare refusal (FR22).
	HeldBy []Role `json:"held_by,omitempty"`
	// Roles is what the caller actually holds, so a denial is diagnosable without a second query.
	Roles []Role `json:"roles,omitempty"`
}

// Error renders a denial as an error for call sites that propagate one.
func (d Decision) Error() error {
	if d.Allowed {
		return nil
	}
	holders := make([]string, 0, len(d.HeldBy))
	for _, r := range d.HeldBy {
		holders = append(holders, r.DisplayName())
	}
	if len(holders) == 0 {
		return fmt.Errorf("adminrbac: %s denied on %s: %s", d.Capability, d.Target, d.Reason)
	}
	return fmt.Errorf("adminrbac: %s denied on %s: %s — held by %s",
		d.Capability, d.Target, d.Reason, strings.Join(holders, " or "))
}

// ErrDenied classifies an authorization refusal so callers can distinguish it from a transport
// failure — the distinction the console renders as "denied" versus "degraded" (FR26).
var ErrDenied = errors.New("adminrbac: denied")

// Gate resolves live role grants against the deny-by-default permission map.
type Gate struct {
	grants *GrantStore
	audit  adminaudit.Store
	now    func() time.Time
}

// NewGate wires the gate. The audit store is required: FR3 says every denial is logged, and a gate
// that can be constructed without somewhere to log denials is a gate that will be.
func NewGate(grants *GrantStore, audit adminaudit.Store, now func() time.Time) (*Gate, error) {
	if grants == nil {
		return nil, errors.New("adminrbac: a gate needs the role-grant log")
	}
	if audit == nil {
		return nil, errors.New("adminrbac: a gate needs an audit store — every denial is logged")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Gate{grants: grants, audit: audit, now: now}, nil
}

// Authorize resolves the caller's LIVE role grants against the permission map and logs every denial.
//
// The signature is the design's: Authorize(admin_id, capability, target) → {allowed, reason?}.
func (g *Gate) Authorize(adminID string, capability Capability, target string) Decision {
	roles := g.grants.Live(adminID)
	for _, r := range roles {
		if permissions[r][capability] {
			return Decision{Allowed: true, Capability: capability, Target: target, Roles: roles}
		}
	}
	d := Decision{
		Allowed: false, Capability: capability, Target: target, Roles: roles,
		Reason: deniedReason(roles, capability), HeldBy: HoldersOf(capability),
	}
	g.logDenial(adminID, d)
	return d
}

// deniedReason phrases a denial in the operator's terms rather than as a status code.
func deniedReason(roles []Role, c Capability) string {
	if len(roles) == 0 {
		return "this admin principal holds no live role"
	}
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.DisplayName())
	}
	return fmt.Sprintf("%s does not grant %q", strings.Join(names, " + "), c.Description())
}

// logDenial writes the denial to the audit chain. A denial that cannot be audited is still a denial —
// the action was refused either way — so this does not fail the request; it is the one place in P8
// where an audit failure is not fail-closed, because failing closed here would mean turning a refusal
// into a different refusal.
func (g *Gate) logDenial(adminID string, d Decision) {
	actor := adminID
	if strings.TrimSpace(actor) == "" {
		actor = "unknown"
	}
	_, _ = g.audit.Append(adminaudit.Entry{
		ActorAdminID: actor,
		Target:       d.Target,
		Action:       adminaudit.ActionAuthorizationDenied,
		Reason:       d.Reason,
		Result:       "denied",
		Evidence:     map[string]string{"capability": string(d.Capability)},
		CreatedAt:    g.now(),
	})
}

// LiveRoles reports the roles an admin holds right now.
func (g *Gate) LiveRoles(adminID string) []Role { return g.grants.Live(adminID) }

// CapabilitiesFor reports every capability an admin can currently reach — the union of their live
// roles' grants. This is what the console reads to render its role-scoped surface.
func (g *Gate) CapabilitiesFor(adminID string) []Capability {
	seen := map[Capability]bool{}
	for _, r := range g.grants.Live(adminID) {
		for _, c := range Grants(r) {
			seen[c] = true
		}
	}
	out := make([]Capability, 0, len(seen))
	for _, c := range Capabilities {
		if seen[c] {
			out = append(out, c)
		}
	}
	return out
}

// Grant adds a role to an admin. Superadmin-gated and audited (FR5).
func (g *Gate) Grant(actorAdminID, subjectAdminID string, role Role, reason string) (RoleGrant, error) {
	return g.mutateRole(actorAdminID, subjectAdminID, role, reason, GrantActionGrant)
}

// Revoke withdraws a role. Same gate, same audit, and it appends a row rather than editing one.
func (g *Gate) Revoke(actorAdminID, subjectAdminID string, role Role, reason string) (RoleGrant, error) {
	return g.mutateRole(actorAdminID, subjectAdminID, role, reason, GrantActionRevoke)
}

func (g *Gate) mutateRole(actorAdminID, subjectAdminID string, role Role, reason string, action GrantAction) (RoleGrant, error) {
	if !role.Valid() {
		return RoleGrant{}, fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	if strings.TrimSpace(reason) == "" {
		return RoleGrant{}, ErrNoReason
	}
	target := "admin:" + subjectAdminID
	if d := g.Authorize(actorAdminID, CapRoleGrant, target); !d.Allowed {
		// The denial is already audited by Authorize. Return the typed sentinel so the caller can tell
		// "you may not" from "the store is down".
		return RoleGrant{}, fmt.Errorf("%w: %v", ErrNotSuperadmin, d.Error())
	}
	auditAction := adminaudit.ActionRoleGrant
	if action == GrantActionRevoke {
		auditAction = adminaudit.ActionRoleRevoke
	}
	// Write-ahead: the audit entry is committed BEFORE the grant takes effect, so a crash cannot leave
	// an unrecorded role change behind (design Decision 3).
	if _, err := g.audit.Append(adminaudit.Entry{
		ActorAdminID: actorAdminID,
		Target:       target,
		Action:       auditAction,
		Reason:       reason,
		Result:       "applied",
		Evidence:     map[string]string{"role": string(role), "subject_admin_id": subjectAdminID},
		ParamsDigest: adminaudit.Digest(subjectAdminID, string(role)),
		CreatedAt:    g.now(),
	}); err != nil {
		return RoleGrant{}, fmt.Errorf("adminrbac: role change not applied: %w", err)
	}
	revokes := ""
	if action == GrantActionRevoke {
		revokes = g.grants.liveGrantID(subjectAdminID, role)
	}
	// The audit entry above is already committed, so a failure here leaves a recorded role change that
	// did not take effect. That is the correct direction and matches Decision 3's write-ahead: the log
	// over-reports an attempt rather than under-reporting an applied change, and the caller is told the
	// change did not apply. The opposite order would let a role change take effect unrecorded.
	return g.grants.append(subjectAdminID, role, action, actorAdminID, reason, revokes)
}
