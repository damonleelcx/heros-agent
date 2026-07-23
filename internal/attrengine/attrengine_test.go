package attrengine

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/evalrun"
	"github.com/heros-foreal/agentd/internal/evalstats"
)

// ─────────────────────────────────────────────────────────────────────────────
// stubs
// ─────────────────────────────────────────────────────────────────────────────

// stubExecutor is a sandboxed unit executor. It records how many times it actually ran (so a test can
// prove idempotent re-delivery did not re-execute) and can gate concurrency to observe the bound.
type stubExecutor struct {
	sandboxed  bool
	costUSD    float64
	runs       int32
	block      chan struct{} // if non-nil, Execute blocks on it to hold units in-flight
	concurrent int32
	maxConc    int32
}

func (e *stubExecutor) Sandboxed() bool { return e.sandboxed }

func (e *stubExecutor) Execute(_ context.Context, unit AblationUnit, v attribution.Variant, metric string) (UnitResult, error) {
	atomic.AddInt32(&e.runs, 1)
	c := atomic.AddInt32(&e.concurrent, 1)
	for {
		m := atomic.LoadInt32(&e.maxConc)
		if c <= m || atomic.CompareAndSwapInt32(&e.maxConc, m, c) {
			break
		}
	}
	if e.block != nil {
		<-e.block
	}
	atomic.AddInt32(&e.concurrent, -1)
	// One observation per (case, seed): two cases, deterministic values.
	return UnitResult{
		Obs: []evalstats.Observation{
			{CaseID: "case-a", Seed: unit.Seed, Value: 0.5},
			{CaseID: "case-b", Seed: unit.Seed, Value: 0.5},
		},
		CostUSD: e.costUSD,
	}, nil
}

func testVariant() attribution.Variant {
	return attribution.Variant{VariantID: "v", ConfigHash: "cfg", EvalSetHash: "es", WorkflowID: "wf"}
}

// Task 9.4: the fan-out runner REFUSES a non-sandboxed executor.
func TestNewFanoutAblationRunner_RefusesUnsandboxed(t *testing.T) {
	_, err := NewFanoutAblationRunner(&stubExecutor{sandboxed: false}, nil, 4)
	if err == nil {
		t.Fatal("expected refusal of a non-sandboxed executor")
	}
	if _, err := NewFanoutAblationRunner(&stubExecutor{sandboxed: true}, nil, 4); err != nil {
		t.Fatalf("sandboxed executor should be accepted: %v", err)
	}
}

// Task 9.2: bounded concurrency — the fan-out never exceeds the configured bound.
func TestFanout_BoundedConcurrency(t *testing.T) {
	block := make(chan struct{})
	exec := &stubExecutor{sandboxed: true, block: block}
	runner, err := NewFanoutAblationRunner(exec, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	var series evalstats.Series
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		series, _ = runner.Baseline(context.Background(), testVariant(), "m", []int64{0, 1, 2, 3, 4, 5, 6, 7})
	}()
	// Give the workers time to saturate the semaphore, then release.
	time.Sleep(50 * time.Millisecond)
	close(block)
	wg.Wait()

	if runner.MaxInFlight() > 3 {
		t.Fatalf("peak in-flight %d exceeded the bound of 3", runner.MaxInFlight())
	}
	if len(series.Obs) != 16 { // 8 seeds × 2 cases
		t.Errorf("series has %d obs, want 16", len(series.Obs))
	}
}

