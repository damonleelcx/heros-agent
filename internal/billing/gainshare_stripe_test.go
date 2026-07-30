package billing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/stripefake"
)

// gainshare_stripe_test.go is P21 section 7: the verified-only invariant, held against a REAL provider.
//
// ## Why this file exists when gainshare_test.go already asserts the invariant
//
// Because P21 changed what is behind the interface, and "we did not loosen it" is a claim about the new
// arrangement rather than about the old one. The P7 suite proves the refusals against `StubProvider`,
// which cannot bill anything anyway. These prove them against a provider that CAN — one that will
// happily create an invoice item for whatever it is handed, over HTTP, with an idempotency key.
//
// The property is the same and the stakes are different: once real money can move, "gainshare bills
// only verified, merged savings" stops being a design statement and becomes the difference between an
// invoice a customer can check and one they have to trust.

// stripeGainshareHarness wires the P7 gainshare stack onto the STRIPE provider.
func stripeGainshareHarness(t *testing.T) (*Service, *stripefake.Server, *metering.MemVerifiedDeltas, *metering.MemSavingsStore, *Rollout) {
	t.Helper()
	h := newHarness(t, "enterprise")
	if _, err := h.accounts.SetGainshareConsent("cus_acme", true, julyStart); err != nil {
		t.Fatalf("consent: %v", err)
	}

	// Stripe must know the handle the ACCOUNT already holds, or every call 404s and the test would pass
	// for the wrong reason — a refusal that is really a misconfiguration.
	acct, err := h.accounts.Get("cus_acme")
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	f := newFakeStripe(t)
	f.SeedCustomerHandle(acct.ProviderCustomerHandle, "cus_acme")
	f.SeedSubscriptionFor(acct.ProviderCustomerHandle, "price_ref_ent_sub", "price_ref_ent_metered")

	secrets := stripeSecrets(t, stripefake.TestKey)
	provider, perr := NewStripeProvider(secrets, ModeTest, func() time.Time { return clockNow }, WithStripeBaseURL(f.URL()))
	if perr != nil {
		t.Fatalf("provider: %v", perr)
	}
	svc, serr := NewService(provider, NewMemLedger(), h.accounts, h.plans, h.meter, secrets)
	if serr != nil {
		t.Fatalf("service: %v", serr)
	}
	svc.SetClock(func() time.Time { return clockNow })

	rollout := NewRollout()
	if err := rollout.Enable(ModeTest); err != nil {
		t.Fatalf("rollout: %v", err)
	}
	svc.WithRollout(rollout)
	return svc, f, metering.NewMemVerifiedDeltas(), metering.NewMemSavingsStore(), rollout
}

// TestGainshareOverStripeBillsOnlyVerifiedMergedSavings is task 7.1.
//
// Each case seeds exactly one ledger entry a naive implementation would bill, and asserts three
// independent things: no ledger row, no STRIPE OBJECT, and a refusal that names why.
func TestGainshareOverStripeBillsOnlyVerifiedMergedSavings(t *testing.T) {
	cases := map[string]func(d metering.VerifiedDelta) metering.VerifiedDelta{
		"estimated, not measured": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Estimated = true
			return d
		},
		"verified but never merged": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Merged, d.MergeCommit = false, ""
			return d
		},
		"merged with no commit to point at": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.MergeCommit = ""
			return d
		},
		"gate did not pass": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Verdict.GateResult = "fail"
			return d
		},
		"not held out": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Verdict.HeldOut = false
			return d
		},
		"not significant": func(d metering.VerifiedDelta) metering.VerifiedDelta {
			d.Verdict.Significant = false
			return d
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			svc, f, deltas, savings, rollout := stripeGainshareHarness(t)
			if err := rollout.EnableGainshare(); err != nil {
				t.Fatalf("enable gainshare: %v", err)
			}
			deltas.Put(mutate(mergedVerified("vd_1", 500, 300)))

			before := f.ItemCount()
			res, err := svc.ChargeGainshare(context.Background(), "cus_acme", july, deltas, savings)
			if err == nil {
				t.Fatalf("a %s saving was BILLED: %+v", name, res.Event)
			}
			if res.Event.EventID != "" {
				t.Errorf("a ledger row was written for a %s saving: %+v", name, res.Event)
			}
			// 🔴 The one that matters now that a real provider is behind the interface: NOTHING reached
			// Stripe. A refusal that still created an invoice item would be a refusal on paper only.
			if after := f.ItemCount(); after != before {
				t.Errorf("a %s saving created %d Stripe object(s)", name, after-before)
			}
			// The refusal names the package that owns the fact and says WHY. Which package that is
			// depends on where the saving fell over — `metering:` when the ledger computed nothing
			// billable, `billing:` when a ref failed re-verification at charge time — and asserting one
			// of them would be asserting the implementation rather than the property.
			msg := err.Error()
			if !strings.HasPrefix(msg, "billing:") && !strings.HasPrefix(msg, "metering:") {
				t.Errorf("the refusal is not attributed to a package: %v", err)
			}
			if !strings.Contains(msg, "verified") && !strings.Contains(msg, "merged") {
				t.Errorf("the refusal does not say why it is not billable — a support engineer cannot "+
					"answer 'why was this not billed' from %q", msg)
			}
		})
	}
}

