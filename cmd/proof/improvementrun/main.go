// Command improvementrun serves the P35 improvement-run surface so the console can be driven in a real
// browser (tasks.md §8).
//
// # What is REAL here, and what is a stand-in — stated, not implied
//
// REAL: the whole `internal/improvementrun` service. The plan is produced by `Translate`, the run is
// driven by the shipped `optimizer.Controller` under the plan's own bounds, only P5.5-verified
// candidates are surfaced, approval goes through `internal/approval`'s seam, re-measurement can
// DISAGREE and withdraw, and delivery goes through `internal/forgedelivery`'s core. Every view the
// browser receives comes from `api.ImprovementPlanViewOf` / `api.ImprovementRunViewOf` — the same
// renderers the console gets in production.
//
// 🔴 That last point is the reason this exists rather than a JSON fixture. A fixture proves the page
// renders SOMETHING; it proves nothing about the view. The ordering, the delta label, the per-axis
// breakdown and the "cannot fail" computation are exactly the parts that could be wrong, and they are
// exactly the parts a hand-written fixture would reimplement correctly.
//
// A STAND-IN: the verifier, the eval harness and the forge. A real one of each needs a provider
// credential, a customer's eval set and a repository to push to — none of which a local browser check
// has — so this command supplies deterministic ones and says so here. What that means precisely: the
// NUMBERS on the screen are invented; the STRUCTURE, the gates they pass through, and every sentence
// rendered beside them are the product's.
//
// 🚫 It opens no pull request. The forge is in-memory. `make p35-live-four-step` is the one that talks
// to a real forge, and it is a separate target for that reason.
//
// # Usage
//
//	go run ./cmd/proof/improvementrun                      # serves on 127.0.0.1:4399
//	go run ./cmd/proof/improvementrun -withdraw            # the re-measurement DISAGREES, so a change
//	                                                       # is approved, applied and then WITHDRAWN
//
// Then run the console against it:
//
//	cd web/console && npm run dev:browser        # PLATFORM_API_BASE is already 127.0.0.1:4399
//
// and open http://127.0.0.1:4320/app/improve
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/deliveryrecord"
	"github.com/heros-foreal/agentd/internal/entitlement"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/improvementrun"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/plancfg"
	"github.com/heros-foreal/agentd/internal/proposalgen"
	"github.com/heros-foreal/agentd/internal/verification"
)

var (
	addr     = flag.String("addr", "127.0.0.1:4399", "listen address")
	withdraw = flag.Bool("withdraw", false,
		"make the re-measurement DISAGREE, so an approved change is applied and then withdrawn (FR16)")
	empty = flag.String("empty", "",
		"force a `nothing to propose` state by name, e.g. no_linked_runs — see internal/proposalgen")
)

const (
	tenantID   = "tenant-hermes"
	workflowID = "github.com/nousresearch/hermes-agent"
	revision   = "9f2c1a4e77b3d0e58a61bb2c4d7e9f01a3b5c6d7"
	modelVer   = "claude-opus-5-2026-05-01"
)

func main() {
	flag.Parse()

	svc := buildService()
	s := api.New(nil, config.Config{})
	s.MountImprovementRuns(svc)

	// 🔴 A fixed principal, because this command is a BROWSER CHECK and not a security demo. It is
	// stated rather than hidden: nothing here exercises the auth gate, and the routes' own tests do.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithPrincipal(r.Context(), auth.Principal{
			TenantID: tenantID, UserID: "you@example.com",
		})
		// The origin decides the delivery MODE (R3), and it is read from the transport. The console's
		// BFF sends its own user agent in production; this sets it so the run takes the console path.
		r.Header.Set("User-Agent", "heros-console/proof")
		s.Handler.ServeHTTP(w, r.WithContext(ctx))
	})

	log.Printf("p35 improvement-run proof on http://%s", *addr)
	log.Printf("  tenant=%s workflow=%s revision=%s", tenantID, workflowID, revision[:12])
	log.Printf("  re-measurement will %s", map[bool]string{
		true:  "DISAGREE — an approved change is applied and then WITHDRAWN (FR16)",
		false: "reproduce — an approved change is delivered",
	}[*withdraw])
	if *empty != "" {
		log.Printf("  forcing the `nothing to propose` state %q", *empty)
	}
	log.Printf("  console: cd web/console && npm run dev:browser  →  http://127.0.0.1:4320/app/improve")
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatal(err)
	}
}

