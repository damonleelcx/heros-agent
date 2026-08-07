// Package axisprojection crosses the platform's total coverage table with the nodes a customer chose to
// report — `coverage × your nodes`.
//
// # The gap this closes
//
// `/app/coverage` renders a full, correct table: `cov-c19cf0c4 · 128 apply / 123 refuse`. It is
// byte-identical to what `heros coverage` prints locally, and it says **nothing about the reader's
// nodes**. Meanwhile the reader's nodes arrive in the opt-in structure payload and nothing has ever
// multiplied the two. Neither side is a new claim — each is a fact its owner already holds — and the
// product of them is the difference between *"128 apply / 123 refuse"* and *"of your 40 nodes, 31 are
// undeliverable by both routes, and here they are."*
//
// # 🔴 THE PLATFORM DOES NOT COMPUTE A VERDICT. Not "does not today" — cannot.
//
// This is the single rule the whole package is built around, and the failure it prevents is specific.
// The platform knows a node's language. It could look up the (axis, language) cell and produce an
// answer that is right most of the time — and it would silently claim `applies` for exactly the call
// sites that refuse for their own shape (`call-site-cannot-carry-it`: arguments unpacked from a mapping,
// a tool list assembled at run time, a registry row with no locator). That information lives only in the
// source, which does not cross the boundary and must not.
//
// A projection that is right most of the time is worse than no projection, because it is the input to a
// customer's decision about what to author. So a verdict is only ever COPIED from what the CLI
// transmitted, and `TestTheProjectionDerivesNoVerdict` is a static check that no path here produces one
// from platform-held node properties.
//
// # It is a READ, not a table
//
// Computed at read time from `transform.AxisCoverage()` (in-process, versioned) and the stored
// structure. Not materialised, for two reasons in priority order: a materialised projection goes stale
// the moment the coverage table version moves and would then be a second source of truth for a refusal;
// and it would be a table whose entire content is derivable, which `careful-table-creation` forbids.
package axisprojection

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/transform"
)

// CellState is what one (node, axis) square says. FOUR states, and the fourth is the point.
type CellState string

const (
	// StateApplies — the CLI's engine produced an edit here, against the customer's real source.
	StateApplies CellState = "applies"
	// StateRefused — the CLI's engine refused here, and Cause names which of the three classes.
	StateRefused CellState = "refused"
	// StateNotApplicable — the axis does not exist for this node's language at all: the coverage table
	// has no cell for the pair.
	//
	// 🔴 THIS IS A CLAIM ABOUT THE CUSTOMER'S CODE and it is the one thing this data must never say by
	// accident. It is produced ONLY from a coverage table that genuinely carries no row — never from a
	// missing verdict, never from a missing language, never from an absent structure. `language-coverage`
	// made absence-as-not-applicable a named defect; a new consumer is the classic place it gets lost,
	// so it is restated here and fenced in `TestNotApplicableIsNeverProducedFromAnAbsentInput`.
	StateNotApplicable CellState = "not-applicable"
	// StateNotReported — the platform was never told. The FOURTH state.
	//
	// It is not a polite way of saying "no". It is the honest answer for a node whose verdict was never
	// transmitted — because the developer linked without `--with-ir`, because their CLI predates the
	// field, or because the engine declined to answer for that pair — and it names the command that
	// would report it.
	StateNotReported CellState = "not-reported"
)

// Cell is one (node, axis) square.
type Cell struct {
	NodeID string    `json:"node_id"`
	Axis   string    `json:"axis"`
	State  CellState `json:"state"`
	// Cause is the stable refusal-class identifier, present only on a refusal. Never a sentence: the
	// console renders its own copy from its own catalogue, so a CLI three versions old cannot put stale
	// prose on a paid surface.
	Cause string `json:"cause,omitempty"`
	// Owner is who can close the refusal — `nobody`, `you`, `the platform` — carried as DATA beside the
	// cause rather than inferred from it at each surface. It is the one word that decides what a reader
	// does next.
	Owner string `json:"owner,omitempty"`
	// Stale marks a cell whose verdict was computed against a coverage table this build does not carry.
	// A stale cell is SHOWN and EXCLUDED FROM EVERY TOTAL — the same discipline `unverified` gets in the
	// delta ledger.
	Stale bool `json:"stale,omitempty"`
}

