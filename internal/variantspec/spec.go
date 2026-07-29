// Package variantspec implements P2's Configuration Layer: the Variant Spec — the canonical
// desired-state config — its resolution against the four registries, and the stable `config_hash`
// derived from it (PRD docs/prd/P2-config-runtime.md §6 FR1–FR5; spec
// openspec/changes/p2-config-runtime/specs/config-layer/spec.md; tasks 2.1, 2.5, 2.6).
//
// # The three contracts this package sits between
//
//	VariantSpec      what a human/P5.5 AUTHORS: a per-node delta of registry version_ids, plus a
//	                 node ordering and a target source_revision. Sparse — an absent dimension means
//	                 "leave the call site alone" (FR2).
//	Workflow IR      what P1 DISCOVERED at source_revision: the default model/prompt/skills/context
//	                 already at each call site.
//	ResolvedConfig   the two merged, with nothing left to defaulting — P0's frozen shape, hashed to
//	                 config_hash (see resolved.go).
//
// Resolution is the merge. This is why a Variant Spec can be a delta while config_hash still denotes
// a complete configuration: the dimensions nobody overrode are pinned by `source_revision`, because
// they are properties of the code, and the ones that were overridden are pinned by an immutable
// registry version_id.
//
// # Fail closed, before any side effect
//
// Resolve is the only way to get from a spec to a transform, and it aborts on the first thing it
// cannot account for (FR5, FR11): a node the IR does not contain, a ref that resolves to nothing, a
// context policy nobody registered, a call site the transform cannot rewrite safely. It creates no
// diff, no worktree, no run record, and makes no provider call — it is a pure read. The rejection
// names the node AND the dimension, because "which node, which dimension" is the whole of what a
// user needs to fix it (PRD §9, Product Designer: design the unhappy path first).
package variantspec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Dimension is one of a node's independently-overridable dimensions (FR2). Typed rather than stringly
// so that every error can name one and the set stays CLOSED.
type Dimension string

const (
	DimModel   Dimension = "model"
	DimPrompt  Dimension = "prompt"
	DimSkills  Dimension = "skills"
	DimContext Dimension = "context"
	// DimTools is tool SELECTION — which of the tools a node already offers the model it keeps
	// (P14, decisions.md D-14.2). It is a fifth member of the closed enum, and it is deliberately NOT
	// folded into DimSkills: a tool is selected (a call-site DELETION of an already-present element)
	// while a skill is bound (a CONSTRUCTION from a sealed contract), and conflating them in the
	// dimension layer would re-introduce, one level up, exactly the confusion D-14.1 splits in the IR.
	//
	// 🚫 There is no new registry `Kind` behind it. A tool is already identified by its call site, so
	// sealing it into the registry would invent a second identity for something that has one; a selection
	// is validated against the node's DISCOVERED tool set instead, fail-closed — the same pattern as an
	// `env` binding against DeclaredEnv and an `expr` binding against in_scope.
	DimTools Dimension = "tools"
	// DimMemory is what the agent CARRIES ACROSS invocations — a store read and written between turns
	// (P17, decisions.md D2). It is a sixth member of the closed enum, and it is deliberately NOT a mode
	// of DimContext: memory persists across invocations and sessions, while context assembly is how a
	// SINGLE call builds its message list. The codebase already draws that line — MemoryManagement's
	// confirmation signal is "memory read/write against a store between turns"
	// (internal/patternclassifier/taxonomy.go) — and the *between-turns* clause is the boundary.
	//
	// Collapsing the two would let a cross-session concern masquerade as a within-call one, and a merged
	// Dimension can never be cleanly un-merged once specs and hashes depend on it.
	//
	// 🔴 Unlike DimTools, this one DOES have a registry Kind behind it (registry.KindMemory): a memory
	// strategy is a (strategy, params) pair authored independently of any call site, so it has no
	// call-site identity to reuse. The Kind is hashed into the version_id, which is what makes a memory
	// ref pasted into another dimension fail closed instead of silently resolving.
	DimMemory Dimension = "memory"
)

