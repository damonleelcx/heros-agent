package forgedelivery

import (
	"context"
	"fmt"
)

// ForgeKind names a forge. github is the FIRST-CLASS forge (task 1.6 / PRD §14 Q1) and matches P11's.
// The vocabulary is open so a second forge is a new recipe, not a core change (L6 扩展性) — but only
// github is implemented in P12; the others are declared-not-implemented rather than pretended.
type ForgeKind string

const (
	ForgeGitHub    ForgeKind = "github"
	ForgeGitLab    ForgeKind = "gitlab"    // declared, not implemented in P12
	ForgeBitbucket ForgeKind = "bitbucket" // declared, not implemented in P12
)

// Supported reports whether this forge is implemented in P12. Only github is first-class.
func (k ForgeKind) Supported() bool { return k == ForgeGitHub }

func (k ForgeKind) unsupportedReason() string {
	switch k {
	case ForgeGitLab, ForgeBitbucket:
		return fmt.Sprintf("forge %q is declared but not implemented in P12; github is the first-class forge", k)
	default:
		return fmt.Sprintf("%q is not a known forge (github, gitlab, bitbucket)", k)
	}
}

// PullRequest is the forge-agnostic handle to an opened pull request. Ref is the forge's own citation
// (e.g. "owner/repo#42"), which becomes the delivery record's forge_ref and P7's gainshare join key.
type PullRequest struct {
	Ref    string // e.g. "nousresearch/hermes-agent#42"
	URL    string // e.g. "https://github.com/nousresearch/hermes-agent/pull/42"
	Number int
	Head   string // the head branch
	Base   string // the base branch
	State  string // "open" | "closed" | "merged" — the forge's view, distinct from our record's State
}

// OpenRequest is everything the forge needs to open (or update) a pull request. The Body is the
// rendered PR content (§1.1); it is identical across modes. There is deliberately no credential field:
// the credential is bound to the ForgeWriter, not passed per call, so a credential cannot end up in a
// request struct that gets logged.
type OpenRequest struct {
	Target Target
	Head   string // the deterministic branch (BranchName)
	Title  string
	Body   string
	// DiffPatch is the unified diff applied on Head. In CI-mediated mode the CI runner applies it with
	// its own checkout; in App mode the ForgeWriter applies it via the forge's contents API.
	DiffPatch string
	Draft     bool
}

// ForgeWriter is the ENTIRE write surface the platform has on a customer's repository. It exposes only
// pull requests and the branches backing them (task 2.8 / design Decision 8). There is deliberately NO
// method to push to a protected branch, create a tag or a release, or file an issue — the restriction
// is enforced by ABSENCE, so it is impossible to widen by calling a method rather than being a policy a
// reviewer has to remember. Widening it is a spec change, not a code convenience (ADR-005).
//
// A credential is bound to a concrete ForgeWriter (a CI runner's token, or the App installation token
// from a secrets manager) — never carried in a method argument, so it cannot leak into a request that
// is logged.
type ForgeWriter interface {
	// EnsureBranch creates or fast-forwards the platform head branch from the base. It refuses any name
	// that is not a platform branch (IsPlatformBranch), so a delivery cannot push over a human's branch.
	EnsureBranch(ctx context.Context, t Target, head string) error
	// OpenOrUpdatePR opens the pull request for req.Head, or — if one already exists for that head→base
	// pair — updates it in place and returns it. This is the forge-layer idempotency that backs the
	// database's partial unique index: a forge refuses a second PR for one head, so a lost race updates.
	OpenOrUpdatePR(ctx context.Context, req OpenRequest) (PullRequest, bool, error)
	// ClosePR closes a pull request WITHOUT merging, stating the reason on it. Used by supersession.
	ClosePR(ctx context.Context, ref, reason string) error
	// MergePR merges a pull request. It is called ONLY under the Autonomous level and ONLY for a
	// gate-passed change; the Deliverer enforces both preconditions before ever reaching here.
	MergePR(ctx context.Context, ref string) (mergeCommit string, err error)
	// OpenPRCount returns how many platform-authored pull requests are currently open on the repository,
	// for the per-repository volume bound (task 2.5).
	OpenPRCount(ctx context.Context, t Target) (int, error)
	// Kind reports which forge this writer targets, for the delivery record and diagnostics.
	Kind() ForgeKind
}

// CredentialCarrier is the property the credential-absence assertion checks (task 5.2 / 7.4). A
// ForgeWriter reports whether it HOLDS a forge credential. The default CI-mediated path binds a writer
// that holds none (the credential lives in the CI runner, not here); the hosted App binds one that
// does. The structural test asserts that the default mode's writer answers false, so "the platform
// holds no forge credential" is provable by absence rather than by policy.
type CredentialCarrier interface {
	// HoldsForgeCredential reports whether this writer holds a platform-side forge credential.
	HoldsForgeCredential() bool
}
