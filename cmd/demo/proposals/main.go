// Command proposals serves the P5.5 ranked-recommendation + verification surface against the REAL
// verification gate (task 7.7: drive the ranked list + source-diff-with-evidence + trend +
// Advisory/Assisted screens against a live, stubbed-provider verification fan-out; confirm all states,
// held-out labelling, cases-fixed/broken rendering, source-diff rendering, and that Assisted PR-open is
// gated on verification).
//
// Every verdict on screen came out of internal/verification.Verify — the significance gate
// (evalstats.Compare), the regression check, and the nothing-unverified filter are the shipped code
// path. The ONLY stub is the EvalRunner (the provider/harness fan-out): the deltas are canned so the
// five verification outcomes (known-good, noise, overfit, cost-regression, cluster-regression) are
// reproducible without a live model. Presentation, narration, the held-out label, the automation-level
// PR gate, and the trend view are all real code (api.BuildCard + internal/verification view helpers).
//
// Not a shipped service: a demo harness.
//
//	go run ./cmd/demo/proposals   # then open the printed URL
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sort"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/verification"
)

var evalSet = []string{"g1", "g2", "g3", "h1", "h2", "h3", "h4", "h5", "h6"}

const baseCfg = "0000000000000000000000000000000000000000000000000000000000000000"

// fixture is one proposal to verify + how to present it. The verdict is produced by the real gate.
type fixture struct {
	proposal verification.Proposal
	pres     proposal.Presentation
	// status is the candidate's real proposal.BuildStatus. It drives the surface through api.CardFor,
	// so this demo cannot disagree with production about what a status means — which is exactly how a
	// third status (P14's `refused`) would otherwise have been rendered as a verified zero delta here.
	status   proposal.BuildStatus
	baseSucc map[string]float64
	// perCaseSuccess for baseline and candidate, so the stubbed runner returns reproducible deltas.
	candSucc          map[string]float64
	candCost, candLat float64
	// buildLog is present only for a build_failed candidate.
	buildLog string
}

// verified reports whether this fixture reaches the verification gate at all. A candidate that never
// built, or that the transform refused, has nothing to run.
func (f fixture) verified() bool { return f.status == proposal.BuildBuilt }

func succ(v float64, over ...map[string]float64) map[string]float64 {
	m := map[string]float64{}
	for _, id := range evalSet {
		m[id] = v
	}
	for _, o := range over {
		for k, val := range o {
			m[k] = val
		}
	}
	return m
}

func diff(node, from, to string) string {
	return "--- a/pipeline.go\n+++ b/pipeline.go\n@@ node " + node + " @@\n-\t" + from + "\n+\t" + to + "\n"
}

