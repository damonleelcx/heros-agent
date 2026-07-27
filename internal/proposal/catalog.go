package proposal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// DefaultCatalog is the P5.5 change-operator catalog (design Decision 2's table, made operational).
// It is a dispatch table: each operator declares the diagnosis codes / signals it handles and the
// patterns it is admissible for. Adding an operator is adding a row here — never editing a switch.
func DefaultCatalog() []Operator {
	return []Operator{
		modelUpgradeOp{},
		enableThinkingOp{},
		modelDowngradeOp{},
		promptRewriteOp{},
		instructionHardenOp{},
		fewShotCurateOp{},
		promptCompressOp{},
		redundancyRemoveOp{},
		paramTuneOp{},
		contextPolicyOp{},
		reorderOp{},
		ragTuneOp{},
		addRerankOp{},
		addSkillOp{},
		removeSkillOp{},
		fixSchemaBindingOp{},
		toolPruneOp{},
		toolMinimizeOp{},
		pruneOp{},
	}
}

// ── model-upgrade / enable-extended-thinking (reasoning-heavy node on a weak model) ──────────────

type modelUpgradeOp struct{}

func (modelUpgradeOp) Kind() OperatorKind { return OpModelUpgrade }
func (modelUpgradeOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CauseModelCapabilityGap}
}
func (modelUpgradeOp) HandlesSignal() Signal { return SignalNone }
func (modelUpgradeOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return nil // a capability gap can appear on any reasoning-bearing node
}

func (op modelUpgradeOp) Propose(in OperatorInput) ([]Candidate, error) {
	tier := currentTier(in)
	var out []Candidate
	for _, m := range in.Menu.strongerModels(tier) {
		spec := cloneSpec(in.Base)
		setModel(spec, in.Diagnosis.NodeID, m.Ref)
		out = append(out, newCandidate(op.Kind(), in, in.Diagnosis.NodeID, []string{"model"}, spec,
			fmt.Sprintf("capability gap on %d case(s) → stronger model %s (tier %d)",
				len(in.Diagnosis.EvidenceCaseIDs), m.ModelID, m.Tier)))
	}
	return out, nil
}

type enableThinkingOp struct{}

func (enableThinkingOp) Kind() OperatorKind { return OpEnableThinking }
func (enableThinkingOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CauseModelCapabilityGap}
}
func (enableThinkingOp) HandlesSignal() Signal { return SignalNone }
func (enableThinkingOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return []patternclassifier.Pattern{patternclassifier.Reflection, patternclassifier.Planning,
		patternclassifier.ReasoningTechniques}
}

func (op enableThinkingOp) Propose(in OperatorInput) ([]Candidate, error) {
	tier := currentTier(in)
	var out []Candidate
	for _, m := range in.Menu.thinkingModels(tier) {
		spec := cloneSpec(in.Base)
		setModel(spec, in.Diagnosis.NodeID, m.Ref)
		out = append(out, newCandidate(op.Kind(), in, in.Diagnosis.NodeID, []string{"model"}, spec,
			fmt.Sprintf("reasoning node on %d case(s) → enable extended thinking (%s)", len(in.Diagnosis.EvidenceCaseIDs), m.ModelID)))
	}
	return out, nil
}

// ── model-downgrade (cheap task on an expensive model — a cost bottleneck) ────────────────────────

type modelDowngradeOp struct{}

func (modelDowngradeOp) Kind() OperatorKind                { return OpModelDowngrade }
func (modelDowngradeOp) Handles() []diagnosis.TaxonomyCode { return nil }
func (modelDowngradeOp) HandlesSignal() Signal             { return SignalCostBottleneck }
func (modelDowngradeOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return nil
}

func (op modelDowngradeOp) Propose(in OperatorInput) ([]Candidate, error) {
	tier := currentTier(in)
	var out []Candidate
	for _, m := range in.Menu.cheaperModels(tier) {
		spec := cloneSpec(in.Base)
		setModel(spec, in.NodeID(), m.Ref)
		c := newCandidate(op.Kind(), in, in.NodeID(), []string{"model"}, spec,
			fmt.Sprintf("cost bottleneck → downgrade to cheaper model %s (tier %d, ~$%.4f/run); admissible only "+
				"under the held-out quality guardrail (equal-quality-cheaper tie)", m.ModelID, m.Tier, m.CostPerRun))
		// A downgrade is admissible ONLY under the held-out CI-overlap guardrail (design Decision 4). Mark
		// the candidate so verification evaluates the guardrail before admitting it (task 3.4).
		c.GuardrailRequired = true
		out = append(out, c)
	}
	return out, nil
}

