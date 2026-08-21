package api

import (
	"sync"
	"time"
)

// conversationstreams.go is P31 §5's observability half: how many conversation streams are open, how
// long the oldest has been open, and a ceiling that refuses rather than exhausts (tasks 5.3, 5.4).
//
// # 🔴 Why a long-lived stream needs a ceiling AND a gauge, and why the two are different jobs
//
// An SSE connection is held for the life of a turn — minutes, not milliseconds. That changes the
// process's connection profile completely: a surface that previously used a socket for 40ms now holds
// one for four minutes, and a hundred people asking a question at once is a hundred simultaneous
// sockets, each with a goroutine and a subscription behind it.
//
// Two things follow, and only one of them is a limit:
//
//   - **The ceiling** exists so exhaustion is a REFUSAL rather than a collapse. Without one, the failure
//     mode is file-descriptor exhaustion, which does not fail the streams — it fails whatever asks for a
//     socket NEXT. That is usually the orchestrator's readiness probe, so the box is marked unhealthy
//     for a reason that has nothing to do with the box being unhealthy, and the streams that caused it
//     keep working. A refusal at a stated number is a fact an operator can act on; a descriptor limit is
//     an outage with a misleading label.
//
//   - **The gauge** exists because a ceiling nobody can see is a ceiling nobody knows they are near.
//     Task 5.4 is explicit that these numbers go on a readable HEALTH ENDPOINT and not only into logs:
//     "how many streams are open right now" is a question asked ABOUT the box that is misbehaving, NOW,
//     and a log line that scrolled past three restarts ago cannot answer it.
//
// # 🔴 Task 5.3 · readiness is NOT behind this
//
// `/readyz` acquires no slot and takes no lock this holds during a stream. It READS the gauge — a mutex
// held for the length of a map copy — and answers. A readiness endpoint that had to wait on the same
// exhaustible resource as the streams would be measuring its own starvation, which is the failure this
// note exists to prevent and which `TestReadinessAnswersWhileEveryStreamSlotIsTaken` asserts.

// maxConcurrentStreams bounds simultaneously open conversation streams on one process.
//
// # Why this number
//
// It is deliberately far below any descriptor limit and far above any plausible interactive load: a
// single process serving one organization's people will not approach it, and a process that does has
// something wrong with it that a refusal will surface immediately. A CONSTANT rather than configuration,
// for the reason every other ceiling in this phase is one — a limit an operator can raise is a limit
// that gets raised at 2am by whoever is being paged, after which the next failure is the descriptor
// limit and it does not name itself.
const maxConcurrentStreams = 256

// streamGauge counts open streams and how long they have been open.
type streamGauge struct {
	mu sync.Mutex
	// open maps a monotonic ticket to when that stream started.
	open map[uint64]time.Time
	next uint64
	// peak is the high-water mark since boot. Kept because "we are at 12 now" and "we hit 250 twice
	// last night" are different facts, and only the second one predicts the refusal.
	peak int
	// refused counts streams turned away at the ceiling. 🔴 A refusal that is not counted is a capacity
	// problem nobody can see; the gauge is where a rising number says "raise the ceiling or shed load"
	// before anybody notices a symptom.
	refused int
	now     func() time.Time
}

func newStreamGauge(now func() time.Time) *streamGauge {
	if now == nil {
		now = time.Now
	}
	return &streamGauge{open: map[uint64]time.Time{}, now: now}
}

// acquire takes a slot, returning the ticket to release and whether a slot was available.
func (g *streamGauge) acquire() (uint64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.open) >= maxConcurrentStreams {
		g.refused++
		return 0, false
	}
	g.next++
	ticket := g.next
	g.open[ticket] = g.now()
	if len(g.open) > g.peak {
		g.peak = len(g.open)
	}
	return ticket, true
}

// release returns a slot. Idempotent: a handler's defer and an early error path may both call it.
func (g *streamGauge) release(ticket uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.open, ticket)
}

// StreamHealth is what `/readyz` reports about the long-lived connections (task 5.4).
//
// 🔴 Counts and durations only. No conversation id, no tenant, no person — `/readyz` is public by
// necessity (a probe behind authentication cannot be probed by the thing that most needs to probe it),
// so everything it says is said to everybody.
type StreamHealth struct {
	// Open is how many conversation streams are held right now.
	Open int `json:"open"`
	// Peak is the high-water mark since this process started.
	Peak int `json:"peak"`
	// Ceiling is the refusal point. Reported so "open: 240" means something without reading the source.
	Ceiling int `json:"ceiling"`
	// Refused is how many streams have been turned away at the ceiling since boot.
	Refused int `json:"refused"`
	// LongestSeconds is how long the OLDEST open stream has been held.
	//
	// The oldest rather than the mean, because the question this answers is "is something stuck?" and a
	// mean over a hundred healthy streams hides one that has been open for six hours. A single long
	// stream is the signal; the average is the thing that erases it.
	LongestSeconds int `json:"longest_seconds"`
}

// Health snapshots the gauge. Takes the lock for the length of a small loop and never blocks on I/O —
// see the task 5.3 note at the top of this file.
func (g *streamGauge) Health() StreamHealth {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := StreamHealth{Open: len(g.open), Peak: g.peak, Ceiling: maxConcurrentStreams, Refused: g.refused}
	now := g.now()
	for _, started := range g.open {
		if seconds := int(now.Sub(started) / time.Second); seconds > out.LongestSeconds {
			out.LongestSeconds = seconds
		}
	}
	return out
}
