package proposal

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P18 §6 — the harness operator and its admissibility gate (Step 8 of the "add an axis" checklist).
//
// The hardest thing to get right here is not the emission. It is that this operator's candidate can be
// MEASURABLY BETTER AND STILL INADMISSIBLE — a heavier scaffold that raises task_success while
// multiplying cost is a bill nobody agreed to — so half this file exists to make a gate that cannot
// reject impossible to ship.

const (
	refHarnessSingle  = "a111111111111111111111111111111111111111111111111111111111111111"
	refHarnessReflex  = "b222222222222222222222222222222222222222222222222222222222222222"
	refHarnessReact   = "c333333333333333333333333333333333333333333333333333333333333333"
	refHarnessCritic  = "d444444444444444444444444444444444444444444444444444444444444444"
	refHarnessPlanExe = "e555555555555555555555555555555555555555555555555555555555555555"
)

func harnessMenu() Menu {
	return Menu{
		HarnessStrategies: []HarnessChoice{
			{Ref: refHarnessSingle, Strategy: "single-shot", Title: "Single shot", MaxTurns: 1},
			{Ref: refHarnessReflex, Strategy: "reflexion", Title: "Answer and revise", MaxTurns: 3},
			{Ref: refHarnessReact, Strategy: "react-loop", Title: "Reason and act", MaxTurns: 6},
			{Ref: refHarnessCritic, Strategy: "critic-loop", Title: "Generate and critique", MaxTurns: 3},
			{Ref: refHarnessPlanExe, Strategy: "plan-execute", Title: "Plan, then execute", MaxTurns: 4},
		},
	}
}

func harnessBase() *variantspec.VariantSpec {
	return &variantspec.VariantSpec{
		WorkflowID: "wf-harness", SourceRevision: "rev1",
		Order: []string{"solve", "check"},
		Nodes: map[string]variantspec.NodeOverride{
			"solve": {HarnessRef: refHarnessSingle},
		},
	}
}

func harnessInput() OperatorInput {
	return OperatorInput{
		Diagnosis: diagnosis.Diagnosis{DiagID: "d1", NodeID: "solve", Confidence: 0.8,
			EvidenceCaseIDs: []string{"c1", "c2"}, Source: diagnosis.SourceRule},
		Signal:  SignalScaffoldMismatch,
		Pattern: patternclassifier.Reflection,
		Base:    harnessBase(),
		Menu:    harnessMenu(),
	}
}

// TestOpHarnessStrategyInCatalog — task 6.1. A reserved constant with no catalog row is a vocabulary
// entry nothing can ever emit; a kind that shares a spelling with another operator is a proposal row
// nobody can attribute.
func TestOpHarnessStrategyInCatalog(t *testing.T) {
	if OpHarnessStrategy != "harness_strategy_switch" {
		t.Fatalf("OpHarnessStrategy = %q, want harness_strategy_switch: the kind is stored on proposal "+
			"rows, so a rename orphans every row already written", OpHarnessStrategy)
	}
	for _, other := range []OperatorKind{OpMemoryPolicy, OpContextPolicy, OpReorder, OpMerge} {
		if OpHarnessStrategy == other {
			t.Fatalf("the harness operator shares a kind with %s; a consumer could not tell a scaffold "+
				"change from it", other)
		}
	}

	var rows []Operator
	for _, op := range DefaultCatalog() {
		if op.Kind() == OpHarnessStrategy {
			rows = append(rows, op)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("DefaultCatalog() has %d harness rows, want 1; a constant with no row can never emit a "+
			"candidate, which would make the whole operator decorative", len(rows))
	}

	// A prior and an order hint, or the candidate cannot be ranked or scheduled.
	if operatorPrior[OpHarnessStrategy] <= 0 {
		t.Error("the harness operator has no prior; it would rank below every operator that has one")
	}
	if VerifyOrderHint(OpHarnessStrategy) >= 99 {
		t.Error("the harness operator has no verify-order hint; it would sort last by accident rather " +
			"than by the cheapest-first rule")
	}

	// 🚫 It never claims a reorder. The dimensions it declares must be exactly [harness].
	got, err := rows[0].Propose(harnessInput())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("the harness operator emitted no candidates from a menu with four alternatives")
	}
	for _, c := range got {
		if len(c.Dimensions) != 1 || c.Dimensions[0] != string(variantspec.DimHarness) {
			t.Errorf("candidate declares dimensions %v, want exactly [harness]", c.Dimensions)
		}
	}
}

