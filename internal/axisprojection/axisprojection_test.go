package axisprojection

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/transform"
)

const buildVersion = "cov-test"

func storedIR(nodes ...runlink.WireIRNode) linkingest.WorkflowIR {
	return linkingest.WorkflowIR{
		TenantID: "t-1", WorkflowID: "wf", SourceRevision: "rev", IRVersion: "v1",
		ReceivedAt: time.Now().UTC(), Nodes: nodes,
	}
}

func cellFor(p Projection, nodeID, axis string) (Cell, bool) {
	for _, n := range p.Nodes {
		if n.NodeID != nodeID {
			continue
		}
		for _, c := range n.Cells {
			if c.Axis == axis {
				return c, true
			}
		}
	}
	return Cell{}, false
}

func totalsFor(p Projection, axis string) AxisTotals {
	for _, t := range p.Totals {
		if t.Axis == axis {
			return t
		}
	}
	return AxisTotals{}
}

// coveredAxisFor returns an axis that this build's coverage table DOES carry a row for, in language.
// Picking it from the engine rather than hard-coding one keeps the tests below true when the table moves.
func coveredAxisFor(t *testing.T, language string) string {
	t.Helper()
	for _, c := range transform.AxisCoverage() {
		if c.Language == language {
			return c.Axis
		}
	}
	t.Fatalf("this build's coverage table carries no row at all for %s — every test below would be "+
		"testing the wrong thing", language)
	return ""
}

// 🔴 §5.2 — THE NO-DERIVED-VERDICT FENCE.
//
// A static check that no path in this package produces a verdict from platform-held node properties.
//
// The failure it prevents is not hypothetical and not exotic: the platform HAS the node's language, the
// coverage table is right there, and `if hasRow[axis+"|"+lang] { applies }` is a one-line "improvement"
// that would make the projection look complete. It would be right most of the time and would claim
// `applies` for exactly the call sites that refuse for their own shape — the input to a customer's
// decision about what to author, wrong in the direction that wastes their afternoon.
//
// Verified red by adding that line: the scan named `StateApplies` assigned in a branch that reads
// `hasRow`, and failed with the sentence below.
func TestTheProjectionDerivesNoVerdict(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}

	// The identifiers a verdict may be derived FROM — the platform-held node properties. A verdict
	// assignment appearing anywhere near one of these is the defect.
	platformHeld := map[string]bool{
		"hasRow": true, "Language": true, "Provider": true, "ModelID": true,
		"ContextPolicy": true, "ToolCount": true, "Symbol": true, "File": true,
	}

	files := 0
	verdictAssignments := 0
	for _, p := range pkgs {
		for name, f := range p.Files {
			files++
			ast.Inspect(f, func(n ast.Node) bool {
				assign, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				// Is this an assignment of a VERDICT state?
				isVerdict := false
				for _, rhs := range assign.Rhs {
					if id, ok := rhs.(*ast.Ident); ok && (id.Name == "StateApplies" || id.Name == "StateRefused") {
						isVerdict = true
					}
				}
				if !isVerdict {
					return true
				}
				verdictAssignments++
				// It must sit inside a branch whose condition reads the TRANSMITTED verdict, and never a
				// platform-held property. The enclosing switch's tag/case is checked by finding the
				// nearest guard textually — a deliberately blunt check, because the precise one is a data
				// -flow analysis and the blunt one is what a reviewer can also perform.
				pos := fset.Position(assign.Pos())
				src, rerr := os.ReadFile(name)
				if rerr != nil {
					return true
				}
				lines := strings.Split(string(src), "\n")
				// Look back up to eight lines for the guard.
				start := pos.Line - 9
				if start < 0 {
					start = 0
				}
				window := strings.Join(lines[start:pos.Line], "\n")
				if !strings.Contains(window, "told") && !strings.Contains(window, "v.Status") {
					t.Errorf("%s:%d assigns a verdict state in a branch that does not read the TRANSMITTED "+
						"verdict.\n  %s\n\n"+
						"  🔴 The platform is FORBIDDEN from deriving a verdict. It knows the node's "+
						"language and could compute the (axis, language) cell — which would be right most "+
						"of the time and would claim `applies` for exactly the call sites that refuse for "+
						"their own shape. A projection that is right most of the time is worse than none.",
						name, pos.Line, strings.TrimSpace(lines[pos.Line-1]))
					return true
				}
				for ident := range platformHeld {
					if strings.Contains(window, ident) {
						t.Errorf("%s:%d assigns a verdict state in a branch that reads the platform-held "+
							"property %q.\n  %s", name, pos.Line, ident, strings.TrimSpace(lines[pos.Line-1]))
					}
				}
				return true
			})
		}
	}
	if files == 0 || verdictAssignments == 0 {
		t.Fatalf("the scan read %d file(s) and found %d verdict assignment(s) — it is not reading this "+
			"package, so it would pass for the wrong reason", files, verdictAssignments)
	}
}

