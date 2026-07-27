// Package sourcerev resolves the commit a run should LABEL its output with, and refuses to label it
// with a commit the checkout is not at.
//
// It is a separate package because internal/discovery may not import os/exec — the no-execution
// invariant I1 is enforced structurally there (TestNoExecutionImports), and running `git` is running a
// process even though it is not running the TARGET. So the git call lives out here, and discovery keeps
// receiving a plain string in Options.CommitSHA.
package sourcerev

import (
	"fmt"
	"os/exec"
	"strings"
)

// Resolve resolves the commit a hermes-agent demo run should LABEL its IR with, and refuses to
// label it with a commit the checkout is not actually at.
//
// # Why this is not just `rev-parse HEAD`, and not just a constant either
//
// `source_revision` is half of the reproducibility key: P2 keys a transform on
// (config_hash, source_revision), and PRD §7/FR16 treat reproducibility as {config_hash,
// source_revision, seed}. So the SHA a demo prints is a CLAIM about which tree produced the IR, and
// the two ways to get it wrong are both silent:
//
//	a hard-coded constant  — the run parses whatever is on disk and labels it with a commit that may
//	                         not be in the repository at all. The output then documents a run that
//	                         never happened, and the reader has no way to tell.
//	a bare rev-parse HEAD  — the pin disappears, so a demo whose numbers were quoted in a document
//	                         silently starts describing a different tree the next time anyone pulls.
//
// So both are supported and neither is guessed at. A caller that declares a pin gets it VERIFIED: if
// the checkout is not at that commit, this fails and says so, naming both SHAs. A caller that declares
// no pin gets HEAD, and gets told that is what happened.
//
// 🔴 It fails loudly rather than falling back. A demo that quietly downgraded a verified pin to "well,
// whatever is checked out" would be the fallback-to-a-plausible-default this codebase declines
// everywhere else — and here the plausible default is a false provenance claim.
func Resolve(repo, pinned string) (sha string, note string, err error) {
	head, err := gitHEAD(repo)
	if err != nil {
		return "", "", err
	}
	if pinned == "" {
		return head, "commit read from the checkout (no pin declared)", nil
	}
	if head == pinned {
		return pinned, "checkout verified at the pinned commit", nil
	}
	// The pin is not what is checked out. Say whether the pinned commit is even reachable, because the
	// two cases have different fixes: "git checkout <pin>" versus "the pin refers to a commit this
	// clone does not have — re-pin or fetch it".
	if objectExists(repo, pinned) {
		return "", "", fmt.Errorf("sourcerev: %s is checked out at %s but this run pins %s; "+
			"the pinned commit IS in the clone, so run `git -C %s checkout %s` (or re-pin) rather than "+
			"labelling the IR with a tree it was not extracted from",
			repo, short(head), short(pinned), repo, short(pinned))
	}
	return "", "", fmt.Errorf("sourcerev: %s is checked out at %s and the pinned commit %s is NOT in this "+
		"clone at all; labelling the IR with it would claim a provenance nothing can check. Fetch that "+
		"commit, or re-pin this run to a commit the clone has",
		repo, short(head), short(pinned))
}

func gitHEAD(repo string) (string, error) {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("sourcerev: read HEAD of %s: %w (is it a git checkout?)", repo, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// objectExists reports whether the clone holds the named object. A shallow clone is the common reason
// it does not.
func objectExists(repo, sha string) bool {
	return exec.Command("git", "-C", repo, "cat-file", "-e", sha+"^{commit}").Run() == nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
