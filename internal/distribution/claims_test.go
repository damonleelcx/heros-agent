package distribution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claims_test.go is the red-check for the honesty gate (task 4.3). The gate's whole value is that it can fail,
// and the specific way it must NOT fail is on the honest disclosure.

func unsignedRelease() Attestation {
	return Attestation{Version: "0.20.0", SignedManifest: true, SigningKeyID: "heros-release-2026b"}
}

func fullySignedRelease() Attestation {
	return Attestation{
		Version: "0.21.0", SignedManifest: true, SigningKeyID: "heros-release-2026b",
		MacOS:   OSTrust{GOOS: "darwin", CodeSigned: true, Notarized: true, Publisher: "Heros Foreal", Identity: "TEAM1234"},
		Windows: OSTrust{GOOS: "windows", CodeSigned: true, Publisher: "Heros Foreal", Identity: "aa:bb"},
	}
}

// TestClaimGateCatchesTheOverstatement — each line below is the kind of sentence a maintainer copies from the
// previous release's notes, and each must stop the release.
func TestClaimGateCatchesTheOverstatement(t *testing.T) {
	a := unsignedRelease()
	overstatements := map[string]string{
		"macos-notarized": "The macOS binaries are notarized, so Gatekeeper opens them without a warning.",
		"macos-signed":    "Every build is Developer ID signed by Heros Foreal.",
		"windows-signed":  "The Windows executable is Authenticode-signed.",
	}
	for claim, line := range overstatements {
		got := AuditClaims("NOTES.md", line, a)
		if len(got) == 0 {
			t.Errorf("the gate accepted an overstatement of %q: %q", claim, line)
			continue
		}
		if got[0].ClaimID != claim {
			t.Errorf("%q was attributed to claim %q, want %q", line, got[0].ClaimID, claim)
		}
	}
	// "Gatekeeper-clean" and "SmartScreen-clean" are the marketing shorthands, and the shorthand is exactly
	// what survives a copy-paste into a landing page.
	for _, line := range []string{"Gatekeeper-clean downloads.", "A SmartScreen-clean installer."} {
		if len(AuditClaims("web.md", line, a)) == 0 {
			t.Errorf("the gate accepted the shorthand claim %q", line)
		}
	}
}

// TestClaimGateAllowsTheHonestDisclosure is the more important half. If the gate flagged the disclosure, the
// cheapest way to make it green would be to DELETE the sentence that tells users the truth — and a gate whose
// easiest fix is removing the honest sentence is worse than no gate at all.
func TestClaimGateAllowsTheHonestDisclosure(t *testing.T) {
	a := unsignedRelease()
	honest := []string{
		"The macOS binaries are NOT notarized. macOS quarantines internet downloads: clear the flag with xattr.",
		"This build is not Authenticode-signed, so SmartScreen will warn on first run.",
		"The macOS binaries carry no Apple code signature.",
		"Until the signing identity exists, releases are unsigned by the OS and say so.",
		"A GA channel refuses to publish without a signed manifest, and this one is not notarized.",
	}
	for _, line := range honest {
		if v := AuditClaims("README.md", line, a); len(v) != 0 {
			t.Errorf("the gate flagged an honest disclosure: %q → %v", line, v[0].Error())
		}
	}
	// And the whole generated block must pass, since that is the text the pipeline actually publishes.
	if v := AuditClaims("notes", a.Describe(), a); len(v) != 0 {
		t.Errorf("the generated trust block fails its own honesty gate: %v", v)
	}
	if v := AuditClaims("notes", ReleaseNotes(a, []string{"ghcr.io/x:1"}), a); len(v) != 0 {
		t.Errorf("the generated release notes fail the honesty gate: %v", v)
	}
}

// TestClaimGatePermitsWhatWasDelivered — once the release earns a property, saying so must be allowed. A gate
// that flagged earned claims would make the signing work unpublishable.
func TestClaimGatePermitsWhatWasDelivered(t *testing.T) {
	a := fullySignedRelease()
	for _, line := range []string{
		"The macOS binaries are notarized by Apple.",
		"Developer ID signed and Gatekeeper-clean.",
		"The Windows executable is Authenticode-signed.",
	} {
		if v := AuditClaims("notes", line, a); len(v) != 0 {
			t.Errorf("the gate flagged a claim this release earned: %q → %v", line, v[0].Error())
		}
	}
	if v := AuditClaims("notes", a.Describe(), a); len(v) != 0 {
		t.Errorf("a fully-signed release's own trust block was flagged: %v", v)
	}
}

