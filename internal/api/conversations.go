package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/conversation"
	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// conversations.go is P31's transport: five routes, and nothing else.
//
// # 🔴 The paths are FLAT, and that is a security decision rather than a style one
//
// `POST /api/v1/conversations/{id}/turns` is the obvious shape and it cannot be published safely. A
// production ingress rule matches a path, and an `Exact` rule cannot match a path with a variable
// segment — so a parameterised route can only be published by a `Prefix` rule, and a `Prefix` rule under
// `/api/v1/conversations/` publishes every sibling anybody adds later, forever, without a review.
// P29 already learned this on `/api/v1/workflows/{id}/ir` (three commands 404'd at the edge behind a
// green build) and the remedy there was the same: the flat shape, not a wider fence.
//
// So the identifiers ride the body or the query, and every one of these five is publishable `Exact`.
//
// # What this file does NOT do
//
// It does not decide whether a message is sayable — `conversation.Emitter` does, and it is the only
// door. It does not gate an approval — `internal/approval` does, and this forwards. It does not compute
// anything the browser renders. The handlers here authenticate, scope to the session's PERSON and
// tenant, and move bytes.
//
// # Scope is the session's person, never the request's (ADR-008, ADR-015)
//
// Every handler derives `conversation.Owner` from `auth.Principal`. 🚫 No field in any request body
// names a tenant or a person, so there is nothing to spoof and nothing to validate. A conversation
// belonging to somebody else is `not found` — one answer for "does not exist" and "is not yours",
// because two answers are an enumeration oracle.

// ConversationApprovalGate records an approval. Satisfied by a thin adapter over `internal/approval`.
//
// 🔴 It is one method with no policy of its own. The moment this interface grows a "may this person
// approve this?" it has become a SECOND approval gate, which is exactly what design.md D4 forbids: a
// second place for the entitlement check, the automation-level check and the attribution to be wrong.
type ConversationApprovalGate interface {
	// Approve forwards to the existing gate. tenantID and userID come from the authenticated session.
	Approve(ctx context.Context, approvalID, tenantID, userID string) error
}

// ConversationWorkflows answers whether a tenant owns a workflow.
//
// Its answer is used to REFUSE, and the refusal must not disclose existence — so the handler turns both
// "no such workflow" and "not yours" into the same 404. The interface returns a bool rather than an
// error-with-a-reason precisely so a call site cannot accidentally surface the difference.
type ConversationWorkflows interface {
	OwnsWorkflow(tenantID, workflowID string) (bool, error)
}

// ConversationMount is everything the conversational surface needs, supplied by the boot path.
//
// A struct rather than an interface because the store and the runner are concrete types with no
// alternative implementation, and an interface over them would be an abstraction invented to satisfy a
// convention. The three genuinely substitutable things — the approval gate, the workflow ownership
// check and the artifact resolvers — ARE interfaces.
type ConversationMount struct {
	Store     *conversation.Store
	Runner    *conversation.Runner
	Approvals ConversationApprovalGate
	Workflows ConversationWorkflows
	Resolvers conversation.Resolvers
	Log       *slog.Logger
	// Now is the injected clock.
	Now func() time.Time
	// Observe records events from the central enum. Optional.
	Observe func(eventname.Name, map[string]any)

	// streams counts open conversation streams and bounds them (§5.3, §5.4). Created by
	// MountConversations; never nil once mounted.
	streams *streamGauge
}

// MountConversations registers the five routes. Call after New.
//
// A nil mount registers nothing at all, so a deployment without the surface answers 404 for a route it
// does not have rather than 503 for one it does — the distinction `accounts` already draws, and the
// right one here: the conversational console is a whole product surface, not a read model that might be
// absent. A person whose deployment does not ship it is told by the console's own navigation.
func (s *Server) MountConversations(m *ConversationMount) {
	if m == nil || m.Store == nil || m.Runner == nil {
		return
	}
	if m.Now == nil {
		m.Now = time.Now
	}
	m.streams = newStreamGauge(m.Now)
	s.conversations = m
	// 🔴 Written as LITERALS, matching every other mount in this package. `registeredRoutes` in
	// ingress_fence_test.go extracts `HandleFunc` arguments from the SOURCE, so a concatenation or a
	// constant is invisible to it — a route registered that way is a route the ingress fence cannot see.
	s.Mux.HandleFunc("POST /api/v1/conversations", s.handleConversationCreate)
	s.Mux.HandleFunc("POST /api/v1/conversation-turns", s.handleConversationTurn)
	s.Mux.HandleFunc("GET /api/v1/conversation-stream", s.handleConversationStream)
	s.Mux.HandleFunc("POST /api/v1/conversation-approvals", s.handleConversationApproval)
	s.Mux.HandleFunc("GET /api/v1/conversation-trace", s.handleConversationTrace)
}

