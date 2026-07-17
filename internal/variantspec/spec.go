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

// Dimension is one of a node's four independently-overridable dimensions (FR2). Typed rather than
// stringly so that every error can name one and the set stays closed.
type Dimension string

const (
	DimModel   Dimension = "model"
	DimPrompt  Dimension = "prompt"
	DimSkills  Dimension = "skills"
	DimContext Dimension = "context"
)

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
)

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
}

// isEmpty reports whether this override sets nothing. A node listed in the ordering with no
// overrides is legitimate and common: it is a node that runs exactly as discovered.
func (o NodeOverride) isEmpty() bool {
	return o.ModelRef == "" && o.PromptRef == "" && len(o.SkillRefs) == 0 && o.ContextPolicy == ""
}

// Edge is one graph edge in the spec's declared ordering.
type Edge struct {
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Kind       string `json:"kind"` // "data" | "control"
}

// VariantSpec is the canonical desired-state config (FR3).
type VariantSpec struct {
	// WorkflowID ties the spec to the discovered workflow whose IR it overrides.
	WorkflowID string `json:"workflow_id"`
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
func (s *VariantSpec) Refs() []string {
	set := map[string]bool{}
	for _, o := range s.Nodes {
		for _, r := range []string{o.ModelRef, o.PromptRef, o.ContextPolicy} {
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
