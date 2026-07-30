package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sshsig_test.go proves the sshsig encoding against the REAL `ssh-keygen -Y verify`.
//
// A wire format implemented from a spec and never checked against the tool that must read it is a format that
// works until someone tries it — and "someone" here is a user running the install script, on their machine,
// where the failure is a refused install they will read as our bug.

func sshKeygen(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not available on this host — the sshsig interop proof cannot run")
	}
	return p
}

// TestSSHSigVerifiesWithRealSSHKeygen is the live-toolchain contract test: our signature, their verifier.
func TestSSHSigVerifiesWithRealSSHKeygen(t *testing.T) {
	bin := sshKeygen(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	manifest := []byte("abc123  heros-0.20.0-darwin-arm64\n")

	dir := t.TempDir()
	msgPath := filepath.Join(dir, "SHA256SUMS")
	sigPath := msgPath + ".sshsig"
	signers := filepath.Join(dir, "allowed_signers")
	if err := os.WriteFile(msgPath, manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, []byte(SSHSigArmored(priv, manifest, SSHSigNamespace)), 0o644); err != nil {
		t.Fatal(err)
	}
	line := "heros-release namespaces=\"" + SSHSigNamespace + "\" " + SSHPublicKeyLine(pub, "test") + "\n"
	if err := os.WriteFile(signers, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	verify := func(msg string) ([]byte, error) {
		f, err := os.Open(msg)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		cmd := exec.Command(bin, "-Y", "verify", "-f", signers, "-I", "heros-release",
			"-n", SSHSigNamespace, "-s", sigPath)
		cmd.Stdin = f
		return cmd.CombinedOutput()
	}

	out, err := verify(msgPath)
	if err != nil {
		t.Fatalf("real ssh-keygen REFUSED our signature: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Good") {
		t.Errorf("ssh-keygen output does not report a good signature: %s", out)
	}

	// The red half: a tampered manifest must be refused by the same command. A verifier only ever shown to
	// accept is indistinguishable from `true`.
	tampered := filepath.Join(dir, "TAMPERED")
	if err := os.WriteFile(tampered, []byte("abc124  heros-0.20.0-darwin-arm64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := verify(tampered); err == nil {
		t.Errorf("ssh-keygen ACCEPTED a tampered manifest:\n%s", out)
	}
}

// TestSSHSigRejectsAForeignKey — the signature must not verify under a key that is not in allowed_signers.
// Without this, an allowed_signers file that failed to parse could read as "anything verifies".
func TestSSHSigRejectsAForeignKey(t *testing.T) {
	bin := sshKeygen(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	manifest := []byte("abc123  heros\n")

	dir := t.TempDir()
	msgPath := filepath.Join(dir, "SHA256SUMS")
	sigPath := msgPath + ".sshsig"
	signers := filepath.Join(dir, "allowed_signers")
	_ = os.WriteFile(msgPath, manifest, 0o644)
	_ = os.WriteFile(sigPath, []byte(SSHSigArmored(priv, manifest, SSHSigNamespace)), 0o644)
	_ = os.WriteFile(signers, []byte("heros-release namespaces=\""+SSHSigNamespace+"\" "+
		SSHPublicKeyLine(otherPub, "not-the-signer")+"\n"), 0o644)

	f, _ := os.Open(msgPath)
	defer f.Close()
	cmd := exec.Command(bin, "-Y", "verify", "-f", signers, "-I", "heros-release", "-n", SSHSigNamespace, "-s", sigPath)
	cmd.Stdin = f
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Errorf("a signature from a key NOT in allowed_signers verified:\n%s", out)
	}
}

// TestBothSignatureEncodingsCoverTheSameBytes — the raw .sig and the .sshsig are the same key over the same
// document. If they could disagree, an attacker would only have to defeat whichever one a given installer
// happened to reach for.
func TestBothSignatureEncodingsCoverTheSameBytes(t *testing.T) {
	bin := sshKeygen(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	orig := trustRoot
	t.Cleanup(func() { trustRoot = orig })
	trustRoot = []TrustKey{{ID: "dual", Hex: hex.EncodeToString(pub), Role: RoleActive, Note: "test"}}

	manifest := []byte("abc123  heros-0.20.0-linux-amd64\n")
	tampered := []byte("abc124  heros-0.20.0-linux-amd64\n")

	if _, err := VerifyTrusted(manifest, Sign(priv, manifest)); err != nil {
		t.Fatalf("raw verifier refused a good signature: %v", err)
	}
	if _, err := VerifyTrusted(tampered, Sign(priv, manifest)); err == nil {
		t.Error("raw verifier accepted a tampered manifest")
	}

	dir := t.TempDir()
	msgPath := filepath.Join(dir, "SHA256SUMS")
	tamperedPath := filepath.Join(dir, "SHA256SUMS.bad")
	sigPath := msgPath + ".sshsig"
	signers := filepath.Join(dir, "allowed_signers")
	_ = os.WriteFile(msgPath, manifest, 0o644)
	_ = os.WriteFile(tamperedPath, tampered, 0o644)
	_ = os.WriteFile(sigPath, []byte(SSHSigArmored(priv, manifest, SSHSigNamespace)), 0o644)
	// AllowedSigners renders from the trust root, so this also proves the trust root and the ssh path agree.
	_ = os.WriteFile(signers, []byte(AllowedSigners()), 0o644)

	run := func(path string) error {
		f, _ := os.Open(path)
		defer f.Close()
		cmd := exec.Command(bin, "-Y", "verify", "-f", signers, "-I", "heros-release",
			"-n", SSHSigNamespace, "-s", sigPath)
		cmd.Stdin = f
		out, err := cmd.CombinedOutput()
		if err != nil {
			return &execErr{err, out}
		}
		return nil
	}
	if err := run(msgPath); err != nil {
		t.Fatalf("ssh verifier refused what the raw verifier accepted: %v", err)
	}
	if err := run(tamperedPath); err == nil {
		t.Error("ssh verifier accepted what the raw verifier rejected — the two encodings disagree")
	}
}

type execErr struct {
	err error
	out []byte
}

func (e *execErr) Error() string { return e.err.Error() + ": " + string(e.out) }
