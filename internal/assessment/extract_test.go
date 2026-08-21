package assessment

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// extract_test.go drives the structural pass over REAL discovery output from REAL fixture
// repositories (task 7.11).
//
// # 🔴 Why not a hand-built IR
//
// `test-fixture-real-schema` forbids an inline simplified fixture, and this is the sharpest case for
// the rule in the phase. The graph extractor's entire job is to distinguish "no edges because the code
// has none" from "no edges because this frontend emits none" — and an IR literal written in a test
// carries whatever `FrontendRun` the author typed. The test would then prove that the extractor agrees
// with the author, on a tree no customer has, about a frontend nobody ran.
//
// So discovery runs. `testdata/fixtures/python` is a real Python repository and the Python frontend is
// genuinely syntactic, which is what makes the `openclaw`-shaped case reachable at all.

// subjectFor runs discovery over a checked-in fixture and returns the assessment's Subject.
func subjectFor(t *testing.T, fixture string) Subject {
	t.Helper()
	dir := filepath.Join("..", "discovery", "testdata", "fixtures", fixture)
	res, err := discovery.Run(discovery.Options{
		Repo:       dir,
		RepoURL:    "local://" + fixture,
		CommitSHA:  "0000000",
		WorkflowID: "wf-" + fixture,
	})
	if err != nil {
		t.Fatalf("discovery over %s: %v", fixture, err)
	}
	ir := res.IR
	return Subject{WorkflowID: "wf-" + fixture, IR: &ir, Report: res.Report}
}

// topologyFor runs the graph extractor BEHIND the P34 gate.
//
// 🔴 Tasks 2.2 and 7.2 are about the topology analysis, and the shipped report refuses that axis until
// P34 lands (task 9.2). Testing through the gate would assert the gate; testing the inner function is
// what keeps the analysis exercised every day it is switched off — otherwise the day the gate lifts is
// the first day anybody runs it.
func topologyFor(t *testing.T, s Subject) Finding {
	t.Helper()
	f, err := extractGraphTopology(s)
	if err != nil {
		t.Fatalf("extractGraphTopology: %v", err)
	}
	return f
}

// subjectForLocal runs discovery over a fixture this package owns.
//
// 🔴 Two fixture roots, deliberately. `subjectFor` reads `internal/discovery`'s fixtures — real trees in
// languages that HAVE frontends, which is what every other test needs. This one reads fixtures for the
// cases discovery has no reason to keep: a repository in a language nothing in this build handles.
// Adding that to discovery's set would change what `discovery_ci.py` expects from a directory it owns.
func subjectForLocal(t *testing.T, fixture string) Subject {
	t.Helper()
	dir := filepath.Join("testdata", fixture)
	res, err := discovery.Run(discovery.Options{
		Repo:       dir,
		RepoURL:    "local://" + fixture,
		CommitSHA:  "0000000",
		WorkflowID: "wf-" + fixture,
	})
	if err != nil {
		t.Fatalf("discovery over %s: %v", fixture, err)
	}
	ir := res.IR
	return Subject{WorkflowID: "wf-" + fixture, IR: &ir, Report: res.Report}
}

// TestALanguageThisBuildCannotReadIsRefusedRatherThanNotMeasured is D1's distinction where it costs the
// most.
//
// A repository written in a language no frontend handles is not one we FAILED to read — it is one we
// CANNOT read, and only we can change that. Reporting `not_measured — no call sites discovered` would
// send the customer to look at their own code, which is fine, for an afternoon.
func TestALanguageThisBuildCannotReadIsRefusedRatherThanNotMeasured(t *testing.T) {
	s := subjectForLocal(t, "unsupported-language")
	if len(s.IR.Nodes) != 0 {
		t.Skipf("this build now reads %d node(s) from the fixture's language; the case needs one it cannot",
			len(s.IR.Nodes))
	}
	for _, e := range Extractors() {
		f, err := e.Extract(s)
		if err != nil {
			t.Fatalf("%s: %v", e.Axis(), err)
		}
		if f.State() != StateRefused {
			t.Fatalf("%s is %s for a repository in a language we have no frontend for, want refused.\n"+
				"  claim: %s", e.Axis(), f.State(), f.Claim())
		}
		if e.Axis().P34Pending() {
			continue // refused for P34's reason, which is checked elsewhere
		}
		if f.RefusalCause() != RefusalLanguage {
			t.Fatalf("%s names %q, want %q — the missing thing is a frontend for the language, and the "+
				"customer cannot supply one", e.Axis(), f.RefusalCause(), RefusalLanguage)
		}
		if !strings.Contains(f.Claim(), "ruby") {
			t.Fatalf("%s does not name the language it cannot read: %q", e.Axis(), f.Claim())
		}
	}
}

