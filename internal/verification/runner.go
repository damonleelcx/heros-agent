// Package verification is the P5.5 verification gate: it proves a proposal before it can surface. It
// auto-executes a candidate's TRANSFORMED WORKING COPY (the built codemod output — the code that would
// ship, per ADR-001) through the P4 eval harness on a held-out split, admits only a
// statistically-significant gain (reusing the P4 evalstats.Compare primitive), and runs a regression
// check with a hard cost/latency budget. A proposal whose gate_result is not `pass` is WITHHELD:
// nothing unverified reaches the user.
package verification

import (
	"context"

	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalstats"
)

// Split names which slice of the eval set a run covers. The held-out split is the cases the proposal
// was NOT generated from — the only honest test of generalization (design Decision 5).
type Split string

const (
	SplitHeldOut    Split = "held_out"   // cases the proposal was not generated from
	SplitGenerating Split = "generating" // the cases that produced the diagnosis (never surfaced as the delta)
	SplitFull       Split = "full"       // no held-out split available; the whole set
)

// RunRequest asks the harness to execute one config over one split, multi-seed.
type RunRequest struct {
	// ConfigHash identifies the transformed working copy to run — the baseline or a candidate. The
	// harness builds and runs THAT code (ADR-001), not a shimmed parameter substitution.
	ConfigHash     string
	SourceRevision string
	// CaseIDs is the split to run over.
	CaseIDs []string
	Split   Split
	// Seeds is the multi-seed set; the significance test requires at least evalstats.DefaultMinSeeds.
	Seeds []int64
}

// RunResult is the harness output for one config over one split: the quality/cost/latency observations
// per (case, seed), ready for evalstats. Payloads never travel here — only metric values.
type RunResult struct {
	Quality evalstats.Series // task_success observations
	Cost    evalstats.Series // eval_cost_usd observations
	Latency evalstats.Series // eval_latency_ms observations
}

// EvalRunner executes a config's transformed working copy over a split and returns its metrics. It is
// the seam onto the P4 harness fan-out (internal/evalrun + internal/evalharness). Making it an
// interface lets the verification-gate logic be proven against fixture deltas (known-good / noise /
// overfit / cost-regression / cluster-regression) without a live provider — the codebase's
// "the only stub is the provider" discipline, applied to the gate.
type EvalRunner interface {
	Run(ctx context.Context, req RunRequest) (RunResult, error)
}

// standardMetrics names the three metric channels the gate reads. They are the P4 metric-name
// constants, so a verification run is an ordinary P4 eval_result slice (§5.4), not a second vocabulary.
var (
	metricQuality = evalharness.MetricTaskSuccess
	metricCost    = evalharness.MetricRunCostUSD
	metricLatency = evalharness.MetricRunLatencyMS
)
