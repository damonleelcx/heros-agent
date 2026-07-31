package distribution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// targets_test.go is what makes the matrix a contract rather than a table (task 1.3). Each test below
// turns one property of the frozen matrix into a red build.

// TestTargetMatrixIsFrozen pins the exact shipped set. It exists so that adding or dropping a supported
// platform is a deliberate two-file change, reviewed as the breaking change it is — not a one-line edit
// that quietly retires a user's CI.
func TestTargetMatrixIsFrozen(t *testing.T) {
	want := []string{
		"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64",
	}
	var got []string
	for _, tt := range Shipped() {
		got = append(got, tt.Key())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("shipped target matrix changed.\n  want %v\n  got  %v\n"+
			"This is a CONTRACT change (task 1.3): update release.yml's matrix, the smoke matrix, and the "+
			"README support table in the same change, then update this test.", want, got)
	}
}

// TestDisclosedLimitsAreStatedTotally is the coverage lesson applied here: every row we do not build must
// exist AND carry both a reason and an answer. A limit with no answer sends the reader nowhere, and a
// missing row sends them to support.
func TestDisclosedLimitsAreStatedTotally(t *testing.T) {
	limits := Limits()
	if len(limits) == 0 {
		t.Fatal("no disclosed limits — the matrix claims total coverage, which is false while windows/arm64 and musl are unbuilt")
	}
	for _, l := range limits {
		if l.Limit == "" {
			t.Errorf("%s: disclosed limit with no reason", l.Key())
		}
		if l.Answer == "" {
			t.Errorf("%s: disclosed limit with no answer — a limit without a next step is an apology", l.Key())
		}
		if l.Runner != "" {
			t.Errorf("%s: a limit row names runner %q — a row nothing builds must name no builder", l.Key(), l.Runner)
		}
	}
	// The two limits the PRD discloses by name (NFR5) must both be present.
	for _, key := range []string{"windows/arm64", "linux/*"} {
		found := false
		for _, l := range limits {
			if l.Key() == key {
				found = true
			}
		}
		if !found {
			t.Errorf("PRD NFR5 discloses %s as a limit, but the matrix has no such row", key)
		}
	}
}

// TestShippedRowsNameANativeRunner enforces D1: every shipped row is built on a runner whose own OS/arch
// matches the target. A row whose runner does not match would be a cross-CGO build — the exact stability
// risk D1 refuses — and it would pass every other test in this file.
func TestShippedRowsNameANativeRunner(t *testing.T) {
	for _, tt := range Shipped() {
		if tt.Runner == "" {
			t.Errorf("%s: shipped with no runner — nothing builds it", tt.Key())
			continue
		}
		// The host is looked up, never inferred from the label's shape. `macos-15` is arm64 and
		// `macos-15-intel` is x86_64, so any suffix rule confident enough to decide that pair decides it
		// wrong — and a wrong answer here reads as "native" while producing the cross-CGO artifact D1 exists
		// to refuse.
		goos, goarch, known := RunnerHost(tt.Runner)
		if !known {
			t.Errorf("%s: runner %q is not in the reviewed runner table — its OS/arch is unknown, so nothing "+
				"here can claim the build is native (D1). Add it to runnerHosts deliberately.", tt.Key(), tt.Runner)
			continue
		}
		if goos != tt.GOOS {
			t.Errorf("%s: runner %q is a %s host — that is a cross-CGO build, which D1 refuses",
				tt.Key(), tt.Runner, goos)
		}
		if goarch != tt.GOARCH {
			t.Errorf("%s: runner %q is a %s host but the row targets %s — cross-CGO (D1)",
				tt.Key(), tt.Runner, goarch, tt.GOARCH)
		}
	}
}

