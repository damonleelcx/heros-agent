package billing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/verification"
)

// gainshare_test.go is task 6.4 — the phase's load-bearing test — plus 6.5's auditability.
//
// The claim under test is not "gainshare works". It is: **an estimated or un-merged saving raises NO
// charge**, and a merged, verified one bills and TRACES to its evidence. Everything else in P7 can be
// re-derived from a ledger; billing a customer for a saving that never happened cannot be undone by
// arithmetic.

// passVerdict is a genuine P5.5 pass: held-out, statistically significant, regression-clean. Built from
// the REAL verification.Verdict so the predicate under test reads the same fields the P5.5 gate writes.
func passVerdict(delta float64) verification.Verdict {
	return verification.Verdict{
		GateResult: verification.GatePass, HeldOut: true, Significant: true, RegressionPass: true,
		Delta: evalstats.Interval{Mean: delta, Low: delta - 0.05, High: delta + 0.05},
	}
}

// fullMethod is a complete, reconstructable baseline + holdout methodology.
func fullMethod(id string) metering.BaselineMethod {
	return metering.BaselineMethod{
		ID: "holdout-v1", EvalSetHash: "es_" + id,
		HoldoutCaseIDs:      []string{"c4", "c5", "c6"},
		GeneratingCaseIDs:   []string{"c1", "c2", "c3"},
		Seeds:               []int64{1, 2, 3, 4, 5},
		BaselineConfigHash:  "base_" + id,
		CandidateConfigHash: "cand_" + id,
	}
}

// mergedVerified is the ONE shape that is billable: verified AND merged.
func mergedVerified(ref string, baselineSUM, optimizedSUM float64) metering.VerifiedDelta {
	return metering.VerifiedDelta{
		Ref: ref, ProposalID: "prop_" + ref, CustomerID: "cus_acme", Period: july.ID,
		Verdict: passVerdict(0.4), Merged: true, MergeCommit: "abc123" + ref,
		BaselineSUM: baselineSUM, OptimizedSUM: optimizedSUM, Baseline: fullMethod(ref),
	}
}

// gainshareHarness is the 7b stack: an Enterprise account with recorded consent, the P5.5 ledger, and
// the savings store.
func gainshareHarness(t *testing.T, consent bool) (*harness, *metering.MemVerifiedDeltas, *metering.MemSavingsStore) {
	t.Helper()
	h := newHarness(t, "enterprise")
	if consent {
		if _, err := h.accounts.SetGainshareConsent("cus_acme", true, julyStart); err != nil {
			t.Fatalf("consent: %v", err)
		}
	}
	return h, metering.NewMemVerifiedDeltas(), metering.NewMemSavingsStore()
}

// TestUnverifiedSavingBillsNothing is task 6.4's first half, run over EVERY way a saving can fail to be
// billable. Each case seeds exactly one ledger entry that a naive implementation would bill, and
// asserts two independent things: ComputeBillableSavings returns ZERO for it, and ChargeGainshare
// REFUSES — no billing event, no provider charge.
func TestUnverifiedSavingBillsNothing(t *testing.T) {
	cases := map[string]func(d metering.VerifiedDelta) metering.VerifiedDelta{
		"estimated, not measured": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Estimated = true
			return d
		},
		"verified but never merged": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Merged, d.MergeCommit = false, ""
			return d
		},
		"merged but the gate did not pass": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Verdict.GateResult = verification.GateFailSig
			return d
		},
		"merged, passing, but not held out": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Verdict.HeldOut = false
			return d
		},
		"merged, passing, but not significant": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Verdict.Significant = false
			return d
		},
		"merged, passing, but regressed another cluster": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Verdict.RegressionPass = false
			return d
		},
		"merged with no merge commit": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.MergeCommit = ""
			return d
		},
		"methodology not reconstructable": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Baseline.HoldoutCaseIDs = nil
			return d
		},
		"the change did not reduce spend": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.BaselineSUM, d.OptimizedSUM = 10, 10
			return d
		},
		"never ran the gate at all": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Verdict = verification.Verdict{GateResult: verification.GateUnrun}
			return d
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h, ledger, savings := gainshareHarness(t, true)
			ctx := context.Background()
			ledger.Put(mutate(mergedVerified("vd1", 100, 60)))

			// (a) The computation contributes ZERO — and names why.
			bs, err := metering.ComputeBillableSavings(ledger, "cus_acme", july)
			if err != nil {
				t.Fatalf("compute: %v", err)
			}
			if bs.Savings != 0 {
				t.Errorf("an unbillable saving contributed %v", bs.Savings)
			}
			if len(bs.VerifiedDeltaRefs) != 0 || len(bs.MergeCommits) != 0 {
				t.Errorf("an unbillable saving contributed evidence: %+v", bs)
			}
			if len(bs.Excluded) != 1 || bs.Excluded[0].Reason == "" {
				t.Errorf("the exclusion was not recorded with a reason: %+v", bs.Excluded)
			}

			// (b) The charge is REFUSED, and nothing was written or called.
			res, err := h.svc.ChargeGainshare(ctx, "cus_acme", july, ledger, savings)
			if !errors.Is(err, metering.ErrNoVerifiedSavings) {
				t.Fatalf("want ErrNoVerifiedSavings, got %v (result %+v)", err, res)
			}
			if h.provider.ChargeCount() != 0 {
				t.Errorf("a provider charge was raised for an unverified saving")
			}
			for _, ev := range testEvents(h.ledger, "cus_acme", "") {
				if ev.Type == TypeGainshareCharge {
					t.Errorf("a gainshare billing event was created: %+v", ev)
				}
			}
			if savings.Rows() != 0 {
				t.Errorf("an unbillable savings row was persisted")
			}
		})
	}
}

