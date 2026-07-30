package changedelivery

import (
	"strings"
	"testing"
)

// p17_memory_test.go — the memory axis's delivery cells (P17 §14, FR34–FR39).
//
// # This is the axis that made the cross-axis contract necessary
//
// A memory strategy is modelled, resolved, hashed, proposed — and then refused at transform in both
// engines, in every language. Before P13 13e that was the end of the sentence: no diff, so no pull
// request, so nothing, and a verified memory proposal produced a silence that looked exactly like a
// proposal nobody had gotten to.
//
// What this axis buys is therefore NOT a route. It is that a memory change now says "undeliverable, by
// both routes, for these two named reasons" — the difference between a product that refuses and a
// product that appears broken.

// TestMemoryReportsBothRefusalCausesSeparately — task 14.1.
func TestMemoryReportsBothRefusalCausesSeparately(t *testing.T) {
	src := SourceOutcome{
		Cause:     "unsafe-rewrite",
		Permanent: true,
		Note:      "refused at transform: node n_answer, dimension memory",
	}
	rep, err := BuildReport(ChangeMemoryStrategy, "go", true, src, RolloutStatus{}, false)
	if err != nil {
		t.Fatal(err)
	}

	srcOut, _ := rep.Outcome(RouteSource)
	rtOut, _ := rep.Outcome(RouteRuntime)

	if !srcOut.Refused() || !rtOut.Refused() {
		t.Fatalf("a memory change was offered a route: source=%v runtime=%v", srcOut.Status, rtOut.Status)
	}
	if srcOut.Cause != "unsafe-rewrite" {
		t.Fatalf("the source cause is not the transform's own: %q", srcOut.Cause)
	}
	if rtOut.Cause != string(CauseNotRuntimeResolvable) {
		t.Fatalf("the runtime cause is %q, want %q", rtOut.Cause, CauseNotRuntimeResolvable)
	}
	// 🔴 Two distinct, separately readable causes. Neither is inferred from the other, and a surface can
	// render both without deciding which one "really" explains it.
	if srcOut.Cause == rtOut.Cause {
		t.Fatal("both routes report the same cause; they refuse for different reasons")
	}
	if !strings.Contains(rtOut.Note, "STORE") {
		t.Fatalf("the runtime refusal does not name the absent store: %q", rtOut.Note)
	}
}

