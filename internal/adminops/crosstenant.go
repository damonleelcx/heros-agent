package adminops

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/account"
	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/billing"
	"github.com/heros-foreal/agentd/internal/metering"
)

// crosstenant.go is the cross-tenant read-model surface (FR14, design Decision 7): aggregate usage/SUM,
// COGS/provider spend, revenue-and-operations aggregates, top consumers and anomalies — read-only,
// permission-gated, and LOGGED even when allowed.
//
// # Why an authorized view is still logged
//
// Fleet operation genuinely needs cross-tenant visibility, and any cross-tenant access is a privacy
// event. Those are both true, so the resolution is not to forbid the read but to make it accountable:
// an auditor must be able to answer "who looked at whom, and when". A log that only records refusals
// answers the least interesting half of that question.
//
// # Why the numbers are derived rather than collected
//
// Every value here comes from the P2.5 substrate and P7's ledger through their own APIs — the meter
// derives SUM from cost events, the ledger reports billing events. Nothing is re-collected and nothing
// is cached, so the console cannot report a number that disagrees with the system of record.
//
// # The minimum-cohort floor
//
// An "aggregate" over one tenant is that tenant's data with a different label. The floor suppresses
// values computed over fewer contributing tenants than MinimumCohort, so an operator who lacks
// per-tenant permission cannot re-identify a small tenant by narrowing an aggregate. A single-tenant
// drill-down is available — as an explicitly per-tenant, permission-gated, logged view (DrillDown).

// Aggregate names one cross-tenant read model. Central enum: an aggregate name is what the audit
// entry records, so it must be spelled one way.
type Aggregate string

const (
	// AggregateUsageSUM is spend-under-management across tenants, from the recorded usage records.
	AggregateUsageSUM Aggregate = "usage_sum"
	// AggregateCOGS is provider spend across tenants, derived from the P2.5 cost events.
	AggregateCOGS Aggregate = "cogs_provider_spend"
	// AggregateRevenueOps is the operational shape of the billing ledger — event counts by type and
	// status. Counts, not amounts: the amounts live with the provider.
	AggregateRevenueOps Aggregate = "revenue_ops"
	// AggregateTopConsumers ranks tenants by usage.
	AggregateTopConsumers Aggregate = "top_consumers"
	// AggregateAnomalies surfaces tenants whose state warrants a look — halted loops, unsettled
	// charges, reconciliation drift.
	AggregateAnomalies Aggregate = "anomalies"
)

// Aggregates is every cross-tenant read model, in the order the console presents them.
var Aggregates = []Aggregate{
	AggregateUsageSUM, AggregateCOGS, AggregateRevenueOps, AggregateTopConsumers, AggregateAnomalies,
}

// Valid reports whether a is a known aggregate.
func (a Aggregate) Valid() bool {
	for _, k := range Aggregates {
		if k == a {
			return true
		}
	}
	return false
}

// DisplayName is the operator-facing aggregate name. English, Title Case.
func (a Aggregate) DisplayName() string {
	switch a {
	case AggregateUsageSUM:
		return "Usage (Spend Under Management)"
	case AggregateCOGS:
		return "Provider Spend"
	case AggregateRevenueOps:
		return "Revenue & Operations"
	case AggregateTopConsumers:
		return "Top Consumers"
	case AggregateAnomalies:
		return "Anomalies"
	}
	return string(a)
}

// DefaultMinimumCohort is how many tenants must contribute before an aggregate reports values.
//
// Three is the smallest number for which an aggregate is not trivially invertible by an operator who
// knows one member: with two, knowing your own contribution reveals the other's exactly. It is a
// default rather than a constant because a large deployment should raise it, and nothing may lower it
// to one — NewCrossTenantService refuses that.
const DefaultMinimumCohort = 3

// AggregateRow is one labelled measurement in a read model.
type AggregateRow struct {
	// Label is what the row measures. For a per-tenant ranking this is the tenant id, which is why
	// such rankings are permission-gated and logged like any other cross-tenant read.
	Label string `json:"label"`
	// Value is the measured quantity, from the substrate at request time.
	Value float64 `json:"value"`
	// Unit names what Value counts, so a chart cannot silently mix units.
	Unit string `json:"unit"`
	// Detail is a short non-sensitive note (an anomaly's cause).
	Detail string `json:"detail,omitempty"`
}

