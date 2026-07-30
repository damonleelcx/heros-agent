package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// trustroot_test.go proves the rotation story is real rather than documented (task 1.4). The rotation
// mechanism only pays off if a binary compiled during the overlap window verifies BOTH keys — so that is
// what is asserted, with actual signatures.

// TestTrustRootMatchesPublishedKey holds the two copies of the trust root identical. The compiled-in copy
// is what verifies; the docs copy is what a human pastes into the documented offline check. If they drift,
// a customer's runbook verifies against a key the binary would reject, and both sides look fine alone.
func TestTrustRootMatchesPublishedKey(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "release", "heros-release.pub"))
	if err != nil {
		t.Skipf("published key not present: %v", err)
	}
	active, err := ActiveKey()
	if err != nil {
		t.Fatalf("trust root has no single active key: %v", err)
	}
	if trim(string(b)) != active.Hex {
		t.Fatalf("docs/release/heros-release.pub does not match the compiled-in active key %q.\n"+
			"The documented verification step would verify against a key the binary rejects.", active.ID)
	}
}

// TestTrustRootIsWellFormed — every published key must be a usable ed25519 key with an id, and exactly one
// must be active. "Two active keys" is not a stricter state; it is an ambiguous one.
func TestTrustRootIsWellFormed(t *testing.T) {
	keys := TrustRoot()
	if len(keys) == 0 {
		t.Fatal("empty trust root — nothing could verify a release")
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if k.ID == "" {
			t.Errorf("key %s… has no id — a rotation cannot be discussed in release notes", k.Hex[:8])
		}
		if seen[k.ID] {
			t.Errorf("duplicate key id %q", k.ID)
		}
		seen[k.ID] = true
		if k.Note == "" {
			t.Errorf("key %q has no note — a trust-root entry with no recorded reason is an audit gap", k.ID)
		}
		raw, err := hex.DecodeString(k.Hex)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			t.Errorf("key %q is not a valid ed25519 public key", k.ID)
		}
	}
	if _, err := ActiveKey(); err != nil {
		t.Error(err)
	}
}

// TestRotationOverlapVerifiesBothKeys is the rotation rehearsal: during the overlap window the retiring
// key still signs and the incoming key is already accepted, so a binary from either side of the window
// verifies. Without this property a rotation is a flag day whose only repair path is an unverified
// reinstall.
func TestRotationOverlapVerifiesBothKeys(t *testing.T) {
	oldPub, oldPriv, _ := ed25519.GenerateKey(rand.Reader)
	newPub, newPriv, _ := ed25519.GenerateKey(rand.Reader)
	manifest := []byte("abc  heros-1.0.0-linux-amd64\n")

	orig := trustRoot
	t.Cleanup(func() { trustRoot = orig })

	// Step 1–2 of the documented rotation: the new key is published as accepted, the old one still signs.
	trustRoot = []TrustKey{
		{ID: "old", Hex: hex.EncodeToString(oldPub), Role: RoleActive, Note: "retiring"},
		{ID: "new", Hex: hex.EncodeToString(newPub), Role: RoleAccepted, Note: "incoming"},
	}
	if id, err := VerifyTrusted(manifest, Sign(oldPriv, manifest)); err != nil || id != "old" {
		t.Fatalf("overlap window: the signing key does not verify (id=%q err=%v)", id, err)
	}
	if id, err := VerifyTrusted(manifest, Sign(newPriv, manifest)); err != nil || id != "new" {
		t.Fatalf("overlap window: the incoming key does not verify (id=%q err=%v)", id, err)
	}
	if a, _ := ActiveKey(); a.ID != "old" {
		t.Errorf("during the overlap the active signer is %q, want the retiring key", a.ID)
	}

	// Step 3: roles flip. Both still verify; the new key now signs.
	trustRoot = []TrustKey{
		{ID: "old", Hex: hex.EncodeToString(oldPub), Role: RoleAccepted, Note: "retired, in overlap"},
		{ID: "new", Hex: hex.EncodeToString(newPub), Role: RoleActive, Note: "signing"},
	}
	if a, _ := ActiveKey(); a.ID != "new" {
		t.Errorf("after the flip the active signer is %q", a.ID)
	}
	if _, err := VerifyTrusted(manifest, Sign(oldPriv, manifest)); err != nil {
		t.Errorf("a release signed before the flip stopped verifying during the overlap: %v", err)
	}

	// Step 4: the old entry is deleted. Only now does the old signature stop verifying — the deliberate,
	// announced break.
	trustRoot = trustRoot[1:]
	if _, err := VerifyTrusted(manifest, Sign(oldPriv, manifest)); err == nil {
		t.Error("a deleted key still verifies — removal from the trust root has no effect")
	}
}

// TestVerifyTrustedFailsClosed — every failure mode must be a refusal. A verifier that returns nil on a
// tampered manifest is the single most valuable bug an attacker could find here.
func TestVerifyTrustedFailsClosed(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	manifest := []byte("abc  heros-1.0.0-linux-amd64\n")
	orig := trustRoot
	t.Cleanup(func() { trustRoot = orig })
	trustRoot = []TrustKey{{ID: "k", Hex: hex.EncodeToString(pub), Role: RoleActive, Note: "test"}}

	sig := Sign(priv, manifest)
	if _, err := VerifyTrusted(manifest, sig); err != nil {
		t.Fatalf("a good signature was refused: %v", err)
	}
	tampered := []byte("abd  heros-1.0.0-linux-amd64\n")
	if _, err := VerifyTrusted(tampered, sig); err == nil {
		t.Error("a tampered manifest verified")
	}
	for _, bad := range []string{"", "zz", sig + "00", sig[:len(sig)-2]} {
		if _, err := VerifyTrusted(manifest, bad); err == nil {
			t.Errorf("malformed signature %q verified", bad)
		}
	}
	// An empty trust root must refuse, not vacuously pass.
	trustRoot = nil
	if _, err := VerifyTrusted(manifest, sig); err == nil {
		t.Error("an empty trust root treated an unverifiable release as verified")
	}
	// A malformed entry must refuse rather than skipping to the next key: a broken root is an operator
	// problem, and silently verifying against the remainder hides it.
	trustRoot = []TrustKey{{ID: "broken", Hex: "not-hex", Role: RoleActive, Note: "x"}}
	if _, err := VerifyTrusted(manifest, sig); err == nil {
		t.Error("a malformed trust-root entry was skipped instead of refused")
	}
}
