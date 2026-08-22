package proposal

// operatorPrior is a cheap, static estimate of an operator's historical lift, used ONLY to order
// candidates for verification (design Q2: pre-verification "expected gain" = operator-prior × cluster
// severity). It is never surfaced as a result — the measured verdict replaces it post-verification.
// The values are deliberately coarse; their only job is to verify the cheap, high-yield operators
// (downgrade, prune) before the expensive multi-candidate prompt sweeps.
var operatorPrior = map[OperatorKind]float64{
	OpModelUpgrade:     0.30,
	OpEnableThinking:   0.25,
	OpModelDowngrade:   0.20,
	OpPromptRewrite:    0.35,
	OpContextPolicy:    0.25,
	OpReorder:          0.15,
	OpRAGTune:          0.30,
	OpAddRerank:        0.30,
	OpAddSkill:         0.35,
	OpFixSchemaBinding: 0.30,
	OpPrune:            0.15,
	OpMerge:            0.15,
	// P13 deeper prompt operators — priors ORDER verification only, never a result (they are replaced
	// by the measured verdict). Hardening sits with the base rewrite; curation/compression/redundancy
	// are lower-yield tidy-ups, so they verify after the higher-yield changes.
	OpInstructionHarden: 0.30,
	OpFewShotCurate:     0.20,
	OpPromptCompress:    0.15,
	OpRedundancyRemove:  0.15,
	// P13 model-parameter tuning — a cheap, targeted knob, ordered near the other model changes.
	OpParamTune: 0.20,
	// P14 skills & tools. The removals sit BELOW their additive siblings on purpose: an add that does
	// nothing wastes tokens, a remove that was wrong takes away a capability the eval set may not cover,
	// so the cheaper-to-be-wrong direction is verified first when both are on the table.
	OpRemoveSkill:  0.20,
	OpToolPrune:    0.20,
	OpToolMinimize: 0.25,
	// Memory. A coarse ordering hint and nothing else, like every other row here — and since P18 gave the
	// axis real materializers, this number now IS replaced by a measured verdict on a covered cell, which
	// is the ordinary case rather than the exception P17 described.
	//
	// The value sits with the context policy's: both change what the model effectively sees, and neither
	// has a cheap tell in advance. It is not lower to "reflect" the refusal — a prior encodes expected
	// lift if applied, and bending it to encode applicability would put two different facts in one number.
	OpMemoryPolicy: 0.25,
	// Harness. The highest prior in the table, and the number states a belief rather than a hope: when a
	// node's failing cases genuinely need a second turn, no in-call change recovers them — a stronger
	// model still answers once. That is why the axis exists.
	//
	// 🔴 A prior encodes expected LIFT IF APPLIED, and nothing else. It is deliberately not lowered to
	// "reflect" that most harness cells refuse at transform, nor to reflect that a heavier scaffold costs
	// more: applicability is the transform's answer and cost is the admissibility gate's, and bending one
	// number to carry three facts is how a ranking silently stops meaning what it says.
	OpHarnessStrategy: 0.35,
	// P34. The loop operator INHERITS the harness operator's prior unchanged, because it is the same
	// hypothesis on the axis ADR-014 moved it to — lowering it would encode "we split an axis" as "we
	// expect less lift", which is two different facts in one number.
	OpLoopStrategy: 0.35,
	// Topology. The lowest prior in the table, and the number states a belief: declaring two independent
	// calls concurrent cannot change what any of them ANSWERS, so its expected lift on `task_success` is
	// zero by construction. What it can buy is wall-clock — which the ranking layer reads from
	// `eval_latency_ms` — and what it costs is peak resource use.
	//
	// 🔴 A prior encodes expected lift IF APPLIED. It is deliberately not raised to reflect that latency
	// wins are valuable: value is the ranking layer's question, and bending this number to carry it is
	// how a ranking silently stops meaning what it says.
	OpGraphTopology: 0.05,
}

