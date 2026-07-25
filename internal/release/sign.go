// Package release is the P11 supply-chain surface: reproducible builds, checksums, and a signature
// over the checksum manifest, with a verification step a customer can run (PRD NFR8, tasks 6.1–6.4).
//
// This binary runs inside customer CI with repository access, so a compromised release is a compromise
// of every customer's build. The defense is not obscurity: it is a signature over a checksum manifest,
// verifiable with a published public key and a documented step, plus builds that reproduce bit-for-bit
// so a third party can rebuild and confirm the bytes.
//
// The signature is ed25519 — small, fast, and in the Go standard library, so the verifier is the same
// binary the customer already trusts (no extra tool to install, which is itself a supply-chain surface).
package release

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// ChecksumManifest is the SHA256SUMS document: one "sha256  name" line per artifact, sorted by name so
// the manifest itself is reproducible.
func ChecksumManifest(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), n)
	}
	return b.String()
}

// VerifyChecksums checks each "sha256  name" line in manifest against the bytes in dir. It returns the
// first mismatch, or nil if every listed file matches. This is the always-runnable half of the
// documented verification step (task 6.4): it needs no key.
func VerifyChecksums(manifest string, read func(name string) ([]byte, error)) error {
	for _, line := range strings.Split(strings.TrimSpace(manifest), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return fmt.Errorf("malformed checksum line: %q", line)
		}
		want, name := fields[0], fields[1]
		data, err := read(name)
		if err != nil {
			return fmt.Errorf("checksum: cannot read %s: %w", name, err)
		}
		got := sha256.Sum256(data)
		if hex.EncodeToString(got[:]) != want {
			return fmt.Errorf("checksum MISMATCH for %s: manifest %s, actual %s", name, want, hex.EncodeToString(got[:]))
		}
	}
	return nil
}

// Sign signs manifest bytes with an ed25519 private key, returning a hex signature.
func Sign(priv ed25519.PrivateKey, manifest []byte) string {
	return hex.EncodeToString(ed25519.Sign(priv, manifest))
}

// Verify checks a hex signature over manifest against a hex public key. This is the second half of the
// documented verification step: it proves the manifest came from the holder of the release key.
func Verify(pubHex string, manifest []byte, sigHex string) error {
	pub, err := hex.DecodeString(strings.TrimSpace(pubHex))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("release: invalid public key")
	}
	sig, err := hex.DecodeString(strings.TrimSpace(sigHex))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("release: invalid signature encoding")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), manifest, sig) {
		return errors.New("release: signature does NOT verify — do not trust this release")
	}
	return nil
}

// ReadFileFunc is the default reader for VerifyChecksums against a directory.
func ReadFileFunc(dir string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) { return os.ReadFile(dir + "/" + name) }
}
