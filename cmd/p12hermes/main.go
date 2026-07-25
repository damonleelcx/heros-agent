// Command p12hermes serves the P12 forge-delivery console read model, addressed as the REAL
// github.com/NousResearch/hermes-agent repository, and demonstrates the delivery lifecycle end to end
// through the actual delivery core (Deliverer + append-only record + a simulated forge).
//
// It follows the convention the other phase demos established (p9hermes, p7hermes, …): drive the phase
// with real code rather than a hand-written fixture. The deliveries the console renders here are
// produced by ACTUAL Deliver / supersede / merge / close calls against an in-memory forge — so the
// console shows what the record layer actually wrote, round-tripped, not an invented shape.
//
// # What is REAL, and what is SIMULATED — stated, not implied
//
// REAL: the delivery core (every server-side precondition — gate, entitlement, halt, route, bound),
// the append-only delivery record, supersession, the observed-merge mechanism, and the PR body render.
// SIMULATED: the forge itself is InMemForge (a simulated repository), because opening a pull request on
// a repository we do not own is a real external write and is not something a demo may do. The property
// under test — that the platform records what it delivered and the console renders the lifecycle — is
// real regardless of whether the forge on the other end is GitHub or a simulation.
//
// # Scenarios (which delivery-route condition to show)
//
//	-scenario populated  (default) a configured route with deliveries in several states
//	-scenario no-route   verified proposals exist but no route is configured (FR13 reported state)
//	-scenario degraded   a route whose CI credential rotated away (reported degraded)
//
// # Usage
//
//	go run ./cmd/p12hermes                       # serve the populated scenario on :4321
//	go run ./cmd/p12hermes -scenario no-route
//
// Then run the console against it:
//
//	cd web/console && PLATFORM_API_BASE=http://127.0.0.1:4321 \
//	  CONSOLE_PLATFORM_CREDENTIAL=p12hermes-demo-credential-do-not-ship \
//	  CONSOLE_TENANT_IDENTITY=dev npm run dev
//	# then open http://127.0.0.1:4320/app/delivery
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/evalstats"
	fd "github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/verification"
)

const (
	tenant     = "dev"
	demoCred   = "p12hermes-demo-credential-do-not-ship"
	consoleURL = "http://127.0.0.1:4320/app/delivery"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4321", "listen address for the platform API")
	scenario := flag.String("scenario", "populated", "populated | no-route | degraded")
	emit := flag.Bool("emit", false, "run one CI-mediated delivery for hermes end to end, print the PR + record, and exit")
	repo := flag.String("repo", "/tmp/hermes-agent", "a hermes-agent checkout, used to read the real HEAD revision for the -emit run")
	flag.Parse()

	if *emit {
		emitHermesDelivery(*repo)
		return
	}

	rec := deliveryrecord.NewMemStore()
	gate := newDemoGate()
	ents := &demoEnts{team: true, enterprise: true}
	halt := fd.HaltReaderFunc(func(string) (bool, string, error) { return false, "", nil })
	del := fd.NewDeliverer(gate, ents, halt, rec, fd.DefaultOpenPRBound)
	del.SetClock(func() time.Time { return time.Unix(0, 0).UTC() })

	routes := &demoRoutes{scenario: *scenario}
	pending := &demoPending{gate: gate, scenario: *scenario}
	svc := fd.NewService(del, routes, pending, "http://127.0.0.1:4320")

	if *scenario == "populated" {
		seedDeliveries(del, gate, rec)
	}

	cfg := config.Config{
		AuthMode:          "required",
		TenantCredentials: []config.TenantCredential{{TenantID: tenant, APIKey: demoCred, Role: "member", KeyID: "p12hermes"}},
	}
	srv := api.New(nil, cfg)
	srv.MountP12(svc)

	fmt.Printf("p12hermes: forge-delivery console API on http://%s  (scenario=%s)\n", *addr, *scenario)
	fmt.Printf("  MOUNTED  P12 delivery   GET /api/p12/deliveries\n")
	fmt.Printf("           CI-mediated    GET /api/p12/ci/pending   POST /api/p12/ci/report\n\n")
	fmt.Printf("console:\n")
	fmt.Printf("  cd web/console && PLATFORM_API_BASE=http://%s \\\n", *addr)
	fmt.Printf("    CONSOLE_PLATFORM_CREDENTIAL=%s \\\n", demoCred)
	fmt.Printf("    CONSOLE_TENANT_IDENTITY=%s npm run dev\n", tenant)
	fmt.Printf("  then open %s\n\n", consoleURL)

	log.Fatal(http.ListenAndServe(*addr, srv.Handler))
}