// TestHarnessProposeSetsOnlyTheHarnessRef — the per-dimension independence FR2 makes mechanical. A
// scaffold swap that also moved a node would produce a candidate whose config_hash records two changes
// and whose rationale describes one.
func TestHarnessProposeSetsOnlyTheHarnessRef(t *testing.T) {
	in := harnessInput()
	in.Base.Nodes["solve"] = variantspec.NodeOverride{
		HarnessRef: refHarnessSingle, ModelRef: "m1", MemoryRef: "mem1",
	}
	got, err := harnessStrategyOp{}.Propose(in)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	for _, c := range got {
		o := c.Spec.Nodes["solve"]
		if o.HarnessRef == refHarnessSingle || o.HarnessRef == "" {
			t.Errorf("the candidate did not change the harness ref (%q)", o.HarnessRef)
		}
		if o.ModelRef != "m1" || o.MemoryRef != "mem1" {
			t.Errorf("the candidate disturbed another dimension: model=%q memory=%q", o.ModelRef, o.MemoryRef)
		}
		// 🚫 Never a rearrangement (decisions.md D-5).
		if len(c.Spec.Order) != len(in.Base.Order) {
			t.Error("the candidate changed the node ordering; a harness never reorders — that is P15's axis")
		}
		for i, id := range c.Spec.Order {
			if id != in.Base.Order[i] {
				t.Errorf("the candidate reordered position %d (%q -> %q)", i, in.Base.Order[i], id)
			}
		}
		if len(c.Spec.HarnessGroups) != 0 {
			t.Error("a node-scoped swap emitted a group harness")
		}
	}
	// And the baseline was never mutated.
	if in.Base.Nodes["solve"].HarnessRef != refHarnessSingle {
		t.Error("Propose mutated the baseline spec")
	}
}

// TestHarnessSwapIsVerificationGated — task 6.2. 🚫 No scaffold swap ships on an unverified opinion. The
// structural assertion: a candidate carries no verdict of its own, and its rationale states the
// trade-off rather than claiming an outcome.
func TestHarnessSwapIsVerificationGated(t *testing.T) {
	got, err := harnessStrategyOp{}.Propose(harnessInput())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	for _, c := range got {
		r := strings.ToLower(c.Rationale)
		// 🔴 A rationale that asserted an outcome would be read as current. The operator is pure and never
		// sees the call site or the eval, so any outcome it stated would be a claim it cannot check.
		for _, forbidden := range []string{"will improve", "improves task_success", "is better", "verified"} {
			if strings.Contains(r, forbidden) {
				t.Errorf("the rationale claims an outcome (%q): %s", forbidden, c.Rationale)
			}
		}
		// And it states the trade-off it CAN state before anything runs: the cost is arithmetic.
		if !strings.Contains(r, "verification") && !strings.Contains(r, "admissibility") {
			t.Errorf("the rationale does not say who decides whether the change is worth it: %s", c.Rationale)
		}
		if c.ExpectedGain <= 0 {
			t.Errorf("candidate has no expected gain, so it cannot be ordered for verification")
		}
	}

	// A heavier candidate must name its cost multiplier before it is chosen.
	for _, c := range got {
		if strings.Contains(c.Rationale, "up to 6 turns") &&
			!strings.Contains(c.Rationale, "multiply") {
			t.Errorf("a 6-turn candidate does not state that it may multiply cost and latency: %s", c.Rationale)
		}
	}
}

