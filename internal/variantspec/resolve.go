package variantspec

import (
	"context"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
)

// Registries is the read side of the four registries Resolve needs. An interface, not the concrete
// *registry.Store, so the Config Layer depends on resolution rather than on Postgres — and so these
// tests can run without one.
type Registries interface {
	ResolveModel(ctx context.Context, versionID string) (*registry.ModelEntry, error)
	ResolvePrompt(ctx context.Context, versionID string) (*registry.PromptEntry, error)
	ResolveSkill(ctx context.Context, versionID string) (*registry.SkillEntry, error)
	ResolveContextPolicy(ctx context.Context, versionID string) (*registry.ContextEntry, error)
}

// Resolved is a spec resolved against the IR and the registries: the hashed configuration plus the
// per-node detail the Transform Engine needs to generate the codemod.
type Resolved struct {
	Config     ResolvedConfig
	ConfigHash string
	// SourceRevision is carried through because the transform is keyed by
	// (config_hash, source_revision), not by config_hash alone.
	SourceRevision string
	// Language is the discovered workflow's language label (discovery.IRWorkflow.Language), carried
	// through so the Transform Engine can dispatch to that language's rewriters (ADR-003 decision 1).
	//
	// It rides here, and not as a parameter to transform.Generate, for the reason worktree.NewApplier
	// takes its verifier rather than deriving one: "which language is this workflow" is DISCOVERY's
	// answer, and this struct is already the resolution of a spec against Discovery's IR — the one place
	// the two are joined. A caller that had to supply it separately could supply a different one from
	// the IR the config_hash was computed against, and rewrite Python with the Go rewriters.
	//
	// 🚫 It is NOT part of config_hash, exactly as SourceRevision is not: the language is a property of
	// the tree, not of the configuration. Hashing it would change every existing config_hash and claim
	// two identical configurations differ.
	//
	// The empty string is invalid rather than defaulting to "go" — a Resolved built without going
	// through Resolve gets a loud dispatch failure, not the silent wrong-language rewrite that a
	// default would produce (禁止偷懒默认).
	Language string
	// Overrides is, per node_id, the dimensions the spec actually overrode and their resolved
	// values. The Transform Engine edits ONLY what appears here — this is where FR2's per-dimension
	// independence becomes mechanical rather than a rule to remember.
	Overrides map[string]ResolvedOverride
	// ApplyModes records each overridden node's apply mode (P10 task 7.1), defaulting to inline. NOT
	// part of config_hash — how a value is written is not part of the configuration it denotes.
	ApplyModes map[string]ApplyMode
}

// ResolvedOverride carries the resolved registry entries for one node's overridden dimensions. A nil
// field means that dimension was not overridden and its call-site construction must not be touched.
type ResolvedOverride struct {
	Model   *registry.ModelEntry
	Prompt  *registry.PromptEntry
	Skills  []*registry.SkillEntry
	Context *registry.ContextEntry
}

// Dimensions returns the dimensions this override actually sets, in a stable order. The Transform
// Engine iterates this; a dimension absent here is never edited.
func (o ResolvedOverride) Dimensions() []Dimension {
	var out []Dimension
	if o.Model != nil {
		out = append(out, DimModel)
	}
	if o.Prompt != nil {
		out = append(out, DimPrompt)
	}
	if len(o.Skills) > 0 {
		out = append(out, DimSkills)
	}
	if o.Context != nil {
		out = append(out, DimContext)
	}
	return out
}

