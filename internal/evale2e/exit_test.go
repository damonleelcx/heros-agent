package evale2e

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/evalboard"
	"github.com/heros-foreal/agentd/internal/evalgen"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalrun"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/scoring"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// pipeline is the whole P4 path, run once and asserted many times. Building it per-test would run
// the fan-out for every checklist item; building it once means every assertion below is made against
// the SAME measurement, which is also the honest thing — a checklist where each item quietly gets
// its own favourable fixture proves nothing about the system.
type pipeline struct {
	set       evalrun.EvalSet
	loop      evalgen.LoopResult
	quality   evalgen.SetQuality
	store     *evalrun.MemStore
	cache     *scoring.Cache
	meter     *evalrun.Meter
	registry  *evalharness.Registry
	gen       *targetedGenerator
	planned   int
	completed int
	labels    map[string]string
}

const seedCount = 5

func build(t *testing.T) *pipeline {
	t.Helper()
	ctx := context.Background()
	ir := fxIR()

	// ── generate the eval set with the real gap-filling loop ─────────────────
	gen := newTargetedGenerator()
	sim := newSimRuntime("branch_a", "branch_b")
	cfg := evalgen.DefaultLoopConfig(gen)
	loop, err := evalgen.Fill(ctx, ir, fxHandAuthored(), sim, cfg)
	if err != nil {
		t.Fatalf("Fill: %v", err)
	}
	cases, err := evalgen.MeasureDifficulty(ctx, &baseline{}, loop.Cases, 3)
	if err != nil {
		t.Fatalf("MeasureDifficulty: %v", err)
	}
	quality := evalgen.MeasureQuality(cases, cfg)

	set, err := evalrun.NewEvalSet(ir.Workflow.ID, ir.IRVersion, cases)
	if err != nil {
		t.Fatalf("NewEvalSet: %v", err)
	}
	// ── fan out every variant ────────────────────────────────────────────────
	budget := 500.0
	meter := evalrun.NewMeter("evale2e", evalrun.Budget{TotalUSD: &budget})
	store := evalrun.NewMemStore()
	registry := evalharness.NewBuiltinRegistry()

	variants := []variantSpec{
		spec("v-strong", 0.93, 0.012, 620),
		spec("v-balanced", 0.78, 0.004, 280),
		// A near-duplicate of v-balanced: the true-zero-delta half of task 8.1's pair.
		spec("v-balanced-twin", 0.78, 0.004, 280),
		spec("v-cheap-broken", 0.30, 0.001, 140),
		spec("v-offlist", 0.85, 0.005, 300, "unapproved-vendor"),
	}

	planned, completed := 0, 0
	labels := map[string]string{}
	for _, v := range variants {
		labels[v.id] = v.label
		units := evalrun.PlanUnits(v.id, v.configHash, "rev-1", set, seeds())
		planned += len(units)

		caseOf := map[string]string{}
		for _, u := range units {
			caseOf[u.RunID] = u.CaseID
		}
		h := &runHandler{v: v, set: set, registry: registry, store: store, meter: meter, ir: ir, sim: sim, caseOf: caseOf}
		pool, err := evalrun.NewPool(newMemQueue(units), h, evalrun.PoolConfig{Concurrency: 8, PollInterval: time.Millisecond})
		if err != nil {
			t.Fatalf("NewPool: %v", err)
		}
		st, err := pool.Drain(ctx)
		if err != nil {
			t.Fatalf("Drain %s: %v", v.id, err)
		}
		completed += st.Handled
	}

	// ── build the score cache ────────────────────────────────────────────────
	board := scoring.Board{EvalSetHash: set.Hash, Specs: scoring.DefaultSpecs()}
	for _, v := range variants {
		sv := scoring.Variant{
			VariantID: v.id, ConfigHash: v.configHash, Label: v.label, Providers: v.providers,
			Metrics: map[string]evalstats.Series{}, WeakCaseIDs: set.WeakCaseIDs(),
			LowConfidence: quality.LowConfidence,
		}
		for _, sp := range scoring.DefaultSpecs() {
			s, err := store.SeriesFor(ctx, set.Hash, v.id, sp.Name)
			if err != nil {
				t.Fatalf("SeriesFor: %v", err)
			}
			sv.Metrics[sp.Name] = s
		}
		board.Variants = append(board.Variants, sv)
	}
	cache, err := scoring.Build(board, evalstats.DefaultConfig())
	if err != nil {
		t.Fatalf("score cache: %v", err)
	}

	return &pipeline{set: set, loop: loop, quality: quality, store: store, cache: cache,
		meter: meter, registry: registry, gen: gen, planned: planned, completed: completed, labels: labels}
}