// ReadModel is one cross-tenant aggregate.
type ReadModel struct {
	Aggregate   Aggregate `json:"aggregate"`
	DisplayName string    `json:"display_name"`
	Period      string    `json:"period"`
	// Cohort is how many tenants contributed. Reported even when the model is suppressed, because
	// "too few tenants to report" is itself the answer.
	Cohort int `json:"cohort"`
	// Suppressed is set when Cohort is below the floor. The rows are then empty and SuppressionReason
	// says why — a suppressed model is never rendered as an empty result (FR26).
	Suppressed        bool           `json:"suppressed"`
	SuppressionReason string         `json:"suppression_reason,omitempty"`
	Rows              []AggregateRow `json:"rows,omitempty"`
	// Source names the substrate the values came from — evidence that this is a read model over
	// existing truth rather than a second pipeline.
	Source string `json:"source"`
	// Degraded and Detail report that the substrate could not be read. Distinct from an empty result.
	Degraded bool   `json:"degraded"`
	Detail   string `json:"detail,omitempty"`
	// PerTenant marks a single-tenant drill-down, which FR14 treats as a per-tenant view rather than
	// as an aggregate.
	PerTenant string `json:"per_tenant,omitempty"`
}

// CrossTenantService serves the cross-tenant read models.
type CrossTenantService struct {
	exec      *Executor
	accounts  account.Store
	meter     *metering.Meter
	ledger    billing.Ledger
	admission *Admission
	minCohort int
}

// CrossTenantConfig configures the service.
type CrossTenantConfig struct {
	Accounts account.Store
	// Meter derives SUM and provider spend from the P2.5 cost events.
	Meter *metering.Meter
	// Ledger is P7's append-only billing ledger, read for the revenue/ops shape.
	Ledger billing.Ledger
	// Admission reports whether a tenant's loop is halted, for the anomalies model.
	Admission *Admission
	// MinimumCohort overrides DefaultMinimumCohort. It may be raised, never lowered below the default.
	MinimumCohort int
}

// NewCrossTenantService wires the service.
func NewCrossTenantService(exec *Executor, cfg CrossTenantConfig) (*CrossTenantService, error) {
	if exec == nil || cfg.Accounts == nil {
		return nil, errors.New("adminops: the cross-tenant service needs the command path and the account store")
	}
	min := cfg.MinimumCohort
	if min == 0 {
		min = DefaultMinimumCohort
	}
	if min < DefaultMinimumCohort {
		return nil, fmt.Errorf("adminops: a minimum-cohort floor of %d is below the platform floor of %d — "+
			"an aggregate over fewer tenants re-identifies them", min, DefaultMinimumCohort)
	}
	return &CrossTenantService{
		exec: exec, accounts: cfg.Accounts, meter: cfg.Meter, ledger: cfg.Ledger,
		admission: cfg.Admission, minCohort: min,
	}, nil
}

// MinimumCohort reports the live floor, so the console can explain a suppression rather than
// hardcoding the number in its copy.
func (s *CrossTenantService) MinimumCohort() int { return s.minCohort }

// View returns one cross-tenant aggregate. Permission-gated; every authorized view is logged.
func (s *CrossTenantService) View(ctx context.Context, aggregate Aggregate, period metering.Period) (ReadModel, error) {
	if !aggregate.Valid() {
		return ReadModel{}, fmt.Errorf("adminops: %q is not a cross-tenant read model", aggregate)
	}
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapCrossTenantRead, TargetGlobal)
	if err != nil {
		return ReadModel{}, err
	}
	// Logged BEFORE the read, for the same reason the command path audits write-ahead: a crash between
	// the read and the log would leave a look at tenant data with no record of it.
	if err := s.logView(sess.AdminID, string(aggregate), TargetGlobal, period.ID); err != nil {
		return ReadModel{}, err
	}
	return s.build(aggregate, period), nil
}

