package release

import (
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"strings"
)

// sshsig.go publishes the release signature in a SECOND encoding — OpenSSH's `sshsig` format — over exactly
// the same bytes, with exactly the same ed25519 key (P20 task 3.1).
//
// # Why a second encoding exists, when D2 says one signature is the floor
//
// It is not a second signature scheme. It is the same ed25519 signature over the same SHA256SUMS, packaged so
// that a tool a user already has can check it.
//
// The installer must verify the signature BEFORE placing anything on PATH (D4), and it must not use the
// binary it just downloaded to do so — that is circular: an attacker who can serve you a binary can serve
// you one that prints "signature OK". So the installer needs an INDEPENDENT ed25519 verifier, and the survey
// of what a real machine actually has is bleak:
//
//	stock macOS         /usr/bin/openssl is LibreSSL 3.3.6 — no `pkeyutl -rawin`, cannot verify ed25519
//	ubuntu:22.04 (min)  no openssl at all
//	OpenSSL ≥ 3.0       can verify the raw signature — but is not present by default on macOS
//	ssh-keygen -Y       present on stock macOS and on every distro that ships openssh-client
//
// An installer that hard-required OpenSSL 3 would refuse on the primary developer platform, and "install
// openssl first" is not a one-command install (NFR4). An installer that skipped the signature on macOS would
// be trading 安全 for UX, which L1 forbids. Publishing the same signature in the format the already-present
// tool reads resolves it without weakening anything: the raw `.sig` stays the floor for `heros upgrade` and
// for the documented offline step, and the `.sshsig` is what a shell script on a stock machine can check.
//
// # Why implemented here rather than by shelling out to ssh-keygen
//
// Signing with `ssh-keygen -Y sign` would need the release key written to disk as an OpenSSH private key
// file — putting the release signing key in a file, in CI, so a tool can read it. The signature itself is
// ed25519 over a documented pre-image, so producing it in-process keeps the key in memory and keeps the
// release's signing path down to one primitive.
//
// The format is PROTOCOL.sshsig from OpenSSH. TestSSHSigVerifiesWithRealSSHKeygen proves this implementation
// against the actual `ssh-keygen -Y verify` binary — a format implemented from a spec and never checked
// against the tool that must read it is a format that works until someone tries it.

// SSHSigNamespace is the namespace the release signature is bound to.
//
// sshsig namespaces exist so a signature made for one purpose cannot be replayed as another: a verifier
// demands the namespace it expects. `file` is OpenSSH's convention for file signatures and is what the
// installer passes to `ssh-keygen -Y verify -n file`.
const SSHSigNamespace = "file"

// sshString length-prefixes a byte string the way the SSH wire format does.
func sshString(b []byte) []byte {
	out := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(out[:4], uint32(len(b)))
	copy(out[4:], b)
	return out
}

// sshEd25519PublicKeyBlob is the SSH wire encoding of an ed25519 public key: string "ssh-ed25519" followed
// by the string of raw key bytes. This is the blob that base64-encodes into an authorized_keys line.
func sshEd25519PublicKeyBlob(pub ed25519.PublicKey) []byte {
	var b []byte
	b = append(b, sshString([]byte("ssh-ed25519"))...)
	b = append(b, sshString(pub)...)
	return b
}

// SSHPublicKeyLine renders an ed25519 public key as the `ssh-ed25519 AAAA…` line ssh-keygen reads.
//
// comment is appended so a human reading an allowed_signers file can tell which key it is; ssh-keygen
// ignores it.
func SSHPublicKeyLine(pub ed25519.PublicKey, comment string) string {
	line := "ssh-ed25519 " + base64.StdEncoding.EncodeToString(sshEd25519PublicKeyBlob(pub))
	if comment != "" {
		line += " " + comment
	}
	return line
}

// sshSigPreimage is the byte sequence that is actually signed (PROTOCOL.sshsig "Signed Data"):
//
//	MAGIC_PREAMBLE "SSHSIG" · string namespace · string reserved · string hash_algorithm · string H(message)
//
// Note that the message is hashed FIRST and the hash is what gets signed. This is why the signature does not
// grow with the manifest, and why the verifier must be told which hash was used.
func sshSigPreimage(namespace string, msgHash []byte) []byte {
	var b []byte
	b = append(b, []byte("SSHSIG")...)
	b = append(b, sshString([]byte(namespace))...)
	b = append(b, sshString(nil)...) // reserved, always empty
	b = append(b, sshString([]byte("sha512"))...)
	b = append(b, sshString(msgHash)...)
	return b
}

