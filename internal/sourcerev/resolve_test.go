package sourcerev

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A fence nobody has seen go red is decoration, and this one guards a claim rather than a crash: the
// failure it prevents is a demo whose printed source_revision names a tree it never parsed. That is
// invisible by construction — the run succeeds, the numbers look fine, and the provenance is wrong.

// tinyRepo builds a real two-commit git repository and returns its path plus both SHAs, newest last.
func tinyRepo(t *testing.T) (dir string, first, head string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("one")
	run("add", "-A")
	run("commit", "-q", "-m", "one")
	first = run("rev-parse", "HEAD")
	write("two")
	run("add", "-A")
	run("commit", "-q", "-m", "two")
	head = run("rev-parse", "HEAD")
	return dir, first, head
}

func TestResolveUsesHeadWhenNoPinIsDeclared(t *testing.T) {
	dir, _, head := tinyRepo(t)
	sha, note, err := Resolve(dir, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if sha != head {
		t.Errorf("sha = %s, want HEAD %s", sha, head)
	}
	// The note is load-bearing: an unpinned run must SAY it is unpinned, or a reader assumes the SHA was
	// a deliberate choice and quotes it as one.
	if !strings.Contains(note, "no pin") {
		t.Errorf("an unpinned resolution must say so, got %q", note)
	}
}

func TestResolveVerifiesAMatchingPin(t *testing.T) {
	dir, _, head := tinyRepo(t)
	sha, note, err := Resolve(dir, head)
	if err != nil {
		t.Fatalf("a pin the checkout is at must resolve: %v", err)
	}
	if sha != head {
		t.Errorf("sha = %s, want %s", sha, head)
	}
	if !strings.Contains(note, "verified") {
		t.Errorf("a matching pin must report that it was verified, got %q", note)
	}
}

// 🔴 The defect this whole package exists for: the checkout is at a DIFFERENT commit than the pin.
// Silently using either one is wrong — the pin is a false provenance, HEAD silently discards a
// deliberate pin — so it refuses and names both.
func TestResolveRefusesAPinTheCheckoutIsNotAt(t *testing.T) {
	dir, first, head := tinyRepo(t)
	_, _, err := Resolve(dir, first)
	if err == nil {
		t.Fatal("a pin the checkout is not at must be refused; labelling the IR with it would claim a " +
			"provenance nothing can check")
	}
	for _, want := range []string{first[:12], head[:12], "checkout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %s so a reader can act on it; got: %v", want, err)
		}
	}
	// The pinned commit IS in this clone, so the actionable fix is a checkout — the message must say that
	// rather than send the reader to fetch something they already have.
	if !strings.Contains(err.Error(), "checkout") || !strings.Contains(err.Error(), "IS in the clone") {
		t.Errorf("the refusal must distinguish 'wrong commit checked out' from 'commit missing': %v", err)
	}
}

// The other half: a pin the clone does not hold at all (the shallow-clone case, and the one that
// actually happened to cmd/proof/promptmodel). Its fix is different, so its message must be too.
func TestResolveRefusesAPinTheCloneDoesNotHave(t *testing.T) {
	dir, _, _ := tinyRepo(t)
	absent := strings.Repeat("a", 40)
	_, _, err := Resolve(dir, absent)
	if err == nil {
		t.Fatal("a pin that is not in the clone must be refused")
	}
	if !strings.Contains(err.Error(), "NOT in this clone") {
		t.Errorf("the refusal must say the commit is absent, not that the wrong one is checked out: %v", err)
	}
	if strings.Contains(err.Error(), "IS in the clone") {
		t.Errorf("the two refusal cases were conflated: %v", err)
	}
}

func TestResolveRefusesANonRepository(t *testing.T) {
	if _, _, err := Resolve(t.TempDir(), ""); err == nil {
		t.Fatal("a directory that is not a git checkout must fail rather than yield an empty revision")
	}
}
