package adminops

import (
	"context"
	"errors"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/legal"
)

// oversight.go answers the three oversight questions an operator could not answer before (P26 wave
// 26e), plus the one it deliberately cannot answer yet.
//
// # The asymmetry: unknown is an answer, and a guess is not
//
// A per-tenant deployed version that is not derivable renders as *unknown*. An inferred version
// rendered as a version is a wrong number that gets acted on during an incident — which is exactly
// when it will be read. So there is no inference here from an API contract version, a feature probe,
// or any other proxy, and the ledger carries the missing collection by name.
//
// # And the one that is not built
//
// P21 has not shipped. The payments block below states that plainly. It renders no count, no zero,
// and no empty table implying there is nothing to show — because an empty state rendered as a working
// page is the failure this whole phase is about.

// IntegrationState is one observability integration's health. Three values: *not configured* is a
// DECISION and *degraded* is a FAULT, and a boolean would have to call one of them the other.
type IntegrationState string

const (
	// IntegrationAbsent — nothing is configured. Not a fault.
	IntegrationAbsent IntegrationState = "absent"
	// IntegrationConfigured — configured and reporting.
	IntegrationConfigured IntegrationState = "configured"
	// IntegrationDegraded — configured and NOT reporting. A fault, and it names its failure class.
	IntegrationDegraded IntegrationState = "degraded"
)

// IntegrationStates lists the three.
func IntegrationStates() []IntegrationState {
	return []IntegrationState{IntegrationAbsent, IntegrationConfigured, IntegrationDegraded}
}

// SessionRow is one operator session and the factor that authenticated it.
type SessionRow struct {
	SessionID string `json:"session_id"`
	AdminID   string `json:"admin_id"`
	// Factor is the factor NAME the platform VERIFIED — never the IdP's claim about one, and never the
	// factor's value. Empty means the verifier recorded none, which is rendered as such.
	Factor     string `json:"factor"`
	VerifiedAt string `json:"verified_at"`
	ExpiresAt  string `json:"expires_at"`
	Live       bool   `json:"live"`
	// MultiFactor distinguishes a single-factor session from a multi-factor one, which is the whole
	// question a reviewer opens this surface to answer.
	MultiFactor bool `json:"multi_factor"`
}

// IdentityProviderView describes the identity provider in use, honestly.
type IdentityProviderView struct {
	Issuer string `json:"issuer"`
	Kind   string `json:"kind"`
	// TestMode is true when the IdP is the fixture. 🔴 The surface says so rather than representing the
	// fixture as a production identity provider — the verifier is real either way, and the factor shown
	// is the one it recorded, but the ISSUER is not a production one and the surface must not imply it.
	TestMode bool   `json:"test_mode"`
	Note     string `json:"note"`
}

// LegalRow is one tenant's acceptance state for one document kind.
type LegalRow struct {
	TenantID string `json:"tenant_id"`
	Kind     string `json:"kind"`
	// AcceptedVersion and AcceptedHash are the version the tenant accepted and the content hash it was
	// shown. ArchiveHref links the ARCHIVED TEXT at that hash, so "what did they actually agree to" is
	// answerable years later.
	AcceptedVersion string `json:"accepted_version,omitempty"`
	AcceptedHash    string `json:"accepted_hash,omitempty"`
	ArchiveHref     string `json:"archive_href,omitempty"`
	AcceptedAt      string `json:"accepted_at,omitempty"`
	// OwedVersion is set when a MATERIAL later version has been published and this tenant has not
	// accepted it. A non-material publication creates no obligation and leaves this empty.
	OwedVersion string `json:"owed_version,omitempty"`
	OwedSince   string `json:"owed_since,omitempty"`
	OwedHref    string `json:"owed_href,omitempty"`
}

// IntegrationRow is one observability integration's state, read from the PLATFORM's readiness surface.
type IntegrationRow struct {
	Name  string           `json:"name"`
	State IntegrationState `json:"state"`
	// FailureClass is required when State is degraded: "configured but unreachable" and "configured but
	// rejecting our schema" send an operator to two different places.
	FailureClass string `json:"failure_class,omitempty"`
	// Source names where this state was read. 🔴 It is the platform's own readiness surface, never a
	// third party's dashboard — which is the least available part of the system during an incident.
	Source string `json:"source"`
}

// DeploymentRow is one tenant's deployment shape and version, or an explicit unknown.
type DeploymentRow struct {
	TenantID string `json:"tenant_id"`
	// Shape is the deployment shape where a signal carries it; empty means unknown.
	Shape string `json:"shape,omitempty"`
	// Version is the release identifier where a signal carries it; empty means unknown.
	Version string `json:"version,omitempty"`
	// Unknown is true when no signal carries the version. 🔴 MissingCollection then names what would
	// make it readable, so a later phase finds a specified task rather than a gap.
	Unknown           bool   `json:"unknown"`
	MissingCollection string `json:"missing_collection,omitempty"`
}