func fixtures() []fixture {
	return []fixture{
		// 1. known-good model upgrade — real, generalizing, held-out gain.
		{
			proposal: verification.Proposal{ProposalID: "p-upgrade", CandidateConfigHash: hash("upgrade"),
				BaselineConfigHash: baseCfg, SourceRevision: "rev1", DiffHash: "d1", GeneratingCaseIDs: []string{"g1", "g2", "g3"}},
			pres: proposal.Presentation{Operator: proposal.OpModelUpgrade, NodeID: "router", Pattern: "routing",
				DiagID: "diag-router", EvidenceCaseIDs: []string{"g1", "g2", "g3"},
				Rationale: "capability gap on 3 case(s) → stronger router model", SourceDiff: diff("router", `Model: "haiku"`, `Model: "sonnet-5"`),
				SpecDiff: []proposal.DimChange{{NodeID: "router", Dimension: "model", From: "haiku", To: "sonnet-5"}}},
			status: proposal.BuildBuilt, baseSucc: succ(0.3), candSucc: succ(0.95), candCost: 0.012, candLat: 620,
		},
		// 2. RAG add-rerank — real gain but NO held-out split (all cases generated it) → not-held-out.
		{
			proposal: verification.Proposal{ProposalID: "p-rerank", CandidateConfigHash: hash("rerank"),
				BaselineConfigHash: baseCfg, SourceRevision: "rev1", DiffHash: "d2", GeneratingCaseIDs: evalSet},
			pres: proposal.Presentation{Operator: proposal.OpAddRerank, NodeID: "rag", Pattern: "retrieval_rag",
				DiagID: "diag-rag", EvidenceCaseIDs: []string{"h4", "h5"},
				Rationale: "retrieval miss → add rerank skill cohere-rerank", SourceDiff: diff("rag", "skills: [retriever]", "skills: [retriever, rerank]"),
				SpecDiff: []proposal.DimChange{{NodeID: "rag", Dimension: "skills", From: "1 skill(s)", To: "2 skill(s)"}}},
			status: proposal.BuildBuilt, baseSucc: succ(0.4), candSucc: succ(0.85), candCost: 0.011, candLat: 540,
		},
		// 3. prompt rewrite — fixes cluster A but breaks cluster B → gate_failed (regression), cases
		//    fixed AND broken side by side.
		{
			proposal: verification.Proposal{ProposalID: "p-prompt", CandidateConfigHash: hash("prompt"),
				BaselineConfigHash: baseCfg, SourceRevision: "rev1", DiffHash: "d3", GeneratingCaseIDs: []string{"g1", "g2", "g3"},
				TargetClusterID: "A", Clusters: []attribution.FailureCluster{
					{ClusterID: "A", Label: "output-format", MemberCaseIDs: []string{"h1", "h2", "h3", "h4", "h5", "h6"}},
					{ClusterID: "B", Label: "tool-errors", MemberCaseIDs: []string{"b1", "b2", "b3", "b4"}}}},
			pres: proposal.Presentation{Operator: proposal.OpPromptRewrite, NodeID: "answer", Pattern: "prompt_chaining",
				DiagID: "diag-answer", EvidenceCaseIDs: []string{"g1", "g2", "g3"},
				Rationale:  "output-contract violation → grounded prompt rewrite + format constraint",
				SourceDiff: diff("answer", `prompt: "Answer."`, `prompt: "Answer. Return JSON {label}."`),
				SpecDiff:   []proposal.DimChange{{NodeID: "answer", Dimension: "prompt", From: "prompt://a", To: "prompt://b"}}},
			status:   proposal.BuildBuilt,
			baseSucc: succ(0.3, map[string]float64{"b1": 0.9, "b2": 0.9, "b3": 0.9, "b4": 0.9}),
			candSucc: succ(0.95, map[string]float64{"b1": 0.1, "b2": 0.1, "b3": 0.1, "b4": 0.1}),
			candCost: 0.011, candLat: 520,
		},
		// 4. model downgrade — a noise proposal (true-zero held-out delta) → gate_failed (tie).
		{
			proposal: verification.Proposal{ProposalID: "p-downgrade", CandidateConfigHash: hash("downgrade"),
				BaselineConfigHash: baseCfg, SourceRevision: "rev1", DiffHash: "d4", GeneratingCaseIDs: []string{"g1", "g2", "g3"}},
			pres: proposal.Presentation{Operator: proposal.OpModelDowngrade, NodeID: "summarize", Pattern: "prompt_chaining",
				DiagID: "diag-sum", EvidenceCaseIDs: []string{"g1"},
				Rationale: "cost bottleneck → downgrade to cheaper model", SourceDiff: diff("summarize", `Model: "opus"`, `Model: "haiku"`),
				SpecDiff: []proposal.DimChange{{NodeID: "summarize", Dimension: "model", From: "opus", To: "haiku"}}},
			status: proposal.BuildBuilt, baseSucc: succ(0.5), candSucc: succ(0.5), candCost: 0.004, candLat: 300,
		},
		// 5. context-policy switch whose codemod does not build → build_failed (never verified).
		{
			pres: proposal.Presentation{Operator: proposal.OpContextPolicy, NodeID: "planner", Pattern: "planning",
				DiagID: "diag-plan", EvidenceCaseIDs: []string{"h6"}, ConfigHash: hash("ctxbad"),
				Rationale: "context overflow → switch to summarization", SourceDiff: diff("planner", "ctx: window", "ctx: summariz(") + " // syntax error",
				SpecDiff: []proposal.DimChange{{NodeID: "planner", Dimension: "context", From: "window", To: "summarization"}}},
			status: proposal.BuildFailed, buildLog: "pipeline.go:42: syntax error: unexpected (",
		},
		// 6. P14 skill binding the TRANSFORM REFUSED — the third status, and the one this demo exists to
		//    keep honest. It never built, never ran, and has no verdict; the surface must say so by name
		//    rather than render a zero delta for a change nobody made (decisions.md D-14.3, task 8.2).
		{
			pres: proposal.Presentation{Operator: proposal.OpAddSkill, NodeID: "agent", Pattern: "tool_use",
				DiagID: "diag-agent", EvidenceCaseIDs: []string{"h1", "h2"}, ConfigHash: hash("refusedskill"),
				Rationale: "missing/erroring tool → bind skill search_kb from the registry",
				SpecDiff: []proposal.DimChange{{NodeID: "agent", Dimension: "skills",
					From: "0 skill(s)", To: "1 skill(s)", Kind: proposal.KindSkillBind, Items: []string{"search_kb@99"}}},
				Refusal: &proposal.ChangeRefusal{NodeID: "agent", Dimension: "skills",
					Reason: "binding skills [search_kb] requires constructing SDK-specific tool values at the " +
						"call site, and no materializer for this language has landed yet (Go is the first — " +
						"decisions.md D-14.4); this engine only replaces value expressions here, so the binding " +
						"is REFUSED rather than dropped"}},
			status: proposal.BuildRefused,
		},
		// 7. P14 tool prune that DID build and verified — the counterpart, so the surface shows a tool
		//    selection next to a skill binding and the two do not read as one change.
		{
			proposal: verification.Proposal{ProposalID: "p-toolprune", CandidateConfigHash: hash("toolprune"),
				BaselineConfigHash: baseCfg, SourceRevision: "rev1", DiffHash: "d7", GeneratingCaseIDs: []string{"g1"}},
			pres: proposal.Presentation{Operator: proposal.OpToolPrune, NodeID: "agent", Pattern: "tool_use",
				EvidenceCaseIDs: []string{"g1"},
				Rationale:       "tool sqlTool is declared but the eval set never calls it → prune it",
				SourceDiff: diff("agent", "Tools: []ToolUnionParam{weatherTool, sqlTool, searchTool}",
					"Tools: []ToolUnionParam{weatherTool, searchTool}"),
				SpecDiff: []proposal.DimChange{{NodeID: "agent", Dimension: "tools",
					From: "3 tool(s): searchTool, sqlTool, weatherTool", To: "2 tool(s): searchTool, weatherTool",
					Kind: proposal.KindToolPrune, Items: []string{"sqlTool"}}}},
			status: proposal.BuildBuilt, baseSucc: succ(0.5), candSucc: succ(0.72), candCost: 0.008, candLat: 460,
		},
		// 8. P14 skill binding that DID materialize and verify. It is the counterpart to #6: the same
		//    operator, on a call site whose (language, provider) pair HAS a materializer, so the diff is a
		//    real construction from the skill's sealed input schema rather than a refusal. Both are kept,
		//    because "this axis applies" and "this axis is refused here" are the two things a reader of the
		//    skill surface has to be able to tell apart.
		{
			proposal: verification.Proposal{ProposalID: "p-bindskill", CandidateConfigHash: hash("bindskill"),
				BaselineConfigHash: baseCfg, SourceRevision: "rev1", DiffHash: "d8", GeneratingCaseIDs: []string{"g1", "g2"}},
			pres: proposal.Presentation{Operator: proposal.OpAddSkill, NodeID: "agent", Pattern: "tool_use",
				DiagID: "diag-agent", EvidenceCaseIDs: []string{"g1", "g2"},
				Rationale: "missing/erroring tool on 2 case(s) → bind skill search_kb from the registry",
				SourceDiff: diff("agent", "anthropic.MessageNewParams{}",
					`anthropic.MessageNewParams{Tools: []anthropic.ToolUnionParam{{OfTool: &anthropic.ToolParam{Name: "search_kb", `+
						`InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{"query": map[string]any{"type": "string"}}}}}}}`),
				SpecDiff: []proposal.DimChange{{NodeID: "agent", Dimension: "skills",
					From: "0 skill(s)", To: "1 skill(s)", Kind: proposal.KindSkillBind, Items: []string{"search_kb@99"}}}},
			status: proposal.BuildBuilt, baseSucc: succ(0.45), candSucc: succ(0.88), candCost: 0.013, candLat: 700,
		},
	}
}

