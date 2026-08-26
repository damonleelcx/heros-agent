package improvementrun

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/evalstats"
)

// approve_test.go proves §4: per-proposal approval routed through the shipped gate, bound to a hash,
// declining continues the run, and re-measurement is allowed to disagree.

// ── the §4 fixture, built on §3's ────────────────────────────────────────────────────────────────

// remeasureStub is the second observation.
type remeasureStub struct {
	byProposal map[string]Measurement
	errs       map[string]error
	calls      []string
}

func (r *remeasureStub) Remeasure(_ context.Context, p VerifiedProposal, want Binding) (Measurement, error) {
	r.calls = append(r.calls, p.ProposalID)
	if err, bad := r.errs[p.ProposalID]; bad {
		return Measurement{}, err
	}
	m, ok := r.byProposal[p.ProposalID]
	if !ok {
		// Default: the change reproduced exactly. A default that DISAGREED would make every test that
		// forgot to set an expectation pass for the wrong reason.
		m = Measurement{
			Delta: p.Delta, Significant: p.Significant,
			ProviderModelVersion: p.ProviderModelVersion,
			ResolvedConfigHash:   want.ConfigHash, SourceRevision: want.SourceRevision,
		}
	}
	return m, nil
}

// approvable wires §3's fixture with §4's collaborators and runs to a surfaced proposal.
func approvable(t *testing.T) (*fixture, *Run, *remeasureStub, Binding) {
	t.Helper()
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_good", passingVerdict(0.08))

	rem := &remeasureStub{byProposal: map[string]Measurement{}, errs: map[string]error{}}
	binding := Binding{ConfigHash: "cfg_good", SourceRevision: "abc123def456"}

	f.svc.Approvals = NewMemApprovalGate()
	f.svc.Remeasure = rem
	f.svc.Subject = func(context.Context, Plan, VerifiedProposal) (Binding, error) { return binding, nil }

	run := f.run(t, "improve my model choice")
	if len(run.Proposals) != 1 {
		t.Fatalf("the fixture surfaced %d proposals, want 1", len(run.Proposals))
	}
	return f, &run, rem, binding
}

// ── 4.1 per proposal, through the shipped gate, with no bulk control ─────────────────────────────

// TestNoBulkApprovalPathExists is design D4 made structural. It asserts by REFLECTION rather than by
// reading the code, because the pressure to add "approve all" arrives in a future phase under delivery
// pressure and a comment does not stop it.
func TestNoBulkApprovalPathExists(t *testing.T) {
	svcT := reflect.TypeOf(&Service{})
	for i := 0; i < svcT.NumMethod(); i++ {
		m := svcT.Method(i)
		if !strings.Contains(strings.ToLower(m.Name), "approv") &&
			!strings.Contains(strings.ToLower(m.Name), "declin") {
			continue
		}
		for a := 0; a < m.Type.NumIn(); a++ {
			in := m.Type.In(a)
			if in.Kind() == reflect.Slice && in.Elem().Kind() == reflect.String {
				t.Fatalf("Service.%s takes a []string. A bundle approval is one click that means several "+
					"things, and the person will read the first item and accept the rest (design D4)", m.Name)
			}
		}
	}
	gateT := reflect.TypeOf((*ApprovalGate)(nil)).Elem()
	for i := 0; i < gateT.NumMethod(); i++ {
		if strings.Contains(strings.ToLower(gateT.Method(i).Name), "all") {
			t.Fatalf("ApprovalGate.%s exists. There is no plural form of consent here",
				gateT.Method(i).Name)
		}
	}
}

func TestConversationalRun_ApprovalIsPerProposalAndRoutedThroughTheShippedGate(t *testing.T) {
	f, run, _, _ := approvable(t)
	gate := f.svc.Approvals.(*MemApprovalGate)

	d, err := f.svc.Approve(context.Background(), run, run.Proposals[0].ProposalID, "person@example.com")
	if err != nil {
		t.Fatalf("approving: %v", err)
	}
	if d.State != DecisionApproved || d.By != "person@example.com" {
		t.Fatalf("the decision is %+v", d)
	}
	if len(gate.decided) != 1 {
		t.Fatalf("the shipped approval gate recorded %d decisions. A second approval path is a second "+
			"place the entitlement check, the automation-level check and the attribution can be wrong",
			len(gate.decided))
	}
	row, _ := run.AxisRow(assessment.AxisModel)
	if row.Approved != 1 {
		t.Fatalf("the per-axis breakdown counts %d approvals on the model axis", row.Approved)
	}
}