func findingFor(t *testing.T, s Subject, axis Axis) Finding {
	t.Helper()
	for _, e := range Extractors() {
		if e.Axis() != axis {
			continue
		}
		f, err := e.Extract(s)
		if err != nil {
			t.Fatalf("extracting %s: %v", axis, err)
		}
		return f
	}
	t.Fatalf("no extractor for %s", axis)
	return Finding{}
}

// TestThereIsOneExtractorPerAxis is the set-equality fence. An axis added to `Axes()` without an
// extractor would produce a report that is one finding short of nine, and the assembler would have to
// invent something for it — which is where a default comes from.
func TestThereIsOneExtractorPerAxis(t *testing.T) {
	got := map[Axis]int{}
	for _, e := range Extractors() {
		got[e.Axis()]++
	}
	for _, axis := range Axes() {
		if got[axis] != 1 {
			t.Fatalf("%s has %d extractors, want exactly 1", axis, got[axis])
		}
	}
	if len(got) != len(Axes()) {
		t.Fatalf("there are %d extractors and %d axes", len(got), len(Axes()))
	}
}

// TestEveryExtractorReturnsAValidFinding is the shape half of task 2.1: no extractor returns a
// default, and every one of them returns something a report can carry.
func TestEveryExtractorReturnsAValidFinding(t *testing.T) {
	for _, fixture := range []string{"python", "typescript", "java", "rust"} {
		t.Run(fixture, func(t *testing.T) {
			s := subjectFor(t, fixture)
			for _, e := range Extractors() {
				f, err := e.Extract(s)
				if err != nil {
					t.Fatalf("%s: %v", e.Axis(), err)
				}
				if err := f.Validate(); err != nil {
					t.Fatalf("%s returned an invalid finding: %v", e.Axis(), err)
				}
				if f.State() == StateNotMeasured && f.MissingInput() == "" {
					t.Fatalf("%s is not_measured and names nothing", e.Axis())
				}
			}
		})
	}
}

// TestAZeroEdgeRepositoryNamesTheFrontend is task 2.2, design D6, acceptance A4 and QA fence 7.2.
//
// 🔴 The fixture is a real Python repository and the Python frontend is a real syntactic analyser, so
// the zero-edge condition here arises the way it arises in production rather than by construction. The
// assertion has three parts and all three matter: the finding must be `not_measured` (not `observed`),
// it must name `frontend_emits_no_edges`, and its CLAIM must name the language — because a reader who
// gets "no topology was read" without "python" learns nothing they can act on.
func TestAZeroEdgeRepositoryNamesTheFrontend(t *testing.T) {
	s := subjectFor(t, "python")
	if len(s.IR.Edges) != 0 {
		t.Skipf("the python fixture now emits %d edges; this test needs an edgeless one", len(s.IR.Edges))
	}
	f := topologyFor(t, s)

	if f.State() != StateNotMeasured {
		t.Fatalf("the graph finding is %s, want not_measured. Zero edges from a SYNTACTIC frontend says "+
			"nothing at all about the code, and reporting it as an observation states a property of our "+
			"parser as a property of the customer's repository.\n  claim: %s", f.State(), f.Claim())
	}
	if f.MissingInput() != MissingFrontendEdges {
		t.Fatalf("the graph finding names %q, want %q", f.MissingInput(), MissingFrontendEdges)
	}
	if !strings.Contains(f.Claim(), "python") {
		t.Fatalf("the claim does not name the language: %q", f.Claim())
	}
	for _, forbidden := range []string{"flat", "single-layer", "independent"} {
		if strings.Contains(strings.ToLower(f.Claim()), forbidden) {
			t.Fatalf("the claim says %q about the repository, which is the P30 defect this rule exists "+
				"to prevent: %q", forbidden, f.Claim())
		}
	}
}