// TestDarwinRowsAgreeWithTheDeploymentFloor keeps the two darwin Platform strings and MacOSFloor from
// drifting. The floor is a claim a user's Mac enforces at launch: if the rows say "macOS 12+" while the
// build pins 15.0, the binary is rejected by the OS on exactly the machines the matrix promised.
func TestDarwinRowsAgreeWithTheDeploymentFloor(t *testing.T) {
	major := strings.SplitN(MacOSFloor, ".", 2)[0]
	for _, tt := range Shipped() {
		if tt.GOOS != "darwin" {
			continue
		}
		if !strings.Contains(tt.Platform, "macOS "+major+"+") {
			t.Errorf("%s: platform %q does not state the built floor macOS %s+ (MacOSFloor=%s)",
				tt.Key(), tt.Platform, major, MacOSFloor)
		}
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", "release-cli.sh"))
	if err != nil {
		t.Skipf("release script not readable: %v", err)
	}
	// The script is where the floor is actually applied. A constant that no build reads is a comment.
	if !strings.Contains(string(b), "MACOSX_DEPLOYMENT_TARGET:-"+MacOSFloor) {
		t.Errorf("release-cli.sh does not default MACOSX_DEPLOYMENT_TARGET to %s — the darwin binaries would "+
			"take their floor from whichever runner image built them", MacOSFloor)
	}
}

// TestEveryRowHasAChannelOrAnAnswer catches the honest-looking gap: a shipped target nothing installs. A
// binary attached to a Release that no channel places on a PATH is not a delivered platform.
func TestEveryRowHasAChannelOrAnAnswer(t *testing.T) {
	for _, tt := range Shipped() {
		if len(tt.Channels) == 0 {
			t.Errorf("%s: shipped but no channel installs it", tt.Key())
		}
	}
}

// TestTargetForReturnsLimitRows is the refusal-quality test. On Alpine the honest answer is the musl row,
// not "linux/amd64 works" — and a caller can only give it if the lookup hands back limit rows.
func TestTargetForReturnsLimitRows(t *testing.T) {
	got, ok := TargetFor("windows", "arm64")
	if !ok {
		t.Fatal("windows/arm64 not found — a user on that machine would get an unnamed refusal")
	}
	if got.Support != SupportLimit || !strings.Contains(got.Answer, "emulation") {
		t.Errorf("windows/arm64 row does not carry its answer: %+v", got)
	}
	if _, ok := TargetFor("plan9", "riscv64"); ok {
		t.Error("an unknown platform resolved to a row")
	}
	// An exact arch match must beat the arch-less musl row.
	lin, _ := TargetFor("linux", "amd64")
	if lin.Support != SupportShipped {
		t.Errorf("linux/amd64 resolved to %q — the arch-less musl row shadowed a shipped target", lin.Support)
	}
}

// TestAssetNameMatchesReleaseScript holds the shell's copy of the asset name to the Go one. The name is
// constructed in both a bash script and this package; when they drift, the installer downloads a 404 and
// the failure surfaces on a user's machine rather than in CI.
func TestAssetNameMatchesReleaseScript(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "scripts", "release-cli.sh"))
	if err != nil {
		t.Skipf("release script not readable: %v", err)
	}
	// The script's shape: name="heros-${VERSION}-${GOOS_T}-${GOARCH_T}${ext}"
	if !strings.Contains(string(b), `heros-${VERSION}-${GOOS_T}-${GOARCH_T}${ext}`) {
		t.Fatalf("release-cli.sh no longer builds the asset name AssetName() reproduces; " +
			"the installer would request a name the release does not publish")
	}
	if got := AssetName("1.2.3", "windows", "amd64"); got != "heros-1.2.3-windows-amd64.exe" {
		t.Errorf("windows asset name = %q, want the .exe suffix the script adds", got)
	}
	if got := AssetName("1.2.3", "linux", "arm64"); got != "heros-1.2.3-linux-arm64" {
		t.Errorf("linux asset name = %q", got)
	}
}

// TestExpectedAssetsIsTheCompletenessSet proves the pipeline's short-set gate has something total to
// compare against — one asset per shipped row, sorted, with no limit row leaking in.
func TestExpectedAssetsIsTheCompletenessSet(t *testing.T) {
	got := ExpectedAssets("0.20.0")
	if len(got) != len(Shipped()) {
		t.Fatalf("expected %d assets, got %d: %v", len(Shipped()), len(got), got)
	}
	for _, name := range got {
		if strings.Contains(name, "*") {
			t.Errorf("limit row leaked into the expected asset set: %q", name)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("expected assets are not sorted: %v", got)
		}
	}
}

// TestMatrixVersionChangesWithTheMatrix — the version is only useful if it moves. A constant "version"
// displayed next to a table that changed is worse than no version, because it actively asserts sameness.
func TestMatrixVersionChangesWithTheMatrix(t *testing.T) {
	before := MatrixVersion()
	if !strings.HasPrefix(before, "targets-") {
		t.Fatalf("matrix version %q has no namespace prefix", before)
	}
	orig := targets
	t.Cleanup(func() { targets = orig })
	targets = append([]Target{{GOOS: "haiku", GOARCH: "amd64", Support: SupportLimit}}, orig...)
	if MatrixVersion() == before {
		t.Error("matrix version did not change when the matrix did")
	}
}
