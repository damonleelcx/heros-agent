// Package nodeaxisvalue answers, for ONE node and ONE axis, what that node's value is TODAY — resolved
// from the structure the customer reported, or `not_measured` with the input that would resolve it.
//
// # The gap this closes (P37 FR1, FR17)
//
// `internal/axisprojection` answers *"can the engine change this?"* — a verdict per (node, axis).
// `internal/assessment` answers *"what is this REPOSITORY like?"* — one finding per axis across every
// node. Neither answers the question an editor has to answer before it can render a control:
//
//	what is THIS node's context policy right now, so the reader can see what they are changing FROM?
//
// Until P32 there was no reader's node to ask it about, so the axis surfaces rendered a demonstration
// node instead (`/app/memory`'s `const NODE_ID = "recall"`). P32 supplies the node; this package
// supplies its current value.
//
// # 🔴 An unresolvable value is `not_measured`, NEVER the vocabulary's default
//
// This is the whole safety argument and it is P37 §5.3.
//
// Four of the eight axes have NO field on the wire at all. `runlink.WireIRNode` carries `ContextPolicy`,
// `Provider`, `ModelID`, `ToolCount` and `Language` — and nothing for memory, harness, loop or prompt.
// The tempting move is to render the vocabulary's identity element (`none`, `single-shot`) as the node's
// value, because it is what discovery's own struct defaults to.
//
// That would put a policy on the reader's screen that their node does not have. They would then author
// a change FROM a baseline that never existed, and the diff they approve would be against a fiction.
// `internal/discovery/emit.go` says the same thing about its own field: emitting `none` for every node
// is *"a statement about the EVIDENCE, not a placeholder"*.
//
// So the answer is `not_measured` with a NAMED missing input, and the name is what makes it actionable
// rather than an apology.
//
// # 🔴 The vocabulary is `internal/assessment`'s, not a fourth one
//
// `State`, `MissingInput` and `Axis` are imported, not redefined. P37 FR8: six axes with six state
// vocabularies is six times the copy and one reader's confusion — and `assessment`'s four states are
// already what the console renders and what P33 wrote the copy for. A package that introduced
// `nodeaxisvalue.StateUnknown` would be the fifth vocabulary in a product that has spent three phases
// converging on one.
//
// # It is a READ, and it materialises nothing
//
// Computed at request time from the stored structure. No table, no migration (P37 §2.4 / D-37.4): a
// stored copy of a node's current policy is a second source of truth for a value the IR already holds,
// and it goes stale the moment the customer re-runs discovery.
package nodeaxisvalue

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/transform"
)

// Value is one node's current value on one axis.
//
// Exactly one of `Current` and `MissingInput` is set, and which one is decided by `State`. They are two
// fields rather than one `string` because a renderer must not be able to print a missing-input name in
// the position a value goes — which is the same failure, one layer down, as printing a default.
type Value struct {
	NodeID string          `json:"node_id"`
	Axis   assessment.Axis `json:"axis"`
	// State is `observed` or `not_measured` here, and never the other two.
	//
	// `measured` is reserved for a number an eval run produced, and reading a field out of the IR is not
	// that. `refused` means THIS BUILD cannot assess the axis — a different owner, and not what an
	// absent wire field means. Both are in the shared enum and both are deliberately unreachable from
	// this package; `TestOnlyTwoStatesAreEverProduced` is the fence.
	State assessment.State `json:"state"`
	// Current is the node's value, present only when State is `observed`. Rendered verbatim.
	Current string `json:"current,omitempty"`
	// Detail is one clause of context a reader needs to act — the provider behind a model id, the
	// language behind a coverage answer. Never a status, never a second value.
	Detail string `json:"detail,omitempty"`
	// MissingInput names what would resolve this, present only when State is `not_measured`. A member of
	// `assessment.MissingInputs()`; the console holds one message per member.
	MissingInput assessment.MissingInput `json:"missing_input,omitempty"`
	// Because is the sentence that says WHY the input is missing, in the reader's terms. Required
	// alongside MissingInput for the reason design D1 gives: a `not_measured` that names a machine
	// identifier and nothing else is an apology, not an action.
	Because string `json:"because,omitempty"`
}

// NodeValues is one node's whole row: every axis, always, in `assessment.Axes()` order.
//
// 🔴 Every axis, always — never only the ones that resolved. P37 FR8's per-node matrix is nine columns
// wide for every node, and a row that omitted its unresolved axes would make absence invisible, which
// is the exact failure `not_measured` exists to prevent.
type NodeValues struct {
	NodeID   string  `json:"node_id"`
	Symbol   string  `json:"symbol,omitempty"`
	File     string  `json:"file,omitempty"`
	Language string  `json:"language,omitempty"`
	Values   []Value `json:"values"`
}

