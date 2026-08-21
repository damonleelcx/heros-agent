package conversation

import (
	"context"
	"strings"
	"testing"
)

// TestTheAdversarialCorpusIsUnsanitized is the fence over the FIXTURES rather than over the code.
//
// 🔴 design.md D7 names this exact failure: "A test whose fixture was already sanitized by a helper
// proves nothing, and is the shape this fence will take if nobody is watching." So the corpus is checked
// for the strings that make it adversarial. A well-meaning cleanup that neutered the fixtures would make
// every §6 fence pass over harmless text, and nothing else would notice.
func TestTheAdversarialCorpusIsUnsanitized(t *testing.T) {
	corpus, err := AdversarialCorpus()
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, f := range corpus {
		all.WriteString(f.Content)
		all.WriteByte('\n')
	}
	joined := strings.ToLower(all.String())

	// Each of these is a DIFFERENT attack the corpus must still carry. Listed individually so a cleanup
	// that removed one is named rather than counted.
	required := map[string]string{
		"ignore all previous instructions": "the instruction-override attempt",
		"approve all":                      "the forged-approval attempt",
		"169.254.169.254":                  "the cloud metadata-service URL — the egress target that turns a followed link into a stolen instance credential",
		"| sh":                             "the shell pipeline",
		"rm -rf":                           "the destructive command",
		`"proposal_id"`:                    "model output shaped like a proposal",
		"prop_7f3a9c21b4e85d60":            "the WELL-FORMED, non-existent proposal id — the fixture §6.2 depends on",
		"del_5f4e3d2c1b0a9988":             "the well-formed, non-existent delivery record",
	}
	for needle, why := range required {
		if !strings.Contains(joined, strings.ToLower(needle)) {
			t.Errorf("the corpus no longer carries %q — %s.\nA sanitized corpus makes every fence over "+
				"it pass while testing nothing.", needle, why)
		}
	}
	if len(corpus) < 5 {
		t.Errorf("the corpus has %d fixtures; it had five, covering five distinct attacks", len(corpus))
	}
}

func TestDetectFindsEachAttackClass(t *testing.T) {
	corpus, err := AdversarialCorpus()
	if err != nil {
		t.Fatal(err)
	}
	found := map[AttemptClass]bool{}
	for _, f := range corpus {
		for _, a := range Detect(f.Name, f.Content) {
			found[a.Class] = true
			if a.Line < 1 {
				t.Errorf("%s: an attempt reports line %d; a finding a reader cannot open is a claim "+
					"they cannot check", f.Name, a.Line)
			}
		}
	}
	for _, class := range []AttemptClass{
		AttemptInstructionOverride, AttemptForgedApproval, AttemptEgress,
		AttemptCommand, AttemptForgedArtifact,
	} {
		if !found[class] {
			t.Errorf("no %s was detected anywhere in the corpus", class)
		}
	}
}

// TestAnExcerptIsBoundedAndCannotDriveATerminal guards the two ways a security REPORT becomes a security
// PROBLEM: an attacker-chosen paragraph in an operator's terminal, and an ANSI escape that rewrites the
// lines above it.
func TestAnExcerptIsBoundedAndCannotDriveATerminal(t *testing.T) {
	nasty := "https://evil.example.com/" + strings.Repeat("A", 4000) +
		"\x1b[2J\x1b[H\x1b[32mALL CHECKS PASSED\x1b[0m"
	attempts := Detect("x.md", nasty)
	if len(attempts) == 0 {
		t.Fatal("nothing was detected in a line containing a URL")
	}
	for _, a := range attempts {
		if len([]rune(a.Excerpt)) > excerptLimit+1 {
			t.Errorf("an excerpt is %d runes; the bound is %d", len([]rune(a.Excerpt)), excerptLimit)
		}
		if strings.ContainsRune(a.Excerpt, 0x1b) {
			t.Error("an excerpt carries an ANSI escape; a report that can repaint a terminal is a " +
				"place to hide a report")
		}
	}
}

// TestAFindingAboutAnInjectionCarriesItsLocation is NFR-S5's shape: the attempt is REPORTED, with an
// evidence reference a person opens, and the run continues.
func TestAFindingAboutAnInjectionCarriesItsLocation(t *testing.T) {
	corpus, err := AdversarialCorpus()
	if err != nil {
		t.Fatal(err)
	}
	var attempts []Attempt
	for _, f := range corpus {
		attempts = append(attempts, Detect(f.Name, f.Content)...)
	}
	findings := FindingsFor(attempts, "/app/workflows/wf_1")
	if len(findings) == 0 {
		t.Fatal("the corpus produced no findings")
	}
	// Every one must satisfy the SAME emitter the rest of the product uses. A finding about an
	// injection is not exempt from FR2 — if anything it is the one that most needs its reference.
	em := newEmitter(&recorder{}, Resolvers{})
	for i := range findings {
		f := findings[i]
		if !strings.Contains(f.EvidenceRef, "#L") {
			t.Errorf("a finding's evidence reference names no line: %q", f.EvidenceRef)
		}
		if _, err := em.Emit(context.Background(), Message{Kind: KindFinding, Finding: &f}); err != nil {
			t.Errorf("the emitter refused an injection finding: %v", err)
		}
	}
}

// TestDetectionIsNotTheDefence states in a test what the doc comment states in prose: turning detection
// off must change nothing about whether an effect can be produced.
//
// 🔴 This is the fence §6.3 and §6.14 expand on. It lives here too because the FIXTURE and the claim are
// here, and a reader of this file should not have to find another package to learn that the classifier
// is not what is holding the line.
func TestDetectionIsNotTheDefence(t *testing.T) {
	// Detection deliberately disabled: nothing calls Detect at all below.
	em := newEmitter(&recorder{}, Resolvers{Proposal: resolves("prop_that_exists")})
	_, err := em.Emit(context.Background(), Message{Kind: KindProposal, Proposal: &ProposalPayload{
		// The identifier straight out of the adversarial corpus.
		ProposalID: "prop_7f3a9c21b4e85d60", Axis: "harness", Node: "extract",
	}})
	if err == nil {
		t.Fatal("a proposal from the attack corpus was accepted with detection disabled. The structural " +
			"defence is the only one that does not depend on a classifier's recall, and it did not hold.")
	}
}