// TestHarnessAdmissibleOnlyWhenCostEarned — task 6.3 🔴. The gate admits a heavier scaffold only when the
// measured task_success gain outweighs the cost it added.
func TestHarnessAdmissibleOnlyWhenCostEarned(t *testing.T) {
	base := HarnessMeasurement{TaskSuccess: 0.70, CostUSD: 0.010, LatencyMS: 1000, MaxTurns: 1,
		CaseIDs: []string{"h1", "h2"}}

	t.Run("a heavier scaffold that earns its cost is admitted", func(t *testing.T) {
		// Cost triples (+200%), so the gain must clear 2.0 * 0.05 = 0.10. It delivers 0.15.
		v := AdmitHarnessSwap(HarnessAdmissibility{
			Baseline: base,
			Candidate: HarnessMeasurement{TaskSuccess: 0.85, CostUSD: 0.030, LatencyMS: 2800, MaxTurns: 3,
				CaseIDs: []string{"h1", "h2"}},
			TuningCaseIDs: []string{"c1", "c2"},
		})
		if !v.Admitted {
			t.Fatalf("a scaffold that bought 15 points of task_success for a tripled cost was rejected: %s", v.Reason)
		}
		if !v.Heavier {
			t.Error("a 1→3 turn change was not recorded as heavier")
		}
		if v.RequiredTaskSuccess <= 0 {
			t.Error("the gate admitted a heavier scaffold without computing what it had to clear")
		}
	})

	t.Run("the same gain does not excuse a bigger bill", func(t *testing.T) {
		// Identical quality gain, ten times the cost: the gain must clear 10 * 0.05 = 0.50.
		v := AdmitHarnessSwap(HarnessAdmissibility{
			Baseline: base,
			Candidate: HarnessMeasurement{TaskSuccess: 0.85, CostUSD: 0.110, LatencyMS: 2000, MaxTurns: 8,
				CaseIDs: []string{"h1", "h2"}},
			TuningCaseIDs: []string{"c1"},
		})
		if v.Admitted {
			t.Fatalf("a scaffold that multiplied cost eleven-fold for 15 points of task_success was "+
				"admitted: %s", v.Reason)
		}
	})

	t.Run("latency is judged too, not only cost", func(t *testing.T) {
		// Cost barely moves, latency multiplies. A user waiting ten times as long is paying too.
		v := AdmitHarnessSwap(HarnessAdmissibility{
			Baseline: base,
			Candidate: HarnessMeasurement{TaskSuccess: 0.75, CostUSD: 0.011, LatencyMS: 11000, MaxTurns: 6,
				CaseIDs: []string{"h1", "h2"}},
			TuningCaseIDs: []string{"c1"},
		})
		if v.Admitted {
			t.Fatalf("a scaffold that made the node ten times slower for 5 points was admitted: %s", v.Reason)
		}
		if !strings.Contains(v.Reason, "latency") {
			t.Errorf("the rejection does not name latency as the binding constraint: %s", v.Reason)
		}
	})

	t.Run("a lighter scaffold is judged on quality alone", func(t *testing.T) {
		// Going from 6 turns to 1: cheaper AND better. There is no cost to earn.
		heavy := HarnessMeasurement{TaskSuccess: 0.70, CostUSD: 0.060, LatencyMS: 6000, MaxTurns: 6,
			CaseIDs: []string{"h1"}}
		v := AdmitHarnessSwap(HarnessAdmissibility{
			Baseline: heavy,
			Candidate: HarnessMeasurement{TaskSuccess: 0.72, CostUSD: 0.010, LatencyMS: 1000, MaxTurns: 1,
				CaseIDs: []string{"h1"}},
			TuningCaseIDs: []string{"c1"},
		})
		if !v.Admitted {
			t.Fatalf("a cheaper, faster, better scaffold was rejected: %s", v.Reason)
		}
		if v.Heavier {
			t.Error("a 6→1 turn change was recorded as heavier")
		}
	})
}

