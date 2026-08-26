package improvementrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/assessment"
)

// killswitch_test.go is tasks 9.2 and 9.3.
//
// # 🔴 Why this uses a double here and proves the fit in `internal/launch`
//
// The claim is "the switch an operator throws stops a run", and the honest way to test it is against
// `adminops.KillSwitchService`. But constructing one needs the operator command path, an RBAC gate and
// an audit store — so importing it here would make this package depend on the operator console, which
// is exactly the dependency `optimizer` refuses for `MergeAdmission`: *the optimizer must remain
// buildable and testable with no operator console at all*, and a run has the same requirement.
//
// So the fit is proved where the wiring lives. `internal/launch` holds
// `var _ improvementrun.OperatorBrake = (*adminops.KillSwitchService)(nil)` — a COMPILE-TIME assertion
// that the shipped switch satisfies this interface with no adapter — and the behaviour under an armed
// or unreadable switch is proved here against a double that reproduces `HaltsMerge`'s exact contract,
// including the one part that matters: an error means INDETERMINATE and never "go ahead".

// armedBrake reproduces `adminops.KillSwitchService.HaltsMerge` for an armed scope, verbatim: global
// first, the operator's reason carried, and no branch that turns an unreadable state into permission.
type armedBrakeStub struct{ reason string }

func (b armedBrakeStub) HaltsMerge(string) (bool, string, error) {
	return true, "global kill switch armed: " + b.reason, nil
}

func armedBrake(t *testing.T, _, reason string) OperatorBrake {
	t.Helper()
	return armedBrakeStub{reason: reason}
}

// TestTheOperatorSwitchStopsARunBeforeAnythingIsSpent is task 9.3's first half.
func TestTheOperatorSwitchStopsARunBeforeAnythingIsSpent(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_good", passingVerdict(0.08))
	f.svc.Brake = armedBrake(t, "global", "provider incident")

	p := f.plan(t, "fix it")
	f.acknowledge(t, p)
	_, err := f.svc.Propose(context.Background(), p)

	var halted *ErrHalted
	if !errors.As(err, &halted) {
		t.Fatalf("an armed operator switch did not stop the run: %v", err)
	}
	if !strings.Contains(halted.Sentence(), "provider incident") {
		t.Fatalf("the refusal drops the operator's own reason (%q). It is the only part a reader can "+
			"act on", halted.Sentence())
	}
	if len(f.verifyOrder) != 0 {
		t.Fatalf("a halted run still called %v. A halt that spends is not a halt", f.verifyOrder)
	}
}

// TestTheOperatorSwitchStopsARunMidFlightAtTheDeliveryGate is the half that matters more: an operator
// who arms the switch while a run is between approval and delivery must stop the act that reaches a
// repository, not only the act that started one.
func TestTheOperatorSwitchStopsARunMidFlightAtTheDeliveryGate(t *testing.T) {
	f, run, del, routes := approvableWithDelivery(t)
	id := run.Proposals[0].ProposalID
	if _, err := f.svc.Approve(context.Background(), run, id, "person@example.com"); err != nil {
		t.Fatal(err)
	}
	// … and NOW the operator throws the switch.
	f.svc.Brake = armedBrake(t, "global", "runaway spend across the fleet")

	_, err := f.svc.Deliver(context.Background(), run, id)
	var halted *ErrHalted
	if !errors.As(err, &halted) {
		t.Fatalf("a run halted mid-flight still delivered: %v", err)
	}
	if len(routes.branchCalls) != 0 || len(del.calls) != 0 {
		t.Fatalf("a halted delivery reached the forge (branches=%v deliveries=%d). Checking the brake "+
			"only at the start would let a switch thrown mid-run miss the one act that touches a "+
			"customer's repository", routes.branchCalls, len(del.calls))
	}
}

// TestAnUnreadableOperatorSwitchHalts is P8 design Decision 4: cannot tell if we are stopped means
// stopped. Refusing a run somebody wanted wastes a click; admitting one somebody stopped spends money
// against an operator's explicit instruction.
func TestAnUnreadableOperatorSwitchHalts(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_good", passingVerdict(0.08))
	f.svc.Brake = unreadableBrake{}

	p := f.plan(t, "fix it")
	f.acknowledge(t, p)
	_, err := f.svc.Propose(context.Background(), p)

	var halted *ErrHalted
	if !errors.As(err, &halted) {
		t.Fatalf("an unreadable kill switch admitted the run: %v", err)
	}
	if !halted.Indeterminate {
		t.Fatal("an unreadable switch reported as ARMED. The two need different sentences: one is \"we " +
			"stopped this\", the other is \"we cannot tell whether we stopped this, so we stopped it\"")
	}
	if !strings.Contains(halted.Sentence(), "ours, not yours") {
		t.Fatalf("the indeterminate sentence does not say it is ours: %q", halted.Sentence())
	}
}

type unreadableBrake struct{}

func (unreadableBrake) HaltsMerge(string) (bool, string, error) {
	return true, "kill-switch state indeterminate", errors.New("the store is unreachable")
}

// TestNoBrakeIsNotTheSameAsNotHalted asserts the nil case is a decision visible in the wiring rather
// than a read that silently returned false.
func TestNoBrakeIsNotTheSameAsNotHalted(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_good", passingVerdict(0.08))
	if f.svc.Brake != nil {
		t.Fatal("the fixture wires a brake; this test is about the nil case")
	}
	if err := f.svc.checkBrake(context.Background(), "ten_1"); err != nil {
		t.Fatalf("a deployment with no operator console was halted: %v", err)
	}
}

// ── 9.2 · spend, capped, attributed and exported ─────────────────────────────────────────────────

// TestSpendIsCappedByThePlanTheersonWasShown is 9.2's first clause. The cap a run is held to must be
// the number the person saw, not a second one an operator set — a run bounded by a number nobody
// displayed is a run whose disclosure meant nothing.
func TestSpendIsCappedByThePlanThePersonWasShown(t *testing.T) {
	p, err := Translate("fix it", okBounds())
	if err != nil {
		t.Fatal(err)
	}
	if CapFor(p) != p.SpendBudgetUSD {
		t.Fatalf("the run's cap is $%.2f and the plan showed $%.2f", CapFor(p), p.SpendBudgetUSD)
	}
	if p.Constraints().BudgetCeilingUSD != CapFor(p) {
		t.Fatal("the cap does not reach the loop's own ceiling, so it bounds nothing")
	}
}

func TestSpendIsAttributedPerTenantAndExported(t *testing.T) {
	f, run, _, _ := approvableWithDelivery(t)
	if _, err := f.svc.Approve(context.Background(), run, run.Proposals[0].ProposalID, "person@example.com"); err != nil {
		t.Fatal(err)
	}
	rows := f.metrics.ExportSpend()
	if len(rows) == 0 {
		t.Fatal("no per-tenant spend was exported, so an operator cannot see which tenant's runs cost " +
			"what — which is the capacity question the export exists for")
	}
	var found bool
	for _, r := range rows {
		if r.TenantID == run.TenantID {
			found = true
			if r.SpendUSD <= 0 {
				t.Fatalf("tenant %q is exported with $%.4f", r.TenantID, r.SpendUSD)
			}
		}
	}
	if !found {
		t.Fatalf("the running tenant is absent from the export: %+v", rows)
	}
	h := f.metrics.Health()
	if h.PerTenantSpendUSD[run.TenantID] <= 0 {
		t.Fatal("the health document carries no per-tenant spend")
	}
}