// 🔴 §5.4 — a node whose language and form ARE covered, carrying no verdict, renders `not-reported`.
// Never `applies`.
//
// This is the exact cell a derived projection would fill, and the reason the fence above exists.
func TestACoveredNodeWithNoVerdictRendersNotReported(t *testing.T) {
	axis := coveredAxisFor(t, "python")
	p := Build(storedIR(runlink.WireIRNode{
		NodeID: "n_1", Symbol: "handle", File: "a.py", Language: "python",
		// No AxisVerdicts at all — a link made without `--with-ir`'s verdicts, or by a CLI that predates
		// them.
	}), buildVersion)

	c, ok := cellFor(p, "n_1", axis)
	if !ok {
		t.Fatalf("no cell for (n_1, %s)", axis)
	}
	if c.State != StateNotReported {
		t.Errorf("a covered python node with NO transmitted verdict renders %q for %s.\n"+
			"  It must be `not-reported`. The platform was not told, and the coverage table cannot tell "+
			"it: a call site whose arguments are unpacked from a mapping refuses while its language and "+
			"form are fully covered.", c.State, axis)
	}
	if p.VerdictsReported != 0 {
		t.Errorf("VerdictsReported = %d for a structure with no verdicts", p.VerdictsReported)
	}
}

// 🔴 §5.5 — `not-applicable` is NEVER produced from an absent input.
//
// Verified red by deleting the `row.Language != ""` guard: a node with no reported language then fell
// into the `!hasRow` branch for every axis (because `axis+"|"` matches no row) and every cell read
// `not-applicable` — a confident claim about the customer's code, sourced entirely from our not having
// been told what language it is in.
func TestNotApplicableIsNeverProducedFromAnAbsentInput(t *testing.T) {
	cases := []struct {
		what string
		node runlink.WireIRNode
	}{
		{"a node with no reported language", runlink.WireIRNode{NodeID: "n_1", Symbol: "s", File: "a.txt"}},
		{"a node with no verdicts and no language", runlink.WireIRNode{NodeID: "n_1"}},
	}
	for _, tc := range cases {
		p := Build(storedIR(tc.node), buildVersion)
		for _, n := range p.Nodes {
			for _, c := range n.Cells {
				if c.State == StateNotApplicable {
					t.Errorf("%s produced `not-applicable` for axis %s.\n"+
						"  🔴 `not-applicable` is a CLAIM ABOUT THE CUSTOMER'S CODE — that this axis does "+
						"not exist for it — and it is the one thing this data must never say by accident. "+
						"Absence of an input is `not-reported`.", tc.what, c.Axis)
				}
			}
		}
	}

	// And the honest case still works: a language the table genuinely carries no row for.
	//
	// A language nobody has a frontend for is the only way to reach it, so this asserts that
	// `not-applicable` is reachable AT ALL — a state that can never be produced is a state whose absence
	// above proves nothing.
	p := Build(storedIR(runlink.WireIRNode{NodeID: "n_1", Language: "cobol"}), buildVersion)
	found := false
	for _, n := range p.Nodes {
		for _, c := range n.Cells {
			if c.State == StateNotApplicable {
				found = true
			}
		}
	}
	if !found {
		t.Error("`not-applicable` was not produced even for a language the coverage table has no row " +
			"for. A state that is unreachable makes the assertion above vacuous.")
	}
}

// §5.3 — a refusal carries its cause AND its owner, as data.
func TestARefusalCarriesItsCauseAndItsOwner(t *testing.T) {
	axis := coveredAxisFor(t, "python")
	p := Build(storedIR(runlink.WireIRNode{
		NodeID: "n_1", Language: "python",
		AxisVerdicts: []runlink.WireAxisVerdict{
			{Axis: axis, Status: runlink.VerdictRefused, Cause: string(transform.CauseCallSiteShape)},
		},
	}), buildVersion)

	c, _ := cellFor(p, "n_1", axis)
	if c.State != StateRefused {
		t.Fatalf("a transmitted refusal rendered %q", c.State)
	}
	if c.Cause != string(transform.CauseCallSiteShape) {
		t.Errorf("cause = %q", c.Cause)
	}
	if c.Owner != "you" {
		t.Errorf("owner = %q, want `you` — the owner is the one word that decides what a reader does "+
			"next, so it is carried beside the cause rather than inferred at each surface", c.Owner)
	}
	// And no sentence crossed.
	if strings.Contains(c.Cause, " ") {
		t.Errorf("the cause looks like prose, not an identifier: %q", c.Cause)
	}
}