// TestAnApprovalMustNameThePersonBeforeAnythingElseHappens asserts the refusal comes FIRST — before the
// binding check and before the apply — so a decision that could never have been recorded costs nothing.
func TestAnApprovalMustNameThePersonBeforeAnythingElseHappens(t *testing.T) {
	f, run, rem, _ := approvable(t)
	if _, err := f.svc.Approve(context.Background(), run, run.Proposals[0].ProposalID, ""); err == nil {
		t.Fatal("an approval naming nobody was accepted")
	}
	if len(rem.calls) != 0 {
		t.Fatalf("the change was re-measured (%v) for an approval that named nobody", rem.calls)
	}
}

// ── 4.2 bound to (config_hash, source_revision) ──────────────────────────────────────────────────

// TestConversationalRun_ApprovalVoidWhenRevisionMoves is FR13. An approval that survived a revision
// change would be an approval for a diff nobody saw.
func TestConversationalRun_ApprovalVoidWhenRevisionMoves(t *testing.T) {
	f, run, rem, _ := approvable(t)
	f.svc.Subject = func(context.Context, Plan, VerifiedProposal) (Binding, error) {
		return Binding{ConfigHash: "cfg_good", SourceRevision: "MOVED_ffffff"}, nil
	}
	d, err := f.svc.Approve(context.Background(), run, run.Proposals[0].ProposalID, "person@example.com")
	if !errors.Is(err, ErrApprovalVoid) {
		t.Fatalf("an approval survived the revision moving: %v", err)
	}
	if d.State != DecisionVoid {
		t.Fatalf("the decision state is %q, want %q", d.State, DecisionVoid)
	}
	if !strings.Contains(d.VoidReason, "revision moved") {
		t.Fatalf("the void reason does not name WHICH half moved: %q. \"The revision moved\" and \"the "+
			"configuration was regenerated\" send a person to two different places", d.VoidReason)
	}
	if len(rem.calls) != 0 {
		t.Fatalf("a void approval still applied and re-measured (%v). Checking the binding before the "+
			"consent is what makes a void approval cost nothing", rem.calls)
	}
	if d.Sentence() == "" || !strings.Contains(d.Sentence(), "asked for again") {
		t.Fatalf("a void decision renders %q; the person is entitled to be told it was re-requested", d.Sentence())
	}
}

// TestAVoidApprovalIsNotServedOverALedgerThatRefusedTheRow is the fence for a silent drop: the void
// branch called `recordDecision` and threw its error away, so a ledger that refused the row still
// produced a clean `ErrApprovalVoid` — 409 at the surface, with the console rendering "this was
// re-requested" while the run read back FROM THE LEDGER still shows the approval pending. The ledger is
// the record, and the approved and declined paths have always aborted on the same failure; only this
// one did not.
//
// 🔴 It asserts the error is NOT `ErrApprovalVoid`, not merely that some error came back. The old code
// also returned an error — the WRONG one — and a test that only checked `err != nil` would have passed
// against the defect. The decision must come back zero for the same reason: a `DecisionVoid` handed to
// a caller is one the surface will render as a recorded fact.
func TestAVoidApprovalIsNotServedOverALedgerThatRefusedTheRow(t *testing.T) {
	f, run, rem, _ := approvable(t)
	f.svc.Subject = func(context.Context, Plan, VerifiedProposal) (Binding, error) {
		return Binding{ConfigHash: "cfg_good", SourceRevision: "MOVED_ffffff"}, nil
	}
	f.ledger.SetDown(true)

	d, err := f.svc.Approve(context.Background(), run, run.Proposals[0].ProposalID, "person@example.com")
	if err == nil {
		t.Fatal("a void approval was returned over a ledger that took no row")
	}
	if errors.Is(err, ErrApprovalVoid) {
		t.Fatalf("the caller was told the approval is void (%v) over a ledger that refused the row. That "+
			"reaches the surface as a 409 for a decision no ledger read will ever show", err)
	}
	if d.State != "" {
		t.Fatalf("an unrecorded decision came back as %q; a surface handed a decision renders it as a "+
			"recorded fact", d.State)
	}
	if len(rem.calls) != 0 {
		t.Fatalf("the change was re-measured (%v) for an approval that was never recorded", rem.calls)
	}
}

