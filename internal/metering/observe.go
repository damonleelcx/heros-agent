package metering

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// observe.go emits the P7 revenue signals on the **P2.5 substrate** (task 8.1 / G11).
//
// ## Why not a separate revenue metrics path
//
// The same reason SUM is a derivation rather than a second collector: two telemetry paths for the same
// business drift apart, and the one nobody is looking at is the one that is wrong. Revenue rides the
// existing `telemetry.Sink` — the same emission gate, the same cardinality filter, the same scrubber,
// the same three stores. Wiring a revenue dashboard is then a query, not a pipeline.
//
// ## What ALERTS, and why only those two
//
// A **failed charge** is revenue that silently did not happen; a **reconciliation drift** is the meter
// and the provider disagreeing about what a customer used. Both are invisible until month-end unless
// something pages, and both are actionable the moment they occur. Everything else here is a signal to
// graph, not to wake someone for — an alert that fires on a number moving is an alert people mute.
//
// ## Failing to observe never fails the business operation
//
// Emission is best-effort by contract (`Sink` cannot block or error), and the alert sink is called
// after the metric. A telemetry outage must not be able to stop a charge from settling or a period from
// closing — the same rule P2.5 Decision 7 applies to a paid provider call.

// AlertKind names an alertable revenue condition. Two, deliberately.
type AlertKind string

const (
	// AlertFailedCharge: a charge-bearing operation did not settle.
	AlertFailedCharge AlertKind = "failed_charge"
	// AlertReconciliationDrift: the platform's usage record and the provider's disagree.
	AlertReconciliationDrift AlertKind = "reconciliation_drift"
)

// Alert is one alertable condition, carrying enough to triage without a second query.
type Alert struct {
	Kind       AlertKind `json:"kind"`
	CustomerID string    `json:"customer_id"`
	Period     string    `json:"period"`
	// Detail is the human explanation. An alert with no detail is a page nobody can act on.
	Detail string `json:"detail"`
	// Quantity is the magnitude where one exists (the drifting amount, the failed charge's quantity).
	Quantity float64   `json:"quantity"`
	At       time.Time `json:"at"`
}

// Alerter receives revenue alerts. Separate from the metric sink because they answer different
// questions: the sink answers "what is the trend", the alerter answers "who needs to look at this now".
type Alerter interface {
	RevenueAlert(a Alert)
}

// Observer emits the revenue taxonomy and raises the two alerts.
type Observer struct {
	sink    telemetry.Sink
	alerter Alerter
	now     func() time.Time
}

// NewObserver builds a revenue observer. Both dependencies are optional: a deployment with no
// telemetry sink still bills correctly, and a nil observer is safe to call (see the nil guards) so no
// caller has to branch on whether observability is wired.
func NewObserver(sink telemetry.Sink, alerter Alerter) *Observer {
	return &Observer{sink: sink, alerter: alerter, now: time.Now}
}

// SetClock injects a deterministic clock (tests).
func (o *Observer) SetClock(now func() time.Time) { o.now = now }

// event builds one billing-scoped metric event: the reserved tags, the real timestamp, and the
// customer/period as DIMENSIONS (never labels — a series per customer is the cardinality explosion
// Decision 4 exists to prevent).
func (o *Observer) event(name string, value float64, unit, customerID, period string, dims map[string]any) metricevent.Event {
	seed := int64(0)
	v := value
	d := map[string]any{
		telemetry.AttrCustomerID:    customerID,
		telemetry.AttrBillingPeriod: period,
	}
	for k, val := range dims {
		d[k] = val
	}
	return metricevent.Event{
		SchemaVersion: metricevent.SchemaVersion,
		VariantID:     telemetry.VariantIDBilling,
		RunID:         telemetry.RunIDBilling,
		NodeID:        telemetry.NodeIDBilling,
		CaseID:        telemetry.CaseIDBilling,
		Seed:          &seed,
		Timestamp:     o.now().UTC().Format(time.RFC3339Nano),
		ConfigHash:    telemetry.ConfigHashBilling,
		MetricName:    name,
		Value:         &v,
		Unit:          unit,
		Dimensions:    d,
	}
}

func (o *Observer) emit(ev metricevent.Event) {
	if o == nil || o.sink == nil {
		return
	}
	o.sink.EmitMetric(context.Background(), ev)
}

func (o *Observer) alert(a Alert) {
	if o == nil || o.alerter == nil {
		return
	}
	a.At = o.now().UTC()
	o.alerter.RevenueAlert(a)
}

