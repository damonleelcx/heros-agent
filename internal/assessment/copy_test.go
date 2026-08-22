package assessment

import (
	"context"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// copy_test.go is §8's half of the phase: the words.
//
// # 🔴 Why copy is fenced rather than reviewed
//
// §9.1 names the hardest sentence in the product: *"the hardest copy in the phase is `not_measured`. It
// must read as 'here is what is missing and what you could do about it', not as 'we failed'. That is
// the difference between a report someone acts on and one they discount."*
//
// A copy rule is exactly the kind that survives its first review and erodes in its third. Somebody adds
// an axis in a hurry and writes *"could not analyse this"* — which is grammatical, honest, and tells
// the reader nothing they can do. Nothing goes red, the report gets one row worse, and the erosion is
// invisible because each individual sentence is defensible.
//
// So the property is asserted over EVERY claim the extractors can produce, on real fixtures.

// everyClaim collects the claim of every finding every extractor produces across the fixtures.
func everyClaim(t *testing.T) map[Axis][]Finding {
	t.Helper()
	out := map[Axis][]Finding{}
	subjects := []Subject{
		subjectFor(t, "python"),
		subjectFor(t, "typescript"),
		subjectFor(t, "java"),
		subjectFor(t, "rust"),
		// A language this build has no frontend for, so the `refused / language` copy is exercised.
		subjectForLocal(t, "unsupported-language"),
		{WorkflowID: "wf-empty", IR: &discovery.IR{}},
		{WorkflowID: "wf-none"},
	}
	for _, s := range subjects {
		for _, e := range Extractors() {
			f, err := e.Extract(s)
			if err != nil {
				t.Fatalf("%s: %v", e.Axis(), err)
			}
			out[f.Axis()] = append(out[f.Axis()], f)
		}
	}
	// The two runtime-produced absences the extractors never emit: a budget refusal and an abstention.
	budget, err := NotMeasured(AxisTools, MissingBudgetExhausted,
		"this assessment reached its $1.00 cap before tools could be inferred; re-running with a "+
			"higher cap will answer it", EvidenceRef{Surface: SurfaceGraph, Locator: "wf"})
	if err != nil {
		t.Fatal(err)
	}
	out[AxisTools] = append(out[AxisTools], budget)

	inf, err := NewHerosInference(&scriptedAnalyst{
		answers:  map[string]Answer{},
		fallback: Answer{Abstained: true, AbstentionReason: "the tool list is assembled at runtime", ProviderModelVersion: "v"},
	}, DefaultConfidenceFloor)
	if err != nil {
		t.Fatal(err)
	}
	abstained, _, err := inf.Infer(context.Background(), AxisMemory, subjectFor(t, "python"))
	if err != nil {
		t.Fatal(err)
	}
	out[AxisMemory] = append(out[AxisMemory], abstained)

	// And the four eval reasons, which are the only absences a reader is meant to act on directly.
	for _, reason := range EvalMissingInputs() {
		f, err := NotMeasured(AxisPrompt, reason,
			Runnability{Reason: reason, Detail: "the specific thing"}.Claim(),
			EvidenceRef{Surface: SurfaceBoard, Locator: "wf"})
		if err != nil {
			t.Fatal(err)
		}
		out[AxisPrompt] = append(out[AxisPrompt], f)
	}
	return out
}

// TestAbsenceReadsAsATaskAndNotAsAFailure is §8.1.
func TestAbsenceReadsAsATaskAndNotAsAFailure(t *testing.T) {
	// 🚫 The vocabulary of blame and of shrugging. Each of these turns a finding into an apology, and
	// an apology is what a reader discounts.
	//
	// `failed`, `error` and `sorry` are the obvious ones. `unknown` and `unavailable` are the subtle
	// ones and they are the reason this list is not shorter: both are TRUE of every absence here and
	// both say nothing — "the memory strategy is unknown" is the shrug D1 exists to forbid, spelled
	// politely.
	banned := []string{
		"failed", "failure", "error", "sorry", "unfortunately", "unable to",
		"unknown", "unavailable", "n/a", "not applicable", "something went wrong",
	}
	for axis, findings := range everyClaim(t) {
		for _, f := range findings {
			lower := strings.ToLower(f.Claim())
			for _, word := range banned {
				if strings.Contains(lower, word) {
					t.Errorf("%s says %q, which contains %q.\n"+
						"An absence must read as \"here is what is missing and what you could do\", not as "+
						"\"we failed\". That is the difference between a report someone acts on and one "+
						"they discount.", axis, f.Claim(), word)
				}
			}
		}
	}
}

// TestEveryAbsenceNamesSomethingSpecific is the positive half, and it is the one that keeps the test
// above from being satisfiable by writing nothing.
//
// A claim that avoids every banned word by saying "this could not be determined" passes the blocklist
// and helps nobody. So every absence must name a NOUN a reader can act on — a frontend, a credential,
// a value, a cap, a snapshot, a call site.
func TestEveryAbsenceNamesSomethingSpecific(t *testing.T) {
	actionable := []string{
		"frontend", "credential", "sandbox", "runner", "cap", "snapshot", "call site", "call sites",
		"turns", "runtime", "statement", "entry point", "llm-eval", "value", "between turns", "loop",
		"language", "parser", "declared",
	}
	for axis, findings := range everyClaim(t) {
		for _, f := range findings {
			// 🔴 `not_measured` only. A REFUSAL is checked by the test below, and against a different
			// rule: the reader's next action on a refusal is not theirs at all, so demanding they be
			// told what to do would push the copy into inventing one.
			if f.State() != StateNotMeasured {
				continue
			}
			lower := strings.ToLower(f.Claim())
			named := false
			for _, noun := range actionable {
				if strings.Contains(lower, noun) {
					named = true
					break
				}
			}
			if !named {
				t.Errorf("%s says %q and names nothing a reader could act on.\n"+
					"A sentence that avoids every apologetic word by saying nothing passes the blocklist "+
					"and helps nobody. Name the frontend, the credential, the value, the cap — the noun "+
					"is what turns a shrug into a task.", axis, f.Claim())
			}
		}
	}
}

// TestEveryRefusalNamesWhichPartOfOursIsMissing is the refusal's own rule, and it is a different one.
//
// D1: *"Collapsing `refused` into `not_measured` conflates 'we could not' with 'this build cannot', and
// only the second is actionable by us rather than by the customer."* A refusal that does not say which
// part of OURS is missing hands the reader a dead end — they cannot tell "coming" from "never", and
// they cannot tell whether asking us would help.
func TestEveryRefusalNamesWhichPartOfOursIsMissing(t *testing.T) {
	ours := []string{"this build", "our ", "we ", "frontend", "analysis", "language support", "P34"}
	for axis, findings := range everyClaim(t) {
		for _, f := range findings {
			if f.State() != StateRefused {
				continue
			}
			if f.RefusalCause() == "" {
				t.Errorf("%s is refused and names none of the three causes", axis)
				continue
			}
			lower := strings.ToLower(f.Claim())
			named := false
			for _, phrase := range ours {
				if strings.Contains(lower, strings.ToLower(phrase)) {
					named = true
					break
				}
			}
			if !named {
				t.Errorf("%s refuses with %q and the SENTENCE does not say the limit is ours: %q.\n"+
					"A reader who cannot tell our gap from theirs goes and looks at their own code.",
					axis, f.RefusalCause(), f.Claim())
			}
		}
	}
}

// TestEveryClaimIsASentenceAboutWhatTheCodeDoes is PRD §14 A3's rule where it can erode: an observation
// describes the code; the judgement that something OUGHT to be different is a proposal and belongs to
// P35, where verification can decide it.
func TestEveryClaimIsASentenceAboutWhatTheCodeDoes(t *testing.T) {
	// The vocabulary of recommendation. Each of these turns a finding into advice we have not verified.
	banned := []string{"should ", "you must ", "we recommend", "consider ", "best practice", "instead you"}
	for axis, findings := range everyClaim(t) {
		for _, f := range findings {
			claim := f.Claim()
			lower := strings.ToLower(claim)
			for _, word := range banned {
				if strings.Contains(lower, word) {
					t.Errorf("%s says %q, which contains %q — that is a RECOMMENDATION. A finding "+
						"describes what the code does; the opinion that it ought to be different is a "+
						"proposal, and it belongs where verification can decide it.", axis, claim, word)
				}
			}
		}
	}
}

// TestTheNounDictionaryHolds is §8.4.
//
// Two halves, and the second is the one a phase drifts on: `workflow` keeps meaning **the target
// program's call graph**. This repository has a `workflow_id` on nine tables and a `/app/workflows`
// rail entry, and every one of them means the customer's program — not a CI pipeline, not a process,
// not a feature of ours.
func TestTheNounDictionaryHolds(t *testing.T) {
	// The seven shared axes are READ from variantspec, so a rename in one place cannot leave the other
	// behind. (`TestTheSevenSharedAxesAreExactlyTheSevenDimensions` asserts the set; this asserts the
	// SPELLING is not merely equal by coincidence of ordering.)
	for _, d := range variantspec.Dimensions() {
		if !Axis(d).Valid() {
			t.Errorf("`%s` is a configuration dimension and not an assessment axis — the console names "+
				"a surface one thing and the report names it another", d)
		}
	}

	// 🚫 The words this phase must not use for its own concepts.
	for axis, findings := range everyClaim(t) {
		for _, f := range findings {
			claim := f.Claim()
			lower := strings.ToLower(claim)
			for word, why := range map[string]string{
				"pipeline": "`workflow` means the target program's LLM call graph. A pipeline is a CI concept " +
					"and using it here makes a reader think we are describing their build",
				"violation": "a finding is not a violation — nothing here certifies against a standard",
				"issue":     "a finding is not an issue; it is a claim with evidence",
				"defect":    "the same",
			} {
				if strings.Contains(lower, word) {
					t.Errorf("%s says %q, which contains %q — %s", axis, claim, word, why)
				}
			}
		}
	}
}

// TestEveryRefusalCauseHasAProducerOrIsRecordedAsHavingNone is the "declared signal with no writer"
// check, applied to this phase's own vocabulary.
//
// # 🔴 Why a member with no producer is worth failing over
//
// `RefusalFrontend`, `RefusalAnalysis` and `RefusalLanguage` are three values a console switches on and
// a monitor groups by. A member nothing ever emits is a branch nobody has seen render, a dashboard row
// that is permanently zero, and — the part that matters — a claim in the spec (*"the refusal names
// which of the frontend, the analysis, or the language support is missing"*) that is true of two thirds
// of the vocabulary.
//
// This test does not demand three producers. It demands that a member without one is RECORDED as such,
// with the reason, so the gap is reviewable rather than discovered by somebody looking for the branch.
func TestEveryRefusalCauseHasAProducerOrIsRecordedAsHavingNone(t *testing.T) {
	produced := map[RefusalCause]bool{}
	for _, findings := range everyClaim(t) {
		for _, f := range findings {
			if f.State() == StateRefused {
				produced[f.RefusalCause()] = true
			}
		}
	}

	// 🔴 The one member nothing emits today, and WHY. `frontend` is the natural cause for "your
	// language's parser cannot give us this axis" — and design D6 deliberately routes that case to
	// `not_measured` with `frontend_emits_no_edges` instead, because a repository read by a syntactic
	// frontend may well HAVE topology: we cannot see it, which is a missing input rather than a
	// capability this build lacks. So the member stays in the vocabulary (the spec names three) and has
	// no producer until an axis exists that a frontend structurally cannot ever supply.
	//
	// It is listed here rather than deleted so that the day one arrives, this list is where somebody
	// removes the entry.
	withoutProducer := map[RefusalCause]string{
		RefusalFrontend: "design D6 routes a frontend limitation to not_measured with a named missing " +
			"input instead, because an axis our parser cannot read may still be present in the code",
		// 🔴 P34 is what emptied this one, and the emptying is the phase's whole point rather than a
		// regression. `analysis` had exactly two producers — the `loop` and `graph` extractors, refused
		// because their CONFIGURATION did not exist ("P33 may report on them only once P34 has landed,
		// or it names axes the configuration layer does not have"). P34 gave both a configuration, both
		// now report, and no other axis in this build is missing its analysis.
		//
		// It is kept in the closed vocabulary rather than removed, and that is a decision rather than an
		// oversight: `analysis` is the only honest cause for the NEXT axis that arrives ahead of the
		// configuration layer, and P33 built the two-function gate pattern (`extractGraph` wrapping
		// `extractGraphTopology`) specifically so that such an axis can ship its analysis behind a
		// refusal and lift it later. Deleting the cause would delete the landing site for that pattern.
		RefusalAnalysis: "P34 gave `loop` and `graph` their configuration, so this build's only two " +
			"producers of `analysis` now report instead of refusing. It is retained for the next axis " +
			"that ships its analysis ahead of its configuration layer — the pattern `extractGraph` " +
			"still demonstrates",
	}

	for _, cause := range RefusalCauses() {
		if produced[cause] {
			if _, listed := withoutProducer[cause]; listed {
				t.Errorf("%q is listed as having no producer and something produced it. Remove the entry "+
					"— a stale exemption is how a real gap hides behind a documented one", cause)
			}
			continue
		}
		if _, listed := withoutProducer[cause]; !listed {
			t.Errorf("nothing in this build ever emits refusal cause %q, and no reason is recorded.\n"+
				"A member of a closed vocabulary that nothing writes is a branch nobody has seen render "+
				"and a monitor row that is permanently zero. Either give it a producer or record why it "+
				"has none in `withoutProducer`.", cause)
		}
	}
}
