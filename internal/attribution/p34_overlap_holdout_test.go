package attribution

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/linkage"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// P34 task 6.4 / QA fence 9.11 — attribution under OVERLAPPING SPANS, measured on a holdout.
//
// # Why this exists and why it is a measurement rather than an argument
//
// P34 lets a spec declare two nodes concurrent. Design.md lists the consequence under "risks this
// design accepts": *"Attribution assumes a linear span sequence in more than one place today,
// implicitly. Concurrency makes overlapping spans a real shape, and the ablation discipline applies:
// prove on a holdout that attribution does not degrade before this ships."* PRD §9.5 adds the clause
// that matters: **no pure-refactor exemption** — a change that looks equivalent still has to produce
// the numbers.
//
// The mechanism at risk is `executionOrder`, which orders nodes by span START TIME with the node id as
// a tiebreak. Under sequential execution that is a faithful reading of what happened. Under concurrency
// it is not: two overlapping spans start microseconds apart in an order the machine chose, so the
// "sequence" attribution walks is partly noise — and `firstDivergenceOrdered` returns the FIRST node in
// that walk whose output diverges.
//
// # The holdout
//
// 60 cases with a KNOWN first-divergence node, each rendered twice: once with sequential spans, once
// with the same two nodes overlapping. Agreement between the two localizations is the metric. A case
// whose answer changes when the only difference is wall-clock interleaving is a case where attribution
// degraded under concurrency.
//
// The cases are generated rather than fixtured, and the generation is DETERMINISTIC (an index-driven
// permutation, no clock and no random source) so the holdout is reproducible and a regression is a real
// change rather than a reroll.

// holdoutSize is the number of cases. Large enough that a single flipped case moves the rate visibly,
// small enough to run in a unit suite.
const holdoutSize = 60

// overlapCase is one holdout case: a two-node fan-out where `bad` produces a contract-violating output.
type overlapCase struct {
	caseID string
	// nodes are the two concurrent siblings, in the order the SPEC declares them.
	nodeA, nodeB string
	// bad is the node whose output violates its contract — the ground truth.
	bad string
	// aFirst is whether node A's span starts first in the sequential rendering.
	aFirst bool
}

func holdoutCases() []overlapCase {
	out := make([]overlapCase, 0, holdoutSize)
	for i := 0; i < holdoutSize; i++ {
		c := overlapCase{
			caseID: fmt.Sprintf("case-%02d", i),
			nodeA:  "n_alpha",
			nodeB:  "n_beta",
			aFirst: i%2 == 0,
		}
		// Alternate the ground truth so a localizer that always answered "n_alpha" scores 50%, not 100%.
		// 🔴 Deliberately anti-correlated with the id ordering for half the cases: `executionOrder`'s
		// tiebreak is the node id, so a holdout where the guilty node is always the alphabetically-first
		// one would be passed by a localizer that reads nothing at all.
		if i%3 == 0 {
			c.bad = c.nodeB
		} else {
			c.bad = c.nodeA
		}
		out = append(out, c)
	}
	return out
}

// holdoutIR is the two-node graph the cases run on. Both nodes declare an output contract, so a
// violating output is a CONTRACT divergence — the strongest evidence kind, and the one that must not
// move when only the wall-clock changes.
func holdoutIR() *discovery.IR {
	node := func(id string) discovery.IRNode {
		return discovery.IRNode{
			NodeID: id, Kind: "static_definition",
			CallSite:        discovery.IRCallSite{File: "flow.py", Symbol: id, LineStart: 1, LineEnd: 1},
			Model:           discovery.IRModel{Provider: "anthropic", ModelID: "claude-sonnet-5", Params: map[string]any{}},
			Prompt:          discovery.IRPrompt{Inline: id, Variables: []string{}},
			ContextAssembly: discovery.IRContextAssembly{Policy: "inline_messages"},
			IOContract: discovery.IRIOContract{
				InputSchema: map[string]any{"type": "object"},
				OutputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"answer": map[string]any{"type": "string"}},
					"required":   []any{"answer"},
				},
			},
		}
	}
	return &discovery.IR{
		IRVersion: "1.0.0",
		Nodes:     []discovery.IRNode{node("n_alpha"), node("n_beta")},
		Edges:     []discovery.IREdge{},
	}
}

