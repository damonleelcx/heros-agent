package adminops_test

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// harness_test.go is the P8 operations fixture (task 14.1): a test-mode admin IdP with SSO + MFA,
// FOUR admin principals (one per role), synthetic tenants on NAMED plans, an append-only hash-chained
// audit store, and the wired command path.
//
// It is shared by every test in this package on purpose. The alternative — each test standing up its
// own partial wiring — is how a test ends up asserting against a stack that is missing the gate it
// meant to prove.

const (
	tenantAcme    = "tenant-acme"   // Enterprise, autonomous merges entitled
	tenantBoreal  = "tenant-boreal" // Business
	tenantCastle  = "tenant-castle" // Team
	adminIdPIssue = "https://admin-idp.test.heros.internal"
)

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }
func (c *testClock) advance(d time.Duration) time.Time {
	c.t = c.t.Add(d)
	return c.t
}

// harness is the wired operator stack under test.
type harness struct {
	t         *testing.T
	clk       *testClock
	audit     *adminaudit.MemoryStore
	grants    *adminrbac.GrantStore
	gate      *adminrbac.Gate
	exec      *adminops.Executor
	accounts  *account.MemStore
	plansrc   *plancfg.MemSource
	plans     *plancfg.Resolver
	admission *adminops.Admission
	tenants   *adminops.TenantService
	sessions  *adminidentity.SessionStore
	authn     *adminidentity.Authenticator
	tokens    map[adminrbac.Role]string
	adminIDs  map[adminrbac.Role]string
	events    []adminops.CommandEvent

	// P7 billing stack the oversight surface reads and commands.
	entitlements  *adminops.EntitlementService
	bills         *adminops.BillingService
	billing       *billing.Service
	provider      *billing.StubProvider
	meter         *metering.Meter
	usage         *metering.MemUsageStore
	costEvents    *metering.MemCostEvents
	auditView     *adminops.AuditService
	crossTenant   *adminops.CrossTenantService
	gdpr          *adminops.GDPRService
	subjects      *adminops.MemSubjectStore
	impersonation *adminops.ImpersonationService
	kill          *adminops.KillSwitchService
	killStore     *adminops.MemKillSwitchStore
	jobs          *adminops.JobService
	jobQueue      *seamQueue
	registry      *adminops.RegistryService
	models        *adminops.ModelRegistry
	deltas        *metering.MemVerifiedDeltas
	savings       *metering.MemSavingsStore
	links         *linkingest.MemStore
	authored      *authoring.MemRecorder
	deliveries    *deliveryrecord.MemStore
	delivery      *adminops.DeliveryService
	period        metering.Period
}

// planCatalog is the fixture plan catalog. Plans are NAMED; limits are quantities; price references
// are opaque strings into the billing provider. No amount, percentage or band appears anywhere.
const planCatalog = `{
  "version": "plan-cfg-2026-03-01",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,"features":["cli","discovery"],"limits":{},"price_refs":{}},
    {"plan_id":"team","display_name":"Team","rank":1,"features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"seats":5,"retention_days":30},
     "price_refs":{"subscription":"price_ref_team_subscription","metered":"price_ref_team_metered"}},
    {"plan_id":"business","display_name":"Business","rank":2,"features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"seats":25,"retention_days":90},
     "price_refs":{"subscription":"price_ref_business_subscription","metered":"price_ref_business_metered"}},
    {"plan_id":"enterprise","display_name":"Enterprise","rank":3,
     "features":["cli","discovery","assisted_pr","dashboard","auto_merge"],
     "limits":{"seats":250,"retention_days":365},
     "price_refs":{"subscription":"price_ref_enterprise_subscription","metered":"price_ref_enterprise_metered","gainshare":"price_ref_enterprise_gainshare"}}
  ]
}`