// ── P13 model-parameter tuning (13b): paramTuneOp ─────────────────────────────────────────────────
//
// A parameter tune changes temperature/max-tokens (NOT the model), modeled and hashed through the SAME
// provider_params field a model swap already populates (no new hashed field — task 4.2). It is emitted
// in BOUND apply mode, because that is the only mode that can carry a param change as data (ADR-004,
// design Decision 7): an inline param override has no call-site rewriter and is refused at transform.

type paramTuneOp struct{}

func (paramTuneOp) Kind() OperatorKind { return OpParamTune }
func (paramTuneOp) Handles() []diagnosis.TaxonomyCode {
	// A determinism problem (format drift) is the diagnosis a temperature tune answers; it adds no new
	// taxonomy code.
	return []diagnosis.TaxonomyCode{diagnosis.CausePromptFormatDrift}
}
func (paramTuneOp) HandlesSignal() Signal                           { return SignalNone }
func (paramTuneOp) AdmissiblePatterns() []patternclassifier.Pattern { return nil }

func (op paramTuneOp) Propose(in OperatorInput) ([]Candidate, error) {
	cur := in.Menu.modelByRef(baseOverride(in.Base, in.NodeID()).ModelRef)
	var out []Candidate
	for _, m := range in.Menu.paramTunedVariants(cur) {
		spec := cloneSpec(in.Base)
		setModel(spec, in.NodeID(), m.Ref)
		// Bound apply mode: a param tune materializes as data in the binding document; the same change
		// inline has no call-site rewriter and would be refused at transform (Decision 7).
		setApplyMode(spec, in.NodeID(), variantspec.ApplyBound)
		out = append(out, newCandidate(op.Kind(), in, in.NodeID(), []string{"model"}, spec,
			fmt.Sprintf("format drift → tune provider params %v (bound apply mode)", m.Params)))
	}
	return out, nil
}

// ── prompt-rewrite + format-constraint/schema (prompt/output-contract violation) ─────────────────

type promptRewriteOp struct{}

func (promptRewriteOp) Kind() OperatorKind { return OpPromptRewrite }
func (promptRewriteOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CausePromptFormatDrift}
}
func (promptRewriteOp) HandlesSignal() Signal { return SignalNone }
func (promptRewriteOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return nil // any prompting node can drift its output contract
}

func (op promptRewriteOp) Propose(in OperatorInput) ([]Candidate, error) {
	if in.PromptOptimizer == nil {
		return nil, nil // no optimizer wired: emit nothing rather than a generic rewrite
	}
	req := PromptOptimizeRequest{
		NodeID:         in.Diagnosis.NodeID,
		BasePromptRef:  baseOverride(in.Base, in.Diagnosis.NodeID).PromptRef,
		BasePromptBody: in.BasePromptBody,
		FailingCases:   in.Groundings,
		RequiredFields: in.RequiredFields,
	}
	edit, err := in.PromptOptimizer.Optimize(req)
	if err != nil {
		// ErrUngrounded is not a hard failure of the batch — this operator simply declines to emit an
		// ungrounded rewrite (§2.2). Any other error is real and propagates.
		if err == ErrUngrounded {
			return nil, nil
		}
		return nil, err
	}
	newRef := syntheticPromptRef(edit.NewPromptBody)
	spec := cloneSpec(in.Base)
	setPrompt(spec, in.Diagnosis.NodeID, newRef)
	c := newCandidate(op.Kind(), in, in.Diagnosis.NodeID, []string{"prompt"}, spec,
		fmt.Sprintf("output-contract violation on %d case(s) → grounded prompt rewrite + format constraint",
			len(in.Diagnosis.EvidenceCaseIDs)))
	g := edit.Grounding
	c.Grounding = &g
	c.NewPromptBody = edit.NewPromptBody
	return []Candidate{c}, nil
}

// ── P13 deeper prompt operators (13a): instruction_harden / few_shot_curate / prompt_compress /
// redundancy_remove ───────────────────────────────────────────────────────────────────────────────
//
// Each is a DISTINCT catalog row (design Decision 1 — not a strategy enum inside one operator), so its
// admissibility is its own. They share only proposePromptStrategy's emission mechanics: grounded-or-
// silent, publish a new immutable version, attach grounding, never apply. All four handle the existing
// CausePromptFormatDrift code — P13 adds NO taxonomy code (task 4.2).