// base is a fixed instant. 🔴 No clock is read anywhere in this file: a holdout whose inputs move with
// the calendar produces a different number on a different day, and then a regression and a Tuesday are
// indistinguishable.
var holdoutBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// renderTrace builds the trace for one case. `overlapping` decides whether the two node spans run one
// after the other or at the same time.
//
// 🔴 The two renderings differ ONLY in wall-clock placement. Same nodes, same outputs, same contract
// violation, same durations. Anything that changes between them is attributable to the interleaving and
// to nothing else, which is what makes the comparison a measurement rather than two unrelated runs.
func renderTrace(c overlapCase, overlapping bool) evalharness.Trace {
	first, second := c.nodeA, c.nodeB
	if !c.aFirst {
		first, second = c.nodeB, c.nodeA
	}
	const dur = 100 * time.Millisecond

	startFirst := holdoutBase
	startSecond := holdoutBase.Add(dur)
	if overlapping {
		// The whole point: both start at (almost) the same moment, in an order the machine chose. One
		// nanosecond apart is the honest model of two goroutines launched together.
		startSecond = holdoutBase.Add(time.Nanosecond)
	}

	span := func(node string, start time.Time) telemetry.Span {
		return telemetry.Span{
			TraceID:   telemetry.TraceID(c.caseID),
			SpanID:    telemetry.NodeSpanID(c.caseID + ":" + node),
			Kind:      telemetry.SpanKindNode,
			Name:      "chat " + node,
			StartTime: start,
			EndTime:   start.Add(dur),
			Status:    telemetry.SpanStatusOK,
			Attributes: map[string]any{
				telemetry.AttrNodeID:     node,
				telemetry.AttrCostUSD:    0.001,
				telemetry.AttrLatencyMS:  float64(dur / time.Millisecond),
				telemetry.AttrNodeFailed: false,
			},
		}
	}

	// 🔴 Every span is status OK. The fault is a CONTENT contract violation — the "silent" AI failure —
	// so a localizer that read only span status would score zero on this holdout, which is the point:
	// what must not move under overlap is the reading of OUTPUTS.
	outputs := map[string]json.RawMessage{
		c.nodeA: json.RawMessage(`{"answer":"ok"}`),
		c.nodeB: json.RawMessage(`{"answer":"ok"}`),
	}
	outputs[c.bad] = json.RawMessage(`{}`) // violates the contract: `answer` is required

	tr := evalharness.Trace{NodeOutputs: outputs}
	tr.Trace = telemetry.Trace{
		Run:   telemetry.RunContext{CaseID: c.caseID},
		Spans: []telemetry.Span{span(first, startFirst), span(second, startSecond)},
	}
	tr.Output = json.RawMessage(`{"answer":"wrong"}`)
	tr.Failed = true
	return tr
}

func holdoutFailingCases(overlapping bool) []FailingCase {
	var out []FailingCase
	for _, c := range holdoutCases() {
		out = append(out, FailingCase{
			Case:  evalharness.Case{CaseID: c.caseID, WorkflowID: "wf-holdout", Label: evalharness.LabelNone},
			Trace: renderTrace(c, overlapping),
		})
	}
	return out
}

// truth maps case id → the node that actually diverged.
func holdoutTruth() map[string]string {
	out := map[string]string{}
	for _, c := range holdoutCases() {
		out[c.caseID] = c.bad
	}
	return out
}

func accuracyOf(got PerNodeContribution, truth map[string]string) (correct int, total int) {
	for _, ca := range got.Cases {
		total++
		if ca.FirstDivergenceNode == truth[ca.CaseID] {
			correct++
		}
	}
	return correct, total
}

