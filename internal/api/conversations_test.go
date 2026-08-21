package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/conversation"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/linkingest"
)

// conversations_test.go exercises the five routes end to end over a REAL mux, a REAL store and a REAL
// emitter. Nothing here stubs the thing under test: the properties are "the route refuses without
// disclosing existence" and "the stream resumes with no gap", and both are properties of the assembled
// surface rather than of any one function.

// ── harness ──────────────────────────────────────────────────────────────────────────────────────

type stubReader struct {
	reading   conversation.SurfaceReading
	err       error
	unmounted map[conversation.Intent]bool
}

func (s stubReader) Read(context.Context, string, string, conversation.IntentSpec) (conversation.SurfaceReading, error) {
	if s.err != nil {
		return conversation.SurfaceReading{}, s.err
	}
	if s.reading.EvidenceRef == "" {
		return conversation.SurfaceReading{
			Claim: "this workflow reported 4 nodes", EvidenceRef: "workflow-ir:wf_1@abc",
			State: conversation.FindingMeasured}, nil
	}
	return s.reading, nil
}

func (s stubReader) Mounted(spec conversation.IntentSpec) bool { return !s.unmounted[spec.Intent] }

type stubBudgets struct{}

func (stubBudgets) Envelope(context.Context, string) (conversation.BudgetEnvelope, error) {
	return conversation.BudgetEnvelope{
		TurnCeiling: 8, TokenBudget: 100_000, ToolCallCeiling: 40, WallClockSeconds: 600}, nil
}

type stubWorkflows struct{ owned map[string]map[string]bool }

func (s stubWorkflows) OwnsWorkflow(tenantID, workflowID string) (bool, error) {
	return s.owned[tenantID][workflowID], nil
}

type recordingGate struct {
	calls []string
	err   error
}

func (g *recordingGate) Approve(_ context.Context, approvalID, tenantID, userID string) error {
	g.calls = append(g.calls, fmt.Sprintf("%s|%s|%s", approvalID, tenantID, userID))
	return g.err
}

type convFixture struct {
	srv   *Server
	gate  *recordingGate
	store *conversation.Store
}

func newConversationServer(t *testing.T, reader conversation.SurfaceReader) *convFixture {
	t.Helper()
	srv := New(nil, config.Config{})
	store := conversation.NewStore(time.Now)
	gate := &recordingGate{}
	mount := &ConversationMount{
		Store:     store,
		Approvals: gate,
		Workflows: stubWorkflows{owned: map[string]map[string]bool{
			"tenant-a": {"wf_1": true},
			"tenant-b": {"wf_secret": true},
		}},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: time.Now,
	}
	mount.Runner = &conversation.Runner{
		Store: store, Router: conversation.NewRouter(), Reader: reader,
		Budgets: stubBudgets{}, Now: time.Now,
	}
	srv.MountConversations(mount)
	return &convFixture{srv: srv, gate: gate, store: store}
}

var (
	personA  = auth.Principal{TenantID: "tenant-a", UserID: "usr_alice", Role: "owner", APIKeyID: "k1"}
	personA2 = auth.Principal{TenantID: "tenant-a", UserID: "usr_bob", Role: "member", APIKeyID: "k2"}
	personB  = auth.Principal{TenantID: "tenant-b", UserID: "usr_mal", Role: "owner", APIKeyID: "k3"}
	machineA = auth.Principal{TenantID: "tenant-a", Role: "member", APIKeyID: "ci"}
)

