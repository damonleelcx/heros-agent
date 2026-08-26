package improvementrun

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/verification"
)

// verify_test.go proves §3.1 and §3.2: the operator catalog is `internal/proposal`'s, unchanged, and
// every candidate is applied in an isolated worktree and scored multi-seed with intervals.

// TestP35AddsNoOperator turns FR4 from a comment into a build failure.
//
// The pressure it resists is entirely predictable: a phase that opens pull requests will be asked for
// "one small operator" that its own surface needs, and the operator will be written where the surface
// is rather than in the catalog — which puts a change generator outside the catalog every other caller
// reads, and outside the admissibility checks that catalog carries.
func TestP35AddsNoOperator(t *testing.T) {
	got, want := Operators(), proposal.DefaultCatalog()
	if len(got) != len(want) {
		t.Fatalf("this package publishes %d operators and internal/proposal publishes %d. P35 adds none: "+
			"a generator outside the catalog is a generator outside its admissibility checks",
			len(got), len(want))
	}
	for i := range got {
		if reflect.TypeOf(got[i]) != reflect.TypeOf(want[i]) {
			t.Fatalf("operator %d is %T here and %T in the catalog", i, got[i], want[i])
		}
	}
}

// ── 3.2 isolated worktree, multi-seed, intervals ─────────────────────────────────────────────────

type stubContract struct{}

func (stubContract) Check(optimizer.SearchCandidate) (bool, string) { return true, "" }

type stubBuild struct{}

func (stubBuild) Build(context.Context, optimizer.SearchCandidate) (bool, string) { return true, "" }

type stubRunner struct{}

func (stubRunner) Run(context.Context, verification.RunRequest) (verification.RunResult, error) {
	return verification.RunResult{}, nil
}

type stubScorer struct{}

func (stubScorer) Composite(context.Context, optimizer.VerifyRequest, float64, float64) (evalstats.Interval, evalstats.Interval, []string, float64, float64, error) {
	return evalstats.Interval{}, evalstats.Interval{}, nil, 0, 0, nil
}

func fullDeps() VerifierDeps {
	return VerifierDeps{
		Contract: stubContract{}, Build: stubBuild{}, Runner: stubRunner{}, Scorer: stubScorer{},
		Isolation: IsolationWorktree,
	}
}

// TestEveryCandidateIsAppliedInAnIsolatedWorktree asserts the property ADR-001 rests on, at the only
// point it is observable: assembly. A build gate that applied in place produces identical verdicts,
// identical logs and identical timings — so the isolation must be DECLARED and checked, never inferred.
func TestEveryCandidateIsAppliedInAnIsolatedWorktree(t *testing.T) {
	d := fullDeps()
	d.Isolation = IsolationNone
	if _, err := NewVerifier(d); err == nil {
		t.Fatal("a verifier was assembled over a build gate that declares no isolation. An in-place " +
			"apply is indistinguishable from an isolated one at this layer, and it has modified a " +
			"customer's tree")
	}
	d.Isolation = ""
	if _, err := NewVerifier(d); err == nil {
		t.Fatal("an UNDECLARED isolation was accepted. An undeclared isolation is not the same as an " +
			"isolated one, and the zero value must not be the safe one")
	}
	if _, err := NewVerifier(fullDeps()); err != nil {
		t.Fatalf("the shipped assembly was refused: %v", err)
	}
}

// TestANilGateIsRefusedByName is the fence for the phase's central risk in its smallest form: a
// `ComposedVerifier` with a nil `Contract` compiles, runs, and admits every candidate.
func TestANilGateIsRefusedByName(t *testing.T) {
	cases := map[string]func(*VerifierDeps){
		"typed-contract": func(d *VerifierDeps) { d.Contract = nil },
		"build gate":     func(d *VerifierDeps) { d.Build = nil },
		"eval runner":    func(d *VerifierDeps) { d.Runner = nil },
		"composite":      func(d *VerifierDeps) { d.Scorer = nil },
	}
	for name, drop := range cases {
		d := fullDeps()
		drop(&d)
		_, err := NewVerifier(d)
		if err == nil {
			t.Fatalf("a verifier assembled with no %s. A nil collaborator here is a gate that is not "+
				"called, and a run past an uncalled gate looks exactly like a run that passed it", name)
		}
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("dropping the %s produced a refusal that does not name it: %v", name, err)
		}
	}
}

// TestTheVerifierIsTheShippedOne asserts P35 returns `optimizer.ComposedVerifier` rather than a wrapper.
// A wrapper is a place to add a step later, and the step somebody adds later is the one that runs
// after the gate.
func TestTheVerifierIsTheShippedOne(t *testing.T) {
	v, err := NewVerifier(fullDeps())
	if err != nil {
		t.Fatal(err)
	}
	var _ optimizer.Verifier = v
	if reflect.TypeOf(v) != reflect.TypeOf(&optimizer.ComposedVerifier{}) {
		t.Fatalf("the verifier is %T, not *optimizer.ComposedVerifier", v)
	}
}

func TestMeasurementIsMultiSeedAndIsThePlatformsOwnConfiguration(t *testing.T) {
	p, _ := Translate("fix it", okBounds())
	cfg := VerificationConfig(p)
	if !MultiSeed(cfg) {
		t.Fatalf("verification runs on %d seed(s). A single-seed measurement produces an interval of "+
			"width zero, which renders as certainty", len(cfg.Seeds))
	}
	if !reflect.DeepEqual(cfg.Seeds, verification.DefaultConfig().Seeds) {
		t.Fatalf("this phase measures on seeds %v while the platform's default is %v. Two seed sets is "+
			"two experiments, and every delta produced under one is incomparable with every delta "+
			"beside it", cfg.Seeds, verification.DefaultConfig().Seeds)
	}
}

// TestAnUnconfiguredBuildGateRejectsRatherThanAdmits is the fail-closed direction on the build side.
func TestAnUnconfiguredBuildGateRejectsRatherThanAdmits(t *testing.T) {
	ok, log := WorktreeBuildGate{}.Build(context.Background(), optimizer.SearchCandidate{})
	if ok {
		t.Fatal("an unconfigured build gate reported a successful build, admitting an unbuilt candidate " +
			"to verification on the strength of a misconfiguration")
	}
	if log == "" {
		t.Fatal("the rejection carries no log, so a reviewer sees \"did not build\" with nothing behind it")
	}
}