// TestAttributionDoesNotDegradeUnderOverlappingSpans is the holdout, and the number it prints is the
// evidence task 6.4 asks for.
//
// 🔴 It asserts EQUALITY, not "still reasonable". Concurrency changes wall-clock and nothing else about
// what each node produced, so a localizer that reads the OUTPUTS should give byte-identical answers.
// Any gap is attribution reading the interleaving as evidence.
func TestAttributionDoesNotDegradeUnderOverlappingSpans(t *testing.T) {
	ir := holdoutIR()
	v := Variant{VariantID: "v1", ConfigHash: "c1", EvalSetHash: "e1", WorkflowID: "wf"}
	truth := holdoutTruth()

	seq := Attribute(ir, v, holdoutFailingCases(false))
	over := Attribute(ir, v, holdoutFailingCases(true))

	seqOK, seqN := accuracyOf(seq, truth)
	overOK, overN := accuracyOf(over, truth)

	t.Logf("P34 §6.4 attribution holdout — %d cases", holdoutSize)
	t.Logf("  sequential spans : %d/%d correct (%.1f%%)", seqOK, seqN, 100*float64(seqOK)/float64(seqN))
	t.Logf("  overlapping spans: %d/%d correct (%.1f%%)", overOK, overN, 100*float64(overOK)/float64(overN))

	// The holdout has to be able to fail. A baseline that was already wrong would make "no degradation"
	// true and meaningless.
	if seqOK != seqN {
		t.Fatalf("the SEQUENTIAL baseline localizes %d/%d; the holdout cannot measure degradation from a "+
			"baseline that is already wrong", seqOK, seqN)
	}
	if overOK != overN {
		t.Fatalf("attribution DEGRADED under overlapping spans: %d/%d correct, was %d/%d.\n"+
			"Concurrency changes wall-clock and nothing else about what each node produced, so a localizer "+
			"reading OUTPUTS must give identical answers. A gap here means the interleaving is being read "+
			"as evidence — which is exactly what design.md flagged as the risk this phase accepts, and "+
			"PRD §9.5 requires proved BEFORE concurrency ships, with no pure-refactor exemption.",
			overOK, overN, seqOK, seqN)
	}

	// And the per-case answers must agree PAIRWISE, not merely in aggregate. Two localizations that were
	// both 55% correct on different halves of the set would satisfy an accuracy comparison and would mean
	// attribution had become a coin flip.
	seqByCase := map[string]string{}
	for _, ca := range seq.Cases {
		seqByCase[ca.CaseID] = ca.FirstDivergenceNode
	}
	flipped := 0
	for _, ca := range over.Cases {
		if seqByCase[ca.CaseID] != ca.FirstDivergenceNode {
			flipped++
			if flipped <= 3 {
				t.Errorf("case %s localizes to %q sequentially and %q under overlap",
					ca.CaseID, seqByCase[ca.CaseID], ca.FirstDivergenceNode)
			}
		}
	}
	if flipped > 0 {
		t.Fatalf("%d/%d cases changed their answer under overlap. An aggregate accuracy comparison would "+
			"have missed this if the flips cancelled out, which is why the check is pairwise", flipped, holdoutSize)
	}
}

