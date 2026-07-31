// Package adminfixture builds the TEST-MODE identity, authorization and command layer the operator
// console needs, so that more than one demo can serve the admin API without a second copy of the graph.
//
// # Why this is a package and not a function in one command
//
// `cmd/proof/operatorconsole` built this inline, with a comment saying it was one function "so the demo and a future
// integration test build the identical graph". The moment a second caller appeared — `cmd/proof/payments`,
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
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/providergateway"
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

	// Principals is the admin directory. Exposed (P22) because the session store now reconciles
	// against it — a caller that offboards has to reach the same store authorization reads.
	Principals *adminidentity.PrincipalStore
	// Factors is the MFA enrolment directory. Empty in a fixture deployment: the HMAC issuer carries
	// the IdP's own factor evidence, so no platform enrolment is required for it.
	Factors *adminidentity.FactorStore
	// IdP is the REAL OIDC provider when one is configured, typed for the browser-flow routes. Nil in a
	// fixture deployment, and the federated routes 404 — no half-wired federated surface to reach.
	IdP *adminidentity.OIDCProvider
	// Challenges mints the single-use WebAuthn challenges the login path consumes.
	Challenges *adminidentity.ChallengeStore
	// TOTPSeeds maps admin_id -> base32 seed, for the FEDERATED fixture only.
	//
	// # Why a demo has to solve this at all
	//
	// A federated deployment has a bootstrap deadlock: the platform will not issue an operator session
	// without a platform-verified factor, enrolling a factor needs a session, and nobody has one. In
	// production that is resolved out of band — the seed is provisioned into the secrets manager and
	// the principal is enrolled by whoever runs the deployment, before the first sign-in.
	//
	// A demo cannot ask for that, so it seeds one per fixture principal and PRINTS the enrolment URI.
	// The seeds are per-run and per-process; nothing is committed and nothing outlives the demo.
	TOTPSeeds map[string]string
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

	// The fixture's own signing keys, PLUS any identity credential the environment injects.
	//
	// The signing keys are fixture values because this layer is the fixture: a demo that made an
	// operator provision three keys before it would start would not be run. The identity credentials
	// are read from the environment under the `env` source's spelling of their reserved logical names
	// (`EnvSecretName`), so a federated demo resolves the SAME logical name a managed deployment
	// resolves from its secrets manager — one name, three sources, rather than a demo-shaped shortcut
	// that stops resembling production the moment it matters.
	fixtureSecrets := providergateway.StaticSecrets{
		adminidentity.SecretAdminSSOSigning:     {APIKey: label + "-sso"},
		adminidentity.SecretAdminMFASigning:     {APIKey: label + "-mfa"},
		adminidentity.SecretAdminSessionSigning: {APIKey: label + "-session"},
	}
	for _, logical := range adminidentity.SecretNames {
		if injected := strings.TrimSpace(os.Getenv(adminidentity.EnvSecretName(logical))); injected != "" {
			fixtureSecrets[logical] = providergateway.Credential{APIKey: injected}
		}
	}
	secrets, err := adminidentity.NewManagedSecrets(fixtureSecrets)
	if err != nil {
		return Layer{}, fmt.Errorf("adminfixture: secrets: %w", err)
	}
	// The provider is the FIXTURE unless the environment names a real one.
	//
	// The default is the fixture because this package IS the fixture: every P8 test and demo that calls
	// Build wants a runnable operator layer with no IdP to stand up, and making them all set an
	// environment variable would be making the common case pay for the rare one.
	//
	// The escape hatch exists so a real deployment is reachable from the same demo — `ADMIN_IDENTITY_MODE=oidc`
	// swaps in the real verifier, and `wire.ProviderFromEnv` is fail-static about what that mode needs.
	// Without it the real providers existed and nothing selected them, which is exactly the gap that
	// made "the operator console federates" untrue however it was configured.
	var provider adminidentity.IdentityProvider
	var oidc *adminidentity.OIDCProvider
	if mode := strings.TrimSpace(os.Getenv("ADMIN_IDENTITY_MODE")); mode != "" && mode != adminidentity.ModeTest {
		wired, err := adminidentity.ProviderFromEnv(secrets, Issuer)
		if err != nil {
			return Layer{}, fmt.Errorf("adminfixture: idp provider: %w", err)
		}
		provider, oidc = wired.Provider, wired.OIDC
	} else {
		p, err := adminidentity.NewHMACProvider(adminidentity.HMACProviderConfig{
			Issuer: Issuer, Secrets: secrets, Now: now, TestMode: true,
		})
		if err != nil {
			return Layer{}, fmt.Errorf("adminfixture: idp provider: %w", err)
		}
		provider = p
	}
	principalsForSessions := adminidentity.NewPrincipalStore()
	sessions, err := adminidentity.NewSessionStore(adminidentity.SessionConfig{
		Now: now, Secrets: secrets, Observer: tel,
		// The reconcile read (P22 task 6.3): a disabled principal holds no live session, whether or not
		// anybody remembered to revoke.
		Principals: principalsForSessions,
	})
	if err != nil {
		return Layer{}, fmt.Errorf("adminfixture: sessions: %w", err)
	}

	principals := principalsForSessions
	// ADMIN_DEMO_SSO_SUBJECT points the Superadmin principal at a REAL IdP subject.
	//
	// Without it a federated demo fails one step past the redirect, in a way that reads as a bug and is
	// not one: a real IdP's `sub` is its own opaque user id (Okta issues `00u…`), and the fixture
	// principals are keyed on `sso|superadmin`. `BySubject` finds nothing and returns
	// ErrUnknownPrincipal — correctly, because "the IdP asserted somebody we have never heard of" is a
	// security event rather than a signup.
	//
	// Production resolves this by provisioning the operator directory with real subjects. A demo has
	// nobody to provision it, so it takes one subject from the environment and says so.
	demoSubject := strings.TrimSpace(os.Getenv("ADMIN_DEMO_SSO_SUBJECT"))
	for _, r := range adminrbac.Roles {
		subject := "sso|" + string(r)
		if demoSubject != "" && r == adminrbac.RoleSuperadmin {
			subject = demoSubject
		}
		if err := principals.Put(adminidentity.Principal{
			AdminID: "adm-" + string(r), SSOSubject: subject, MFAEnrolled: true,
			Status: adminidentity.StatusActive, CreatedAt: now(),
		}); err != nil {
			return Layer{}, fmt.Errorf("adminfixture: principal %s: %w", r, err)
		}
	}
	idp, err := adminidentity.NewIdPFixture(Issuer, secrets, now)
	if err != nil {
		return Layer{}, fmt.Errorf("adminfixture: idp fixture: %w", err)
	}
	// A REAL provider proves only the SSO subject, so it needs a platform-verified factor — and
	// NewAuthenticatorFor refuses to construct without one, because a real provider with no verifier
	// authenticates on SSO alone and every login looks fine.
	factors := adminidentity.NewFactorStore()
	var verifier adminidentity.FactorVerifier
	if oidc != nil {
		v, err := adminidentity.NewPlatformFactors(adminidentity.PlatformFactorsConfig{
			Factors: factors, Secrets: secrets, Now: now,
			RPID:    strings.TrimSpace(os.Getenv("ADMIN_WEBAUTHN_RP_ID")),
			Origins: []string{strings.TrimSpace(os.Getenv("ADMIN_CONSOLE_ORIGIN"))},
		})
		if err != nil {
			return Layer{}, fmt.Errorf("adminfixture: factor verifier: %w", err)
		}
		verifier = v
	}
	// Seed a factor per principal when federated. A real provider proves only the SSO subject, so
	// without this the demo would federate correctly and then refuse every login — technically right
	// and useless as a demonstration.
	totpSeeds := map[string]string{}
	if oidc != nil {
		for _, r := range adminrbac.Roles {
			adminID := "adm-" + string(r)
			raw := make([]byte, 20)
			if _, err := rand.Read(raw); err != nil {
				return Layer{}, fmt.Errorf("adminfixture: totp seed: %w", err)
			}
			seed := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
			totpSeeds[adminID] = seed
			fixtureSecrets[adminidentity.TOTPSeedName(adminID)] = providergateway.Credential{APIKey: seed}
			if err := factors.Enroll(adminidentity.EnrolledFactor{
				AdminID: adminID, Kind: adminidentity.FactorTOTP,
				SecretName: adminidentity.TOTPSeedName(adminID),
			}); err != nil {
				return Layer{}, fmt.Errorf("adminfixture: enrol %s: %w", adminID, err)
			}
		}
	}

	authn, err := adminidentity.NewAuthenticatorFor(adminidentity.AuthenticatorConfig{
		Provider: provider, Principals: principals, Sessions: sessions, Observer: tel,
		Factors: verifier, Now: now,
	})
	if err != nil {
		return Layer{}, fmt.Errorf("adminfixture: authenticator: %w", err)
	}
	exec, err := adminops.NewExecutor(gate, audit, tel, now)
	if err != nil {
		return Layer{}, fmt.Errorf("adminfixture: executor: %w", err)
	}

	layer := Layer{
		Authenticator: authn, Sessions: sessions, Gate: gate, Executor: exec,
		Telemetry: tel, Audit: audit, Grants: grants,
		Principals: principals, Factors: factors, IdP: oidc, TOTPSeeds: totpSeeds,
		Challenges: adminidentity.NewChallengeStore(now),
	}
	// The test-mode issuer is present ONLY in a fixture deployment. A federated one leaves it nil, so
	// the /testmode/assert route 404s and there is no second door into the operator surface.
	if oidc == nil {
		layer.TestModeIdP = idp
	}
	return layer, nil
}
