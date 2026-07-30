package distribution

import (
	"fmt"
	"sort"
	"strings"
)

// trust.go is the OS-trust posture (P20 D3, PRD OQ1) split into the two things it actually is: the
// DECISION, which is ratified once and is a spend commitment, and the DELIVERY, which is a per-release
// fact the pipeline reports.
//
// # Why those are separate types
//
// Because conflating them is the exact failure task 4.3 forbids. "We decided to notarize" is a statement
// about a budget; "this download is notarized" is a statement about the bytes in the user's hand. If one
// constant carried both, then the moment the decision was ratified every README, installer banner, and
// release note would begin claiming a property no artifact had yet — and the user would find out that the
// claim was aspirational when Gatekeeper refused to open the binary.
//
// So: Posture is the decision. Attestation is what a specific release delivered. Every user-visible claim
// is rendered from the Attestation and never from the Posture. `Claims()` cannot say "notarized" unless
// the notarization actually ran, and `FirstRunNotice()` keeps telling the user the quarantine-clearing
// command for exactly as long as the release still needs it.
//
// This is also what makes D3-A implementable today: the decision is funded and ratified, the pipeline
// carries the signing steps, and until the signing secrets exist the delivered posture is honestly the
// unsigned one — with no prose to go back and correct on the day the certificates land.

// Posture is the ratified OS-trust decision.
type Posture string

const (
	// PostureSignNotarize is D3 option (A): Apple Developer ID signature + notarization on macOS and
	// Authenticode on Windows. Costs recurring money and an organizational identity.
	PostureSignNotarize Posture = "sign-notarize"
	// PostureDocumentedClear is D3 option (B): ship unsigned and surface the one-command clear.
	PostureDocumentedClear Posture = "documented-clear"
)

// ChosenPosture is the ratified decision: D3 option (A), sign + notarize on BOTH macOS and Windows.
//
// Escalated to and decided by the product owner on 2026-07-29 (PRD OQ1 / task 1.2) — not self-decided,
// because it commits recurring spend and an organizational identity, and the rulebook puts a
// cost-escalation path above an implementer's convenience.
//
// What this constant does and does not do: it obliges the PIPELINE to carry the signing and notarization
// steps and obliges the release to FAIL rather than silently ship unsigned on a GA channel once the
// signing identity is configured. It does not, by itself, make any artifact signed — see Attestation.
const ChosenPosture = PostureSignNotarize

// OSTrust is what a release actually delivered on one OS. Every field is a fact about the artifacts, set
// by the pipeline from the outcome of a step that ran — never from the posture above.
type OSTrust struct {
	// GOOS the row describes.
	GOOS string
	// CodeSigned is true when the binary carries a valid OS code signature (Developer ID on macOS,
	// Authenticode on Windows) applied by the release job.
	CodeSigned bool
	// Notarized is true when Apple's notary service ACCEPTED the artifact. Meaningless off macOS and
	// asserted false there by TestAttestationRejectsImpossibleClaims.
	Notarized bool
	// Stapled is true when the notarization ticket is attached to the artifact itself.
	//
	// 🔴 Separate from Notarized, and the separation is not pedantry. `stapler` can only attach a ticket to
	// a .app, .dmg or .pkg — a BARE executable, which is what this release ships, cannot carry one. So a
	// notarized heros binary is notarized-but-not-stapled, and Gatekeeper resolves it with an ONLINE check
	// on first run. That difference is exactly what a user on a disconnected machine experiences, so
	// claiming "the ticket is stapled" when it is not would be a claim that fails in the one situation
	// where it matters.
	Stapled bool
	// Publisher is the identity the signature names, or the publisher metadata declared on the package
	// when unsigned. Declared either way (task 4.2): a user who sees "unknown publisher" learns
	// nothing, and a user who sees the org name can at least compare it with the docs.
	Publisher string
	// Identity is the signing identity's stable fingerprint (Apple Team ID, Authenticode thumbprint),
	// carried so a user can check that two releases were signed by the same org rather than trusting a
	// display name that anyone can put in a certificate.
	Identity string
}