// Report is the whole read for one workflow.
type Report struct {
	WorkflowID     string `json:"workflow_id"`
	SourceRevision string `json:"source_revision,omitempty"`
	// CoverageVersion is the table this build carries, so a surface can say which set version its
	// picker was bound to (P37 FR5 — "the axis's closed vocabulary at its recorded set version").
	CoverageVersion string       `json:"coverage_version"`
	Nodes           []NodeValues `json:"nodes"`
}

// Build resolves every reported node's current value on every axis.
func Build(ir linkingest.WorkflowIR) Report {
	out := Report{
		WorkflowID:      ir.WorkflowID,
		SourceRevision:  ir.SourceRevision,
		CoverageVersion: transform.CoverageTableVersion(),
		Nodes:           make([]NodeValues, 0, len(ir.Nodes)),
	}
	for _, n := range ir.Nodes {
		out.Nodes = append(out.Nodes, ForNode(n))
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].NodeID < out.Nodes[j].NodeID })
	return out
}

// ForNode resolves one node's row.
func ForNode(n runlink.WireIRNode) NodeValues {
	row := NodeValues{NodeID: n.NodeID, Symbol: n.Symbol, File: n.File, Language: n.Language}
	for _, axis := range assessment.Axes() {
		row.Values = append(row.Values, valueFor(n, axis))
	}
	return row
}

// valueFor is the whole per-axis decision, in one place.
//
// A switch rather than a map of functions because the list is closed, short, and every arm has to be
// read against its neighbours: the interesting property of this function is which axes DO NOT resolve,
// and that is only visible when they are next to each other.
func valueFor(n runlink.WireIRNode, axis assessment.Axis) Value {
	switch axis {
	case assessment.AxisModel:
		return modelValue(n)
	case assessment.AxisContext:
		return contextValue(n)
	case assessment.AxisTools:
		return toolsValue(n)

	// ── The four with no field on the wire ───────────────────────────────────────────────────────
	//
	// 🔴 Read this block as one thing. Each of these is `not_measured` because the IR PAYLOAD HAS NO
	// FIELD FOR IT — not because the customer did something wrong, and not because the platform failed.
	// The day a field arrives, exactly one arm moves out of this block, and the fence
	// `TestAnAxisWithAWireFieldIsNeverNotMeasured` is what makes that a compile-and-test event rather
	// than something somebody has to notice.
	case assessment.AxisPrompt:
		return notVisible(n, axis,
			"a prompt's origin is a template reference resolved at the call site, and the reported "+
				"structure carries no field for it — so the platform cannot say which prompt version this "+
				"node uses today")
	case assessment.AxisSkills:
		return notVisible(n, axis,
			"skill bindings are registry rows resolved at run time, and the reported structure carries no "+
				"field for them")
	case assessment.AxisMemory:
		return notVisible(n, axis,
			"a memory strategy is a store read and written BETWEEN turns, and the reported structure "+
				"describes one call site at a time; this says nothing about whether this node has memory, "+
				"only that we have not looked between its turns")
	case assessment.AxisHarness:
		return notVisible(n, axis,
			"an execution envelope is a property of how this node is DEPLOYED — where it may reach, what "+
				"it may spend — and none of that is written at a call site in any language, so there is "+
				"nothing in a source snapshot to read it from")
	case assessment.AxisLoop:
		return notVisible(n, axis,
			"a control loop is the iteration policy AROUND a call, and the reported structure describes "+
				"the call; the platform is not told how many turns this node takes")
	case assessment.AxisGraph:
		return notVisible(n, axis,
			"topology is a property BETWEEN nodes rather than of one, so a per-node read cannot answer it; "+
				"the workflow's own graph view is where this axis is read")
	}

	// 🔴 Unreachable while `assessment.Axes()` is the loop's source, and it is. It is here because a
	// silent zero Value would render as `observed` with an empty current — a blank where a policy goes,
	// which is the one thing this package exists to prevent. If a tenth axis is ever added without a
	// case above, this makes it say so on the screen.
	return Value{
		NodeID: n.NodeID, Axis: axis, State: assessment.StateNotMeasured,
		MissingInput: assessment.MissingUnresolvedField,
		Because: fmt.Sprintf(
			"this build has no per-node reader for the %q axis, so nothing here can say what this node "+
				"does on it", axis),
	}
}

// modelValue reads the node's current model binding.
func modelValue(n runlink.WireIRNode) Value {
	// 🔴 The sentinel, not the empty string. `discovery` writes `unresolved` into BOTH fields when a
	// call site's model cannot be resolved — a literal value that a naive `!= ""` check reads as a real
	// model called "unresolved", and prints as this node's model.
	if unresolved(n.ModelID) || unresolved(n.Provider) {
		return Value{
			NodeID: n.NodeID, Axis: assessment.AxisModel, State: assessment.StateNotMeasured,
			MissingInput: assessment.MissingUnresolvedField,
			Because: "discovery could not resolve which model this call site binds — the value is " +
				"assembled somewhere the call site does not show",
		}
	}
	if n.ModelID == "" {
		return Value{
			NodeID: n.NodeID, Axis: assessment.AxisModel, State: assessment.StateNotMeasured,
			MissingInput: assessment.MissingUnresolvedField,
			Because: "the reported structure carries no model for this node, which a client older than " +
				"the field does; re-run `heros link --with-ir` from a current CLI",
		}
	}
	return Value{
		NodeID: n.NodeID, Axis: assessment.AxisModel, State: assessment.StateObserved,
		Current: n.ModelID, Detail: n.Provider,
	}
}