func TestAVoidApprovalIsNotTheSameAsPending(t *testing.T) {
	seen := map[string]DecisionState{}
	for _, st := range DecisionStates() {
		d := Decision{ProposalID: "p", State: st, By: "person", VoidReason: "the source revision moved"}
		s := d.Sentence()
		if prior, dup := seen[s]; dup {
			t.Fatalf("%q and %q render the SAME sentence; \"you approved this and the revision moved\" "+
				"is a different sentence from \"this is waiting for you\"", st, prior)
		}
		seen[s] = st
	}
}

func TestABindingNeedsBothHalves(t *testing.T) {
	if (Binding{ConfigHash: "c"}).Complete() || (Binding{SourceRevision: "r"}).Complete() {
		t.Fatal("a half binding validated. A binding on config_hash alone survives the source moving " +
			"underneath a diff that still resolves the same way; either half alone is an approval for " +
			"something nobody saw")
	}
}

// ── 4.3 declining one continues the run and stays visible ────────────────────────────────────────

func TestConversationalRun_DecliningOneProposalContinuesTheRun(t *testing.T) {
	f := newFixture(t)
	f.offer(assessment.AxisModel, "cfg_a", passingVerdict(0.08))
	f.offer(assessment.AxisMemory, "cfg_b", passingVerdict(0.11))
	rem := &remeasureStub{byProposal: map[string]Measurement{}, errs: map[string]error{}}
	f.svc.Approvals, f.svc.Remeasure = NewMemApprovalGate(), rem
	f.svc.Subject = func(_ context.Context, _ Plan, p VerifiedProposal) (Binding, error) {
		return Binding{ConfigHash: p.ConfigHash, SourceRevision: "abc123def456"}, nil
	}
	run := f.run(t, "fix it")
	if len(run.Proposals) != 2 {
		t.Fatalf("surfaced %d proposals, want 2", len(run.Proposals))
	}

	declined := run.Proposals[0]
	if _, err := f.svc.Decline(context.Background(), &run, declined.ProposalID, "person@example.com"); err != nil {
		t.Fatalf("declining: %v", err)
	}
	// 🔴 The proposal stays in the list.
	if _, ok := run.proposal(declined.ProposalID); !ok {
		t.Fatal("the declined proposal disappeared from the run. A proposal that vanished when it was " +
			"declined looks exactly like one that was never made")
	}
	d := run.DecisionFor(declined.ProposalID)
	if d.State != DecisionDeclined || d.By == "" {
		t.Fatalf("the decline was not recorded on the run: %+v", d)
	}
	// … and the OTHER proposal is still approvable. One no is not a cancel.
	other := run.Proposals[1]
	if _, err := f.svc.Approve(context.Background(), &run, other.ProposalID, "person@example.com"); err != nil {
		t.Fatalf("the run did not continue after a decline: %v", err)
	}
}

func TestAnUndecidedProposalReadsAsPendingRatherThanBlank(t *testing.T) {
	_, run, _, _ := approvable(t)
	d := run.DecisionFor(run.Proposals[0].ProposalID)
	if d.State != DecisionPending {
		t.Fatalf("an undecided proposal reads as %q. A zero decision has an empty state, and a surface "+
			"switching on it falls into a default arm and renders a blank card", d.State)
	}
	if !d.Binding.Complete() {
		t.Fatal("a pending decision carries no binding, so the surface cannot show what would be approved")
	}
}

// ── 4.4 / 4.5 / 4.6 re-measurement, pinning, and withdrawal ──────────────────────────────────────

