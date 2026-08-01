package proposal

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// P13 §2 — the deeper prompt operators. Each is grounded-or-silent, publishes a new immutable version,
// carries its grounding, and is never applied without verification.

// p13Grounded builds groundings for the given case ids.
func p13Grounded(cases ...string) []FailingCaseGrounding {
	var out []FailingCaseGrounding
	for _, c := range cases {
		out = append(out, FailingCaseGrounding{
			CaseID: c, FailureReason: "under-specified step in " + c, TraceRef: strings.Repeat("a", 64)})
	}
	return out
}

// p13Engine wires the four prompt operators (via the default catalog) with the in-repo grounded
// optimizer and the shared base spec.
func p13Engine() Engine {
	return Engine{Menu: testMenu(), Base: baseSpec(), Optimizer: SelfRefineOptimizer{}}
}

func p13Target(body string, cases ...string) Target {
	return Target{
		Diagnosis:      diag("answer", diagnosis.CausePromptFormatDrift, cases...),
		Pattern:        patternclassifier.PromptChaining,
		BasePromptBody: body,
		Groundings:     p13Grounded(cases...),
	}
}

// 2.1: instruction hardening fires on a GROUNDED under-specification and is SILENT when ungrounded.
func TestInstructionHardenOnlyOnGroundedUnderspec(t *testing.T) {
	e := p13Engine()

	grounded := e.Propose([]Target{p13Target("Answer the question.", "c1", "c2")})
	c := findCandidate(t, grounded.Candidates, OpInstructionHarden, "answer")
	if c.Grounding == nil || !c.Grounding.GroundedIn("c1") {
		t.Error("hardened candidate must carry its grounding")
	}
	if !strings.Contains(c.NewPromptBody, "exactly") {
		t.Errorf("hardening did not add an explicit constraint:\n%s", c.NewPromptBody)
	}

	// Ungrounded: no attached cases → no candidate, no error.
	ungrounded := e.Propose([]Target{p13Target("Answer the question.")})
	if hasCandidate(ungrounded.Candidates, OpInstructionHarden, "answer") {
		t.Error("instruction hardening emitted an ungrounded candidate")
	}
	for _, r := range ungrounded.Refusals {
		if r.Operator == OpInstructionHarden {
			t.Errorf("declining ungrounded was reported as an error: %s", r.Reason)
		}
	}
}

// 2.2: few-shot curation removes duplicate exemplars when grounded, and is silent when there is nothing
// to curate (grounded-or-silent).
func TestFewShotCurateGroundedOrSilent(t *testing.T) {
	e := p13Engine()

	withDupes := "Do the task.\nExample: map A->B\nExample: map A->B\nReturn JSON."
	got := e.Propose([]Target{p13Target(withDupes, "c1")})
	c := findCandidate(t, got.Candidates, OpFewShotCurate, "answer")
	if strings.Count(c.NewPromptBody, "Example: map A->B") != 1 {
		t.Errorf("duplicate exemplar was not curated:\n%s", c.NewPromptBody)
	}
	if c.Grounding == nil {
		t.Error("curated candidate must carry grounding")
	}

	// No exemplars to curate → silent even though grounded.
	clean := e.Propose([]Target{p13Target("Do the task. Return JSON.", "c1")})
	if hasCandidate(clean.Candidates, OpFewShotCurate, "answer") {
		t.Error("few-shot curation emitted a candidate with nothing to curate")
	}
}

// 2.3: compression competes on the standard metric family — it carries no token-count target as a goal,
// and its candidate is scored like any other (Dimensions=["prompt"]).
func TestCompressCompetesOnMetricsNotTokenTarget(t *testing.T) {
	e := p13Engine()
	bloated := "Answer clearly.   \n\n\n\nBe concise."
	got := e.Propose([]Target{p13Target(bloated, "c1")})
	c := findCandidate(t, got.Candidates, OpPromptCompress, "answer")

	if len(c.NewPromptBody) >= len(bloated) {
		t.Errorf("compression did not reduce the body: %d >= %d", len(c.NewPromptBody), len(bloated))
	}
	// It is scored on the prompt dimension by the standard family — no bespoke token-target metric.
	if len(c.Dimensions) != 1 || c.Dimensions[0] != "prompt" {
		t.Errorf("compression candidate must be a prompt-dimension change, got %v", c.Dimensions)
	}
	// The rationale must not encode a token count as a success criterion — token reduction is a means.
	low := strings.ToLower(c.Rationale)
	if strings.Contains(low, "token target") || strings.Contains(low, "tokens=") {
		t.Errorf("compression rationale states a token target as a goal: %q", c.Rationale)
	}
	if c.Grounding == nil {
		t.Error("compression candidate must carry grounding")
	}
}

