package evalrun

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/heros-foreal/agentd/internal/evalharness"
)

// spend.go is DevOps task 6.4: meter and CAP judge/generation spend per eval run, and surface it.
//
// The failure it prevents is specific and expensive: measuring a workflow is itself a workload. An
// eval run over 200 cases x 5 seeds with an LLM judge on each is a thousand paid judge calls, and a
// gap-filling loop pointed at an unreachable path will happily ask a generator for cases forever.
// Both are invisible until the invoice arrives, which is why the cap is enforced at the call site
// and the running total is readable at any moment rather than reconciled afterwards.

// SpendKind separates what the money went on, because "the eval run cost $40" is not actionable and
// "the judge cost $38 of the $40" is.
type SpendKind string

const (
	SpendJudge      SpendKind = "judge"
	SpendGeneration SpendKind = "generation"
	SpendExecution  SpendKind = "execution"
)

// SpendKinds is the closed vocabulary, in report order.
var SpendKinds = []SpendKind{SpendExecution, SpendJudge, SpendGeneration}

// ErrBudgetExhausted is returned when a charge would exceed the cap. It is an ERROR at the call
// site, not a warning after the fact: a cap that logs is a cap that gets discovered on the invoice.
var ErrBudgetExhausted = errors.New("evalrun: eval-run budget exhausted")

// Budget is the per-eval-run spend cap. A nil limit means uncapped for that kind — expressed as a
// pointer so "no cap configured" is distinguishable from "capped at zero", which would refuse
// everything.
type Budget struct {
	TotalUSD      *float64 `json:"total_usd,omitempty"`
	JudgeUSD      *float64 `json:"judge_usd,omitempty"`
	GenerationUSD *float64 `json:"generation_usd,omitempty"`
	// MaxJudgeCalls caps the COUNT as well as the dollars. A cheap model can run a very long way
	// inside a dollar cap, and an unbounded call count is an unbounded wall-clock.
	MaxJudgeCalls int `json:"max_judge_calls,omitempty"`
	MaxGenCalls   int `json:"max_generation_calls,omitempty"`
}

// SpendReport is the surfaced total.
type SpendReport struct {
	EvalRunID   string                `json:"eval_run_id"`
	ByKind      map[SpendKind]float64 `json:"by_kind"`
	CallsByKind map[SpendKind]int     `json:"calls_by_kind"`
	TotalUSD    float64               `json:"total_usd"`
	Budget      Budget                `json:"budget"`
	// Exhausted names the caps that have been hit, so the UI can say WHICH limit stopped the run.
	Exhausted []string `json:"exhausted,omitempty"`
}

// Meter accumulates and caps spend for one eval run. Safe for concurrent use because the fan-out
// charges from many workers at once.
type Meter struct {
	evalRunID string
	budget    Budget

	mu     sync.Mutex
	byKind map[SpendKind]float64
	calls  map[SpendKind]int
	hit    map[string]bool
}

// NewMeter builds a spend meter for one eval run.
func NewMeter(evalRunID string, b Budget) *Meter {
	return &Meter{
		evalRunID: evalRunID,
		budget:    b,
		byKind:    map[SpendKind]float64{},
		calls:     map[SpendKind]int{},
		hit:       map[string]bool{},
	}
}

// Charge records spend AFTER checking that it fits. It returns ErrBudgetExhausted without recording
// anything when the charge would breach a cap — the caller must then not make the call.
//
// Check-then-charge in ONE locked step matters under fan-out: a read-then-charge would let twenty
// concurrent workers each see room for one more judge call and all twenty make it.
func (m *Meter) Charge(kind SpendKind, usd float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.wouldBreach(kind, usd); err != nil {
		return err
	}
	m.byKind[kind] += usd
	m.calls[kind]++
	return nil
}

