package optimizer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// PRRef identifies a pull request the loop opened. Per ADR-001 "apply" delivers a reviewable PR; the
// loop opens one and — under the hard constraints, every gate green — merges it. A draft PR is the
// dry-run artifact (opened, never merged).
type PRRef struct {
	ProposalID   string `json:"proposal_id"`
	Branch       string `json:"branch"`
	Draft        bool   `json:"draft"`
	URL          string `json:"url"`
	ToConfigHash string `json:"to_config_hash"`
}

// OpenPRRequest is the candidate to open a PR for: the branch to cut, the candidate Variant Spec bytes
// that become the live spec on merge, and whether it is a draft (dry-run).
type OpenPRRequest struct {
	ProposalID string
	Branch     string
	SpecBytes  []byte
	Draft      bool
	Message    string
}

// Repo is the operational substrate the merge path depends on: it delivers the audit trail's "what"
// (git history) and the rollback (`git revert`). Making it an interface lets the loop's decision logic
// be proven with FakeRepo, while the shipped path (GitRepo) runs real git on the target workflow repo.
//
// The loop NEVER touches the user's working tree in place — every candidate is a branch + PR, and a
// merge is a merge commit, so "what is live now" and "what was live at iteration k" are exact and a
// rollback is a `git revert` of the merge commit (design "Data model sketch").
type Repo interface {
	// Head returns the config_hash of the currently-live (merged) Variant Spec.
	Head(ctx context.Context) (string, error)
	// OpenPR cuts a branch carrying the candidate spec and opens a PR (draft when req.Draft). It does not
	// merge — a draft PR is exactly the dry-run artifact.
	OpenPR(ctx context.Context, req OpenPRRequest) (PRRef, error)
	// Merge merges an open PR, producing a merge commit recorded in git history. After Merge, Head
	// returns the candidate's config_hash.
	Merge(ctx context.Context, pr PRRef) (mergeCommit string, err error)
	// Revert reverts a merge commit via `git revert`, reconstructing the exact prior Variant Spec. It
	// returns the revert commit and the resulting live config_hash (which equals the pre-merge hash).
	Revert(ctx context.Context, mergeCommit string) (revertCommit string, headConfigHash string, err error)
}

// ── GitRepo: the real, git-backed substrate ─────────────────────────────────────────────────────────

// GitRepo is a Repo backed by an actual git repository on disk (the target workflow's checkout). The
// live Variant Spec is a single content-addressed file (SpecPath); its config_hash is the file's
// content hash, so a `git revert` that restores the file's bytes restores the exact config_hash — the
// property task 4.5 / 8.5 assert against real git.
//
// It is used by the demo and the end-to-end run against a real repo; it is the shipped code, not a
// stub. Tests that only exercise loop DECISION logic use FakeRepo instead.
type GitRepo struct {
	// Dir is the repository root the loop owns (a clone the optimizer operates on — never an upstream
	// working tree it does not own).
	Dir string
	// SpecPath is the repo-relative path of the live Variant Spec file.
	SpecPath string
	// Branch is the integration branch merges land on (default "main").
	Branch string
	// Author identity for the loop's commits (git requires one). The loop is a non-human actor, so it
	// names itself explicitly in every commit it authors.
	AuthorName  string
	AuthorEmail string
}

func (g GitRepo) branch() string {
	if g.Branch != "" {
		return g.Branch
	}
	return "main"
}

func (g GitRepo) authorName() string {
	if g.AuthorName != "" {
		return g.AuthorName
	}
	return "heros-optimizer"
}

func (g GitRepo) authorEmail() string {
	if g.AuthorEmail != "" {
		return g.AuthorEmail
	}
	return "optimizer@heros.local"
}

