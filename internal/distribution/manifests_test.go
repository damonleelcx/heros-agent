package distribution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// manifests_test.go is the D5 enforcement suite: every generated manifest must carry the tag's version and a
// checksum that came from the SIGNED release manifest, and the generator must refuse rather than emit a
// manifest with a hole in it.

func fixtureSums(version string) map[string]string {
	sums := map[string]string{}
	// Distinct, recognisable checksums per target, so a test can prove the right one landed in the right
	// place. A single shared value would let a formula that put the darwin sum on the linux row pass.
	digits := []string{"1", "2", "3", "4", "5"}
	for i, t := range Shipped() {
		sums[AssetName(version, t.GOOS, t.GOARCH)] = strings.Repeat(digits[i%len(digits)], 64)
	}
	return sums
}

func generateFixture(t *testing.T, tag string) (Version, []GeneratedFile) {
	t.Helper()
	v, err := ParseTag(tag)
	if err != nil {
		t.Fatal(err)
	}
	a := Attestation{Version: v.Version, Assets: ExpectedAssets(v.Version), SignedManifest: true, SigningKeyID: "k"}
	files, err := Generate(v, fixtureSums(v.Version), a)
	if err != nil {
		t.Fatal(err)
	}
	return v, files
}

func fileNamed(files []GeneratedFile, suffix string) (GeneratedFile, bool) {
	for _, f := range files {
		if strings.HasSuffix(f.Path, suffix) {
			return f, true
		}
	}
	return GeneratedFile{}, false
}

// TestGeneratedManifestsCarryNoSecondVersion is the gate design.md's ratification record points at for D5.
// Every generated file must state the tag's version — and none may state any OTHER version, which is what a
// copy-paste from the previous release looks like.
func TestGeneratedManifestsCarryNoSecondVersion(t *testing.T) {
	v, files := generateFixture(t, "v0.20.1")
	if len(files) < 8 {
		t.Fatalf("expected a manifest per channel plus the capability map, got %d: %v", len(files), files)
	}
	for _, f := range files {
		if !strings.Contains(f.Content, v.Version) {
			t.Errorf("%s does not carry the version %s", f.Path, v.Version)
		}
		for _, stale := range []string{"0.19.", "0.20.0-", "1.0.0"} {
			if strings.Contains(f.Content, stale) {
				t.Errorf("%s carries a version other than the tag's (%q) — that is the drift D5 forbids",
					f.Path, stale)
			}
		}
		// A leading "v" in a package version is rejected by Homebrew and by nfpm. The tag has one; the
		// version must not.
		if strings.Contains(f.Content, "version \"v") || strings.Contains(f.Content, "version: \"v") {
			t.Errorf("%s carries the tag rather than the version in its version field", f.Path)
		}
	}
}

