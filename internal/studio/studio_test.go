package studio

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/evalrun"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// fakeCompleter counts provider calls, so a test can prove a capped run did NOT contact the provider.
type fakeCompleter struct {
	calls int
	usage providergateway.Usage
}

func (f *fakeCompleter) Complete(_ context.Context, _ *registry.ModelEntry, _ providergateway.Request, _ *int64) (*providergateway.Response, error) {
	f.calls++
	return &providergateway.Response{
		Content: "ok", Provider: "openai", ModelID: "gpt-x",
		Usage: f.usage,
	}, nil
}

func usd(v float64) *float64 { return &v }

// flatPrice charges a fixed dollar amount per call regardless of usage — enough to drive the cap.
func flatPrice(amount float64) Pricer {
	return func(_, _ string, _ providergateway.Usage) float64 { return amount }
}

func modelEntry() *registry.ModelEntry {
	return &registry.ModelEntry{VersionID: "m1", Name: "m", Spec: registry.ModelSpec{Provider: "openai", ModelID: "gpt-x"}}
}

// Task 5.4: the cap STOPS execution rather than overspending. With a $1 per-user cap and $0.60/call, a
// third call must be refused BEFORE the provider is contacted.
func TestRunner_CapStopsExecutionRatherThanOverspending(t *testing.T) {
	fc := &fakeCompleter{usage: providergateway.Usage{InputTokens: 10, OutputTokens: 5}}
	meter := NewSpendMeter(Cap{PerUserUSD: usd(1.0)})
	r := NewRunner(fc, meter, flatPrice(0.60), func() time.Time { return time.Unix(0, 0) })
	caller := Caller{TenantID: "t", UserID: "u"}
	ctx := context.Background()

	r1, _ := r.Run(ctx, caller, modelEntry(), providergateway.Request{}, nil)
	if r1.Capped {
		t.Fatal("first run should execute")
	}
	r2, _ := r.Run(ctx, caller, modelEntry(), providergateway.Request{}, nil)
	if r2.Capped {
		t.Fatal("second run should execute (total 1.20 recorded only after)")
	}
	// After two calls the running total is 1.20 >= 1.00, so the third is refused before the provider.
	callsBefore := fc.calls
	r3, _ := r.Run(ctx, caller, modelEntry(), providergateway.Request{}, nil)
	if !r3.Capped {
		t.Fatal("third run must be capped")
	}
	if r3.CapScope != "user" {
		t.Fatalf("cap scope = %q, want user", r3.CapScope)
	}
	if fc.calls != callsBefore {
		t.Fatal("a capped run must NOT contact the provider — that is overspending")
	}
}

func TestRunner_PerTenantCapBoundsSumOfUsers(t *testing.T) {
	fc := &fakeCompleter{}
	meter := NewSpendMeter(Cap{PerTenantUSD: usd(1.0)})
	r := NewRunner(fc, meter, flatPrice(0.60), func() time.Time { return time.Unix(0, 0) })
	ctx := context.Background()

	// Two different users in the same tenant; together they exceed the tenant cap.
	r.Run(ctx, Caller{TenantID: "t", UserID: "u1"}, modelEntry(), providergateway.Request{}, nil)
	r.Run(ctx, Caller{TenantID: "t", UserID: "u2"}, modelEntry(), providergateway.Request{}, nil)
	res, _ := r.Run(ctx, Caller{TenantID: "t", UserID: "u3"}, modelEntry(), providergateway.Request{}, nil)
	if !res.Capped || res.CapScope != "tenant" {
		t.Fatalf("tenant cap should bound the sum of users; got capped=%v scope=%q", res.Capped, res.CapScope)
	}
}

func TestRunner_RecordsCostLatencyTokensUnderStudioKind(t *testing.T) {
	fc := &fakeCompleter{usage: providergateway.Usage{InputTokens: 100, OutputTokens: 40}}
	meter := NewSpendMeter(Cap{})
	r := NewRunner(fc, meter, flatPrice(0.25), func() time.Time { return time.Unix(0, 0) })
	caller := Caller{TenantID: "t", UserID: "u"}

	res, err := r.Run(context.Background(), caller, modelEntry(), providergateway.Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "ok" || res.CostUSD != 0.25 || res.InputTokens != 100 || res.OutputTokens != 40 {
		t.Fatalf("result did not carry output/cost/tokens: %+v", res)
	}
	rep := meter.Report(caller)
	if rep.ByKind[SpendStudio] != 0.25 {
		t.Fatalf("studio spend not recorded under the studio kind: %+v", rep.ByKind)
	}
}

// Task 5.4: studio cost never appears within eval cost. The two ledgers are different objects, so a
// studio charge is invisible to an evalrun report and vice versa — a structural guarantee, asserted.
func TestSpend_StudioAndEvalLedgersAreDisjoint(t *testing.T) {
	studioMeter := NewSpendMeter(Cap{})
	caller := Caller{TenantID: "t", UserID: "u"}
	studioMeter.Charge(caller, 5.00)

	evalMeter := evalrun.NewMeter("run-1", evalrun.Budget{})
	if err := evalMeter.Charge(evalrun.SpendExecution, 2.00); err != nil {
		t.Fatal(err)
	}

	// The eval report contains only eval spend; the studio total is nowhere in it.
	evalReport := evalMeter.Report()
	for kind, amount := range evalReport.ByKind {
		if string(kind) == string(SpendStudio) {
			t.Fatalf("studio kind leaked into the eval report")
		}
		if amount == 5.00 {
			t.Fatalf("studio spend (5.00) appeared in eval cost under kind %q", kind)
		}
	}
	// And the studio report contains only studio spend.
	studioReport := studioMeter.Report(caller)
	if studioReport.TotalUSD != 5.00 {
		t.Fatalf("studio total = %v, want 5.00", studioReport.TotalUSD)
	}
	if _, ok := studioReport.ByKind[SpendStudio]; !ok {
		t.Fatal("studio report missing the studio kind")
	}
}