func (f *convFixture) do(t *testing.T, p auth.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	req = req.WithContext(auth.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	f.srv.Mux.ServeHTTP(rec, req)
	return rec
}

func (f *convFixture) newConversation(t *testing.T, p auth.Principal, workflowID string) string {
	t.Helper()
	rec := f.do(t, p, "POST", "/api/v1/conversations", `{"workflow_id":"`+workflowID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var out conversationView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.ConversationID
}

// waitForTerminal polls the store until the turn ends. The turn runs on a DETACHED goroutine by design
// (FR7), so a test that read immediately after the POST would be racing the run rather than testing it.
func (f *convFixture) waitForTerminal(t *testing.T, convID string, owner conversation.Owner) []conversation.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := f.store.Messages(convID, owner, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) > 0 && msgs[len(msgs)-1].Kind.Terminal() {
			return msgs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the turn never reached a terminal message")
	return nil
}

// ── 2.1 · create, and the non-disclosing refusal ─────────────────────────────────────────────────

func TestCreateBindsAConversationToAWorkflowTheTenantOwns(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	rec := f.do(t, personA, "POST", "/api/v1/conversations", `{"workflow_id":"wf_1"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out conversationView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.ConversationID == "" || out.RunID == "" {
		t.Errorf("create returned no ids: %+v", out)
	}
	// 🔴 Task 4.10 arrives as DATA, not as a sentence in the console. The day ADR-015's Q1 is revisited
	// the console must not still be promising the old behaviour from a string literal.
	if out.Persistence == "" {
		t.Error("the response does not say what a reload preserves; users discover that by losing a transcript")
	}
}

func TestCrossTenantCreateDoesNotDiscloseThatTheWorkflowExists(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	// `wf_secret` EXISTS and belongs to tenant-b.
	real := f.do(t, personA, "POST", "/api/v1/conversations", `{"workflow_id":"wf_secret"}`)
	fake := f.do(t, personA, "POST", "/api/v1/conversations", `{"workflow_id":"wf_does_not_exist"}`)
	if real.Code != http.StatusNotFound {
		t.Fatalf("a cross-tenant create returned %d; want 404", real.Code)
	}
	if real.Code != fake.Code || real.Body.String() != fake.Body.String() {
		t.Errorf("a workflow that exists elsewhere (%d %s) is distinguishable from one that does not "+
			"(%d %s).\nThat difference is an oracle: a caller can walk the id space and enumerate "+
			"another organization's workflows.",
			real.Code, strings.TrimSpace(real.Body.String()), fake.Code, strings.TrimSpace(fake.Body.String()))
	}
}

func TestAMachineCredentialHasNoConversation(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	rec := f.do(t, machineA, "POST", "/api/v1/conversations", `{"workflow_id":"wf_1"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d; a conversation is per-person, and a credential naming nobody would make "+
			"every machine credential in an organization share one scope", rec.Code)
	}
}

// ── 2.2 · a turn runs, and the transcript has the right shape ────────────────────────────────────

func TestATurnRunsAndProducesAPlanThenAResult(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	convID := f.newConversation(t, personA, "wf_1")
	rec := f.do(t, personA, "POST", "/api/v1/conversation-turns",
		`{"conversation_id":"`+convID+`","question":"what does this node remember between calls?"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("turn: %d %s", rec.Code, rec.Body.String())
	}
	var out turnView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.TraceID == "" {
		t.Error("the turn returned no trace id; a person whose turn is stuck at four minutes needs it most")
	}

	owner := conversation.Owner{TenantID: personA.TenantID, UserID: personA.UserID}
	msgs := f.waitForTerminal(t, convID, owner)
	if msgs[0].Kind != conversation.KindPlan {
		t.Errorf("first message is %q, want plan", msgs[0].Kind)
	}
	plan := msgs[0].Plan
	if !plan.Budget.Complete() {
		t.Errorf("the plan declares an incomplete envelope: %+v", plan.Budget)
	}
	last := msgs[len(msgs)-1]
	if last.Kind != conversation.KindResult {
		t.Fatalf("last message is %q, want result", last.Kind)
	}
	if len(last.Result.Reconciliation) != len(plan.Steps) {
		t.Errorf("%d reconciliation entries for %d planned steps",
			len(last.Result.Reconciliation), len(plan.Steps))
	}
	for _, m := range msgs {
		if m.TraceID != out.TraceID {
			t.Errorf("a message carries trace %q; the turn's is %q", m.TraceID, out.TraceID)
			break
		}
	}
}

func TestATurnOnSomebodyElsesConversationIsNotFound(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	convID := f.newConversation(t, personA, "wf_1")
	// A COLLEAGUE in the same tenant: per-person scope.
	rec := f.do(t, personA2, "POST", "/api/v1/conversation-turns",
		`{"conversation_id":"`+convID+`","question":"what happened in that run?"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("a colleague posted a turn on another person's conversation: %d", rec.Code)
	}
}

// ── 2.3 · the stream, its state preamble and its resume ──────────────────────────────────────────

// sseFrame is one parsed `id:`/`event:`/`data:` frame.
type sseFrame struct {
	id    string
	event string
	data  string
}

// readSSE parses frames from a recorder's body. Deliberately a real parser rather than a substring
// search: the assertions below are about IDS AND ORDER, and a substring search would pass on a body
// that delivered the right messages in the wrong sequence.
func readSSE(body string) []sseFrame {
	var out []sseFrame
	var cur sseFrame
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.event != "" || cur.data != "" {
				out = append(out, cur)
			}
			cur = sseFrame{}
		case strings.HasPrefix(line, "id: "):
			cur.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		}
	}
	if cur.event != "" || cur.data != "" {
		out = append(out, cur)
	}
	return out
}

// streamOnce opens the stream against a FINISHED turn and returns its frames. The handler blocks until
// the client's context ends, so the request carries a short deadline: the backlog is written before the
// first select, which is exactly the replay path under test.
func (f *convFixture) streamOnce(t *testing.T, p auth.Principal, query string) []sseFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/v1/conversation-stream?"+query, nil)
	req = req.WithContext(auth.WithPrincipal(ctx, p))
	rec := httptest.NewRecorder()
	f.srv.Mux.ServeHTTP(rec, req)
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q, want text/event-stream (body: %s)", ct, rec.Body.String())
	}
	// 🔴 The header that decides whether this is a stream or a batch. A proxy that buffers turns SSE
	// into "everything arrives at the end", which does not error and is indistinguishable from slowness.
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want \"no\"", got)
	}
	return readSSE(rec.Body.String())
}