// NotYetReadable states a capability that has not shipped, in the shape the ledger uses.
type NotYetReadable struct {
	Subject string `json:"subject"`
	// Requires names the collection that would make the read possible. Never empty: an unnamed missing
	// input is a wish, not a task.
	Requires string `json:"requires"`
	// Statement is what the surface renders in place of a page. It renders NO count and NO zero.
	Statement string `json:"statement"`
}

// OversightView is the operator's identity, consent and reporting-health read model.
type OversightView struct {
	Sessions         []SessionRow         `json:"sessions"`
	IdentityProvider IdentityProviderView `json:"identity_provider"`
	Legal            []LegalRow           `json:"legal"`
	// LegalKnown is false when no legal service is wired — the rows are then absent rather than empty,
	// because an empty acceptance table reads as "nobody owes anything".
	LegalKnown   bool             `json:"legal_known"`
	Integrations []IntegrationRow `json:"integrations"`
	// IntegrationsKnown is false when the platform's readiness surface is not wired. The integrations
	// are then NOT reported as `absent`: "nothing is configured" and "we did not ask" are different.
	IntegrationsKnown bool             `json:"integrations_known"`
	Deployments       []DeploymentRow  `json:"deployments"`
	NotYetReadable    []NotYetReadable `json:"not_yet_readable"`
	ReadOnly          bool             `json:"read_only"`
	Source            string           `json:"source"`
}

// ReadinessSource reports each observability integration's state from the PLATFORM's own readiness
// surface. A deployment with none wired reports `IntegrationsKnown: false` rather than a list of
// `absent`.
type ReadinessSource interface {
	Integrations() []IntegrationRow
	Describe() string
}

// DeploymentSource reports per-tenant deployment shape and version WHERE DERIVABLE. It is expected to
// return an unknown row rather than an inferred one; the read model asserts nothing was inferred by
// requiring a named missing collection on every unknown.
type DeploymentSource interface {
	Deployment(tenantID string) DeploymentRow
	Describe() string
}

// MissingDeploymentHeartbeat is the collection that would make a per-tenant version readable. It is a
// constant so the surface and the ledger name the same thing.
const MissingDeploymentHeartbeat = "a deployment heartbeat carrying the release identifier"

// PaymentsNotYetReadable is the P21 gap, specified rather than left blank.
func PaymentsNotYetReadable() NotYetReadable {
	return NotYetReadable{
		Subject: "payment webhooks and dunning",
		Requires: "the P21 webhook receipt store and payment collection records — the recorded provider " +
			"webhook events with their idempotent dispositions, and the dunning attempts against a failed " +
			"collection",
		Statement: "The payments subsystem has not shipped. There is nothing to count here yet, and " +
			"nothing below is a zero: this surface states the absence rather than rendering an empty " +
			"table that would read as 'no failed payments'.",
	}
}

// OversightService serves the oversight read model. READ-ONLY.
type OversightService struct {
	exec        *Executor
	sessions    *adminidentity.SessionStore
	identity    adminidentity.ProviderInfo
	legal       *legal.Service
	tenants     func() []string
	readiness   ReadinessSource
	deployments DeploymentSource
}

// OversightConfig configures the read model. Every source is optional and its absence is REPORTED
// rather than rendered as an empty result.
type OversightConfig struct {
	Sessions    *adminidentity.SessionStore
	Identity    adminidentity.ProviderInfo
	Legal       *legal.Service
	Tenants     func() []string
	Readiness   ReadinessSource
	Deployments DeploymentSource
}

// NewOversightService wires the read model.
func NewOversightService(exec *Executor, cfg OversightConfig) (*OversightService, error) {
	if exec == nil {
		return nil, errors.New("adminops: the oversight read model needs the command path")
	}
	return &OversightService{
		exec: exec, sessions: cfg.Sessions, identity: cfg.Identity, legal: cfg.Legal,
		tenants: cfg.Tenants, readiness: cfg.Readiness, deployments: cfg.Deployments,
	}, nil
}

