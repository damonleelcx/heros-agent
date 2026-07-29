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
	// ResolveMemory resolves a memory-registry version_id (P17 task 4.1). It is its OWN method, and not a
	// mode of ResolveContextPolicy, for the reason decisions.md D2 keeps the dimensions apart: memory
	// persists ACROSS invocations, context is within one call, and one method returning either would let
	// a cross-session concern resolve through a within-call path.
	ResolveMemory(ctx context.Context, versionID string) (*registry.MemoryEntry, error)
	// ResolveHarness resolves a harness-registry version_id (P18 task 3.3). Its OWN method for the reason
	// ResolveMemory is: a harness is a different Kind with a different id space, and one method returning
	// either would let a scaffold resolve through a memory path — the exact cross-dimension confusion the
	// Kind is hashed into the version_id to prevent.
	ResolveHarness(ctx context.Context, versionID string) (*registry.HarnessEntry, error)
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
	// DiscoveredWiring is the wiring the IR recorded at source_revision — the order and edges the
	// checked-out SOURCE actually has, as opposed to the ones this spec ASKS for (P15 task 4.2).
	//
	// The two can differ, and the difference is the whole of the wiring axis: `Order`/`Edges` are
	// identity-bearing, so a reorder/merge/prune is a new config_hash — but no rewriter materializes it
	// as source yet. Carrying the discovered wiring here is what lets the transform engine TELL that a
	// spec is asking for a rewire it cannot perform, and refuse instead of emitting a diff that changes
	// only the node contents while the config_hash claims a different graph.
	//
	// 🚫 NOT part of config_hash, exactly as SourceRevision and Language are not: it is a property of
	// the tree, not of the configuration. It rides here for the reason Language does — this struct is
	// the one place a spec and the IR it was resolved against are already joined, so a caller cannot
	// supply a wiring from a different IR than the hash was computed over.
	DiscoveredWiring Wiring
	// HarnessGroups are the resolved group harnesses, carried beside the hashed projection so the
	// transform can name the exact edge set it is refusing (P18 task 5.3). Nil when the spec declares
	// none, which is the common case.
	HarnessGroups []ResolvedGroupOverride
}

// ResolvedGroupOverride is one group harness as the transform sees it: the registry entry the author
// pinned, and the ordered edge set it wraps. The entry rather than the projection, for the same reason
// ResolvedOverride carries entries — a refusal has to name what the author wrote.
type ResolvedGroupOverride struct {
	Entry *registry.HarnessEntry
	Edges []ResolvedEdge
}

// Wiring is a graph's shape: the node order and the edges between them. It is the same pair
// `config_hash` covers, in the same resolved spelling, so a comparison between what a spec asks for
// and what the source has is a comparison of like with like.
//
// Empty means NOT RECORDED, which is different from "an empty graph": a caller that assembled a
// Resolved by hand (the P5 editor, a codemod test) may have no IR to read, and the transform's wiring
// check declines to conclude anything from an absent record rather than refusing everything.
type Wiring struct {
	Order []string       `json:"order"`
	Edges []ResolvedEdge `json:"edges"`
}

// Recorded reports whether any discovered wiring was captured.
func (w Wiring) Recorded() bool { return len(w.Order) > 0 }

// WiringOf projects a discovered IR into the wiring shape. The node order is the IR's own order —
// what Discovery found, in the order it found it — and the edges are copied verbatim; the optional
// provenance/confidence fields are deliberately dropped, because whether an edge was recovered or
// framework-certain does not change what the source WIRES.
func WiringOf(ir *discovery.IR) Wiring {
	if ir == nil {
		return Wiring{}
	}
	w := Wiring{Order: make([]string, 0, len(ir.Nodes)), Edges: make([]ResolvedEdge, 0, len(ir.Edges))}
	for _, n := range ir.Nodes {
		w.Order = append(w.Order, n.NodeID)
	}
	for _, e := range ir.Edges {
		w.Edges = append(w.Edges, ResolvedEdge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind})
	}
	return w
}

