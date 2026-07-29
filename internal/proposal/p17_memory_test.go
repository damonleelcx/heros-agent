package proposal

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// P17 §8 — the memory operator (Step 8 of the "add an axis" checklist).
//
// The hardest thing to get right here is not the emission — it is the CLAIM. At M20 the transform
// refuses every memory rewrite, so a memory candidate can never be verified, so it can never be a win.
// Half this file exists to make that impossible to accidentally undo.

const (
	refMemScratch = "1111111111111111111111111111111111111111111111111111111111111111"
	refMemSummary = "2222222222222222222222222222222222222222222222222222222222222222"
	refMemVector  = "3333333333333333333333333333333333333333333333333333333333333333"
	refMemNone    = "4444444444444444444444444444444444444444444444444444444444444444"
)

func memoryMenu() Menu {
	return Menu{
		MemoryStrategies: []MemoryChoice{
			{Ref: refMemScratch, Strategy: "scratchpad", Title: "Scratchpad"},
			{Ref: refMemSummary, Strategy: "summary-buffer", Title: "Rolling summary"},
			{Ref: refMemVector, Strategy: "vector-recall", Title: "Vector recall"},
			{Ref: refMemNone, Strategy: "none", Title: "No memory"},
		},
	}
}

func memoryBase() *variantspec.VariantSpec {
	return &variantspec.VariantSpec{
		WorkflowID: "wf-mem", SourceRevision: "rev1",
		Order: []string{"recall", "answer"},
		Nodes: map[string]variantspec.NodeOverride{
			"recall": {MemoryRef: refMemScratch},
		},
	}
}

func memoryInput(signal Signal) OperatorInput {
	return OperatorInput{
		Diagnosis: diagnosis.Diagnosis{DiagID: "d1", NodeID: "recall", Confidence: 0.8,
			EvidenceCaseIDs: []string{"c1", "c2"}, Source: diagnosis.SourceRule},
		Signal:  signal,
		Pattern: patternclassifier.MemoryManagement,
		Base:    memoryBase(),
		Menu:    memoryMenu(),
	}
}

// TestOpMemoryPolicyKindStable — task 8.1. The kind is the wire value stored on a proposal row, so it
// is a stable string. Renaming it orphans every stored proposal.
func TestOpMemoryPolicyKindStable(t *testing.T) {
	if OpMemoryPolicy != "memory_policy_switch" {
		t.Fatalf("OpMemoryPolicy = %q, want memory_policy_switch: the kind is stored on proposal rows, so "+
			"a rename orphans every row already written", OpMemoryPolicy)
	}
	// It is its OWN kind, not a mode of the context switch (decisions.md D2).
	if OpMemoryPolicy == OpContextPolicy {
		t.Fatal("the memory and context operators share a kind; a consumer could not tell a cross-invocation " +
			"change from a within-call one")
	}
	// The signal spellings are the classifier's own failure modes, lifted verbatim. If these drifted, the
	// vocabulary that DETECTS a memory problem and the one that DRIVES a proposal would need a translation
	// layer, and a translation layer is a thing that can be wrong.
	ms, ok := patternclassifier.MetricSetFor(patternclassifier.MemoryManagement)
	if !ok {
		t.Fatal("MemoryManagement has no metric set; the axis would have nothing to be scored against")
	}
	modes := ms.FailureModes
	for _, sig := range []Signal{SignalStaleMemory, SignalContradictoryMemory} {
		found := false
		for _, m := range modes {
			if m == string(sig) {
				found = true
			}
		}
		if !found {
			t.Errorf("signal %q is not one of the classifier's MemoryManagement failure modes %v; the two "+
				"vocabularies must be the same vocabulary", sig, modes)
		}
	}
	// And the local `none` spelling matches the registry's, so memoryStrategiesExcept cannot silently
	// stop excluding the identity strategy.
	if memoryStrategyNone != registry.StrategyNone {
		t.Fatalf("memoryStrategyNone = %q but registry.StrategyNone = %q; the target-exclusion rule would "+
			"stop firing", memoryStrategyNone, registry.StrategyNone)
	}
}

