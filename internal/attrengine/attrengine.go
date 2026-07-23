// Package attrengine is the P4.5 DevOps wiring (§9): read-only trace access for the attribution
// engine, the ablation fan-out on the P4 run queue (bounded concurrency, backpressure, idempotent
// re-delivery), the analyst + ablation spend meter and caps, the P3-sandbox / no-ambient-credentials
// guarantee for ablation re-runs, and the content-hash discipline that keeps possibly-PII payloads
// out of logs.
//
// It holds the operational concerns so the attribution/diagnosis engines stay pure: those packages
// take an AblationRunner and an Analyst as interfaces, and this package supplies the metered,
// sandboxed, idempotent implementations behind them.
package attrengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/evalrun"
	"github.com/heros-foreal/agentd/internal/evalstats"
)

// ─────────────────────────────────────────────────────────────────────────────
// 9.1 — read-only trace access
// ─────────────────────────────────────────────────────────────────────────────

// TraceSource is the attribution engine's READ-ONLY window onto the P2.5 span store / TSDB. It has
// only read methods — there is no write, so the engine cannot mutate a trace or an eval result through
// it. This is the same structural discipline as the report store: the read-only property is a shape of
// the interface, not a convention.
type TraceSource interface {
	// FailingCases returns the failing cases (case + trace) for a variant over an eval set, read from
	// the span store. It is the only trace access the attribution engine has.
	FailingCases(ctx context.Context, v attribution.Variant) ([]attribution.FailingCase, error)
}

// ─────────────────────────────────────────────────────────────────────────────
// 9.3 — metered analyst (rules-first ⇒ most cases cost nothing)
// ─────────────────────────────────────────────────────────────────────────────

// AnalystCostUSD is the metered cost charged per analyst call. It is a value, not a guess buried in a
// provider adapter, so a spend report can attribute the analyst line exactly.
const AnalystCostUSD = 0.02

// MeteredAnalyst wraps a diagnosis.Analyst and charges the eval-run meter for every call, enforcing
// the analyst spend cap. Because the diagnosis orchestrator calls the analyst ONLY on the rules-first
// residue, a run whose rules explain everything charges the analyst nothing — the rules-first
// discipline is what keeps the analyst spend bounded, and this wrapper is where that spend is metered
// and capped (task 9.3). A charge that would breach the cap returns ErrBudgetExhausted and the
// underlying analyst is NOT called.
type MeteredAnalyst struct {
	inner   diagnosis.Analyst
	meter   *evalrun.Meter
	costUSD float64
}

// NewMeteredAnalyst wraps an analyst with spend metering. costUSD ≤ 0 uses AnalystCostUSD.
func NewMeteredAnalyst(inner diagnosis.Analyst, meter *evalrun.Meter, costUSD float64) *MeteredAnalyst {
	if costUSD <= 0 {
		costUSD = AnalystCostUSD
	}
	return &MeteredAnalyst{inner: inner, meter: meter, costUSD: costUSD}
}

// Analyze charges the meter (as judge-kind spend — the analyst is an LLM call, metered exactly like
// the P4 judge) then delegates. The cap is checked BEFORE the call, so an exhausted budget prevents
// the spend rather than reporting it after the invoice.
func (a *MeteredAnalyst) Analyze(ctx context.Context, fc attribution.FailingCase, rubric diagnosis.Rubric) (diagnosis.AnalystResponse, error) {
	if a.meter != nil {
		if err := a.meter.Charge(evalrun.SpendJudge, a.costUSD); err != nil {
			return diagnosis.AnalystResponse{}, err
		}
	}
	return a.inner.Analyze(ctx, fc, rubric)
}

// ─────────────────────────────────────────────────────────────────────────────
// 9.2 + 9.4 — ablation fan-out: bounded concurrency, idempotent re-delivery,
//             sandboxed with no ambient credentials, spend-metered
// ─────────────────────────────────────────────────────────────────────────────

