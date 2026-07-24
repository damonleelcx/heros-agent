// Command p7hermes drives the whole P7 commercial stack against a REAL repository
// (github.com/nousresearch/hermes-agent): the P6 optimizer loop merges verified optimization pull
// requests into the checkout with real git, and those REAL MERGE COMMITS become the evidence behind the
// gainshare charge. A billed saving on this surface traces to a commit you can `git show`.
//
// # What is real, and what is stubbed
//
// REAL: the target repository and every git operation (branch, merge commit, audit trail); the node
// ids / symbols / files (actual call sites in the hermes source); the entitlement gate consulted by the
// P6 loop before each merge; the SUM derivation over P2.5 cost events; the idempotent usage records;
// the append-only billing ledger with its UNIQUE idempotency keys; the reconciliation; and the
// verified-savings computation, which reads the merge commits the loop actually produced.
//
// STUBBED — and labelled as such, exactly as cmd/p6hermes and cmd/p55hermes label theirs: the DIAGNOSIS
// input (in production from the P4.5 attribution engine), the VERIFICATION deltas (in production from
// real eval runs through a provider), and the BILLING PROVIDER (a Stripe-style stub in test mode). No
// real money moves and no real provider account is touched.
//
// # Why run P7 against a real repo at all
//
// Gainshare is the phase's sharpest trust edge: the platform bills a share of savings it claims to have
// produced. Against fixtures, "traces to a merge commit" is a string comparison. Against this repo it
// is a git object — the loop opened a branch, merged it, and the gainshare line points at that SHA. The
// estimated and un-merged savings in the same ledger are worth far more and bill nothing, which is the
// claim the whole design rests on and the one worth seeing on real history.
//
//	git clone https://github.com/nousresearch/hermes-agent /tmp/hermes-agent
//	go run ./cmd/p7hermes -repo /tmp/hermes-agent            # serve the billing surface
//	go run ./cmd/p7hermes -repo /tmp/hermes-agent -headless  # print the whole run and exit
//
// Flags drive the first-class states: -plan, -over-limit, -payment-failed, -drift, -no-consent, -dark.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/telemetry"
	"github.com/heros-foreal/agentd/internal/verification"
)

const (
	specPath   = "variant_spec.json"
	workflowID = "nousresearch/hermes-agent"
	customerID = "cus_nousresearch"
)