// TestHeavierHarnessCostWinRejected — task 6.4 🔴. The gate MUST be able to say no. A gate that never
// rejects is a failing test, so the cases below are the ones it exists for.
func TestHeavierHarnessCostWinRejected(t *testing.T) {
	base := HarnessMeasurement{TaskSuccess: 0.80, CostUSD: 0.010, LatencyMS: 1000, MaxTurns: 1,
		CaseIDs: []string{"h1", "h2"}}

	t.Run("cost rose and quality did not", func(t *testing.T) {
		v := AdmitHarnessSwap(HarnessAdmissibility{
			Baseline: base,
			Candidate: HarnessMeasurement{TaskSuccess: 0.80, CostUSD: 0.040, LatencyMS: 4000, MaxTurns: 4,
				CaseIDs: []string{"h1", "h2"}},
			TuningCaseIDs: []string{"c1"},
		})
		if v.Admitted {
			t.Fatalf("a scaffold that quadrupled the bill and answered no better was admitted: %s", v.Reason)
		}
		if !strings.Contains(v.Reason, "task_success did not improve") {
			t.Errorf("the rejection does not say quality was flat: %s", v.Reason)
		}
	})

	t.Run("quality fell", func(t *testing.T) {
		v := AdmitHarnessSwap(HarnessAdmissibility{
			Baseline: base,
			Candidate: HarnessMeasurement{TaskSuccess: 0.74, CostUSD: 0.040, LatencyMS: 4000, MaxTurns: 4,
				CaseIDs: []string{"h1", "h2"}},
			TuningCaseIDs: []string{"c1"},
		})
		if v.Admitted {
			t.Fatalf("a scaffold that cost more and scored worse was admitted: %s", v.Reason)
		}
	})

	// 🔴 The gate must actually be capable of rejecting a POSITIVE gain. If it were not, every check above
	// would pass for the wrong reason — because the gain happened to be non-positive.
	t.Run("a real but insufficient gain is rejected", func(t *testing.T) {
		v := AdmitHarnessSwap(HarnessAdmissibility{
			Baseline: base,
			Candidate: HarnessMeasurement{TaskSuccess: 0.81, CostUSD: 0.050, LatencyMS: 5000, MaxTurns: 5,
				CaseIDs: []string{"h1", "h2"}},
			TuningCaseIDs: []string{"c1"},
		})
		if v.Admitted {
			t.Fatalf("a scaffold that bought one point of task_success for a five-fold bill was admitted: %s",
				v.Reason)
		}
		if v.DeltaTaskSuccess <= 0 {
			t.Fatal("test setup: the gain was not positive, so this asserts the wrong rejection")
		}
		if v.RequiredTaskSuccess <= v.DeltaTaskSuccess {
			t.Fatalf("the required gain (%v) did not exceed the delivered gain (%v); the rejection came "+
				"from somewhere other than the cost clause", v.RequiredTaskSuccess, v.DeltaTaskSuccess)
		}
	})
}

// TestAdmissibilityHeldOutDisjoint — task 6.4 🔴, the second half. A win measured on the cases the
// proposal was tuned against is overfitting with a confidence interval, and the gate refuses it as an
// EVIDENCE failure rather than a quality one.
func TestAdmissibilityHeldOutDisjoint(t *testing.T) {
	base := HarnessMeasurement{TaskSuccess: 0.60, CostUSD: 0.010, LatencyMS: 1000, MaxTurns: 1,
		CaseIDs: []string{"h1"}}
	// A candidate that would sail through on the merits.
	great := HarnessMeasurement{TaskSuccess: 0.95, CostUSD: 0.011, LatencyMS: 1100, MaxTurns: 3}

	t.Run("an overlapping held-out set is refused", func(t *testing.T) {
		great.CaseIDs = []string{"h1", "c2"}
		v := AdmitHarnessSwap(HarnessAdmissibility{
			Baseline: base, Candidate: great, TuningCaseIDs: []string{"c1", "c2"},
		})
		if v.Admitted {
			t.Fatal("a measurement taken partly on the tuning cases was admitted; a gain measured on the " +
				"cases that produced the proposal is not evidence that it generalizes")
		}
		if !strings.Contains(v.Reason, "c2") {
			t.Errorf("the refusal does not name the leaking case: %s", v.Reason)
		}
	})

	t.Run("an empty held-out set is refused, not waved through", func(t *testing.T) {
		great.CaseIDs = nil
		v := AdmitHarnessSwap(HarnessAdmissibility{
			Baseline: base, Candidate: great, TuningCaseIDs: []string{"c1"},
		})
		if v.Admitted {
			t.Fatal("a candidate with NO held-out measurement was admitted; fail-closed is the whole point " +
				"— otherwise forgetting to supply the set silently admits everything")
		}
	})

	t.Run("a disjoint set is accepted", func(t *testing.T) {
		great.CaseIDs = []string{"h1", "h2"}
		v := AdmitHarnessSwap(HarnessAdmissibility{
			Baseline: base, Candidate: great, TuningCaseIDs: []string{"c1", "c2"},
		})
		if !v.Admitted {
			t.Fatalf("a disjoint, strongly-positive measurement was rejected: %s", v.Reason)
		}
	})
}

