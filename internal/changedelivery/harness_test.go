package changedelivery

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// p18_harness_test.go — the harness axis's delivery cells (P18 §15, FR46–FR51).
//
// # The distinction this axis is most likely to lose
//
// A scaffold is STRUCTURE: swapping a node onto a reason-and-act loop changes how many calls the
// program makes and in what control flow, and no document introduces a loop. But `max_turns`, the retry
// budget and the stop condition are PARAMETERS OF A LOOP ALREADY WRITTEN — data in exactly the sense
// the binding document was designed for.
//
// 🔴 And separately: `hostAbsent` and `notRuntimeResolvable` both mention the runtime and mean opposite
// things. One is answered by starting a service; the other cannot be answered.

// TestStrategyAndParamsCarryDifferentCauses — task 15.1.
func TestStrategyAndParamsCarryDifferentCauses(t *testing.T) {
	strategy, err := RuntimeEligibility(ChangeHarnessStrategy, true)
	if err != nil {
		t.Fatal(err)
	}
	params, err := RuntimeEligibility(ChangeHarnessParams, true)
	if err != nil {
		t.Fatal(err)
	}

	if strategy.Cause != CauseNotRuntimeResolvable {
		t.Fatalf("strategy swap: got %q, want %q", strategy.Cause, CauseNotRuntimeResolvable)
	}
	if params.Cause != CauseNoRolloutBinding {
		t.Fatalf("bounded params: got %q, want %q", params.Cause, CauseNoRolloutBinding)
	}
	if strategy.Cause == params.Cause {
		t.Fatal("the two harness cells collapsed onto one cause")
	}
	if !strings.Contains(strings.ToLower(strategy.Note), "loop") {
		t.Fatalf("the strategy refusal does not name the control loop: %q", strategy.Note)
	}
	if strategy.MissingArtifact != "" {
		t.Fatalf("a permanent boundary names artifact %q", strategy.MissingArtifact)
	}
	if params.MissingArtifact == "" {
		t.Fatal("the params gap names no missing field")
	}
	// The params cell reads as one that can gain a row; the strategy cell does not.
	if params.Cause.Permanent() {
		t.Fatal("a bounded parameter was reported as structurally impossible")
	}
	if !strategy.Cause.Permanent() {
		t.Fatal("a strategy swap was reported as unbuilt work")
	}
	// 🚫 And the strategy boundary is NOT contingent — unlike memory, a control loop is not waiting on a
	// component that could arrive.
	if strategy.Contingent {
		t.Fatal("the harness strategy boundary was marked contingent; a loop is structure, not a missing service")
	}
}

// TestHarnessSwapRefusesEveryRuntimeCell — task 15.3.
func TestHarnessSwapRefusesEveryRuntimeCell(t *testing.T) {
	for _, bound := range []bool{true, false} {
		got, err := RuntimeEligibility(ChangeHarnessStrategy, bound)
		if err != nil {
			t.Fatal(err)
		}
		if got.Eligible {
			t.Fatalf("bound=%v: a harness strategy swap was reported eligible", bound)
		}
		if got.Cause != CauseNotRuntimeResolvable {
			t.Fatalf("bound=%v: got %q — apply mode must not change the answer", bound, got.Cause)
		}
		// 🚫 No bound migration is suggested; it would not help.
		if strings.Contains(strings.ToLower(got.Note), "migrat") {
			t.Fatalf("the strategy refusal suggests a migration: %q", got.Note)
		}
	}
}