// TestCatalogHasMemoryPolicyRow — task 8.2. A reserved constant with no catalog row is a vocabulary
// entry nothing can ever emit.
func TestCatalogHasMemoryPolicyRow(t *testing.T) {
	var rows []Operator
	for _, op := range DefaultCatalog() {
		if op.Kind() == OpMemoryPolicy {
			rows = append(rows, op)
		}
	}
	if len(rows) == 0 {
		t.Fatal("OpMemoryPolicy is not registered in DefaultCatalog(); a constant with no row can never emit " +
			"a candidate, which would make the whole operator decorative")
	}

	// One row per memory failure mode, because `fires` matches a single Signal per row.
	seen := map[Signal]bool{}
	for _, op := range rows {
		seen[op.HandlesSignal()] = true
		// 🔴 Gated to MemoryManagement. A memory swap on a node that keeps no memory is a change with no
		// mechanism: it would resolve, hash, occupy a verification slot, and measure nothing.
		pats := op.AdmissiblePatterns()
		if len(pats) != 1 || pats[0] != patternclassifier.MemoryManagement {
			t.Errorf("the memory operator's admissible patterns are %v, want exactly [memory_management]", pats)
		}
		// It is signal-driven, not taxonomy-driven: "the agent recalled something stale" is a property of
		// the strategy across turns, not of any one failing case.
		if len(op.Handles()) != 0 {
			t.Errorf("the memory operator claims taxonomy codes %v; it is signal-driven", op.Handles())
		}
	}
	for _, want := range []Signal{SignalStaleMemory, SignalContradictoryMemory} {
		if !seen[want] {
			t.Errorf("no catalog row handles signal %q; that memory failure mode would drive nothing", want)
		}
	}

	// And both rows actually FIRE on their signal against a MemoryManagement node.
	for _, sig := range []Signal{SignalStaleMemory, SignalContradictoryMemory} {
		fired := false
		for _, op := range DefaultCatalog() {
			if fires(op, Target{Signal: sig, Pattern: patternclassifier.MemoryManagement,
				Diagnosis: diagnosis.Diagnosis{NodeID: "recall"}}) && op.Kind() == OpMemoryPolicy {
				fired = true
			}
		}
		if !fired {
			t.Errorf("signal %q fires no memory operator", sig)
		}
	}
}

// TestMemoryPolicyPriorAndOrderHint — task 8.3. A prior and an order hint are ORDERING hints, never
// results. On this axis that distinction is permanent, not temporary: with the transform refusing, the
// prior will never be replaced by a measured verdict, so anything read off it is read off it forever.
func TestMemoryPolicyPriorAndOrderHint(t *testing.T) {
	prior, ok := operatorPrior[OpMemoryPolicy]
	if !ok {
		t.Fatal("OpMemoryPolicy has no operatorPrior; expectedGain would be 0 and every memory candidate " +
			"would sort last for a reason that has nothing to do with memory")
	}
	if prior <= 0 || prior >= 1 {
		t.Errorf("operatorPrior[OpMemoryPolicy] = %v, want a coarse fraction in (0,1)", prior)
	}

	hint, ok := verifyOrderHint[OpMemoryPolicy]
	if !ok {
		t.Fatal("OpMemoryPolicy has no verifyOrderHint")
	}
	if hint < 0 {
		t.Errorf("verifyOrderHint[OpMemoryPolicy] = %d, want a non-negative cost class", hint)
	}
	// 🔴 It is a COST class, so it must not be pushed to the back "because it will be refused anyway".
	// That would encode applicability in a cost hint — two different facts in one number.
	if hint > verifyOrderHint[OpPromptRewrite] {
		t.Errorf("the memory operator sorts after the most expensive operator (prompt rewrite); this table "+
			"answers \"how expensive to verify\", not \"can it be verified\" — the refusal belongs at the "+
			"transform, typed (got %d vs %d)", hint, verifyOrderHint[OpPromptRewrite])
	}
	if VerifyOrderHint(OpMemoryPolicy) != hint {
		t.Errorf("VerifyOrderHint disagrees with the table")
	}
}

