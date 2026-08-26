package improvementrun

import (
	"sort"
	"sync"

	"github.com/heros-foreal/agentd/internal/assessment"
)

// health.go is §9.6 and task 9.1: *"runs started / bounded-out / cancelled, proposals generated /
// verified / approved / delivered, deliveries deduplicated — on a **readable health endpoint**, not the
// dashboard."*
//
// # Why not the dashboard, said plainly
//
// A dashboard reads historical aggregates. It can look completely healthy while the pipeline is broken,
// because the aggregate it reads was computed before the break and the break produces no rows to move
// it. The signal an operator needs is a value they can GET right now and alert on, and only the process
// doing the work can publish it.
//
// # 🔴 The counter that matters, and why it needs its own
//
// `Withdrawn`. A withdrawal is the product WORKING — a change that failed to reproduce its verified
// delta was stopped before it reached a customer's repository. So every conventional metric goes the
// right way: the run completed, the API returned 200, no error was logged, latency was normal.
//
// And a withdrawal RATE that climbs means something is wrong that nothing else can see: the eval set has
// become noisy, or a provider moved underneath us. `assessment.Health.AllNotMeasured` is the same shape
// of signal for the same reason, and this is its analogue one phase later.
//
// # Why per-axis rather than a total
//
// §9.5, and `proposal.AxisPassRate` does the arithmetic: an operator with a 5% verification pass rate
// hidden inside a healthy average is an operator that is not working, and the operator with the
// smallest sample is the newest one — so an aggregate is least sensitive exactly where it matters.

// AlertWithdrawalRateAbove is when the rate of withdrawn changes becomes a page.
//
// # Why a quarter
//
// A single withdrawal is ORDINARY and is the feature working: eval sets are noisy and a verified delta
// is a claim about a sample. Alerting on one occurrence would page on the system doing its job.
//
// One in four is not ordinary. At that rate the verification gate is passing changes that do not
// reproduce, which means the gate's evidence is weaker than it reports — and that is a claim about OUR
// measurement rather than about any customer's repository, so it is correlated and it is ours to fix.
//
// 🔴 A CONSTANT and not a configuration key, for `assessment.AlertAllNotMeasuredAbove`'s reason: a
// tunable threshold is a threshold somebody raises during the incident it was meant to catch.
const AlertWithdrawalRateAbove = 0.25

// AxisCell is one axis's count at one stage.
type AxisCell struct {
	Axis  assessment.Axis `json:"axis"`
	Stage Stage           `json:"stage"`
	Count int64           `json:"count"`
}

// Health is what the health endpoint publishes.
type Health struct {
	// PlansCreated counts plans built, INCLUDING ones that never ran. 🔴 Published separately from
	// RunsStarted, because a deployment where they diverge is one where the disclosure threshold is
	// stopping people before they spend — a product signal that is invisible if only started runs count.
	PlansCreated int64 `json:"plans_created"`
	// PlansAwaitingAcknowledgement counts plans above the disclosure threshold at creation.
	PlansAwaitingAcknowledgement int64 `json:"plans_awaiting_acknowledgement"`

	RunsStarted int64 `json:"runs_started"`
	// RunsBoundedOut is runs that stopped on a bound; RunsCancelled is the kill switch; RunsFaulted is
	// a dependency failure. 🚫 The three are never summed into "runs ended": a fault and a bound are
	// different events with different owners, and one aggregate would let a rising fault rate hide
	// inside a steady total.
	RunsBoundedOut int64 `json:"runs_bounded_out"`
	RunsCancelled  int64 `json:"runs_cancelled"`
	RunsFaulted    int64 `json:"runs_faulted"`

	// PerBound is which bound stopped runs, by name. A closed set, so every entry is present even at
	// zero — unlike PerAxis below, where 45 cells at zero would bury the occupied ones.
	PerBound map[Bound]int64 `json:"per_bound"`

	// PerAxis is the breakdown at every stage. A LIST for a stable diff order.
	PerAxis []AxisCell `json:"per_axis"`

	// DeliveriesDeduplicated counts second deliveries that returned the FIRST delivery (FR20). 🔴 A
	// deduplication rate of zero over a busy deployment means the idempotency path is never exercised,
	// and an unexercised idempotency path is one nobody knows is broken.
	DeliveriesDeduplicated int64 `json:"deliveries_deduplicated"`
	// DeliveriesWithheld counts deliveries withheld with a named cause — most often "no installation".
	DeliveriesWithheld int64 `json:"deliveries_withheld"`
	// DeliveriesPendingForge counts deliveries handed to the reconciliation pass (decisions.md D-35.5).
	DeliveriesPendingForge int64 `json:"deliveries_pending_forge"`

	// ProposalsApproved / ProposalsDeclined / ChangesWithdrawn are the consent-and-outcome counters.
	ProposalsApproved int64 `json:"proposals_approved"`
	ProposalsDeclined int64 `json:"proposals_declined"`
	ChangesWithdrawn  int64 `json:"changes_withdrawn"`

	// WithdrawalRate is ChangesWithdrawn over ProposalsApproved. 🔴 -1 when nothing has been approved,
	// never 0.0: a rate over zero approvals is UNDEFINED, and 0.0 would tell a monitor "we checked and
	// it is fine" about a deployment that has never approved anything.
	WithdrawalRate float64 `json:"withdrawal_rate"`
	// AlertAbove is the threshold, published so a monitor does not hard-code it.
	AlertAbove float64 `json:"alert_above"`
	// Alerting is the field to page on. A VALUE rather than a condition the monitor computes, so the
	// rule is stated once, here, next to the reason it is a quarter.
	Alerting bool `json:"alerting"`

	// SpendUSD is total provider spend across runs in this process; WithdrawnSpendUSD is the part spent
	// on changes that were withdrawn (decisions.md D-35.4). PerTenantSpendUSD is task 9.2's attribution.
	SpendUSD          float64            `json:"spend_usd"`
	WithdrawnSpendUSD float64            `json:"withdrawn_spend_usd"`
	PerTenantSpendUSD map[string]float64 `json:"per_tenant_spend_usd,omitempty"`

	// KillSwitchArmed reports whether the operator kill switch is currently armed for this process
	// (task 9.3). Published so an operator can confirm the switch they threw is the one this process
	// reads — a switch whose effect is not observable is a switch nobody trusts in an incident.
	KillSwitchArmed bool `json:"kill_switch_armed"`
	// ReconcileLastSuccessMS is when the reconciliation pass last completed (task 9.4). 🔴 Zero
	// deletions is the normal result of that pass, so "did it do anything" cannot be the signal —
	// "when did it last complete" is, and only the pass can publish it.
	ReconcileLastSuccessMS int64 `json:"reconcile_last_success_ms"`
	// ReconcileResolved counts deliveries the pass completed.
	ReconcileResolved int64 `json:"reconcile_resolved"`
}