// Dimensions is the closed enum, in a stable order. Exported so a consumer iterating dimensions cannot
// silently miss one that was added — the alternative is a hand-written list somewhere else that drifts.
func Dimensions() []Dimension {
	return []Dimension{DimModel, DimPrompt, DimSkills, DimContext, DimTools, DimMemory}
}

// Sentinel errors. Typed so the Loader, the UI, and P4 can tell "you asked for something that does
// not exist" from "we cannot safely do what you asked" — the first is the author's mistake, the
// second is a limit of the transform engine, and they need different messages.
var (
	// ErrUnknownNode: the spec overrides a node_id the IR at source_revision does not contain.
	ErrUnknownNode = errors.New("variantspec: spec references a node that is not in the IR")
	// ErrUnresolvedRef: a *_ref does not resolve to a registry entry (FR5, FR11).
	ErrUnresolvedRef = errors.New("variantspec: spec references a registry entry that does not exist")
	// ErrInlineDefinition: a spec tried to inline a definition instead of referencing a version ID.
	ErrInlineDefinition = errors.New("variantspec: spec inlines a definition instead of referencing a version_id")
	// ErrInvalidSpec: the spec is structurally malformed (empty ordering, duplicate node, edge to
	// nowhere, missing source_revision).
	ErrInvalidSpec = errors.New("variantspec: spec is malformed")
	// ErrUnsafeRewrite: the spec is valid, but the transform cannot rewrite this call site for this
	// dimension without risking behavior change. FR5 requires rejecting these BEFORE a transform is
	// generated rather than emitting a diff nobody can trust.
	ErrUnsafeRewrite = errors.New("variantspec: the transform cannot rewrite this call site safely")

	// ── P10 variable-bindings failure classes (task 3.2) ────────────────────────────────────────────
	// All reported through SpecError{NodeID, Dim, Ref} — no second error channel (task 3.2 / spec
	// "Binding failures use the existing specification error shape"). Dim is DimPrompt (a binding
	// satisfies a prompt slot); Ref is the offending slot, expression, variable, or kind.

	// ErrUnknownBindingKind: a binding declares a source kind outside {literal, expr, env, input}.
	ErrUnknownBindingKind = errors.New("variantspec: binding declares an unknown source kind")
	// ErrUnsatisfiedSlot: a prompt slot is satisfied by neither an explicit binding nor an
	// identically-spelled call-site expression.
	ErrUnsatisfiedSlot = errors.New("variantspec: prompt slot is not satisfied by any binding or call-site expression")
	// ErrAmbiguousSlot: a slot is satisfied by BOTH an explicit binding and an identically-spelled
	// call-site expression — rejected rather than silently preferring one.
	ErrAmbiguousSlot = errors.New("variantspec: prompt slot is satisfied twice (explicit binding and call-site expression)")
	// ErrBindingOutOfScope: an expr binding names an expression the IR does not record as in scope at
	// that call site, an env binding names an undeclared variable, or an input binding violates the
	// node's typed contract.
	ErrBindingOutOfScope = errors.New("variantspec: binding references something not available at the call site")

	// ErrRewriteUnappliesNode (P13 task 2.6, design Decision 3): a prompt rewrite whose slot-set change
	// drops a slot the discovered call site still supplies a value for would leave that runtime value
	// unbound — un-applying the node. It is REFUSED at resolve with the slot named (via P10 impact
	// analysis), never silently dropped or emitted as an un-bindable diff. Reported through
	// SpecError{NodeID, Dim: DimPrompt, Ref: slot} like every other binding failure (task 3.2).
	ErrRewriteUnappliesNode = errors.New("variantspec: a prompt rewrite drops a slot the call site still supplies, un-applying the node")

	// ErrToolNotDiscovered (P14, decisions.md D-14.2): a tool selection names a tool the IR does not
	// record for that node. FAIL CLOSED — the selection is rejected at resolve rather than applied to
	// nothing, because "keep only these three tools" applied to a set that does not contain one of them
	// silently means something different from what the author wrote.
	//
	// It is its OWN sentinel and not ErrUnresolvedRef: a tool is not a registry entry, so "this ref does
	// not resolve" would send the reader to look up a version_id that was never supposed to exist.
	ErrToolNotDiscovered = errors.New("variantspec: tool selection names a tool this node does not offer")
)

