package improvementrun

import (
	"context"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/optimizer"
	"github.com/heros-foreal/agentd/internal/verification"
)

// looptest_helpers_test.go drives the REAL `optimizer.Controller`, never a stand-in.
//
// 🔴 That is the whole point of task 2.4's fence. A helper that simulated the loop would prove the
// simulation obeys the plan's bounds and say nothing about the loop the product runs — which is exactly
// the failure "no fork of the loop" exists to prevent, one level down in the tests.

// runLoop runs the shipped controller with the given enumerator and a candidate cap, and returns the
// terminal result.
func runLoop(t *testing.T, enum optimizer.Enumerator, cap int) optimizer.RunResult {
	t.Helper()
	repo := optimizer.NewFakeRepo([]byte(`{"baseline":true}`))
	ctrl := &optimizer.Controller{
		Search:   optimizer.Search{Enum: enum},
		Verifier: optimizer.StaticVerifier{ByConfig: map[string]optimizer.VerifyResult{}, Spend: 0.01},
		Repo:     repo,
		Ledger:   optimizer.NewMemLedger(),
		Kill:     optimizer.NewKillSwitch(),
		Clock:    func() time.Time { return time.Unix(0, 0).UTC() },
	}
	res, err := ctrl.Run(context.Background(), optimizer.RunInput{
		Authority: optimizer.Authority{
			RunID: "run_1", WorkflowID: "wf_1", Actor: "test",
			Constraints: optimizer.Constraints{
				BudgetCeilingUSD: 100, MaxIterations: cap, MinImprovement: 0.001, StallK: 100,
			},
			KillSwitchArmed: true, AuditArmed: true, RollbackArmed: true,
		},
		Targets: []optimizer.Target{{DiagnosisID: "d1", Node: "n1", Dimension: "model", Priority: 1}},
	})
	if err != nil {
		t.Fatalf("the shipped loop refused to start: %v", err)
	}
	return res
}

// verifiedResult is a candidate that passes the typed contract, builds, and passes the held-out gate
// with a real gain. It is `optimizer`'s own `goodResult` shape, rebuilt here because that helper is in
// the optimizer's test binary and is not importable.
func verifiedResult(compositeLow, delta float64) optimizer.VerifyResult {
	return optimizer.VerifyResult{
		ContractOK: true, Builds: true,
		Verdict: verification.Verdict{
			GateResult: verification.GatePass, Significant: true, HeldOut: true, RegressionPass: true,
			Delta: evalstats.Interval{Mean: delta, Low: delta - 0.05, High: delta + 0.05},
		},
		Metrics: optimizer.CandidateMetrics{
			Providers: []string{"anthropic"}, Quality: 0.9, LatencyMS: 500,
			Composite: evalstats.Interval{Mean: compositeLow + 0.05, Low: compositeLow, High: compositeLow + 0.1},
		},
	}
}