// TestVerifiedMergedSavingIsBilledAndTraces is task 6.4's second half. Without it, the test above would
// pass for an implementation that bills nothing at all.
func TestVerifiedMergedSavingIsBilledAndTraces(t *testing.T) {
	h, ledger, savings := gainshareHarness(t, true)
	ctx := context.Background()

	// Two billable deltas, plus three that must contribute nothing.
	ledger.Put(mergedVerified("vd1", 100, 60)) // saves 40
	ledger.Put(mergedVerified("vd2", 50, 35))  // saves 15
	est := mergedVerified("vd3", 1000, 1)      // an enormous ESTIMATE
	est.Estimated = true
	ledger.Put(est)
	unmerged := mergedVerified("vd4", 500, 1) // verified, never merged
	unmerged.Merged, unmerged.MergeCommit = false, ""
	ledger.Put(unmerged)
	otherPeriod := mergedVerified("vd5", 900, 1)
	otherPeriod.Period = "2026-06"
	ledger.Put(otherPeriod)

	res, err := h.svc.ChargeGainshare(ctx, "cus_acme", july, ledger, savings)
	if err != nil {
		t.Fatalf("gainshare: %v", err)
	}

	// The figure is exactly the two verified, merged deltas — the estimate and the un-merged proposal
	// are worth 1498 between them and contributed nothing.
	const want = (100 - 60) + (50 - 35)
	if res.Savings.Savings != want {
		t.Fatalf("billable savings = %v, want %v (the estimate and the un-merged saving must contribute zero)",
			res.Savings.Savings, want)
	}
	if res.Savings.BaselineSUM != 150 || res.Savings.OptimizedSUM != 95 {
		t.Errorf("baseline/optimized = %v/%v, want 150/95", res.Savings.BaselineSUM, res.Savings.OptimizedSUM)
	}
	if len(res.Savings.Excluded) != 2 {
		t.Errorf("exclusions = %d, want 2 (the estimate and the un-merged proposal), got %+v",
			len(res.Savings.Excluded), res.Savings.Excluded)
	}

	// The charge exists, is a gainshare charge, and bills the verified quantity.
	if res.Event.Type != TypeGainshareCharge || res.Event.Kind != KindGainshare {
		t.Errorf("event type/kind = %s/%s", res.Event.Type, res.Event.Kind)
	}
	if res.Event.Quantity != want {
		t.Errorf("charged quantity = %v, want %v", res.Event.Quantity, want)
	}
	if res.Event.Status != StatusRecorded {
		t.Errorf("the gainshare charge was not settled: %+v", res.Event)
	}

	// It TRACES: the event carries the verifying ledger refs AND the merge commits.
	if len(res.Event.Evidence) != 4 {
		t.Fatalf("evidence = %v, want 2 verified-delta refs + 2 merge commits", res.Event.Evidence)
	}
	for _, want := range []string{"verified_delta:vd1", "verified_delta:vd2", "merge:abc123vd1", "merge:abc123vd2"} {
		found := false
		for _, e := range res.Event.Evidence {
			if e == want {
				found = true
			}
		}
		if !found {
			t.Errorf("evidence is missing %q: %v", want, res.Event.Evidence)
		}
	}

	// And the persisted savings row carries the same evidence.
	stored, err := savings.Get("cus_acme", july.ID)
	if err != nil {
		t.Fatalf("stored savings: %v", err)
	}
	if len(stored.VerifiedDeltaRefs) != 2 || len(stored.MergeCommits) != 2 {
		t.Errorf("the stored savings row does not carry its evidence: %+v", stored)
	}

	// A retry raises no second gainshare charge.
	again, err := h.svc.ChargeGainshare(ctx, "cus_acme", july, ledger, savings)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if again.Event.EventID != res.Event.EventID {
		t.Errorf("a retried gainshare charge created a second event (%s vs %s)", again.Event.EventID, res.Event.EventID)
	}
	if h.provider.ChargeCount() != 1 {
		t.Errorf("provider charges = %d, want 1", h.provider.ChargeCount())
	}
}

