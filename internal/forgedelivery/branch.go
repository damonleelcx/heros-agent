package forgedelivery

import "fmt"

// BranchName is the deterministic head branch a delivery pushes to (task 1.5 / PRD §14 Q2).
//
//	heros/opt/<config_hash[:12]>-<source_revision[:12]>
//
// Determinism is the point: the branch name is itself an idempotency anchor. Two concurrent opens of
// the same change compute the SAME branch, and a forge refuses a second open pull request for one
// head→base pair — so the branch name backs the database's partial unique index at the forge layer.
// A predictable scheme also lets a customer recognize a platform branch at a glance.
func BranchName(configHash, sourceRevision string) string {
	return fmt.Sprintf("heros/opt/%s-%s", short(configHash, 12), short(sourceRevision, 12))
}

// IsPlatformBranch reports whether a branch name is one this platform would have created. Used by the
// forge writer to refuse writing to any branch it did not name — a delivery must not push over a branch
// a human is working on.
func IsPlatformBranch(name string) bool {
	return len(name) > len("heros/opt/") && name[:len("heros/opt/")] == "heros/opt/"
}

// StaleBranchPolicy is the decision, encoded so it cannot drift into a config toggle: the platform
// NEVER deletes a branch (task 1.5). Supersession closes the pull request and leaves the branch,
// because deletion could remove something a customer built on — a one-way door the spec forbids.
// Removing an abandoned branch is the customer's action in their own tooling.
type StaleBranchPolicy struct{}

// MayDelete always reports false. It exists as a method rather than an absent capability so a future
// reader sees the decision was made, not forgotten — and so a caller cannot express deletion at all.
func (StaleBranchPolicy) MayDelete(branch string) bool { return false }

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
