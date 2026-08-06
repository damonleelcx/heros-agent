package launch

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/mailer"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/seats"
	"github.com/heros-foreal/agentd/internal/signup"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

// accountsystem.go composes P27's identity domain at boot: pick the store, seed it from configuration,
// point `auth.Registry` at it, and report what happened.
//
// # The order matters and is not arbitrary
//
// The store is chosen first, the seed runs SECOND, and only then does the registry start resolving
// against the store. A registry pointed at an unseeded store would refuse every configured credential
// for as long as the seed takes — which on a deployment with a slow database is a window in which every
// existing customer is locked out by an upgrade.
//
// # 🔴 A partial seed refuses to serve
//
// `Seed` returns an error the moment an entry cannot be written, and this function turns that into a
// failed boot. A platform that starts and then rejects seven of its ten customers looks broken to seven
// customers and healthy to its monitoring; a platform that will not start says exactly what is wrong,
// once, to the person doing the deploy. That is the same posture `openPlatformDB` takes for an
// unreachable DSN and `identity.ts` takes for a development identity provider in production.

// SelfServeSignupEnv is the DECLARED self-serve posture.
//
// 🔴 Unset means OFF. It is never inferred from whether an identity provider is configured: the three
// deployment shapes this product ships in want three different answers, so no single inference is right,
// and an air-gapped install must not grow a registration form by upgrading.
const SelfServeSignupEnv = "HEROS_SELF_SERVE_SIGNUP"