func hash(seed string) string {
	h := ""
	for len(h) < 64 {
		h += fmt.Sprintf("%02x", seed[len(h)/2%len(seed)])
	}
	return h[:64]
}

// runData is the canned outcome for one config (baseline or candidate).
type runData struct {
	succ map[string]float64
	cost float64
	lat  float64
}

// stubRunner is the ONLY stub: it returns each config's canned per-case success/cost/latency, keyed by
// config hash, so the real gate computes a reproducible, per-proposal verdict.
type stubRunner struct{ byConfig map[string]runData }

func (s stubRunner) Run(_ context.Context, req verification.RunRequest) (verification.RunResult, error) {
	d, ok := s.byConfig[req.ConfigHash]
	if !ok {
		d = runData{succ: map[string]float64{}, cost: 0.01, lat: 500}
	}
	return verification.RunResult{
		Quality: series(d.succ, req),
		Cost:    constSeries(d.cost, req),
		Latency: constSeries(d.lat, req),
	}, nil
}

func series(succ map[string]float64, req verification.RunRequest) evalstats.Series {
	var s evalstats.Series
	for _, id := range req.CaseIDs {
		v := succ[id]
		for _, seed := range req.Seeds {
			s.Obs = append(s.Obs, evalstats.Observation{CaseID: id, Seed: seed, Value: v})
		}
	}
	return s
}