func newHarness(t *testing.T) *harness {
	t.Helper()
	clk := &testClock{t: time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)}
	audit := adminaudit.NewMemoryStore(clk.now)

	// ── four admin principals, one per role ──
	grants := adminrbac.NewGrantStore(clk.now)
	adminIDs := map[adminrbac.Role]string{}
	for _, r := range adminrbac.Roles {
		id := "adm-" + string(r)
		adminIDs[r] = id
		if _, err := grants.Seed(id, r, "fixture: one admin principal per role"); err != nil {
			t.Fatalf("Seed %s: %v", r, err)
		}
	}
	gate, err := adminrbac.NewGate(grants, audit, clk.now)
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}

	h := &harness{t: t, clk: clk, audit: audit, grants: grants, gate: gate, adminIDs: adminIDs}

	exec, err := adminops.NewExecutor(gate, audit, adminops.ObserverFunc(func(ev adminops.CommandEvent) {
		h.events = append(h.events, ev)
	}), clk.now)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	h.exec = exec

	// ── admin IdP (test mode), sessions, one live session per role ──
	secrets, err := adminidentity.FixtureSecrets("sso-k", "mfa-k", "session-k")
	if err != nil {
		t.Fatalf("FixtureSecrets: %v", err)
	}
	provider, err := adminidentity.NewHMACProvider(adminidentity.HMACProviderConfig{
		Issuer: adminIdPIssue, Secrets: secrets, Now: clk.now, TestMode: true,
	})
	if err != nil {
		t.Fatalf("NewHMACProvider: %v", err)
	}
	sessions, err := adminidentity.NewSessionStore(adminidentity.SessionConfig{Now: clk.now, Secrets: secrets})
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	h.sessions = sessions
	principals := adminidentity.NewPrincipalStore()
	idp, err := adminidentity.NewIdPFixture(adminIdPIssue, secrets, clk.now)
	if err != nil {
		t.Fatalf("NewIdPFixture: %v", err)
	}
	authn, err := adminidentity.NewAuthenticator(provider, principals, sessions, nil)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	h.authn = authn
	h.tokens = map[adminrbac.Role]string{}
	for _, r := range adminrbac.Roles {
		subject := "sso|" + string(r)
		if err := principals.Put(adminidentity.Principal{
			AdminID: adminIDs[r], SSOSubject: subject, MFAEnrolled: true,
			Status: adminidentity.StatusActive, CreatedAt: clk.now(),
		}); err != nil {
			t.Fatalf("Put principal: %v", err)
		}
		assertion, err := idp.Assert(context.Background(), subject, "webauthn")
		if err != nil {
			t.Fatalf("Assert: %v", err)
		}
		_, token, err := authn.Authenticate(context.Background(), assertion)
		if err != nil {
			t.Fatalf("Authenticate %s: %v", r, err)
		}
		h.tokens[r] = token
	}

	// ── plan configuration (config store, not git) ──
	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(planCatalog)); err != nil {
		t.Fatalf("PublishJSON: %v", err)
	}
	resolver := plancfg.NewResolver(src, plancfg.NewMemAudit())
	resolver.SetClock(clk.now)
	if _, err := resolver.Reload("fixture"); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	h.plansrc, h.plans = src, resolver

	// ── synthetic tenants on named plans ──
	//
	// The provider handle comes from the provider itself, exactly as it does in production: the
	// platform stores the handle the billing provider issued, never a locally-invented string.
	h.provider = billing.NewStubProvider()
	accounts := account.NewMemStore()
	for _, tc := range []struct{ id, plan string }{
		{tenantAcme, "enterprise"}, {tenantBoreal, "business"}, {tenantCastle, "team"},
	} {
		handle, err := h.provider.EnsureCustomer(context.Background(), tc.id)
		if err != nil {
			t.Fatalf("EnsureCustomer %s: %v", tc.id, err)
		}
		if _, err := accounts.Create(account.Account{
			CustomerID: tc.id, ProviderCustomerHandle: handle, ActivePlanID: tc.plan,
			PlanConfigVersion: resolver.Version(), GainshareConsent: true, CreatedAt: clk.now(),
		}); err != nil {
			t.Fatalf("Create %s: %v", tc.id, err)
		}
	}
	h.accounts = accounts

	admission, err := adminops.NewAdmission(accounts, nil)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}
	h.admission = admission

	// ── the operator kill switch, wired into the SAME admission gate the P6 loop reads ──
	h.killStore = adminops.NewMemKillSwitchStore()
	killSvc, err := adminops.NewKillSwitchService(exec, h.killStore, adminops.DefaultKillSwitchPolicy())
	if err != nil {
		t.Fatalf("NewKillSwitchService: %v", err)
	}
	h.kill = killSvc
	admission.SetKillSwitch(killSvc)

	tenants, err := adminops.NewTenantService(exec, accounts, resolver, admission)
	if err != nil {
		t.Fatalf("NewTenantService: %v", err)
	}
	h.tenants = tenants

	// ── entitlement override ──
	ents, err := adminops.NewEntitlementService(exec, accounts, resolver)
	if err != nil {
		t.Fatalf("NewEntitlementService: %v", err)
	}
	h.entitlements = ents

	// ── the P7 billing stack the oversight surface reads and commands ──
	h.period = metering.MonthPeriod(clk.now())
	h.costEvents = metering.NewMemCostEvents()
	h.usage = metering.NewMemUsageStore()
	meter := metering.NewMeter(h.costEvents, h.usage)
	meter.SetClock(clk.now)
	h.meter = meter
	billingSecrets, err := billing.NewManagedSecrets(providergateway.StaticSecrets{
		billing.SecretBillingAPIKey:         {APIKey: "billing-provider-key"},
		billing.SecretBillingWebhookSigning: {APIKey: "billing-webhook-secret"},
	})
	if err != nil {
		t.Fatalf("billing.NewManagedSecrets: %v", err)
	}
	svc, err := billing.NewService(h.provider, billing.NewMemLedger(), accounts, resolver, meter, billingSecrets)
	if err != nil {
		t.Fatalf("billing.NewService: %v", err)
	}
	svc.SetClock(clk.now)
	h.billing = svc
	h.deltas = metering.NewMemVerifiedDeltas()
	h.savings = metering.NewMemSavingsStore()
	// The P11 link-coverage source. Wired empty: coverage starts UNKNOWN, and a test that wants a
	// figure rendered has to say what the platform observed — which is the property under test.
	h.links = linkingest.NewMemStore()
	// The append-only authored-change record the improvement aggregate reads through the ONE filter.
	h.authored = authoring.NewMemRecorder()

	bill, err := adminops.NewBillingService(exec, svc, h.deltas, h.links)
	if err != nil {
		t.Fatalf("NewBillingService: %v", err)
	}
	h.bills = bill

	// ── model registry (configuration, not git) ──
	h.models = adminops.NewModelRegistry(clk.now)
	reg, err := adminops.NewRegistryService(exec, h.models)
	if err != nil {
		t.Fatalf("NewRegistryService: %v", err)
	}
	h.registry = reg

	// ── the EXISTING P4/P6 queue, through the operator seam ──
	h.jobQueue = newSeamQueue()
	jobs, err := adminops.NewJobService(exec, h.jobQueue)
	if err != nil {
		t.Fatalf("NewJobService: %v", err)
	}
	h.jobs = jobs

	// ── impersonation, which also installs the command path's write guard ──
	imp, err := adminops.NewImpersonationService(exec)
	if err != nil {
		t.Fatalf("NewImpersonationService: %v", err)
	}
	h.impersonation = imp

	// ── the audit-log viewer ──
	av, err := adminops.NewAuditService(exec)
	if err != nil {
		t.Fatalf("NewAuditService: %v", err)
	}
	h.auditView = av

	// ── cross-tenant read models over the P2.5 substrate ──
	ct, err := adminops.NewCrossTenantService(exec, adminops.CrossTenantConfig{
		Accounts: accounts, Meter: meter, Ledger: svc.Ledger(), Admission: admission,
		Authored: h.authored, Deltas: h.deltas,
	})
	if err != nil {
		t.Fatalf("NewCrossTenantService: %v", err)
	}
	h.crossTenant = ct

	// ── P26 delivery oversight over the REAL P12 record ──
	h.deliveries = deliveryrecord.NewMemStore()
	dlv, err := adminops.NewDeliveryService(exec, h.deliveries, accounts)
	if err != nil {
		t.Fatalf("NewDeliveryService: %v", err)
	}
	h.delivery = dlv

	// ── compliance ──
	h.subjects = adminops.NewMemSubjectStore()
	gd, err := adminops.NewGDPRService(exec, h.subjects)
	if err != nil {
		t.Fatalf("NewGDPRService: %v", err)
	}
	h.gdpr = gd
	return h
}

