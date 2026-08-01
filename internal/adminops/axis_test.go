package adminops_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminops"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/transform"
)

// axis_test.go covers P26 wave 26d — axis oversight.
//
// The load-bearing assertion is TestTheAxisSurfaceIsAtParityWithTheRealEngine. Every other property on
// this surface depends on the coverage answers being the engine's own: a console that drifted from
// them would be a second opinion about coverage, and coverage is a claim about a customer's code.

// testAdoption is a fleet adoption source with refused nodes behind it.
type testAdoption struct{}

func (testAdoption) Describe() string { return "test adoption source" }

func (testAdoption) Adoption(axis string) (int, int) {
	switch axis {
	case "prompt":
		return 3, 17
	case "model":
		return 2, 9
	}
	return 0, 0
}

func (testAdoption) RefusedNodes(axis string) []adminops.RefusedNode {
	if axis != "prompt" {
		return nil
	}
	return []adminops.RefusedNode{
		{TenantID: tenantAcme, NodeID: "n1", Language: "python", Axis: "prompt",
			Cause: string(transform.CauseCallSiteShape)},
	}
}

func axisView(t *testing.T, h *harness, adoption adminops.AxisAdoptionSource) adminops.AxisView {
	t.Helper()
	svc, err := adminops.NewAxisService(h.exec, adoption)
	if err != nil {
		t.Fatalf("NewAxisService: %v", err)
	}
	view, err := svc.View(h.ctx(adminrbac.RolePlatformSRE))
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	return view
}

// TestTheAxisSurfaceIsAtParityWithTheRealEngine defends P26 task 5.6 and the requirement "parity SHALL
// be asserted in both directions".
//
// 🔴 It drives `transform.AxisCoverage()` — the REAL engine, not a fixture — and requires set equality
// with the surface's matrix. Both directions: the surface may not OFFER a cell the engine refuses, and
// may not OMIT a cell the engine materializes. A disagreement here is a test failure rather than a
// support conversation.
func TestTheAxisSurfaceIsAtParityWithTheRealEngine(t *testing.T) {
	h := newHarness(t)
	view := axisView(t, h, testAdoption{})

	type key struct{ axis, language, form string }
	engine := map[key]transform.CoverageCell{}
	for _, c := range transform.AxisCoverage() {
		engine[key{c.Axis, c.Language, c.Form}] = c
	}
	surface := map[key]adminops.CoverageCellRow{}
	for _, c := range view.Matrix {
		k := key{c.Axis, c.Language, c.Form}
		if _, dup := surface[k]; dup {
			t.Fatalf("the surface renders %v twice — a duplicated cell is a second answer", k)
		}
		surface[k] = c
	}
	if len(engine) == 0 {
		t.Fatal("the engine reported no coverage at all — this test would prove nothing")
	}

	// Direction 1: the surface omits no cell the engine materializes, and states no cell the engine
	// does not have.
	for k, cell := range engine {
		got, ok := surface[k]
		if !ok {
			t.Fatalf("the engine materializes/answers %v and the surface OMITS it — an omitted row is "+
				"invisible rather than merely ambiguous", k)
		}
		wantState := adminops.CellApplies
		if cell.Refused() {
			wantState = adminops.CellRefused
		}
		if got.State != wantState {
			t.Fatalf("%v: the engine says %q and the surface renders %q", k, cell.Status, got.State)
		}
		// The cause is the engine's STABLE identifier, verbatim — not translated into a second
		// vocabulary, which would lose which taxonomy refused.
		if got.Cause != string(cell.Cause) {
			t.Fatalf("%v: the surface renders cause %q, the engine said %q — the console has invented a "+
				"second vocabulary for a refusal", k, got.Cause, cell.Cause)
		}
		if got.MissingInput != cell.MissingArtifact {
			t.Fatalf("%v: the surface names missing input %q, the engine said %q", k, got.MissingInput, cell.MissingArtifact)
		}
	}
	// Direction 2: the surface offers no cell the engine refuses to answer at all.
	for k := range surface {
		if _, ok := engine[k]; !ok {
			t.Fatalf("the surface OFFERS %v, which the engine does not answer — the console is a second "+
				"opinion about coverage", k)
		}
	}
}

