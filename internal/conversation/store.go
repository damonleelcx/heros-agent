package conversation

import (
	"errors"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/harnessruntime"
)

// store.go holds the run and its message log, and hands out resumable subscriptions.
//
// # Why in-process and not a table (ADR-015)
//
// A stored transcript is customer prose about customer source — a new data class with retention, export
// and deletion consequences, and a row in P23's data inventory. The eight-level rule trades that
// (levels 1 and 5) against being able to scroll back through last week's question (level 3), and level 3
// loses. So the run's message log lives for as long as the run does, in the process that is running it,
// with no query path of its own.
//
// 🔴 THE CONSEQUENCE IS REAL AND IS STATED IN THE UI: a reload resumes the RUN and replays its messages;
// it does not restore a history spanning runs. Task 4.10 puts that sentence on the screen rather than
// letting a user discover it.
//
// # Why resume reads from HERE and never from the client (FR21, task 2.18)
//
// The client sends ONE thing on reconnect: the id of the last message it processed. Everything else —
// the phase, the remaining budget, which steps completed — is read from the run. A client that claims to
// have had 90,000 tokens left changes nothing, because nothing reads that claim. The property is a
// consequence of where the number lives, not of a validation, which is why the fence for it (task 6.18)
// tampers with the claim and asserts the server's answer is unchanged.
//
// # Scope is per-person (PRD §14 Q4)
//
// Every lookup takes a tenant AND a person and returns `not found` when either fails to match. 🔴 One
// answer for "does not exist" and "is not yours", because two answers are an enumeration oracle: a
// caller that can tell them apart can discover which conversation ids exist.

// ErrNotFound is returned for a conversation, turn or trace that does not exist OR is not the caller's.
//
// 🔴 ONE error for both, deliberately. See the scope note above: distinguishing them would let a caller
// walk the id space and learn which ids are real, which is the disclosure every cross-tenant scenario in
// the spec refuses.
var ErrNotFound = errors.New("conversation: no such conversation")

// ErrLagged closes a subscription whose consumer fell too far behind to be served without a gap.
//
// 🔴 It is a CLOSE rather than a drop. Dropping messages to keep a slow consumer alive produces exactly
// the failure FR6 forbids — a gap — and hides it, because the stream keeps working. Closing sends the
// client back through resume with its last acknowledged id, which is a mechanism that already exists and
// is already tested.
var ErrLagged = errors.New("conversation: the subscriber fell behind; reconnect with your last acknowledged id")

// Owner is who a conversation belongs to. A struct rather than two strings so a call site cannot
// transpose them — `(tenantID, userID)` and `(userID, tenantID)` are both two strings and only one is
// right, and the wrong one fails open on a single-member organization.
type Owner struct {
	TenantID string
	UserID   string
}

// Conversation is a view over a run.
type Conversation struct {
	ID         string
	Owner      Owner
	WorkflowID string
	RunID      string
	CreatedAt  time.Time
}

// TurnState is what resume needs, read from the run.
// 🔴 Every field carries a `json` tag. Without them the generated console type would carry Go's
// exported names — `TurnID`, `LastMessageID` — while the wire carries whatever `encoding/json` chose,
// and the two agree only by coincidence. That is the ADR-007 failure with the generator pointed at it.
type TurnState struct {
	TurnID  string `json:"turn_id"`
	TraceID string `json:"trace_id"`
	Intent  Intent `json:"intent"`
	// Phase is where the turn is now.
	Phase Phase `json:"phase"`
	// Envelope is what was declared; Remaining is what is left. Both from the run's own accounting.
	Envelope  BudgetEnvelope  `json:"envelope"`
	Remaining BudgetRemaining `json:"remaining"`
	// Completed is every step that has resolved, and how.
	Completed map[string]StepState `json:"completed"`
	// Terminal reports whether the turn has ended.
	Terminal bool `json:"terminal"`
	// Stop is the reason it ended, empty while it has not.
	Stop harnessruntime.StopReason `json:"stop_reason"`
	// LastMessageID is the highest message id this turn has produced.
	LastMessageID int64 `json:"last_message_id"`
}

// turn is the mutable half, held by the store.
type turn struct {
	id            string
	traceID       string
	conversation  string
	owner         Owner
	intent        Intent
	phase         Phase
	budget        *Budget
	completed     map[string]StepState
	terminal      bool
	stop          harnessruntime.StopReason
	lastMessageID int64
}

// Store holds conversations, their turns and their message logs.
type Store struct {
	mu sync.Mutex

	conversations map[string]*Conversation
	turns         map[string]*turn
	// log is the ordered message record per conversation. The run's own record — resume replays it.
	log map[string][]Message
	// nextID is the monotonic message id per conversation. Per conversation rather than global so the
	// acknowledgement cursor a client sends is meaningful without also naming a conversation.
	nextID map[string]int64
	// subs are the live subscriptions per conversation.
	subs map[string][]*subscription

	now func() time.Time
}