// TestHarnessVerdictAlwaysExplainsItself — a rejection a human cannot read is a rejection they will
// route around, and an admission nobody can audit is a decision nobody owns.
func TestHarnessVerdictAlwaysExplainsItself(t *testing.T) {
	cases := []HarnessAdmissibility{
		{Baseline: HarnessMeasurement{MaxTurns: 1, CaseIDs: []string{"h1"}},
			Candidate: HarnessMeasurement{MaxTurns: 3, CaseIDs: []string{"h1"}}},
		{Baseline: HarnessMeasurement{TaskSuccess: 0.5, CostUSD: 1, MaxTurns: 1, CaseIDs: []string{"h1"}},
			Candidate:     HarnessMeasurement{TaskSuccess: 0.9, CostUSD: 1.1, MaxTurns: 2, CaseIDs: []string{"h1"}},
			TuningCaseIDs: []string{"c1"}},
		{Baseline: HarnessMeasurement{TaskSuccess: 0.5, MaxTurns: 4, CaseIDs: []string{"h1"}},
			Candidate:     HarnessMeasurement{TaskSuccess: 0.6, MaxTurns: 1, CaseIDs: []string{"h1"}},
			TuningCaseIDs: []string{"c1"}},
	}
	for i, in := range cases {
		if v := AdmitHarnessSwap(in); strings.TrimSpace(v.Reason) == "" {
			t.Errorf("case %d produced admitted=%v with no reason", i, v.Admitted)
		}
	}
}

// TestCloneOverrideCarriesEveryDimension is the regression guard for a defect P18 found in P17's code:
// cloneOverride silently DROPPED MemoryRef, so a proposal derived from a baseline that bound a memory
// strategy produced a candidate that also un-bound it.
//
// 🔴 That is worse than a missing feature. The candidate's config_hash then differs from the baseline in
// TWO dimensions while its Dimensions() claims one, and the eval attributes the whole delta to the
// dimension the operator named — a measurement that is not merely imprecise but attributed to the wrong
// cause.
//
// 🚫 The test enumerates the FIELDS via a fully-populated override rather than listing the ones someone
// remembered, because "we forgot one" is exactly the failure being guarded against.
func TestCloneOverrideCarriesEveryDimension(t *testing.T) {
	tol := 0.25
	full := variantspec.NodeOverride{
		ModelRef:             "m1",
		PromptRef:            "p1",
		SkillRefs:            []string{"s1", "s2"},
		ContextPolicy:        "ctx1",
		ApplyMode:            variantspec.ApplyBound,
		Bindings:             map[string]variantspec.BindingSource{"slot": {Kind: variantspec.BindLiteral, Value: "v"}},
		ToolSelection:        []string{"t1"},
		ContextDropTolerance: &tol,
		MemoryRef:            "mem1",
		HarnessRef:           "h1",
	}
	got := cloneOverride(full)

	if got.ModelRef != full.ModelRef || got.PromptRef != full.PromptRef ||
		got.ContextPolicy != full.ContextPolicy || got.ApplyMode != full.ApplyMode ||
		got.MemoryRef != full.MemoryRef || got.HarnessRef != full.HarnessRef {
		t.Fatalf("cloneOverride dropped a scalar dimension:\n got %+v\nwant %+v", got, full)
	}
	if len(got.SkillRefs) != 2 || len(got.ToolSelection) != 1 || len(got.Bindings) != 1 {
		t.Fatalf("cloneOverride dropped a collection dimension: %+v", got)
	}
	if got.ContextDropTolerance == nil || *got.ContextDropTolerance != tol {
		t.Fatalf("cloneOverride dropped the drop tolerance: %+v", got.ContextDropTolerance)
	}
	// Copied by value, never aliased: a later mutation of one must not reach the other.
	if got.ContextDropTolerance == full.ContextDropTolerance {
		t.Error("cloneOverride aliased the drop-tolerance pointer")
	}
	got.SkillRefs[0] = "mutated"
	if full.SkillRefs[0] == "mutated" {
		t.Error("cloneOverride aliased the skill refs")
	}

	// 🔴 And the clone must be non-empty in exactly the way the original is: an override that reports
	// isEmpty after cloning a populated one would make a candidate look like an untouched node.
	if cloneSpec(&variantspec.VariantSpec{
		SourceRevision: "r", Order: []string{"n"},
		Nodes: map[string]variantspec.NodeOverride{"n": {HarnessRef: "h1"}},
	}).Nodes["n"].HarnessRef != "h1" {
		t.Error("cloneSpec dropped a harness-only override")
	}
}

