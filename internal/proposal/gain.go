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
}

// verifyOrderHint ranks operators cheapest-first for verification ordering (design 5.1): a single
// cheap change (downgrade / prune) is proven before an expensive multi-candidate prompt sweep. Lower
// is cheaper. Unknown operators sort last.
var verifyOrderHint = map[OperatorKind]int{
	OpModelDowngrade:   0,
	OpPrune:            0,
	OpMerge:            1,
	OpReorder:          1,
	OpFixSchemaBinding: 2,
	OpAddSkill:         2,
	OpContextPolicy:    3,
	OpModelUpgrade:     3,
	OpEnableThinking:   3,
	OpRAGTune:          4,
	OpAddRerank:        4,
	OpPromptRewrite:    5, // most expensive: a candidate sweep with generated prompts
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
