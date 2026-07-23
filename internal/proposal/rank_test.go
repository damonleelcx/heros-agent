package proposal

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// rankMenu has two upgrade targets of equal capability but different cost, plus a budget-blowing and an
// off-allowlist model, so the ranker's ordering and exclusion can be exercised.
func rankMenu() Menu {
	return Menu{Models: []ModelChoice{
		{Ref: refWeakModel, Provider: "anthropic", ModelID: "haiku", Tier: 1, CostPerRun: 0.001, LatencyMS: 200},
		{Ref: refStrongModel, Provider: "anthropic", ModelID: "sonnet", Tier: 3, CostPerRun: 0.010, LatencyMS: 800},
		{Ref: refCheapModel, Provider: "anthropic", ModelID: "sonnet-cheap", Tier: 3, CostPerRun: 0.005, LatencyMS: 600},
		{Ref: refThinkModel, Provider: "openai", ModelID: "o-pricey", Tier: 4, CostPerRun: 0.200, LatencyMS: 3000},
	}}
}

func builtCandidate(op OperatorKind, node, modelRef, configHash string, gain float64) Compiled {
	spec := &variantspec.VariantSpec{WorkflowID: "wf1", SourceRevision: "rev1",
		Order: []string{node}, Nodes: map[string]variantspec.NodeOverride{node: {ModelRef: modelRef}}}
	return Compiled{
		Candidate: Candidate{Operator: op, NodeID: node, DiagID: "d-" + node, Spec: spec,
			ExpectedGain: gain, EvidenceCaseIDs: []string{"c1"}, Rationale: "r"},
		ConfigHash:  configHash,
		BuildStatus: BuildBuilt,
		Patch:       &transform.Patch{Diff: []byte("--- a\n+++ b\n"), DiffHash: configHash},
	}
}

// §3.4: two admissible candidates with EQUAL expected gain but different cost-of-change → the cheaper
// one ranks ahead.
func TestRank_OrdersByGainPerCost(t *testing.T) {
	base := &variantspec.VariantSpec{Nodes: map[string]variantspec.NodeOverride{"n": {ModelRef: refWeakModel}}}
	cheaper := builtCandidate(OpModelUpgrade, "n", refCheapModel, "aaaa", 0.30)
	pricier := builtCandidate(OpModelUpgrade, "n", refStrongModel, "bbbb", 0.30)

	res := Rank([]Compiled{pricier, cheaper}, base, rankMenu(), Constraints{})
	if len(res.Ranked) != 2 {
		t.Fatalf("want 2 ranked, got %d", len(res.Ranked))
	}
	if res.Ranked[0].Compiled.ConfigHash != "aaaa" {
		t.Errorf("the cheaper equal-gain candidate must rank first, got %q", res.Ranked[0].Compiled.ConfigHash)
	}
}

// §3.2 / §3.4: a budget-violating candidate is constraint-excluded (not ranked #1); a cheaper
// admissible candidate for the same target ranks ahead of it.
func TestRank_BudgetViolatorExcluded(t *testing.T) {
	base := &variantspec.VariantSpec{Nodes: map[string]variantspec.NodeOverride{"n": {ModelRef: refWeakModel}}}
	pricey := builtCandidate(OpModelUpgrade, "n", refThinkModel, "pricey", 0.9) // huge gain but $0.20/run
	cheap := builtCandidate(OpModelUpgrade, "n", refCheapModel, "cheap", 0.2)

	res := Rank([]Compiled{pricey, cheap}, base, rankMenu(), Constraints{BudgetCeilingUSD: 0.05})

	for _, r := range res.Ranked {
		if r.Compiled.ConfigHash == "pricey" {
			t.Fatal("a budget-violating candidate was ranked as a recommendation")
		}
	}
	if len(res.Ranked) == 0 || res.Ranked[0].Compiled.ConfigHash != "cheap" {
		t.Fatalf("the cheap admissible candidate must be the top recommendation, got %+v", res.Ranked)
	}
	if len(res.Excluded) != 1 || res.Excluded[0].ViolatedConstraint != "budget ceiling" {
		t.Fatalf("the excluded candidate must name the budget ceiling, got %+v", res.Excluded)
	}
}

// §3.2: provider-allowlist and latency-SLA violations are also excluded with the constraint named.
func TestRank_ProviderAndLatencyExclusion(t *testing.T) {
	base := &variantspec.VariantSpec{Nodes: map[string]variantspec.NodeOverride{"n": {ModelRef: refWeakModel}}}
	offlist := builtCandidate(OpModelUpgrade, "n", refThinkModel, "off", 0.5) // provider "openai", latency 3000
	ok := builtCandidate(OpModelUpgrade, "n", refStrongModel, "ok", 0.4)

	res := Rank([]Compiled{offlist, ok}, base, rankMenu(), Constraints{ProviderAllowlist: []string{"anthropic"}})
	if len(res.Excluded) != 1 || res.Excluded[0].ViolatedConstraint != "provider allowlist" {
		t.Fatalf("off-allowlist candidate must be excluded naming the provider allowlist, got %+v", res.Excluded)
	}

	res2 := Rank([]Compiled{offlist, ok}, base, rankMenu(), Constraints{LatencySLAms: 1000})
	if len(res2.Excluded) != 1 || res2.Excluded[0].ViolatedConstraint != "latency SLA" {
		t.Fatalf("slow candidate must be excluded naming the latency SLA, got %+v", res2.Excluded)
	}
}

// §1b.2 reinforced at the ranker: a build-failed candidate is never ranked or excluded — it is dropped.
func TestRank_DropsBuildFailed(t *testing.T) {
	base := &variantspec.VariantSpec{Nodes: map[string]variantspec.NodeOverride{"n": {ModelRef: refWeakModel}}}
	failed := builtCandidate(OpModelUpgrade, "n", refStrongModel, "failed", 0.5)
	failed.BuildStatus = BuildFailed
	res := Rank([]Compiled{failed}, base, rankMenu(), Constraints{})
	if len(res.Ranked) != 0 || len(res.Excluded) != 0 {
		t.Fatalf("a build-failed candidate must be dropped, got ranked=%d excluded=%d", len(res.Ranked), len(res.Excluded))
	}
}

// §3.3: each ranked candidate is presented as a reviewable source diff paired with the Variant-Spec
// diff, with the diagnosis and failing cases attached as evidence.
func TestRank_PresentationCarriesDiffAndEvidence(t *testing.T) {
	base := &variantspec.VariantSpec{Nodes: map[string]variantspec.NodeOverride{"n": {ModelRef: refWeakModel}}}
	c := builtCandidate(OpModelUpgrade, "n", refStrongModel, "cfg", 0.5)
	res := Rank([]Compiled{c}, base, rankMenu(), Constraints{})
	p := res.Ranked[0].Presentation
	if p.SourceDiff == "" {
		t.Error("presentation is missing the reviewable source diff")
	}
	if len(p.SpecDiff) == 0 || p.SpecDiff[0].Dimension != "model" {
		t.Errorf("presentation is missing the Variant-Spec (model) diff: %+v", p.SpecDiff)
	}
	if p.DiagID == "" || len(p.EvidenceCaseIDs) == 0 {
		t.Error("presentation is missing the diagnosis / failing-case evidence")
	}
}
