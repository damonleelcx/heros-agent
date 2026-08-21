package assessment

import (
	"context"
	"sort"
	"sync"
)

// health.go is §9.6's obligation: *"assessments started / completed / refused, per axis and per state,
// on a readable health endpoint."*
//
// # 🔴 Why the AGGREGATE is refused as the only figure
//
// §9.6 names the signal precisely: *"How many assessments produced nine `not_measured` findings is the
// single best early signal that a frontend or the sandbox broke, and it is invisible in an aggregate
// success rate."*
//
// An assessment that returns nine absences is a SUCCESS by every aggregate measure. It completed. It
// returned 201. It persisted nine rows. Nothing errored, nothing retried, no latency moved. The only
// thing that changed is that the product stopped saying anything — and there is no number on a
// conventional dashboard that goes the wrong way when that happens.
//
// So `AllNotMeasured` is its own counter, and `Rate` is published beside `Started` rather than instead
// of it: a ratio of 1.0 over one assessment and a ratio of 1.0 over four hundred are different
// emergencies, and a single float cannot say which is happening.
//
// # Why this is in-memory and the store also computes it
//
// They answer different questions and both are wanted. This one is CHEAP and covers the process's
// lifetime, so `/readyz` can carry it on every probe without touching the database — which is what
// makes it safe to alert on. `PGStore.AxisStateBreakdown` covers a WINDOW across replicas and
// restarts, which is what an investigation needs after the alert fires. Neither is a substitute: a
// counter that resets on deploy cannot answer "was it like this yesterday", and a query that costs a
// scan cannot run every fifteen seconds.

// AxisStateCell is one (axis, state) count.
type AxisStateCell struct {
	Axis  Axis  `json:"axis"`
	State State `json:"state"`
	Count int64 `json:"count"`
}

// Health is what the health endpoint publishes.
type Health struct {
	// Started, Completed and Refused are the three lifecycle counters §9.6 names.
	//
	// `Refused` is an assessment that could not be produced at all — no source, an unresolvable
	// evidence reference, a store failure. It is NOT an assessment whose findings are `refused`: those
	// are a normal, healthy outcome and they live in the per-axis breakdown. Conflating them would make
	// the two P34 axes, which are `refused` on every assessment today, read as a fleet-wide failure.
	Started   int64 `json:"started"`
	Completed int64 `json:"completed"`
	Refused   int64 `json:"refused"`

	// AllNotMeasured is task 6.2's counter: assessments that completed with every one of the nine axes
	// in `not_measured`.
	AllNotMeasured int64 `json:"all_not_measured"`
	// AllNotMeasuredRate is that count over `Completed`. Published as well as the counts so a monitor
	// does not divide two fields and get it wrong; -1 when nothing has completed, because a rate over
	// zero assessments is not 0.0 — 0.0 would say "we checked and it is fine".
	AllNotMeasuredRate float64 `json:"all_not_measured_rate"`
	// AlertAbove is the threshold, published so a monitor does not hard-code it.
	AlertAbove float64 `json:"alert_above"`
	// Alerting is the field to page on. A VALUE rather than a log-line regex, and rather than a
	// condition the monitor computes: the rule is stated once, here, where the reason it is 0.5 is
	// written down next to it.
	Alerting bool `json:"alerting"`

	// PerAxis is the breakdown. 🔴 A LIST rather than a map, so its ordering is stable in a diff and a
	// test can assert "these entries, in this order" without sorting keys.
	//
	// Every (axis, state) pair that has EVER been counted appears. A pair at zero is absent, which is
	// correct here and not the trap `ForgeStats.ByCause` avoids: there, four causes are a closed set
	// somebody watches for; here it is 36 cells and rendering the empty ones would bury the occupied
	// ones under a wall of zeros.
	PerAxis []AxisStateCell `json:"per_axis"`

	// SpendUSD is total provider spend across assessments in this process, and PerTenantSpendUSD is
	// task 6.4's attribution.
	SpendUSD          float64            `json:"spend_usd"`
	PerTenantSpendUSD map[string]float64 `json:"per_tenant_spend_usd,omitempty"`
}

// AlertAllNotMeasuredAbove is when the rate of nine-absence assessments becomes a page.
//
// # Why one half and not something smaller
//
// A single assessment returning nine absences is ORDINARY: a customer connects a repository written in
// a language no frontend handles, or pushes a snapshot of a directory with no LLM calls in it. Alerting
// on one occurrence would page on a customer's honest first attempt.
//
// Half of all completed assessments is not ordinary. It means the thing that is failing is on OUR side
// — a language frontend that stopped emitting nodes, a sandbox that refuses everything, a source store
// returning empty trees — because customer repositories do not become unreadable in a correlated way.
//
// 🔴 The threshold is a CONSTANT and not a configuration key, deliberately. A tunable threshold is a
// threshold somebody raises during the incident it was meant to catch.
const AlertAllNotMeasuredAbove = 0.5