// conversationOwner resolves the session's person and tenant, or writes the refusal.
//
// 🔴 A MACHINE credential is refused. A conversation is per-person (ADR-015), so a credential that
// names nobody has no conversation to be in — and admitting one would make `Owner.UserID` empty, at
// which point every machine credential in an organization shares one conversation scope.
func (s *Server) conversationOwner(w http.ResponseWriter, r *http.Request) (conversation.Owner, bool) {
	principal, ok := auth.PrincipalFrom(r.Context())
	if !ok || principal.TenantID == "" {
		writeJSON(w, http.StatusUnauthorized, specError{
			Error: "the conversational console requires an authenticated tenant"})
		return conversation.Owner{}, false
	}
	if !principal.Personal() {
		writeJSON(w, http.StatusForbidden, specError{
			Error: "a conversation belongs to a person; this credential names none. " +
				"Sign in to the console, or use the machine surfaces under /api/v1/workflows."})
		return conversation.Owner{}, false
	}
	return conversation.Owner{TenantID: principal.TenantID, UserID: principal.UserID}, true
}

// ── POST /api/v1/conversations ───────────────────────────────────────────────────────────────────

type createConversationRequest struct {
	WorkflowID string `json:"workflow_id"`
}

type conversationView struct {
	ConversationID string `json:"conversation_id"`
	WorkflowID     string `json:"workflow_id"`
	RunID          string `json:"run_id"`
	CreatedAt      string `json:"created_at"`
	// Persistence states, on the wire, what a reload preserves and what it does not (task 4.10). 🔴 A
	// FIELD rather than a hard-coded sentence in the console, because the day ADR-015's Q1 is revisited
	// the console must not still be promising the old behaviour from a string literal.
	Persistence string `json:"persistence"`
}

// conversationPersistenceNote is ADR-015's consequence, said out loud.
const conversationPersistenceNote = "This conversation lives with its run. Reloading resumes the run " +
	"and replays its messages; it does not restore a history spanning runs, and nothing you type here " +
	"is kept after the run ends."

func (s *Server) handleConversationCreate(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.conversationOwner(w, r)
	if !ok {
		return
	}
	var req createConversationRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the request body is not a conversation: " + err.Error()})
		return
	}
	req.WorkflowID = strings.TrimSpace(req.WorkflowID)
	if req.WorkflowID == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "a conversation names the workflow it is about"})
		return
	}
	if s.conversations.Workflows != nil {
		owns, err := s.conversations.Workflows.OwnsWorkflow(owner.TenantID, req.WorkflowID)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, specError{Error: "the workflow catalogue could not be read"})
			return
		}
		if !owns {
			// 🔴 404, and the SAME 404 for "no such workflow". Answering 403 for a workflow that exists
			// and 404 for one that does not turns this route into an oracle a caller can walk to
			// enumerate another organization's workflow ids.
			writeJSON(w, http.StatusNotFound, specError{Error: "no such workflow"})
			return
		}
	}

	id := "conv_" + randomID()
	runID := "run_" + randomID()
	c, err := s.conversations.Store.Create(id, owner, req.WorkflowID, runID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, specError{Error: "the conversation could not be opened"})
		return
	}
	writeJSON(w, http.StatusCreated, conversationView{
		ConversationID: c.ID, WorkflowID: c.WorkflowID, RunID: c.RunID,
		CreatedAt: c.CreatedAt.Format(time.RFC3339), Persistence: conversationPersistenceNote,
	})
}

// ── POST /api/v1/conversation-turns ──────────────────────────────────────────────────────────────

type turnRequest struct {
	ConversationID string `json:"conversation_id"`
	Question       string `json:"question"`
}