// Resolve merges a Variant Spec onto the IR discovered at source_revision, resolves every ref
// against the registries, and computes config_hash (tasks 2.5, 2.6; FR11).
//
// It fails closed on the first problem and has no side effects — it only reads. Nothing downstream
// (transform generation, worktree checkout, build, run) may begin until this returns cleanly, which
// is what makes "a dangling ref never triggers a partial run" structural rather than a discipline.
func Resolve(ctx context.Context, spec *VariantSpec, ir *discovery.IR, regs Registries) (*Resolved, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if ir == nil {
		return nil, specErr("", "", ErrInvalidSpec, "no IR supplied for source_revision %s", spec.SourceRevision)
	}

	// Index the IR's nodes so an unknown node_id is caught before any ref is fetched.
	irNodes := make(map[string]*discovery.IRNode, len(ir.Nodes))
	for i := range ir.Nodes {
		irNodes[ir.Nodes[i].NodeID] = &ir.Nodes[i]
	}

	out := &Resolved{
		SourceRevision: spec.SourceRevision,
		Language:       ir.Workflow.Language,
		Overrides:      map[string]ResolvedOverride{},
		ApplyModes:     map[string]ApplyMode{},
		Config:         ResolvedConfig{IRVersion: ir.IRVersion, Nodes: []ResolvedNode{}, Edges: []ResolvedEdge{}},
	}

	for _, nodeID := range spec.Order {
		irNode, ok := irNodes[nodeID]
		if !ok {
			return nil, specErr(nodeID, "", ErrUnknownNode,
				"the IR at source_revision %s has no such node", spec.SourceRevision)
		}
		override := spec.Nodes[nodeID]

		resolvedOverride, node, err := resolveNode(ctx, nodeID, override, irNode, ir, regs)
		if err != nil {
			return nil, err
		}
		if !override.isEmpty() {
			out.Overrides[nodeID] = resolvedOverride
			out.ApplyModes[nodeID] = override.ApplyMode.Mode()
		}
		out.Config.Nodes = append(out.Config.Nodes, node)
	}

	// A conversion, not a field-by-field literal: Edge (the authored wire shape) and ResolvedEdge
	// (P0's frozen hash shape) are separate contracts that happen to coincide today. The conversion
	// stops compiling the moment either one gains a field, which is exactly the signal wanted — a
	// manual literal would keep compiling and silently leave the new field zero in the hashed output.
	for _, e := range spec.Edges {
		out.Config.Edges = append(out.Config.Edges, ResolvedEdge(e))
	}

	hash, err := out.Config.Hash()
	if err != nil {
		return nil, err
	}
	out.ConfigHash = hash
	return out, nil
}