// seedDeliveries runs REAL deliveries through the core so the console renders round-tripped record
// state: one open, one merged (observed), one superseded by a newer proposal, one closed-without-merge.
func seedDeliveries(del *fd.Deliverer, gate *demoGate, rec *deliveryrecord.MemStore) {
	ctx := context.Background()
	forge := fd.NewInMemForge(fd.ForgeGitHub, true) // hosted-App writer for the demo path

	deliver := func(ch, rev, title, wf string, level fd.Level) fd.Result {
		gate.allow(ch, rev)
		p := fd.Proposal{
			TenantID: tenant, ProposalID: "prop-" + ch, ConfigHash: ch, SourceRevision: rev,
			Title: title, DiffStat: "3 files, +42 −11", Level: level,
			ConsoleRef: "http://127.0.0.1:4320/app/transforms/" + ch + "/" + rev,
		}
		route := &fd.Route{Mode: fd.ModeApp, ForgeKind: fd.ForgeGitHub,
			Target: fd.Target{Owner: "nousresearch", Repo: "hermes-agent", Base: "main", Workflow: wf}}
		res, err := del.Deliver(ctx, p, route, forge)
		if err != nil {
			log.Printf("seed deliver %s: %v", ch, err)
		}
		return res
	}

	// Workflow "reasoning": an open candidate, then a newer proposal supersedes it.
	deliver("cfg-reason-1", "rev-a1", "Route step-by-step traces to the cheaper model", "reasoning", entitlement.LevelAssisted)
	deliver("cfg-reason-2", "rev-a2", "Tighten the reasoning prompt; drop a redundant tool call", "reasoning", entitlement.LevelAssisted)

	// Workflow "extraction": delivered then MERGED (observed) — the billable outcome.
	merged := deliver("cfg-extract-1", "rev-b1", "Batch the extraction calls into one structured request", "extraction", entitlement.LevelAssisted)
	obs := fd.NewMergeObserver(rec)
	obs.SetClock(func() time.Time { return time.Unix(10, 0).UTC() })
	if err := obs.ObserveMerge(ctx, merged.DeliveryID, "a1b2c3d4e5", "ci"); err != nil {
		log.Printf("seed observe merge: %v", err)
	}

	// Workflow "summarize": delivered then CLOSED without merging (a human declined).
	closed := deliver("cfg-sum-1", "rev-c1", "Swap the summarizer to a smaller model at equal quality", "summarize", entitlement.LevelAssisted)
	obs.SetClock(func() time.Time { return time.Unix(20, 0).UTC() })
	if err := obs.ObserveClose(ctx, closed.DeliveryID, "app-webhook", "reviewer preferred to keep the current model"); err != nil {
		log.Printf("seed observe close: %v", err)
	}
}

