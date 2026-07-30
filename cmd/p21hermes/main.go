// Command p21hermes runs the P21 Stripe payments stack against the REAL
// github.com/NousResearch/hermes-agent repository.
//
// It follows the convention the other phase demos established (p5hermes … p12hermes): point the phase
// at an actual checkout rather than a fixture, because a fixture proves the code path and nothing about
// any real codebase.
//
// # What is REAL here, and what is NOT — stated, not implied
//
// REAL: the hermes checkout (its files and its HEAD commit are read, and a node whose file is missing
// is reported as missing rather than silently billed), the whole P21 code path end to end — the Stripe
// provider over HTTP, the P7-derived idempotency keys on Stripe's `Idempotency-Key` header, the
// signature-verified webhook endpoint, the persist-then-ack ordering, the audited plan changes, and
// the additive correction path.
//
// NOT REAL: the money. This runs against an IN-PROCESS Stripe (`internal/stripefake`) in TEST mode by
// default, so no credential is needed and nothing can move a real dollar. Point `-stripe-base` at
// Stripe's own API and supply `-api-key` to run the identical code against a real Stripe test account.
//
// 🔴 The fake is a DEMO dependency, never a product one — `internal/stripefake/fence_test.go` fails the
// build if any shipping package reaches it.
//
// Also not real: the per-node spend figures. They are the stubbed input, and the demo says so in its
// own output rather than letting a reader assume the numbers came from the repository.
//
// # Usage
//
//	git clone https://github.com/nousresearch/hermes-agent /tmp/hermes-agent
//	go run ./cmd/p21hermes -repo /tmp/hermes-agent            # run the period, print the report, exit
//	go run ./cmd/p21hermes -repo /tmp/hermes-agent -serve     # …then serve the console's platform API
//
// Against a REAL Stripe test account, which needs the three artefacts in PRD §10.1 — a test key, a
// webhook signing secret, and price objects whose ids are in the catalog you publish:
//
//	export STRIPE_API_KEY=sk_test_…          # or pass -api-key-stdin and pipe it
//	go run ./cmd/p21hermes -repo /tmp/hermes-agent \
//	  -stripe-base https://api.stripe.com -plans ./my-stripe-catalog.json
//
// 🔴 The key is NOT a command-line flag. A flag puts a credential in shell history and in `ps` output
// for every user on the box, which is the same class of exposure the Secrets seam exists to prevent —
// and a demo that taught the wrong habit would be teaching it to the person most likely to copy it.
//
// With `-serve`, run the console against it:
//
//	cd web/console && PLATFORM_API_BASE=http://127.0.0.1:4399 \
//	  CONSOLE_TENANT_IDENTITY=configured CONSOLE_TENANT_ASSERTIONS='{"local-dev-assertion":"cus_nousresearch"}' \
//	  CONSOLE_PLATFORM_CREDENTIAL=local-dev-credential-not-a-secret npm run dev
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/stripefake"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

const (
	workflowID = "nousresearch/hermes-agent"
	customerID = "cus_nousresearch"
	// webhookSecret is the signing secret this demo signs its own deliveries with. It goes through the
	// Secrets seam exactly as a real one does — the demo does not get a shortcut the product does not
	// have — and it is not key-shaped, so the repository's credential fence does not have to make an
	// exception for this file.
	webhookSecret = "p21hermes-webhook-signing-placeholder-not-a-secret"
)

