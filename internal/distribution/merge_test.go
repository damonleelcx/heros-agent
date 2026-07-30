package distribution

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// merge_test.go proves the merge step's integrity properties (task 2.2) and that the release gate can
// actually go red (task 2.5). A gate never shown to reject is treated as absent.

// runnerBundle writes the artifact layout actions/download-artifact produces: one directory per matrix
// job, holding that job's binary and its own SHA256SUMS.
func runnerBundle(t *testing.T, root, job, asset, content string) {
	t.Helper()
	dir := filepath.Join(root, job)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, asset), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	line := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset)
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fullRelease lays down every shipped target for a version, so the completeness gate is satisfied.
func fullRelease(t *testing.T, root, version string) {
	t.Helper()
	for _, tt := range Shipped() {
		name := AssetName(version, tt.GOOS, tt.GOARCH)
		runnerBundle(t, root, "build-"+tt.GOOS+"-"+tt.GOARCH, name, "binary bytes for "+name)
	}
}

// TestMergeProducesOneSortedManifest — the merged manifest must be a function of its contents, not of the
// order five parallel jobs finished in, or the signature over it is noise.
func TestMergeProducesOneSortedManifest(t *testing.T) {
	root := t.TempDir()
	fullRelease(t, root, "0.20.0")
	arts, err := CollectRunnerArtifacts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != len(Shipped()) {
		t.Fatalf("collected %d artifacts, want %d", len(arts), len(Shipped()))
	}
	m, err := Merge(arts)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(m.Text), "\n")
	if len(lines) != len(Shipped()) {
		t.Fatalf("manifest has %d lines, want %d", len(lines), len(Shipped()))
	}
	// Sorted BY NAME — the second field. Comparing whole lines would compare hashes, which are sorted
	// by nothing.
	for i := 1; i < len(lines); i++ {
		prev := strings.Fields(lines[i-1])[1]
		cur := strings.Fields(lines[i])[1]
		if prev >= cur {
			t.Errorf("manifest is not sorted by name:\n%s", m.Text)
			break
		}
	}
	// Reversing the collection order must not change one byte of the signed document.
	rev := make([]Artifact, len(arts))
	for i := range arts {
		rev[i] = arts[len(arts)-1-i]
	}
	m2, err := Merge(rev)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Text != m.Text {
		t.Error("the merged manifest depends on artifact order — two identical releases would have different signatures")
	}
}

