package assessment

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// axis.go is the noun dictionary (task 8.4). An axis is named here EXACTLY as the console, the CLI and
// the configuration layer name it, because a report that calls a surface `context` where the editor
// calls it `context assembly` makes the reader do a translation the platform should have done.

// Axis is one of the nine surfaces an assessment reports on.
//
// 🔴 The shared members are READ from `variantspec.Dimensions()` rather than retyped, and
// `TestTheSharedAxesAreExactlyTheDimensions` asserts it. A retyped closed set is a second copy, and the
// copy is what goes stale — a Dimension added by a later phase would otherwise give the platform a
// configuration axis nothing ever assesses, with nothing going red.
//
// 🔴 That is exactly what happened, on purpose, when P34 landed: `DimLoop` joined `Dimensions()`, the
// derived list grew from seven to eight, and this file's duplicate `AxisLoop` const became a second
// spelling of the same axis — which turned every axis-count assertion red until it was read from the
// dimension instead. The seam worked.
type Axis string

const (
	// AxisModel — which model each call site uses, and with what parameters.
	AxisModel Axis = Axis(variantspec.DimModel)
	// AxisPrompt — where each call site's prompt comes from.
	AxisPrompt Axis = Axis(variantspec.DimPrompt)
	// AxisSkills — what is bound at each call site.
	AxisSkills Axis = Axis(variantspec.DimSkills)
	// AxisContext — how a SINGLE call builds its message list.
	AxisContext Axis = Axis(variantspec.DimContext)
	// AxisTools — what is offered to the model.
	AxisTools Axis = Axis(variantspec.DimTools)
	// AxisMemory — what persists BETWEEN turns and sessions. The boundary against context is
	// variantspec's, unchanged: *between turns* is the line.
	AxisMemory Axis = Axis(variantspec.DimMemory)
	// AxisHarness — the scaffold around a call: sandboxing, ceilings, retries.
	AxisHarness Axis = Axis(variantspec.DimHarness)

	// ── The two P34 owned ───────────────────────────────────────────────────────────────────────
	//
	// Before P34 neither had a `variantspec.Dimension` behind it, and both were refused with
	// `RefusalAnalysis`: an assessment that reported on them would have been naming axes the
	// configuration layer did not have. `P34Pending` was the single predicate, so that the day P34
	// landed the deferral came off in one place.
	//
	// 🔴 That day has come for `loop`. `AxisLoop` is now READ from `variantspec.DimLoop`, exactly like
	// the seven above, so this enum has one spelling of it rather than two.

	// AxisLoop — the ITERATION POLICY: which control loop a node runs, what stops it, and how many turns
	// its author chose. A configuration dimension since P34 (ADR-014).
	AxisLoop Axis = Axis(variantspec.DimLoop)
	// AxisGraph — the topology: nodes, edges, concurrency, conditional routing.
	//
	// 🔴 It has no `Dimension` behind it and never will. P34 design D3 settles it: every member of
	// `Dimensions()` is a property of ONE node, and topology is a property BETWEEN nodes — so graph lives
	// at the spec level, beside `order` and `edges`. This axis is therefore a permanent non-dimension
	// member of the enum, which is different from being a pending one.
	AxisGraph Axis = "graph"
)

// sharedAxes are the ones that ARE configuration dimensions, in variantspec's own order. Eight since
// P34 added `DimLoop`; the count is derived, never written down here.
func sharedAxes() []Axis {
	dims := variantspec.Dimensions()
	out := make([]Axis, 0, len(dims))
	for _, d := range dims {
		out = append(out, Axis(d))
	}
	return out
}

// Axes returns all nine, in report order: the configuration dimensions in variantspec's order, then
// `graph`, which is spec-level rather than a dimension (P34 design D3). A copy, so no caller can widen
// the enum.
//
// 🔴 `AxisLoop` is NOT appended here any more. It arrives through `sharedAxes()` because it is a
// `Dimension` now, and appending it as well produced a report that named `loop` twice — caught by
// `TestNineAxesInARepositoryThatFailsAtEveryAxis` the moment P34 landed.
//
// The order is the REPORT's spine — `Assessment` requires a finding for each of these and no others —
// but it is NOT the render order. Findings are rendered by evidence strength (FR5), which `Ordered`
// computes and which deliberately cuts across this list.
func Axes() []Axis {
	out := sharedAxes()
	return append(out, AxisGraph)
}

// Valid reports membership. 🔴 The empty axis is invalid rather than defaulted: a finding nobody
// attributed to an axis is a finding that cannot be placed in a nine-row report.
func (a Axis) Valid() bool {
	for _, v := range Axes() {
		if v == a {
			return true
		}
	}
	return false
}

// String makes Axis printable without a conversion at every call site.
func (a Axis) String() string { return string(a) }

// P34Pending reports whether this axis's CONFIGURATION does not exist yet.
//
// It is a predicate on the axis rather than a list at each call site for the reason P33 task 9.2
// states: the deferral has to be a fact the code can be asked about, so that the day P34 lands the
// deferral is removed in one place and every surface stops refusing at once.
//
// 🔴 `loop` came off this list when P34 §3 landed `variantspec.DimLoop` and `registry.KindLoop`, and
// `graph` came off when P34 §5 landed `GraphGroup`, the predicate edge kind and the merge declaration.
// The predicate is kept — returning false for everything — rather than deleted along with its call
// sites, because deleting it would delete the RECORD that these two axes were once refused for a
// reason, and the next axis to arrive ahead of its configuration would have to rediscover the pattern.
func (a Axis) P34Pending() bool { return false }

// AxisNames returns the nine as sorted plain strings — the form a JSON schema and a TypeScript union
// both want. Sorted rather than in report order: a schema's `enum` is a set, and a set written in a
// meaningful order invites a reader to believe the order means something there too.
func AxisNames() []string {
	axes := Axes()
	out := make([]string, 0, len(axes))
	for _, a := range axes {
		out = append(out, string(a))
	}
	sort.Strings(out)
	return out
}
