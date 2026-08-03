package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// sourcebundle.go builds the snapshot `heros push-source` transmits.
//
// # `git archive`, and why not a directory walk
//
// The bundle is produced by `git archive <revision>`, which emits a tar of exactly the tree git has at
// that commit. A hand-rolled filepath.Walk was the obvious alternative and is the wrong one, for reasons
// that are about what ends up on the wire rather than about effort:
//
//   - A walk sends the WORKING DIRECTORY, which is not any revision. The snapshot would be labelled with
//     a commit whose content it does not contain — and every graph discovered from it would be attributed
//     to code that was never at that sha.
//   - A walk needs an exclusion list, and an exclusion list is a denylist. `.env`, `.env.local`,
//     `secrets.yaml`, a stray `id_rsa`, the AWS credentials file someone once copied into the repo to
//     debug something — each is excluded only if somebody thought of it. `git archive` sends tracked
//     files, so anything gitignored (which is where secrets live, precisely because they are gitignored)
//     is absent by construction rather than by enumeration.
//   - `.git/` itself would have to be excluded by hand, and it contains every revision of everything.
//
// 🔴 So this REFUSES rather than falling back when git is unavailable or the revision is unknown. A
// fallback path here would be the denylist walk, reached exactly when the safe mechanism is missing —
// which is the worst possible time to be less careful.
//
// The honest residue, stated because it is real: a secret that is COMMITTED is tracked, and this will
// send it. That is not a gap this bundler can close — the file is in the customer's history and in every
// clone of their repository already — but it is a fact worth knowing before pushing, which is why
// BundleStats reports the file count and size and the command prints them before transmitting.

// BundleStats describes a built snapshot, for the line printed before anything is sent.
type BundleStats struct {
	Revision   string
	Files      int
	Bytes      int
	Compressed int
}

// BuildSourceBundle produces a gzipped tar of the repository at revision.
//
// revision must already be resolved to something git can name. The caller resolves HEAD; passing a
// symbolic name here is allowed but the caller is expected to record the sha it resolved to, because a
// snapshot labelled "HEAD" describes nothing a week later.
func BuildSourceBundle(ctx context.Context, repo, revision string) ([]byte, BundleStats, error) {
	if revision == "" {
		return nil, BundleStats{}, fmt.Errorf("push-source: no revision to snapshot")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, BundleStats{}, fmt.Errorf(
			"push-source: git is required to build a source snapshot, and it is not on PATH. " +
				"There is deliberately no fallback: the alternative is to archive the working directory " +
				"behind a denylist, which would send whatever nobody remembered to exclude")
	}

	// --format=tar, not tar.gz: git's gzip level is not configurable per invocation and doing it here
	// keeps the uncompressed bytes available for the stats line, which is what the user reads before
	// deciding to transmit.
	var tarBuf, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "archive", "--format=tar", revision)
	cmd.Stdout = &tarBuf
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, BundleStats{}, fmt.Errorf("push-source: git archive %s: %w: %s",
			revision, err, strings.TrimSpace(stderr.String()))
	}
	if tarBuf.Len() == 0 {
		return nil, BundleStats{}, fmt.Errorf("push-source: git archive produced an empty snapshot for %s", revision)
	}

	files, err := countTrackedFiles(ctx, repo, revision)
	if err != nil {
		return nil, BundleStats{}, err
	}

	var gzBuf bytes.Buffer
	zw := gzip.NewWriter(&gzBuf)
	if _, err := zw.Write(tarBuf.Bytes()); err != nil {
		return nil, BundleStats{}, fmt.Errorf("push-source: compress snapshot: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, BundleStats{}, fmt.Errorf("push-source: finish compressing snapshot: %w", err)
	}

	return gzBuf.Bytes(), BundleStats{
		Revision:   revision,
		Files:      files,
		Bytes:      tarBuf.Len(),
		Compressed: gzBuf.Len(),
	}, nil
}

// countTrackedFiles reports how many files the snapshot contains, from git rather than by re-reading the
// tar: the tar is what git decided to include, and asking git the same question twice is more likely to
// agree with itself than a second parser is.
func countTrackedFiles(ctx context.Context, repo, revision string) (int, error) {
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "ls-tree", "-r", "--name-only", revision)
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("push-source: git ls-tree %s: %w: %s", revision, err, strings.TrimSpace(stderr.String()))
	}
	n := 0
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
}

// ResolveRevision turns a symbolic revision into a full sha.
//
// A snapshot is identified by its sha everywhere else in the system, so resolving here means the
// console, the graph and the CLI's own output all name the same thing. "HEAD" resolved at push time and
// stored as "HEAD" would collide with every other push from every other branch.
func ResolveRevision(ctx context.Context, repo, revision string) (string, error) {
	if revision == "" {
		revision = "HEAD"
	}
	var out, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", revision)
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("push-source: cannot resolve %q in %s: %w: %s",
			revision, repo, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// WorkingTreeIsDirty reports whether the repo has uncommitted changes.
//
// Worth asking because the answer changes what the snapshot MEANS: `git archive` sends the committed
// tree, so a developer with uncommitted work pushes something that is not what they are looking at. The
// command warns rather than refuses — the snapshot is still a valid, nameable revision, and refusing
// would block the common case of pushing a clean commit from a branch with scratch edits.
func WorkingTreeIsDirty(ctx context.Context, repo string) (bool, error) {
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "status", "--porcelain")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		// Not fatal: a dirtiness check that fails must not stop a push. Report "not dirty" and let the
		// push proceed, because the alternative is failing a valid operation over a warning.
		return false, nil
	}
	return strings.TrimSpace(out.String()) != "", nil
}
