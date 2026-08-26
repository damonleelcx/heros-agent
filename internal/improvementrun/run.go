package improvementrun

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/assessment"
)

// run.go is what one improvement run IS, as a value: the plan it ran under, how it ended, what it
// proposed, and — at every stage — the breakdown per axis.
//
// # 🔴 Why there is no aggregate count on this type
//
// PRD §9.5: *"an aggregate hides the single-sample defect"*, and the arithmetic is `proposal.AxisPassRate`'s:
//
//	model     120 generated, 70% verified  ─┐
//	prompt     80 generated, 60% verified   ├─ aggregate 61%. Healthy.
//	graph       6 generated,  5% verified  ─┘
//
// The graph operator is not working and nothing in that 61% says so — and it never will, because the
// operator with the smallest sample is the newest one, so the number that would reveal a broken new
// operator is the number an aggregate is least sensitive to.
//
// So `Run` carries `PerAxis` and NOT `Generated`, `Verified`, `Approved` or `Delivered` totals. A
// caller that wants a total writes the loop, and writing it is a decision somebody makes rather than a
// default they inherit — the same absence `proposal.AxisPassRate` maintains, extended to four stages.

// Stage is one point in a proposal's life. A closed set, in order, because the per-axis breakdown has
// to exist at EVERY stage (task 7.15) and "every" needs a list.
type Stage string

const (
	// StageGenerated — the enumerator admitted the candidate.
	StageGenerated Stage = "generated"
	// StageVerified — it passed the P5.5 held-out gate and was surfaced.
	StageVerified Stage = "verified"
	// StageApproved — a person approved it through `internal/approval`.
	StageApproved Stage = "approved"
	// StageDelivered — a pull request exists for it.
	StageDelivered Stage = "delivered"
	// StageWithdrawn — it was approved and applied, and re-measurement disagreed (FR16).
	//
	// 🔴 A stage rather than a failure. A withdrawal is the product working, and a breakdown that
	// omitted it would make an axis with a noisy eval set look like an axis that produces nothing.
	StageWithdrawn Stage = "withdrawn"
)

var stages = []Stage{StageGenerated, StageVerified, StageApproved, StageDelivered, StageWithdrawn}

// Stages returns the closed set in order. A copy.
func Stages() []Stage { return append([]Stage(nil), stages...) }

// Valid reports membership.
func (s Stage) Valid() bool {
	for _, v := range stages {
		if v == s {
			return true
		}
	}
	return false
}

// String makes Stage printable.
func (s Stage) String() string { return string(s) }

// AxisStage is one axis's count at every stage.
//
// 🔴 A LIST of these rather than a map, for `assessment.Health.PerAxis`' reason: a list's ordering is
// stable in a diff and a test can assert "these entries, in this order" without sorting keys.
type AxisStage struct {
	Axis assessment.Axis `json:"axis"`
	// InScope says the plan admitted this axis. 🔴 An axis at zero because the SCOPE excluded it and an
	// axis at zero because its operators produced nothing are opposite findings, and a bare count of
	// zero cannot tell them apart.
	InScope   bool `json:"in_scope"`
	Generated int  `json:"generated"`
	Verified  int  `json:"verified"`
	Approved  int  `json:"approved"`
	Delivered int  `json:"delivered"`
	Withdrawn int  `json:"withdrawn"`
}

// Count returns this axis's number at one stage.
func (a AxisStage) Count(s Stage) int {
	switch s {
	case StageGenerated:
		return a.Generated
	case StageVerified:
		return a.Verified
	case StageApproved:
		return a.Approved
	case StageDelivered:
		return a.Delivered
	case StageWithdrawn:
		return a.Withdrawn
	default:
		return 0
	}
}

// Run is one improvement run, terminal or in progress.
type Run struct {
	RunID    string `json:"run_id"`
	TenantID string `json:"tenant_id"`
	Plan     Plan   `json:"plan"`

	// Outcome says which bound stopped the run, or which fault ended it. Zero while running.
	Outcome Outcome `json:"outcome"`

	// Proposals are the VERIFIED ones, in the order they were surfaced. Only verified candidates ever
	// reach this field — `NewVerifiedProposal` is the only way to make one.
	Proposals []VerifiedProposal `json:"proposals"`

	// Decisions is the per-proposal approval state, keyed by proposal id. 🔴 A DECLINED proposal keeps
	// its entry and stays in `Proposals`: a proposal that disappeared when it was declined looks like
	// one that was never made (FR12, §9.4).
	Decisions map[string]Decision `json:"decisions,omitempty"`

	// Withdrawals are changes that were approved, applied, and then withdrawn because re-measurement
	// disagreed (FR16). 🔴 BOTH measurements travel on each one.
	Withdrawals []Withdrawal `json:"withdrawals,omitempty"`

	// Deliveries are the pull requests opened, one per delivered proposal.
	Deliveries []DeliveryResult `json:"deliveries,omitempty"`

	// Empty is the named "nothing to propose" answer, present only when the run produced no candidates.
	// 🔴 A pointer so its ABSENCE is distinguishable from a zero-valued state — an empty-state struct
	// with an empty name is exactly the discarded reason P30 found.
	Empty *EmptyState `json:"empty,omitempty"`

	// PerAxis is the breakdown at every stage. See the file header for why there is no total.
	PerAxis []AxisStage `json:"per_axis"`

	StartedAtMS  int64 `json:"started_at_ms"`
	FinishedAtMS int64 `json:"finished_at_ms"`

	// SpendUSD is what the run cost. WithdrawnSpendUSD is the part of it spent on changes that were
	// withdrawn — reported separately, per decisions.md D-35.4: a run that spent 40% of its budget on
	// candidates it withdrew is telling us something about the eval set, and an aggregate hides it.
	SpendUSD          float64 `json:"spend_usd"`
	WithdrawnSpendUSD float64 `json:"withdrawn_spend_usd"`
}