// TestHostAbsentAndNotRuntimeResolvableAreDistinct — task 15.2.
//
// 🔴 Rendering these alike sends an operator to restart something that was never the problem.
func TestHostAbsentAndNotRuntimeResolvableAreDistinct(t *testing.T) {
	src := SourceOutcomeFor(ChangeHarnessStrategy, "go", "")
	base, err := BuildReport(ChangeHarnessStrategy, "go", true, src, RolloutStatus{}, false)
	if err != nil {
		t.Fatal(err)
	}
	withHost := base.WithCondition(ExecutionCondition{
		Kind:   ConditionHostAbsent,
		Detail: "the critic model's host service is not running in this deployment",
	})

	// ── an absent host does NOT change delivery eligibility
	for _, r := range Routes() {
		a, _ := base.Outcome(r)
		b, _ := withHost.Outcome(r)
		if a.Status != b.Status || a.Cause != b.Cause {
			t.Fatalf("route %s changed when a host went absent: %v/%v -> %v/%v", r, a.Status, a.Cause, b.Status, b.Cause)
		}
	}
	if base.State != withHost.State {
		t.Fatalf("an absent host changed the delivery state: %q -> %q", base.State, withHost.State)
	}

	// ── and it is reported as its OWN condition, with its own kind
	if len(withHost.Conditions) != 1 || withHost.Conditions[0].Kind != ConditionHostAbsent {
		t.Fatalf("the absent host was not reported as an execution condition: %+v", withHost.Conditions)
	}
	if len(base.Conditions) != 0 {
		t.Fatal("a report with no condition carries one")
	}

	// ── 🚫 a delivery refusal never mentions a host service, and never offers starting one as a remedy
	for _, r := range Routes() {
		o, _ := withHost.Outcome(r)
		if !o.Refused() {
			continue
		}
		low := strings.ToLower(o.Cause + " " + o.Note)
		for _, banned := range []string{"host service", "start the", "restart", "not running"} {
			if strings.Contains(low, banned) {
				t.Fatalf("route %s's delivery refusal mentions %q, which sends an operator to the wrong problem: %s",
					r, banned, o.Note)
			}
		}
	}
}

// TestRolloutArmCannotRemoveABound — task 15.4.
//
// A rollout must not become the one place a bound can be removed. The check is the registry's OWN
// seal-time validator, passed in rather than re-implemented — a second validator would drift, and it
// would drift toward permissive.
func TestRolloutArmCannotRemoveABound(t *testing.T) {
	// A stand-in for the registry's seal check, with the shape the real one has: a positive, bounded
	// turn ceiling, and no parameter the strategy does not declare.
	sealCheck := func(raw json.RawMessage) error {
		// Only the parameters this strategy DECLARES. Anything else is inexpressible rather than
		// ignored, which is what DisallowUnknownFields buys.
		var p struct {
			MaxTurns *int `json:"max_turns"`
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return err
		}
		if p.MaxTurns == nil {
			return errors.New("max_turns is required: a strategy with no ceiling is unbounded")
		}
		if *p.MaxTurns <= 0 {
			return errors.New("max_turns must be positive")
		}
		return nil
	}

	base := AuthorRequest{
		Rollout: Rollout{
			ID: "ro_h", ParentConfigHash: "p", CandidateConfigHash: "c",
			Change: ChangeModelWithinProvider, ShareBasisPoints: 500, ExpiresAtUnixMs: testExpires,
		},
		NodeIsBound:     true,
		Entitled:        true,
		Halt:            HaltState{Readable: true},
		Guardrail:       GuardrailNotApplicable,
		CreatedAtUnixMs: testNow,
	}

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"absent ceiling", `{}`},
		{"non-positive ceiling", `{"max_turns":0}`},
		{"negative ceiling", `{"max_turns":-3}`},
		{"parameter the strategy does not declare", `{"max_turns":3,"critic_temperature":2}`},
	} {
		req := base
		req.ArmParams = &ArmParams{Raw: json.RawMessage(tc.raw), Validate: sealCheck}
		err := AuthorRollout(req)
		if err == nil {
			t.Fatalf("%s: a rollout arm carrying %s was authored", tc.name, tc.raw)
		}
		var refused *ErrRolloutRefused
		if !asRefusal(err, &refused) || refused.Cause != RefusedArmParams {
			t.Fatalf("%s: want %q, got %v", tc.name, RefusedArmParams, err)
		}
		// The refusal must attribute itself to the registry's check rather than inventing its own.
		if !strings.Contains(refused.Detail, "the registry applies at seal") {
			t.Fatalf("%s: the refusal does not name the shared check: %q", tc.name, refused.Detail)
		}
	}

	// A well-formed arm passes.
	ok := base
	ok.ArmParams = &ArmParams{Raw: json.RawMessage(`{"max_turns":3}`), Validate: sealCheck}
	if err := AuthorRollout(ok); err != nil {
		t.Fatalf("a valid arm was refused: %v", err)
	}
}