// TestCloneSpecCarriesGroupHarnesses — the same defect one level up, with a larger blast radius: a group
// spans several nodes, so dropping it changes the configuration for all of them.
func TestCloneSpecCarriesGroupHarnesses(t *testing.T) {
	base := harnessBase()
	base.Edges = []variantspec.Edge{{FromNodeID: "solve", ToNodeID: "check", Kind: "data"}}
	base.HarnessGroups = []variantspec.HarnessGroup{{
		HarnessRef: refHarnessPlanExe,
		Edges:      []variantspec.Edge{{FromNodeID: "solve", ToNodeID: "check", Kind: "data"}},
	}}

	got := cloneSpec(base)
	if len(got.HarnessGroups) != 1 {
		t.Fatalf("cloneSpec dropped the group harness (%d groups); a node-scoped proposal would then also "+
			"remove a group the author declared, across every node it spans", len(got.HarnessGroups))
	}
	if got.HarnessGroups[0].HarnessRef != refHarnessPlanExe || len(got.HarnessGroups[0].Edges) != 1 {
		t.Fatalf("the cloned group lost content: %+v", got.HarnessGroups[0])
	}
	got.HarnessGroups[0].Edges[0].ToNodeID = "mutated"
	if base.HarnessGroups[0].Edges[0].ToNodeID == "mutated" {
		t.Error("cloneSpec aliased the group's edge slice")
	}
}