// TestTheOverlapHoldoutCanActuallyFail is the fence on the fence. A holdout whose two renderings were
// accidentally identical, or whose ground truth was always the tiebreak winner, would be permanently
// green and would prove nothing.
func TestTheOverlapHoldoutCanActuallyFail(t *testing.T) {
	cases := holdoutCases()

	// 1. The two renderings must genuinely differ in span placement.
	seqT := renderTrace(cases[0], false)
	overT := renderTrace(cases[0], true)
	if seqT.Spans[1].StartTime.Equal(overT.Spans[1].StartTime) {
		t.Fatal("the sequential and overlapping renderings place spans identically; the holdout is " +
			"comparing a trace with itself")
	}
	if !overT.Spans[0].EndTime.After(overT.Spans[1].StartTime) {
		t.Fatal("the 'overlapping' rendering does not actually overlap: the first span ends before the " +
			"second begins, so this holdout measures two sequential runs")
	}

	// 2. The ground truth must not be predictable from the node id, or a localizer that reads nothing
	//    and returns the alphabetically-first node scores 100%.
	alphaWins, betaWins := 0, 0
	for _, c := range cases {
		if c.bad == c.nodeA {
			alphaWins++
		} else {
			betaWins++
		}
	}
	if alphaWins == 0 || betaWins == 0 {
		t.Fatalf("the guilty node is always %s; a localizer that returns a constant would score 100%%",
			map[bool]string{true: "n_alpha", false: "n_beta"}[betaWins == 0])
	}

	// 3. Both span orders must be exercised, or the holdout never sees the case where the guilty node
	//    ran second.
	aFirst, bFirst := 0, 0
	for _, c := range cases {
		if c.aFirst {
			aFirst++
		} else {
			bFirst++
		}
	}
	if aFirst == 0 || bFirst == 0 {
		t.Fatal("every case renders its spans in the same order; the holdout never exercises the case " +
			"where the guilty node started second")
	}

	// 4. And a BROKEN localizer must score badly on it — the direct demonstration that the metric
	//    discriminates. "First node in start-time order" is the naive localizer this whole file exists
	//    to test attribution is not.
	truth := holdoutTruth()
	naiveCorrect := 0
	for _, c := range cases {
		tr := renderTrace(c, true)
		first := attrString(tr.Spans[0].Attributes, telemetry.AttrNodeID)
		if first == truth[c.caseID] {
			naiveCorrect++
		}
	}
	if naiveCorrect == len(cases) {
		t.Fatalf("a localizer that blindly names the first span scores %d/%d on this holdout; the set "+
			"cannot distinguish reading outputs from reading the clock", naiveCorrect, len(cases))
	}
	t.Logf("holdout discrimination: a naive first-span localizer scores %d/%d (%.1f%%)",
		naiveCorrect, len(cases), 100*float64(naiveCorrect)/float64(len(cases)))
}

