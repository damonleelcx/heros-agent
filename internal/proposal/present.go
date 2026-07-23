package proposal

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// DimChange is one entry in the Variant-Spec diff: a node dimension whose ref changed from the
// baseline. It is the structural, human-legible companion to the source diff — "which node dimension
// moved" alongside "which call-site bytes changed" (§3.3, reusing the P5 Variant-Spec diff idea).
type DimChange struct {
	NodeID    string `json:"node_id"`
	Dimension string `json:"dimension"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// Presentation is everything the recommendation surface renders for one candidate: the reviewable
// source diff (the codemod output), the Variant-Spec diff (dimension changes), and the evidence —
// the originating diagnosis and the specific failing cases (§3.3). It is derived purely from a
// compiled candidate plus the baseline; it holds no verdict (verification attaches that later).
type Presentation struct {
	Operator OperatorKind `json:"operator"`
	NodeID   string       `json:"node_id"`
	Pattern  string       `json:"pattern"`
	// SourceDiff is the codemod's unified diff — the reviewable artifact ADR-001 makes the product's
	// output.
	SourceDiff string `json:"source_diff"`
	DiffHash   string `json:"diff_hash"`
	ConfigHash string `json:"config_hash"`
	// SpecDiff is the Variant-Spec diff: which node dimension(s) changed and from/to which ref.
	SpecDiff []DimChange `json:"spec_diff"`
	// Rationale is the one-line, evidence-anchored reason.
	Rationale string `json:"rationale"`
	// DiagID and EvidenceCaseIDs are the evidence: the originating diagnosis and the specific failing
	// cases that motivated it.
	DiagID          string   `json:"diag_id"`
	EvidenceCaseIDs []string `json:"evidence_case_ids"`
	// GroundingHash, when present, is the content hash of the prompt-rewrite grounding bundle.
	GroundingHash string `json:"grounding_hash,omitempty"`
}

// Present builds the reviewable presentation for a compiled candidate against the baseline spec.
func Present(c Compiled, base *variantspec.VariantSpec) Presentation {
	p := Presentation{
		Operator:        c.Candidate.Operator,
		NodeID:          c.Candidate.NodeID,
		Pattern:         string(c.Candidate.Pattern),
		DiffHash:        c.DiffHash,
		ConfigHash:      c.ConfigHash,
		SpecDiff:        specDiff(base, c.Candidate.Spec),
		Rationale:       c.Candidate.Rationale,
		DiagID:          c.Candidate.DiagID,
		EvidenceCaseIDs: append([]string(nil), c.Candidate.EvidenceCaseIDs...),
	}
	if c.Patch != nil {
		p.SourceDiff = string(c.Patch.Diff)
	}
	if c.Candidate.Grounding != nil {
		p.GroundingHash = c.Candidate.Grounding.Hash
	}
	return p
}

// specDiff computes the Variant-Spec diff between a baseline and a candidate: every node dimension
// whose ref differs, plus order changes. Deterministic (sorted by node then dimension).
func specDiff(base, cand *variantspec.VariantSpec) []DimChange {
	var out []DimChange
	if cand == nil {
		return out
	}
	nodes := map[string]bool{}
	if base != nil {
		for id := range base.Nodes {
			nodes[id] = true
		}
	}
	for id := range cand.Nodes {
		nodes[id] = true
	}
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		var bo variantspec.NodeOverride
		if base != nil {
			bo = base.Nodes[id]
		}
		co := cand.Nodes[id]
		out = append(out, dimChanges(id, bo, co)...)
	}
	if base != nil && !equalStrings(base.Order, cand.Order) {
		out = append(out, DimChange{NodeID: "", Dimension: "order",
			From: fmt.Sprintf("%v", base.Order), To: fmt.Sprintf("%v", cand.Order)})
	}
	return out
}

func dimChanges(id string, from, to variantspec.NodeOverride) []DimChange {
	var out []DimChange
	if from.ModelRef != to.ModelRef {
		out = append(out, DimChange{NodeID: id, Dimension: "model", From: shortRef(from.ModelRef), To: shortRef(to.ModelRef)})
	}
	if from.PromptRef != to.PromptRef {
		out = append(out, DimChange{NodeID: id, Dimension: "prompt", From: shortRef(from.PromptRef), To: shortRef(to.PromptRef)})
	}
	if from.ContextPolicy != to.ContextPolicy {
		out = append(out, DimChange{NodeID: id, Dimension: "context", From: shortRef(from.ContextPolicy), To: shortRef(to.ContextPolicy)})
	}
	if !equalStrings(from.SkillRefs, to.SkillRefs) {
		out = append(out, DimChange{NodeID: id, Dimension: "skills",
			From: fmt.Sprintf("%d skill(s)", len(from.SkillRefs)), To: fmt.Sprintf("%d skill(s)", len(to.SkillRefs))})
	}
	return out
}

func shortRef(ref string) string {
	if ref == "" {
		return "(none)"
	}
	if len(ref) > 12 {
		return ref[:12]
	}
	return ref
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
