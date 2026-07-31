package distribution

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// claims.go is the honesty gate (P20 task 4.3): a document may state a trust property only if this release
// delivered it.
//
// # Why a scanner and not a review checklist
//
// Because the failure is invisible to review. Release notes are written by copying the previous release's, and
// the previous release's described the previous release's posture — so the first release after a posture change
// ships last release's promises, and nobody notices because the sentence has been true for months. The reader
// who finds out is the one whose Gatekeeper blocks a binary the notes called notarized.
//
// So the claim vocabulary is INVENTORIED. Each claim has a positive pattern, and a document containing one
// fails the release unless the matching Claim is Earned in the attestation.
//
// # Why only positive patterns, and why negation is a guard rather than a pattern
//
// A naïve scan for the word "notarized" would flag the sentence that DISCLOSES the absence — "the macOS
// binaries are NOT notarized" — and the fastest way to make the gate green would be to delete the disclosure.
// A gate whose easiest fix is removing the honest sentence is worse than no gate. So the patterns match
// affirmative constructions, and any match whose surrounding sentence is negated is not a claim.

// ClaimViolation is one place a document claims something the release did not deliver.
type ClaimViolation struct {
	// Doc is the file the text came from.
	Doc string
	// ClaimID is which inventoried claim was made.
	ClaimID string
	// Line is 1-indexed.
	Line int
	// Text is the offending sentence.
	Text string
}

func (c ClaimViolation) Error() string {
	return fmt.Sprintf("%s:%d claims %q, which this release did not deliver: %q",
		c.Doc, c.Line, c.ClaimID, c.Text)
}

// claimPattern pairs an inventoried claim with the affirmative constructions that assert it.
type claimPattern struct {
	claimID  string
	patterns []*regexp.Regexp
}

// claimVocabulary is the inventory. Adding a trust property to the product means adding it here, which is the
// point: a claim nobody inventoried is a claim nobody can check.
var claimVocabulary = []claimPattern{
	{
		claimID: "macos-notarized",
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\b(is|are|been|fully)\s+notarized\b`),
			regexp.MustCompile(`(?i)\bnotarized\s+(by\s+apple|binaries|build|release)\b`),
			regexp.MustCompile(`(?i)gatekeeper[-\s]clean`),
			regexp.MustCompile(`(?i)\bno\s+gatekeeper\s+(warning|prompt)`),
		},
	},
	{
		claimID: "macos-signed",
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bdeveloper[-\s]id\s+(signed|signature|code[-\s]signed)`),
			regexp.MustCompile(`(?i)\b(is|are)\s+apple[-\s]signed\b`),
		},
	},
	{
		claimID: "windows-signed",
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bauthenticode[-\s]signed\b`),
			regexp.MustCompile(`(?i)smartscreen[-\s]clean`),
			regexp.MustCompile(`(?i)\bno\s+smartscreen\s+(warning|prompt)`),
		},
	},
	{
		claimID: "signed-manifest",
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)\bmanifest\s+is\s+signed\b`),
			regexp.MustCompile(`(?i)\bsigned\s+(release\s+)?manifest\b`),
		},
	},
}

// negation matches the constructions a disclosure uses. A match inside one of these is a statement of absence,
// not a claim — and the disclosure has to be allowed, or the cheapest way to green the gate is to delete it.
// 🔴 The word list is deliberately NARROW. "without" was here first and laundered a real claim: the sentence
// "the macOS binaries are notarized, so Gatekeeper opens them without a warning" contains it, and the gate read
// the whole line as a disclosure. A negation word that appears in the middle of a boast is not a negation.
var negation = regexp.MustCompile(`(?i)\b(not|never|no|no longer|unsigned|un-notarized|refuses to|refuse to|cannot)\b`)

// AuditClaims reports every place doc's text states a trust property the attestation did not earn.
//
// Each line is examined independently. That is deliberate: a paragraph whose first sentence discloses the gap
// and whose fourth sentence claims the property is a document that misleads a skimmer, and line-scoping is what
// makes the gate notice.
func AuditClaims(docName, text string, a Attestation) []ClaimViolation {
	var out []ClaimViolation
	for i, line := range strings.Split(text, "\n") {
		for _, cv := range claimVocabulary {
			if a.Earned(cv.claimID) {
				continue // the release delivered it; saying so is not a violation, it is the point
			}
			for _, p := range cv.patterns {
				loc := p.FindStringIndex(line)
				if loc == nil {
					continue
				}
				// Is this sentence a disclosure? Look at the clause the match sits in, not the whole line: a
				// line that discloses one property and claims another must still fail on the second.
				if negation.MatchString(clauseAround(line, loc[0])) {
					continue
				}
				out = append(out, ClaimViolation{
					Doc: docName, ClaimID: cv.claimID, Line: i + 1, Text: strings.TrimSpace(line),
				})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].ClaimID < out[j].ClaimID
	})
	return out
}

// clauseAround returns the clause containing position pos, bounded by clause punctuation.
//
// Commas and colons are boundaries, not just sentence-enders. Without them, "the build is not signed, the macOS
// binaries are notarized" reads as one disclosure and the second half goes unchallenged — which is precisely the
// construction a maintainer writes when a posture changes for one OS and not the other.
func clauseAround(line string, pos int) string {
	const bounds = ".;!?,:"
	start, end := 0, len(line)
	for i := pos; i > 0; i-- {
		if strings.ContainsRune(bounds, rune(line[i-1])) {
			start = i
			break
		}
	}
	for i := pos; i < len(line); i++ {
		if strings.ContainsRune(bounds, rune(line[i])) {
			end = i
			break
		}
	}
	return line[start:end]
}

// GateClaims turns claim violations into release-gate failures, so a document that overstates the trust posture
// stops a release through the same path as an incomplete matrix.
//
// This is what makes task 4.3 a gate rather than a guideline: the release notes are generated from the
// attestation, and the README is then checked against it — so neither the generated document nor the
// hand-written one can carry a promise the artifacts do not keep.
func GateClaims(violations []ClaimViolation) []GateFailure {
	var fails []GateFailure
	for _, v := range violations {
		fails = append(fails, GateFailure{"honest-claims", v.Error()})
	}
	return fails
}

// ClaimIDs is the inventoried trust vocabulary, as a value.
//
// P23's install fence reads it: a documentation page may use one of these claims only when the release
// earned it. Exported here rather than duplicated in the console's scripts, because a second copy of the
// vocabulary is a second answer to "may we say notarized", and the copy is always the optimistic one.
func ClaimIDs() []string {
	out := make([]string, 0, len(claimVocabulary))
	for _, c := range claimVocabulary {
		out = append(out, c.claimID)
	}
	sort.Strings(out)
	return out
}