func constSeries(val float64, req verification.RunRequest) evalstats.Series {
	var s evalstats.Series
	for _, id := range req.CaseIDs {
		for _, seed := range req.Seeds {
			s.Obs = append(s.Obs, evalstats.Observation{CaseID: id, Seed: seed, Value: val})
		}
	}
	return s
}

// source is the ProposalsSource: it runs the real gate over the fixtures once and serves the assembled surface.
type source struct {
	surface api.Surface
}

func (s source) Surface(string) (api.Surface, bool) { return s.surface, true }

func (s source) OpenPR(_, proposalID string) (api.PRResult, error) {
	return api.PRResult{ProposalID: proposalID, Branch: "optimizer/" + proposalID,
		URL: "(local) git show optimizer/" + proposalID, Draft: false,
		Rollback: "git revert <merge-commit>"}, nil
}

func build(level verification.AutomationLevel) source {
	fx := fixtures()
	byConfig := map[string]runData{}
	for i := range fx {
		f := &fx[i]
		if !f.verified() {
			continue
		}
		// Give each proposal its own baseline config so per-proposal baselines never collide.
		f.proposal.BaselineConfigHash = hash("base-" + f.proposal.ProposalID)
		byConfig[f.proposal.CandidateConfigHash] = runData{succ: f.candSucc, cost: f.candCost, lat: f.candLat}
		byConfig[f.proposal.BaselineConfigHash] = runData{succ: f.baseSucc, cost: 0.01, lat: 500}
	}
	runner := stubRunner{byConfig: byConfig}

	var recs, withheld []api.Card
	for _, f := range fx {
		var v verification.Verdict
		if f.verified() {
			got, err := verification.Verify(context.Background(), runner, f.proposal, evalSet, verification.DefaultConfig())
			if err != nil {
				log.Fatalf("verify %s: %v", f.proposal.ProposalID, err)
			}
			v = got
		}
		// ONE routing decision, shared with production: the status picks the card, and both halves
		// (it built AND its verdict passed) pick the list.
		card := api.CardFor(f.pres, f.status, f.buildLog, v, level)
		if api.Recommendable(f.status, v) {
			recs = append(recs, card)
		} else {
			withheld = append(withheld, card)
		}
	}
	// Rank recommendations by verified delta (descending).
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].Delta > recs[j].Delta })

	trend := verification.BuildTrend([]verification.TrendPoint{
		{VariantID: "v1", Iteration: 1, OverallSuccess: 0.60, ClusterSizes: map[string]int{"A": 5, "B": 1}},
		{VariantID: "v2", Iteration: 2, OverallSuccess: 0.61, ClusterSizes: map[string]int{"A": 3, "B": 3}},
		{VariantID: "v3", Iteration: 3, OverallSuccess: 0.60, ClusterSizes: map[string]int{"A": 1, "B": 5}},
	})

	state := "ready"
	if len(recs) == 0 {
		state = "empty"
	}
	return source{surface: api.Surface{
		WorkflowID: "demo-workflow", AutomationLevel: string(level), State: state,
		Recommendations: recs, Withheld: withheld, Trend: trend,
	}}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8486", "listen address")
	level := flag.String("level", "assisted", "automation level: advisory | assisted")
	flag.Parse()

	s := api.New(nil, config.Config{})
	s.MountProposals(build(verification.AutomationLevel(*level)))

	fmt.Printf("P5.5 recommendations:  http://%s/recommendations?workflow=demo-workflow\n", *addr)
	fmt.Printf("surface JSON:          http://%s/api/v1/workflows/demo-workflow/proposals\n", *addr)
	log.Fatal(http.ListenAndServe(*addr, s.Handler))
}
