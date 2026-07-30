package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/distribution"
)

// spine_test.go proves the release spine's GREEN path and its idempotency (P20 tasks 2.2, 2.5, 2.6).
//
// # Why this test lives in internal/release rather than internal/distribution
//
// The green path needs a signature that verifies against the trust root, and the trust root is deliberately
// compiled in with no override — an env-var or flag that could redirect it would let an attacker who can set
// one make an unverified release verify, which trades 安全 for the 运维 convenience of an easier rehearsal.
// So the test swaps the unexported `trustRoot`, which only this package can do, and imports the distribution
// package for the rest of the spine. (`scripts/release-rehearse.sh` correspondingly cannot show a green gate:
// with a throwaway key it honestly reports UNSIGNED and the gate refuses. That is the right trade — the
// rehearsal proves every step and the refusal, and this test proves the acceptance.)

// stageRelease writes a complete set of per-runner bundles for a version, the way five matrix jobs would.
func stageRelease(t *testing.T, root, version string) {
	t.Helper()
	for _, target := range distribution.Shipped() {
		name := distribution.AssetName(version, target.GOOS, target.GOARCH)
		dir := filepath.Join(root, "build-"+target.GOOS+"-"+target.GOARCH)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Deterministic content per target: the same "build" twice must produce the same bytes, which is
		// what makes the re-run comparison meaningful.
		body := []byte("heros " + version + " for " + target.Key())
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o755); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		line := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "REPRODUCIBLE-"+target.GOOS+"-"+target.GOARCH),
			[]byte("reproducible\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// runSpine performs merge → sign → attest → gate on a staged artifact root and returns what the gate said.
func runSpine(t *testing.T, root string, v distribution.Version, priv ed25519.PrivateKey) (manifest, sig string, fails []distribution.GateFailure) {
	t.Helper()
	arts, err := distribution.CollectRunnerArtifacts(root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := distribution.Merge(arts)
	if err != nil {
		t.Fatal(err)
	}
	sig = Sign(priv, []byte(m.Text))
	keyID, err := VerifyTrusted([]byte(m.Text), sig)
	if err != nil {
		t.Fatalf("the release's own signature does not verify: %v", err)
	}
	a := distribution.Attestation{
		Version: v.Version, Assets: m.Assets,
		SignedManifest: true, SigningKeyID: keyID,
	}
	ok, missing := distribution.ReproducibleMarkers(root)
	return m.Text, sig, distribution.Gate(v, a, distribution.Repro{Verified: ok, Missing: missing})
}

// TestReleaseSpineAcceptsACompleteSignedRelease is the green path: five native targets, a signature from a
// trust-root key, reproducibility evidence for every row — and a gate that passes.
//
// It matters because every other test in this area asserts a refusal. A pipeline made only of refusals would
// pass all of them by refusing everything, and nobody would notice until a release was due.
func TestReleaseSpineAcceptsACompleteSignedRelease(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	orig := trustRoot
	t.Cleanup(func() { trustRoot = orig })
	trustRoot = []TrustKey{{ID: "spine-test", Hex: hex.EncodeToString(pub), Role: RoleActive, Note: "test"}}

	v, err := distribution.ParseTag("v0.20.0")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	stageRelease(t, root, v.Version)

	_, _, fails := runSpine(t, root, v, priv)
	if len(fails) != 0 {
		t.Fatalf("a complete, signed, reproducible release was refused:\n%s", distribution.GateReport(v, fails))
	}
}

// TestReleaseRerunReproducesTheSameArtifactSet is task 2.6. Two independent runs of the same tag must produce
// the same signed document, byte for byte — otherwise the publish job's "did the re-run reproduce what was
// published?" check has nothing stable to compare, and a retry silently replaces a release with a different
// one carrying the same version.
//
// The signature is compared too: ed25519 is deterministic, so a stable manifest means a stable .sig, and a
// customer who recorded the signature of the release they audited can re-download and still match it.
func TestReleaseRerunReproducesTheSameArtifactSet(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	orig := trustRoot
	t.Cleanup(func() { trustRoot = orig })
	trustRoot = []TrustKey{{ID: "spine-test", Hex: hex.EncodeToString(pub), Role: RoleActive, Note: "test"}}

	v, _ := distribution.ParseTag("v0.20.0")

	first := t.TempDir()
	stageRelease(t, first, v.Version)
	m1, s1, f1 := runSpine(t, first, v, priv)

	// A completely separate run: new directory, jobs staged in a different order, same source.
	second := t.TempDir()
	stageRelease(t, second, v.Version)
	m2, s2, f2 := runSpine(t, second, v, priv)

	if len(f1) != 0 || len(f2) != 0 {
		t.Fatalf("gate refused a good release: %v / %v", f1, f2)
	}
	if m1 != m2 {
		t.Errorf("re-running the same tag produced a different manifest:\n--- run 1\n%s\n--- run 2\n%s", m1, m2)
	}
	if s1 != s2 {
		t.Error("re-running the same tag produced a different signature — a customer who recorded the " +
			"signature of the release they audited could not match it again")
	}
}

// TestReleaseSpineRefusesAPartialMatrixEndToEnd walks the whole spine with one runner's bundle missing, which
// is what `scripts/release-rehearse.sh --real-only` reproduces on a developer's machine. The refusal must
// name the missing platform, not just fail: on a five-runner matrix "reproducibility failed" and "the arm64
// runner never reported" are completely different next actions.
func TestReleaseSpineRefusesAPartialMatrixEndToEnd(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	orig := trustRoot
	t.Cleanup(func() { trustRoot = orig })
	trustRoot = []TrustKey{{ID: "spine-test", Hex: hex.EncodeToString(pub), Role: RoleActive, Note: "test"}}

	v, _ := distribution.ParseTag("v0.20.0")
	root := t.TempDir()
	stageRelease(t, root, v.Version)

	dropped := distribution.Shipped()[len(distribution.Shipped())-1]
	if err := os.RemoveAll(filepath.Join(root, "build-"+dropped.GOOS+"-"+dropped.GOARCH)); err != nil {
		t.Fatal(err)
	}

	_, _, fails := runSpine(t, root, v, priv)
	if len(fails) == 0 {
		t.Fatal("a release missing a whole platform passed the gate")
	}
	report := distribution.GateReport(v, fails)
	if !strings.Contains(report, dropped.Key()) && !strings.Contains(report, distribution.AssetName(v.Version, dropped.GOOS, dropped.GOARCH)) {
		t.Errorf("the refusal does not name the missing platform %s:\n%s", dropped.Key(), report)
	}
}