// View returns the oversight picture.
//
// It is gated on `audit.read` rather than a new capability: operator sessions, consent state and
// reporting health are the record of who did what, which is the question the audit capability already
// governs. Adding a fourth capability for the same question would partition nothing.
func (s *OversightService) View(ctx context.Context) (OversightView, error) {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapAuditRead, TargetGlobal)
	if err != nil {
		return OversightView{}, err
	}
	if _, err := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: sess.AdminID, Target: TargetGlobal, Action: adminaudit.ActionCrossTenantView,
		Reason: "oversight read", Result: "viewed",
		Evidence: map[string]string{"read_model": "oversight"}, CreatedAt: s.exec.Now(),
	}); err != nil {
		return OversightView{}, errors.New("adminops: oversight read refused — it could not be logged: " + err.Error())
	}

	now := s.exec.Now()
	// 🔴 Every list starts EMPTY rather than nil. A nil slice marshals to `null`, and a client that
	// reads a list's length off `null` crashes — which it did, on this exact page, the first time it was
	// opened in a browser. "A list is a list" is a wire contract, not a formatting preference: the
	// distinction this read model needs is carried by the `…Known` booleans beside them, not by a
	// null-versus-empty subtlety a consumer has to know about.
	view := OversightView{
		Sessions:     []SessionRow{},
		Legal:        []LegalRow{},
		Integrations: []IntegrationRow{},
		Deployments:  []DeploymentRow{},
		ReadOnly:     true,
		Source:       "operator session store + the P23 legal manifest + the platform's readiness surface",
		NotYetReadable: []NotYetReadable{PaymentsNotYetReadable(), {
			Subject:  "per-tenant deployed version",
			Requires: MissingDeploymentHeartbeat,
			Statement: "No signal carries a customer deployment's release identifier. The version is " +
				"shown as unknown rather than inferred from an API contract version, a feature probe or " +
				"any other proxy — an inferred version rendered as a version is a wrong number that gets " +
				"acted on during an incident.",
		}},
	}

	// ── Which factor authenticated each session, and when ──
	view.IdentityProvider = IdentityProviderView{
		Issuer: s.identity.Issuer, Kind: s.identity.Kind, TestMode: s.identity.TestMode,
	}
	if s.identity.TestMode {
		view.IdentityProvider.Note = "This deployment's admin identity provider is the TEST-MODE fixture. " +
			"The verifier below is the real one — the factor shown is what it verified, MFA included — " +
			"but the issuer is not a production identity provider and this surface does not claim one."
	} else {
		view.IdentityProvider.Note = "A production identity provider. The factor shown is the one the " +
			"platform verified, never the provider's claim about one."
	}
	if s.sessions != nil {
		for _, row := range s.sessions.Sessions() {
			view.Sessions = append(view.Sessions, SessionRow{
				SessionID: row.SessionID, AdminID: row.AdminID, Factor: row.MFAFactor,
				VerifiedAt: stamp(row.IssuedAt), ExpiresAt: stamp(row.ExpiresAt),
				Live: row.Live(now), MultiFactor: row.MFAFactor != "",
			})
		}
	}

	// ── Which document versions each tenant has accepted, and which are owed ──
	if s.legal != nil && s.tenants != nil {
		view.LegalKnown = true
		for _, tenantID := range s.tenants() {
			view.Legal = append(view.Legal, s.legalRows(ctx, tenantID)...)
		}
	}

	// ── Whether reporting is actually working ──
	if s.readiness != nil {
		view.IntegrationsKnown = true
		view.Integrations = s.readiness.Integrations()
	}

	// ── Deployment shape and version WHERE DERIVABLE ──
	if s.tenants != nil {
		for _, tenantID := range s.tenants() {
			if s.deployments == nil {
				view.Deployments = append(view.Deployments, DeploymentRow{
					TenantID: tenantID, Unknown: true, MissingCollection: MissingDeploymentHeartbeat,
				})
				continue
			}
			row := s.deployments.Deployment(tenantID)
			row.TenantID = tenantID
			if row.Version == "" {
				// 🔴 No inference. An unknown version is stated, and it names what would make it readable.
				row.Unknown = true
				row.MissingCollection = MissingDeploymentHeartbeat
			}
			view.Deployments = append(view.Deployments, row)
		}
	}
	return view, nil
}

// legalRows builds one tenant's acceptance state across every document kind.
func (s *OversightService) legalRows(ctx context.Context, tenantID string) []LegalRow {
	history, err := s.legal.Read(ctx, tenantID, "")
	if err != nil {
		return []LegalRow{{TenantID: tenantID}}
	}
	accepted := map[legal.Kind]legal.Acceptance{}
	for _, a := range history.Accepted {
		if prev, ok := accepted[a.DocumentKind]; !ok || a.AcceptedAt.After(prev.AcceptedAt) {
			accepted[a.DocumentKind] = a
		}
	}
	var out []LegalRow
	for kind, a := range accepted {
		row := LegalRow{
			TenantID: tenantID, Kind: string(kind),
			AcceptedVersion: a.DocumentVersion, AcceptedHash: a.ContentHash,
			// The ARCHIVED text at the accepted content hash — not the current text, which is a different
			// document and is exactly what a dispute is about.
			ArchiveHref: "/legal/archive/" + string(kind) + "/" + a.DocumentVersion + "#" + a.ContentHash,
			AcceptedAt:  stamp(a.AcceptedAt),
		}
		out = append(out, row)
	}
	// Versions OWED after a material publication. `Pending` applies the manifest's own materiality
	// declaration, so a non-material version creates no obligation and nothing here infers one.
	for _, v := range history.Pending {
		out = append(out, LegalRow{
			TenantID: tenantID, OwedVersion: v.Version, OwedSince: v.EffectiveDate,
			OwedHref: "/legal/archive/" + v.Route + "#" + v.Hash,
		})
	}
	if len(out) == 0 {
		out = append(out, LegalRow{TenantID: tenantID})
	}
	return out
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
