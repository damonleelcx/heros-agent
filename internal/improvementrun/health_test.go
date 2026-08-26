package improvementrun

import (
	"context"
	"testing"

	"github.com/heros-foreal/agentd/internal/assessment"
)

// health_test.go is task 9.1: the counters an operator reads, on a READABLE ENDPOINT rather than on a
// dashboard.
//
// # Why "not the dashboard" is the requirement rather than a preference
//
// A dashboard reads historical aggregates. It can look completely healthy while the pipeline is broken,
// because the aggregate it reads was computed before the break and the break produces no rows to move
// it. The signal an operator needs is a value they can GET right now and alert on, and only the process
// doing the work can publish it.

// TestTheHealthDocumentCarriesEveryCounterTaskNineOneNames asserts the document by NAME rather than by
// ranging over it — a test that ranged would pass for any set, including one missing the counter that
// matters.
func TestTheHealthDocumentCarriesEveryCounterTaskNineOneNames(t *testing.T) {
	f, run, _, _ := approvableWithDelivery(t)
	id := run.Proposals[0].ProposalID
	updated, _, err := f.svc.Decide(context.Background(), run.TenantID, run.RunID, id, DecideApprove, "person@example.com")
	if err != nil {
		t.Fatal(err)
	}
	h := f.metrics.Health()

	// Runs started / bounded-out / cancelled.
	if h.RunsStarted == 0 {
		t.Error("runs_started is zero after a run")
	}
	if h.RunsBoundedOut == 0 {
		t.Error("runs_bounded_out is zero after a run that stopped on a bound")
	}
	// 🚫 The three run outcomes are never summed. A fault and a bound are different events with
	// different owners, and one aggregate would let a rising fault rate hide inside a steady total.
	if h.RunsFaulted != 0 {
		t.Errorf("runs_faulted is %d after a clean run", h.RunsFaulted)
	}

	// Which bound, by name, with EVERY bound present even at zero — it is a closed set of four somebody
	// watches for, and an absent key reads as "we do not measure that".
	for _, b := range BoundsSet() {
		if _, ok := h.PerBound[b]; !ok {
			t.Errorf("the health document has no entry for the bound %q", b)
		}
	}

	// Proposals generated / verified / approved / delivered, PER AXIS.
	seen := map[string]bool{}
	for _, cell := range h.PerAxis {
		seen[cell.Stage.String()] = true
	}
	for _, want := range []Stage{StageGenerated, StageVerified, StageApproved, StageDelivered} {
		if !seen[want.String()] {
			t.Errorf("the per-axis breakdown carries no %q cell", want)
		}
	}

	// Deliveries deduplicated, withheld, pending-forge — the three delivery outcomes.
	// 🔴 The UPDATED run, not the stale local. `Decide` returns the run as it stands after the decision
	// — a caller that kept the old value would be delivering against a run that has not been approved,
	// which is exactly the state this second call is meant to exercise the idempotent path from.
	if _, err := f.svc.Deliver(context.Background(), &updated, id); err != nil {
		t.Fatal(err)
	}
	if f.metrics.Health().DeliveriesDeduplicated == 0 {
		t.Error("deliveries_deduplicated is zero after a second delivery of the same change. A " +
			"deduplication rate of zero over a busy deployment means the idempotency path is never " +
			"exercised, and an unexercised idempotency path is one nobody knows is broken")
	}
}

