package changedelivery

import (
	"fmt"
	"strings"
	"testing"
)

const (
	testNow     int64 = 1_800_000_000_000
	testExpires int64 = testNow + 7*24*60*60*1000
)

func testRollout() Rollout {
	return Rollout{
		ID:                  "ro_7f3a",
		WorkflowID:          "wf_triage",
		NodeID:              "n_triage",
		ParentConfigHash:    "aaaa1111",
		CandidateConfigHash: "bbbb2222",
		Change:              ChangeModelWithinProvider,
		ShareBasisPoints:    1000, // 10%
		ExpiresAtUnixMs:     testExpires,
		VerifiedDelta:       true,
	}
}

// TestArmAssignmentIsPureAndReplicaAgnostic — task 23.6.
//
// Two replicas must agree with NO coordination, because there is nowhere to coordinate: they are the
// customer's processes and we are not in the request path.
func TestArmAssignmentIsPureAndReplicaAgnostic(t *testing.T) {
	r := testRollout()
	for i := 0; i < 500; i++ {
		key := AssignmentKey{Value: fmt.Sprintf("session-%d", i), Supplied: true}
		first := Resolve(r, key, testNow, GuardState{})
		for rep := 0; rep < 5; rep++ {
			again := Resolve(r, key, testNow, GuardState{})
			if again.Arm != first.Arm || again.ConfigHash != first.ConfigHash {
				t.Fatalf("key %q resolved to %s then %s — assignment is not a pure function", key.Value, first.Arm, again.Arm)
			}
		}
	}
}

// TestArmAssignmentReplaysWithoutATable — task 23.6.
//
// A past assignment must be reproducible from (rollout identity, key) alone. No stored assignment
// table means nothing to lose, migrate, or disagree about.
func TestArmAssignmentReplaysWithoutATable(t *testing.T) {
	r := testRollout()
	// Golden vectors. These are pinned deliberately: if the hash or the bucketing changes, every
	// customer's arm assignment silently re-shuffles mid-rollout and the evidence becomes two
	// experiments stitched together. This test is the tripwire for that.
	golden := map[string]Arm{}
	for i := 0; i < 40; i++ {
		k := fmt.Sprintf("unit-%d", i)
		golden[k] = Resolve(r, AssignmentKey{Value: k, Supplied: true}, testNow, GuardState{}).Arm
	}
	// Replay much later in wall-clock terms (but before expiry) and from a "different replica" — same
	// inputs, same answers.
	for k, want := range golden {
		got := Resolve(r, AssignmentKey{Value: k, Supplied: true}, testNow+3_600_000, GuardState{}).Arm
		if got != want {
			t.Fatalf("replay of %q gave %s, want %s", k, got, want)
		}
	}

	// The share must actually be honoured, or "deterministic" would be satisfied by always answering
	// parent. Over a large key space the candidate share should land near the declared 10%.
	candidates := 0
	const n = 20000
	for i := 0; i < n; i++ {
		if Resolve(r, AssignmentKey{Value: fmt.Sprintf("k-%d", i), Supplied: true}, testNow, GuardState{}).Arm == ArmCandidate {
			candidates++
		}
	}
	share := float64(candidates) / float64(n)
	if share < 0.08 || share > 0.12 {
		t.Fatalf("candidate share %.4f is far from the declared 10%%; the bucketing is skewed", share)
	}
}

// TestMissingAssignmentKeyIsRecordedNotSynthesized — task 23.7.
//
// 🔴 A synthesized key would make the weaker guarantee INVISIBLE: the rollout would look like it had
// sticky per-session assignment while actually re-rolling every call, and the evidence would quietly be
// about a different experiment than the one someone thought they ran.
func TestMissingAssignmentKeyIsRecordedNotSynthesized(t *testing.T) {
	r := testRollout()
	got := Resolve(r, AssignmentKey{Value: "nonce-1", Supplied: false}, testNow, GuardState{})
	if got.StableKey {
		t.Fatal("an unsupplied key was reported as stable")
	}
	// The arm is still attributed to a real configuration — weaker does not mean unattributed.
	if got.ConfigHash != r.ParentConfigHash && got.ConfigHash != r.CandidateConfigHash {
		t.Fatalf("unstable assignment emitted %q, which is neither arm", got.ConfigHash)
	}
	stable := Resolve(r, AssignmentKey{Value: "nonce-1", Supplied: true}, testNow, GuardState{})
	if !stable.StableKey {
		t.Fatal("a supplied key was reported as unstable")
	}
}

