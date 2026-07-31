// Package adminfixture builds the TEST-MODE identity, authorization and command layer the operator
// console needs, so that more than one demo can serve the admin API without a second copy of the graph.
//
// # Why this is a package and not a function in one command
//
// `cmd/p8hermes` built this inline, with a comment saying it was one function "so the demo and a future
// integration test build the identical graph". The moment a second caller appeared — `cmd/p21hermes`,
// serving the same console against the REAL billing stack — that intention needed somewhere to live.
// Two hand-rolled identity graphs would drift, and the drift would show up as an operator who can sign
// in to one surface and not the other, which is the kind of difference nobody can debug from the UI.
//
// 🔴 TEST MODE, and it says so in its own output. The IdP fixture mints assertions signed with the
// admin IdP keys and verified by the REAL verifier, MFA included — it is a test ISSUER, never an MFA
// bypass. A production deployment builds this layer from its own identity provider and leaves the
// fixture nil, at which point `/testmode/assert` 404s.
package adminfixture

import (
	"fmt"
	"log"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// Issuer is the fixture admin IdP's issuer. Shared so a session minted by one demo is describable in
// the same words by the other.
const Issuer = "https://admin-idp.test.heros.internal"

// Layer is the identity, authorization and command substrate of an admin API. Every field is what
// [api.AdminDeps] needs; the operator SERVICES on top of it are the caller's business, because that is
// precisely what differs between one deployment and another.
type Layer struct {
	Authenticator *adminidentity.Authenticator
	Sessions      *adminidentity.SessionStore
	Gate          *adminrbac.Gate
	Executor      *adminops.Executor
	TestModeIdP   *adminidentity.IdPFixture
	Telemetry     *adminops.Telemetry
	Audit         *adminaudit.MemoryStore
	Grants        *adminrbac.GrantStore
}

// Principals are the fixture admin subjects, one per role — the strings an operator types at sign-in.
func Principals() []string {
	out := make([]string, 0, len(adminrbac.Roles))
	for _, r := range adminrbac.Roles {
		out = append(out, "sso|"+string(r))
	}
	return out
}

// Build assembles the layer. `label` names the process in telemetry lines, so two demos logging to the
// same terminal stay tellable apart.
func Build(label string, now func() time.Time) (Layer, error) {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	audit := adminaudit.NewMemoryStore(now)

	// One principal per role, so every capability in the RBAC table has someone who holds it. A console
	// that denies an action must be able to NAME who could perform it, and it can only do that if such
	// a person exists.
	grants := adminrbac.NewGrantStore(now)
	for _, r := range adminrbac.Roles {
		if _, err := grants.Seed("adm-"+string(r), r, label+" fixture: one principal per role"); err != nil {
			return Layer{}, fmt.Errorf("adminfixture: seed grant %s: %w", r, err)
		}
	}
	gate, err := adminrbac.NewGate(grants, audit, now)
	if err != nil {
		return Layer{}, fmt.Errorf("adminfixture: gate: %w", err)
	}

	// Admin activity onto the P2.5 substrate. The sink logs the metric NAME and its non-sensitive
	// dimensions; there is no field here that could carry a secret.
	tel := adminops.NewTelemetry(adminops.TelemetrySinkFunc(func(m adminops.Metric) {
		log.Printf("%s metric %s %v %v", label, m.Name, m.Value, m.Dimensions)
	}), nil, now)
	tel.OnAlert(func(a adminops.Alert) { log.Printf("%s ANOMALY %s: %s", label, a.Metric, a.Description) })

	secrets, err := adminidentity.FixtureSecrets(label+"-sso", label+"-mfa", label+"-session")
	if err != nil {
		return Layer{}, fmt.Errorf("adminfixture: secrets: %w", err)
	}
	provider, err := adminidentity.NewHMACProvider(adminidentity.HMACProviderConfig{
		Issuer: Issuer, Secrets: secrets, Now: now, TestMode: true,
	})
	if err != nil {
		return Layer{}, fmt.Errorf("adminfixture: idp provider: %w", err)
	}
	sessions, err := adminidentity.NewSessionStore(adminidentity.SessionConfig{Now: now, Secrets: secrets, Observer: tel})
	if err != nil {
		return Layer{}, fmt.Errorf("adminfixture: sessions: %w", err)
	}

	principals := adminidentity.NewPrincipalStore()
	for _, r := range adminrbac.Roles {
		if err := principals.Put(adminidentity.Principal{
			AdminID: "adm-" + string(r), SSOSubject: "sso|" + string(r), MFAEnrolled: true,
			Status: adminidentity.StatusActive, CreatedAt: now(),
		}); err != nil {
			return Layer{}, fmt.Errorf("adminfixture: principal %s: %w", r, err)
		}
	}
	idp, err := adminidentity.NewIdPFixture(Issuer, secrets, now)
	if err != nil {
		return Layer{}, fmt.Errorf("adminfixture: idp fixture: %w", err)
	}
	authn, err := adminidentity.NewAuthenticator(provider, principals, sessions, tel)
	if err != nil {
		return Layer{}, fmt.Errorf("adminfixture: authenticator: %w", err)
	}
	exec, err := adminops.NewExecutor(gate, audit, tel, now)
	if err != nil {
		return Layer{}, fmt.Errorf("adminfixture: executor: %w", err)
	}

	return Layer{
		Authenticator: authn, Sessions: sessions, Gate: gate, Executor: exec,
		TestModeIdP: idp, Telemetry: tel, Audit: audit, Grants: grants,
	}, nil
}