// NewStore opens an empty store. The clock is injected for the reason budget.go gives.
func NewStore(now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{
		conversations: map[string]*Conversation{},
		turns:         map[string]*turn{},
		log:           map[string][]Message{},
		nextID:        map[string]int64{},
		subs:          map[string][]*subscription{},
		now:           now,
	}
}

// Create binds a new conversation to a workflow and a run.
func (s *Store) Create(id string, owner Owner, workflowID, runID string) (Conversation, error) {
	if id == "" || owner.TenantID == "" || owner.UserID == "" {
		return Conversation{}, errors.New("conversation: a conversation needs an id, a tenant and a person")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.conversations[id]; exists {
		return Conversation{}, errors.New("conversation: that id is taken")
	}
	c := &Conversation{ID: id, Owner: owner, WorkflowID: workflowID, RunID: runID, CreatedAt: s.now().UTC()}
	s.conversations[id] = c
	s.log[id] = nil
	return *c, nil
}

// Get returns a conversation the caller owns, or ErrNotFound.
func (s *Store) Get(id string, owner Owner) (Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.getLocked(id, owner)
	if err != nil {
		return Conversation{}, err
	}
	return *c, nil
}

func (s *Store) getLocked(id string, owner Owner) (*Conversation, error) {
	c, ok := s.conversations[id]
	if !ok || c.Owner != owner {
		return nil, ErrNotFound
	}
	return c, nil
}

// StartTurn opens a turn on a conversation and records its budget. Returns the turn's state.
func (s *Store) StartTurn(conversationID string, owner Owner, turnID, traceID string, intent Intent, b *Budget) (TurnState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.getLocked(conversationID, owner); err != nil {
		return TurnState{}, err
	}
	t := &turn{
		id: turnID, traceID: traceID, conversation: conversationID, owner: owner,
		intent: intent, phase: PhaseUnderstand, budget: b, completed: map[string]StepState{},
	}
	s.turns[turnID] = t
	return snapshot(t), nil
}

// AdvancePhase moves a turn forward. 🔴 It REFUSES to move backwards rather than silently accepting it:
// a phase that can rewind cannot answer "how far along is this?", which is the one question a spinner
// cannot answer and the phase vocabulary exists to answer.
func (s *Store) AdvancePhase(turnID string, p Phase) error {
	if !p.Valid() {
		return errors.New("conversation: that is not a phase")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.turns[turnID]
	if !ok {
		return ErrNotFound
	}
	if !p.After(t.phase) {
		return errors.New("conversation: a phase may not move backwards")
	}
	t.phase = p
	return nil
}

// RecordStep records how a plan step resolved.
func (s *Store) RecordStep(turnID, stepID string, state StepState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.turns[turnID]
	if !ok {
		return ErrNotFound
	}
	t.completed[stepID] = state
	return nil
}

// FinishTurn marks a turn terminal with its stop reason.
func (s *Store) FinishTurn(turnID string, stop harnessruntime.StopReason) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.turns[turnID]
	if !ok {
		return ErrNotFound
	}
	t.terminal, t.stop = true, stop
	return nil
}

// TurnStateByID returns a turn's state to its owner, or ErrNotFound.
//
// 🔴 Task 2.17's cross-tenant rule lives here: a turn belonging to somebody else is `not found`, with no
// signal that it exists. The ownership comparison is on the STORED owner, never on anything the request
// supplied beyond the session-derived pair.
func (s *Store) TurnStateByID(turnID string, owner Owner) (TurnState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.turns[turnID]
	if !ok || t.owner != owner {
		return TurnState{}, ErrNotFound
	}
	return snapshot(t), nil
}

// TurnStateByTrace resolves a turn by its trace id (FR23), for its owner only.
func (s *Store) TurnStateByTrace(traceID string, owner Owner) (TurnState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.turns {
		if t.traceID == traceID {
			if t.owner != owner {
				return TurnState{}, ErrNotFound
			}
			return snapshot(t), nil
		}
	}
	return TurnState{}, ErrNotFound
}

// snapshot copies a turn's state out. Called under the store's lock.
func snapshot(t *turn) TurnState {
	st := TurnState{
		TurnID: t.id, TraceID: t.traceID, Intent: t.intent, Phase: t.phase,
		Completed: map[string]StepState{}, Terminal: t.terminal, Stop: t.stop,
		LastMessageID: t.lastMessageID,
	}
	for k, v := range t.completed {
		st.Completed[k] = v
	}
	if t.budget != nil {
		st.Envelope = t.budget.Envelope()
		st.Remaining = t.budget.Remaining()
	}
	return st
}

// ── The message log and its subscriptions ────────────────────────────────────────────────────────

// Append assigns the next id, records the message, and hands it to every live subscriber.
//
// 🔴 The id assignment, the log write and the fan-out happen under ONE lock. If they did not, a
// subscriber registering between the append and the fan-out would either miss the message (a gap) or
// receive it twice (a duplicate) — the two failures FR6 names, and both of them intermittent.
func (s *Store) Append(m Message) (Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.conversations[m.ConversationID]; !ok {
		return Message{}, ErrNotFound
	}
	s.nextID[m.ConversationID]++
	m.ID = s.nextID[m.ConversationID]
	s.log[m.ConversationID] = append(s.log[m.ConversationID], m)
	if t, ok := s.turns[m.TurnID]; ok {
		t.lastMessageID = m.ID
	}
	for _, sub := range s.subs[m.ConversationID] {
		sub.push(m)
	}
	return m, nil
}

// Messages returns a conversation's log after the given id, for its owner. The replay half of resume.
func (s *Store) Messages(conversationID string, owner Owner, afterID int64) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.getLocked(conversationID, owner); err != nil {
		return nil, err
	}
	return after(s.log[conversationID], afterID), nil
}