// TestGeneratedChecksumsComeFromTheSignedManifest — the chain from `brew install` back to the release key is
// exactly this: the formula's sha256 was copied from the signed document. If the generator hashed files
// itself, the formula would attest to whatever bytes it happened to hold.
func TestGeneratedChecksumsComeFromTheSignedManifest(t *testing.T) {
	v, files := generateFixture(t, "v0.20.1")
	sums := fixtureSums(v.Version)

	brew, ok := fileNamed(files, "homebrew/heros.rb")
	if !ok {
		t.Fatal("no Homebrew formula generated")
	}
	// Each of the four unix targets' checksums must appear, next to its own URL.
	for _, target := range Shipped() {
		if target.GOOS == "windows" {
			continue
		}
		asset := AssetName(v.Version, target.GOOS, target.GOARCH)
		if !strings.Contains(brew.Content, asset) {
			t.Errorf("formula does not reference %s", asset)
		}
		if !strings.Contains(brew.Content, sums[asset]) {
			t.Errorf("formula does not carry the signed checksum for %s", asset)
		}
	}

	scoop, ok := fileNamed(files, "scoop/heros.json")
	if !ok {
		t.Fatal("no Scoop manifest generated")
	}
	winAsset := AssetName(v.Version, "windows", "amd64")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(scoop.Content), &parsed); err != nil {
		t.Fatalf("the Scoop manifest is not valid JSON — scoop would reject it: %v", err)
	}
	if got := parsed["version"]; got != v.Version {
		t.Errorf("scoop version = %v, want %s", got, v.Version)
	}
	arch := parsed["architecture"].(map[string]any)["64bit"].(map[string]any)
	if arch["hash"] != sums[winAsset] {
		t.Errorf("scoop hash = %v, want the signed checksum %s", arch["hash"], sums[winAsset])
	}

	inst, ok := fileNamed(files, ".installer.yaml")
	if !ok {
		t.Fatal("no winget installer manifest generated")
	}
	// winget wants the sha256 uppercased. A lowercase one is rejected by the winget validation pipeline,
	// which is a failure that only shows up in someone else's CI.
	if !strings.Contains(inst.Content, strings.ToUpper(sums[winAsset])) {
		t.Errorf("winget installer manifest does not carry the uppercased signed checksum")
	}
	if !strings.Contains(inst.Content, "InstallerType: portable") {
		t.Error("winget manifest does not declare the artifact as portable — the release ships a bare .exe, " +
			"and any other InstallerType makes winget attempt a silent install that does not exist")
	}
}

// TestGenerateRefusesAnIncompleteManifest — a formula with an empty sha256 installs nothing and reports a
// confusing error on the user's machine. Refusing at generation time reports it here instead.
func TestGenerateRefusesAnIncompleteManifest(t *testing.T) {
	v, _ := ParseTag("v0.20.1")
	a := Attestation{Version: v.Version}
	for _, drop := range Shipped() {
		sums := fixtureSums(v.Version)
		delete(sums, AssetName(v.Version, drop.GOOS, drop.GOARCH))
		_, err := Generate(v, sums, a)
		if err == nil {
			t.Errorf("Generate succeeded with %s missing from the signed manifest", drop.Key())
			continue
		}
		if !strings.Contains(err.Error(), AssetName(v.Version, drop.GOOS, drop.GOARCH)) {
			t.Errorf("the refusal for a missing %s does not name the asset: %v", drop.Key(), err)
		}
	}
}

// TestGeneratedFilesAreNeverCommitted closes the loop with the D5 audit: the generator writes into the release
// directory, and the repository must hold none of its output. A committed formula is a second copy of the
// version that drifts on the next tag.
func TestGeneratedFilesAreNeverCommitted(t *testing.T) {
	_, files := generateFixture(t, "v0.20.1")
	root := filepath.Join("..", "..")
	for _, f := range files {
		// The packaging output goes under dist/, which the audit skips and .gitignore excludes. What must not
		// exist is the same path at the repository root.
		if _, err := os.Stat(filepath.Join(root, f.Path)); err == nil {
			t.Errorf("%s exists in the repository — generated manifests must live only in the release output", f.Path)
		}
	}
}

// TestChannelReportDisclosesTheUndelivered is the Sales-lens gate (task 8.1/8.3). The capability map must not
// quietly omit a channel whose manifest exists but which nobody can install from — an absent row reads as
// "not supported", and that is not what is true.
func TestChannelReportDisclosesTheUndelivered(t *testing.T) {
	v, _ := ParseTag("v0.20.1")
	a := Attestation{Version: v.Version, SignedManifest: true, SigningKeyID: "k"}
	report := ChannelReport(v, a)
	for _, c := range Channels() {
		if !strings.Contains(report, c.Label) {
			t.Errorf("the channel report omits %q entirely", c.Label)
		}
		if !c.Delivered() && !strings.Contains(report, c.Blocker) {
			t.Errorf("the channel report lists %q without saying what is missing", c.Label)
		}
	}
	if !strings.Contains(report, "Generated but not yet publishable") {
		t.Error("the channel report has no section for the channels a user cannot yet install from")
	}
}