type turnView struct {
	TurnID string `json:"turn_id"`
	// TraceID is displayed by the console and is copyable (task 4.14, FR23).
	TraceID string `json:"trace_id"`
	// FirstMessageID lets the client open the stream with an acknowledgement cursor that cannot miss
	// the plan: it subscribes from `first_message_id - 1`.
	FirstMessageID int64 `json:"first_message_id"`
}

// maxQuestionBytes bounds a question. Large enough for a paragraph, small enough that a repository's
// contents cannot be pasted in as an "instruction" — which is the untrusted-source boundary arriving
// through the front door rather than through a file.
const maxQuestionBytes = 4096

func (s *Server) handleConversationTurn(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.conversationOwner(w, r)
	if !ok {
		return
	}
	var req turnRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxQuestionBytes+1<<12))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the request body is not a turn: " + err.Error()})
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "a turn carries a question"})
		return
	}
	if len(req.Question) > maxQuestionBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, specError{
			Error: "that question is longer than this surface accepts; ask about one thing at a time"})
		return
	}
	conv, err := s.conversations.Store.Get(req.ConversationID, owner)
	if err != nil {
		writeJSON(w, http.StatusNotFound, specError{Error: "no such conversation"})
		return
	}

	turnID := "turn_" + randomID()
	// 🔴 The trace id is DERIVED from the turn id rather than minted. `telemetry.TraceID` is what the
	// run's own spans use, so a log line, a span and this response join without a translation table —
	// the property `tracecontext.go` exists to keep, and the one an incident depends on.
	traceID := telemetry.TraceID(turnID)

	em := &conversation.Emitter{
		ConversationID: conv.ID,
		TurnID:         turnID,
		TenantID:       owner.TenantID,
		TraceID:        traceID,
		// Three DIFFERENT identities, deliberately (task 2.10): the request's own identity, the turn's
		// trace, and the turn's span. An operator handed one of the three can reach the other two.
		RequestID:  telemetry.TraceIDFromContext(r.Context()),
		SpanID:     telemetry.RunSpanID(turnID),
		Provenance: conversation.ProvenanceGenerated,
		Resolvers:  s.conversations.Resolvers,
		Sink:       s.conversations.Store,
		Log:        s.conversations.Log,
		Now:        s.conversations.Now,
		Observe:    s.conversations.Observe,
	}

	// 🔴 The turn runs in its own goroutine on a context DETACHED from the request. FR7: a closed tab
	// does not cancel a run, and neither does the POST returning. Cancellation is an explicit act with
	// its own message, and a run that died because a browser was closed would be indistinguishable from
	// one that finished — the failure the whole lifecycle section exists to prevent.
	//
	// PRD §14 Q5 is the same decision one layer out: a session expiring mid-run does not cancel it. The
	// run was authorized when it started; its result is retrievable by the TENANT, not by the expired
	// session.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), turnHardDeadline)
	go func() {
		defer cancel()
		if _, err := s.conversations.Runner.Run(runCtx, conversation.TurnRequest{
			ConversationID: conv.ID, Owner: owner, WorkflowID: conv.WorkflowID,
			TurnID: turnID, TraceID: traceID, Question: req.Question, Emitter: em,
		}); err != nil {
			log := s.conversations.Log
			if log == nil {
				log = slog.Default()
			}
			log.Error("conversation turn failed",
				"event", eventname.ConversationRefused.String(),
				"request_id", em.RequestID, "trace_id", traceID, "span_id", em.SpanID,
				"cause", err.Error())
		}
	}()

	writeJSON(w, http.StatusAccepted, turnView{TurnID: turnID, TraceID: traceID, FirstMessageID: 0})
}

// turnHardDeadline is the outermost bound on a detached turn. It is NOT the wall-clock ceiling — that
// is the plan's declared limit and is what a stop reason names. This is the backstop that stops a
// goroutine outliving the process's usefulness if the accounting itself is wrong, and it is deliberately
// far above any plan's ceiling so it never fires in normal operation and never masks a real limit.
const turnHardDeadline = 30 * time.Minute

// ── GET /api/v1/conversation-stream ──────────────────────────────────────────────────────────────

// streamHeartbeat is how often a comment frame is written while nothing else is.
//
// The spec's rule is that no interval longer than 15 seconds passes without a message while the run is
// non-terminal, and the runner's `progress` cadence is what satisfies it. This is the TRANSPORT's own
// keep-alive, at a shorter interval, and it exists for a different reason: an idle TCP connection
// through a load balancer with a 60-second idle timeout is closed silently, and the client then
// reconnects every minute forever while everything looks healthy.
const streamHeartbeat = 10 * time.Second

