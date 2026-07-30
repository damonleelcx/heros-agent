package distribution

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// handwritten.go is the second half of task 2.4 and the enforcement of D5: no packaging manifest may carry a
// hand-written version.
//
// # The failure this prevents
//
// A Homebrew formula, a Scoop manifest, a winget manifest and an nfpm config each contain a version and a
// checksum. If any of them is committed to the repository, then the repository holds a SECOND copy of the
// version — and second copies drift in one direction: someone bumps the tag, the release ships, and the tap
// still points at last release's URL and last release's sha256. The user who runs `brew install` gets the
// old binary, `heros version` disagrees with the Release page, and the bug report is filed against a version
// nobody shipped.
//
// So the rule is structural rather than procedural: these files are GENERATED into the release directory at
// release time and are never committed. A committed one is a failure, and this check is what makes it one.
//
// # Why a version literal, specifically
//
// A template is welcome in the repository — that is how the generator works. What is forbidden is a
// *resolved* version inside a packaging file, because that is the copy that can be stale. A template
// containing `{{ .Version }}` has nothing to drift.

// packagingFile matches the file shapes a package manager consumes.
var packagingFile = regexp.MustCompile(`(?i)(\.rb|\.rockspec|nfpm.*\.ya?ml|.*scoop.*\.json|.*winget.*\.ya?ml|.*\.installer\.ya?ml)$`)

// versionLiteral matches a resolved semver: 1.2.3 with an optional prerelease. Deliberately not anchored, so
// it finds a version embedded in a URL — which is exactly where a stale formula hides one.
var versionLiteral = regexp.MustCompile(`\b\d+\.\d+\.\d+(-[0-9A-Za-z.\-]+)?\b`)

// HandWrittenVersion is one packaging file found carrying a resolved version.
type HandWrittenVersion struct {
	Path    string
	Line    int
	Version string
	Text    string
}

func (h HandWrittenVersion) Error() string {
	return fmt.Sprintf("%s:%d carries the hand-written version %q — packaging manifests are generated from "+
		"the tag (D5); a committed copy drifts to last release's URL and checksum: %s",
		h.Path, h.Line, h.Version, h.Text)
}

// AuditNoHandWrittenVersions walks root for packaging-manifest-shaped files carrying a resolved version.
//
// skipDirs keeps it away from the places a version legitimately appears in a filename or a vendored file:
// the module cache, node_modules, the build output, and git internals. It deliberately does NOT skip
// `packaging/` — that directory is exactly what must stay template-only.
func AuditNoHandWrittenVersions(root string) ([]HandWrittenVersion, error) {
	var found []HandWrittenVersion
	// The skipped directories are the ones whose contents are NOT committed: the module cache, build output, and
	// the install-smoke staging area. The rule this audit enforces is "no COMMITTED packaging manifest carries a
	// resolved version", and a generated manifest inside a gitignored build directory is exactly what the
	// generator is supposed to produce — flagging it would make the correct behaviour red and the fix would be to
	// delete the audit.
	//
	// 🔴 It deliberately does NOT skip `packaging/`. That directory must stay template-only, and it is the one
	// place a hand-written formula would actually be committed.
	skip := map[string]bool{
		".git": true, "node_modules": true, "dist": true, "vendor": true,
		".next": true, "tmp": true, ".parity": true, ".smoke": true,
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable directory cannot hide a committed file from git
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !packagingFile.MatchString(d.Name()) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(b), "\n") {
			// A template placeholder on the line is the sanctioned form.
			if strings.Contains(line, "{{") {
				continue
			}
			if m := versionLiteral.FindString(line); m != "" {
				found = append(found, HandWrittenVersion{
					Path: rel, Line: i + 1, Version: m, Text: strings.TrimSpace(line),
				})
			}
		}
		return nil
	})
	sort.Slice(found, func(i, j int) bool {
		if found[i].Path != found[j].Path {
			return found[i].Path < found[j].Path
		}
		return found[i].Line < found[j].Line
	})
	return found, err
}

// GateManifests turns audit findings into release-gate failures, so "a manifest carries a hand-written
// version" refuses a release through the same reporting path as an incomplete matrix (task 2.4).
//
// It is a separate function from Gate because Gate is pure — it decides from values it was handed — while
// this one's input comes from walking a filesystem. Keeping the walk out of Gate is what lets every gate
// rule be tested without a directory.
func GateManifests(found []HandWrittenVersion) []GateFailure {
	var fails []GateFailure
	for _, f := range found {
		fails = append(fails, GateFailure{"no-hand-written-version", f.Error()})
	}
	return fails
}
