package assessment

import (
	"context"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/herosagent"
)

// inference_test.go covers §3.1, §3.2, §3.3 and §3.6.

func answering(claim string, confidence float64, version string) *scriptedAnalyst {
	return &scriptedAnalyst{
		answers:  map[string]Answer{},
		fallback: Answer{Claim: claim, Confidence: confidence, ProviderModelVersion: version},
	}
}

// TestTheQuestionCarriesOnlyTheAxisItAsksAbout is §3.1 as a property of the TYPE rather than of the
// caller. An inference handed every field of every node is an inference whose cost is proportional to
// the repository rather than to the gap.
func TestTheQuestionCarriesOnlyTheAxisItAsksAbout(t *testing.T) {
	s := subjectFor(t, "python")
	memory, err := questionFor(AxisMemory, s)
	if err != nil {
		t.Fatalf("questionFor: %v", err)
	}
	model, err := questionFor(AxisModel, s)
	if err != nil {
		t.Fatalf("questionFor: %v", err)
	}
	if len(memory.Facts) == 0 || len(model.Facts) == 0 {
		t.Fatal("a question carries no facts at all")
	}
	// The memory question must not carry the model id, and the model question must not carry the
	// memory floor. Two axes, two disjoint field sets.
	for _, f := range memory.Facts {
		if _, ok := f.Fields["model_id"]; ok {
			t.Fatalf("the memory question carries model_id for %s — an inference that can see every "+
				"field can answer about an axis it was not asked about", f.NodeID)
		}
	}
	for _, f := range model.Facts {
		if _, ok := f.Fields["ir_floor"]; ok {
			t.Fatalf("the model question carries the memory/harness floor for %s", f.NodeID)
		}
	}
}

// TestThePromptQuestionCarriesNoPromptText is §7.4 where it is easiest to break.
//
// 🔴 The prompt axis is the one place a "just send the prompt" shortcut is natural and would be a
// privacy regression: prompt text is customer content, it would travel to a provider, and it would be
// cached there. The question carries the prompt's SHAPE — resolved or not, how many variables — and
// never its words.
func TestThePromptQuestionCarriesNoPromptText(t *testing.T) {
	s := subjectFor(t, "python")
	q, err := questionFor(AxisPrompt, s)
	if err != nil {
		t.Fatalf("questionFor: %v", err)
	}
	// The python fixture's prompts are the message contents at each call site. Whatever discovery
	// resolved must NOT be in the question.
	for _, n := range s.IR.Nodes {
		text := strings.TrimSpace(n.Prompt.Inline)
		if text == "" || text == discovery.UnresolvedSentinel || len(text) < 6 {
			continue
		}
		for _, f := range q.Facts {
			for k, v := range f.Fields {
				if strings.Contains(v, text) {
					t.Fatalf("the prompt question carries the prompt TEXT in %s.%s — customer content "+
						"would travel to a provider and be cached there", f.NodeID, k)
				}
			}
		}
	}
}

// TestTheQuestionCarriesTheFrontendsSoTheModelKnowsWhyAGapExists is design D6 reaching the model.
// "The python frontend is syntactic and cannot follow a value across a statement" is the single most
// useful thing an analyser can be told about why a field is empty.
func TestTheQuestionCarriesTheFrontendsSoTheModelKnowsWhyAGapExists(t *testing.T) {
	q, err := questionFor(AxisMemory, subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("questionFor: %v", err)
	}
	if len(q.Frontends) == 0 {
		t.Fatal("the question names no frontend, so a model asked why a field is empty has to guess")
	}
	found := false
	for _, f := range q.Frontends {
		if f.AnalysisKind == discovery.AnalysisSyntactic {
			found = true
		}
	}
	if !found {
		t.Fatal("the python fixture's frontend is not reported as syntactic in the question")
	}
}

// TestTheFloorPassesTheIRFloorThroughLabelled is the D6 inversion at the prompt boundary.
//
// Handing a model `memory: none` as though it were an observation is how "this repository has no
// memory strategy" gets said with a measurement's confidence. The field is present — the model should
// know what the analyser emitted — and it travels with a sentence saying what it means.
func TestTheFloorPassesTheIRFloorThroughLabelled(t *testing.T) {
	for _, axis := range []Axis{AxisMemory, AxisHarness} {
		q, err := questionFor(axis, subjectFor(t, "python"))
		if err != nil {
			t.Fatalf("questionFor: %v", err)
		}
		for _, f := range q.Facts {
			if f.Fields["ir_floor"] == "" {
				t.Fatalf("%s/%s carries no ir_floor", axis, f.NodeID)
			}
			if !strings.Contains(f.Fields["ir_floor_means"], "absence of evidence") {
				t.Fatalf("%s/%s carries the floor without saying what a floor is — a model told `none` "+
					"will report that the repository has none", axis, f.NodeID)
			}
		}
	}
}

