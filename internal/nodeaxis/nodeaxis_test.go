package nodeaxis

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

const fixtureRevision = "0000000000000000000000000000000000000000"

func computeFixture(t *testing.T, repo string) (*discovery.IR, Report) {
	t.Helper()
	res, err := discovery.Run(discovery.Options{Repo: repo, WorkflowID: "w", CommitSHA: fixtureRevision})
	if err != nil {
		t.Fatalf("discover %s: %v", repo, err)
	}
	if len(res.IR.Nodes) == 0 {
		t.Fatalf("%s produced no nodes — every assertion below would pass vacuously", repo)
	}
	return &res.IR, Compute(&res.IR, repo)
}

// bySymbol keys a report's node entries by the enclosing symbol, so a test names a call site by what it
// is rather than by an opaque hash.
func bySymbol(ir *discovery.IR, rep Report) map[string]NodeReport {
	sym := map[string]string{}
	for _, n := range ir.Nodes {
		sym[n.NodeID] = n.CallSite.Symbol
	}
	out := map[string]NodeReport{}
	for _, nr := range rep.Nodes {
		name := sym[nr.NodeID]
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		out[name] = nr
	}
	return out
}

func verdictFor(nr NodeReport, axis string) (Verdict, bool) {
	for _, v := range nr.Verdicts {
		if v.Axis == axis {
			return v, true
		}
	}
	return Verdict{}, false
}

// 🔴 THE FENCE (P29 §2.3). Two call sites, identical in every property the coverage table records —
// language `python`, provider `openai`, the same SDK method, therefore the same registry row and the same
// FORM — and the engine answers them differently. `plain` writes its arguments at the call site;
// `unpacked` assembles them elsewhere and splats them.
//
// A verdict computed by looking up (axis, language, form) cannot tell these two apart. It would report
// `applies` for both, and the half of that answer which is wrong is wrong in the direction that costs the
// reader an afternoon: they would author a model change against a call site that has no `model=` to
// rewrite and never will.
//
// This test is the reason the whole computation runs on the customer's machine.
func TestACoveredLanguageAndFormStillRefusesForTheCallSitesOwnShape(t *testing.T) {
	ir, rep := computeFixture(t, "testdata/pyrepo")
	nodes := bySymbol(ir, rep)

	plain, ok := nodes["plain"]
	if !ok {
		t.Fatalf("the fixture's `plain` call site was not reported; got %v", keysOfReport(nodes))
	}
	unpacked, ok := nodes["unpacked"]
	if !ok {
		t.Fatalf("the fixture's `unpacked` call site was not reported; got %v", keysOfReport(nodes))
	}

	// Both are the same language, and it came from a frontend.
	for name, nr := range map[string]NodeReport{"plain": plain, "unpacked": unpacked} {
		if nr.Language != "python" {
			t.Fatalf("%s reports language %q, want python — the two call sites must be identical in every "+
				"property the coverage table records, or this test proves nothing", name, nr.Language)
		}
	}

	pv, ok := verdictFor(plain, "model")
	if !ok || pv.Status != "applies" {
		t.Errorf("`plain` writes `model=` at the call site and the engine rewrites it; the verdict is %+v "+
			"(reported: %v). Without this half, the test below could pass with an engine that refuses "+
			"everything.", pv, ok)
	}

	uv, ok := verdictFor(unpacked, "model")
	if !ok {
		t.Fatalf("`unpacked` carries NO model verdict. It must carry a refusal: the engine looked at this " +
			"call site and declined, which is an answer, and dropping it would render `not-reported` for a " +
			"node we do know about.")
	}
	if uv.Status != "refused" {
		t.Errorf("`unpacked` transmits %q for the model axis. Its arguments are unpacked from a mapping — "+
			"there is no `model=` to rewrite and no materializer will create one — so this MUST be a "+
			"refusal. A coverage lookup on (model, python, openai-chat) would have said `applies` here, "+
			"and that is the fabrication this computation exists to prevent.", uv.Status)
	}
	if uv.Cause != string(transform.CauseCallSiteShape) {
		t.Errorf("`unpacked` refuses with cause %q, want %q. The class matters: %q tells the reader their "+
			"own call site cannot carry it, where %q would tell them to wait for a materializer that "+
			"would refuse this site too.",
			uv.Cause, transform.CauseCallSiteShape,
			transform.CauseCallSiteShape, transform.CauseNoMaterializer)
	}
}

