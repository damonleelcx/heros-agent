package herosagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/providercall"
)

// P30 workstream 9 — DevOps.

// ── task 9.2 · the ceiling, before the provider call ─────────────────────────────────────────────

func cappedRunner(t *testing.T, limit int64, alreadySpent int64) (*Runner, *recordingModel, *MemSpendStore) {
	t.Helper()
	ctx := context.Background()
	caps := NewMemCapStore()
	if err := caps.Set(ctx, Cap{
		TenantID: "t1", MaxTokens: limit, Reason: "a test set this", SetBy: "test", UpdatedAtMS: 1,
	}); err != nil {
		t.Fatal(err)
	}
	meter := NewMemSpendStore()
	if alreadySpent > 0 {
		if err := meter.Record(ctx, Spend{
			TenantID: "t1", InferenceID: "prior", TokensIn: alreadySpent, CreatedAtMS: 1_000,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The clock sits inside the window, so `SpentSince` sees the prior row. Injected rather than
	// `time.Now`, because a test whose result depends on the calendar has a second clock.
	now := func() int64 { return 2_000 }
	checker, err := NewCapChecker(caps, meter, now)
	if err != nil {
		t.Fatal(err)
	}
	m := &recordingModel{
		result: RawResult{Edges: []RawEdge{{From: "a", To: "c", Kind: "data", Confidence: conf(0.9)}}},
		// A REAL usage, so the meter has something to record. A model reporting zero tokens would let
		// this suite pass against a runner that writes an empty row — which is the shape of "the meter
		// is wired" being true and useless.
		usage: providercall.Usage{InputTokens: 40, OutputTokens: 20},
	}
	r, err := NewRunner(m, NewMemInferenceStore(), 0.5, now, WithCaps(checker, meter))
	if err != nil {
		t.Fatal(err)
	}
	return r, m, meter
}

// 🔴 THE POINT OF 9.2 — asserted on the PROVIDER CALL COUNT, not on the error.
//
// A cap enforced after the call is an accounting record: the tokens are spent, the bill is incurred,
// and what the check buys is a slightly faster stop on the NEXT run. `err != nil` is satisfied by both
// versions; a count of zero is satisfied by only one.
func TestACapIsEnforcedBeforeTheProviderCall(t *testing.T) {
	r, m, _ := cappedRunner(t, 100, 150)

	res, err := r.Infer(context.Background(), inputFor(irWith([]string{"a", "b", "c"})), "cfg1", PlacementPlatform)
	if !errors.Is(err, ErrCapReached) {
		t.Fatalf("err is %v, want ErrCapReached", err)
	}
	if m.count() != 0 {
		t.Errorf("the provider was called %d times past a reached ceiling — a cap enforced afterwards "+
			"is an accounting record, not a cap", m.count())
	}
	if res.Code != CodeCapReached {
		t.Errorf("code is %q, want %q", res.Code, CodeCapReached)
	}
	// The refusal names the SCOPE and the operator's own note. An operator who raises the wrong ceiling
	// has raised nothing.
	for _, want := range []string{"tenant", "a test set this", "150", "100"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// The event fires, because a cap that stops spend silently is a cap nobody knows is binding — and
// "which tenant is capped, and since when" is the question asked the moment a customer reports that
// analysis stopped.
func TestReachingACapEmitsTheEvent(t *testing.T) {
	r, _, _ := cappedRunner(t, 100, 150)
	var got []Event
	r.emit = func(e Event, _ map[string]any) { got = append(got, e) }

	if _, err := r.Infer(context.Background(), inputFor(irWith([]string{"a", "b", "c"})), "cfg1", PlacementPlatform); err == nil {
		t.Fatal("the capped run was allowed")
	}
	for _, e := range got {
		if e == EventCapReached {
			return
		}
	}
	t.Errorf("events were %v, want one %q", got, EventCapReached)
}

// Under the ceiling, the run proceeds AND its spend is recorded — so the next check has something to
// read. A meter nothing writes to is a ceiling nobody is ever under.
func TestARunUnderTheCeilingProceedsAndIsMetered(t *testing.T) {
	r, m, meter := cappedRunner(t, 100_000, 10)

	if _, err := r.Infer(context.Background(), inputFor(irWith([]string{"a", "b", "c"})), "cfg1", PlacementPlatform); err != nil {
		t.Fatal(err)
	}
	if m.count() != 1 {
		t.Fatalf("the provider was called %d times", m.count())
	}
	spent, err := meter.SpentSince(context.Background(), "t1", 0)
	if err != nil {
		t.Fatal(err)
	}
	// 10 preceded the run and the model reported 60, so anything but 70 means the meter recorded the
	// wrong number rather than merely recording something.
	if spent != 70 {
		t.Errorf("spend after the run is %d, want 70 (10 prior + the 60 this run reported) — a meter "+
			"nothing writes to is a ceiling nobody is ever under, and one that writes the wrong number "+
			"is a ceiling that binds at the wrong time", spent)
	}
}

// 🔴 A CACHE HIT IS NOT REFUSED. It makes zero provider calls, so it costs nothing — refusing one
// under a cap would make the ceiling an availability limit rather than a spend limit.
func TestACappedTenantStillReadsItsStoredAnswer(t *testing.T) {
	ctx := context.Background()
	caps := NewMemCapStore()
	meter := NewMemSpendStore()
	now := func() int64 { return 2_000 }
	checker, err := NewCapChecker(caps, meter, now)
	if err != nil {
		t.Fatal(err)
	}
	m := &recordingModel{result: RawResult{Edges: []RawEdge{{From: "a", To: "c", Kind: "data", Confidence: conf(0.9)}}}}
	r, err := NewRunner(m, NewMemInferenceStore(), 0.5, now, WithCaps(checker, meter))
	if err != nil {
		t.Fatal(err)
	}
	ir := irWith([]string{"a", "b", "c"})
	if _, err := r.Infer(ctx, inputFor(ir), "cfg1", PlacementPlatform); err != nil {
		t.Fatal(err)
	}

	// NOW impose a ceiling already exceeded.
	if err := caps.Set(ctx, Cap{TenantID: "t1", MaxTokens: 1, Reason: "r", SetBy: "test", UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	if err := meter.Record(ctx, Spend{TenantID: "t1", InferenceID: "x", TokensIn: 999, CreatedAtMS: 1_500}); err != nil {
		t.Fatal(err)
	}
	before := m.count()
	if _, err := r.Infer(ctx, inputFor(ir), "cfg1", PlacementPlatform); err != nil {
		t.Errorf("a capped tenant was denied its STORED answer, which costs nothing: %v", err)
	}
	if m.count() != before {
		t.Errorf("the cached read made %d provider calls", m.count()-before)
	}
}

// A ceiling of zero cannot be written. `0` is ambiguous between "spend nothing" and "no limit", and a
// checker reading it would have to guess — so removing a cap is a delete.
func TestAZeroCeilingIsRefused(t *testing.T) {
	err := NewMemCapStore().Set(context.Background(), Cap{TenantID: "t1", MaxTokens: 0, Reason: "r"})
	if err == nil {
		t.Error("a ceiling of zero was accepted")
	}
}

// ── task 9.1 · readiness resolved by DOING, not by asserting ─────────────────────────────────────

type stubResolver struct {
	err  error
	kind string
	// asked records the provider it was asked about, so a test can prove the resolution HAPPENED.
	asked string
}

func (s *stubResolver) Resolve(_ context.Context, provider string) error {
	s.asked = provider
	return s.err
}
func (s *stubResolver) Describe() string { return s.kind }

func readinessFixture(t *testing.T, placement Placement, resolver CredentialResolver) (ReadinessInput, *MemCapStore) {
	t.Helper()
	ctx := context.Background()
	versions := NewMemVersionStore()
	if err := versions.Put(ctx, Version{
		ConfigHash: "cfg-live", RehearsalState: RehearsalPassed, CreatedAtMS: 1,
		Definition: Definition{CredentialRef: "anthropic", PromptRef: "p", ModelRef: "m"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := versions.Activate(ctx, "cfg-live", 2); err != nil {
		t.Fatal(err)
	}
	placements := NewMemPlacementStore()
	if placement != PlacementDisabled {
		if err := placements.Set(ctx, TenantPlacement{
			TenantID: "t1", Placement: placement, Reason: "a test", SetBy: "test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	caps := NewMemCapStore()
	checker, err := NewCapChecker(caps, NewMemSpendStore(), func() int64 { return 2_000 })
	if err != nil {
		t.Fatal(err)
	}
	return ReadinessInput{
		Versions: versions, Placements: placements, Credentials: resolver, Caps: checker,
	}, caps
}

// 🔴 THE CREDENTIAL IS RESOLVED, NOT ASSERTED (task 9.1's own words).
//
// The stub records what it was asked, so this proves the resolution HAPPENED against the real provider
// reference — the difference between this signal and the one that reported `components.postgres: ready`
// on a process that had never opened a Postgres connection.
func TestReadinessResolvesTheCredentialRatherThanAssertingIt(t *testing.T) {
	resolver := &stubResolver{err: errors.New("no such secret"), kind: "env"}
	in, _ := readinessFixture(t, PlacementPlatform, resolver)

	got := Check(context.Background(), in)
	if resolver.asked != "anthropic" {
		t.Errorf("the resolver was asked about %q, want the ACTIVE definition's credential reference — "+
			"a readiness check that does not ask cannot report that the answer is no", resolver.asked)
	}
	if got.State != ReadyCredentialUnresolved {
		t.Fatalf("state is %q, want %q", got.State, ReadyCredentialUnresolved)
	}
	if !strings.Contains(got.Detail, "anthropic") || !strings.Contains(got.Detail, "fails closed") {
		t.Errorf("the detail does not name the provider and the consequence: %q", got.Detail)
	}
}

// 🔴 `disabled` and `capped` are NOT faults. A readiness signal that goes red on the default
// configuration, or on a ceiling working as intended, is one an operator learns to ignore.
func TestTheDefaultAndACappedFleetAreNotFaults(t *testing.T) {
	in, _ := readinessFixture(t, PlacementDisabled, &stubResolver{kind: "env"})
	off := Check(context.Background(), in)
	if off.State != ReadyDisabled {
		t.Errorf("state is %q, want %q", off.State, ReadyDisabled)
	}
	if !strings.Contains(off.Detail, "not a fault") {
		t.Errorf("the default state does not say it is not a fault: %q", off.Detail)
	}

	// A fleet ceiling already exceeded.
	ctx := context.Background()
	caps := NewMemCapStore()
	if err := caps.Set(ctx, Cap{TenantID: FleetTenantID, MaxTokens: 1, Reason: "r", SetBy: "t"}); err != nil {
		t.Fatal(err)
	}
	meter := NewMemSpendStore()
	if err := meter.Record(ctx, Spend{TenantID: "t1", InferenceID: "x", TokensIn: 99, CreatedAtMS: 1_500}); err != nil {
		t.Fatal(err)
	}
	checker, err := NewCapChecker(caps, meter, func() int64 { return 2_000 })
	if err != nil {
		t.Fatal(err)
	}
	on, _ := readinessFixture(t, PlacementPlatform, &stubResolver{kind: "env"})
	on.Caps = checker
	capped := Check(ctx, on)
	if capped.State != ReadyCapped {
		t.Fatalf("state is %q, want %q", capped.State, ReadyCapped)
	}
	if !strings.Contains(capped.Detail, "healthy") {
		t.Errorf("a capped deployment does not read as healthy: %q", capped.Detail)
	}
}

// An enabled fleet with NO ceiling says so on the ready line — the state where it is most dangerous
// and least visible, because everything works and nothing is bounded.
func TestAnUnboundedDeploymentSaysSoWhileReportingReady(t *testing.T) {
	in, _ := readinessFixture(t, PlacementPlatform, &stubResolver{kind: "env"})
	in.Caps = nil
	got := Check(context.Background(), in)
	if got.State != ReadyReady {
		t.Fatalf("state is %q, want %q", got.State, ReadyReady)
	}
	if got.CapsEnforced {
		t.Error("caps_enforced is true with no checker wired")
	}
	if !strings.Contains(got.Detail, "NO TOKEN CEILING") {
		t.Errorf("a ready-and-unbounded deployment does not say it is unbounded: %q", got.Detail)
	}
}

// Enabled tenants with no active definition is its own state — a configuration half-done, distinct
// from `disabled`.
func TestEnabledWithNoDefinitionIsItsOwnState(t *testing.T) {
	in, _ := readinessFixture(t, PlacementPlatform, &stubResolver{kind: "env"})
	in.Versions = NewMemVersionStore()
	got := Check(context.Background(), in)
	if got.State != ReadyNoDefinition {
		t.Errorf("state is %q, want %q", got.State, ReadyNoDefinition)
	}
}

// ── task 9.4 · the rollout ladder ────────────────────────────────────────────────────────────────

func passingEvidence(enabled ...string) RolloutEvidence {
	return RolloutEvidence{
		EnabledTenants: enabled, InternalTenants: []string{"internal-1"},
		ActiveConfigHash: "cfg-live", RehearsalState: RehearsalPassed,
		FleetCapSet: true, TotalTenants: 10,
	}
}

// 🔴 A STAGE CANNOT BE SKIPPED. Each stage exists to produce the evidence the next one is decided on.
func TestARolloutStageCannotBeSkipped(t *testing.T) {
	e := passingEvidence() // nobody enabled → StageNone
	if got := CurrentStage(e); got != StageNone {
		t.Fatalf("current stage is %q, want %q", got, StageNone)
	}
	if err := Advance(e, StageOptIn); !errors.Is(err, ErrRolloutStageSkipped) {
		t.Errorf("skipping to opt_in gave %v, want ErrRolloutStageSkipped", err)
	}
	if err := Advance(e, StageInternal); err != nil {
		t.Errorf("the adjacent step was refused: %v", err)
	}
}

// 🔴 THE REHEARSAL GATE IS BETWEEN EVERY PAIR, not only at the start. A definition that served the
// internal tenant and was then REPUBLISHED has its own gate to pass before it reaches a design partner.
func TestTheRehearsalGateSitsBetweenEveryPair(t *testing.T) {
	e := passingEvidence("internal-1") // StageInternal
	e.RehearsalState = RehearsalPending

	if err := Advance(e, StagePartner); !errors.Is(err, ErrRehearsalNotPassed) {
		t.Fatalf("err is %v, want ErrRehearsalNotPassed", err)
	}
	if !strings.Contains(Advance(e, StagePartner).Error(), "EVERY pair") {
		t.Error("the refusal does not say the gate applies between every pair")
	}
}

// A fleet ceiling is required before the first stage that analyses somebody else's repository. The
// internal rung is exempt: it is the rung whose purpose is to find out what an analysis costs.
func TestAFleetCeilingIsRequiredBeforeAnyCustomerIsAnalysed(t *testing.T) {
	e := passingEvidence()
	e.FleetCapSet = false
	if err := Advance(e, StageInternal); err != nil {
		t.Errorf("the internal rung was gated on a ceiling it exists to measure: %v", err)
	}

	e = passingEvidence("internal-1")
	e.FleetCapSet = false
	if err := Advance(e, StagePartner); !errors.Is(err, ErrNoFleetCap) {
		t.Errorf("reaching a customer with no ceiling gave %v, want ErrNoFleetCap", err)
	}
}

// Retreating is never gated. An operator switching tenants off during an incident must not be blocked
// on a rehearsal.
func TestRetreatingIsNeverGated(t *testing.T) {
	e := passingEvidence("internal-1", "partner-1")
	e.RehearsalState = RehearsalFailed
	e.FleetCapSet = false
	if err := Advance(e, StageInternal); err != nil {
		t.Errorf("retreating was refused: %v", err)
	}
	if err := Advance(e, StageNone); err != nil {
		t.Errorf("retreating to none was refused: %v", err)
	}
}

// The stage is DERIVED from the fleet's shape, never stored. A stored stage is a second source of
// truth for what the placement table already says.
func TestTheStageIsDerivedFromTheFleetsActualShape(t *testing.T) {
	for _, c := range []struct {
		name    string
		enabled []string
		total   int
		want    Stage
	}{
		{"nobody", nil, 10, StageNone},
		{"the internal tenant alone", []string{"internal-1"}, 10, StageInternal},
		{"internal plus one partner", []string{"internal-1", "partner-1"}, 10, StagePartner},
		{"a handful", []string{"internal-1", "a", "b"}, 10, StageOptIn},
		{"most of the fleet", []string{"internal-1", "a", "b", "c", "d", "e", "f", "g"}, 10, StageDefaultOn},
		// 🔴 One tenant that is NOT the internal one is `opt_in`, not `internal`. The rung is a claim
		// about WHICH tenant, and a count alone would let a design partner be mistaken for our own box.
		{"one tenant that is not ours", []string{"acme"}, 10, StageOptIn},
	} {
		e := passingEvidence(c.enabled...)
		e.TotalTenants = c.total
		if got := CurrentStage(e); got != c.want {
			t.Errorf("%s: stage is %q, want %q", c.name, got, c.want)
		}
	}
}

// ── task 9.5 · disabling marks stale, and does not delete ────────────────────────────────────────

// 🔴 DISABLING MARKS AND DOES NOT DELETE — asserted on the STORE, not on the vocabulary.
//
// The first version of this test checked only that the two reasons carried distinct sentences, and a
// drill that emptied `StaleDisabled` did NOT turn it red: the sentence table simply re-keyed itself
// under the empty string and every assertion still held. It was a test of a map, in a file named for a
// behaviour. What 9.5 actually promises is that the ROWS SURVIVE, so that is what this reads.
func TestDisablingMarksStaleRatherThanDeleting(t *testing.T) {
	ctx := context.Background()
	store := NewMemInferenceStore()
	for _, id := range []string{"i1", "i2"} {
		if err := store.Put(ctx, Stored{
			InferenceID: id, TenantID: "t1", WorkflowID: "wf-" + id, SourceRevision: "rev",
			AgentConfigHash: "cfg", Placement: PlacementPlatform,
			Edges:       []ProvenancedEdge{{From: "a", To: "b", Kind: "data", Confidence: 0.9}},
			CreatedAtMS: 10,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Another tenant's row, which must not be touched.
	if err := store.Put(ctx, Stored{
		InferenceID: "other", TenantID: "t2", WorkflowID: "wf-x", SourceRevision: "rev",
		AgentConfigHash: "cfg", CreatedAtMS: 10,
	}); err != nil {
		t.Fatal(err)
	}

	n, err := store.MarkStale(ctx, "t1", StaleDisabled, 99)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("marked %d rows, want 2", n)
	}
	// 🔴 THE ROWS SURVIVE. A version that deleted them would return the same count and leave nothing.
	if store.Len() != 3 {
		t.Fatalf("the store holds %d inferences after disabling, want all 3 — disabling must not "+
			"delete: a customer who re-enables would pay twice for the same answers, and `which of "+
			"these edges did the model write` is unanswerable against rows somebody removed", store.Len())
	}
	got, ok, _ := store.Get(ctx, "wf-i1", "rev", "cfg")
	if !ok {
		t.Fatal("a stored inference disappeared when its tenant was disabled")
	}
	if got.StaleReason != StaleDisabled || got.StaleAtMS != 99 {
		t.Errorf("the row is marked %q at %d, want %q with its timestamp — a stale row that cannot "+
			"answer `since when` cannot answer the question the mark exists for",
			got.StaleReason, got.StaleAtMS, StaleDisabled)
	}
	// And the FACTS are still there, still attributed.
	if len(got.Edges) != 1 || got.AgentConfigHash != "cfg" {
		t.Errorf("a stale row lost its facts or its attribution: %+v", got)
	}

	other, ok, _ := store.Get(ctx, "wf-x", "rev", "cfg")
	if !ok || other.StaleReason != "" {
		t.Errorf("another tenant's inference was marked: %+v", other)
	}

	// Re-enabling clears the mark and 🚫 re-runs nothing.
	cleared, err := store.ClearStale(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if cleared != 2 {
		t.Errorf("cleared %d rows, want 2", cleared)
	}
	again, _, _ := store.Get(ctx, "wf-i1", "rev", "cfg")
	if again.StaleReason != "" || again.StaleAtMS != 0 {
		t.Errorf("re-enabling left the mark: %+v", again)
	}

	// An out-of-vocabulary reason is refused rather than written.
	if _, err := store.MarkStale(ctx, "t1", "the model was wrong", 100); err == nil {
		t.Error("a free-text stale reason was accepted")
	}
}

// The vocabulary itself: every member carries its own sentence, and there is no fallback.
func TestEveryStaleReasonCarriesItsOwnSentence(t *testing.T) {
	reasons := StaleReasons()
	if len(reasons) != 2 {
		t.Fatalf("there are %d stale reasons, want 2", len(reasons))
	}
	seen := map[string]bool{}
	for _, r := range reasons {
		s := SentenceForStale(r)
		if s == "" {
			t.Errorf("%s has no sentence", r)
		}
		if seen[s] {
			t.Errorf("%s reuses another reason's sentence", r)
		}
		seen[s] = true
	}
	// 🚫 No generic fallback, for the reason every other sentence table here has none.
	if SentenceForStale("something_else") != "" {
		t.Error("an unknown stale reason resolved to a sentence")
	}
	// The disabled sentence must say the facts are KEPT and that re-enabling does not re-run them —
	// both are the parts a reader would otherwise assume wrongly in opposite directions.
	s := SentenceForStale(StaleDisabled)
	if !strings.Contains(s, "kept") || !strings.Contains(s, "does not re-run") {
		t.Errorf("the disabled sentence does not say the facts are kept and not re-run: %q", s)
	}
}

// The cap window is a rolling period of a stated length, so the same configuration behaves the same
// way every day — a calendar month would make a test written on the 28th pass for reasons it does not
// state.
func TestTheCapWindowIsARollingPeriodRatherThanACalendarMonth(t *testing.T) {
	if CapWindow < 24*time.Hour {
		t.Fatalf("the cap window is %v", CapWindow)
	}
	ctx := context.Background()
	meter := NewMemSpendStore()
	// One row inside the window and one outside it.
	if err := meter.Record(ctx, Spend{TenantID: "t1", InferenceID: "old", TokensIn: 500, CreatedAtMS: 0}); err != nil {
		t.Fatal(err)
	}
	if err := meter.Record(ctx, Spend{TenantID: "t1", InferenceID: "new", TokensIn: 7, CreatedAtMS: 10_000}); err != nil {
		t.Fatal(err)
	}
	got, err := meter.SpentSince(ctx, "t1", 5_000)
	if err != nil {
		t.Fatal(err)
	}
	if got != 7 {
		t.Errorf("spend since 5000 is %d, want only the row inside the window", got)
	}
}
