package assessment

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// runner_test.go covers the assembler: the budget stop, the evidence gate, and the three ways a report
// can be assembled wrong without anything failing.

// ── Doubles ──────────────────────────────────────────────────────────────────────────────────────

type memStore struct {
	mu   sync.Mutex
	put  []Assessment
	fail error
}

func (m *memStore) Put(_ context.Context, a Assessment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
	}
	m.put = append(m.put, a)
	return nil
}

// allResolve is the resolver a happy path uses. `refuse` names the surfaces that do NOT resolve, so a
// test states what is missing rather than what is present.
type allResolve struct{ refuse map[Surface]bool }

func (a allResolve) Resolves(_ context.Context, _ string, ref EvidenceRef) (bool, error) {
	return !a.refuse[ref.Surface], nil
}

// countingInference answers every axis it is asked about and counts the provider calls.
type countingInference struct {
	mu       sync.Mutex
	calls    int
	spend    float64
	failWith error
}

func (c *countingInference) Infer(_ context.Context, axis Axis, s Subject) (Finding, float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failWith != nil {
		return Finding{}, 0, c.failWith
	}
	c.calls++
	f, err := Inferred(axis, "inferred a strategy for "+string(axis), s.Evidence(),
		"claude-opus-5-20260501", "sha256:"+string(axis))
	return f, c.spend, err
}