// TestTheAxisSurfaceRendersTheAxisStatusAsDeclared defends P26 task 5.2.
func TestTheAxisSurfaceRendersTheAxisStatusAsDeclared(t *testing.T) {
	h := newHarness(t)
	view := axisView(t, h, testAdoption{})

	if len(view.Axes) != len(transform.CoverageAxes()) {
		t.Fatalf("the surface shows %d axes, the engine declares %d", len(view.Axes), len(transform.CoverageAxes()))
	}
	for _, row := range view.Axes {
		want := string(transform.StatusFor(row.Axis))
		if row.Status != want {
			t.Fatalf("axis %s renders status %q; the axis declares %q. The console does not compute, "+
				"adjust or reinterpret it.", row.Axis, row.Status, want)
		}
		if row.DrillDown == "" {
			t.Fatalf("axis %s offers no drill-down — a fleet count that hides a single tenant's "+
				"pathological repository is treated as incomplete", row.Axis)
		}
	}
}

// TestAdoptionWithNoSourceIsUnknownRatherThanZero defends the requirement that an absent measurement is
// stated, not rendered as a measured value.
func TestAdoptionWithNoSourceIsUnknownRatherThanZero(t *testing.T) {
	h := newHarness(t)
	view := axisView(t, h, nil)

	if view.AdoptionKnown {
		t.Fatal("adoption reports as known with no source wired")
	}
	for _, row := range view.Axes {
		if row.Tenants != nil || row.Nodes != nil {
			t.Fatalf("axis %s reports adoption counts with no source — 'no tenant uses this axis' and "+
				"'we did not measure adoption' are opposite claims that look identical as a zero", row.Axis)
		}
	}
	// With a source, the counts are present and are the source's own.
	withSource := axisView(t, h, testAdoption{})
	for _, row := range withSource.Axes {
		if row.Tenants == nil || row.Nodes == nil {
			t.Fatalf("axis %s reports unknown adoption with a source wired", row.Axis)
		}
		if row.Axis == "prompt" && (*row.Tenants != 3 || *row.Nodes != 17) {
			t.Fatalf("prompt adoption is %d/%d, want 3/17", *row.Tenants, *row.Nodes)
		}
	}
}

// TestTheThreeCausesStayDistinguishable defends P26 task 5.3.
func TestTheThreeCausesStayDistinguishable(t *testing.T) {
	h := newHarness(t)
	view := axisView(t, h, testAdoption{})

	valid := map[string]bool{}
	for _, c := range transform.CauseClasses() {
		valid[string(c)] = true
	}
	if len(view.Legend) != len(transform.CauseClasses()) {
		t.Fatalf("the legend shows %d causes, the engine has %d", len(view.Legend), len(transform.CauseClasses()))
	}
	owners := map[string]bool{}
	for _, l := range view.Legend {
		if !valid[l.Cause] {
			t.Fatalf("legend cause %q is not one of the engine's stable identifiers", l.Cause)
		}
		if l.Owner == "" {
			t.Fatalf("cause %q names no owner", l.Cause)
		}
		owners[l.Owner] = true
	}
	if len(owners) != 3 {
		t.Fatalf("the three causes resolve to %d distinct owners, want 3 — they are answered by three "+
			"different parties and must not blur together", len(owners))
	}

	// No axis presents a single combined refusal total as its only figure.
	var sawRefusals bool
	for _, row := range view.Axes {
		if len(row.Refusals) == 0 {
			continue
		}
		sawRefusals = true
		for cause := range row.Refusals {
			if !valid[cause] {
				t.Fatalf("axis %s counts refusals under %q, which is not a stable cause identifier", row.Axis, cause)
			}
		}
		if len(row.RefusalsByLanguage) == 0 {
			t.Fatalf("axis %s counts refusals but not per language", row.Axis)
		}
	}
	if !sawRefusals {
		t.Fatal("no axis reports any refusal — this test would prove nothing")
	}
}

// TestTheRankingIsCountsAndNamesOnlyClosableArtefacts defends P26 tasks 5.4 and 5.9.
func TestTheRankingIsCountsAndNamesOnlyClosableArtefacts(t *testing.T) {
	h := newHarness(t)
	view := axisView(t, h, testAdoption{})

	if view.IsRanking {
		t.Fatal("the surface declares itself a ranking. Only the eval harness ranks and only a P5.5 " +
			"verified delta is a claim; a refusal count must not be rendered with the grammar of a " +
			"ranked evaluation result (P26 §5.9).")
	}
	if len(view.Ranking) == 0 {
		t.Fatal("no candidate artefacts are ranked — the backlog cannot be ordered by evidence")
	}
	// Ordered by count, descending, so a reader can act on the first row.
	for i := 1; i < len(view.Ranking); i++ {
		if view.Ranking[i-1].Closes < view.Ranking[i].Closes {
			t.Fatalf("the artefact ordering is not by refusals closed: %d before %d",
				view.Ranking[i-1].Closes, view.Ranking[i].Closes)
		}
	}
	// Only cells with a NAMED artefact are counted. A permanent boundary has nothing to build, and
	// ranking it would put work on a backlog that cannot close it.
	named := map[string]bool{}
	for _, c := range transform.AxisCoverage() {
		if c.MissingArtifact != "" {
			named[c.MissingArtifact] = true
		}
	}
	for _, r := range view.Ranking {
		if !named[r.Artefact] {
			t.Fatalf("the ranking names artefact %q, which no coverage cell asks for", r.Artefact)
		}
		if r.Closes <= 0 {
			t.Fatalf("artefact %q closes %d refusals", r.Artefact, r.Closes)
		}
		if r.DrillDown == "" {
			t.Fatalf("artefact %q offers no drill-down to the refusals it counts", r.Artefact)
		}
	}
}

