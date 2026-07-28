package authoring

import (
	"fmt"
	"sort"
	"strings"
)

// WIRING AUTHORING (P15 15d, tasks 19.3–19.9).
//
// # Why this axis needs more than the shared contract
//
// A graph editor LOOKS like it can do anything. Dragging a node is a two-second gesture that expresses
// an arbitrary rewiring, and nothing about the gesture communicates that the platform materializes
// exactly ONE shape — a transposition of two adjacent, independent statements — while a merge, a prune,
// an edge change or a non-adjacent move is refused.
//
// Left alone, the editor becomes a machine for generating disappointment. Worse, an unmaterializable
// wiring change is not merely un-appliable: it is UNSCOREABLE. Evaluating a wiring-changed `config_hash`
// against unchanged source would score the base configuration under a variant's hash — a false result,
// and the exact failure the interim refusal exists to prevent. So this axis holds a harder line than the
// other three: a refused wiring draft is not a scoreable variant, and the surface must not present it as
// one waiting for a number.
//
// # The compensating half
//
// This is also the axis with the strongest PRE-SUBMISSION safety net anywhere in the platform. The
// typed-contract coherence gate decides, statically and cheaply — no eval spend, no model call, no
// build — whether an ordering breaks a producer→consumer contract. Moving that decision to preflight is
// what turns a guessing surface into one that tells the truth about every gesture as it is made.

// WiringShape names the KIND of wiring change a draft expresses. It is a closed vocabulary because a
// surface groups refusals by it, and a shape invented per call site would make that grouping meaningless.
type WiringShape string

const (
	ShapeTransposition WiringShape = "adjacent transposition"
	ShapeMerge         WiringShape = "merge"
	ShapePrune         WiringShape = "prune"
	ShapeEdgeChange    WiringShape = "edge change"
	ShapeNonAdjacent   WiringShape = "non-adjacent move"
	ShapeMultiSwap     WiringShape = "more than one transposition"
	ShapeParallelize   WiringShape = "parallelization"
)

