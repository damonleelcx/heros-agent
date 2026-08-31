// Package intake turns something a person types — a local path or a GitHub link — into a Source that a
// run can be pinned to.
//
// # Why intake is its own step rather than a field on the goal
//
// Because "which repository" and "which exact bytes" are different questions, and only the second one
// makes a run re-measurable. A goal that records `github.com/acme/bot` and nothing else cannot answer
// "did the change I made help?", because the thing it measured has moved. So intake's real job is
// PINNING: resolve whatever was typed to a specific revision, and refuse when it cannot.
//
// # 🔴 Reading a repository is a real filesystem read, and it is bounded
//
// A path from a person is not a promise about what is behind it. The walk below refuses to leave the
// resolved root, skips the directories that are always noise, caps file size and file count, and never
// follows a symlink out of the tree. None of that is paranoia about the user; it is that an agent which
// can be pointed at `/` and will dutifully read it is a data-exfiltration tool with a friendly name.
package intake

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Kind is how a source was referenced.
type Kind string

const (
	// KindLocal — a directory on this machine, read in place. Nothing is copied and nothing is uploaded.
	KindLocal Kind = "local"
	// KindGitHub — a GitHub repository, cloned shallowly into a cache directory.
	KindGitHub Kind = "github"
)

// Source is a pinned, readable repository.
type Source struct {
	Kind Kind
	// Root is the absolute directory to read. For KindGitHub this is the clone, not the URL.
	Root string
	// Reference is what the person actually typed, kept verbatim so an error can quote it back.
	Reference string
	// RemoteURL is the canonical remote, empty for a local repository with no remote.
	RemoteURL string
	// Revision is the pinned commit. 🔴 Never empty on a successful resolve: an unpinned source is the
	// one thing this package exists to prevent.
	Revision string
	// Branch is the checked-out branch, empty on a detached HEAD.
	Branch string
	// Dirty reports uncommitted changes in the working tree.
	//
	// 🔴 Recorded rather than refused, and SURFACED rather than swallowed. A dirty tree means the
	// revision does not describe what was actually read, so a later re-measurement against that
	// revision compares against something that never existed. That is fatal for a run that opens a pull
	// request and merely awkward for a read-only look, so the decision belongs to the caller — but the
	// caller cannot make it if intake quietly pins HEAD and says nothing.
	Dirty bool
	// ResolvedAt is when the pin was taken.
	ResolvedAt time.Time
}

// Describe renders the source the way a person would say it.
func (s Source) Describe() string {
	name := s.RemoteURL
	if name == "" {
		name = s.Root
	}
	out := fmt.Sprintf("%s @ %s", name, short(s.Revision))
	if s.Branch != "" {
		out += " (" + s.Branch + ")"
	}
	if s.Dirty {
		out += " · uncommitted changes"
	}
	return out
}

func short(rev string) string {
	if len(rev) > 8 {
		return rev[:8]
	}
	return rev
}

var (
	ErrNotFound     = errors.New("intake: no such directory")
	ErrNotADir      = errors.New("intake: not a directory")
	ErrNotAGitRepo  = errors.New("intake: not a git repository, so there is no revision to pin")
	ErrNoRevision   = errors.New("intake: the repository has no commits, so there is nothing to pin")
	ErrBadReference = errors.New("intake: could not understand that reference")
	ErrCloneFailed  = errors.New("intake: could not clone")
)

// githubRef matches the forms a person actually types.
var githubRef = regexp.MustCompile(
	`^(?:https?://github\.com/|git@github\.com:|github\.com/)?([\w.-]+)/([\w.-]+?)(?:\.git)?/?$`)

// Resolver resolves references. CacheDir is where GitHub clones live.
type Resolver struct {
	CacheDir string
	// Timeout bounds a clone. A clone with no bound hangs the intake step, and from the outside that is
	// indistinguishable from a very large repository.
	Timeout time.Duration
}

// NewResolver builds a resolver rooted at a cache directory.
func NewResolver(cacheDir string) *Resolver {
	return &Resolver{CacheDir: cacheDir, Timeout: 3 * time.Minute}
}