// resolveNode produces one node's resolved_config entry by laying the spec's overrides over what the
// IR discovered at the call site.
//
// The merge rule, per dimension: an override resolves to a registry entry and is pinned by its
// immutable version_id; no override falls back to the discovered value and is pinned by
// source_revision. Both are reproducible — one by content address, one by commit — which is why
// resolved_config can be complete while the spec that produced it is sparse.
func resolveNode(ctx context.Context, nodeID string, o NodeOverride, irNode *discovery.IRNode, ir *discovery.IR, regs Registries) (ResolvedOverride, ResolvedNode, error) {
	var ro ResolvedOverride
	node := ResolvedNode{NodeID: nodeID}

	// ── model ────────────────────────────────────────────────────────────────────────────────────
	if o.ModelRef != "" {
		entry, err := regs.ResolveModel(ctx, o.ModelRef)
		if err != nil {
			return ro, node, refError(nodeID, DimModel, o.ModelRef, err)
		}
		ro.Model = entry
		node.ModelRef = entry.Spec.Provider + "/" + entry.Spec.ModelID
		node.ProviderParams = providerParams(modelParamsView{
			Temperature:    entry.Spec.Params.Temperature,
			MaxTokens:      entry.Spec.Params.MaxTokens,
			ThinkingBudget: entry.Spec.Params.ThinkingBudget,
		})
	} else {
		// The discovered binding. A model the discovery engine could not resolve statically is
		// "unresolved" in the IR; that is a faithful record of the call site, and it is pinned by
		// source_revision like any other default.
		node.ModelRef = irNode.Model.Provider + "/" + irNode.Model.ModelID
		node.ProviderParams = copyParams(irNode.Model.Params)
	}

	// ── prompt ───────────────────────────────────────────────────────────────────────────────────
	if o.PromptRef != "" {
		entry, err := regs.ResolvePrompt(ctx, o.PromptRef)
		if err != nil {
			return ro, node, refError(nodeID, DimPrompt, o.PromptRef, err)
		}
		ro.Prompt = entry
		node.PromptRef = "prompt://" + entry.Name + "@" + entry.VersionID
	} else {
		// P1 always emits an INLINE prompt (there is no registry behind a discovered call site), so
		// there is no name@version to cite. Its identity is its bytes, so cite those: an
		// `inline://<sha256>` ref pins the exact template just as tightly as a registry version, and
		// re-deriving it from source_revision gives the same string. The alternative — auto-
		// registering every discovered prompt just to have a ref — would fill the registry with
		// entries nobody authored.
		node.PromptRef = "inline://" + sha256Hex([]byte(irNode.Prompt.Inline))
	}

	// ── skills ───────────────────────────────────────────────────────────────────────────────────
	if len(o.SkillRefs) > 0 {
		refs := make([]string, 0, len(o.SkillRefs))
		for _, ref := range o.SkillRefs {
			entry, err := regs.ResolveSkill(ctx, ref)
			if err != nil {
				return ro, node, refError(nodeID, DimSkills, ref, err)
			}
			ro.Skills = append(ro.Skills, entry)
			refs = append(refs, entry.Name+"@"+entry.VersionID)
		}
		// Order preserved, never sorted: JCS leaves arrays alone, so the spec's declared binding
		// order is identity-bearing, and it is also the order the call site will bind them in.
		node.SkillRefs = refs
	} else {
		node.SkillRefs = append([]string{}, irNode.ToolsSkills...)
	}

	// ── context ──────────────────────────────────────────────────────────────────────────────────
	if o.ContextPolicy != "" {
		entry, err := regs.ResolveContextPolicy(ctx, o.ContextPolicy)
		if err != nil {
			return ro, node, refError(nodeID, DimContext, o.ContextPolicy, err)
		}
		ro.Context = entry
		node.ContextPolicy = entry.Spec.Policy
		params, err := decodeParams(entry.Spec.Params)
		if err != nil {
			return ro, node, specErr(nodeID, DimContext, ErrUnresolvedRef,
				"context entry %s has params that are not a JSON object: %v", entry.VersionID, err)
		}
		node.ContextParams = params
	} else {
		node.ContextPolicy = irNode.ContextAssembly.Policy
		node.ContextParams = map[string]any{}
	}

	// ── bindings (P10 tasks 3.2–3.5, 3.8) ──────────────────────────────────────────────────────────
	// Validated HERE, at resolve, before any transformation is generated — every failure class is a
	// resolve-time SpecError naming node, dimension and slot (task 3.2), so nothing is discovered at
	// codemod time that could have been caught earlier.
	//
	// The effective slot set is the pinned prompt's when one was overridden, else the discovered
	// prompt's declared variables. Both are the interface the transform will have to satisfy.
	var slots []string
	if ro.Prompt != nil {
		slots = ro.Prompt.Template.Slots()
	} else {
		slots = irNode.Prompt.Variables
	}
	bindings, err := validateBindings(nodeID, o, irNode, ir, slots)
	if err != nil {
		return ro, node, err
	}
	node.Bindings = bindings // nil when no explicit bindings — omitted from config_hash

	return ro, node, nil
}