// 🔴 §5.6 — a STALE projection is labelled, shows both versions, and its cells are EXCLUDED from every
// total.
//
// The stale path is easy to never see, because coverage rarely moves. So it is exercised by pinning a
// stored version this build does not have, and the arithmetic is asserted rather than the label.
func TestAStaleProjectionExcludesItsCellsFromEveryTotal(t *testing.T) {
	axis := coveredAxisFor(t, "python")
	ir := storedIR(runlink.WireIRNode{
		NodeID: "n_1", Language: "python",
		AxisVerdicts: []runlink.WireAxisVerdict{{Axis: axis, Status: runlink.VerdictApplies}},
	})
	ir.CoverageVersion = "cov-from-another-build"

	p := Build(ir, buildVersion)
	if !p.Stale {
		t.Fatal("a structure whose coverage version differs from this build's was not labelled stale")
	}
	if p.ReportedCoverageVersion != "cov-from-another-build" || p.CoverageVersion != buildVersion {
		t.Errorf("both versions must be shown: reported=%q build=%q",
			p.ReportedCoverageVersion, p.CoverageVersion)
	}

	tot := totalsFor(p, axis)
	if tot.Applies != 0 {
		t.Errorf("a STALE `applies` was counted (applies=%d). Two builds' answers mixed into one count "+
			"is a count nobody can act on.", tot.Applies)
	}
	if tot.StaleExcluded != 1 {
		t.Errorf("stale_excluded = %d, want 1 — the exclusion must be REPORTED, or the arithmetic does "+
			"not add up and the reader cannot see why", tot.StaleExcluded)
	}
	// 🔴 The arithmetic, asserted: the four states plus the exclusions equal the denominator, always.
	for _, tt := range p.Totals {
		sum := tt.Applies + tt.Refused + tt.NotApplicable + tt.NotReported + tt.StaleExcluded
		if sum != tt.Nodes {
			t.Errorf("axis %s: %d+%d+%d+%d+%d = %d, and the denominator is %d. A count that does not "+
				"reconcile with its denominator is a count a reader cannot check.",
				tt.Axis, tt.Applies, tt.Refused, tt.NotApplicable, tt.NotReported, tt.StaleExcluded,
				sum, tt.Nodes)
		}
	}
}

// An ABSENT reported version is NOT stale. It is not reported — a different fact with a different
// remedy, and conflating them would tell every pre-P29 client their data came from the wrong build.
func TestAnAbsentCoverageVersionIsNotStale(t *testing.T) {
	p := Build(storedIR(runlink.WireIRNode{NodeID: "n_1", Language: "python"}), buildVersion)
	if p.Stale {
		t.Error("a structure that reported NO coverage version was labelled stale. Absence and " +
			"disagreement are different facts: one is a client that predates the field, the other is two " +
			"builds' answers mixed together.")
	}
	if p.ReportedCoverageVersion != "" {
		t.Errorf("an unreported version was filled in as %q — the platform must never substitute its own",
			p.ReportedCoverageVersion)
	}
}

// §5.7 — every count carries its denominator.
func TestEveryTotalCarriesItsDenominator(t *testing.T) {
	p := Build(storedIR(
		runlink.WireIRNode{NodeID: "n_1", Language: "python"},
		runlink.WireIRNode{NodeID: "n_2", Language: "go"},
	), buildVersion)
	if p.NodeCount != 2 {
		t.Errorf("NodeCount = %d, want 2", p.NodeCount)
	}
	for _, tt := range p.Totals {
		if tt.Nodes != 2 {
			t.Errorf("axis %s reports counts over a denominator of %d, want 2. A proportion without the "+
				"count behind it is a number a reader cannot check — \"68%% covered\" over three nodes and "+
				"over four hundred are the same string and different facts.", tt.Axis, tt.Nodes)
		}
	}
}

// 🔴 §5.9 — BIDIRECTIONAL. The projection offers no cell the engine refuses to have an axis for, and
// the engine materialises no axis the projection has no column for.
func TestTheProjectionAndTheEngineAgreeOnTheAxisSet(t *testing.T) {
	p := Build(storedIR(runlink.WireIRNode{NodeID: "n_1", Language: "python"}), buildVersion)

	engineAxes := map[string]bool{}
	for _, a := range transform.CoverageAxes() {
		engineAxes[a] = true
	}
	projAxes := map[string]bool{}
	for _, a := range p.Axes {
		projAxes[a] = true
	}

	for a := range engineAxes {
		if !projAxes[a] {
			t.Errorf("the engine materialises axis %q and the projection has no column for it — a reader "+
				"would never learn it exists", a)
		}
	}
	for a := range projAxes {
		if !engineAxes[a] {
			t.Errorf("the projection offers a cell for axis %q, which the engine does not carry. Every "+
				"such cell is a square with no possible honest value.", a)
		}
	}
	// And every node row has exactly one cell per axis — no axis silently missing from a row.
	for _, n := range p.Nodes {
		if len(n.Cells) != len(p.Axes) {
			t.Errorf("node %s has %d cell(s) for %d axes. A missing cell renders as nothing, which reads "+
				"as `not applicable` — the one claim this data must never make by accident.",
				n.NodeID, len(n.Cells), len(p.Axes))
		}
	}
}

// The projection creates no table and caches nothing — asserted structurally, because "it is a read"
// is the kind of property that stops being true one convenience at a time.
func TestTheProjectionHoldsNoStateBetweenCalls(t *testing.T) {
	src, err := os.ReadFile("axisprojection.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for _, forbidden := range []string{"var cache", "sync.Map", "database/sql", "CREATE TABLE"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("axisprojection.go contains %q. It is a READ: computed at read time from the "+
				"coverage table and the stored structure. A materialised projection goes stale the moment "+
				"the table version moves and becomes a second source of truth for a refusal.", forbidden)
		}
	}
}