// TestConversationalRun_RemeasurementDisagreementWithdraws is FR16 with its teeth: BOTH measurements
// are reported, because a withdrawal with one number looks like a bug and with two it is a finding.
func TestConversationalRun_RemeasurementDisagreementWithdraws(t *testing.T) {
	f, run, rem, binding := approvable(t)
	p := run.Proposals[0]
	rem.byProposal[p.ProposalID] = Measurement{
		// verified +0.08 [0.06, 0.10]; re-measured +0.01 [-0.03, 0.05] — the intervals do not overlap.
		Delta:                evalstats.Interval{Mean: 0.01, Low: -0.03, High: 0.005, NSeeds: 5, NCases: 40},
		Significant:          false,
		ProviderModelVersion: p.ProviderModelVersion,
		ResolvedConfigHash:   binding.ConfigHash, SourceRevision: binding.SourceRevision,
		SpendUSD: 0.40,
	}
	if _, err := f.svc.Approve(context.Background(), run, p.ProposalID, "person@example.com"); err != nil {
		t.Fatalf("approving: %v", err)
	}
	if len(run.Withdrawals) != 1 {
		t.Fatalf("a change whose re-measurement disagreed was not withdrawn (%d withdrawals). A second "+
			"observation that can only confirm is theatre", len(run.Withdrawals))
	}
	w := run.Withdrawals[0]
	if w.Reason != WithdrawnDidNotReproduce {
		t.Fatalf("the withdrawal reason is %q, want %q", w.Reason, WithdrawnDidNotReproduce)
	}
	sentence := w.Sentence()
	for _, want := range []string{"+0.080", "+0.010"} {
		if !strings.Contains(sentence, want) {
			t.Fatalf("the withdrawal reports %q and does not carry %q. With one number it looks like a "+
				"bug; with two it is a finding about the eval set as much as about the change",
				sentence, want)
		}
	}
	row, _ := run.AxisRow(assessment.AxisModel)
	if row.Withdrawn != 1 {
		t.Fatal("the per-axis breakdown does not count the withdrawal, so an axis with a noisy eval set " +
			"reads as an axis that produces nothing")
	}
	if run.WithdrawnSpendUSD != 0.40 {
		t.Fatalf("the withdrawn spend is $%.2f, want $0.40. decisions.md D-35.4 charges it against the "+
			"budget and bills none of it, and both halves need a measured number", run.WithdrawnSpendUSD)
	}
}

func TestAChangeThatReproducesIsNotWithdrawn(t *testing.T) {
	f, run, _, _ := approvable(t)
	if _, err := f.svc.Approve(context.Background(), run, run.Proposals[0].ProposalID, "person@example.com"); err != nil {
		t.Fatalf("approving: %v", err)
	}
	if len(run.Withdrawals) != 0 {
		t.Fatalf("a change that reproduced was withdrawn: %+v", run.Withdrawals[0])
	}
}

// TestAPinnedMeasurementFailsRatherThanScoring is FR17. A score from the wrong configuration is not a
// worse score — it is a number about something else, indistinguishable from a real one once written.
func TestAPinnedMeasurementFailsRatherThanScoring(t *testing.T) {
	f, run, rem, _ := approvable(t)
	p := run.Proposals[0]
	rem.errs[p.ProposalID] = ErrPinBroken
	if _, err := f.svc.Approve(context.Background(), run, p.ProposalID, "person@example.com"); err != nil {
		t.Fatalf("approving: %v", err)
	}
	if len(run.Withdrawals) != 1 || run.Withdrawals[0].Reason != WithdrawnPinBroken {
		t.Fatalf("a broken pin was not reported as one: %+v", run.Withdrawals)
	}
	if run.Withdrawals[0].Remeasured.Delta.Mean != 0 {
		t.Fatal("a broken-pin withdrawal carries a re-measured delta. There is no comparable number, " +
			"and carrying one invites a reader to compare it")
	}
}