// 2.4: redundancy removal drops exact-duplicate instruction lines when grounded.
func TestRedundancyRemoveGrounded(t *testing.T) {
	e := p13Engine()
	redundant := "Rule A: be precise.\nRule B: cite sources.\nRule A: be precise.\nDone."
	got := e.Propose([]Target{p13Target(redundant, "c1")})
	c := findCandidate(t, got.Candidates, OpRedundancyRemove, "answer")
	if strings.Count(c.NewPromptBody, "Rule A: be precise.") != 1 {
		t.Errorf("redundant line was not removed:\n%s", c.NewPromptBody)
	}
	if c.Grounding == nil {
		t.Error("redundancy-removal candidate must carry grounding")
	}

	// No redundancy → silent.
	clean := e.Propose([]Target{p13Target("Rule A.\nRule B.\nDone.", "c1")})
	if hasCandidate(clean.Candidates, OpRedundancyRemove, "answer") {
		t.Error("redundancy removal emitted a candidate with no redundancy")
	}
}

// 2.5: every rewrite publishes a NEW content-addressed prompt version, and the parent stays intact.
func TestRewritePublishesNewImmutableVersion(t *testing.T) {
	e := p13Engine()
	got := e.Propose([]Target{p13Target("Answer the question.", "c1")})

	for _, op := range []OperatorKind{OpInstructionHarden} {
		c := findCandidate(t, got.Candidates, op, "answer")
		ref := c.Spec.Nodes["answer"].PromptRef
		if len(ref) != 64 {
			t.Errorf("%s: PromptRef is not a 64-hex content address: %q", op, ref)
		}
		if ref != syntheticPromptRef(c.NewPromptBody) {
			t.Errorf("%s: PromptRef is not the content address of the new body", op)
		}
		// Content-addressed = immutable: the same body always mints the same ref; a different body a
		// different ref. Two distinct bodies must not collide.
		if syntheticPromptRef("body-x") == syntheticPromptRef("body-y") {
			t.Error("two different bodies produced the same version id")
		}
	}
	// The parent (baseline) is untouched — it stays resolvable exactly as before.
	if _, ok := e.Base.Nodes["answer"]; ok {
		t.Error("the baseline node was mutated by a rewrite (parent not intact)")
	}
}

// 2.7: every prompt candidate carries its grounding (the cases it addresses).
func TestPromptCandidateCarriesGrounding(t *testing.T) {
	e := p13Engine()
	body := "Answer.\nExample: a->b\nExample: a->b\nRule X.\nRule X.   \n\n\nEnd."
	got := e.Propose([]Target{p13Target(body, "c1", "c2")})
	ops := []OperatorKind{OpInstructionHarden, OpFewShotCurate, OpPromptCompress, OpRedundancyRemove}
	for _, op := range ops {
		c := findCandidate(t, got.Candidates, op, "answer")
		if c.Grounding == nil {
			t.Errorf("%s: candidate carries no grounding", op)
			continue
		}
		for _, id := range []string{"c1", "c2"} {
			if !c.Grounding.GroundedIn(id) {
				t.Errorf("%s: candidate not traceable to case %s", op, id)
			}
		}
		if len(c.Grounding.Hash) != 64 {
			t.Errorf("%s: grounding bundle is not content-hashed", op)
		}
	}
}

// 2.8: every new prompt operator has a gain prior (ordering only) and a verify-order hint.
func TestNewPromptOperatorsHavePriors(t *testing.T) {
	for _, op := range []OperatorKind{OpInstructionHarden, OpFewShotCurate, OpPromptCompress, OpRedundancyRemove} {
		if p, ok := operatorPrior[op]; !ok || p <= 0 {
			t.Errorf("%s has no positive gain prior (got %v, present=%v)", op, p, ok)
		}
		if VerifyOrderHint(op) == 99 {
			t.Errorf("%s has no verify-order hint (falls to the unknown default)", op)
		}
	}
}

// 2.9: no path applies a prompt candidate directly — the operator only EMITS, and Propose leaves the
// baseline untouched. A candidate reaches a diff solely through Compile (resolve + codemod + build
// gate) and then the verification gate — never from the operator itself.
func TestPromptCandidateNeverAppliedWithoutVerification(t *testing.T) {
	e := p13Engine()
	// Snapshot the baseline "answer" node before proposing.
	_, hadOverrideBefore := e.Base.Nodes["answer"]

	body := "Answer.\nExample: a->b\nExample: a->b\nRule X.\nRule X.\n\n\nEnd."
	got := e.Propose([]Target{p13Target(body, "c1")})

	// The baseline is not mutated: no candidate was applied to it.
	if _, has := e.Base.Nodes["answer"]; has != hadOverrideBefore {
		t.Fatal("Propose applied a candidate to the baseline spec")
	}
	// Every emitted prompt candidate carries a fresh Spec that is a DISTINCT object from the baseline —
	// an emission, not an application.
	for _, c := range got.Candidates {
		if !isPromptOp(c.Operator) {
			continue
		}
		if c.Spec == e.Base {
			t.Errorf("%s: candidate aliases the baseline spec instead of deriving a copy", c.Operator)
		}
		if c.Spec.Nodes["answer"].PromptRef == "" {
			t.Errorf("%s: candidate did not express its change on its own spec", c.Operator)
		}
	}
}

func isPromptOp(op OperatorKind) bool {
	switch op {
	case OpInstructionHarden, OpFewShotCurate, OpPromptCompress, OpRedundancyRemove, OpPromptRewrite:
		return true
	}
	return false
}
