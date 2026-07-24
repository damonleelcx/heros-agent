package billing

import (
	"context"
	"testing"

	"github.com/heros-foreal/agentd/internal/metering"
)

// reconcile_test.go is task 4.5 / FR4: seed a drift, assert it is SURFACED rather than silently
// accepted — and, just as load-bearing, that the reconciler did not "fix" it by overwriting either
// ledger.

// reportAll reports every meter the customer has for the period, so the two ledgers start in agreement.
func reportAll(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	recs, err := h.usage.Period("cus_acme", july.ID)
	if err != nil {
		t.Fatalf("period: %v", err)
	}
	for _, r := range recs {
		if _, err := h.svc.ReportUsage(ctx, "cus_acme", july, r.Metric); err != nil {
			t.Fatalf("report %s: %v", r.Metric, err)
		}
	}
}

// snapshotUsage captures the whole platform-side ledger so a later comparison can prove the reconciler
// wrote nothing.
func snapshotUsage(t *testing.T, h *harness) []metering.UsageRecord {
	t.Helper()
	recs, err := h.usage.Period("cus_acme", july.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return recs
}

// TestAMatchingPeriodReconcilesClean is the metering spec's clean scenario, and the precondition for
// every drift test below: with both ledgers agreeing, Reconcile reports matched, raises no alert, and
// running it again yields the same result without mutating anything.
func TestAMatchingPeriodReconcilesClean(t *testing.T) {
	h := newHarness(t, "team")
	ctx := context.Background()
	reportAll(t, h)
	before := snapshotUsage(t, h)
	alerts := &metering.MemAlerts{}

	for i := 0; i < 3; i++ {
		res, err := h.svc.Reconcile(ctx, "cus_acme", july, alerts)
		if err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
		if !res.Matched || len(res.Drift) != 0 {
			t.Fatalf("reconcile %d reported drift on a matching period: %+v", i, res.Drift)
		}
	}
	if len(alerts.Alerts()) != 0 {
		t.Errorf("a clean reconciliation raised %d alerts", len(alerts.Alerts()))
	}
	if got := snapshotUsage(t, h); !sameRecords(before, got) {
		t.Errorf("reconciliation mutated the platform's usage records:\n before %+v\n after  %+v", before, got)
	}
}

// TestDriftIsSurfacedNotSilentlyAccepted is task 4.5 / FR4. Each seeded divergence must come back as a
// NAMED drift with its magnitude, must raise an alert, and must leave both ledgers untouched.
func TestDriftIsSurfacedNotSilentlyAccepted(t *testing.T) {
	cases := []struct {
		name     string
		seed     func(t *testing.T, h *harness)
		wantKind metering.DriftKind
		check    func(t *testing.T, d metering.Drift)
	}{
		{
			name: "the provider is missing usage the platform reported",
			seed: func(t *testing.T, h *harness) {
				reportAll(t, h)
				h.provider.DropUsage("cus_acme", july.ID, string(metering.MetricSUM))
			},
			wantKind: metering.DriftMissingAtProvider,
			check: func(t *testing.T, d metering.Drift) {
				if d.PlatformQuantity != wantSUM {
					t.Errorf("drift does not carry the platform quantity: %+v", d)
				}
			},
		},
		{
			name: "the provider recorded usage the platform never reported",
			seed: func(t *testing.T, h *harness) {
				reportAll(t, h)
				h.provider.InjectUsage("cus_acme", july.ID, string(metering.MetricSeats), 42)
			},
			wantKind: metering.DriftMissingAtPlatform,
			check: func(t *testing.T, d metering.Drift) {
				if d.ProviderQuantity != 42 {
					t.Errorf("drift does not carry the provider quantity: %+v", d)
				}
			},
		},
		{
			name: "the two ledgers disagree about a quantity",
			seed: func(t *testing.T, h *harness) {
				reportAll(t, h)
				h.provider.DropUsage("cus_acme", july.ID, string(metering.MetricSUM))
				h.provider.InjectUsage("cus_acme", july.ID, string(metering.MetricSUM), wantSUM+5)
			},
			wantKind: metering.DriftQuantityMismatch,
			check: func(t *testing.T, d metering.Drift) {
				if d.PlatformQuantity != wantSUM || d.ProviderQuantity != wantSUM+5 {
					t.Errorf("drift does not carry BOTH quantities — an alert with no magnitude cannot be triaged: %+v", d)
				}
			},
		},
		{
			name:     "the platform holds a meter it has not reported yet",
			seed:     func(t *testing.T, h *harness) { /* nothing reported at all */ },
			wantKind: metering.DriftUnreported,
			check: func(t *testing.T, d metering.Drift) {
				if d.PlatformQuantity != wantSUM {
					t.Errorf("drift does not carry the platform quantity: %+v", d)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "team")
			ctx := context.Background()
			tc.seed(t, h)

			before := snapshotUsage(t, h)
			providerBefore, err := h.provider.RecordedUsage(ctx, "cus_acme", july.ID)
			if err != nil {
				t.Fatalf("provider usage: %v", err)
			}
			alerts := &metering.MemAlerts{}

			res, err := h.svc.Reconcile(ctx, "cus_acme", july, alerts)
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}

			// SURFACED, with its kind and its magnitude.
			if res.Matched {
				t.Fatalf("the seeded drift was silently accepted: %+v", res)
			}
			var found *metering.Drift
			for i := range res.Drift {
				if res.Drift[i].Kind == tc.wantKind {
					found = &res.Drift[i]
				}
			}
			if found == nil {
				t.Fatalf("drift kind %q not surfaced; got %+v", tc.wantKind, res.Drift)
			}
			if found.Detail == "" {
				t.Error("the drift carries no explanation — an alert nobody can act on")
			}
			tc.check(t, *found)

			// ALERTED — drift only returned to a caller is drift nobody sees at 3am.
			if len(alerts.Alerts()) != 1 {
				t.Errorf("alerts raised = %d, want 1", len(alerts.Alerts()))
			}

			// NEVER RECONCILED BY OVERWRITE — both ledgers are exactly as they were.
			if got := snapshotUsage(t, h); !sameRecords(before, got) {
				t.Errorf("the reconciler overwrote the platform's usage:\n before %+v\n after  %+v", before, got)
			}
			providerAfter, err := h.provider.RecordedUsage(ctx, "cus_acme", july.ID)
			if err != nil {
				t.Fatalf("provider usage: %v", err)
			}
			if len(providerAfter) != len(providerBefore) {
				t.Errorf("the reconciler wrote to the provider: %d -> %d records", len(providerBefore), len(providerAfter))
			}

			// IDEMPOTENT — a second pass says the same thing and still changes nothing.
			res2, err := h.svc.Reconcile(ctx, "cus_acme", july, alerts)
			if err != nil {
				t.Fatalf("reconcile again: %v", err)
			}
			if res2.Matched != res.Matched || len(res2.Drift) != len(res.Drift) {
				t.Errorf("reconciliation is not idempotent: %+v then %+v", res, res2)
			}
		})
	}
}