// TestGainshareOverStripeChargesAndTracesToItsEvidence is task 7.2.
func TestGainshareOverStripeChargesAndTracesToItsEvidence(t *testing.T) {
	svc, f, deltas, savings, rollout := stripeGainshareHarness(t)
	if err := rollout.EnableGainshare(); err != nil {
		t.Fatalf("enable gainshare: %v", err)
	}
	// Two merged, verified savings and one that was verified and NOT merged. On this fixture the
	// un-merged one is the LARGER, which is the case worth testing: the invariant costs revenue.
	deltas.Put(mergedVerified("vd_1", 500, 400))
	deltas.Put(mergedVerified("vd_2", 300, 260))
	unmerged := mergedVerified("vd_big", 900, 100)
	unmerged.Merged, unmerged.MergeCommit = false, ""
	deltas.Put(unmerged)

	res, err := svc.ChargeGainshare(context.Background(), "cus_acme", july, deltas, savings)
	if err != nil {
		t.Fatalf("ChargeGainshare: %v", err)
	}

	// Only the merged savings are in the figure: 100 + 40, not the 800 the un-merged one would add.
	if res.Savings.Savings != 140 {
		t.Errorf("billable savings = %v, want 140 (the merged ones only) — the un-merged 800 must not be in it", res.Savings.Savings)
	}
	if res.Event.Type != TypeGainshareCharge || res.Event.Kind != KindGainshare {
		t.Errorf("the row is %s/%s, want a gainshare charge", res.Event.Type, res.Event.Kind)
	}
	if res.Event.Status != StatusRecorded || res.Event.ProviderRef == "" {
		t.Fatalf("the charge did not settle against Stripe: %+v", res.Event)
	}

	// It reached STRIPE, on the plan's gainshare price reference, with the platform's own basis stamped
	// on it — so the read-back invoice line can name what justified it.
	item, ok := f.Item(res.Event.ProviderRef)
	if !ok {
		t.Fatalf("Stripe has no object for %s", res.Event.ProviderRef)
	}
	if item.Price != "price_ref_ent_gainshare" {
		t.Errorf("charged on price %q, want the plan's gainshare price reference", item.Price)
	}
	if item.Metadata["platform_kind"] != string(KindGainshare) {
		t.Errorf("the Stripe object is not marked as a gainshare line: %+v", item.Metadata)
	}
	if item.Quantity != 140 {
		t.Errorf("Stripe recorded quantity %v, want 140", item.Quantity)
	}

	// 🔴 The trace: from the billing event alone, an auditor gets the verifying deltas and the merge
	// commits — each delta carrying its fixed baseline + holdout methodology.
	traced, merges, err := GainshareEvidence(res.Event, deltas)
	if err != nil {
		t.Fatalf("GainshareEvidence: %v", err)
	}
	if len(traced) != 2 || len(merges) != 2 {
		t.Fatalf("traced %d delta(s) and %d merge(s), want 2 and 2", len(traced), len(merges))
	}
	for _, d := range traced {
		if !d.Merged || d.MergeCommit == "" {
			t.Errorf("a traced delta is not merged: %+v", d)
		}
		if d.Baseline.EvalSetHash == "" || len(d.Baseline.HoldoutCaseIDs) == 0 {
			t.Errorf("a traced delta carries no reconstructable methodology: %+v", d.Baseline)
		}
		if d.Ref == "vd_big" {
			t.Error("the UN-MERGED saving is in the evidence — it must not be, and it must not be billed")
		}
	}
}