// TestAPartiallyResolvedAxisIsNotMeasured is the third way "no extractor returns a default" breaks,
// and it is the most tempting one because the result LOOKS informative.
//
// 🔴 The `typescript` fixture is exactly the shape: three call sites, two with a literal model, one
// whose model comes from a call the frontend cannot follow. Reporting "2 of your 3 call sites use
// gpt-4o" is true, and it invites the reader to conclude something about the third — which is the one
// we could not read. ANY unresolved node makes the axis `not_measured`, and the claim says how many.
//
// This test exists because the mutation drill found nothing fencing it: `make p33-fence-redcheck`
// disabled the check and every test in the package still passed.
func TestAPartiallyResolvedAxisIsNotMeasured(t *testing.T) {
	s := subjectFor(t, "typescript")

	resolved, unresolved := 0, 0
	for _, n := range s.IR.Nodes {
		if n.Model.ModelID == discovery.UnresolvedSentinel || n.Model.Provider == discovery.UnresolvedSentinel {
			unresolved++
		} else {
			resolved++
		}
	}
	if resolved == 0 || unresolved == 0 {
		t.Skipf("the typescript fixture now resolves %d of %d models; this test needs a MIXED one",
			resolved, resolved+unresolved)
	}

	f := findingFor(t, s, AxisModel)
	if f.State() != StateNotMeasured {
		t.Fatalf("with %d of %d call sites resolved, the model axis is %s. A partial answer here reads "+
			"as a whole one: %q", resolved, resolved+unresolved, f.State(), f.Claim())
	}
	if f.MissingInput() != MissingUnresolvedField {
		t.Fatalf("the finding names %q, want %q", f.MissingInput(), MissingUnresolvedField)
	}
	// And the claim must state the RATIO, so the reader knows how much was readable rather than
	// concluding that nothing was.
	if !strings.Contains(f.Claim(), "of") {
		t.Fatalf("the claim does not say how many call sites were readable: %q", f.Claim())
	}
}

// TestATypedFrontendsEmptyGraphIsAnObservation is the OTHER side of D6, and it is what makes the test
// above meaningful. If every empty graph were `not_measured`, the rule would be "never say anything",
// which is not honesty — it is silence. Zero edges from a TYPED frontend is a fact about the code, and
// `discovery.AnalysisTyped`'s own doc says so.
func TestATypedFrontendsEmptyGraphIsAnObservation(t *testing.T) {
	s := subjectFor(t, "python")
	// The same IR, re-attributed to a typed frontend. This is the one place a constructed report is
	// correct: the question under test is what the extractor does with a FRONTEND FACT, and the fact
	// is the input, not the tree.
	s.Report.Frontends = []discovery.FrontendRun{{
		Language: "go", AnalysisKind: discovery.AnalysisTyped, Nodes: len(s.IR.Nodes),
	}}
	f := topologyFor(t, s)
	if f.State() != StateObserved {
		t.Fatalf("a typed frontend's empty edge list is %s, want observed — otherwise the rule is "+
			"\"never say anything\" rather than \"say what we know\".\n  claim: %s", f.State(), f.Claim())
	}
}

// TestMemoryAndHarnessRefuseTheFloor is the second way task 2.1 breaks, and the subtler one.
//
// `discovery` emits `memory: none` and `harness: single-shot` for EVERY node and documents both as
// *"a statement about the EVIDENCE, not a placeholder ... the honest floor"*. An extractor reading them
// would report "this repository has no memory strategy" for every repository on earth, with a
// measurement's confidence, and every test over every fixture would pass.
func TestMemoryAndHarnessRefuseTheFloor(t *testing.T) {
	s := subjectFor(t, "python")
	for _, axis := range []Axis{AxisMemory, AxisHarness} {
		f := findingFor(t, s, axis)
		if f.State() != StateNotMeasured {
			t.Fatalf("%s is %s, want not_measured. Discovery emits a FLOOR on this axis for every node "+
				"in every repository; reading it as a finding is a sentence about our parser wearing the "+
				"customer's name.\n  claim: %s", axis, f.State(), f.Claim())
		}
		if f.MissingInput() != MissingNotVisibleStatically {
			t.Fatalf("%s names %q, want %q", axis, f.MissingInput(), MissingNotVisibleStatically)
		}
	}
}

// TestBothP34AxesAreRefusedAndSaySo is task 9.2: **stated rather than discovered.**
//
// 🔴 BOTH axes, and on EVERY subject. PRD §3 puts loop and graph behind P34 — "P33 may report on them
// only once P34 has landed, or it names axes the configuration layer does not have" — and a report that
// answered on one of them would be doing exactly that.
func TestBothP34AxesAreRefusedAndSaySo(t *testing.T) {
	for _, fixture := range []string{"python", "typescript", "java", "rust"} {
		s := subjectFor(t, fixture)
		for _, axis := range []Axis{AxisLoop, AxisGraph} {
			f := findingFor(t, s, axis)
			if f.State() != StateRefused {
				t.Fatalf("%s/%s is %s, want refused until P34 lands.\n  claim: %s",
					fixture, axis, f.State(), f.Claim())
			}
			if f.RefusalCause() != RefusalAnalysis {
				t.Fatalf("%s names %q, want %q — nothing about the language or its frontend is missing; "+
					"the ANALYSIS does not exist as an axis in this build", axis, f.RefusalCause(), RefusalAnalysis)
			}
			// STATED. A refusal that does not name the phase that lifts it is a dead end, and a reader
			// has no way to tell "coming" from "never".
			if !strings.Contains(f.Claim(), "P34") {
				t.Fatalf("%s's refusal does not name the phase that lifts it: %q", axis, f.Claim())
			}
		}
	}
}