// SUMRecorded reports a customer-period's derived spend under management.
func (o *Observer) SUMRecorded(customerID string, p Period, quantity float64) {
	if o == nil {
		return
	}
	o.emit(o.event(telemetry.MetricRevenueSUM, quantity, telemetry.UnitUSD, customerID, p.ID, nil))
}

// UsageReported reports a metered quantity handed to the billing provider.
func (o *Observer) UsageReported(customerID string, p Period, metric Metric, quantity float64) {
	if o == nil {
		return
	}
	o.emit(o.event(telemetry.MetricRevenueMetered, quantity, telemetry.UnitCount, customerID, p.ID,
		map[string]any{telemetry.AttrMeterName: string(metric)}))
}

// InvoiceState reports an invoice/subscription state the provider told us about. The STATE is a
// dimension and the value is 1: a state is not a quantity, and encoding it as one (paid=1, failed=0)
// is how a dashboard ends up averaging statuses.
func (o *Observer) InvoiceState(customerID, period, state string) {
	if o == nil {
		return
	}
	o.emit(o.event(telemetry.MetricRevenueInvoiceState, 1, telemetry.UnitCount, customerID, period,
		map[string]any{telemetry.AttrBillingState: state}))
}

// ChargeFailed reports — and ALERTS on — a charge that did not settle.
func (o *Observer) ChargeFailed(customerID, period, kind string, quantity float64, detail string) {
	if o == nil {
		return
	}
	o.emit(o.event(telemetry.MetricRevenueChargeFailed, 1, telemetry.UnitCount, customerID, period,
		map[string]any{telemetry.AttrChargeKind: kind}))
	o.alert(Alert{Kind: AlertFailedCharge, CustomerID: customerID, Period: period,
		Quantity: quantity, Detail: detail})
}

// GainshareBilled reports a gainshare charge's billed quantity — verified savings only, by
// construction, since nothing else can produce one.
func (o *Observer) GainshareBilled(customerID string, p Period, savings float64, deltaRefs int) {
	if o == nil {
		return
	}
	o.emit(o.event(telemetry.MetricRevenueGainshareBilled, savings, telemetry.UnitUSD, customerID, p.ID,
		map[string]any{"verified_delta_count": strconv.Itoa(deltaRefs)}))
}

// DriftDetected reports — and ALERTS on — every drift a reconciliation surfaced. It satisfies
// AlertSink, so the reconciler pushes straight at it.
func (o *Observer) DriftDetected(res ReconcileResult) {
	if o == nil {
		return
	}
	for _, d := range res.Drift {
		o.emit(o.event(telemetry.MetricRevenueReconcileDrift, 1, telemetry.UnitCount, res.CustomerID, res.Period,
			map[string]any{
				telemetry.AttrDriftKind: string(d.Kind),
				telemetry.AttrMeterName: string(d.Metric),
			}))
		o.alert(Alert{Kind: AlertReconciliationDrift, CustomerID: res.CustomerID, Period: res.Period,
			Quantity: d.PlatformQuantity - d.ProviderQuantity, Detail: string(d.Kind) + ": " + d.Detail})
	}
}

// MemAlerter is an in-memory Alerter for the demo and the tests. It satisfies AlertSink as well, so
// ONE object can be handed to both the reconciler and the observer — which matters because a test that
// wires two different collectors then has to remember which one saw what.
type MemAlerter struct {
	mu     sync.Mutex
	alerts []Alert
}

// DriftDetected turns a drifting reconciliation into alerts, satisfying AlertSink. The reconciler can
// therefore push straight at a MemAlerter without an Observer in between — the shape a test that only
// cares about alerting wants.
func (m *MemAlerter) DriftDetected(res ReconcileResult) {
	for _, d := range res.Drift {
		m.RevenueAlert(Alert{
			Kind: AlertReconciliationDrift, CustomerID: res.CustomerID, Period: res.Period,
			Quantity: d.PlatformQuantity - d.ProviderQuantity,
			Detail:   string(d.Kind) + ": " + d.Detail,
		})
	}
}

// RevenueAlert records an alert.
func (m *MemAlerter) RevenueAlert(a Alert) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts = append(m.alerts, a)
}

// Alerts returns every alert raised, in order.
func (m *MemAlerter) Alerts() []Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Alert(nil), m.alerts...)
}

// OfKind returns the alerts of one kind.
func (m *MemAlerter) OfKind(k AlertKind) []Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Alert
	for _, a := range m.alerts {
		if a.Kind == k {
			out = append(out, a)
		}
	}
	return out
}