// TestRepairIsAdditiveOnly is PRD Q6: the ONE permitted auto-correction is re-reporting missing
// platform→provider usage idempotently. A provider-side excess or a quantity disagreement is NEVER
// auto-repaired — reducing a provider charge goes through the audited credit path.
func TestRepairIsAdditiveOnly(t *testing.T) {
	h := newHarness(t, "team")
	ctx := context.Background()
	alerts := &metering.MemAlerts{}

	// Un-reported usage: repairable, and the repair closes the drift.
	res, err := h.svc.Reconcile(ctx, "cus_acme", july, alerts)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	repaired, err := h.svc.RepairUnreported(ctx, "cus_acme", july, res)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(repaired) != 1 || repaired[0] != metering.MetricSUM {
		t.Fatalf("repaired = %v, want [sum]", repaired)
	}
	after, err := h.svc.Reconcile(ctx, "cus_acme", july, alerts)
	if err != nil {
		t.Fatalf("reconcile after repair: %v", err)
	}
	if !after.Matched {
		t.Errorf("the additive repair did not close the drift: %+v", after.Drift)
	}
	// Repairing twice is a no-op — the report is idempotent under its own key.
	if _, err := h.svc.RepairUnreported(ctx, "cus_acme", july, res); err != nil {
		t.Fatalf("repeat repair: %v", err)
	}
	if h.provider.UsageCount() != 1 {
		t.Errorf("the repair created %d provider usage records, want 1", h.provider.UsageCount())
	}

	// Provider-side excess: NOT auto-repairable.
	h.provider.InjectUsage("cus_acme", july.ID, string(metering.MetricSeats), 42)
	res, err = h.svc.Reconcile(ctx, "cus_acme", july, alerts)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	repaired, err = h.svc.RepairUnreported(ctx, "cus_acme", july, res)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if len(repaired) != 0 {
		t.Errorf("a provider-side excess was auto-repaired (%v) — that is reconcile-by-overwrite", repaired)
	}
	still, err := h.svc.Reconcile(ctx, "cus_acme", july, alerts)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if still.Matched {
		t.Error("the un-repairable drift vanished without an audited correction")
	}
}

// TestReconcileFailsLoudWhenTheProviderCannotBeRead: an unreadable provider is NOT a clean
// reconciliation. Reporting "matched" during an outage would turn it into a false all-clear.
func TestReconcileFailsLoudWhenTheProviderCannotBeRead(t *testing.T) {
	h := newHarness(t, "team")
	ctx := context.Background()
	reportAll(t, h)
	h.provider.SetDown(true)

	res, err := h.svc.Reconcile(ctx, "cus_acme", july, &metering.MemAlerts{})
	if err == nil {
		t.Fatalf("reconcile returned a verdict against an unreadable provider: %+v", res)
	}
	if res.Matched {
		t.Error("a failed reconciliation reported matched — a false all-clear is worse than no reconciliation")
	}
}

func sameRecords(a, b []metering.UsageRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