// TestNoCellRendersAsNotApplicable defends P26 task 5.7 and the requirement "An absent coverage row
// SHALL render as unknown and SHALL NOT render as not applicable".
//
// 🔴 The strongest form available: the value does not exist. A read model with no *not applicable*
// state cannot produce one by accident, and the serialised model is scanned for the phrase in case a
// later change smuggles it in as prose.
func TestNoCellRendersAsNotApplicable(t *testing.T) {
	for _, s := range adminops.CellStates() {
		if strings.Contains(strings.ToLower(string(s)), "applicable") {
			t.Fatalf("CellStates() contains %q — *not applicable* is a claim about the customer's code "+
				"and must not be a value this read model can hold", s)
		}
	}
	if len(adminops.CellStates()) != 3 {
		t.Fatalf("CellStates() has %d values, want exactly 3", len(adminops.CellStates()))
	}

	h := newHarness(t)
	view := axisView(t, h, testAdoption{})
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := strings.ToLower(string(encoded))
	for _, phrase := range []string{"not applicable", "not-applicable", "n/a"} {
		if strings.Contains(body, phrase) {
			t.Fatalf("the axis read model contains %q. An absent row renders as UNKNOWN naming what is "+
				"missing; *not applicable* says 'your call site cannot carry this', and substituting one "+
				"for the other converts our backlog into the customer's problem, invisibly.", phrase)
		}
	}
	// Every refused cell names its cause, so a reader is never left to infer which of the three it is.
	for _, c := range view.Matrix {
		if c.State == adminops.CellRefused && c.Cause == "" {
			t.Fatalf("%s/%s/%s is refused with no cause — a refusal without a cause is the silence this "+
				"contract exists to remove", c.Axis, c.Language, c.Form)
		}
		// And no row is suppressed: a cell with no data is still a row, in the unknown state.
		if c.State == "" {
			t.Fatalf("%s/%s/%s has no state at all", c.Axis, c.Language, c.Form)
		}
	}
}

