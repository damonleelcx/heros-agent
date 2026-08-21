package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/approval"
	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/conversation"
	"github.com/heros-foreal/agentd/internal/db"
)

// conversation_fences_test.go holds the two P31 fences that cannot be satisfied by reading source:
// §6.8 (an approval is a WRITE, and a 200 is not evidence of one) and §6.6 (a stream killed mid-run
// resumes with no duplicate and no gap).

// ── §6.8 · a live approval, then a SELECT ────────────────────────────────────────────────────────

// TestAnApprovalIsAWriteAndTheRowProvesIt is the acceptance §9.3 asks for, in full.
//
// # 🔴 Why the SELECT is the test and the 200 is not
//
// An approval that returns 200 has not necessarily been recorded. The route could forward to a gate
// that swallowed the error, the UPDATE could have matched zero rows because the proposal was already
// reviewed, or the adapter could have been wired to a different database entirely — and every one of
// those returns 200 to a browser that then renders "Recorded".
//
// So this drives the REAL route, over the REAL mux, into the REAL `internal/approval` on a REAL SQLite
// ledger with the REAL migrations, and then reads the row back.
func TestAnApprovalIsAWriteAndTheRowProvesIt(t *testing.T) {
	ledger := ledgerForTest(t)
	tenant := personA.TenantID

	pending, err := approval.Submit(ledger, tenant, approval.LayerHarness,
		"raise max_turns on extract", "the reflexion loop stops one turn early", "--- a\n+++ b\n")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if pending.Status != approval.StatusPending {
		t.Fatalf("the seeded proposal is %q, not pending", pending.Status)
	}

	f := newConversationServerOverLedger(t, ledger)
	convID := f.newConversation(t, personA, "wf_1")

	rec := f.do(t, personA, "POST", "/api/v1/conversation-approvals",
		fmt.Sprintf(`{"conversation_id":%q,"approval_id":%q}`, convID, pending.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}

	// 🔴 THE SELECT. Everything above this line is what a 200 already told us.
	after, err := approval.Get(ledger, pending.ID)
	if err != nil {
		t.Fatalf("reading the row back: %v", err)
	}
	if after.Status != approval.StatusApproved {
		t.Fatalf("the row is %q after a 200 response. The HTTP status said the approval was recorded and "+
			"the database says it was not — which is the exact gap this fence exists for.", after.Status)
	}
	if after.ReviewedAt == nil {
		t.Error("the approved row carries no reviewed_at")
	}
	// Attributed to the SESSION's person (NFR-S3). Nothing in the request body named one.
	who, ok, err := approval.ApprovedBy(ledger, pending.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovedBy = %q, %v, %v", who, ok, err)
	}
	if who != personA.UserID {
		t.Errorf("approved_by = %q, want the session's person %q. An audit entry that names the wrong "+
			"person is worse than one that names none, because it is believed.", who, personA.UserID)
	}

	// 🔴 AND THE DOWNSTREAM CONSEQUENCE. The approval id now resolves through the SAME resolver the
	// emitter consults before it will emit an `approval_request` — so the write did not merely land in a
	// row, it changed what the conversation is allowed to say. (A run ADVANCING on an approval is P35's
	// autonomous improvement run; this phase routes the approval and stops there, which is stated in the
	// task list rather than faked here.)
	resolver := ledgerProposals{db: ledger}
	resolves, err := resolver.Resolve(tenant, pending.ID)
	if err != nil || !resolves {
		t.Errorf("the approved proposal does not resolve for its own tenant: %v, %v", resolves, err)
	}
	// And not for anybody else's.
	if crossed, _ := resolver.Resolve("tenant-b", pending.ID); crossed {
		t.Error("another tenant's resolver resolved this proposal")
	}
}

func TestApprovingSomethingThatIsNotYoursIsIndistinguishableFromApprovingNothing(t *testing.T) {
	ledger := ledgerForTest(t)
	theirs, err := approval.Submit(ledger, "tenant-b", approval.LayerPrompt, "t", "r", "d")
	if err != nil {
		t.Fatal(err)
	}
	f := newConversationServerOverLedger(t, ledger)
	convID := f.newConversation(t, personA, "wf_1")

	notYours := f.do(t, personA, "POST", "/api/v1/conversation-approvals",
		fmt.Sprintf(`{"conversation_id":%q,"approval_id":%q}`, convID, theirs.ID))
	invented := f.do(t, personA, "POST", "/api/v1/conversation-approvals",
		fmt.Sprintf(`{"conversation_id":%q,"approval_id":"prop_0000000000000000"}`, convID))

	if notYours.Code == http.StatusOK {
		t.Fatal("another tenant's proposal was approved")
	}
	if notYours.Body.String() != invented.Body.String() {
		t.Errorf("a real proposal belonging to somebody else (%s) is distinguishable from one that does "+
			"not exist (%s) — which lets a caller enumerate another organization's proposals.",
			strings.TrimSpace(notYours.Body.String()), strings.TrimSpace(invented.Body.String()))
	}
	// And the row is untouched.
	after, err := approval.Get(ledger, theirs.ID)
	if err != nil || after.Status != approval.StatusPending {
		t.Errorf("the other tenant's row is %q after a refused approval", after.Status)
	}
}

// ledgerForTest opens a throwaway SQLite ledger with the REAL migrations applied.
//
// 🔴 The real ones. A test that created its own simplified `proposals` table would be testing a schema
// no deployment has — and `approved_by` is a column this phase ADDED, so a hand-rolled table is exactly
// how "the migration was never written" ships green.
func ledgerForTest(t *testing.T) *sql.DB {
	t.Helper()
	f, err := os.CreateTemp("", "heros-p31-approval-*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	_ = f.Close()
	t.Cleanup(func() { _ = os.Remove(path) })
	ledger, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ledger.Close() })
	return ledger
}

