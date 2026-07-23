package verification

import (
	"context"
	"sort"

	"github.com/heros-foreal/agentd/internal/evalrun"
)

// Job is one proposal to verify, plus the ordering + spend hints the fan-out needs. OrderHint encodes
// cheapest-operator-first (design 5.1): a lower hint verifies earlier, so a downgrade / prune is
// proven before a multi-candidate prompt sweep. ExpectedCostUSD is the pre-verification spend estimate
// the batch cap is enforced against.
type Job struct {
	Proposal        Proposal
	EvalSetCaseIDs  []string
	OrderHint       int
	ExpectedCostUSD float64
}

// BatchResult is the outcome of verifying a batch: the verdicts (in verification order), the surfaced
// spend report, and the config_hashes skipped because the spend cap would have been breached.
type BatchResult struct {
	Verdicts []Verdict           `json:"verdicts"`
	Spend    evalrun.SpendReport `json:"spend"`
	// Skipped names candidates the cap stopped before they ran — surfaced, never silently dropped
	// (§5.2: proving proposals must not silently blow a budget).
	Skipped []string `json:"skipped"`
}

// BatchVerify verifies a batch of proposals cheapest-operator-first under a hard spend cap, and is
// idempotent on config_hash: a config already verified in this batch reuses its verdict and is NOT
// charged again (design 5.1 — a re-verified config_hash does not double-charge). The per-batch cap
// (design 5.2) is the reused P4 evalrun.Meter, so a proving run cannot silently blow the budget.
//
// The eval fan-out for a single Verify (multi-seed, bounded concurrency, backpressure, idempotent
// re-delivery) lives in the EvalRunner's P2-run-queue implementation; BatchVerify owns the
// proposal-level ordering, cap, and dedup.
func BatchVerify(ctx context.Context, runner EvalRunner, batchID string, jobs []Job, cfg Config, budget evalrun.Budget) (BatchResult, error) {
	ordered := append([]Job(nil), jobs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].OrderHint != ordered[j].OrderHint {
			return ordered[i].OrderHint < ordered[j].OrderHint // cheapest operator first
		}
		return ordered[i].Proposal.CandidateConfigHash < ordered[j].Proposal.CandidateConfigHash
	})

	meter := evalrun.NewMeter(batchID, budget)
	seen := map[string]Verdict{} // config_hash -> verdict (idempotent re-delivery)
	var res BatchResult

	for _, job := range ordered {
		ch := job.Proposal.CandidateConfigHash
		if v, ok := seen[ch]; ok {
			res.Verdicts = append(res.Verdicts, v) // re-delivery: reuse, do not re-charge
			continue
		}
		// Enforce the batch spend cap BEFORE running: a job whose estimated spend would breach the cap
		// is skipped and surfaced, not run.
		if err := meter.Charge(evalrun.SpendExecution, job.ExpectedCostUSD); err != nil {
			res.Skipped = append(res.Skipped, ch)
			continue
		}
		v, err := Verify(ctx, runner, job.Proposal, job.EvalSetCaseIDs, cfg)
		if err != nil {
			return BatchResult{}, err
		}
		seen[ch] = v
		res.Verdicts = append(res.Verdicts, v)
	}
	res.Spend = meter.Report()
	return res, nil
}