// TestTheWithdrawalRateIsTheFieldToPageOn is the signal §9.6 names and nothing conventional carries.
//
// 🔴 A withdrawal is the product WORKING — a change that failed to reproduce its verified delta was
// stopped before it reached a customer's repository. So every ordinary metric goes the RIGHT way while
// it happens: the run completed, the API answered 201, nothing errored and latency did not move. A rate
// that climbs means the gate's evidence is weaker than it reports, which is a claim about OUR
// measurement and is therefore correlated and ours to fix.
func TestTheWithdrawalRateIsTheFieldToPageOn(t *testing.T) {
	m := NewMetrics()

	// -1, never 0, before anything is approved. A rate over zero approvals is UNDEFINED, and 0.0 would
	// tell a monitor "we checked and it is fine" about a deployment that has never approved anything.
	if r := m.Health().WithdrawalRate; r != -1 {
		t.Fatalf("the withdrawal rate over zero approvals is %v, want -1", r)
	}
	if m.Health().Alerting {
		t.Fatal("a deployment that has approved nothing is alerting")
	}

	for i := 0; i < 10; i++ {
		m.Approved(assessment.AxisModel)
	}
	for i := 0; i < 2; i++ {
		m.Withdrawn(assessment.AxisModel)
	}
	h := m.Health()
	if h.WithdrawalRate != 0.2 {
		t.Fatalf("2 withdrawals over 10 approvals is %v, want 0.2", h.WithdrawalRate)
	}
	if h.Alerting {
		t.Fatalf("a 20%% withdrawal rate alerts against a %v threshold. A single withdrawal is ORDINARY "+
			"and is the feature working; paging on it would page on the system doing its job",
			h.AlertAbove)
	}

	for i := 0; i < 3; i++ {
		m.Withdrawn(assessment.AxisModel)
	}
	if h := m.Health(); !h.Alerting {
		t.Fatalf("a %.0f%% withdrawal rate does not alert against a %v threshold", h.WithdrawalRate*100, h.AlertAbove)
	}
	// The threshold is PUBLISHED so a monitor does not hard-code it.
	if m.Health().AlertAbove != AlertWithdrawalRateAbove {
		t.Fatal("the health document does not publish the threshold it alerts on")
	}
}

// TestPlansCreatedAndRunsStartedAreSeparatelyAnswerable is the product signal the disclosure threshold
// makes possible: a divergence between the two means people are seeing plans and declining them, which
// is invisible if only started runs are counted.
func TestPlansCreatedAndRunsStartedAreSeparatelyAnswerable(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_good", passingVerdict(0.08))

	// Three plans, one run.
	for i := 0; i < 3; i++ {
		f.plan(t, "fix it")
	}
	f.run(t, "fix it")

	h := f.metrics.Health()
	if h.PlansCreated < 4 {
		t.Fatalf("plans_created is %d after four plans", h.PlansCreated)
	}
	if h.RunsStarted != 1 {
		t.Fatalf("runs_started is %d after one run", h.RunsStarted)
	}
	if h.PlansAwaitingAcknowledgement == 0 {
		t.Fatal("plans_awaiting_acknowledgement is zero, so the disclosure threshold's effect on this " +
			"deployment is invisible")
	}
}

// TestTheReconciliationTimestampIsOnlyWrittenByASuccessfulPass — a fresh timestamp over a pass that
// examined nothing makes the staleness signal lie.
func TestTheReconciliationTimestampIsOnlyWrittenByASuccessfulPass(t *testing.T) {
	m := NewMetrics()
	if m.Health().ReconcileLastSuccessMS != 0 {
		t.Fatal("a fresh accumulator reports a reconciliation that never happened")
	}
	m.ReconcilePassed(1234, 0)
	h := m.Health()
	if h.ReconcileLastSuccessMS != 1234 {
		t.Fatalf("the timestamp is %d", h.ReconcileLastSuccessMS)
	}
	// 🔴 Zero resolutions is the NORMAL result and is still a success. "Did it do anything" cannot be
	// the signal; "when did it last complete" is.
	if h.ReconcileResolved != 0 {
		t.Fatalf("a pass that resolved nothing reports %d resolutions", h.ReconcileResolved)
	}
}

// TestTheKillSwitchStateIsReadableFromTheHealthEndpoint is task 9.3's observability half: a switch
// whose effect is not observable is a switch nobody trusts in an incident.
func TestTheKillSwitchStateIsReadableFromTheHealthEndpoint(t *testing.T) {
	m := NewMetrics()
	if m.Health().KillSwitchArmed {
		t.Fatal("a fresh accumulator reports the kill switch armed")
	}
	m.SetKillSwitch(true)
	if !m.Health().KillSwitchArmed {
		t.Fatal("the health document does not report an armed kill switch, so an operator cannot " +
			"confirm the switch they threw is the one this process reads")
	}
}
