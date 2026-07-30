package distribution

import (
	"strings"
	"testing"
)

// trust_test.go is the honesty gate at the type level (tasks 1.2, 4.3). Every test here describes a way a
// release could tell a user something it had not delivered.

// TestChosenPostureIsTheRatifiedDecision pins the escalated answer. If someone later "simplifies" the
// posture back to (B), this fails and they have to go and get the decision changed by the person who owns
// the budget — which is the whole point of escalating it.
func TestChosenPostureIsTheRatifiedDecision(t *testing.T) {
	if ChosenPosture != PostureSignNotarize {
		t.Fatalf("chosen posture = %q; D3 was escalated (PRD OQ1) and answered (A) sign+notarize on "+
			"2026-07-29. Changing it requires the same escalation, not a code edit.", ChosenPosture)
	}
}

// TestUndeliveredPostureNeverClaimsItself is the central assertion of task 4.3, and the reason Posture and
// Attestation are separate types: with the decision ratified as (A), a release built before the signing
// identity exists must still describe itself as unsigned.
func TestUndeliveredPostureNeverClaimsItself(t *testing.T) {
	a := Attestation{Version: "0.20.0", SignedManifest: true, SigningKeyID: "heros-release-2026a"}
	desc := strings.ToLower(a.Describe())
	for _, forbidden := range []string{"is notarized", "developer id code-signed", "authenticode-signed ("} {
		if strings.Contains(desc, forbidden) {
			t.Errorf("a release with no OS signing claims %q:\n%s", forbidden, a.Describe())
		}
	}
	if a.Earned("macos-notarized") || a.Earned("windows-signed") || a.Earned("macos-signed") {
		t.Error("OS-trust claims are earned by an attestation that recorded no signing step")
	}
	if !a.Earned("signed-manifest") {
		t.Error("the verification floor is not earned even though the manifest was signed")
	}
}

// TestDeliveredPostureDropsTheWorkaround — the payoff of D3-A. On the day notarization ships, the
// quarantine instructions must disappear from the installer output and the docs automatically, because a
// stale workaround teaches users to run `xattr` on binaries that did not need it.
func TestDeliveredPostureDropsTheWorkaround(t *testing.T) {
	unsigned := Attestation{Version: "0.20.0", SignedManifest: true, SigningKeyID: "k"}
	if !strings.Contains(unsigned.FirstRunNotice("darwin", "/usr/local/bin/heros"), "xattr -d com.apple.quarantine /usr/local/bin/heros") {
		t.Error("unsigned macOS release does not print the pasteable quarantine-clearing command")
	}
	if !strings.Contains(unsigned.FirstRunNotice("windows", `C:\heros.exe`), "Run anyway") {
		t.Error("unsigned Windows release does not print the SmartScreen step")
	}
	signed := Attestation{
		Version: "0.21.0", SignedManifest: true, SigningKeyID: "k",
		MacOS:   OSTrust{GOOS: "darwin", CodeSigned: true, Notarized: true, Publisher: "Heros Foreal Ltd", Identity: "TEAMID1234"},
		Windows: OSTrust{GOOS: "windows", CodeSigned: true, Publisher: "Heros Foreal Ltd", Identity: "aa:bb:cc"},
	}
	if n := signed.FirstRunNotice("darwin", "/usr/local/bin/heros"); n != "" {
		t.Errorf("notarized release still tells the user to clear quarantine: %q", n)
	}
	if n := signed.FirstRunNotice("windows", "heros.exe"); n != "" {
		t.Errorf("Authenticode-signed release still tells the user about SmartScreen: %q", n)
	}
	if !signed.Earned("macos-notarized") || !signed.Earned("windows-signed") {
		t.Error("a release that signed and notarized does not earn its claims")
	}
}

// TestVerifiedIsTheFloorNotThePosture keeps the two gates apart. If Verified() required OS signing, every
// release would be blocked until the certificates arrived; if it required nothing, an unsigned manifest
// could reach a GA channel. Both are failures, in opposite directions.
func TestVerifiedIsTheFloorNotThePosture(t *testing.T) {
	unsignedManifest := Attestation{Version: "0.20.0"}
	if unsignedManifest.Verified() {
		t.Error("a release with an unsigned manifest reports itself verified")
	}
	floorOnly := Attestation{Version: "0.20.0", SignedManifest: true, SigningKeyID: "heros-release-2026a"}
	if !floorOnly.Verified() {
		t.Error("a signed manifest with no OS code signing is refused — that blocks every release until the certs land")
	}
	claimedButUnkeyed := Attestation{Version: "0.20.0", SignedManifest: true}
	if claimedButUnkeyed.Verified() {
		t.Error("a manifest claiming a signature with no key id passes — 'signed by nobody' is not signed")
	}
}

// TestCompleteFailsClosedOnAShortSet — a release missing one target is the dangerous case, because every
// other channel keeps working and only that platform's users see a 404.
func TestCompleteSetFailsClosedOnAShortSet(t *testing.T) {
	full := Attestation{Version: "0.20.0", Assets: ExpectedAssets("0.20.0")}
	if ok, missing := full.Complete(); !ok {
		t.Fatalf("a complete asset set reports missing rows: %v", missing)
	}
	short := Attestation{Version: "0.20.0", Assets: ExpectedAssets("0.20.0")[1:]}
	ok, missing := short.Complete()
	if ok || len(missing) != 1 {
		t.Fatalf("a short set passed the completeness gate (missing=%v)", missing)
	}
	// A release whose assets are named for a DIFFERENT version is also short — every row is missing.
	wrongVersion := Attestation{Version: "0.20.0", Assets: ExpectedAssets("0.19.0")}
	if ok, missing := wrongVersion.Complete(); ok || len(missing) != len(Shipped()) {
		t.Errorf("assets from another version satisfied this version's completeness gate (missing=%v)", missing)
	}
}

// TestAttestationRejectsImpossibleClaims guards the one nonsense state the shape allows: notarization is
// an Apple service, so a Windows row claiming it would be a copy-paste error that reads as a stronger
// guarantee than exists.
func TestAttestationRejectsImpossibleClaims(t *testing.T) {
	a := Attestation{Version: "0.20.0", Windows: OSTrust{GOOS: "windows", CodeSigned: true, Notarized: true}}
	// Notarization is claimed only through the macOS row; the Windows row's flag must reach no claim.
	for _, c := range a.Claims() {
		if c.ID == "macos-notarized" && c.Earned {
			t.Error("a Windows row's Notarized flag earned the macOS notarization claim")
		}
	}
}

// TestDescribeMarksUnearnedClaims — the release-notes block must show the ⛔ rows too. A block listing
// only what was delivered is a sales sheet; this one is evidence.
func TestDescribeMarksUnearnedClaims(t *testing.T) {
	a := Attestation{Version: "0.20.0", SignedManifest: true, SigningKeyID: "heros-release-2026a"}
	out := a.Describe()
	if !strings.Contains(out, "⛔") {
		t.Errorf("release-notes block discloses no gaps even though nothing was OS-signed:\n%s", out)
	}
	if !strings.Contains(out, "✅") {
		t.Errorf("release-notes block does not credit the signed manifest:\n%s", out)
	}
	if !strings.Contains(out, "heros-release-2026a") {
		t.Errorf("release-notes block does not name the signing key:\n%s", out)
	}
}
