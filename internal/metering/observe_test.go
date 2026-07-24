package metering

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// observe_test.go keeps the alert routing and the emitted taxonomy from drifting apart.
//
// This is a specific, real failure: a rule file that names a metric the code stopped emitting looks
// like coverage and fires never; a metric the code emits that no rule mentions is a signal nobody will
// ever see. Both are silent, and both are exactly what the "健康信号必须可外部读取" rule is about —
// a signal that reaches no human is not a health signal.

// TestAlertConfigCoversTheWholeRevenueTaxonomy asserts every emitted revenue metric appears in the
// shipped alert/recording rules, and that nothing in the rules references a metric nobody emits.
func TestAlertConfigCoversTheWholeRevenueTaxonomy(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "p7-revenue-alerts.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	cfg := string(b)
	if len(cfg) < 500 {
		t.Fatalf("%s is only %d bytes — the fence is not seeing the config", path, len(cfg))
	}

	// Read the taxonomy from the CENTRAL enum rather than a local copy, so adding a metric there
	// automatically demands a rule here.
	for _, name := range revenueMetricNames() {
		if !strings.Contains(cfg, name) {
			t.Errorf("metric %q is emitted but appears in no alert or recording rule — a signal that "+
				"reaches no human is not a health signal", name)
		}
	}

	// The two conditions that must PAGE, by name. If somebody downgrades one to a recording rule, this
	// is where it shows up.
	for _, alert := range []string{"P7BillingChargeFailed", "P7BillingReconciliationDrift"} {
		if !strings.Contains(cfg, "alert: "+alert) {
			t.Errorf("%s is not a paging alert — a failed charge and a drift are the two conditions "+
				"that are invisible until month-end unless something pages", alert)
		}
	}
	// Every alert carries a runbook: a page with no next step is an alarm, not an alert.
	if strings.Count(cfg, "runbook:") < 2 {
		t.Error("an alert has no runbook — a page nobody can act on")
	}
	// And the aggregate-only rule is honoured: no rule may group by customer, which would cost a series
	// per customer forever.
	if strings.Contains(cfg, "by (customer_id)") || strings.Contains(cfg, "by(customer_id)") {
		t.Error("a rule groups by customer_id — that is one TSDB series per customer, the exact " +
			"cardinality explosion the label allowlist exists to prevent")
	}
}

// revenueMetricNames reads the taxonomy out of the telemetry package's central enum via the source
// file, so the fence cannot be satisfied by a stale local list. Reading the SOURCE rather than
// importing the slice is deliberate: it also catches a metric name added to the file but left out of
// RevenueMetricNames.
func revenueMetricNames() []string {
	b, err := os.ReadFile(filepath.Join("..", "telemetry", "attributes.go"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, "MetricRevenue") || !strings.Contains(line, "=") {
			continue
		}
		if i, j := strings.Index(line, `"`), strings.LastIndex(line, `"`); i >= 0 && j > i {
			out = append(out, line[i+1:j])
		}
	}
	return out
}

// TestObserverIsSafeWhenNothingIsWired: a nil observer and a wired-but-sink-less one must both be
// callable. Every caller in the billing path invokes the observer unconditionally, so this is what
// keeps a telemetry-free deployment from panicking on its first charge.
func TestObserverIsSafeWhenNothingIsWired(t *testing.T) {
	var nilObs *Observer
	obs := NewObserver(nil, nil)

	for _, o := range []*Observer{nilObs, obs} {
		o.SUMRecorded("c", july, 1)
		o.UsageReported("c", july, MetricSUM, 1)
		o.InvoiceState("c", july.ID, "paid")
		o.ChargeFailed("c", july.ID, "metered", 1, "provider down")
		o.GainshareBilled("c", july, 1, 2)
		o.DriftDetected(ReconcileResult{CustomerID: "c", Period: july.ID,
			Drift: []Drift{{Kind: DriftMissingAtProvider, Metric: MetricSUM, Detail: "d"}}})
	}
}