func TestTheStreamReplaysTheTranscriptAndLeadsWithTheRunsOwnState(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	convID := f.newConversation(t, personA, "wf_1")
	f.do(t, personA, "POST", "/api/v1/conversation-turns",
		`{"conversation_id":"`+convID+`","question":"what does this node remember between calls?"}`)
	owner := conversation.Owner{TenantID: personA.TenantID, UserID: personA.UserID}
	all := f.waitForTerminal(t, convID, owner)

	frames := f.streamOnce(t, personA, "conversation_id="+convID)
	if len(frames) == 0 {
		t.Fatal("the stream delivered nothing")
	}
	// 🔴 TASK 2.18. The state preamble comes FIRST and is read from the run, so there is no window in
	// which the browser has to reconstruct state from the transcript.
	if frames[0].event != "state" {
		t.Fatalf("the first frame is %q; the run's own state must precede any message", frames[0].event)
	}
	var st conversation.TurnState
	if err := json.Unmarshal([]byte(frames[0].data), &st); err != nil {
		t.Fatal(err)
	}
	if st.Phase == "" || !st.Terminal || st.Envelope.TokenBudget == 0 {
		t.Errorf("the state preamble is incomplete: %+v", st)
	}

	var delivered []string
	for _, fr := range frames {
		if fr.event == "message" {
			delivered = append(delivered, fr.id)
		}
	}
	if len(delivered) != len(all) {
		t.Fatalf("the stream replayed %d messages; the transcript has %d", len(delivered), len(all))
	}
	for i, m := range all {
		if delivered[i] != fmt.Sprint(m.ID) {
			t.Fatalf("frame %d has id %q, want %d", i, delivered[i], m.ID)
		}
	}
}