// after returns the messages with an id strictly greater than afterID. The log is append-only and ids
// are monotonic, so a linear scan is correct without a sort.
func after(log []Message, afterID int64) []Message {
	out := make([]Message, 0, len(log))
	for _, m := range log {
		if m.ID > afterID {
			out = append(out, m)
		}
	}
	return out
}

// subscription is one live SSE consumer.
type subscription struct {
	mu     sync.Mutex
	queue  []Message
	notify chan struct{}
	closed bool
	lagged bool
}

// maxQueued bounds one subscriber's backlog. Reaching it closes the subscription rather than dropping,
// for ErrLagged's reason.
const maxQueued = 4096

func (sub *subscription) push(m Message) {
	sub.mu.Lock()
	if sub.closed {
		sub.mu.Unlock()
		return
	}
	if len(sub.queue) >= maxQueued {
		sub.lagged, sub.closed = true, true
		sub.mu.Unlock()
		select {
		case sub.notify <- struct{}{}:
		default:
		}
		return
	}
	sub.queue = append(sub.queue, m)
	sub.mu.Unlock()
	select {
	case sub.notify <- struct{}{}:
	default:
	}
}

// drain takes everything queued. Returns lagged=true when the subscription was closed for falling behind.
func (sub *subscription) drain() (out []Message, lagged bool) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	out, sub.queue = sub.queue, nil
	return out, sub.lagged
}

// Subscription is the caller's handle on a live stream.
type Subscription struct {
	// Backlog is everything already recorded after the acknowledged id, in order. Delivered by the
	// caller BEFORE it waits on Notify, which is what makes resume gapless.
	Backlog []Message
	// Notify fires when new messages are queued.
	Notify <-chan struct{}

	sub   *subscription
	store *Store
	conv  string
}

// Subscribe replays everything after afterID and then streams what follows, with no gap and no
// duplicate between the two halves.
//
// 🔴 The backlog snapshot and the registration happen under one lock — see Append. This is the whole
// mechanism behind the spec's "no message is delivered twice and none is skipped", and it is why the
// fence for it (task 6.6) kills a stream mid-run rather than testing the two halves separately.
func (s *Store) Subscribe(conversationID string, owner Owner, afterID int64) (*Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.getLocked(conversationID, owner); err != nil {
		return nil, err
	}
	sub := &subscription{notify: make(chan struct{}, 1)}
	s.subs[conversationID] = append(s.subs[conversationID], sub)
	return &Subscription{
		Backlog: after(s.log[conversationID], afterID),
		Notify:  sub.notify,
		sub:     sub,
		store:   s,
		conv:    conversationID,
	}, nil
}

// Next drains everything queued since the last call. `lagged` reports that the subscription was closed
// because the consumer could not keep up; the caller must end the stream so the client resumes.
func (s *Subscription) Next() (msgs []Message, lagged bool) { return s.sub.drain() }

// Close removes the subscription. Idempotent, so a handler's defer and an error path can both call it.
func (s *Subscription) Close() {
	s.sub.mu.Lock()
	s.sub.closed = true
	s.sub.mu.Unlock()

	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	live := s.store.subs[s.conv]
	for i, existing := range live {
		if existing == s.sub {
			s.store.subs[s.conv] = append(live[:i], live[i+1:]...)
			return
		}
	}
}