// ResolvedOverride carries the resolved registry entries for one node's overridden dimensions. A nil
// field means that dimension was not overridden and its call-site construction must not be touched.
type ResolvedOverride struct {
	Model   *registry.ModelEntry
	Prompt  *registry.PromptEntry
	Skills  []*registry.SkillEntry
	Context *registry.ContextEntry
	// ToolSelection is the KEPT tool set, canonical (sorted, validated against the node's discovered
	// tools). The transform derives the DELETIONS from it — everything the call site declares that this
	// set does not keep. Carrying the kept set rather than the dropped one is deliberate: the kept set is
	// what the author wrote and what config_hash records, so the two cannot disagree.
	ToolSelection []string
	// ParamTune marks a model override that changes ONLY the provider parameters (temperature,
	// max-tokens) and not the provider or model id — a P13 parameter tune, not a model swap (design
	// Decision 7). It is transform-facing and NOT part of config_hash (the HASHED effect is
	// ResolvedNode.ProviderParams): its only job is to tell the transform that this override's effect is
	// a parameter change, which materializes in BOUND mode and is REFUSED in inline mode (there is no
	// call-site parameter rewriter — an inline param change would hash one thing and run another).
	ParamTune bool
	// ContextDropTolerance is the node's authored drop tolerance, carried through so the admissibility
	// gate reads it from the RESOLVED override rather than re-reading the spec (P16 task 1.5). Nil when
	// the node declares none — the gate then falls back to its pattern-derived default, which is a
	// gate-time decision and deliberately not frozen into the hash.
	ContextDropTolerance *float64
	// Memory is the resolved memory strategy — what this node carries ACROSS invocations (P17 task 4.1).
	// Nil means the node did not override memory; non-nil means it did, INCLUDING when the strategy it
	// selected is `none`.
	//
	// 🔴 That distinction is load-bearing and is why this is a pointer rather than a string. "No override"
	// and "an override that selects the identity strategy" produce the same HASHED bytes (D3: none ≡
	// absent) but are different facts about what the author asked for, and the transform has to be able to
	// tell them apart: an explicit `none` is a no-op it applies silently, while a real strategy is a
	// refusal it must raise. Collapsing them would either refuse `none` (wrong — the identity strategy
	// changes nothing, so there is nothing to refuse) or skip the refusal entirely.
	Memory *registry.MemoryEntry
	// Harness is the resolved harness strategy — the control loop this node's call runs inside (P18 task
	// 3.3). Nil means the node did not override the harness; non-nil means it did, INCLUDING when the
	// strategy it selected is `single-shot`.
	//
	// 🔴 That distinction is load-bearing and is why this is a pointer rather than a string. "No override"
	// and "an override that selects the identity strategy" produce the same HASHED bytes (D-8:
	// single-shot ≡ absent) but are different facts about what the author asked for, and the transform has
	// to tell them apart: an explicit `single-shot` is a no-op it applies silently, while a multi-turn
	// strategy is either materialized or refused. Collapsing them would either refuse `single-shot` (wrong
	// — one turn is exactly the un-rewritten call site) or skip the decision entirely.
	Harness *registry.HarnessEntry
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
	if len(o.ToolSelection) > 0 {
		out = append(out, DimTools)
	}
	// Memory is reported iff the node OVERRODE it — including an override that selects `none`. See the
	// Memory field's comment: the transform needs to tell "no override" from "the identity strategy",
	// because one is skipped and the other is a no-op it applies. A node that never mentioned memory is
	// never dispatched here, which is how FR2's per-dimension independence stays mechanical.
	if o.Memory != nil {
		out = append(out, DimMemory)
	}
	// Harness is reported iff the node OVERRODE it — including an override that selects `single-shot`, for
	// the reason the Harness field's comment gives: the transform must be able to tell "no override" from
	// "the identity strategy", because one is never dispatched and the other is a no-op it applies.
	if o.Harness != nil {
		out = append(out, DimHarness)
	}
	return out
}