// newConversationServerOverLedger builds the surface with the REAL approval adapter over a real ledger.
func newConversationServerOverLedger(t *testing.T, ledger *sql.DB) *convFixture {
	t.Helper()
	f := newConversationServer(t, stubReader{})
	f.srv.DB = ledger
	// The real adapter, not the recording stub: this fence is about what reaches the database.
	f.srv.conversations.Approvals = ledgerApprovalGate{db: ledger}
	return f
}

// ── §6.6 · kill the stream mid-run, reconnect ────────────────────────────────────────────────────

// gatedReader releases one surface read at a time, so a turn can be held open while its stream is
// killed. 🔴 A turn that completed instantly could not exercise this: the whole failure being fenced is
// a disconnect that lands BETWEEN two messages.
type gatedReader struct {
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (g *gatedReader) Read(ctx context.Context, _, _ string, _ conversation.IntentSpec) (conversation.SurfaceReading, error) {
	select {
	case <-g.release:
	case <-ctx.Done():
		return conversation.SurfaceReading{}, ctx.Err()
	case <-time.After(10 * time.Second):
		return conversation.SurfaceReading{}, fmt.Errorf("the gate was never opened")
	}
	g.mu.Lock()
	g.calls++
	n := g.calls
	g.mu.Unlock()
	return conversation.SurfaceReading{
		Claim:       fmt.Sprintf("reading %d", n),
		EvidenceRef: fmt.Sprintf("evidence:%d", n),
		State:       conversation.FindingMeasured,
	}, nil
}

func (g *gatedReader) Mounted(conversation.IntentSpec) bool { return true }

func TestAStreamKilledMidRunResumesWithNoDuplicateAndNoGap(t *testing.T) {
	gate := &gatedReader{release: make(chan struct{}, 8)}
	f := newConversationServer(t, gate)
	convID := f.newConversation(t, personA, "wf_1")
	owner := conversation.Owner{TenantID: personA.TenantID, UserID: personA.UserID}

	f.do(t, personA, "POST", "/api/v1/conversation-turns",
		fmt.Sprintf(`{"conversation_id":%q,"question":"what does this node remember between calls?"}`, convID))

	// Let the first step through, so the transcript has a plan, a progress and a finding.
	gate.release <- struct{}{}
	waitFor(t, func() bool {
		msgs, _ := f.store.Messages(convID, owner, 0)
		return len(msgs) >= 3
	}, "the first step's messages")

	// ── the kill ──────────────────────────────────────────────────────────────────────────────────
	//
	// The client's context is cancelled mid-run. 🔴 The RUN must not die with it (FR7): the turn is on a
	// detached context, and the assertion after the reconnect is what proves it kept going.
	firstHalf := f.streamUntilCancelled(t, personA, "conversation_id="+convID, 250*time.Millisecond)
	if len(firstHalf) == 0 {
		t.Fatal("the stream delivered nothing before it was killed")
	}
	lastSeen := firstHalf[len(firstHalf)-1]

	// The run continues while nobody is watching.
	gate.release <- struct{}{}
	gate.release <- struct{}{}
	waitFor(t, func() bool {
		msgs, _ := f.store.Messages(convID, owner, 0)
		return len(msgs) > 0 && msgs[len(msgs)-1].Kind.Terminal()
	}, "the turn to finish with nobody connected")

	// ── the reconnect ─────────────────────────────────────────────────────────────────────────────
	secondHalf := f.streamOnce(t, personA, fmt.Sprintf("conversation_id=%s&after=%d", convID, lastSeen))

	var resumed []int64
	for _, fr := range secondHalf {
		if fr.event != "message" {
			continue
		}
		var m conversation.Message
		if err := json.Unmarshal([]byte(fr.data), &m); err != nil {
			t.Fatal(err)
		}
		resumed = append(resumed, m.ID)
	}
	if len(resumed) == 0 {
		t.Fatal("the reconnect replayed nothing; the run's messages are supposed to remain retrievable")
	}

	// NO DUPLICATE: nothing the client already acknowledged comes back.
	for _, id := range resumed {
		if id <= lastSeen {
			t.Errorf("the reconnect re-delivered message %d, which the client had already acknowledged "+
				"(last seen %d)", id, lastSeen)
		}
	}
	// NO GAP: the first resumed id is exactly the next one, and the sequence is contiguous.
	if resumed[0] != lastSeen+1 {
		t.Errorf("the reconnect started at %d; the client had %d, so %d was skipped",
			resumed[0], lastSeen, lastSeen+1)
	}
	for i := 1; i < len(resumed); i++ {
		if resumed[i] != resumed[i-1]+1 {
			t.Errorf("a gap in the resumed stream: %d follows %d", resumed[i], resumed[i-1])
		}
	}
	// And the two halves together are the WHOLE transcript — the property "no duplicate and no gap" is
	// about the union, and checking the halves separately would miss a message lost at the boundary.
	all, err := f.store.Messages(convID, owner, 0)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(all)) != resumed[len(resumed)-1] {
		t.Errorf("the run holds %d messages and the last resumed id is %d", len(all), resumed[len(resumed)-1])
	}
	if lastSeen+int64(len(resumed)) != int64(len(all)) {
		t.Errorf("%d seen before the kill + %d after = %d, but the transcript has %d",
			lastSeen, len(resumed), lastSeen+int64(len(resumed)), len(all))
	}
}

// streamUntilCancelled opens the stream, lets it run for `hold`, then cancels the request as a closed
// tab would. It returns the ids delivered before the cancellation.
func (f *convFixture) streamUntilCancelled(t *testing.T, p auth.Principal, query string, hold time.Duration) []int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), hold)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/v1/conversation-stream?"+query, nil)
	req = req.WithContext(auth.WithPrincipal(ctx, p))
	rec := httptest.NewRecorder()
	f.srv.Mux.ServeHTTP(rec, req)

	var ids []int64
	for _, fr := range readSSE(rec.Body.String()) {
		if fr.event != "message" {
			continue
		}
		var m conversation.Message
		if err := json.Unmarshal([]byte(fr.data), &m); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	return ids
}

// waitFor polls a condition. The turn runs on a detached goroutine by design, so a test that read
// immediately after the POST would be racing the run rather than testing it.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
