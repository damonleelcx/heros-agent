package changedelivery

import (
	"strings"
	"testing"
)

// p16_context_test.go — the context axis's delivery cells (P16 §11, FR52–FR57).
//
// "Context strategy" is one name for two facts that land in different columns, and the split is not
// where a reader expects it: a RETRIEVAL PARAMETER is a number the binding document was built to carry,
// while a SELECTION POLICY is a DELETION of written turns that no document performs in built code.

// TestRetrievalAndPolicyCarryDifferentCauses — task 11.1.
func TestRetrievalAndPolicyCarryDifferentCauses(t *testing.T) {
	retrieval, err := RuntimeEligibility(ChangeRetrievalParams, true)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := RuntimeEligibility(ChangeSelectionPolicy, true)
	if err != nil {
		t.Fatal(err)
	}

	if retrieval.Cause != CauseNoRolloutBinding {
		t.Fatalf("retrieval params: got %q, want %q", retrieval.Cause, CauseNoRolloutBinding)
	}
	if policy.Cause != CauseNotRuntimeResolvable {
		t.Fatalf("selection policy: got %q, want %q", policy.Cause, CauseNotRuntimeResolvable)
	}
	if retrieval.Cause == policy.Cause {
		t.Fatal("the two context cells collapsed onto one cause")
	}

	// The retrieval cell must read as one that CAN gain a row: not permanent, artifact named, ours.
	if retrieval.Cause.Permanent() {
		t.Fatal("a retrieval parameter was reported as a structural impossibility")
	}
	if retrieval.MissingArtifact == "" {
		t.Fatal("the retrieval gap names no missing field")
	}
	if retrieval.Cause.Owner() != "the platform" {
		t.Fatalf("the retrieval gap is owned by %q, want the platform", retrieval.Cause.Owner())
	}
	// …and the policy cell must read as one that cannot.
	if !policy.Cause.Permanent() {
		t.Fatal("a selection policy was reported as unbuilt work")
	}
	if policy.MissingArtifact != "" {
		t.Fatalf("a permanent boundary names artifact %q", policy.MissingArtifact)
	}
	if !strings.Contains(strings.ToLower(policy.Note), "delet") {
		t.Fatalf("the policy refusal does not name the deletion that makes it permanent: %q", policy.Note)
	}
}

// TestDropToleranceGatesRolloutAuthoring — task 11.3.
func TestDropToleranceGatesRolloutAuthoring(t *testing.T) {
	base := AuthorRequest{
		Rollout: Rollout{
			ID: "ro_ctx", ParentConfigHash: "p", CandidateConfigHash: "c",
			Change: ChangeModelWithinProvider, ShareBasisPoints: 500, ExpiresAtUnixMs: testExpires,
		},
		NodeIsBound:     true,
		Entitled:        true,
		Halt:            HaltState{Readable: true},
		Guardrail:       GuardrailNotApplicable,
		VerifiedDelta:   true,
		CreatedAtUnixMs: testNow,
	}

	rejected := base
	rejected.GateRejected = true
	rejected.GateCause = "drop-tolerance: n_summarize would discard an item marked must-retain"
	err := AuthorRollout(rejected)
	if err == nil {
		t.Fatal("a drop-tolerance rejection did not block rollout authoring")
	}
	var refused *ErrRolloutRefused
	if !asRefusal(err, &refused) || refused.Cause != RefusedGate {
		t.Fatalf("want %q, got %v", RefusedGate, err)
	}
	if !strings.Contains(refused.Detail, "drop-tolerance") {
		t.Fatalf("the refusal does not name the gate: %q", refused.Detail)
	}
}

// TestUnknownToleranceDoesNotBlockAuthoring — task 11.3.
//
// 🔴 The gate's standing rule, extended rather than restated: it never refuses on IGNORANCE. Refusing
// because nobody has said whether an item matters would block every change on a workflow nobody has
// annotated, which is most of them. So the unknown is recorded and carried.
func TestUnknownToleranceDoesNotBlockAuthoring(t *testing.T) {
	req := AuthorRequest{
		Rollout: Rollout{
			ID: "ro_ctx2", ParentConfigHash: "p", CandidateConfigHash: "c",
			Change: ChangeInferenceParams, ShareBasisPoints: 500, ExpiresAtUnixMs: testExpires,
		},
		NodeIsBound:          true,
		Entitled:             true,
		Halt:                 HaltState{Readable: true},
		Guardrail:            GuardrailNotApplicable,
		VerifiedDelta:        true,
		DropToleranceUnknown: true,
		CreatedAtUnixMs:      testNow,
	}
	if err := AuthorRollout(req); err != nil {
		t.Fatalf("an unknown drop tolerance blocked authoring: %v", err)
	}
	// …and it is carried rather than dropped on the floor.
	if !Stamp(req).DropToleranceUnknown {
		t.Fatal("an unknown drop tolerance was not recorded on the rollout")
	}
	known := req
	known.DropToleranceUnknown = false
	if Stamp(known).DropToleranceUnknown {
		t.Fatal("a known tolerance was recorded as unknown")
	}
}

// TestOverlappingSplitBlocksRolloutAuthoring — task 11.4.
func TestOverlappingSplitBlocksRolloutAuthoring(t *testing.T) {
	req := AuthorRequest{
		Rollout: Rollout{
			ID: "ro_ctx3", ParentConfigHash: "p", CandidateConfigHash: "c",
			Change: ChangeInferenceParams, ShareBasisPoints: 500, ExpiresAtUnixMs: testExpires,
		},
		NodeIsBound:     true,
		Entitled:        true,
		Halt:            HaltState{Readable: true},
		Guardrail:       GuardrailNotApplicable,
		GateRejected:    true,
		GateCause:       "held-out overlap: the evaluation split intersects the retrieval corpus",
		CreatedAtUnixMs: testNow,
	}
	err := AuthorRollout(req)
	if err == nil {
		t.Fatal("a retrieval change with an overlapping split was authored as a rollout candidate")
	}
	var refused *ErrRolloutRefused
	if !asRefusal(err, &refused) || refused.Cause != RefusedGate {
		t.Fatalf("want %q, got %v", RefusedGate, err)
	}
	// 🔴 The refusal names the OVERLAP, not the delivery route — a reader must not conclude that some
	// other route would have accepted it.
	if !strings.Contains(refused.Detail, "overlap") {
		t.Fatalf("the refusal does not name the overlap: %q", refused.Detail)
	}
	if strings.Contains(strings.ToLower(refused.Detail), "rollout is not eligible") {
		t.Fatalf("the refusal blames the route rather than the overlap: %q", refused.Detail)
	}
}