// proposePromptStrategy is the shared emission path for the deeper prompt operators (§2). It builds a
// grounded optimize request for the given strategy, declines SILENTLY when ungrounded or when the
// strategy has nothing to change, publishes the rewritten body as a NEW content-addressed prompt
// version (task 2.5), and attaches the grounding bundle (task 2.7). It applies nothing — the candidate
// is returned for verification exactly like every other operator's output (task 2.9).
func proposePromptStrategy(kind OperatorKind, in OperatorInput, strategy PromptStrategy, rationale string) ([]Candidate, error) {
	if in.PromptOptimizer == nil {
		return nil, nil // no optimizer wired: emit nothing rather than a generic rewrite
	}
	req := PromptOptimizeRequest{
		NodeID:         in.Diagnosis.NodeID,
		BasePromptRef:  baseOverride(in.Base, in.Diagnosis.NodeID).PromptRef,
		BasePromptBody: in.BasePromptBody,
		FailingCases:   in.Groundings,
		RequiredFields: in.RequiredFields,
		Strategy:       strategy,
	}
	edit, err := in.PromptOptimizer.Optimize(req)
	if err != nil {
		// grounded-or-silent: neither "no cases to ground on" (ErrUngrounded) nor "grounded but nothing
		// to change" (ErrNoChange) is a batch error — the operator emits no candidate (Decision 2).
		if err == ErrUngrounded || err == ErrNoChange {
			return nil, nil
		}
		return nil, err
	}
	// A rewrite that changed nothing is not a candidate — it would tie itself and spend a verification
	// slot on a no-op.
	if edit.NewPromptBody == in.BasePromptBody {
		return nil, nil
	}
	newRef := syntheticPromptRef(edit.NewPromptBody)
	spec := cloneSpec(in.Base)
	setPrompt(spec, in.Diagnosis.NodeID, newRef)
	c := newCandidate(kind, in, in.Diagnosis.NodeID, []string{"prompt"}, spec, rationale)
	g := edit.Grounding
	c.Grounding = &g
	c.NewPromptBody = edit.NewPromptBody
	return []Candidate{c}, nil
}

type instructionHardenOp struct{}

func (instructionHardenOp) Kind() OperatorKind { return OpInstructionHarden }
func (instructionHardenOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CausePromptFormatDrift}
}
func (instructionHardenOp) HandlesSignal() Signal                           { return SignalNone }
func (instructionHardenOp) AdmissiblePatterns() []patternclassifier.Pattern { return nil }

func (op instructionHardenOp) Propose(in OperatorInput) ([]Candidate, error) {
	return proposePromptStrategy(op.Kind(), in, StrategyInstructionHarden,
		fmt.Sprintf("under-specification on %d case(s) → harden instructions (grounded)", len(in.Diagnosis.EvidenceCaseIDs)))
}

type fewShotCurateOp struct{}

func (fewShotCurateOp) Kind() OperatorKind { return OpFewShotCurate }
func (fewShotCurateOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CausePromptFormatDrift}
}
func (fewShotCurateOp) HandlesSignal() Signal                           { return SignalNone }
func (fewShotCurateOp) AdmissiblePatterns() []patternclassifier.Pattern { return nil }

func (op fewShotCurateOp) Propose(in OperatorInput) ([]Candidate, error) {
	return proposePromptStrategy(op.Kind(), in, StrategyFewShotCurate,
		fmt.Sprintf("dead/duplicate exemplars on %d case(s) → curate few-shot set (grounded)", len(in.Diagnosis.EvidenceCaseIDs)))
}

type promptCompressOp struct{}

func (promptCompressOp) Kind() OperatorKind { return OpPromptCompress }
func (promptCompressOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CausePromptFormatDrift}
}
func (promptCompressOp) HandlesSignal() Signal                           { return SignalNone }
func (promptCompressOp) AdmissiblePatterns() []patternclassifier.Pattern { return nil }

// Propose emits a token-reduced candidate. Note the rationale carries NO token target: a compression
// competes on the full standard metric family (task_success, cost, …) exactly like any other candidate
// (task 2.3), and a shorter-but-worse prompt loses (FR8). Token reduction is a means, never a goal.
func (op promptCompressOp) Propose(in OperatorInput) ([]Candidate, error) {
	return proposePromptStrategy(op.Kind(), in, StrategyCompress,
		fmt.Sprintf("prompt bloat on %d case(s) → compress; competes on verified quality, not token count", len(in.Diagnosis.EvidenceCaseIDs)))
}

type redundancyRemoveOp struct{}

func (redundancyRemoveOp) Kind() OperatorKind { return OpRedundancyRemove }
func (redundancyRemoveOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CausePromptFormatDrift}
}
func (redundancyRemoveOp) HandlesSignal() Signal                           { return SignalNone }
func (redundancyRemoveOp) AdmissiblePatterns() []patternclassifier.Pattern { return nil }