// Metrics accumulates the health signal. Safe for concurrent use.
type Metrics struct {
	mu sync.Mutex

	plansCreated, plansAwaiting            int64
	runsStarted, boundedOut, cancelled     int64
	runsFaulted                            int64
	perBound                               map[Bound]int64
	perAxis                                map[AxisCell]int64
	approved, declined, withdrawn          int64
	dedup, withheld, pendingForge          int64
	spend, withdrawnSpend                  float64
	perTenant                              map[string]float64
	killArmed                              bool
	reconcileLastSuccessMS, reconcileFixed int64
}

// NewMetrics returns an empty accumulator.
func NewMetrics() *Metrics {
	return &Metrics{
		perBound: map[Bound]int64{}, perAxis: map[AxisCell]int64{}, perTenant: map[string]float64{},
	}
}

// PlanCreated records a plan being built.
func (m *Metrics) PlanCreated(p Plan) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.plansCreated++
	if p.RequiresAcknowledgement() {
		m.plansAwaiting++
	}
}

// RunStarted records a run beginning.
func (m *Metrics) RunStarted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runsStarted++
}

// RunFinished records everything derivable from a terminal run.
//
// 🔴 It takes the WHOLE run rather than pre-computed numbers, for `assessment.Metrics.Completed`'s
// reason: a caller that passed counts could pass wrong ones; a caller that passes the run cannot.
func (m *Metrics) RunFinished(r Run) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case r.Outcome.Faulted():
		m.runsFaulted++
	case r.Outcome.Bound == BoundKillSwitch:
		m.cancelled++
		m.perBound[BoundKillSwitch]++
	case r.Outcome.Stopped():
		m.boundedOut++
		m.perBound[r.Outcome.Bound]++
	}
	// 🔴 Only the stages the RUN itself produced. Approval, withdrawal and delivery happen in later
	// calls and are counted by their own methods — folding the run's snapshot of them in here as well
	// would double-count every one, because `RunFinished` is called once at the end of the propose
	// phase and those stages are all still zero at that moment.
	for _, row := range r.PerAxis {
		for _, st := range []Stage{StageGenerated, StageVerified} {
			if n := row.Count(st); n > 0 {
				m.perAxis[AxisCell{Axis: row.Axis, Stage: st}] += int64(n)
			}
		}
	}
	m.spend += r.SpendUSD
	m.withdrawnSpend += r.WithdrawnSpendUSD
	if r.TenantID != "" {
		m.perTenant[r.TenantID] += r.SpendUSD
	}
}