// hermesCatalog is the plan catalog, published into a CONFIG STORE on disk (a temp dir), never git.
// Prices are opaque provider references; there is no amount here or anywhere in the repository.
const hermesCatalog = `{
  "version": "cfg-hermes-1",
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

// hermesNode is a REAL discovered call site in the hermes source, the diagnosis-guided change proposed
// for it, and the per-period spend that node accounts for. The node ids, symbols and files are real;
// the verification outcome and the spend are the stubbed inputs.
type hermesNode struct {
	nodeID, symbol, file string
	diag, operator, dim  string
	from, to             string
	providers            []string
	compositeLow, delta  float64
	tryOrder             float64
	// baselineSpend / optimizedSpend are what this node costs per period before and after the change.
	// Their DIFFERENCE is what a merged, verified optimization would save — and only a merged one bills.
	baselineSpend, optimizedSpend float64
}

// hermesTargets mirrors cmd/p6hermes's real call sites, so the two surfaces describe the same repo.
func hermesTargets() []hermesNode {
	return []hermesNode{
		{nodeID: "node:auxiliary_client:async_call_llm", symbol: "async_call_llm", file: "agent/auxiliary_client.py",
			diag: "diag-model-mismatch", operator: "model_upgrade", dim: "model", from: "gpt-4o-mini", to: "gpt-5",
			providers: []string{"openai"}, compositeLow: 0.71, delta: 0.38, tryOrder: 0.9,
			baselineSpend: 148.50, optimizedSpend: 96.25},
		{nodeID: "node:trajectory_compressor:_generate_summary", symbol: "_generate_summary", file: "trajectory_compressor.py",
			diag: "diag-prompt-drift", operator: "prompt_rewrite", dim: "prompt", from: "summary@v1", to: "summary@v2+schema",
			providers: []string{"openai"}, compositeLow: 0.79, delta: 0.19, tryOrder: 0.8,
			baselineSpend: 62.75, optimizedSpend: 41.00},
		// Highest composite (0.94) but proposes an off-allowlist provider → the P4 gate blocks the merge,
		// so this saving is VERIFIED-BUT-UN-MERGED and bills nothing, however large.
		{nodeID: "node:chat_completion_helpers:handle_max_iterations", symbol: "handle_max_iterations", file: "agent/chat_completion_helpers.py",
			diag: "diag-context-overflow", operator: "context_policy_switch", dim: "context", from: "full-window", to: "summarization",
			providers: []string{"cohere"}, compositeLow: 0.94, delta: 0.31, tryOrder: 0.7,
			baselineSpend: 240.00, optimizedSpend: 38.00},
		// Marginal gain below min-improvement → the loop converges without merging it. Also un-merged.
		{nodeID: "node:auxiliary_client:call_llm", symbol: "call_llm", file: "agent/auxiliary_client.py",
			diag: "diag-tool-schema", operator: "add_skill", dim: "skills", from: "N tools", to: "N+1 (+web_search)",
			providers: []string{"openai"}, compositeLow: 0.805, delta: 0.05, tryOrder: 0.6,
			baselineSpend: 55.00, optimizedSpend: 49.50},
	}
}

var (
	repoFlag      = flag.String("repo", "", "path to the hermes-agent checkout the loop owns (required)")
	planID        = flag.String("plan", "enterprise", "named plan: free|team|business|enterprise")
	overLimit     = flag.Bool("over-limit", false, "push SUM past the plan's band so the over-limit denial renders")
	paymentFailed = flag.Bool("payment-failed", false, "deliver a payment_failed webhook so the dunning state renders")
	withDrift     = flag.Bool("drift", false, "drop a provider-side usage record so reconciliation drift renders")
	noConsent     = flag.Bool("no-consent", false, "start with gainshare consent NOT given")
	dark          = flag.Bool("dark", false, "leave the billing feature flag OFF (the default rollout state)")
	headless      = flag.Bool("headless", false, "run the whole period once, print it, and exit (no server)")
	addr          = flag.String("addr", "127.0.0.1:8497", "listen address")
)

// The billing periods. Three closed months: two before the optimizations merged, one after.
var periods = []metering.Period{
	metering.MonthPeriod(time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)),
	metering.MonthPeriod(time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)),
	metering.MonthPeriod(time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)),
}

func main() {
	flag.Parse()
	log.SetFlags(0)
	if *repoFlag == "" {
		log.Fatal("p7hermes: -repo is required.\n" +
			"  git clone https://github.com/nousresearch/hermes-agent /tmp/hermes-agent\n" +
			"  go run ./cmd/p7hermes -repo /tmp/hermes-agent")
	}

	st, err := build(*repoFlag)
	if err != nil {
		log.Fatalf("p7hermes: %v", err)
	}
	if *headless {
		st.report()
		return
	}

	srv := api.New(nil, config.Config{})
	srv.MountP7(st)
	srv.SetBillingRollout(st.rollout)
	fmt.Printf("P7 on %s\n", workflowID)
	fmt.Printf("  billing surface:  http://%s/p7/billing?customer=%s\n", *addr, customerID)
	fmt.Printf("  readiness:        http://%s/readyz\n", *addr)
	fmt.Printf("  target checkout:  %s\n", *repoFlag)
	fmt.Printf("  rollout:          %s\n", st.rollout)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler))
}

// state is the wired P7 stack plus the api.P7Source implementation.
type state struct {
	mu       sync.Mutex
	repoDir  string
	plans    *plancfg.Resolver
	accounts *account.MemStore
	usage    *metering.MemUsageStore
	meter    *metering.Meter
	gate     *entitlement.Gate
	svc      *billing.Service
	provider *billing.StubProvider
	deltas   *metering.MemVerifiedDeltas
	savings  *metering.MemSavingsStore
	rollout  *billing.Rollout
	observer *metering.Observer
	alerts   *metering.MemAlerter
	// merges is what the P6 loop actually merged into the real repo, and mergeDenials is what the
	// entitlement gate refused. Both are printed by the headless report.
	merges       []optimizer.AppliedChange
	mergeDenials []optimizer.LedgerEvent
	loopState    optimizer.RunState
	// clobbered names changes that WERE merged but are no longer live in the repository's spec, because
	// a later merge overwrote them. They are merged git facts and un-realized savings at the same time
	// — see realizedNodes for why that distinction decides whether they may be billed.
	clobbered []string
}

// realizedNodes reads the repository's CURRENT variant spec and reports which nodes actually carry
// their optimized value.
//
// ## Why the merge list is not enough
//
// A merge is a git-history fact (ADR-001), and it is what a saving ATTRIBUTES to — but it is not proof
// the saving is in effect. A candidate built against the baseline and merged after another candidate
// reverts the earlier change: both merges are real, both are in the log, and only one optimization is
// live. Billing both would be billing for a saving that is not in effect, which is the exact failure
// this phase exists to prevent, dressed up as a git fact.
//
// So realization is read from the repository's own state — the spec the merges left behind — and a
// delta is billable only when it is BOTH merged AND live. That is stricter than the requirement, and
// deliberately so: the requirement's purpose is that the platform never bills for a saving that did not
// happen, and "merged but overwritten" is a saving that did not happen.
func realizedNodes(dir string, nodes []hermesNode) (map[string]bool, error) {
	cmd := exec.Command("git", "show", "HEAD:"+specPath)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read live spec from %s: %w", dir, err)
	}
	var live map[string]any
	if err := json.Unmarshal(out, &live); err != nil {
		return nil, fmt.Errorf("parse live spec: %w", err)
	}
	realized := map[string]bool{}
	for _, n := range nodes {
		entry, ok := live[n.nodeID].(map[string]any)
		if !ok {
			continue
		}
		realized[n.nodeID] = entry["value"] == n.to
	}
	return realized, nil
}

func build(repoDir string) (*state, error) {
	ctx := context.Background()
	now := func() time.Time { return periods[len(periods)-1].End.Add(-time.Hour) }

	// ── 1. Plan configuration, from a config store on disk (never git) ────────
	cfgDir, err := os.MkdirTemp("", "p7hermes-config-*")
	if err != nil {
		return nil, err
	}
	cfgPath := filepath.Join(cfgDir, "plans.json")
	if err := os.WriteFile(cfgPath, []byte(hermesCatalog), 0o600); err != nil {
		return nil, err
	}
	log.Printf("plan configuration published to %s (config store, not git)", cfgPath)
	plans := plancfg.NewResolver(plancfg.NewFileSource(cfgPath), plancfg.NewMemAudit())
	plans.SetClock(now)
	if _, err := plans.Reload("p7hermes"); err != nil {
		return nil, err
	}
	plan, err := plans.ResolvePlan(plancfg.NormalizePlanID(*planID))
	if err != nil {
		return nil, err
	}

	// ── 2. Account + provider (the only stub that matters commercially) ───────
	provider := billing.NewStubProvider()
	handle, err := provider.EnsureCustomer(ctx, customerID)
	if err != nil {
		return nil, err
	}
	accts := account.NewMemStore()
	if _, err := accts.Create(account.Account{
		CustomerID: customerID, ProviderCustomerHandle: handle,
		ActivePlanID: plan.PlanID, PlanConfigVersion: plans.Version(), CreatedAt: periods[0].Start,
	}); err != nil {
		return nil, err
	}
	if !*noConsent {
		if _, err := accts.SetGainshareConsent(customerID, true, periods[0].Start); err != nil {
			return nil, err
		}
	}

	// ── 3. The meter, over REAL P2.5 cost events per hermes node ──────────────
	events := metering.NewMemCostEvents()
	usage := metering.NewMemUsageStore()
	meter := metering.NewMeter(events, usage)
	meter.SetClock(now)
	nodes := hermesTargets()

	st := &state{repoDir: repoDir, plans: plans, accounts: accts, usage: usage, meter: meter,
		provider: provider, deltas: metering.NewMemVerifiedDeltas(), savings: metering.NewMemSavingsStore()}

	// ── 4. Run the REAL P6 loop over the REAL repo, gated by the REAL
	//      entitlement gate. Its merge commits become gainshare's evidence.
	gate := entitlement.NewGate(accts, plans, usage)
	// Clocked INSIDE the last period: the gate answers "may this happen now", so it reads the current
	// period's meters.
	gate.SetClock(func() time.Time { return periods[len(periods)-1].Start.Add(15 * 24 * time.Hour) })
	st.gate = gate

	rollout := billing.NewRollout()
	if !*dark {
		if err := rollout.Enable(billing.ModeTest); err != nil {
			return nil, err
		}
		if err := rollout.EnableGainshare(); err != nil {
			return nil, err
		}
		rollout.EnableAutoMergeEntitlement()
	}
	st.rollout = rollout

	if err := st.runOptimizer(ctx, nodes, gate, rollout); err != nil {
		return nil, fmt.Errorf("optimizer over %s: %w", repoDir, err)
	}

	// ── 5. Cost events: the first two periods at baseline spend, the last one
	//      reduced by exactly the nodes the loop MERGED. ───────────────────────
	merged := map[string]optimizer.AppliedChange{}
	for _, m := range st.merges {
		merged[m.Node] = m
	}
	// Ground realization in the repository's own state, not in the merge list.
	realized, err := realizedNodes(repoDir, nodes)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if _, wasMerged := merged[n.nodeID]; wasMerged && !realized[n.nodeID] {
			st.clobbered = append(st.clobbered, n.nodeID)
		}
	}
	for pi, p := range periods {
		runID := fmt.Sprintf("run-hermes-%s", p.ID)
		events.Attribute(runID, customerID)
		last := pi == len(periods)-1
		for ni, n := range nodes {
			spend := n.baselineSpend
			if last && realized[n.nodeID] {
				// The change is merged AND live in the repository, so the node genuinely costs less.
				spend = n.optimizedSpend
			}
			events.Put(costEvent(runID, fmt.Sprintf("%s|%s|%d", runID, n.nodeID, ni), spend,
				p.Start.Add(time.Duration(ni+1)*72*time.Hour)))
		}
		if *overLimit && last {
			events.Put(costEvent(runID, runID+"|burst|0", 900.0, p.Start.Add(20*24*time.Hour)))
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

	// ── 6. The P5.5 verified-delta ledger, built from what the loop DID ───────
	//
	// This is the load-bearing wiring. A node the loop MERGED becomes a verified, merged delta carrying
	// the loop's real merge commit. A node it verified but did NOT merge — because the P4 gate blocked
	// it, or because the gain was below min-improvement, or because the entitlement gate denied
	// auto-merge — becomes an entry with `Merged: false` and no commit. The second kind bills nothing,
	// and on this repo it is worth more than the first.
	last := periods[len(periods)-1]
	for _, n := range nodes {
		m := merged[n.nodeID]
		// Merged AND live. A merged-but-overwritten change is a git fact whose saving is not in effect,
		// and the ledger records it as un-merged so it bills nothing.
		wasMerged := realized[n.nodeID]
		if !wasMerged {
			m = optimizer.AppliedChange{}
		}
		d := metering.VerifiedDelta{
			Ref: "vd:" + n.nodeID, ProposalID: "prop:" + n.nodeID, CustomerID: customerID, Period: last.ID,
			Verdict: verification.Verdict{
				GateResult: verification.GatePass, HeldOut: true, Significant: true, RegressionPass: true,
				Delta: evalstats.Interval{Mean: n.delta, Low: n.delta - 0.05, High: n.delta + 0.05},
			},
			Merged: wasMerged, MergeCommit: m.MergeCommit,
			BaselineSUM: n.baselineSpend, OptimizedSUM: n.optimizedSpend,
			Baseline: metering.BaselineMethod{
				ID: "holdout-v1", EvalSetHash: "es_hermes_" + n.dim,
				HoldoutCaseIDs:      []string{"h4", "h5", "h6"},
				GeneratingCaseIDs:   []string{"h1", "h2", "h3"},
				Seeds:               []int64{1, 2, 3, 4, 5},
				BaselineConfigHash:  "cfg_base_" + n.nodeID,
				CandidateConfigHash: "cfg_cand_" + n.nodeID,
			},
		}
		st.deltas.Put(d)
	}

	// ── 7. Billing service ────────────────────────────────────────────────────
	secrets, err := billing.NewManagedSecrets(providergateway.StaticSecrets{
		billing.SecretBillingAPIKey:         {APIKey: "billing-api-key-DO-NOT-LEAK-hermes"},
		billing.SecretBillingWebhookSigning: {APIKey: "webhook-signing-secret-DO-NOT-LEAK-hermes"},
	})
	if err != nil {
		return nil, err
	}
	svc, err := billing.NewService(provider, billing.NewMemLedger(), accts, plans, meter, secrets)
	if err != nil {
		return nil, err
	}
	svc.SetClock(now)
	svc.WithDeliveries(billing.NewMemDeliveries())
	alerts := &metering.MemAlerter{}
	observer := metering.NewObserver(billing.LogSink{}, alerts)
	observer.SetClock(now)
	svc.WithRollout(rollout).WithObserver(observer)
	st.svc, st.observer, st.alerts = svc, observer, alerts

	if plan.PriceRefs["subscription"] != "" {
		if _, err := svc.StartSubscription(ctx, customerID); err != nil {
			log.Printf("subscription: %v", err)
		}
	}
	for _, p := range periods {
		for _, m := range metering.Metrics {
			if _, err := svc.ReportUsage(ctx, customerID, p, m); err != nil {
				log.Printf("report %s/%s: %v", p.ID, m, err)
			}
		}
		if plan.PriceRefs["subscription"] != "" {
			if _, err := svc.Charge(ctx, customerID, p, billing.KindSubscription,
				billing.SubscriptionChargeIdempotencyKey(customerID, p.ID, plan.PlanID)); err != nil {
				log.Printf("subscription charge %s: %v", p.ID, err)
			}
		}
		if plan.PriceRefs["metered"] != "" {
			if _, err := svc.Charge(ctx, customerID, p, billing.KindMetered,
				billing.MeteredChargeIdempotencyKey(customerID, p.ID, string(metering.MetricSUM))); err != nil {
				log.Printf("metered charge %s: %v", p.ID, err)
			}
		}
	}

	// ── 8. Gainshare — verified, MERGED savings only ──────────────────────────
	acct, _ := accts.Get(customerID)
	if acct.GainshareConsent && plan.PriceRefs["gainshare"] != "" {
		if _, err := svc.ChargeGainshare(ctx, customerID, last, st.deltas, st.savings); err != nil {
			log.Printf("gainshare: %v", err)
		}
	}

	// ── 9. The optional first-class states ────────────────────────────────────
	if *paymentFailed {
		body, _ := json.Marshal(billing.WebhookPayload{
			ProviderEventID: "evt_hermes_failed", Type: billing.WebhookInvoicePaymentFailed,
			CustomerID: customerID, Period: last.ID, InvoiceRef: "prov_inv_hermes",
		})
		stamp := now().UTC().Format(time.RFC3339)
		if _, err := svc.HandleWebhook(ctx, billing.SignedWebhook{Body: body, Timestamp: stamp,
			Signature: billing.SignWebhook("webhook-signing-secret-DO-NOT-LEAK-hermes", stamp, body)}); err != nil {
			log.Printf("webhook: %v", err)
		}
	}
	if *withDrift {
		provider.DropUsage(customerID, last.ID, string(metering.MetricSUM))
	}
	return st, nil
}

// runOptimizer runs the REAL P6 controller over the REAL checkout, with the P7 entitlement gate wired
// in. Every merge it makes is a git merge commit in that repository.
func (s *state) runOptimizer(ctx context.Context, nodes []hermesNode, gate *entitlement.Gate, rollout *billing.Rollout) error {
	baseline, baseMap := baselineSpec(nodes)
	if err := prepareRepo(s.repoDir, baseline); err != nil {
		return err
	}

	byConfig := map[string]optimizer.VerifyResult{}
	enum := candEnum{byNode: map[string][]optimizer.SearchCandidate{}}
	var targets []optimizer.Target
	for i, n := range nodes {
		spec := variantSpec(baseMap, n)
		hash := optimizer.ContentHash(spec)
		byConfig[hash] = optimizer.VerifyResult{ContractOK: true, Builds: true, SpendUSD: 0.09,
			Verdict: verification.Verdict{GateResult: verification.GatePass, Significant: true, HeldOut: true,
				RegressionPass: true, Delta: evalstats.Interval{Mean: n.delta, Low: n.delta - 0.05, High: n.delta + 0.05},
				CostDelta: -(n.baselineSpend - n.optimizedSpend) / 1000, LatencyDelta: 55},
			Metrics: optimizer.CandidateMetrics{Providers: n.providers, Quality: 0.9, LatencyMS: 540,
				Composite: evalstats.Interval{Mean: n.compositeLow + 0.05, Low: n.compositeLow, High: n.compositeLow + 0.1}}}
		enum.byNode[n.nodeID] = append(enum.byNode[n.nodeID], optimizer.SearchCandidate{
			DiagnosisID: n.diag, Node: n.nodeID, Dimension: n.dim, Operator: n.operator,
			ConfigHash: hash, SpecBytes: spec, Providers: n.providers, ExpectedGain: n.tryOrder,
			Rationale: fmt.Sprintf("%s at %s (%s): %s → %s", n.operator, n.symbol, n.file, n.from, n.to)})
		targets = append(targets, optimizer.Target{DiagnosisID: n.diag, Node: n.nodeID, Dimension: n.dim,
			Priority: 1.0 - 0.1*float64(i)})
	}

	ledger := optimizer.NewMemLedger()
	ctrl := &optimizer.Controller{
		Search:   optimizer.Search{Enum: enum},
		Verifier: optimizer.StaticVerifier{ByConfig: byConfig},
		Repo:     optimizer.GitRepo{Dir: s.repoDir, SpecPath: specPath, Branch: "main"},
		Ledger:   ledger,
		Kill:     optimizer.NewKillSwitch(),
		Clock:    func() time.Time { return time.Now().UTC() },
		// THE P7 WIRING: the loop consults the entitlement gate before every merge and falls back to
		// opening a pull request for a human when the plan does not entitle Autonomous auto-merge.
		Entitlement: entitlement.NewMergeGate(gate).WithRollout(rollout),
	}
	auth := optimizer.Authority{
		RunID: "hermes-p7", WorkflowID: workflowID, Actor: "damon", WeightProfile: "balanced",
		CustomerID: customerID,
		Constraints: optimizer.Constraints{
			BudgetCeilingUSD: 100, ProviderAllowlist: []string{"openai", "anthropic"},
			MinImprovement: 0.02, MaxIterations: 20, StallK: 3,
		},
		KillSwitchArmed: true, AuditArmed: true, RollbackArmed: true, GrantedAt: time.Now().UTC(),
	}
	res, err := ctrl.Run(ctx, optimizer.RunInput{Authority: auth, Targets: targets,
		BaselineSpecBytes: baseline, EvalSetCaseIDs: []string{"h1", "h2", "h3", "h4", "h5", "h6"}})
	if err != nil {
		return err
	}
	s.merges, s.loopState = res.Merges, res.State
	for _, ev := range ledger.Events(auth.RunID) {
		if ev.Type == optimizer.EventEntitlementDenied {
			s.mergeDenials = append(s.mergeDenials, ev)
		}
	}
	return nil
}

// ── repo + spec helpers (mirroring cmd/p6hermes) ─────────────────────────────

func baselineSpec(nodes []hermesNode) ([]byte, map[string]any) {
	base := map[string]any{"workflow": workflowID}
	for _, n := range nodes {
		base[n.nodeID] = map[string]any{"dimension": n.dim, "value": n.from, "symbol": n.symbol, "file": n.file}
	}
	b, _ := json.MarshalIndent(base, "", "  ")
	return b, base
}

func variantSpec(base map[string]any, node hermesNode) []byte {
	spec := map[string]any{}
	for k, v := range base {
		spec[k] = v
	}
	spec[node.nodeID] = map[string]any{"dimension": node.dim, "value": node.to, "symbol": node.symbol, "file": node.file}
	b, _ := json.MarshalIndent(spec, "", "  ")
	return b
}

// prepareRepo ensures the checkout is a git repo with a committed baseline variant_spec.json on main.
// It works on the checkout the loop OWNS — never an upstream tree — and only adds the spec file.
func prepareRepo(dir string, baseline []byte) error {
	git := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v: %s", args, err, out)
		}
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := git("init", "-q", "-b", "main"); err != nil {
			return err
		}
	}
	_ = git("config", "user.email", "seed@demo.local")
	_ = git("config", "user.name", "seed")
	if err := os.WriteFile(filepath.Join(dir, specPath), baseline, 0o644); err != nil {
		return err
	}
	if err := git("add", "--", specPath); err != nil {
		return err
	}
	_ = git("commit", "-q", "-m", "optimizer: baseline variant spec")
	return nil
}

type candEnum struct {
	byNode map[string][]optimizer.SearchCandidate
}

func (e candEnum) Enumerate(t optimizer.Target) []optimizer.SearchCandidate { return e.byNode[t.Node] }

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