func (op redundancyRemoveOp) Propose(in OperatorInput) ([]Candidate, error) {
	return proposePromptStrategy(op.Kind(), in, StrategyRedundancyRemove,
		fmt.Sprintf("redundant instructions on %d case(s) → de-duplicate (grounded)", len(in.Diagnosis.EvidenceCaseIDs)))
}

// ── context-policy switch / reorder (context overflow / lost-in-middle) ───────────────────────────

type contextPolicyOp struct{}

func (contextPolicyOp) Kind() OperatorKind { return OpContextPolicy }
func (contextPolicyOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CauseContextOverflow, diagnosis.CauseLostInMiddle}
}
func (contextPolicyOp) HandlesSignal() Signal                           { return SignalNone }
func (contextPolicyOp) AdmissiblePatterns() []patternclassifier.Pattern { return nil }

func (op contextPolicyOp) Propose(in OperatorInput) ([]Candidate, error) {
	var out []Candidate
	for _, policy := range []string{"summarization", "sliding_window"} {
		for _, c := range in.Menu.contextPoliciesOfKind(policy) {
			spec := cloneSpec(in.Base)
			setContext(spec, in.Diagnosis.NodeID, c.Ref)
			out = append(out, newCandidate(op.Kind(), in, in.Diagnosis.NodeID, []string{"context"}, spec,
				fmt.Sprintf("%s → switch context policy to %s", in.Diagnosis.TaxonomyCode, policy)))
		}
	}
	return out, nil
}

type reorderOp struct{}

func (reorderOp) Kind() OperatorKind { return OpReorder }
func (reorderOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CauseLostInMiddle}
}
func (reorderOp) HandlesSignal() Signal { return SignalNone }
func (reorderOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return nil
}

func (op reorderOp) Propose(in OperatorInput) ([]Candidate, error) {
	if in.Base == nil || len(in.Base.Order) < 2 {
		return nil, nil
	}
	// Move the diagnosed node earlier (a lost-in-middle node is one buried in a long context; pulling
	// it toward the front of its neighbours is the minimal reorder). The contract gate rejects any
	// reorder that breaks a downstream typed input.
	order := append([]string(nil), in.Base.Order...)
	idx := indexOf(order, in.Diagnosis.NodeID)
	if idx <= 0 {
		return nil, nil
	}
	order[idx-1], order[idx] = order[idx], order[idx-1]
	cand := variantspec.Reorder(in.Base, in.Base.ParentVariantID, order, in.Base.Edges)
	c := newCandidate(op.Kind(), in, in.Diagnosis.NodeID, []string{"order"}, cand,
		"lost-in-middle → move node earlier in the ordering")
	return []Candidate{c}, nil
}

// ── RAG-tune / add-rerank (RAG relevance low) ─────────────────────────────────────────────────────

type ragTuneOp struct{}

func (ragTuneOp) Kind() OperatorKind { return OpRAGTune }
func (ragTuneOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CauseRetrievalMiss}
}
func (ragTuneOp) HandlesSignal() Signal { return SignalNone }
func (ragTuneOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return []patternclassifier.Pattern{patternclassifier.RetrievalRAG}
}

func (op ragTuneOp) Propose(in OperatorInput) ([]Candidate, error) {
	var out []Candidate
	// tune top-k: swap in a larger-window context policy.
	for _, c := range in.Menu.contextPoliciesOfKind("topk") {
		spec := cloneSpec(in.Base)
		setContext(spec, in.Diagnosis.NodeID, c.Ref)
		out = append(out, newCandidate(op.Kind(), in, in.Diagnosis.NodeID, []string{"context"}, spec,
			fmt.Sprintf("retrieval miss → increase top-k (%d)", c.TopK)))
	}
	// swap retriever / embedding.
	for _, kind := range []string{skillKindRetriever, skillKindEmbedding} {
		for _, s := range in.Menu.skillsOfKind(kind) {
			spec := cloneSpec(in.Base)
			swapSkillOfRole(spec, in.Diagnosis.NodeID, s.Ref, kind, in.Menu)
			out = append(out, newCandidate(op.Kind(), in, in.Diagnosis.NodeID, []string{"skills"}, spec,
				fmt.Sprintf("retrieval miss → swap %s to %s", kind, s.Name)))
		}
	}
	return out, nil
}

type addRerankOp struct{}

func (addRerankOp) Kind() OperatorKind { return OpAddRerank }
func (addRerankOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CauseRetrievalMiss}
}
func (addRerankOp) HandlesSignal() Signal { return SignalNone }
func (addRerankOp) AdmissiblePatterns() []patternclassifier.Pattern {
	// The load-bearing gate (§1.5): add-rerank is admissible ONLY on a Retrieval (RAG) node. It is
	// nonsensical on a Routing node and must never be emitted there.
	return []patternclassifier.Pattern{patternclassifier.RetrievalRAG}
}