// TestBothNodesDivergingIsWhereOrderActuallyDecides is the arm that can catch the real defect, and the
// first version of this holdout could not.
//
// 🔴 With exactly ONE contract-violating node per case, the walk order is irrelevant: whatever sequence
// `firstDivergenceOrdered` visits in, it finds the one guilty node and returns it. A holdout built only
// that way reports 100% under overlap and proves nothing about interleaving — which is what the arm
// above did until this was added.
//
// The shape where order genuinely decides is BOTH concurrent nodes violating their contracts. Then
// "first divergence" is a statement about sequence, and under concurrency the sequence is partly the
// machine's choice. This asserts the honest consequence: the answer must be a function of the SPEC's
// declared order, not of which goroutine happened to start first.
func TestBothNodesDivergingIsWhereOrderActuallyDecides(t *testing.T) {
	ir := holdoutIR()
	v := Variant{VariantID: "v1", ConfigHash: "c1", EvalSetHash: "e1", WorkflowID: "wf"}

	// One case, rendered four ways: {alpha starts first, beta starts first} × {sequential, overlapping}.
	// Both nodes violate their contract in every rendering.
	build := func(aFirst, overlapping bool) FailingCase {
		c := overlapCase{caseID: "both-bad", nodeA: "n_alpha", nodeB: "n_beta", bad: "", aFirst: aFirst}
		tr := renderTrace(c, overlapping)
		tr.NodeOutputs = map[string]json.RawMessage{
			"n_alpha": json.RawMessage(`{}`),
			"n_beta":  json.RawMessage(`{}`),
		}
		return FailingCase{
			Case:  evalharness.Case{CaseID: "both-bad", WorkflowID: "wf-holdout", Label: evalharness.LabelNone},
			Trace: tr,
		}
	}
	// The SPEC's declared order. P34 design D4 keeps it as a linear sequence containing every node
	// precisely so a replay has one, and it is what attribution walks when the caller can supply it.
	declared := []string{"n_alpha", "n_beta"}

	// Two localizers: the pre-fix path (no declared order — start time decides) and the post-fix one.
	byClock := func(fc FailingCase) string {
		got := Attribute(ir, v, []FailingCase{fc})
		if len(got.Cases) != 1 {
			t.Fatalf("want one case attribution, got %d", len(got.Cases))
		}
		if !got.OverlappingSpans && spansOverlap(fc.Trace) {
			t.Error("attribution did not notice that the spans overlapped; a consumer rendering a " +
				"first-divergence node would present a scheduling artifact as a finding with nothing to " +
				"warn them")
		}
		return got.Cases[0].FirstDivergenceNode
	}
	byDeclaration := func(fc FailingCase) string {
		got := AttributeWithOrder(ir, v, []FailingCase{fc}, linkage.Topology{}, declared)
		if len(got.Cases) != 1 {
			t.Fatalf("want one case attribution, got %d", len(got.Cases))
		}
		if !got.OrderedByDeclaration {
			t.Error("attribution did not record that the declared order constrained the walk")
		}
		return got.Cases[0].FirstDivergenceNode
	}

	seqA := byClock(build(true, false))
	seqB := byClock(build(false, false))
	clockOverA := byClock(build(true, true))
	clockOverB := byClock(build(false, true))
	overA := byDeclaration(build(true, true))
	overB := byDeclaration(build(false, true))

	t.Logf("P34 §6.4 · both-nodes-diverging arm")
	t.Logf("  sequential, by clock       : alpha-first → %s | beta-first → %s", seqA, seqB)
	t.Logf("  overlapping, by clock      : alpha-first → %s | beta-first → %s  (the defect)", clockOverA, clockOverB)
	t.Logf("  overlapping, by declaration: alpha-first → %s | beta-first → %s  (the fix)", overA, overB)

	// Sequentially, the answer legitimately depends on which ran first — that IS the sequence, recorded.
	if seqA == seqB {
		t.Fatalf("sequential attribution gave %q for both span orders; if start time did not decide here, "+
			"this arm is not measuring what it claims to", seqA)
	}

	// 🔴 The defect, asserted as still real on the clock path. If this stopped being true the fix below
	// would be unfalsifiable — a test that passes because the problem it guards vanished for some other
	// reason is a test nobody can trust the next time it goes green.
	if clockOverA == clockOverB {
		t.Fatalf("ordering overlapping spans by START TIME gave %q both ways. That is the behaviour this "+
			"holdout was built to catch as WRONG; if it no longer reproduces, the comparison below no "+
			"longer demonstrates anything", clockOverA)
	}

	// 🔴 Under OVERLAP, both localizations must agree with each other, because there is no sequence to
	// read: the two spans are concurrent, so "which started first" is a nanosecond of scheduling noise.
	// An attribution that answers differently is reading the machine's choice as evidence about the
	// customer's code.
	if overA != overB {
		t.Fatalf("attribution under OVERLAPPING spans, ordered by the DECLARATION, localized to %q when "+
			"alpha's span started first and "+
			"%q when beta's did — the same two nodes, the same two contract violations, the same durations, "+
			"differing only by a nanosecond of scheduling.\n"+
			"This is the degradation design.md flagged and PRD §9.5 requires proved absent BEFORE "+
			"concurrency ships. Attribution must order overlapping nodes by the SPEC's declared order (P34 "+
			"design D4 keeps `order` precisely so a replay has one), never by span start time.",
			overA, overB)
	}
}

// TestTopologyStillConstrainsOrderUnderOverlap — the second mechanism, asserted separately.
//
// When the caller supplies a recovered topology, attribution orders by it rather than by start time.
// That path is what makes localization robust to interleaving in the first place, and it must keep
// working when the spans overlap — otherwise the graph axis would have removed the one signal that
// survives concurrency.
func TestTopologyStillConstrainsOrderUnderOverlap(t *testing.T) {
	ir := holdoutIR()
	v := Variant{VariantID: "v1", ConfigHash: "c1", EvalSetHash: "e1", WorkflowID: "wf"}
	topo := linkage.Topology{Edges: []linkage.Edge{
		{From: "n_alpha", To: "n_beta", Provenance: linkage.ProvFramework},
	}}
	got := AttributeWithTopology(ir, v, holdoutFailingCases(true), topo)
	if !got.OrderedByTopology {
		t.Fatal("attribution did not use the supplied topology under overlapping spans; it fell back to " +
			"start-time order, which under concurrency is partly the machine's choice rather than evidence")
	}
	truth := holdoutTruth()
	ok, n := accuracyOf(got, truth)
	if ok != n {
		t.Fatalf("topology-ordered attribution localizes %d/%d under overlap", ok, n)
	}
}