func TestResumeFromAnAcknowledgedIDSkipsWhatTheClientAlreadyHas(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	convID := f.newConversation(t, personA, "wf_1")
	f.do(t, personA, "POST", "/api/v1/conversation-turns",
		`{"conversation_id":"`+convID+`","question":"what does this node remember between calls?"}`)
	owner := conversation.Owner{TenantID: personA.TenantID, UserID: personA.UserID}
	all := f.waitForTerminal(t, convID, owner)
	if len(all) < 3 {
		t.Fatalf("the turn produced only %d messages; this test needs at least three", len(all))
	}
	ack := all[1].ID

	for _, tc := range []struct {
		name  string
		query string
	}{
		{"query cursor", fmt.Sprintf("conversation_id=%s&after=%d", convID, ack)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := f.streamOnce(t, personA, tc.query)
			var got []int64
			for _, fr := range frames {
				if fr.event != "message" {
					continue
				}
				var m conversation.Message
				if err := json.Unmarshal([]byte(fr.data), &m); err != nil {
					t.Fatal(err)
				}
				got = append(got, m.ID)
			}
			if len(got) != len(all)-2 {
				t.Fatalf("resumed with %d messages, want %d", len(got), len(all)-2)
			}
			for i, id := range got {
				if want := all[i+2].ID; id != want {
					t.Fatalf("resume delivered %v; want the tail after id %d — a duplicate or a gap "+
						"here is intermittent in production and invisible in a screenshot", got, ack)
				}
			}
		})
	}
}

// TestLastEventIDResumesAnAutomaticBrowserReconnect covers the path a client never writes code for: an
// `EventSource` reconnecting on its own sends `Last-Event-ID` and nothing else.
func TestLastEventIDResumesAnAutomaticBrowserReconnect(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	convID := f.newConversation(t, personA, "wf_1")
	f.do(t, personA, "POST", "/api/v1/conversation-turns",
		`{"conversation_id":"`+convID+`","question":"what does this node remember between calls?"}`)
	owner := conversation.Owner{TenantID: personA.TenantID, UserID: personA.UserID}
	all := f.waitForTerminal(t, convID, owner)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/v1/conversation-stream?conversation_id="+convID, nil)
	req.Header.Set("Last-Event-ID", fmt.Sprint(all[0].ID))
	req = req.WithContext(auth.WithPrincipal(ctx, personA))
	rec := httptest.NewRecorder()
	f.srv.Mux.ServeHTTP(rec, req)

	n := 0
	for _, fr := range readSSE(rec.Body.String()) {
		if fr.event == "message" {
			n++
		}
	}
	if n != len(all)-1 {
		t.Errorf("an automatic reconnect replayed %d messages, want %d; without honouring "+
			"Last-Event-ID a browser's own reconnect re-renders the whole transcript", n, len(all)-1)
	}
}

func TestTheStreamOfAnotherPersonsConversationIsNotFound(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	convID := f.newConversation(t, personA, "wf_1")
	req := httptest.NewRequest("GET", "/api/v1/conversation-stream?conversation_id="+convID, nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), personB))
	rec := httptest.NewRecorder()
	f.srv.Mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("another tenant opened the stream: %d", rec.Code)
	}
}

// ── 2.4 · the approval forwards, and adds no gate ────────────────────────────────────────────────