// TestEveryChannelIsUninstallableAndPinnable is task 3.8, asserted as a property of the contract rather than
// as a sentence in a doc. A channel with no uninstall idiom leaves users deleting files by hand.
func TestEveryChannelIsUninstallableAndPinnable(t *testing.T) {
	for _, c := range Channels() {
		if c.Install == "" {
			t.Errorf("%s: no install command", c.ID)
		}
		if c.Uninstall == "" {
			t.Errorf("%s: no uninstall idiom — task 3.8 requires every channel to be removable by its own means", c.ID)
		}
		if c.Pin == "" {
			t.Errorf("%s: no way to install a specific prior version — that is the documented rollback", c.ID)
		}
		if c.Upgrade == "" {
			t.Errorf("%s: no upgrade command", c.ID)
		}
		if c.Verification == "" {
			t.Errorf("%s: does not state how it establishes that the bytes are ours", c.ID)
		}
		if !c.Delivered() && c.Blocker == "" {
			t.Errorf("%s: not delivered and does not say why", c.ID)
		}
		if len(c.GOOSes) == 0 {
			t.Errorf("%s: serves no operating system", c.ID)
		}
		// A version placeholder that never gets substituted renders as literal "{{version}}" in a README.
		for name, tmpl := range map[string]string{"install": c.Install, "pin": c.Pin} {
			if strings.Contains(Command(tmpl, "9.9.9"), "{{version}}") {
				t.Errorf("%s: %s command has an unsubstituted placeholder", c.ID, name)
			}
		}
	}
}