func (op addRerankOp) Propose(in OperatorInput) ([]Candidate, error) {
	var out []Candidate
	for _, s := range in.Menu.skillsOfKind(skillKindRerank) {
		if hasSkill(in.Base, in.Diagnosis.NodeID, s.Ref) {
			continue // already reranking with this skill
		}
		spec := cloneSpec(in.Base)
		addSkill(spec, in.Diagnosis.NodeID, s.Ref)
		out = append(out, newCandidate(op.Kind(), in, in.Diagnosis.NodeID, []string{"skills"}, spec,
			fmt.Sprintf("retrieval miss → add rerank skill %s", s.Name)))
	}
	return out, nil
}

// ── add-skill / fix-schema-binding (missing / erroring tool) ──────────────────────────────────────

type addSkillOp struct{}

func (addSkillOp) Kind() OperatorKind { return OpAddSkill }
func (addSkillOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CauseToolSchemaMismatch}
}
func (addSkillOp) HandlesSignal() Signal { return SignalNone }
func (addSkillOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return []patternclassifier.Pattern{patternclassifier.ToolUse}
}

func (op addSkillOp) Propose(in OperatorInput) ([]Candidate, error) {
	var out []Candidate
	for _, s := range in.Menu.skillsOfKind(skillKindTool) {
		if hasSkill(in.Base, in.Diagnosis.NodeID, s.Ref) {
			continue
		}
		spec := cloneSpec(in.Base)
		addSkill(spec, in.Diagnosis.NodeID, s.Ref)
		out = append(out, newCandidate(op.Kind(), in, in.Diagnosis.NodeID, []string{"skills"}, spec,
			fmt.Sprintf("missing/erroring tool → add skill %s from registry", s.Name)))
	}
	return out, nil
}

type fixSchemaBindingOp struct{}

func (fixSchemaBindingOp) Kind() OperatorKind { return OpFixSchemaBinding }
func (fixSchemaBindingOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CauseToolSchemaMismatch}
}
func (fixSchemaBindingOp) HandlesSignal() Signal { return SignalNone }
func (fixSchemaBindingOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return []patternclassifier.Pattern{patternclassifier.ToolUse}
}

func (op fixSchemaBindingOp) Propose(in OperatorInput) ([]Candidate, error) {
	// A schema-binding fix swaps the erroring tool skill for a corrected registry entry of the same
	// role. It is expressed as a skill_ref swap, gated on the typed contract like every other change.
	var out []Candidate
	current := baseOverride(in.Base, in.Diagnosis.NodeID)
	for _, s := range in.Menu.skillsOfKind(skillKindTool) {
		if hasSkillRef(current.SkillRefs, s.Ref) {
			continue
		}
		if len(current.SkillRefs) == 0 {
			continue // nothing to correct; that is add-skill's job
		}
		spec := cloneSpec(in.Base)
		replaceFirstSkill(spec, in.Diagnosis.NodeID, s.Ref)
		out = append(out, newCandidate(op.Kind(), in, in.Diagnosis.NodeID, []string{"skills"}, spec,
			fmt.Sprintf("tool schema mismatch → correct binding to %s", s.Name)))
	}
	return out, nil
}

// ── P14 remove-skill (an erroring or never-exercised skill) ───────────────────────────────────────
//
// The mirror of addSkillOp, and the operator that makes the skill axis a full one: P14 promises
// add / remove / rerank, each verification-gated, and without a removal the axis can only ever grow.
//
// # Grounded or silent
//
// It emits ONLY for a skill the recorded usage says errored or was never exercised. Unbinding a
// capability because a diagnosis fired somewhere on the node would be a change on no evidence — the
// same failure the prompt operators decline with ErrUngrounded — and the cost of being wrong is not
// symmetric: an unnecessary ADD wastes tokens, an unnecessary REMOVE takes away something the workflow
// needed and the eval set may not cover.
//
// 🔴 It is a PROPOSAL, like every other row. A removal that regresses the verified score does not ship
// (task 3.2); the operator's prior only orders it for verification.

type removeSkillOp struct{}

func (removeSkillOp) Kind() OperatorKind { return OpRemoveSkill }
func (removeSkillOp) Handles() []diagnosis.TaxonomyCode {
	return []diagnosis.TaxonomyCode{diagnosis.CauseToolSchemaMismatch}
}
func (removeSkillOp) HandlesSignal() Signal { return SignalNone }
func (removeSkillOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return []patternclassifier.Pattern{patternclassifier.ToolUse}
}

