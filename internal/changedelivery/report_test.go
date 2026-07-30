package changedelivery

import (
	"strings"
	"testing"
)

func materializes() SourceOutcome { return SourceOutcome{Materializes: true} }
func refusesSource() SourceOutcome {
	return SourceOutcome{Cause: "no-materializer-for-this-language",
		MissingArtifact: "a Go statement resolver", Note: "not built yet"}
}

// TestRolloutNeverReachesDeliveredState — task 23.3, the precursor rule.
//
// 🔴 This is the single most load-bearing assertion in the package. If a rollout could end in
// `delivered`, the customer's repository would stop describing what their agent does, and every later
// codemod would be computed against a source that is not what runs.
func TestRolloutNeverReachesDeliveredState(t *testing.T) {
	cases := []struct {
		name    string
		src     SourceOutcome
		rollout RolloutStatus
	}{
		{"active rollout, source materializes", materializes(), RolloutStatus{Active: true}},
		{"active rollout, source refuses", refusesSource(), RolloutStatus{Active: true}},
		{"completed rollout, source materializes", materializes(), RolloutStatus{Completed: true}},
		{"completed rollout, source refuses", refusesSource(), RolloutStatus{Completed: true}},
		{"reverted rollout", materializes(), RolloutStatus{Reverted: true}},
	}
	for _, tc := range cases {
		rep, err := BuildReport(ChangeModelWithinProvider, "go", true, tc.src, tc.rollout, false)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if rep.State == StateDelivered {
			t.Fatalf("%s: a rollout reached %q without a merged pull request", tc.name, StateDelivered)
		}
		if rep.State == StateRolloutActive && rep.RemainingStep == "" {
			t.Fatalf("%s: an active rollout names no remaining step", tc.name)
		}
	}

	// The ONLY way to `delivered` is a merged pull request.
	merged := materializes()
	merged.Merged = true
	rep, err := BuildReport(ChangeModelWithinProvider, "go", true, merged, RolloutStatus{}, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if rep.State != StateDelivered {
		t.Fatalf("a merged pull request produced %q, want %q", rep.State, StateDelivered)
	}
	if rep.RemainingStep != "" {
		t.Fatalf("a delivered change still names a remaining step: %q", rep.RemainingStep)
	}

	// A rollout on an axis with no materializer must SAY it can never become permanent, rather than
	// implying a pull request is coming.
	rep, err = BuildReport(ChangeModelWithinProvider, "rust", true, refusesSource(), RolloutStatus{Active: true}, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(rep.RemainingStep, "cannot be made permanent") {
		t.Fatalf("a rollout with no source route does not say it cannot become permanent: %q", rep.RemainingStep)
	}
}

// TestReportIsTotalAndNamesBothRoutes — task 23.1's per-change half.
func TestReportIsTotalAndNamesBothRoutes(t *testing.T) {
	for _, kind := range ChangeKinds() {
		rep, err := BuildReport(kind, "go", true, SourceOutcomeFor(kind, "go", ""), RolloutStatus{}, false)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if len(rep.Routes) != len(Routes()) {
			t.Fatalf("%s: report carries %d routes, want %d", kind, len(rep.Routes), len(Routes()))
		}
		for _, r := range Routes() {
			o, ok := rep.Outcome(r)
			if !ok {
				t.Fatalf("%s: no outcome for route %s", kind, r)
			}
			if o.Refused() && o.Cause == "" {
				t.Fatalf("%s/%s: refusal with no cause", kind, r)
			}
			if o.Refused() && o.Owner == "" {
				t.Fatalf("%s/%s: refusal with no owner — the one word that decides what a reader does next", kind, r)
			}
		}
		if rep.State == "" {
			t.Fatalf("%s: no state", kind)
		}
	}
}

// TestUndeliverableIsReportedNotPending — task 23.1/23.2.
func TestUndeliverableIsReportedNotPending(t *testing.T) {
	// Memory: both routes refuse. This is the axis that made the contract necessary.
	src := SourceOutcome{Cause: "unsafe-rewrite", Permanent: true, Note: "refused at transform"}
	rep, err := BuildReport(ChangeMemoryStrategy, "go", true, src, RolloutStatus{}, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if rep.State != StateUndeliverable {
		t.Fatalf("state %q, want %q", rep.State, StateUndeliverable)
	}
	if !rep.Undeliverable() {
		t.Fatal("Undeliverable() disagrees with State")
	}
	// Both causes readable, neither inferred from the other.
	srcOut, _ := rep.Outcome(RouteSource)
	rtOut, _ := rep.Outcome(RouteRuntime)
	if srcOut.Cause == "" || rtOut.Cause == "" {
		t.Fatal("a route reports no cause")
	}
	if srcOut.Cause == rtOut.Cause {
		t.Fatalf("both routes report the same cause %q; they refuse for different reasons and a reader must be able to tell", srcOut.Cause)
	}
}

// TestIdentityChangeHasNothingToDeliver — P17 FR37, exercised through the shared state machine.
func TestIdentityChangeHasNothingToDeliver(t *testing.T) {
	rep, err := BuildReport(ChangeMemoryStrategy, "go", true,
		SourceOutcome{Cause: "unsafe-rewrite", Permanent: true}, RolloutStatus{}, true)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if rep.State != StateNothingToDeliver {
		t.Fatalf("state %q, want %q — an identity change is neither a delivery nor a refusal", rep.State, StateNothingToDeliver)
	}
}

// TestGateRejectedChangeIsUndeliverableByEveryRoute — P15 FR54, enforced centrally so no axis can
// forget it.
//
// 🔴 A second route arriving beside a gate whose whole purpose is to produce nothing is exactly where
// someone reasons "the rewriter refused, so roll it out instead". That reading would turn the strongest
// safety gate in the system into a speed bump.
func TestGateRejectedChangeIsUndeliverableByEveryRoute(t *testing.T) {
	src := SourceOutcome{GateRejected: true, Cause: "incoherent-ordering", Permanent: true}
	// Deliberately chosen: a change kind whose runtime route WOULD otherwise be eligible. If the gate
	// did not override eligibility, this report would offer the runtime route.
	rep, err := BuildReport(ChangeModelWithinProvider, "go", true, src, RolloutStatus{}, false)
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, r := range Routes() {
		o, _ := rep.Outcome(r)
		if !o.Refused() {
			t.Fatalf("route %s delivers a gate-rejected change", r)
		}
	}
	if rep.State != StateUndeliverable {
		t.Fatalf("state %q, want %q", rep.State, StateUndeliverable)
	}
}

// TestDeliveryGuaranteesAreSabotageable — task 23.21, the gate that must go red.
//
// # Why this test exists at all
//
// Every guarantee in this package is enforced by a check, and a check nobody has ever seen FAIL is
// indistinguishable from a check that cannot fail. This project's own history is a list of green
// suites hiding dead assertions. So each guarantee is deliberately violated here, and each violation
// must be caught by a DISTINCT mechanism — if two sabotages trip the same check, one of the guarantees
// is not actually being enforced.
func TestDeliveryGuaranteesAreSabotageable(t *testing.T) {
	t.Run("arm attribution", func(t *testing.T) {
		r := testRollout()
		if err := ValidateAttribution(r, r.ID); err == nil {
			t.Fatal("sabotage undetected: the rollout identity passed as an arm hash")
		}
	})

	t.Run("expiry", func(t *testing.T) {
		r := testRollout()
		r.ExpiresAtUnixMs = testNow + MaxRolloutLifetimeMs*2
		if err := r.Validate(testNow); err == nil {
			t.Fatal("sabotage undetected: an effectively unbounded rollout validated")
		}
	})

	t.Run("guard revert", func(t *testing.T) {
		r := testRollout()
		r.Guards = []Guard{{Kind: GuardErrorRate, Threshold: 100}}
		tripped := EvaluateGuards(r, GuardState{}, GuardObservation{ErrorRatePerMyriad: 9999})
		if !tripped.Tripped {
			t.Fatal("sabotage undetected: a guard well past its threshold did not trip")
		}
		// …and a "sabotaged" resolver that ignored the trip would serve the candidate. Assert the real
		// one does not.
		served := map[Arm]bool{}
		for i := 0; i < 100; i++ {
			served[Resolve(r, AssignmentKey{Value: string(rune('a' + i%26))}, testNow, tripped).Arm] = true
		}
		if served[ArmCandidate] {
			t.Fatal("sabotage undetected: the candidate arm was served after a guard trip")
		}
	})

	t.Run("precursor rule", func(t *testing.T) {
		// A Report hand-built to claim delivery after a completed rollout must not survive Validate…
		bad := Report{
			Change: ChangeModelWithinProvider, Axis: AxisModel,
			Routes:        []RouteOutcome{{Route: RouteSource, Status: StatusDelivers}, {Route: RouteRuntime, Status: StatusDelivers}},
			State:         StateDelivered,
			RemainingStep: "a pull request is still required",
		}
		if err := bad.Validate(); err == nil {
			t.Fatal("sabotage undetected: a delivered report that still names a remaining step validated")
		}
		// …and the state machine itself must never produce delivered from a rollout.
		rep, err := BuildReport(ChangeModelWithinProvider, "go", true, materializes(), RolloutStatus{Completed: true}, false)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if rep.State == StateDelivered {
			t.Fatal("sabotage undetected: a completed rollout produced delivered")
		}
	})

	t.Run("cause order and the boundary asymmetry", func(t *testing.T) {
		bad := Report{
			Change: ChangeWiring, Axis: AxisWiring,
			Routes: []RouteOutcome{
				{Route: RouteSource, Status: StatusDelivers},
				{Route: RouteRuntime, Status: StatusRefuses, Cause: string(CauseNotRuntimeResolvable),
					Permanent: true, MissingArtifact: "a wiring rollout resolver, Q3"},
			},
			State: StateSourcePending,
		}
		if err := bad.Validate(); err == nil {
			t.Fatal("sabotage undetected: a permanent boundary that names a missing artifact validated — this is exactly how 'never' becomes 'not yet'")
		}
	})

	t.Run("route totality", func(t *testing.T) {
		bad := Report{
			Change: ChangeModelWithinProvider, Axis: AxisModel,
			Routes: []RouteOutcome{{Route: RouteSource, Status: StatusDelivers}},
			State:  StateSourcePending,
		}
		if err := bad.Validate(); err == nil {
			t.Fatal("sabotage undetected: a report missing the runtime route validated")
		}
	})

	t.Run("causeless refusal", func(t *testing.T) {
		bad := Report{
			Change: ChangeModelWithinProvider, Axis: AxisModel,
			Routes: []RouteOutcome{
				{Route: RouteSource, Status: StatusRefuses},
				{Route: RouteRuntime, Status: StatusDelivers},
			},
			State: StateUndeliverable,
		}
		if err := bad.Validate(); err == nil {
			t.Fatal("sabotage undetected: a refusal with no cause validated — that is the silence this contract removes")
		}
	})
}

// TestSourceOutcomeIsReadFromCoverage — task 23.1, the "no second table" half.
func TestSourceOutcomeIsReadFromCoverage(t *testing.T) {
	// Model in Go materializes; the coverage table is the authority and this must track it.
	got := SourceOutcomeFor(ChangeModelWithinProvider, "go", "")
	if !got.Materializes {
		t.Fatalf("model/go does not materialize per the delivery join, but the coverage table says otherwise")
	}
	// A provider crossing is permanently refused in every language, without consulting a table cell.
	for _, lang := range []string{"go", "python", "rust"} {
		out := SourceOutcomeFor(ChangeModelAcrossProvider, lang, "")
		if out.Materializes {
			t.Fatalf("%s: a provider crossing was reported as materializing", lang)
		}
		if !out.Permanent {
			t.Fatalf("%s: a provider crossing was reported as a temporary gap", lang)
		}
		if out.MissingArtifact != "" {
			t.Fatalf("%s: a permanent source refusal names artifact %q", lang, out.MissingArtifact)
		}
	}
	// Every change kind resolves to SOME source answer in every registered language — no blank cells.
	for _, kind := range ChangeKinds() {
		for _, lang := range []string{"go", "python"} {
			out := SourceOutcomeFor(kind, lang, "")
			if !out.Materializes && out.Cause == "" {
				t.Fatalf("%s/%s: neither materializes nor names a cause", kind, lang)
			}
			if out.Cause == "no-coverage-cell" {
				t.Fatalf("%s/%s: coverage has no cell at all, which is a totality violation upstream", kind, lang)
			}
		}
	}
}

// TestMixedLanguageResolvesConservatively — the half of the coverage join that decides whether the
// product over-promises.
//
// Python materializes a model change at an `openai` call and refuses one at a `langchain` call, because
// the second binds its model before the call. 🚫 Answering "python materializes model changes" because
// SOME form does is the optimistic copy the coverage contract exists to prevent — it promises a reader
// a diff their particular call site will never produce.
func TestMixedLanguageResolvesConservatively(t *testing.T) {
	// With no form supplied, a mixed language reports the refusal and NAMES the split.
	summary := SourceOutcomeFor(ChangeModelWithinProvider, "python", "")
	if summary.Materializes {
		t.Fatal("a mixed language was reported as materializing without a call-site form")
	}
	if !strings.Contains(summary.Note, "the call site's own form decides") {
		t.Fatalf("the mixed answer does not say the form decides: %q", summary.Note)
	}

	// With the form supplied, the answer is the honest per-call-site one — both directions.
	yes := SourceOutcomeFor(ChangeModelWithinProvider, "python", "py.openai.chat.completions.create")
	if !yes.Materializes {
		t.Fatalf("a covered python form was reported as refusing: %+v", yes)
	}
	no := SourceOutcomeFor(ChangeModelWithinProvider, "python", "py.langchain.chatopenai.invoke")
	if no.Materializes {
		t.Fatal("an uncovered python form was reported as materializing")
	}
	if no.Cause == "" {
		t.Fatal("an uncovered form reports no cause")
	}
}

// TestVariesIsNotARefusal — the distinction that keeps a partially-covered axis from being counted as
// a dead end.
//
// The conservative Materializes=false is the right answer to "will MY call site get a diff". It is the
// WRONG input to "is this change undeliverable anywhere", because some call site plainly does get one.
// One value cannot answer both, so `Varies` exists.
func TestVariesIsNotARefusal(t *testing.T) {
	mixed := SourceOutcomeFor(ChangeModelWithinProvider, "python", "")
	if mixed.Materializes {
		t.Fatal("a mixed language reported as materializing")
	}
	if !mixed.Varies {
		t.Fatal("a mixed language was not marked as varying — it would be counted as a dead end")
	}

	// A language where the axis materializes everywhere does not vary…
	whole := SourceOutcomeFor(ChangeModelWithinProvider, "go", "")
	if whole.Varies {
		t.Fatalf("a fully covered language was marked as varying: %+v", whole)
	}
	// …and neither does one where nothing does. Skill binding in Rust has no materializer at all —
	// unlike memory in Rust, whose identity strategy (`none`) DOES materialize, which is exactly the
	// kind of partial coverage `Varies` exists to keep visible.
	none := SourceOutcomeFor(ChangeSkillBinding, "rust", "")
	if none.Materializes {
		t.Fatal("skills/rust reported as materializing")
	}
	if none.Varies {
		t.Fatalf("a wholly uncovered cell was marked as varying: %+v", none)
	}
}