func buildService() *improvementrun.Service {
	now := func() time.Time { return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC) }
	ledger := improvementrun.NewMemLedger()
	source := &fixtureSource{proposals: map[string]improvementrun.VerifiedProposal{}}

	return &improvementrun.Service{
		Bounds:         fixtureBounds{},
		Acks:           improvementrun.NewMemAckStore(),
		Enumerations:   source,
		Proposals:      source,
		ProposalReader: source,
		Ledger:         ledger,
		Metrics:        improvementrun.NewMetrics(),
		Verifier:       source,
		Contract:       source,
		Repo:           optimizer.NewFakeRepo([]byte(`{"baseline":true}`)),
		ChangeLedger:   optimizer.NewMemLedger(),
		Approvals:      improvementrun.NewMemApprovalGate(),
		Remeasure:      source,
		Subject: func(_ context.Context, _ improvementrun.Plan, p improvementrun.VerifiedProposal) (improvementrun.Binding, error) {
			return improvementrun.Binding{ConfigHash: p.ConfigHash, SourceRevision: revision}, nil
		},
		Deliveries: forgedelivery.NewDeliverer(
			source, allowAll{}, openHalt{}, deliveryrecord.NewMemStore(), 10),
		Routes: source,
		Now:    now,
	}
}

type fixtureBounds struct{}

// BoundsFor returns bounds that project ABOVE the disclosure threshold on purpose: the acknowledgement
// step is the one this page most needs looked at in a browser, and a fixture that stayed under the
// threshold would render the flow that skips it.
func (fixtureBounds) BoundsFor(_ context.Context, tenant string) (improvementrun.Bounds, error) {
	return improvementrun.Bounds{
		TenantID: tenant, WorkflowID: workflowID, SourceRevision: revision,
		MaxCandidates: 12, MaxSpendUSD: 6.00,
	}, nil
}

// fixtureSource is the enumerator, the verifier, the contract check, the proposal recorder and reader,
// the re-measurer, the gate oracle and the route source — one type, because they are all the same
// stand-in and splitting them across six would suggest six independent decisions.
type fixtureSource struct {
	mu sync.Mutex
	// proposals is what `ProposalReader` reads back. 🔴 Populated by `Record`, which is where a real
	// deployment's proposal store is written too — a reader that could not see what the recorder wrote
	// would make `Decide` fail with "no such proposal" for a proposal the page is displaying.
	proposals map[string]improvementrun.VerifiedProposal
}

// candidates are three changes on three axes, one of which FAILS the held-out gate. The failing one is
// deliberate: a page that only ever renders successes never shows what the gate is for.
var candidates = []struct {
	axis     assessment.Axis
	node     string
	operator string
	why      string
	delta    float64
	passes   bool
}{
	{assessment.AxisModel, "extract_entities", "model_downgrade",
		"this node accounts for 61% of the workflow's cost and a cheaper published tier scores within noise on the held-out set",
		0.012, true},
	{assessment.AxisContext, "summarise", "context_policy_switch",
		"this node is handed the full transcript on every call; the last three turns score the same and cost a quarter as much",
		0.031, true},
	{assessment.AxisPrompt, "classify", "instruction_harden",
		"this prompt leaves the output shape implicit, and the failures cluster on malformed output",
		0.004, false},
}