func testRunner(t *testing.T, inf Inference, res EvidenceResolver) (*Runner, *memStore) {
	t.Helper()
	store := &memStore{}
	tick := int64(1000)
	r, err := NewRunner(store, res, inf, func() int64 { tick += 5; return tick },
		slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	return r, store
}

func cfg() Config {
	return Config{AssessmentID: "as-1", TenantID: "tn-1", SourceRevision: "rev-1",
		AgentConfigHash: "cfg-1", SpendCapUSD: 1.00}
}

// ── The wiring refusals ──────────────────────────────────────────────────────────────────────────

func TestARunnerWithoutAnEvidenceResolverIsRefused(t *testing.T) {
	_, err := NewRunner(&memStore{}, nil, nil, func() int64 { return 0 }, nil)
	if err == nil || !strings.Contains(err.Error(), "evidence resolver") {
		t.Fatalf("a runner with no resolver was built: %v", err)
	}
}

func TestAZeroSpendCapIsRefused(t *testing.T) {
	r, _ := testRunner(t, nil, allResolve{})
	c := cfg()
	c.SpendCapUSD = 0
	_, err := r.Run(context.Background(), c, subjectFor(t, "python"))
	if err == nil || !strings.Contains(err.Error(), "zero is not") {
		t.Fatalf("a runner accepted an unbounded assessment: %v", err)
	}
}

// ── The happy path ───────────────────────────────────────────────────────────────────────────────

func TestAStructuralOnlyRunPersistsNineFindings(t *testing.T) {
	r, store := testRunner(t, nil, allResolve{})
	a, err := r.Run(context.Background(), cfg(), subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(a.Findings) != len(Axes()) {
		t.Fatalf("got %d findings, want %d", len(a.Findings), len(Axes()))
	}
	if len(store.put) != 1 {
		t.Fatalf("the assessment was not persisted (%d writes)", len(store.put))
	}
	if a.Tally().Inferred != 0 {
		t.Fatal("a structural-only run produced an inferred finding")
	}
	if a.SpendUSD != 0 {
		t.Fatalf("a structural-only run spent $%.2f", a.SpendUSD)
	}
}

// ── The evidence gate (task 2.4) ─────────────────────────────────────────────────────────────────

// TestAnUnresolvableEvidenceReferenceFailsTheWrite is the requirement's teeth. The alternative —
// persist it and let the reader discover it — turns design D5 from a property into a hope, and the
// discovery happens weeks later to the one person least able to fix it.
func TestAnUnresolvableEvidenceReferenceFailsTheWrite(t *testing.T) {
	r, store := testRunner(t, nil, allResolve{refuse: map[Surface]bool{SurfaceGraph: true}})
	_, err := r.Run(context.Background(), cfg(), subjectFor(t, "python"))
	if err == nil || !strings.Contains(err.Error(), "does not resolve") {
		t.Fatalf("an assessment with a dead evidence link was written: %v", err)
	}
	if len(store.put) != 0 {
		t.Fatal("the report was persisted anyway — the check must run BEFORE the write, and before ANY " +
			"of the nine, or a reader gets a report that is partly checked")
	}
}

// ── The budget (task 2.5) ────────────────────────────────────────────────────────────────────────

// TestTheBudgetStopsBeforeTheCallAndDegradesTheRest is §7.3 and QA task 7.7.
//
// 🔴 Two assertions, and the second is the one that matters. The first is that spending stops. The
// second is that the report does NOT shrink: the axes that were never reached are present, in state
// `not_measured`, naming `budget_exhausted` — because an error page loses the axes that DID resolve and
// a shorter report presents a partial answer as a complete one.
func TestTheBudgetStopsBeforeTheCallAndDegradesTheRest(t *testing.T) {
	// One call exhausts a $1.00 cap, so the SECOND residue axis is degraded. The residue is small on
	// purpose: `loop` and `graph` are refused until P34, so only `memory` and `harness` are inferable —
	// which means a per-call spend below the cap could never trip it, and a test written that way would
	// pass by never exercising the branch.
	inf := &countingInference{spend: 1.00}
	r, _ := testRunner(t, inf, allResolve{})
	a, err := r.Run(context.Background(), cfg(), subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("a budget refusal must not be an error: %v", err)
	}
	if inf.calls > 1 {
		t.Fatalf("the runner made %d provider calls under a $1.00 cap at $1.00 each — the check is "+
			"AFTER the call, which is an accounting record and not a cap", inf.calls)
	}
	if !a.Partial() {
		t.Fatal("the report does not say it is partial")
	}
	if len(a.Findings) != len(Axes()) {
		t.Fatalf("the report shrank to %d findings; a budget refusal degrades a STATE, never the axis count",
			len(a.Findings))
	}
	exhausted := 0
	for _, f := range a.Findings {
		if f.MissingInput() == MissingBudgetExhausted {
			exhausted++
			if !strings.Contains(f.Claim(), "cap") {
				t.Fatalf("%s degraded without saying why: %q", f.Axis(), f.Claim())
			}
		}
	}
	if exhausted == 0 {
		t.Fatal("no axis reports budget_exhausted, so the reader is not told the report stopped early")
	}
}

// ── The residue rule (design D2) ─────────────────────────────────────────────────────────────────

// TestInferenceIsAskedOnlyAboutTheResidue is D2 as a call-count assertion.
//
// An axis structural extraction ANSWERED must not be sent to a model: it costs money to have a parse
// contradicted, and it grows the inferred proportion of the report, which is the number D2 exists to
// keep small.
func TestInferenceIsAskedOnlyAboutTheResidue(t *testing.T) {
	inf := &countingInference{}
	r, _ := testRunner(t, inf, allResolve{})
	s := subjectFor(t, "python")

	structural := 0
	for _, e := range Extractors() {
		f, _ := e.Extract(s)
		if f.State() != StateNotMeasured {
			structural++
		}
	}
	a, err := r.Run(context.Background(), cfg(), s)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if inf.calls+structural != len(Axes()) {
		t.Fatalf("%d inference calls plus %d structurally-answered axes is %d, want %d — an axis was "+
			"either asked twice or asked about after being answered",
			inf.calls, structural, inf.calls+structural, len(Axes()))
	}
	for _, f := range a.Findings {
		if f.Origin() == OriginInferred && f.State() == StateRefused {
			t.Fatalf("%s was refused AND inferred: a refusal is a fact about this build, and paying a "+
				"model to contradict it is the one call that can never be worth making", f.Axis())
		}
	}
}

// TestAFailedInferenceLeavesTheStructuralFindingStanding is the rollout plan's rule as a test:
// "rollback is disabling the newest source of findings; the report shrinks in STATE, never in axis
// count". Losing eight good findings because a ninth provider call timed out is the failure shape the
// whole plan is built to avoid.
func TestAFailedInferenceLeavesTheStructuralFindingStanding(t *testing.T) {
	inf := &countingInference{failWith: errors.New("the provider timed out")}
	r, _ := testRunner(t, inf, allResolve{})
	a, err := r.Run(context.Background(), cfg(), subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("a provider failure took down the whole assessment: %v", err)
	}
	if len(a.Findings) != len(Axes()) {
		t.Fatalf("got %d findings, want %d", len(a.Findings), len(Axes()))
	}
	if a.Tally().Inferred != 0 {
		t.Fatal("a failed inference still produced an inferred finding")
	}
}

// ── The seam's own guarantees ────────────────────────────────────────────────────────────────────

type wrongOriginInference struct{}

func (wrongOriginInference) Infer(_ context.Context, axis Axis, s Subject) (Finding, float64, error) {
	f, err := Observed(axis, "a structural-looking claim from the inference seam", s.Evidence())
	return f, 0, err
}

// TestTheInferenceSeamCannotReturnAStructuralFinding is the fence on the one boundary where a model's
// reading could arrive wearing a parser's confidence.
func TestTheInferenceSeamCannotReturnAStructuralFinding(t *testing.T) {
	r, _ := testRunner(t, wrongOriginInference{}, allResolve{})
	_, err := r.Run(context.Background(), cfg(), subjectFor(t, "python"))
	if err == nil || !strings.Contains(err.Error(), "wearing a parser's confidence") {
		t.Fatalf("the seam returned a structural finding and it was accepted: %v", err)
	}
}

type wrongAxisInference struct{}

func (wrongAxisInference) Infer(_ context.Context, _ Axis, s Subject) (Finding, float64, error) {
	f, err := Inferred(AxisModel, "a claim about the wrong axis", s.Evidence(), "m", "a")
	return f, 0, err
}

func TestTheInferenceSeamCannotAnswerADifferentAxis(t *testing.T) {
	r, _ := testRunner(t, wrongAxisInference{}, allResolve{})
	_, err := r.Run(context.Background(), cfg(), subjectFor(t, "python"))
	if err == nil || !strings.Contains(err.Error(), "returned a finding for") {
		t.Fatalf("an answer for the wrong axis was accepted, which would leave the report one axis "+
			"short and one axis doubled: %v", err)
	}
}
