package proposal

import (
	"fmt"
	"sort"
	"strings"
)

// The cost/quality admissibility gate for a harness swap (P18 tasks 6.3/6.4, decisions.md D-6)
// ────────────────────────────────────────────────────────────────────────────────────────────
//
// Every other operator's admissibility question is "did it score better". This one's cannot be, and the
// reason is arithmetic rather than philosophy: a heavier scaffold almost always raises `task_success`
// SOMEWHERE, because taking a second look at a wrong answer sometimes fixes it — while multiplying
// `eval_cost_usd` and `eval_latency_ms` by its turn ceiling on EVERY case, including the ones that were
// already right.
//
// 🔴 So a gate that admits any scaffold raising `task_success` would ship expensive loops that won on
// quality alone, and the customer would discover the trade in a bill they never agreed to. The trade-off
// has to be a gate that can REJECT — and one that rejects on data the proposal was not tuned against,
// because a win measured on its own tuning set is overfitting with a confidence interval.
//
// # What this gate is NOT
//
// 🚫 It is not a measurement. All three inputs come from the existing eval harness unchanged
// (`internal/evalharness/metricnames.go`: `task_success`, `eval_cost_usd`, `eval_latency_ms`); this file
// is arithmetic over them. P18 introduces no metric and no scoring change, and this gate is where that
// promise is most tempting to break.
//
// 🚫 It is not ranking. Ranking orders candidates that are already admissible; this decides whether a
// candidate is admissible at all, and an inadmissible one is not "ranked last" — it does not ship.

// HarnessQualityPerCostDoubling is the exchange rate: the `task_success` gain a DOUBLING of the node's
// per-run cost (or latency) must buy for a heavier scaffold to be admitted.
//
// 🔴 It is a named constant with a stated meaning rather than a tuned threshold, because it encodes a
// POLICY and policies should be readable. At 0.05, doubling what a node costs must buy five points of
// task_success; tripling it must buy ten. A team that disagrees with that trade should change this line
// and say why in a decision record — which is exactly the conversation a magic number buried in an
// expression prevents.
//
// The value is deliberately not zero: zero would admit any scaffold with a non-negative gain, which is
// the "it scored higher" rule D-6 rejects by name.
const HarnessQualityPerCostDoubling = 0.05

// HarnessMeasurement is one side of the comparison — what the harness measured for a configuration on
// the held-out set. Every field is an existing metric, read as-is.
type HarnessMeasurement struct {
	// TaskSuccess is the `task_success` rate in [0,1].
	TaskSuccess float64
	// CostUSD is `eval_cost_usd` per case.
	CostUSD float64
	// LatencyMS is `eval_latency_ms` per case.
	LatencyMS float64
	// MaxTurns is the configuration's turn ceiling, which is what makes one scaffold HEAVIER than
	// another. Carried alongside the metrics because "heavier" is a property of the configuration, not of
	// the measurement, and deriving it from cost would confuse a heavier scaffold with a pricier model.
	MaxTurns int
	// CaseIDs are the cases this measurement was taken over. Carried so the disjointness check below can
	// be performed on evidence rather than on an assurance.
	CaseIDs []string
}

// HarnessAdmissibility is the gate's input: the baseline, the candidate, and the cases the proposal was
// tuned on.
type HarnessAdmissibility struct {
	Baseline  HarnessMeasurement
	Candidate HarnessMeasurement
	// TuningCaseIDs are the cases used to SHAPE the proposal — the diagnosis's evidence cases. The
	// held-out measurement must be disjoint from these.
	TuningCaseIDs []string
}

// HarnessVerdict is the gate's answer. Admitted plus a reason, always — a rejection a human cannot read
// is a rejection they will route around.
type HarnessVerdict struct {
	Admitted bool
	// Reason states WHY, in the terms the decision was made in. Never empty, including on admission:
	// "why did this ship" is asked as often as "why did this not".
	Reason string
	// Heavier records whether the candidate raised the turn ceiling. A lighter or equal candidate is
	// judged on quality alone — the cost clause exists to stop a scaffold from BUYING quality with money,
	// and a cheaper scaffold is not doing that.
	Heavier bool
	// DeltaTaskSuccess, DeltaCostRatio and DeltaLatencyRatio are the computed inputs, exposed so a
	// surface can show the arithmetic rather than assert the conclusion.
	DeltaTaskSuccess  float64
	DeltaCostRatio    float64
	DeltaLatencyRatio float64
	// RequiredTaskSuccess is the gain the candidate had to clear. Zero for a non-heavier candidate.
	RequiredTaskSuccess float64
}