// AblationUnit is one measurement unit of an ablation: one seed of the (possibly ablated) variant.
// RunID is derived from the unit's coordinates so a re-delivery of the same unit recomputes the SAME
// id — the P2 idempotency identity, which is what makes a redelivered unit collapse instead of
// double-charging.
type AblationUnit struct {
	RunID      string
	Seed       int64
	Node       string
	SwappedRef string
	Baseline   bool
}

// UnitResult is one seed's per-case observations plus what that seed cost to run.
type UnitResult struct {
	Obs     []evalstats.Observation
	CostUSD float64
}

// SandboxedExecutor runs one ablation unit. It MUST execute in the P3 sandbox with no ambient
// credentials (task 9.4) — Sandboxed() reports that, and the fan-out runner refuses to construct
// against an executor that answers false. It is an interface so the real executor (P4 harness on the
// sandbox) is wired here and tests stub it.
type SandboxedExecutor interface {
	Execute(ctx context.Context, unit AblationUnit, v attribution.Variant, metric string) (UnitResult, error)
	// Sandboxed reports whether the executor runs in the P3 sandbox with no ambient credentials.
	Sandboxed() bool
}

// ErrNotSandboxed is returned when the fan-out runner is asked to run against an executor that is not
// the P3 sandbox — ablation re-runs execute DISCOVERED code, so an unsandboxed executor with ambient
// credentials is refused outright, not merely warned about.
var ErrNotSandboxed = fmt.Errorf("attrengine: ablation executor is not the P3 sandbox with no ambient credentials")

// FanoutAblationRunner implements attribution.AblationRunner over the P4 run queue: it fans out an
// ablation's seeds with bounded concurrency, charges each unique unit's execution to the meter once
// (idempotent re-delivery — no double-charge), and assembles the multi-seed Series the P4 statistics
// consume.
type FanoutAblationRunner struct {
	exec        SandboxedExecutor
	meter       *evalrun.Meter
	concurrency int

	mu          sync.Mutex
	cache       map[string]UnitResult // run_id → result, for idempotent re-delivery
	inFlight    int
	maxInFlight int
}

// NewFanoutAblationRunner builds the runner. It REFUSES a non-sandboxed executor: ablation re-runs
// discovered code, and doing so with ambient credentials is the one thing this phase must never do.
func NewFanoutAblationRunner(exec SandboxedExecutor, meter *evalrun.Meter, concurrency int) (*FanoutAblationRunner, error) {
	if exec == nil || !exec.Sandboxed() {
		return nil, ErrNotSandboxed
	}
	if concurrency <= 0 {
		concurrency = 8
	}
	return &FanoutAblationRunner{exec: exec, meter: meter, concurrency: concurrency, cache: map[string]UnitResult{}}, nil
}

// MaxInFlight is the peak concurrency observed — exported so a test can assert the fan-out was
// actually bounded rather than trusting the semaphore.
func (r *FanoutAblationRunner) MaxInFlight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxInFlight
}

// Baseline runs the variant as-is, multi-seed, and returns its metric series.
func (r *FanoutAblationRunner) Baseline(ctx context.Context, v attribution.Variant, metric string, seeds []int64) (evalstats.Series, error) {
	return r.fanout(ctx, v, metric, seeds, "", "", true)
}

// Ablated runs the variant with only `node`'s config swapped to swappedConfigRef, multi-seed.
func (r *FanoutAblationRunner) Ablated(ctx context.Context, v attribution.Variant, node, swappedConfigRef, metric string, seeds []int64) (evalstats.Series, error) {
	return r.fanout(ctx, v, metric, seeds, node, swappedConfigRef, false)
}