// validateBindings runs every binding failure class at resolve time and returns the resolved binding
// map for the hash (tasks 3.2–3.5). It reports through SpecError{NodeID, Dim, Ref} exclusively — no
// second error channel (task 3.2).
//
// Two things happen here. First, each explicit binding's reference is validated against the fact its
// kind depends on: an `expr` against the IR's recorded in-scope symbols, an `env` against the
// workflow's declared variables, an `input` against the node's typed contract. Second, the
// exactly-once satisfaction rule (task 3.3) is enforced: every prompt slot must be satisfied by an
// explicit binding OR an identically-spelled call-site expression, and by exactly one of them.
//
// The satisfaction rule is gated so pre-P10 specs keep resolving. It engages when the IR records an
// in-scope set for this call site (a P10-aware discovery) OR the spec declares any binding. When
// neither holds — an old IR, a spec with no bindings — the check is deferred to the transform's own
// promptExprFor, exactly as before this capability existed.
func validateBindings(nodeID string, o NodeOverride, irNode *discovery.IRNode, ir *discovery.IR, slots []string) (map[string]ResolvedBinding, error) {
	slotSet := map[string]bool{}
	for _, s := range slots {
		slotSet[s] = true
	}

	// 1. Per-binding reference validation. Iterate in sorted order so the FIRST reported failure is
	//    deterministic (a spec with two bad bindings names the same one every run).
	for _, slot := range sortedKeys(o.Bindings) {
		b := o.Bindings[slot]
		// A binding for a slot the prompt does not declare is a mistake — an unknown binding, the
		// mirror of Render's "binding matches no slot".
		if !slotSet[slot] {
			return nil, &SpecError{NodeID: nodeID, Dim: DimPrompt, Ref: slot, Err: ErrUnsatisfiedSlot,
				Detail: fmt.Sprintf("binding names slot %q, which the effective prompt does not declare", slot)}
		}
		switch b.Kind {
		case BindLiteral:
			// A constant — nothing external to validate.
		case BindExpr:
			// Validated against the IR's recorded in-scope symbols; a conservative record fails closed.
			if !irNode.CallSite.HasInScope(b.Value) {
				return nil, &SpecError{NodeID: nodeID, Dim: DimPrompt, Ref: b.Value, Err: ErrBindingOutOfScope,
					Detail: fmt.Sprintf("slot %q binds expr %q, which the IR does not record as in scope at this call site", slot, b.Value)}
			}
		case BindEnv:
			if !ir.DeclaresEnv(b.Value) {
				return nil, &SpecError{NodeID: nodeID, Dim: DimPrompt, Ref: b.Value, Err: ErrBindingOutOfScope,
					Detail: fmt.Sprintf("slot %q binds env %q, which is not a declared environment variable", slot, b.Value)}
			}
		case BindInput:
			if !inputContractAdmits(irNode, b.Value) {
				return nil, &SpecError{NodeID: nodeID, Dim: DimPrompt, Ref: b.Value, Err: ErrBindingOutOfScope,
					Detail: fmt.Sprintf("slot %q binds input %q, which the node's typed input contract does not admit", slot, b.Value)}
			}
		}
	}

	// 2. Exactly-once satisfaction (task 3.3), gated as documented above.
	if irNode.CallSite.InScopeRecorded() || len(o.Bindings) > 0 {
		for _, slot := range slots {
			_, hasBinding := o.Bindings[slot]
			hasCallSite := irNode.CallSite.HasInScope(slot)
			switch {
			case hasBinding && hasCallSite:
				return nil, &SpecError{NodeID: nodeID, Dim: DimPrompt, Ref: slot, Err: ErrAmbiguousSlot,
					Detail: fmt.Sprintf("slot %q is satisfied by both an explicit binding and an identically-spelled call-site expression", slot)}
			case !hasBinding && !hasCallSite:
				return nil, &SpecError{NodeID: nodeID, Dim: DimPrompt, Ref: slot, Err: ErrUnsatisfiedSlot,
					Detail: fmt.Sprintf("slot %q has no explicit binding and no identically-spelled call-site expression", slot)}
			}
		}
	}

	// 3. Project the explicit bindings into the hashed form. nil (not empty) when there are none, so a
	//    node with no bindings is omitted from config_hash entirely (byte-identical to pre-P10).
	if len(o.Bindings) == 0 {
		return nil, nil
	}
	out := make(map[string]ResolvedBinding, len(o.Bindings))
	for slot, b := range o.Bindings {
		out[slot] = ResolvedBinding{Kind: string(b.Kind), Value: b.Value}
	}
	return out, nil
}

// inputContractAdmits reports whether the node's typed input contract admits a value named `name`. The
// contract is a JSON Schema object; a value is admitted iff it is a declared property. An absent or
// propertyless schema admits nothing — fail closed, never a false acceptance.
func inputContractAdmits(irNode *discovery.IRNode, name string) bool {
	props, ok := irNode.IOContract.InputSchema["properties"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = props[name]
	return ok
}

// refError turns a registry miss into a rejection that names the node, the dimension, and the ref.
//
// registry.ErrNotFound is the interesting one: it is exactly FR5's "a *_ref that does not resolve",
// and the registries spec's "unavailable skill rejected — resolution fails closed and the node does
// not execute". Anything else is an infrastructure failure and must not be dressed up as a bad ref.
func refError(nodeID string, dim Dimension, ref string, err error) error {
	if errors.Is(err, registry.ErrNotFound) {
		return &SpecError{NodeID: nodeID, Dim: dim, Ref: ref, Err: ErrUnresolvedRef,
			Detail: "no registry entry has this version_id"}
	}
	if errors.Is(err, registry.ErrCorruptEntry) {
		return &SpecError{NodeID: nodeID, Dim: dim, Ref: ref, Err: ErrUnresolvedRef,
			Detail: fmt.Sprintf("the registry entry is corrupt: %v", err)}
	}
	return fmt.Errorf("variantspec: node %q, dimension %q, ref %q: %w", nodeID, dim, ref, err)
}

func copyParams(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