// TestMemoryReadsAsUndeliverableNotPending — task 14.1.
func TestMemoryReadsAsUndeliverableNotPending(t *testing.T) {
	src := SourceOutcome{Cause: "unsafe-rewrite", Permanent: true}
	rep, err := BuildReport(ChangeMemoryStrategy, "go", true, src, RolloutStatus{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateUndeliverable {
		t.Fatalf("state %q, want %q", rep.State, StateUndeliverable)
	}
	// 🚫 The states a dead end used to borrow.
	for _, bad := range []State{StateSourcePending, StateRolloutActive, StateDelivered} {
		if rep.State == bad {
			t.Fatalf("an undeliverable memory change reads as %q", bad)
		}
	}
	if rep.RemainingStep != "" {
		t.Fatalf("an undeliverable change names a remaining step, which reads as a queue position: %q", rep.RemainingStep)
	}
}

// TestMemoryRefusalIsContingentAndUndated — task 14.2.
//
// 🔴 The distinction that decides whether a reader should ever ask again. Wiring is a property of
// compiled code and will not move. Memory refuses because a runtime component does not exist — and one
// could. Rendering the two identically tells someone to stop asking about something that is merely
// unbuilt, or to keep waiting on something that cannot be built.
func TestMemoryRefusalIsContingentAndUndated(t *testing.T) {
	memory, err := RuntimeEligibility(ChangeMemoryStrategy, true)
	if err != nil {
		t.Fatal(err)
	}
	wiring, err := RuntimeEligibility(ChangeWiring, true)
	if err != nil {
		t.Fatal(err)
	}

	// Both carry the same cause — accurately, because neither is data today.
	if memory.Cause != CauseNotRuntimeResolvable || wiring.Cause != CauseNotRuntimeResolvable {
		t.Fatalf("fixture drifted: memory=%q wiring=%q", memory.Cause, wiring.Cause)
	}
	// …and they are still distinguishable, by data rather than by prose.
	if !memory.Contingent {
		t.Fatal("the memory refusal is not marked contingent; it is indistinguishable from a boundary")
	}
	if wiring.Contingent {
		t.Fatal("the wiring boundary is marked contingent; order is compiled and that will not change")
	}
	if memory.MissingComponent == "" {
		t.Fatal("a contingent refusal names no missing component")
	}
	if wiring.MissingComponent != "" {
		t.Fatalf("a permanent boundary names missing component %q", wiring.MissingComponent)
	}

	// 🚫 Contingent does NOT mean scheduled. Naming a missing component is not a promise to build it.
	blob := strings.ToLower(memory.MissingComponent + " " + memory.Note)
	for _, banned := range []string{"q1", "q2", "q3", "q4", "coming soon", "roadmap", "will land", "by the end of", "next release", "planned for"} {
		if strings.Contains(blob, banned) {
			t.Fatalf("the memory refusal carries a commitment (%q): %s", banned, blob)
		}
	}
	// It also must not name a binding-document ARTIFACT, which is the other axis's vocabulary and would
	// send the reader to the wrong backlog.
	if memory.MissingArtifact != "" {
		t.Fatalf("the memory cell names a document artifact %q; its gap is a runtime component", memory.MissingArtifact)
	}
}

// TestIdentityStrategyNeedsNoRoute — task 14.3.
func TestIdentityStrategyNeedsNoRoute(t *testing.T) {
	src := SourceOutcome{Cause: "unsafe-rewrite", Permanent: true}
	rep, err := BuildReport(ChangeMemoryStrategy, "go", true, src, RolloutStatus{}, true /* identity */)
	if err != nil {
		t.Fatal(err)
	}
	if rep.State != StateNothingToDeliver {
		t.Fatalf("state %q, want %q", rep.State, StateNothingToDeliver)
	}
	// 🔴 A third value on purpose: reporting `delivered` would count a no-op as a delivery, and reporting
	// a refusal would alarm someone whose change is simply already true.
	if rep.State == StateDelivered || rep.State == StateUndeliverable {
		t.Fatalf("an identity change was reported as %q", rep.State)
	}
	if !rep.IdentityChange {
		t.Fatal("the identity flag was not carried onto the report")
	}
}

// TestMemoryRefusalTotalityCanaryCoversBothRoutes — task 14.4.
//
// A node constructed to make a memory strategy take effect by EITHER route must come back refused, and
// a sabotaged refusal on either route must turn the cell red.
func TestMemoryRefusalTotalityCanaryCoversBothRoutes(t *testing.T) {
	// ── the runtime route: authoring is refused with the transform's own typed cause
	req := AuthorRequest{
		Rollout: Rollout{
			ID: "ro_mem", ParentConfigHash: "p", CandidateConfigHash: "c",
			Change: ChangeMemoryStrategy, ShareBasisPoints: 500, ExpiresAtUnixMs: testExpires,
		},
		NodeIsBound:           true,
		Entitled:              true,
		Halt:                  HaltState{Readable: true},
		Guardrail:             GuardrailNotApplicable,
		TransformRefusalCause: "unsafeRewrite: node n_answer carries a memory strategy",
		CreatedAtUnixMs:       testNow,
	}
	err := AuthorRollout(req)
	if err == nil {
		t.Fatal("a memory strategy was authored as a rollout candidate; no document may carry one")
	}
	var refused *ErrRolloutRefused
	if !asRefusal(err, &refused) || refused.Cause != RefusedTransform {
		t.Fatalf("want %q, got %v", RefusedTransform, err)
	}
	// The SAME cause the transform returns — the two routes must not disagree about why.
	if !strings.Contains(refused.Detail, "unsafeRewrite") {
		t.Fatalf("the authoring refusal does not carry the transform's cause: %q", refused.Detail)
	}

	// …and even with the transform cause omitted, the cell's own eligibility still refuses. This is the
	// sabotage half: removing one guard must not open the path.
	sabotaged := req
	sabotaged.TransformRefusalCause = ""
	err = AuthorRollout(sabotaged)
	if err == nil {
		t.Fatal("removing the transform-refusal input opened a path for a memory strategy — the cell's own eligibility is not enforcing anything")
	}
	if !asRefusal(err, &refused) || refused.Cause != RefusedIneligible {
		t.Fatalf("want %q as the second line of defence, got %v", RefusedIneligible, err)
	}

	// ── the source route: no enumerated path delivers it
	rep, err := BuildReport(ChangeMemoryStrategy, "go", true,
		SourceOutcome{Cause: "unsafe-rewrite", Permanent: true}, RolloutStatus{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Routes() {
		o, _ := rep.Outcome(r)
		if !o.Refused() {
			t.Fatalf("route %s delivers a memory strategy", r)
		}
	}
}

// TestDeliveryReportDoesNotUpgradeARefusedProposal — task 14.5.
func TestDeliveryReportDoesNotUpgradeARefusedProposal(t *testing.T) {
	// Even with rollout evidence attached, a refused memory proposal stays refused-not-scored.
	for _, rollout := range []RolloutStatus{{}, {Completed: true}, {Reverted: true}} {
		rep, err := BuildReport(ChangeMemoryStrategy, "go", true,
			SourceOutcome{Cause: "unsafe-rewrite", Permanent: true}, rollout, false)
		if err != nil {
			t.Fatal(err)
		}
		if rep.State == StateDelivered {
			t.Fatalf("rollout %+v upgraded a refused memory proposal to delivered", rollout)
		}
		if rep.State != StateUndeliverable {
			t.Fatalf("rollout %+v produced state %q, want %q", rollout, rep.State, StateUndeliverable)
		}
	}
	// And rollout evidence is never a verified delta, so no memory win can be reported anywhere.
	if RolloutEvidenceIsVerifiedDelta() {
		t.Fatal("rollout evidence counts as a verified delta")
	}
}