func (f *fixtureSource) Enumerate(_ context.Context, p improvementrun.Plan) (improvementrun.Enumeration, error) {
	if *empty != "" {
		st, err := improvementrun.EmptyStateFor(proposalgen.Result{
			State:  proposalgen.State(*empty),
			Detail: "This sentence comes from the generation pass itself, which knows things the state table does not.",
		})
		if err != nil {
			return improvementrun.Enumeration{}, err
		}
		return improvementrun.Enumeration{State: st}, nil
	}
	st, _ := improvementrun.EmptyStateFor(proposalgen.Result{
		State: proposalgen.StateNoBottleneck,
		Detail: "No node dominates this workflow's cost or latency. There is nothing here to downgrade, " +
			"which is a healthy result rather than a missing one.",
	})
	var targets []optimizer.Target
	for i, c := range candidates {
		targets = append(targets, optimizer.Target{
			DiagnosisID: fmt.Sprintf("diag_%d", i), Node: c.node,
			Dimension: string(c.axis), Priority: float64(len(candidates) - i),
		})
	}
	return improvementrun.Enumeration{
		Enumerator: candidateSource{}, Targets: targets, State: st,
		BaselineSpecBytes: []byte(`{}`), EvalSetCaseIDs: []string{"c1", "c2", "c3", "c4"},
	}, nil
}

// candidateSource is the delegate `optimizer.Enumerator`, a separate type because `fixtureSource`
// already has an `Enumerate` with the EnumerationSource signature and one type cannot satisfy both.
//
// 🔴 Deterministic per target, which every real enumerator is — see `BoundedEnumerator.Delegate` for
// what happens to a loop when a delegate mints a fresh config hash on every call.
type candidateSource struct{}

func (candidateSource) Enumerate(t optimizer.Target) []optimizer.SearchCandidate {
	for _, c := range candidates {
		if string(c.axis) != t.Dimension {
			continue
		}
		return []optimizer.SearchCandidate{{
			DiagnosisID: "diag", Node: c.node, Dimension: string(c.axis),
			// A REALISTIC hash. The console renders the first twelve characters, and a readable
			// `cfg_extract_entities` would truncate to `cfg_extract_` — which looks like a rendering bug
			// in a browser check whose whole job is to show what production shows.
			ConfigHash: configHashFor(c.node), Operator: c.operator, Rationale: c.why,
			SpecBytes: []byte(`{}`), ExpectedGain: c.delta,
		}}
	}
	return nil
}

// configHashFor derives a stable, realistic-looking config hash from a node name.
func configHashFor(node string) string {
	sum := sha256.Sum256([]byte("p35-proof\x00" + node))
	return hex.EncodeToString(sum[:20])
}

func (f *fixtureSource) Check(optimizer.SearchCandidate) (bool, string) { return true, "" }

func (f *fixtureSource) Verify(_ context.Context, req optimizer.VerifyRequest) (optimizer.VerifyResult, error) {
	for _, c := range candidates {
		if configHashFor(c.node) != req.Candidate.ConfigHash {
			continue
		}
		res := optimizer.VerifyResult{
			ContractOK: true, Builds: true, SpendUSD: 0.35,
			Verdict: verification.Verdict{
				GateResult: verification.GatePass, Significant: true, HeldOut: true, RegressionPass: true,
				Metric: "quality",
				Delta: evalstats.Interval{
					Mean: c.delta, Low: c.delta - 0.006, High: c.delta + 0.006,
					NSeeds: 5, NCases: 48, Method: "bootstrap", Confidence: 0.95,
				},
				CostDelta: -0.0021, LatencyDelta: -140,
			},
			Metrics: optimizer.CandidateMetrics{
				Providers: []string{"anthropic"}, Quality: 0.9, LatencyMS: 480,
				Composite: evalstats.Interval{Mean: 0.71, Low: 0.68, High: 0.74},
			},
		}
		if !c.passes {
			// 🔴 The gate FAILS. It is not surfaced, and the page shows what the gate is for.
			res.Verdict.GateResult, res.Verdict.Significant = verification.GateFailSig, false
		}
		return res, nil
	}
	return optimizer.VerifyResult{ContractOK: true, Builds: true}, nil
}

