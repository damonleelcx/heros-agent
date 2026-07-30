package changedelivery

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// TestDeliveryTableIsTotalOverEveryAxis — task 23.1.
//
// 🔴 The property under test is TOTALITY, not correctness of any one cell. A cell that falls out of the
// table does not render as "unknown"; it renders as nothing, and nothing reads on every surface as "not
// applicable" — a claim about the customer's code that we never made.
func TestDeliveryTableIsTotalOverEveryAxis(t *testing.T) {
	cells := Table()
	seen := map[string]map[Route]bool{}
	for _, c := range cells {
		switch c.Status {
		case StatusDelivers, StatusRefuses, StatusVaries:
		default:
			t.Fatalf("cell %s/%s/%s carries status %q, outside the closed set", c.Axis, c.Change, c.Route, c.Status)
		}
		// 🔴 Only the SOURCE route may answer "varies": the runtime route's eligibility is a property of
		// the change (data or structure), which does not vary by frontend. A runtime cell that hedged
		// would be hiding a real answer behind a per-language excuse.
		if c.Status == StatusVaries && c.Route != RouteSource {
			t.Fatalf("cell %s/%s on route %s answers %q; only the source route varies by language", c.Axis, c.Change, c.Route, StatusVaries)
		}
		if c.Refused() && c.Cause == "" {
			t.Fatalf("cell %s/%s/%s refuses with no cause", c.Axis, c.Change, c.Route)
		}
		if c.Refused() && !c.Cause.Valid() {
			t.Fatalf("cell %s/%s/%s carries cause %q, outside the closed set", c.Axis, c.Change, c.Route, c.Cause)
		}
		key := c.Axis + "/" + string(c.Change)
		if seen[key] == nil {
			seen[key] = map[Route]bool{}
		}
		if seen[key][c.Route] {
			t.Fatalf("duplicate cell for %s on route %s", key, c.Route)
		}
		seen[key][c.Route] = true
	}

	// Every change kind must answer for BOTH routes.
	for _, kind := range ChangeKinds() {
		axis, _ := AxisFor(kind)
		key := axis + "/" + string(kind)
		for _, r := range Routes() {
			if !seen[key][r] {
				t.Fatalf("delivery table is not total: %s has no cell for route %s", key, r)
			}
		}
	}

	// And every axis the platform ships must appear. A new axis added without a delivery row is exactly
	// the silence this contract removes, so the list is spelled out rather than derived from the table
	// it is meant to police.
	for _, axis := range []string{AxisModel, AxisPrompt, AxisSkills, AxisTools, AxisContext, AxisMemory, AxisHarness, AxisWiring} {
		found := false
		for _, c := range cells {
			if c.Axis == axis {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("axis %q has no delivery cell at all", axis)
		}
	}
}

// TestBoundaryCellsCarryNoArtifact — tasks 23.4/23.19, the asymmetry that keeps a boundary from
// wearing a backlog item's clothes.
func TestBoundaryCellsCarryNoArtifact(t *testing.T) {
	for _, c := range Table() {
		if !c.Refused() {
			continue
		}
		if c.Cause.Permanent() && c.MissingArtifact != "" {
			t.Fatalf("cell %s/%s is a permanent boundary but names missing artifact %q — attaching one turns 'never' into 'not yet'",
				c.Axis, c.Change, c.MissingArtifact)
		}
		if c.Cause == CauseNoRolloutBinding && c.MissingArtifact == "" {
			t.Fatalf("cell %s/%s owes work but names no artifact — 'the platform' with no named thing is an apology, not a cause",
				c.Axis, c.Change)
		}
	}
}

// TestBoundaryAndBacklogRowsAreDistinguishable — task 23.19.
//
// The two must differ in a way a surface can BRANCH on, not merely in prose: owner, permanence, and the
// presence of an artifact.
func TestBoundaryAndBacklogRowsAreDistinguishable(t *testing.T) {
	boundary, _ := RuntimeEligibility(ChangeWiring, true)
	backlog, _ := RuntimeEligibility(ChangeToolSet, true)

	if boundary.Cause != CauseNotRuntimeResolvable || backlog.Cause != CauseNoRolloutBinding {
		t.Fatalf("fixture drifted: got %q and %q", boundary.Cause, backlog.Cause)
	}
	if boundary.Cause.Owner() == backlog.Cause.Owner() {
		t.Fatalf("boundary and backlog share owner %q; a reader cannot tell whose move it is", boundary.Cause.Owner())
	}
	if !boundary.Cause.Permanent() || backlog.Cause.Permanent() {
		t.Fatal("permanence does not distinguish the two causes")
	}
	if boundary.MissingArtifact != "" {
		t.Fatalf("boundary names artifact %q", boundary.MissingArtifact)
	}
	if backlog.MissingArtifact == "" {
		t.Fatal("backlog names no artifact")
	}
}

// TestEligibilityCauseOrderPrefersTheBoundary — task 23.4.
//
// 🔴 This is the requirement that saves a user a wasted afternoon. A wiring change on an inline node is
// BOTH not-runtime-resolvable and not-bound. Reporting `nodeNotBound` would send them to migrate the
// node to bound mode — work that cannot possibly help, because no document reorders statements.
func TestEligibilityCauseOrderPrefersTheBoundary(t *testing.T) {
	for _, kind := range []ChangeKind{ChangeWiring, ChangeModelAcrossProvider, ChangeSelectionPolicy, ChangeMemoryStrategy, ChangeHarnessStrategy, ChangeSkillBinding} {
		for _, bound := range []bool{true, false} {
			got, err := RuntimeEligibility(kind, bound)
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			if got.Cause != CauseNotRuntimeResolvable {
				t.Fatalf("%s (bound=%v): got cause %q, want the boundary announced first", kind, bound, got.Cause)
			}
			if strings.Contains(strings.ToLower(got.Note), "not yet") {
				t.Fatalf("%s: a permanent boundary's note says 'not yet': %q", kind, got.Note)
			}
		}
	}

	// And the ordering must actually distinguish: a DATA change on an inline node reports nodeNotBound,
	// while the same change on a bound node reports the schema gap. If the order were reversed, both
	// would collapse onto the same answer and the test above would still pass.
	inline, _ := RuntimeEligibility(ChangeToolSet, false)
	if inline.Cause != CauseNodeNotBound {
		t.Fatalf("data change on an inline node: got %q, want %q", inline.Cause, CauseNodeNotBound)
	}
	bound, _ := RuntimeEligibility(ChangeToolSet, true)
	if bound.Cause != CauseNoRolloutBinding {
		t.Fatalf("data change on a bound node: got %q, want %q", bound.Cause, CauseNoRolloutBinding)
	}
}

// TestBoundNodeFieldsAreRolloutEligible — task 23.13.
func TestBoundNodeFieldsAreRolloutEligible(t *testing.T) {
	live := []ChangeKind{ChangeModelWithinProvider, ChangeInferenceParams, ChangePromptVersion}
	for _, kind := range live {
		got, err := RuntimeEligibility(kind, true)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if !got.Eligible {
			t.Fatalf("%s on a bound node: not eligible (cause %q)", kind, got.Cause)
		}
		// The same change on an inline node reports the migration, and the source route is untouched.
		inline, _ := RuntimeEligibility(kind, false)
		if inline.Cause != CauseNodeNotBound {
			t.Fatalf("%s on an inline node: got %q, want %q", kind, inline.Cause, CauseNodeNotBound)
		}
	}

	// 🚫 And nothing else is live. This half is what keeps the table honest as axes are added: a new
	// eligible cell must be a deliberate edit here, not a side effect somewhere else.
	for _, kind := range ChangeKinds() {
		got, _ := RuntimeEligibility(kind, true)
		if !got.Eligible {
			continue
		}
		found := false
		for _, k := range live {
			if k == kind {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s became rollout-eligible without being declared in this test — the eligible set is a product claim and must move deliberately", kind)
		}
	}
}

// TestProviderCrossingRefusesBeforeApplyModeIsRead — task 23.14.
func TestProviderCrossingRefusesBeforeApplyModeIsRead(t *testing.T) {
	for _, bound := range []bool{true, false} {
		got, err := RuntimeEligibility(ChangeModelAcrossProvider, bound)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got.Cause != CauseNotRuntimeResolvable {
			t.Fatalf("bound=%v: got %q, want %q", bound, got.Cause, CauseNotRuntimeResolvable)
		}
		low := strings.ToLower(got.Note)
		if !strings.Contains(low, "sdk call") {
			t.Fatalf("the refusal does not name the SDK call rewrite: %q", got.Note)
		}
		if strings.Contains(low, "migrate") || strings.Contains(low, "bound mode would make") {
			t.Fatalf("the refusal suggests a bound migration, which cannot help: %q", got.Note)
		}
	}

	// The two model cells must be separately readable — a table that carries one "model" row is the
	// coarse table that misleads a reader into thinking they can canary one vendor against another.
	within, _ := RuntimeEligibility(ChangeModelWithinProvider, true)
	across, _ := RuntimeEligibility(ChangeModelAcrossProvider, true)
	if within.Axis != across.Axis {
		t.Fatal("fixture drifted: the two model cells should share an axis")
	}
	if within.Eligible == across.Eligible {
		t.Fatal("the within-provider and across-provider cells carry the same eligibility; the table is too coarse to be honest")
	}
}

// TestUnknownChangeKindFailsClosed — a kind the table has never heard of is an error, never a default.
func TestUnknownChangeKindFailsClosed(t *testing.T) {
	_, err := RuntimeEligibility(ChangeKind("teleportation"), true)
	if err == nil {
		t.Fatal("an unknown change kind was answered rather than refused")
	}
	if _, ok := err.(*ErrUnknownChangeKind); !ok {
		t.Fatalf("want *ErrUnknownChangeKind, got %T", err)
	}
}

// TestRolloutArmAndMaterializedChangeHashIdentically — task 23.5, the "no second apply path" rule.
//
// # Why this is a STRUCTURAL test rather than a behavioural one
//
// The requirement is that a rollout arm and a materialized change resolve and hash through the SAME
// spine. A behavioural test that fed one configuration into two code paths and compared the outputs
// would pass just as well if this package grew its own hash derivation that happened to agree today —
// and the day it drifted, two runs of "the same" configuration would stop being comparable with nothing
// reporting an error.
//
// So the assertion is that no second derivation EXISTS: this package never imports the hash machinery
// and never computes a config hash. Arms carry hashes handed to them by the one resolver.
func TestRolloutArmAndMaterializedChangeHashIdentically(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			for _, imp := range file.Imports {
				if strings.Contains(imp.Path.Value, "internal/confighash") {
					t.Fatalf("%s imports confighash — a delivery route that derives its own config hash is the second apply path the contract forbids", path)
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if id.Name == "confighash" {
					t.Fatalf("%s calls confighash.%s", path, sel.Sel.Name)
				}
				return true
			})
		}
	}
}

// TestSourceColumnDoesNotHedgeWhereItRefusesEverywhere — the ledger's source column must not answer
// "per language" for a change no language will ever materialize.
//
// Saying "it depends on your language" about a provider crossing sends a reader to check twelve
// coverage cells that all say the same thing, and implies one of them might one day say something else.
func TestSourceColumnDoesNotHedgeWhereItRefusesEverywhere(t *testing.T) {
	var found bool
	for _, c := range Table() {
		if c.Route != RouteSource || c.Change != ChangeModelAcrossProvider {
			continue
		}
		found = true
		if c.Status != StatusRefuses {
			t.Fatalf("the source column answers %q for a provider crossing; it refuses in every language", c.Status)
		}
		if c.MissingArtifact != "" {
			t.Fatalf("a permanent source refusal names artifact %q", c.MissingArtifact)
		}
	}
	if !found {
		t.Fatal("no source cell for model-across-provider")
	}

	// And it DOES still hedge where hedging is the true answer.
	for _, c := range Table() {
		if c.Route == RouteSource && c.Change == ChangeModelWithinProvider && c.Status != StatusVaries {
			t.Fatalf("the source column answers %q for a within-provider model change; that genuinely varies by language", c.Status)
		}
	}
}