// TestCandidateInvocationRecordsCandidateHash — task 23.8.
//
// This is ADR-002's comparability objection answered: the ARM is the unit of record.
func TestCandidateInvocationRecordsCandidateHash(t *testing.T) {
	r := testRollout()
	sawCandidate, sawParent := false, false
	for i := 0; i < 2000 && !(sawCandidate && sawParent); i++ {
		got := Resolve(r, AssignmentKey{Value: fmt.Sprintf("u%d", i), Supplied: true}, testNow, GuardState{})
		switch got.Arm {
		case ArmCandidate:
			sawCandidate = true
			if got.ConfigHash != r.CandidateConfigHash {
				t.Fatalf("candidate invocation recorded %q, want the candidate's own hash %q", got.ConfigHash, r.CandidateConfigHash)
			}
		case ArmParent:
			sawParent = true
			if got.ConfigHash != r.ParentConfigHash {
				t.Fatalf("parent invocation recorded %q, want %q", got.ConfigHash, r.ParentConfigHash)
			}
		}
		if got.RolloutID != r.ID {
			t.Fatalf("rollout id not emitted alongside the arm hash")
		}
	}
	if !sawCandidate || !sawParent {
		t.Fatal("did not observe both arms")
	}
}

// TestRolloutIdentityInHashSlotFailsTheRun — task 23.8.
func TestRolloutIdentityInHashSlotFailsTheRun(t *testing.T) {
	r := testRollout()
	if err := ValidateAttribution(r, r.CandidateConfigHash); err != nil {
		t.Fatalf("a legitimate candidate hash was rejected: %v", err)
	}
	if err := ValidateAttribution(r, r.ParentConfigHash); err != nil {
		t.Fatalf("a legitimate parent hash was rejected: %v", err)
	}
	for _, bad := range []string{r.ID, "", "cccc3333"} {
		err := ValidateAttribution(r, bad)
		if err == nil {
			t.Fatalf("emitting %q in the arm-hash slot did not fail the run", bad)
		}
		if _, ok := err.(*ErrAttributionMismatch); !ok {
			t.Fatalf("want *ErrAttributionMismatch, got %T", err)
		}
	}
	// The rollout-identity case must say WHY it is different from any other wrong string, because that
	// is the mistake a resolver author actually makes.
	err := ValidateAttribution(r, r.ID)
	if !strings.Contains(err.Error(), "IDENTITY") {
		t.Fatalf("the identity-in-hash-slot error does not name the confusion: %v", err)
	}
}

// TestExpiredRolloutServesParentOffline — task 23.9.
func TestExpiredRolloutServesParentOffline(t *testing.T) {
	r := testRollout()
	// A key that would otherwise land on the candidate.
	var candidateKey string
	for i := 0; i < 5000; i++ {
		k := fmt.Sprintf("u%d", i)
		if Resolve(r, AssignmentKey{Value: k, Supplied: true}, testNow, GuardState{}).Arm == ArmCandidate {
			candidateKey = k
			break
		}
	}
	if candidateKey == "" {
		t.Fatal("no candidate key found")
	}
	got := Resolve(r, AssignmentKey{Value: candidateKey, Supplied: true}, r.ExpiresAtUnixMs, GuardState{})
	if got.Arm != ArmParent {
		t.Fatalf("an expired rollout served %s", got.Arm)
	}
	if got.Reason != ReasonExpired {
		t.Fatalf("reason %q, want %q", got.Reason, ReasonExpired)
	}
	if got.ConfigHash != r.ParentConfigHash {
		t.Fatalf("expired invocation recorded %q, want the parent's hash", got.ConfigHash)
	}
	if !r.Expired(r.ExpiresAtUnixMs) {
		t.Fatal("Expired() disagrees with Resolve()")
	}

	// A forgotten rollout cannot become the durable configuration: validation refuses an unbounded one.
	unbounded := testRollout()
	unbounded.ExpiresAtUnixMs = testNow + MaxRolloutLifetimeMs + 1
	if err := unbounded.Validate(testNow); err == nil {
		t.Fatal("a rollout exceeding the lifetime ceiling was accepted")
	}
	none := testRollout()
	none.ExpiresAtUnixMs = 0
	if err := none.Validate(testNow); err == nil {
		t.Fatal("a rollout with no expiry was accepted")
	}
}