// TestAnInferredFindingRecordsTheProviderModelVersion is §3.6 and design D7.
func TestAnInferredFindingRecordsTheProviderModelVersion(t *testing.T) {
	inf := holdoutInference(t, answering("a per-session store, never pruned", 0.9, "anthropic/claude-opus-5-20260501"))
	f, _, err := inf.Infer(context.Background(), AxisMemory, subjectFor(t, "python"))
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if f.ProviderModelVersion() != "anthropic/claude-opus-5-20260501" {
		t.Fatalf("the finding records %q as its model version", f.ProviderModelVersion())
	}
	if f.Origin() != OriginInferred {
		t.Fatalf("the finding's origin is %s", f.Origin())
	}
}

// TestAnAnswerWithNoModelVersionIsRefused is D7's teeth. A placeholder here would make the field LOOK
// recorded, which is worse than an empty one.
func TestAnAnswerWithNoModelVersionIsRefused(t *testing.T) {
	inf := holdoutInference(t, answering("a per-session store", 0.9, ""))
	_, _, err := inf.Infer(context.Background(), AxisMemory, subjectFor(t, "python"))
	if err == nil || !strings.Contains(err.Error(), "named no version") {
		t.Fatalf("an unattributable answer was accepted: %v", err)
	}
}

// TestTheContentAddressIsStableAndInputDerived is §3.2.
//
// Stable, so two runs that saw the same thing share an address and a re-inference is a diff of ONE
// thing. Input-derived, so a change in the repository changes it — otherwise a "diff" would compare
// two answers about two different questions.
func TestTheContentAddressIsStableAndInputDerived(t *testing.T) {
	s := subjectFor(t, "python")
	q, err := questionFor(AxisMemory, s)
	if err != nil {
		t.Fatalf("questionFor: %v", err)
	}
	first, err := ContentAddress(q)
	if err != nil {
		t.Fatalf("ContentAddress: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := ContentAddress(q)
		if err != nil {
			t.Fatalf("ContentAddress: %v", err)
		}
		if again != first {
			t.Fatalf("the address is not stable: %s vs %s", first, again)
		}
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("the address is not a content hash: %q", first)
	}

	// A different axis over the same repository is a different question and must address differently,
	// or two axes would share a pin and a provider honouring the idempotency key would answer the
	// second from the first.
	other, err := questionFor(AxisHarness, s)
	if err != nil {
		t.Fatalf("questionFor: %v", err)
	}
	otherAddr, err := ContentAddress(other)
	if err != nil {
		t.Fatalf("ContentAddress: %v", err)
	}
	if otherAddr == first {
		t.Fatal("the memory and harness questions share a content address — nine axes would share one " +
			"idempotency key, and a provider that honours the header would answer all nine from the first")
	}

	// A different repository is a different question.
	moved, err := questionFor(AxisMemory, subjectFor(t, "typescript"))
	if err != nil {
		t.Fatalf("questionFor: %v", err)
	}
	movedAddr, err := ContentAddress(moved)
	if err != nil {
		t.Fatalf("ContentAddress: %v", err)
	}
	if movedAddr == first {
		t.Fatal("two different repositories share a content address")
	}
}

// TestTheFloorMatchesTheAgentsFloor is doc'd in inference.go: two floors in one product means two
// definitions of "confident enough" and a reader has no way to know which one a claim passed.
func TestTheFloorMatchesTheAgentsFloor(t *testing.T) {
	if DefaultConfidenceFloor != herosagent.DefaultConfidenceFloor {
		t.Fatalf("the assessment floor is %v and the agent's is %v. Two floors in one product means two "+
			"definitions of \"confident enough\"; if this divergence is deliberate, say why here and in "+
			"inference.go", DefaultConfidenceFloor, herosagent.DefaultConfidenceFloor)
	}
}

// TestAZeroFloorIsRefused — a floor nobody set is a floor that is zero, and a zero floor is FR10
// switched off.
func TestAZeroFloorIsRefused(t *testing.T) {
	if _, err := NewHerosInference(answering("x", 1, "v"), 0); err == nil {
		t.Fatal("a zero confidence floor was accepted")
	}
}

// TestNoModelIsRefusedRatherThanStubbed mirrors `herosagent.newRunner`'s refusal and its reason: "a
// stub returning plausible edges is indistinguishable from a working agent".
func TestNoModelIsRefusedRatherThanStubbed(t *testing.T) {
	if _, err := NewHerosInference(nil, DefaultConfidenceFloor); err == nil {
		t.Fatal("an inference with no model was built, so a deployment could ship one that answers " +
			"nothing and looks wired")
	}
}
