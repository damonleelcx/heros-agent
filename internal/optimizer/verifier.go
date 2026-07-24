package optimizer

import (
	"context"

	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/verification"
)

// verifier.go is verification-in-the-loop (design Decision 6): every candidate is proven on a held-out
// split BEFORE it can be merged. The order is fixed and load-bearing — P5 typed-contract check → P5.5
// build gate → P5.5 held-out verification (multi-seed, mean+CI, significance vs. current best,
// regression) — because the single most dangerous thing an autonomous loop can do is trust its own
// proposal. "Diagnosis proposes; verification decides", even with no human in the seat.

// VerifyRequest is one candidate to prove, plus the eval context the P5.5 gate needs (the full eval set
// and the generating cases, so the held-out split is the set MINUS the generating cases — never the
// cases that produced the proposal, design Decision 6).
type VerifyRequest struct {
	Candidate       SearchCandidate
	BaselineConfig  string
	EvalSetCaseIDs  []string
	GeneratingCases []string
	Config          verification.Config
	// Proposal carries the cluster structure the regression check re-scores. The loop fills it from the
	// diagnosis/attribution; the candidate's config/diff hashes are copied in by the verifier.
	Proposal verification.Proposal
}

// VerifyResult is everything the loop reads to decide a merge: did the diff pass the typed contract,
// did it build, the held-out verdict (delta±CI, significance, regression, gate_result), the candidate's
// measured metrics (providers/cost/latency/quality/composite) for the P4 gates and the objective, and
// the provider spend this verification incurred (for the cumulative-budget halt). Every field is
// measured — the loop invents nothing.
type VerifyResult struct {
	ContractOK        bool                 `json:"contract_ok"`
	ContractReason    string               `json:"contract_reason,omitempty"`
	Builds            bool                 `json:"builds"`
	BuildLog          string               `json:"build_log,omitempty"`
	Verdict           verification.Verdict `json:"verdict"`
	Metrics           CandidateMetrics     `json:"metrics"`
	BaselineComposite evalstats.Interval   `json:"baseline_composite"`
	SpendUSD          float64              `json:"spend_usd"`
}

// MergeReady reports whether verification alone clears the candidate for merge: it passed the typed
// contract, it built, and the held-out gate passed. The loop ANDs this with the P4 gate verdict and
// merge_enabled before it merges — a candidate that fails any one of the three is never merged
// (design Decision 6; spec "A candidate SHALL NOT be merged on a non-building diff, on an unverified
// delta, or on the cases that generated it").
func (r VerifyResult) MergeReady() bool {
	return r.ContractOK && r.Builds && r.Verdict.Passed()
}

// Verifier proves one candidate. It is the loop's seam onto the real P5/P5.5 machinery; a StaticVerifier
// drives the loop's decision logic in tests, and a ComposedVerifier wires the shipped pipeline.
type Verifier interface {
	Verify(ctx context.Context, req VerifyRequest) (VerifyResult, error)
}

// ── ComposedVerifier: the shipped contract → build → held-out pipeline ──────────────────────────────

// ContractChecker validates a candidate against the P5 typed I/O contracts before verification (task
// 2.3): it rejects incoherent orderings/re-arrangements rather than letting them reach the gate.
type ContractChecker interface {
	Check(cand SearchCandidate) (ok bool, reason string)
}

// BuildGate compiles a candidate's transformed working copy (the P5.5 build gate, task 2.1). A
// non-building diff is rejected before any verification run.
type BuildGate interface {
	Build(ctx context.Context, cand SearchCandidate) (builds bool, log string)
}

// CompositeScorer computes the P4 composite score (with CI) for the candidate and the baseline from
// their held-out runs — the objective (design Decision 1). It is the P4 scoring pipeline; the verifier
// does not reinvent it.
type CompositeScorer interface {
	Composite(ctx context.Context, req VerifyRequest, candQuality, baseQuality float64) (cand, base evalstats.Interval, providers []string, cost, latency float64, err error)
}