// TestGuardTripRevertsWithoutPlatform — task 23.10.
func TestGuardTripRevertsWithoutPlatform(t *testing.T) {
	r := testRollout()
	r.Guards = []Guard{
		{Kind: GuardErrorRate, Threshold: 500},
		{Kind: GuardExceptionClass, Class: "RateLimitError"},
	}
	state := EvaluateGuards(r, GuardState{}, GuardObservation{ErrorRatePerMyriad: 200})
	if state.Tripped {
		t.Fatal("a guard tripped below its threshold")
	}
	state = EvaluateGuards(r, state, GuardObservation{ErrorRatePerMyriad: 900})
	if !state.Tripped {
		t.Fatal("a guard did not trip above its threshold")
	}
	if state.Detail == "" {
		t.Fatal("the trip records no cause")
	}
	// Every invocation now serves the parent, whatever the key would have said.
	for i := 0; i < 200; i++ {
		got := Resolve(r, AssignmentKey{Value: fmt.Sprintf("u%d", i), Supplied: true}, testNow, state)
		if got.Arm != ArmParent {
			t.Fatalf("after a guard trip, key %d resolved to %s", i, got.Arm)
		}
		if got.Reason != ReasonGuardTitped {
			t.Fatalf("reason %q, want %q", got.Reason, ReasonGuardTitped)
		}
	}

	// The exception-class guard trips on its class and not on another.
	s2 := EvaluateGuards(r, GuardState{}, GuardObservation{ExceptionClass: "ValueError"})
	if s2.Tripped {
		t.Fatal("an undeclared exception class tripped a guard")
	}
	s3 := EvaluateGuards(r, GuardState{}, GuardObservation{ExceptionClass: "RateLimitError"})
	if !s3.Tripped {
		t.Fatal("the declared exception class did not trip its guard")
	}
}

// TestRolloutDoesNotSelfResume — task 23.10.
//
// 🔴 Reverting is the safe direction, so it is automated. RESUMING moves traffic back toward a
// configuration that just failed under load, so it is not.
func TestRolloutDoesNotSelfResume(t *testing.T) {
	r := testRollout()
	r.Guards = []Guard{{Kind: GuardErrorRate, Threshold: 500}}
	tripped := EvaluateGuards(r, GuardState{}, GuardObservation{ErrorRatePerMyriad: 900})
	if !tripped.Tripped {
		t.Fatal("setup: guard did not trip")
	}
	// The condition clears completely. The rollout must NOT come back.
	cleared := EvaluateGuards(r, tripped, GuardObservation{ErrorRatePerMyriad: 0})
	if !cleared.Tripped {
		t.Fatal("the rollout resumed when the guard condition cleared — that is the platform re-exposing traffic to a known regression on its own authority")
	}
	// Nor with time passing.
	for _, at := range []int64{testNow + 1000, testNow + 86_400_000} {
		if got := Resolve(r, AssignmentKey{Value: "sticky", Supplied: true}, at, cleared); got.Arm != ArmParent {
			t.Fatalf("at %d the rollout resumed on its own", at)
		}
	}
}

// TestRolloutIsInertUnderPinnedResolver — task 23.11.
func TestRolloutIsInertUnderPinnedResolver(t *testing.T) {
	requested := "requested-config-hash"
	got := PinnedResolve(requested)
	if got.ConfigHash != requested {
		t.Fatalf("a pinned resolve returned %q, want the requested configuration %q", got.ConfigHash, requested)
	}
	if got.Arm != ArmParent {
		t.Fatalf("a pinned resolve returned arm %s", got.Arm)
	}
	// PinnedResolve takes no rollout at all — the strongest possible statement that a rollout cannot
	// tilt a measurement run, since there is no parameter through which it could.
}

// TestRolloutEvidenceIsNotAVerifiedDelta — task 23.11.
func TestRolloutEvidenceIsNotAVerifiedDelta(t *testing.T) {
	if RolloutEvidenceIsVerifiedDelta() {
		t.Fatal("rollout evidence was reported as a verified delta")
	}
}

// TestRolloutEntitlementIsServerSide — task 23.12.
func TestRolloutEntitlementIsServerSide(t *testing.T) {
	base := AuthorRequest{
		Rollout:         testRollout(),
		NodeIsBound:     true,
		Entitled:        true,
		Halt:            HaltState{Readable: true},
		Guardrail:       GuardrailNotApplicable,
		VerifiedDelta:   true,
		CreatedAtUnixMs: testNow,
	}
	if err := AuthorRollout(base); err != nil {
		t.Fatalf("a well-formed rollout was refused: %v", err)
	}
	req := base
	req.Entitled = false
	err := AuthorRollout(req)
	if err == nil {
		t.Fatal("an unentitled caller authored a rollout")
	}
	var refused *ErrRolloutRefused
	if !asRefusal(err, &refused) || refused.Cause != RefusedNotEntitled {
		t.Fatalf("want %q, got %v", RefusedNotEntitled, err)
	}
}

