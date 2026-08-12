// Package adminlaunch assembles the operator console's admin API for a REAL deployment.
//
// # The gap it closes
//
// Before it, the only thing that assembled this graph was `internal/adminfixture`, called from three
// `cmd/proof/*` demo binaries. `internal/launch` — the agentd boot path every deployment actually runs
// — never called `api.NewAdminAPI`, never called `adminidentity.ProviderFromEnv`, and never called
// `Server.SetAdminIdentity`. The consequences were all silent and all downstream of the same fact:
//
//   - The console's BFF was pointed at `ADMIN_API_BASE=http://agentd:4321` by every shipped manifest,
//     and agentd's mux has no `/admin/*` route in it, so every admin call 404'd.
//   - `/readyz` reported `admin_idp` ABSENT no matter what `ADMIN_IDP_ISSUER` was set to, because the
//     field is populated by `SetAdminIdentity` and nothing called it. Setting the issuer could not
//     change the symptom that made an operator go looking for it.
//   - `ADMIN_WEBAUTHN_RP_ID` and `ADMIN_CONSOLE_ORIGIN` were set on agentd by the Compose and
//     Kubernetes manifests and read only by `adminfixture`, a package agentd does not import.
//
// So the operator console federated in a demo and could not federate anywhere else, however it was
// configured. This package is the production counterpart of `adminfixture`: same graph, no fixtures.
//
// # What it deliberately does NOT do
//
// It builds the identity, authorization and command layer, and it leaves every operator SERVICE nil
// (`Tenants`, `Billing`, `Registry`, …). Those services exist only over in-memory demo stores today —
// `cmd/proof/operatorconsole` fabricates five synthetic tenants and a stub billing provider to fill
// them. Wiring those into production would put invented tenants in front of an operator, which is worse
// than an empty surface: `api.AdminDeps` routes an unmounted service to a 503 that NAMES itself, so the
// console renders "not mounted" and nobody is misled. Sign-in works completely; the data surfaces
// behind it light up when they get durable sources, one at a time.
//
// The audit log is likewise still `adminaudit.NewMemoryStore` — hash-chained and append-only, but
// restarting from empty. `audit_entry` exists in migration 0014 and giving it a writer is its own piece
// of work. It is stated here, and in the deploy notes, rather than left to be discovered.
//
// # Fail static, at boot
//
// Every refusal below happens at STARTUP. This package is only reached when the deployment declares a
// federated operator console, and a federated console that is missing a piece must not come up: the
// alternative is a sign-in page that renders correctly and refuses everyone, which is the failure mode
// `adminidentity/wire.go` was written to prevent one layer down.
package adminlaunch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/adminstore"
	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/erroreport"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// EnvListenAddr is the admin API's own listen address.
//
// Its OWN listener, not a route group on agentd's mux, and that is P8 Decision 11 rather than a
// deployment preference: the operator console is isolated from the customer console by the BROWSER
// ORIGIN boundary, and that only holds if the server side is separate too. An `/admin` route group
// inside the customer server would put a cross-tenant capability one routing mistake away from a tenant
// session. `internal/api/adminconsole.go` says the same thing at the top of the file, and this constant
// is what keeps it true on a deployment rather than only in a demo.
//
// 🔴 It must never be published through an Ingress. The only caller is the admin BFF, inside the
// cluster, presenting the platform credential.
const EnvListenAddr = "ADMIN_API_LISTEN_ADDR"

// DefaultListenAddr is the port the demo binaries already use, so an operator who has read the P8 docs
// finds the surface where those docs say it is.
const DefaultListenAddr = "0.0.0.0:4311"

// Assembly is a wired admin API plus the pieces the caller still needs to reach.
type Assembly struct {
	// API is the admin surface, credential gate included.
	API *api.AdminAPI
	// Identity is the live authenticator, for `Server.SetAdminIdentity` so `/readyz` reports `admin_idp`.
	Identity *adminidentity.Authenticator
	// ListenAddr is where the caller should serve API.Handler.
	ListenAddr string
	// Mode is the resolved identity mode, for the boot log.
	Mode string
	// Issuer is the resolved IdP issuer, for the boot log. Not a secret: it is a public URL a browser is
	// about to be redirected to.
	Issuer string
}

// Federated reports whether this deployment declares a real operator IdP.
//
// It reads ADMIN_IDENTITY_MODE and nothing else, so `internal/launch` can decide whether to build any
// of this without importing the reasons. `test` and unset are both false: a test-mode operator console
// is served by the demo binaries, which is where its fixture issuer belongs.
func Federated() bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv("ADMIN_IDENTITY_MODE"))) {
	case adminidentity.ModeOIDC, adminidentity.ModeSAML:
		return true
	default:
		return false
	}
}

