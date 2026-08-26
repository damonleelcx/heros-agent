//go:build live

// livefourstep_test.go is `tasks.md` 7.14 and acceptance criterion A13 — the LIVE four-step, behind a
// build tag because it needs a real forge and it writes to a real repository.
//
// # Why this exists when nine other fences already pass
//
// §9.3 in one line: **a 200 is not evidence of a write.** Every other fence in this package proves that
// the platform CALLED something correctly. None of them proves a pull request exists. A delivery that
// returns 200 has not necessarily produced one, and a pull request existing is not a delivery record —
// the two can each be true with the other false, and every combination has been shipped by somebody.
//
// So acceptance is these four steps, in this order, with a real forge on the far side of the fourth:
//
//	1. approve a proposal through the conversational path
//	2. SELECT the approval row and assert it names the approving person
//	3. SELECT the delivery record and assert it carries the pull-request URL
//	4. FETCH the pull request from the forge and assert it exists at the recorded URL
//
// 🔴 Step 4 is the only one that is not our own bookkeeping, and it is the only one that can fail while
// the other three pass. That is the entire point of running it.
//
// Run it with: make p35-live-four-step
//
// Required environment:
//
//	HEROS_LIVE_FORGE_TOKEN   a token with `pull_requests:write` and `contents:write` on the repository
//	HEROS_LIVE_FORGE_REPO    "owner/repo" — 🚫 a repository you are willing to have a pull request opened on
//	HEROS_LIVE_FORGE_BASE    the base branch (default "main")
//
// ⚠️ It OPENS A PULL REQUEST. It does not merge one — this phase never merges — and it leaves the pull
// request open for a person to close, because closing it automatically would exercise a path
// `ObserveClose` deliberately keeps separate from a merge.

package improvementrun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func liveEnv(t *testing.T) (token, repo, base string) {
	t.Helper()
	token = os.Getenv("HEROS_LIVE_FORGE_TOKEN")
	repo = os.Getenv("HEROS_LIVE_FORGE_REPO")
	base = os.Getenv("HEROS_LIVE_FORGE_BASE")
	if base == "" {
		base = "main"
	}
	if token == "" || repo == "" {
		t.Skip("HEROS_LIVE_FORGE_TOKEN and HEROS_LIVE_FORGE_REPO are required; this test opens a real " +
			"pull request and is skipped rather than faked")
	}
	if !strings.Contains(repo, "/") {
		t.Fatalf("HEROS_LIVE_FORGE_REPO must be owner/repo, got %q", repo)
	}
	return token, repo, base
}

// TestLiveFourStep_ApproveSelectSelectFetch is A13.
func TestLiveFourStep_ApproveSelectSelectFetch(t *testing.T) {
	token, repo, _ := liveEnv(t)
	ctx := context.Background()

	f, run, _, _ := approvableWithDelivery(t)
	id := run.Proposals[0].ProposalID

	// ── 1 · approve through the CONVERSATIONAL path ──────────────────────────────────────────────
	updated, decision, err := f.svc.Decide(ctx, run.TenantID, run.RunID, id, DecideApprove, "person@example.com")
	if err != nil {
		t.Fatalf("step 1 — approving through the conversation: %v", err)
	}
	t.Logf("step 1 ✓ approved by %q", decision.By)

	// ── 2 · SELECT the approval row ──────────────────────────────────────────────────────────────
	//
	// 🔴 Read back from the LEDGER, not from the value the call returned. A method returning what it was
	// given proves nothing about what was written, and "the approval is recorded" is the claim.
	entries, err := f.ledger.Entries(ctx, run.RunID)
	if err != nil {
		t.Fatalf("step 2 — reading the ledger: %v", err)
	}
	var approvedBy string
	for _, e := range entries {
		if e.Kind == KindProposalApproved && e.ProposalID == id {
			approvedBy = e.Actor
		}
	}
	if approvedBy == "" {
		t.Fatal("step 2 ✗ no approval row names the approving person. A row that records a decision " +
			"and cannot say who made it is worse than no row, because it is believed")
	}
	t.Logf("step 2 ✓ the approval row names %q", approvedBy)

	// ── 3 · SELECT the delivery record and read the URL off it ───────────────────────────────────
	var recordedURL, deliveryID string
	for _, e := range entries {
		if e.Kind == KindDeliveryOpened && e.ProposalID == id {
			recordedURL, deliveryID = e.Detail, e.DeliveryID
		}
	}
	if len(updated.Deliveries) == 0 {
		t.Fatal("step 3 ✗ the run carries no delivery")
	}
	if recordedURL == "" || deliveryID == "" {
		t.Fatalf("step 3 ✗ the delivery record carries url=%q delivery_id=%q", recordedURL, deliveryID)
	}
	if recordedURL != updated.Deliveries[0].PullRequestURL {
		t.Fatalf("step 3 ✗ the record says %q and the result says %q — two sources of truth for the one "+
			"URL a person will click", recordedURL, updated.Deliveries[0].PullRequestURL)
	}
	t.Logf("step 3 ✓ the delivery record carries %s", recordedURL)

	// ── 4 · FETCH the pull request FROM THE FORGE ────────────────────────────────────────────────
	//
	// 🔴 The only step that is not our own bookkeeping. Steps 1–3 can all pass over a pull request that
	// does not exist.
	number := strings.TrimPrefix(updated.Deliveries[0].PullRequestRef, repo+"#")
	if number == updated.Deliveries[0].PullRequestRef {
		t.Fatalf("step 4 ✗ the forge ref %q does not name %q, so there is nothing to fetch",
			updated.Deliveries[0].PullRequestRef, repo)
	}
	api := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%s", repo, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("step 4 — fetching the pull request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("step 4 ✗ the forge answered %d for %s. The delivery record claims a pull request that "+
			"the forge does not have — which is exactly the state a 200 from our own API cannot rule out",
			resp.StatusCode, api)
	}
	var pr struct {
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
		Merged  bool   `json:"merged"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		t.Fatal(err)
	}
	if pr.HTMLURL != recordedURL {
		t.Fatalf("step 4 ✗ the pull request is at %q and the record says %q. 🚫 A URL the platform "+
			"composed is a URL that 404s in a customer's browser while looking like one that works",
			pr.HTMLURL, recordedURL)
	}
	// 🔴 And it is NOT merged. This phase opens pull requests; auto-merge is P6's Autonomous level.
	if pr.Merged {
		t.Fatal("step 4 ✗ the pull request is MERGED. P35 never merges, at any level")
	}
	// The evidence task 5.7 requires is in the body a reviewer will actually read.
	for _, want := range []string{"## Verified delta", "## How to revert this"} {
		if !strings.Contains(pr.Body, want) {
			t.Errorf("step 4 ✗ the delivered pull request body does not carry %q", want)
		}
	}
	t.Logf("step 4 ✓ the pull request exists at %s, state=%s, merged=%v", pr.HTMLURL, pr.State, pr.Merged)
	t.Log("⚠️ a pull request was opened and left OPEN for a person to close; nothing closed it, because " +
		"closing it automatically would exercise a path ObserveClose keeps separate from a merge")
}
