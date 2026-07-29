package verification

import "fmt"

// What the platform may SAY about a USER-AUTHORED change (P13 13c tasks 10.1, 10.3).
//
// The split of responsibility is deliberate and is why this lives here rather than beside the guardrail:
//
//	internal/proposal   answers ADMISSIBILITY — may this candidate be verified at all?
//	internal/verification answers REPORTABILITY — what may we now claim about it?
//
// Authoring changes who PICKS the candidate. It changes nothing about who JUDGES it, and nothing about
// what the judgment is allowed to be called. There is deliberately no authored-specific Verdict type:
// a second verdict shape would be a second definition of "better", and the two would drift.

// QualityClaim is what may be said about an authored change's quality. The vocabulary is closed so a
// surface cannot invent a friendlier word for a regression.
type QualityClaim string

const (
	// ClaimUnmeasured: the harness never ran. This is the state EVERY authored change starts in, and it
	// is not a hedge — it is the truth, and the ledger filters on it.
	ClaimUnmeasured QualityClaim = "unmeasured"
	// ClaimTie: quality is statistically indistinguishable from the incumbent.
	ClaimTie QualityClaim = "tie"
	// ClaimWin: a measured quality improvement.
	ClaimWin QualityClaim = "win"
	// ClaimRegression: a measured quality loss. A cheaper model that lands here is a REGRESSION and is
	// never described as equal-quality, no matter how much cost it saved.
	ClaimRegression QualityClaim = "regression"
)

// GuardrailOutcome is the three-valued input the held-out downgrade guardrail produces, as this package
// needs to consume it.
//
// It is intentionally a small local tri-state rather than an import: `internal/proposal` owns the
// guardrail's computation and its own richer result type, and importing it here would invert the
// dependency the whole engine is built on (proposals feed verification, never the reverse). The caller
// translates once, at the seam.
type GuardrailOutcome int

const (
	// GuardrailNotRun: no guardrail applies, or it has not been evaluated.
	GuardrailNotRun GuardrailOutcome = iota
	// GuardrailCleared: held-out intervals overlap — the platform cannot tell the models apart.
	GuardrailCleared
	// GuardrailFailed: held-out intervals do NOT overlap, or there was not enough held-out data to tell.
	// The two collapse HERE, and only here, because both mean the same thing for reporting: we may not
	// call this equal-quality. They stay distinct upstream, where the remedy differs (pick another
	// model vs. collect more cases).
	GuardrailFailed
)

// AuthoredReport is what a surface may render about an authored change.
type AuthoredReport struct {
	// Quality is the claim. ClaimUnmeasured until the harness has run.
	Quality QualityClaim `json:"quality"`
	// CostWin is true only when cost strictly dropped AND the run happened. An unmeasured change has no
	// cost claim either — the tokens a shorter prompt did not spend are not a saving until something
	// measured what they bought.
	CostWin bool `json:"cost_win"`
	// EqualQuality is the phrase that must never be attached to a failed guardrail. It is a separate
	// field, rather than derived at each call site, so a test can assert it directly.
	EqualQuality bool `json:"equal_quality"`
	// Countable reports whether this change may contribute to an aggregate improvement/savings figure.
	// False for everything unmeasured — this is the ledger's filter, expressed once.
	Countable bool `json:"countable"`
	// Reason narrates the claim from the fields above. Never a source of truth.
	Reason string `json:"reason"`
}

// ReportAuthored decides what may be claimed about an authored change.
//
// `ran` is whether the harness produced a verdict at all. It is a separate parameter rather than being
// inferred from a zero Verdict because a genuinely zero delta is a real, measured outcome, and inferring
// "unmeasured" from it would silently discard a tie somebody paid to establish.
func ReportAuthored(v Verdict, ran bool, guardrail GuardrailOutcome) AuthoredReport {
	if !ran {
		// 🔴 The default, and it is load-bearing. An applied-but-unverified change claims NOTHING: no
		// quality word, no cost saving, and no contribution to any aggregate.
		return AuthoredReport{
			Quality:   ClaimUnmeasured,
			Countable: false,
			Reason:    "applied without verification — no quality or cost claim is attached until the harness runs",
		}
	}

	switch guardrail {
	case GuardrailFailed:
		// A cheaper model whose held-out quality interval does not overlap the incumbent's is a
		// REGRESSION. The cost saving is real and is not mentioned as a win, because pairing "we saved
		// money" with a measured quality loss is how a regression gets shipped.
		return AuthoredReport{
			Quality: ClaimRegression, CostWin: false, EqualQuality: false, Countable: true,
			Reason: fmt.Sprintf(
				"held-out %s did not hold: the cheaper model is measurably worse, so this is a quality regression, not an equal-quality change",
				metricName(v)),
		}
	case GuardrailCleared:
		return AuthoredReport{
			Quality: ClaimTie, CostWin: v.CostDelta < 0, EqualQuality: true, Countable: true,
			Reason: fmt.Sprintf(
				"held-out %s intervals overlap — quality is a tie and the change is a cost win", metricName(v)),
		}
	}

	// No guardrail applies: read the claim off the measured delta the same way any other candidate's is
	// read. Same machinery, same words.
	switch {
	case v.Significant && v.Delta.Mean > 0:
		return AuthoredReport{Quality: ClaimWin, CostWin: v.CostDelta < 0, Countable: true,
			Reason: fmt.Sprintf("verified %s improvement", metricName(v))}
	case v.Significant && v.Delta.Mean < 0:
		return AuthoredReport{Quality: ClaimRegression, Countable: true,
			Reason: fmt.Sprintf("verified %s regression", metricName(v))}
	default:
		return AuthoredReport{Quality: ClaimTie, CostWin: v.CostDelta < 0, EqualQuality: true, Countable: true,
			Reason: fmt.Sprintf("%s intervals overlap — reported as a tie", metricName(v))}
	}
}

func metricName(v Verdict) string {
	if v.Metric == "" {
		return "task_success"
	}
	return v.Metric
}