// TestGainshareOverStripeStaysBehindTheRolloutFlag is task 7.2's second clause.
//
// The flag is the thing standing between "the code is correct" and "the code is trusted with the
// customer's most contestable invoice line". It is checked BEFORE anything is written or called, so a
// dark deployment leaves no pending row a later flip would settle into a real charge.
func TestGainshareOverStripeStaysBehindTheRolloutFlag(t *testing.T) {
	svc, f, deltas, savings, rollout := stripeGainshareHarness(t)
	deltas.Put(mergedVerified("vd_1", 500, 400))

	// Billing is on; gainshare is NOT.
	_, err := svc.ChargeGainshare(context.Background(), "cus_acme", july, deltas, savings)
	if !errors.Is(err, ErrGainshareDisabled) {
		t.Fatalf("a gainshare charge with the flag off returned %v, want ErrGainshareDisabled", err)
	}
	if n := f.ItemCount(); n != 0 {
		t.Errorf("%d Stripe object(s) were created with the gainshare flag off", n)
	}
	if rows := svc.Ledger().Events("cus_acme", july.ID); len(rows) != 0 {
		t.Errorf("%d ledger row(s) were written with the gainshare flag off — a later flip would settle them into real charges", len(rows))
	}

	// Flipping it on makes exactly the same call succeed, so the gate is a gate rather than a wall.
	if err := rollout.EnableGainshare(); err != nil {
		t.Fatalf("enable gainshare: %v", err)
	}
	if _, err := svc.ChargeGainshare(context.Background(), "cus_acme", july, deltas, savings); err != nil {
		t.Fatalf("with the flag on: %v", err)
	}
	if n := f.ItemCount(); n != 1 {
		t.Errorf("%d Stripe object(s), want 1", n)
	}

	// And gainshare cannot be enabled on top of dark billing — a configuration that can do nothing and
	// reads as enabled.
	dark := NewRollout()
	if err := dark.EnableGainshare(); !errors.Is(err, ErrBillingDark) {
		t.Errorf("gainshare was enabled while billing is dark: %v", err)
	}
}

// TestGainshareOverStripeIsChargedExactlyOnce: the same period, charged twice, is one Stripe object and
// one ledger row — the never-double-charge guarantee, on the line where a customer is least likely to
// give the platform the benefit of the doubt.
func TestGainshareOverStripeIsChargedExactlyOnce(t *testing.T) {
	svc, f, deltas, savings, rollout := stripeGainshareHarness(t)
	if err := rollout.EnableGainshare(); err != nil {
		t.Fatalf("enable gainshare: %v", err)
	}
	deltas.Put(mergedVerified("vd_1", 500, 400))

	first, err := svc.ChargeGainshare(context.Background(), "cus_acme", july, deltas, savings)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	for i := 0; i < 3; i++ {
		again, aerr := svc.ChargeGainshare(context.Background(), "cus_acme", july, deltas, savings)
		if aerr != nil {
			t.Fatalf("retry %d: %v", i, aerr)
		}
		if again.Event.EventID != first.Event.EventID {
			t.Fatalf("retry %d produced a DIFFERENT ledger row %s", i, again.Event.EventID)
		}
	}
	if n := f.ItemCount(); n != 1 {
		t.Errorf("stripe holds %d gainshare objects after four charge attempts, want 1", n)
	}
}
