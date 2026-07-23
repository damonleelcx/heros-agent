package verification

import (
	"fmt"
	"sort"
)

// AutomationLevel is the trust contract the workflow runs under (design Decision 9). Advisory is the
// default (report / open a draft PR; the human applies and merges); Assisted is an explicit
// per-workflow opt-in (one-click open the verified PR; the human still reviews and merges). Neither
// level ever merges to the default branch or mutates the working tree — the system stops at opening a
// reviewable PR.
type AutomationLevel string

const (
	Advisory AutomationLevel = "advisory"
	Assisted AutomationLevel = "assisted"
)

// ProposalState is the first-class UI state for one proposal (§6.5). It is derived ONLY from the
// persisted verdict, so it can never drift from the terminal result.
type ProposalState string

const (
	StateLoading    ProposalState = "loading"
	StateVerifying  ProposalState = "verifying"
	StateVerified   ProposalState = "verified"    // gate_result = pass
	StateGateFailed ProposalState = "gate_failed" // ran the gate, did not pass
	StateError      ProposalState = "error"
)

// State maps a verdict to its UI state. A never-run verdict (GateUnrun) is "verifying"; a pass is
// "verified"; anything else that ran is "gate_failed".
func State(v Verdict) ProposalState {
	switch v.GateResult {
	case GatePass:
		return StateVerified
	case GateUnrun, "":
		return StateVerifying
	default:
		return StateGateFailed
	}
}

// HeldOutLabel is the verbatim label the UI shows for a verdict's generalization status (§6.5).
func HeldOutLabel(v Verdict) string {
	if v.HeldOut {
		return "held-out"
	}
	return "not held-out"
}

// Narrate produces the human-readable synthesis for a verdict. It is strictly NARRATION OVER THE
// STRUCTURED VERDICT (§6.7): every clause is read off the verdict fields, so the summary can never
// contradict or replace the source of truth. It never invents a number the verdict does not carry.
func Narrate(v Verdict) string {
	switch v.GateResult {
	case GatePass:
		s := fmt.Sprintf("Verified %s improvement of %+.3f (95%% CI [%+.3f, %+.3f])", v.Metric, v.Delta.Mean, v.Delta.Low, v.Delta.High)
		if !v.HeldOut {
			s += ", not held-out"
		}
		s += fmt.Sprintf("; %d case(s) fixed", len(v.CasesFixed))
		if len(v.CasesBroken) > 0 {
			s += fmt.Sprintf(", %d broken", len(v.CasesBroken))
		}
		s += fmt.Sprintf(". Cost impact %+.4f $/run, latency %+.0f ms/run.", v.CostDelta, v.LatencyDelta)
		return s
	case GateFailSig:
		return "Withheld: the held-out gain is not statistically significant (a tie). " + v.Reason
	case GateFailRegress:
		s := "Withheld: passed its target but failed the regression check. " + v.Reason
		if len(v.CasesBroken) > 0 {
			s += fmt.Sprintf(" (%d case(s) broken)", len(v.CasesBroken))
		}
		return s
	case GateFailConstrain:
		return "Withheld: violates a hard constraint. " + v.Reason
	default:
		return "Not yet verified."
	}
}

// PROpenAvailable reports whether a one-click open-PR control may be offered for a proposal, and — when
// not — the reason to show on the disabled control (§6.4). The control is offered ONLY in Assisted mode
// AND ONLY when gate_result = pass: the convenience of Assisted must never become a channel for
// shipping an unverified change. Advisory never offers one-click PR-open (its apply is opening a draft
// PR the human drives).
func PROpenAvailable(level AutomationLevel, v Verdict) (bool, string) {
	if !v.Passed() {
		return false, "not verified — no gate-passing verdict"
	}
	if level != Assisted {
		return false, "Advisory mode: apply by opening a draft PR for review"
	}
	return true, ""
}

// ── Trend view (§6.3, §7.6) ──────────────────────────────────────────────────────────────────────

// TrendPoint is one variant iteration's measured position: its overall success and the size (failing
// case count) of each failure cluster. It is read from structured eval history, never a narrative.
type TrendPoint struct {
	VariantID      string         `json:"variant_id"`
	Iteration      int            `json:"iteration"`
	OverallSuccess float64        `json:"overall_success"`
	ClusterSizes   map[string]int `json:"cluster_sizes"`
}

// TrendView is the computed answer to "did the workflow actually improve, or did the failure mass just
// move between clusters?" (§6.3). GloballyImproved and ProblemsMoved are derived from the structured
// points; Narrative narrates them.
type TrendView struct {
	Points           []TrendPoint `json:"points"`
	GloballyImproved bool         `json:"globally_improved"`
	ProblemsMoved    bool         `json:"problems_moved"`
	Narrative        string       `json:"narrative"`
}

// BuildTrend computes the trend across iterations (assumed in iteration order). The default success
// threshold below which "no global improvement" is declared is 0.05; a cluster shift above 1 case is
// "movement".
func BuildTrend(points []TrendPoint) TrendView {
	tv := TrendView{Points: points}
	if len(points) < 2 {
		tv.Narrative = "Not enough iterations to establish a trend."
		return tv
	}
	first, last := points[0], points[len(points)-1]
	deltaOverall := last.OverallSuccess - first.OverallSuccess

	// Per-cluster movement between first and last.
	clusters := map[string]bool{}
	for c := range first.ClusterSizes {
		clusters[c] = true
	}
	for c := range last.ClusterSizes {
		clusters[c] = true
	}
	var maxDrop, maxRise int
	for c := range clusters {
		d := last.ClusterSizes[c] - first.ClusterSizes[c]
		if d < 0 && -d > maxDrop {
			maxDrop = -d
		}
		if d > 0 && d > maxRise {
			maxRise = d
		}
	}

	const successEps = 0.05
	tv.GloballyImproved = deltaOverall > successEps
	// Problems moved: one cluster shrank while another grew by a comparable amount, with little net
	// global gain — the failure mass relocated rather than shrank.
	tv.ProblemsMoved = maxDrop > 1 && maxRise > 1 && !tv.GloballyImproved

	switch {
	case tv.GloballyImproved:
		tv.Narrative = fmt.Sprintf("The workflow improved: overall success rose %+.3f across %d iterations.", deltaOverall, len(points))
	case tv.ProblemsMoved:
		tv.Narrative = fmt.Sprintf("No global improvement: the failure mass moved between clusters (one shrank by %d, another grew by %d) while overall success changed only %+.3f.", maxDrop, maxRise, deltaOverall)
	default:
		tv.Narrative = fmt.Sprintf("Overall success changed %+.3f across %d iterations.", deltaOverall, len(points))
	}
	return tv
}

// SortByScore is a stable helper for ordering a slice of (proposalID, score) pairs, used by the UI
// assembly to present verdicts in verified-delta order. Descending score, id tiebreak.
func SortByScore(ids []string, score map[string]float64) {
	sort.SliceStable(ids, func(i, j int) bool {
		if score[ids[i]] != score[ids[j]] {
			return score[ids[i]] > score[ids[j]]
		}
		return ids[i] < ids[j]
	})
}
