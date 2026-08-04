package verification

import "github.com/heros-foreal/agentd/internal/evalstats"

// GateResult is the terminal verdict of the verification gate. It is the single predicate the
// recommendation surface reads: only `pass` may be shown (§4.5).
type GateResult string

const (
	GatePass          GateResult = "pass"
	GateFailSig       GateResult = "fail_significance"
	GateFailRegress   GateResult = "fail_regression"
	GateFailConstrain GateResult = "fail_constraint"
	// GateUnrun marks a proposal that never ran the gate — also withheld.
	GateUnrun GateResult = "unrun"
)

// Verdict is the structured, source-of-truth result of verifying one proposal (design Decision 8's
// schema). Every field is measured; any human-readable synthesis narrates this record and can never
// contradict it (§6.7).
type Verdict struct {
	ProposalID string `json:"proposal_id"`
	ConfigHash string `json:"config_hash"`
	DiffHash   string `json:"diff_hash"`

	Metric string `json:"metric"`
	// Delta is the held-out (where available) quality delta with its confidence interval.
	Delta       evalstats.Interval `json:"delta"`
	Significant bool               `json:"significant"`
	// HeldOut is false when no held-out split could be formed; the delta then generalizes unproven.
	HeldOut bool `json:"held_out"`

	CostDelta    float64 `json:"cost_delta"`
	LatencyDelta float64 `json:"latency_delta"`

	// CasesFixed / CasesBroken are the case IDS, and they are present only when the verdict was produced
	// in the same environment as the cases — i.e. by Verify below. A verdict REPORTED by a customer's CI
	// arrives with these empty on purpose: a case id is customer-authored text, so the ids are refused at
	// the P11 boundary and the counts cross instead (internal/runlink/verdict.go says why).
	CasesFixed  []string `json:"cases_fixed"`
	CasesBroken []string `json:"cases_broken"`
	// CasesFixedCount / CasesBrokenCount are HOW MANY, and they are the authoritative fields.
	//
	// 🔴 Read these, never len(CasesFixed). The lists are optional detail and are empty on every
	// reported verdict; `len()` of an empty list is 0, which renders as "fixed nothing" — a claim, where
	// the truth was "we were told four, and not which four". Verify sets both, so the count is never
	// derived from the list and the two can never disagree.
	CasesFixedCount  int `json:"cases_fixed_count"`
	CasesBrokenCount int `json:"cases_broken_count"`

	RegressionPass bool       `json:"regression_pass"`
	GateResult     GateResult `json:"gate_result"`
	// Reason is a verbatim-renderable explanation of the gate_result (never the source of truth, but the
	// narration the UI shows). A withheld proposal with no reason reads as "the tool did not work".
	Reason string `json:"reason"`
}

// SetCases records the case ids and their counts TOGETHER.
//
// It exists because the two can be set apart, and a Verdict with ids and a zero count reports "fixed
// nothing" to every consumer — they all read the count now, since a reported verdict has no ids. Verify
// sets cases only through this, and so should any hand-built verdict (proof binaries, fixtures):
// assigning CasesFixed on its own compiles, looks complete, and is wrong in the direction that
// understates the change.
func (v *Verdict) SetCases(fixed, broken []string) {
	v.CasesFixed, v.CasesBroken = fixed, broken
	v.CasesFixedCount, v.CasesBrokenCount = len(fixed), len(broken)
}

// Passed reports whether this verdict may surface as a recommendation. This is the ONE predicate the
// recommendation surface uses — "surface" is a property of the verdict, not a UI choice (§4.5).
func (v Verdict) Passed() bool { return v.GateResult == GatePass }
