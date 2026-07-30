package distribution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// handwritten_test.go is the red-check for the D5 gate (task 2.4). A gate over a directory that does not yet
// exist would pass vacuously, so the test PLANTS the failure and requires the gate to find it.

// TestHandWrittenVersionGateGoesRed plants exactly the drift D5 forbids: a committed Homebrew formula with
// last release's version and checksum in it.
func TestHandWrittenVersionGateGoesRed(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "packaging")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	formula := `class Heros < Formula
  desc "heros CLI"
  url "https://github.com/heros-foreal/heros/releases/download/v0.19.0/heros-0.19.0-darwin-arm64"
  sha256 "deadbeef"
  version "0.19.0"
end
`
	if err := os.WriteFile(filepath.Join(pkg, "heros.rb"), []byte(formula), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := AuditNoHandWrittenVersions(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("the D5 gate did not find a committed formula carrying a hand-written version — the gate is decoration")
	}
	var versions []string
	for _, f := range found {
		versions = append(versions, f.Version)
		if !strings.Contains(f.Error(), "packaging/heros.rb") {
			t.Errorf("the finding does not name the file: %s", f.Error())
		}
	}
	if !strings.Contains(strings.Join(versions, ","), "0.19.0") {
		t.Errorf("the gate found %v but not the stale version 0.19.0", versions)
	}
}

// TestHandWrittenVersionGateAcceptsATemplate — the generator's input must be allowed to live in the
// repository. A gate that rejected the template too would leave nowhere to keep it, and the response would be
// to delete the gate.
func TestHandWrittenVersionGateAcceptsATemplate(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "packaging")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpl := `class Heros < Formula
  url "https://github.com/heros-foreal/heros/releases/download/v{{ .Version }}/{{ .AssetDarwinArm64 }}"
  sha256 "{{ .SumDarwinArm64 }}"
end
`
	if err := os.WriteFile(filepath.Join(pkg, "heros.rb.tmpl.rb"), []byte(tmpl), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := AuditNoHandWrittenVersions(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("the gate rejected a template with no resolved version: %v", found)
	}
}

// TestRepositoryCarriesNoHandWrittenPackagingVersion runs the gate over the real tree. It is the gate that
// actually protects the repository; the two tests above are what prove it can speak.
func TestRepositoryCarriesNoHandWrittenPackagingVersion(t *testing.T) {
	found, err := AuditNoHandWrittenVersions(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Errorf("%s", f.Error())
	}
}
