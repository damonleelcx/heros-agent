package diagnosis

import "sort"

// calibration.go is the analyst-trust half of §6. LLM-as-analyst is noisy and biased — this is stated
// plainly in the source plan — so its automated diagnoses are only usable if a human can see how much
// to trust them. Calibration against a human-labeled subset produces that number: Cohen's κ and raw
// percent agreement, with n_human, exactly as P4's judge calibration does. An analyst with no human
// labels, or one below the configured floor, is FLAGGED and its diagnoses are surfaced only as
// low-trust reports.

// DefaultAnalystFloor is the default κ below which the analyst is considered uncalibrated-for-trust.
// 0.6 is the conventional "substantial agreement" threshold and is written here once so "below floor"
// is a value, not a habit. It is independent of the P4 judge floor (design Q4) — a diagnosis analyst
// and a quality judge answer different questions.
const DefaultAnalystFloor = 0.6

// AnalystCalibration is the analyst's measured trust, matching the design's analyst_cal row. Every
// field an agreement number needs to be weighed rides along: n_human (an agreement over n=3 is not
// evidence), the floor it is judged against, and whether it was calibrated at all.
type AnalystCalibration struct {
	AnalystMetric    string  `json:"analyst_metric"`
	Agreement        float64 `json:"agreement"`         // Cohen's κ over the shared labels
	PercentAgreement float64 `json:"percent_agreement"` // raw fraction of labels that matched
	NHuman           int     `json:"n_human"`
	Calibrated       bool    `json:"calibrated"`
	Floor            float64 `json:"floor"`
}

// BelowFloor reports whether the analyst is uncalibrated or below its agreement floor — the predicate
// that flags an analyst as low-trust. It lives in one place so every consumer (the diagnosis
// orchestrator, the scorecard) asks the same question.
func (c AnalystCalibration) BelowFloor() bool {
	return !c.Calibrated || c.Agreement < c.Floor
}

// Calibrate computes the analyst's agreement against a human-labeled subset (task 6.3). human and
// analyst map case_id → taxonomy code over the SAME calibration cases; only cases present in both are
// scored. It returns Cohen's κ (chance-corrected), raw percent agreement, and n_human — never an
// agreement without its n.
func Calibrate(analystMetric string, human, analyst map[string]TaxonomyCode, floor float64) AnalystCalibration {
	if floor <= 0 {
		floor = DefaultAnalystFloor
	}
	// Score only the cases both labeled, in sorted order so the computation is deterministic.
	var caseIDs []string
	for id := range human {
		if _, ok := analyst[id]; ok {
			caseIDs = append(caseIDs, id)
		}
	}
	sort.Strings(caseIDs)

	cal := AnalystCalibration{AnalystMetric: analystMetric, NHuman: len(caseIDs), Floor: floor}
	if len(caseIDs) == 0 {
		// No human labels → uncalibrated. Not an error: a fresh analyst is legitimately uncalibrated,
		// and the flag is the honest state, not a failure.
		return cal
	}
	cal.Calibrated = true

	var agree int
	humanCount := map[TaxonomyCode]int{}
	analystCount := map[TaxonomyCode]int{}
	for _, id := range caseIDs {
		h, a := human[id], analyst[id]
		if h == a {
			agree++
		}
		humanCount[h]++
		analystCount[a]++
	}
	n := float64(len(caseIDs))
	cal.PercentAgreement = float64(agree) / n

	// Cohen's κ = (po − pe) / (1 − pe): observed agreement corrected for chance agreement expected
	// from the two raters' marginal label distributions.
	po := cal.PercentAgreement
	var pe float64
	for code, hc := range humanCount {
		pe += (float64(hc) / n) * (float64(analystCount[code]) / n)
	}
	if pe >= 1 {
		// Perfect marginal concentration (both rated every case the same single code). κ is undefined;
		// report κ=1 iff they also agreed on every case, else 0.
		if po >= 1 {
			cal.Agreement = 1
		}
		return cal
	}
	cal.Agreement = (po - pe) / (1 - pe)
	return cal
}