// TestAdmissibilitySuite — task 8.4 🔴. The acceptance gate's admissibility half: a cost-only win is
// rejected on held-out cases, and the held-out set is disjoint.
//
// 🔴 The suite asserts the gate can go BOTH ways over one scenario, because a gate that only ever admits
// and a gate that only ever rejects both pass a one-directional test. The same candidate is judged twice
// — once with a bill it earned, once with one it did not — and the two answers must differ.
func TestAdmissibilitySuite(t *testing.T) {
	base := HarnessMeasurement{TaskSuccess: 0.70, CostUSD: 0.010, LatencyMS: 1000, MaxTurns: 1,
		CaseIDs: []string{"h1", "h2", "h3"}}
	tuning := []string{"c1", "c2"}

	earned := AdmitHarnessSwap(HarnessAdmissibility{
		Baseline: base, TuningCaseIDs: tuning,
		Candidate: HarnessMeasurement{TaskSuccess: 0.88, CostUSD: 0.025, LatencyMS: 2400, MaxTurns: 3,
			CaseIDs: []string{"h1", "h2", "h3"}},
	})
	unearned := AdmitHarnessSwap(HarnessAdmissibility{
		Baseline: base, TuningCaseIDs: tuning,
		Candidate: HarnessMeasurement{TaskSuccess: 0.72, CostUSD: 0.090, LatencyMS: 9000, MaxTurns: 9,
			CaseIDs: []string{"h1", "h2", "h3"}},
	})
	if !earned.Admitted {
		t.Errorf("a scaffold that bought 18 points for 2.5x cost was rejected: %s", earned.Reason)
	}
	if unearned.Admitted {
		t.Errorf("a scaffold that bought 2 points for 9x cost was admitted: %s", unearned.Reason)
	}
	if earned.Admitted == unearned.Admitted {
		t.Fatal("the gate returned the same answer for a bill that was earned and one that was not; a " +
			"gate that cannot go both ways is not a gate")
	}

	// Disjointness, over the SAME strongly-positive candidate: evidence failure beats merit.
	leaky := AdmitHarnessSwap(HarnessAdmissibility{
		Baseline: base, TuningCaseIDs: []string{"c1", "h2"},
		Candidate: HarnessMeasurement{TaskSuccess: 0.99, CostUSD: 0.011, LatencyMS: 1100, MaxTurns: 2,
			CaseIDs: []string{"h1", "h2"}},
	})
	if leaky.Admitted {
		t.Error("a candidate measured partly on its own tuning cases was admitted; a gain measured on the " +
			"cases that produced the proposal is overfitting with a confidence interval")
	}
	if !strings.Contains(leaky.Reason, "h2") {
		t.Errorf("the refusal does not name the leaking case: %s", leaky.Reason)
	}
}

// TestHarnessOperatorNeverProposesTheBaseline — a defect found by RUNNING the operator against
// nousresearch/hermes-agent, where every node is implicitly `single-shot` and the first candidate the
// operator emitted resolved to the baseline's own config_hash.
//
// 🔴 A candidate that IS the baseline is a proposal of nothing. It would occupy a verification slot
// measuring a configuration against itself, and its verdict — necessarily a tie — would be recorded as
// evidence about a change that was never made.
//
// 🚫 The identity is not excluded wholesale, and that asymmetry is the point: proposing `single-shot`
// against a node that runs a five-turn loop is often the correct and cheapest answer. It is excluded only
// where it is ALREADY IN FORCE.
func TestHarnessOperatorNeverProposesTheBaseline(t *testing.T) {
	if harnessStrategySingleShot != registry.StrategySingleShot {
		t.Fatalf("harnessStrategySingleShot = %q but registry.StrategySingleShot = %q; the exclusion rule "+
			"would stop firing", harnessStrategySingleShot, registry.StrategySingleShot)
	}

	t.Run("a node with no harness gets no identity candidate", func(t *testing.T) {
		in := harnessInput()
		in.Base.Nodes["solve"] = variantspec.NodeOverride{} // implicitly single-shot, like every real node
		got, err := harnessStrategyOp{}.Propose(in)
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(got) == 0 {
			t.Fatal("the operator emitted nothing at all; excluding the no-op must not exclude the rest")
		}
		for _, c := range got {
			ref := c.Spec.Nodes["solve"].HarnessRef
			if ref == refHarnessSingle {
				t.Error("the operator proposed the identity at a node that already runs it. That candidate " +
					"resolves to the baseline's own config_hash, so it is a proposal of nothing — and its " +
					"verdict, necessarily a tie, would be recorded as evidence about a change never made")
			}
			if ref == "" {
				t.Error("the operator emitted a candidate that clears the harness rather than setting one")
			}
		}
	})

	t.Run("a node running a loop DOES get an identity candidate", func(t *testing.T) {
		in := harnessInput()
		in.Base.Nodes["solve"] = variantspec.NodeOverride{HarnessRef: refHarnessReact}
		got, err := harnessStrategyOp{}.Propose(in)
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		found := false
		for _, c := range got {
			if c.Spec.Nodes["solve"].HarnessRef == refHarnessSingle {
				found = true
			}
		}
		if !found {
			t.Fatal("the operator did not offer the identity to a node running a six-turn loop. 'Run one " +
				"turn' is a real and often correct answer to a scaffold that its cases never needed, and " +
				"it is the cheapest fix on the table")
		}
	})
}