// TestTargetMatrixChannelsExist — the target matrix names channels by id, and a typo there would render a
// platform row pointing at a channel that does not exist.
func TestTargetMatrixChannelsExist(t *testing.T) {
	for _, target := range Targets() {
		for _, id := range target.Channels {
			if _, ok := ChannelByID(id); !ok {
				t.Errorf("target %s names channel %q, which is not defined", target.Key(), id)
			}
		}
	}
	// And the reverse: a delivered channel that no target row lists is a channel no reader will find.
	for _, c := range DeliveredChannels() {
		found := false
		for _, target := range Targets() {
			for _, id := range target.Channels {
				if id == c.ID {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("channel %q is delivered but no target row lists it", c.ID)
		}
	}
}

// TestManagerOwnedDetectionIsConservative — `heros upgrade` must defer to a package manager rather than
// overwrite a file it owns (D7), but the default for an unrecognised path must be "not manager-owned".
// Guessing the other way tells a user with a hand-placed binary to run `brew upgrade` for a brew they may not
// have installed.
func TestManagerOwnedDetectionIsConservative(t *testing.T) {
	owned := map[string]string{
		"/opt/homebrew/bin/heros":                                         "homebrew",
		"/home/linuxbrew/.linuxbrew/bin/heros":                            "homebrew",
		"/usr/local/Cellar/heros/0.20.0/bin/heros":                        "homebrew",
		"C:\\Users\\x\\scoop\\shims\\heros.exe":                           "scoop",
		"C:\\Users\\x\\AppData\\Local\\Microsoft\\WindowsApps\\heros.exe": "winget",
		"/usr/bin/heros":                                                  "deb",
	}
	for path, want := range owned {
		c, ok := ManagerOwnedChannelFor(path)
		if !ok || c.ID != want {
			t.Errorf("ManagerOwnedChannelFor(%q) = (%q, %v), want %q", path, c.ID, ok, want)
		}
	}
	for _, path := range []string{
		"/usr/local/bin/heros", // what install.sh writes — must stay upgradeable in place
		"/home/dev/.local/bin/heros",
		"C:\\Users\\x\\AppData\\Local\\Programs\\heros\\heros.exe", // what install.ps1 writes
		"./heros",
	} {
		if c, ok := ManagerOwnedChannelFor(path); ok {
			t.Errorf("ManagerOwnedChannelFor(%q) claimed channel %q owns it — upgrade would refuse to replace "+
				"a binary it placed itself", path, c.ID)
		}
	}
}

// TestGenerateScopedToOneChannelRequiresOnlyItsPlatforms — a `.deb` is built from the linux binaries and has
// nothing to do with the darwin ones. Demanding all five targets to emit an nfpm config coupled a channel to
// platforms it never mentions, and it broke a legitimate single-platform packaging run.
//
// The unscoped call must still require everything: a Release publishing a formula that points at four platforms
// while its manifest lists three is broken in a way only the fourth platform's users discover.
func TestGenerateScopedToOneChannelRequiresOnlyItsPlatforms(t *testing.T) {
	v, _ := ParseTag("v0.20.1")
	a := Attestation{Version: v.Version}

	linuxOnly := map[string]string{
		AssetName(v.Version, "linux", "amd64"): strings.Repeat("1", 64),
		AssetName(v.Version, "linux", "arm64"): strings.Repeat("2", 64),
	}
	files, err := Generate(v, linuxOnly, a, "deb")
	if err != nil {
		t.Fatalf("generating the deb channel from the linux rows alone was refused: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected the two nfpm configs, got %d: %v", len(files), files)
	}
	for _, f := range files {
		if f.Channel != "deb" {
			t.Errorf("a --only deb run emitted a %q file: %s", f.Channel, f.Path)
		}
	}

	// The capability map is deliberately NOT emitted for a scoped run: a CHANNELS.md listing every channel,
	// written by a run that generated one, would describe a release that does not exist.
	for _, f := range files {
		if strings.HasSuffix(f.Path, "CHANNELS.md") {
			t.Error("a scoped run emitted the full channel capability map")
		}
	}

	// Windows-only rows must not satisfy a scoop run's requirement for... themselves; and asking for scoop with
	// only linux rows must still refuse.
	if _, err := Generate(v, linuxOnly, a, "scoop"); err == nil {
		t.Error("generating the scoop manifest with no windows asset succeeded — it would carry an empty hash")
	}
	// And the unscoped call still demands the full set.
	if _, err := Generate(v, linuxOnly, a); err == nil {
		t.Error("an unscoped Generate accepted a partial manifest — a release must publish a complete set")
	}
}

// TestScopedDebGenerationTakesTheArchesItHas — the nfpm configs are per-arch, so a scoped run on one
// architecture must produce that architecture's config rather than refusing because the other is absent. An
// unscoped run (a real release) must still demand both: a Release offering a .deb for amd64 and not arm64 is a
// gap only arm64's users find.
func TestScopedDebGenerationTakesTheArchesItHas(t *testing.T) {
	v, _ := ParseTag("v0.20.1")
	a := Attestation{Version: v.Version}

	armOnly := map[string]string{AssetName(v.Version, "linux", "arm64"): strings.Repeat("2", 64)}
	files, err := Generate(v, armOnly, a, "deb")
	if err != nil {
		t.Fatalf("a scoped deb run with only the arm64 binary was refused: %v", err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0].Path, "nfpm-arm64.yaml") {
		t.Fatalf("expected exactly the arm64 nfpm config, got %v", files)
	}
	if !strings.Contains(files[0].Content, AssetName(v.Version, "linux", "arm64")) {
		t.Error("the arm64 config does not reference the arm64 binary")
	}

	// No linux binary at all must still refuse: packaging an empty payload produces an installable package
	// that installs nothing.
	none := map[string]string{AssetName(v.Version, "windows", "amd64"): strings.Repeat("5", 64)}
	if _, err := Generate(v, none, a, "deb"); err == nil {
		t.Error("a deb run with no linux binary succeeded")
	}

	// The unscoped release path still needs both arches.
	if _, err := Generate(v, armOnly, a); err == nil {
		t.Error("an unscoped Generate accepted a manifest with only one linux arch")
	}
}