// contextValue reads the node's current context policy.
func contextValue(n runlink.WireIRNode) Value {
	if unresolved(n.ContextPolicy) {
		return Value{
			NodeID: n.NodeID, Axis: assessment.AxisContext, State: assessment.StateNotMeasured,
			MissingInput: assessment.MissingUnresolvedField,
			Because: "this call site's message assembly could not be resolved — it is built somewhere " +
				"the call does not show, so there is no policy here to read",
		}
	}
	if n.ContextPolicy == "" {
		return Value{
			NodeID: n.NodeID, Axis: assessment.AxisContext, State: assessment.StateNotMeasured,
			MissingInput: assessment.MissingUnresolvedField,
			Because: "the reported structure carries no context policy for this node; re-run " +
				"`heros link --with-ir` from a current CLI",
		}
	}
	return Value{
		NodeID: n.NodeID, Axis: assessment.AxisContext, State: assessment.StateObserved,
		Current: n.ContextPolicy, Detail: n.Language,
	}
}

// toolsValue reads how many tools this call site offers.
//
// 🔴 A COUNT, and the wire carries only a count on purpose: the tool NAMES are call-site identifiers
// from the customer's source and do not cross the boundary. So the honest value is "3 tools", and the
// surface must not imply it knows which three.
func toolsValue(n runlink.WireIRNode) Value {
	if n.ToolCount < 0 {
		return Value{
			NodeID: n.NodeID, Axis: assessment.AxisTools, State: assessment.StateNotMeasured,
			MissingInput: assessment.MissingUnresolvedField,
			Because:      "the reported tool count for this node is not a count",
		}
	}
	return Value{
		NodeID: n.NodeID, Axis: assessment.AxisTools, State: assessment.StateObserved,
		Current: fmt.Sprintf("%d tool%s", n.ToolCount, plural(n.ToolCount)),
		Detail:  "the names are call-site identifiers and do not cross the boundary; only the count does",
	}
}

// notVisible builds the `not_measured` answer for an axis the wire has no field for.
func notVisible(n runlink.WireIRNode, axis assessment.Axis, because string) Value {
	return Value{
		NodeID: n.NodeID, Axis: axis, State: assessment.StateNotMeasured,
		MissingInput: assessment.MissingNotVisibleStatically,
		Because:      because,
	}
}

// unresolved reports whether a field carries discovery's sentinel.
func unresolved(v string) bool { return v == discovery.UnresolvedSentinel }

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ── Live per-node coverage (P37 FR17) ─────────────────────────────────────────────────────────────

// PolicyCoverage is what the engine does with one policy at THIS node's call site, read from the
// engine's own table at request time.
//
// 🔴 This is what replaces `/app/context`'s hand-transcribed `COVERAGE` array. That array was kept
// honest by `TestContextCoverageTableMatchesEngine`, and the fence was correct — but it guarded a
// TRANSCRIPTION, and after this read there is no transcription to drift. Removing a fence whose subject
// still exists is a different act with the same diff, which is why P37 §9.3 reviews that removal on its
// own (design D5).
type PolicyCoverage struct {
	Policy string `json:"policy"`
	// Mode is the engine's own word: `identity`, `applied` or `declined`.
	Mode string `json:"mode"`
	// Reason is the engine's own sentence for that mode.
	Reason string `json:"reason,omitempty"`
}

// ContextCoverageForLanguage returns what each registered policy does at a call site in `language`.
//
// An unknown or absent language returns nil rather than the Go row. A coverage answer attributed to the
// wrong language is a claim about the reader's code drawn from a guess, and `nil` renders as
// `not_measured` on the surface — which is true and says so.
func ContextCoverageForLanguage(language string) []PolicyCoverage {
	lang := strings.ToLower(strings.TrimSpace(language))
	if lang == "" {
		return nil
	}
	var out []PolicyCoverage
	for _, c := range transform.ContextMaterializerCoverage() {
		if c.Language != lang {
			continue
		}
		out = append(out, PolicyCoverage{Policy: c.Policy, Mode: c.Mode, Reason: c.Reason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Policy < out[j].Policy })
	return out
}

// CoveredLanguages lists the languages whose context selection rewriter has landed, so a surface with
// no coverage for the reader's language can NAME the ones that do have it rather than saying only "no".
func CoveredLanguages() []string { return transform.ContextMaterializerLanguages() }
