package herosagent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/providercall"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// p36_rehearsal_test.go is §7 — evaluating a definition that is a graph.

// 🔴 §7.1 — A MULTI-NODE DEFINITION IS EVALUATED PER NODE, NOT AS ONE AGENT.
//
// This is the fence for the sentence the task is built on: *a definition whose critic never disagrees
// scores well as a whole and is broken in the half that matters.* The merged per-fixture numbers are
// exactly the aggregate that hides it — the analyst carries every fixture, and one number cannot say
// that the second model call bought nothing.
func TestADefinitionWhoseSecondNodeContributesNothingFailsTheGate(t *testing.T) {
	ctx := context.Background()

	// Two nodes. The analyst answers well; the critic runs on every fixture and returns NOTHING.
	d := twoNodeDefinition()
	rep := rehearseWith(t, ctx, d, map[string]modelKind{
		"analyst": answersTruthfully,
		"critic":  answersNothing,
	})

	// 🔴 The MERGED numbers are fine. That is the whole point — a gate reading only these passes.
	if rep.WorstPrecision < 0.9 || rep.WorstRecall < 0.7 {
		t.Fatalf("the merged per-fixture numbers are already failing (p=%.2f r=%.2f), so this test is "+
			"not exercising the thing it exists for: a definition that looks GOOD as a whole and is "+
			"broken in half", rep.WorstPrecision, rep.WorstRecall)
	}

	if rep.Passed {
		t.Fatalf("a definition whose second node contributed nothing PASSED. The analyst carries every "+
			"fixture, the critic is a second model call on every analysis paid for with nothing to show, "+
			"and the merged numbers cannot see it.\n  report: %+v", rep)
	}
	var named bool
	for _, f := range rep.Failures {
		if strings.Contains(f, `node "critic"`) && strings.Contains(f, "contributed NO edge") {
			named = true
		}
	}
	if !named {
		t.Errorf("the gate failed and did not name the node or what was wrong with it: %v", rep.Failures)
	}

	// The per-node record is on the report, so an operator can see WHICH half.
	if len(rep.Nodes) != 2 {
		t.Fatalf("the report carries %d node score(s) for a two-node definition: %+v", len(rep.Nodes), rep.Nodes)
	}
	byID := map[string]NodeScore{}
	for _, n := range rep.Nodes {
		byID[n.NodeID] = n
	}
	if !byID["analyst"].Contributed {
		t.Error("the analyst is recorded as contributing nothing, and it answered every fixture")
	}
	if byID["critic"].Contributed {
		t.Error("the critic is recorded as contributing, and it returned nothing")
	}
	if byID["critic"].Fixtures == 0 {
		t.Error("the critic is recorded as never entered, and it ran on every fixture — those are two " +
			"different failures with two different next actions")
	}
}

// 🔴 §7.1 — ANTI-VACUITY: a definition whose nodes BOTH contribute passes.
//
// Without this, a gate that failed every multi-node definition would satisfy the test above and make
// the graph axis unusable.
func TestADefinitionWhoseNodesBothContributePasses(t *testing.T) {
	ctx := context.Background()
	d := twoNodeDefinition()
	rep := rehearseWith(t, ctx, d, map[string]modelKind{
		"analyst": answersTruthfully, "critic": answersTruthfully,
	})
	if !rep.Passed {
		t.Fatalf("a definition whose nodes both contribute was refused: %v", rep.Failures)
	}
}