// TestGainshareRefusesWhenTheEvidenceDoesNotResolve is guard 3: the refs are re-verified against the
// ledger AT CHARGE TIME, so a stale or corrupted savings row still cannot produce a charge.
func TestGainshareRefusesWhenTheEvidenceDoesNotResolve(t *testing.T) {
	h, ledger, savings := gainshareHarness(t, true)
	ctx := context.Background()

	// A ledger that reports a billable saving in the listing but cannot resolve the ref afterwards — the
	// shape a corrupted or partially-replicated ledger takes.
	forgetful := &forgetfulLedger{inner: ledger}
	ledger.Put(mergedVerified("vd1", 100, 60))

	res, err := h.svc.ChargeGainshare(ctx, "cus_acme", july, forgetful, savings)
	if !errors.Is(err, ErrSavingsNotVerified) {
		t.Fatalf("want ErrSavingsNotVerified, got %v (result %+v)", err, res)
	}
	if h.provider.ChargeCount() != 0 {
		t.Error("a charge was raised on evidence that does not resolve")
	}
	if savings.Rows() != 0 {
		t.Error("a savings row was persisted despite unresolvable evidence")
	}
}

// forgetfulLedger lists an entry but cannot resolve it by ref.
type forgetfulLedger struct{ inner *metering.MemVerifiedDeltas }

func (f *forgetfulLedger) VerifiedDeltas(c, p string) ([]metering.VerifiedDelta, error) {
	return f.inner.VerifiedDeltas(c, p)
}
func (f *forgetfulLedger) ByRef(string) (metering.VerifiedDelta, bool) {
	return metering.VerifiedDelta{}, false
}
func (f *forgetfulLedger) Describe() string { return "forgetful" }

// TestGainshareRequiresRecordedConsent: gainshare is a contract the customer opts into, and revoking it
// stops future charges.
func TestGainshareRequiresRecordedConsent(t *testing.T) {
	h, ledger, savings := gainshareHarness(t, false) // no consent
	ctx := context.Background()
	ledger.Put(mergedVerified("vd1", 100, 60))

	if _, err := h.svc.ChargeGainshare(ctx, "cus_acme", july, ledger, savings); !errors.Is(err, ErrNoGainshareConsent) {
		t.Fatalf("want ErrNoGainshareConsent, got %v", err)
	}
	if h.provider.ChargeCount() != 0 {
		t.Error("a gainshare charge was raised without consent")
	}

	// Consent granted: the charge proceeds.
	if _, err := h.accounts.SetGainshareConsent("cus_acme", true, julyStart); err != nil {
		t.Fatalf("consent: %v", err)
	}
	if _, err := h.svc.ChargeGainshare(ctx, "cus_acme", july, ledger, savings); err != nil {
		t.Fatalf("after consent: %v", err)
	}

	// Consent REVOKED: the next period's charge is refused. Consent is revocable, not a one-way door.
	if _, err := h.accounts.SetGainshareConsent("cus_acme", false, julyStart); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	aug := metering.MonthPeriod(clockNow)
	augDelta := mergedVerified("vd_aug", 100, 60)
	augDelta.Period = aug.ID
	ledger.Put(augDelta)
	if _, err := h.svc.ChargeGainshare(ctx, "cus_acme", aug, ledger, savings); !errors.Is(err, ErrNoGainshareConsent) {
		t.Errorf("a charge was raised after consent was revoked: %v", err)
	}
}

// TestGainshareRequiresAPriceReference: a plan with no gainshare price reference cannot be gainshared,
// and the refusal names the plan rather than defaulting to some other price.
func TestGainshareRequiresAPriceReference(t *testing.T) {
	h := newHarness(t, "team") // the fixture Team plan has no gainshare price ref
	if _, err := h.accounts.SetGainshareConsent("cus_acme", true, julyStart); err != nil {
		t.Fatalf("consent: %v", err)
	}
	ledger := metering.NewMemVerifiedDeltas()
	ledger.Put(mergedVerified("vd1", 100, 60))

	_, err := h.svc.ChargeGainshare(context.Background(), "cus_acme", july, ledger, metering.NewMemSavingsStore())
	if !errors.Is(err, ErrNoGainsharePrice) {
		t.Fatalf("want ErrNoGainsharePrice, got %v", err)
	}
	if !strings.Contains(err.Error(), "team") {
		t.Errorf("the refusal does not name the plan: %v", err)
	}
}

