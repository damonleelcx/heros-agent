package release

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// trustroot.go is the release trust root — the set of ed25519 public keys a `heros` binary will accept a
// release signature from — and the rotation story that set exists to make possible (P20 task 1.4, D3
// "Key management").
//
// # Why the trust root is Go source and not a fetched file
//
// The installer and `heros upgrade` verify a downloaded manifest. If the key they verified against were
// itself downloaded, the verification would prove nothing: an attacker who can serve you a binary can
// serve you a key. So the trust root is COMPILED IN — it ships inside the artifact whose provenance the
// user already decided to trust (the repo, the tap, the previous verified binary) — and the copy in
// docs/release/heros-release.pub exists only so a human can read it and run the documented offline
// verification by hand.
//
// The two copies are held identical by TestTrustRootMatchesPublishedKey. Two copies with a drift gate is
// the deliberate trade here: a `go:embed` of a file two directories up is not expressible, and moving the
// key into this package would break the documented `--pub "$(cat docs/release/heros-release.pub)"` step
// that customers' runbooks already contain.
//
// # Why a SET of keys rather than one
//
// Because rotation is a one-way door that must be planned before it is needed, not improvised during an
// incident. A single-key verifier makes rotation a flag day: the moment a new key signs, every installed
// binary rejects every new release, and the only repair path is "reinstall from an unverified download" —
// which is the exact hole the signature exists to close.
//
// With a key set, rotation is ADDITIVE and staged:
//
//	1. generate the next keypair; publish its public key here with RoleAccepted;
//	2. release once more with the OLD key — every binary in the field now trusts both;
//	3. flip the roles: the new key becomes RoleActive and signs; the old stays RoleAccepted;
//	4. after the overlap window (one minor version, stated in docs/release/install.md), delete the old
//	   entry — and only then does a binary older than step 2 stop verifying.
//
// A compromise skips the overlap: the leaked key is deleted in the same commit that adds its replacement,
// and the release notes say so. That is a deliberate break — loud, and narrower than the alternative.
//
// The PRIVATE key is never in this repository, in a log, or in an artifact. It is a CI secret consumed by
// `scripts/release-cli.sh` in `${VAR:?}` refuse-to-start form, and nothing outside the release workflow
// can read it.

// KeyRole says whether a key may SIGN a new release or only VERIFY an existing one.
//
// The distinction is the whole point of the set: exactly one key is active, so there is never a question
// of which key a release was signed with, while several may be accepted, so a binary in the field spans
// a rotation.
type KeyRole string

const (
	// RoleActive is the key the current release is signed with. Exactly one key holds this role.
	RoleActive KeyRole = "active"
	// RoleAccepted is a key whose signatures still verify but which no longer signs — the previous key
	// during a rotation's overlap window.
	RoleAccepted KeyRole = "accepted"
)

// TrustKey is one published release key.
type TrustKey struct {
	// ID is a stable human label used in release notes and rotation records ("heros-release-2026a").
	// It is never derived from the key material, because the point of an ID is to survive being talked
	// about in a changelog by someone who does not have the key in front of them.
	ID string
	// Hex is the 32-byte ed25519 public key, hex-encoded.
	Hex string
	// Role decides whether this key may sign (RoleActive) or only verify (RoleAccepted).
	Role KeyRole
	// Note records why the key is in this state — the audit trail a rotation is supposed to leave.
	Note string
}

// trustRoot is the published set. Order is significant only for display.
//
// 🔴 Adding, removing, or re-roling an entry here is a trust-root change: it is reviewed as a security
// change, recorded in docs/release/install.md's rotation section, and named in the release notes.
var trustRoot = []TrustKey{
	{
		ID:   "heros-release-2026b",
		Hex:  "4d5d06df5701268afbaddc7ec1961fb4d28007a64efcfaee9f5edc8f303cc967",
		Role: RoleActive,
		Note: "P20 release key, 2026-07-29; the first key with a configured CI secret, and therefore the " +
			"first that can sign a published release.",
	},
	// 🔴 The P11 launch key (heros-release-2026a, 1f117664…) was REMOVED rather than demoted to
	// RoleAccepted, and that is deliberate.
	//
	// The rotation procedure documented above exists to protect binaries in the field that were built
	// while an older key was active. There are none: `heros-release-2026a` never had a private half
	// configured as a CI secret (the repository had zero secrets when P20 landed) and the repository had
	// no `v*` tag, so no release was ever signed with it and no installed binary has ever verified
	// anything against it. Keeping it as an accepted key would widen the set of keys that can produce a
	// release this binary trusts, in exchange for compatibility with a release that does not exist —
	// trust surface for nothing.
	//
	// This is therefore a REPLACEMENT, not a rotation, and the distinction matters: the first real
	// rotation must follow the four staged steps above, because by then there will be binaries in the
	// field to protect.
}

// TrustRoot returns the published key set. The slice is a copy, so a caller cannot widen the trust root
// of a running process by mutating it.
func TrustRoot() []TrustKey {
	out := make([]TrustKey, len(trustRoot))
	copy(out, trustRoot)
	return out
}

// ActiveKey returns the one key releases are currently signed with.
//
// It returns an error rather than a zero value if the set is malformed, because "no active key" and "two
// active keys" are both states where a caller's next action must be to STOP, not to pick one.
func ActiveKey() (TrustKey, error) {
	var found TrustKey
	n := 0
	for _, k := range trustRoot {
		if k.Role == RoleActive {
			found = k
			n++
		}
	}
	switch n {
	case 1:
		return found, nil
	case 0:
		return TrustKey{}, errors.New("release: trust root has no active key — a release cannot be signed")
	default:
		return TrustKey{}, fmt.Errorf("release: trust root has %d active keys — which one signed a release would be ambiguous", n)
	}
}

// VerifyTrusted verifies a detached hex signature over manifest against the published trust root, and
// reports WHICH key verified it.
//
// The key id is returned rather than discarded because "verified" is not the whole answer during a
// rotation: an operator needs to know a release still being signed by the retiring key is why the notes
// say what they say. Callers that print it give the user a fact; callers that ignore it lose nothing.
//
// It fails closed: an empty trust root, an unparseable signature, and a signature that matches no key
// are all a refusal, never a pass with a warning.
func VerifyTrusted(manifest []byte, sigHex string) (keyID string, err error) {
	if len(trustRoot) == 0 {
		return "", errors.New("release: trust root is empty — refusing to treat an unverifiable release as verified")
	}
	sig, err := hex.DecodeString(strings.TrimSpace(sigHex))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return "", errors.New("release: invalid signature encoding")
	}
	for _, k := range trustRoot {
		raw, err := hex.DecodeString(strings.TrimSpace(k.Hex))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			// A malformed entry is a trust-root defect, not a verification outcome. Refuse rather than
			// silently verifying against the remaining keys: the operator has to fix the root.
			return "", fmt.Errorf("release: trust-root key %q is not a valid ed25519 public key", k.ID)
		}
		if ed25519.Verify(ed25519.PublicKey(raw), manifest, sig) {
			return k.ID, nil
		}
	}
	return "", errors.New("release: signature does NOT verify under any published release key — do not trust this release")
}

// publicKeyBytes decodes a trust-root key's hex into raw ed25519 bytes. Shared by the raw verifier and the
// sshsig renderer so both derive from the one stored form; a second decode is a second chance to be lenient
// about a malformed key.
func publicKeyBytes(hexKey string) (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("release: invalid public key")
	}
	return ed25519.PublicKey(raw), nil
}