// seedMeteredCharge puts a settled metered charge on a tenant's ledger, so a correction has something
// real to correct. It walks the REAL P7 path — report usage, then charge — rather than writing a
// ledger row directly, because a credit against a row nothing produced proves nothing.
func (h *harness) seedMeteredCharge(tenantID string) string {
	h.t.Helper()
	ctx := context.Background()
	h.costEvents.Attribute("run-"+tenantID, tenantID)
	h.costEvents.Put(metricevent.Event{
		SchemaVersion: metricevent.SchemaVersion, RunID: "run-" + tenantID, VariantID: "v1", NodeID: "n1",
		MetricName: telemetry.MetricCostUSD, Value: floatPtr(4), Unit: "usd",
		Timestamp: h.clk.now().Format(time.RFC3339Nano), ConfigHash: "cfg1",
	})
	// Derive the period's SUM from those cost events — the P2.5 derivation, not a hand-written row.
	if _, _, err := h.meter.RecordSUM(tenantID, h.period); err != nil {
		h.t.Fatalf("RecordSUM: %v", err)
	}
	if _, err := h.billing.StartSubscription(ctx, tenantID); err != nil {
		h.t.Fatalf("StartSubscription: %v", err)
	}
	if _, err := h.billing.ReportUsage(ctx, tenantID, h.period, metering.MetricSUM); err != nil {
		h.t.Fatalf("ReportUsage: %v", err)
	}
	ev, err := h.billing.Charge(ctx, tenantID, h.period, billing.KindMetered, "metered-"+tenantID+"-"+h.period.ID)
	if err != nil {
		h.t.Fatalf("Charge: %v", err)
	}
	return ev.EventID
}

func floatPtr(f float64) *float64 { return &f }

// ctx returns a request context carrying a live admin session for the given role — the same path a
// real request takes, via token verification rather than by constructing a Session literal.
func (h *harness) ctx(role adminrbac.Role) context.Context {
	h.t.Helper()
	sess, err := h.sessions.Authorize(context.Background(), h.tokens[role])
	if err != nil {
		h.t.Fatalf("authorize %s session: %v", role, err)
	}
	return adminidentity.WithSession(context.Background(), sess)
}

// entriesFor returns audit entries for one action.
func (h *harness) entriesFor(action adminaudit.Action) []adminaudit.Entry {
	return adminaudit.Select(h.audit, adminaudit.Filter{Action: action})
}

// assertChainIntact fails unless the whole audit chain verifies.
func (h *harness) assertChainIntact() {
	h.t.Helper()
	if v := h.audit.Verify(); !v.Intact {
		h.t.Fatalf("audit chain broken at seq %d: %s", v.BreakAt, v.Detail)
	}
}

// identityInfo reports the fixture IdP's own description, so the oversight assertions read what the
// real verifier says about itself rather than a literal a test invented.
func (h *harness) identityInfo() adminidentity.ProviderInfo { return h.authn.Describe() }