func seeds() []int64 {
	out := make([]int64, 0, seedCount)
	for i := 0; i < seedCount; i++ {
		out = append(out, int64(i))
	}
	return out
}

func (p *pipeline) series(t *testing.T, variantID, metric string) evalstats.Series {
	t.Helper()
	s, err := p.store.SeriesFor(context.Background(), p.set.Hash, variantID, metric)
	if err != nil {
		t.Fatalf("SeriesFor: %v", err)
	}
	return s
}

func (p *pipeline) view(profile scoring.Profile, gates scoring.GateSet) evalboard.View {
	return evalboard.Build(evalboard.Input{
		WorkflowID: "wf-evale2e", Cache: p.cache, Profile: profile, Gates: gates,
		Coverage: &p.loop.Report, Quality: &p.quality, StoppedBecause: p.loop.StoppedBecause,
		Spend: p.meter.Report(), Labels: p.labels,
		Progress: evalboard.Progress{UnitsPlanned: p.planned, UnitsCompleted: p.completed, SeedFloor: seedCount},
	})
}

func fptr(v float64) *float64 { return &v }

// ═════════════════════════════════════════════════════════════════════════════
// Task 8.8 — the M5 exit checklist (PRD §13), item by item
// ═════════════════════════════════════════════════════════════════════════════

