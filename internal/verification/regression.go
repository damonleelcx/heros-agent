package verification

import (
	"context"
	"fmt"
	"sort"
)

// regressionOutcome is the internal result of the regression check.
type regressionOutcome struct {
	pass        bool
	reason      string
	brokenCases []string
}

// regressionCheck enforces the two halves of design Decision 7: (a) a HARD cost/latency budget on the
// candidate's own run, and (b) re-scoring every OTHER failure cluster to confirm none degrades beyond
// the threshold. Either breach fails the check deterministically.
//
// candOnSplit is the candidate's already-computed run on the held-out split, used for the budget
// check; the cluster re-scores are additional runs (their spend is why §5 caps verification spend).
func regressionCheck(ctx context.Context, runner EvalRunner, p Proposal, candOnSplit RunResult, cfg Config) regressionOutcome {
	// (a) hard cost/latency budget on the candidate.
	candCost := candOnSplit.Cost.Mean()
	candLatency := candOnSplit.Latency.Mean()
	if cfg.Budget.MaxCostUSD > 0 && candCost > cfg.Budget.MaxCostUSD {
		return regressionOutcome{pass: false,
			reason: fmt.Sprintf("cost budget breached: $%.4f/run > $%.4f ceiling", candCost, cfg.Budget.MaxCostUSD)}
	}
	if cfg.Budget.MaxLatencyMS > 0 && candLatency > cfg.Budget.MaxLatencyMS {
		return regressionOutcome{pass: false,
			reason: fmt.Sprintf("latency budget breached: %.0fms/run > %.0fms ceiling", candLatency, cfg.Budget.MaxLatencyMS)}
	}

	// (b) re-score every other cluster; confirm none degrades beyond the threshold.
	threshold := cfg.Budget.ClusterDegradeThreshold
	if threshold <= 0 {
		threshold = 0.10
	}
	var allBroken []string
	for _, cl := range p.Clusters {
		if cl.ClusterID == p.TargetClusterID || len(cl.MemberCaseIDs) == 0 {
			continue
		}
		base, err := runner.Run(ctx, RunRequest{ConfigHash: p.BaselineConfigHash, SourceRevision: p.SourceRevision,
			CaseIDs: cl.MemberCaseIDs, Split: SplitFull, Seeds: cfg.Seeds})
		if err != nil {
			return regressionOutcome{pass: false, reason: "regression re-score failed: " + err.Error()}
		}
		cand, err := runner.Run(ctx, RunRequest{ConfigHash: p.CandidateConfigHash, SourceRevision: p.SourceRevision,
			CaseIDs: cl.MemberCaseIDs, Split: SplitFull, Seeds: cfg.Seeds})
		if err != nil {
			return regressionOutcome{pass: false, reason: "regression re-score failed: " + err.Error()}
		}
		baseSucc := meanSuccess(base)
		candSucc := meanSuccess(cand)
		_, broken := fixedAndBroken(base.Quality, cand.Quality)
		allBroken = append(allBroken, broken...)
		if baseSucc-candSucc > threshold {
			sort.Strings(allBroken)
			return regressionOutcome{pass: false, brokenCases: dedupe(allBroken),
				reason: fmt.Sprintf("cluster %s degraded %.3f→%.3f (> %.2f threshold)", cl.Label, baseSucc, candSucc, threshold)}
		}
	}
	return regressionOutcome{pass: true, brokenCases: dedupe(allBroken)}
}

// meanSuccess is the overall success rate of a run (mean of the task_success series).
func meanSuccess(r RunResult) float64 { return r.Quality.Mean() }

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
