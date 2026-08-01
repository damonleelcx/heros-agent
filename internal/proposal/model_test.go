package proposal

import (
	"testing"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P13 §3 — model selection under the held-out quality guardrail.

var fiveSeeds = []int64{1, 2, 3, 4, 5}

// 3.1: a cheaper model whose held-out task-success CI does NOT overlap the incumbent's is inadmissible,
// no matter how much cheaper it is.
func TestDowngradeInadmissibleWhenCINoOverlap(t *testing.T) {
	cases := caseIDs(30)
	// Incumbent succeeds everywhere; the cheaper candidate fails everywhere → disjoint intervals.
	incumbent := successSeries("incumbent", cases, fiveSeeds, 1.0)
	candidate := successSeries("cheaper", cases, fiveSeeds, 0.0)

	res := EvaluateDowngradeGuardrail("cfg-nooverlap", incumbent, candidate, evalstats.DefaultConfig())
	if res.Verdict != GuardrailInadmissible {
		t.Fatalf("want %q, got %q (reason: %s)", GuardrailInadmissible, res.Verdict, res.Reason)
	}
	if res.Verdict.Admissible() {
		t.Error("a non-overlapping downgrade must not be admissible")
	}
	// Cost is irrelevant to admissibility — the guardrail reads only held-out task_success.
	out := ClassifyDowngrade(res, 0.02 /*incumbent*/, 0.0005 /*much cheaper*/)
	if out.Admissible {
		t.Error("lower cost must not rescue an inadmissible downgrade")
	}
}

// 3.3: an ADMITTED downgrade (overlapping intervals, strictly lower cost) is reported as a cost win and
// a quality tie — never a quality win.
func TestDowngradeTieIsCostWinNotQualityWin(t *testing.T) {
	cases := caseIDs(30)
	// Both models score identically on the held-out cases → intervals overlap → statistically a tie.
	incumbent := successSeries("incumbent", cases, fiveSeeds, 1.0)
	candidate := successSeries("cheaper", cases, fiveSeeds, 1.0)

	res := EvaluateDowngradeGuardrail("cfg-overlap", incumbent, candidate, evalstats.DefaultConfig())
	if res.Verdict != GuardrailAdmissible {
		t.Fatalf("want %q, got %q (reason: %s)", GuardrailAdmissible, res.Verdict, res.Reason)
	}

	out := ClassifyDowngrade(res, 0.02, 0.0005)
	if !out.Admissible {
		t.Fatal("an overlapping downgrade must be admissible")
	}
	if !out.CostWin {
		t.Error("a strictly cheaper admitted downgrade must be a cost win")
	}
	if !out.QualityTie {
		t.Error("an admitted downgrade must be reported as a quality tie")
	}
	if out.QualityWin {
		t.Error("a downgrade must NEVER be reported as a quality win")
	}
}

// 3.4: the guardrail is an admissibility predicate on model-downgrade ONLY; upgrade/thinking are
// unchanged. Downgrade candidates are marked GuardrailRequired; upgrade candidates are not.
func TestUpgradeAdmissibilityUnchanged(t *testing.T) {
	if !RequiresHeldOutGuardrail(OpModelDowngrade) {
		t.Error("model-downgrade must require the held-out guardrail")
	}
	for _, op := range []OperatorKind{OpModelUpgrade, OpEnableThinking, OpPromptRewrite, OpParamTune} {
		if RequiresHeldOutGuardrail(op) {
			t.Errorf("%s must not require the downgrade guardrail", op)
		}
	}

	// The downgrade candidate carries the guardrail marker; the upgrade candidate does not.
	e := Engine{Menu: testMenu(), Base: baseSpec()}
	down := e.Propose([]Target{
		{Diagnosis: diagnosis.Diagnosis{NodeID: "rag", EvidenceCaseIDs: []string{"c1"}},
			Signal: SignalCostBottleneck, Pattern: patternclassifier.RetrievalRAG},
	})
	dc := findCandidate(t, down.Candidates, OpModelDowngrade, "rag")
	if !dc.GuardrailRequired {
		t.Error("a downgrade candidate must be marked GuardrailRequired")
	}

	up := e.Propose([]Target{
		{Diagnosis: diag("rag", diagnosis.CauseModelCapabilityGap, "c1"), Pattern: patternclassifier.RetrievalRAG},
	})
	uc := findCandidate(t, up.Candidates, OpModelUpgrade, "rag")
	if uc.GuardrailRequired {
		t.Error("an upgrade candidate must NOT be marked GuardrailRequired (admissibility unchanged)")
	}
}

// 3.8: param tuning has a gain prior (ordering only) and a verify-order hint.
func TestParamTuneHasPrior(t *testing.T) {
	if p, ok := operatorPrior[OpParamTune]; !ok || p <= 0 {
		t.Errorf("param-tune has no positive gain prior (got %v, present=%v)", p, ok)
	}
	if VerifyOrderHint(OpParamTune) == 99 {
		t.Error("param-tune has no verify-order hint")
	}
}

// paramTuneOp emits a bound-mode candidate that selects a param-tuned variant of the current model.
func TestParamTuneEmitsBoundCandidate(t *testing.T) {
	menu := testMenu()
	// A param-tuned variant of the weak model: same provider+model_id, different params.
	tunedRef := "7777777777777777777777777777777777777777777777777777777777777777"
	menu.Models = append(menu.Models, ModelChoice{
		Ref: tunedRef, Provider: "anthropic", ModelID: "haiku", Tier: 1,
		Params: map[string]any{"temperature": 0.1},
	})
	// The current model of the "rag" node is refWeakModel (anthropic/haiku) with no params.
	menu.Models[0].Params = map[string]any{"temperature": 0.7}

	e := Engine{Menu: menu, Base: baseSpec()}
	em := e.Propose([]Target{
		{Diagnosis: diag("rag", diagnosis.CausePromptFormatDrift, "c1"), Pattern: patternclassifier.RetrievalRAG},
	})
	c := findCandidate(t, em.Candidates, OpParamTune, "rag")
	if c.Spec.Nodes["rag"].ModelRef != tunedRef {
		t.Errorf("param-tune must select the tuned variant, got %q", c.Spec.Nodes["rag"].ModelRef)
	}
	if c.Spec.Nodes["rag"].ApplyMode != variantspec.ApplyBound {
		t.Errorf("param-tune must apply in bound mode, got %q", c.Spec.Nodes["rag"].ApplyMode)
	}
}