// emitHermesDelivery runs ONE CI-mediated delivery for nousresearch/hermes-agent end to end and prints
// the concrete artifact: the exact pull-request body that would land on the repository, and the
// append-only delivery record the platform wrote. It is the "run it for hermes" demonstration.
//
// REAL here: the repository identity and — when the checkout is present — the actual HEAD revision as
// source_revision; the whole delivery core (gate/entitlement/halt/bound preconditions), the CI-mediated
// fetch→open→report flow, the PR-body render, and the append-only record. SIMULATED, and stated: the
// verification VERDICT (a real one needs the P4/P5.5 fan-out against a provider, which — per the
// p9hermes convention — this command does not stub), and the FORGE (opening a pull request on a
// repository we do not own is a real external write a demo may not perform).
func emitHermesDelivery(repo string) {
	ctx := context.Background()
	sourceRevision := hermesHeadRevision(repo)
	configHash := "cfg-" + sourceRevision[:min(8, len(sourceRevision))]

	rec := deliveryrecord.NewMemStore()
	gate := newDemoGate()
	gate.allow(configHash, sourceRevision)
	del := fd.NewDeliverer(gate, &demoEnts{team: true}, fd.HaltReaderFunc(func(string) (bool, string, error) {
		return false, "", nil
	}), rec, fd.DefaultOpenPRBound)

	target := fd.Target{Owner: "nousresearch", Repo: "hermes-agent", Base: "main", Workflow: "agent"}
	route := &fd.Route{Mode: fd.ModeCI, ForgeKind: fd.ForgeGitHub, Target: target}
	prop := fd.Proposal{
		TenantID: tenant, ProposalID: "hermes-opt-1", ConfigHash: configHash, SourceRevision: sourceRevision,
		Title:      "Route the agent's tool-selection step to a cheaper model at equal quality",
		DiffStat:   "2 files, +18 −6",
		Level:      entitlement.LevelAssisted,
		ConsoleRef: mustRef(configHash, sourceRevision),
	}

	// The CI-mediated flow: the platform PREPARES (server-side enforcement); the CI runner OPENS with its
	// own token (a simulated forge here, holding no platform credential); the platform RECORDS the report.
	prep, err := del.Prepare(ctx, prop, route)
	if err != nil {
		log.Fatalf("prepare: %v", err)
	}
	ciForge := fd.NewInMemForge(fd.ForgeGitHub, false) // false == the CI posture: no platform-held credential
	pr, created, err := fd.OpenFromPrepared(ctx, ciForge, prep, fd.DefaultOpenPRBound)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	if _, err := del.RecordFromReport(ctx, prep, fd.Report{DeliveryID: prep.DeliveryID, ForgeRef: pr.Ref, ForgeURL: pr.URL, Created: created}); err != nil {
		log.Fatalf("record: %v", err)
	}
	// A human later merges; the platform observes it (CI-reported) — the billable outcome.
	obs := fd.NewMergeObserver(rec)
	if err := obs.ObserveMerge(ctx, prep.DeliveryID, "hermesmerge0abc123", "ci"); err != nil {
		log.Fatalf("observe merge: %v", err)
	}

	body, _ := ciForge.PRBody(pr.Ref)
	fmt.Println("═══════════════════════════════════════════════════════════════════════════════")
	fmt.Println(" P12 forge delivery — run for github.com/nousresearch/hermes-agent")
	fmt.Println("═══════════════════════════════════════════════════════════════════════════════")
	fmt.Printf(" REAL:      repository identity + source_revision (%s), delivery core, CI-mediated\n", sourceRevision)
	fmt.Printf("            fetch→open→report, PR-body render, append-only record.\n")
	fmt.Printf(" SIMULATED: the verification verdict (needs the P4/P5.5 fan-out) and the forge itself\n")
	fmt.Printf("            (opening a PR on a repo we do not own is a real external write).\n\n")
	fmt.Printf(" mode:            %s  (platform holds no forge credential: %v)\n", prep.Mode, !ciForge.HoldsForgeCredential())
	fmt.Printf(" delivery_id:     %s\n", prep.DeliveryID)
	fmt.Printf(" head branch:     %s\n", prep.Branch)
	fmt.Printf(" pull request:    %s  (%s)\n", pr.Ref, pr.URL)
	fmt.Printf(" open PRs on repo: %d\n\n", openCount(ctx, ciForge, target))

	fmt.Println("─── pull-request body (exactly what lands on the customer's repository) ─────────")
	fmt.Println(body)
	fmt.Println("─── append-only delivery record (what P7 gainshare reads) ───────────────────────")
	hist, _ := rec.History(ctx, prep.DeliveryID)
	for _, e := range hist {
		line := fmt.Sprintf(" %-10s  mode=%s  forge_ref=%s", e.State, e.Mode, e.ForgeRef)
		if e.MergeCommit != "" {
			line += "  merge=" + e.MergeCommit
		}
		fmt.Println(line)
	}
	fmt.Println("─────────────────────────────────────────────────────────────────────────────────")
	fmt.Println(" A human merged it; the platform observed the merge (CI-reported) — the billable outcome.")
}

