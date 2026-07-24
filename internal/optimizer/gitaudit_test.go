package optimizer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initGitRepo makes a throwaway git repo with a baseline spec file committed on main, and returns a
// GitRepo pointed at it. This is a real repo the optimizer OWNS (a temp dir) — never an upstream tree.
func initGitRepo(t *testing.T, baseline []byte) GitRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "seed@test.local")
	run("config", "user.name", "seed")
	specPath := "variant_spec.json"
	if err := os.WriteFile(filepath.Join(dir, specPath), baseline, 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", specPath)
	run("commit", "-q", "-m", "baseline")
	return GitRepo{Dir: dir, SpecPath: specPath, Branch: "main"}
}

// Section 4.5 / 8.5: merge → git revert → the live spec is byte-identical to the prior spec
// (config_hash match), against REAL git.
func TestGitRepo_MergeThenRevertRestoresPriorSpec(t *testing.T) {
	baseline := []byte(`{"variant":"baseline","model":"haiku"}`)
	repo := initGitRepo(t, baseline)
	ctx := context.Background()

	priorHash, err := repo.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if priorHash != ContentHash(baseline) {
		t.Fatalf("baseline head hash %s != content hash %s", priorHash, ContentHash(baseline))
	}

	candidate := []byte(`{"variant":"candidate","model":"sonnet-5"}`)
	pr, err := repo.OpenPR(ctx, OpenPRRequest{ProposalID: "p1", Branch: "optimizer/p1", SpecBytes: candidate})
	if err != nil {
		t.Fatal(err)
	}
	mergeCommit, err := repo.Merge(ctx, pr)
	if err != nil {
		t.Fatal(err)
	}
	afterMerge, _ := repo.Head(ctx)
	if afterMerge != ContentHash(candidate) {
		t.Fatalf("after merge, head %s != candidate hash %s", afterMerge, ContentHash(candidate))
	}

	// git revert of the merge commit reconstructs the exact prior spec.
	revertCommit, live, err := repo.Revert(ctx, mergeCommit)
	if err != nil {
		t.Fatal(err)
	}
	if revertCommit == "" {
		t.Fatal("expected a revert commit")
	}
	if live != priorHash {
		t.Fatalf("git revert did not restore the byte-identical prior spec: live %s, prior %s", live, priorHash)
	}
	got, _ := os.ReadFile(filepath.Join(repo.Dir, repo.SpecPath))
	if ContentHash(got) != ContentHash(baseline) {
		t.Fatal("reverted file bytes differ from the baseline")
	}
}

// A draft PR is never merged (the dry-run artifact).
func TestGitRepo_RefusesToMergeDraft(t *testing.T) {
	repo := initGitRepo(t, []byte(`{"v":"b"}`))
	ctx := context.Background()
	pr, err := repo.OpenPR(ctx, OpenPRRequest{ProposalID: "p", Branch: "optimizer/p", SpecBytes: []byte(`{"v":"c"}`), Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Merge(ctx, pr); err == nil {
		t.Fatal("merging a draft PR must be refused")
	}
}
