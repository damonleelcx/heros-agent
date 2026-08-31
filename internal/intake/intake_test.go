package intake

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitRepo builds a real git repository with one commit. A real repository rather than a stub directory,
// because what intake does is READ GIT — a fake that satisfies isGitRepo would test nothing.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "agent.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "first")
	return dir
}

// TestALocalRepositoryIsPinnedToARevision. An unpinned source is the one thing this package exists to
// prevent: without a revision, "did my change help?" compares against something that has moved.
func TestALocalRepositoryIsPinnedToARevision(t *testing.T) {
	dir := gitRepo(t)
	src, err := NewResolver(t.TempDir()).Resolve(dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if src.Revision == "" {
		t.Fatal("resolved a source with no revision")
	}
	if len(src.Revision) < 40 {
		t.Errorf("revision %q is not a full commit id", src.Revision)
	}
	if src.Kind != KindLocal {
		t.Errorf("kind = %s", src.Kind)
	}
	if src.Branch != "main" {
		t.Errorf("branch = %q, want main", src.Branch)
	}
	if src.Dirty {
		t.Error("a clean tree was reported dirty")
	}
}

// TestADirtyTreeIsRecordedRatherThanIgnored. A dirty tree means the revision does not describe what was
// read, so a later re-measurement compares against something that never existed. The caller decides what
// to do — but it cannot decide if intake stays silent.
func TestADirtyTreeIsRecordedRatherThanIgnored(t *testing.T) {
	dir := gitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "agent.py"), []byte("x = 2  # edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := NewResolver(t.TempDir()).Resolve(dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !src.Dirty {
		t.Fatal("uncommitted changes were not reported; a run pinned to this revision would be " +
			"re-measured against code that never existed")
	}
	if !contains(src.Describe(), "uncommitted") {
		t.Errorf("Describe() hides the dirty state: %q", src.Describe())
	}
}

// TestARepositoryWithoutGitIsRefused, with a reason that says what to do.
func TestARepositoryWithoutGitIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agent.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewResolver(t.TempDir()).Resolve(dir)
	if !errors.Is(err, ErrNotAGitRepo) {
		t.Fatalf("a directory with no git was accepted: %v", err)
	}
	if !contains(err.Error(), "git init") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}
}

// TestAnExistingDirectoryBeatsGitHubSyntax.
//
// 🔴 `acme/bot` is both a valid GitHub reference and a perfectly good relative directory name. A person
// standing in a directory that contains it means that one — and guessing GitHub first would clone a
// stranger's repository because the user had a folder with a common name.
func TestAnExistingDirectoryBeatsGitHubSyntax(t *testing.T) {
	parent := t.TempDir()
	nested := filepath.Join(parent, "acme", "bot")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	// Make it a real repo so resolution can succeed.
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = nested
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(nested, "a.py"), []byte("x=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "c"}} {
		c := exec.Command("git", args...)
		c.Dir = nested
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.invalid",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.invalid")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	cwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}
	src, err := NewResolver(t.TempDir()).Resolve("acme/bot")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if src.Kind != KindLocal {
		t.Fatalf("kind = %s — a local directory was treated as a GitHub reference, which would clone "+
			"a stranger's repository because the user had a folder with a common name", src.Kind)
	}
}

// TestGitHubFormsAreRecognised. Parsing only — no network.
func TestGitHubFormsAreRecognised(t *testing.T) {
	for _, ref := range []string{
		"https://github.com/acme/bot",
		"https://github.com/acme/bot.git",
		"github.com/acme/bot",
		"git@github.com:acme/bot.git",
		"acme/bot",
	} {
		m := githubRef.FindStringSubmatch(ref)
		if m == nil {
			t.Errorf("%q was not recognised as a GitHub reference", ref)
			continue
		}
		if m[1] != "acme" || m[2] != "bot" {
			t.Errorf("%q parsed as %q/%q", ref, m[1], m[2])
		}
	}
	for _, bad := range []string{"", "  ", "https://gitlab.com/acme/bot", "not a ref at all"} {
		if githubRef.MatchString(bad) && bad != "" {
			t.Errorf("%q was accepted as a GitHub reference", bad)
		}
	}
}

// TestAnUnresolvableReferenceSaysWhatToTypeInstead.
func TestAnUnresolvableReferenceSaysWhatToTypeInstead(t *testing.T) {
	_, err := NewResolver(t.TempDir()).Resolve("this is not a repository")
	if !errors.Is(err, ErrBadReference) {
		t.Fatalf("got %v", err)
	}
	if !contains(err.Error(), "github.com") {
		t.Errorf("the error does not show an example of what would work: %v", err)
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