// DrillDown returns one tenant's slice of an aggregate.
//
// FR14 treats this as a PER-TENANT view rather than as part of an aggregate: it is permission-gated
// and logged against that tenant, so an auditor sees which tenant was looked at rather than only that
// somebody opened a fleet page.
func (s *CrossTenantService) DrillDown(ctx context.Context, tenantID string, aggregate Aggregate, period metering.Period) (ReadModel, error) {
	if !aggregate.Valid() {
		return ReadModel{}, fmt.Errorf("adminops: %q is not a cross-tenant read model", aggregate)
	}
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapCrossTenantRead, TenantTarget(tenantID))
	if err != nil {
		return ReadModel{}, err
	}
	if err := s.logView(sess.AdminID, string(aggregate), TenantTarget(tenantID), period.ID); err != nil {
		return ReadModel{}, err
	}
	m := ReadModel{
		Aggregate: aggregate, DisplayName: aggregate.DisplayName(), Period: period.ID,
		PerTenant: tenantID, Cohort: 1, Source: s.source(),
	}
	acct, err := s.accounts.Get(tenantID)
	if err != nil {
		m.Degraded, m.Detail = true, err.Error()
		return m, nil
	}
	// No cohort floor here: this is explicitly a single-tenant view, gated and logged as one. Applying
	// the floor would suppress the only thing the operator asked for while still having logged the
	// look — worse for privacy AND useless.
	m.Rows = s.rowsFor(aggregate, []account.Account{acct}, period)
	return m, nil
}

// logView records an authorized cross-tenant read. A view that cannot be logged does not happen —
// the same fail-closed rule the command path applies to writes, for the same reason.
func (s *CrossTenantService) logView(adminID, aggregate, target, period string) error {
	_, err := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: adminID,
		Target:       target,
		Action:       adminaudit.ActionCrossTenantView,
		Reason:       "cross-tenant read model",
		Result:       "viewed",
		Evidence:     map[string]string{"read_model": aggregate, "period": period},
		CreatedAt:    s.exec.Now(),
	})
	if err != nil {
		return fmt.Errorf("adminops: cross-tenant view refused — it could not be logged: %w", err)
	}
	return nil
}

func (s *CrossTenantService) source() string {
	if s.meter != nil {
		return "p2.5 cost-event substrate + usage records"
	}
	return "p2.5 substrate (not wired)"
}

// build assembles one aggregate, applying the minimum-cohort floor.
func (s *CrossTenantService) build(aggregate Aggregate, period metering.Period) ReadModel {
	accts := s.accounts.List()
	m := ReadModel{
		Aggregate: aggregate, DisplayName: aggregate.DisplayName(), Period: period.ID,
		Cohort: len(accts), Source: s.source(),
	}
	if len(accts) < s.minCohort {
		m.Suppressed = true
		m.SuppressionReason = fmt.Sprintf(
			"suppressed: %d tenants contributed, below the minimum cohort of %d — an aggregate over fewer "+
				"tenants would re-identify them", len(accts), s.minCohort)
		return m
	}
	m.Rows = s.rowsFor(aggregate, accts, period)
	return m
}

// rowsFor computes one aggregate's rows from the substrate.
func (s *CrossTenantService) rowsFor(aggregate Aggregate, accts []account.Account, period metering.Period) []AggregateRow {
	switch aggregate {
	case AggregateUsageSUM:
		return s.usageRows(accts, period)
	case AggregateCOGS:
		return s.cogsRows(accts, period)
	case AggregateRevenueOps:
		return s.revenueOpsRows(accts, period)
	case AggregateTopConsumers:
		return s.topConsumerRows(accts, period)
	case AggregateAnomalies:
		return s.anomalyRows(accts, period)
	}
	return nil
}

// usageRows totals the RECORDED usage per metric — what the platform metered and will bill from.
func (s *CrossTenantService) usageRows(accts []account.Account, period metering.Period) []AggregateRow {
	if s.meter == nil {
		return nil
	}
	totals := map[metering.Metric]float64{}
	for _, a := range accts {
		for _, metric := range metering.Metrics {
			rec, err := s.meter.Usage().Get(metering.Key{CustomerID: a.CustomerID, Period: period.ID, Metric: metric})
			if err != nil {
				continue // no record for this meter is genuinely zero usage, not a gap
			}
			totals[metric] += rec.Quantity
		}
	}
	rows := make([]AggregateRow, 0, len(totals))
	for _, metric := range metering.Metrics {
		if v, ok := totals[metric]; ok {
			rows = append(rows, AggregateRow{Label: string(metric), Value: v, Unit: string(metric)})
		}
	}
	return rows
}