// Metrics accumulates the health signal. Safe for concurrent use.
type Metrics struct {
	mu sync.Mutex

	started, completed, refused int64
	allNotMeasured              int64
	perAxis                     map[AxisStateCell]int64
	spend                       float64
	perTenant                   map[string]float64
}

// NewMetrics returns an empty accumulator.
func NewMetrics() *Metrics {
	return &Metrics{perAxis: map[AxisStateCell]int64{}, perTenant: map[string]float64{}}
}

// Started records an assessment beginning.
func (m *Metrics) Started() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started++
}

// Refused records an assessment that could not be produced at all.
func (m *Metrics) Refused() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refused++
}

// Completed records a finished assessment and everything derivable from it.
//
// 🔴 It takes the WHOLE assessment rather than a set of pre-computed numbers, so the per-axis
// breakdown and the all-not-measured count cannot disagree with the report they describe. A caller
// that passed counts could pass wrong ones; a caller that passes the report cannot.
func (m *Metrics) Completed(a Assessment) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed++
	if a.AllNotMeasured() {
		m.allNotMeasured++
	}
	for _, f := range a.Findings {
		m.perAxis[AxisStateCell{Axis: f.Axis(), State: f.State()}]++
	}
	m.spend += a.SpendUSD
	if a.TenantID != "" {
		m.perTenant[a.TenantID] += a.SpendUSD
	}
}

// Health renders the document.
func (m *Metrics) Health() Health {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := Health{
		Started: m.started, Completed: m.completed, Refused: m.refused,
		AllNotMeasured: m.allNotMeasured,
		AlertAbove:     AlertAllNotMeasuredAbove,
		SpendUSD:       m.spend,
		PerAxis:        make([]AxisStateCell, 0, len(m.perAxis)),
	}
	// -1, not 0. A rate over zero completed assessments is UNDEFINED, and 0.0 would tell a monitor
	// "we checked and it is fine" about a deployment that has never assessed anything.
	out.AllNotMeasuredRate = -1
	if m.completed > 0 {
		out.AllNotMeasuredRate = float64(m.allNotMeasured) / float64(m.completed)
		out.Alerting = out.AllNotMeasuredRate > AlertAllNotMeasuredAbove
	}

	for cell, n := range m.perAxis {
		cell.Count = n
		out.PerAxis = append(out.PerAxis, cell)
	}
	// Sorted by the AXIS's report order, then by state, so the document reads like the report does.
	sort.Slice(out.PerAxis, func(i, j int) bool {
		if a, b := axisOrder(out.PerAxis[i].Axis), axisOrder(out.PerAxis[j].Axis); a != b {
			return a < b
		}
		return out.PerAxis[i].State < out.PerAxis[j].State
	})

	if len(m.perTenant) > 0 {
		out.PerTenantSpendUSD = make(map[string]float64, len(m.perTenant))
		for k, v := range m.perTenant {
			out.PerTenantSpendUSD[k] = v
		}
	}
	return out
}

// Observer is the seam the runner reports through. An interface so a deployment can publish to
// something other than `/readyz` without this package learning about it, and so a runner with no
// observer is a runner that simply does not report — telemetry is not a precondition for assessing
// anything.
type Observer interface {
	Started()
	Refused()
	Completed(a Assessment)
}

// SpendExport is task 6.4: what each tenant's assessments cost, in a shape something else can bill,
// invoice or chart from.
//
// 🚫 It is an EXPORT, not a charge. PRD §14 A2: an inference is the PLATFORM's spend, and P7 G9 is
// that the platform never resells provider tokens — `billing.ErrResoldTokens` enforces it on the
// invoice. This exists so an operator can see which tenant's repositories are expensive to analyse,
// which is a capacity question, and so a per-tenant ceiling has something to be checked against.
type SpendExport struct {
	TenantID string  `json:"tenant_id"`
	SpendUSD float64 `json:"spend_usd"`
	// Assessments is the count, so a per-assessment average is available without dividing by a number
	// from a second document.
	Assessments int64 `json:"assessments"`
}

// ExportSpend reads per-tenant provider spend over a window from the store.
func (s *PGStore) ExportSpend(parent context.Context, sinceMS int64) ([]SpendExport, error) {
	ctx, cancel := s.ctx(parent)
	defer cancel()
	rows, err := s.db.QueryContext(ctx,
		`SELECT tenant_id, COALESCE(SUM(spend_usd), 0), COUNT(*)
		   FROM assessment WHERE started_at_ms >= $1
		  GROUP BY tenant_id ORDER BY tenant_id`, sinceMS)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SpendExport{}
	for rows.Next() {
		var e SpendExport
		if err := rows.Scan(&e.TenantID, &e.SpendUSD, &e.Assessments); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
