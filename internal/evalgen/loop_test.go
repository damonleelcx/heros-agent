package evalgen

import (
	"context"
	"errors"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalharness"
)

// answerableTargets builds the set of obligations the scripted model can actually produce a case
// for. Everything about the fixture workflow is reachable except what a test deliberately excludes.
func answerableTargets(extra ...string) map[string]bool {
	m := map[string]bool{
		EdgeID("router", "branch_a"):        true,
		EdgeID("router", "branch_b"):        true,
		EdgeID("branch_a", "reflect"):       true,
		EdgeID("branch_b", "reflect"):       true,
		EdgeID("reflect", "reflect"):        true,
		BranchID("router", "branch_a"):      true,
		BranchID("router", "branch_b"):      true,
		LoopBoundID("reflect", LoopMin):     true,
		LoopBoundID("reflect", LoopTypical): true,
		LoopBoundID("reflect", LoopMax):     true,
		"router":                            true,
		"branch_a":                          true,
		"branch_b":                          true,
		"reflect":                           true,
	}
	for _, e := range extra {
		m[e] = true
	}
	return m
}

// Task 4.3 / 4.4 / 8.5 — the loop iterates until MEASURED path coverage reaches the threshold.
func TestLoopIteratesUntilPathCoverageThresholdIsMet(t *testing.T) {
	ir := fxRouterIR()
	model := &scriptedCaseModel{answerable: answerableTargets()}
	cfg := DefaultLoopConfig(model)
	p := newSimProber("branch_a", "branch_b")

	seed := []evalharness.Case{handCase("hand-1", "branch_a", "hello", 1)}
	res, err := Fill(context.Background(), ir, seed, p, cfg)
	if err != nil {
		t.Fatalf("Fill: %v", err)
	}

	// Print the process, so the conclusion is derivable rather than asserted.
	for _, r := range res.Rounds {
		t.Logf("round %d: gap=[%s] produced=%v skipped=%v deduped=%d -> %s",
			r.Iteration, r.GapBefore.String(), r.Produced, r.Skipped, r.Deduped, r.After.Summary())
	}
	t.Logf("stopped: %s", res.StoppedBecause)

	if !res.Report.Path.Met() {
		t.Fatalf("path coverage %v did not reach the threshold %v; uncovered: %s",
			res.Report.Path.Achieved, res.Report.Path.Target, joined(res.Report.Path.Uncovered()))
	}
	if len(res.Rounds) == 0 {
		t.Fatal("the loop must actually iterate, not return the seed set unchanged")
	}
	if len(res.Report.Residual) != 0 {
		t.Fatalf("a met report must have no residual, got %s", joined(res.Report.Residual))
	}
}

// Task 4.4 / 8.5 — an unreachable path terminates the loop at the bound and is REPORTED as residual.
// No false 100%.
func TestUnreachablePathTerminatesWithAReportedResidual(t *testing.T) {
	ir := fxUnreachableIR()
	// The model cannot produce a case for the dead branch, and the simulated router never selects it.
	model := &scriptedCaseModel{answerable: answerableTargets()}
	cfg := DefaultLoopConfig(model)
	cfg.MaxIterations = 3
	p := newSimProber("branch_a", "branch_b")

	seed := []evalharness.Case{handCase("hand-1", "branch_a", "hello", 1)}
	res, err := Fill(context.Background(), ir, seed, p, cfg)
	if err != nil {
		t.Fatalf("Fill: %v", err)
	}
	for _, r := range res.Rounds {
		t.Logf("round %d: gap=[%s] produced=%v -> %s", r.Iteration, r.GapBefore.String(), r.Produced, r.After.Summary())
	}
	t.Logf("stopped: %s", res.StoppedBecause)

	if res.Report.Met() {
		t.Fatal("coverage must NOT be reported as met when a branch is unreachable")
	}
	if res.Report.Path.Achieved >= 1 {
		t.Fatalf("path coverage must be below 100%%, got %v", res.Report.Path.Achieved)
	}
	if !containsAll(res.Report.Residual, EdgeID("router", "branch_dead")) {
		t.Fatalf("the unreachable edge must be named in the residual, got %s", joined(res.Report.Residual))
	}
	if !containsAll(res.Report.Residual, "branch_dead") {
		t.Fatalf("the unreachable node must be named in the residual, got %s", joined(res.Report.Residual))
	}
	if res.StoppedBecause == "" {
		t.Fatal("the loop must say why it stopped")
	}
	// The unreachable obligations stay in the DENOMINATOR: dropping them would raise the achieved
	// percentage by deleting the evidence of failure.
	found := false
	for _, it := range res.Report.Path.Items {
		if it.ID == EdgeID("router", "branch_dead") {
			found = true
			if it.Covered || !it.Unreachable {
				t.Fatalf("the dead edge must be present, uncovered and flagged unreachable: %+v", it)
			}
		}
	}
	if !found {
		t.Fatal("the unreachable obligation was dropped from the report; that is the false-100% failure")
	}
}

