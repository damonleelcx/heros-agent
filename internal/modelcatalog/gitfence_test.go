package modelcatalog

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gitfence_test.go is internal/plancfg/gitfence_test.go's argument applied to this catalog: it carries
// PRICES (cost_per_run), so no published catalog may exist in a git-tracked file.
//
// Why a test and not a shell grep: the fence must be AUTO-DISCOVERING. A hand-maintained list of files
// to check protects only the files somebody remembered to add. This enumerates the entire git index, so
// a catalog committed under any name in any directory is caught the first time.
//
// It is also anti-vacuous: if `git ls-files` returns nothing (wrong directory, no git), it FAILS rather
// than passing over an empty set. A fence whose assertions are vacuously true stopped guarding.

var (
	reHasModels = regexp.MustCompile(`(?i)"?models"?\s*[:=]\s*\[`)
	reHasPrice  = regexp.MustCompile(`(?i)"?cost_per_run"?\s*[:=]`)
)

// dataExts are the file kinds a catalog can actually be LOADED from. Source files are excluded on
// purpose: this package's own tests embed catalogs in Go string literals, and those are fixtures rather
// than a live catalog. The rule is "no priced catalog in git", not "no test may mention a model".
var dataExts = map[string]bool{
	".json": true, ".yaml": true, ".yml": true, ".toml": true,
	".ini": true, ".cfg": true, ".conf": true, ".env": true, ".properties": true,
}

func TestNoModelCatalogIsGitTracked(t *testing.T) {
	// Rooted at the repository, not at this package's directory. `git ls-files` is relative to the
	// working directory, and a fence run from internal/modelcatalog sees the four files beside it —
	// which is how it reported ZERO candidates and would have passed vacuously the moment the
	// anti-vacuity check was softened. plancfg's equivalent roots itself the same way.
	rootOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v — this fence enumerates the index, and cannot pass without it", err)
	}
	root := strings.TrimSpace(string(rootOut))

	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v — this fence enumerates the index, and cannot pass without it", err)
	}
	files := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	checked := 0
	for _, f := range files {
		if f == "" || !dataExts[strings.ToLower(filepath.Ext(f))] {
			continue
		}
		checked++
		show := exec.Command("git", "show", ":"+f)
		show.Dir = root
		b, err := show.Output()
		if err != nil {
			continue // not in the index (deleted, or a submodule) — nothing to read
		}
		body := string(b)
		if reHasModels.MatchString(body) && reHasPrice.MatchString(body) {
			t.Errorf("%s looks like a published model catalog (a `models` list with `cost_per_run`) and "+
				"is GIT-TRACKED. This catalog carries prices and is deployment configuration: it is read "+
				"from %s and must never be committed.", f, PathEnv)
		}
	}
	if checked == 0 {
		t.Fatal("the fence examined ZERO candidate files — it is passing vacuously, which is the one " +
			"way a fence like this stops guarding without anybody noticing")
	}
	t.Logf("examined %d git-tracked data file(s)", checked)
}