// TestAProviderThatMovedIsNotReportedAsAChangeThatFailed is design D2's trap, fenced. Reporting it as
// `did_not_reproduce` would blame a customer's change for a vendor's release.
func TestAProviderThatMovedIsNotReportedAsAChangeThatFailed(t *testing.T) {
	f, run, rem, binding := approvable(t)
	p := run.Proposals[0]
	rem.byProposal[p.ProposalID] = Measurement{
		Delta:                evalstats.Interval{Mean: 0.01, Low: -0.03, High: 0.005, NSeeds: 5, NCases: 40},
		ProviderModelVersion: "claude-opus-5-2026-09", // moved
		ResolvedConfigHash:   binding.ConfigHash, SourceRevision: binding.SourceRevision,
	}
	if _, err := f.svc.Approve(context.Background(), run, p.ProposalID, "person@example.com"); err != nil {
		t.Fatalf("approving: %v", err)
	}
	w := run.Withdrawals[0]
	if w.Reason != WithdrawnProviderMoved {
		t.Fatalf("a provider that moved was reported as %q. That blames a customer's change for a "+
			"vendor's release", w.Reason)
	}
	if w.Reason.AboutTheChange() {
		t.Fatal("`provider_moved` reports as being about the change")
	}
	if !strings.Contains(w.Sentence(), "NOT a result about the change") {
		t.Fatalf("the sentence does not say it is not about the change: %q", w.Sentence())
	}
}

// TestASingleSeedMeasurementIsRefused is the second of D2's three mechanisms. Without multi-seed
// intervals every re-measurement disagrees and every change is withdrawn.
func TestASingleSeedMeasurementIsRefused(t *testing.T) {
	m := Measurement{
		Delta:                evalstats.Interval{Mean: 0.08, Low: 0.08, High: 0.08, NSeeds: 1, NCases: 40},
		ProviderModelVersion: "v1",
	}
	if err := m.Validate(); err == nil {
		t.Fatal("a single-seed measurement was accepted. Its interval has width zero, which overlaps " +
			"nothing — under it every re-measurement disagrees and every change is withdrawn")
	}
}

func TestAMeasurementWithNoProviderVersionIsRefused(t *testing.T) {
	m := Measurement{Delta: evalstats.Interval{Mean: 0.08, Low: 0.06, High: 0.10, NSeeds: 5, NCases: 40}}
	if err := m.Validate(); err == nil {
		t.Fatal("a measurement with no provider model version was accepted, so a disagreement between " +
			"two of them could not be told apart from the provider moving underneath both")
	}
}

func TestEveryWithdrawalReasonHasItsOwnSentence(t *testing.T) {
	seen := map[string]WithdrawalReason{}
	for _, r := range WithdrawalReasons() {
		w := Withdrawal{
			Reason: r, Detail: "d",
			Verified:   Measurement{Delta: evalstats.Interval{Mean: 0.08, NSeeds: 5, NCases: 40}, ProviderModelVersion: "a"},
			Remeasured: Measurement{Delta: evalstats.Interval{Mean: 0.01, NSeeds: 5, NCases: 40}, ProviderModelVersion: "b"},
		}
		s := w.Sentence()
		if s == "" {
			t.Fatalf("%q renders no sentence", r)
		}
		if prior, dup := seen[s]; dup {
			t.Fatalf("%q and %q render the SAME sentence; only one of the four is a statement about the "+
				"change", r, prior)
		}
		seen[s] = r
	}
}

// TestOverlapIsThePlatformsOwnTiePredicate asserts the comparison is `evalstats`', not a second one.
func TestOverlapIsThePlatformsOwnTiePredicate(t *testing.T) {
	a := Measurement{Delta: evalstats.Interval{Mean: 0.08, Low: 0.06, High: 0.10, NSeeds: 5, NCases: 40}}
	b := Measurement{Delta: evalstats.Interval{Mean: 0.05, Low: 0.03, High: 0.07, NSeeds: 5, NCases: 40}}
	if Reproduced(a, b) != a.Delta.Overlaps(b.Delta) {
		t.Fatal("Reproduced does not agree with evalstats.Interval.Overlaps. Two definitions of \"these " +
			"agree\" is two answers to the only question that decides whether a change ships")
	}
	if !Reproduced(a, b) {
		t.Fatal("two overlapping intervals were reported as disagreeing")
	}
}

func TestApprovingSomethingThisRunNeverSurfacedIsRefused(t *testing.T) {
	f, run, _, _ := approvable(t)
	if _, err := f.svc.Approve(context.Background(), run, "prop_from_another_run", "person@example.com"); !errors.Is(err, ErrNotSurfaced) {
		t.Fatalf("got %v, want ErrNotSurfaced. An approval for something this run never offered is not "+
			"an approval of anything it can act on", err)
	}
}
