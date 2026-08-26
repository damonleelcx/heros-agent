package improvementrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/proposal"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/verification"
	"github.com/heros-foreal/agentd/internal/worktree"
)

// verify.go is FR4/FR5/FR6 and tasks 3.1–3.3: candidates come from `internal/proposal` UNCHANGED, each
// is applied in an ISOLATED WORKTREE, and each is scored by the eval harness UNCHANGED — multi-seed,
// with intervals.
//
// # 🚫 What this file is not allowed to be
//
// It is not a verifier. `optimizer.ComposedVerifier` runs the shipped pipeline — typed contract → build
// gate → held-out verification → composite — and P35 uses that object, not a re-assembly of its parts.
// The one thing this file does is CONSTRUCT it, and constructing it is where the mistakes live: a
// `ComposedVerifier` with a nil `Contract` compiles, runs, and admits every candidate.
//
// So `NewVerifier` refuses a nil collaborator by name. That is the whole of task 3.3's guarantee on
// this side: the ordering is the shipped verifier's, and the only way to lose it from here is to hand
// it nothing to order.

// ErrNoOperatorAdded is the assertion task 3.1 makes into a value: P35 adds no change operator.
//
// It exists so the claim is checkable rather than a comment. `Operators()` returns the catalog
// `internal/proposal` publishes, and `TestP35AddsNoOperator` compares it against
// `proposal.DefaultCatalog()` — so an operator added here, in a future phase, under delivery pressure,
// is a red build rather than a paragraph nobody re-reads.
var ErrNoOperatorAdded = errors.New(
	"improvementrun: this phase adds no change operator; candidate generation is internal/proposal's, unchanged")

// Operators returns the catalog this package proposes from. 🔴 It IS `proposal.DefaultCatalog()`,
// returned rather than filtered, wrapped or extended.
func Operators() []proposal.Operator { return proposal.DefaultCatalog() }

// VerifierDeps are the shipped collaborators the composed verifier needs. Every one is REQUIRED.
//
// 🔴 A struct of required dependencies rather than optional fields with sensible fallbacks, and the
// difference is the phase's whole risk: a fallback for `Contract` is a path where the typed-contract
// gate is not called, and nothing about a run down that path looks different.
type VerifierDeps struct {
	// Contract is the P5 typed I/O check. It runs FIRST and short-circuits, so a contract-violating
	// candidate never costs a provider call and never produces a verdict row.
	Contract optimizer.ContractChecker
	// Build is the P5.5 build gate. In production it is backed by `worktree.Applier`, which checks out
	// an ISOLATED worktree from the bare clone — never the customer's tree (ADR-001). `Isolation`
	// below is how that is asserted rather than assumed.
	Build optimizer.BuildGate
	// Runner is the eval harness. UNCHANGED: P35 supplies no scoring, no metric and no oracle.
	Runner verification.EvalRunner
	// Scorer is the P4 composite pipeline.
	Scorer optimizer.CompositeScorer
	// Isolation names how the build gate isolates. It is DATA rather than a comment so a fence can read
	// it — see `Isolation` and `TestEveryCandidateIsAppliedInAnIsolatedWorktree`.
	Isolation Isolation
}

// Isolation names where a candidate is applied.
//
// # Why this is a declared value and not an implementation detail
//
// ADR-001's founding property is that the code under measurement is the code that would ship, applied
// on an isolated branch, never in place. A build gate that applied in place would produce identical
// verdicts, identical logs and identical timings — and would have modified a customer's tree. There is
// no observable difference at this layer, which is exactly why the property has to be DECLARED by
// whoever builds the gate and asserted by a fence, rather than inferred.
type Isolation string

const (
	// IsolationWorktree is the shipped path: `worktree.Applier` checks out from the bare clone at the
	// patch's `source_revision`, applies the codemod, commits on a variant branch, and verifies there.
	IsolationWorktree Isolation = "isolated_worktree"
	// IsolationNone is what a test double declares. 🔴 It is a value rather than the zero value, so a
	// caller that forgot to declare isolation is refused rather than silently treated as isolated.
	IsolationNone Isolation = "none"
)

// Valid reports membership.
func (i Isolation) Valid() bool { return i == IsolationWorktree || i == IsolationNone }

// Isolated reports whether this isolation satisfies FR5.
func (i Isolation) Isolated() bool { return i == IsolationWorktree }