func (op removeSkillOp) Propose(in OperatorInput) ([]Candidate, error) {
	if !in.Usage.Recorded() {
		return nil, nil // no evidence: decline silently rather than unbind on a hunch
	}
	current := baseOverride(in.Base, in.NodeID())
	var out []Candidate
	for _, ref := range current.SkillRefs {
		name := in.Menu.skillNameByRef(ref)
		if name == "" {
			// A ref the menu cannot name is a ref this operator cannot ground a decision about. Skipping
			// it is the fail-closed direction: the alternative is removing a skill whose usage we never
			// actually looked up.
			continue
		}
		why := ""
		switch {
		case in.Usage.erroring(name):
			why = "errored during the eval"
		case !in.Usage.exercised(name):
			why = "was never exercised by the eval set"
		default:
			continue // exercised and clean — leave it alone
		}
		spec := cloneSpec(in.Base)
		removeSkill(spec, in.NodeID(), ref)
		out = append(out, newCandidate(op.Kind(), in, in.NodeID(), []string{"skills"}, spec,
			fmt.Sprintf("skill %s %s → unbind it (verified before it ships)", name, why)))
	}
	return out, nil
}

// ── P14 tool pruning / minimization (14b) ─────────────────────────────────────────────────────────
//
// Both land in `NodeOverride.ToolSelection` — the KEPT set — and both are validated at resolve against
// the node's DISCOVERED tool set (D-14.2), so an operator can never propose keeping a tool the node does
// not offer: the spec would be rejected before a diff exists.
//
// 🚫 Neither introduces a metric. The win is fewer `eval_tokens_total` (a declared tool costs tokens on
// every call) and, where a pruned tool was erroring, a lower `tool_error_rate` — both already in the
// harness's standard family. A bespoke "tools_pruned" number would measure the change rather than its
// effect, and a change that measures itself always looks like a win.
//
// Neither declares AdmissiblePatterns. That is deliberate and is not laziness: the grounding is the
// recorded USAGE, not the pattern label, and a RAG or Planning node that happens to offer tools has
// exactly the same unused-tool cost as one the classifier labelled Tool Use. Gating on the label would
// make the saving depend on a classification that has nothing to do with it.

type toolPruneOp struct{}

func (toolPruneOp) Kind() OperatorKind                { return OpToolPrune }
func (toolPruneOp) Handles() []diagnosis.TaxonomyCode { return nil }
func (toolPruneOp) HandlesSignal() Signal             { return SignalUnusedTools }
func (toolPruneOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return nil
}

// Propose emits ONE candidate PER unused tool, rather than one candidate dropping them all.
//
// One-per-tool is the smaller blast radius (design Q4's "a minimal change beats a maximal one when both
// clear the bar") and it is what makes the verdict attributable: if three tools are dropped together
// and the score moves, nothing says which drop moved it. The all-at-once variant is a separate operator
// (tool-minimize below) precisely so the two are scored as the different bets they are.
func (op toolPruneOp) Propose(in OperatorInput) ([]Candidate, error) {
	if !in.Usage.Recorded() || len(in.Usage.Discovered) == 0 {
		return nil, nil // no evidence: never prune a tool set nobody looked at
	}
	var out []Candidate
	for _, tool := range in.Usage.Discovered {
		if in.Usage.exercised(tool) {
			continue
		}
		kept := keepAllBut(in.Usage.Discovered, tool)
		if len(kept) == len(in.Usage.Discovered) {
			continue
		}
		spec := cloneSpec(in.Base)
		setToolSelection(spec, in.NodeID(), kept)
		out = append(out, newCandidate(op.Kind(), in, in.NodeID(), []string{"tools"}, spec,
			fmt.Sprintf("tool %s is declared but the eval set never calls it → prune it (fewer declared-tool "+
				"tokens; scored on the standard metric family, not on the saving)", tool)))
	}
	return out, nil
}

type toolMinimizeOp struct{}

func (toolMinimizeOp) Kind() OperatorKind                { return OpToolMinimize }
func (toolMinimizeOp) Handles() []diagnosis.TaxonomyCode { return nil }
func (toolMinimizeOp) HandlesSignal() Signal             { return SignalUnusedTools }
func (toolMinimizeOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return nil
}