// TestInapplicableParamStaysInexpressible — task 15.4.
func TestInapplicableParamStaysInexpressible(t *testing.T) {
	// The check rejects unknown fields rather than ignoring them. "Silently ignored" is the failure
	// this axis names explicitly: a user sets a parameter, nothing happens, and nothing says so.
	strict := func(raw json.RawMessage) error {
		var p struct {
			MaxTurns int `json:"max_turns"`
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		return dec.Decode(&p)
	}
	req := AuthorRequest{
		Rollout: Rollout{
			ID: "ro_h2", ParentConfigHash: "p", CandidateConfigHash: "c",
			Change: ChangeModelWithinProvider, ShareBasisPoints: 500, ExpiresAtUnixMs: testExpires,
		},
		NodeIsBound:     true,
		Entitled:        true,
		Halt:            HaltState{Readable: true},
		Guardrail:       GuardrailNotApplicable,
		CreatedAtUnixMs: testNow,
		ArmParams:       &ArmParams{Raw: json.RawMessage(`{"max_turns":3,"retry_budget":9}`), Validate: strict},
	}
	if err := AuthorRollout(req); err == nil {
		t.Fatal("a parameter the strategy does not declare was silently accepted")
	}
}

// TestHarnessRefusalTotalityCanaryCoversBothRoutes — task 15.5.
func TestHarnessRefusalTotalityCanaryCoversBothRoutes(t *testing.T) {
	req := AuthorRequest{
		Rollout: Rollout{
			ID: "ro_h3", ParentConfigHash: "p", CandidateConfigHash: "c",
			Change: ChangeHarnessStrategy, ShareBasisPoints: 500, ExpiresAtUnixMs: testExpires,
		},
		NodeIsBound:           true,
		Entitled:              true,
		Halt:                  HaltState{Readable: true},
		Guardrail:             GuardrailNotApplicable,
		TransformRefusalCause: "unsafeRewrite: node n_plan carries a harness strategy",
		CreatedAtUnixMs:       testNow,
	}
	err := AuthorRollout(req)
	if err == nil {
		t.Fatal("a harness strategy was authored as a rollout candidate; no document may carry one")
	}
	var refused *ErrRolloutRefused
	if !asRefusal(err, &refused) || refused.Cause != RefusedTransform {
		t.Fatalf("want %q, got %v", RefusedTransform, err)
	}
	if !strings.Contains(refused.Detail, "unsafeRewrite") {
		t.Fatalf("the authoring refusal does not carry the transform's own cause: %q", refused.Detail)
	}

	// 🔴 The sabotage half: removing the transform input must NOT open the path — the cell's own
	// eligibility is the second line of defence, and it must be doing real work.
	sabotaged := req
	sabotaged.TransformRefusalCause = ""
	err = AuthorRollout(sabotaged)
	if err == nil {
		t.Fatal("removing the transform-refusal input opened a path for a harness strategy")
	}
	if !asRefusal(err, &refused) || refused.Cause != RefusedIneligible {
		t.Fatalf("want %q as the second line of defence, got %v", RefusedIneligible, err)
	}

	// And no enumerated delivery route carries it.
	rep, err := BuildReport(ChangeHarnessStrategy, "go", true,
		SourceOutcome{Cause: "unsafe-rewrite", Permanent: true}, RolloutStatus{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Routes() {
		o, _ := rep.Outcome(r)
		if !o.Refused() {
			t.Fatalf("route %s delivers a harness strategy", r)
		}
	}
}