// TestTheGatedGraphExtractorStillWorks is the anti-rot half of the gate above.
//
// A gated analysis is an analysis nobody runs, and the day the gate lifts is the first day anybody
// finds out it broke. So the topology extractor is exercised directly on every fixture, every day it
// is switched off.
func TestTheGatedGraphExtractorStillWorks(t *testing.T) {
	if !AxisGraph.P34Pending() {
		t.Skip("P34 has landed; the gate is gone and the ordinary extractor tests cover this")
	}
	for _, fixture := range []string{"python", "typescript", "java", "rust"} {
		f := topologyFor(t, subjectFor(t, fixture))
		if err := f.Validate(); err != nil {
			t.Fatalf("%s: the gated topology extractor produces an invalid finding: %v", fixture, err)
		}
		if f.State() == StateNotMeasured && f.MissingInput() == "" {
			t.Fatalf("%s: the gated topology extractor is not_measured and names nothing", fixture)
		}
	}
}

// TestAnEmptyRepositoryStillProducesNineFindings is acceptance A1's hardest case at the extractor
// level, and it also separates the two preconditions: no snapshot and no call sites are different
// facts with different next actions.
func TestAnEmptyRepositoryStillProducesNineFindings(t *testing.T) {
	t.Run("no snapshot", func(t *testing.T) {
		s := Subject{WorkflowID: "wf-1"}
		for _, e := range Extractors() {
			f, err := e.Extract(s)
			if err != nil {
				t.Fatalf("%s: %v", e.Axis(), err)
			}
			if e.Axis().P34Pending() {
				// Both are refused before anything is read, which is correct: a refusal is a fact about
				// this build's capability and does not depend on what the tree contains.
				continue
			}
			if f.MissingInput() != MissingSourceRevision {
				t.Fatalf("%s names %q with no snapshot, want %q", e.Axis(), f.MissingInput(), MissingSourceRevision)
			}
		}
	})
	t.Run("no call sites", func(t *testing.T) {
		s := Subject{WorkflowID: "wf-1", IR: &discovery.IR{}}
		f := findingFor(t, s, AxisModel)
		if f.MissingInput() != MissingNoNodes {
			t.Fatalf("an empty IR names %q, want %q — \"we never saw your code\" and \"we saw it and it "+
				"calls no model we recognise\" send a reader to two different places",
				f.MissingInput(), MissingNoNodes)
		}
	})
}

// TestAnAbsenceNamesAPlaceAReaderCanOpen is the copy defect the live run against
// `nousresearch/hermes-agent` surfaced, as a fence.
//
// 🔴 The report said *"28 of 28 call sites name a model discovery could not resolve
// (n_0017914833d3d240, n_008b903482b33087 and n_0110783593788f45 and 25 more)"*. Every word true, and
// the three things it named were opaque hashes. §8.1's rule is that an absence names what is missing
// AND what the reader could do; a node id satisfies the first half and defeats the second, because
// there is nowhere to type it.
func TestAnAbsenceNamesAPlaceAReaderCanOpen(t *testing.T) {
	s := subjectFor(t, "typescript")
	f := findingFor(t, s, AxisModel)
	if f.State() != StateNotMeasured {
		t.Skipf("the typescript fixture now resolves every model; this test needs an unresolved one")
	}
	// The fixture's call sites live in a real file, so the claim must name it.
	if !strings.Contains(f.Claim(), ".ts") {
		t.Fatalf("the claim names no file a reader could open: %q", f.Claim())
	}
	// And it must NOT name a node id, which is what it named before.
	for _, n := range s.IR.Nodes {
		if strings.Contains(f.Claim(), n.NodeID) {
			t.Fatalf("the claim names the node id %q. A reader cannot open a hash — the node id is what "+
				"the graph evidence link addresses, and the SENTENCE is the part read without following "+
				"a link:\n  %s", n.NodeID, f.Claim())
		}
	}
}

// TestClaimsAreDeterministic is FR15 at the extractor level. Every claim is assembled from map keys,
// and a map iteration inside a sentence is exactly the instability that makes a byte-comparison fence
// flap without anybody changing anything.
func TestClaimsAreDeterministic(t *testing.T) {
	s := subjectFor(t, "python")
	first := map[Axis]string{}
	for _, e := range Extractors() {
		f, _ := e.Extract(s)
		first[e.Axis()] = f.Claim()
	}
	for i := 0; i < 25; i++ {
		for _, e := range Extractors() {
			f, _ := e.Extract(s)
			if got := f.Claim(); got != first[e.Axis()] {
				t.Fatalf("%s produced two different claims for one input:\n  %q\n  %q", e.Axis(), first[e.Axis()], got)
			}
		}
	}
}