// SSHSigArmored signs msg with priv and returns an armored OpenSSH signature — the exact bytes that belong
// in a `.sshsig` file and that `ssh-keygen -Y verify -s <file>` reads.
func SSHSigArmored(priv ed25519.PrivateKey, msg []byte, namespace string) string {
	if namespace == "" {
		namespace = SSHSigNamespace
	}
	sum := sha512.Sum512(msg)
	sig := ed25519.Sign(priv, sshSigPreimage(namespace, sum[:]))

	pub := priv.Public().(ed25519.PublicKey)

	// The signature blob: string "ssh-ed25519" · string signature.
	var sigBlob []byte
	sigBlob = append(sigBlob, sshString([]byte("ssh-ed25519"))...)
	sigBlob = append(sigBlob, sshString(sig)...)

	// The outer container: MAGIC · uint32 version · string publickey · string namespace · string reserved ·
	// string hash_algorithm · string signature.
	var blob []byte
	blob = append(blob, []byte("SSHSIG")...)
	ver := make([]byte, 4)
	binary.BigEndian.PutUint32(ver, 1)
	blob = append(blob, ver...)
	blob = append(blob, sshString(sshEd25519PublicKeyBlob(pub))...)
	blob = append(blob, sshString([]byte(namespace))...)
	blob = append(blob, sshString(nil)...)
	blob = append(blob, sshString([]byte("sha512"))...)
	blob = append(blob, sshString(sigBlob)...)

	return armor(blob)
}

// armor wraps the signature blob in OpenSSH's PEM-like envelope, base64 in 70-character lines.
//
// The line width is 70 because that is what ssh-keygen emits; the verifier accepts other widths, but a file
// that differs from what the tool produces invites someone to conclude ours is malformed.
func armor(blob []byte) string {
	enc := base64.StdEncoding.EncodeToString(blob)
	var b strings.Builder
	b.WriteString("-----BEGIN SSH SIGNATURE-----\n")
	for i := 0; i < len(enc); i += 70 {
		end := i + 70
		if end > len(enc) {
			end = len(enc)
		}
		b.WriteString(enc[i:end])
		b.WriteString("\n")
	}
	b.WriteString("-----END SSH SIGNATURE-----\n")
	return b.String()
}

// AllowedSigners renders the `allowed_signers` file `ssh-keygen -Y verify -f` requires, listing every key in
// the published trust root.
//
// # Why every key, and why the principal is fixed
//
// Every key, because the trust root is a SET (see trustroot.go): during a rotation's overlap window a release
// may be signed by either the retiring or the incoming key, and an allowed_signers file holding only the
// active one would reject a release the raw verifier accepts — two verifiers disagreeing about the same
// release is worse than either being strict.
//
// The principal is a fixed identity rather than an email address because there is no person here: the
// signature is a release-engineering fact, and `ssh-keygen -Y verify -I` is passed the same constant. A
// per-maintainer identity would imply the release is attributable to an individual, which it is not.
func AllowedSigners() string {
	var b strings.Builder
	for _, k := range TrustRoot() {
		pub, err := publicKeyBytes(k.Hex)
		if err != nil {
			continue
		}
		b.WriteString("heros-release namespaces=\"" + SSHSigNamespace + "\" " +
			SSHPublicKeyLine(pub, k.ID+" ("+string(k.Role)+")") + "\n")
	}
	return b.String()
}

// TrustRootSSHLine renders the ACTIVE key as the `ssh-ed25519 AAAA…` line the install scripts pin.
//
// It exists so the drift gate has one authoritative form to compare the scripts against. Without it each test
// would re-derive the line, and a bug in the derivation would make the test agree with a broken script.
func TrustRootSSHLine() (string, error) {
	active, err := ActiveKey()
	if err != nil {
		return "", err
	}
	pub, err := publicKeyBytes(active.Hex)
	if err != nil {
		return "", err
	}
	return SSHPublicKeyLine(pub, ""), nil
}