// NewVerifier assembles the SHIPPED verifier from the shipped parts, refusing any missing one by name.
//
// 🚫 It returns `*optimizer.ComposedVerifier` rather than a P35 type. A P35 wrapper would be a place to
// add a step later, and the step somebody adds later is the one that runs after the gate.
func NewVerifier(d VerifierDeps) (*optimizer.ComposedVerifier, error) {
	var missing []string
	if d.Contract == nil {
		missing = append(missing, "the typed-contract check (without it, a contract-violating candidate is verified and surfaced)")
	}
	if d.Build == nil {
		missing = append(missing, "the build gate (without it, a candidate that does not compile is measured)")
	}
	if d.Runner == nil {
		missing = append(missing, "the eval runner (without it, nothing is measured at all)")
	}
	if d.Scorer == nil {
		missing = append(missing, "the composite scorer (without it, no objective exists to gate against)")
	}
	if !d.Isolation.Valid() {
		missing = append(missing, "a declared isolation (an undeclared one is not the same as an isolated one)")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("improvementrun: the verifier cannot be assembled — it is missing %v. "+
			"None of these has a safe default: a nil collaborator here is a gate that is not called, "+
			"and a run past an uncalled gate looks exactly like a run that passed it", missing)
	}
	if !d.Isolation.Isolated() {
		return nil, fmt.Errorf("improvementrun: this build gate declares isolation %q. FR5 requires each "+
			"candidate to be applied in an isolated worktree, and an in-place apply produces identical "+
			"verdicts, identical logs and identical timings while having modified a customer's tree",
			d.Isolation)
	}
	return &optimizer.ComposedVerifier{
		Contract: d.Contract, Build: d.Build, Runner: d.Runner, Scorer: d.Scorer,
	}, nil
}

// VerificationConfig is the harness configuration a run measures under.
//
// 🔴 It IS `verification.DefaultConfig()` with the run's latency SLA applied, which is what
// `optimizer.verifyConfigFor` already does for every other caller. P35 declares no seeds of its own:
// a phase that picked its own seed set would be measuring on a different experiment than the rest of
// the platform, and every delta it produced would be incomparable with every delta beside it.
func VerificationConfig(p Plan) verification.Config {
	cfg := verification.DefaultConfig()
	_ = p // the plan carries no measurement knob today, and that is the point — see the doc comment.
	return cfg
}

// MultiSeed reports whether a configuration measures on more than one seed. Read by the fence rather
// than asserted in prose: a single-seed measurement produces an interval of width zero, which renders
// as certainty.
func MultiSeed(cfg verification.Config) bool { return len(cfg.Seeds) > 1 }

// WorktreeBuildGate adapts `worktree.Applier` to `optimizer.BuildGate`.
//
// It is the seam that makes `IsolationWorktree` true rather than claimed. A deployment builds one of
// these; a test builds a double and declares `IsolationNone`, which `NewVerifier` refuses — so a test
// double cannot reach the production assembly path by accident.
type WorktreeBuildGate struct {
	Applier *worktree.Applier
	// Patch resolves a candidate to the transform patch the applier applies. Supplied by the caller
	// because compiling a candidate into a patch is `internal/hostedcompile`'s job, not this package's.
	Patch func(ctx context.Context, cand optimizer.SearchCandidate) (*transform.Patch, error)
}

// Build implements optimizer.BuildGate.
func (g WorktreeBuildGate) Build(ctx context.Context, cand optimizer.SearchCandidate) (bool, string) {
	if g.Applier == nil || g.Patch == nil {
		// 🚫 `false`, not `true`. A build gate that cannot run reports NOT BUILT, so the candidate is
		// rejected. Reporting `true` would admit an unbuilt candidate to verification on the strength of
		// a misconfiguration.
		return false, "improvementrun: the build gate is not configured, so nothing was built and " +
			"nothing is admitted"
	}
	patch, err := g.Patch(ctx, cand)
	if err != nil {
		return false, "improvementrun: this candidate could not be compiled into a patch: " + err.Error()
	}
	applied, err := g.Applier.Apply(ctx, patch)
	if err != nil {
		// 🔴 A build rejection is a REPORTED state, not a swallowed one: `Apply` returns BOTH the error
		// and an `Applied` describing the rejection, and the log is what tells a reviewer which node
		// the compiler blamed. Dropping it would render "did not build" with nothing behind it.
		log := err.Error()
		if applied != nil && applied.BuildLog != "" {
			log = applied.BuildLog
		}
		return false, "improvementrun: the isolated worktree build rejected this candidate: " + log
	}
	return applied.Status == worktree.StatusBuilt, applied.BuildLog
}

var _ optimizer.BuildGate = WorktreeBuildGate{}