// NodeRow is one reported node and its cells.
type NodeRow struct {
	NodeID string `json:"node_id"`
	Symbol string `json:"symbol,omitempty"`
	File   string `json:"file,omitempty"`
	// Language is what the CLI's frontend reported. ABSENT when it reported none — never inferred here
	// from the file extension, because a guessed language would make a guessed verdict look computed.
	Language string `json:"language,omitempty"`
	Cells    []Cell `json:"cells"`
}

// AxisTotals is one axis's counts across this tenant's nodes, WITH its denominator.
//
// 🔴 Every count carries `Nodes`. A proportion rendered without the count behind it is a number a reader
// cannot check — "68% covered" over three nodes and over four hundred are the same string and different
// facts — so the denominator is part of the type rather than something a surface is trusted to add.
type AxisTotals struct {
	Axis    string `json:"axis"`
	Applies int    `json:"applies"`
	Refused int    `json:"refused"`
	// NotApplicable is how many nodes' languages the axis has no coverage row for.
	NotApplicable int `json:"not_applicable"`
	// NotReported is how many nodes the platform was never told about for this axis. It is a first-class
	// count, not a remainder somebody computes by subtraction.
	NotReported int `json:"not_reported"`
	// Nodes is the DENOMINATOR: how many reported nodes this axis was crossed with.
	Nodes int `json:"nodes"`
	// StaleExcluded is how many cells were dropped from the four counts above because their verdicts
	// were computed against a different coverage table. Reported so the arithmetic is checkable: the
	// four states plus this equals Nodes, always.
	StaleExcluded int `json:"stale_excluded"`
}

// Projection is the whole read for one workflow.
type Projection struct {
	WorkflowID     string `json:"workflow_id"`
	SourceRevision string `json:"source_revision"`
	// ReportedAt is when the structure was transmitted.
	ReportedAt string `json:"reported_at,omitempty"`
	// CoverageVersion is the table THIS BUILD carries.
	CoverageVersion string `json:"coverage_version"`
	// ReportedCoverageVersion is the table the customer's verdicts were computed against. ABSENT when
	// they reported none, which is what a pre-P29 CLI does.
	ReportedCoverageVersion string `json:"reported_coverage_version,omitempty"`
	// Stale is true when the two versions differ AND the customer reported one. Two builds' answers are
	// then mixed, so the counts exclude the customer's cells and say so.
	Stale bool `json:"stale"`
	// Axes are the axes this projection covers, in the engine's own order.
	Axes   []string     `json:"axes"`
	Totals []AxisTotals `json:"totals"`
	Nodes  []NodeRow    `json:"nodes"`
	// NodeCount is the denominator for the whole projection.
	NodeCount int `json:"node_count"`
	// VerdictsReported is how many nodes carried at least one verdict. It is the number that tells a
	// reader whether they opted in at all, and it is the difference between "the platform has nothing to
	// say" and "you have not told it anything".
	VerdictsReported int `json:"verdicts_reported"`
}

// causeOwner maps a refusal class to who can close it.
//
// Spelled from the engine's own constants, so the boundary and the engine cannot disagree about what a
// valid cause is — a check with its own copy of a closed set is the copy that goes stale.
func causeOwner(cause string) string {
	switch transform.CauseClass(cause) {
	case transform.CauseNotAtCallSite:
		return "nobody"
	case transform.CauseCallSiteShape:
		return "you"
	case transform.CauseNoMaterializer:
		return "the platform"
	}
	return ""
}