// Propose emits the MINIMAL set: exactly the tools the eval set exercised.
//
// It is emitted alongside the individual prunes, not instead of them, and the harness scores all of
// them against the full-tool-set baseline. That is the honest way to ask "how far can this go": the
// minimal set is a hypothesis about the whole set, the single prunes are hypotheses about one tool each,
// and measurement — not the operator — decides which survives.
func (op toolMinimizeOp) Propose(in OperatorInput) ([]Candidate, error) {
	if !in.Usage.Recorded() || len(in.Usage.Discovered) == 0 {
		return nil, nil
	}
	var kept []string
	for _, tool := range in.Usage.Discovered {
		if in.Usage.exercised(tool) {
			kept = append(kept, tool)
		}
	}
	// Nothing to minimize when every declared tool is already used.
	if len(kept) == len(in.Usage.Discovered) {
		return nil, nil
	}
	// 🔴 An empty minimal set is NOT emitted. "The eval set exercised no tool" is at least as likely to
	// mean the eval set does not cover this node's tool use as it is to mean the node needs no tools, and
	// the two are indistinguishable from here. Unbinding every tool on that evidence is the asymmetric
	// mistake; the per-tool prunes above still offer the incremental version.
	if len(kept) == 0 {
		return nil, nil
	}
	spec := cloneSpec(in.Base)
	setToolSelection(spec, in.NodeID(), kept)
	return []Candidate{newCandidate(op.Kind(), in, in.NodeID(), []string{"tools"}, spec,
		fmt.Sprintf("%d of %d declared tools are exercised → propose the minimal set %v, scored against the "+
			"full set", len(kept), len(in.Usage.Discovered), kept))}, nil
}

// keepAllBut returns the discovered set minus one tool, preserving IR order (the selection is
// canonicalized at resolve, so the order here is only for a legible rationale).
func keepAllBut(all []string, drop string) []string {
	out := make([]string, 0, len(all))
	for _, t := range all {
		if t != drop {
			out = append(out, t)
		}
	}
	return out
}

func setToolSelection(s *variantspec.VariantSpec, node string, kept []string) {
	o := s.Nodes[node]
	o.ToolSelection = append([]string(nil), kept...)
	s.Nodes[node] = o
}

// ── prune (redundant node) ────────────────────────────────────────────────────────────────────────

type pruneOp struct{}

func (pruneOp) Kind() OperatorKind                { return OpPrune }
func (pruneOp) Handles() []diagnosis.TaxonomyCode { return nil }
func (pruneOp) HandlesSignal() Signal             { return SignalRedundantNode }
func (pruneOp) AdmissiblePatterns() []patternclassifier.Pattern {
	return nil
}

func (op pruneOp) Propose(in OperatorInput) ([]Candidate, error) {
	node := in.NodeID()
	if in.Base == nil || indexOf(in.Base.Order, node) < 0 {
		return nil, nil
	}
	spec := pruneNode(in.Base, node)
	c := newCandidate(op.Kind(), in, node, []string{"order"}, spec,
		"redundant node → prune it and rewire its neighbours")
	return []Candidate{c}, nil
}

// ── shared derivation helpers ────────────────────────────────────────────────────────────────────

func newCandidate(kind OperatorKind, in OperatorInput, nodeID string, dims []string, spec *variantspec.VariantSpec, rationale string) Candidate {
	return Candidate{
		Operator:        kind,
		DiagID:          in.Diagnosis.DiagID,
		NodeID:          nodeID,
		Pattern:         in.Pattern,
		Dimensions:      dims,
		Spec:            spec,
		Rationale:       rationale,
		EvidenceCaseIDs: append([]string(nil), in.Diagnosis.EvidenceCaseIDs...),
		ExpectedGain:    expectedGain(kind, in),
	}
}

func setModel(s *variantspec.VariantSpec, node, ref string) {
	o := s.Nodes[node]
	o.ModelRef = ref
	s.Nodes[node] = o
}

func setPrompt(s *variantspec.VariantSpec, node, ref string) {
	o := s.Nodes[node]
	o.PromptRef = ref
	s.Nodes[node] = o
}

func setApplyMode(s *variantspec.VariantSpec, node string, mode variantspec.ApplyMode) {
	o := s.Nodes[node]
	o.ApplyMode = mode
	s.Nodes[node] = o
}

func setContext(s *variantspec.VariantSpec, node, ref string) {
	o := s.Nodes[node]
	o.ContextPolicy = ref
	s.Nodes[node] = o
}

func addSkill(s *variantspec.VariantSpec, node, ref string) {
	o := s.Nodes[node]
	o.SkillRefs = append(append([]string(nil), o.SkillRefs...), ref)
	s.Nodes[node] = o
}

