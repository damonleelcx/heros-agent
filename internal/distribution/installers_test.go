package distribution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/release"
)

// installers_test.go holds the two install scripts to the properties they cannot state about themselves.
//
// These are shell and PowerShell, so no compiler checks them and no import graph links them to the trust root
// they pin. The drift they can suffer is silent and total: a script verifying against a retired key rejects
// every real release, and each side looks correct read on its own.

func readScript(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", name))
	if err != nil {
		t.Fatalf("%s not readable: %v", name, err)
	}
	return string(b)
}

// TestInstallScriptsPinTheTrustRoot is the drift gate. Both installers embed the release public key — they
// must, since a downloaded key proves nothing — and both copies must equal the compiled-in active key.
func TestInstallScriptsPinTheTrustRoot(t *testing.T) {
	active, err := release.ActiveKey()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := release.TrustRootSSHLine()
	if err != nil {
		t.Fatal(err)
	}

	sh := readScript(t, "install.sh")
	if !strings.Contains(sh, active.Hex) {
		t.Errorf("install.sh does not pin the active release key %s (%s…) — it would verify against a key the "+
			"binary rejects", active.ID, active.Hex[:12])
	}
	if !strings.Contains(sh, pub) {
		t.Errorf("install.sh does not pin the active key in ssh form — `ssh-keygen -Y verify` is its primary " +
			"verifier, so that copy is the one that decides whether an install proceeds")
	}

	ps := readScript(t, "install.ps1")
	if !strings.Contains(ps, pub) {
		t.Errorf("install.ps1 does not pin the active key in ssh form — on Windows ssh-keygen is the ONLY " +
			"ed25519 verifier available, so this is its only check")
	}
	// The raw hex has no verifier on Windows (.NET Framework has no Ed25519), so install.ps1 does NOT carry
	// it. Asserting its absence keeps someone from adding a key copy nothing uses — an unused pinned key is a
	// second thing to rotate and a second thing to forget.
	if strings.Contains(ps, active.Hex) {
		t.Error("install.ps1 pins the raw hex key, which nothing on Windows can verify against — an unused " +
			"key copy is a second thing to rotate and forget")
	}
}

// TestInstallScriptsFailClosed reads both scripts for the shapes that would turn a refusal into a warning.
// D4 is "verify before PATH, fail closed", and the way that rule dies in a shell script is a well-meant
// `|| true` added to get past a flaky step.
func TestInstallScriptsFailClosed(t *testing.T) {
	sh := readScript(t, "install.sh")

	if !strings.Contains(sh, "set -eu") {
		t.Error("install.sh does not set -eu — an unset variable or a failed command would be ignored")
	}
	// The two verification calls must both happen before the install, in that order. Checking the ORDER
	// matters: verifying the signature before the checksum leaves a window where a trusted manifest describes
	// a file nobody hashed.
	//
	// Anchored on CALL SITES with their real arguments, not on bare names: the function definitions appear
	// earlier in the file and take `$1`/`$2`, so matching a name would compare the order of the definitions
	// rather than the order of execution — a test that passes no matter what the script does.
	body := sh[strings.Index(sh, "set -eu"):]
	iChecksum := strings.Index(body, `verify_checksum "${TMP}/${asset}"`)
	iSignature := strings.Index(body, `verify_signature "${TMP}/SHA256SUMS"`)
	iInstall := strings.Index(body, `mv "${TMP}/${asset}" "${dest}/.heros.new"`)
	if iChecksum < 0 || iSignature < 0 || iInstall < 0 {
		t.Fatalf("install.sh no longer has a recognisable verify-then-install sequence "+
			"(checksum=%d signature=%d place=%d)", iChecksum, iSignature, iInstall)
	}
	if iChecksum >= iSignature || iSignature >= iInstall {
		t.Errorf("install.sh's order is wrong (checksum=%d signature=%d install=%d): both verifications must "+
			"complete before anything is placed on PATH", iChecksum, iSignature, iInstall)
	}
	// An escape hatch is the fallback D4 forbids: "install anyway, unverified" trades security for the
	// convenience of not handling the error path.
	for _, hatch := range []string{
		"HEROS_SKIP_VERIFY", "HEROS_INSECURE", "--no-verify", "SKIP_SIGNATURE",
	} {
		if strings.Contains(sh, hatch) {
			t.Errorf("install.sh has an escape hatch (%s) — an installer that can be told to skip verification "+
				"will be, by a wrapper script nobody reviews", hatch)
		}
	}
	// The verifier must never be the binary being installed... unless it is an ALREADY-INSTALLED one, which
	// is a different thing. The distinction is that the check runs `heros` from PATH, not the download.
	if strings.Contains(sh, `"${TMP}/${asset}" verify`) {
		t.Error("install.sh verifies with the binary it just downloaded — that is circular: an attacker who " +
			"can serve you a binary can serve you one that prints 'signature OK'")
	}

	ps := readScript(t, "install.ps1")
	if !strings.Contains(ps, "$ErrorActionPreference = 'Stop'") {
		t.Error("install.ps1 does not stop on error — PowerShell's default is to continue past a failure, " +
			"which would carry on to the install step after a failed download")
	}
	if !strings.Contains(ps, "Set-StrictMode") {
		t.Error("install.ps1 does not set strict mode — an unset variable would expand to empty and the " +
			"checksum comparison would compare two empty strings, which are equal")
	}
	// Measured from the start of the executable body: the doc comment above it explains the verifier ladder
	// and mentions `-Y verify`, and matching that mention would compare prose against code.
	psBody := ps[strings.Index(ps, "Set-StrictMode"):]
	iPsHash := strings.Index(psBody, "Get-FileHash")
	iPsSig := strings.Index(psBody, "-Y verify")
	iPsCopy := strings.Index(psBody, "Copy-Item $assetPath $exe")
	if iPsHash < 0 || iPsSig < 0 || iPsCopy < 0 {
		t.Fatal("install.ps1 no longer has a recognisable verify-then-install sequence")
	}
	if iPsHash >= iPsSig || iPsSig >= iPsCopy {
		t.Errorf("install.ps1's order is wrong (hash=%d sig=%d copy=%d)", iPsHash, iPsSig, iPsCopy)
	}
}

