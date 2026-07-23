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
		contextPolicyOp{},
		reorderOp{},
		ragTuneOp{},
		addRerankOp{},
		addSkillOp{},
		fixSchemaBindingOp{},
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
		out = append(out, newCandidate(op.Kind(), in, in.NodeID(), []string{"model"}, spec,
			fmt.Sprintf("cost bottleneck → downgrade to cheaper model %s (tier %d, ~$%.4f/run)", m.ModelID, m.Tier, m.CostPerRun)))
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