// WiringShapes is every shape, sorted. Exported so a surface can enumerate what it may refuse rather
// than discovering the vocabulary one refusal at a time.
func WiringShapes() []WiringShape {
	out := []WiringShape{ShapeTransposition, ShapeMerge, ShapePrune, ShapeEdgeChange,
		ShapeNonAdjacent, ShapeMultiSwap, ShapeParallelize}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// CoherenceBreak is one producer→consumer contract the draft would violate.
//
// All three names are required, and that is the point. "Invalid ordering" is the refusal this type
// exists to make impossible: the platform KNOWS which consumer, which producer, and which field, and
// withholding them leaves the author with a graph and no idea which part of it to look at.
type CoherenceBreak struct {
	// Consumer is the node whose input would become undefined.
	Consumer string `json:"consumer"`
	// Producer is the node that produces the field the consumer requires.
	Producer string `json:"producer"`
	// Field is what would become undefined.
	Field string `json:"field"`
	// Detail is the gate's own sentence, rendered verbatim.
	Detail string `json:"detail"`
}

// Named reports whether this break names all three. A break missing one is a bug in whatever produced
// it, and the preflight result carries this so a test can say so rather than a reviewer noticing.
func (b CoherenceBreak) Named() bool {
	return b.Consumer != "" && b.Producer != "" && b.Field != ""
}

// InsertedAdapter is a component the platform would ADD to make an ordering legal.
//
// It is surfaced BEFORE submission, never only in the diff afterwards. An indirection never hides a
// value from review: an author who reorders two nodes and later receives a diff containing a component
// they never saw proposed cannot meaningfully review it, and the gate having proved the adapter drops
// nothing required does not change that — they did not agree to it.
type InsertedAdapter struct {
	NodeID    string `json:"node_id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Kind      string `json:"kind"`
	Rationale string `json:"rationale"`
}

// WiringVerdict is preflight's answer for a wiring draft.
//
// `Scoreable` is a separate field from `Admissible` on purpose. They are the same today — only a
// materializable change may be scored — but they answer different questions, and collapsing them would
// let a future "score it anyway with the wiring ignored" mode look like a small change rather than the
// false measurement it would be.
type WiringVerdict struct {
	Verdict Verdict `json:"verdict"`
	// Shape is what kind of change this is, always populated.
	Shape WiringShape `json:"shape"`
	// Breaks are the contract violations, when the ordering is incoherent.
	Breaks []CoherenceBreak `json:"breaks,omitempty"`
	// Adapters are the components that would be inserted, when the ordering is adapted.
	Adapters []InsertedAdapter `json:"adapters,omitempty"`
	// Refusal carries the named cause when refused.
	Refusal Refusal `json:"refusal,omitempty"`
	// Scoreable reports whether this draft may be submitted for evaluation. 🔴 FALSE for every refused
	// shape — the property that keeps a wiring hash from being scored against unchanged source.
	Scoreable bool `json:"scoreable"`
}

// RecordedIntent is a refused wiring draft the surface chose to keep.
//
// It is explicitly NOT a variant: it has no configuration hash, it is never evaluated, and it must never
// be described as pending or queued, which would imply somebody is working on it. Keeping it is useful —
// it records what the author wanted — and calling it anything else would be a small lie that compounds.
type RecordedIntent struct {
	Shape WiringShape `json:"shape"`
	Nodes []string    `json:"nodes"`
	// Reason is why it cannot be applied, verbatim.
	Reason string `json:"reason"`
	// Applicable and Scoreable are both false, always. They are present as fields rather than implied by
	// the type so a caller cannot accidentally treat this as a variant and a test can assert it.
	Applicable bool `json:"applicable"`
	Scoreable  bool `json:"scoreable"`
}

// OrderingGate is the typed-contract coherence gate, as this package needs it.
//
// It is an interface so `internal/authoring` keeps its independence from the transform side while the
// PRODUCTION implementation calls the very same `GateReorder` → `ValidateOrdering` a compile does. A
// second validator is how an editor starts blessing what the compiler rejects, so the wiring adapter
// asserts it delegates.
type OrderingGate interface {
	// Check validates a candidate ordering. It returns the breaks when incoherent, the adapters when
	// adapted, and neither when coherent as-is.
	Check(order []string, edges []WiringEdge) (breaks []CoherenceBreak, adapters []InsertedAdapter)
}

// WiringEdge is one edge of the DESIRED wiring.
//
// 🔴 Kind is not decoration. Only a DATA edge carries an I/O-contract obligation, so an edge that loses
// its kind on the way to the gate is an edge the gate skips — and a draft whose every edge was skipped
// comes back "coherent" no matter what it does to the graph. An earlier version of this interface passed
// bare `[2]string` pairs and produced exactly that: a consumer ordered before its producer, admitted.
type WiringEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Kind is "data" or "control", copied from the discovered edge.
	Kind string `json:"kind"`
}

// Materializability answers whether the transform can emit source for a shape.
//
// Supplied by the caller from the real planner, for the same reason the coverage table is: preflight and
// the transform must be the same answer.
type Materializability interface {
	// CanMaterialize reports whether this shape, on this language, can be emitted, and why not otherwise.
	CanMaterialize(shape WiringShape, language string) (ok bool, reason string)
}

// WiringDraft is one authored wiring change.
type WiringDraft struct {
	NodeIDs []string
	// Order and Edges are the DESIRED wiring.
	Order []string
	Edges []WiringEdge
	// Shape is what the author expressed.
	Shape WiringShape
	// Language is the workflow's discovered language.
	Language string
	// Independent reports whether the analysis PROVED the affected nodes data-independent. Absent proof
	// is not permission: an unprovable independence is a refusal, matching the conservative posture the
	// statement materializer already takes.
	Independent bool
	// Dependency names what blocks independence, when it is not proven.
	Dependency string
}

// PreflightWiring decides a wiring draft, before submission and before any codemod.
//
// The order is deliberate: coherence FIRST, because "this breaks your graph" is a different and more
// urgent answer than "we cannot apply this shape yet", and a reader told the second when the first is
// also true would fix the wrong thing.
func PreflightWiring(d WiringDraft, gate OrderingGate, mat Materializability) WiringVerdict {
	out := WiringVerdict{Shape: d.Shape}

	if gate == nil || mat == nil {
		out.Verdict = VerdictRefused
		out.Refusal = Refusal{
			Cause:  "authoring: the wiring gate or the materializability probe was not wired, so this draft cannot be decided",
			Shape:  string(d.Shape),
			NodeID: firstOf(d.NodeIDs),
		}
		return out
	}

	// 1. Coherence. A break here means the graph would not run, whatever the shape.
	breaks, adapters := gate.Check(d.Order, d.Edges)
	if len(breaks) > 0 {
		out.Verdict = VerdictRefused
		out.Breaks = breaks
		out.Refusal = Refusal{Cause: describeBreaks(breaks), NodeID: breaks[0].Consumer,
			Field: breaks[0].Field, Shape: string(d.Shape)}
		return out
	}
	out.Adapters = adapters

	// 2. Independence, for a parallelization. Unprovable is a refusal, not a permission.
	if d.Shape == ShapeParallelize && !d.Independent {
		out.Verdict = VerdictRefused
		out.Refusal = Refusal{
			Cause: fmt.Sprintf(
				"nodes %s cannot be marked parallelizable: their data-independence is not proven%s — "+
					"an unprovable independence is refused rather than assumed",
				strings.Join(d.NodeIDs, " and "), dependencyClause(d.Dependency)),
			NodeID: firstOf(d.NodeIDs), Shape: string(ShapeParallelize)}
		return out
	}

	// 3. Materializability. The shape the transform can emit is the shape that may be applied.
	ok, reason := mat.CanMaterialize(d.Shape, d.Language)
	if !ok {
		out.Verdict = VerdictRefused
		out.Refusal = Refusal{
			Cause:  fmt.Sprintf("a %s cannot be materialized as source: %s", d.Shape, reason),
			NodeID: firstOf(d.NodeIDs), Shape: string(d.Shape)}
		return out
	}

	out.Verdict = VerdictAdmissible
	// 🔴 Scoreable ONLY here. Every refused path above leaves it false, which is what keeps a wiring
	// `config_hash` from being submitted for scoring against source that was never rearranged.
	out.Scoreable = true
	return out
}

// IntentFor turns a refused wiring verdict into a recorded intent — the only thing a refused draft may
// become. It refuses to build one from an admissible verdict, because an applicable change belongs in
// the variant list, not in a list of things we could not do.
func IntentFor(d WiringDraft, v WiringVerdict) (RecordedIntent, bool) {
	if v.Verdict != VerdictRefused {
		return RecordedIntent{}, false
	}
	return RecordedIntent{
		Shape: d.Shape, Nodes: append([]string(nil), d.NodeIDs...),
		Reason: v.Refusal.Cause,
		// Both false, always. Stated rather than implied so a consumer cannot read the zero value as
		// "not yet decided".
		Applicable: false, Scoreable: false,
	}, true
}

// MayEnqueueEvaluation is the single predicate every evaluation entry point consults for a wiring draft.
//
// It exists as a FUNCTION rather than as a comparison at each call site so there is exactly one place
// the rule lives. An `if verdict == admissible` copied into three schedulers is three chances to get it
// wrong once — and getting it wrong once produces a false measurement, not a visible error.
func MayEnqueueEvaluation(v WiringVerdict) bool { return v.Scoreable }

func describeBreaks(breaks []CoherenceBreak) string {
	parts := make([]string, 0, len(breaks))
	for _, b := range breaks {
		parts = append(parts, fmt.Sprintf(
			"node %q consumes %q, which node %q produces — this ordering runs the consumer first, so the "+
				"field would be undefined", b.Consumer, b.Field, b.Producer))
	}
	return strings.Join(parts, "; ")
}

func dependencyClause(dep string) string {
	if dep == "" {
		return ""
	}
	return fmt.Sprintf(" (%s)", dep)
}

func firstOf(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}