// git runs a git command in the repo dir with the loop's identity pinned, returning stdout. A non-zero
// exit is a real error surfaced with the combined output — a failed merge/revert must NOT be swallowed
// (the loop treats it as "cannot merge" / "cannot revert" and fails closed).
func (g GitRepo) git(ctx context.Context, args ...string) (string, error) {
	full := append([]string{
		"-c", "user.name=" + g.authorName(),
		"-c", "user.email=" + g.authorEmail(),
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = g.Dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// Head returns the content hash of the live spec file at HEAD of the integration branch.
func (g GitRepo) Head(ctx context.Context) (string, error) {
	if _, err := g.git(ctx, "checkout", g.branch()); err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(g.Dir, g.SpecPath))
	if err != nil {
		return "", fmt.Errorf("read live spec: %w", err)
	}
	return ContentHash(b), nil
}

// OpenPR cuts req.Branch from the integration branch, writes the candidate spec, and commits it. The
// branch IS the PR (a local-git stand-in for a forge PR, the same posture P5.5's OpenPR uses). It does
// not merge.
func (g GitRepo) OpenPR(ctx context.Context, req OpenPRRequest) (PRRef, error) {
	if _, err := g.git(ctx, "checkout", g.branch()); err != nil {
		return PRRef{}, err
	}
	// Recreate the branch cleanly so an identical candidate is byte-reproducible.
	_, _ = g.git(ctx, "branch", "-D", req.Branch)
	if _, err := g.git(ctx, "checkout", "-b", req.Branch); err != nil {
		return PRRef{}, err
	}
	if err := os.WriteFile(filepath.Join(g.Dir, g.SpecPath), req.SpecBytes, 0o644); err != nil {
		return PRRef{}, fmt.Errorf("write candidate spec: %w", err)
	}
	if _, err := g.git(ctx, "add", "--", g.SpecPath); err != nil {
		return PRRef{}, err
	}
	msg := req.Message
	if msg == "" {
		msg = "optimizer: candidate " + req.ProposalID
	}
	if _, err := g.git(ctx, "commit", "-m", msg); err != nil {
		return PRRef{}, err
	}
	// Return to the integration branch; the PR is the open branch.
	if _, err := g.git(ctx, "checkout", g.branch()); err != nil {
		return PRRef{}, err
	}
	return PRRef{
		ProposalID:   req.ProposalID,
		Branch:       req.Branch,
		Draft:        req.Draft,
		URL:          "(local) git show " + req.Branch,
		ToConfigHash: ContentHash(req.SpecBytes),
	}, nil
}

// Merge merges the PR branch into the integration branch with a merge commit (--no-ff), so the merge is
// a distinct commit in git history that a `git revert` can target.
func (g GitRepo) Merge(ctx context.Context, pr PRRef) (string, error) {
	if pr.Draft {
		return "", fmt.Errorf("optimizer: refusing to merge a draft PR (%s)", pr.Branch)
	}
	if _, err := g.git(ctx, "checkout", g.branch()); err != nil {
		return "", err
	}
	if _, err := g.git(ctx, "merge", "--no-ff", "--no-edit", "-m", "optimizer merge "+pr.ProposalID, pr.Branch); err != nil {
		return "", err
	}
	return g.git(ctx, "rev-parse", "HEAD")
}

// Revert reverts the merge commit with `git revert -m 1`, reconstructing the exact prior spec, and
// returns the revert commit plus the resulting live config_hash.
//
// Depth (design Q7): reverting the LATEST merge always applies cleanly. Reverting an OLDER merge while
// a newer merge sits on top can conflict (git cannot know two merges touched disjoint parts of the
// file); on that conflict we `git revert --abort` so the repo is left un-mutated (last-good still live)
// and return a clear error, rather than leaving a half-applied revert — the reversibility guarantee
// must not itself corrupt the tree.
func (g GitRepo) Revert(ctx context.Context, mergeCommit string) (string, string, error) {
	if _, err := g.git(ctx, "checkout", g.branch()); err != nil {
		return "", "", err
	}
	if _, err := g.git(ctx, "revert", "--no-edit", "-m", "1", mergeCommit); err != nil {
		// Clean up a conflicted revert so the last-good spec stays live (never leave a mid-revert tree).
		_, _ = g.git(ctx, "revert", "--abort")
		return "", "", fmt.Errorf("%w (a later merge may conflict with reverting this one — revert the most recent merge, or revert the newer merge first; design Q7)", err)
	}
	revertCommit, err := g.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	head, err := g.Head(ctx)
	if err != nil {
		return "", "", err
	}
	return revertCommit, head, nil
}

// ── FakeRepo: an in-memory Repo for loop-decision tests ─────────────────────────────────────────────

// FakeRepo models the git substrate in memory: a stack of merged config hashes and a merge-commit →
// prior-hash map so Revert restores the byte-identical prior spec. It lets the loop's stopping/halt/
// stall/apply logic be tested without spawning git, while GitRepo proves the real thing separately.
type FakeRepo struct {
	mu           sync.Mutex
	head         string
	specByHash   map[string][]byte
	merges       map[string]string // merge commit → prior config hash (what a revert restores)
	nextMerge    int
	nextRevert   int
	openBranches map[string]PRRef
	// FailMerge / FailRevert force the corresponding op to error, for recovery/failure tests.
	FailMerge  bool
	FailRevert bool

	// opened / merged / lastDraft are observation counters. A test that asserts only on the loop's own
	// RunResult cannot tell "did not merge" apart from "silently dropped the candidate" — both leave
	// zero merges. These record what the repository was actually ASKED to do, which is the difference.
	opened    int
	merged    int
	lastDraft bool
}

// Opened is how many pull requests were opened against this repo.
func (r *FakeRepo) Opened() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.opened
}

// Merged is how many merges the repo actually performed.
func (r *FakeRepo) Merged() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.merged
}