func TestAnApprovalForwardsThePersonAndTenantFromTheSession(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	convID := f.newConversation(t, personA, "wf_1")
	rec := f.do(t, personA, "POST", "/api/v1/conversation-approvals",
		`{"conversation_id":"`+convID+`","approval_id":"prop_1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	if len(f.gate.calls) != 1 {
		t.Fatalf("the gate was called %d times, want once", len(f.gate.calls))
	}
	// 🔴 The person and tenant come from the SESSION. Nothing in the body names either, so there is
	// nothing to spoof — which is the untrusted-source boundary's NFR-S3 as a property of the shape.
	if got, want := f.gate.calls[0], "prop_1|tenant-a|usr_alice"; got != want {
		t.Errorf("the gate received %q, want %q", got, want)
	}
}

func TestABodyCannotNameItsOwnTenantOrPerson(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	convID := f.newConversation(t, personA, "wf_1")
	// `DisallowUnknownFields` is what makes the attempt a 400 rather than a silently ignored field.
	// Silently ignoring it would work correctly today and stop working the day somebody adds a
	// `tenant_id` field for an unrelated reason.
	rec := f.do(t, personA, "POST", "/api/v1/conversation-approvals",
		`{"conversation_id":"`+convID+`","approval_id":"prop_1","tenant_id":"tenant-b"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a body naming a tenant was accepted with %d", rec.Code)
	}
}

// ── 2.17 · the trace ─────────────────────────────────────────────────────────────────────────────

func TestATraceResolvesForItsOwnerAndIsNotFoundForAnybodyElse(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	convID := f.newConversation(t, personA, "wf_1")
	rec := f.do(t, personA, "POST", "/api/v1/conversation-turns",
		`{"conversation_id":"`+convID+`","question":"what does this node remember between calls?"}`)
	var out turnView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	f.waitForTerminal(t, convID, conversation.Owner{TenantID: personA.TenantID, UserID: personA.UserID})

	mine := f.do(t, personA, "GET", "/api/v1/conversation-trace?trace_id="+out.TraceID, "")
	if mine.Code != http.StatusOK {
		t.Fatalf("the owner could not read their own trace: %d %s", mine.Code, mine.Body.String())
	}
	var st conversation.TurnState
	if err := json.Unmarshal(mine.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Terminal || st.Stop == "" {
		t.Errorf("the trace resolves to an incomplete state: %+v", st)
	}

	// 🔴 The trace EXISTS. The answer must not say so.
	theirs := f.do(t, personB, "GET", "/api/v1/conversation-trace?trace_id="+out.TraceID, "")
	invented := f.do(t, personB, "GET", "/api/v1/conversation-trace?trace_id=deadbeefdeadbeef", "")
	if theirs.Code != http.StatusNotFound {
		t.Errorf("a cross-tenant trace lookup returned %d", theirs.Code)
	}
	if theirs.Body.String() != invented.Body.String() {
		t.Errorf("a real trace (%s) is distinguishable from an invented one (%s)",
			strings.TrimSpace(theirs.Body.String()), strings.TrimSpace(invented.Body.String()))
	}
}

// ── 2.18 · resume ignores the client's claimed history ───────────────────────────────────────────

// TestTamperedClientHistoryChangesNothing is task 6.18, stated at the transport.
//
// 🔴 The property is a consequence of WHERE the numbers live, not of a validation. The client is allowed
// to say exactly one thing — the id of the last message it saw — and everything else is read from the
// run. So the test asserts that inventing extra query parameters changes nothing about the answer.
func TestTamperedClientHistoryChangesNothing(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	convID := f.newConversation(t, personA, "wf_1")
	f.do(t, personA, "POST", "/api/v1/conversation-turns",
		`{"conversation_id":"`+convID+`","question":"what does this node remember between calls?"}`)
	owner := conversation.Owner{TenantID: personA.TenantID, UserID: personA.UserID}
	f.waitForTerminal(t, convID, owner)

	honest := f.streamOnce(t, personA, "conversation_id="+convID)
	tampered := f.streamOnce(t, personA, "conversation_id="+convID+
		"&remaining_tokens=999999&phase=understand&completed_steps=0&budget=unlimited")
	if len(honest) != len(tampered) {
		t.Fatalf("a client's invented state changed the response: %d frames vs %d", len(honest), len(tampered))
	}
	for i := range honest {
		if honest[i].data != tampered[i].data {
			t.Fatalf("frame %d differs when the client claims its own state.\nhonest:   %s\ntampered: %s",
				i, honest[i].data, tampered[i].data)
		}
	}
}

// ── input bounds ─────────────────────────────────────────────────────────────────────────────────

func TestAQuestionLongerThanTheBoundIsRefused(t *testing.T) {
	f := newConversationServer(t, stubReader{})
	convID := f.newConversation(t, personA, "wf_1")
	// A repository's worth of text arriving through the front door is the untrusted-source boundary
	// without a file in the middle.
	huge := strings.Repeat("a", maxQuestionBytes+1)
	rec := f.do(t, personA, "POST", "/api/v1/conversation-turns",
		`{"conversation_id":"`+convID+`","question":"`+huge+`"}`)
	if rec.Code != http.StatusRequestEntityTooLarge && rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d; a question of %d bytes was accepted", rec.Code, len(huge))
	}
}

// ── 2.8 · the pinned-inference replay path ───────────────────────────────────────────────────────

// fakeInferences is a ConversationPinSource over fixed rows. It COUNTS its reads, because the property
// under test is that a replay is a store read and nothing more.
type fakeInferences struct {
	byKey     map[string]herosagent.Stored
	latest    herosagent.Stored
	hasLatest bool
	gets      int
}

func (f *fakeInferences) Get(_ context.Context, workflowID, sourceRevision, hash string) (herosagent.Stored, bool, error) {
	f.gets++
	s, ok := f.byKey[workflowID+"|"+sourceRevision+"|"+hash]
	return s, ok, nil
}

func (f *fakeInferences) LatestFor(context.Context, string, string) (herosagent.Stored, bool, error) {
	return f.latest, f.hasLatest, nil
}

type fixedRevision struct {
	revision string
	found    bool
}

func (f fixedRevision) Latest(string, string) (linkingest.WorkflowIR, bool, error) {
	return linkingest.WorkflowIR{SourceRevision: f.revision}, f.found, nil
}

func stored(tenant, workflow, revision, hash, narrative string) herosagent.Stored {
	return herosagent.Stored{
		InferenceID: "inf_" + revision, TenantID: tenant, WorkflowID: workflow,
		SourceRevision: revision, AgentConfigHash: hash, Narrative: narrative,
	}
}

func pinsFor(src *fakeInferences, revision, hash string) storedPins {
	return storedPins{
		inferences: src,
		structure:  fixedRevision{revision: revision, found: revision != ""},
		configHash: func(context.Context) string { return hash },
	}
}

func TestAPinIsResolvedOnTheThreePartKey(t *testing.T) {
	src := &fakeInferences{byKey: map[string]herosagent.Stored{
		"wf_1|rev_current|hash_a": stored("tenant-a", "wf_1", "rev_current", "hash_a", "this node keeps no memory"),
	}}
	spec, _ := conversation.Lookup(conversation.IntentMemory)

	pin, err := pinsFor(src, "rev_current", "hash_a").Resolve(context.Background(), "tenant-a", "wf_1", spec)
	if err != nil || !pin.Found {
		t.Fatalf("pin = %+v, err = %v; want a hit on the exact key", pin, err)
	}
	if pin.Stale() {
		t.Error("a pin at the current revision reported itself stale")
	}
	if pin.Reading.EvidenceRef != "inference:inf_rev_current" {
		t.Errorf("evidence ref = %q; a replayed claim must point at the row that produced it, not at a "+
			"page that recomputes something similar", pin.Reading.EvidenceRef)
	}
}

// TestAPinFromADifferentAgentConfigurationIsNotAPin is the half of the three-part key that is easy to
// drop, and dropping it inverts the determinism guarantee into a determinism lie.
func TestAPinFromADifferentAgentConfigurationIsNotAPin(t *testing.T) {
	old := stored("tenant-a", "wf_1", "rev_current", "hash_OLD", "written by a retired definition")
	src := &fakeInferences{
		byKey:  map[string]herosagent.Stored{},
		latest: old, hasLatest: true,
	}
	spec, _ := conversation.Lookup(conversation.IntentMemory)
	pin, err := pinsFor(src, "rev_current", "hash_NEW").Resolve(context.Background(), "tenant-a", "wf_1", spec)
	if err != nil {
		t.Fatal(err)
	}
	if pin.Found {
		t.Fatal("an inference from another agent configuration was replayed; it would attribute one " +
			"configuration's reasoning to another, which no label could honestly describe")
	}
}

// TestAPinFromAnotherTenantIsNotAPin guards the one gap the three-part key leaves open: it contains no
// tenant, so two organizations that both call their workflow `main` share a key.
func TestAPinFromAnotherTenantIsNotAPin(t *testing.T) {
	src := &fakeInferences{byKey: map[string]herosagent.Stored{
		"wf_1|rev_current|hash_a": stored("tenant-b", "wf_1", "rev_current", "hash_a", "somebody else's analysis"),
	}}
	spec, _ := conversation.Lookup(conversation.IntentMemory)
	pin, err := pinsFor(src, "rev_current", "hash_a").Resolve(context.Background(), "tenant-a", "wf_1", spec)
	if err != nil {
		t.Fatal(err)
	}
	if pin.Found {
		t.Fatal("a stored row belonging to another tenant was replayed as this tenant's pin")
	}
}

func TestAPinTakenAtAnEarlierRevisionIsStaleAndNamesIt(t *testing.T) {
	older := stored("tenant-a", "wf_1", "rev_old", "hash_a", "this node keeps no memory")
	src := &fakeInferences{byKey: map[string]herosagent.Stored{}, latest: older, hasLatest: true}
	spec, _ := conversation.Lookup(conversation.IntentMemory)

	pin, err := pinsFor(src, "rev_current", "hash_a").Resolve(context.Background(), "tenant-a", "wf_1", spec)
	if err != nil || !pin.Found {
		t.Fatalf("pin = %+v, err = %v; a stale pin is SERVED, not refused — refusing applies P30's "+
			"operator rule to a customer and is too rigid", pin, err)
	}
	if !pin.Stale() {
		t.Fatal("a pin taken at an earlier revision did not report itself stale")
	}
	if pin.SourceRevision != "rev_old" {
		t.Errorf("source revision = %q; 'stale' with no revision is a warning about nothing", pin.SourceRevision)
	}
}

// TestAnInferenceWithNoNarrativeReplaysAsNotMeasured is the state that would otherwise render as an
// empty card — which on a conversational surface reads as "nothing is wrong here".
func TestAnInferenceWithNoNarrativeReplaysAsNotMeasured(t *testing.T) {
	src := &fakeInferences{byKey: map[string]herosagent.Stored{
		"wf_1|rev_current|hash_a": stored("tenant-a", "wf_1", "rev_current", "hash_a", "   "),
	}}
	spec, _ := conversation.Lookup(conversation.IntentMemory)
	pin, err := pinsFor(src, "rev_current", "hash_a").Resolve(context.Background(), "tenant-a", "wf_1", spec)
	if err != nil || !pin.Found {
		t.Fatalf("pin = %+v, err = %v", pin, err)
	}
	if pin.Reading.State != conversation.FindingNotMeasured {
		t.Errorf("state = %q, want not_measured", pin.Reading.State)
	}
	if pin.Reading.MissingInput == "" {
		t.Error("the replayed not_measured names no missing input")
	}
}

// TestNothingIsPinnedWithoutAnActiveDefinition guards the empty-hash case, which would otherwise match
// rows from every configuration at once.
func TestNothingIsPinnedWithoutAnActiveDefinition(t *testing.T) {
	src := &fakeInferences{
		byKey:  map[string]herosagent.Stored{"wf_1|rev_current|": stored("tenant-a", "wf_1", "rev_current", "", "x")},
		latest: stored("tenant-a", "wf_1", "rev_current", "", "x"), hasLatest: true,
	}
	spec, _ := conversation.Lookup(conversation.IntentMemory)
	pin, err := pinsFor(src, "rev_current", "").Resolve(context.Background(), "tenant-a", "wf_1", spec)
	if err != nil {
		t.Fatal(err)
	}
	if pin.Found {
		t.Fatal("a replay keyed on an empty config hash matched a row")
	}
	if src.gets != 0 {
		t.Errorf("the store was read %d times with no active definition; a replay that cannot be keyed "+
			"must not be attempted", src.gets)
	}
}
