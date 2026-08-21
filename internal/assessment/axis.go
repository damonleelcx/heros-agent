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
// 🔴 The seven shared members are READ from `variantspec.Dimensions()` rather than retyped, and
// `TestTheSevenSharedAxesAreExactlyTheSevenDimensions` asserts it. A retyped closed set is a second
// copy, and the copy is what goes stale — an eighth Dimension added by a later phase would otherwise
// give the platform a configuration axis nothing ever assesses, with nothing going red.
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

	// ── The two P34 owns ────────────────────────────────────────────────────────────────────────
	//
	// 🔴 These have NO `variantspec.Dimension` behind them, and that is not an oversight this package
	// should route around. `DimHarness`'s own doc defines it as two things — "how many turns it runs
	// and in what control loop" — and P34 is the change that splits them. Until it lands there is no
	// configuration axis called `loop` and no `Dimension` for topology at all, so an assessment that
	// reported on them would be naming axes the configuration layer does not have.
	//
	// They are therefore members of THIS enum (an assessment reports nine, always nine — FR1) and are
	// refused with `RefusalAnalysis` until P34 exists. `P34Pending` is the single predicate; see
	// `report.go`. Task 9.2: this is stated rather than discovered.

	// AxisLoop — the control loop a multi-turn node runs in, and its stop conditions.
	AxisLoop Axis = "loop"
	// AxisGraph — the topology: nodes, edges, concurrency, conditional routing.
	AxisGraph Axis = "graph"
)

// sharedAxes are the seven that ARE configuration dimensions, in variantspec's own order.
func sharedAxes() []Axis {
	dims := variantspec.Dimensions()
	out := make([]Axis, 0, len(dims))
	for _, d := range dims {
		out = append(out, Axis(d))
	}
	return out
}

// Axes returns all nine, in report order: the seven configuration dimensions in variantspec's order,
// then the two P34 owns. A copy, so no caller can widen the enum.
//
// The order is the REPORT's spine — `Assessment` requires a finding for each of these and no others —
// but it is NOT the render order. Findings are rendered by evidence strength (FR5), which `Ordered`
// computes and which deliberately cuts across this list.
func Axes() []Axis {
	out := sharedAxes()
	return append(out, AxisLoop, AxisGraph)
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

// P34Pending reports whether this axis is one of the two whose CONFIGURATION does not exist yet.
//
// It is a predicate on the axis rather than a list at each call site for the reason task 9.2 states:
// the deferral has to be a fact the code can be asked about, so that the day P34 lands the deferral is
// removed in one place and every surface stops refusing at once.
func (a Axis) P34Pending() bool { return a == AxisLoop || a == AxisGraph }

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