// discoveredToolNames lists the tools the IR records for a node, for a rejection a human has to act on:
// "you asked to keep X" is only actionable next to "the node offers Y and Z".
func discoveredToolNames(irNode *discovery.IRNode) []string {
	out := make([]string, 0, len(irNode.Tools))
	for _, t := range irNode.Tools {
		out = append(out, t.Name)
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
		// What the SOURCE wires, carried beside what the spec asks for, so the transform engine can tell
		// a wiring change from a content change (P15 task 4.2). Read-only, never hashed.
		DiscoveredWiring: WiringOf(ir),
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

	// Group harnesses (P18 FR15, decisions.md D-5). Resolved after the nodes so a group naming an edge
	// between nodes that failed to resolve never gets here — and projected in the spec's declared order,
	// because a loop over a→b→c is not a loop over c→b→a.
	for i, g := range spec.HarnessGroups {
		entry, err := regs.ResolveHarness(ctx, g.HarnessRef)
		if err != nil {
			return nil, refError("", DimHarness, g.HarnessRef, err)
		}
		params, err := decodeParams(entry.Spec.Params)
		if err != nil {
			return nil, specErr("", DimHarness, ErrUnresolvedRef,
				"harness_groups[%d]: entry %s has params that are not a JSON object: %v", i, entry.VersionID, err)
		}
		rg := ResolvedHarnessGroup{Harness: ResolvedHarness{Strategy: entry.Spec.Strategy, Params: params}}
		for _, e := range g.Edges {
			rg.Edges = append(rg.Edges, ResolvedEdge(e))
		}
		out.Config.HarnessGroups = append(out.Config.HarnessGroups, rg)
		out.HarnessGroups = append(out.HarnessGroups, ResolvedGroupOverride{Entry: entry, Edges: rg.Edges})
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
		// A model override that keeps the SAME provider+model_id but changes the params is a parameter
		// tune (P13 Decision 7), not a model swap. Flag it so the transform materializes it in bound mode
		// and refuses it inline — never a silent same-model, changed-params inline edit that hashes one
		// thing and runs another.
		if entry.Spec.Provider == irNode.Model.Provider && entry.Spec.ModelID == irNode.Model.ModelID &&
			!paramsEqual(node.ProviderParams, copyParams(irNode.Model.Params)) {
			ro.ParamTune = true
		}
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
	// Drop tolerance freezes as authored, and ONLY as authored (P16 task 1.5, decisions.md D-2). No
	// default is materialized here: the pattern-derived default belongs to the admissibility gate, which
	// knows the node's pattern, and writing it into the hashed projection would make two specs that
	// declared nothing hash differently the day the default changed.
	if o.ContextDropTolerance != nil {
		t := *o.ContextDropTolerance
		node.ContextDropTolerance = &t
		ro.ContextDropTolerance = &t
	}

	// ── memory (P17 task 4.1, decisions.md D3/D4) ──────────────────────────────────────────────────
	//
	// The merge rule is the same as every other dimension's: an override resolves to a registry entry
	// pinned by its immutable version_id; no override falls back to the DISCOVERED default, pinned by
	// source_revision. Discovery emits `none` for every node today (there is no static evidence of a
	// cross-invocation store at a call site), so the fallback is inert — but it is WIRED, not assumed,
	// because the resolver's job is to leave nothing to defaulting and an assumption here would be a
	// silent default the day discovery learns to detect one.
	//
	// 🔴 What does NOT happen here is the refusal. A spec carrying a MemoryRef resolves cleanly and
	// produces a stable config_hash; only the TRANSFORM refuses (decisions.md D4). Blocking at resolve
	// would throw away the modeling, hashing, and proposal that are entirely safe and correct, and would
	// make the axis useless for the large fraction of its value that needs no codemod.
	if o.MemoryRef != "" {
		// Defense in depth against a caller that assembled a Resolve by hand and skipped Validate. Same
		// helper, so there is one inline-vs-reference rule rather than two that can disagree.
		if inlinesDefinition(o.MemoryRef) {
			return ro, node, &SpecError{NodeID: nodeID, Dim: DimMemory, Ref: o.MemoryRef, Err: ErrInlineDefinition,
				Detail: "memory_ref carries an inline strategy definition; it must be a memory-registry " +
					"version_id, so the configuration is resolvable back from a config_hash"}
		}
		entry, err := regs.ResolveMemory(ctx, o.MemoryRef)
		if err != nil {
			return ro, node, refError(nodeID, DimMemory, o.MemoryRef, err)
		}
		ro.Memory = entry
		// `none` ≡ absent (D3): the identity strategy emits NO memory key, so an explicitly-`none` node
		// canonicalizes byte-identically to a node that never mentioned memory and the P0 golden vectors
		// keep reproducing. The override is still recorded on `ro` — the transform must be able to see
		// that the author asked for something, even when what they asked for changes nothing.
		if !entry.IsNone() {
			params, err := decodeParams(entry.Spec.Params)
			if err != nil {
				return ro, node, specErr(nodeID, DimMemory, ErrUnresolvedRef,
					"memory entry %s has params that are not a JSON object: %v", entry.VersionID, err)
			}
			node.Memory = &ResolvedMemory{Strategy: entry.Spec.Strategy, Params: params}
		}
	} else if base := irNode.MemoryDefault(); base != registry.StrategyNone {
		// The discovered default, pinned by source_revision like any other un-overridden dimension. No
		// params: the IR records WHICH strategy a call site already implements, not how it is tuned —
		// inventing params here would be this layer guessing at a configuration nobody wrote.
		node.Memory = &ResolvedMemory{Strategy: base, Params: map[string]any{}}
	}

	// ── harness (P18 task 3.3, decisions.md D-2/D-8) ───────────────────────────────────────────────
	//
	// The merge rule is every other dimension's: an override resolves to a registry entry pinned by its
	// immutable version_id; no override falls back to the DISCOVERED default, pinned by source_revision.
	// Discovery emits `single-shot` for every node it cannot PROVE runs a loop, so the fallback is usually
	// inert — but it is WIRED rather than assumed, because a node whose source already implements a ReAct
	// loop has a different default, and an assumption here would silently claim it was a single shot.
	//
	// 🔴 What does NOT happen here is the refusal or the materialization. A spec carrying a HarnessRef
	// resolves cleanly and produces a stable config_hash; only the TRANSFORM decides whether this cell can
	// carry it (decisions.md D-4, narrowed per cell by D-11). Blocking at resolve would throw away the
	// modeling, hashing and proposal that are entirely safe and correct.
	if o.HarnessRef != "" {
		// Defense in depth against a caller that assembled a Resolve by hand and skipped Validate.
		if inlinesDefinition(o.HarnessRef) {
			return ro, node, &SpecError{NodeID: nodeID, Dim: DimHarness, Ref: o.HarnessRef, Err: ErrInlineDefinition,
				Detail: "harness_ref carries an inline strategy definition; it must be a harness-registry " +
					"version_id, so the configuration is resolvable back from a config_hash"}
		}
		entry, err := regs.ResolveHarness(ctx, o.HarnessRef)
		if err != nil {
			return ro, node, refError(nodeID, DimHarness, o.HarnessRef, err)
		}
		ro.Harness = entry
		// `single-shot` with no params ≡ absent (D-8): the identity strategy emits NO harness key, so an
		// explicitly-single-shot node canonicalizes byte-identically to a node that never mentioned a
		// harness and the P0 golden vectors keep reproducing. The override is still recorded on `ro` — the
		// transform must be able to see that the author asked for something, even when what they asked for
		// changes nothing.
		params, err := decodeParams(entry.Spec.Params)
		if err != nil {
			return ro, node, specErr(nodeID, DimHarness, ErrUnresolvedRef,
				"harness entry %s has params that are not a JSON object: %v", entry.VersionID, err)
		}
		if !entry.IsSingleShot() || len(params) > 0 {
			node.Harness = &ResolvedHarness{Strategy: entry.Spec.Strategy, Params: params}
		}
	} else if base := irNode.HarnessDefault(); base != registry.StrategySingleShot {
		// The discovered default, pinned by source_revision like any other un-overridden dimension. No
		// params: the IR records WHICH scaffold a call site already implements, not how it is tuned —
		// inventing params here would be this layer guessing at a configuration nobody wrote.
		node.Harness = &ResolvedHarness{Strategy: base, Params: map[string]any{}}
	}

	// ── tools (P14 task 5.3, decisions.md D-14.2) ──────────────────────────────────────────────────
	//
	// A tool selection is VALIDATED AGAINST THE NODE'S DISCOVERED TOOL SET and fails closed. This is the
	// third application of a pattern the codebase has already proved twice — an `env` binding validated
	// against DeclaredEnv, an `expr` binding validated against in_scope — and it fails in the same safe
	// direction: a selection naming a tool the IR does not record for this node is REJECTED here, before
	// any diff exists, rather than applied to nothing and shipped as a prune that pruned nothing.
	//
	// 🔴 An IR that predates the split is also a rejection, not a pass. `Tools == nil` means "this IR
	// does not record tools", which is NOT "this node offers none" — treating the two alike would accept
	// every selection against every pre-P14 IR, which is precisely the false acceptance fail-closed
	// exists to prevent.
	if len(o.ToolSelection) > 0 {
		if !irNode.ToolsRecorded() {
			return ro, node, &SpecError{NodeID: nodeID, Dim: DimTools, Err: ErrToolNotDiscovered,
				Detail: "the IR at this source_revision records no tool set for this node (it predates the " +
					"tools/skills split), so a selection over it cannot be validated and is refused rather " +
					"than applied to an unknown set"}
		}
		for _, name := range o.ToolSelection {
			if !irNode.DeclaresTool(name) {
				return ro, node, &SpecError{NodeID: nodeID, Dim: DimTools, Ref: name, Err: ErrToolNotDiscovered,
					Detail: fmt.Sprintf("the node's discovered tool set is %v", discoveredToolNames(irNode))}
			}
		}
		ro.ToolSelection = o.SelectedTools()
		node.ToolSelection = ro.ToolSelection
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

	// 1b. Un-apply refusal (P13 task 2.6, design Decision 3). A rewrite publishes a NEW pinned prompt;
	//     if that prompt's slot set drops a variable the DISCOVERED call site still supplies a value for,
	//     the runtime value would no longer bind — the node is un-applied. Refuse here, at resolve,
	//     naming the slot (P10 impact analysis), rather than emitting a diff that silently discards it.
	//
	//     Engages only when the prompt was actually overridden (a rewrite) AND the discovered prompt
	//     declared variables: the discovered prompt's own variables are the baseline the call site feeds,
	//     so a pinned prompt that no longer declares one of them is the un-apply case Decision 3 refuses.
	//     A slot the rewrite ADDS but the call site does not supply is the mirror case, caught by the
	//     satisfaction loop below (ErrUnsatisfiedSlot).
	if o.PromptRef != "" {
		for _, v := range irNode.Prompt.Variables {
			if slotSet[v] {
				continue // the rewrite kept this slot; the runtime value still binds
			}
			if _, rebound := o.Bindings[v]; rebound {
				continue // an explicit binding re-supplies it; not un-applied
			}
			return nil, &SpecError{NodeID: nodeID, Dim: DimPrompt, Ref: v, Err: ErrRewriteUnappliesNode,
				Detail: fmt.Sprintf("the rewritten prompt no longer declares slot %q, but the call site "+
					"still supplies a value for it; the rewrite would un-apply this node", v)}
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

// paramsEqual reports whether two provider-param maps hold the same keys and values. Used only to tell
// a parameter tune from a no-op model override; never a hash input.
func paramsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || av != bv {
			return false
		}
	}
	return true
}

func copyParams(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