// Build crosses the running build's coverage table with one stored structure.
//
// 🔴 Read what it does NOT do. It never inspects a node's provider, model, context policy or tool count
// to decide a state; it never looks up (axis, language) to produce `applies` or `refused`. The ONLY
// inputs to a verdict are the verdicts the customer transmitted. The coverage table is used for exactly
// one thing: deciding whether a cell EXISTS at all — which is `not-applicable`, a claim about the
// language rather than about the call site.
func Build(ir linkingest.WorkflowIR, buildCoverageVersion string) Projection {
	cells := transform.AxisCoverage()

	// hasRow answers "does this build's coverage table carry any row for (axis, language)?" — the ONLY
	// question the table is asked here.
	hasRow := map[string]bool{}
	for _, c := range cells {
		hasRow[c.Axis+"|"+c.Language] = true
	}

	axes := transform.CoverageAxes()
	p := Projection{
		WorkflowID: ir.WorkflowID, SourceRevision: ir.SourceRevision,
		CoverageVersion:         buildCoverageVersion,
		ReportedCoverageVersion: ir.CoverageVersion,
		Axes:                    axes,
		Nodes:                   make([]NodeRow, 0, len(ir.Nodes)),
		NodeCount:               len(ir.Nodes),
	}
	if !ir.ReceivedAt.IsZero() {
		p.ReportedAt = ir.ReceivedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	// 🔴 Stale only when the customer REPORTED a version and it differs. An ABSENT version is not stale
	// — it is not reported, which is a different fact with a different remedy, and conflating them would
	// tell every pre-P29 client that their data was from the wrong build.
	p.Stale = ir.CoverageVersion != "" && ir.CoverageVersion != buildCoverageVersion

	totals := map[string]*AxisTotals{}
	for _, axis := range axes {
		totals[axis] = &AxisTotals{Axis: axis, Nodes: len(ir.Nodes)}
	}

	for _, n := range ir.Nodes {
		row := NodeRow{NodeID: n.NodeID, Symbol: n.Symbol, File: n.File, Language: n.Language}
		// Transmitted verdicts, keyed by axis. This map is the ONLY source of `applies` and `refused`.
		reported := map[string]runlink.WireAxisVerdict{}
		for _, v := range n.AxisVerdicts {
			reported[v.Axis] = v
		}
		if len(n.AxisVerdicts) > 0 {
			p.VerdictsReported++
		}

		for _, axis := range axes {
			cell := Cell{NodeID: n.NodeID, Axis: axis, Stale: p.Stale}
			v, told := reported[axis]
			switch {
			case told && v.Status == runlink.VerdictApplies:
				cell.State = StateApplies
			case told && v.Status == runlink.VerdictRefused:
				cell.State = StateRefused
				cell.Cause = v.Cause
				cell.Owner = causeOwner(v.Cause)
			case !told && row.Language != "" && !hasRow[axis+"|"+row.Language]:
				// The table carries NO ROW for this (axis, language). That is a fact about the LANGUAGE,
				// which the platform legitimately holds — the engine simply has nothing to say here in
				// any repository. It is the one honest `not-applicable`.
				cell.State = StateNotApplicable
			default:
				// 🔴 EVERYTHING ELSE IS `not-reported`, including — especially — a node whose language and
				// form ARE covered and which carries no verdict. That is precisely the cell a derived
				// projection would fill with `applies`, and filling it is the fabrication this package
				// exists to prevent.
				cell.State = StateNotReported
			}
			row.Cells = append(row.Cells, cell)

			t := totals[axis]
			if cell.Stale {
				// Shown, and excluded from every total. A count that mixes two builds' answers is a count
				// nobody can act on, and the version is displayed beside it so the reader can see why.
				t.StaleExcluded++
				continue
			}
			switch cell.State {
			case StateApplies:
				t.Applies++
			case StateRefused:
				t.Refused++
			case StateNotApplicable:
				t.NotApplicable++
			case StateNotReported:
				t.NotReported++
			}
		}
		p.Nodes = append(p.Nodes, row)
	}

	p.Totals = make([]AxisTotals, 0, len(axes))
	for _, axis := range axes {
		p.Totals = append(p.Totals, *totals[axis])
	}
	sort.Slice(p.Nodes, func(i, j int) bool { return p.Nodes[i].NodeID < p.Nodes[j].NodeID })
	return p
}