func (f *fixtureSource) Record(_ context.Context, p improvementrun.Plan, runID string, cand optimizer.SearchCandidate, vr optimizer.VerifyResult) (
	string, string, string, string, *assessment.EvalSetReport, error) {
	id := "prop_" + cand.Node
	// 🔴 The case list carries a DECISIVE oracle, so `CannotFail()` is false and the card renders the
	// ordinary path. The first version of this fixture listed one case with a zero `OracleVerdict` —
	// which is `Decisive: false` — so every proposal rendered the "every case passes whatever the agent
	// does" warning beside a set the same fixture said was 79% covered. The screen contradicted itself,
	// and the product was right: `CannotFail()` reads the CASE LIST, not the coverage number, exactly so
	// the claim and the enumeration behind it cannot disagree.
	evalSet := &assessment.EvalSetReport{
		EvalSetHash: "set-hermes-1", NCases: 48, CoverageMeasured: true,
		OracleCoverage: 0.79, NIndecisive: 10,
		Cases: []assessment.CaseView{
			{CaseID: "c1", Suite: "extraction", Oracle: evalharness.OracleVerdict{Decisive: true}},
			{CaseID: "c2", Suite: "extraction", Oracle: evalharness.OracleVerdict{Decisive: true}},
		},
	}
	if vp, err := improvementrun.NewVerifiedProposal(runID, p, cand, vr, id,
		"/app/transforms/"+cand.ConfigHash+"/"+revision, "1 file, +7 −3", modelVer, evalSet); err == nil {
		f.mu.Lock()
		f.proposals[id] = vp
		f.mu.Unlock()
	}
	return id,
		"/app/transforms/" + cand.ConfigHash + "/" + revision,
		"1 file, +7 −3",
		modelVer,
		evalSet,
		nil
}

func (f *fixtureSource) Proposal(_ context.Context, _, id string) (improvementrun.VerifiedProposal, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.proposals[id]
	return p, ok, nil
}

// Remeasure is the second observation, and the `-withdraw` flag is what makes it DISAGREE.
func (f *fixtureSource) Remeasure(_ context.Context, p improvementrun.VerifiedProposal, want improvementrun.Binding) (improvementrun.Measurement, error) {
	m := improvementrun.Measurement{
		Delta: p.Delta, Significant: p.Significant, ProviderModelVersion: p.ProviderModelVersion,
		ResolvedConfigHash: want.ConfigHash, SourceRevision: want.SourceRevision, SpendUSD: 0.28,
	}
	if *withdraw {
		// A delta that does not reproduce: the intervals do not overlap, so `Reproduced` says no and the
		// change is withdrawn before delivery, with BOTH numbers reported.
		m.Delta = evalstats.Interval{
			Mean: 0.002, Low: -0.004, High: 0.005, NSeeds: 5, NCases: 48,
			Method: "bootstrap", Confidence: 0.95,
		}
		m.Significant = false
	}
	return m, nil
}

func (f *fixtureSource) Route(_ context.Context, _, _ string, mode forgedelivery.Mode) (
	*forgedelivery.Route, forgedelivery.ForgeWriter, bool, error) {
	route := &forgedelivery.Route{
		Mode: mode, ForgeKind: forgedelivery.ForgeGitHub,
		Target: forgedelivery.Target{Owner: "nousresearch", Repo: "hermes-agent", Base: "main"},
	}
	if mode == forgedelivery.ModeApp {
		return route, forgedelivery.NewInMemForge(forgedelivery.ForgeGitHub, true), true, nil
	}
	return route, nil, true, nil
}

// Verdict is the gate ORACLE `forgedelivery.Prepare` consults. It answers from the same table the
// verifier does, so the two cannot disagree.
func (f *fixtureSource) Verdict(_ context.Context, _, configHash, _ string) (verification.Verdict, bool, error) {
	res, _ := f.Verify(context.Background(), optimizer.VerifyRequest{
		Candidate: optimizer.SearchCandidate{ConfigHash: configHash},
	})
	return res.Verdict, true, nil
}

type allowAll struct{}

func (allowAll) CheckEntitlement(string, plancfg.Feature, entitlement.AutomationLevel) (entitlement.Decision, error) {
	return entitlement.Decision{Allowed: true, PlanName: "Team"}, nil
}

type openHalt struct{}

func (openHalt) HaltsDelivery(string) (bool, string, error) { return false, "", nil }