// Attestation is the machine-readable trust posture of one release: the version, the assets, and what was
// delivered per OS. The pipeline writes it as trust.json alongside SHA256SUMS; the README generator, the
// installer banner, the console surface and the release notes all render from it.
//
// It deliberately has no "posture" field. A reader asking "did you sign this?" is answered by the facts,
// and a reader asking "do you intend to sign?" is answered by the design document.
type Attestation struct {
	Version string  `json:"version"`
	MacOS   OSTrust `json:"macos"`
	Windows OSTrust `json:"windows"`
	// SignedManifest is true when SHA256SUMS carries an ed25519 signature from the release trust root.
	// This is the verification FLOOR (D2) and is a different property from OS code signing: it is what
	// every channel checks, offline, with no account, and it is never optional on a non-dev channel.
	SignedManifest bool `json:"signed_manifest"`
	// SigningKeyID is which trust-root key signed the manifest, so a release inside a key rotation's
	// overlap window can say so instead of leaving a user to guess.
	SigningKeyID string `json:"signing_key_id,omitempty"`
	// Assets is the sorted asset list the manifest covers, so the completeness gate has a record.
	Assets []string `json:"assets"`
}

// Claim is one user-visible trust statement, plus whether this release earned it.
//
// Earned claims and unearned ones are the same type on purpose. A renderer that received only the earned
// ones could not disclose a gap, and an undisclosed gap is how a user ends up discovering Gatekeeper by
// being blocked by it.
type Claim struct {
	// ID is the stable identifier a doc gate matches on, never the prose.
	ID string
	// Text is the sentence, phrased so it is true. An unearned claim's text states the ABSENCE ("not
	// notarized") rather than negating a positive sentence, because a reader skimming for "notarized"
	// must not find it next to a word like "not" that a screenshot can crop off.
	Text string
	// Earned is whether the release delivered the property.
	Earned bool
}

// Claims renders every trust statement for this release, earned and not.
//
// 🔴 The honesty gate (task 4.3) is this function plus AuditClaims: a README or release note may contain
// a claim vocabulary word only if the Claim with that ID is Earned here. There is no path by which a
// hand-written "notarized" reaches a user, because the words themselves are inventoried.
func (a Attestation) Claims() []Claim {
	return []Claim{
		{
			ID:     "signed-manifest",
			Earned: a.SignedManifest,
			Text: pick(a.SignedManifest,
				"The checksum manifest is signed with the heros release key (ed25519). Verify it offline, with no account.",
				"This build's checksum manifest is NOT signed. Checksums prove the download is intact, not who produced it — a GA channel refuses to publish in this state."),
		},
		{
			ID:     "macos-signed",
			Earned: a.MacOS.CodeSigned,
			Text: pick(a.MacOS.CodeSigned,
				"The macOS binaries are Developer ID code-signed ("+a.MacOS.Publisher+").",
				"The macOS binaries carry no Apple code signature."),
		},
		{
			ID:     "macos-notarized",
			Earned: a.MacOS.Notarized,
			Text: pick(a.MacOS.Notarized,
				pick(a.MacOS.Stapled,
					"The macOS binaries are notarized by Apple and the ticket is stapled — Gatekeeper opens them without a warning, online or offline.",
					"The macOS binaries are notarized by Apple. The ticket is NOT stapled, because a bare executable cannot carry one — Gatekeeper confirms the notarization with an online check the first time you run it, so the first run needs a network."),
				"The macOS binaries are NOT notarized. macOS quarantines internet downloads: clear the flag with the one command the installer prints, or install with Homebrew, which is not quarantined."),
		},
		{
			ID:     "windows-signed",
			Earned: a.Windows.CodeSigned,
			Text: pick(a.Windows.CodeSigned,
				"The Windows binary is Authenticode-signed ("+a.Windows.Publisher+"), which is also what carries the publisher identity on the .exe itself.",
				"The Windows binary is NOT Authenticode-signed. SmartScreen warns on first run: More info → Run anyway. "+
					"Publisher metadata is declared where a package can carry it — the winget manifest and the .deb/.rpm "+
					"metadata name "+PackagePublisher+" — but the bare .exe carries none of its own, because on Windows the "+
					"Authenticode signature IS the publisher declaration. Its file properties will show no publisher."),
		},
	}
}