// TestClaimGateIsNotLaunderedByADistantNegation — a line that discloses one gap and claims another must fail on
// the claim. Scoping the negation check to the clause is what makes that work; a line-wide check would let any
// paragraph containing the word "not" say anything it liked.
func TestClaimGateIsNotLaunderedByADistantNegation(t *testing.T) {
	a := unsignedRelease()
	line := "The build is not Authenticode-signed; the macOS binaries are notarized by Apple."
	v := AuditClaims("notes", line, a)
	found := false
	for _, x := range v {
		if x.ClaimID == "macos-notarized" {
			found = true
		}
		if x.ClaimID == "windows-signed" {
			t.Errorf("the Windows disclosure in the same line was flagged as a claim: %s", x.Error())
		}
	}
	if !found {
		t.Errorf("a notarization claim laundered by a nearby negation was accepted: %q", line)
	}
}

// TestRepositoryDocsMakeNoUnearnedClaim runs the gate over the documents this repository actually ships, against
// the posture a release delivers TODAY: manifest signed, nothing OS-signed. This is the gate that protects the
// project; the tests above are what prove it can speak.
func TestRepositoryDocsMakeNoUnearnedClaim(t *testing.T) {
	a := unsignedRelease()
	for _, doc := range []string{
		filepath.Join("..", "..", "README.md"),
		filepath.Join("..", "..", "docs", "release", "cli-verification.md"),
		filepath.Join("..", "..", "docs", "release", "install.md"),
	} {
		b, err := os.ReadFile(doc)
		if err != nil {
			continue // install.md arrives with section 8; a missing doc makes no claims
		}
		for _, v := range AuditClaims(filepath.Base(doc), string(b), a) {
			t.Errorf("%s", v.Error())
		}
	}
}

// TestRatifiedPostureIsHonouredByThePipeline checks the pipeline against whichever posture is ratified — both
// branches, so the test stays meaningful across a reversal instead of silently skipping.
//
// Under (A) the obligation is that the signing steps EXIST: a ratified posture with no steps in the workflow is
// a decision nobody acted on, and the gap would surface only when someone finally bought the certificates.
//
// Under (B) — ratified 2026-07-30 — the obligation is the opposite one and easier to get wrong: nothing may
// CLAIM signing, and the workaround must be surfaced rather than merely documented. The steps are allowed to
// remain (gated and inert) so that funding signing later is a secrets change rather than a pipeline change; what
// is not allowed is a workflow that reads as though signing were coming when it is not.
func TestRatifiedPostureIsHonouredByThePipeline(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("release workflow not readable: %v", err)
	}
	wf := string(b)

	if ChosenPosture == PostureSignNotarize {
		for _, needle := range []struct{ text, why string }{
			{"codesign", "no macOS code-signing step (task 4.1)"},
			{"notarytool", "no Apple notarization step (task 4.1)"},
			{"signtool", "no Windows Authenticode step (task 4.2)"},
			{"--macos-signed", "the attestation is never told whether macOS signing ran"},
			{"--windows-signed", "the attestation is never told whether Windows signing ran"},
		} {
			if !strings.Contains(wf, needle.text) {
				t.Errorf("release.yml: %s (missing %q)", needle.why, needle.text)
			}
		}
		return
	}

	// ── (B) is ratified ────────────────────────────────────────────────────────────────────────────────
	//
	// Whatever signing machinery remains must be incapable of making a claim on its own. The attestation flags
	// are derived from marker files a step wrote, never hard-coded, so an inert step cannot mark a release as
	// signed.
	if strings.Contains(wf, "--macos-notarized") && !strings.Contains(wf, "grep -q '^notarized=true'") {
		t.Error("release.yml can pass --macos-notarized without a marker written by a step that ran — under " +
			"(B) nothing signs, so any path to that flag is a path to an unearned claim")
	}
	if strings.Contains(wf, "--windows-signed") && !strings.Contains(wf, `grep -q '^signed=true' "$win"`) {
		t.Error("release.yml can pass --windows-signed without a marker written by a step that ran")
	}
	// The notices must say signing is NOT PLANNED. Under (A) they said "not yet provisioned", which under (B) is
	// a promise nobody intends to keep — and a maintainer reading it would go looking for a budget.
	if strings.Contains(wf, "D3 option (A) is ratified") {
		t.Error("release.yml still tells a reader that option (A) is ratified; the posture was reversed to (B) " +
			"on 2026-07-30 and the notices must not describe signing as merely unprovisioned")
	}
	if !strings.Contains(wf, "not part of the ratified posture") {
		t.Error("release.yml's signing steps do not state that signing is outside the ratified posture — an " +
			"inert step with no explanation reads as one that is about to be switched on")
	}
	// And the release must still be publishable with no signing identity at all, which is the whole of (B).
	if !strings.Contains(wf, `[ -z "${APPLE_CERT_P12:-}" ]`) || !strings.Contains(wf, "if (-not $env:WINDOWS_CERT_PFX)") {
		t.Error("release.yml's signing steps are not tolerant of absent credentials — under (B) there will never " +
			"be any, so an intolerant step would block every release forever")
	}
}
