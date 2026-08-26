// Command improvehermes runs P35's improvement run against a REAL repository — by default
// github.com/nousresearch/hermes-agent — and prints what is real, what is not, and why.
//
// # Why this exists when every P35 fence is green
//
// Every fence in `internal/improvementrun` is green and all twenty gates have been drilled red
// (`make p35-fence-redcheck`). Green fences prove the parts, against fixtures this repository wrote,
// with node ids and deltas chosen to make the assertion clean. That is the right shape for a fence and
// **it is not evidence about a customer's repository.**
//
// This proves the WALK. It discovers hermes-agent's actual call sites, builds the plan against the ids
// that come out, drives the SHIPPED `optimizer.Controller` under that plan's bounds, and prints every
// refusal naming a symbol somebody else wrote.
//
// # 🔴 What is REAL here, and what is NOT — stated first, not buried
//
//	REAL   P1 discovery over the checkout: the call sites, the node ids, the edges. Every id printed
//	       below came out of hermes-agent's own source.
//	REAL   the source revision, read from the checkout with `sourcerev.Resolve` and refused if the
//	       checkout is not at the commit claimed.
//	REAL   `improvementrun.Translate` — the plan, its bounds, and the refusals. A question that cannot
//	       be bounded is refused here exactly as it is in production.
//	REAL   the SHIPPED `optimizer.Controller`, driven by a bounded enumerator built from the plan. No
//	       fork of the loop; the gates it calls are the gates it calls for every other caller.
//	REAL   `improvementrun.Reconcile` — the withdrawal decision, including the provider-moved and
//	       pin-broken cases, computed by the same function the product runs.
//
//	NOT    the VERDICTS. Verification runs the customer's eval harness on the customer's machine
//	       against the customer's eval set. This command has none of the three, and a number invented
//	       here would be the demo that overstates — a support and churn cost that lands after the sale.
//	       So the verdicts below are declared, printed as declared, and every delta is labelled.
//	NOT    the FORGE. 🚫 This opens no pull request. hermes-agent is somebody else's repository and
//	       this command has no business writing to it. `make p35-live-four-step` is the one that talks
//	       to a real forge, against a repository whoever runs it chose.
//
// # 🚫 It calls no provider and costs nothing
//
// Usage:
//
//	git clone --depth 1 https://github.com/nousresearch/hermes-agent /tmp/hermes-agent
//	make improve-hermes
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/forgedelivery"
	"github.com/heros-foreal/agentd/internal/improvementrun"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/proposalgen"
	"github.com/heros-foreal/agentd/internal/sourcerev"
	"github.com/heros-foreal/agentd/internal/verification"
)

const modelVersion = "declared-not-measured"