// pick is a two-branch selector kept as a helper so each Claim above reads as one row of a table.
func pick(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

// Earned reports whether the claim with this ID is delivered by the release. Unknown IDs are false: a
// claim nobody defined is not a claim anybody earned.
func (a Attestation) Earned(claimID string) bool {
	for _, c := range a.Claims() {
		if c.ID == claimID {
			return c.Earned
		}
	}
	return false
}

// FirstRunNotice is what the installer prints, and the README documents, about the first-run OS warning
// for a given GOOS — computed from what the release delivered.
//
// It returns "" when the release earned its way out of the warning. That empty string is the payoff of
// D3-A: the day notarization actually ships, this sentence disappears from the installer output and from
// the docs at once, with no prose to remember to delete.
//
// installPath is the destination the caller is about to write, so the command it prints is the one the
// user can paste — not a placeholder they have to adapt, which is where a documented workaround usually
// fails.
func (a Attestation) FirstRunNotice(goos, installPath string) string {
	switch goos {
	case "darwin":
		if a.MacOS.Notarized {
			return ""
		}
		return "macOS quarantines files downloaded from the internet, and this build is not notarized. " +
			"Clear the flag with exactly:\n    xattr -d com.apple.quarantine " + installPath + "\n" +
			"Or install with Homebrew (brew install heros-foreal/tap/heros), which is not quarantined."
	case "windows":
		if a.Windows.CodeSigned {
			return ""
		}
		return "Windows SmartScreen warns about programs it has not seen signed. This build is not " +
			"Authenticode-signed: choose More info → Run anyway.\n" +
			"    Its file properties will show no publisher — on Windows the Authenticode signature is what " +
			"carries that, so an unsigned .exe has none. What does name the publisher is the winget manifest " +
			"and the .deb/.rpm metadata (" + PackagePublisher + "), and the checksum + release signature this " +
			"installer already verified."
	default:
		return ""
	}
}

// Verified is the one condition under which a release may be published to a non-dev channel: the
// verification floor holds. It is deliberately NOT "posture fully delivered" — OS code signing is a UX
// upgrade (D3), while the signed manifest is the security floor (D2), and collapsing the two would either
// block every release until the certificates land or let an unsigned manifest through.
func (a Attestation) Verified() bool { return a.SignedManifest && a.SigningKeyID != "" }

// Complete reports whether the attestation covers every shipped target, returning the missing asset names.
//
// This is the completeness half of the pipeline's fail-closed gate (task 2.5). A release that omits a
// target is more dangerous than one that fails outright, because the installer for the missing row 404s
// against a Release that otherwise looks healthy.
func (a Attestation) Complete() (bool, []string) {
	have := map[string]bool{}
	for _, n := range a.Assets {
		have[n] = true
	}
	var missing []string
	for _, want := range ExpectedAssets(a.Version) {
		if !have[want] {
			missing = append(missing, want)
		}
	}
	sort.Strings(missing)
	return len(missing) == 0, missing
}

// Describe renders the attestation as the block the release notes carry — every claim, earned first, each
// prefixed so the reader can see at a glance which are which.
//
// Sales lens: this is the artifact a customer's engineer reads before approving the tool, and the reason
// it is generated is that a hand-written version of it has one job (persuade) and this one has another
// (be checkable).
func (a Attestation) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Trust posture — heros %s\n\n", a.Version)
	for _, c := range a.Claims() {
		mark := "⛔"
		if c.Earned {
			mark = "✅"
		}
		fmt.Fprintf(&b, "%s %s\n", mark, c.Text)
	}
	if a.SigningKeyID != "" {
		fmt.Fprintf(&b, "\nManifest signed by release key %s.\n", a.SigningKeyID)
	}
	return b.String()
}