func openCount(ctx context.Context, f *fd.InMemForge, t fd.Target) int {
	n, _ := f.OpenPRCount(ctx, t)
	return n
}

func mustRef(ch, rev string) string {
	r, err := fd.ConsoleEvidenceRef("http://127.0.0.1:4320", ch, rev)
	if err != nil {
		return ""
	}
	return r
}

// hermesHeadRevision returns the checkout's HEAD sha (real lineage) or a stable placeholder when the
// checkout is absent, so -emit runs anywhere while using the real revision where it can.
func hermesHeadRevision(repo string) string {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return "hermeshead000000000000000000000000000000"
	}
	return strings.TrimSpace(string(out))
}

// ── demo collaborators ────────────────────────────────────────────────────────

// demoGate passes the gate for config hashes explicitly allowed. It is the demo's stand-in for the
// P5.5 verified-delta ledger; every verdict it returns is gate-passed with a real interval.
type demoGate struct{ allowed map[string]bool }

func newDemoGate() *demoGate             { return &demoGate{allowed: map[string]bool{}} }
func (g *demoGate) allow(ch, rev string) { g.allowed[ch+"|"+rev] = true }

func (g *demoGate) Verdict(_ context.Context, _, ch, rev string) (verification.Verdict, bool, error) {
	if !g.allowed[ch+"|"+rev] {
		return verification.Verdict{}, false, nil
	}
	return verification.Verdict{
		ConfigHash: ch, Metric: "quality",
		Delta:       evalstats.Interval{Mean: 0.11, Low: 0.03, High: 0.19, Confidence: 0.95},
		Significant: true, HeldOut: true, RegressionPass: true,
		GateResult: verification.GatePass, CasesFixed: []string{"c1", "c2", "c3"},
		CostDelta: -0.0009, LatencyDelta: -33,
	}, true, nil
}

// demoEnts allows delivery (Team) and, when enterprise is set, auto-merge.
type demoEnts struct{ team, enterprise bool }

func (e *demoEnts) CheckEntitlement(_ string, f plancfg.Feature, _ entitlement.AutomationLevel) (entitlement.Decision, error) {
	allow := false
	switch f {
	case plancfg.FeatureAssistedPR:
		allow = e.team
	case plancfg.FeatureAutoMerge:
		allow = e.enterprise
	}
	d := entitlement.Decision{Allowed: allow, Feature: f}
	if !allow {
		d.Reason, d.ReasonCode, d.UpgradePlanName = "not included in this plan", entitlement.ReasonNotEntitled, "Team"
	}
	return d, nil
}

// demoRoutes reports the route condition per scenario.
type demoRoutes struct{ scenario string }

func (r *demoRoutes) RouteFor(_ context.Context, _, _ string) (*fd.Route, error) {
	if r.scenario == "no-route" {
		return nil, nil
	}
	return &fd.Route{Mode: fd.ModeApp, ForgeKind: fd.ForgeGitHub,
		Target: fd.Target{Owner: "nousresearch", Repo: "hermes-agent", Base: "main"}}, nil
}

func (r *demoRoutes) Capability(_ context.Context, _ string) (fd.RouteConditionKind, string, error) {
	if r.scenario == "degraded" {
		return fd.RouteDegraded, "The CI credential used for delivery to nousresearch/hermes-agent expired 2 days ago.", nil
	}
	return "", "", nil
}

// demoPending returns verified proposals for the no-route scenario, so "verified proposals exist but no
// route is configured" is a real, non-empty condition rather than a fabricated banner.
type demoPending struct {
	gate     *demoGate
	scenario string
}

func (p *demoPending) PendingVerified(_ context.Context, _ string) ([]fd.Proposal, error) {
	if p.scenario != "no-route" {
		return nil, nil
	}
	p.gate.allow("cfg-pending-1", "rev-p1")
	return []fd.Proposal{{
		TenantID: tenant, ProposalID: "prop-pending", ConfigHash: "cfg-pending-1", SourceRevision: "rev-p1",
		Title: "Verified but undelivered", Level: entitlement.LevelAssisted,
	}}, nil
}
