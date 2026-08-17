package herosagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// recordingAnalyser captures the Input the gate actually sends, so a test can assert what was asked
// rather than only what came back.
type recordingAnalyser struct{ seen map[string]Input }

func (r recordingAnalyser) Infer(_ context.Context, in Input, _ string, _ Placement) (Result, error) {
	r.seen[in.WorkflowID] = in
	return Result{}, nil
}

// 🔴 PREVIEW AND RUN MUST ASSEMBLE THE SAME BYTES.
//
// `Preview` exists so "what is the model being shown" can be answered without a provider call. That is
// only worth anything if it shows what the LIVE run sends. Two callers preparing the residue separately
// is the exact skew modelinput.go was written against: the dry run would describe a request the real
// run never makes, and nothing would notice, because both would work.
//
// Both paths go through `Rehearsal.prepare` today. This asserts the consequence rather than the
// structure, so it keeps holding if someone re-splits them.
func TestThePreviewShowsExactlyWhatTheRunWouldSend(t *testing.T) {
	loader := DiskFixtures{Root: repoRoot(t), Discover: realDiscoverer(t)}
	rec := recordingAnalyser{seen: map[string]Input{}}
	r, err := NewRehearsal(loader, realDiscoverer(t), rec, 0.9, 0.7)
	if err != nil {
		t.Fatalf("NewRehearsal: %v", err)
	}

	previews, err := r.Preview()
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := r.Run(context.Background(), "cfg"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(previews) == 0 || len(previews) != len(rec.seen) {
		t.Fatalf("preview covered %d fixture(s), the run asked about %d", len(previews), len(rec.seen))
	}

	for _, p := range previews {
		in, ok := rec.seen[p.Fixture]
		if !ok {
			t.Errorf("%s was previewed and never asked", p.Fixture)
			continue
		}
		mi, aerr := AssembleModelInput(in)
		if aerr != nil {
			t.Fatalf("%s: assembling what the run sent: %v", p.Fixture, aerr)
		}
		sent, berr := mi.Bytes()
		if berr != nil {
			t.Fatalf("%s: %v", p.Fixture, berr)
		}
		if !bytes.Equal(p.ModelInput, sent) {
			t.Errorf("%s: the preview and the run assembled DIFFERENT context.\npreview: %s\nrun:     %s",
				p.Fixture, p.ModelInput, sent)
		}
		// The held-out count travels with it: a preview that showed the ablated graph but reported the
		// wrong ablation would misdescribe how the fixture was scored.
		if p.HeldOutEdges > 0 && len(in.RuleIR.Edges) != 0 && p.Fixture == "go_chain" {
			t.Errorf("go_chain was previewed as ablated but the run was sent %d edge(s)", len(in.RuleIR.Edges))
		}
	}
}

// 🔴 A ZERO HAS TWO CAUSES AND THE REPORT MUST NAME WHICH.
//
// # The defect this defends
//
// "0 correct, 0 wrong, 2 missed" is what the gate printed for three consecutive live runs, and it was
// read each time as a verdict on the model. It cannot be one. The identical numbers are produced by:
//
//   - the model proposed nothing, which IS an answer about the model; and
//   - the model proposed the right edges and `validate` refused every one of them — a missing
//     `confidence` key, a `kind` of "dataflow", a node id that is not in the IR.
//
// The second is a defect in the prompt or the wire contract, and the fix for it has nothing to do with
// the model. `Score` carried no abstentions, so the report was structurally incapable of telling an
// operator which of the two they were looking at, and three runs of evidence were spent on a question
// the harness could not answer.
//
// The assertion is the discrimination itself: two scores with IDENTICAL precision, recall and counts,
// differing only in what validation refused, must not produce the same sentence.
func TestAZeroSaysWhetherTheModelOrTheValidatorProducedIt(t *testing.T) {
	r := &Rehearsal{MinPrecision: 0.9, MinRecall: 0.7}
	numbers := Score{
		Fixture: "py_linear_chain", Language: "python", Precision: 0, Recall: 0,
		TruePositives: 0, FalsePositives: 0, FalseNegatives: 2, Note: "a linear chain",
	}

	silent := r.verdict(RehearsalReport{Scores: []Score{numbers}})
	conf := 0.9
	refusedScore := numbers
	refusedScore.Abstentions = []Abstention{
		{Subject: "n_6fbbf0b34ab205dd→n_f74c615ce693e847", Reason: AbstainNoCandidate},
		{Subject: "n_f74c615ce693e847→n_b812fd2b1a2391ae", Reason: AbstainNoCandidate},
		{Subject: "n_6fbbf0b34ab205dd→n_b812fd2b1a2391ae", Reason: AbstainBelowFloor, Confidence: &conf},
	}
	refused := r.verdict(RehearsalReport{Scores: []Score{refusedScore}})

	if len(silent.Failures) != 1 || len(refused.Failures) != 1 {
		t.Fatalf("both must fail: silent=%v refused=%v", silent.Failures, refused.Failures)
	}
	if silent.Failures[0] == refused.Failures[0] {
		t.Fatalf("a model that said NOTHING and a model whose every proposal was REFUSED produced the "+
			"same failure line. That is the defect: the numbers are identical and only the abstentions "+
			"distinguish them.\n%s", silent.Failures[0])
	}

	// The silent case must not be describable as a rejection...
	if !strings.Contains(silent.Failures[0], "proposed NOTHING") {
		t.Errorf("a genuinely empty answer is not named as one: %s", silent.Failures[0])
	}
	// ...and the refused case must name the REASON and the SUBJECTS, because "something was refused"
	// sends an operator back to the logs while `no_candidate_offered ×2 [n_6fb…→n_f74…]` names the fix.
	for _, want := range []string{
		"REFUSED", string(AbstainNoCandidate), "×2", string(AbstainBelowFloor),
		"n_6fbbf0b34ab205dd→n_f74c615ce693e847",
	} {
		if !strings.Contains(refused.Failures[0], want) {
			t.Errorf("the refusal does not mention %q: %s", want, refused.Failures[0])
		}
	}
}

// The truncation in a summary must SAY it truncated. A list that quietly stopped at six reads as the
// complete set, which is the "no silent caps" rule at sentence grain.
func TestALongAbstentionListSaysHowManyItDidNotShow(t *testing.T) {
	var as []Abstention
	for i := 0; i < maxSubjectsPerReason+4; i++ {
		as = append(as, Abstention{Subject: fmt.Sprintf("n_a%d→n_b%d", i, i), Reason: AbstainUnknownNode})
	}
	got := abstentionSummary(as)
	if !strings.Contains(got, "+4 more") {
		t.Errorf("a truncated summary did not say what it left out: %s", got)
	}
	if !strings.Contains(got, fmt.Sprintf("×%d", maxSubjectsPerReason+4)) {
		t.Errorf("the summary does not carry the TOTAL count, so the truncation hides the size: %s", got)
	}
}

// The empty-truth scoring rule, stated as a test because it is a definition rather than a derivation.
func TestScoringAnEmptyTruth(t *testing.T) {
	f := Fixture{Name: "near_miss", TrueEdges: nil}
	if got := scoreEdges(f, nil, nil); got.Precision != 1 || got.Recall != 1 || !got.Vacuous {
		t.Errorf("emitting nothing over an empty truth scored %+v, want perfect and flagged vacuous", got)
	}
	got := scoreEdges(f, []ProvenancedEdge{{From: "a", To: "b"}}, nil)
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

// 🔴 EVERY FIXTURE'S ANSWER MUST BE PROPOSABLE, and the first live gate run is what found that one was
// not.
//
// `go_chain`'s ground truth is the Go frontend's own edges (task 5.2). D3's fence 1 excludes exactly
// those from the residue. The agent was therefore asked for two edges it was structurally forbidden to
// propose, and scored 0.00 precision and 0.00 recall no matter what it answered — a harness defect that
// read as a model result, on the one fixture in the set that measures edge-finding against a measurement.
//
// Nothing in the harness could see it. `TestAPerfectAgentPassesAndCanBeActivated` could not: its oracle
// is a scripted analyser that answers with the truth directly and never passes through the write fence a
// real runner applies. This test asserts the property itself — the answer is in the residue the fixture
// is scored against — which is analyser-independent and is the shape of the defect rather than one
// instance of it.
func TestEveryFixtureAnswerIsProposableInItsOwnResidue(t *testing.T) {
	loader := DiskFixtures{Root: repoRoot(t), Discover: realDiscoverer(t)}
	fixtures, err := loader.Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	disc := realDiscoverer(t)

	var everHeld int
	for _, f := range fixtures {
		ir, report, derr := disc(f.Repo)
		if derr != nil {
			t.Fatalf("%s: %v", f.Name, derr)
		}

		// 🔴 The fence has CONTENT: without the hold-out, this fixture's answer is unproposable. If this
		// ever stops being true for every measured fixture, the hold-out has become decoration and the
		// test below would pass for the wrong reason.
		if f.Truth == TruthMeasured && len(f.TrueEdges) > 0 {
			if len(unproposable(ir, SelectResidue(ir, report, nil), f.TrueEdges)) == 0 {
				t.Errorf("%s: its answer is proposable from the UNTOUCHED IR, so the hold-out this test "+
					"guards is doing nothing and the test would pass whether or not Run withheld anything",
					f.Name)
			}
		}

		shown, held := withholdAnswer(ir, f.TrueEdges)
		everHeld += held
		if f.Truth == TruthDeclared && held != 0 {
			t.Errorf("%s: the hold-out removed %d edge(s) from a HAND-DECLARED fixture. Those frontends "+
				"emit no edges, so removing one means the answer key and the frontend now disagree about "+
				"a fact somebody measured", f.Name, held)
		}
		if f.Truth == TruthMeasured && held != len(f.TrueEdges) {
			t.Errorf("%s: %d of %d measured true edges were withheld. A measured truth IS the frontend's "+
				"edge set; any it did not match is an answer key that drifted from the measurement",
				f.Name, held, len(f.TrueEdges))
		}

		if missing := unproposable(shown, SelectResidue(shown, report, nil), f.TrueEdges); len(missing) > 0 {
			t.Errorf("%s (%s): %d of %d true edges are NOT in the residue it is scored against: %v. This "+
				"fixture's recall is zero whatever the agent answers, and the number would be reported "+
				"against the model.", f.Name, f.Language, len(missing), len(f.TrueEdges), missing)
		}
	}
	if everHeld == 0 {
		t.Error("no fixture in the set held anything out. That is only correct if no fixture's truth is " +
			"the frontend's own output — and if so the set has lost its one MEASURED answer")
	}
}

// The hold-out is recorded on the score, so a stored report cannot be read as the agent having found
// edges in a graph nobody touched.
func TestTheReportSaysWhatWasHeldOut(t *testing.T) {
	loader := DiskFixtures{Root: repoRoot(t), Discover: realDiscoverer(t)}
	fixtures, err := loader.Fixtures()
	if err != nil {
		t.Fatal(err)
	}
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
	var found bool
	for _, s := range rep.Scores {
		if s.Truth == TruthMeasured && s.HeldOutEdges > 0 {
			found = true
		}
		if s.Truth == TruthDeclared && s.HeldOutEdges != 0 {
			t.Errorf("%s: a hand-declared fixture reports %d held-out edges", s.Fixture, s.HeldOutEdges)
		}
	}
	if !found {
		t.Error("no score records a hold-out. A measured fixture's report has to say that its answer was " +
			"removed from the graph the agent was shown, or the number reads as a finding over an " +
			"untouched one")
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
