package verification

import (
	"context"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalrun"
)

// batchRunner returns a real gain for every candidate config, so every Verify passes and the fan-out's
// ordering / cap / idempotency are what the tests isolate.
func batchRunner(configs []string) fakeRunner {
	succ := map[string]map[string]float64{baseCfg: succMap(evalSet, 0.3)}
	cost := map[string]float64{baseCfg: 0.01}
	lat := map[string]float64{baseCfg: 500}
	for _, c := range configs {
		succ[c] = succMap(evalSet, 0.9)
		cost[c] = 0.01
		lat[c] = 500
	}
	return fakeRunner{success: succ, cost: cost, latency: lat}
}

func jobFor(id, cfg string, hint int, cost float64) Job {
	p := baseProposal()
	p.ProposalID = id
	p.CandidateConfigHash = cfg
	return Job{Proposal: p, EvalSetCaseIDs: evalSet, OrderHint: hint, ExpectedCostUSD: cost}
}

// §5.1: proposals are verified cheapest-operator-first (by OrderHint).
func TestBatchVerify_CheapestFirst(t *testing.T) {
	r := batchRunner([]string{"cfgA", "cfgB", "cfgC"})
	jobs := []Job{
		jobFor("pricey", "cfgA", 5, 0.01),
		jobFor("cheap", "cfgB", 0, 0.01),
		jobFor("mid", "cfgC", 3, 0.01),
	}
	res, err := BatchVerify(context.Background(), r, "batch1", jobs, DefaultConfig(), evalrun.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	order := []string{res.Verdicts[0].ProposalID, res.Verdicts[1].ProposalID, res.Verdicts[2].ProposalID}
	want := []string{"cheap", "mid", "pricey"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("verification order = %v, want cheapest-first %v", order, want)
		}
	}
}

// §5.1: a re-verified config_hash reuses its verdict and does NOT double-charge (idempotent re-delivery).
func TestBatchVerify_IdempotentNoDoubleCharge(t *testing.T) {
	r := batchRunner([]string{"cfgDup"})
	jobs := []Job{
		jobFor("first", "cfgDup", 0, 0.02),
		jobFor("again", "cfgDup", 0, 0.02), // same config_hash re-delivered
	}
	res, err := BatchVerify(context.Background(), r, "batch2", jobs, DefaultConfig(), evalrun.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Verdicts) != 2 {
		t.Fatalf("both deliveries must yield a verdict, got %d", len(res.Verdicts))
	}
	if res.Spend.TotalUSD != 0.02 {
		t.Errorf("a re-delivered config_hash must be charged once, got total $%.4f", res.Spend.TotalUSD)
	}
}

// §5.2: the per-batch spend cap stops proposals before they blow the budget, and the skipped ones are
// SURFACED (never silently dropped).
func TestBatchVerify_SpendCapSurfacesSkipped(t *testing.T) {
	r := batchRunner([]string{"c1", "c2", "c3"})
	cap := 0.05
	jobs := []Job{
		jobFor("j1", "c1", 0, 0.03),
		jobFor("j2", "c2", 0, 0.03), // 0.06 total > 0.05 cap → skipped
		jobFor("j3", "c3", 0, 0.03), // skipped
	}
	res, err := BatchVerify(context.Background(), r, "batch3", jobs, DefaultConfig(), evalrun.Budget{TotalUSD: &cap})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Verdicts) != 1 || res.Verdicts[0].ProposalID != "j1" {
		t.Fatalf("only the first job fits the cap, got %d verdicts", len(res.Verdicts))
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("the two over-budget jobs must be surfaced as skipped, got %v", res.Skipped)
	}
	if res.Spend.TotalUSD != 0.03 {
		t.Errorf("only the run job may be charged, got $%.4f", res.Spend.TotalUSD)
	}
}