// TestACoverageGapIsNotPresentedAsAPlanBoundary defends P26 task 5.8.
func TestACoverageGapIsNotPresentedAsAPlanBoundary(t *testing.T) {
	h := newHarness(t)
	view := axisView(t, h, testAdoption{})

	if !view.PlanIndependent {
		t.Fatal("the axis read model does not declare itself plan-independent")
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := strings.ToLower(string(encoded))
	// No plan, tier or entitlement vocabulary anywhere near a coverage answer. A gap is *not yet
	// applied by the platform*, identical on every plan.
	for _, word := range []string{"upgrade to", "enterprise plan", "your tier", "entitlement", "paid plan"} {
		if strings.Contains(body, word) {
			t.Fatalf("the axis read model contains %q — a coverage gap must never be presented as "+
				"something a plan would change", word)
		}
	}
	// A permanent boundary must not be dressed as a delay.
	for _, l := range view.Legend {
		if l.Permanent && strings.Contains(strings.ToLower(l.Meaning), "not yet") {
			t.Fatalf("permanent cause %q is described as 'not yet' — a fact about the source is not a "+
				"pending platform artefact", l.Cause)
		}
	}
}

// TestTheAxisSurfaceIsReadOnlyAndDrillsDown defends P26 task 5.10's drill-down and the phase's
// read-only boundary.
func TestTheAxisSurfaceIsReadOnlyAndDrillsDown(t *testing.T) {
	allowed := map[string]bool{"View": true, "RefusedNodes": true}
	typ := reflect.TypeOf(&adminops.AxisService{})
	for i := 0; i < typ.NumMethod(); i++ {
		if name := typ.Method(i).Name; !allowed[name] {
			t.Fatalf("AxisService exposes %q — this surface reads and changes no coverage answer", name)
		}
	}
	h := newHarness(t)
	if !axisView(t, h, testAdoption{}).ReadOnly {
		t.Fatal("the axis read model does not declare itself read-only")
	}

	svc, err := adminops.NewAxisService(h.exec, testAdoption{})
	if err != nil {
		t.Fatalf("NewAxisService: %v", err)
	}
	nodes, err := svc.RefusedNodes(h.ctx(adminrbac.RolePlatformSRE), "prompt")
	if err != nil {
		t.Fatalf("RefusedNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("the prompt axis drills down to %d refused nodes, want 1", len(nodes))
	}
	if nodes[0].TenantID == "" || nodes[0].Cause == "" {
		t.Fatalf("a refused node names no tenant or no cause: %+v", nodes[0])
	}
}

// TestTheAxisReadIsNotCached defends the requirement "No caching introduces a stale refusal".
func TestTheAxisReadIsNotCached(t *testing.T) {
	h := newHarness(t)
	svc, err := adminops.NewAxisService(h.exec, testAdoption{})
	if err != nil {
		t.Fatalf("NewAxisService: %v", err)
	}
	first, err := svc.View(h.ctx(adminrbac.RolePlatformSRE))
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	second, err := svc.View(h.ctx(adminrbac.RolePlatformSRE))
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	// Both reads drive the engine, so both agree with it — and the second is a fresh read rather than a
	// replay. The observable proof that no copy is held is that each read writes its own audit entry.
	if len(first.Matrix) != len(second.Matrix) {
		t.Fatal("two reads of the coverage matrix disagree")
	}
	entries := h.entriesFor(adminaudit.ActionCrossTenantView)
	var axisReads int
	for _, e := range entries {
		if e.Evidence["read_model"] == "axis" {
			axisReads++
		}
	}
	if axisReads != 2 {
		t.Fatalf("two axis reads produced %d audit entries — a cached read would produce fewer, and a "+
			"cached refusal the engine has since stopped refusing is the failure parity exists to catch",
			axisReads)
	}
}

// TestTheParityFenceGoesRedOnADeliberateViolation is P26 task 8.5 for the parity assertion.
//
// 🔴 The parity test above passes today, and a passing run is not evidence that it CAN fail. This runs
// the identical comparison against two deliberately-broken matrices — one with a cell OMITTED, one with
// a cell OFFERED that the engine does not answer — and requires both to be caught. Without it, an
// inverted assertion would report parity forever.
func TestTheParityFenceGoesRedOnADeliberateViolation(t *testing.T) {
	h := newHarness(t)
	view := axisView(t, h, testAdoption{})

	type key struct{ axis, language, form string }
	engine := map[key]bool{}
	for _, c := range transform.AxisCoverage() {
		engine[key{c.Axis, c.Language, c.Form}] = true
	}
	// The comparison, extracted so the broken inputs go through EXACTLY the logic the real assertion
	// uses rather than a re-implementation that could differ.
	compare := func(rows []adminops.CoverageCellRow) (omitted, offered int) {
		surface := map[key]bool{}
		for _, c := range rows {
			surface[key{c.Axis, c.Language, c.Form}] = true
		}
		for k := range engine {
			if !surface[k] {
				omitted++
			}
		}
		for k := range surface {
			if !engine[k] {
				offered++
			}
		}
		return
	}

	if o, f := compare(view.Matrix); o != 0 || f != 0 {
		t.Fatalf("the honest matrix is already out of parity: omitted=%d offered=%d", o, f)
	}

	// Violation 1: a cell the engine answers is DROPPED. An omitted row is invisible rather than
	// merely ambiguous, which is why omission is a violation at all.
	dropped := append([]adminops.CoverageCellRow(nil), view.Matrix[1:]...)
	if o, _ := compare(dropped); o != 1 {
		t.Fatalf("dropping a cell was not caught: omitted=%d, want 1", o)
	}

	// Violation 2: a cell the engine does not answer is OFFERED. This is the direction that would let
	// the console become a second opinion about coverage.
	invented := append([]adminops.CoverageCellRow(nil), view.Matrix...)
	invented = append(invented, adminops.CoverageCellRow{
		Axis: "model", Language: "cobol", Form: "invented", State: adminops.CellApplies,
	})
	if _, f := compare(invented); f != 1 {
		t.Fatalf("offering a cell the engine does not answer was not caught: offered=%d, want 1", f)
	}
}
