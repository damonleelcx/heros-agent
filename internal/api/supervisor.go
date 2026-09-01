// Package api serves the conversational console: routing, subject intake, and live run streams.
package api

import (
	"context"
	"sync"
	"time"

	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/store"
	"github.com/heros-foreal/heros/internal/task"
	"github.com/heros-foreal/heros/internal/worker"
)

// Event is one thing that happened to a goal, as the console needs to render it.
type Event struct {
	Type   string `json:"type"` // task | goal | spend
	TaskID string `json:"task_id,omitempty"`
	State  string `json:"state,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Result is the task's output, sent only on success so a finding can render without a second fetch.
	Result string `json:"result,omitempty"`

	Tokens         int64 `json:"tokens"`
	CostMicroCents int64 `json:"cost_micro_cents"`
	Done           int   `json:"done"`
	Total          int   `json:"total"`
	// Terminal marks the last event on a stream, so the browser closes rather than waiting forever.
	Terminal bool `json:"terminal,omitempty"`
}

// Supervisor drives goals and publishes what happens.
//
// # 🔴 Why the driving loop lives here rather than in worker
//
// `worker.RunOnce` is deliberately one bounded cycle with no loop of its own — that is what makes a
// crash indistinguishable from "nobody called it again", and what lets a test drive it step by step.
// Something still has to call it repeatedly, and that something needs to know about subscribers, which
// worker must not. So the loop is here, and worker stays ignorant of anyone watching.
type Supervisor struct {
	Store  store.Store
	Worker *worker.Worker
	// Interval is the pause between cycles when a goal reports there is nothing to do right now. Without
	// it a goal whose only ready task is held by another worker spins at full speed.
	Interval time.Duration

	mu   sync.Mutex
	subs map[goal.ID][]chan Event
	// history replays what a late subscriber missed. A browser that connects after the first two axes
	// finished would otherwise render a run that appears to start in the middle.
	history map[goal.ID][]Event
	running map[goal.ID]bool
}

// NewSupervisor builds one.
func NewSupervisor(s store.Store, w *worker.Worker) *Supervisor {
	return &Supervisor{
		Store: s, Worker: w, Interval: 150 * time.Millisecond,
		subs: map[goal.ID][]chan Event{}, history: map[goal.ID][]Event{}, running: map[goal.ID]bool{},
	}
}

// Subscribe returns a channel of events, replaying everything already published for this goal.
func (s *Supervisor) Subscribe(id goal.ID) <-chan Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Buffered generously: a slow browser must not block the run. If it fills, events are dropped for
	// that subscriber alone rather than stalling the goal — see publish.
	ch := make(chan Event, 256)
	for _, e := range s.history[id] {
		ch <- e
	}
	s.subs[id] = append(s.subs[id], ch)
	return ch
}

// Unsubscribe releases a subscriber.
func (s *Supervisor) Unsubscribe(id goal.ID, ch <-chan Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs := s.subs[id]
	for i, c := range subs {
		if c == ch {
			s.subs[id] = append(subs[:i], subs[i+1:]...)
			close(c)
			return
		}
	}
}

// publish records an event and fans it out.
//
// 🔴 A full subscriber channel is SKIPPED, never waited on. A browser that stopped reading must not be
// able to stall a durable run — the run is the product, the view of it is not.
func (s *Supervisor) publish(id goal.ID, e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history[id] = append(s.history[id], e)
	for _, ch := range s.subs[id] {
		select {
		case ch <- e:
		default:
		}
	}
}

// Start drives a goal to completion in the background, at most once per goal.
func (s *Supervisor) Start(ctx context.Context, id goal.ID) {
	s.mu.Lock()
	if s.running[id] {
		s.mu.Unlock()
		return
	}
	s.running[id] = true
	s.mu.Unlock()

	go s.drive(ctx, id)
}

func (s *Supervisor) drive(ctx context.Context, id goal.ID) {
	defer func() {
		s.mu.Lock()
		s.running[id] = false
		s.mu.Unlock()
	}()

	for {
		if ctx.Err() != nil {
			return
		}
		out, err := s.Worker.RunOnce(ctx, id)
		if err != nil {
			s.publish(id, Event{Type: "goal", State: "error", Detail: err.Error(), Terminal: true})
			return
		}
		s.emit(id, out)
		if !out.More {
			return
		}
		if out.Did == worker.DidNothing {
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.Interval):
			}
		}
	}
}

// emit converts one cycle's outcome into the events the console renders.
func (s *Supervisor) emit(id goal.ID, out worker.Outcome) {
	g, err := s.Store.LoadGoal(id)
	if err != nil {
		return
	}
	e := Event{
		Type: "task", TaskID: string(out.TaskID), Detail: out.Detail,
		Tokens: g.Spend.Tokens, CostMicroCents: g.Spend.CostMicroCents,
	}
	if d, derr := s.Store.LoadDAG(id); derr == nil {
		e.Done, e.Total = d.Progress()
		if out.TaskID != "" {
			if t := d.Tasks[out.TaskID]; t != nil {
				e.State = string(t.State)
				if t.State == task.Succeeded {
					e.Result = string(t.Result)
				}
			}
		}
	}
	switch out.Did {
	case worker.DidNothing:
		return // not worth an event; it says only that another worker holds the task
	case worker.DidReplan:
		e.Type, e.State = "goal", "replan"
	case worker.DidComplete, worker.DidStall, worker.DidStop, worker.DidBlockedOnApproval:
		e.Type, e.State, e.Terminal = "goal", string(out.Did), true
	}
	s.publish(id, e)
}