// Every transmitted verdict is drawn from the closed sets, and a refusal always names a valid class.
// This is the property `internal/runlink`'s allowlist fence asserts about the TYPES; here it is asserted
// about the VALUES this package actually produces against a real tree.
func TestEveryProducedVerdictIsAClosedSetMember(t *testing.T) {
	_, rep := computeFixture(t, "testdata/pyrepo")
	axes := map[string]bool{}
	for _, a := range ProbedAxes() {
		axes[a] = true
	}
	seen := 0
	for _, nr := range rep.Nodes {
		for _, v := range nr.Verdicts {
			seen++
			if !axes[v.Axis] {
				t.Errorf("verdict for node %s names axis %q, which is not a probed axis", nr.NodeID, v.Axis)
			}
			switch v.Status {
			case "applies":
				if v.Cause != "" {
					t.Errorf("an `applies` verdict carries a cause (%q) — a cause is a refusal's field", v.Cause)
				}
			case "refused":
				if !transform.CauseClass(v.Cause).Valid() {
					t.Errorf("node %s / %s refuses with cause %q, which is not one of the three classes",
						nr.NodeID, v.Axis, v.Cause)
				}
			default:
				t.Errorf("node %s / %s carries status %q; the set is {applies, refused} and a verdict this "+
					"package could not compute must be ABSENT, not a third value", nr.NodeID, v.Axis, v.Status)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no verdicts were produced at all — every assertion above passed vacuously")
	}
}

// 🔴 §2.4 — the transmitted verdicts agree with what `heros coverage` reports locally for the same
// repository. A divergence is a defect, not a per-surface behaviour.
//
// What "agree" means precisely, because the two are at different grains: coverage answers
// (axis, language, form) and a verdict answers (axis, node). The checkable relation is CONTAINMENT in
// both directions —
//
//	an `applies` verdict requires at least one MATERIALIZING coverage cell for (axis, language). If the
//	table says the engine cannot apply this axis in this language at all, and a node says it did, one of
//	the two is lying and the projection would be built on the liar;
//
//	a `refused` verdict's CAUSE must appear among that (axis, language)'s refusal causes. A node
//	refusing with a class the table never emits for its language means the engine and the table have
//	drifted — which is the exact failure `heros coverage`'s version identifier exists to make
//	diagnosable, and it should not need a support cycle to notice.
func TestTransmittedVerdictsAgreeWithTheLocalCoverageTable(t *testing.T) {
	_, rep := computeFixture(t, "testdata/pyrepo")
	if rep.CoverageVersion != transform.CoverageTableVersion() {
		t.Fatalf("the report names coverage %q and this build's table is %q — the version travels with "+
			"the verdicts precisely so a console can tell them apart, so they must not disagree HERE",
			rep.CoverageVersion, transform.CoverageTableVersion())
	}

	cells := transform.AxisCoverage()
	materializes := map[string]bool{}      // axis|language -> some form materializes
	causes := map[string]map[string]bool{} // axis|language -> the causes the table emits
	for _, c := range cells {
		k := c.Axis + "|" + c.Language
		if c.Status == transform.CoverageMaterializes {
			materializes[k] = true
			continue
		}
		if causes[k] == nil {
			causes[k] = map[string]bool{}
		}
		causes[k][string(c.Cause)] = true
	}

	checked := 0
	for _, nr := range rep.Nodes {
		for _, v := range nr.Verdicts {
			checked++
			k := v.Axis + "|" + nr.Language
			switch v.Status {
			case "applies":
				if !materializes[k] {
					t.Errorf("node %s transmits `applies` for %s in %s, and `heros coverage` reports NO "+
						"materializing cell for that pair. The console would show a node applying an axis "+
						"the same build says it cannot apply.", nr.NodeID, v.Axis, nr.Language)
				}
			case "refused":
				if v.Cause == string(transform.CauseCallSiteShape) {
					// 🔴 EXEMPT, and the exemption is the point of the whole computation rather than a
					// hole in this check. `call-site-cannot-carry-it` is BY DEFINITION invisible to the
					// coverage table: the table answers (axis, language, form), and this class says "this
					// particular site's own source cannot express it". Requiring the table to have
					// predicted it would require the table to have read the customer's code.
					//
					// The fixture exercises it: the table reports `memory / python / scratchpad` as
					// APPLIES, and both call sites refuse — one because its arguments are unpacked, one
					// because its result is not assigned to a name. Neither is a drift; both are the fact
					// that made §2.3 necessary.
					continue
				}
				// The other two classes ARE the table's own facts. `no-materializer-for-this-language`
				// and `not-expressible-at-a-call-site` are properties of (axis, language), so an engine
				// that emits one where the table does not is a genuine drift between the rewriter and the
				// table that claims to describe it.
				if !causes[k][v.Cause] {
					t.Errorf("node %s refuses %s in %s with cause %q, which `heros coverage` never emits "+
						"for that pair (it emits %v). That class is a property of the LANGUAGE, not of "+
						"this call site, so the engine and the table have drifted — which is exactly the "+
						"disagreement the coverage version identifier exists to make diagnosable.",
						nr.NodeID, v.Axis, nr.Language, v.Cause, sortedKeys(causes[k]))
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no verdicts were compared — this test passed vacuously")
	}
}

// A node the frontends do not claim gets no language and no verdicts, and the language is never inferred
// from the file's extension. Verified red by making languagesFor fall back to the extension: the
// `.py`-named file in a directory no frontend walks then reported `python`, and every axis got a verdict
// computed against an engine that had never seen the call site.
func TestAnUnclaimedNodeGetsNoLanguageAndNoVerdicts(t *testing.T) {
	// The fixture's own report is not read here — this test is about the WIDENED one below — so it is
	// discarded rather than assigned and overwritten, which reads as a value somebody forgot to check.
	ir, _ := computeFixture(t, "testdata/pyrepo")
	// Add a node the tree does not contain. It is `.py` by path, so an extension-derived language would
	// happily label it.
	ghost := discovery.IRNode{NodeID: "n_ghost", CallSite: discovery.IRCallSite{File: "app/svc.py", Symbol: "gone"}}
	widened := *ir
	widened.Nodes = append(append([]discovery.IRNode{}, ir.Nodes...), ghost)
	rep := Compute(&widened, "testdata/pyrepo")

	for _, nr := range rep.Nodes {
		if nr.NodeID != "n_ghost" {
			continue
		}
		if nr.Language != "" {
			t.Errorf("a node no frontend claimed reports language %q. It must be absent: a guessed "+
				"language makes a guessed verdict look computed.", nr.Language)
		}
		if len(nr.Verdicts) != 0 {
			t.Errorf("a node no frontend claimed carries %d verdict(s): %v", len(nr.Verdicts), nr.Verdicts)
		}
		return
	}
	t.Fatal("the ghost node was not in the report at all — it must be present and empty, so the console " +
		"can render `not-reported` rather than omitting the row")
}

// The probed axes are the engine's own node dimensions, minus wiring. A dimension added to variantspec
// and not probed here would silently stop being reported, and the projection would show `not-reported`
// forever with nothing failing.
func TestProbedAxesCoverEveryNodeDimension(t *testing.T) {
	probed := map[string]bool{}
	for _, a := range ProbedAxes() {
		probed[a] = true
	}
	for _, d := range dimensionNames {
		if !probed[d] {
			t.Errorf("dimension %q is not probed. Either add a probe in probeFor, or state here why the "+
				"axis has no per-node answer — as wiring does, whose scope is a set of edges.", d)
		}
	}
	for _, a := range transform.CoverageAxes() {
		if a == "wiring" {
			if probed[a] {
				t.Error("wiring is probed per node. Its scope is a set of EDGES: `checkWiring` decides " +
					"against the graph before any node is visited, so a per-node wiring verdict invents a " +
					"grain the engine cannot answer at.")
			}
			continue
		}
		if !probed[a] {
			t.Errorf("coverage reports axis %q and nothing probes it", a)
		}
	}
}

// 🔴 THE ANTI-FABRICATION FENCE, and it is here because it caught a real one.
//
// A probe has to be a change the engine would genuinely accept somewhere, or the refusal it produces is a
// fact about the PROBE and not about the customer's call site — the same fabrication D2 forbids the
// platform, committed one layer down where nobody would look for it.
//
// The first skills probe declared `{"type":"object"}` with no `properties`, and the engine answered:
//
//	skills: skill "probe": the sealed input schema declares no `properties`, so there is no argument
//	shape to construct a tool value from (schema keys: [type])
//
// `refused / call-site-cannot-carry-it`, at a call site that accepts a real skill perfectly well. It
// would have shipped as "your code cannot carry skills" on a paid surface. The bug was invisible to every
// other test in this file — the verdict was well-formed, closed-set, coverage-consistent, and wrong.
//
// So: no refusal this package acts on may NAME the probe. If the engine's own sentence mentions the
// probe's sentinel, the engine is talking about our input.
func TestNoRefusalIsAboutTheProbeItself(t *testing.T) {
	res, err := discovery.Run(discovery.Options{Repo: "testdata/pyrepo", WorkflowID: "w", CommitSHA: fixtureRevision})
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, n := range res.IR.Nodes {
		for _, axis := range ProbedAxes() {
			ov, ok := probeFor(axis, n)
			if !ok {
				continue
			}
			_, gerr := transform.Generate(&variantspec.Resolved{
				ConfigHash: strings.Repeat("0", 64), SourceRevision: "probe", Language: "python",
				Overrides: map[string]variantspec.ResolvedOverride{n.NodeID: ov},
			}, "testdata/pyrepo")
			if gerr == nil {
				continue
			}
			var re *transform.RewriteError
			if !errors.As(gerr, &re) {
				continue
			}
			checked++
			if strings.Contains(re.Detail, `"probe"`) || strings.Contains(re.Detail, "probe-model") {
				t.Errorf("the %s probe made the engine refuse node %s by naming the PROBE:\n  %s\n"+
					"  That refusal is a fact about our input, not about the customer's call site, and "+
					"transmitting it would put this package's limitation into a sentence about their code.",
					axis, n.CallSite.Symbol, re.Detail)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no refusal was inspected — this fence passed vacuously")
	}
}

func keysOfReport(m map[string]NodeReport) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
