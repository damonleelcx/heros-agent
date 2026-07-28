package proposal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

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
		// P15 (15a): the wiring axis's third operator. One ROW, exactly like the other eighteen — the
		// merge is not a mode of pruneOp, because "drop a dead node" and "fuse two nodes into one" answer
		// different admissibility questions and produce different edges (decisions.md D-1).
		mergeOp{},
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

// freeReorderBudget bounds how many data-independent adjacent pairs one pass proposes.
//
// The reorder space is factorial and the verification budget is not: every candidate costs a held-out
// eval run, so proposing every permutation would spend the budget on the axis with the LOWEST operator
// prior. Four is a working bound, not a law — and when it truncates, the rationale says so (no silent
// cap), because a user reading "3 reorders proposed" on a nine-node graph would otherwise believe the
// space was exhausted.
const freeReorderBudget = 4

// Propose emits the lost-in-middle swap AND bounded free rewiring of data-independent neighbours
// (P15 task 3.2).
//
// The three shapes it emits, all in the Order/Edges space and nothing else:
//
//	SWAP (diagnosed)      move the diagnosed node one step earlier — the minimal answer to lost-in-middle.
//	SWAP (independent)    exchange an adjacent pair with NO data edge between them. Their relative order
//	                      is arbitrary today; which one runs first is a real, measurable choice.
//	PARALLELIZE           drop the CONTROL edge that sequences a data-independent pair. Two nodes with no
//	                      data dependency and no control edge between them are, by the executor's reading
//	                      of the graph, free to run concurrently — so "mark it parallelizable" needs no
//	                      new field and no new dimension: it is the ABSENCE of a sequencing edge, which
//	                      Edges already expresses and config_hash already covers.
//
// 🔴 Every one of them is a PROPOSAL and every one goes through the same coherence gate. A reorder the
// gate rejects yields no runnable spec, so no diff and no PR (design Decision 3) — this operator is
// deliberately free to propose an ordering that turns out to be incoherent, because the gate, not the
// operator, is where that is decided.
func (op reorderOp) Propose(in OperatorInput) ([]Candidate, error) {
	if in.Base == nil || len(in.Base.Order) < 2 {
		return nil, nil
	}
	parentID := parentVariantID(in)
	var out []Candidate
	seen := map[string]bool{} // dedupe by resulting wiring: two operators may reach the same graph

	emit := func(node string, order []string, edges []variantspec.Edge, rationale string) {
		key := wiringKey(order, edges)
		if seen[key] || key == wiringKey(in.Base.Order, in.Base.Edges) {
			return // a candidate identical to the baseline is a no-op that would tie itself
		}
		seen[key] = true
		out = append(out, newCandidate(op.Kind(), in, node, []string{"order"},
			variantspec.Reorder(in.Base, parentID, order, edges), rationale))
	}

	// 1. The diagnosed node moves one step earlier (unchanged behaviour).
	if idx := indexOf(in.Base.Order, in.Diagnosis.NodeID); idx > 0 {
		order := append([]string(nil), in.Base.Order...)
		order[idx-1], order[idx] = order[idx], order[idx-1]
		emit(in.Diagnosis.NodeID, order, in.Base.Edges, "lost-in-middle → move node earlier in the ordering")
	}

	// 2/3. Free rewiring over data-independent adjacent pairs, bounded and with the bound stated.
	pairs := independentAdjacentPairs(in.Base)
	budgeted := pairs
	if len(budgeted) > freeReorderBudget {
		budgeted = budgeted[:freeReorderBudget]
	}
	bound := ""
	if len(pairs) > len(budgeted) {
		bound = fmt.Sprintf(" (bounded: %d of %d independent adjacent pairs proposed this pass)",
			len(budgeted), len(pairs))
	}
	for _, p := range budgeted {
		order := append([]string(nil), in.Base.Order...)
		order[p.i], order[p.i+1] = order[p.i+1], order[p.i]
		emit(p.first, order, in.Base.Edges, fmt.Sprintf(
			"%s and %s have no data dependency → exchange their order; which of two independent nodes runs "+
				"first is a measurable choice, not a fact about the code%s", p.first, p.second, bound))

		if p.control {
			// The pair is sequenced only by a control edge. Dropping it leaves both nodes in the graph with
			// no path between them — the wiring-level statement of "these may run in parallel".
			emit(p.first, in.Base.Order, withoutControlEdge(in.Base.Edges, p.first, p.second), fmt.Sprintf(
				"%s and %s are sequenced by a control edge but exchange no data → drop the sequencing edge so "+
					"they may run in parallel%s", p.first, p.second, bound))
		}
	}
	return out, nil
}

