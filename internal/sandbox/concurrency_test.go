package sandbox

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// P34 task 4.4 / QA fence 9.12 (second half) — the sandbox's own concurrency limit.
//
// 🔴 Every test here drives the REAL Sandbox.Run path rather than the gate directly, because the claim
// under test is "the sandbox enforces it", and a gate that is correct but never taken enforces nothing.

// blockingEnforcer creates isolates whose Exec parks until released, so a test can hold N isolates open
// and observe how many the sandbox admitted.
type blockingEnforcer struct {
	started chan struct{}
	release chan struct{}
	// peak is the greatest number of isolates ever executing at once, sampled inside Exec.
	live, peak int64
}

func (e *blockingEnforcer) Capabilities() Capabilities {
	return Capabilities{ScrubEnv: true, ResourceLimits: true, NetworkDeny: true, FilesystemScope: true}
}

func (e *blockingEnforcer) Create(_ context.Context, _ Spec, _ *warmPool) (Isolate, error) {
	return &blockingIsolate{e: e}, nil
}

type blockingIsolate struct{ e *blockingEnforcer }

func (i *blockingIsolate) Exec(_ context.Context, _ Tool, _ func(Denial)) (*Result, error) {
	n := atomic.AddInt64(&i.e.live, 1)
	for {
		p := atomic.LoadInt64(&i.e.peak)
		if n <= p || atomic.CompareAndSwapInt64(&i.e.peak, p, n) {
			break
		}
	}
	i.e.started <- struct{}{}
	<-i.e.release
	atomic.AddInt64(&i.e.live, -1)
	return &Result{ExitCode: 0}, nil
}

func (i *blockingIsolate) Destroy() {}

func runGroup(t *testing.T, s *Sandbox, e *blockingEnforcer, n, declaredLimit int) *sync.WaitGroup {
	t.Helper()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Run(context.Background(), Spec{
				NodeID:           "n",
				RunID:            "run-1",
				Concurrency:      ConcurrencyKey{RunID: "run-1", Group: "g"},
				ConcurrencyLimit: declaredLimit,
			}, Tool{Argv: []string{"true"}})
		}()
	}
	return &wg
}

// TestTheSandboxCapsAGroupAtItsDeclaredWidth — the ordinary case. Six nodes declared with a limit of
// two must never have three isolates alive at once.
func TestTheSandboxCapsAGroupAtItsDeclaredWidth(t *testing.T) {
	e := &blockingEnforcer{started: make(chan struct{}, 16), release: make(chan struct{})}
	s := New(e)
	wg := runGroup(t, s, e, 6, 2)

	// 🔴 Wait for the limit's worth of starts BEFORE releasing anything. Two isolates are then PROVABLY
	// alive at once, which is what makes the upper-bound assertion below mean something — a gate that
	// serialised everything would satisfy "peak <= 2" trivially and this test would be blind to it.
	//
	// 🚫 Not "release one at a time and check the peak at the end". That version passed and failed on the
	// same code depending on how fast the next goroutine happened to be scheduled: a stopwatch, not a
	// fence. It failed 1-in-5 on this machine before it was rewritten.
	for i := 0; i < 2; i++ {
		select {
		case <-e.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of 2 isolates started under a declared limit of 2; the gate is serialising "+
				"a group that was declared concurrent", i)
		}
	}
	if got := atomic.LoadInt64(&e.peak); got < 2 {
		t.Fatalf("peak was %d while two isolates were provably alive at once", got)
	}

	// Now drain. Each release lets exactly one more in, so the remaining four arrive one per release.
	for i := 0; i < 6; i++ {
		e.release <- struct{}{}
		if i < 4 {
			select {
			case <-e.started:
			case <-time.After(5 * time.Second):
				t.Fatalf("the gate did not hand a freed slot on after release %d", i)
			}
		}
	}
	wg.Wait()

	// The upper bound, measured inside Exec across the whole run rather than sampled.
	if got := atomic.LoadInt64(&e.peak); got > 2 {
		t.Fatalf("%d isolates ran concurrently under a declared limit of 2. Concurrency multiplies a "+
			"run's PEAK resource use by the group's width, so the width is a blast-radius bound rather "+
			"than a scheduling hint", got)
	}
}

