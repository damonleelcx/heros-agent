package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// release_test.go proves the two supply-chain claims against a REAL built artifact (tasks 6.2/6.4):
// the documented verification step succeeds, and builds reproduce bit-for-bit.

// buildHeros builds cmd/heros with the reproducible release flags and returns the output path.
func buildHeros(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	out := filepath.Join(dir, "heros")
	// The CLI links CGO tree-sitter frontends (Python, etc.), so the release is built with CGO on the
	// TARGET's native runner rather than cross-compiled CGO-free. Reproducibility holds on a fixed
	// toolchain + C compiler; -trimpath + -buildvcs=false remove the host-path and VCS variance.
	cmd := exec.Command("go", "build",
		"-buildvcs=false", "-trimpath",
		"-ldflags", "-s -w -X github.com/heros-foreal/agentd/internal/cli.ToolVersion=test",
		"-o", out, "../../cmd/heros")
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build heros: %v\n%s", err, b)
	}
	return out
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// TestReproducibleBuild — the same source + toolchain + flags yields byte-identical binaries (NFR8).
func TestReproducibleBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build in -short")
	}
	a := buildHeros(t, t.TempDir())
	b := buildHeros(t, t.TempDir())
	if sha256File(t, a) != sha256File(t, b) {
		t.Errorf("build is not reproducible: two builds produced different bytes")
	}
}

// TestDocumentedVerificationSucceeds — build a real artifact, write the manifest, sign it, and run the
// exact verification the docs describe: checksum match, then signature verify (task 6.4).
func TestDocumentedVerificationSucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build in -short")
	}
	dir := t.TempDir()
	bin := buildHeros(t, dir)
	binBytes, _ := os.ReadFile(bin)

	// Manifest over the real artifact.
	manifest := ChecksumManifest(map[string][]byte{"heros": binBytes})
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	// Step 1: checksum verification (no key).
	if err := VerifyChecksums(manifest, func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(dir, name))
	}); err != nil {
		t.Fatalf("checksum verification failed: %v", err)
	}

	// Step 2: signature verification with an ephemeral release key (a real release uses a held secret).
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sig := Sign(priv, []byte(manifest))
	if err := Verify(hex.EncodeToString(pub), []byte(manifest), sig); err != nil {
		t.Fatalf("signature verification failed on a genuine release: %v", err)
	}

	// A tampered binary must FAIL checksum verification — the guarantee being real, not decorative.
	tampered := append([]byte(nil), binBytes...)
	tampered[len(tampered)/2] ^= 0xff
	if err := VerifyChecksums(manifest, func(string) ([]byte, error) { return tampered, nil }); err == nil {
		t.Error("checksum verification PASSED a tampered binary — the guarantee is decoration")
	}

	// A wrong key must FAIL signature verification.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := Verify(hex.EncodeToString(otherPub), []byte(manifest), sig); err == nil {
		t.Error("signature verification PASSED under the wrong key")
	}
}

// TestPublishedPublicKeyIsValid — the committed public key is a well-formed ed25519 key, so the
// documented Step 2 can actually run against it.
func TestPublishedPublicKeyIsValid(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "release", "heros-release.pub"))
	if err != nil {
		t.Skipf("published key not present: %v", err)
	}
	raw, err := hex.DecodeString(trim(string(b)))
	if err != nil || len(raw) != ed25519.PublicKeySize {
		t.Fatalf("published public key is not a valid ed25519 key (%d bytes)", len(raw))
	}
}

func trim(s string) string {
	out := s
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == ' ' || out[len(out)-1] == '\t' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	return out
}