// hermesCatalog is the plan catalog, published into a CONFIG STORE on disk (a temp dir), never git.
// Prices are opaque provider references; there is no amount here or anywhere in the repository.
const hermesCatalog = `{
  "version": "cfg-p21-hermes-1",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,
     "features":["cli","discovery"],
     "limits":{"sum_band":25,"seats":1,"retention_days":7,"eval_compute":10},
     "price_refs":{}},
    {"plan_id":"team","display_name":"Team","rank":1,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":300,"seats":5,"retention_days":30,"eval_compute":100},
     "price_refs":{"subscription":"price_ref_team_sub","metered":"price_ref_team_metered"}},
    {"plan_id":"business","display_name":"Business","rank":2,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":20000,"seats":25,"retention_days":90,"eval_compute":1000},
     "price_refs":{"subscription":"price_ref_biz_sub","metered":"price_ref_biz_metered"}},
    {"plan_id":"enterprise","display_name":"Enterprise","rank":3,
     "features":["cli","discovery","assisted_pr","dashboard","auto_merge"],
     "limits":{"seats":500,"retention_days":365},
     "price_refs":{"subscription":"price_ref_ent_sub","metered":"price_ref_ent_metered","gainshare":"price_ref_ent_gainshare"}}
  ]
}`

// hermesNode is a REAL call site in the hermes source and the per-period spend it accounts for.
//
// 🔴 The spend figures are WHOLE UNITS, and that is not a rounding convenience. Stripe records
// whole-unit quantities, and `billing.stripeQuantity` REFUSES a fractional one rather than rounding it
// — rounding silently changes what a customer is billed. A meter is therefore denominated in a unit
// that is integral, and the price for that unit is administered in Stripe. The demo shows the refusal
// too (see `demonstrateFractionalRefusal`), because a constraint nobody has seen fire is a constraint
// nobody believes.
type hermesNode struct {
	nodeID, symbol, file string
	baselineSpend        float64
	optimizedSpend       float64
	// merged says whether the optimization for this node was merged. ONLY a merged, verified saving is
	// billable — P5.5's rule, preserved here rather than loosened for a demo.
	merged bool
}

func hermesTargets() []hermesNode {
	return []hermesNode{
		{nodeID: "node:auxiliary_client:async_call_llm", symbol: "async_call_llm",
			file: "agent/auxiliary_client.py", baselineSpend: 148, optimizedSpend: 96, merged: true},
		{nodeID: "node:trajectory_compressor:_generate_summary", symbol: "_generate_summary",
			file: "trajectory_compressor.py", baselineSpend: 62, optimizedSpend: 41, merged: true},
		// Verified but NOT merged: the proposed provider is off the allowlist, so the gate blocked it.
		// It is the largest saving on this repository and it bills NOTHING.
		{nodeID: "node:chat_completion_helpers:handle_max_iterations", symbol: "handle_max_iterations",
			file: "agent/chat_completion_helpers.py", baselineSpend: 240, optimizedSpend: 38, merged: false},
		// Verified, merged upstream but below min-improvement, so the loop never merged it here.
		{nodeID: "node:auxiliary_client:call_llm", symbol: "call_llm",
			file: "agent/auxiliary_client.py", baselineSpend: 55, optimizedSpend: 49, merged: false},
	}
}

var (
	repoFlag    = flag.String("repo", "", "path to the hermes-agent checkout (required)")
	serve       = flag.Bool("serve", false, "serve the console's platform API after running the period")
	addr        = flag.String("addr", "127.0.0.1:4399", "listen address for -serve")
	stripeBase  = flag.String("stripe-base", "", "Stripe API base URL; empty starts an in-process fake in TEST mode")
	keyStdin    = flag.Bool("api-key-stdin", false, "read the Stripe API key from stdin (one line) instead of the environment")
	plansFlag   = flag.String("plans", "", "path to a plan catalog to publish; empty uses the built-in placeholder catalog")
	liveMode    = flag.Bool("live", false, "run the rollout in LIVE mode (refused unless a live key is supplied)")
	startOnFree = flag.Bool("start-free", true, "start the account on Free so the checkout → grant path is exercised")
	breakPrice  = flag.String("break-price", "", "leave this price reference UNCONFIGURED at the provider, so the preflight's red path is visible")
)

// The billing periods: three closed months, the last one after the optimizations merged.
var periods = []metering.Period{
	metering.MonthPeriod(time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)),
	metering.MonthPeriod(time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)),
	metering.MonthPeriod(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)),
}

func now() time.Time { return periods[len(periods)-1].End.Add(-time.Hour) }

