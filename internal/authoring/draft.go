package authoring

import (
	"errors"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// A DRAFT is what a person is holding before they commit to anything (P13 task 9.1, FR28).
//
// It is deliberately NOT a variant. A variant has an identity — a `config_hash` — and identity comes
// from resolution; a draft has not been resolved, so storing it among the variants would put rows with
// no computable hash into the one space whose entire purpose is that everything in it has one.
//
// A draft hangs off an IMMUTABLE parent and carries a concurrency token. That pairing is what makes
// concurrent authoring safe without a lock: two people editing from one parent produce TWO variants,
// and a submission whose parent has moved is a NAMED CONFLICT rather than a silent overwrite. A lost
// update here would be indistinguishable, from the outside, from the platform quietly discarding
// somebody's work.
type Draft struct {
	// ID identifies the draft while it is being edited. It is not a variant id and never becomes one.
	ID string `json:"id"`
	// WorkflowID is the workflow being edited.
	WorkflowID string `json:"workflow_id"`
	// ParentVariantID is the immutable variant this draft edits. Never mutated — the whole point.
	ParentVariantID string `json:"parent_variant_id"`
	// Edits are the per-node changes, keyed by node id. A node absent here is left exactly as it is.
	Edits map[string]Edit `json:"edits"`
	// Actor is who is editing. Recorded on every downstream record; never hashed.
	Actor Actor `json:"actor"`
	// ForkedFromProposal is the candidate id this draft was forked from, when a person took an
	// operator's proposal and corrected it. Both lineages stay visible; the operator is not credited
	// with the outcome (FR29).
	ForkedFromProposal string `json:"forked_from_proposal,omitempty"`
	// ConcurrencyToken is what the parent's head looked like when this draft was started. Submission
	// compares it against the head at that moment; a mismatch is ErrStaleDraft, naming the parent.
	//
	// 🚫 Optimistic, not a lock. A long-held lock is an operational burden in a private deployment and
	// fails in a shape nobody can read ("who holds it, and are they still alive?"); a token plus a named
	// conflict fails in a shape a person can act on.
	ConcurrencyToken string `json:"concurrency_token"`
}

// Edit is one node's authored change. Every field is a POINTER, because this type has to distinguish
// three states that a value type collapses into two:
//
//	nil        — leave this dimension alone
//	&""/&nil   — CLEAR this dimension (the user removed an override they had set)
//	&"x"       — set it to x
//
// A plain string could not express "clear", and a clear that silently read as "leave alone" would make
// an override impossible to take back — which is also how a revert quietly becomes a third
// configuration rather than the one the user had before.
type Edit struct {
	ModelRef      *string   `json:"model_ref,omitempty"`
	PromptRef     *string   `json:"prompt_ref,omitempty"`
	SkillRefs     *[]string `json:"skill_refs,omitempty"`
	ToolSelection *[]string `json:"tool_selection,omitempty"`
	ContextPolicy *string   `json:"context_policy,omitempty"`
	// ApplyMode carries a provider-parameter change: params are data in `bound` mode and code in
	// `inline` (ADR-004), so an authored parameter tune is a bound-mode model ref, exactly as
	// `paramTuneOp` expresses it. There is no second representation of the same intent.
	ApplyMode *variantspec.ApplyMode `json:"apply_mode,omitempty"`
	// DropTolerance declares or clears the node's context drop tolerance (P16 FR19). Two pointers deep
	// because "clear the tolerance" and "leave the tolerance alone" are different edits and the outer
	// nil is the only way to say the second.
	DropTolerance **float64 `json:"drop_tolerance,omitempty"`
	// MemoryRef sets or clears the node's memory strategy — what it carries ACROSS invocations (P17
	// FR17/FR18). A pointer to "" CLEARS it, and clearing must reproduce the pre-selection `config_hash`
	// byte-identically: the field is additive and omit-when-absent, so an empty ref removes the key
	// entirely rather than writing an empty one.
	//
	// 🔴 Its own field, never folded into ContextPolicy. Memory persists across invocations; context
	// assembly is within one call (P17 decisions.md D2). One field meaning both would let a user edit a
	// cross-session concern through a within-call control and could never be cleanly separated once
	// drafts and hashes depended on it.
	MemoryRef *string `json:"memory_ref,omitempty"`
	// HarnessRef sets or clears the node's harness strategy — the control loop its call runs inside
	// (P18 §12). Nil leaves the dimension alone; `""` clears the override; a ref sets it.
	//
	// 🔴 Its own field, never folded into MemoryRef or ContextPolicy. A harness decides how many calls
	// happen at all, while those two describe what one call carries and what survives between calls; one
	// field meaning two of them would let a scaffold change resolve through another dimension's path.
	HarnessRef *string `json:"harness_ref,omitempty"`
	// LoopRef sets or clears the node's ITERATION POLICY — which control loop runs, what stops it, how
	// many turns the author chose (P34 task 3.7). Nil leaves the dimension alone; `""` clears the
	// override; a ref sets it.
	//
	// 🔴 Its own field, and the one that makes task 3.7 mechanical: `HarnessRef` above now writes an
	// EXECUTION ENVELOPE, and this writes the loop. A new authoring surface therefore cannot create a
	// loop-bearing harness entry by accident — there is no control on any surface that would produce one,
	// because the two intents have two fields and the strategy vocabularies behind them are disjoint at
	// the point of authoring (`HarnessStrategyOptions` offers the loop set, `EnvelopeOptions` the
	// envelope). Legacy loop-bearing entries stay resolvable; they are simply no longer authorable.
	LoopRef *string `json:"loop_ref,omitempty"`
}

// Dimensions names which dimensions this edit touches, in the closed enum's stable order. It is what
// the record's `axis` column and the surface's summary both read, so there is one answer.
func (e Edit) Dimensions() []variantspec.Dimension {
	var dims []variantspec.Dimension
	if e.ModelRef != nil || e.ApplyMode != nil {
		dims = append(dims, variantspec.DimModel)
	}
	if e.PromptRef != nil {
		dims = append(dims, variantspec.DimPrompt)
	}
	if e.SkillRefs != nil {
		dims = append(dims, variantspec.DimSkills)
	}
	if e.ContextPolicy != nil || e.DropTolerance != nil {
		dims = append(dims, variantspec.DimContext)
	}
	if e.ToolSelection != nil {
		dims = append(dims, variantspec.DimTools)
	}
	if e.HarnessRef != nil {
		dims = append(dims, variantspec.DimHarness)
	}
	if e.LoopRef != nil {
		dims = append(dims, variantspec.DimLoop)
	}
	if e.MemoryRef != nil {
		dims = append(dims, variantspec.DimMemory)
	}
	return dims
}

// Empty reports whether this edit changes nothing.
func (e Edit) Empty() bool { return len(e.Dimensions()) == 0 }

var (
	// ErrEmptyDraft: a draft that changes nothing is refused rather than submitted as a no-op variant
	// that would hash identically to its parent and clutter the lineage with a change that is not one.
	ErrEmptyDraft = errors.New("authoring: draft changes nothing")
	// ErrUnknownDraftNode: the draft edits a node the parent spec does not order.
	ErrUnknownDraftNode = errors.New("authoring: draft edits a node the parent does not contain")
	// ErrNoParent: a draft with no parent spec cannot be derived. There is no "edit from nothing" —
	// authoring is always a derivation, which is what keeps lineage total.
	ErrNoParent = errors.New("authoring: draft has no parent spec to derive from")
)

// Derive produces the candidate Variant Spec this draft denotes, WITHOUT mutating the parent.
//
// The parent is deep-copied before a single field is touched. That is not defensive habit: the parent
// is shared with the compare view, the rollback path, and possibly another person's in-flight draft,
// and an in-place edit here would rewrite history for all three.
func (d Draft) Derive(parent *variantspec.VariantSpec) (*variantspec.VariantSpec, error) {
	if parent == nil {
		return nil, ErrNoParent
	}
	if d.editCount() == 0 {
		return nil, ErrEmptyDraft
	}
	next := cloneSpec(parent)
	next.ParentVariantID = d.ParentVariantID

	ordered := map[string]bool{}
	for _, id := range next.Order {
		ordered[id] = true
	}
	for _, nodeID := range sortedNodes(d.Edits) {
		if !ordered[nodeID] {
			return nil, fmt.Errorf("%w: %s", ErrUnknownDraftNode, nodeID)
		}
		edit := d.Edits[nodeID]
		if edit.Empty() {
			continue
		}
		if next.Nodes == nil {
			next.Nodes = map[string]variantspec.NodeOverride{}
		}
		next.Nodes[nodeID] = applyEdit(next.Nodes[nodeID], edit)
	}
	return next, nil
}

// ToCandidate wraps a derived spec as the SAME shape an operator emits, so the shared compile path
// takes it without a branch. The Operator kind is `authored` — a real, distinct value rather than a
// borrowed operator name, because a record that claimed `model_upgrade` for a change nobody's catalog
// proposed would corrupt every operator statistic that reads it.
func (d Draft) ToCandidate(spec *variantspec.VariantSpec, nodeID string, dims []string) proposal.Candidate {
	return proposal.Candidate{
		Operator:           OperatorAuthored,
		NodeID:             nodeID,
		Dimensions:         dims,
		Spec:               spec,
		Rationale:          "authored directly by " + displayActor(d.Actor),
		Origin:             OriginUser,
		Actor:              d.Actor,
		ForkedFromProposal: d.ForkedFromProposal,
	}
}

// OperatorAuthored is the candidate's operator kind for a user-authored change.
const OperatorAuthored proposal.OperatorKind = "authored"

func (d Draft) editCount() int {
	n := 0
	for _, e := range d.Edits {
		if !e.Empty() {
			n++
		}
	}
	return n
}

// TouchedDimensions is every dimension the draft touches across all nodes, deduplicated and in the
// closed enum's order.
func (d Draft) TouchedDimensions() []string {
	seen := map[variantspec.Dimension]bool{}
	for _, e := range d.Edits {
		for _, dim := range e.Dimensions() {
			seen[dim] = true
		}
	}
	var out []string
	for _, dim := range variantspec.Dimensions() { // stable order, and cannot miss a new member
		if seen[dim] {
			out = append(out, string(dim))
		}
	}
	return out
}

// TouchedNodes lists the nodes this draft edits, sorted.
func (d Draft) TouchedNodes() []string {
	var out []string
	for id, e := range d.Edits {
		if !e.Empty() {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func applyEdit(o variantspec.NodeOverride, e Edit) variantspec.NodeOverride {
	if e.ModelRef != nil {
		o.ModelRef = *e.ModelRef
	}
	if e.PromptRef != nil {
		o.PromptRef = *e.PromptRef
	}
	if e.SkillRefs != nil {
		o.SkillRefs = append([]string(nil), (*e.SkillRefs)...)
	}
	if e.ToolSelection != nil {
		o.ToolSelection = append([]string(nil), (*e.ToolSelection)...)
	}
	if e.ContextPolicy != nil {
		o.ContextPolicy = *e.ContextPolicy
	}
	if e.ApplyMode != nil {
		o.ApplyMode = *e.ApplyMode
	}
	if e.DropTolerance != nil {
		if *e.DropTolerance == nil {
			o.ContextDropTolerance = nil // cleared — the key disappears, so the hash returns to pre-declaration
		} else {
			v := **e.DropTolerance
			o.ContextDropTolerance = &v
		}
	}
	if e.MemoryRef != nil {
		// Setting "" CLEARS the strategy. Because MemoryRef is `omitempty`, the empty string emits no key
		// at all, so a select-then-clear returns to the pre-selection bytes and the pre-selection
		// config_hash — the byte-exact back-out P17 FR18 requires. No branch is needed for it, and that is
		// the point: the clear path and the set path are one assignment, so they cannot drift.
		o.MemoryRef = *e.MemoryRef
	}
	if e.HarnessRef != nil {
		// Setting "" CLEARS the strategy, on exactly the same terms as MemoryRef above: `omitempty` means
		// the empty string emits no key, so a select-then-clear returns to the pre-selection bytes and the
		// pre-selection config_hash (FR43). One assignment for both paths, so they cannot drift.
		o.HarnessRef = *e.HarnessRef
	}
	if e.LoopRef != nil {
		// Setting "" CLEARS the loop, on exactly the same terms: `omitempty` means the empty string emits
		// no key, so a select-then-clear returns to the pre-selection bytes and the pre-selection
		// config_hash. One assignment for both paths, so they cannot drift.
		o.LoopRef = *e.LoopRef
	}
	return o
}

// cloneSpec deep-copies everything a draft can reach. Anything shallow-copied here would be a parent
// the derivation silently mutates.
func cloneSpec(s *variantspec.VariantSpec) *variantspec.VariantSpec {
	out := *s
	out.Order = append([]string(nil), s.Order...)
	out.Edges = append([]variantspec.Edge(nil), s.Edges...)
	out.InsertedAdapters = append([]variantspec.InsertedAdapter(nil), s.InsertedAdapters...)
	if s.Nodes != nil {
		out.Nodes = make(map[string]variantspec.NodeOverride, len(s.Nodes))
		for id, o := range s.Nodes {
			out.Nodes[id] = cloneOverride(o)
		}
	}
	return &out
}

func cloneOverride(o variantspec.NodeOverride) variantspec.NodeOverride {
	out := o
	out.SkillRefs = append([]string(nil), o.SkillRefs...)
	out.ToolSelection = append([]string(nil), o.ToolSelection...)
	if o.Bindings != nil {
		out.Bindings = make(map[string]variantspec.BindingSource, len(o.Bindings))
		for k, v := range o.Bindings {
			out.Bindings[k] = v
		}
	}
	if o.ContextDropTolerance != nil {
		v := *o.ContextDropTolerance
		out.ContextDropTolerance = &v
	}
	return out
}

func sortedNodes(m map[string]Edit) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func displayActor(a Actor) string {
	if a.ID == "" {
		return "an unidentified actor"
	}
	return a.ID
}
