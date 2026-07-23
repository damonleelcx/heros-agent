package scorecard

import (
	"context"
	"log/slog"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/linkage"
	"github.com/heros-foreal/agentd/internal/reportstore"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// engine.go runs the whole read-only pipeline once and persists every output to the report store,
// then builds the scorecard View from those outputs. It is the single place the attribution and
// diagnosis engines are composed, so the read-only guarantee is asserted at one seam: Generate takes
// no config store and returns no proposal — it cannot mutate a workflow.

// Engine composes the attribution + diagnosis engines over a report store. The AblationRunner and the
// Analyst are optional: without a runner, ablation is skipped and the scorecard is `partial` for any
// candidate node; without an analyst, only rule diagnoses are produced.
type Engine struct {
	Store   reportstore.Store
	Runner  attribution.AblationRunner
	Analyst diagnosis.Analyst
	Cal     diagnosis.AnalystCalibration
	// Log, when set, receives a WARN per unclassified node — the reduced-coverage signal is surfaced
	// as structured data on the View (the primary, non-silent channel), and ALSO logged here so a
	// silent degradation is impossible to miss (logging-conventions). Nil = no logging (the default,
	// matching the package's no-ambient-logger style).
	Log *slog.Logger
}

// GenerateInput drives one scorecard build.
type GenerateInput struct {
	IR           *discovery.IR
	Variant      attribution.Variant
	FailingCases []attribution.FailingCase
	Overall      OverallMetrics

	// AblationTopN is how many top-contribution nodes to ablate (0 = none). SwappedConfigRef supplies
	// the counterfactual config ref for a node; AblationConfig carries the metric/seeds/stats.
	AblationTopN     int
	SwappedConfigRef func(node string) string
	AblationConfig   attribution.AblationConfig

	// StaticCallSites are the per-call-site linkage signals a P1 frontend extracted (enclosing symbol,
	// callees, shared-state refs). Optional: when supplied, inferred_static edges are recovered from
	// them; when absent, only the dynamic (trace) and framework (IR) edges are used. See
	// internal/linkage — deep extraction is the staged P1 uplift.
	StaticCallSites []linkage.CallSite
}

// Generate runs attribution + diagnosis (+ optional ablation), persists every report, and returns the
// read-only View. Nothing it does writes to a Variant Spec, a registry, or a config — the only writes
// are to the append-only report store.
func (e *Engine) Generate(ctx context.Context, in GenerateInput) (View, error) {
	v := in.Variant
	key := reportstore.ReportKey{VariantID: v.VariantID, EvalSetHash: v.EvalSetHash, ConfigHash: v.ConfigHash}

	// ── Recover the effective topology (Decision 8): framework edges from the IR + inferred_static
	// from the call-site signals (if any) + inferred_dynamic from the failing-case spans, reconciled
	// by provenance. Attribution orders first-divergence by this recovered set (13.3); the scorecard
	// surfaces each edge's provenance (13.4). ──
	var allSpans []telemetry.Span
	for _, fc := range in.FailingCases {
		allSpans = append(allSpans, fc.Trace.Spans...)
	}
	topo := linkage.Reconcile(
		linkage.FromIR(in.IR),
		linkage.InferStatic(in.StaticCallSites),
		linkage.InferDynamic(allSpans),
	)

	// ── Attribution ──
	contrib := attribution.AttributeWithTopology(in.IR, v, in.FailingCases, topo)
	if e.Store != nil {
		if err := e.Store.PutAttribution(ctx, key, contrib.Rows); err != nil {
			return ErrorView(v, "persist attribution: "+err.Error()), err
		}
	}

	clusters := attribution.Cluster(in.IR, v, in.FailingCases)
	flags := attribution.Bottleneck(v, in.FailingCases, attribution.BottleneckConfig{})
	if e.Store != nil {
		if err := e.Store.PutClusters(ctx, key, clusters); err != nil {
			return ErrorView(v, "persist clusters: "+err.Error()), err
		}
		if err := e.Store.PutBottleneckFlags(ctx, key, flags); err != nil {
			return ErrorView(v, "persist bottleneck flags: "+err.Error()), err
		}
	}

	// ── Diagnosis (rules-first, analyst on residue) ──
	diags, _, _, err := diagnosis.Diagnose(ctx, e.Analyst, in.IR, v, in.FailingCases, e.Cal, 0)
	if err != nil {
		return ErrorView(v, "diagnose: "+err.Error()), err
	}
	if e.Store != nil {
		if err := e.Store.PutDiagnoses(ctx, key, diags); err != nil {
			return ErrorView(v, "persist diagnoses: "+err.Error()), err
		}
		if e.Cal.AnalystMetric != "" {
			_ = e.Store.PutAnalystCalibration(ctx, e.Cal)
		}
	}

	// ── Ablation (optional) — seed from the contribution ranking ──
	var ablations []attribution.AblationResult
	ablationInProgress := false
	if in.AblationTopN > 0 {
		if e.Runner == nil || in.SwappedConfigRef == nil {
			// A candidate set exists but the fan-out has not run: the scorecard is PARTIAL, not
			// silently complete.
			ablationInProgress = len(attribution.CandidateNodes(contrib, in.AblationTopN)) > 0
		} else {
			for _, node := range attribution.CandidateNodes(contrib, in.AblationTopN) {
				res, err := attribution.Ablate(ctx, e.Runner, v, node, in.SwappedConfigRef(node), in.AblationConfig)
				if err != nil {
					// One ablation failing (e.g. under-seeded) does not sink the scorecard; mark the
					// fan-out partial and carry on.
					ablationInProgress = true
					continue
				}
				ablations = append(ablations, res)
			}
			if e.Store != nil && len(ablations) > 0 {
				if err := e.Store.PutAblation(ctx, key, ablations); err != nil {
					return ErrorView(v, "persist ablation: "+err.Error()), err
				}
			}
		}
	}

	// Per-node P3.5 pattern labels — "" for an unclassified node (a hand-rolled agent with no
	// discovered graph). Computed here (the engine holds the IR) so the read model can mark "not
	// classified" without reaching into the IR, and so a reduced-coverage WARN can be emitted.
	patterns := map[string]string{}
	for _, n := range contrib.Nodes {
		pat, _ := evalharness.NodePattern(in.IR, n.NodeID)
		patterns[n.NodeID] = string(pat)
		if pat == "" && e.Log != nil {
			// Not a silent default: the node is diagnosed by pattern-agnostic rules only, and that
			// reduced coverage is both surfaced on the View and logged (logging-conventions).
			e.Log.Warn("p4.5 scorecard: node has no P3.5 pattern label; pattern-scoped diagnosis skipped",
				"variant_id", v.VariantID, "node_id", n.NodeID, "coverage", "pattern_agnostic_only")
		}
	}

	return Build(Input{
		Variant:            v,
		Overall:            in.Overall,
		Contrib:            contrib,
		Clusters:           clusters,
		Flags:              flags,
		Diagnoses:          diags,
		Ablations:          ablations,
		Analyst:            e.Cal,
		Patterns:           patterns,
		Topology:           topo,
		AblationInProgress: ablationInProgress,
	}), nil
}
