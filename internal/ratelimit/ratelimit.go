// Package ratelimit is a per-key token bucket, held in memory.
//
// # 🔴 Why the counter must not depend on whether the account exists
//
// The endpoint this exists for answers identically for a real address and an unknown one — same body,
// same status, same timing — because "I forgot my password" is a request anybody can make about anybody,
// and a reply that differs answers "does this person have an account with you" for the whole internet.
//
// A rate limiter is the classic way that property gets quietly reopened. Count only the requests that
// found an account, or refuse only those, and 429-versus-200 becomes exactly the oracle the constant
// answer was protecting: the addresses that can be rate-limited are the addresses that exist. So the
// bucket is consumed for EVERY request, before anything is looked up, and the limiter is never told
// what the lookup found. It cannot leak what it does not know.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter allows a burst of requests per key, refilling one token at a time.
//
// # Why a token bucket and not a fixed window
//
// A fixed window ("three per hour, reset on the hour") lets six requests through across a window
// boundary, which for a mail-sending endpoint is six mails in two seconds — the exact burst the limit
// exists to prevent, arriving at a moment nobody chose. A bucket refills continuously, so the ceiling
// holds no matter when the requests arrive, and it yields an honest Retry-After without extra
// bookkeeping.
type Limiter struct {
	burst    int
	refill   time.Duration
	capacity int

	// now is injectable so tests can advance time rather than sleep through it. A limiter tested by
	// sleeping is a limiter tested at one speed, on a machine that was not busy.
	now func() time.Time

	mu   sync.Mutex
	keys map[string]*bucket
}

type bucket struct {
	// tokens is fractional so a partial refill is not silently rounded away — with integer tokens and a
	// twenty-minute refill, a request every nineteen minutes would never regain anything.
	tokens float64
	last   time.Time
}

// New builds a limiter.
//
// burst is how many requests a key may make back to back. refill is how long one token takes to come
// back, so the sustained rate is one per refill. capacity bounds the number of distinct keys held; see
// Allow for what happens at the ceiling.
func New(burst int, refill time.Duration, capacity int) *Limiter {
	if burst < 1 {
		panic("ratelimit: a burst below 1 would refuse everything")
	}
	if refill <= 0 {
		panic("ratelimit: a refill of zero would never restore a token")
	}
	return &Limiter{
		burst: burst, refill: refill, capacity: capacity,
		now: time.Now, keys: map[string]*bucket{},
	}
}

// WithClock replaces the clock. For tests.
func (l *Limiter) WithClock(now func() time.Time) *Limiter {
	l.now = now
	return l
}

// Allow consumes one token for a key, reporting whether the request may proceed and — when it may not —
// how long until it could.
//
// 🔴 The key is whatever the caller supplies, including a value an attacker chose. That is why capacity
// exists: an unbounded map keyed on request input is a memory-exhaustion vector reachable by anybody who
// can send an unauthenticated POST.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, seen := l.keys[key]
	if !seen {
		// A bucket that has fully refilled carries no information, so dropping it changes nothing. Sweeping
		// before admitting a new key keeps the map proportional to RECENT traffic rather than to all
		// traffic ever, which is what stops the ceiling being reached in normal operation.
		if len(l.keys) >= l.capacity {
			l.sweepFull(now)
		}
		if len(l.keys) >= l.capacity {
			// 🔴 REFUSE rather than evict. Evicting somebody's bucket to make room is a bypass wearing the
			// costume of a memory bound: an attacker floods `capacity` junk keys, the victim's bucket is
			// evicted, and the flood on that victim resumes from zero — so the measure meant to bound
			// memory would defeat the control it was protecting.
			//
			// The cost is real and is chosen deliberately: while a flood holds the map at its ceiling,
			// addresses that were not already tracked are refused. That is an availability failure under
			// attack, in exchange for the protection never being removable by attack. Sized so that
			// reaching it means a genuine flood rather than a busy afternoon.
			return false, l.refill
		}
		b = &bucket{tokens: float64(l.burst), last: now}
		l.keys[key] = b
	}

	// Refill for the time that has passed, then spend.
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += elapsed.Seconds() / l.refill.Seconds()
		if b.tokens > float64(l.burst) {
			b.tokens = float64(l.burst)
		}
		b.last = now
	}
	if b.tokens < 1 {
		// Time until the bucket reaches one whole token, rounded up — a Retry-After that is a fraction
		// short sends a polite client back to be refused again.
		need := 1 - b.tokens
		wait := time.Duration(need * float64(l.refill))
		return false, wait.Round(time.Second) + time.Second
	}
	b.tokens--
	return true, 0
}

// Restore gives one token back.
//
// # 🔴 Why a limiter needs this at all
//
// Login spends a token before it knows anything, then gives it back if the password was CORRECT. The
// difference that makes is the difference between a limit and a weapon: charged for every attempt, a
// per-account login limit lets anybody lock anybody out of their own account by making failed attempts
// with their address. Charged only for failures, an attacker holding the bucket empty can make the real
// owner retry — because the owner's correct attempt costs nothing the moment it lands — but cannot shut
// them out.
//
// It is spend-then-restore rather than check-then-charge-on-failure because the check and the spend must
// be one atomic step. Two concurrent callers that both merely LOOK at a bucket with one token left will
// both proceed, and a limit that leaks a little under concurrency leaks most under attack, which is the
// only time it matters.
//
// A key that has been swept is not recreated: it had refilled completely, so there is nothing owed to it.
func (l *Limiter) Restore(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.keys[key]
	if !ok {
		return
	}
	// 🔴 Clamped. Every Restore follows an Allow that spent one, so this cannot legitimately overflow —
	// but an unclamped counter is one mispaired call away from a bucket that grows without bound and a
	// key that is never limited again.
	if b.tokens++; b.tokens > float64(l.burst) {
		b.tokens = float64(l.burst)
	}
}

// Len reports how many keys are held. For tests and for a health signal.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.keys)
}

// sweepFull drops every key whose bucket has refilled completely.
//
// Such a key is indistinguishable from one never seen, so forgetting it cannot let anything through that
// a fresh key would not. Callers hold the mutex.
func (l *Limiter) sweepFull(now time.Time) {
	for key, b := range l.keys {
		refilled := b.tokens + now.Sub(b.last).Seconds()/l.refill.Seconds()
		if refilled >= float64(l.burst) {
			delete(l.keys, key)
		}
	}
}