// BindingKind is the source of a prompt-slot binding (P10 task 3.1). The kind is recorded EXPLICITLY,
// never inferred from the value's shape — "gold" could be a literal or an env var name, and guessing
// is exactly the plausible-but-wrong behaviour this codebase declines.
type BindingKind string

const (
	// BindLiteral is a constant value written directly into the prompt. Runtime-changeable in bound
	// mode (it lives in the binding document).
	BindLiteral BindingKind = "literal"
	// BindExpr is an expression in scope at the call site (e.g. `ticket`). Structure, not data — it
	// names a variable in the user's lexical scope, so it stays at the rewritten call site (ADR-004).
	BindExpr BindingKind = "expr"
	// BindEnv is a named environment variable read at run time. Runtime-changeable (the variable NAME
	// lives in the binding document); an absent value at run time is a typed failure (task 3.5).
	BindEnv BindingKind = "env"
	// BindInput is a typed value from the node's P5 input contract.
	BindInput BindingKind = "input"
)

func (k BindingKind) valid() bool {
	switch k {
	case BindLiteral, BindExpr, BindEnv, BindInput:
		return true
	default:
		return false
	}
}

// BindingSource is one slot's binding: its kind and the value that kind interprets (a constant, an
// expression, a variable name, or an input reference).
type BindingSource struct {
	Kind  BindingKind `json:"kind"`
	Value string      `json:"value"`
}

// ApplyMode selects how a node's configuration is written into the codemod (P10 task 7.1, ADR-004).
// `inline` writes the value at the call site exactly as ADR-001 always did; `bound` writes an
// indirection plus a generated binding artifact and a resolved binding document, all in one change.
// It defaults to `inline` — nothing acquires an indirection unless asked.
type ApplyMode string

const (
	ApplyInline ApplyMode = "inline"
	ApplyBound  ApplyMode = "bound"
)

// Mode returns the node's apply mode, defaulting to inline. An empty or unset value is inline, so a
// pre-P10 spec and a node that states no mode both apply exactly as before this capability existed.
func (m ApplyMode) Mode() ApplyMode {
	if m == ApplyBound {
		return ApplyBound
	}
	return ApplyInline
}

// SpecError names the node and dimension a rejection is about, so an unhappy path can tell the user
// where to look instead of just that something was wrong.
type SpecError struct {
	NodeID string
	Dim    Dimension
	Ref    string // the offending ref, when there is one
	Err    error  // one of the sentinels above
	Detail string
}

func (e *SpecError) Error() string {
	var b strings.Builder
	b.WriteString(e.Err.Error())
	if e.NodeID != "" {
		fmt.Fprintf(&b, ": node %q", e.NodeID)
	}
	if e.Dim != "" {
		fmt.Fprintf(&b, ", dimension %q", e.Dim)
	}
	if e.Ref != "" {
		fmt.Fprintf(&b, ", ref %q", e.Ref)
	}
	if e.Detail != "" {
		fmt.Fprintf(&b, ": %s", e.Detail)
	}
	return b.String()
}

func (e *SpecError) Unwrap() error { return e.Err }

func specErr(nodeID string, dim Dimension, err error, format string, args ...any) *SpecError {
	return &SpecError{NodeID: nodeID, Dim: dim, Err: err, Detail: fmt.Sprintf(format, args...)}
}