// Task 4.2 — the LLM layer is handed the SPECIFIC uncovered obligations, not the whole space.
func TestLLMGeneratorIsPointedAtTheResidual(t *testing.T) {
	ir := fxRouterIR()
	model := &scriptedCaseModel{answerable: answerableTargets()}
	p := newSimProber("branch_a", "branch_b")

	// Seed covers branch_a only; branch_b is the residual after the cheap layers.
	seed := []evalharness.Case{handCase("hand-1", "branch_a", "hello", 1)}
	ev, _ := ProbeAll(context.Background(), p, ir, seed)
	rep := Measure(ir, seed, ev, DefaultThresholds())
	gap := GapOf(rep)

	g := &LLMGenerator{Model: model}
	produced, err := g.Generate(context.Background(), ir, gap, seed)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("want one request, got %d", len(model.requests))
	}
	req := model.requests[0]
	if !containsAll(req.Targets, EdgeID("router", "branch_b")) {
		t.Fatalf("the uncovered branch must be an explicit target, got %s", joined(req.Targets))
	}
	if !containsAll([]string{req.Prompt}, req.Prompt) || len(req.Prompt) == 0 {
		t.Fatal("the request must carry a rendered prompt")
	}
	if len(produced) == 0 {
		t.Fatal("the targeted generator produced nothing")
	}
	// And the produced case actually forces execution down that branch.
	forced := false
	for _, c := range produced {
		e, err := p.Probe(context.Background(), ir, c)
		if err != nil {
			t.Fatalf("probe: %v", err)
		}
		if containsAll(e.Edges, EdgeID("router", "branch_b")) {
			forced = true
		}
	}
	if !forced {
		t.Fatal("no generated case actually reached the targeted branch")
	}
}

// The seed-from-real-traces layer is present as an interface and reports itself INACTIVE rather than
// silently producing nothing — P5 activates it by supplying a source.
func TestSeedTraceGeneratorIsPresentButInactive(t *testing.T) {
	g := &SeedTraceGenerator{}
	_, err := g.Generate(context.Background(), fxRouterIR(), Gap{}, nil)
	if !errors.Is(err, ErrGeneratorInactive) {
		t.Fatalf("want ErrGeneratorInactive, got %v", err)
	}

	// The loop records the skip rather than treating it as a failure.
	model := &scriptedCaseModel{answerable: answerableTargets()}
	cfg := DefaultLoopConfig(model)
	res, err := Fill(context.Background(), fxRouterIR(),
		[]evalharness.Case{handCase("hand-1", "branch_a", "hello", 1)}, newSimProber("branch_a", "branch_b"), cfg)
	if err != nil {
		t.Fatalf("Fill: %v", err)
	}
	if len(res.Rounds) == 0 {
		t.Fatal("expected at least one round")
	}
	if _, ok := res.Rounds[0].Skipped["seed_from_real_traces"]; !ok {
		t.Fatalf("the inactive layer must be recorded as skipped, got %v", res.Rounds[0].Skipped)
	}
}

// Fill refuses to run without a Prober: coverage measured from declared intent is not coverage.
func TestFillRequiresAProber(t *testing.T) {
	_, err := Fill(context.Background(), fxRouterIR(), nil, nil, DefaultLoopConfig(nil))
	if err == nil {
		t.Fatal("Fill without a prober must be refused")
	}
}