// LastPRWasDraft reports whether the most recently opened PR was a draft.
func (r *FakeRepo) LastPRWasDraft() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastDraft
}

// NewFakeRepo builds a fake repo whose live spec is the given baseline bytes.
func NewFakeRepo(baseline []byte) *FakeRepo {
	h := ContentHash(baseline)
	return &FakeRepo{
		head:         h,
		specByHash:   map[string][]byte{h: append([]byte(nil), baseline...)},
		merges:       map[string]string{},
		openBranches: map[string]PRRef{},
	}
}

func (r *FakeRepo) Head(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.head, nil
}

func (r *FakeRepo) OpenPR(_ context.Context, req OpenPRRequest) (PRRef, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := ContentHash(req.SpecBytes)
	r.specByHash[h] = append([]byte(nil), req.SpecBytes...)
	pr := PRRef{ProposalID: req.ProposalID, Branch: req.Branch, Draft: req.Draft,
		URL: "(fake) " + req.Branch, ToConfigHash: h}
	r.openBranches[req.Branch] = pr
	r.opened++
	r.lastDraft = req.Draft
	return pr, nil
}

func (r *FakeRepo) Merge(_ context.Context, pr PRRef) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pr.Draft {
		return "", fmt.Errorf("optimizer: refusing to merge a draft PR (%s)", pr.Branch)
	}
	if r.FailMerge {
		return "", fmt.Errorf("optimizer: simulated merge failure for %s", pr.Branch)
	}
	prior := r.head
	r.nextMerge++
	r.merged++
	commit := fmt.Sprintf("merge%03d", r.nextMerge)
	r.merges[commit] = prior
	r.head = pr.ToConfigHash
	return commit, nil
}

func (r *FakeRepo) Revert(_ context.Context, mergeCommit string) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.FailRevert {
		return "", "", fmt.Errorf("optimizer: simulated revert failure for %s", mergeCommit)
	}
	prior, ok := r.merges[mergeCommit]
	if !ok {
		return "", "", fmt.Errorf("optimizer: unknown merge commit %s", mergeCommit)
	}
	r.head = prior
	r.nextRevert++
	return fmt.Sprintf("revert%03d", r.nextRevert), prior, nil
}