// ComposedVerifier runs the real pipeline: typed-contract → build → verification.Verify → composite.
// A rejected contract or a failed build short-circuits BEFORE any verification run — nothing unbuilt or
// contract-incoherent ever costs a provider call (task 2.1/2.3).
type ComposedVerifier struct {
	Contract ContractChecker
	Build    BuildGate
	Runner   verification.EvalRunner
	Scorer   CompositeScorer
}

// Verify proves req.Candidate. It returns a VerifyResult whose MergeReady is true only when the
// contract passed, the diff built, and the held-out gate passed.
func (v ComposedVerifier) Verify(ctx context.Context, req VerifyRequest) (VerifyResult, error) {
	var res VerifyResult

	// 1. Typed I/O contract (task 2.3): reject incoherent orderings/re-arrangements before verifying.
	if v.Contract != nil {
		ok, reason := v.Contract.Check(req.Candidate)
		res.ContractOK, res.ContractReason = ok, reason
		if !ok {
			return res, nil
		}
	} else {
		res.ContractOK = true
	}

	// 2. Build gate (task 2.1): the diff must compile before any provider call.
	if v.Build != nil {
		builds, log := v.Build.Build(ctx, req.Candidate)
		res.Builds, res.BuildLog = builds, log
		if !builds {
			return res, nil
		}
	} else {
		res.Builds = true
	}

	// 3. Held-out verification (task 2.1): multi-seed, mean+CI, significance vs. current best,
	//    regression — computed on the transformed working copy over the held-out split.
	p := req.Proposal
	p.CandidateConfigHash = req.Candidate.ConfigHash
	p.BaselineConfigHash = req.BaselineConfig
	verdict, err := verification.Verify(ctx, v.Runner, p, req.EvalSetCaseIDs, req.Config)
	if err != nil {
		return res, err
	}
	res.Verdict = verdict

	// 4. Composite objective + gate metrics (design Decision 1): the P4 composite for candidate and
	//    baseline, and the candidate's providers/cost/latency for the P4 gates.
	res.Metrics = CandidateMetrics{ConfigHash: req.Candidate.ConfigHash, Providers: req.Candidate.Providers}
	if v.Scorer != nil {
		candQ := 0.0 // quality means come from the runs; the scorer recomputes from the same runner.
		baseQ := 0.0
		cand, base, providers, cost, latency, serr := v.Scorer.Composite(ctx, req, candQ, baseQ)
		if serr != nil {
			return res, serr
		}
		res.Metrics.Composite = cand
		res.Metrics.CostUSD = cost
		res.Metrics.LatencyMS = latency
		res.Metrics.Quality = verdict.Delta.Mean // held-out delta over baseline (informational)
		if len(providers) > 0 {
			res.Metrics.Providers = providers
		}
		res.BaselineComposite = base
	}
	return res, nil
}

// ── StaticVerifier: a canned Verifier for loop-decision tests ──────────────────────────────────────

// StaticVerifier returns a pre-canned VerifyResult per candidate config hash, so the loop's stopping/
// stall/halt/apply logic is driven by known verdicts without a live provider. Missing entries return a
// benign "verified, tiny gain" result so a search that wanders onto an unknown candidate does not crash.
type StaticVerifier struct {
	ByConfig map[string]VerifyResult
	// Spend is the per-verification provider spend charged to the run's cumulative budget. It lets a
	// test drive a run past the budget ceiling (task 5.2).
	Spend float64
	// Err, when non-nil for a config, makes Verify return it (the verification-service-down seam, task
	// 6.3).
	Err map[string]error
}

func (s StaticVerifier) Verify(_ context.Context, req VerifyRequest) (VerifyResult, error) {
	h := req.Candidate.ConfigHash
	if s.Err != nil {
		if err, ok := s.Err[h]; ok && err != nil {
			return VerifyResult{}, err
		}
	}
	r, ok := s.ByConfig[h]
	if !ok {
		r = VerifyResult{ContractOK: true, Builds: true}
	}
	if r.SpendUSD == 0 {
		r.SpendUSD = s.Spend
	}
	return r, nil
}
