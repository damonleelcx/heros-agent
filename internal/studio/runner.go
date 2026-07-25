package studio

import (
	"context"
	"time"

	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// runner.go executes a studio test-run through the provider gateway (task 5.1).
//
// Studio is a PLATFORM caller (ADR-002): it calls the gateway itself, exactly as the eval harness
// does, rather than reaching for a provider SDK directly. The transformed customer program still calls
// its own SDKs; the studio does not. That keeps ADR-002's boundary intact while giving the studio a
// real execution against a selected model.

// Completer is the slice of providergateway.Gateway a studio run needs. An interface so the runner can
// be tested without live provider credentials; *providergateway.Gateway satisfies it.
type Completer interface {
	Complete(ctx context.Context, entry *registry.ModelEntry, req providergateway.Request, seed *int64) (*providergateway.Response, error)
}

// Pricer maps a completed call's usage to a dollar cost. Injected because this codebase has no central
// pricing table and derives cost per call (as verification/fanout does with ExpectedCostUSD); a
// deployment supplies its own price book.
type Pricer func(provider, modelID string, u providergateway.Usage) float64

// Runner executes studio test-runs and meters their cost under the studio spend kind.
type Runner struct {
	gw    Completer
	meter *SpendMeter
	price Pricer
	now   func() time.Time
}

// NewRunner builds a studio runner. now defaults to time.Now; a test injects a clock.
func NewRunner(gw Completer, meter *SpendMeter, price Pricer, now func() time.Time) *Runner {
	if now == nil {
		now = time.Now
	}
	return &Runner{gw: gw, meter: meter, price: price, now: now}
}

// Result is a studio test-run's outcome: the output plus the cost, latency and tokens of THAT
// execution (task 4.6). It carries no score, rank, or judgement — a studio result is exploratory.
type Result struct {
	Output       string  `json:"output"`
	CostUSD      float64 `json:"cost_usd"`
	LatencyMS    int64   `json:"latency_ms"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	// Capped is true when the run did NOT execute because the caller had reached a spend cap. It is not
	// a failure — the studio stopped as configured (task 5.3). CapScope names which cap ("user"/"tenant").
	Capped   bool   `json:"capped"`
	CapScope string `json:"cap_scope,omitempty"`
	// Kind labels the result exploratory, on the result itself.
	Kind string `json:"kind"`
}

// Run executes one studio test-run for a caller against a model entry, metering the cost under the
// studio spend kind. If the caller has reached a spend cap, it returns a Capped result WITHOUT
// contacting the provider — the cap stops execution rather than overspending (task 5.4).
func (r *Runner) Run(ctx context.Context, caller Caller, entry *registry.ModelEntry, req providergateway.Request, seed *int64) (*Result, error) {
	if ok, scope := r.meter.Allow(caller); !ok {
		// Configured behaviour, not an error: report the cap and do not call the provider.
		return &Result{Capped: true, CapScope: scope, Kind: "exploratory"}, nil
	}

	start := r.now()
	resp, err := r.gw.Complete(ctx, entry, req, seed)
	if err != nil {
		return nil, err
	}
	latency := r.now().Sub(start).Milliseconds()

	cost := 0.0
	if r.price != nil {
		cost = r.price(resp.Provider, resp.ModelID, resp.Usage)
	}
	// Charge under the studio kind, in the studio meter — never the eval ledger (task 5.2).
	r.meter.Charge(caller, cost)

	return &Result{
		Output:       resp.Content,
		CostUSD:      cost,
		LatencyMS:    latency,
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		Kind:         "exploratory",
	}, nil
}
