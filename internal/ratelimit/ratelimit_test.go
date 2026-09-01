package ratelimit

import (
	"sync"
	"testing"
	"time"
)

// clock is a hand-advanced time source.
//
// 🔴 Tests advance it rather than sleeping. A limiter tested by sleeping is a limiter tested at one
// speed, on a machine that happened not to be busy — and the tests that hurt are the ones about a
// twenty-minute refill, which nobody would ever wait for and everybody would therefore shorten until the
// test no longer resembled the thing shipped.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// TestABurstIsAllowedAndTheNextRequestIsNot.
func TestABurstIsAllowedAndTheNextRequestIsNot(t *testing.T) {
	c := newClock()
	l := New(3, 20*time.Minute, 100).WithClock(c.now)

	for i := range 3 {
		if ok, _ := l.Allow("someone@example.test"); !ok {
			t.Fatalf("request %d of the burst was refused", i+1)
		}
	}
	ok, wait := l.Allow("someone@example.test")
	if ok {
		t.Fatal("a fourth request was allowed; the burst is not a ceiling")
	}
	if wait <= 0 || wait > 21*time.Minute {
		t.Errorf("Retry-After is %s, which is not the time until a token returns", wait)
	}
}

// TestATokenComesBackAfterTheRefill.
//
// And crucially, only ONE does — a bucket that refilled all the way on the first tick would make the
// burst a per-request allowance rather than a ceiling.
func TestATokenComesBackAfterTheRefill(t *testing.T) {
	c := newClock()
	l := New(3, 20*time.Minute, 100).WithClock(c.now)
	for range 3 {
		l.Allow("k")
	}

	c.advance(19 * time.Minute)
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("a token came back before the refill elapsed")
	}
	c.advance(2 * time.Minute) // 21 total: one whole token
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("no token came back after the refill elapsed")
	}
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("two tokens came back for one refill period")
	}
}

// TestPartialRefillsAccumulate.
//
// 🔴 With integer tokens, a caller arriving every nineteen minutes would regain nothing, ever: each
// refill rounds to zero and the bucket stays empty forever. Fractional tokens are why the sustained rate
// is actually one per refill rather than "one per refill, unless you ask slightly too early, in which
// case never".
func TestPartialRefillsAccumulate(t *testing.T) {
	c := newClock()
	l := New(3, 20*time.Minute, 100).WithClock(c.now)
	for range 3 {
		l.Allow("k")
	}
	// Three steps of five minutes: 0.75 of a token, refused every time. Each call is what makes the
	// fraction have to survive — the refill is computed from `last`, which every call moves forward.
	for i := range 3 {
		c.advance(5 * time.Minute)
		if ok, _ := l.Allow("k"); ok {
			t.Fatalf("allowed after only %d minutes", (i+1)*5)
		}
	}
	// The fourth step completes the twenty. With fractions kept this is one whole token; with each
	// quarter truncated to zero it is still nothing, and always would be.
	c.advance(5 * time.Minute)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("four partial refills added up to nothing; the fractions are being discarded, so a " +
			"caller arriving slightly too early would never regain a token at all")
	}
}

// TestKeysAreIndependent.
//
// One address being flooded must not stop anybody else resetting their password — otherwise the limiter
// hands an attacker a way to lock the whole deployment out of its own recovery path.
func TestKeysAreIndependent(t *testing.T) {
	c := newClock()
	l := New(3, 20*time.Minute, 100).WithClock(c.now)
	for range 5 {
		l.Allow("victim@example.test")
	}
	if ok, _ := l.Allow("bystander@example.test"); !ok {
		t.Fatal("flooding one address refused a different one")
	}
}

// TestFullBucketsAreForgotten.
//
// A bucket that has refilled completely is indistinguishable from a key never seen, so keeping it is
// pure memory. This is what stops the map growing with all traffic ever and reaching the ceiling in
// ordinary operation.
func TestFullBucketsAreForgotten(t *testing.T) {
	c := newClock()
	l := New(2, time.Minute, 3).WithClock(c.now)
	l.Allow("a")
	l.Allow("b")
	l.Allow("c")
	if l.Len() != 3 {
		t.Fatalf("expected 3 keys, have %d", l.Len())
	}
	c.advance(10 * time.Minute) // everything refills
	if ok, _ := l.Allow("d"); !ok {
		t.Fatal("a new key was refused although every existing bucket had refilled and could be dropped")
	}
	if l.Len() > 2 {
		t.Errorf("stale full buckets were kept: %d keys remain", l.Len())
	}
}

// TestTheCeilingRefusesRatherThanEvicting.
//
// # 🔴 The bypass this is guarding
//
// The map is keyed on a value the caller supplies, so it must be bounded. The tempting bound is
// least-recently-used eviction — and it is a bypass wearing the costume of a memory limit: an attacker
// floods `capacity` invented addresses, the victim's bucket is evicted to make room, and the flood on
// that victim resumes from a full allowance. The measure meant to bound memory would defeat the control
// it was protecting.
//
// Refusing instead means that while a flood holds the map at its ceiling, addresses not already tracked
// are turned away. That is an availability failure under attack, chosen deliberately over a protection
// that an attacker can remove by attacking.
func TestTheCeilingRefusesRatherThanEvicting(t *testing.T) {
	c := newClock()
	l := New(3, 20*time.Minute, 4).WithClock(c.now)

	// The victim spends their allowance.
	for range 3 {
		l.Allow("victim@example.test")
	}
	if ok, _ := l.Allow("victim@example.test"); ok {
		t.Fatal("the victim is not actually limited; the premise of this test is wrong")
	}
	// The attacker fills the rest of the map with junk.
	for _, k := range []string{"junk1", "junk2", "junk3", "junk4", "junk5"} {
		l.Allow(k)
	}
	// The victim must STILL be limited.
	if ok, _ := l.Allow("victim@example.test"); ok {
		t.Fatal("flooding the limiter with distinct keys cleared the victim's bucket — the memory bound " +
			"is a bypass of the limit it was meant to make safe")
	}
	if l.Len() > 4 {
		t.Errorf("the ceiling is not holding: %d keys for a capacity of 4", l.Len())
	}
}

// TestConcurrentCallersCannotExceedTheBurst.
//
// Run under -race. A limiter whose check and decrement are not atomic lets N goroutines all read "one
// token left" and all proceed — which for a mail-sending endpoint is the flood arriving in parallel
// instead of in series.
func TestConcurrentCallersCannotExceedTheBurst(t *testing.T) {
	l := New(5, time.Hour, 100)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow("k"); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 5 {
		t.Fatalf("%d of 200 concurrent requests were allowed; the burst is 5", allowed)
	}
}