// verifyOrderHint ranks operators cheapest-first for verification ordering (design 5.1): a single
// cheap change (downgrade / prune) is proven before an expensive multi-candidate prompt sweep. Lower
// is cheaper. Unknown operators sort last.
var verifyOrderHint = map[OperatorKind]int{
	OpModelDowngrade:   0,
	OpPrune:            0,
	OpParamTune:        0, // a single param swap, as cheap as a model swap
	OpMerge:            1,
	OpReorder:          1,
	OpFixSchemaBinding: 2,
	OpAddSkill:         2,
	// P14: a single skill/tool delta costs about what a skill swap costs — one candidate, one eval pass.
	// Minimization sits one step later: it is a whole-set change, so its diff is the widest of the three.
	OpRemoveSkill:       2,
	OpToolPrune:         2,
	OpToolMinimize:      3,
	OpContextPolicy:     3,
	OpModelUpgrade:      3,
	OpEnableThinking:    3,
	OpRAGTune:           4,
	OpAddRerank:         4,
	OpPromptRewrite:     5, // most expensive: a candidate sweep with generated prompts
	OpInstructionHarden: 5, // the P13 prompt operators are sweeps too — same cost class
	OpFewShotCurate:     5,
	OpPromptCompress:    5,
	OpRedundancyRemove:  5,
	// Memory. It sits with the context policy at 3 — one candidate, one eval pass, same cost class.
	//
	// 🔴 It was never sorted last "because it will be refused anyway", and that restraint is why nothing
	// here had to change when P18 made memory verifiable. Encoding applicability in a COST hint would have
	// been the same category error as bending the prior: this table answers "how expensive is it to
	// verify", the refusal answers "can it be verified at all", and one number cannot carry both.
	OpMemoryPolicy: 3,
	// Harness. The MOST expensive thing to verify in the table, and honestly so: every other operator
	// runs the eval set once per candidate, while a heavier scaffold runs it once per candidate and then
	// pays max_turns model calls for each case inside that run. Ordering it last is not a judgement about
	// its value (the prior says the opposite) — it is the cheapest-first rule applied to the operator
	// that is genuinely the least cheap.
	OpHarnessStrategy: 6,
	// P34. The loop operator inherits the harness operator's slot: same cost class, same axis, one
	// candidate per alternative strategy.
	OpLoopStrategy: 6,
	// Topology sits at 1, beside merge and reorder: it emits ONE candidate and changes no node's
	// contents, so its diff is as narrow as a rearrangement's. Verifying it early is the cheapest-first
	// rule working — it is quick to disprove, and disproving it costs one eval pass.
	OpGraphTopology: 1,
}

// VerifyOrderHint returns the cheapest-operator-first verification order hint for an operator, for the
// verification fan-out (verification.Job.OrderHint). Lower runs first.
func VerifyOrderHint(op OperatorKind) int {
	if h, ok := verifyOrderHint[op]; ok {
		return h
	}
	return 99
}

// expectedGain estimates pre-verification gain as operator-prior × severity, where severity is
// proxied by the diagnosis confidence (a higher-confidence diagnosis is a firmer target) with a
// floor so a Signal-driven operator with no diagnosis confidence still ranks.
func expectedGain(kind OperatorKind, in OperatorInput) float64 {
	prior := operatorPrior[kind]
	sev := in.Diagnosis.Confidence
	if sev <= 0 {
		sev = 0.5
	}
	return prior * sev
}

// ── Operator credit (P13 13c task 10.4, FR29) ───────────────────────────────────────────────────
//
// An operator's record must measure the OPERATOR. The moment a human corrects a nearly-right proposal
// and the corrected version wins, crediting that win to the originating operator turns its win rate
// into a measure of how often people fix it — and every downstream use of that number (the gain prior
// above, cheapest-first ordering, "which operators earn their keep") is then reading the wrong thing.
//
// So there are exactly two rules, and they are stated here rather than at each reporting call site:
//
//	a USER-originated candidate credits no operator at all;
//	a candidate FORKED from a proposal credits no operator either — its outcome belongs to the person
//	who changed it, and the original proposal's own outcome (accepted or rejected as proposed) is what
//	the operator is measured on.

// CreditedOperator reports which operator, if any, may be credited with this candidate's outcome.
//
// The boolean is not a nicety: `("", false)` and `("authored", true)` are different facts, and a caller
// that ignored the flag would create an "authored" row in a table of operator performance — which reads
// as a catalog operator that nobody can find in the catalog.
func CreditedOperator(c Candidate) (OperatorKind, bool) {
	if c.Origin.IsUser() {
		return "", false
	}
	if c.ForkedFromProposal != "" {
		// Defensive, and deliberately not merely defensive: a candidate carrying a fork pointer was
		// touched by a person even if something upstream forgot to set Origin. The pointer is the
		// evidence; trusting only Origin would let one missed assignment restore the inflation.
		return "", false
	}
	return c.Operator, true
}

// OperatorCredits tallies outcomes per operator, excluding everything CreditedOperator withholds.
//
// `won` is supplied by the caller because "won" is a verification verdict and this package does not
// decide verdicts — passing it in keeps the tally arithmetic here and the judgment where it belongs.
func OperatorCredits(cands []Candidate, won func(Candidate) bool) map[OperatorKind]OperatorCredit {
	out := map[OperatorKind]OperatorCredit{}
	for _, c := range cands {
		op, ok := CreditedOperator(c)
		if !ok {
			continue
		}
		cr := out[op]
		cr.Proposed++
		if won != nil && won(c) {
			cr.Won++
		}
		out[op] = cr
	}
	return out
}

// OperatorCredit is one operator's tally.
type OperatorCredit struct {
	Proposed int
	Won      int
}

// WinRate is Won/Proposed, or 0 when nothing was proposed. Stated as a method so no caller divides by
// zero in its own way.
func (c OperatorCredit) WinRate() float64 {
	if c.Proposed == 0 {
		return 0
	}
	return float64(c.Won) / float64(c.Proposed)
}