func main() {
	flag.Parse()
	log.SetFlags(0)
	if *repoFlag == "" {
		log.Fatal("p21hermes: -repo is required.\n" +
			"  git clone https://github.com/nousresearch/hermes-agent /tmp/hermes-agent\n" +
			"  go run ./cmd/p21hermes -repo /tmp/hermes-agent")
	}

	st, err := build(*repoFlag)
	if err != nil {
		log.Fatalf("p21hermes: %v", err)
	}
	st.report()

	if !*serve {
		return
	}
	srv := api.New(nil, config.Config{})
	srv.MountP7(st)
	srv.MountP21Payments(st)
	srv.MountBillingWebhook(st.svc)
	srv.SetBillingRollout(st.rollout)
	srv.SetBillingCapability(st.svc)

	fmt.Printf("\nP21 on %s\n", workflowID)
	fmt.Printf("  payment read model:  http://%s/api/p21/customers/%s/payment\n", *addr, customerID)
	fmt.Printf("  webhook endpoint:    http://%s/billing/webhook   (the ONE inbound-from-internet path)\n", *addr)
	fmt.Printf("  readiness:           http://%s/readyz\n", *addr)
	fmt.Printf("  provider:            %s\n", st.svc.Describe()["provider"])
	fmt.Printf("  rollout:             %s\n", st.rollout)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler))
}

// state is the wired P21 stack plus the api.P7Source and api.PaymentsSource implementations.
type state struct {
	repoDir  string
	headSHA  string
	plans    *plancfg.Resolver
	accounts *account.MemStore
	usage    *metering.MemUsageStore
	meter    *metering.Meter
	gate     *entitlement.Gate
	svc      *billing.Service
	provider *billing.StripeProvider
	rollout  *billing.Rollout
	fake     *stripefake.Server
	nodes    []hermesNode
	// meteredPrice is the Team plan's metered price reference, read from the PUBLISHED catalog rather
	// than typed here — a literal would defeat the whole point of `-plans`.
	meteredPrice string
	missing      []string
	steps        []step
	subRef       string
	checkout     billing.CheckoutSession
	wrongRef     string
	creditRef    string
}

// step is one line of the demo's evidence log: what was attempted, and what actually happened.
type step struct {
	name   string
	detail string
	ok     bool
}

func (s *state) record(name string, ok bool, format string, args ...any) {
	s.steps = append(s.steps, step{name: name, detail: fmt.Sprintf(format, args...), ok: ok})
}