// independentPair is one adjacent (order[i], order[i+1]) pair with no DATA edge between them, and
// whether a control edge is what sequences them.
type independentPair struct {
	i             int
	first, second string
	control       bool
}

// independentAdjacentPairs walks the order once and returns every adjacent pair the graph does not
// make data-dependent, in order — so the result, and therefore the emitted candidates, are a pure
// function of the base spec (task 3.4).
func independentAdjacentPairs(base *variantspec.VariantSpec) []independentPair {
	var out []independentPair
	for i := 0; i+1 < len(base.Order); i++ {
		a, b := base.Order[i], base.Order[i+1]
		var data, control bool
		for _, e := range base.Edges {
			touches := (e.FromNodeID == a && e.ToNodeID == b) || (e.FromNodeID == b && e.ToNodeID == a)
			if !touches {
				continue
			}
			if e.Kind == "data" {
				data = true
			} else {
				control = true
			}
		}
		if data {
			continue // a data dependency is a fact about the code, not an ordering choice
		}
		out = append(out, independentPair{i: i, first: a, second: b, control: control})
	}
	return out
}

func withoutControlEdge(edges []variantspec.Edge, a, b string) []variantspec.Edge {
	out := make([]variantspec.Edge, 0, len(edges))
	for _, e := range edges {
		if e.Kind != "data" && ((e.FromNodeID == a && e.ToNodeID == b) || (e.FromNodeID == b && e.ToNodeID == a)) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// wiringKey renders an ordering + edge set as a comparable string, so two candidates that denote the
// same graph are recognised as one. It is a de-duplication key, NOT an identity: config_hash is the
// identity, and it is computed at resolve from the same two fields.
func wiringKey(order []string, edges []variantspec.Edge) string {
	var b strings.Builder
	for _, n := range order {
		b.WriteString(n)
		b.WriteByte('>')
	}
	b.WriteByte('|')
	rendered := make([]string, 0, len(edges))
	for _, e := range edges {
		rendered = append(rendered, e.FromNodeID+"-"+e.Kind+"->"+e.ToNodeID)
	}
	sort.Strings(rendered) // an edge SET: two specs listing the same edges in another order are one graph
	for _, r := range rendered {
		b.WriteString(r)
		b.WriteByte(';')
	}
	return b.String()
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

// Propose emits one candidate per retrieval-parameter entry the menu offers for this node.
//
// P16 task 6.2 widens the knobs from top-k alone to top-k, CHUNK SIZE, and EMBEDDING MODEL — the three
// parameters that actually decide what a retriever returns. They are proposed as registry entries, not
// as inline params, exactly like every other dimension: the operator references a pinned entry by
// version_id, so what was tried is re-derivable from `config_hash` months later.
//
// 🔴 Each candidate's rationale names WHICH knob moved and from what. That is not cosmetic: verification
// attributes a measured delta to a candidate, and a rationale that said only "retrieval miss → tune"
// would leave the ledger recording that *something* about retrieval helped, which is not a finding
// anyone can act on. An entry identical to what the node already pins is skipped — it would spend a
// verification slot proving a no-op.
func (op ragTuneOp) Propose(in OperatorInput) ([]Candidate, error) {
	var out []Candidate
	cur, _ := in.Menu.contextByRef(baseOverride(in.Base, in.Diagnosis.NodeID).ContextPolicy)
	for _, c := range in.Menu.contextPoliciesOfKind("topk") {
		delta := retrievalDelta(cur, c)
		if delta == "" {
			continue // identical to the node's current retrieval configuration: nothing to verify
		}
		spec := cloneSpec(in.Base)
		setContext(spec, in.Diagnosis.NodeID, c.Ref)
		out = append(out, newCandidate(op.Kind(), in, in.Diagnosis.NodeID, []string{"context"}, spec,
			fmt.Sprintf("retrieval miss → %s", delta)))
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

// retrievalDelta describes, in words a reviewer and the verified-delta ledger both read, what a
// candidate retrieval entry changes relative to the one the node pins now. Empty when nothing changes.
//
// The knobs are listed in a fixed order so two runs of the same proposal produce the same sentence —
// a rationale that varied by map ordering would make identical candidates look different in the UI.
func retrievalDelta(cur, next ContextChoice) string {
	var parts []string
	if next.TopK != 0 && next.TopK != cur.TopK {
		parts = append(parts, fmt.Sprintf("top-k %s→%d", zeroAsUnset(cur.TopK), next.TopK))
	}
	if next.ChunkSize != 0 && next.ChunkSize != cur.ChunkSize {
		parts = append(parts, fmt.Sprintf("chunk size %s→%d", zeroAsUnset(cur.ChunkSize), next.ChunkSize))
	}
	if next.EmbeddingModel != "" && next.EmbeddingModel != cur.EmbeddingModel {
		from := cur.EmbeddingModel
		if from == "" {
			from = "(unset)"
		}
		parts = append(parts, fmt.Sprintf("embedding %s→%s", from, next.EmbeddingModel))
	}
	return strings.Join(parts, ", ")
}

func zeroAsUnset(v int) string {
	if v == 0 {
		return "(unset)"
	}
	return strconv.Itoa(v)
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
	spec := pruneNode(in.Base, node, parentVariantID(in))
	c := newCandidate(op.Kind(), in, node, []string{"order"}, spec,
		"redundant node → prune it and rewire its neighbours")
	return []Candidate{c}, nil
}

// ── P15 merge (a redundant node fused into its adjacent neighbour) ───────────────────────────────
//
// `OpMerge` has existed as a reserved constant with a gain prior since P5.5 and had no implementation;
// this is it. Its semantics are a ONE-WAY DOOR (decisions.md D-1): the moment a stored proposal row
// names `merge`, every future reader — the compare view, the verified-delta ledger, a re-run months
// later — depends on what that word meant. So the scope is fixed narrow and stays narrow:
//
//	ADJACENT PAIR ONLY. One survivor, one absorbed. The absorbed node leaves `Order`; its inbound edges
//	retarget the survivor and its outbound edges re-source from it — a MECHANICAL rewire, not a re-plan.
//
// 🚫 It does not fuse a non-adjacent set, and it does not fuse three nodes at once. The coherence gate
// would happily admit such a merge (it only checks I/O contracts), so the gate alone does not bound
// reviewability — the operator's scope must. A chain of three redundant calls merges pairwise across
// proposal iterations, where each intermediate step is separately gated, separately scored, and
// separately reviewable, instead of one unfalsifiable n-ary claim.
//
// 🔴 It is a PROPOSAL. A merge that reads redundant but scores worse on held-out data does not ship
// (task 6.3) — "the second call was correcting the first" is a real and common shape, and only
// verification can tell it apart from genuine redundancy.

type mergeOp struct{}

func (mergeOp) Kind() OperatorKind                { return OpMerge }
func (mergeOp) Handles() []diagnosis.TaxonomyCode { return nil }
func (mergeOp) HandlesSignal() Signal             { return SignalRedundantNode }
func (mergeOp) AdmissiblePatterns() []patternclassifier.Pattern {
	// Pattern-agnostic for the same reason prune is: redundancy is a property of what the node
	// CONTRIBUTED, which the signal carries, not of the label the classifier gave it.
	return nil
}

// Propose emits at most ONE candidate: the flagged node fused into the neighbour it already runs
// beside. The survivor is the node's PREDECESSOR in the order when it has one, and its successor
// otherwise — deterministic in both directions, so the same base + signal always names the same pair
// (task 3.4). A node that is alone in the order has no adjacent pair and yields nothing.
func (op mergeOp) Propose(in OperatorInput) ([]Candidate, error) {
	if in.Base == nil {
		return nil, nil
	}
	absorbed := in.NodeID()
	idx := indexOf(in.Base.Order, absorbed)
	if idx < 0 {
		return nil, nil // the signal names a node this spec does not order; nothing to fuse
	}
	survivor := adjacentSurvivor(in.Base.Order, idx)
	if survivor == "" {
		return nil, nil // a single-node order: a merge needs two adjacent nodes
	}
	spec := mergeNodes(in.Base, survivor, absorbed, parentVariantID(in))
	// NodeID is the SURVIVOR, not the absorbed node: the candidate's graph no longer contains the
	// absorbed node, so pointing the UI (and the ranking sort) at a node the spec does not order would
	// name something a reviewer cannot open. The rationale names both halves of the pair.
	c := newCandidate(op.Kind(), in, survivor, []string{"order"}, spec,
		fmt.Sprintf("node %s is redundant beside adjacent node %s → fuse the pair: %s survives and absorbs it, "+
			"%s's edges are rewired through the survivor (verified on held-out data before it is recommended)",
			absorbed, survivor, survivor, absorbed))
	return []Candidate{c}, nil
}

// adjacentSurvivor names the neighbour that absorbs the node at idx: the predecessor when there is
// one, else the successor. Preferring the predecessor is not arbitrary — the survivor keeps its own
// inbound edges, so fusing FORWARD (into the node that already runs first) leaves the graph's entry
// points untouched and makes the resulting order a prefix-preserving edit a reviewer can follow.
func adjacentSurvivor(order []string, idx int) string {
	switch {
	case idx > 0:
		return order[idx-1]
	case idx+1 < len(order):
		return order[idx+1]
	default:
		return ""
	}
}

// mergeNodes derives the fused candidate: absorbed leaves the order, its edges move to the survivor,
// every other per-node override is inherited unchanged, and the parent is never touched (D-1, D-2).
//
// It derives through variantspec.Reorder — the same helper the P5 editor's re-arrangement uses — so
// there is ONE way a wiring candidate comes into existence and it is the one that records lineage.
func mergeNodes(base *variantspec.VariantSpec, survivor, absorbed, parentID string) *variantspec.VariantSpec {
	order := make([]string, 0, len(base.Order))
	for _, n := range base.Order {
		if n != absorbed {
			order = append(order, n)
		}
	}
	spec := variantspec.Reorder(base, parentID, order, rewireThroughSurvivor(base.Edges, survivor, absorbed))
	// The absorbed node's override goes with it. A spec that kept it would be rejected by Validate
	// ("node has overrides but is not in order") — dead config the author believes is live.
	delete(spec.Nodes, absorbed)
	return spec
}

// rewireThroughSurvivor is the mechanical half of D-1: every edge touching the absorbed node is
// retargeted or re-sourced onto the survivor, in input order, with the pair's own edge dropped (it was
// internal to the fusion) and duplicates collapsed. Deterministic by construction — no map iteration
// decides the result, only the input order does.
func rewireThroughSurvivor(edges []variantspec.Edge, survivor, absorbed string) []variantspec.Edge {
	out := make([]variantspec.Edge, 0, len(edges))
	seen := map[variantspec.Edge]bool{}
	for _, e := range edges {
		if e.FromNodeID == absorbed {
			e.FromNodeID = survivor
		}
		if e.ToNodeID == absorbed {
			e.ToNodeID = survivor
		}
		if e.FromNodeID == e.ToNodeID {
			// The survivor→absorbed (or absorbed→survivor) edge became a self-edge: it described data flow
			// BETWEEN the two fused calls, which after the fusion happens inside one call. Keeping it would
			// claim the merged node feeds itself.
			continue
		}
		if seen[e] {
			continue // two edges that collapsed onto the same pair are one edge
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

// parentVariantID is the identity a derived wiring candidate records as its parent.
//
// It prefers the baseline's OWN config_hash when the caller supplied one (Engine.BaseVariantID), which
// is what "derived from" means; it falls back to whatever lineage the baseline itself carries so a
// caller that never resolved the baseline still produces a spec with a lineage pointer rather than an
// empty one. The value is never hashed — lineage is a property of how a spec was authored, not of the
// configuration it denotes (spec.go:306-310).
func parentVariantID(in OperatorInput) string {
	if in.BaseVariantID != "" {
		return in.BaseVariantID
	}
	if in.Base != nil {
		return in.Base.ParentVariantID
	}
	return ""
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
func pruneNode(base *variantspec.VariantSpec, node, parentID string) *variantspec.VariantSpec {
	spec := cloneSpec(base)
	spec.ParentVariantID = parentID
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
