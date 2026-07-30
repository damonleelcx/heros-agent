package release

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// verify.go is THE verification routine (P20 task 5.5): checksum, then ed25519 signature, against the
// compiled-in trust root.
//
// # Why there is exactly one of these
//
// Before this, verification existed twice: as prose in a runbook a customer follows by hand, and as steps in
// an installer script. Two implementations of a security check drift in the direction that makes the check
// weaker — one of them grows a `|| true`, or forgets that the manifest must be checked before the file it
// describes — and the drift is invisible because each side passes its own tests.
//
// So `heros verify-release` and `heros upgrade` both call this function, and `scripts/install.sh` calls
// `heros verify-release` when a previously-installed heros is available. The shell path uses ssh-keygen for a
// fresh install because it cannot call Go — but it verifies the same two properties in the same order, and
// TestVerifyOrderMatchesTheInstaller holds the sequence together.
//
// # Why checksum first, then signature
//
// Both must pass before anything is used, so for safety alone the order does not matter. It matters for
// DIAGNOSIS. A user whose download was truncated and a user who was served a forged release need different
// next actions — retry, versus stop and report — and checking the cheap local property first means the common
// case (a bad download) reports as a bad download rather than as a signature failure, which reads like an
// attack and escalates accordingly.
//
// The order is also identical to the installer's, so a user comparing the two sees the same message from both.

// Bundle is what a verifier was given: the manifest, its detached signature, and the assets to check.
type Bundle struct {
	// Manifest is the exact SHA256SUMS bytes as downloaded. Not a parsed form: the signature covers the
	// bytes, so re-serialising a parsed manifest would verify a document nobody published.
	Manifest []byte
	// SignatureHex is the raw ed25519 signature over Manifest, hex-encoded (SHA256SUMS.sig).
	SignatureHex string
	// Assets maps asset name to its bytes. Only the assets a caller actually intends to use need be
	// present — an installer checks the one binary it downloaded, not all five.
	Assets map[string][]byte
}

// Outcome is what a successful verification established. It is returned rather than reduced to a bool because
// each field answers a question a user asks next: which key signed this (during a rotation), and what exactly
// was checked (so "verified" cannot quietly mean "verified nothing").
type Outcome struct {
	// SigningKeyID is the trust-root key that verified the manifest.
	SigningKeyID string
	// Checked is the sorted asset names whose checksums matched.
	Checked []string
	// ManifestEntries is how many assets the manifest covers, checked or not. A caller can see it verified
	// one of five, which is correct for an installer and would be wrong for a mirror.
	ManifestEntries int
}

// ErrNothingChecked is returned when a bundle carries no assets to verify.
//
// It exists because the dangerous shape of this function is a silent success: a verifier handed an empty asset
// map would confirm the signature, report no failures, and a caller would read that as "the binary is good".
var ErrNothingChecked = errors.New("release: nothing to verify — a bundle with no assets would report success " +
	"about a file nobody checked")

// VerifyBundle checks every asset's checksum against the manifest, then the manifest's signature against the
// published trust root. It fails closed: any problem is an error and no partial success is reported.
func VerifyBundle(b Bundle) (Outcome, error) {
	if len(b.Manifest) == 0 {
		return Outcome{}, errors.New("release: no checksum manifest — checksums are the only thing that ties " +
			"the signature to the bytes you downloaded")
	}
	entries := parseSums(string(b.Manifest))
	if len(entries) == 0 {
		return Outcome{}, errors.New("release: the checksum manifest lists no assets — it may be an error page " +
			"rather than a manifest")
	}
	if len(b.Assets) == 0 {
		return Outcome{}, ErrNothingChecked
	}

	// 1. Checksums. The cheap, local, no-key property first: a truncated download reports as a truncated
	// download rather than as a signature failure that reads like an attack.
	var checked []string
	for name, data := range b.Assets {
		want, ok := entries[name]
		if !ok {
			return Outcome{}, fmt.Errorf("release: %s is not listed in the checksum manifest — the release is "+
				"incomplete for this platform, which is a different problem from a failed download", name)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if got != want {
			return Outcome{}, fmt.Errorf("release: CHECKSUM MISMATCH for %s — manifest says %s, these bytes "+
				"hash to %s. Do not run this file; retry once in case of a truncated download, and if it "+
				"repeats, report it", name, want, got)
		}
		checked = append(checked, name)
	}
	sort.Strings(checked)

	// 2. The signature over the manifest. Without this the checksums prove only that the download matches a
	// document that arrived alongside it — which an attacker who served both would satisfy perfectly.
	keyID, err := VerifyTrusted(b.Manifest, b.SignatureHex)
	if err != nil {
		return Outcome{}, fmt.Errorf("%w — the checksums matched, so the download is intact, but the manifest "+
			"is not signed by a published heros release key. Either it was replaced in transit or the release "+
			"key was rotated and this binary predates the rotation", err)
	}

	return Outcome{SigningKeyID: keyID, Checked: checked, ManifestEntries: len(entries)}, nil
}

// parseSums reads "sha256  name" lines. Kept here rather than shared with the merge path because this one
// reads a document produced by SOMEBODY ELSE — possibly an attacker — so it is deliberately strict about the
// hex length and silent about lines it does not understand, rather than tolerant in a way an attacker could
// aim at.
func parseSums(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 || len(fields[0]) != 64 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			continue
		}
		out[fields[1]] = strings.ToLower(fields[0])
	}
	return out
}

// ParseSums exposes the strict "sha256  name" reader to a caller that must decide WHICH assets to check —
// `heros verify-release` checks every asset in the manifest that is present on disk, which it can only do by
// reading the manifest first. Exported from here rather than reimplemented so the two never disagree about
// what counts as a valid line.
func ParseSums(text string) map[string]string { return parseSums(text) }

// Describe renders a verification outcome for a human. Used by `heros verify-release` and by `heros upgrade`,
// so both report the same facts in the same words.
func (o Outcome) Describe() string {
	return fmt.Sprintf("verified %d of %d listed artifacts (%s) — manifest signed by release key %s",
		len(o.Checked), o.ManifestEntries, strings.Join(o.Checked, ", "), o.SigningKeyID)
}
