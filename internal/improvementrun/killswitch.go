package improvementrun

import "context"

// killswitch.go is FR27 and task 9.3: the operator's brake, reachable from the operator console and
// effective on a run in flight.
//
// # Why the run reads the PLATFORM's switch and not only its own
//
// `optimizer.Controller` already holds a per-run `KillSwitch` — an in-process flag this run's own
// caller fires. That is the customer's cancel, and it is enough for the customer.
//
// It is not enough for an operator. The switch an operator throws is the one in the operator console:
// set from OUTSIDE this process, effective immediately, with no deploy. A runaway fleet needs that one,
// and a run that consulted only its own flag would keep spending while somebody watched a dashboard
// they had already stopped.
//
// So the two are wired to the SAME check points — before the run starts, and at the delivery gate —
// rather than to two, because two enforcement points is two that can drift. `optimizer.Controller`
// makes exactly this argument for `MergeAdmission` beside `Kill`, and this is that argument one layer
// up.
//
// # 🔴 An unreadable switch HALTS
//
// "Cannot tell if we are stopped" means stopped. It is P8 design Decision 4, and the direction is the
// one that costs nothing to be wrong about: refusing a run somebody wanted wastes a click, while
// admitting one somebody stopped spends money against an operator's explicit instruction.

// OperatorBrake is the platform's kill switch, as this package sees it.
//
// Declared here as the narrowest possible interface — one method — for `optimizer.MergeAdmission`'s
// reason: the run must remain buildable and testable with no operator console at all.
// `adminops.KillSwitchService` satisfies it.
type OperatorBrake interface {
	// HaltsMerge reports whether the platform currently halts autonomous action for this tenant.
	//
	// 🔴 The error return is the load-bearing part. It means the state is INDETERMINATE — the switch's
	// store is unreachable — and its only correct handling is to halt. An implementation must never
	// return (false, "", nil) when it could not read the state.
	HaltsMerge(tenantID string) (halted bool, reason string, err error)
}

// ErrHalted is a run refused by the operator brake. It is a REPORTED condition the surface renders
// with the operator's own reason, not a fault: an operator stopped this deliberately, and telling the
// customer "something went wrong" would send them to support over a decision somebody made.
type ErrHalted struct {
	Reason string
	// Indeterminate says the switch could not be READ, rather than that it was armed. The two need
	// different sentences: one is "we stopped this", the other is "we cannot tell whether we stopped
	// this, so we stopped it".
	Indeterminate bool
}

func (e *ErrHalted) Error() string {
	if e.Indeterminate {
		return "improvementrun: the platform's kill-switch state could not be read, so the run is " +
			"withheld — cannot tell whether we are stopped means stopped: " + e.Reason
	}
	if e.Reason == "" {
		return "improvementrun: an operator has halted runs for this organization"
	}
	return "improvementrun: an operator has halted runs for this organization: " + e.Reason
}

// Sentence is what the surface says. The operator's own words travel on an armed halt, because they
// are the only part a reader can act on; an indeterminate one says plainly that it is ours.
func (e *ErrHalted) Sentence() string {
	if e.Indeterminate {
		return "This run was withheld because the platform could not read its own kill-switch state. " +
			"Nothing has been spent. This is ours, not yours, and it clears on its own."
	}
	if e.Reason == "" {
		return "Runs are paused for this organization by an operator. Nothing has been spent."
	}
	return "Runs are paused for this organization by an operator: " + e.Reason + ". Nothing has been spent."
}

// checkBrake consults the operator's switch. Nil means no operator console is wired, which is correct
// for a self-hosted deployment — and is NOT the same as "not halted": the difference is that a nil here
// is a decision visible in the wiring rather than a read that silently returned false.
func (s *Service) checkBrake(_ context.Context, tenantID string) error {
	if s.Brake == nil {
		return nil
	}
	halted, reason, err := s.Brake.HaltsMerge(tenantID)
	if err != nil {
		return &ErrHalted{Reason: err.Error(), Indeterminate: true}
	}
	if halted {
		return &ErrHalted{Reason: reason}
	}
	return nil
}

// SpendExport is task 9.2: what each tenant's runs cost, in a shape something else can chart or bill
// from.
//
// 🚫 It is an EXPORT, not a charge, and the distinction is P7's: the platform never resells provider
// tokens (`billing.ErrResoldTokens` enforces it on the invoice). This exists so an operator can see
// which tenant's runs are expensive — a capacity question — and so a per-tenant ceiling has something
// to be checked against.
//
// 🔴 `WithdrawnUSD` is broken out because decisions.md D-35.4 makes a withdrawn change's compute
// CHARGEABLE against the run's budget and NOT billable. A single total cannot express two ledgers, and
// the number an operator needs when a customer asks "what did I pay for that" is the difference.
type SpendExport struct {
	TenantID string  `json:"tenant_id"`
	SpendUSD float64 `json:"spend_usd"`
	// WithdrawnUSD is the part of it spent on changes that were withdrawn before delivery.
	WithdrawnUSD float64 `json:"withdrawn_usd"`
	// Runs is the count, so a per-run average is available without dividing by a number from a second
	// document.
	Runs int64 `json:"runs"`
}

// ExportSpend renders per-tenant provider spend from the in-process metrics, sorted by tenant so the
// output is stable in a diff.
func (m *Metrics) ExportSpend() []SpendExport {
	h := m.Health()
	out := make([]SpendExport, 0, len(h.PerTenantSpendUSD))
	for tenant, spend := range h.PerTenantSpendUSD {
		out = append(out, SpendExport{TenantID: tenant, SpendUSD: spend})
	}
	sortSpend(out)
	return out
}

func sortSpend(rows []SpendExport) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].TenantID < rows[j-1].TenantID; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
}

// CapFor is task 9.2's other half: the per-run provider ceiling, which is the plan's own spend budget.
//
// 🔴 It is a FUNCTION over the plan rather than a deployment-wide constant, and that is the whole
// point: the cap a run is held to is the one the person was SHOWN before it started. A separate
// operator-set ceiling would be a second number, and a run bounded by a number nobody displayed is a
// run whose disclosure meant nothing.
func CapFor(p Plan) float64 { return p.SpendBudgetUSD }
