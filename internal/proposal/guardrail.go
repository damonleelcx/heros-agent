package proposal

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/evalstats"
)

// guardrail.go is P13 Decision 4's one genuinely-new contract: a model downgrade is admissible ONLY
// when the platform cannot statistically tell the cheaper model apart from the incumbent on cases the
// operator did not select. It is deliberately assembled from an EXISTING predicate (CI overlap) over an
// EXISTING metric (task_success), so it cannot fork the definition of quality (design Decision 5) and
// introduces no new eval, oracle, or metric (task 4.2).
//
// Nothing here is hashed or stored: the guardrail is a verification-time, in-memory admissibility
// predicate on modelDowngradeOp. Its verdict rides on the in-memory Candidate, never on config_hash.

// MinHeldOutCases is the floor below which the held-out set cannot support a discriminating interval.
// Below it the guardrail returns GuardrailInsufficientData rather than a FALSE TIE — an under-populated
// held-out set understates variance the same way an under-seeded CI does (evalstats.ErrUnderSeeded),
// so a "tie" declared on too little data is a cost win bought with a quality claim nobody measured.
// Five mirrors evalstats.DefaultMinSeeds for the same reason: it is the smallest N at which a paired
// bootstrap over the held-out set says anything at all.
const MinHeldOutCases = 5

// GuardrailVerdict is the THREE-valued outcome of the downgrade guardrail. The third value is
// load-bearing (task 1.4): "not enough held-out data to tell" is a distinct answer from "the intervals
// overlap", and collapsing the two would admit a downgrade on the strength of data that was never
// collected.
type GuardrailVerdict string

const (
	// GuardrailAdmissible: the cheaper model's task-success CI OVERLAPS the incumbent's on held-out
	// cases — the platform cannot tell them apart, so the downgrade is an equal-quality-cheaper tie.
	GuardrailAdmissible GuardrailVerdict = "admissible"
	// GuardrailInadmissible: the CIs do NOT overlap on held-out cases — the cheaper model measurably
	// regressed quality, so no cost improvement can make it admissible.
	GuardrailInadmissible GuardrailVerdict = "inadmissible"
	// GuardrailInsufficientData: the held-out set is smaller than MinHeldOutCases, so the guardrail
	// refuses to render a discriminating verdict. NOT admitted as a tie by default (spec scenario
	// "Insufficient held-out data yields an explicit verdict, not a pass").
	GuardrailInsufficientData GuardrailVerdict = "inadmissible-insufficient-data"
)

// Admissible reports whether a verdict admits the downgrade for verification. Only GuardrailAdmissible
// does; both inadmissible verdicts refuse it. Stated as a method so no caller re-derives the rule.
func (v GuardrailVerdict) Admissible() bool { return v == GuardrailAdmissible }

// Split is a deterministic, disjoint partition of a candidate's eval cases into the MOTIVATING set (the
// cases an operator may use to argue for a change) and the HELD-OUT set (the cases the guardrail judges
// on). Disjoint by construction: every case lands in exactly one bucket.
type Split struct {
	Motivating []string
	HeldOut    []string
}

// HeldOutSplit derives the motivating/held-out partition deterministically from the configuration hash
// and the case ids (task 1.4). The derivation is a pure function of (configHash, caseID) per case, so:
//
//   - it is REPRODUCIBLE — the same config and case set always split the same way, which is what lets a
//     guardrail verdict be re-checked months later (TestHeldOutSplitIsDeterministic);
//   - it is INPUT-ORDER-INDEPENDENT — each case's bucket depends only on its own id and the config hash,
//     never on the slice order, so a caller that shuffles the cases gets the same split;
//   - it is DISJOINT by construction — a case is assigned to exactly one bucket, so the held-out set can
//     never share a case with the motivating set (task 3.2, TestGuardrailCasesDisjointFromMotivating).
//
// Seeding the split by config_hash (not a fixed constant) means two different configurations do not
// share a split, so a case that motivated one change can be held-out for another — the assignment is
// tied to the configuration being judged, not global.
func HeldOutSplit(configHash string, caseIDs []string) Split {
	// De-duplicate first: a repeated case id must not tip the balance or appear twice in a bucket.
	seen := map[string]bool{}
	uniq := make([]string, 0, len(caseIDs))
	for _, id := range caseIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	sort.Strings(uniq)

	var s Split
	for _, id := range uniq {
		if heldOutBucket(configHash, id) {
			s.HeldOut = append(s.HeldOut, id)
		} else {
			s.Motivating = append(s.Motivating, id)
		}
	}
	return s
}