// TestTheSandboxCapsEvenWhenTheSpecAsksForMoreThanTheCeiling is the second half of QA fence 9.12, and
// it is the one that matters.
//
// 🔴 A spec declaring a width of 100 has NOT been through the resolve-time gate — that gate would have
// refused it against the envelope's limit. This is the path that stays standing when the first one is
// bypassed: a Resolved assembled by hand, a spec resolved under an older binary, any caller that built a
// Spec directly. A limit with one entrance is a limit with a way around it.
func TestTheSandboxCapsEvenWhenTheSpecAsksForMoreThanTheCeiling(t *testing.T) {
	e := &blockingEnforcer{started: make(chan struct{}, 64), release: make(chan struct{})}
	s := New(e)
	const n = SandboxConcurrencyCeiling + 6
	wg := runGroup(t, s, e, n, 100)

	// The ceiling's worth of starts, before any release — so the cap is measured against isolates that
	// are provably alive together rather than against a schedule.
	for i := 0; i < SandboxConcurrencyCeiling; i++ {
		select {
		case <-e.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d isolates started; the gate is narrower than its own ceiling",
				i, SandboxConcurrencyCeiling)
		}
	}
	for i := 0; i < n; i++ {
		e.release <- struct{}{}
		if i < n-SandboxConcurrencyCeiling {
			select {
			case <-e.started:
			case <-time.After(5 * time.Second):
				t.Fatalf("the gate did not hand a freed slot on after release %d", i)
			}
		}
	}
	wg.Wait()

	if got := atomic.LoadInt64(&e.peak); got > SandboxConcurrencyCeiling {
		t.Fatalf("%d isolates ran concurrently against a sandbox ceiling of %d. The sandbox trusted the "+
			"number the spec handed it, which means the ONLY limit on group width is a gate that every "+
			"non-resolving caller skips", got, SandboxConcurrencyCeiling)
	}

	// And the narrowing is PUBLISHED, because "the early gate is not running" is invisible in every
	// aggregate: nothing errors, nothing retries, the work simply runs narrower than it asked to.
	h := s.Concurrency()
	if h.Capped == 0 {
		t.Error("the health gauge reports no capped admissions after a spec asked for 100 against a " +
			"ceiling of 8; an operator has no way to learn the resolve-time gate was bypassed")
	}
	if h.Ceiling != SandboxConcurrencyCeiling {
		t.Errorf("health reports ceiling %d, want %d — a gauge whose ceiling is not published makes "+
			"\"active: 6\" meaningless without reading the source", h.Ceiling, SandboxConcurrencyCeiling)
	}
}

// TestPeakIsRecordedBecauseTheMomentThatMattersHasPassed — task 8.3. A current gauge cannot answer
// "how loaded did this box get", because by the time anybody looks the answer is gone.
func TestPeakIsRecordedBecauseTheMomentThatMattersHasPassed(t *testing.T) {
	e := &blockingEnforcer{started: make(chan struct{}, 16), release: make(chan struct{})}
	s := New(e)
	wg := runGroup(t, s, e, 4, 3)
	for i := 0; i < 3; i++ {
		select {
		case <-e.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of 3 isolates started under a declared limit of 3", i)
		}
	}
	for i := 0; i < 4; i++ {
		e.release <- struct{}{}
		if i == 0 {
			<-e.started // the fourth, admitted when the first slot frees
		}
	}
	wg.Wait()

	h := s.Concurrency()
	if h.Active != 0 {
		t.Errorf("active is %d after every isolate finished; slots are leaking, and the gate would "+
			"eventually admit nothing", h.Active)
	}
	if h.Peak == 0 || h.PeakGroupWidth == 0 {
		t.Fatalf("peak is %d and peak group width is %d after four isolates ran; a health surface that "+
			"only reports the CURRENT count cannot answer the question it exists for",
			h.Peak, h.PeakGroupWidth)
	}
	if h.PeakGroupWidth > 3 {
		t.Errorf("peak group width is %d under a declared limit of 3", h.PeakGroupWidth)
	}
}