// Approved, Declined, Withdrawn and Delivered record the consent-and-outcome events.
//
// 🔴 Each takes the AXIS, and it is not optional. `RunFinished` folds in the per-axis breakdown as it
// stood when the RUN ended — after generation and verification, and before anybody has decided
// anything. Approval, withdrawal and delivery all happen later, in separate calls, so an axis-blind
// counter here would leave `approved` and `delivered` permanently ABSENT from the per-axis document
// while the totals moved. Found by asserting the document by name: the two stages that come after the
// run were simply never in it.
//
// The empty axis is admitted rather than refused — a decision on a proposal whose axis could not be
// read is still a decision, and dropping the count would understate a total to protect a breakdown.
func (m *Metrics) Approved(axis assessment.Axis) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.approved++
	m.bumpAxis(axis, StageApproved)
}

// Declined counts a refusal. 🚫 It is NOT a per-axis STAGE: `Stages()` tracks what happened TO a
// change, and a declined proposal had nothing happen to it. Counting it as a stage would put it in a
// column beside `delivered`, which reads as something the platform did rather than something a person
// chose.
func (m *Metrics) Declined(assessment.Axis) { m.mu.Lock(); m.declined++; m.mu.Unlock() }

func (m *Metrics) Withdrawn(axis assessment.Axis) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.withdrawn++
	m.bumpAxis(axis, StageWithdrawn)
}

func (m *Metrics) Delivered(axis assessment.Axis) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bumpAxis(axis, StageDelivered)
}

// bumpAxis records one (axis, stage) observation. Callers hold the lock.
func (m *Metrics) bumpAxis(axis assessment.Axis, s Stage) {
	if axis == "" {
		return
	}
	m.perAxis[AxisCell{Axis: axis, Stage: s}]++
}

// Deduplicated, Withheld and PendingForge record the delivery outcomes.
func (m *Metrics) Deduplicated() { m.mu.Lock(); m.dedup++; m.mu.Unlock() }
func (m *Metrics) Withheld()     { m.mu.Lock(); m.withheld++; m.mu.Unlock() }
func (m *Metrics) PendingForge() { m.mu.Lock(); m.pendingForge++; m.mu.Unlock() }

// SetKillSwitch records whether the operator kill switch is armed for this process.
func (m *Metrics) SetKillSwitch(armed bool) { m.mu.Lock(); m.killArmed = armed; m.mu.Unlock() }

// ReconcilePassed records a completed reconciliation pass and how many deliveries it resolved.
func (m *Metrics) ReconcilePassed(atMS int64, resolved int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconcileLastSuccessMS = atMS
	m.reconcileFixed += int64(resolved)
}

// Health renders the document.
func (m *Metrics) Health() Health {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := Health{
		PlansCreated: m.plansCreated, PlansAwaitingAcknowledgement: m.plansAwaiting,
		RunsStarted: m.runsStarted, RunsBoundedOut: m.boundedOut,
		RunsCancelled: m.cancelled, RunsFaulted: m.runsFaulted,
		PerBound:               map[Bound]int64{},
		PerAxis:                make([]AxisCell, 0, len(m.perAxis)),
		DeliveriesDeduplicated: m.dedup, DeliveriesWithheld: m.withheld,
		DeliveriesPendingForge: m.pendingForge,
		ProposalsApproved:      m.approved, ProposalsDeclined: m.declined,
		ChangesWithdrawn: m.withdrawn,
		AlertAbove:       AlertWithdrawalRateAbove,
		SpendUSD:         m.spend, WithdrawnSpendUSD: m.withdrawnSpend,
		KillSwitchArmed:        m.killArmed,
		ReconcileLastSuccessMS: m.reconcileLastSuccessMS,
		ReconcileResolved:      m.reconcileFixed,
	}
	// Every bound present, even at zero: it is a closed set of four somebody watches for, and an absent
	// key reads as "we do not measure that".
	for _, b := range BoundsSet() {
		out.PerBound[b] = m.perBound[b]
	}
	for cell, n := range m.perAxis {
		cell.Count = n
		out.PerAxis = append(out.PerAxis, cell)
	}
	sort.Slice(out.PerAxis, func(i, j int) bool {
		if out.PerAxis[i].Axis != out.PerAxis[j].Axis {
			return out.PerAxis[i].Axis < out.PerAxis[j].Axis
		}
		return out.PerAxis[i].Stage < out.PerAxis[j].Stage
	})

	// -1, not 0. See the field's own comment.
	out.WithdrawalRate = -1
	if m.approved > 0 {
		out.WithdrawalRate = float64(m.withdrawn) / float64(m.approved)
		out.Alerting = out.WithdrawalRate > AlertWithdrawalRateAbove
	}
	if len(m.perTenant) > 0 {
		out.PerTenantSpendUSD = make(map[string]float64, len(m.perTenant))
		for k, v := range m.perTenant {
			out.PerTenantSpendUSD[k] = v
		}
	}
	return out
}