// axisIndex builds the per-axis rows for a plan, with every one of the nine present and `InScope` set
// from the plan.
//
// 🔴 ALL NINE, always, not only the ones in scope. A breakdown that listed only the scoped axes would
// make "we did not look at graph" and "graph produced nothing" render identically — which is the same
// omission `assessment` refuses when it insists on reporting all nine findings.
func axisIndex(p Plan) []AxisStage {
	inScope := map[assessment.Axis]bool{}
	for _, a := range p.Axes {
		inScope[a] = true
	}
	out := make([]AxisStage, 0, len(assessment.Axes()))
	for _, a := range assessment.Axes() {
		out = append(out, AxisStage{Axis: a, InScope: inScope[a]})
	}
	return out
}

// bump increments one axis's count at one stage, creating no row: every axis already has one.
func bump(rows []AxisStage, axis assessment.Axis, s Stage, n int) {
	for i := range rows {
		if rows[i].Axis != axis {
			continue
		}
		switch s {
		case StageGenerated:
			rows[i].Generated += n
		case StageVerified:
			rows[i].Verified += n
		case StageApproved:
			rows[i].Approved += n
		case StageDelivered:
			rows[i].Delivered += n
		case StageWithdrawn:
			rows[i].Withdrawn += n
		}
		return
	}
}

// AxisRow returns one axis's row, and whether it exists.
func (r Run) AxisRow(a assessment.Axis) (AxisStage, bool) {
	for _, row := range r.PerAxis {
		if row.Axis == a {
			return row, true
		}
	}
	return AxisStage{}, false
}

// proposal finds one surfaced proposal by id.
func (r Run) proposal(id string) (VerifiedProposal, bool) {
	for _, p := range r.Proposals {
		if p.ProposalID == id {
			return p, true
		}
	}
	return VerifiedProposal{}, false
}

// setDecision records a decision on the run, creating the map on first use.
func (r *Run) setDecision(d Decision) {
	if r.Decisions == nil {
		r.Decisions = map[string]Decision{}
	}
	r.Decisions[d.ProposalID] = d
}

// DecisionFor returns one proposal's decision, defaulting to `pending` rather than to a zero value.
//
// 🔴 The default matters: a zero `Decision` has an empty state, and a surface switching on it would fall
// into a default arm and render nothing — a blank card, which a reader cannot tell from a proposal that
// was never made.
func (r Run) DecisionFor(proposalID string) Decision {
	if d, ok := r.Decisions[proposalID]; ok {
		return d
	}
	for _, p := range r.Proposals {
		if p.ProposalID == proposalID {
			return Decision{
				ProposalID: proposalID, Axis: p.Axis, State: DecisionPending,
				Binding: Binding{ConfigHash: p.ConfigHash, SourceRevision: p.SourceRevision},
			}
		}
	}
	return Decision{ProposalID: proposalID, State: DecisionPending}
}

// WithdrawalFor returns a proposal's withdrawal, if it has one.
func (r Run) WithdrawalFor(proposalID string) (Withdrawal, bool) {
	for _, w := range r.Withdrawals {
		if w.ProposalID == proposalID {
			return w, true
		}
	}
	return Withdrawal{}, false
}

// ProposalsByAxis groups the surfaced proposals, ordered as `assessment.Axes()` orders them.
//
// Used by the console so a run over nine axes reads as nine groups rather than as one list a reader has
// to sort in their head.
func (r Run) ProposalsByAxis() map[assessment.Axis][]VerifiedProposal {
	out := map[assessment.Axis][]VerifiedProposal{}
	for _, p := range r.Proposals {
		out[p.Axis] = append(out[p.Axis], p)
	}
	for a := range out {
		sort.SliceStable(out[a], func(i, j int) bool {
			return out[a][i].Delta.Mean > out[a][j].Delta.Mean
		})
	}
	return out
}