// removeSkill unbinds one skill ref at a node, PRESERVING the order of the rest. Order is
// identity-bearing (ResolvedNode.SkillRefs is never sorted), so a removal that also reordered the
// survivors would be two changes wearing one rationale, and verification could not tell which of them
// moved the score.
func removeSkill(s *variantspec.VariantSpec, node, ref string) {
	o := s.Nodes[node]
	refs := make([]string, 0, len(o.SkillRefs))
	for _, r := range o.SkillRefs {
		if r != ref {
			refs = append(refs, r)
		}
	}
	o.SkillRefs = refs
	s.Nodes[node] = o
}

func replaceFirstSkill(s *variantspec.VariantSpec, node, ref string) {
	o := s.Nodes[node]
	refs := append([]string(nil), o.SkillRefs...)
	if len(refs) > 0 {
		refs[0] = ref
	}
	o.SkillRefs = refs
	s.Nodes[node] = o
}

// swapSkillOfRole replaces a skill of the given role at the node with ref. Because the spec stores
// skill refs as an opaque list (roles live in the registry), a swap here appends when there is no
// existing skill to replace; the contract gate is what keeps the result valid.
func swapSkillOfRole(s *variantspec.VariantSpec, node, ref, role string, menu Menu) {
	o := s.Nodes[node]
	refs := append([]string(nil), o.SkillRefs...)
	roleRefs := map[string]bool{}
	for _, sc := range menu.skillsOfKind(role) {
		roleRefs[sc.Ref] = true
	}
	replaced := false
	for i, r := range refs {
		if roleRefs[r] {
			refs[i] = ref
			replaced = true
			break
		}
	}
	if !replaced {
		refs = append(refs, ref)
	}
	o.SkillRefs = refs
	s.Nodes[node] = o
}

func hasSkill(s *variantspec.VariantSpec, node, ref string) bool {
	if s == nil {
		return false
	}
	return hasSkillRef(s.Nodes[node].SkillRefs, ref)
}

func hasSkillRef(refs []string, ref string) bool {
	for _, r := range refs {
		if r == ref {
			return true
		}
	}
	return false
}

func baseOverride(s *variantspec.VariantSpec, node string) variantspec.NodeOverride {
	if s == nil {
		return variantspec.NodeOverride{}
	}
	return s.Nodes[node]
}

// pruneNode removes node from the spec's ordering and rewires each predecessor directly to each
// successor, dropping every edge touching the node. The contract gate validates the result.
func pruneNode(base *variantspec.VariantSpec, node string) *variantspec.VariantSpec {
	spec := cloneSpec(base)
	spec.ParentVariantID = base.ParentVariantID
	// drop from order
	order := make([]string, 0, len(spec.Order))
	for _, n := range spec.Order {
		if n != node {
			order = append(order, n)
		}
	}
	spec.Order = order
	// collect preds/succs and rewire
	var preds, succs []string
	kept := make([]variantspec.Edge, 0, len(spec.Edges))
	for _, e := range spec.Edges {
		switch node {
		case e.ToNodeID:
			preds = append(preds, e.FromNodeID)
		case e.FromNodeID:
			succs = append(succs, e.ToNodeID)
		default:
			kept = append(kept, e)
		}
	}
	sort.Strings(preds)
	sort.Strings(succs)
	for _, p := range preds {
		for _, sc := range succs {
			kept = append(kept, variantspec.Edge{FromNodeID: p, ToNodeID: sc, Kind: "data"})
		}
	}
	spec.Edges = kept
	delete(spec.Nodes, node)
	return spec
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

// currentTier reports the capability tier of the node's current model, resolved against the menu. An
// unknown current model is tier 0, so any menu model counts as "stronger" — the conservative default
// that lets an upgrade be proposed rather than silently suppressed.
func currentTier(in OperatorInput) int {
	ref := baseOverride(in.Base, in.NodeID()).ModelRef
	for _, m := range in.Menu.Models {
		if m.Ref == ref {
			return m.Tier
		}
	}
	if in.Bottleneck != nil || in.Signal == SignalCostBottleneck {
		// For a cost-bottleneck downgrade with no discoverable current model, assume the node runs the
		// top tier available so every cheaper model is a candidate.
		return maxTier(in.Menu) + 1
	}
	return 0
}

func maxTier(m Menu) int {
	t := 0
	for _, c := range m.Models {
		if c.Tier > t {
			t = c.Tier
		}
	}
	return t
}

// syntheticPromptRef derives a deterministic 64-hex ref for a rewritten prompt body, so a candidate's
// PromptRef is stable across identical rewrites. The compiler registers the body under this ref.
func syntheticPromptRef(body string) string {
	sum := sha256.Sum256([]byte("prompt-rewrite\x00" + body))
	return hex.EncodeToString(sum[:])
}