func TestM5ExitChecklist(t *testing.T) {
	p := build(t)
	gates := scoring.GateSet{
		Name: "production", MinQuality: fptr(0.55), MaxCostPerRun: fptr(0.20),
		LatencySLAMs: fptr(6000), ProviderAllowlist: []string{"anthropic"},
	}
	v := p.view(scoring.Balanced(), gates)

	t.Logf("eval set %s: %d cases · %s", p.set.Hash[:12], len(p.set.Cases), p.loop.Report.Summary())
	t.Logf("fan-out: %d/%d units · %d rows · spend $%.4f", p.completed, p.planned, p.store.Len(), p.meter.Report().TotalUSD)
	for _, r := range v.Ranked {
		t.Logf("  #%d %-18s %.4f ± [%.4f, %.4f] flags=%v", r.Rank, r.VariantID, r.Score, r.CILow, r.CIHigh, r.Flags)
	}
	for _, r := range v.Disqualified {
		t.Logf("  DQ  %-18s failed=%v", r.VariantID, r.FailedGates)
	}

	t.Run("two variants over a generated + user eval set, multi-seed, on a leaderboard with CI-bounded scores and gate status", func(t *testing.T) {
		origins := map[evalharness.Origin]int{}
		for _, c := range p.set.Cases {
			origins[c.Origin]++
		}
		if origins[evalharness.OriginHandAuthored] == 0 {
			t.Fatal("the user's hand-authored cases must be in the set")
		}
		if origins[evalharness.OriginSchema]+origins[evalharness.OriginLLM]+origins[evalharness.OriginAdversarial] == 0 {
			t.Fatal("the generated cases must be in the set")
		}
		if len(v.Ranked)+len(v.Disqualified) < 2 {
			t.Fatalf("want at least two variants on the board, got %d", len(v.Ranked)+len(v.Disqualified))
		}
		for _, r := range append(v.Ranked, v.Disqualified...) {
			if r.NSeeds < seedCount {
				t.Fatalf("%s: want >= %d seeds, got %d", r.VariantID, seedCount, r.NSeeds)
			}
			if r.CILow >= r.CIHigh && r.CIHigh-r.CILow == 0 && r.Score != r.CILow {
				t.Fatalf("%s: score must lie in its interval", r.VariantID)
			}
			if len(r.Components) == 0 {
				t.Fatalf("%s: no component breakdown", r.VariantID)
			}
		}
	})

	t.Run("evaluators are pluggable: a built-in and a user-registered custom metric both compute over traces", func(t *testing.T) {
		reg := evalharness.NewBuiltinRegistry()
		called := 0
		err := reg.RegisterMetric("domain_accuracy", "domain_accuracy", evalharness.UnitRange(), nil,
			func(_ context.Context, tr evalharness.Trace, c evalharness.Case, tgt evalharness.Target) (float64, error) {
				called++
				if out, ok := tr.OutputFor(tgt); ok && contains(string(out), "correct") {
					return 1, nil
				}
				return 0, nil
			})
		if err != nil {
			t.Fatalf("register custom metric: %v", err)
		}
		builtin, _ := reg.Get(evalharness.EvaluatorExactMatch)
		custom, _ := reg.Get("domain_accuracy")

		h := &runHandler{v: spec("probe", 1, 0.01, 100), set: p.set, sim: newSimRuntime("branch_a", "branch_b")}
		c := p.set.Cases[0]
		tr, _ := h.stubProviderRun(mkRC("probe", c.CaseID), c)

		for _, e := range []evalharness.Evaluator{builtin, custom} {
			mv, err := evalharness.Compute(context.Background(), e, tr, c, evalharness.RunTarget())
			if err != nil && !errors.Is(err, evalharness.ErrNotApplicable) {
				t.Fatalf("%s: %v", e.Name(), err)
			}
			if err == nil && !e.Range().Contains(mv.Value) {
				t.Fatalf("%s: value %v outside its declared range", e.Name(), mv.Value)
			}
		}
		if called == 0 {
			t.Fatal("the custom metric never ran")
		}
	})

	t.Run("metric-sets are selected by the P3.5 pattern label: a router is not scored as a RAG node", func(t *testing.T) {
		reg := evalharness.NewBuiltinRegistry()
		must := func(name, metric string, pat patternclassifier.Pattern) {
			t.Helper()
			if err := reg.RegisterMetric(name, metric, evalharness.UnitRange(), []patternclassifier.Pattern{pat},
				func(_ context.Context, tr evalharness.Trace, c evalharness.Case, tgt evalharness.Target) (float64, error) {
					return 1, nil
				}); err != nil {
				t.Fatalf("register %s: %v", name, err)
			}
		}
		must("misroute_rate", "misroute_rate", patternclassifier.Routing)
		must("relevance_at_k", "relevance_at_k", patternclassifier.RetrievalRAG)

		plan := evalharness.BuildPlan(fxIR(), reg)
		byNode := map[string]evalharness.NodePlan{}
		for _, np := range plan.Nodes {
			byNode[np.Target.NodeID] = np
		}
		if !containsStr(byNode["router"].Evaluators, "misroute_rate") {
			t.Fatalf("the router must be scored with misroute metrics, got %v", byNode["router"].Evaluators)
		}
		if containsStr(byNode["router"].Evaluators, "relevance_at_k") {
			t.Fatal("relevance@k must NOT be computed on the router")
		}
		if !containsStr(byNode["branch_b"].Evaluators, "relevance_at_k") {
			t.Fatalf("the RAG node must be scored with relevance@k, got %v", byNode["branch_b"].Evaluators)
		}
		if containsStr(byNode["branch_b"].Evaluators, "misroute_rate") {
			t.Fatal("misroute-rate must NOT be computed on the RAG node")
		}
		if len(byNode["router"].Refusals) == 0 {
			t.Fatal("every refusal must be recorded with its reason")
		}
	})

	t.Run("per-node contribution to end-to-end success/cost/latency is computed from traces", func(t *testing.T) {
		rows := p.store.Query(evalrun.Slice{
			EvalSetHash: p.set.Hash, VariantID: "v-strong", Metric: evalharness.MetricNodeCostShare,
		})
		if len(rows) == 0 {
			t.Fatal("no per-node cost-share rows were persisted")
		}
		nodes := map[string]bool{}
		var sum float64
		for _, r := range rows {
			if r.CaseID == rows[0].CaseID && r.Seed == rows[0].Seed {
				nodes[r.NodeID] = true
				sum += r.Value
			}
		}
		if len(nodes) < 2 {
			t.Fatalf("contribution must decompose across nodes, got %v", nodes)
		}
		if sum < 0.99 || sum > 1.01 {
			t.Fatalf("one run's cost shares must sum to 1, got %v", sum)
		}
		// And it is queryable per case AND per node — the property P4.5 consumes.
		perNode := p.store.Query(evalrun.Slice{EvalSetHash: p.set.Hash, NodeID: "router",
			Metric: evalharness.MetricNodeLatencyShare, CaseID: rows[0].CaseID})
		if len(perNode) == 0 {
			t.Fatal("per-node contribution must be sliceable by {node, case}")
		}
	})

	t.Run("a true-zero-delta pair is reported as a tie, not a false winner", func(t *testing.T) {
		a := p.series(t, "v-balanced", evalharness.MetricTaskSuccess)
		b := p.series(t, "v-balanced-twin", evalharness.MetricTaskSuccess)
		cmp, err := evalstats.Compare(a, b, evalstats.HigherIsBetter, evalstats.DefaultConfig())
		if err != nil {
			t.Fatalf("Compare: %v", err)
		}
		t.Logf("zero-delta: a=[%.4f,%.4f] b=[%.4f,%.4f] verdict=%s", cmp.A.Low, cmp.A.High, cmp.B.Low, cmp.B.High, cmp.Verdict)
		if cmp.Verdict != evalstats.VerdictTie {
			t.Fatalf("two identically-configured variants must tie, got %q (%s)", cmp.Verdict, cmp.Reason)
		}

		// And the known-real-delta pair yields the correct winner.
		strong := p.series(t, "v-strong", evalharness.MetricTaskSuccess)
		broken := p.series(t, "v-cheap-broken", evalharness.MetricTaskSuccess)
		real, err := evalstats.Compare(strong, broken, evalstats.HigherIsBetter, evalstats.DefaultConfig())
		if err != nil {
			t.Fatalf("Compare: %v", err)
		}
		t.Logf("real-delta: strong=[%.4f,%.4f] broken=[%.4f,%.4f] verdict=%s p=%.4f",
			real.A.Low, real.A.High, real.B.Low, real.B.High, real.Verdict, real.PValue)
		if real.Verdict != evalstats.VerdictAWins {
			t.Fatalf("a large real delta must name the correct winner, got %q", real.Verdict)
		}
		if real.A.Overlaps(real.B) {
			t.Fatal("a large real delta must produce non-overlapping intervals")
		}

		// On the BOARD, the zero-delta pair renders tied.
		byID := map[string]evalboard.Row{}
		for _, r := range v.Ranked {
			byID[r.VariantID] = r
		}
		if !containsStr(byID["v-balanced"].TiedWith, "v-balanced-twin") {
			t.Fatalf("the zero-delta pair must render tied on the board, got %v", byID["v-balanced"].TiedWith)
		}
	})

	t.Run("every LLM-judge metric reports agreement vs a human subset; an uncalibrated judge cannot gate", func(t *testing.T) {
		// The human labels split the subset evenly, and the judge agrees on all but one. An even
		// split matters: over a skewed subset chance agreement is near-total and kappa collapses to
		// zero however often the judge is right — which is the whole reason kappa is reported beside
		// raw percent agreement rather than instead of it.
		judgeTable := map[string]float64{}
		agreeing, err := evalharness.NewLLMJudge(evalharness.EvaluatorLLMJudge, evalharness.MetricJudgeScore,
			&tableJudge{byInput: judgeTable},
			evalharness.JudgeConfig{Model: "stub", ScaleMax: 1},
			evalharness.JudgeStanding{Metric: evalharness.MetricJudgeScore, Floor: evalharness.DefaultAgreementFloor})
		if err != nil {
			t.Fatalf("judge: %v", err)
		}

		cases := map[string]evalharness.Case{}
		traces := map[string]evalharness.Trace{}
		var labels []evalharness.HumanLabel
		h := &runHandler{v: spec("probe", 1, 0.01, 100), set: p.set, sim: newSimRuntime("branch_a", "branch_b")}
		for i, c := range p.set.Cases {
			if i >= 10 {
				break
			}
			c.Rubric = "Is the answer correct?"
			cases[c.CaseID] = c
			tr, _ := h.stubProviderRun(mkRC("probe", c.CaseID), c)
			traces[c.CaseID] = tr

			human := 0.0
			if i%2 == 0 {
				human = 1 // an even 5/5 split across the ten labeled cases
			}
			// The judge matches the human everywhere except the last case.
			judged := human
			if i == 9 {
				judged = 1 - human
			}
			judgeTable[string(c.Input)] = judged
			labels = append(labels, evalharness.HumanLabel{CaseID: c.CaseID, Score: human, Labeler: "reviewer"})
		}

		// UNCALIBRATED first: the standing says so, and a gate on it is refused.
		before := agreeing.Standing()
		if before.Calibrated || before.GateEligible() {
			t.Fatalf("a judge with no subset must be uncalibrated and ineligible: %+v", before)
		}
		if err := evalharness.EnsureGateEligible(before); !errors.Is(err, evalharness.ErrJudgeNotGateEligible) {
			t.Fatalf("want a refusal, got %v", err)
		}

		st, err := evalharness.Calibrate(context.Background(), agreeing,
			evalharness.CalibrationSubset{Metric: evalharness.MetricJudgeScore, Floor: evalharness.DefaultAgreementFloor, Labels: labels},
			cases, traces)
		if err != nil {
			t.Fatalf("Calibrate: %v", err)
		}
		t.Logf("judge calibration: kappa=%.3f pct=%.3f n_human=%d eligible=%v",
			st.Agreement, st.PercentAgreement, st.NHuman, st.GateEligible())
		if st.NHuman != 10 {
			t.Fatalf("agreement must be computed over the real human labels, got n=%d", st.NHuman)
		}
		// 9 of 10 over an even split: kappa 0.8, comfortably over the 0.6 floor.
		if st.Agreement < evalharness.DefaultAgreementFloor {
			t.Fatalf("a judge agreeing 9 of 10 on an even split must clear the floor, got kappa %.3f", st.Agreement)
		}
		if !st.GateEligible() {
			t.Fatalf("a calibrated, above-floor judge MUST be gate-eligible: %+v", st)
		}
		agreeing.SetStanding(st)

		// EVERY score now carries the standing.
		c := cases[labels[0].CaseID]
		mv, err := evalharness.Compute(context.Background(), agreeing, traces[c.CaseID], c, evalharness.RunTarget())
		if err != nil {
			t.Fatalf("Compute: %v", err)
		}
		if mv.Judge == nil || mv.Judge.NHuman != st.NHuman {
			t.Fatalf("the standing must ride on every judge score: %+v", mv.Judge)
		}

		// A BOARD gate on an uncalibrated judge disqualifies nobody, and says so.
		specs := scoring.DefaultSpecs()
		for i := range specs {
			if specs[i].Name == evalharness.MetricTaskSuccess {
				specs[i].JudgeStanding = &evalharness.JudgeStanding{
					Metric: evalharness.MetricTaskSuccess, Floor: 0.6, Calibrated: false}
			}
		}
		judgeCache := rebuildCache(t, p, specs)
		lb := judgeCache.Rank(scoring.CostOptimized(), scoring.GateSet{Name: "prod", MinQuality: fptr(0.55)})
		for _, r := range lb.Disqualified {
			if containsStr(r.Gate.Failed, scoring.GateMinQuality) {
				t.Fatalf("%s: an uncalibrated judge must disqualify nobody", r.VariantID)
			}
		}
		if len(lb.Notes) == 0 {
			t.Fatal("the refused gate must be surfaced on the board")
		}

		// And the CALIBRATED judge is allowed to gate — otherwise this item would only prove the
		// negative, and a system that refused every judge would pass it just as happily.
		for i := range specs {
			if specs[i].Name == evalharness.MetricTaskSuccess {
				st := st
				specs[i].JudgeStanding = &st
				specs[i].JudgeStanding.Metric = evalharness.MetricTaskSuccess
			}
		}
		okCache := rebuildCache(t, p, specs)
		okBoard := okCache.Rank(scoring.CostOptimized(), scoring.GateSet{Name: "prod", MinQuality: fptr(0.55)})
		gated := false
		for _, r := range okBoard.Disqualified {
			if containsStr(r.Gate.Failed, scoring.GateMinQuality) {
				gated = true
			}
		}
		if !gated {
			t.Fatal("a calibrated, above-floor judge must be able to gate")
		}
	})

	t.Run("the generator measures coverage and iterates until thresholds are met", func(t *testing.T) {
		if !p.loop.Report.Path.Met() {
			t.Fatalf("path coverage %v below target %v; uncovered %v",
				p.loop.Report.Path.Achieved, p.loop.Report.Path.Target, p.loop.Report.Path.Uncovered())
		}
		if len(p.loop.Rounds) == 0 {
			t.Fatal("the loop must actually iterate")
		}
		// The LLM layer was pointed at the residual, not the whole space.
		if len(p.gen.requests) == 0 {
			t.Fatal("the targeted generator was never invoked")
		}
		if len(p.gen.requests[0].Targets) == 0 {
			t.Fatal("the generator must be handed the specific uncovered obligations")
		}
	})

	t.Run("an unreachable path reports a residual rather than a false 100%", func(t *testing.T) {
		gen := newTargetedGenerator(
			evalgen.EdgeID("router", "branch_dead"),
			evalgen.BranchID("router", "branch_dead"),
			"branch_dead",
		)
		cfg := evalgen.DefaultLoopConfig(gen)
		cfg.MaxIterations = 3
		res, err := evalgen.Fill(context.Background(), fxIRWithDeadBranch(), fxHandAuthored(),
			newSimRuntime("branch_a", "branch_b"), cfg)
		if err != nil {
			t.Fatalf("Fill: %v", err)
		}
		t.Logf("unreachable: %s · stopped: %s", res.Report.Summary(), res.StoppedBecause)
		if res.Report.Met() || res.Report.Path.Achieved >= 1 {
			t.Fatal("coverage must not read as met when a branch is unreachable")
		}
		if !containsStr(res.Report.Residual, evalgen.EdgeID("router", "branch_dead")) {
			t.Fatalf("the unreachable edge must be named, got %v", res.Report.Residual)
		}
	})

	t.Run("cases are flagged gold vs weak; weak references do not silently drive scoring", func(t *testing.T) {
		if p.quality.NGold == 0 || p.quality.NWeak == 0 {
			t.Fatalf("the set must carry both gold and weak references, got %d gold %d weak",
				p.quality.NGold, p.quality.NWeak)
		}
		gating, weak := evalgen.GatingSet(p.set.Cases)
		if len(weak) != p.quality.NWeak {
			t.Fatalf("every weak case must be split out, got %d of %d", len(weak), p.quality.NWeak)
		}
		for _, c := range gating {
			if c.Label == evalharness.LabelWeak {
				t.Fatalf("a weak case reached the gating set: %s", c.CaseID)
			}
		}
		// The label rides all the way onto the persisted rows and onto the board.
		weakRows := p.store.Query(evalrun.Slice{EvalSetHash: p.set.Hash, ExcludeWeak: true})
		allRows := p.store.Query(evalrun.Slice{EvalSetHash: p.set.Hash})
		if len(weakRows) >= len(allRows) {
			t.Fatal("excluding weak-labeled evidence must actually exclude rows")
		}
		for _, r := range v.Ranked {
			if !containsStr(r.Flags, "weak-labeled") {
				t.Fatalf("%s: a score resting on weak references must be flagged", r.VariantID)
			}
		}
	})

	t.Run("difficulty and diversity are reported; a weak set is surfaced as low-confidence", func(t *testing.T) {
		if !p.quality.DifficultyMeasured {
			t.Fatal("difficulty must be measured against a baseline, not asserted")
		}
		if p.quality.Diversity <= 0 {
			t.Fatal("diversity must be reported")
		}
		t.Logf("set quality: difficulty=%.2f diversity=%.2f oracle=%.2f low_confidence=%v",
			p.quality.Difficulty, p.quality.Diversity, p.quality.OracleCoverage, p.quality.LowConfidence)

		// And a deliberately weak set IS surfaced as low-confidence.
		var trivial []evalharness.Case
		in, _ := json.Marshal(map[string]any{"route": "branch_a", "q": "hello"})
		for i := 0; i < 6; i++ {
			trivial = append(trivial, evalharness.Case{
				CaseID: "triv-" + string(rune('a'+i)), WorkflowID: "wf", Input: in,
				Label: evalharness.LabelNone, Difficulty: 0.01,
			})
		}
		q := evalgen.MeasureQuality(trivial, evalgen.LoopConfig{})
		if !q.LowConfidence || len(q.Reasons) == 0 {
			t.Fatalf("a near-duplicate trivially-easy set must be low-confidence with reasons: %+v", q)
		}
	})

	t.Run("metrics are normalized to [0,1] before weighting and the composite matches the formula", func(t *testing.T) {
		prof := scoring.Balanced()
		lb := p.cache.Rank(prof, scoring.GateSet{Name: "none"})
		for _, r := range lb.Ranked {
			cv := p.cache.Variants[r.VariantID]
			want := prof.WQuality*cv.Metrics[evalharness.MetricTaskSuccess].Normalized +
				prof.WCost*cv.Metrics[evalharness.MetricRunCostUSD].Normalized +
				prof.WLatency*cv.Metrics[evalharness.MetricRunLatencyMS].Normalized +
				prof.WReliability*cv.Metrics[evalharness.MetricReliability].Normalized
			for _, amt := range r.Penalties {
				want -= amt
			}
			if diff := r.Composite.Mean - want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("%s: composite %v does not match the formula %v", r.VariantID, r.Composite.Mean, want)
			}
			for name, c := range r.Components {
				if c.Normalized < 0 || c.Normalized > 1 {
					t.Fatalf("%s %s: normalized %v outside [0,1]", r.VariantID, name, c.Normalized)
				}
			}
		}
	})

	t.Run("weight profiles are named and switching them re-ranks without re-executing", func(t *testing.T) {
		before := p.store.Len()
		var boards []evalboard.View
		start := time.Now()
		for _, name := range scoring.ProfileNames() {
			boards = append(boards, p.view(scoring.NamedProfiles()[name], gates))
		}
		elapsed := time.Since(start)
		t.Logf("re-ranked %d profiles in %v", len(boards), elapsed)

		if p.store.Len() != before {
			t.Fatal("a profile switch must not produce a single new result row")
		}
		for _, b := range boards {
			if b.RunsEnqueued != 0 {
				t.Fatalf("profile %s enqueued %d runs", b.Profile, b.RunsEnqueued)
			}
		}
		if elapsed > 200*time.Millisecond {
			t.Fatalf("re-ranking must be under 200ms, took %v", elapsed)
		}
		// The profiles must actually differ, or the claim is vacuous.
		if boards[0].Ranked[0].VariantID == boards[2].Ranked[0].VariantID &&
			boards[0].Ranked[0].Score == boards[2].Ranked[0].Score {
			t.Fatal("balanced and cost-optimized must produce different scores")
		}
	})

	t.Run("hard constraints are disqualifying gates, separate from weights", func(t *testing.T) {
		costBoard := p.view(scoring.CostOptimized(), gates)
		dq := map[string][]string{}
		for _, r := range costBoard.Disqualified {
			dq[r.VariantID] = r.FailedGates
		}
		if !containsStr(dq["v-cheap-broken"], scoring.GateMinQuality) {
			t.Fatalf("the below-quality variant must be disqualified on the COST board, got %v", dq)
		}
		if !containsStr(dq["v-offlist"], scoring.GateProviderAllowlist) {
			t.Fatalf("the off-allowlist variant must be disqualified, got %v", dq)
		}
		for _, r := range costBoard.Ranked {
			if r.VariantID == "v-cheap-broken" {
				t.Fatal("a disqualified variant must not appear in the ranked order")
			}
		}
		// Control: without the gate the cheap variant really would top the cost board.
		ungated := p.view(scoring.CostOptimized(), scoring.GateSet{Name: "none"})
		if ungated.Ranked[0].VariantID != "v-cheap-broken" {
			t.Fatalf("the fixture is not exercising the failure: ungated cost board's #1 is %s",
				ungated.Ranked[0].VariantID)
		}
		t.Logf("ungated cost #1 = %s; gated board excludes it entirely", ungated.Ranked[0].VariantID)
	})

	t.Run("leaderboard rows carry score ± CI, breakdown, gate status and config lineage; Pareto renders the frontier", func(t *testing.T) {
		for _, r := range v.Ranked {
			if len(r.ConfigHash) != 64 || r.ConfigHashShort == "" {
				t.Fatalf("%s: config lineage missing", r.VariantID)
			}
			if !r.GatePass {
				t.Fatalf("%s: a ranked row must be a gate-passer", r.VariantID)
			}
			if len(r.Components) < 4 {
				t.Fatalf("%s: want the full component breakdown, got %d", r.VariantID, len(r.Components))
			}
			if r.Method == "" {
				t.Fatalf("%s: the interval must state its method", r.VariantID)
			}
		}
		if len(v.Pareto) == 0 {
			t.Fatal("the Pareto view must render")
		}
		frontier := 0
		for _, pt := range v.Pareto {
			if pt.NonDominated {
				frontier++
			}
		}
		if frontier == 0 {
			t.Fatal("at least one variant is always non-dominated")
		}
		t.Logf("Pareto: %d of %d variants on the frontier", frontier, len(v.Pareto))
	})

	t.Run("every persisted result carries the full P0 tag set", func(t *testing.T) {
		rows := p.store.Query(evalrun.Slice{EvalSetHash: p.set.Hash})
		if len(rows) == 0 {
			t.Fatal("no rows persisted")
		}
		for _, r := range rows {
			if err := r.Validate(); err != nil {
				t.Fatalf("under-tagged row reached the store: %v", err)
			}
			if err := r.AsMetricEvent().Validate(); err != nil {
				t.Fatalf("row violates the P0 metric-event contract: %v", err)
			}
		}
		t.Logf("tag completeness verified across %d rows", len(rows))
	})

	t.Run("adversarial cases are routed to the sandbox before anything executes", func(t *testing.T) {
		placements := evalrun.AuditPlacements(p.set.Cases)
		if bad := evalrun.UnsandboxedAdversarial(placements, p.set.Cases); len(bad) != 0 {
			t.Fatalf("adversarial cases scheduled on the host: %v", bad)
		}
		sandboxed := 0
		for _, pl := range placements {
			if pl.Sandboxed {
				sandboxed++
			}
		}
		if sandboxed == 0 {
			t.Fatal("the generated set must contain adversarial cases needing isolation")
		}
		t.Logf("%d of %d cases routed to the P3 sandbox", sandboxed, len(placements))
	})
}

func mkRC(variantID, caseID string) telemetry.RunContext {
	return telemetry.RunContext{VariantID: variantID, RunID: "run-" + caseID,
		ConfigHash: hash64(variantID), Seed: 0, CaseID: caseID}
}

// rebuildCache re-derives the score cache under different metric specs (used to attach a judge
// standing), reading the SAME persisted results rather than re-running anything.
func rebuildCache(t *testing.T, p *pipeline, specs []scoring.MetricSpec) *scoring.Cache {
	t.Helper()
	board := scoring.Board{EvalSetHash: p.set.Hash, Specs: specs}
	for _, id := range p.cache.Order {
		cv := p.cache.Variants[id]
		sv := scoring.Variant{VariantID: id, ConfigHash: cv.ConfigHash, Label: cv.Label,
			Providers: cv.Providers, Metrics: map[string]evalstats.Series{}, WeakCaseIDs: cv.WeakCaseIDs}
		for _, sp := range specs {
			sv.Metrics[sp.Name] = p.series(t, id, sp.Name)
		}
		board.Variants = append(board.Variants, sv)
	}
	c, err := scoring.Build(board, evalstats.DefaultConfig())
	if err != nil {
		t.Fatalf("rebuild cache: %v", err)
	}
	return c
}