// fanout runs the units with bounded concurrency and assembles the series. Backpressure is the bounded
// semaphore: at most `concurrency` units execute at once, the rest wait — a slow provider slows the
// fan-out instead of stampeding it.
func (r *FanoutAblationRunner) fanout(ctx context.Context, v attribution.Variant, metric string, seeds []int64, node, ref string, baseline bool) (evalstats.Series, error) {
	series := evalstats.Series{VariantID: v.VariantID, ConfigHash: v.ConfigHash, Metric: metric}

	sem := make(chan struct{}, r.concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for _, seed := range seeds {
		unit := AblationUnit{
			RunID:      r.runID(v, node, ref, baseline, seed),
			Seed:       seed,
			Node:       node,
			SwappedRef: ref,
			Baseline:   baseline,
		}
		wg.Add(1)
		go func(unit AblationUnit) {
			defer wg.Done()
			sem <- struct{}{}
			r.enter()
			defer func() { r.leave(); <-sem }()

			res, err := r.execUnit(ctx, unit, v, metric)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			series.Obs = append(series.Obs, res.Obs...)
		}(unit)
	}
	wg.Wait()
	if firstErr != nil {
		return evalstats.Series{}, firstErr
	}
	// Sort observations for a deterministic series regardless of goroutine completion order.
	sort.Slice(series.Obs, func(i, j int) bool {
		if series.Obs[i].CaseID != series.Obs[j].CaseID {
			return series.Obs[i].CaseID < series.Obs[j].CaseID
		}
		return series.Obs[i].Seed < series.Obs[j].Seed
	})
	return series, nil
}

// execUnit executes one unit, charging its cost to the meter EXACTLY ONCE per unique run id. A
// re-delivered unit (same run id) returns the cached result without charging again — the idempotency
// that keeps a redelivery from double-charging (task 9.2, inheriting P2).
func (r *FanoutAblationRunner) execUnit(ctx context.Context, unit AblationUnit, v attribution.Variant, metric string) (UnitResult, error) {
	r.mu.Lock()
	if cached, ok := r.cache[unit.RunID]; ok {
		r.mu.Unlock()
		return cached, nil // idempotent re-delivery: no re-charge
	}
	r.mu.Unlock()

	res, err := r.exec.Execute(ctx, unit, v, metric)
	if err != nil {
		return UnitResult{}, err
	}
	if r.meter != nil {
		if err := r.meter.Charge(evalrun.SpendExecution, res.CostUSD); err != nil {
			return UnitResult{}, err
		}
	}
	r.mu.Lock()
	r.cache[unit.RunID] = res
	r.mu.Unlock()
	return res, nil
}

func (r *FanoutAblationRunner) enter() {
	r.mu.Lock()
	r.inFlight++
	if r.inFlight > r.maxInFlight {
		r.maxInFlight = r.inFlight
	}
	r.mu.Unlock()
}

func (r *FanoutAblationRunner) leave() {
	r.mu.Lock()
	r.inFlight--
	r.mu.Unlock()
}

// runID derives the idempotency identity of an ablation unit from the eval-run coordinates via the
// same RunIDFor primitive the P4 queue uses, domain-separated by the ablation's node + swapped ref so
// a baseline unit and an ablated unit of the same seed never collide.
func (r *FanoutAblationRunner) runID(v attribution.Variant, node, ref string, baseline bool, seed int64) string {
	tag := "ablation:" + node + ":" + ref
	if baseline {
		tag = "ablation:baseline"
	}
	return evalrun.RunIDFor(v.ConfigHash, tag, v.EvalSetHash, seed)
}

// ─────────────────────────────────────────────────────────────────────────────
// 9.4 — content-hash discipline: possibly-PII payloads never inline in logs
// ─────────────────────────────────────────────────────────────────────────────

// ContentRef returns the content hash of a payload (trace excerpt, analyst prompt, embedding) — the
// ONLY form in which it may appear in a log or a report row. The payload itself goes to the object
// store under this hash; the hash is safe to log because it reveals nothing about the (possibly PII)
// content.
func ContentRef(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// LogSafe renders a payload for a log line as its content ref, never its bytes. Using it everywhere a
// trace excerpt or analyst prompt would otherwise be logged is what keeps user data out of the logs.
func LogSafe(payload []byte) string {
	return "blob:" + ContentRef(payload)[:16]
}

// compile-time assertion that the fan-out runner satisfies the attribution AblationRunner contract.
var _ attribution.AblationRunner = (*FanoutAblationRunner)(nil)
