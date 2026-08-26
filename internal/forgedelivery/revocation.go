package forgedelivery

import (
	"context"
	"fmt"
)

// revocation.go is P35 FR25 and the `forge-delivery` spec's two added requirements:
//
//	Revoking a write installation SHALL stop pushes IMMEDIATELY rather than at the next token refresh.
//	The read connection and the write installation SHALL be separate grants with independent revocations.
//
// # 🔴 What "immediately" rules out, precisely
//
// The natural implementation of an App integration caches an installation token for its lifetime —
// GitHub's are an hour — and refreshes it when it expires. Under that design a revocation takes effect
// **whenever the cached token happens to run out**, which is somewhere between now and an hour from now
// and is not knowable from the outside. A customer who revokes during an incident and watches a pull
// request appear four minutes later has learned that the control they were given is advisory.
//
// So the check is made on the path of EVERY write, not on the token's lifecycle:
// `AppForgeWriter.withDelegate` resolves the installation through `InstallationStore.ForRepo` before
// every single call, and a revoked installation fails there — before a token is even requested. The
// token cache is therefore irrelevant to revocation, which is the property being claimed.
//
// # Why the two grants are separate, and what would happen if they were not
//
// P32's read connection and this write installation are two credentials with two scopes, and the
// temptation is to hold one that does both — it is fewer moving parts and one consent screen. ADR-005
// and ADR-013 both refuse it, and P35 adds the reason that only becomes true once both exist: a
// customer revoking WRITE access would lose their assessments, their proposals and every derived tree,
// because the platform could no longer read. Revocation would then be a decision with a consequence
// nobody warned them about, and the practical effect is that they would not revoke.

// PushGuard is the check every write goes through. It answers one question — may the platform push to
// this repository for this tenant, right now — and it answers it from the CURRENT installation state.
//
// 🔴 It is an interface so the guard can be asserted independently of the writer, and so a fence can
// prove that a revocation recorded a microsecond ago stops the very next call. `*InstallationStore`
// satisfies it.
type PushGuard interface {
	// MayPush reports whether an active installation covers this repository. The error names the
	// condition — `ErrNoInstallation` or `ErrInstallationRevoked` — and both are REPORTED states the
	// surface renders, never silence.
	MayPush(ctx context.Context, tenantID, repo string) error
}

// MayPush implements PushGuard. It reads the store's live state; there is deliberately no cache.
//
// 🚫 A cache here would reintroduce exactly the lag this file exists to remove. The store read is a map
// lookup under a mutex in this implementation, and a database-backed one is a single indexed row — the
// cost of doing it per write is not the reason anybody would add a cache, and the reason they would is
// the reason not to.
func (s *InstallationStore) MayPush(_ context.Context, tenantID, repo string) error {
	if _, err := s.ForRepo(tenantID, repo); err != nil {
		return err
	}
	return nil
}

var _ PushGuard = (*InstallationStore)(nil)

// GrantKind names which of a tenant's two grants for one repository is meant.
//
// It exists so "these are two grants" is a value rather than a claim in a document: a caller has to say
// which one it means, and `TestNeitherGrantImpliesTheOther` reads this enum rather than a hand-written
// list that would stop covering a third grant somebody adds.
type GrantKind string

const (
	// GrantRead is P32's source-read connection: the platform may read the repository's contents.
	// 🚫 It creates NO write capability.
	GrantRead GrantKind = "source_read"
	// GrantWrite is this package's hosted Git App installation: the platform may open and update pull
	// requests, and push the branches backing them. 🚫 It creates NO read capability for P32's purposes
	// — a tenant with only this grant has no source snapshot and no derived trees.
	GrantWrite GrantKind = "write_installation"
)

var grantKinds = []GrantKind{GrantRead, GrantWrite}

// GrantKinds returns the closed set. A copy.
func GrantKinds() []GrantKind { return append([]GrantKind(nil), grantKinds...) }

// Implies reports whether holding one grant confers the other. 🔴 It is ALWAYS false, and it is a
// method rather than an absent capability so that a future reader sees the decision was made rather
// than forgotten — the same shape `StaleBranchPolicy.MayDelete` takes, for the same reason.
func (g GrantKind) Implies(other GrantKind) bool { return false }

// String makes GrantKind printable.
func (g GrantKind) String() string { return string(g) }

// RevocationEffect is what a revocation did, reported so the customer sees the boundary they just
// exercised rather than inferring it.
type RevocationEffect struct {
	Grant GrantKind `json:"grant"`
	// StoppedPushes is true when this revocation removed the platform's ability to push. False for a
	// read revocation, which is the point.
	StoppedPushes bool `json:"stopped_pushes"`
	// StoppedReads is true when this revocation removed the platform's ability to read source.
	StoppedReads bool `json:"stopped_reads"`
	// OtherGrantIntact names the grant this revocation did NOT touch, so the customer is told plainly
	// that the other one still stands. 🔴 Said out loud rather than left to be discovered: a customer
	// who revokes write access and is not told their read connection survives will assume it did not,
	// and act on that.
	OtherGrantIntact GrantKind `json:"other_grant_intact"`
	Detail           string    `json:"detail"`
}

// EffectOfRevoking describes what revoking one grant does, and what it deliberately leaves alone.
func EffectOfRevoking(g GrantKind) (RevocationEffect, error) {
	switch g {
	case GrantWrite:
		return RevocationEffect{
			Grant: GrantWrite, StoppedPushes: true, OtherGrantIntact: GrantRead,
			Detail: "The platform can no longer push to this repository or open pull requests on it, " +
				"from this moment — not at the next token refresh. Your source-read connection is " +
				"untouched: assessments, proposals and everything derived from your source still work.",
		}, nil
	case GrantRead:
		return RevocationEffect{
			Grant: GrantRead, StoppedReads: true, OtherGrantIntact: GrantWrite,
			Detail: "The platform can no longer read this repository, and every tree derived from it is " +
				"deleted. Your write installation is untouched — it was never able to read source, and " +
				"revoking reads does not revoke it.",
		}, nil
	default:
		return RevocationEffect{}, fmt.Errorf("forgedelivery: %q is not a grant kind (%s, %s)",
			g, GrantRead, GrantWrite)
	}
}
