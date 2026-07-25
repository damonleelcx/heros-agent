package forgedelivery

import (
	"fmt"
	"strings"
)

// Target names WHERE a delivery goes. It keys on (repository, workflow) — with workflow optional —
// because a monorepo may host several workflows with different reviewers, and keying on the repository
// alone would force one route, and one reviewer set, on all of them (task 1.4 / PRD §14 Q6).
//
// The canonical string form is the third component of the idempotency key, so it is derived here in
// exactly one place: two spellings of one target must not become two pull requests.
type Target struct {
	// Owner and Repo identify the repository, e.g. "nousresearch" / "hermes-agent".
	Owner string
	Repo  string
	// Base is the branch a merge lands in — the customer's default branch unless configured otherwise.
	// It is the branch gainshare observes a merge into.
	Base string
	// Workflow is optional. Empty means the repository's single/default workflow; set means one of
	// several in a monorepo, so its route (and reviewers) can differ from a sibling's.
	Workflow string
}

// Key is the canonical string form: "owner/repo" or "owner/repo#workflow". It is the `target` column
// and the third component of the idempotency key. It deliberately omits Base: the base branch is a
// property of where a PR lands, not of which logical delivery this is — two deliveries to the same
// (repo, workflow) targeting different bases would still be the same idempotent delivery, and opening
// two PRs for them is exactly the duplicate this key exists to prevent.
func (t Target) Key() string {
	k := t.Owner + "/" + t.Repo
	if t.Workflow != "" {
		k += "#" + t.Workflow
	}
	return k
}

// Validate rejects a target missing a part a pull request cannot be opened without.
func (t Target) Validate() error {
	if strings.TrimSpace(t.Owner) == "" || strings.TrimSpace(t.Repo) == "" {
		return fmt.Errorf("forgedelivery: a target needs an owner and a repository (got %q/%q)", t.Owner, t.Repo)
	}
	if strings.TrimSpace(t.Base) == "" {
		return fmt.Errorf("forgedelivery: target %s has no base branch to open a pull request against", t.Key())
	}
	return nil
}

// Route is a repository's configured means of receiving pull requests: which credential mode, and the
// target. A repository with verified proposals and NO route is a reported state, not silence
// (design Decision 6 / task 6.1) — which is why the delivery entry point takes a *Route and treats nil
// as that reported condition rather than as an error to swallow.
type Route struct {
	Mode   Mode
	Target Target
	// ForgeKind is which forge this route delivers to. github is the first-class forge (task 1.6).
	ForgeKind ForgeKind
}

// Validate checks a route is deliverable: a known mode, a valid target, a supported forge.
func (r Route) Validate() error {
	if !r.Mode.Valid() {
		return fmt.Errorf("forgedelivery: %q is not a delivery mode (ci, app)", r.Mode)
	}
	if err := r.Target.Validate(); err != nil {
		return err
	}
	if !r.ForgeKind.Supported() {
		return fmt.Errorf("forgedelivery: %s", r.ForgeKind.unsupportedReason())
	}
	return nil
}