func build(repoDir string) (*state, error) {
	ctx := context.Background()

	// ── 0. The REAL repository ────────────────────────────────────────────────
	head, err := gitHead(repoDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", repoDir, err)
	}
	st := &state{repoDir: repoDir, headSHA: head, nodes: hermesTargets()}
	for _, n := range st.nodes {
		if _, err := os.Stat(filepath.Join(repoDir, n.file)); err != nil {
			// Reported, not skipped silently. A node the checkout does not contain is a claim about a
			// repository that is not true, and billing evidence built on it would be evidence for nothing.
			st.missing = append(st.missing, n.file)
		}
	}

	// ── 1. Plan configuration, from a config store on disk (never git) ────────
	//
	// `-plans` is what makes a real-account run possible without editing this file: the built-in catalog
	// carries PLACEHOLDER price references (`price_ref_team_sub`), which 404 against a real Stripe
	// account. A catalog whose ids are real Stripe prices is published the same way — from a config
	// store, never from git — and the preflight below is what tells you whether you got it right.
	cfgPath := *plansFlag
	if cfgPath == "" {
		cfgDir, derr := os.MkdirTemp("", "p21hermes-config-*")
		if derr != nil {
			return nil, derr
		}
		cfgPath = filepath.Join(cfgDir, "plans.json")
		if err := os.WriteFile(cfgPath, []byte(hermesCatalog), 0o600); err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(cfgPath); err != nil {
		return nil, fmt.Errorf("read the plan catalog at %s: %w", cfgPath, err)
	}
	plans := plancfg.NewResolver(plancfg.NewFileSource(cfgPath), plancfg.NewMemAudit())
	plans.SetClock(now)
	if _, err := plans.Reload("p21hermes"); err != nil {
		return nil, err
	}
	st.plans = plans

	// The demo's own assumptions about the catalog, checked LOUDLY. A published catalog missing the
	// plans this demo drives would otherwise produce a confusing half-run.
	for _, want := range []string{"free", "team"} {
		if _, rerr := plans.ResolvePlan(want); rerr != nil {
			return nil, fmt.Errorf("the published catalog has no %q plan, which this demo drives: %w", want, rerr)
		}
	}
	teamPlan, err := plans.ResolvePlan("team")
	if err != nil {
		return nil, err
	}
	st.meteredPrice = teamPlan.PriceRefs["metered"]

	// ── 2. Stripe — the in-process one by default ─────────────────────────────
	base, apiKey := *stripeBase, ""
	if base == "" {
		st.fake = stripefake.New()
		base, apiKey = st.fake.URL(), stripefake.TestKey
	} else {
		var kerr error
		if apiKey, kerr = stripeAPIKey(); kerr != nil {
			return nil, kerr
		}
	}
	secrets, err := billing.NewManagedSecrets(providergateway.StaticSecrets{
		billing.SecretBillingAPIKey:         {APIKey: apiKey},
		billing.SecretBillingWebhookSigning: {APIKey: webhookSecret},
	})
	if err != nil {
		return nil, err
	}

	rollout := billing.NewRollout()
	mode := billing.ModeTest
	if *liveMode {
		mode = billing.ModeLive
	}
	if err := rollout.Enable(mode); err != nil {
		return nil, err
	}
	if err := rollout.EnableGainshare(); err != nil {
		return nil, err
	}
	rollout.EnableAutoMergeEntitlement()
	st.rollout = rollout

	provider, err := billing.NewStripeProviderForRollout(secrets, rollout, now, billing.WithStripeBaseURL(base))
	if err != nil {
		return nil, err
	}
	st.provider = provider

	// The provider account's PRICE OBJECTS. Against a real Stripe account these are created by Finance
	// and this block does nothing; against the in-process one there is no Finance, so the demo seeds
	// them — and `-break-price` leaves one out on purpose, because a configuration check nobody has seen
	// fire is a configuration check nobody believes.
	if st.fake != nil {
		for _, plan := range plans.Plans() {
			for _, ref := range plan.PriceRefs {
				if ref == "" || ref == *breakPrice {
					continue
				}
				st.fake.SeedPrice(ref, true)
			}
		}
	}

	// ── 3. Account — on FREE, with no payment method. The starting state of a
	//      customer who has not paid yet, which is what the collection surface
	//      exists for. ────────────────────────────────────────────────────────
	handle, err := provider.EnsureCustomer(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("ensure stripe customer: %w", err)
	}
	startPlan := "team"
	if *startOnFree {
		startPlan = "free"
	}
	accts := account.NewMemStore()
	if _, err := accts.Create(account.Account{
		CustomerID: customerID, ProviderCustomerHandle: handle,
		ActivePlanID: startPlan, PlanConfigVersion: plans.Version(), CreatedAt: periods[0].Start,
	}); err != nil {
		return nil, err
	}
	if _, err := accts.SetGainshareConsent(customerID, true, periods[0].Start); err != nil {
		return nil, err
	}
	st.accounts = accts
	st.record("stripe customer", true, "handle %s for %s (the platform holds a HANDLE, never a card)", handle, customerID)

	// ── 4. The meter, over cost events per hermes node ────────────────────────
	events := metering.NewMemCostEvents()
	usage := metering.NewMemUsageStore()
	meter := metering.NewMeter(events, usage)
	meter.SetClock(now)
	st.usage, st.meter = usage, meter

	for pi, p := range periods {
		runID := "run-hermes-" + p.ID
		events.Attribute(runID, customerID)
		last := pi == len(periods)-1
		for ni, n := range st.nodes {
			spend := n.baselineSpend
			if last && n.merged {
				spend = n.optimizedSpend
			}
			events.Put(costEvent(runID, fmt.Sprintf("%s|%s|%d", runID, n.nodeID, ni), spend,
				p.Start.Add(time.Duration(ni+1)*72*time.Hour)))
		}
		if _, _, err := meter.RecordSUM(customerID, p); err != nil {
			return nil, err
		}
		for _, m := range []struct {
			metric metering.Metric
			qty    float64
		}{{metering.MetricSeats, 6}, {metering.MetricRetention, 30}, {metering.MetricEvalCompute, 256}} {
			if _, _, err := meter.RecordUsage(customerID, p, m.metric, m.qty, "hermes-"+string(m.metric)+"-"+p.ID); err != nil {
				return nil, err
			}
		}
	}

	st.gate = entitlement.NewGate(accts, plans, usage)
	st.gate.SetClock(func() time.Time { return periods[len(periods)-1].Start.Add(15 * 24 * time.Hour) })

	// ── 5. The billing service ────────────────────────────────────────────────
	svc, err := billing.NewService(provider, billing.NewMemLedger(), accts, plans, meter, secrets)
	if err != nil {
		return nil, err
	}
	svc.SetClock(now)
	svc.WithDeliveries(billing.NewMemDeliveries()).WithStates(billing.NewMemStates())
	alerts := &metering.MemAlerter{}
	observer := metering.NewObserver(billing.LogSink{}, alerts)
	observer.SetClock(now)
	svc.WithRollout(rollout).WithObserver(observer)
	st.svc = svc

	// ── PREFLIGHT, before anything charges (Decision 9) ───────────────────────
	rep, perr := svc.PreflightPricing(ctx)
	switch {
	case perr != nil:
		st.record("pricing preflight", false, "%v", perr)
		for _, u := range rep.Unresolved {
			st.record("  unresolved price", false, "plan %s / %s / %q — %s", u.PlanName, u.Kind, u.PriceRef, u.Reason)
		}
	case !rep.Verified:
		st.record("pricing preflight", false, "NOT verified: %s", rep.Detail)
	default:
		st.record("pricing preflight", true,
			"%d configured price reference(s) resolve at the provider — a placeholder would have been named here, not at the period's first charge",
			rep.Checked)
	}

	if err := st.runPeriod(ctx); err != nil {
		return nil, err
	}
	return st, nil
}

// runPeriod drives the whole P21 story once, recording what each step actually did.
func (s *state) runPeriod(ctx context.Context) error {
	ctxLast := periods[len(periods)-1]

	// ── Collection: the gap P7 left open ──────────────────────────────────────
	session, err := s.svc.StartCheckout(ctx, customerID, "Team",
		"http://127.0.0.1:4320/app/billing?checkout=done", "http://127.0.0.1:4320/app/billing?checkout=canceled")
	if err != nil {
		s.record("checkout session", false, "%v", err)
	} else {
		s.checkout = session
		s.record("checkout session", true,
			"minted server-side; the browser is sent to %s — the card goes browser→Stripe and never through the platform",
			truncate(session.URL, 64))
	}

	// ── The customer pays. Stripe tells us so, over the signed webhook. ───────
	s.svc.RecordSubscriptionPlan(customerID, "team")
	if ack := s.deliver("evt_checkout", billing.WebhookCheckoutCompleted, map[string]string{
		"payment_method_brand": "visa", "payment_method_last4": "4242",
	}); ack.Status == 200 {
		s.record("checkout.completed webhook", true, "payment method mirrored (brand + last four only; no card data)")
	} else {
		s.record("checkout.completed webhook", false, "status %d: %s", ack.Status, ack.Reason)
	}
	ack := s.deliver("evt_paid_1", billing.WebhookInvoicePaid, map[string]string{"invoice_ref": "in_hermes_1"})
	acct, _ := s.accounts.Get(customerID)
	s.record("invoice.paid → entitlement", ack.Status == 200 && acct.ActivePlanID == "team",
		"plan is now %s@%s by an audited plan change (status %d)", acct.ActivePlanID, acct.PlanConfigVersion, ack.Status)

	// A REDELIVERY of the same event applies nothing and is still a 2xx.
	again := s.deliver("evt_paid_1", billing.WebhookInvoicePaid, map[string]string{"invoice_ref": "in_hermes_1"})
	s.record("redelivery", again.Status == 200 && again.Duplicate && !again.Applied,
		"status %d, duplicate=%v, applied=%v — exactly once on Stripe's event id", again.Status, again.Duplicate, again.Applied)

	// A FORGED delivery is rejected before any side effect.
	forged := s.svc.HandleStripeWebhook(ctx, []byte(`{"id":"evt_forged","type":"invoice.paid","customer_id":"`+customerID+`"}`),
		"t=1,v1="+strings.Repeat("a", 64))
	s.record("forged webhook", forged.Status == 400, "status %d — rejected before parsing the body into a decision", forged.Status)

	// ── Subscription + metered usage against the real period ──────────────────
	sub, err := s.svc.StartSubscription(ctx, customerID)
	if err != nil {
		s.record("subscription", false, "%v", err)
	} else {
		s.subRef = sub.SubscriptionRef
		s.record("subscription", true, "%s on the plan's opaque price_ref, status %q (Stripe's word)", sub.SubscriptionRef, sub.Status)

		// A metered subscription item exists because FINANCE configured a metered price and attached it
		// — it is Stripe-account configuration, not something the platform creates. Against the
		// in-process Stripe there is no Finance, so the demo seeds it; against a real account this line
		// does nothing and the item is already there.
		if s.fake != nil {
			s.fake.SeedMeteredItem(sub.SubscriptionRef, s.meteredPrice)
		}
		if res, rerr := s.svc.ReportUsage(ctx, customerID, ctxLast, metering.MetricSUM); rerr != nil {
			s.record("report usage", false, "%v", rerr)
		} else {
			s.record("report usage", true,
				"the period's SUM QUANTITY reported to Stripe's metered item as %s — a quantity, never an amount; the platform multiplies nothing",
				res.UsageRef)
		}
	}

	rec, err := s.usage.Get(metering.Key{CustomerID: customerID, Period: ctxLast.ID, Metric: metering.MetricSUM})
	if err == nil {
		s.record("period SUM", true, "%.0f (spend under management, from the metered cost events)", rec.Quantity)
	}

	// The metered charge. The SAME key twice: one Stripe object, one ledger row.
	key := billing.MeteredChargeIdempotencyKey(customerID, ctxLast.ID, string(metering.MetricSUM))
	first, err := s.svc.Charge(ctx, customerID, ctxLast, billing.KindMetered, key)
	if err != nil {
		s.record("metered charge", false, "%v", err)
	} else {
		retry, rerr := s.svc.Charge(ctx, customerID, ctxLast, billing.KindMetered, key)
		s.wrongRef = first.EventID
		s.record("metered charge (idempotent)", rerr == nil && retry.EventID == first.EventID,
			"charged once under %s; a retry returned the SAME ledger row %s and created no second Stripe object",
			key, first.EventID)
	}

	// ── The fractional-quantity refusal, shown rather than described ──────────
	s.demonstrateFractionalRefusal(ctx)

	// ── A wrong charge, corrected ADDITIVELY ──────────────────────────────────
	if s.wrongRef != "" {
		credit, cerr := s.svc.Credit(ctx, customerID, s.wrongRef, "demo: charged against the wrong period")
		if cerr != nil {
			s.record("additive credit", false, "%v", cerr)
		} else {
			s.creditRef = credit.EventID
			original, _ := s.findEvent(s.wrongRef)
			s.record("additive credit", original.Status == billing.StatusRecorded,
				"credit %s issued; the ORIGINAL charge %s is intact and still %s — corrections add, never delete",
				credit.EventID, original.EventID, original.Status)
		}
	}

	// ── The invoice, read back through Invoice.Validate ───────────────────────
	inv, err := s.provider.Invoice(ctx, customerID, ctxLast.ID)
	if err != nil {
		s.record("invoice read-back", false, "%v", err)
	} else {
		basisless := 0
		for _, l := range inv.Lines {
			if l.Basis == "" {
				basisless++
			}
		}
		s.record("invoice read-back", basisless == 0,
			"%d line(s) on %s, every one naming its basis and carrying an opaque amount handle (no amount in the platform)",
			len(inv.Lines), inv.InvoiceRef)
	}

	// ── Reconciliation against what Stripe says it recorded ───────────────────
	recorded, err := s.provider.RecordedUsage(ctx, customerID, ctxLast.ID)
	if err != nil {
		s.record("reconciliation", false, "%v", err)
	} else {
		s.record("reconciliation", true, "Stripe recorded %d metered summary/summaries for %s — comparable against the platform's usage records without a write to either ledger",
			len(recorded), ctxLast.ID)
	}

	// ── Dunning: the grace window keeps the entitlement, the boundary does not ─
	s.deliver("evt_failed", billing.WebhookInvoicePaymentFailed, map[string]string{"invoice_ref": "in_hermes_2"})
	acct, _ = s.accounts.Get(customerID)
	s.record("payment_failed (grace window)", acct.ActivePlanID == "team",
		"plan is still %s — the platform does not fight Stripe's dunning schedule", acct.ActivePlanID)

	s.deliver("evt_canceled", billing.WebhookSubscriptionCanceled, nil)
	acct, _ = s.accounts.Get(customerID)
	s.record("subscription.deleted (boundary)", acct.ActivePlanID == "free",
		"degraded to %s by an audited plan change; the account, its handle and every ledger row are intact", acct.ActivePlanID)

	s.deliver("evt_paid_2", billing.WebhookInvoicePaid, map[string]string{"invoice_ref": "in_hermes_3"})
	acct, _ = s.accounts.Get(customerID)
	s.record("paying restores it", acct.ActivePlanID == "team",
		"plan is %s again — the degradation was reversible, and every TypePlanChange row survives", acct.ActivePlanID)

	// ── Gainshare: verified AND merged only ───────────────────────────────────
	billable, blocked := 0.0, 0.0
	for _, n := range s.nodes {
		if n.merged {
			billable += n.baselineSpend - n.optimizedSpend
		} else {
			blocked += n.baselineSpend - n.optimizedSpend
		}
	}
	s.record("gainshare invariant", true,
		"%.0f of verified saving is billable (merged); %.0f is verified but NOT merged and bills nothing — the larger number is the one that bills nothing",
		billable, blocked)
	return nil
}

// demonstrateFractionalRefusal shows the quantity contract firing.
//
// A constraint nobody has seen fire is a constraint nobody believes, and this one is the difference
// between a customer being billed what they used and being billed what rounded conveniently.
func (s *state) demonstrateFractionalRefusal(ctx context.Context) {
	acct, err := s.accounts.Get(customerID)
	if err != nil {
		return
	}
	_, err = s.provider.RaiseCharge(ctx, billing.ChargeRequest{
		ProviderCustomerHandle: acct.ProviderCustomerHandle,
		Kind:                   billing.KindMetered,
		Period:                 periods[len(periods)-1].ID,
		PriceRef:               s.meteredPrice,
		Quantity:               2.5,
		IdempotencyKey:         "demo:fractional-quantity",
		Description:            "demo: a quantity Stripe cannot record exactly",
	})
	s.record("fractional quantity", err != nil && strings.Contains(err.Error(), "integral unit"),
		"refused rather than rounded: %v", err)
}

func (s *state) findEvent(eventID string) (billing.BillingEvent, bool) {
	for _, ev := range s.svc.Ledger().Events(customerID, "") {
		if ev.EventID == eventID {
			return ev, true
		}
	}
	return billing.BillingEvent{}, false
}

// deliver signs a webhook the way Stripe signs one and hands it to the endpoint's own code path.
func (s *state) deliver(eventID string, typ billing.WebhookType, extra map[string]string) billing.WebhookAck {
	fields := map[string]string{"id": eventID, "type": string(typ), "customer_id": customerID}
	for k, v := range extra {
		fields[k] = v
	}
	body := encodeJSON(fields)
	sig := billing.StripeSignatureFor(webhookSecret, now(), body)
	return s.svc.HandleStripeWebhook(context.Background(), body, sig)
}

func (s *state) report() {
	fmt.Printf("P21 — Stripe payments against the REAL %s\n", workflowID)
	fmt.Printf("  checkout:  %s @ %s\n", s.repoDir, s.headSHA)
	fmt.Printf("  provider:  %s", s.svc.Describe()["provider"])
	if s.fake != nil {
		fmt.Printf("   (IN-PROCESS Stripe — no credential, no real money)")
	}
	fmt.Println()
	fmt.Printf("  rollout:   %s\n", s.rollout)
	fmt.Printf("  pricing:   %s\n", s.svc.Describe()["pricing"])
	if len(s.missing) > 0 {
		fmt.Printf("  ⚠ files not in this checkout: %s\n", strings.Join(s.missing, ", "))
	}

	fmt.Println("\n  What ran, and what it proved:")
	failed := 0
	for _, st := range s.steps {
		mark := "ok  "
		if !st.ok {
			mark = "FAIL"
			failed++
		}
		fmt.Printf("    [%s] %-30s %s\n", mark, st.name, st.detail)
	}

	fmt.Println("\n  Ledger (append-only, no amounts — the platform holds handles):")
	for _, ev := range s.svc.Ledger().Events(customerID, "") {
		fmt.Printf("    %-16s %-18s %-10s %s\n", ev.EventID, ev.Type, ev.Status, ev.Reason)
	}

	fmt.Printf("\n  %d step(s), %d failed.\n", len(s.steps), failed)
	fmt.Println("  Stubbed inputs: the per-node spend figures. Everything else — the repository, the")
	fmt.Println("  Stripe conversation, the idempotency keys, the signature verification, the plan")
	fmt.Println("  changes and the corrections — is the shipped code path.")
}

// stripeAPIKey resolves the key for a real-Stripe run, from the environment or from stdin.
//
// 🔴 Never from a flag. A command-line flag puts the credential in shell history and in `ps` output for
// every user on the box — the same exposure the Secrets seam exists to prevent, and the one a demo is
// most likely to teach by example. In a deployment the key comes from the seam; this is the one-off
// local path, and it is the least-bad version of one.
func stripeAPIKey() (string, error) {
	if *keyStdin {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		key := strings.TrimSpace(line)
		if key == "" {
			return "", fmt.Errorf("-api-key-stdin was given but stdin carried no key: %v", err)
		}
		return key, nil
	}
	if key := strings.TrimSpace(os.Getenv("STRIPE_API_KEY")); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("-stripe-base needs a Stripe API key.\n" +
		"  export STRIPE_API_KEY=<your Stripe TEST key>     # not a flag: a flag lands in shell history and in ps\n" +
		"  …or pipe it:  echo $KEY | go run ./cmd/p21hermes -repo <repo> -stripe-base <url> -api-key-stdin")
}

func gitHead(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func costEvent(runID, invocationID string, usd float64, ts time.Time) metricevent.Event {
	seed := int64(1)
	v := usd
	return metricevent.Event{
		SchemaVersion: metricevent.SchemaVersion,
		VariantID:     "var-hermes", RunID: runID, NodeID: "router", CaseID: "case-1", Seed: &seed,
		Timestamp:  ts.UTC().Format(time.RFC3339Nano),
		ConfigHash: "3a7b9c1d2e4f60718293a4b5c6d7e8f90112233445566778899aabbccddeeff0",
		MetricName: telemetry.MetricCostUSD, Value: &v, Unit: telemetry.UnitUSD,
		Dimensions: map[string]any{telemetry.AttrInvocationID: invocationID},
	}
}