func main() {
	log.SetFlags(0)
	local := flag.String("local", "", "a checkout already on disk (required; clone it first)")
	workflow := flag.String("workflow", "github.com/nousresearch/hermes-agent", "the workflow id to label the run with")
	flag.Parse()

	if *local == "" {
		log.Fatal("improvehermes: -local is required. Point it at a checkout of the repository under test.\n" +
			"  git clone --depth 1 https://github.com/nousresearch/hermes-agent /tmp/hermes-agent\n" +
			"  go run ./cmd/proof/improvehermes -local /tmp/hermes-agent")
	}
	ctx := context.Background()

	fmt.Println("P35 · the improvement run, against a real repository")
	fmt.Println(strings.Repeat("─", 78))
	fmt.Println("REAL: discovery, the source revision, the plan and its refusals, the shipped optimizer loop,")
	fmt.Println("      the bounded enumerator, and the withdrawal decision.")
	fmt.Println("NOT : the verdicts (no eval set, no harness, no provider) and the forge (opens nothing).")
	fmt.Println(strings.Repeat("─", 78))

	// ── 1 · the real tree ────────────────────────────────────────────────────────────────────────
	step(1, "discover the repository's own call sites")
	reg, err := discovery.DefaultRegistry()
	if err != nil {
		log.Fatalf("discovery registry: %v", err)
	}
	res, err := discovery.Run(discovery.Options{
		Repo: *local, Registry: reg, WorkflowID: *workflow, CommitSHA: "local",
	})
	if err != nil {
		log.Fatalf("discovery: %v", err)
	}
	ir := &res.IR
	if len(ir.Nodes) == 0 {
		log.Fatalf("improvehermes: discovery found no nodes in %s. There is nothing to propose against, "+
			"and inventing one would be the demo that overstates", *local)
	}
	fmt.Printf("    %d node(s), %d edge(s) — every id below is the repository's own\n",
		len(ir.Nodes), len(ir.Edges))
	for i, n := range ir.Nodes {
		if i >= 4 {
			fmt.Printf("      … and %d more\n", len(ir.Nodes)-4)
			break
		}
		fmt.Printf("      %s\n", n.NodeID)
	}

	// ── 2 · the real revision ────────────────────────────────────────────────────────────────────
	step(2, "read the source revision from the checkout, refusing a claim it cannot verify")
	revision, note, err := sourcerev.Resolve(*local, "")
	if err != nil {
		log.Fatalf("source revision: %v", err)
	}
	fmt.Printf("    %s\n    (%s)\n", revision, note)

	// ── 3 · a question becomes a bounded plan ────────────────────────────────────────────────────
	step(3, "a question becomes a bounded plan — shown BEFORE anything runs")
	bounds := improvementrun.Bounds{
		TenantID: "proof", WorkflowID: *workflow, SourceRevision: revision,
		Origin: improvementrun.OriginConsole, MaxCandidates: 6, MaxSpendUSD: 3.00,
		NowMS: time.Now().UnixMilli(),
	}
	const question = "fix what you can prove, and open a pull request"
	plan, err := improvementrun.Translate(question, bounds)
	if err != nil {
		log.Fatalf("the question was refused: %v", err)
	}
	fmt.Printf("    question       %q\n", question)
	fmt.Printf("    plan           %s\n", plan.PlanID)
	fmt.Printf("    axes in scope  %d — %s\n", len(plan.Axes), joinAxes(plan.Axes))
	fmt.Printf("    candidate cap  %d\n", plan.CandidateCap)
	fmt.Printf("    spend budget   $%.2f  (projected $%.2f)\n", plan.SpendBudgetUSD, plan.ProjectedSpendUSD)
	fmt.Printf("    stops when     verified gain falls below %.3f\n", plan.Stopping.MinImprovement)
	fmt.Printf("    acknowledge?   %v (threshold $%.2f)\n",
		plan.RequiresAcknowledgement(), improvementrun.DisclosureThresholdUSD)

	// ── 4 · the refusals, on this repository's own subject ───────────────────────────────────────
	step(4, "questions that cannot be bounded — REFUSED, each naming its own next action")
	for _, q := range []string{
		"keep improving it with no limit until it is perfect",
		"improve all my repositories",
		"improve my retriever",
	} {
		_, err := improvementrun.Translate(q, bounds)
		var ref *improvementrun.Refusal
		if !asRefusal(err, &ref) {
			fail("%q was NOT refused (%v)", q, err)
			continue
		}
		fmt.Printf("    ✓ %q\n      cause  %s\n      says   %s\n      next   %s\n",
			q, ref.Cause, wrap(ref.Detail, 6), wrap(ref.NextAction, 6))
	}
	// … and one that IS bounded, scoped to a single axis by its own words.
	scoped, err := improvementrun.Translate("improve my memory strategy", bounds)
	if err != nil {
		fail("a single-axis question was refused: %v", err)
	} else {
		fmt.Printf("    ✓ %q → %d axis in scope (%s), same cap and budget\n",
			"improve my memory strategy", len(scoped.Axes), joinAxes(scoped.Axes))
	}

	// ── 5 · candidates from the repository's own nodes, through the shipped loop ─────────────────
	step(5, "the SHIPPED optimizer loop, under this plan's bounds, over this repository's nodes")
	enum := &nodeEnumerator{nodes: ir.Nodes}
	bounded := improvementrun.NewBoundedEnumerator(plan, enum)
	targets := enum.targets()
	fmt.Printf("    %d target(s) built from the repository's nodes\n", len(targets))

	kill := optimizer.NewKillSwitch()
	ctrl := &optimizer.Controller{
		Search:   optimizer.Search{Enum: bounded},
		Verifier: declaredVerifier{},
		Repo:     optimizer.NewFakeRepo([]byte(`{"baseline":true}`)),
		Ledger:   optimizer.NewMemLedger(),
		Kill:     kill,
		Clock:    time.Now,
	}
	runRes, err := ctrl.Run(ctx, optimizer.RunInput{
		Authority: optimizer.Authority{
			RunID: "proof", WorkflowID: *workflow, Actor: "improvehermes",
			Constraints: plan.Constraints(),
			// 🚫 The three merge prerequisites are NOT armed, exactly as the product leaves them: P35
			// opens pull requests and does not merge, and arming them here would give the loop a merge
			// path that bypasses the approval gate.
			GrantedAt: time.Now(),
		},
		Targets:           targets,
		BaselineSpecBytes: []byte(`{}`),
		EvalSetCaseIDs:    []string{"declared-1", "declared-2"},
	})
	if err != nil {
		log.Fatalf("the loop refused to start: %v", err)
	}
	outcome := improvementrun.OutcomeOf(runRes, kill.Fired())
	fmt.Printf("    admitted       %d distinct candidate(s) under a cap of %d\n",
		bounded.Admitted(), plan.CandidateCap)
	fmt.Printf("    out of scope   %d (the plan's axes excluded them)\n", bounded.OutOfScope())
	fmt.Printf("    iterations     %d\n", len(runRes.Iterations))
	fmt.Printf("    stopped on     %s\n", boundLabel(outcome))
	fmt.Printf("                   %s\n", wrap(outcome.Sentence(), 19))
	fmt.Printf("    per axis       %v\n", bounded.PerAxis())

	// ── 6 · only a verified candidate is surfaced ────────────────────────────────────────────────
	step(6, "only a candidate the gate passed is surfaced — and the delta travels with it")
	cand := optimizer.SearchCandidate{
		DiagnosisID: "declared", Node: ir.Nodes[0].NodeID, Dimension: string(assessment.AxisModel),
		ConfigHash: "declared-cfg-" + shortID(ir.Nodes[0].NodeID), Operator: "model_downgrade",
		Rationale: "declared for this walk; a real rationale comes from the operator that fired",
	}
	failing := declaredResult(0.42)
	failing.Verdict.GateResult, failing.Verdict.Significant = verification.GateFailSig, false
	if _, err := improvementrun.NewVerifiedProposal("proof", plan, cand, failing,
		"prop-1", "diff-1", "declared", modelVersion, nil); err == nil {
		fail("a candidate the gate FAILED was surfaced, with a +0.42 delta")
	} else {
		fmt.Printf("    ✓ a +0.420 candidate that failed the gate is NOT surfaced:\n%s\n", indent(err.Error()))
	}
	good, err := improvementrun.NewVerifiedProposal("proof", plan, cand, declaredResult(0.031),
		"prop-1", "/app/transforms/declared/"+revision, "declared", modelVersion, nil)
	if err != nil {
		log.Fatalf("a gate-passing candidate was refused: %v", err)
	}
	fmt.Printf("    ✓ surfaced: %s on %s\n      %s\n", good.Operator, good.Node, good.DeltaLabel())

	// ── 7 · the second measurement is allowed to disagree ────────────────────────────────────────
	step(7, "re-measurement after applying — allowed to DISAGREE, and disagreement withdraws")
	verified := improvementrun.Measurement{
		Delta: good.Delta, Significant: good.Significant, ProviderModelVersion: modelVersion,
		ResolvedConfigHash: good.ConfigHash, SourceRevision: revision,
	}
	reproduced := verified
	if w, _ := improvementrun.Reconcile(good, verified, reproduced, nil, time.Now().UnixMilli()); w != nil {
		fail("a change that reproduced exactly was withdrawn: %s", w.Reason)
	} else {
		fmt.Println("    ✓ a re-measurement that reproduces proceeds to delivery")
	}

	disagreed := verified
	disagreed.Delta = evalstats.Interval{
		Mean: 0.002, Low: -0.004, High: 0.005, NSeeds: 5, NCases: 48,
		Method: "bootstrap", Confidence: 0.95,
	}
	disagreed.Significant = false
	w, _ := improvementrun.Reconcile(good, verified, disagreed, nil, time.Now().UnixMilli())
	if w == nil || w.Reason != improvementrun.WithdrawnDidNotReproduce {
		fail("a re-measurement that disagreed did not withdraw the change")
	} else {
		fmt.Printf("    ✓ withdrawn, with BOTH numbers:\n%s\n", indent(w.Sentence()))
	}

	moved := verified
	moved.ProviderModelVersion = "a-different-model-version"
	moved.Delta = disagreed.Delta
	w2, _ := improvementrun.Reconcile(good, verified, moved, nil, time.Now().UnixMilli())
	if w2 == nil || w2.Reason != improvementrun.WithdrawnProviderMoved {
		fail("a provider that MOVED was blamed on the change")
	} else {
		fmt.Printf("    ✓ a provider that moved is NOT blamed on the change:\n%s\n", indent(w2.Sentence()))
	}

	pinned, _ := improvementrun.Reconcile(good, verified, improvementrun.Measurement{},
		improvementrun.ErrPinBroken, time.Now().UnixMilli())
	if pinned == nil || pinned.Reason != improvementrun.WithdrawnPinBroken {
		fail("a broken pin was scored rather than failed")
	} else {
		fmt.Println("    ✓ a re-measurement resolving a different configuration FAILS rather than scoring")
	}

	// ── 8 · what a pull request would say, and what it would NOT do ──────────────────────────────
	step(8, "the pull request body this change would carry — and the forge that is NOT called")
	body := forgedelivery.RenderPRBody(forgedelivery.Evidence{
		Title:      fmt.Sprintf("%s on %s", good.Operator, good.Node),
		Level:      "assisted",
		Verdict:    declaredResult(0.031).Verdict,
		ConfigHash: good.ConfigHash, SourceRevision: revision,
		Axis: good.Axis.String(), Node: good.Node, Operator: good.Operator,
		EvalSetCases: 48, EvalSetIndecisive: 10,
		DiffStat: "declared", RevertRef: "<the merge commit>",
		ConsoleRef: "/app/improve",
	})
	for _, want := range []string{"## What changed", "## How decisive the eval set is", "## How to revert this"} {
		if !strings.Contains(body, want) {
			fail("the pull request body carries no %q", want)
		}
	}
	fmt.Printf("    ✓ %d bytes, contract %s, carrying the axis, the node, the delta with its interval,\n"+
		"      how decisive the set was, and how to revert\n", len(body), forgedelivery.PRBodyContractVersion)
	fmt.Printf("    🚫 NO PULL REQUEST WAS OPENED. %s is somebody else's repository, and this command\n"+
		"       has no business writing to it. `make p35-live-four-step` is the one that talks to a\n"+
		"       forge, against a repository whoever runs it chose.\n", *workflow)

	// ── 9 · what a hosted deployment would actually answer today ─────────────────────────────────
	step(9, "what a hosted deployment answers for this repository today — stated, not implied")
	empty, err := improvementrun.EmptyStateFor(proposalgen.Result{
		State: proposalgen.StateNoRuns,
		Detail: "No runs have been linked for this workflow, so there is nothing to attribute cost to. " +
			"Run `heros link` after an eval.",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("    with no linked runs, the surface says — by NAME, never as an empty result:\n")
	fmt.Printf("      state  %s\n      says   %s\n      next   %s\n",
		empty.State, wrap(empty.Headline, 6), wrap(empty.NextAction, 6))
	fmt.Printf("    and %d other named states exist for the other ways a run finds nothing\n",
		countEmptyStates()-1)

	fmt.Println()
	fmt.Println(strings.Repeat("─", 78))
	if failed {
		fmt.Println("improvehermes: FAILED — see the ✗ lines above")
		os.Exit(1)
	}
	fmt.Printf("improvehermes: the walk completed against %s at %s\n", *workflow, revision[:12])
	fmt.Println("  🔴 The deltas above are DECLARED, not measured. Verification runs your eval harness on")
	fmt.Println("     your machine against your eval set; this command has none of the three, and a number")
	fmt.Println("     invented here would be the demo that overstates.")
	fmt.Println("  🚫 No provider was called. No pull request was opened. Nothing was spent.")
}

// ── the seams this walk supplies, each named for what it stands in for ───────────────────────────

// nodeEnumerator builds one candidate per discovered node, on the model axis.
//
// 🔴 It is DETERMINISTIC per target — the same target yields the same config hashes on every call —
// because the loop re-enumerates each iteration and matches against its own consumed set by hash. A
// delegate that minted a fresh hash per call would make the run report the search space as exhausted
// after a single candidate. See `BoundedEnumerator.Delegate`.
type nodeEnumerator struct{ nodes []discovery.IRNode }

// targets builds one attributed target per node, on the model axis, in the order discovery found them.
//
// 🔴 Capped at a handful. hermes-agent has more call sites than a walk should enumerate, and a proof
// that printed two hundred targets would be a proof nobody reads — but the CAP is stated rather than
// silent, because a silent truncation reads as "this is all there was".
func (e *nodeEnumerator) targets() []optimizer.Target {
	const shown = 6
	out := make([]optimizer.Target, 0, shown)
	for i, n := range e.nodes {
		if i >= shown {
			fmt.Printf("    (only the first %d of %d nodes are enumerated here — a walk, not a sweep)\n",
				shown, len(e.nodes))
			break
		}
		out = append(out, optimizer.Target{
			DiagnosisID: fmt.Sprintf("declared-%d", i), Node: n.NodeID,
			Dimension: string(assessment.AxisModel), Priority: float64(shown - i),
		})
	}
	return out
}

// Enumerate implements optimizer.Enumerator, deterministically per target.
func (e *nodeEnumerator) Enumerate(t optimizer.Target) []optimizer.SearchCandidate {
	return []optimizer.SearchCandidate{{
		DiagnosisID: t.DiagnosisID, Node: t.Node, Dimension: t.Dimension,
		ConfigHash: "declared-cfg-" + shortID(t.Node), Operator: "model_downgrade",
		Rationale:    "declared for this walk; a real rationale comes from the operator that fired",
		SpecBytes:    []byte(`{}`),
		ExpectedGain: t.Priority,
	}}
}

// declaredVerifier returns a DECLARED result. It is named for what it is: this command holds no eval
// set, no harness and no provider, so it declares a verdict and every delta it produces is labelled.
type declaredVerifier struct{}

func (declaredVerifier) Verify(_ context.Context, req optimizer.VerifyRequest) (optimizer.VerifyResult, error) {
	return declaredResult(0.012), nil
}

func declaredResult(delta float64) optimizer.VerifyResult {
	return optimizer.VerifyResult{
		ContractOK: true, Builds: true, SpendUSD: 0.20,
		Verdict: verification.Verdict{
			GateResult: verification.GatePass, Significant: true, HeldOut: true, RegressionPass: true,
			Metric: "quality",
			Delta: evalstats.Interval{
				Mean: delta, Low: delta - 0.006, High: delta + 0.006,
				NSeeds: 5, NCases: 48, Method: "bootstrap", Confidence: 0.95,
			},
			CostDelta: -0.0021, LatencyDelta: -140,
		},
		Metrics: optimizer.CandidateMetrics{
			Providers: []string{"declared"}, Quality: 0.9, LatencyMS: 480,
			Composite: evalstats.Interval{Mean: 0.71, Low: 0.68, High: 0.74},
		},
	}
}

// ── printing ─────────────────────────────────────────────────────────────────────────────────────

var failed bool

func step(n int, what string) { fmt.Printf("\n%d) %s\n", n, what) }

func fail(format string, args ...any) {
	failed = true
	fmt.Printf("    ✗ "+format+"\n", args...)
}

func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		b.WriteString("      " + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// wrap re-flows a sentence at 78 columns with a hanging indent, so a refusal's own words are readable
// rather than one long line a reader skips.
func wrap(s string, indent int) string {
	const width = 78
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	col := indent
	for i, word := range strings.Fields(s) {
		if i > 0 && col+1+len(word) > width {
			b.WriteString("\n" + pad)
			col = indent
		} else if i > 0 {
			b.WriteString(" ")
			col++
		}
		b.WriteString(word)
		col += len(word)
	}
	return b.String()
}

func joinAxes(axes []assessment.Axis) string {
	out := make([]string, 0, len(axes))
	for _, a := range axes {
		out = append(out, a.String())
	}
	return strings.Join(out, ", ")
}

func boundLabel(o improvementrun.Outcome) string {
	if o.Faulted() {
		return "a FAULT (not a bound): " + o.Fault
	}
	if o.Bound == improvementrun.BoundNone {
		return "no bound"
	}
	return string(o.Bound)
}

func shortID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[len(s)-8:]
}

func countEmptyStates() int { return len(improvementrun.EmptyStates()) }

func asRefusal(err error, out **improvementrun.Refusal) bool {
	r, ok := err.(*improvementrun.Refusal)
	if ok {
		*out = r
	}
	return ok
}

var _ = proposal.DefaultCatalog