func (s *Server) handleConversationStream(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.conversationOwner(w, r)
	if !ok {
		return
	}
	conversationID := r.URL.Query().Get("conversation_id")
	if _, err := s.conversations.Store.Get(conversationID, owner); err != nil {
		writeJSON(w, http.StatusNotFound, specError{Error: "no such conversation"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, specError{Error: "streaming unsupported"})
		return
	}

	// The acknowledgement cursor. 🔴 It is the ONLY thing the client is allowed to tell the server
	// about the state of the world, and it is a lower bound on delivery — never a source of task state.
	// See handleConversationTrace and FR21.
	after := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("after")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeJSON(w, http.StatusBadRequest, specError{
				Error: "`after` is the id of the last message you processed, or absent"})
			return
		}
		after = parsed
	}
	// `Last-Event-ID` is the EventSource standard's own resume header, and a browser sends it
	// automatically on an automatic reconnect. Honoured when the query says nothing, so a reconnect the
	// browser performed on its own is as gapless as one the client code performed deliberately.
	if after == 0 {
		if raw := strings.TrimSpace(r.Header.Get("Last-Event-ID")); raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
				after = parsed
			}
		}
	}

	// 🔴 §5.3 · the slot is taken BEFORE the connection is held, and exhaustion REFUSES.
	//
	// Without a ceiling the failure mode is descriptor exhaustion, which does not fail the streams — it
	// fails whatever asks for a socket next, usually the orchestrator's readiness probe. The box is then
	// marked unhealthy for a reason that has nothing to do with the box, while the streams that caused
	// it keep working. A 503 naming the ceiling is a fact an operator can act on.
	ticket, admitted := s.conversations.streams.acquire()
	if !admitted {
		writeJSON(w, http.StatusServiceUnavailable, specError{
			Error: "this process is holding as many conversation streams as it will hold; " +
				"retry, or add capacity — the count and the ceiling are on /readyz"})
		return
	}
	defer s.conversations.streams.release(ticket)

	sub, err := s.conversations.Store.Subscribe(conversationID, owner, after)
	if err != nil {
		writeJSON(w, http.StatusNotFound, specError{Error: "no such conversation"})
		return
	}
	defer sub.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("Connection", "keep-alive")
	// 🔴 The one header that decides whether this is a stream or a batch. A reverse proxy that buffers
	// turns SSE into "everything arrives at the end", which does not error, does not log, and is
	// indistinguishable from slowness at the application layer (design.md D3). Asserted at the edge as
	// well as requested here, because a header is a request and an assertion is a fact.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	if after > 0 && s.conversations.Observe != nil {
		s.conversations.Observe(eventname.ConversationStreamResumed, map[string]any{
			"conversation_id": conversationID, "after": after,
		})
	}

	write := func(event string, id int64, payload any) bool {
		b, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if id > 0 {
			// The SSE `id:` field is what makes `Last-Event-ID` work on an automatic reconnect.
			_, _ = w.Write([]byte("id: " + strconv.FormatInt(id, 10) + "\n"))
		}
		_, _ = w.Write([]byte("event: " + event + "\ndata: "))
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n\n"))
		flusher.Flush()
		return true
	}

	// 🔴 THE STATE PREAMBLE IS TASK 2.18. Phase, envelope, remaining budget and completed steps, read
	// from the RUN. A client that reconnects gets the server's answer before it gets any message, so
	// there is no window in which the browser has to reconstruct state from the transcript — which is
	// the reconstruction FR21 forbids.
	for _, st := range s.turnStatesFor(conversationID, owner, sub.Backlog) {
		write("state", 0, st)
	}
	for _, m := range sub.Backlog {
		write("message", m.ID, m)
	}

	ticker := time.NewTicker(streamHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			// 🔴 The CLIENT went away. The run does not (FR7): the goroutine started by the turn route
			// is on a detached context, and its messages remain retrievable on reconnect.
			return
		case <-ticker.C:
			// A comment frame. Invisible to an EventSource consumer, fatal to an idle-connection timer.
			_, _ = w.Write([]byte(": keep-alive\n\n"))
			flusher.Flush()
		case <-sub.Notify:
			msgs, lagged := sub.Next()
			for _, m := range msgs {
				write("message", m.ID, m)
			}
			if lagged {
				// 🚫 Never drop to keep a slow consumer alive: dropping produces the gap FR6 forbids
				// and HIDES it, because the stream keeps working. Ending the response sends the client
				// back through resume with its last acknowledged id, which is a path that already
				// exists and is already fenced.
				write("lagged", 0, map[string]string{
					"error": conversation.ErrLagged.Error(),
				})
				return
			}
		}
	}
}

