package verification

import (
	"context"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/evalstats"
)

// Proposal is the verification input: the candidate and baseline configs to compare, the cases the
// diagnosis was generated from (excluded from the held-out split), and the failure-cluster structure
// so the regression check can re-score the OTHER clusters.
type Proposal struct {
	ProposalID          string
	CandidateConfigHash string
	BaselineConfigHash  string
	SourceRevision      string
	DiffHash            string
	// GeneratingCaseIDs are the cases that produced the diagnosis. The held-out split is the eval set
	// minus these (design Decision 5).
	GeneratingCaseIDs []string
	// Clusters is every failure cluster; the regression check re-scores each cluster other than
	// TargetClusterID.
	Clusters        []attribution.FailureCluster
	TargetClusterID string
}

// Budget is the regression check's hard cost/latency gate plus the other-cluster degradation
// threshold (design Decision 7). A zero cost/latency field is unset (no ceiling); the cluster
// threshold defaults via DefaultBudget.
type Budget struct {
	// MaxCostUSD is a HARD ceiling on the candidate's cost per run — "fixed accuracy, tripled cost"
	// fails deterministically here (§5.3), not as a soft penalty.
	MaxCostUSD float64
	// MaxLatencyMS is the hard latency ceiling per run.
	MaxLatencyMS float64
	// ClusterDegradeThreshold is the largest allowed drop in another cluster's success rate before the
	// proposal fails the regression check.
	ClusterDegradeThreshold float64
}

// Config governs a verification run.
type Config struct {
	Seeds  []int64
	Stats  evalstats.Config
	Budget Budget
	// ConstraintViolated, when non-empty, names a hard constraint the proposal breaches (e.g. a
	// provider-allowlist violation surfaced from ranking). It short-circuits to fail_constraint.
	ConstraintViolated string
}

// DefaultConfig is a sane multi-seed configuration.
func DefaultConfig() Config {
	return Config{
		Seeds:  []int64{1, 2, 3, 4, 5},
		Stats:  evalstats.DefaultConfig(),
		Budget: Budget{ClusterDegradeThreshold: 0.10},
	}
}

// Verify auto-executes the proposal's transformed working copy through the harness on the held-out
// split, runs the significance gate (evalstats.Compare) and the regression check, and emits the
// verdict. The returned verdict is the source of truth; Passed() is the ONLY predicate the surface
// reads (§4.5).
func Verify(ctx context.Context, runner EvalRunner, p Proposal, evalSetCaseIDs []string, cfg Config) (Verdict, error) {
	if runner == nil {
		return Verdict{}, fmt.Errorf("verification: Verify requires an EvalRunner")
	}
	v := Verdict{
		ProposalID: p.ProposalID, ConfigHash: p.CandidateConfigHash, DiffHash: p.DiffHash,
		Metric: metricQuality,
	}

	// 1. Form the held-out split: eval set minus generating cases (design Decision 5). No held-out
	//    slice → run the full set and flag the verdict not-held-out.
	held := subtract(evalSetCaseIDs, p.GeneratingCaseIDs)
	split := SplitHeldOut
	cases := held
	v.HeldOut = true
	if len(held) == 0 {
		split, cases, v.HeldOut = SplitFull, append([]string(nil), evalSetCaseIDs...), false
	}

	base, err := runner.Run(ctx, RunRequest{ConfigHash: p.BaselineConfigHash, SourceRevision: p.SourceRevision, CaseIDs: cases, Split: split, Seeds: cfg.Seeds})
	if err != nil {
		return Verdict{}, fmt.Errorf("verification: baseline run: %w", err)
	}
	cand, err := runner.Run(ctx, RunRequest{ConfigHash: p.CandidateConfigHash, SourceRevision: p.SourceRevision, CaseIDs: cases, Split: split, Seeds: cfg.Seeds})
	if err != nil {
		return Verdict{}, fmt.Errorf("verification: candidate run: %w", err)
	}

	// 2. Significance gate on the held-out quality delta (candidate=a, baseline=b).
	cmp, err := evalstats.Compare(cand.Quality, base.Quality, evalstats.HigherIsBetter, cfg.Stats)
	if err != nil {
		return Verdict{}, fmt.Errorf("verification: significance test: %w", err)
	}
	v.Delta = cmp.Delta
	realGain := cmp.Significant && cmp.Verdict == evalstats.VerdictAWins
	v.Significant = realGain

	// 3. Cost / latency impact and cases fixed/broken on the split.
	v.CostDelta = cand.Cost.Mean() - base.Cost.Mean()
	v.LatencyDelta = cand.Latency.Mean() - base.Latency.Mean()
	fixed, broken := fixedAndBroken(base.Quality, cand.Quality)
	// SetCases, not two assignments: the counts are what every consumer reads, and a verdict carrying
	// ids with a zero count reports that the change fixed nothing.
	v.SetCases(fixed, broken)

	// 4. Gate precedence: constraint → significance → regression → pass.
	if cfg.ConstraintViolated != "" {
		v.GateResult, v.Reason = GateFailConstrain, "violates hard constraint: "+cfg.ConstraintViolated
		return v, nil
	}
	if !realGain {
		v.GateResult = GateFailSig
		v.Reason = "held-out quality gain is not statistically significant: " + cmp.Reason
		if !v.HeldOut {
			v.Reason += " (not held-out)"
		}
		return v, nil
	}

	// 5. Regression check: hard cost/latency budget + other-cluster re-scoring.
	reg := regressionCheck(ctx, runner, p, cand, cfg)
	v.RegressionPass = reg.pass
	v.SetCases(v.CasesFixed, mergeSorted(v.CasesBroken, reg.brokenCases))
	if !reg.pass {
		v.GateResult, v.Reason = GateFailRegress, reg.reason
		return v, nil
	}

	v.GateResult = GatePass
	v.Reason = fmt.Sprintf("held-out gain +%.3f [%.3f, %.3f], regression-clean", v.Delta.Mean, v.Delta.Low, v.Delta.High)
	if !v.HeldOut {
		v.Reason += " (not held-out)"
	}
	return v, nil
}

// fixedAndBroken compares per-case success between baseline and candidate over the split. A case is
// fixed when it went failing→passing, broken when passing→failing. Success is a per-case mean ≥ 0.5
// over the seeds.
func fixedAndBroken(baseline, candidate evalstats.Series) (fixed, broken []string) {
	b := caseSuccess(baseline)
	c := caseSuccess(candidate)
	for id, bs := range b {
		cs, ok := c[id]
		if !ok {
			continue
		}
		switch {
		case bs < 0.5 && cs >= 0.5:
			fixed = append(fixed, id)
		case bs >= 0.5 && cs < 0.5:
			broken = append(broken, id)
		}
	}
	sort.Strings(fixed)
	sort.Strings(broken)
	return fixed, broken
}

// caseSuccess reduces a Series to a per-case mean success value.
func caseSuccess(s evalstats.Series) map[string]float64 {
	sum := map[string]float64{}
	n := map[string]int{}
	for _, o := range s.Obs {
		sum[o.CaseID] += o.Value
		n[o.CaseID]++
	}
	out := make(map[string]float64, len(sum))
	for id, tot := range sum {
		out[id] = tot / float64(n[id])
	}
	return out
}

func subtract(all, remove []string) []string {
	rm := map[string]bool{}
	for _, r := range remove {
		rm[r] = true
	}
	var out []string
	for _, a := range all {
		if !rm[a] {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

func mergeSorted(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string(nil), a...), b...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