// wouldBreach reports the first cap the charge would exceed. Caller holds the lock.
func (m *Meter) wouldBreach(kind SpendKind, usd float64) error {
	total := 0.0
	for _, v := range m.byKind {
		total += v
	}
	if m.budget.TotalUSD != nil && total+usd > *m.budget.TotalUSD {
		m.hit["total_usd"] = true
		return fmt.Errorf("%w: total $%.4f + $%.4f exceeds the $%.4f cap for eval run %s",
			ErrBudgetExhausted, total, usd, *m.budget.TotalUSD, m.evalRunID)
	}
	switch kind {
	case SpendJudge:
		if m.budget.JudgeUSD != nil && m.byKind[SpendJudge]+usd > *m.budget.JudgeUSD {
			m.hit["judge_usd"] = true
			return fmt.Errorf("%w: judge spend $%.4f + $%.4f exceeds the $%.4f cap",
				ErrBudgetExhausted, m.byKind[SpendJudge], usd, *m.budget.JudgeUSD)
		}
		if m.budget.MaxJudgeCalls > 0 && m.calls[SpendJudge]+1 > m.budget.MaxJudgeCalls {
			m.hit["max_judge_calls"] = true
			return fmt.Errorf("%w: judge call %d exceeds the %d-call cap",
				ErrBudgetExhausted, m.calls[SpendJudge]+1, m.budget.MaxJudgeCalls)
		}
	case SpendGeneration:
		if m.budget.GenerationUSD != nil && m.byKind[SpendGeneration]+usd > *m.budget.GenerationUSD {
			m.hit["generation_usd"] = true
			return fmt.Errorf("%w: generation spend $%.4f + $%.4f exceeds the $%.4f cap",
				ErrBudgetExhausted, m.byKind[SpendGeneration], usd, *m.budget.GenerationUSD)
		}
		if m.budget.MaxGenCalls > 0 && m.calls[SpendGeneration]+1 > m.budget.MaxGenCalls {
			m.hit["max_generation_calls"] = true
			return fmt.Errorf("%w: generation call %d exceeds the %d-call cap",
				ErrBudgetExhausted, m.calls[SpendGeneration]+1, m.budget.MaxGenCalls)
		}
	}
	return nil
}

// Report surfaces the running total. It is readable at any moment — mid-fan-out included — because
// "how much has this measurement cost so far?" is a question that must be answerable before the run
// ends, not after.
func (m *Meter) Report() SpendReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := SpendReport{
		EvalRunID:   m.evalRunID,
		ByKind:      map[SpendKind]float64{},
		CallsByKind: map[SpendKind]int{},
		Budget:      m.budget,
	}
	for _, k := range SpendKinds {
		r.ByKind[k] = m.byKind[k]
		r.CallsByKind[k] = m.calls[k]
		r.TotalUSD += m.byKind[k]
	}
	for name := range m.hit {
		r.Exhausted = append(r.Exhausted, name)
	}
	sort.Strings(r.Exhausted)
	return r
}

// ─────────────────────────────────────────────────────────────────────────────
// Metered judge
// ─────────────────────────────────────────────────────────────────────────────

// MeteredJudge wraps a JudgeModel so every judge call is charged before it is made. It is the
// enforcement point: an evaluator cannot spend outside the meter because the only judge it holds is
// this one.
type MeteredJudge struct {
	Inner evalharness.JudgeModel
	Meter *Meter
	// CostPerCall is the estimated cost of one judge call, charged up front. Estimating before the
	// call rather than reconciling after is deliberate: a cap that only knows what a call cost
	// AFTER making it cannot prevent the call that breaches it.
	CostPerCall float64
}

// Judge charges the meter, then delegates. A breached cap refuses the call.
func (j *MeteredJudge) Judge(ctx context.Context, req evalharness.JudgeRequest) (evalharness.RawVerdict, error) {
	if j.Meter != nil {
		if err := j.Meter.Charge(SpendJudge, j.CostPerCall); err != nil {
			return evalharness.RawVerdict{}, err
		}
	}
	return j.Inner.Judge(ctx, req)
}