// TestGainshareMethodologyIsAuditable is task 6.5: given only the billing event, an auditor recovers the
// verified deltas and their FIXED baseline + holdout methodology.
func TestGainshareMethodologyIsAuditable(t *testing.T) {
	h, ledger, savings := gainshareHarness(t, true)
	ctx := context.Background()
	ledger.Put(mergedVerified("vd1", 100, 60))
	ledger.Put(mergedVerified("vd2", 50, 35))

	res, err := h.svc.ChargeGainshare(ctx, "cus_acme", july, ledger, savings)
	if err != nil {
		t.Fatalf("gainshare: %v", err)
	}

	// Starting from the BILLING EVENT alone — what an auditor is handed.
	deltas, merges, err := GainshareEvidence(res.Event, ledger)
	if err != nil {
		t.Fatalf("resolve evidence: %v", err)
	}
	if len(deltas) != 2 || len(merges) != 2 {
		t.Fatalf("recovered %d deltas and %d merges, want 2 and 2", len(deltas), len(merges))
	}

	for _, d := range deltas {
		m := d.Baseline
		if !m.Complete() {
			t.Errorf("delta %s: the methodology is not reconstructable: %+v", d.Ref, m)
		}
		if m.ID != "holdout-v1" {
			t.Errorf("delta %s: methodology id = %q — a FIXED methodology is what makes two periods comparable", d.Ref, m.ID)
		}
		// The generating cases are excluded from the held-out split — the exclusion that makes the delta
		// generalize rather than describe the cases it was fitted on.
		for _, g := range m.GeneratingCaseIDs {
			for _, ho := range m.HoldoutCaseIDs {
				if g == ho {
					t.Errorf("delta %s: case %q is in BOTH the generating and held-out splits", d.Ref, g)
				}
			}
		}
		if len(m.Seeds) < 2 {
			t.Errorf("delta %s: a single-seed delta is not a multi-seed verification: %v", d.Ref, m.Seeds)
		}
		if d.Verdict.GateResult != verification.GatePass || !d.Verdict.HeldOut || !d.Verdict.Significant {
			t.Errorf("delta %s: the recovered verdict is not a genuine P5.5 pass: %+v", d.Ref, d.Verdict)
		}
		// And the arithmetic behind the billed figure is checkable from what was recovered.
		if d.Savings() != d.BaselineSUM-d.OptimizedSUM {
			t.Errorf("delta %s: savings arithmetic does not reconstruct", d.Ref)
		}
	}

	// A non-gainshare event, or one with no evidence, is refused rather than answered with a guess.
	if _, _, err := GainshareEvidence(BillingEvent{Type: TypeCharge}, ledger); err == nil {
		t.Error("evidence was resolved for a non-gainshare event")
	}
	if _, _, err := GainshareEvidence(BillingEvent{Type: TypeGainshareCharge}, ledger); err == nil {
		t.Error("evidence was resolved for an event carrying none")
	}
}

// TestSavingsStoreRefusesUntraceableRows: the store is the last line before a figure becomes an invoice.
func TestSavingsStoreRefusesUntraceableRows(t *testing.T) {
	s := metering.NewMemSavingsStore()
	if _, err := s.Upsert(metering.BillableSavings{
		CustomerID: "cus_acme", Period: july.ID, BaselineSUM: 100, OptimizedSUM: 60, Savings: 40,
	}); !errors.Is(err, metering.ErrUntraceableSavings) {
		t.Errorf("a savings row with no evidence was accepted: %v", err)
	}
	if _, err := s.Upsert(metering.BillableSavings{
		CustomerID: "cus_acme", Period: july.ID, Savings: -1,
	}); err == nil {
		t.Error("a negative saving was accepted")
	}
	// A zero-savings row needs no evidence — there is nothing to justify.
	if _, err := s.Upsert(metering.BillableSavings{CustomerID: "cus_acme", Period: july.ID}); err != nil {
		t.Errorf("a zero-savings row was rejected: %v", err)
	}
}

// TestGainshareLedgerReadFailureRefuses: an unreadable P5.5 ledger must refuse, never bill a zero or a
// stale figure.
func TestGainshareLedgerReadFailureRefuses(t *testing.T) {
	h, ledger, savings := gainshareHarness(t, true)
	ledger.Put(mergedVerified("vd1", 100, 60))
	ledger.SetErr(errors.New("verified-delta ledger unavailable"))

	if _, err := h.svc.ChargeGainshare(context.Background(), "cus_acme", july, ledger, savings); err == nil {
		t.Fatal("a gainshare charge proceeded against an unreadable verified-delta ledger")
	}
	if h.provider.ChargeCount() != 0 {
		t.Error("a charge was raised")
	}
}