// AdmitHarnessSwap decides whether a harness candidate may ship.
//
// The order of the checks is the order of the failures a reader most needs distinguished:
//
//  1. 🔴 the held-out set is EMPTY or OVERLAPS the tuning set — inadmissible, because there is no
//     evidence, not because the evidence was bad. Fail-closed: a gate that treated "no held-out data" as
//     "nothing to object to" would admit everything the moment a caller forgot to supply it.
//  2. quality did not improve — inadmissible whatever it cost.
//  3. the candidate is not heavier — admitted on the quality gain alone.
//  4. the candidate is heavier — admitted only if the gain clears the cost it added.
func AdmitHarnessSwap(in HarnessAdmissibility) HarnessVerdict {
	v := HarnessVerdict{
		Heavier:          in.Candidate.MaxTurns > in.Baseline.MaxTurns,
		DeltaTaskSuccess: in.Candidate.TaskSuccess - in.Baseline.TaskSuccess,
	}
	v.DeltaCostRatio = growthRatio(in.Baseline.CostUSD, in.Candidate.CostUSD)
	v.DeltaLatencyRatio = growthRatio(in.Baseline.LatencyMS, in.Candidate.LatencyMS)

	// 1. The evidence itself.
	if leak := overlap(in.Candidate.CaseIDs, in.TuningCaseIDs); len(leak) > 0 {
		v.Reason = fmt.Sprintf("inadmissible: the measurement was taken over case(s) %s, which the proposal "+
			"was tuned on. A gain measured on the cases that produced the proposal is overfitting with a "+
			"confidence interval, so the admissibility set must be disjoint from the tuning set",
			strings.Join(leak, ", "))
		return v
	}
	if len(in.Candidate.CaseIDs) == 0 {
		v.Reason = "inadmissible: no held-out cases were measured. This is a refusal for absence of " +
			"evidence, not a judgement on it — treating an empty held-out set as 'nothing to object to' " +
			"would admit every scaffold the moment a caller forgot to supply one"
		return v
	}

	// 2. Quality.
	if v.DeltaTaskSuccess <= 0 {
		v.Reason = fmt.Sprintf("inadmissible: task_success did not improve on held-out cases (%+.4f). A "+
			"scaffold that costs more and answers no better is a cost increase with a rationale attached",
			v.DeltaTaskSuccess)
		return v
	}

	// 3. A lighter or equal scaffold. Judged on quality alone.
	if !v.Heavier {
		v.Admitted = true
		v.Reason = fmt.Sprintf("admitted: task_success improved by %+.4f on held-out cases and the scaffold "+
			"is not heavier (%d turns vs %d), so it is not buying quality with cost",
			v.DeltaTaskSuccess, in.Candidate.MaxTurns, in.Baseline.MaxTurns)
		return v
	}

	// 4. A heavier scaffold. It must earn what it added.
	burden := v.DeltaCostRatio
	worst := "cost"
	if v.DeltaLatencyRatio > burden {
		burden, worst = v.DeltaLatencyRatio, "latency"
	}
	v.RequiredTaskSuccess = burden * HarnessQualityPerCostDoubling
	if v.DeltaTaskSuccess < v.RequiredTaskSuccess {
		v.Reason = fmt.Sprintf("inadmissible: the scaffold went from %d to %d turns and raised %s by %.0f%% "+
			"on held-out cases, which requires a task_success gain of at least %+.4f; it delivered %+.4f. "+
			"Shipping it would buy %.4f of quality with a %.0f%% bill the customer never agreed to",
			in.Baseline.MaxTurns, in.Candidate.MaxTurns, worst, burden*100,
			v.RequiredTaskSuccess, v.DeltaTaskSuccess, v.DeltaTaskSuccess, burden*100)
		return v
	}
	v.Admitted = true
	v.Reason = fmt.Sprintf("admitted: the scaffold went from %d to %d turns, raising %s by %.0f%%, and "+
		"bought %+.4f of task_success on held-out cases against the %+.4f that increase required",
		in.Baseline.MaxTurns, in.Candidate.MaxTurns, worst, burden*100,
		v.DeltaTaskSuccess, v.RequiredTaskSuccess)
	return v
}

// growthRatio is the fractional increase from base to candidate, floored at zero.
//
// 🔴 A base of zero is treated as "no increase measurable" rather than as infinity. A zero baseline cost
// means the harness measured nothing, and dividing by it would turn a missing measurement into an
// automatic rejection with a nonsensical reason — which is a worse failure than the one it would be
// standing in for. The empty-held-out check above is where a missing measurement is actually caught.
func growthRatio(base, candidate float64) float64 {
	if base <= 0 {
		return 0
	}
	r := (candidate - base) / base
	if r < 0 {
		return 0
	}
	return r
}

// overlap returns the ids present in both sets, sorted. Sorted because the reason string names them, and
// a rejection whose wording depends on map iteration order is a rejection nobody can diff.
func overlap(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	inB := make(map[string]bool, len(b))
	for _, id := range b {
		inB[id] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range a {
		if inB[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
