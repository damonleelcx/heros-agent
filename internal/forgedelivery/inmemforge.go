package forgedelivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// InMemForge is a simulated repository implementing ForgeWriter. It is the demo/dev forge AND the test
// double, so the property under test is the property demonstrated. It models the one forge behaviour
// idempotency leans on: a forge admits AT MOST ONE open pull request per head→base pair, so a second
// open for the same head UPDATES rather than duplicates — the same guarantee real GitHub gives with a
// 422 "A pull request already exists".
//
// It never holds a credential unless told to, which is how the CI-mediated (no credential) and hosted
// App (credential) postures are both representable and testable.
type InMemForge struct {
	mu sync.Mutex

	kind        ForgeKind
	holdsCred   bool
	nextPR      int
	branches    map[string]bool         // head branch -> exists
	byHead      map[string]string       // head branch -> current PR ref
	prs         map[string]*PullRequest // ref -> PR
	bodyByRef   map[string]string       // ref -> the exact body opened, for parity byte-comparison
	writesOther []string                // any write OTHER than PRs/branches would be appended here; must stay empty

	// down simulates a forge outage. Every write and count fails while set, so per-repository failure
	// isolation and the degraded-credential path are exercisable.
	down bool
}

// NewInMemForge builds a simulated repository. holdsCredential models the credential posture: false for
// the CI-mediated writer (the credential lives in CI, not here), true for the hosted App writer.
func NewInMemForge(kind ForgeKind, holdsCredential bool) *InMemForge {
	if kind == "" {
		kind = ForgeGitHub
	}
	return &InMemForge{
		kind: kind, holdsCred: holdsCredential,
		branches: map[string]bool{}, byHead: map[string]string{}, prs: map[string]*PullRequest{},
		bodyByRef: map[string]string{},
	}
}

// SetDown takes the forge offline (or brings it back).
func (f *InMemForge) SetDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

// Kind implements ForgeWriter.
func (f *InMemForge) Kind() ForgeKind { return f.kind }

// HoldsForgeCredential implements CredentialCarrier.
func (f *InMemForge) HoldsForgeCredential() bool { return f.holdsCred }

var errForgeDown = errors.New("simulated forge outage")

// EnsureBranch implements ForgeWriter. It refuses any branch this platform would not have named, so a
// delivery cannot push over a human's branch.
func (f *InMemForge) EnsureBranch(ctx context.Context, t Target, head string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errForgeDown
	}
	if !IsPlatformBranch(head) {
		return fmt.Errorf("forgedelivery: refusing to write non-platform branch %q", head)
	}
	f.branches[head] = true
	return nil
}

// OpenOrUpdatePR implements ForgeWriter with forge-native idempotency on the head branch.
func (f *InMemForge) OpenOrUpdatePR(ctx context.Context, req OpenRequest) (PullRequest, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return PullRequest{}, false, errForgeDown
	}
	if ref, ok := f.byHead[req.Head]; ok {
		pr := f.prs[ref]
		if pr.State == "open" {
			// Update in place — never a second PR for the same head.
			pr.Base = req.Target.Base
			f.bodyByRef[ref] = req.Body
			return *pr, false, nil
		}
		// A closed/merged PR for this head does not block a fresh one; fall through to open.
	}
	f.nextPR++
	repo := req.Target.Owner + "/" + req.Target.Repo
	pr := &PullRequest{
		Ref:    fmt.Sprintf("%s#%d", repo, f.nextPR),
		URL:    fmt.Sprintf("https://%s.example/%s/pull/%d", f.kind, repo, f.nextPR),
		Number: f.nextPR, Head: req.Head, Base: req.Target.Base, State: "open",
	}
	f.prs[pr.Ref] = pr
	f.byHead[req.Head] = pr.Ref
	f.bodyByRef[pr.Ref] = req.Body
	return *pr, true, nil
}

// ClosePR implements ForgeWriter: close without merging.
func (f *InMemForge) ClosePR(ctx context.Context, ref, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errForgeDown
	}
	pr, ok := f.prs[ref]
	if !ok {
		return fmt.Errorf("forgedelivery: no such pull request %q", ref)
	}
	pr.State = "closed"
	return nil
}

// MergePR implements ForgeWriter and returns the merge commit.
func (f *InMemForge) MergePR(ctx context.Context, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return "", errForgeDown
	}
	pr, ok := f.prs[ref]
	if !ok {
		return "", fmt.Errorf("forgedelivery: no such pull request %q", ref)
	}
	pr.State = "merged"
	return "merge-" + strings.ReplaceAll(ref, "/", "-"), nil
}

// OpenPRCount implements ForgeWriter.
func (f *InMemForge) OpenPRCount(ctx context.Context, t Target) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return 0, errForgeDown
	}
	n := 0
	for _, pr := range f.prs {
		if pr.State == "open" {
			n++
		}
	}
	return n, nil
}

// PRState returns a pull request's forge-side state, for tests and the demo.
func (f *InMemForge) PRState(ref string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, ok := f.prs[ref]
	if !ok {
		return "", false
	}
	return pr.State, true
}

// PRBody returns the exact body a pull request was opened/updated with, for parity byte-comparison.
func (f *InMemForge) PRBody(ref string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.bodyByRef[ref]
	return b, ok
}

// OtherWrites returns any write the platform performed that was NOT a pull request or its branch. It
// must always be empty — the structural proof (task 2.8) that the platform writes only pull requests
// and their branches. There is no method on this forge that would append to it, which is the point.
func (f *InMemForge) OtherWrites() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writesOther...)
}