// TestMemoryProposalRefusedNotScored — task 8.4 🚫 and task 11.6. The dormancy contract.
func TestMemoryProposalRefusedNotScored(t *testing.T) {
	op := memoryPolicyOp{signal: SignalStaleMemory}
	cands, err := op.Propose(memoryInput(SignalStaleMemory))
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(cands) == 0 {
		t.Fatal("the memory operator emitted nothing against a memory bottleneck with a populated menu; a " +
			"dormant operator still PROPOSES — dormancy is about what may be claimed, not about staying silent")
	}

	for _, c := range cands {
		if c.Operator != OpMemoryPolicy {
			t.Errorf("candidate carries operator %q, want %q", c.Operator, OpMemoryPolicy)
		}
		// It changes ONLY the memory dimension. A proposal that also moved the model would make the
		// eventual measurement un-attributable.
		if len(c.Dimensions) != 1 || c.Dimensions[0] != string(variantspec.DimMemory) {
			t.Errorf("candidate changes dimensions %v, want exactly [memory]", c.Dimensions)
		}
		ov := c.Spec.Nodes["recall"]
		if ov.MemoryRef == "" || ov.MemoryRef == refMemScratch {
			t.Errorf("candidate did not change the node's memory ref (got %q, baseline was %q)",
				ov.MemoryRef, refMemScratch)
		}
		// 🚫 `none` is never a proposed TARGET. Answering "your recall is stale" with "then recall
		// nothing" removes the capability being diagnosed rather than fixing it.
		if ov.MemoryRef == refMemNone {
			t.Error("the operator proposed `none` as a fix for a memory bottleneck; that is the removal of " +
				"the capability being diagnosed, not a repair. A USER may author it — that is their call " +
				"about their own agent — but the platform must not recommend it")
		}
		// The baseline is untouched — a candidate never shares backing storage with Base.
		if memoryBase().Nodes["recall"].MemoryRef != refMemScratch {
			t.Error("Propose mutated the baseline spec")
		}

		// 🔴 The rationale states the refusal IN THE PROPOSAL. A user reading a candidate list must not
		// discover one click later that this one cannot be applied — that is the "refusal discovered late"
		// failure the authoring contract (D7) rejects, and a rationale is the cheapest place to prevent it.
		low := strings.ToLower(c.Rationale)
		if !strings.Contains(low, "refused") {
			t.Errorf("the rationale does not say the change is refused: %q", c.Rationale)
		}
		if !strings.Contains(low, "not applied") && !strings.Contains(low, "no measured") &&
			!strings.Contains(low, "carries no measured result") {
			t.Errorf("the rationale does not say the change is unapplied and unmeasured: %q", c.Rationale)
		}

		// 🚫 And it claims NO result. ExpectedGain is a pre-verification ordering estimate; nothing on the
		// candidate may read as a measured outcome.
		for _, word := range []string{"improve", "gain of", "reduces staleness", "win", "faster", "cheaper"} {
			if strings.Contains(low, word) {
				t.Errorf("the rationale claims an outcome (%q) for a change that cannot be verified at M20: %q",
					word, c.Rationale)
			}
		}
		if c.GuardrailRequired {
			t.Error("a memory candidate requires the held-out downgrade guardrail; that guardrail is about " +
				"model cost/quality ties and has no meaning here")
		}
	}
}

// TestMemorySignalUsesExistingMetricSet — task 8.5. No new metric, no taxonomy change. The improvement
// signal, when it is finally scorable, is the classifier's existing MemoryManagement metric set.
func TestMemorySignalUsesExistingMetricSet(t *testing.T) {
	ms, ok := patternclassifier.MetricSetFor(patternclassifier.MemoryManagement)
	if !ok {
		t.Fatal("MemoryManagement has no metric set")
	}

	if ms.Primary != "memory_hit_rate" {
		t.Errorf("the MemoryManagement primary metric is %q, want memory_hit_rate; P17 adds no metric and "+
			"must not have moved this one", ms.Primary)
	}
	want := []string{"memory_hit_rate", "recall_precision", "staleness", "write_amplification"}
	if len(ms.Metrics) != len(want) {
		t.Fatalf("the MemoryManagement metric set is %v, want %v — P17 adds NO new metric; a number invented "+
			"to flatter the axis is exactly what the phase declines", ms.Metrics, want)
	}
	for i := range want {
		if ms.Metrics[i] != want[i] {
			t.Errorf("metric %d is %q, want %q", i, ms.Metrics[i], want[i])
		}
	}

	// 🚫 And the taxonomy is untouched: memory was already pattern 8, and P17 neither adds a pattern nor
	// bumps the version.
	if patternclassifier.TaxonomySize != 20 {
		t.Errorf("TaxonomySize is %d, want 20; P17 CONSUMES the existing MemoryManagement pattern and adds "+
			"none", patternclassifier.TaxonomySize)
	}
	if !patternclassifier.InTaxonomy(patternclassifier.MemoryManagement) {
		t.Error("MemoryManagement left the taxonomy")
	}
}

// TestMemoryOperatorDeclinesWithoutAMenu is the grounded-or-silent rule this catalog applies everywhere:
// with nothing to swap TO, the operator emits nothing rather than inventing a strategy.
func TestMemoryOperatorDeclinesWithoutAMenu(t *testing.T) {
	in := memoryInput(SignalStaleMemory)
	in.Menu = Menu{}
	cands, err := memoryPolicyOp{signal: SignalStaleMemory}.Propose(in)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("the operator emitted %d candidate(s) with an empty menu; a strategy it invented would be "+
			"a ref that resolves to nothing", len(cands))
	}

	// A menu holding ONLY the strategy the node already binds also yields nothing — proposing the current
	// configuration as a change is a candidate that costs a verification slot and can differ from the
	// baseline in nothing at all.
	in.Menu = Menu{MemoryStrategies: []MemoryChoice{{Ref: refMemScratch, Strategy: "scratchpad"}}}
	cands, err = memoryPolicyOp{signal: SignalStaleMemory}.Propose(in)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("the operator proposed the strategy the node already binds (%d candidates)", len(cands))
	}
}
