package proposal

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
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
	// Kind names WHAT KIND of change this is, so the surface can say "bound a platform skill" where it
	// used to say "skills: 1 skill(s) -> 2 skill(s)" (P14 task 8.1). It is drawn from the tools≠skills
	// split, which is the only place the difference is recorded (decisions.md D-14.1).
	//
	// 🔴 The distinction is not cosmetic. Binding a skill CONSTRUCTS a value from a sealed contract;
	// pruning a tool DELETES a declaration the author wrote. A reviewer approving one has not approved
	// the other, and a surface that renders both as "skills changed" is asking for an approval nobody
	// gave. Empty for the pre-P14 dimensions, whose single kind is already their name.
	Kind ChangeKind `json:"kind,omitempty"`
	// Items names the specific skills or tools that moved, so the change reads as "pruned weatherTool"
	// rather than "one fewer tool". Omitted where there is nothing nameable.
	Items []string `json:"items,omitempty"`
}

// ChangeKind is the closed set of tool/skill change kinds the surface distinguishes.
type ChangeKind string

const (
	// KindSkillBind / KindSkillUnbind / KindSkillRerank are PLATFORM SKILL changes — a construction from
	// a sealed contract, or the removal of one.
	KindSkillBind   ChangeKind = "skill_bind"
	KindSkillUnbind ChangeKind = "skill_unbind"
	KindSkillRerank ChangeKind = "skill_rerank"
	// KindToolPrune / KindToolRestore are PROVIDER TOOL changes — a call-site deletion of an element the
	// author already wrote, or its reversal.
	KindToolPrune   ChangeKind = "tool_prune"
	KindToolRestore ChangeKind = "tool_restore"
)

// Legible returns the sentence the surface shows for this kind. One place, so a badge in the console
// and a line in a PR description cannot say two different things about one change.
func (k ChangeKind) Legible() string {
	switch k {
	case KindSkillBind:
		return "bound a platform skill"
	case KindSkillUnbind:
		return "unbound a platform skill"
	case KindSkillRerank:
		return "reordered the bound platform skills"
	case KindToolPrune:
		return "pruned a provider tool"
	case KindToolRestore:
		return "restored a provider tool"
	default:
		return ""
	}
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
	// Refusal is present when the transform DECLINED this change (P14 task 8.2). It is a first-class
	// field rather than an absence, because the failure D-14.3 is written against is a diff that "looks
	// complete": a refusal that is merely missing from the surface reads as a change that was made.
	Refusal *ChangeRefusal `json:"refusal,omitempty"`
}

// Present builds the reviewable presentation for a compiled candidate against the baseline spec.
func Present(c Compiled, base *variantspec.VariantSpec) Presentation {
	p := Presentation{
		Operator:        c.Candidate.Operator,
		NodeID:          c.Candidate.NodeID,
		Pattern:         string(c.Candidate.Pattern),
		DiffHash:        c.DiffHash,
		ConfigHash:      c.ConfigHash,
		SpecDiff:        specDiff(base, c.Candidate.Spec, c.IR),
		Rationale:       c.Candidate.Rationale,
		DiagID:          c.Candidate.DiagID,
		EvidenceCaseIDs: append([]string(nil), c.Candidate.EvidenceCaseIDs...),
	}
	if c.Refusal.Refused() {
		r := c.Refusal
		p.Refusal = &r
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
func specDiff(base, cand *variantspec.VariantSpec, ir *discovery.IR) []DimChange {
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
		out = append(out, dimChanges(id, bo, co, irNode(ir, id))...)
	}
	if base != nil && !equalStrings(base.Order, cand.Order) {
		out = append(out, DimChange{NodeID: "", Dimension: "order",
			From: fmt.Sprintf("%v", base.Order), To: fmt.Sprintf("%v", cand.Order)})
	}
	return out
}

func dimChanges(id string, from, to variantspec.NodeOverride, node *discovery.IRNode) []DimChange {
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
		out = append(out, skillChange(id, from.SkillRefs, to.SkillRefs))
	}
	if !equalStrings(from.SelectedTools(), to.SelectedTools()) {
		out = append(out, toolChange(id, from, to, node))
	}
	return out
}