// heldOutBucket decides one case's bucket: hash (configHash, caseID) and read the low bit. Even → held
// out. A per-case decision is what makes the split order-independent and disjoint.
func heldOutBucket(configHash, caseID string) bool {
	h := sha256.Sum256([]byte("p13-heldout\x00" + configHash + "\x00" + caseID))
	return binary.BigEndian.Uint64(h[:8])%2 == 0
}

// GuardrailResult is the guardrail's full verdict for one downgrade candidate: the three-valued
// verdict, the held-out split it judged on, and the two intervals behind it. It is in-memory only —
// carried on the Candidate for the reviewer, never hashed or stored.
type GuardrailResult struct {
	Verdict GuardrailVerdict
	// HeldOut and Motivating are the split the verdict was computed over (for the reviewer, and to prove
	// disjointness in a test).
	HeldOut    []string
	Motivating []string
	// Incumbent and Candidate are the task-success intervals on the HELD-OUT cases. Zero-valued when the
	// verdict is GuardrailInsufficientData (there was not enough data to compute them).
	Incumbent evalstats.Interval
	Candidate evalstats.Interval
	// Reason states, in words a UI can render verbatim, why the verdict is what it is.
	Reason string
}

// EvaluateDowngradeGuardrail is the admissibility predicate for a model downgrade (tasks 3.1, 3.2). It
// derives the held-out split from config_hash + the union of case ids, filters BOTH series to the
// held-out cases, and admits the downgrade iff the cheaper model's task-success interval overlaps the
// incumbent's THERE — the same overlap predicate evalstats.Compare uses for ties.
//
// It reads task-success and its confidence interval only; it registers no metric and computes no new
// quality number (design Decision 5). `incumbent` and `candidate` are task_success Series over the same
// case universe under multiple seeds.
func EvaluateDowngradeGuardrail(configHash string, incumbent, candidate evalstats.Series, cfg evalstats.Config) GuardrailResult {
	// The case universe is the intersection of the two series' cases — a held-out case only discriminates
	// if BOTH models were run on it.
	split := HeldOutSplit(configHash, intersectCases(incumbent, candidate))
	res := GuardrailResult{HeldOut: split.HeldOut, Motivating: split.Motivating}

	if len(split.HeldOut) < MinHeldOutCases {
		res.Verdict = GuardrailInsufficientData
		res.Reason = insufficientReason(len(split.HeldOut))
		return res
	}

	heldOut := map[string]bool{}
	for _, id := range split.HeldOut {
		heldOut[id] = true
	}
	inc := filterSeries(incumbent, heldOut)
	cand := filterSeries(candidate, heldOut)

	ci, errI := evalstats.Aggregate(inc, cfg)
	cc, errC := evalstats.Aggregate(cand, cfg)
	if errI != nil || errC != nil {
		// Under-seeded or empty on the held-out slice: not enough data to discriminate — the same honesty
		// as the case floor, never a silent pass.
		res.Verdict = GuardrailInsufficientData
		res.Reason = "held-out task-success could not be aggregated to a discriminating interval (under-seeded or empty)"
		return res
	}
	res.Incumbent, res.Candidate = ci, cc

	if ci.Overlaps(cc) {
		res.Verdict = GuardrailAdmissible
		res.Reason = overlapReason(ci, cc)
	} else {
		res.Verdict = GuardrailInadmissible
		res.Reason = noOverlapReason(ci, cc)
	}
	return res
}