// turnStatesFor returns the state of every turn represented in a backlog, newest last.
//
// It reads the STORE, not the messages. The messages are what the client will render; the state is what
// the server knows, and the two are produced by different code paths on purpose — a state derived from
// the transcript would be the browser's reconstruction moved server-side, which passes the letter of
// FR21 and misses all of it.
func (s *Server) turnStatesFor(conversationID string, owner conversation.Owner, backlog []conversation.Message) []conversation.TurnState {
	seen := map[string]bool{}
	var out []conversation.TurnState
	for _, m := range backlog {
		if m.TurnID == "" || seen[m.TurnID] {
			continue
		}
		seen[m.TurnID] = true
		st, err := s.conversations.Store.TurnStateByID(m.TurnID, owner)
		if err != nil {
			continue
		}
		out = append(out, st)
	}
	_ = conversationID
	return out
}

// ── POST /api/v1/conversation-approvals ──────────────────────────────────────────────────────────

type approvalRequest struct {
	ConversationID string `json:"conversation_id"`
	ApprovalID     string `json:"approval_id"`
}

func (s *Server) handleConversationApproval(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.conversationOwner(w, r)
	if !ok {
		return
	}
	var req approvalRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, specError{Error: "the request body is not an approval: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.ApprovalID) == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "an approval names what is being approved"})
		return
	}
	if _, err := s.conversations.Store.Get(req.ConversationID, owner); err != nil {
		writeJSON(w, http.StatusNotFound, specError{Error: "no such conversation"})
		return
	}
	if s.conversations.Approvals == nil {
		writeJSON(w, http.StatusServiceUnavailable, specError{
			Error: "approvals are not mounted in this deployment"})
		return
	}
	// 🔴 FORWARD. No entitlement check here, no automation-level check here, no attribution decided
	// here — the person and the tenant come from the session and go straight through. Adding any one of
	// those would make this a second gate, and a second gate is a second place for the checks that
	// stand between a proposal and a customer's repository to be wrong.
	if err := s.conversations.Approvals.Approve(r.Context(), req.ApprovalID, owner.TenantID, owner.UserID); err != nil {
		writeJSON(w, http.StatusConflict, specError{Error: "the approval was not recorded: " + err.Error()})
		return
	}
	if s.conversations.Observe != nil {
		// AFTER the gate returned, never before: an event emitted on the way in counts intent rather
		// than effect, and a 200 is not evidence of a write.
		s.conversations.Observe(eventname.ConversationApprovalRecorded, map[string]any{
			"conversation_id": req.ConversationID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}

// ── GET /api/v1/conversation-trace ───────────────────────────────────────────────────────────────

func (s *Server) handleConversationTrace(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.conversationOwner(w, r)
	if !ok {
		return
	}
	traceID := strings.TrimSpace(r.URL.Query().Get("trace_id"))
	if traceID == "" {
		writeJSON(w, http.StatusBadRequest, specError{Error: "a trace lookup names a trace id"})
		return
	}
	st, err := s.conversations.Store.TurnStateByTrace(traceID, owner)
	if err != nil {
		// 🔴 Task 2.17. A trace belonging to another tenant is `not found`, with nothing in the
		// response that distinguishes it from one that never existed. This is the only refusal on this
		// surface where getting it wrong leaks the existence of another organization's work.
		if errors.Is(err, conversation.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, specError{Error: "no such trace"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, specError{Error: "the trace could not be read"})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// randomID mints an opaque identifier. 🚫 Never derived from a tenant, a person or a question: an id a
// caller can predict is an id a caller can address, and these name resources scoped to one person.
func randomID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A failure of the system random source is not a reason to mint a guessable id.
		return "unavailable-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}