// skillChange renders a PLATFORM SKILL change: a construction from a sealed contract, or its removal.
//
// The kind is decided by what actually moved — added, removed, or the same set in a different order —
// because "skills changed" is the sentence that made a rerank and an unbind look alike.
func skillChange(id string, from, to []string) DimChange {
	c := DimChange{NodeID: id, Dimension: "skills",
		From: fmt.Sprintf("%d skill(s)", len(from)), To: fmt.Sprintf("%d skill(s)", len(to))}
	added := missingFrom(to, from)
	removed := missingFrom(from, to)
	switch {
	case len(added) > 0 && len(removed) == 0:
		c.Kind, c.Items = KindSkillBind, shortRefs(added)
	case len(removed) > 0 && len(added) == 0:
		c.Kind, c.Items = KindSkillUnbind, shortRefs(removed)
	case len(added) == 0 && len(removed) == 0:
		// Same set, different order. Identity-bearing (ResolvedNode.SkillRefs is never sorted), so it is a
		// real change and must not render as "no change".
		c.Kind, c.Items = KindSkillRerank, shortRefs(to)
	default:
		// A simultaneous add and remove — a schema-binding fix swaps one skill for another.
		c.Kind, c.Items = KindSkillBind, shortRefs(added)
	}
	return c
}

// toolChange renders a PROVIDER TOOL change: a call-site deletion of an element the author wrote.
//
// 🔴 The baseline comes from the IR's SPLIT tool set (`IRNode.Tools`), not from the spec. That matters:
// a node with no tool selection has pruned nothing, and the only place the full offered set is recorded
// is the split field D-14.1 added. Without it the surface could say "kept 2 tools" but never "pruned
// weatherTool", which is the sentence a reviewer actually needs.
func toolChange(id string, from, to variantspec.NodeOverride, node *discovery.IRNode) DimChange {
	before, after := from.SelectedTools(), to.SelectedTools()
	c := DimChange{NodeID: id, Dimension: "tools"}

	discovered := discoveredTools(node)
	// The effective "before" set: an unset selection means the node offers everything the IR recorded.
	if len(before) == 0 && len(discovered) > 0 {
		before = discovered
	}
	c.From = describeToolSet(before)
	c.To = describeToolSet(after)

	dropped := missingFrom(before, after)
	restored := missingFrom(after, before)
	switch {
	case len(dropped) > 0:
		c.Kind, c.Items = KindToolPrune, dropped
	case len(restored) > 0:
		c.Kind, c.Items = KindToolRestore, restored
	}
	return c
}

func discoveredTools(node *discovery.IRNode) []string {
	if node == nil {
		return nil
	}
	out := make([]string, 0, len(node.Tools))
	for _, t := range node.Tools {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

func describeToolSet(names []string) string {
	if len(names) == 0 {
		return "(none recorded)"
	}
	return fmt.Sprintf("%d tool(s): %s", len(names), strings.Join(names, ", "))
}

// missingFrom returns the members of a that b does not contain, in a's order.
func missingFrom(a, b []string) []string {
	have := make(map[string]bool, len(b))
	for _, x := range b {
		have[x] = true
	}
	var out []string
	for _, x := range a {
		if !have[x] {
			out = append(out, x)
		}
	}
	return out
}

func shortRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, shortRef(r))
	}
	return out
}

func irNode(ir *discovery.IR, id string) *discovery.IRNode {
	if ir == nil {
		return nil
	}
	for i := range ir.Nodes {
		if ir.Nodes[i].NodeID == id {
			return &ir.Nodes[i]
		}
	}
	return nil
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
