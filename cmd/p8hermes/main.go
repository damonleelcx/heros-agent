// Command p8hermes drives the whole P8 operator console against a REAL, wired platform stack.
//
// It stands up the admin identity provider (test mode, SSO + MFA), the RBAC gate, the append-only
// hash-chained audit log, and every operator service — tenant lifecycle, entitlement override, billing
// oversight, model registry, job ops, the global + per-tenant kill switch, impersonation, cross-tenant
// read models, and GDPR — over synthetic tenants on NAMED plans and the SAME P7 billing stack, P2.5
// meter and P6 change ledger the rest of the platform uses. The Go admin API (internal/api/p8.go) is
// served on its own listener, and the separate Next.js console (web/admin-console) is its BFF.
//
// # What is real, and what is test-mode
//
// REAL: the identity/RBAC/audit machinery, the command path with its write-ahead audit and confirm+
// reason discipline, the kill switch wired into the SAME admission gate the P6 loop reads, the SUM
// derivation over P2.5 cost events, the append-only billing ledger, and the tamper-evident audit
// chain. TEST-MODE — and labelled as such: the admin IdP (it mints signed assertions the real verifier
// checks, MFA and all; it is a test ISSUER, not an MFA bypass) and the billing provider (a Stripe-style
// stub). No real money moves and no real provider account is touched.
//
//	go run ./cmd/p8hermes                     # serve the admin API on 127.0.0.1:4311
//	go run ./cmd/p8hermes -addr 127.0.0.1:4311
//
// Then run the console BFF against it:
//
//	cd web/admin-console && ADMIN_API_BASE=http://127.0.0.1:4311 \
//	  ADMIN_PLATFORM_CREDENTIAL=<the credential this prints> npm run dev
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminfixture"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/runqueue"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

const (
	// platformCredential is the BFF credential this demo prints for the console to use. In production
	// it comes from the secrets manager and never appears on a terminal.
	platformCredential = "p8hermes-demo-platform-credential-do-not-ship"
)

// planCatalog is the demo plan configuration — NAMED plans, quantity limits, opaque price references.
// No amount, percentage or band anywhere.
const planCatalog = `{
  "version": "plan-cfg-p8hermes-2026-03",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,"features":["cli","discovery"],"limits":{},"price_refs":{}},
    {"plan_id":"team","display_name":"Team","rank":1,"features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"seats":5,"retention_days":30},"price_refs":{"subscription":"price_ref_team_subscription","metered":"price_ref_team_metered"}},
    {"plan_id":"business","display_name":"Business","rank":2,"features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"seats":25,"retention_days":90},"price_refs":{"subscription":"price_ref_business_subscription","metered":"price_ref_business_metered"}},
    {"plan_id":"enterprise","display_name":"Enterprise","rank":3,
     "features":["cli","discovery","assisted_pr","dashboard","auto_merge"],
     "limits":{"seats":250,"retention_days":365},
     "price_refs":{"subscription":"price_ref_enterprise_subscription","metered":"price_ref_enterprise_metered","gainshare":"price_ref_enterprise_gainshare"}}
  ]
}`

func main() {
	addr := flag.String("addr", "127.0.0.1:4311", "admin API listen address")
	repo := flag.String("repo", "", "path to a REAL hermes-agent checkout; when set, the demo runs a real "+
		"P6 autonomous merge against it and the resulting merge commit lands in the P8 audit chain")
	flag.Parse()

	deps, err := wire(*repo)
	if err != nil {
		log.Fatalf("p8hermes: wiring failed: %v", err)
	}
	adminAPI, err := api.NewAdminAPI(deps)
	if err != nil {
		log.Fatalf("p8hermes: admin API: %v", err)
	}

	fmt.Println("── P8 operator console — wired platform stack ──────────────────────────")
	for k, v := range adminAPI.Describe() {
		fmt.Printf("  %-16s %s\n", k, v)
	}
	fmt.Println()
	fmt.Println("  Admin API:            http://" + *addr)
	fmt.Println("  Platform credential:  " + platformCredential)
	fmt.Println()
	fmt.Println("  Fixture admin principals (sign in with these SSO subjects, any MFA factor):")
	fmt.Println("    sso|support        → Support")
	fmt.Println("    sso|billing_ops    → Billing-Ops")
	fmt.Println("    sso|platform_sre   → Platform-SRE")
	fmt.Println("    sso|superadmin     → Superadmin")
	fmt.Println()
	fmt.Println("  Start the console BFF:")
	fmt.Println("    cd web/admin-console && \\")
	fmt.Printf("      ADMIN_API_BASE=http://%s \\\n", *addr)
	fmt.Println("      ADMIN_PLATFORM_CREDENTIAL=" + platformCredential + " \\")
	fmt.Println("      npm run dev   # → http://localhost:4310")
	fmt.Println("────────────────────────────────────────────────────────────────────────")

	log.Fatal(http.ListenAndServe(*addr, adminAPI.Handler))
}