// TestAnUngroupedIsolateTakesNoSlot keeps the gate from becoming a global throttle. A node that is not
// declared concurrent with anything is not competing with anything, and making it queue would turn a
// blast-radius bound into a throughput knob that behaves differently depending on who else is running.
func TestAnUngroupedIsolateTakesNoSlot(t *testing.T) {
	e := &blockingEnforcer{started: make(chan struct{}, 64), release: make(chan struct{})}
	s := New(e)
	const n = SandboxConcurrencyCeiling + 4
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Run(context.Background(), Spec{NodeID: "n", RunID: "run-1"}, Tool{Argv: []string{"true"}})
		}()
	}
	for i := 0; i < n; i++ {
		select {
		case <-e.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d ungrouped isolates started; the gate is throttling isolates that "+
				"declared no group", i, n)
		}
	}
	for i := 0; i < n; i++ {
		e.release <- struct{}{}
	}
	wg.Wait()
}

// TestTwoGroupsDoNotContend — the gate is keyed by (run, group) rather than being global. A global
// semaphore would make one tenant's wide group slow every other tenant's narrow ones.
func TestTwoGroupsDoNotContend(t *testing.T) {
	e := &blockingEnforcer{started: make(chan struct{}, 16), release: make(chan struct{})}
	s := New(e)
	var wg sync.WaitGroup
	for _, g := range []string{"a", "b"} {
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(group string) {
				defer wg.Done()
				_, _ = s.Run(context.Background(), Spec{
					NodeID: "n", RunID: "run-1",
					Concurrency:      ConcurrencyKey{RunID: "run-1", Group: group},
					ConcurrencyLimit: 1,
				}, Tool{Argv: []string{"true"}})
			}(g)
		}
	}
	// 🔴 Wait for TWO starts BEFORE releasing anything. That is the whole assertion: with a limit of one
	// per group, two simultaneous starts are only possible if the two groups have separate slots. A
	// global gate at one would admit the first and park the second forever, and this would time out.
	//
	// 🚫 Not "run them all and check the peak" — releasing one at a time makes the peak depend on how
	// fast the next goroutine is scheduled, which is a stopwatch, not a fence.
	for i := 0; i < 2; i++ {
		select {
		case <-e.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of 2 groups started concurrently; two groups at a limit of one each are "+
				"contending, which means the gate is global rather than per-group — one tenant's wide "+
				"group would then slow every other tenant's narrow ones", i)
		}
	}
	if got := atomic.LoadInt64(&e.peak); got < 2 {
		t.Fatalf("peak was %d while two isolates were provably alive at once", got)
	}
	for i := 0; i < 4; i++ {
		e.release <- struct{}{}
		if i < 2 {
			<-e.started // the second isolate of each group
		}
	}
	wg.Wait()
}

// TestACancelledWaiterDoesNotHangForever — the gate BLOCKS rather than refusing, so the context is the
// only thing that bounds the wait. sync.Cond has no context form; a waiter that nobody wakes is a
// goroutine leak that presents as a stuck run.
func TestACancelledWaiterDoesNotHangForever(t *testing.T) {
	e := &blockingEnforcer{started: make(chan struct{}, 4), release: make(chan struct{})}
	s := New(e)

	// Fill the single slot and hold it.
	go func() {
		_, _ = s.Run(context.Background(), Spec{
			NodeID: "held", RunID: "run-1",
			Concurrency:      ConcurrencyKey{RunID: "run-1", Group: "g"},
			ConcurrencyLimit: 1,
		}, Tool{Argv: []string{"true"}})
	}()
	<-e.started

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(ctx, Spec{
			NodeID: "waiting", RunID: "run-1",
			Concurrency:      ConcurrencyKey{RunID: "run-1", Group: "g"},
			ConcurrencyLimit: 1,
		}, Tool{Argv: []string{"true"}})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a waiter whose context expired was admitted anyway")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a waiter whose context expired never returned; sync.Cond has no context form, so a " +
			"waiter nobody wakes is a goroutine leak that presents as a stuck run")
	}
	e.release <- struct{}{}
}