// NodeOverride is a node's per-dimension delta. Every field is optional and an empty one means "no
// override — leave this dimension's construction at the call site untouched" (FR2, and the
// config-layer spec's "Absent override leaves the call site unchanged for that dimension").
//
// Every ref is an immutable registry version_id — a 64-hex content address from internal/registry —
// and nothing else. Inline definitions are rejected (FR3): a spec that carried its own model params
// would be a configuration whose content lives outside any registry, so it could never be resolved
// back from a config_hash months later, which is the one thing the whole lineage design exists to do.
type NodeOverride struct {
	ModelRef  string   `json:"model_ref,omitempty"`
	PromptRef string   `json:"prompt_ref,omitempty"`
	SkillRefs []string `json:"skill_refs,omitempty"`
	// ContextPolicy is a context-registry version_id, despite the name FR3 froze for it — it names a
	// registered (policy, params) entry, not a bare policy string.
	ContextPolicy string `json:"context_policy,omitempty"`
	// ApplyMode selects inline (default) or bound apply for this node (P10 task 7.1). Empty = inline.
	ApplyMode ApplyMode `json:"apply_mode,omitempty"`
	// Bindings maps a prompt slot name to its binding source (P10 task 3.1). Optional and additive:
	// omitempty + nil-when-empty, so a spec that declares no bindings serialises exactly as it did
	// before this field existed and hashes byte-identically (decisions.md D-1.4). The keys are slot
	// names; a slot with no entry here must be satisfied by an identically-spelled call-site expression
	// or the spec is rejected at resolve (task 3.3).
	Bindings map[string]BindingSource `json:"bindings,omitempty"`
	// ToolSelection is the KEPT subset of the node's discovered tools (P14, DimTools). Optional and
	// additive: omitempty + nil-when-empty, so a spec that prunes nothing serialises exactly as it did
	// before this field existed (decisions.md D-1.4, applied a second time).
	//
	// 🔴 The entries are DISCOVERED CALL-SITE IDENTIFIERS, not registry version_ids — a tool has no
	// registry identity (D-14.2). That is why this field does not feed Refs(): there is nothing here for
	// a registry to resolve, and listing it among the refs would send the loader looking for entries that
	// were never registered.
	//
	// It is a SET, not a sequence. Two specs that keep the same tools in different authoring order
	// denote one configuration and must share a config_hash, so the resolved projection sorts it —
	// unlike SkillRefs, whose order is identity-bearing because the call site binds them in that order.
	ToolSelection []string `json:"tool_selection,omitempty"`
	// ContextDropTolerance is the fraction of source context this node's JOB can afford to lose, in
	// [0,1] (P16 task 1.5, decisions.md D-2). It is read at proposal admissibility: a proposal whose
	// resolved policy would drive this node's drop ratio past the tolerance is inadmissible — rejected
	// before transform and before any eval spend.
	//
	// 🔴 It is a POINTER, and that is the whole of D-2. Additive, omit-when-absent: a node that declares
	// no tolerance emits NO `context_drop_tolerance` key, so it serialises byte-identically to a pre-P16
	// node and every frozen config_hash golden vector keeps reproducing. The alternative — an
	// always-present float defaulting to 0.0 — would both move the frozen bytes and encode a hostile
	// default (zero tolerance rejects every lossy policy). This is the same additive discipline
	// Bindings and ToolSelection above already follow.
	//
	// 🚫 It is NOT a policy fact and does not belong on the context entry: whether a given drop is
	// acceptable is a property of the node's job (a Retrieval node tolerates augmentation a
	// Summarization node does not), and a policy is shared across nodes with different tolerances.
	ContextDropTolerance *float64 `json:"context_drop_tolerance,omitempty"`
	// MemoryRef is a memory-registry version_id naming a sealed (strategy, params) entry — what this node
	// CARRIES ACROSS invocations (P17 task 3.2, decisions.md D3).
	//
	// 🔴 Additive and `omitempty`: a node that binds no memory emits NO `memory_ref` key, so it
	// serialises byte-identically to a pre-P17 override and every frozen config_hash golden vector keeps
	// reproducing. This is the fourth application of the discipline Bindings, ToolSelection and
	// ContextDropTolerance above follow, and it is the whole of D3 — an always-present field would move
	// the canonical bytes of EVERY existing node and orphan every keyed row.
	//
	// 🚫 It is a version_id and nothing else. A spec that inlined a strategy's params would be a
	// configuration whose content lives outside any registry, so it could never be resolved back from a
	// config_hash months later — rejected at resolve with ErrInlineDefinition, exactly like every other
	// ref (FR3).
	MemoryRef string `json:"memory_ref,omitempty"`
}