// wire assembles the whole operator stack. It is one function so the demo and a future integration
// test build the identical graph.
func wire(repoDir string) (api.AdminDeps, error) {
	now := func() time.Time { return time.Now().UTC() }

	// The identity, authorization and command layer is SHARED with the other demo that serves this
	// console (cmd/p21hermes). Built here rather than copied so the two cannot drift into letting an
	// operator sign in to one surface and not the other.
	layer, err := adminfixture.Build("p8", now)
	if err != nil {
		return api.AdminDeps{}, err
	}
	audit, gate, exec := layer.Audit, layer.Gate, layer.Executor

	// ── plan configuration + synthetic tenants ──
	src := plancfg.NewMemSource()
	if err := src.PublishJSON([]byte(planCatalog)); err != nil {
		return api.AdminDeps{}, err
	}
	resolver := plancfg.NewResolver(src, plancfg.NewMemAudit())
	resolver.SetClock(now)
	if _, err := resolver.Reload("p8hermes"); err != nil {
		return api.AdminDeps{}, err
	}

	provStub := billing.NewStubProvider()
	accounts := account.NewMemStore()
	tenants := []struct{ id, plan string }{
		// tenant-hermes is the REAL workflow: github.com/nousresearch/hermes-agent, on Enterprise so its
		// autonomous auto-merge is entitled.
		{"tenant-hermes", "enterprise"},
		{"tenant-acme", "enterprise"}, {"tenant-boreal", "business"},
		{"tenant-castle", "team"}, {"tenant-dune", "team"},
	}
	for _, tc := range tenants {
		handle, err := provStub.EnsureCustomer(context.Background(), tc.id)
		if err != nil {
			return api.AdminDeps{}, err
		}
		if _, err := accounts.Create(account.Account{
			CustomerID: tc.id, ProviderCustomerHandle: handle, ActivePlanID: tc.plan,
			PlanConfigVersion: resolver.Version(), GainshareConsent: true, CreatedAt: now(),
		}); err != nil {
			return api.AdminDeps{}, err
		}
	}

	// ── billing stack + a little metered usage so the oversight and cross-tenant views have data ──
	events := metering.NewMemCostEvents()
	usage := metering.NewMemUsageStore()
	meter := metering.NewMeter(events, usage)
	meter.SetClock(now)
	period := metering.MonthPeriod(now())
	for i, tc := range tenants {
		events.Attribute("run-"+tc.id, tc.id)
		events.Put(metricevent.Event{
			SchemaVersion: metricevent.SchemaVersion, RunID: "run-" + tc.id, VariantID: "v1", NodeID: "n1",
			MetricName: telemetry.MetricCostUSD, Value: fptr(float64(3 + i)), Unit: "usd",
			Timestamp: now().Format(time.RFC3339Nano), ConfigHash: "cfg1",
		})
		if _, _, err := meter.RecordSUM(tc.id, period); err != nil {
			return api.AdminDeps{}, err
		}
	}
	// The demo's billing provider is a stub, so these are labels rather than credentials — there is no
	// account for them to authenticate against. They are written as sentences on purpose: a fixture
	// shaped like a key teaches the repository's secret scanner to tolerate key-shaped strings, and a
	// scanner that tolerates them is the one that misses the real thing.
	billingSecrets, err := billing.NewManagedSecrets(providergateway.StaticSecrets{
		billing.SecretBillingAPIKey:         {APIKey: "stub provider, not a credential"},
		billing.SecretBillingWebhookSigning: {APIKey: "stub provider, no webhook signing"},
	})
	if err != nil {
		return api.AdminDeps{}, err
	}
	billSvc, err := billing.NewService(provStub, billing.NewMemLedger(), accounts, resolver, meter, billingSecrets)
	if err != nil {
		return api.AdminDeps{}, err
	}
	billSvc.SetClock(now)
	for _, tc := range tenants {
		if _, err := billSvc.StartSubscription(context.Background(), tc.id); err != nil {
			return api.AdminDeps{}, err
		}
		if _, err := billSvc.ReportUsage(context.Background(), tc.id, period, metering.MetricSUM); err != nil {
			return api.AdminDeps{}, err
		}
		if _, err := billSvc.Charge(context.Background(), tc.id, period, billing.KindMetered, "metered-"+tc.id+"-"+period.ID); err != nil {
			return api.AdminDeps{}, err
		}
	}

	// ── operator services ──
	killStore := adminops.NewMemKillSwitchStore()
	killSvc, err := adminops.NewKillSwitchService(exec, killStore, adminops.DefaultKillSwitchPolicy())
	if err != nil {
		return api.AdminDeps{}, err
	}
	admission, err := adminops.NewAdmission(accounts, killSvc)
	if err != nil {
		return api.AdminDeps{}, err
	}
	tenantSvc, err := adminops.NewTenantService(exec, accounts, resolver, admission)
	if err != nil {
		return api.AdminDeps{}, err
	}
	entSvc, err := adminops.NewEntitlementService(exec, accounts, resolver)
	if err != nil {
		return api.AdminDeps{}, err
	}
	deltas := metering.NewMemVerifiedDeltas()
	billOversight, err := adminops.NewBillingService(exec, billSvc, deltas)
	if err != nil {
		return api.AdminDeps{}, err
	}
	models := adminops.NewModelRegistry(now)
	if _, err := models.Add("sonnet-5", "anthropic", "price_ref_sonnet5_v1"); err != nil {
		return api.AdminDeps{}, err
	}
	if _, err := models.Add("haiku-4-5", "anthropic", "price_ref_haiku45_v1"); err != nil {
		return api.AdminDeps{}, err
	}
	regSvc, err := adminops.NewRegistryService(exec, models)
	if err != nil {
		return api.AdminDeps{}, err
	}

	queue := seedJobQueue(now())
	jobSvc, err := adminops.NewJobService(exec, queue)
	if err != nil {
		return api.AdminDeps{}, err
	}
	impSvc, err := adminops.NewImpersonationService(exec)
	if err != nil {
		return api.AdminDeps{}, err
	}
	crossSvc, err := adminops.NewCrossTenantService(exec, adminops.CrossTenantConfig{
		Accounts: accounts, Meter: meter, Ledger: billSvc.Ledger(), Admission: admission,
	})
	if err != nil {
		return api.AdminDeps{}, err
	}
	auditSvc, err := adminops.NewAuditService(exec)
	if err != nil {
		return api.AdminDeps{}, err
	}
	subjects := adminops.NewMemSubjectStore()
	subjects.Put("subject:person-7741", "trace-1", "a customer trace with PII")
	subjects.Put("subject:person-7741", "memory-2", "a stored memory with PII")
	gdprSvc, err := adminops.NewGDPRService(exec, subjects)
	if err != nil {
		return api.AdminDeps{}, err
	}

	// ── autonomous merges into the audit chain ───────────────────────────────
	//
	// The ledger is the P6 change ledger WRAPPED by P8's AuditingLedger, so every merge the loop makes
	// is mirrored into the tamper-evident chain on the same code path — write-ahead, and fail-closed if
	// the chain refuses it (FR16).
	auditLedger, err := adminops.NewAuditingLedger(optimizer.NewMemLedger(), audit, func(runID string) string {
		// run-<tenant> → the tenant that owns it, so every merge entry is reconstructable to a tenant.
		return strings.TrimPrefix(runID, "run-")
	})
	if err != nil {
		return api.AdminDeps{}, err
	}

	if repoDir != "" {
		// ── REAL merge against the REAL hermes-agent checkout ──
		merge, err := runHermesMerge(repoDir, "tenant-hermes", auditLedger, admission)
		if err != nil {
			return api.AdminDeps{}, fmt.Errorf("real hermes-agent merge: %w", err)
		}
		log.Printf("p8hermes: REAL merge on %s — node %s, diagnosis %s, commit %s (git -C %s show %s)",
			repoDir, merge.Node, merge.DiagnosisID, merge.MergeCommit, repoDir, merge.MergeCommit)
	} else {
		// No checkout supplied: record one synthetic merge so the audit viewer is not empty. Labelled as
		// synthetic in its own summary, so nobody mistakes it for a git fact.
		seq, _ := auditLedger.Append(optimizer.LedgerEvent{
			RunID: "run-tenant-acme", Type: optimizer.EventApply, Actor: "optimizer", DiagnosisID: "diag-synthetic-1",
			FromConfigHash: "cfg-old", ToConfigHash: "cfg-new", PRRef: "optimizer/synthetic",
			Summary: "SYNTHETIC merge (no -repo supplied): held-out +0.420, cost -0.0031", TS: now(),
		})
		_ = auditLedger.Backfill("run-tenant-acme", seq, "0000000synthetic")
	}

	// ── rollout: 8a live once the checklist is green; 8b live once its dependencies are ──
	rollout := adminops.NewRollout()
	rollout.MarkChecklistGreen()
	if err := rollout.EnableWave8a(true); err != nil {
		return api.AdminDeps{}, err
	}
	rollout.MarkKillSwitchLive()
	rollout.MarkAggregatesLive()
	if err := rollout.EnableWave8b(); err != nil {
		return api.AdminDeps{}, err
	}

	return api.AdminDeps{
		PlatformCredential: platformCredential,
		Authenticator:      layer.Authenticator,
		Sessions:           layer.Sessions,
		Gate:               gate,
		Executor:           exec,
		Tenants:            tenantSvc,
		Entitlements:       entSvc,
		Billing:            billOversight,
		Registry:           regSvc,
		Jobs:               jobSvc,
		KillSwitch:         killSvc,
		Impersonation:      impSvc,
		CrossTenant:        crossSvc,
		Audit:              auditSvc,
		GDPR:               gdprSvc,
		TestModeIdP:        layer.TestModeIdP,
		Rollout:            rollout,
		Now:                now,
	}, nil
}

