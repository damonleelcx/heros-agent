package herosagent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// P30 workstream 5 — calibration and the rehearsal gate.

// realDiscoverer runs the actual discovery pipeline over a fixture tree. The rehearsal's value comes
// from running against REAL trees; a fake discoverer would measure the harness and nothing else.
func realDiscoverer(t *testing.T) Discoverer {
	t.Helper()
	reg, err := discovery.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	return func(repo string) (*discovery.IR, discovery.DiscoveryReport, error) {
		out, err := discovery.Run(discovery.Options{Repo: repo, Registry: reg, WorkflowID: "fixture"})
		if err != nil {
			return nil, discovery.DiscoveryReport{}, err
		}
		ir := out.IR
		return &ir, out.Report, nil
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// scriptedAnalyser answers with a fixed edge set per fixture, so a test can drive the GATE rather than
// a model.
type scriptedAnalyser struct {
	byWorkflow map[string][]ProvenancedEdge
	// connectEverything makes it emit an edge for every residue pair — the agent with no
	// discriminative power, which the near-misses exist to catch.
	connectEverything bool
	err               error
}

func (s scriptedAnalyser) Infer(_ context.Context, in Input, _ string, _ Placement) (Result, error) {
	if s.err != nil {
		return Result{Code: CodeProviderFailed}, s.err
	}
	if s.connectEverything {
		out := Result{Code: CodeOK}
		for _, p := range in.Residue.Pairs {
			out.Edges = append(out.Edges, ProvenancedEdge{From: p.From, To: p.To, Kind: "data", Confidence: 0.99})
		}
		return out, nil
	}
	return Result{Code: CodeOK, Edges: s.byWorkflow[in.WorkflowID]}, nil
}

// Task 5.1/5.2 — the calibration set loads WHOLE, from real trees, and the Go fixture's ground truth is
// the frontend's own MEASURED output.
func TestTheCalibrationSetLoadsWholeAndMeasuresGoFromTheFrontend(t *testing.T) {
	loader := DiskFixtures{Root: repoRoot(t), Discover: realDiscoverer(t)}
	fixtures, err := loader.Fixtures()
	if err != nil {
		t.Fatalf("the calibration set did not load: %v", err)
	}
	if len(fixtures) != len(calibrationSet) {
		t.Fatalf("%d fixtures loaded, %d declared — a shorter set is an easier gate nobody chose",
			len(fixtures), len(calibrationSet))
	}

	langs := map[string]bool{}
	var measured, nearMissEmpty int
	for _, f := range fixtures {
		langs[f.Language] = true
		if f.Truth == TruthMeasured {
			measured++
		}
		if len(f.TrueEdges) == 0 {
			nearMissEmpty++
		}
		if f.Note == "" {
			t.Errorf("fixture %q records no note — a failing report would name it and say nothing about "+
				"what it is evidence for", f.Name)
		}
	}
	if measured == 0 {
		t.Error("no fixture's ground truth is MEASURED. The Go frontend's real edge output is the only " +
			"ground truth this platform owns, and a set of entirely hand-declared answers is a set of " +
			"somebody's readings")
	}
	if nearMissEmpty == 0 {
		t.Error("no fixture has an EMPTY true edge set. Without one the gate cannot tell an agent that " +
			"discriminates from one that connects whatever is nearby")
	}
	// The three named near-misses are all present.
	for _, want := range []string{"py_linear_chain", "py_fanout_no_merge", "py_independent_calls"} {
		var found bool
		for _, f := range fixtures {
			if f.Name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the near-miss %q is missing from the calibration set", want)
		}
	}
	if len(langs) < 5 {
		t.Errorf("the set covers %d language(s): %v. A gate measured on one language ships a disaster "+
			"to the others.", len(langs), langs)
	}
}

// 🔴 TASK 5.4 — THE GATE READS THE MINIMUM, NOT THE MEAN.
//
// This is the assertion that would pass under a mean-based gate and must not: an agent that is perfect
// on eight fixtures and catastrophic on one has a fine mean and ships a disaster to that one language's
// customers.
func TestTheGateReadsTheMinimumAndNotTheMean(t *testing.T) {
	rep := RehearsalReport{MinPrecision: 0.9, MinRecall: 0.7}
	// Twelve perfect fixtures, one catastrophe. The count is chosen so the MEAN is comfortably ABOVE
	// the floor — otherwise the test would pass under a mean-based gate too and would prove nothing.
	for i := 0; i < 12; i++ {
		rep.Scores = append(rep.Scores, Score{Fixture: "good", Precision: 1, Recall: 1})
	}
	rep.Scores = append(rep.Scores, Score{Fixture: "disaster", Language: "rust", Precision: 0.1, Recall: 0.1})

	r := &Rehearsal{MinPrecision: 0.9, MinRecall: 0.7}
	got := r.verdict(rep)

	if got.MeanPrecision <= 0.9 {
		t.Fatalf("the fixture does not model the failure: the MEAN precision is %.2f, which a "+
			"mean-based gate would already reject", got.MeanPrecision)
	}
	if got.Passed {
		t.Errorf("a set with a catastrophic fixture PASSED on a mean of %.2f. The mean is exactly the "+
			"aggregate that hides a per-repository disaster.", got.MeanPrecision)
	}
	if got.WorstPrecision != 0.1 {
		t.Errorf("worst precision = %.2f, want 0.1", got.WorstPrecision)
	}
	// Task 5.5 — the failure NAMES the fixture, its language and its numbers.
	if len(got.Failures) != 1 {
		t.Fatalf("failures = %v", got.Failures)
	}
	for _, want := range []string{"disaster", "rust", "0.10"} {
		if !strings.Contains(got.Failures[0], want) {
			t.Errorf("the failure does not mention %q: %s", want, got.Failures[0])
		}
	}
	// And the mean is REPORTED, so somebody can still quote it.
	if got.MeanPrecision == 0 {
		t.Error("the mean is not reported")
	}
}

// 🔴 TASK 5.7 — ANTI-VACUITY. A fixture set that fails to load FAILS the rehearsal; an empty one is
// never a pass.
func TestAnUnloadableOrEmptyFixtureSetFailsTheRehearsal(t *testing.T) {
	t.Run("load failure", func(t *testing.T) {
		r, err := NewRehearsal(failingLoader{}, realDiscoverer(t), scriptedAnalyser{}, 0.9, 0.7)
		if err != nil {
			t.Fatal(err)
		}
		rep, rerr := r.Run(context.Background(), "cfg1")
		if rerr == nil {
			t.Fatal("a rehearsal whose fixtures could not be loaded returned no error")
		}
		if rep.Passed {
			t.Error("a rehearsal that measured NOTHING passed")
		}
	})
	t.Run("empty set", func(t *testing.T) {
		r, err := NewRehearsal(emptyLoader{}, realDiscoverer(t), scriptedAnalyser{}, 0.9, 0.7)
		if err != nil {
			t.Fatal(err)
		}
		rep, rerr := r.Run(context.Background(), "cfg1")
		if rerr == nil || rep.Passed {
			t.Error("an EMPTY calibration set passed every threshold vacuously — which is a gate that " +
				"reports success for an agent nothing measured")
		}
	})
}

type failingLoader struct{}

func (failingLoader) Fixtures() ([]Fixture, error) { return nil, errors.New("disk unreadable") }

type emptyLoader struct{}

func (emptyLoader) Fixtures() ([]Fixture, error) { return nil, nil }

// 🔴 The near-miss does its job: an agent that connects everything scores ZERO PRECISION on it and the
// gate refuses. This is the whole reason the near-misses exist.
func TestAnAgentThatConnectsEverythingFailsTheNearMisses(t *testing.T) {
	loader := DiskFixtures{Root: repoRoot(t), Discover: realDiscoverer(t)}
	r, err := NewRehearsal(loader, realDiscoverer(t), scriptedAnalyser{connectEverything: true},
		DefaultMinPrecision, DefaultMinRecall)
	if err != nil {
		t.Fatal(err)
	}
	rep, rerr := r.Run(context.Background(), "cfg1")
	if rerr == nil && rep.Passed {
		t.Fatal("an agent that emits an edge for EVERY candidate pair passed the gate. It has no " +
			"discriminative power at all, and the near-misses exist precisely to catch it.")
	}
	// And it fails on the fixture whose true edge count is zero, by NAME.
	var named bool
	for _, f := range rep.Failures {
		if strings.Contains(f, "py_independent_calls") {
			named = true
		}
	}
	if !named {
		t.Errorf("the two-independent-calls fixture did not fail: %v", rep.Failures)
	}
}

// The empty-truth scoring rule, stated as a test because it is a definition rather than a derivation.
func TestScoringAnEmptyTruth(t *testing.T) {
	f := Fixture{Name: "near_miss", TrueEdges: nil}
	if got := scoreEdges(f, nil); got.Precision != 1 || got.Recall != 1 || !got.Vacuous {
		t.Errorf("emitting nothing over an empty truth scored %+v, want perfect and flagged vacuous", got)
	}
	got := scoreEdges(f, []ProvenancedEdge{{From: "a", To: "b"}})
	if got.Precision != 0 {
		t.Errorf("emitting an edge over an empty truth scored precision %.2f, want 0 — this is the "+
			"number that catches an agent with no discriminative power", got.Precision)
	}
}

// Task 5.6 — the per-fixture report is STORED on the version row, whether it passed or failed.
func TestTheRehearsalReportIsStoredEvenOnFailure(t *testing.T) {
	store := NewMemVersionStore()
	ctx := context.Background()
	if err := store.Put(ctx, Version{ConfigHash: "cfg1", RehearsalState: RehearsalPending}); err != nil {
		t.Fatal(err)
	}
	loader := DiskFixtures{Root: repoRoot(t), Discover: realDiscoverer(t)}
	r, err := NewRehearsal(loader, realDiscoverer(t), scriptedAnalyser{connectEverything: true},
		DefaultMinPrecision, DefaultMinRecall)
	if err != nil {
		t.Fatal(err)
	}
	if _, gerr := r.GateActivation(ctx, store, "cfg1"); gerr == nil {
		t.Fatal("a failing rehearsal gated nothing")
	}
	v, _, _ := store.Get(ctx, "cfg1")
	if v.RehearsalState != RehearsalFailed {
		t.Errorf("state = %q, want %q", v.RehearsalState, RehearsalFailed)
	}
	if v.RehearsalReport == "" {
		t.Error("a FAILED rehearsal discarded its numbers, leaving an operator with `it did not pass` " +
			"and nothing to act on")
	}
	if !strings.Contains(v.RehearsalReport, "py_independent_calls") {
		t.Errorf("the stored report does not name the failing fixture: %s", v.RehearsalReport)
	}
}

// A perfect agent passes, and the activation gate then opens. The positive case matters: a gate that
// only ever refuses is indistinguishable from one that is broken.
func TestAPerfectAgentPassesAndCanBeActivated(t *testing.T) {
	loader := DiskFixtures{Root: repoRoot(t), Discover: realDiscoverer(t)}
	fixtures, err := loader.Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	// Answer each fixture with exactly its true edge set.
	byWorkflow := map[string][]ProvenancedEdge{}
	for _, f := range fixtures {
		for _, p := range f.TrueEdges {
			byWorkflow[f.Name] = append(byWorkflow[f.Name],
				ProvenancedEdge{From: p.From, To: p.To, Kind: "data", Confidence: 0.99})
		}
	}
	r, err := NewRehearsal(loader, realDiscoverer(t), scriptedAnalyser{byWorkflow: byWorkflow},
		DefaultMinPrecision, DefaultMinRecall)
	if err != nil {
		t.Fatal(err)
	}
	rep, rerr := r.Run(context.Background(), "cfg1")
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !rep.Passed {
		t.Errorf("an agent answering with exactly the truth FAILED: %v", rep.Failures)
	}
	if rep.WorstPrecision != 1 || rep.WorstRecall != 1 {
		t.Errorf("worst precision/recall = %.2f/%.2f, want 1/1", rep.WorstPrecision, rep.WorstRecall)
	}
}

// 🔴 THE ANTI-VACUITY FENCE THE SET ITSELF NEEDED, found by MEASURING the set rather than reading it.
//
// The first calibration set loaded, scored and passed — and every one of its nine fixtures had an EMPTY
// true edge set, because the Go tree it pointed at (`discovery/testdata/samplerepo`) resolves no edges.
// A set like that measures whether the agent DISCRIMINATES and cannot measure whether it finds an edge
// at all: an agent that emits nothing, ever, scores a perfect 1.00/1.00 on every fixture and passes.
//
// That is task 5.7's vacuity one level up, and nothing in the harness could see it. This is the
// assertion that can.
func TestTheSetCanMeasureEdgeFinding(t *testing.T) {
	loader := DiskFixtures{Root: repoRoot(t), Discover: realDiscoverer(t)}
	fixtures, err := loader.Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	var withEdges []string
	for _, f := range fixtures {
		if len(f.TrueEdges) > 0 {
			withEdges = append(withEdges, f.Name)
		}
	}
	if len(withEdges) == 0 {
		t.Fatal("EVERY fixture has an empty true edge set. The gate can measure discrimination and " +
			"cannot measure edge-finding — an agent that emits nothing scores 1.00/1.00 everywhere and " +
			"passes. A calibration set that cannot fail an agent which does nothing is not a gate.")
	}
	var measured bool
	for _, f := range fixtures {
		if f.Truth == TruthMeasured && len(f.TrueEdges) > 0 {
			measured = true
		}
	}
	if !measured {
		t.Error("no fixture has a NON-EMPTY MEASURED truth. Every rich answer in the set is somebody's " +
			"reading, and the Go frontend's real output — the only ground truth this platform owns — is " +
			"contributing nothing.")
	}

	// And the fence bites: an agent that emits NOTHING must fail this set.
	r, err := NewRehearsal(loader, realDiscoverer(t), scriptedAnalyser{}, DefaultMinPrecision, DefaultMinRecall)
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := r.Run(context.Background(), "cfg1")
	if rep.Passed {
		t.Errorf("an agent that emits NOTHING passed the gate. Fixtures with a non-empty truth: %v — "+
			"they are not doing their job.", withEdges)
	}
}