// isEmpty reports whether this override sets nothing. A node listed in the ordering with no
// overrides is legitimate and common: it is a node that runs exactly as discovered.
func (o NodeOverride) isEmpty() bool {
	return o.ModelRef == "" && o.PromptRef == "" && len(o.SkillRefs) == 0 && o.ContextPolicy == "" &&
		len(o.Bindings) == 0 && len(o.ToolSelection) == 0 && o.ContextDropTolerance == nil &&
		o.MemoryRef == ""
}

// SelectedTools returns the kept tool set in canonical (sorted, de-duplicated) order — the same order
// the resolved projection hashes, so a caller comparing two selections compares the SET rather than the
// order they happened to be written in. Nil when nothing is selected.
//
// 🚫 Deliberately NOT named Refs and deliberately not folded into VariantSpec.Refs(): these are
// call-site identifiers, and a registry lookup for one of them would fail on something that was never
// meant to be registered.
func (o NodeOverride) SelectedTools() []string {
	if len(o.ToolSelection) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(o.ToolSelection))
	for _, t := range o.ToolSelection {
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Edge is one graph edge in the spec's declared ordering.
type Edge struct {
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Kind       string `json:"kind"` // "data" | "control"
}

// InsertedAdapter is one catalog adapter the ordering-coherence validator inserted on a mismatching
// edge to make a re-arrangement coherent (P5 Decision 3). It is an EXPLICIT, inspectable node carrying
// its own io_contract — not a hidden runtime coercion — so it appears in the spec's node list and its
// diff against the parent. The transform engine (§2) materialises it as generated source.
type InsertedAdapter struct {
	// AdapterNodeID is the synthetic node_id of the inserted adapter, on the edge from→to.
	AdapterNodeID string `json:"adapter_node_id"`
	FromNodeID    string `json:"from_node_id"`
	ToNodeID      string `json:"to_node_id"`
	// CatalogKind is one of the fixed typedcontract catalog kinds (rename, projection, …).
	CatalogKind string `json:"catalog_kind"`
	// Params are the kind-specific parameters (e.g. rename from→to). InSchema/OutSchema are the
	// adapter's own io_contract, so a stored spec records exactly what was inserted and why.
	Params    map[string]any `json:"params"`
	InSchema  map[string]any `json:"in_schema"`
	OutSchema map[string]any `json:"out_schema"`
}

// VariantSpec is the canonical desired-state config (FR3).
type VariantSpec struct {
	// WorkflowID ties the spec to the discovered workflow whose IR it overrides.
	WorkflowID string `json:"workflow_id"`
	// ParentVariantID is the config_hash of the spec this one was derived from by a re-arrangement
	// edit (P5 lineage — Decision Data model). Empty for a root spec. It is NOT part of config_hash:
	// lineage is a property of how a spec was authored, not of the configuration it denotes, so two
	// specs reached by different edit paths that resolve to the same configuration still share a hash.
	ParentVariantID string `json:"parent_variant_id,omitempty"`
	// InsertedAdapters are the adapter nodes the validator inserted to make this arrangement coherent.
	// Empty when the reorder was coherent as-is. Carried on the spec so the compare view and the
	// transform engine both see exactly what was bridged.
	InsertedAdapters []InsertedAdapter `json:"inserted_adapters,omitempty"`
	// SourceRevision is the exact commit the transform targets. It is deliberately NOT part of
	// config_hash — P0's include set does not have it, and PRD §7/FR16 treat reproducibility as
	// {config_hash, source_revision, seed}, three separate axes. The generated diff is keyed by the
	// PAIR (the `transform` table is unique on (config_hash, source_revision)), which is what task
	// 2.3 means by "same config_hash + same source_revision -> byte-identical diff".
	//
	// The pair is not redundant: a config_hash still moves with the revision whenever a rewritten
	// call site's discovered DEFAULT changes, because defaults are part of resolved_config.
	SourceRevision string `json:"source_revision"`
	// Order is the node ordering the executor walks. Identity-bearing: reordering changes
	// config_hash (FR4), because the wiring is part of a configuration.
	Order []string `json:"order"`
	// Nodes maps node_id -> its override. A node in Order with no entry here runs as discovered.
	Nodes map[string]NodeOverride `json:"nodes"`
	Edges []Edge                  `json:"edges"`
}

// Validate checks the spec's internal structure — everything checkable without touching the IR or
// the registries. Resolve calls it first; it is exported because the UI can run it on a draft with
// no database round-trip.
//
// It does NOT check that refs resolve or that nodes exist: those need the registries and the IR, and
// live in Resolve. This split keeps "your JSON is malformed" separate from "your JSON is fine but
// points at something that isn't there".
func (s *VariantSpec) Validate() error {
	if s.SourceRevision == "" {
		return specErr("", "", ErrInvalidSpec, "source_revision must be set: a transform has no meaning without an exact target commit")
	}
	if len(s.Order) == 0 {
		return specErr("", "", ErrInvalidSpec, "order must list at least one node")
	}

	seen := map[string]bool{}
	for _, id := range s.Order {
		if id == "" {
			return specErr("", "", ErrInvalidSpec, "order contains an empty node_id")
		}
		if seen[id] {
			// A duplicate would make the ordering ambiguous and silently double-execute a node.
			return specErr(id, "", ErrInvalidSpec, "node appears more than once in order")
		}
		seen[id] = true
	}

	// Every overridden node must be in the ordering. Otherwise the override is dead config that the
	// author believes is live — it would never reach a call site, and config_hash would not record it.
	for _, id := range sortedKeys(s.Nodes) {
		if !seen[id] {
			return specErr(id, "", ErrInvalidSpec, "node has overrides but is not in order")
		}
		o := s.Nodes[id]
		for i, r := range o.SkillRefs {
			if r == "" {
				return specErr(id, DimSkills, ErrInvalidSpec, "skill_refs[%d] is empty", i)
			}
		}
		if dup := firstDuplicate(o.SkillRefs); dup != "" {
			return specErr(id, DimSkills, ErrInvalidSpec, "skill_refs binds %q twice", dup)
		}
		// Tool selection — the structure checkable without the IR. Whether the named tools EXIST at the
		// node is the IR's question and lives in Resolve, exactly as a skill_ref's existence does.
		for i, t := range o.ToolSelection {
			if t == "" {
				return specErr(id, DimTools, ErrInvalidSpec, "tool_selection[%d] is empty", i)
			}
		}
		if dup := firstDuplicate(o.ToolSelection); dup != "" {
			// A duplicate is rejected rather than de-duplicated: a selection is a SET, so writing a tool
			// twice means the author believes something about it that is not true, and silently collapsing
			// it would hide the misunderstanding rather than surface it.
			return specErr(id, DimTools, ErrInvalidSpec, "tool_selection keeps %q twice", dup)
		}
		// Drop tolerance — a RATIO, so the only structural claim is its range (P16 task 1.5). Whether a
		// given policy would exceed it needs the resolved policy and the node's measured drop, so that
		// check lives at proposal admissibility, not here. A value outside [0,1] is rejected rather than
		// clamped: "tolerate 1.5 of the context" is a misunderstanding, and silently reading it as 1.0
		// would hide the misunderstanding behind a gate that then never bites.
		if t := o.ContextDropTolerance; t != nil && (*t < 0 || *t > 1) {
			return specErr(id, DimContext, ErrInvalidSpec,
				"context_drop_tolerance is %v, want a ratio in [0,1]", *t)
		}
		// Memory ref — the one thing checkable without the registry: that it is a REFERENCE at all
		// (P17 task 3.2 / 5.6). An inlined strategy definition is a structural error, not a dangling ref,
		// so it gets ErrInlineDefinition rather than ErrUnresolvedRef: the author did not point at a
		// missing entry, they declined to point at one. See inlinesDefinition for what counts and why.
		if inlinesDefinition(o.MemoryRef) {
			return &SpecError{NodeID: id, Dim: DimMemory, Ref: o.MemoryRef, Err: ErrInlineDefinition,
				Detail: "memory_ref carries an inline strategy definition; it must be a memory-registry " +
					"version_id, so the configuration is resolvable back from a config_hash"}
		}
		// Binding structure — everything checkable without the IR or registries (kind is in the set; a
		// non-literal binding names something). The scope/declared/contract checks and the exactly-once
		// satisfaction rule need the IR and the resolved prompt, so they live in Resolve (task 3.2/3.4).
		for _, slot := range sortedKeys(o.Bindings) {
			b := o.Bindings[slot]
			if !b.Kind.valid() {
				return &SpecError{NodeID: id, Dim: DimPrompt, Ref: slot, Err: ErrUnknownBindingKind,
					Detail: fmt.Sprintf("slot %q declares kind %q, want one of literal, expr, env, input", slot, b.Kind)}
			}
			// A literal may legitimately be the empty string (a constant blank). expr/env/input must
			// name something — an empty reference cannot be validated or materialised.
			if b.Kind != BindLiteral && b.Value == "" {
				return specErr(id, DimPrompt, ErrInvalidSpec, "slot %q binding of kind %q has an empty value", slot, b.Kind)
			}
		}
	}

	// Edges must connect nodes the ordering knows about, or the graph the executor walks is not the
	// graph the author described.
	for i, e := range s.Edges {
		if !seen[e.FromNodeID] {
			return specErr(e.FromNodeID, "", ErrInvalidSpec, "edges[%d] starts at a node that is not in order", i)
		}
		if !seen[e.ToNodeID] {
			return specErr(e.ToNodeID, "", ErrInvalidSpec, "edges[%d] ends at a node that is not in order", i)
		}
		switch e.Kind {
		case "data", "control":
		default:
			return specErr("", "", ErrInvalidSpec, "edges[%d].kind is %q, want data or control", i, e.Kind)
		}
	}
	return nil
}

// Refs returns every registry version_id the spec references, deduplicated and sorted. Used by
// Resolve to fail closed on dangling refs, and by the UI to show what an author pinned.
//
// 🚫 ContextDropTolerance is deliberately absent, for the same reason ToolSelection is: it is a NUMBER,
// not a registry address. Listing it here would send the loader looking up "0.2" as a version_id and
// fail closed on a value that was never meant to be registered.
func (s *VariantSpec) Refs() []string {
	set := map[string]bool{}
	for _, o := range s.Nodes {
		for _, r := range []string{o.ModelRef, o.PromptRef, o.ContextPolicy, o.MemoryRef} {
			if r != "" {
				set[r] = true
			}
		}
		for _, r := range o.SkillRefs {
			set[r] = true
		}
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

// inlinesDefinition reports whether a ref field carries a DEFINITION rather than a reference to one
// (FR3, P17 task 5.6). It is the one inline-vs-reference test in the package, read by Validate (the
// structural gate) and again by resolveNode (defense in depth, for a caller that assembled a Resolve
// by hand) — 禁止分裂 source-of-truth: two copies would be two rules to keep true.
//
// A version_id is an opaque token. A JSON object or array is the shape params take, so a ref whose
// first non-space byte is `{` or `[` is an author writing the strategy out instead of registering it.
// That is the mistake FR3 exists to catch, and catching it here means it is caught BEFORE any diff,
// worktree, or provider call exists.
//
// 🚫 Deliberately NOT a 64-hex format check. Refs are opaque to this package — a test registry, a
// future addressing scheme, or a shortened alias are all legitimate references, and rejecting them
// would be this layer inventing a registry rule it does not own. What it CAN say is "this is not a
// reference at all", and it says only that.
func inlinesDefinition(ref string) bool {
	t := strings.TrimSpace(ref)
	return strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")
}

func firstDuplicate(xs []string) string {
	seen := map[string]bool{}
	for _, x := range xs {
		if seen[x] {
			return x
		}
		seen[x] = true
	}
	return ""
}