// TestMergeCatchesAnArtifactChangedInTransit is the reason the merge recomputes instead of concatenating.
// Without it, a corrupted upload produces a perfectly signed manifest describing bytes nobody has.
func TestMergeCatchesAnArtifactChangedInTransit(t *testing.T) {
	root := t.TempDir()
	fullRelease(t, root, "0.20.0")
	target := Shipped()[0]
	name := AssetName("0.20.0", target.GOOS, target.GOARCH)
	path := filepath.Join(root, "build-"+target.GOOS+"-"+target.GOARCH, name)
	if err := os.WriteFile(path, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	arts, err := CollectRunnerArtifacts(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Merge(arts)
	if err == nil {
		t.Fatal("merge accepted an artifact whose bytes no longer match the checksum its builder recorded")
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("the refusal does not name the file: %v", err)
	}
}

// TestMergeRefusesUncheckableAndEmptyInput — the two silent-success shapes. An empty manifest signs and
// verifies perfectly while attesting to nothing; a zero-byte binary does the same and fails on the user's
// machine.
func TestMergeRefusesUncheckableAndEmptyInput(t *testing.T) {
	if _, err := Merge(nil); err == nil {
		t.Error("merge signed an empty manifest")
	}
	root := t.TempDir()
	name := AssetName("0.20.0", "linux", "amd64")
	// A bundle with a binary but NO per-runner SHA256SUMS: nothing to cross-check against.
	dir := filepath.Join(root, "build-linux-amd64")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	arts, err := CollectRunnerArtifacts(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(arts); err == nil || !strings.Contains(err.Error(), "cross-check") {
		t.Errorf("merge accepted an artifact with no travelling checksum: %v", err)
	}
	// A zero-byte binary.
	runnerBundle(t, root, "build-empty", AssetName("0.20.0", "darwin", "arm64"), "")
	arts, err = CollectRunnerArtifacts(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(arts); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("merge accepted a zero-byte binary: %v", err)
	}
}

// TestCollectRefusesOverlappingMatrixRows — two jobs producing the same asset name means the matrix has an
// overlapping row, and a last-writer-wins merge would publish whichever job finished second.
func TestCollectRefusesOverlappingMatrixRows(t *testing.T) {
	root := t.TempDir()
	name := AssetName("0.20.0", "linux", "amd64")
	runnerBundle(t, root, "job-a", name, "from a")
	runnerBundle(t, root, "job-b", name, "from b")
	if _, err := CollectRunnerArtifacts(root); err == nil {
		t.Fatal("collect accepted the same asset from two jobs")
	}
}

// TestReleaseGateGoesRed is the red-check on the gate itself (task 2.5). Each subtest breaks exactly one
// rule and asserts the gate names that rule — a gate that only ever passes is indistinguishable from none.
func TestReleaseGateGoesRed(t *testing.T) {
	v, err := ParseTag("v0.20.0")
	if err != nil {
		t.Fatal(err)
	}
	good := Attestation{
		Version: "0.20.0", Assets: ExpectedAssets("0.20.0"),
		SignedManifest: true, SigningKeyID: "heros-release-2026a",
	}
	if fails := Gate(v, good, Repro{Verified: true}); len(fails) != 0 {
		t.Fatalf("a complete, signed, reproducible release was refused: %v", fails)
	}

	named := func(t *testing.T, fails []GateFailure, gate string) {
		t.Helper()
		for _, f := range fails {
			if f.Gate == gate {
				return
			}
		}
		t.Errorf("gate %q did not fire; failures were %v", gate, fails)
	}

	t.Run("incomplete matrix", func(t *testing.T) {
		a := good
		a.Assets = good.Assets[1:]
		named(t, Gate(v, a, Repro{Verified: true}), "matrix-complete")
	})
	t.Run("unsigned on a non-dev channel", func(t *testing.T) {
		a := good
		a.SignedManifest, a.SigningKeyID = false, ""
		named(t, Gate(v, a, Repro{Verified: true}), "manifest-signed")
	})
	t.Run("reproducibility regression", func(t *testing.T) {
		named(t, Gate(v, good, Repro{Missing: []string{"linux/arm64"}}), "reproducible-build")
	})
	t.Run("version drift from the tag", func(t *testing.T) {
		a := good
		a.Version = "0.20.1"
		named(t, Gate(v, a, Repro{Verified: true}), "version-single-source")
	})
	t.Run("dev channel tolerates an unsigned manifest", func(t *testing.T) {
		dev := DevVersion()
		a := Attestation{Version: dev.Version, Assets: ExpectedAssets(dev.Version)}
		for _, f := range Gate(dev, a, Repro{Verified: true}) {
			if f.Gate == "manifest-signed" {
				t.Error("the dev channel demanded a signature — local builds would need the release key")
			}
		}
	})
	t.Run("report shows every failure at once", func(t *testing.T) {
		a := Attestation{Version: "9.9.9"}
		fails := Gate(v, a, Repro{})
		if len(fails) < 3 {
			t.Fatalf("expected several failures, got %v", fails)
		}
		report := GateReport(v, fails)
		for _, f := range fails {
			if !strings.Contains(report, f.Gate) {
				t.Errorf("report omits gate %q:\n%s", f.Gate, report)
			}
		}
		if !strings.Contains(report, "Nothing is published") {
			t.Errorf("report does not state that nothing published:\n%s", report)
		}
	})
	t.Run("a passing gate is visible", func(t *testing.T) {
		if r := GateReport(v, nil); !strings.Contains(r, "✅") {
			t.Errorf("a passing gate renders silently: %q", r)
		}
	})
}