// Build assembles the admin API from the deployment's environment, secrets source and platform database.
//
// `platformDB` is REQUIRED and the refusal is the point — see the comment on that check.
func Build(ctx context.Context, secretsSrc providergateway.Secrets, platformDB *sql.DB, reporter erroreport.Reporter) (*Assembly, error) {
	now := func() time.Time { return time.Now().UTC() }

	if secretsSrc == nil {
		return nil, errors.New("adminlaunch: a secrets source is required — the operator signing keys are never read from code or config")
	}
	// 🔴 No durable directory, no federated operator console.
	//
	// Every other store in this graph can be rebuilt; the DIRECTORY cannot. On a federated deployment
	// `NewAuthenticatorFor` requires a platform-verified factor, so an empty enrolment directory refuses
	// every sign-in — and the only way to enrol a factor is through a session, which needs a factor.
	// An in-memory directory therefore does not degrade at a restart, it locks every operator out
	// permanently, with nothing logged: from the process's point of view it simply started with an empty
	// map. Refusing to boot is the only honest option.
	if platformDB == nil {
		return nil, errors.New("adminlaunch: a federated operator console requires the platform database (DATABASE_URL) — " +
			"the admin directory must survive a restart, because enrolling a factor requires a session and issuing a " +
			"session requires a factor, so an in-memory directory is a permanent lockout rather than lost state")
	}

	secrets, err := adminidentity.NewManagedSecrets(secretsSrc)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: secrets: %w", err)
	}

	// The BFF's credential, resolved through the same seam as every other platform credential. The
	// CONSOLE takes it from its own environment because a Next.js process has no secrets seam; both sides
	// are injected from one secret, so there is one value and one place to rotate it.
	credential, err := secrets.Named(ctx, adminidentity.SecretAdminPlatformCredential)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: the admin BFF credential could not be resolved: %w", err)
	}

	wired, err := adminidentity.ProviderFromEnv(secrets, "")
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	if wired.OIDC == nil && wired.SAML == nil {
		return nil, fmt.Errorf("adminlaunch: ADMIN_IDENTITY_MODE=%s resolved no federated provider", wired.Mode)
	}

	// The two WebAuthn bindings, required rather than defaulted.
	//
	// `NewPlatformFactors` accepts both empty, and the result is quiet in the worst way: TOTP keeps
	// working, so sign-in looks fine, while the origin allowlist holds one empty string that matches no
	// real origin — so every security key silently fails and the operator concludes their key is broken.
	// Origin binding is the ONE thing WebAuthn gives over TOTP; a deployment that has not stated these
	// has not configured WebAuthn, and should be told so at boot.
	rpID := strings.TrimSpace(os.Getenv("ADMIN_WEBAUTHN_RP_ID"))
	origin := strings.TrimSpace(os.Getenv("ADMIN_CONSOLE_ORIGIN"))
	if rpID == "" || origin == "" {
		return nil, errors.New("adminlaunch: a federated operator console requires ADMIN_WEBAUTHN_RP_ID and " +
			"ADMIN_CONSOLE_ORIGIN — left empty, TOTP still works and every security key silently fails an " +
			"origin check against an allowlist holding one empty string")
	}

	store, err := adminstore.New(platformDB)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}

	// ── The three durable directories ──
	//
	// Writer first, THEN load. The other order would replay rows into a store that is not yet writing
	// through, which is harmless today and is exactly the kind of ordering that stops being harmless when
	// someone adds a mutation to the load path.
	principals := adminidentity.NewPrincipalStore()
	if err := principals.SetWriter(store); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	principalRows, err := store.Principals(ctx)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	if err := adminidentity.LoadPrincipals(principals, principalRows); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}

	factors := adminidentity.NewFactorStore()
	if err := factors.SetWriter(store); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	factorRows, err := store.Factors(ctx)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	if err := adminidentity.LoadFactors(factors, factorRows); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}

	grants := adminrbac.NewGrantStore(now)
	if err := grants.SetWriter(store); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	grantRows, err := store.Grants(ctx)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	if err := adminrbac.LoadGrants(grants, grantRows); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}

	// A directory with nobody in it is not an error — it is a deployment that has not been bootstrapped
	// yet, and refusing to boot would mean the bootstrap command could never run against a live process.
	// It is LOGGED, at the one moment somebody is reading the boot output, naming the command that fixes
	// it. The alternative is a console that redirects to Okta, comes back, and refuses with a message
	// about the second factor — which is true and points at entirely the wrong thing.
	if len(principalRows) == 0 {
		log.Printf("operator console: the admin directory is EMPTY — no operator can sign in until one is " +
			"bootstrapped (`agentd -admin-bootstrap-subject=<idp sub>`)")
	} else {
		log.Printf("operator console: %d admin principal(s), %d enrolled factor(s), %d role-grant row(s) loaded from the platform database",
			len(principalRows), len(factorRows), len(grantRows))
	}

	// ── Telemetry, audit, sessions, verification ──
	tel := adminops.NewTelemetry(adminops.TelemetrySinkFunc(func(m adminops.Metric) {
		log.Printf("admin metric %s %v %v", m.Name, m.Value, m.Dimensions)
	}), nil, now)
	tel.OnAlert(func(a adminops.Alert) { log.Printf("admin ANOMALY %s: %s", a.Metric, a.Description) })

	// 🔴 In-memory, and it starts empty on every boot. `audit_entry` is in migration 0014 with a
	// write-once trigger and a hash chain; it has no Go writer yet. Until it does, `/audit` shows what
	// this process has seen, which is less than "every admin action". Logged rather than left implicit.
	audit := adminaudit.NewMemoryStore(now)
	log.Printf("operator console: the admin audit log is IN-MEMORY and starts empty at each restart — " +
		"audit_entry has no writer yet, so /audit covers this process's lifetime, not the deployment's")

	sessions, err := adminidentity.NewSessionStore(adminidentity.SessionConfig{
		Now: now, Secrets: secrets, Observer: tel, Principals: principals,
	})
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: sessions: %w", err)
	}

	verifier, err := adminidentity.NewPlatformFactors(adminidentity.PlatformFactorsConfig{
		Factors: factors, Secrets: secrets, Now: now, RPID: rpID, Origins: []string{origin},
	})
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: factor verifier: %w", err)
	}

	authn, err := adminidentity.NewAuthenticatorFor(adminidentity.AuthenticatorConfig{
		Provider: wired.Provider, Principals: principals, Sessions: sessions,
		Observer: tel, Factors: verifier, Now: now,
	})
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: authenticator: %w", err)
	}

	gate, err := adminrbac.NewGate(grants, audit, now)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: gate: %w", err)
	}
	exec, err := adminops.NewExecutor(gate, audit, tel, now)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: executor: %w", err)
	}

	// ── The operator SERVICES (services.go) ──
	//
	// Every one that has a durable source is mounted; the four that do not are named in the log with
	// the reason. This is what turns the console's pages from "this deployment does not carry the X
	// surface" into real data — they were nil not because the stores were missing but because nothing
	// handed the admin surface the ones `internal/launch/capabilities.go` already builds.
	sources, err := buildSources(platformDB, secretsSrc, now)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: operator sources: %w", err)
	}
	var svcs serviceSet
	if err := mountServices(exec, sources, sessions, authn.Describe(), newReadinessSource(readyzAddr()), &svcs); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}

	adminAPI, err := api.NewAdminAPI(api.AdminDeps{
		PlatformCredential: string(credential),
		Authenticator:      authn,
		Sessions:           sessions,
		Gate:               gate,
		Executor:           exec,
		// The federated browser flow. `TestModeIdP` is deliberately left nil: a federated deployment has
		// no fixture issuer, so `/admin/api/testmode/assert` 404s and there is no second door.
		IdP:        wired.OIDC,
		Challenges: adminidentity.NewChallengeStore(now),
		Factors:    factors,
		// The operator services. Those still nil (kill switch, model registry, GDPR, releases, axes)
		// route to a 503 that NAMES itself rather than to fabricated data — see services.go for why each
		// one is a decision.
		Tenants:       svcs.Tenants,
		Entitlements:  svcs.Entitlements,
		Billing:       svcs.Billing,
		Jobs:          svcs.Jobs,
		Impersonation: svcs.Impersonation,
		CrossTenant:   svcs.CrossTenant,
		Audit:         svcs.Audit,
		Delivery:      svcs.Delivery,
		Oversight:     svcs.Oversight,
		KillSwitch:    svcs.KillSwitch,
		Registry:      svcs.Registry,
		Release:       svcs.Release,
		Axis:          svcs.Axis,
		Agent:         svcs.Agent,
		GDPR:          svcs.GDPR,
		ErrorReporter: reporter,
		Secrets:       secretsSrc,
		Now:           now,
	})
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: admin API: %w", err)
	}

	addr := strings.TrimSpace(os.Getenv(EnvListenAddr))
	if addr == "" {
		addr = DefaultListenAddr
	}
	return &Assembly{
		API: adminAPI, Identity: authn, ListenAddr: addr,
		Mode: wired.Mode, Issuer: authn.Describe().Issuer,
	}, nil
}

// Serve starts the admin API on its own listener and returns the server so the caller can shut it down.
func (a *Assembly) Serve() (*http.Server, error) {
	srv := &http.Server{
		Addr:              a.ListenAddr,
		Handler:           a.API.Handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := netListen(a.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: listen %s: %w", a.ListenAddr, err)
	}
	go func() {
		log.Printf("operator admin API listening on http://%s (%s, issuer %s) — this port must NOT be published through an ingress",
			ln.Addr().String(), a.Mode, a.Issuer)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("operator admin API: %v", err)
		}
	}()
	return srv, nil
}

// readyzAddr is where this process serves its own /readyz — the customer listener, not the admin one.
func readyzAddr() string {
	if v := strings.TrimSpace(os.Getenv("HEROS_LISTEN_ADDR")); v != "" {
		return v
	}
	return "0.0.0.0:4321"
}

// netListen is a seam so Serve's error path is testable without binding a privileged port. It is not an
// abstraction over the network: there is exactly one implementation and it is net.Listen.
var netListen = func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }
