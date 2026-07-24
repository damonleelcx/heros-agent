package metering

import (
	"context"
	"fmt"
	"sort"
)

// reconcile.go compares the platform's `usage_record` against the billing provider's recorded usage
// and SURFACES any divergence (task 4.2 / design Decision 7).
//
// ## Two systems of record, one reconciliation, no third writer
//
// `usage_record` is the system of record for **what was used**. The provider is the system of record
// for **what was charged**. Two ledgers that must agree will drift unless something actively keeps them
// honest — but the reconciler must never become a THIRD writer that papers over disagreement by
// clobbering one side. So Reconcile is READ-ONLY: it holds no store handle it can write through, it
// reports drift, and it does not decide what to do about it.
//
// That is not squeamishness. "Reconcile by overwrite" is how a meter and a provider silently converge
// on a number neither of them measured, and month-end becomes a reconstruction. The permitted repairs
// live elsewhere and are both additive: re-report missing platform→provider usage idempotently (which
// changes nothing if it was already there), or issue an audited CREDIT (which never reduces a prior
// record).
//
// Reconcile is also idempotent: running it twice yields the same verdict and mutates nothing.

// DriftKind names one way the two ledgers can disagree. Each is a different operational response, which
// is why they are distinct rather than one "mismatch": a record the provider is missing is un-billed
// revenue, a record only the provider has is a possible over-charge, and a quantity mismatch is a
// measurement disagreement.
type DriftKind string

const (
	// DriftMissingAtProvider: the platform holds a usage record the provider did not record. Revenue the
	// customer used and was not billed for.
	DriftMissingAtProvider DriftKind = "missing_at_provider"
	// DriftMissingAtPlatform: the provider recorded usage the platform never reported. A charge with no
	// platform-side justification — the shape an over-charge takes.
	DriftMissingAtPlatform DriftKind = "missing_at_platform"
	// DriftQuantityMismatch: both sides hold the meter, with different quantities.
	DriftQuantityMismatch DriftKind = "quantity_mismatch"
	// DriftUnreported: the platform holds the record and has NOT yet reported it, and the provider
	// agrees it is absent. Distinguished from DriftMissingAtProvider because the repair is different and
	// benign — report it — whereas a record the platform believes it already reported and the provider
	// does not have is a genuine loss.
	DriftUnreported DriftKind = "unreported"
)

// Drift is one disagreement between the two ledgers.
type Drift struct {
	Kind   DriftKind `json:"kind"`
	Metric Metric    `json:"metric"`
	Period string    `json:"period"`
	// PlatformQuantity / ProviderQuantity are both reported so the alert says HOW MUCH, not just that
	// something is wrong. A drift alert with no magnitude cannot be triaged.
	PlatformQuantity float64 `json:"platform_quantity"`
	ProviderQuantity float64 `json:"provider_quantity"`
	// Detail is a short human explanation for the alert and the UI.
	Detail string `json:"detail"`
}

// ReconcileResult is the verdict for one customer-period.
type ReconcileResult struct {
	CustomerID string  `json:"customer_id"`
	Period     string  `json:"period"`
	Matched    bool    `json:"matched"`
	Drift      []Drift `json:"drift,omitempty"`
}

// ProviderUsage is what the billing provider says it recorded. Declared here rather than imported from
// the billing package so the dependency runs one way only (billing knows about metering; metering does
// not know about billing) — the meter must be usable without a billing provider wired at all.
type ProviderUsage struct {
	Metric   string
	Period   string
	Quantity float64
	UsageRef string
}

// ProviderUsageSource is the provider's side of the comparison — READ ONLY, by construction. There is
// no write method on this interface, so the reconciler is structurally incapable of overwriting the
// provider's records even if a future change wanted it to.
type ProviderUsageSource interface {
	RecordedUsage(ctx context.Context, customerID, period string) ([]ProviderUsage, error)
}

// AlertSink receives drift. Drift that is only returned to a caller is drift nobody sees at 3am, so the
// reconciler pushes it at an alerting surface as well as returning it (task 8.1, G11).
type AlertSink interface {
	DriftDetected(res ReconcileResult)
}

// MemAlerts is an in-memory AlertSink for the demo and the tests.
type MemAlerts struct{ results []ReconcileResult }

// DriftDetected records a drifting reconciliation.
func (a *MemAlerts) DriftDetected(res ReconcileResult) { a.results = append(a.results, res) }

// Alerts returns every drift alert raised.
func (a *MemAlerts) Alerts() []ReconcileResult { return a.results }

// Reconciler compares the two ledgers for a customer-period.
type Reconciler struct {
	usage    UsageStore
	provider ProviderUsageSource
	alerts   AlertSink
}

// NewReconciler builds a reconciler. The usage store is taken as the READ interface it is; nothing in
// this type calls Upsert or MarkReported.
func NewReconciler(usage UsageStore, provider ProviderUsageSource, alerts AlertSink) *Reconciler {
	return &Reconciler{usage: usage, provider: provider, alerts: alerts}
}

// Reconcile compares the platform's usage records for a period against the provider's recorded usage.
//
// It returns `{matched, drift[]}` and raises an alert when drift is present. It writes nothing on
// either side — a divergence is reported, never resolved by fiat.
func (r *Reconciler) Reconcile(ctx context.Context, customerID string, p Period) (ReconcileResult, error) {
	platform, err := r.usage.Period(customerID, p.ID)
	if err != nil {
		return ReconcileResult{}, fmt.Errorf("metering: read platform usage for %s/%s: %w", customerID, p.ID, err)
	}
	recorded, err := r.provider.RecordedUsage(ctx, customerID, p.ID)
	if err != nil {
		// A provider that cannot be read is NOT a clean reconciliation. Returning "matched" here would
		// turn an outage into a false all-clear, which is worse than no reconciliation at all.
		return ReconcileResult{}, fmt.Errorf("metering: read provider usage for %s/%s: %w", customerID, p.ID, err)
	}

	byProvider := make(map[string]ProviderUsage, len(recorded))
	for _, u := range recorded {
		byProvider[u.Metric] = u
	}
	seen := make(map[string]bool, len(platform))

	res := ReconcileResult{CustomerID: customerID, Period: p.ID}
	for _, rec := range platform {
		seen[string(rec.Metric)] = true
		pu, ok := byProvider[string(rec.Metric)]
		switch {
		case !ok && !rec.ReportedToProvider:
			res.Drift = append(res.Drift, Drift{
				Kind: DriftUnreported, Metric: rec.Metric, Period: p.ID,
				PlatformQuantity: rec.Quantity,
				Detail:           "the platform holds this meter and has not reported it yet — repair by reporting it idempotently",
			})
		case !ok:
			res.Drift = append(res.Drift, Drift{
				Kind: DriftMissingAtProvider, Metric: rec.Metric, Period: p.ID,
				PlatformQuantity: rec.Quantity,
				Detail: "the platform reported this meter (ref " + rec.ProviderUsageRef +
					") but the provider has no record of it — usage the customer will not be billed for",
			})
		case pu.Quantity != rec.Quantity:
			res.Drift = append(res.Drift, Drift{
				Kind: DriftQuantityMismatch, Metric: rec.Metric, Period: p.ID,
				PlatformQuantity: rec.Quantity, ProviderQuantity: pu.Quantity,
				Detail: "the platform and the provider disagree about this meter's quantity",
			})
		}
	}
	for metric, pu := range byProvider {
		if seen[metric] {
			continue
		}
		res.Drift = append(res.Drift, Drift{
			Kind: DriftMissingAtPlatform, Metric: Metric(metric), Period: p.ID,
			ProviderQuantity: pu.Quantity,
			Detail: "the provider recorded this meter (ref " + pu.UsageRef +
				") but the platform never reported it — a charge with no platform-side justification",
		})
	}

	sort.Slice(res.Drift, func(i, j int) bool {
		if res.Drift[i].Metric != res.Drift[j].Metric {
			return metricRank(res.Drift[i].Metric) < metricRank(res.Drift[j].Metric)
		}
		return res.Drift[i].Kind < res.Drift[j].Kind
	})
	res.Matched = len(res.Drift) == 0

	if !res.Matched && r.alerts != nil {
		r.alerts.DriftDetected(res)
	}
	return res, nil
}