// DowngradeOutcome is how an ADMITTED downgrade is reported (task 3.3): a win on cost and a TIE on
// quality — never a quality win. The guardrail admits a downgrade precisely because the platform cannot
// tell the two models apart on held-out quality, so calling that a quality win would claim a
// measurement the overlap denies.
type DowngradeOutcome struct {
	// Admissible mirrors the guardrail verdict: only an admitted downgrade is a shippable outcome.
	Admissible bool
	// CostWin is true when the cheaper model's cost is strictly lower.
	CostWin bool
	// QualityTie is true for every admitted downgrade — overlap ⇒ tie.
	QualityTie bool
	// QualityWin is ALWAYS false for a downgrade. Present as a field so a caller/test can assert it is
	// never set, rather than inferring its absence.
	QualityWin bool
}

// ClassifyDowngrade turns a guardrail result and the two cost figures into the reported outcome
// (task 3.3). An inadmissible verdict is not a shippable outcome. An admissible one is a cost win (when
// cost strictly drops) and a quality tie, never a quality win.
func ClassifyDowngrade(res GuardrailResult, incumbentCostUSD, candidateCostUSD float64) DowngradeOutcome {
	if !res.Verdict.Admissible() {
		return DowngradeOutcome{}
	}
	return DowngradeOutcome{
		Admissible: true,
		CostWin:    candidateCostUSD < incumbentCostUSD,
		QualityTie: true,
		QualityWin: false,
	}
}

// RequiresHeldOutGuardrail reports whether an operator's candidates must clear the held-out downgrade
// guardrail before they are admissible (task 3.4). ONLY model-downgrade does — a cheaper model is the
// one change whose own goal (lower cost) makes "ship it" the tempting failure. Model-upgrade and
// extended-thinking are unaffected: they become applicable on a ranked WIN like any other candidate,
// and this predicate leaves their admissibility exactly as it was.
func RequiresHeldOutGuardrail(op OperatorKind) bool { return op == OpModelDowngrade }

func intersectCases(a, b evalstats.Series) []string {
	set := map[string]bool{}
	for _, c := range b.Cases() {
		set[c] = true
	}
	var out []string
	for _, c := range a.Cases() {
		if set[c] {
			out = append(out, c)
		}
	}
	return out
}

// filterSeries returns the sub-series of s restricted to the given case ids, preserving every
// observation (all seeds) of a kept case.
func filterSeries(s evalstats.Series, keep map[string]bool) evalstats.Series {
	out := evalstats.Series{VariantID: s.VariantID, ConfigHash: s.ConfigHash, Metric: s.Metric}
	for _, o := range s.Obs {
		if keep[o.CaseID] {
			out.Obs = append(out.Obs, o)
		}
	}
	return out
}

func insufficientReason(n int) string {
	return fmt.Sprintf("held-out set has %d case(s), below the floor of %d; the guardrail cannot render "+
		"a discriminating verdict and refuses to admit a tie by default", n, MinHeldOutCases)
}

func overlapReason(inc, cand evalstats.Interval) string {
	return fmt.Sprintf("held-out task-success intervals overlap (%s vs %s); the models are statistically "+
		"indistinguishable → equal-quality-cheaper tie", fmtCI(inc), fmtCI(cand))
}

func noOverlapReason(inc, cand evalstats.Interval) string {
	return fmt.Sprintf("held-out task-success intervals do not overlap (%s vs %s); the cheaper model "+
		"measurably differs in quality → inadmissible", fmtCI(inc), fmtCI(cand))
}

func fmtCI(i evalstats.Interval) string {
	return fmt.Sprintf("[%.4f,%.4f]", i.Low, i.High)
}
