// Package worktree applies a generated codemod to an isolated working copy, builds it, caches the
// result, and surfaces it as a reviewable change (PRD §6 FR5b/FR5d/FR6, §8; tasks 3.4, 3.5, 3.6,
// 3.10).
//
// # The one rule everything here exists to enforce
//
// ADR-001 made the product's output an edit to the user's source code. That is high blast radius, so
// the requirement is absolute: "Transformations are applied to an isolated working copy (git
// worktree/branch), never the user's working tree in place." Every design choice below follows from
// making that structural rather than careful:
//
//   - The pool never opens the user's repository for writing. It clones it ONCE, BARE, and every
//     worktree is added from the clone. A bare repository has no working tree of its own, so there is
//     not even a checkout in the writable path to accidentally mutate — the isolation is a property
//     of the object graph, not of us remembering to be careful.
//   - Nothing in this package takes the user's repo path after NewPool. Apply cannot touch it because
//     it never learns where it is.
//
// # Why a pool at all
//
// P4 fans out over many variants of one base (PRD §7: "runs are enqueued and transforms/builds are
// pooled + cached so P4's fan-out over many variants of one base lands cleanly"). Cloning per variant
// would copy the whole repository N times; one bare clone plus N worktrees shares the object store,
// and `git clone --local` hardlinks it, so the marginal cost of a variant is a checkout rather than a
// copy.
package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Pool owns one bare clone of a target repository and hands out isolated worktrees from it.
type Pool struct {
	// mirror is the bare clone. The user's repo path is deliberately NOT retained past NewPool
	// beyond what fetch needs: see fetchFrom.
	mirror string
	root   string
	source string

	mu sync.Mutex // serializes git operations on the shared mirror
}

// Worktree is one isolated checkout on its own branch.
type Worktree struct {
	Dir    string
	Branch string
	Rev    string

	pool *Pool
}

// NewPool creates (or reuses) a bare clone of sourceRepo under root.
//
// sourceRepo is only ever read: the clone is one-directional, and nothing in this package writes to
// it. That is PRD §7's "the transform runs against a read-only clone of the target repo".
func NewPool(ctx context.Context, sourceRepo, root string) (*Pool, error) {
	src, err := filepath.Abs(sourceRepo)
	if err != nil {
		return nil, fmt.Errorf("worktree: resolve %s: %w", sourceRepo, err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("worktree: create pool root %s: %w", root, err)
	}
	p := &Pool{mirror: filepath.Join(root, "repo.git"), root: root, source: src}

	if _, err := os.Stat(p.mirror); os.IsNotExist(err) {
		// --local shares the object store by hardlink where the filesystem allows, so a second
		// variant costs a checkout rather than a copy of every object.
		if out, err := git(ctx, "", "clone", "--bare", "--local", src, p.mirror); err != nil {
			return nil, fmt.Errorf("worktree: clone %s: %w\n%s", src, err, out)
		}
	} else if err != nil {
		return nil, fmt.Errorf("worktree: stat mirror: %w", err)
	}
	return p, nil
}

// Acquire checks out an isolated worktree at rev on its own branch.
//
// The branch name is caller-supplied (the applier uses variant/<config_hash>), so a worktree is
// self-describing in `git branch` output — an operator looking at the pool can tell which
// configuration each one is.
func (p *Pool) Acquire(ctx context.Context, rev, branch string) (*Worktree, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.ensureRev(ctx, rev); err != nil {
		return nil, err
	}
	dir := filepath.Join(p.root, "wt", sanitize(branch))

	// A leftover worktree from a killed run would make `worktree add` fail on a path that already
	// exists. Clearing it is safe precisely because everything here is derived: the worktree holds no
	// state that is not reproducible from {rev, patch}.
	if _, err := os.Stat(dir); err == nil {
		if err := p.remove(ctx, dir, branch); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return nil, fmt.Errorf("worktree: create worktree parent: %w", err)
	}
	// -B resets the branch if it already exists, so a re-run of the same config_hash starts from the
	// pinned revision rather than stacking commits on a previous attempt.
	if out, err := git(ctx, p.mirror, "worktree", "add", "--force", "-B", branch, dir, rev); err != nil {
		return nil, fmt.Errorf("worktree: add %s at %s: %w\n%s", dir, rev, err, out)
	}
	return &Worktree{Dir: dir, Branch: branch, Rev: rev, pool: p}, nil
}

// ensureRev makes sure the mirror actually contains rev, fetching from the source if not.
//
// A pool outlives the clone that seeded it, so a spec targeting a commit pushed after NewPool would
// otherwise fail with git's own opaque "invalid reference". Fetching on miss — rather than on every
// Acquire — keeps the common fan-out case (many variants, one revision) from paying for a network
// round trip per variant.
func (p *Pool) ensureRev(ctx context.Context, rev string) error {
	if _, err := git(ctx, p.mirror, "cat-file", "-e", rev+"^{commit}"); err == nil {
		return nil
	}
	if out, err := git(ctx, p.mirror, "fetch", "--prune", p.source, "+refs/heads/*:refs/heads/*"); err != nil {
		return fmt.Errorf("worktree: fetch %s to find %s: %w\n%s", p.source, rev, err, out)
	}
	if out, err := git(ctx, p.mirror, "cat-file", "-e", rev+"^{commit}"); err != nil {
		return fmt.Errorf("worktree: %s does not contain revision %s (even after fetching): %w\n%s",
			p.source, rev, err, out)
	}
	return nil
}

// Release removes a worktree and its branch.
func (w *Worktree) Release(ctx context.Context) error {
	if w == nil || w.pool == nil {
		return nil
	}
	w.pool.mu.Lock()
	defer w.pool.mu.Unlock()
	return w.pool.remove(ctx, w.Dir, w.Branch)
}

func (p *Pool) remove(ctx context.Context, dir, branch string) error {
	if out, err := git(ctx, p.mirror, "worktree", "remove", "--force", dir); err != nil {
		// Fall back to deleting the directory and pruning the registration: a worktree git has lost
		// track of must not wedge the pool forever.
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			return fmt.Errorf("worktree: remove %s: %w\n%s", dir, err, out)
		}
		_, _ = git(ctx, p.mirror, "worktree", "prune")
	}
	_, _ = git(ctx, p.mirror, "branch", "-D", branch) // best effort: an absent branch is not an error
	return nil
}

// Mirror returns the bare clone's path (for tests and diagnostics).
func (p *Pool) Mirror() string { return p.mirror }

// git runs a git command, returning combined output. dir is the repository (-C); "" runs it wherever
// the process is, which only `clone` needs.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	full := args
	if dir != "" {
		full = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	// A hermetic environment: git config from the developer's home directory (hooks, gpg signing,
	// commit.template, an editor) would otherwise change what the pool does from machine to machine,
	// and task 2.3's byte-identical promise does not survive a signing key appearing in a commit.
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+os.TempDir(), "XDG_CONFIG_HOME="+os.TempDir(),
		"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// sanitize turns a branch name into a safe single path segment.
func sanitize(s string) string {
	r := strings.NewReplacer("/", "-", "\\", "-", "..", "-", " ", "-")
	return r.Replace(s)
}