// TestInstallScriptsDiscloseTheLimits — a user on Alpine or windows/arm64 must be told what is true and what
// to do, by the installer, at the moment it matters. A generic "unsupported platform" sends them to support.
func TestInstallScriptsDiscloseTheLimits(t *testing.T) {
	sh := readScript(t, "install.sh")
	musl, ok := TargetFor("linux", "")
	if !ok {
		t.Fatal("no musl limit row in the matrix")
	}
	if !strings.Contains(sh, "musl") || !strings.Contains(sh, ImageRepo) {
		t.Errorf("install.sh does not detect musl and point at the container image — a glibc binary installs "+
			"fine there and then dies with a loader error on first use. The matrix row says: %s", musl.Answer)
	}
	ps := readScript(t, "install.ps1")
	if !strings.Contains(ps, "ARM64") || !strings.Contains(ps, "emulation") {
		t.Error("install.ps1 does not handle windows/arm64 — the matrix discloses it as a limit answered by " +
			"x64 emulation, and the installer is where that answer is needed")
	}
}

// TestInstallScriptsOfferTheChannelIdioms — when the installer refuses, the way out it names must be a real
// channel command from the contract, not an invented one.
func TestInstallScriptsOfferTheChannelIdioms(t *testing.T) {
	sh := readScript(t, "install.sh")
	brew, _ := ChannelByID("homebrew")
	// The formula's install command names the tap; the installer's suggestion must match it exactly, or the
	// user pastes a command that does not resolve.
	if !strings.Contains(sh, "brew install heros-foreal/tap/heros") ||
		!strings.Contains(brew.Install, "brew install heros-foreal/tap/heros") {
		t.Errorf("install.sh's Homebrew suggestion and the channel contract disagree:\n  script mentions brew: %v\n  contract: %s",
			strings.Contains(sh, "brew install heros-foreal/tap/heros"), brew.Install)
	}
	if !strings.Contains(sh, "HEROS_UNINSTALL") {
		t.Error("install.sh has no uninstall path — task 3.8 requires the channel to be removable by its own idiom")
	}
	if !strings.Contains(sh, "HEROS_VERSION") {
		t.Error("install.sh cannot install a pinned version — that is the documented rollback (task 3.8)")
	}
}

// TestReadmeInstallSectionMatchesContract is the honesty gate for the README (tasks 3.7, 8.1–8.3).
//
// The README is what a customer's engineer reads. If it can drift from the channel contract, then the moment a
// channel's Publication changes the README keeps sending users to a command that fails — which is the exact
// failure the Delivered()/Publication split exists to prevent, reintroduced one document later.
func TestReadmeInstallSectionMatchesContract(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("README.md not readable: %v", err)
	}
	got, ok := ReadmeSection(string(b))
	if !ok {
		t.Fatalf("README.md has no generated install section. Add the markers and run "+
			"`make readme-install`:\n%s\n%s", ReadmeMarkerStart, ReadmeMarkerEnd)
	}
	v, err := ParseTag(DocumentedRelease)
	if err != nil {
		t.Fatal(err)
	}
	want := ReadmeInstallSection(v, "")
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Errorf("README.md's install section has drifted from the channel contract. Run `make readme-install`.\n"+
			"first difference at byte %d", firstDiff(got, want))
	}
}

func firstDiff(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// TestReadmeClaimsOnlyDeliveredChannels is the Sales-lens assertion, checked against the rendered document
// rather than the generator: a channel a user cannot install from must never appear as an install command, and
// the undeliverable ones must still be DISCLOSED with their blocker.
func TestReadmeClaimsOnlyDeliveredChannels(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	section, ok := ReadmeSection(string(b))
	if !ok {
		t.Fatal("no generated install section in README.md")
	}
	// Everything before the "not installable" disclosure is the part a reader treats as available.
	cut := strings.Index(section, "Not supported")
	if cut < 0 {
		t.Fatal("the install section does not disclose what is unsupported")
	}
	available := section[:cut]
	for _, c := range Channels() {
		installCmd := Command(c.Install, "0.20.0")
		if c.Delivered() {
			if !strings.Contains(available, installCmd) {
				t.Errorf("%s is delivered but the README does not show its install command", c.Label)
			}
			continue
		}
		if strings.Contains(available, installCmd) {
			t.Errorf("the README presents %q as available, but %s", installCmd, c.Blocker)
		}
		if !strings.Contains(section, c.Blocker) {
			t.Errorf("the README does not disclose why %s is unavailable", c.Label)
		}
	}
	// The free/paid boundary and the pinned-URL audit path are both required by name.
	for _, needle := range []string{
		"free, with no account, forever",
		"raw.githubusercontent.com/damonleelcx/heros-agent/" + DocumentedRelease + "/scripts/install.sh",
		"ssh-keygen -Y verify",
	} {
		if !strings.Contains(section, needle) {
			t.Errorf("the README's install section is missing %q", needle)
		}
	}
}
