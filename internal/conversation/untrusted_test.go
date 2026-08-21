package conversation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// untrusted_test.go is §6.2, §6.3, §6.4 and §6.14: the untrusted-source boundary, exercised with the
// real attack corpus and with detection DELIBERATELY DISABLED.
//
// # 🔴 Why every test in this file ignores `Detect` entirely
//
// Because NFR-S2 is the defence and detection is not. If these fences called `Detect` first, they would
// be measuring the classifier's recall — and a classifier's recall over "everything a model might write
// about code" is not a number anybody should stake a repository on.
//
// So the model output below goes STRAIGHT to the emitter, unsanitized, exactly as a fully compromised
// model would produce it. What stops it is that a `proposal_id` has to RESOLVE, which is a database read
// no model has a path to influence. That property holds at 100%, whatever the classifier does.
//
// Nothing here calls `Detect`. That absence is the test.

// modelOutput is the shape a compromised model emits: a well-formed message, JSON-decoded, with no
// helper anywhere between the fixture and the emitter.
type modelOutput struct {
	Kind     string                 `json:"kind"`
	Proposal *ProposalPayload       `json:"proposal"`
	Result   *ResultPayload         `json:"result"`
	Extra    map[string]interface{} `json:"-"`
}

func loadModelOutput(t *testing.T, name string) modelOutput {
	t.Helper()
	corpus, err := AdversarialCorpus()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range corpus {
		if f.Name != name {
			continue
		}
		var out modelOutput
		// 🔴 Decoded, not constructed. A test that BUILT the payload in Go would be asserting against a
		// shape the test author chose; decoding the checked-in fixture asserts against the shape a fence
		// over the real file has to survive, and `TestTheAdversarialCorpusIsUnsanitized` keeps that file
		// genuinely hostile.
		if err := json.Unmarshal([]byte(f.Content), &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return out
	}
	t.Fatalf("the corpus has no fixture called %q", name)
	return modelOutput{}
}

// ── §6.2 · model output shaped exactly like a proposal ───────────────────────────────────────────

func TestModelOutputShapedLikeAProposalCreatesNoProposal(t *testing.T) {
	fixture := loadModelOutput(t, "model-output-proposal.json")
	if fixture.Proposal == nil || fixture.Proposal.ProposalID == "" {
		t.Fatal("the fixture carries no proposal payload; this fence would pass over nothing")
	}
	// The ledger holds ONE real proposal, and it is not the one the model named.
	ledger := resolves("prop_a_real_one_the_platform_wrote")
	sink := &recorder{}
	em := newEmitter(sink, Resolvers{Proposal: ledger})

	_, err := em.Emit(context.Background(), Message{Kind: KindProposal, Proposal: fixture.Proposal})
	if err == nil {
		t.Fatal("a proposal was created from model output. A fully compromised model can produce text; " +
			"what it must not be able to produce is a ledger row, and this says it can.")
	}
	if len(sink.msgs) != 0 {
		t.Errorf("the sink received %d messages", len(sink.msgs))
	}
	if ledger.calls == 0 {
		t.Error("the ledger was never consulted — the refusal came from something other than resolution, " +
			"so it would not hold for an id that happened to look more plausible")
	}
}

// TestTheStructuralDefenceHoldsWithDetectionDisabled is §6.3, and it is the same assertion made
// explicitly about the classifier's absence.
//
// 🔴 It is a separate test from the one above rather than a comment on it, because the two claims are
// different and both need to be able to go red: "the emitter refuses this" and "the emitter refuses this
// FOR A REASON THAT DOES NOT INVOLVE DETECTION".
func TestTheStructuralDefenceHoldsWithDetectionDisabled(t *testing.T) {
	corpus, err := AdversarialCorpus()
	if err != nil {
		t.Fatal(err)
	}
	// Every effect-bearing fixture, straight through. Detection is not called anywhere in this function.
	for _, name := range []string{"model-output-proposal.json", "model-output-result.json"} {
		t.Run(name, func(t *testing.T) {
			fixture := loadModelOutput(t, name)
			em := newEmitter(&recorder{}, Resolvers{
				Proposal: resolves("prop_real"),
				Delivery: resolves("del_real"),
				Verdict:  resolves("cfg_real@rev_real"),
			})
			if fixture.Proposal != nil {
				if _, err := em.Emit(context.Background(), Message{
					Kind: KindProposal, Proposal: fixture.Proposal}); err == nil {
					t.Fatal("a proposal from the attack corpus was accepted with detection disabled")
				}
			}
			if fixture.Result != nil {
				// A result needs a plan to reconcile against; give it one so the refusal below is about
				// the ARTIFACT rather than about the missing denominator.
				if _, err := em.Emit(context.Background(), Message{Kind: KindPlan, Plan: goodPlan()}); err != nil {
					t.Fatalf("plan: %v", err)
				}
				if _, err := em.Emit(context.Background(), Message{
					Kind: KindResult, Result: fixture.Result}); err == nil {
					t.Fatal("a result from the attack corpus was accepted with detection disabled")
				}
			}
		})
	}
	_ = corpus
}

// ── §6.14 · a result citing a verdict that does not exist ────────────────────────────────────────

func TestAResultCitingANonExistentVerdictIsRefusedWithoutDetection(t *testing.T) {
	fixture := loadModelOutput(t, "model-output-result.json")
	if !fixture.Result.VerifiedClaim || fixture.Result.VerdictRef == "" {
		t.Fatal("the fixture does not claim verification with a well-formed verdict reference; this " +
			"fence would pass over the wrong thing")
	}
	verdicts := resolves("cfg_the_platform_actually_verified@rev_real")
	em := newEmitter(&recorder{}, Resolvers{
		Delivery: resolves("del_real"), Verdict: verdicts,
	})
	if _, err := em.Emit(context.Background(), Message{Kind: KindPlan, Plan: goodPlan()}); err != nil {
		t.Fatalf("plan: %v", err)
	}
	// The delivery reference is invented too, so this is refused twice over. The assertion below is
	// specifically about the VERDICT, so the delivery is repaired first — otherwise the test would pass
	// for the wrong reason, which is the shape a fence takes when nobody is watching.
	result := *fixture.Result
	result.DeliveryRef = "del_real"
	result.Reconciliation = []ReconciliationEntry{{StepID: "s1", State: StepDone}}
	_, err := em.Emit(context.Background(), Message{Kind: KindResult, Result: &result})
	if err == nil {
		t.Fatal("a result claiming verification against a verdict nobody recorded was accepted. " +
			"*Diagnosis proposes, verification decides* is a message rule here, and it did not hold.")
	}
	if verdicts.calls == 0 {
		t.Error("the verification ledger was never consulted")
	}

	// And the same message with a verdict that DOES resolve goes through — which is what makes the
	// refusal above a check rather than a blanket refusal of verified results.
	result.VerdictRef = "cfg_the_platform_actually_verified@rev_real"
	if _, err := em.Emit(context.Background(), Message{Kind: KindResult, Result: &result}); err != nil {
		t.Fatalf("a result citing a real verdict was refused: %v", err)
	}
}

// ── §6.4 · a repository fixture containing an injection ──────────────────────────────────────────

// TestAnInjectionInRepositoryContentRaisesAFindingAndTakesNoAction is NFR-S5.
//
// Two halves, and the second is the one people forget: the attempt is REPORTED (silently ignoring it
// wastes the one signal that something is wrong) and NOTHING IS DONE about it — no approval, no
// proposal, no egress.
func TestAnInjectionInRepositoryContentRaisesAFindingAndTakesNoAction(t *testing.T) {
	corpus, err := AdversarialCorpus()
	if err != nil {
		t.Fatal(err)
	}
	var readme AdversarialFixture
	for _, f := range corpus {
		if f.Name == "README.md" {
			readme = f
		}
	}
	if !strings.Contains(strings.ToLower(readme.Content), "approve all") {
		t.Fatal("the README fixture no longer carries the forged-approval attempt")
	}

	attempts := Detect(readme.Name, readme.Content)
	if len(attempts) == 0 {
		t.Fatal("nothing was detected in a README that instructs an agent to approve everything")
	}
	findings := FindingsFor(attempts, "/app/workflows/wf_1")
	if len(findings) == 0 {
		t.Fatal("the attempt was detected and produced no finding — which is 'silently ignoring it' " +
			"with an extra step")
	}

	sink := &recorder{}
	em := newEmitter(sink, Resolvers{})
	for i := range findings {
		f := findings[i]
		if _, err := em.Emit(context.Background(), Message{Kind: KindFinding, Finding: &f}); err != nil {
			t.Fatalf("the emitter refused an injection finding: %v", err)
		}
	}

	// 🔴 THE SECOND HALF. Every message that reached the sink is a `finding`. Nothing effect-bearing was
	// produced, and the run is expected to continue — an injection is a fact ABOUT the repository, not
	// an event that stops the work.
	for _, m := range sink.msgs {
		if m.Kind != KindFinding {
			t.Errorf("processing repository content produced a %s. Content that instructs an agent must "+
				"produce a claim about the text and nothing else.", m.Kind)
		}
		if EffectBearing(m.Kind) {
			t.Errorf("an effect-bearing %s was produced from repository content", m.Kind)
		}
	}
	if len(sink.msgs) != len(findings) {
		t.Errorf("%d messages for %d findings", len(sink.msgs), len(findings))
	}
}

// TestNoUrlFromRepositoryContentIsEverAddressed is NFR-S4's shape, asserted where it can be.
//
// 🔴 What this CAN prove and what it cannot, stated so nobody reads it for more than it is worth: the
// conversation package makes no outbound request at all — it has no HTTP client, no dialer and no way
// to acquire one — so a URL in repository content cannot be followed FROM HERE. Egress belongs to the
// gateway's allowlist and is fenced there. What this asserts is that the detector treats such a URL as
// a REPORTABLE FACT and never as a destination, which is the half that lives in this package.
func TestNoUrlFromRepositoryContentIsEverAddressed(t *testing.T) {
	corpus, err := AdversarialCorpus()
	if err != nil {
		t.Fatal(err)
	}
	egress := 0
	for _, f := range corpus {
		for _, a := range Detect(f.Name, f.Content) {
			if a.Class == AttemptEgress {
				egress++
			}
		}
	}
	if egress == 0 {
		t.Fatal("no egress attempt was detected across the corpus, which carries a metadata-service " +
			"URL and two attacker endpoints")
	}
	// The detector's output is a REPORT. It carries a class, a file and a line — and deliberately no
	// field an egress call could be built from.
	findings := FindingsFor(Detect("config.yaml", "webhook: http://169.254.169.254/latest/meta-data/"), "/app/x")
	for _, f := range findings {
		if strings.Contains(f.Claim, "169.254.169.254") {
			t.Errorf("a finding's CLAIM reproduces the attacker's URL: %q.\nThe excerpt belongs in the "+
				"evidence a person opens, not in a sentence that gets copied into a chat or a ticket.", f.Claim)
		}
	}
}
