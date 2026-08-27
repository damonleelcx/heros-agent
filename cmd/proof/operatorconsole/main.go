// Command operatorconsole drives the whole P8 operator console against a REAL, wired platform stack.
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
//	go run ./cmd/proof/operatorconsole                     # serve the admin API on 127.0.0.1:4311
//	go run ./cmd/proof/operatorconsole -addr 127.0.0.1:4311
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
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminfixture"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/linkingest"
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

// enrolmentSeeds carries the federated demo's per-run TOTP seeds from wire() to the banner in main().
//
// A package-level in a demo `main`, deliberately: threading a second return value through wire()'s six
// error paths would be more moving parts than the thing it carries, and this file is one process whose
// whole lifetime is the demo.
var enrolmentSeeds map[string]string

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

	// A FEDERATED demo has to hand the operator a factor, or the deadlock is unbreakable: no session
	// without a platform-verified factor, no enrolment without a session, and nobody has either. In
	// production this is provisioned out of band before the first sign-in; here the seeds are per-run,
	// per-process, and printed once.
	if len(enrolmentSeeds) > 0 {
		fmt.Println()
		fmt.Println("  🔴 FEDERATED — enrol one of these in an authenticator app BEFORE signing in:")
		for adminID, seed := range enrolmentSeeds {
			{
				fmt.Printf("    %-18s otpauth://totp/heros:%s?secret=%s&issuer=heros-operator\n", adminID, adminID, seed)
			}
		}
	}
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
	// console (cmd/proof/payments). Built here rather than copied so the two cannot drift into letting an
	// operator sign in to one surface and not the other.
	layer, err := adminfixture.Build("p8", now)
	enrolmentSeeds = layer.TOTPSeeds
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
	// The P11 link-coverage source, wired so the billing surface can state what its SUM-derived figures
	// actually reflect. The three shapes are all present on purpose, because they are the three the
	// surface must render differently: COMPLETE coverage, PARTIAL coverage, and UNKNOWN coverage — and
	// the third withholds the figure rather than showing it bare.
	links := linkingest.NewMemStore()
	for i, tc := range tenants {
		if tc.id == "tenant-dune" {
			continue // no run count ever reported: coverage is UNKNOWN, and no SUM figure is shown.
		}
		reported := 10 + i*5
		linked := reported
		if tc.id == "tenant-boreal" {
			linked = reported / 3 // partial: the coverage percentage is what changes a credit decision.
		}
		_ = links.ObserveRunsReported(tc.id, reported)
		for r := 0; r < linked; r++ {
			if _, err := links.Record(linkingest.LinkedRun{
				RunID: fmt.Sprintf("run-%s-%d", tc.id, r), TenantID: tc.id, LinkedAt: now(),
			}); err != nil {
				return api.AdminDeps{}, err
			}
		}
	}
	billOversight, err := adminops.NewBillingService(exec, billSvc, deltas, links)
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
	// The authored-change record the improvement aggregate reads. Seeded with BOTH states, because the
	// property the surface has to demonstrate is that only one of them moves a figure: the verified
	// change is counted, the unverified one is counted as excluded and contributes zero.
	authored := authoring.NewMemRecorder()
	for i, tc := range tenants {
		state := authoring.StateVerified
		if i%2 == 1 {
			state = authoring.StateUnverified
		}
		if err := authored.Append(context.Background(), authoring.Entry{
			ChangeID: "chg-" + tc.id, Action: authoring.ActionSubmitted, TenantID: tc.id,
			ActorID: "user-" + tc.id, WorkflowID: "wf-" + tc.id, ConfigHash: "cfg1",
			Axis: "prompt", Origin: "studio", VerificationState: state, At: now(),
		}); err != nil {
			return api.AdminDeps{}, err
		}
	}
	// P30 · the analysis agent, with a POPULATED store so the surface can be seen with data.
	//
	// 🔴 The demo publishes one definition and leaves it `pending`, because that is the honest default
	// state and it is the one worth looking at: a published definition that has not met its floor is
	// NOT serving, and the page has to say so. A demo that pre-activated one would show the happy path
	// and hide the gate.
	agentVersions := herosagent.NewMemVersionStore()
	if err := agentVersions.Put(context.Background(), herosagent.Version{
		ConfigHash: "b7f2c1d4e9a08e5c3f6b1a2d4e7c9f0b1a2d4e7c9f0b1a2d4e7c9f0b1a2d4e7c",
		Definition: herosagent.SingleNode(herosagent.Node{
			PromptRef: "prompt/heros-residue@3", ModelRef: "claude-opus-5",
			// A PROVIDER NAME. There is no key here and no field that could hold one.
			CredentialRef: "anthropic",
			ContextRef:    "context/residue-only@1",
			HarnessRef:    "harness/single-shot@1",
		}),
		ModelRef: "claude-opus-5", CredentialRef: "anthropic",
		RehearsalState: herosagent.RehearsalPending,
		CreatedAtMS:    now().UnixMilli(),
	}); err != nil {
		return api.AdminDeps{}, err
	}
	agentSvc, err := adminops.NewAgentService(exec, agentVersions, nil, newDemoAgentSpend(), nil, nil,
		// P30's runner supplies NO host services, so `react-loop`, `plan-execute` and `critic-loop`
		// render unavailable WITH what each would need — which is the half of D11 worth seeing.
		herosagent.RunnerHosts{})
	if err != nil {
		return api.AdminDeps{}, err
	}

	crossSvc, err := adminops.NewCrossTenantService(exec, adminops.CrossTenantConfig{
		Accounts: accounts, Meter: meter, Ledger: billSvc.Ledger(), Admission: admission,
		Authored: authored, Deltas: deltas,
	})
	if err != nil {
		return api.AdminDeps{}, err
	}
	// ── P26 delivery oversight over the REAL P12 record ──
	//
	// Seeded with all three merge outcomes, because the property this surface exists to preserve is
	// that they stay three: an open pull request is UNKNOWN, a closed one is CLOSED UNMERGED, and only
	// an observed merge is MERGED. A demo that seeded only merges would render correctly while proving
	// nothing about the distinction.
	deliveries := deliveryrecord.NewMemStore()
	for i, tc := range tenants {
		id := forgedelivery.DeliveryID("cfg1", "rev-"+tc.id, "main")
		if err := deliveries.Append(context.Background(), forgedelivery.Entry{
			DeliveryID: id, TenantID: tc.id, ConfigHash: "cfg1", SourceRevision: "rev-" + tc.id,
			Target: "main", ForgeRef: "pr-" + tc.id, Mode: forgedelivery.ModeCI,
			State: forgedelivery.StateOpened, Actor: "customer-ci", At: now(),
		}); err != nil {
			return api.AdminDeps{}, err
		}
		switch i % 3 {
		case 0:
			// OBSERVED merge — the only outcome that means a change shipped.
			if err := deliveries.Append(context.Background(), forgedelivery.Entry{
				DeliveryID: id, TenantID: tc.id, ConfigHash: "cfg1", SourceRevision: "rev-" + tc.id,
				Target: "main", ForgeRef: "pr-" + tc.id, Mode: forgedelivery.ModeCI,
				State: forgedelivery.StateMerged, Actor: "customer-ci",
				MergeCommit: "merge-" + tc.id, At: now(),
			}); err != nil {
				return api.AdminDeps{}, err
			}
		case 1:
			// Closed WITHOUT merging. It must never render as merged.
			if err := deliveries.Append(context.Background(), forgedelivery.Entry{
				DeliveryID: id, TenantID: tc.id, ConfigHash: "cfg1", SourceRevision: "rev-" + tc.id,
				Target: "main", ForgeRef: "pr-" + tc.id, Mode: forgedelivery.ModeCI,
				State: forgedelivery.StateClosed, Actor: "customer-ci",
				Reason: "the author closed it without merging", At: now(),
			}); err != nil {
				return api.AdminDeps{}, err
			}
		default:
			// Still open: the merge outcome is UNKNOWN, and it is rendered as unknown.
		}
	}
	deliverySvc, err := adminops.NewDeliveryService(exec, deliveries, accounts)
	if err != nil {
		return api.AdminDeps{}, err
	}

	// ── P26 release oversight over the REAL channel contract and the REAL compiled trust root ──
	//
	// The channels, the target matrix and the key set come from `internal/distribution` and
	// `internal/release` unchanged. What this demo supplies is the per-release RECORD a publish pipeline
	// produces — and it supplies all three smoke outcomes on purpose, because `queued until timeout` is
	// the one this surface exists to keep from being read as a failure.
	releaseSvc, err := adminops.NewReleaseService(exec, demoReleases{})
	if err != nil {
		return api.AdminDeps{}, err
	}

	// ── P26 axis oversight over the REAL coverage source ──
	//
	// The matrix, the causes and each axis's declared status come from `transform.AxisCoverage()`
	// unchanged — the same read the transform's refusal, preflight, the CLI and the customer console
	// perform. What this demo supplies is fleet ADOPTION, which is a per-deployment fact.
	axisSvc, err := adminops.NewAxisService(exec, demoAdoption{})
	if err != nil {
		return api.AdminDeps{}, err
	}

	// ── P26 oversight: sessions + their verified factor, legal acceptance, reporting health ──
	tenantIDs := make([]string, 0, len(tenants))
	for _, tc := range tenants {
		tenantIDs = append(tenantIDs, tc.id)
	}
	oversightSvc, err := adminops.NewOversightService(exec, adminops.OversightConfig{
		Sessions: layer.Sessions,
		Identity: layer.Authenticator.Describe(),
		Tenants:  func() []string { return tenantIDs },
		// No legal service and no deployment source in this demo: both absences are REPORTED rather
		// than rendered as empty tables, which is the property wave 26e exists to hold.
		Readiness: demoReadiness{},
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
		Delivery:           deliverySvc,
		Release:            releaseSvc,
		Axis:               axisSvc,
		Agent:              agentSvc,
		Oversight:          oversightSvc,
		TestModeIdP:        layer.TestModeIdP,
		IdP:                layer.IdP,
		Factors:            layer.Factors,
		Challenges:         layer.Challenges,
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

// demoReleases is the publish record this demo stands in for.
//
// 🔴 It is labelled as a demo record on the surface (Describe), because an operator has to be able to
// tell a real publish outcome from a fixture without reading this file. The three smoke outcomes are
// all present deliberately: `passed`, `failed`, and `queued_until_timeout` — the last is the P20
// lesson (a retired runner label queues rather than failing) and the one the surface must never render
// as a failure.
type demoReleases struct{}

func (demoReleases) Describe() string {
	return "demo publish record (cmd/proof/operatorconsole) over the real channel contract and the real compiled trust root"
}

func (demoReleases) Releases() []adminops.ReleaseRecord {
	return []adminops.ReleaseRecord{
		{
			Version: "v0.20.0", Channel: "curl-sh", PublishedAt: "2026-07-30T00:00:00Z",
			SigningKeyID: "heros-release-2026c",
			Artefacts: []adminops.ArtefactRecord{
				{Platform: "linux/amd64", Name: "heros_0.20.0_linux_amd64.tar.gz", Published: true,
					Verification: adminops.VerifyVerified, Smoke: adminops.SmokePassed},
				{Platform: "darwin/arm64", Name: "heros_0.20.0_darwin_arm64.tar.gz", Published: true,
					Verification: adminops.VerifyVerified, Smoke: adminops.SmokeQueuedUntilTimeout,
					SmokeDetail: "runner label macos-13 was retired; the job queued until the workflow timed out and never started"},
				{Platform: "windows/amd64", Name: "heros_0.20.0_windows_amd64.zip", Published: true,
					Verification: adminops.VerifyVerified, Smoke: adminops.SmokePassed},
			},
		},
		{
			// Published, verified, and its smoke FAILED — the state that reaches a stranger's laptop.
			Version: "v0.20.1", Channel: "curl-sh", PublishedAt: "2026-07-31T00:00:00Z",
			SigningKeyID: "heros-release-2026c",
			Artefacts: []adminops.ArtefactRecord{
				{Platform: "linux/amd64", Name: "heros_0.20.1_linux_amd64.tar.gz", Published: true,
					Verification: adminops.VerifyVerified, Smoke: adminops.SmokeFailed,
					SmokeDetail: "the installed binary reported a version that did not match the tag"},
				{Platform: "darwin/arm64", Name: "heros_0.20.1_darwin_arm64.tar.gz", Published: false},
			},
		},
		{
			// Signed with a RETIRED key. This is the P20 incident question, answerable from the console.
			Version: "v0.20.0-rc.4", Channel: "curl-sh", PublishedAt: "2026-07-29T00:00:00Z",
			SigningKeyID: "heros-release-2026b",
			Artefacts: []adminops.ArtefactRecord{
				{Platform: "linux/amd64", Name: "heros_0.20.0-rc.4_linux_amd64.tar.gz", Published: true,
					Verification: adminops.VerifyNotYet},
			},
		},
	}
}

// demoAdoption is the fleet adoption this demo stands in for. Coverage itself is NOT from here — it is
// read from the engine — and that split is the point: adoption is a per-deployment fact, coverage is a
// claim about a customer's code and has exactly one source.
type demoAdoption struct{}

func (demoAdoption) Describe() string { return "demo fleet adoption (cmd/proof/operatorconsole)" }

func (demoAdoption) Adoption(axis string) (int, int) {
	switch axis {
	case "prompt":
		return 4, 23
	case "model":
		return 3, 11
	case "context":
		return 2, 6
	case "memory":
		return 1, 2
	}
	return 0, 0
}

func (demoAdoption) RefusedNodes(axis string) []adminops.RefusedNode {
	if axis != "prompt" {
		return nil
	}
	return []adminops.RefusedNode{
		{TenantID: "tenant-hermes", NodeID: "hermes/agent.py:call_model", Language: "python",
			Axis: "prompt", Cause: "call-site-cannot-carry-it"},
		{TenantID: "tenant-acme", NodeID: "svc/plan.rs:plan", Language: "rust",
			Axis: "prompt", Cause: "no-materializer-for-this-language"},
	}
}

// demoReadiness reports the observability integrations from the PLATFORM's own readiness surface.
//
// All three states are present because they are three different answers: `absent` is a decision,
// `configured` is health, and `degraded` is a fault that names its failure class. A demo with only
// `configured` rows would render correctly and prove nothing.
type demoReadiness struct{}

func (demoReadiness) Describe() string { return "platform readiness surface (/admin/api/readyz)" }

func (demoReadiness) Integrations() []adminops.IntegrationRow {
	const src = "platform readiness surface"
	return []adminops.IntegrationRow{
		{Name: "metric store (P2.5)", State: adminops.IntegrationConfigured, Source: src},
		{Name: "error monitoring (P24)", State: adminops.IntegrationAbsent, Source: src},
		{Name: "product analytics (P24)", State: adminops.IntegrationAbsent, Source: src},
		{Name: "trace export", State: adminops.IntegrationDegraded,
			FailureClass: "configured, and the exporter has not accepted a flush since this process started",
			Source:       src},
	}
}

// demoAgentSpend is the analysis-agent spend source for the demo.
//
// 🔴 It carries an UNPRICED tenant and a DEFAULTED placement on purpose. Those are the two states this
// surface exists to keep distinguishable — `unpriced` must never render as `0`, and a tenant nobody has
// considered must never look like one somebody switched off — and a demo showing only priced, explicitly
// placed tenants would show neither.
// It HOLDS the edits it is given, in memory, for the life of the process.
//
// 🔴 The three setters used to return "the demo does not persist caps", which was true and harmless
// only for as long as nothing could call them: the console had no cap or placement control, so the
// write path of this surface was undemonstrable — and the demo binary is where this repository shows
// the console working. Now that the controls exist, a setter that always fails would mean the only way
// to see a placement change is production.
//
// It is in-memory ON PURPOSE and does not pretend otherwise: a restart returns the three tenants
// below, which is the demo's fixture and the state every run should start from.
type demoAgentSpend struct {
	mu         sync.Mutex
	fleetCap   int64
	caps       map[string]int64
	placements map[string]string
}

func newDemoAgentSpend() *demoAgentSpend {
	return &demoAgentSpend{caps: map[string]int64{}, placements: map[string]string{}}
}

func (d *demoAgentSpend) Spend(context.Context) ([]adminops.AgentSpendRow, error) {
	rows := []adminops.AgentSpendRow{
		{
			TenantID: "acme", Inferences: 12, TokensIn: 184_320, TokensOut: 9_140,
			EstimatedCost: 4.87, Priced: true,
			Placement: "platform", PlacementSource: adminops.PlacementExplicit,
			Cap: 500_000,
		},
		{
			// UNPRICED: a real token count and no cost. The page prints the word.
			TenantID: "globex", Inferences: 3, TokensIn: 41_002, TokensOut: 2_210,
			Priced:    false,
			Placement: "customer", PlacementSource: adminops.PlacementExplicit,
		},
		{
			// DEFAULTED: nobody has looked at this tenant. Same VALUE as an explicit `disabled`, and a
			// completely different fact.
			TenantID: "initech", Placement: "disabled", PlacementSource: adminops.PlacementDefaulted,
		},
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range rows {
		if p, ok := d.placements[rows[i].TenantID]; ok {
			rows[i].Placement = p
			// 🔴 And the SOURCE moves with it. An operator setting `initech` to `disabled` — the value it
			// already had — has turned "nobody has looked at this" into "somebody decided", which is the
			// entire distinction this column exists for and the one a demo must not flatten.
			rows[i].PlacementSource = adminops.PlacementExplicit
		}
		if c, ok := d.caps[rows[i].TenantID]; ok {
			rows[i].Cap = c
		}
	}
	return rows, nil
}

func (d *demoAgentSpend) FleetCap(context.Context) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fleetCap, nil
}

func (d *demoAgentSpend) SetFleetCap(_ context.Context, tokens int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fleetCap = tokens
	return nil
}

func (d *demoAgentSpend) SetTenantCap(_ context.Context, tenantID string, tokens int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Zero REMOVES the ceiling, matching the real store, which deletes the row. Recording a literal
	// zero instead would render as "cap: 0" — a ceiling of nothing rather than the absence of one.
	if tokens == 0 {
		delete(d.caps, tenantID)
		return nil
	}
	d.caps[tenantID] = tokens
	return nil
}

func (d *demoAgentSpend) SetPlacement(_ context.Context, tenantID, placement string) error {
	// Parsed by the package that owns the vocabulary, exactly as the real store does — so the demo
	// refuses an invalid placement for the same reason and with the same sentence.
	if _, err := herosagent.ParsePlacement(placement); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.placements[tenantID] = placement
	return nil
}