// 🔴 §7.2 — the report is per NODE and per FIXTURE, and both are readable.
func TestTheRehearsalReportsPerNodeAndPerFixture(t *testing.T) {
	ctx := context.Background()
	d := twoNodeDefinition()
	rep := rehearseWith(t, ctx, d, map[string]modelKind{
		"analyst": answersTruthfully, "critic": answersTruthfully,
	})
	if len(rep.Scores) == 0 {
		t.Fatal("the report carries no per-fixture scores")
	}
	if len(rep.Nodes) == 0 {
		t.Fatal("the report carries no per-node scores. `which fixture` and `which node` are two " +
			"different questions and a report answering one of them sends half the readers away")
	}
	// The node order is the DEFINITION's ordering, not a map's.
	if rep.Nodes[0].NodeID != "analyst" || rep.Nodes[1].NodeID != "critic" {
		t.Errorf("the node scores are not in the definition's ordering: %v", rep.Nodes)
	}
	// And the whole thing round-trips as the JSON stored on the version row.
	js, err := rep.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"nodes"`, `"node_id"`, `"coverage"`, `"scores"`} {
		if !strings.Contains(js, want) {
			t.Errorf("the stored report carries no %s: %s", want, js)
		}
	}
}

// 🔴 §7.4 / D-36.5 — A DEFINITION DECLARING A CAPABILITY THE CALIBRATION SET CANNOT EXERCISE IS
// REFUSED, not passed with a warning.
//
// `RehearsalPassed` arms the activation gate. A warning beside a passing verdict is a warning that
// arms the gate, and the gate is the only thing between an unmeasured configuration and every tenant
// at once.
func TestARehearsalThatCannotExerciseTheCapabilityIsRefused(t *testing.T) {
	ctx := context.Background()

	// A definition declaring a CONDITIONAL EDGE whose predicate holds on EVERY fixture — so the route
	// was never compared against its alternative.
	//
	// 🔴 `produced_narrative` rather than `produced_edges`, and the difference is the finding task 7.4
	// asks for. `produced_edges` IS exercised both ways by the existing set, because the near-miss
	// fixtures have an empty true edge set: the analyst produces nothing on those, the predicate does
	// not hold, and the critic is skipped. See TestTheCalibrationSetExercisesAConditionalEdge.
	// `produced_narrative` holds on every fixture this model answers, which is the uncovered shape.
	d := twoNodeDefinition()
	d.Edges = []variantspec.Edge{{FromNodeID: "analyst", ToNodeID: "critic",
		Kind: variantspec.EdgeKindPredicate, Predicate: "produced_narrative"}}

	rep := rehearseWith(t, ctx, d, map[string]modelKind{
		"analyst": answersTruthfully, "critic": answersTruthfully,
	})

	if !rep.Coverage.DeclaresConditional {
		t.Fatal("the coverage record does not see the declared conditional edge, so this test is " +
			"measuring nothing")
	}
	if rep.Coverage.ExercisedConditional {
		t.Fatal("the coverage record claims the conditional edge was exercised in both directions; the " +
			"predicate held on every fixture, so it was taken one way only")
	}
	if rep.Passed {
		t.Fatalf("a definition whose conditional edge was never compared against its alternative "+
			"PASSED. A rehearsal that cannot fail on the new capability is not a rehearsal of it — and "+
			"passing arms the activation gate.\n  coverage: %+v", rep.Coverage)
	}
	var named bool
	for _, f := range rep.Failures {
		if strings.Contains(f, "CONDITIONAL EDGE") {
			named = true
		}
	}
	if !named {
		t.Errorf("the refusal does not name the capability that went unexercised: %v", rep.Failures)
	}
	if rep.Coverage.Sufficient() {
		t.Error("the coverage reports itself sufficient while carrying a gap")
	}

	// 🔴 ANTI-VACUITY: a definition declaring NEITHER capability has no gap and is not refused for one.
	plain := twoNodeDefinition()
	plainRep := rehearseWith(t, ctx, plain, map[string]modelKind{
		"analyst": answersTruthfully, "critic": answersTruthfully,
	})
	if !plainRep.Coverage.Sufficient() {
		t.Errorf("a definition declaring no fan-in and no conditional edge reports a coverage gap: %v",
			plainRep.Coverage.Gaps)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────────────────────────

// modelKind names which behaviour a node's model has, so a test reads as a claim about the DEFINITION
// rather than as a wiring exercise.
type modelKind int

const (
	// answersTruthfully returns exactly the fixture's ground-truth edges — the node that works.
	answersTruthfully modelKind = iota
	// answersNothing returns an empty result. It is the "critic that never disagrees": it costs a
	// second model call on every analysis and contributes nothing, and the MERGED per-fixture numbers
	// cannot see it because the other node carries them.
	answersNothing
)

// rehearseWith runs the REAL Rehearsal over the REAL disk fixtures, with a real Runner walking the
// definition — so the per-node numbers come from an execution rather than from a stub that was told
// what to say. A scripted Analyser would let the gate be driven directly, which is the right shape for
// testing the FLOORS and the wrong one for testing per-node attribution: the attribution is produced by
// the runner, and a stub would be asserting that the test fixture fills in a struct.
func rehearseWith(t *testing.T, ctx context.Context, d Definition, kinds map[string]modelKind) RehearsalReport {
	t.Helper()
	loader := DiskFixtures{Root: repoRoot(t), Discover: realDiscoverer(t)}
	fixtures, err := loader.Fixtures()
	if err != nil {
		t.Fatalf("the calibration set did not load: %v", err)
	}
	truth := map[string][]Pair{}
	for _, f := range fixtures {
		truth[f.Name] = f.TrueEdges
	}

	hash, herr := d.ConfigHash()
	if herr != nil {
		t.Fatal(herr)
	}
	models := map[string]Model{}
	for id, kind := range kinds {
		models[id] = &fixtureModel{kind: kind, truth: truth}
	}
	runner, rerr := NewRunner(models[d.Nodes[0].NodeID], NewMemInferenceStore(), 0.5,
		func() int64 { return 1 },
		WithNodeModels(func(n Node) (Model, error) {
			m, ok := models[n.NodeID]
			if !ok {
				return nil, fmt.Errorf("no model for node %q", n.NodeID)
			}
			return m, nil
		}))
	if rerr != nil {
		t.Fatal(rerr)
	}
	reh, nerr := NewRehearsal(loader, realDiscoverer(t), runner, DefaultMinPrecision, DefaultMinRecall)
	if nerr != nil {
		t.Fatal(nerr)
	}
	rep, runErr := reh.Run(ctx, BindDefinition(hash, d))
	if runErr != nil {
		t.Fatalf("the rehearsal could not run: %v", runErr)
	}
	return rep
}

// fixtureModel answers each fixture according to its kind.
type fixtureModel struct {
	kind  modelKind
	truth map[string][]Pair
}

func (m *fixtureModel) Infer(_ context.Context, in Input) (RawResult, providercall.Usage, error) {
	usage := providercall.Usage{InputTokens: 1, OutputTokens: 1}
	if m.kind == answersNothing {
		return RawResult{}, usage, nil
	}
	conf := 0.99
	out := RawResult{Narrative: "the fixture's own answer"}
	for _, p := range m.truth[in.WorkflowID] {
		out.Edges = append(out.Edges, RawEdge{From: p.From, To: p.To, Kind: "data", Confidence: &conf})
	}
	return out, usage, nil
}

// 🔴 §7.4 — DOES THE CALIBRATION SET EXERCISE A FAN-IN AND A CONDITIONAL EDGE AT ALL?
//
// The task says to establish this rather than assume it, because "a calibration set sized for one node
// may not exercise a fan-in at all, in which case rehearsal passes without testing the new capability
// — a fence that cannot go red".
//
// # The answer, and it was not the expected one
//
// **A conditional edge IS exercised, and by an accident of the existing set that is worth writing
// down.** The near-miss fixtures (`py_linear_chain`, `py_fanout_no_merge`, `py_independent_calls`)
// have an EMPTY true edge set — they exist to catch an agent that connects whatever is nearby. So an
// analyst answering truthfully produces nothing on those and edges on the others, which takes a
// `produced_edges` predicate in BOTH directions across one run of the set.
//
// The set was not designed for that; it is a property of having deliberately-empty fixtures. This test
// pins it, so a later edit that removes the empty-truth fixtures fails HERE — naming the consequence —
// rather than silently making every conditional-edge rehearsal one-directional.
//
// **A fan-in is exercised too**, because every node of a group runs on every fixture the ordering
// reaches. That one is not an accident and needs no fixture change.
func TestTheCalibrationSetExercisesAConditionalEdgeAndAFanIn(t *testing.T) {
	ctx := context.Background()

	d := twoNodeDefinition()
	d.Edges = []variantspec.Edge{{FromNodeID: "analyst", ToNodeID: "critic",
		Kind: variantspec.EdgeKindPredicate, Predicate: "produced_edges"}}
	rep := rehearseWith(t, ctx, d, map[string]modelKind{
		"analyst": answersTruthfully, "critic": answersTruthfully,
	})

	if !rep.Coverage.ExercisedConditional {
		t.Errorf("the calibration set no longer takes a `produced_edges` predicate in both directions. "+
			"It did so because the near-miss fixtures have an EMPTY true edge set, so the analyst "+
			"produces nothing on them; removing those fixtures makes every conditional-edge rehearsal "+
			"one-directional, which is a rehearsal that cannot fail on the capability.\n  coverage: %+v",
			rep.Coverage)
	}
	if !rep.Coverage.ExercisedFanIn {
		t.Errorf("the calibration set never ran two nodes on one fixture, so a fan-in cannot be "+
			"rehearsed: %+v", rep.Coverage)
	}
	// 🔴 And the reason it works is asserted directly, not inferred from the flag above — otherwise a
	// later change could satisfy the flag some other way and this note would become false.
	loader := DiskFixtures{Root: repoRoot(t), Discover: realDiscoverer(t)}
	fixtures, err := loader.Fixtures()
	if err != nil {
		t.Fatal(err)
	}
	var empty, nonEmpty int
	for _, f := range fixtures {
		if len(f.TrueEdges) == 0 {
			empty++
			continue
		}
		nonEmpty++
	}
	if empty == 0 || nonEmpty == 0 {
		t.Errorf("the set has %d empty-truth and %d non-empty-truth fixture(s); both are needed for a "+
			"`produced_edges` predicate to be taken in both directions", empty, nonEmpty)
	}
}