// cogsRows derives provider spend from the P2.5 cost events — the platform's cost of goods, distinct
// from what was metered.
func (s *CrossTenantService) cogsRows(accts []account.Account, period metering.Period) []AggregateRow {
	if s.meter == nil {
		return nil
	}
	var total float64
	var priced int
	for _, a := range accts {
		res, err := s.meter.DeriveSUM(a.CustomerID, period)
		if err != nil {
			continue
		}
		total += res.Quantity
		priced++
	}
	return []AggregateRow{
		{Label: "provider_spend", Value: total, Unit: "sum"},
		{Label: "tenants_with_priced_spend", Value: float64(priced), Unit: "tenants"},
	}
}

// revenueOpsRows reports the SHAPE of the billing ledger — counts by event type and status. Counts,
// never amounts.
func (s *CrossTenantService) revenueOpsRows(accts []account.Account, period metering.Period) []AggregateRow {
	if s.ledger == nil {
		return nil
	}
	byType := map[string]float64{}
	var pending float64
	for _, a := range accts {
		for _, ev := range s.ledger.Events(a.CustomerID, period.ID) {
			byType[string(ev.Type)]++
			if ev.Status == billing.StatusPending {
				pending++
			}
		}
	}
	rows := make([]AggregateRow, 0, len(byType)+1)
	keys := make([]string, 0, len(byType))
	for k := range byType {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rows = append(rows, AggregateRow{Label: k, Value: byType[k], Unit: "billing_events"})
	}
	rows = append(rows, AggregateRow{Label: "unsettled", Value: pending, Unit: "billing_events",
		Detail: "events still awaiting provider confirmation"})
	return rows
}

// topConsumerRows ranks tenants by metered SUM.
func (s *CrossTenantService) topConsumerRows(accts []account.Account, period metering.Period) []AggregateRow {
	if s.meter == nil {
		return nil
	}
	rows := make([]AggregateRow, 0, len(accts))
	for _, a := range accts {
		rec, err := s.meter.Usage().Get(metering.Key{CustomerID: a.CustomerID, Period: period.ID, Metric: metering.MetricSUM})
		if err != nil {
			continue
		}
		rows = append(rows, AggregateRow{Label: a.CustomerID, Value: rec.Quantity, Unit: string(metering.MetricSUM)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Value == rows[j].Value {
			return rows[i].Label < rows[j].Label
		}
		return rows[i].Value > rows[j].Value
	})
	return rows
}

// anomalyRows surfaces tenants whose state warrants a look. Each row names the tenant AND the cause,
// because an anomaly with no cause is a number nobody can act on.
func (s *CrossTenantService) anomalyRows(accts []account.Account, period metering.Period) []AggregateRow {
	var rows []AggregateRow
	for _, a := range accts {
		if a.Status.Suspended() {
			rows = append(rows, AggregateRow{Label: a.CustomerID, Value: 1, Unit: "anomaly",
				Detail: "tenant is suspended: " + a.SuspensionReason})
		}
		if s.admission != nil {
			allowed, why, err := s.admission.AllowMerge(a.CustomerID)
			switch {
			case err != nil:
				rows = append(rows, AggregateRow{Label: a.CustomerID, Value: 1, Unit: "anomaly",
					Detail: "autonomous-merge admission is indeterminate — merges fail closed to halt"})
			case !allowed && !a.Status.Suspended():
				rows = append(rows, AggregateRow{Label: a.CustomerID, Value: 1, Unit: "anomaly",
					Detail: "autonomous merges halted: " + why})
			}
		}
		if s.ledger != nil {
			for _, ev := range s.ledger.Events(a.CustomerID, period.ID) {
				if ev.Status == billing.StatusPending {
					rows = append(rows, AggregateRow{Label: a.CustomerID, Value: 1, Unit: "anomaly",
						Detail: "billing event " + ev.EventID + " is unsettled"})
				}
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Label+rows[i].Detail < rows[j].Label+rows[j].Detail })
	return rows
}