func fptr(f float64) *float64 { return &f }

// seedJobQueue builds an in-memory P4/P6 queue with a running, a queued, a done, a stuck and a parked
// job, so the fleet page has something to show and something to cancel/retry.
func seedJobQueue(now time.Time) *adminops.MemJobQueue {
	q := adminops.NewMemJobQueue(func() time.Time { return now })
	future := now.Add(4 * time.Minute)
	expired := now.Add(-2 * time.Minute)
	q.Seed(runqueue.Job{RunID: "run-acme-opt-17", ConfigHash: "cfg-a", SourceRevision: "rev1", State: "leased",
		Attempts: 1, LeasedBy: "worker-a", LeaseExpiresAt: &future, EnqueuedAt: now.Add(-3 * time.Minute)})
	q.Seed(runqueue.Job{RunID: "run-boreal-eval-4", ConfigHash: "cfg-b", SourceRevision: "rev1", State: "leased",
		Attempts: 2, LeasedBy: "worker-b", LeaseExpiresAt: &expired, EnqueuedAt: now.Add(-25 * time.Minute)})
	q.Seed(runqueue.Job{RunID: "run-castle-discover-9", ConfigHash: "cfg-c", SourceRevision: "rev1", State: "ready",
		EnqueuedAt: now.Add(-1 * time.Minute)})
	q.Seed(runqueue.Job{RunID: "run-dune-opt-2", ConfigHash: "cfg-d", SourceRevision: "rev1", State: "done",
		EnqueuedAt: now.Add(-40 * time.Minute)})
	q.Seed(runqueue.Job{RunID: "run-acme-opt-11", ConfigHash: "cfg-e", SourceRevision: "rev1", State: "dead",
		Attempts: 3, DeadLetterReason: "exhausted 3 attempts without completing", EnqueuedAt: now.Add(-50 * time.Minute)})
	return q
}