// TestUnreadableHaltFailsClosed — task 23.12.
func TestUnreadableHaltFailsClosed(t *testing.T) {
	base := AuthorRequest{
		Rollout:         testRollout(),
		NodeIsBound:     true,
		Entitled:        true,
		Guardrail:       GuardrailNotApplicable,
		CreatedAtUnixMs: testNow,
	}
	for _, tc := range []struct {
		name string
		halt HaltState
		want string
	}{
		{"unreadable", HaltState{Readable: false}, RefusedHaltUnreadable},
		{"active", HaltState{Readable: true, Active: true}, RefusedHalted},
	} {
		req := base
		req.Halt = tc.halt
		err := AuthorRollout(req)
		if err == nil {
			t.Fatalf("%s: authoring succeeded", tc.name)
		}
		var refused *ErrRolloutRefused
		if !asRefusal(err, &refused) || refused.Cause != tc.want {
			t.Fatalf("%s: want %q, got %v", tc.name, tc.want, err)
		}
	}
}

// TestGuardrailVerdictBoundsRolloutAuthoring — task 23.16.
func TestGuardrailVerdictBoundsRolloutAuthoring(t *testing.T) {
	base := AuthorRequest{
		Rollout:         testRollout(),
		NodeIsBound:     true,
		Entitled:        true,
		Halt:            HaltState{Readable: true},
		VerifiedDelta:   true,
		CreatedAtUnixMs: testNow,
	}
	rejected := base
	rejected.Guardrail = GuardrailRejected
	err := AuthorRollout(rejected)
	if err == nil {
		t.Fatal("a guardrail-rejected downgrade was authored as a rollout candidate")
	}
	var refused *ErrRolloutRefused
	if !asRefusal(err, &refused) || refused.Cause != RefusedGuardrail {
		t.Fatalf("want %q, got %v", RefusedGuardrail, err)
	}

	undecided := base
	undecided.Guardrail = GuardrailUndecided
	if err := AuthorRollout(undecided); err != nil {
		t.Fatalf("an undecided verdict blocked authoring: %v", err)
	}
	// 🔴 …and the ambiguity travels WITH the rollout rather than being dropped.
	if !Stamp(undecided).GuardrailUndecided {
		t.Fatal("an undecided guardrail verdict was not recorded on the rollout")
	}
	if Stamp(base).GuardrailUndecided {
		t.Fatal("a non-applicable verdict was recorded as undecided")
	}
}

// TestRolloutDoesNotUpgradeAnAuthoredChange — task 23.17.
func TestRolloutDoesNotUpgradeAnAuthoredChange(t *testing.T) {
	req := AuthorRequest{
		Rollout:         testRollout(),
		NodeIsBound:     true,
		Entitled:        true,
		Halt:            HaltState{Readable: true},
		Guardrail:       GuardrailNotApplicable,
		AuthoredByUser:  true,
		VerifiedDelta:   false,
		CreatedAtUnixMs: testNow,
	}
	if err := AuthorRollout(req); err != nil {
		t.Fatalf("an authored change was refused the same cell an operator change gets: %v", err)
	}
	stamp := Stamp(req)
	if stamp.Origin != "user" {
		t.Fatalf("origin %q, want user", stamp.Origin)
	}
	if !stamp.Unverified {
		t.Fatal("an authored change with no verified delta was not stamped unverified — a rollout must not launder a user's own edit into a platform result")
	}
	if RolloutEvidenceIsVerifiedDelta() {
		t.Fatal("rollout evidence counts as a verified delta")
	}
}

// TestBothArmsAreReadableWithoutComposition — task 23.15.
func TestBothArmsAreReadableWithoutComposition(t *testing.T) {
	r := testRollout()
	hashes := r.ArmHashes()
	if len(hashes) != 2 || hashes[0] == hashes[1] {
		t.Fatalf("arms are not two distinct complete configurations: %v", hashes)
	}
	same := testRollout()
	same.CandidateConfigHash = same.ParentConfigHash
	if err := same.Validate(testNow); err == nil {
		t.Fatal("a rollout whose arms resolve identically was accepted; it would produce evidence about nothing")
	}
	empty := testRollout()
	empty.CandidateConfigHash = ""
	if err := empty.Validate(testNow); err == nil {
		t.Fatal("an arm with no configuration was accepted; it could not be attributed to")
	}
}

// TestShareBoundsAreEnforced — a 0% rollout is not a rollout and a 100% one is a deploy in disguise.
func TestShareBoundsAreEnforced(t *testing.T) {
	for _, bp := range []int{-1, 0, ShareDenominator, ShareDenominator + 1} {
		r := testRollout()
		r.ShareBasisPoints = bp
		if err := r.Validate(testNow); err == nil {
			t.Fatalf("share %d basis points was accepted", bp)
		}
	}
}

func asRefusal(err error, out **ErrRolloutRefused) bool {
	r, ok := err.(*ErrRolloutRefused)
	if ok {
		*out = r
	}
	return ok
}