// SelfServeEnabled reads the declared posture. Only an explicit affirmative turns it on.
func SelfServeEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(SelfServeSignupEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// AccountSystem is what the boot path builds: the store and the posture it reports. The REGISTRY is not
// built here — the server already owns exactly one, and this wiring points that one at the store rather
// than creating a second. Two registries is how a deployment ends up authenticating against the
// configuration file on one path and the database on another.
type AccountSystem struct {
	Store   tenancy.Store
	Posture tenancy.Posture
	// Surface is what `internal/api` mounts. Nil when this deployment cannot serve the account routes —
	// which today means it has no plan catalog, because a members page that cannot say how many seats
	// the plan includes is a members page that invites a support ticket on its first use.
	Surface *accountSurface
}

// buildAccountSystem picks a store, seeds it, and returns the wiring.
//
// `platformDB` nil selects the in-memory store. That is honest rather than degraded: a single-container
// deployment with no Postgres has always run this way, and the posture SAYS `memory` so a deployment
// that believes it is durable finds out here rather than after a restart.
func buildAccountSystem(cfg config.Config, platformDB *sql.DB, now time.Time) (*AccountSystem, error) {
	var (
		store tenancy.Store
		kind  tenancy.StoreKind
	)
	if platformDB != nil {
		pg, err := tenancy.NewPGStore(platformDB)
		if err != nil {
			return nil, fmt.Errorf("identity store: %w", err)
		}
		store, kind = pg, tenancy.StorePostgres
	} else {
		store, kind = tenancy.NewMemStore(), tenancy.StoreMemory
	}

	entries := make([]tenancy.SeedEntry, 0, len(cfg.TenantCredentials))
	for _, c := range cfg.TenantCredentials {
		entries = append(entries, tenancy.SeedEntry{
			TenantID: c.TenantID, APIKey: c.APIKey, Role: c.Role, KeyID: c.KeyID,
		})
	}

	seed, err := tenancy.Seed(store, entries, now)
	if err != nil {
		// See the header: a partial seed refuses to serve.
		return nil, fmt.Errorf("tenant seed: %w — refusing to start with an incomplete set of "+
			"organizations; a platform that serves some of its customers is worse than one that says why "+
			"it did not start", err)
	}
	log.Printf("account system: store=%s self_serve_signup=%v; %s", kind, SelfServeEnabled(), seed.Summary())
	for _, skipped := range seed.Skipped {
		// Named, at WARN volume. A configured credential somebody believes works and that silently does
		// not is a support call with no evidence attached to it.
		log.Printf("account system: WARN configured credential skipped — %s", skipped)
	}

	sys := &AccountSystem{
		Store: store,
		Posture: tenancy.Posture{
			Store:           kind,
			SelfServeSignup: SelfServeEnabled(),
			Seed:            &seed,
			// Filled in below, once the mailer is built. It is FALSE here rather than absent because the
			// early-return paths (no plan catalog, a catalog that will not load) leave the surface unmounted
			// and never reach the mailer — and a deployment with no account surface genuinely cannot deliver
			// a confirmation link, so `false` is the true answer on those paths and not a placeholder.
			MailConfigured: false,
			IdentityKind:   consoleIdentityKind(),
		},
	}

	// The account (billing) store and the plan catalog. Both are needed for the seat allowance and for
	// sign-up's free plan; without either, the account SURFACE stays absent while the store and the seed
	// above still work — authentication does not depend on a catalog.
	at := now.UTC()
	var accounts account.Store
	if platformDB != nil {
		pg, err := account.NewPGStore(platformDB)
		if err != nil {
			return nil, fmt.Errorf("account store: %w", err)
		}
		accounts = pg
	} else {
		accounts = account.NewMemStore()
	}

	// 🔴 A seeded organization needs an account too, or it has no plan.
	//
	// `tenancy.Seed` deliberately knows nothing about billing — the two are separate bounded contexts,
	// and an import in that direction means an identity migration cannot run without a billing outage.
	// So the composition root, which is allowed to know about both, closes the gap: every organization
	// the seed created gets the same Free account a sign-up would have created.
	//
	// Without it a configured tenant has no `account` row, `SeatsAllowed` cannot resolve a plan, and the
	// members page reports an unlimited seat allowance for a plan that sets one — the "unmeasurable"
	// path being honest about a gap that did not need to exist. This was invisible before P27 only
	// because nothing read an account for a configured tenant at all.
	if err := ensureSeededAccounts(store, accounts, at); err != nil {
		return nil, err
	}

	catalog := planCatalogPath()
	if catalog == "" {
		log.Printf("account system: no plan catalog declared — the organization surface stays absent " +
			"(a members page that cannot state the seat allowance is a page that invites a support ticket)")
		return sys, nil
	}
	src := plancfg.NewFileSource(catalog)
	if _, err := src.Load(); err != nil {
		// The same posture the billing capability takes: a catalog whose EXISTENCE was proven and whose
		// CONTENTS were never read is a mounted surface that fails on its first use.
		log.Printf("account system: WARN the plan catalog at %s does not load (%v) — the organization "+
			"surface stays absent", catalog, err)
		return sys, nil
	}
	plans := signup.NewCatalogPlans(src)

	// Mail. 🔴 A configuration error here REFUSES THE BOOT rather than degrading silently — `mailer.New`
	// only errors on a configuration that is present and wrong (clear-text to a real host, an unknown TLS
	// mode), and a deployment that asked for mail and got a fallback would believe its reset links were
	// being delivered. An ABSENT configuration is not an error: it selects the operator fallback, which is
	// reported on the readiness surface rather than assumed away.
	mail, merr := mailer.New(mailer.ConfigFromEnv(), nil)
	if merr != nil {
		return nil, fmt.Errorf("mail: %w", merr)
	}
	if !mail.Configured() {
		log.Printf("account system: WARN mail is NOT configured — confirmation and password-reset links " +
			"cannot be delivered. They are held on the operator surface and reported by /readyz; set " +
			"HEROS_SMTP_HOST and HEROS_SMTP_FROM to deliver them.")
	}

	surface := &accountSurface{
		store:       store,
		selfServe:   SelfServeEnabled(),
		plans:       plans,
		accountRead: accounts,
		mail:        mail,
		consoleURL:  consoleBaseURL(),
		now:         func() time.Time { return time.Now().UTC() },
	}
	if SelfServeEnabled() {
		creator, cerr := tenancy.AsOrganizationCreator(store)
		if cerr != nil {
			return nil, fmt.Errorf("self-serve sign-up is enabled but the identity store cannot create organizations: %w", cerr)
		}
		svc, serr := signup.New(creator, accounts, plans, surface.now)
		if serr != nil {
			return nil, fmt.Errorf("sign-up: %w", serr)
		}
		surface.signUp = svc
	}
	sys.Posture.MailConfigured = mail.Configured()

	// The first owner, on a deployment with no sign-up form. Runs LAST, after the store, the seed and the
	// mailer are all in place — it needs all three, and a bootstrap that ran earlier would find no seeded
	// person to bootstrap. Idempotent and non-fatal: see ownerbootstrap.go.
	bootstrapOwner(store, mail, surface.consoleURL, at)

	sys.Surface = surface
	return sys, nil
}

// accountSurface adapts the boot-time wiring to the HTTP layer's `api.AccountSurface`.
//
// It exists so that `internal/api` depends on an INTERFACE it declares rather than on this package's
// composition — the transport layer should not know how a deployment chose its store — and so that a
// deployment which mounts no account system passes nil and gets no routes at all.
type accountSurface struct {
	store       tenancy.Store
	signUp      *signup.Service
	selfServe   bool
	plans       *signup.CatalogPlans
	accountRead account.Store
	// meter records the period's seat PEAK after a membership change. Nil on a deployment with no
	// metering, which makes ObserveSeats a no-op rather than a failure.
	meter *metering.Meter
	// mail delivers confirmation and reset links (P28). Never nil: `mailer.New` returns the operator-visible
	// fallback when no SMTP is configured, so "unconfigured" is a reported state and never a discard.
	mail       mailer.Mailer
	consoleURL string
	now        func() time.Time
}

func (a *accountSurface) Store() tenancy.Store    { return a.store }
func (a *accountSurface) SignUp() *signup.Service { return a.signUp }
func (a *accountSurface) SelfServeEnabled() bool  { return a.selfServe }
func (a *accountSurface) Now() time.Time          { return a.now() }
func (a *accountSurface) Mailer() mailer.Mailer   { return a.mail }
func (a *accountSurface) ConsoleURL() string      { return a.consoleURL }

// SeatsHeld is `entitlement.SeatCounter`: the seat count read from MEMBERSHIP, which is where it lives.
// The entitlement gate used to read a `seats` usage record that nothing ever wrote — see
// `internal/seats`' header for why that made a five-seat plan admit five hundred.
func (a *accountSurface) SeatsHeld(tenantID string) (int, error) {
	return seats.Current(a.store, tenantID)
}

// ObserveSeats re-derives and records the current period's seat peak.
//
// 🔴 A failure here is LOGGED, never returned to the caller. The membership change has already
// committed; refusing it after the fact is impossible, and reporting a billing-side failure as if the
// removal did not happen would be the more dangerous lie. The number is recoverable — `internal/seats`
// recomputes from the timeline — so the next membership change, or a re-run, corrects it.
func (a *accountSurface) ObserveSeats(tenantID string) {
	if a.meter == nil {
		return
	}
	if _, err := seats.Observe(a.store, a.meter, tenantID, metering.MonthPeriod(a.now())); err != nil {
		log.Printf("account system: WARN could not record the seat peak for %s: %v "+
			"(the membership change stands; the peak is re-derived on the next change)", tenantID, err)
	}
}

// SeatsAllowed resolves the organization's plan and returns its seat allowance.
//
// 🔴 An ABSENT allowance is unlimited, never zero. Returning zero on a plan with no seat limit would
// deny every invitation on the most permissive plan the catalog has, which is the failure mode the
// `plancfg.Limits` comment warns about in exactly these words.
func (a *accountSurface) SeatsAllowed(tenantID string) (float64, bool, error) {
	if a.accountRead == nil || a.plans == nil {
		return 0, false, nil
	}
	acct, err := a.accountRead.Get(tenantID)
	if err != nil {
		return 0, false, err
	}
	// An operator's per-tenant override REPLACES the plan's allowance for this one limit, unchanged
	// from P7 — so it is read first.
	if v, ok := acct.QuotaOverride(string(plancfg.LimitSeats)); ok {
		return v, true, nil
	}
	plan, err := a.plans.Resolve(acct.ActivePlanID)
	if err != nil {
		return 0, false, err
	}
	v, ok := plan.Limit(plancfg.LimitSeats)
	return v, ok, nil
}

// ensureSeededAccounts gives every organization the seed created the same Free account a sign-up would.
//
// Create-if-absent, like the seed itself: an organization that already has an account keeps it, plan and
// all. A seed that "corrected" an existing account would move a paying customer back to Free on the next
// restart, which is the reconciliation this whole file rejects, applied to money.
func ensureSeededAccounts(store tenancy.Store, accounts account.Store, at time.Time) error {
	catalog := planCatalogPath()
	if catalog == "" || accounts == nil {
		// No catalog means no plan to start anybody on. The organization surface stays absent for the
		// same reason, and saying so once is enough.
		return nil
	}
	plans := signup.NewCatalogPlans(plancfg.NewFileSource(catalog))
	freeID := plans.FreePlanID()
	if freeID == "" {
		log.Printf("account system: WARN the plan catalog has no plan that charges nothing, so seeded " +
			"organizations start on no plan; their seat allowance will report as unmeasurable")
		return nil
	}
	plan, err := plans.Resolve(freeID)
	if err != nil {
		return fmt.Errorf("account system: resolving the free plan: %w", err)
	}

	tenants, err := store.ListTenants()
	if err != nil {
		return fmt.Errorf("account system: listing organizations to seed accounts: %w", err)
	}
	created := 0
	for _, t := range tenants {
		if _, err := accounts.Get(t.TenantID); err == nil {
			continue
		} else if !errors.Is(err, account.ErrNotFound) {
			return fmt.Errorf("account system: reading the account for %q: %w", t.TenantID, err)
		}
		if _, err := accounts.Create(account.Account{
			CustomerID:        t.TenantID,
			ActivePlanID:      plan.PlanID,
			PlanConfigVersion: plan.Version,
			// No provider handle, legal precisely because the plan charges nothing.
			PlanCharges: false,
			CreatedAt:   at,
		}); err != nil && !errors.Is(err, account.ErrExists) {
			return fmt.Errorf("account system: creating the account for %q: %w", t.TenantID, err)
		}
		created++
	}
	if created > 0 {
		log.Printf("account system: created %d free account(s) for seeded organizations", created)
	}
	return nil
}
