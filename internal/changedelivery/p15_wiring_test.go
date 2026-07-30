package changedelivery

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/transform"
)

// p15_wiring_test.go — the wiring axis's delivery cells (P15 §21, FR52–FR56).
//
// # Two properties, and the second is why this file is longer than the axis deserves
//
// Wiring is permanently outside the runtime route: order and concurrency are compiled program
// structure, and a document that could reorder statements in a built binary would be an interpreter we
// have no business shipping into a customer's process.
//
// 🔴 And this is the axis with a gate that REJECTS AT COMPILE — an incoherent ordering yields no
// runnable spec, therefore no codemod, no diff, no pull request. A second delivery route arriving beside
// a gate whose entire purpose is to produce *nothing* is exactly where someone reasons "the rewriter
// refused, so let us just roll it out instead." That reading would convert the strongest safety gate in
// the system into a speed bump, so it is asserted rather than assumed.

// TestWiringCellsAreBoundariesNotBacklog — task 21.1.
func TestWiringCellsAreBoundariesNotBacklog(t *testing.T) {
	got, err := RuntimeEligibility(ChangeWiring, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cause != CauseNotRuntimeResolvable {
		t.Fatalf("got %q, want %q", got.Cause, CauseNotRuntimeResolvable)
	}
	if !got.Cause.Permanent() {
		t.Fatal("the wiring cell is not marked permanent")
	}
	if got.MissingArtifact != "" {
		t.Fatalf("a boundary names artifact %q — this is precisely how 'never' becomes 'not yet'", got.MissingArtifact)
	}
	if got.Cause.Owner() != "nobody" {
		t.Fatalf("the wiring boundary is owned by %q; there is no one to do it", got.Cause.Owner())
	}
	low := strings.ToLower(got.Note)
	if !strings.Contains(low, "compiled") {
		t.Fatalf("the refusal does not say why it is permanent: %q", got.Note)
	}
	for _, banned := range []string{"not yet", "coming", "planned", "roadmap", "will land", "q1", "q2", "q3", "q4", "soon"} {
		if strings.Contains(low, banned) {
			t.Fatalf("the wiring boundary contains completion language %q: %s", banned, got.Note)
		}
	}

	// It is structurally distinguishable from a backlog cell — a surface can branch without reading prose.
	gap, _ := RuntimeEligibility(ChangeToolSet, true)
	if got.Cause.Permanent() == gap.Cause.Permanent() || got.Cause.Owner() == gap.Cause.Owner() {
		t.Fatal("the wiring boundary and a platform gap are not distinguishable by data")
	}
}

// TestBoundNodeDoesNotUnlockWiring — task 21.2.
func TestBoundNodeDoesNotUnlockWiring(t *testing.T) {
	bound, _ := RuntimeEligibility(ChangeWiring, true)
	inline, _ := RuntimeEligibility(ChangeWiring, false)
	if bound.Cause != inline.Cause {
		t.Fatalf("apply mode changed the wiring answer: bound=%q inline=%q", bound.Cause, inline.Cause)
	}
	if bound.Cause != CauseNotRuntimeResolvable {
		t.Fatalf("got %q, want the permanent boundary", bound.Cause)
	}
	// 🚫 And nothing in the refusal suggests a bound migration would help.
	if strings.Contains(strings.ToLower(bound.Note), "bound mode") &&
		!strings.Contains(strings.ToLower(bound.Note), "any apply mode") {
		t.Fatalf("the wiring refusal mentions bound mode in a way that could read as a suggestion: %q", bound.Note)
	}
}

// TestGateRejectedOrderingCannotBeRolledOut — task 21.3.
//
// 🔴 The rule the second route exists to be constrained by.
func TestGateRejectedOrderingCannotBeRolledOut(t *testing.T) {
	req := AuthorRequest{
		Rollout: Rollout{
			ID: "ro_wiring", ParentConfigHash: "p1", CandidateConfigHash: "c1",
			Change: ChangeWiring, ShareBasisPoints: 1000, ExpiresAtUnixMs: testExpires,
		},
		NodeIsBound:     true,
		Entitled:        true,
		Halt:            HaltState{Readable: true},
		Guardrail:       GuardrailNotApplicable,
		GateRejected:    true,
		GateCause:       "incoherent-ordering: n_score consumes a field n_extract does not produce",
		CreatedAtUnixMs: testNow,
	}
	err := AuthorRollout(req)
	if err == nil {
		t.Fatal("a gate-rejected ordering was authored as a rollout candidate")
	}
	var refused *ErrRolloutRefused
	if !asRefusal(err, &refused) {
		t.Fatalf("want *ErrRolloutRefused, got %T", err)
	}
	// The refusal names the GATE, not the delivery route — the reader must not conclude that a different
	// route would have worked.
	if refused.Cause != RefusedGate {
		t.Fatalf("cause %q, want %q", refused.Cause, RefusedGate)
	}
	if !strings.Contains(refused.Detail, "incoherent-ordering") {
		t.Fatalf("the refusal does not carry the gate's own cause: %q", refused.Detail)
	}
	if !strings.Contains(refused.Detail, "not an alternative route around a gate") {
		t.Fatalf("the refusal does not state the rule it enforces: %q", refused.Detail)
	}

	// 🔴 The gate check must come BEFORE eligibility. A gate-rejected change on an ELIGIBLE cell must
	// still be refused as gate-rejected — if the order were reversed, a gate-rejected model change would
	// sail through.
	eligible := req
	eligible.Rollout.Change = ChangeModelWithinProvider
	err = AuthorRollout(eligible)
	if err == nil {
		t.Fatal("a gate-rejected change on a rollout-eligible cell was authored")
	}
	if !asRefusal(err, &refused) || refused.Cause != RefusedGate {
		t.Fatalf("cause %v, want %q — the gate must outrank eligibility", err, RefusedGate)
	}
}

// TestNoDeliveryPathExistsForAGateRejectedChange — task 21.3.
func TestNoDeliveryPathExistsForAGateRejectedChange(t *testing.T) {
	src := SourceOutcome{GateRejected: true, Cause: "incoherent-ordering", Permanent: true,
		Note: "the coherence gate returned no runnable spec"}
	rep, err := BuildReport(ChangeWiring, "go", true, src, RolloutStatus{}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Enumerate the routes. There must be none that delivers.
	for _, r := range Routes() {
		o, ok := rep.Outcome(r)
		if !ok {
			t.Fatalf("route %s has no outcome", r)
		}
		if !o.Refused() {
			t.Fatalf("route %s delivers a gate-rejected ordering", r)
		}
	}
	if rep.State != StateUndeliverable {
		t.Fatalf("state %q, want %q", rep.State, StateUndeliverable)
	}
}

// TestRejectedTransformReportsBothRoutes — task 21.4.
func TestRejectedTransformReportsBothRoutes(t *testing.T) {
	src := SourceOutcome{Cause: "rejected_transform", Permanent: true,
		Note: "the reorder was rejected at transform"}
	rep, err := BuildReport(ChangeWiring, "go", true, src, RolloutStatus{}, false)
	if err != nil {
		t.Fatal(err)
	}
	srcOut, _ := rep.Outcome(RouteSource)
	rtOut, _ := rep.Outcome(RouteRuntime)
	if srcOut.Cause != "rejected_transform" {
		t.Fatalf("source cause %q, want the transform's own rejection", srcOut.Cause)
	}
	if rtOut.Cause != string(CauseNotRuntimeResolvable) {
		t.Fatalf("runtime cause %q, want %q", rtOut.Cause, CauseNotRuntimeResolvable)
	}
	if rep.State != StateUndeliverable {
		t.Fatalf("state %q, want %q", rep.State, StateUndeliverable)
	}
	// 🚫 Never pending, never in review, never in progress.
	for _, bad := range []State{StateSourcePending, StateRolloutActive, StateDelivered} {
		if rep.State == bad {
			t.Fatalf("a rejected transform reads as %q", bad)
		}
	}
}

// TestSourceRouteIsUnchangedForCoveredSwaps — task 21.5.
func TestSourceRouteIsUnchangedForCoveredSwaps(t *testing.T) {
	// Wiring materializes an adjacent transposition in the covered languages; the delivery layer must
	// not alter that answer.
	var covered string
	for _, lang := range transform.RegisteredLanguages() {
		if SourceOutcomeFor(ChangeWiring, lang, "an adjacent statement transposition").Materializes {
			covered = lang
			break
		}
	}
	if covered == "" {
		t.Skip("no language currently materializes an adjacent transposition")
	}

	src := SourceOutcomeFor(ChangeWiring, covered, "an adjacent statement transposition")
	rep, err := BuildReport(ChangeWiring, covered, true, src, RolloutStatus{}, false)
	if err != nil {
		t.Fatal(err)
	}
	srcOut, _ := rep.Outcome(RouteSource)
	if srcOut.Refused() {
		t.Fatalf("a covered wiring swap was refused by the source route: %+v", srcOut)
	}
	if rep.State != StateSourcePending {
		t.Fatalf("state %q, want %q — the pull request is the next step", rep.State, StateSourcePending)
	}
	// 🚫 The runtime route's refusal must NOT appear as a warning on a working source delivery. It is
	// reported as its own route's answer and nothing more.
	rtOut, _ := rep.Outcome(RouteRuntime)
	if !rtOut.Refused() {
		t.Fatal("the runtime route stopped refusing wiring")
	}
	if strings.Contains(strings.ToLower(rep.RemainingStep), "runtime") ||
		strings.Contains(strings.ToLower(rep.RemainingStep), "rollout") {
		t.Fatalf("the source delivery's next step mentions the runtime route: %q", rep.RemainingStep)
	}
}
