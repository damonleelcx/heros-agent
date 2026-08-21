package conversation

import (
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/harnessruntime"
)

func newTestStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	c := newClock()
	return NewStore(c.now), c
}

var (
	alice = Owner{TenantID: "tenant-a", UserID: "usr_alice"}
	bob   = Owner{TenantID: "tenant-a", UserID: "usr_bob"}
	mal   = Owner{TenantID: "tenant-b", UserID: "usr_mal"}
)

func seed(t *testing.T, s *Store, owner Owner) Conversation {
	t.Helper()
	c, err := s.Create("conv_1", owner, "wf_1", "run_1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return c
}

func msg(conv, turn string, kind Kind) Message {
	return Message{ConversationID: conv, TurnID: turn, Kind: kind, Provenance: ProvenanceGenerated}
}

// ── per-person scope, and the enumeration oracle it must not become ──────────────────────────────

func TestAConversationIsVisibleOnlyToThePersonWhoStartedIt(t *testing.T) {
	s, _ := newTestStore(t)
	seed(t, s, alice)

	if _, err := s.Get("conv_1", alice); err != nil {
		t.Fatalf("the owner could not read their own conversation: %v", err)
	}
	// A COLLEAGUE in the same tenant. Per-person scope (PRD §14 Q4) means this is refused — and the
	// work is still reachable: the tenant owns the run, and `/app/runs` is where a colleague finds it.
	if _, err := s.Get("conv_1", bob); !errors.Is(err, ErrNotFound) {
		t.Errorf("a colleague read another person's conversation: %v", err)
	}
	if _, err := s.Get("conv_1", mal); !errors.Is(err, ErrNotFound) {
		t.Errorf("another tenant read this conversation: %v", err)
	}
}

// TestNotYoursAndDoesNotExistAreIndistinguishable is the disclosure rule, asserted on the ERROR VALUE
// rather than on a message — because the two answers must be the same value, not merely similar prose.
func TestNotYoursAndDoesNotExistAreIndistinguishable(t *testing.T) {
	s, _ := newTestStore(t)
	seed(t, s, alice)

	notYours := func() error { _, err := s.Get("conv_1", mal); return err }()
	neverExisted := func() error { _, err := s.Get("conv_nonexistent", mal); return err }()
	if notYours != neverExisted {
		t.Fatalf("a caller can tell 'not yours' (%v) from 'does not exist' (%v).\n"+
			"That difference is an oracle: a caller can walk the id space and learn which "+
			"conversations another organization has.", notYours, neverExisted)
	}
}

func TestATraceFromAnotherTenantIsNotFound(t *testing.T) {
	s, c := newTestStore(t)
	seed(t, s, alice)
	b, _ := NewBudget(fullEnvelope(), c.at, c.now)
	if _, err := s.StartTurn("conv_1", alice, "turn_1", "trace_abc", IntentMemory, b); err != nil {
		t.Fatalf("start turn: %v", err)
	}
	if _, err := s.TurnStateByTrace("trace_abc", alice); err != nil {
		t.Fatalf("the owner could not resolve their own trace: %v", err)
	}
	// 🔴 Task 2.17. The trace EXISTS, and the answer must not say so.
	fromOtherTenant := func() error { _, err := s.TurnStateByTrace("trace_abc", mal); return err }()
	fromNowhere := func() error { _, err := s.TurnStateByTrace("trace_does_not_exist", mal); return err }()
	if fromOtherTenant != fromNowhere {
		t.Errorf("a cross-tenant trace lookup (%v) is distinguishable from a nonexistent one (%v)",
			fromOtherTenant, fromNowhere)
	}
}

// ── the acknowledgement cursor: no duplicate, no gap ─────────────────────────────────────────────

func TestResumeReplaysFromTheAcknowledgedIDWithNoDuplicateAndNoGap(t *testing.T) {
	s, _ := newTestStore(t)
	seed(t, s, alice)
	for i := 0; i < 5; i++ {
		if _, err := s.Append(msg("conv_1", "turn_1", KindProgress)); err != nil {
			t.Fatal(err)
		}
	}
	// The client processed up to 2 and died.
	replay, err := s.Messages("conv_1", alice, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{3, 4, 5}
	if len(replay) != len(want) {
		t.Fatalf("replay = %d messages, want %d", len(replay), len(want))
	}
	for i, m := range replay {
		if m.ID != want[i] {
			t.Errorf("replay[%d].ID = %d, want %d", i, m.ID, want[i])
		}
	}
}

// TestSubscribeIsGaplessAcrossTheBacklogLiveBoundary is the one that matters, and the one a naive
// implementation gets wrong intermittently: a message appended between "read the backlog" and
// "register the subscriber" is either lost or delivered twice.
func TestSubscribeIsGaplessAcrossTheBacklogLiveBoundary(t *testing.T) {
	s, _ := newTestStore(t)
	seed(t, s, alice)
	for i := 0; i < 3; i++ {
		if _, err := s.Append(msg("conv_1", "turn_1", KindProgress)); err != nil {
			t.Fatal(err)
		}
	}
	sub, err := s.Subscribe("conv_1", alice, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	for i := 0; i < 3; i++ {
		if _, err := s.Append(msg("conv_1", "turn_1", KindProgress)); err != nil {
			t.Fatal(err)
		}
	}

	seen := append([]Message(nil), sub.Backlog...)
	deadline := time.After(2 * time.Second)
	for len(seen) < 5 {
		select {
		case <-sub.Notify:
			more, lagged := sub.Next()
			if lagged {
				t.Fatal("the subscription lagged on six messages")
			}
			seen = append(seen, more...)
		case <-deadline:
			t.Fatalf("only %d of 5 messages arrived", len(seen))
		}
	}
	want := []int64{2, 3, 4, 5, 6}
	if len(seen) != len(want) {
		t.Fatalf("delivered %d messages, want %d: %v", len(seen), len(want), ids(seen))
	}
	for i, m := range seen {
		if m.ID != want[i] {
			t.Fatalf("delivery = %v, want %v.\nA duplicate or a gap here is intermittent in "+
				"production and invisible in a screenshot.", ids(seen), want)
		}
	}
}

func ids(ms []Message) []int64 {
	out := make([]int64, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func TestMessageIDsAreMonotonicAndAssignedByTheStore(t *testing.T) {
	s, _ := newTestStore(t)
	seed(t, s, alice)
	// A caller trying to choose its own id must not win: two turns emitting concurrently on one
	// conversation would both mint the same number, and a repeated cursor is a gap or a duplicate
	// depending on which way the client rounds.
	m := msg("conv_1", "turn_1", KindProgress)
	m.ID = 999
	got, err := s.Append(m)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 1 {
		t.Errorf("ID = %d; the store assigns it, not the caller", got.ID)
	}
}

// ── resume reads the RUN, never the client (FR21, task 2.18) ─────────────────────────────────────

func TestResumeReadsPhaseBudgetAndStepsFromTheRun(t *testing.T) {
	s, c := newTestStore(t)
	seed(t, s, alice)
	b, _ := NewBudget(fullEnvelope(), c.at, c.now)
	if _, err := s.StartTurn("conv_1", alice, "turn_1", "trace_abc", IntentMemory, b); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePhase("turn_1", PhaseAct); err != nil {
		t.Fatal(err)
	}
	if err := b.Admit("s1", StepCost{Tokens: 2500, ToolCalls: 1, Turns: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordStep("turn_1", "s1", StepDone); err != nil {
		t.Fatal(err)
	}

	st, err := s.TurnStateByID("turn_1", alice)
	if err != nil {
		t.Fatal(err)
	}
	if st.Phase != PhaseAct {
		t.Errorf("phase = %q, want %q", st.Phase, PhaseAct)
	}
	if st.Remaining.Tokens != 10000-2500 {
		t.Errorf("remaining tokens = %d, want %d", st.Remaining.Tokens, 7500)
	}
	if st.Completed["s1"] != StepDone {
		t.Errorf("completed[s1] = %q, want done", st.Completed["s1"])
	}
	if st.Envelope != fullEnvelope() {
		t.Errorf("envelope = %+v, want %+v", st.Envelope, fullEnvelope())
	}

	// 🔴 The snapshot is a COPY. A caller mutating it must not reach the run — otherwise "the run holds
	// the state" is true only until somebody writes through the returned map.
	st.Completed["s1"] = StepRefused
	again, _ := s.TurnStateByID("turn_1", alice)
	if again.Completed["s1"] != StepDone {
		t.Error("a caller mutated the run's state through a returned snapshot")
	}
}

func TestAPhaseMayNotMoveBackwards(t *testing.T) {
	s, c := newTestStore(t)
	seed(t, s, alice)
	b, _ := NewBudget(fullEnvelope(), c.at, c.now)
	if _, err := s.StartTurn("conv_1", alice, "turn_1", "trace_abc", IntentMemory, b); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePhase("turn_1", PhaseRespond); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePhase("turn_1", PhaseAct); err == nil {
		t.Fatal("a phase moved backwards; a turn that can rewind cannot answer 'how far along is this?'")
	}
}

func TestFinishTurnRecordsTheStopReason(t *testing.T) {
	s, c := newTestStore(t)
	seed(t, s, alice)
	b, _ := NewBudget(fullEnvelope(), c.at, c.now)
	if _, err := s.StartTurn("conv_1", alice, "turn_1", "trace_abc", IntentMemory, b); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishTurn("turn_1", harnessruntime.StopTokenBudget); err != nil {
		t.Fatal(err)
	}
	st, _ := s.TurnStateByID("turn_1", alice)
	if !st.Terminal || st.Stop != harnessruntime.StopTokenBudget {
		t.Errorf("terminal=%v stop=%q; want a terminal turn naming the token budget", st.Terminal, st.Stop)
	}
}
