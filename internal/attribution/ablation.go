package attribution

import (
	"context"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// ablation.go is §3: the ONLY rigorous causal isolation in the phase. Correlational attribution
// (per-node contribution, first-divergence) LOCALIZES but cannot PROVE — the node that first diverges
// may be a symptom. The counterfactual is the proof: hold every OTHER node's config fixed, swap
// exactly ONE node's config, re-run through the P4 harness multi-seed, and measure the delta with its
// confidence interval via the SAME evalstats.Compare primitive the leaderboard uses.
//
// Two structural properties carry from P4 into this file unchanged:
//   - A single-run delta is never a causal claim. Ablation reuses the multi-seed + CI + tie machinery,
//     so a delta whose CI overlaps zero is `inconclusive`, exactly as a comparison whose CIs overlap
//     is a tie. There is no path here that names a bottleneck from one run.
//   - Ablation runs are EPHEMERAL measurement variants. This package enqueues them through the runner
//     and reads their metrics back; it has no function that persists a variant or writes a node config.
//     The read-only guarantee is therefore a property of the code shape, not a promise (task 3.3).

// Verdict is an ablation's outcome. Only two values exist: a swap either moved the metric out of the
// noise band (`bottleneck`) or it did not (`inconclusive`). There is deliberately no "improvement"
// verdict — ablation attributes causal contribution, it does not recommend a change (that is P5.5).
type Verdict string

const (
	// VerdictBottleneck: the swapped node's config change moved the metric with a CI that excludes
	// zero AND non-overlapping intervals — the node is on the causal path for this metric.
	VerdictBottleneck Verdict = "bottleneck"
	// VerdictInconclusive: the delta's CI overlaps zero (or the intervals overlap). The swap did not
	// prove the node causal; naming it a bottleneck would be reading noise as signal.
	VerdictInconclusive Verdict = "inconclusive"
)

// AblationResult is one counterfactual measurement, matching the design's ablation_result row. It
// carries the full comparison so the CI and method are auditable, not just the verdict. Ephemeral is
// always true: an ablation result is never a user variant.
type AblationResult struct {
	VariantID   string `json:"variant_id"`
	EvalSetHash string `json:"eval_set_hash"`
	NodeID      string `json:"node_id"`
	// SwappedConfigRef is the content ref of the one node config that was swapped in. It is a
	// reference, not the config itself — the config lives in the object store, never inline.
	SwappedConfigRef string `json:"swapped_config_ref"`
	Metric           string `json:"metric"`

	DeltaMean float64 `json:"delta_mean"`
	CILow     float64 `json:"ci_low"`
	CIHigh    float64 `json:"ci_high"`
	NSeeds    int     `json:"n_seeds"`

	Verdict Verdict `json:"verdict"`
	// Comparison is the full evalstats result the verdict was derived from — kept so the delta ± CI,
	// the p-value, and the method are all auditable rather than believed.
	Comparison evalstats.Comparison `json:"comparison"`
	// Ephemeral records that this measurement variant was never persisted as a user variant. It is
	// always true; a false here would be a bug the store's constraint also refuses.
	Ephemeral bool `json:"ephemeral"`
}

// AblationRunner executes the two multi-seed measurement runs an ablation needs: the variant as-is
// (baseline) and the variant with exactly one node's config swapped (ablated). It is an INTERFACE so
// this package depends on no executor — the real runner (P4 harness fan-out on the P3 sandbox, no
// ambient credentials, spend-capped) is wired in §9/DevOps, and tests stub it.
//
// The runner's contract carries the phase's safety properties: the ablated run is an ephemeral
// measurement variant it enqueues and discards, and it MUST execute in the P3 sandbox. There is no
// method on this interface that persists a variant or applies a config — the interface cannot express
// a mutation.
type AblationRunner interface {
	// Baseline returns the metric series for the variant exactly as configured, over the given seeds.
	Baseline(ctx context.Context, v Variant, metric string, seeds []int64) (evalstats.Series, error)
	// Ablated returns the metric series with ONLY node's config replaced by swappedConfigRef, every
	// other node held fixed, over the given seeds. The run is an ephemeral measurement variant.
	Ablated(ctx context.Context, v Variant, node, swappedConfigRef, metric string, seeds []int64) (evalstats.Series, error)
}

// AblationConfig controls one ablation. Metric and Direction are explicit rather than inferred, for
// the same reason evalstats keeps Direction explicit: guessing "success is higher-is-better" from a
// name is how a delta eventually gets read backwards.
type AblationConfig struct {
	Metric    string
	Direction evalstats.Direction
	Seeds     []int64
	Stats     evalstats.Config
}

// Ablate runs one counterfactual: swap node's config to swappedConfigRef, re-run multi-seed, and
// return the measured delta with its CI and a bottleneck/inconclusive verdict (tasks 3.1, 3.2). The
// verdict is a pure function of evalstats.Compare — a tie (CI overlaps zero) is inconclusive, any
// declared winner is a bottleneck. No branch here can name a bottleneck from an overlapping CI.
func Ablate(ctx context.Context, runner AblationRunner, v Variant, node, swappedConfigRef string, cfg AblationConfig) (AblationResult, error) {
	if cfg.Metric == "" {
		return AblationResult{}, fmt.Errorf("attribution: ablation requires a metric")
	}
	if len(cfg.Seeds) < cfg.Stats.MinSeeds || len(cfg.Seeds) < evalstats.DefaultMinSeeds {
		// Under-seeding manufactures the non-overlap that produces a false bottleneck, exactly as it
		// manufactures a false leaderboard winner. Refuse rather than measure dishonestly.
		floor := cfg.Stats.MinSeeds
		if floor < evalstats.DefaultMinSeeds {
			floor = evalstats.DefaultMinSeeds
		}
		return AblationResult{}, fmt.Errorf("%w: ablation of node %q has %d seeds, floor is %d",
			evalstats.ErrUnderSeeded, node, len(cfg.Seeds), floor)
	}

	baseline, err := runner.Baseline(ctx, v, cfg.Metric, cfg.Seeds)
	if err != nil {
		return AblationResult{}, fmt.Errorf("attribution: ablation baseline for node %q: %w", node, err)
	}
	ablated, err := runner.Ablated(ctx, v, node, swappedConfigRef, cfg.Metric, cfg.Seeds)
	if err != nil {
		return AblationResult{}, fmt.Errorf("attribution: ablation run for node %q: %w", node, err)
	}

	// Compare ABLATED vs BASELINE: "did swapping this node's config move the metric?" The delta and
	// its CI come straight from the P4 primitive; the tie rule is enforced inside Compare, not here.
	cmp, err := evalstats.Compare(ablated, baseline, cfg.Direction, cfg.Stats)
	if err != nil {
		return AblationResult{}, fmt.Errorf("attribution: ablation compare for node %q: %w", node, err)
	}

	verdict := VerdictInconclusive
	if cmp.Verdict != evalstats.VerdictTie {
		verdict = VerdictBottleneck
	}
	return AblationResult{
		VariantID:        v.VariantID,
		EvalSetHash:      v.EvalSetHash,
		NodeID:           node,
		SwappedConfigRef: swappedConfigRef,
		Metric:           cfg.Metric,
		DeltaMean:        cmp.Delta.Mean,
		CILow:            cmp.Delta.Low,
		CIHigh:           cmp.Delta.High,
		NSeeds:           cmp.Delta.NSeeds,
		Verdict:          verdict,
		Comparison:       cmp,
		Ephemeral:        true,
	}, nil
}

// CandidateNodes seeds the ablation set from the per-node contribution ranking (design Q3 / task
// 3.4): ablate the nodes that localize the most failures first, so the combinatorial ablation space
// is entered at its most promising point rather than swept blindly. Nodes are ranked by first-
// divergence failure share (desc), then mean cost share (desc), then node id — a total order so the
// candidate list is deterministic. Nodes that localize no failure and carry no cost are dropped.
func CandidateNodes(contrib PerNodeContribution, topN int) []string {
	ranked := append([]NodeAttribution(nil), contrib.Nodes...)
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.FailureShare != b.FailureShare {
			return a.FailureShare > b.FailureShare
		}
		if a.MeanCostShare != b.MeanCostShare {
			return a.MeanCostShare > b.MeanCostShare
		}
		return a.NodeID < b.NodeID
	})
	out := make([]string, 0, topN)
	for _, n := range ranked {
		if n.FirstDivergenceCount == 0 && n.MeanCostShare == 0 && n.MeanLatencyShare == 0 {
			continue
		}
		out = append(out, n.NodeID)
		if topN > 0 && len(out) >= topN {
			break
		}
	}
	return out
}