// Resolve turns a reference into a pinned Source.
//
// A reference is a local path when it exists on disk, and a GitHub repository otherwise. 🔴 Existence
// decides, not syntax: `acme/bot` is a perfectly good relative directory name, and a person standing in
// a directory that contains it means that one. Guessing GitHub first would clone a stranger's
// repository because the user had a folder with a common name.
func (r *Resolver) Resolve(ref string) (Source, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Source{}, fmt.Errorf("%w: empty reference", ErrBadReference)
	}
	if expanded, ok := localPath(ref); ok {
		return r.resolveLocal(expanded, ref)
	}
	if m := githubRef.FindStringSubmatch(ref); m != nil {
		return r.resolveGitHub(m[1], m[2], ref)
	}
	return Source{}, fmt.Errorf("%w: %q is neither a directory on this machine nor a GitHub "+
		"repository. Give a path like ./my-agent, or a link like github.com/acme/bot", ErrBadReference, ref)
}

// localPath reports whether a reference names an existing directory.
func localPath(ref string) (string, bool) {
	candidate := ref
	if strings.HasPrefix(ref, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			candidate = filepath.Join(home, ref[2:])
		}
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return abs, true
}

func (r *Resolver) resolveLocal(root, ref string) (Source, error) {
	// 🔴 Resolve symlinks BEFORE anything else reads the tree. Every later containment check compares
	// against this root, and comparing against an unresolved path lets a symlinked root make every
	// subsequent "is this inside the root" check answer about a different directory than the one being
	// walked.
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Source{}, fmt.Errorf("%w: %s", ErrNotFound, root)
	}
	if !isGitRepo(real) {
		return Source{}, fmt.Errorf("%w: %s — run `git init` and commit, or point me at a repository "+
			"that has commits. Without a revision I cannot tell you later whether a change helped",
			ErrNotAGitRepo, real)
	}
	rev, err := gitOut(real, "rev-parse", "HEAD")
	if err != nil || rev == "" {
		return Source{}, fmt.Errorf("%w: %s", ErrNoRevision, real)
	}
	branch, _ := gitOut(real, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "HEAD" {
		branch = "" // detached
	}
	remote, _ := gitOut(real, "config", "--get", "remote.origin.url")
	status, _ := gitOut(real, "status", "--porcelain")

	return Source{
		Kind: KindLocal, Root: real, Reference: ref, RemoteURL: remote,
		Revision: rev, Branch: branch, Dirty: strings.TrimSpace(status) != "",
		ResolvedAt: time.Now().UTC(),
	}, nil
}

func (r *Resolver) resolveGitHub(owner, repo, ref string) (Source, error) {
	if r.CacheDir == "" {
		return Source{}, fmt.Errorf("%w: no cache directory configured", ErrCloneFailed)
	}
	dest := filepath.Join(r.CacheDir, owner, repo)
	url := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)

	if isGitRepo(dest) {
		// Refresh an existing clone rather than re-cloning. A shallow fetch, because history is not what
		// is being read and a large repository's history is most of its bytes.
		if err := gitRun(r.Timeout, dest, "fetch", "--depth", "1", "origin"); err != nil {
			return Source{}, fmt.Errorf("%w: refreshing %s: %v", ErrCloneFailed, url, err)
		}
		head, err := gitOut(dest, "rev-parse", "FETCH_HEAD")
		if err != nil {
			return Source{}, fmt.Errorf("%w: %s: %v", ErrCloneFailed, url, err)
		}
		if err := gitRun(r.Timeout, dest, "checkout", "--force", head); err != nil {
			return Source{}, fmt.Errorf("%w: %s: %v", ErrCloneFailed, url, err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return Source{}, fmt.Errorf("%w: %v", ErrCloneFailed, err)
		}
		if err := gitRun(r.Timeout, "", "clone", "--depth", "1", url, dest); err != nil {
			return Source{}, fmt.Errorf("%w: %s — check the name, and that the repository is public. "+
				"Private repositories need a connection, which is set up outside this conversation: %v",
				ErrCloneFailed, url, err)
		}
	}

	rev, err := gitOut(dest, "rev-parse", "HEAD")
	if err != nil || rev == "" {
		return Source{}, fmt.Errorf("%w: %s", ErrNoRevision, url)
	}
	branch, _ := gitOut(dest, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "HEAD" {
		branch = ""
	}
	return Source{
		Kind: KindGitHub, Root: dest, Reference: ref, RemoteURL: url,
		Revision: rev, Branch: branch, Dirty: false, ResolvedAt: time.Now().UTC(),
	}, nil
}

func isGitRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular()) // a worktree's .git is a file
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// 🚫 No credential prompts. A git subprocess that can ask for a password will block forever inside a
	// worker, holding its lease, with nobody at a terminal to answer.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func gitRun(timeout time.Duration, dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return fmt.Errorf("timed out after %s", timeout)
	}
}