// Task 9.2: idempotent re-delivery — re-running the same seeds does not re-execute or double-charge.
func TestFanout_IdempotentNoDoubleCharge(t *testing.T) {
	exec := &stubExecutor{sandboxed: true, costUSD: 0.01}
	total := 1.0
	meter := evalrun.NewMeter("run", evalrun.Budget{TotalUSD: &total})
	runner, err := NewFanoutAblationRunner(exec, meter, 4)
	if err != nil {
		t.Fatal(err)
	}
	seeds := []int64{0, 1, 2, 3, 4}
	if _, err := runner.Baseline(context.Background(), testVariant(), "m", seeds); err != nil {
		t.Fatal(err)
	}
	firstRuns := atomic.LoadInt32(&exec.runs)
	firstSpend := meter.Report().TotalUSD

	// Re-deliver the exact same units.
	if _, err := runner.Baseline(context.Background(), testVariant(), "m", seeds); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&exec.runs); got != firstRuns {
		t.Errorf("re-delivery re-executed units: %d → %d (must be idempotent)", firstRuns, got)
	}
	if got := meter.Report().TotalUSD; got != firstSpend {
		t.Errorf("re-delivery double-charged: $%.4f → $%.4f", firstSpend, got)
	}
	if firstSpend != 0.05 { // 5 seeds × $0.01
		t.Errorf("ablation spend = $%.4f, want $0.05", firstSpend)
	}
}

// Task 9.3: the ablation spend cap is enforced — a fan-out that would breach the cap fails rather than
// silently overspending.
func TestFanout_SpendCapEnforced(t *testing.T) {
	exec := &stubExecutor{sandboxed: true, costUSD: 0.10}
	cap := 0.25
	meter := evalrun.NewMeter("run", evalrun.Budget{TotalUSD: &cap})
	runner, err := NewFanoutAblationRunner(exec, meter, 1) // serialize so the cap bites deterministically
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Baseline(context.Background(), testVariant(), "m", []int64{0, 1, 2, 3, 4})
	if err == nil {
		t.Fatal("expected the spend cap to stop the fan-out")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Errorf("expected a budget-exhausted error, got %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// metered analyst (9.3): rules-first ⇒ most cases cost nothing
// ─────────────────────────────────────────────────────────────────────────────

type stubAnalyst struct{ calls int }

func (a *stubAnalyst) Analyze(_ context.Context, fc attribution.FailingCase, _ diagnosis.Rubric) (diagnosis.AnalystResponse, error) {
	a.calls++
	return diagnosis.AnalystResponse{Code: string(diagnosis.CauseNonConvergence), Confidence: 0.8}, nil
}

func TestMeteredAnalyst_ChargesPerCallAndCaps(t *testing.T) {
	inner := &stubAnalyst{}
	total := 0.05
	meter := evalrun.NewMeter("run", evalrun.Budget{TotalUSD: &total})
	analyst := NewMeteredAnalyst(inner, meter, 0.02)

	ctx := context.Background()
	fc := attribution.FailingCase{}
	// Two calls fit ($0.04 ≤ $0.05); the third breaches.
	if _, err := analyst.Analyze(ctx, fc, diagnosis.Rubric{}); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if _, err := analyst.Analyze(ctx, fc, diagnosis.Rubric{}); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if _, err := analyst.Analyze(ctx, fc, diagnosis.Rubric{}); err == nil {
		t.Fatal("call 3 should have breached the cap")
	}
	// The breaching call must NOT have reached the inner analyst.
	if inner.calls != 2 {
		t.Errorf("inner analyst called %d times, want 2 (the capped call must not delegate)", inner.calls)
	}
	if got := meter.Report().ByKind[evalrun.SpendJudge]; got != 0.04 {
		t.Errorf("analyst spend = $%.4f, want $0.04", got)
	}
}

// Task 9.4: content-hash discipline — a payload is never logged inline; only its ref appears.
func TestLogSafe_NeverInlinesPayload(t *testing.T) {
	secret := []byte(`{"user":"alice@example.com","ssn":"123-45-6789"}`)
	got := LogSafe(secret)
	if strings.Contains(got, "alice") || strings.Contains(got, "6789") {
		t.Fatalf("LogSafe leaked payload content: %q", got)
	}
	if !strings.HasPrefix(got, "blob:") {
		t.Errorf("LogSafe should render a blob ref, got %q", got)
	}
	// Same payload → same ref (content-addressed); different payload → different ref.
	if LogSafe(secret) != got {
		t.Error("content ref not stable")
	}
	if LogSafe([]byte("other")) == got {
		t.Error("distinct payloads collided")
	}
}
