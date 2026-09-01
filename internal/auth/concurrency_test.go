package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heros-foreal/heros/internal/tenancy"
)

// concurrency_test.go covers the ceiling on how much argon2id runs at once.

// withGate installs a ceiling and a wait for one test, restoring both afterwards.
//
// 🔴 Restored with t.Cleanup rather than at the end of the test body. These are package-level, so a test
// that returns early — through t.Fatal, or a panic — would leave every later test in the package running
// against a one-slot gate with a millisecond of patience, and the failures would appear somewhere else
// entirely.
func withGate(t *testing.T, slots int, wait time.Duration) {
	t.Helper()
	oldWait := maxWait
	oldGate := current.Load()
	t.Cleanup(func() {
		maxWait = oldWait
		current.Store(oldGate)
	})
	SetConcurrency(slots)
	SetMaxWait(wait)
}

// TestNoMoreThanTheCeilingRunAtOnce.
//
// # 🔴 What is actually being bounded
//
// Memory. Each argon2id call asks for 64 MiB and holds it for tens of milliseconds, so the number that
// can run at once IS the peak footprint of this package: two hundred simultaneous sign-ins would ask for
// thirteen gigabytes and the kernel would end the process — taking with it every unrelated request in
// flight, the console, and the worker.
func TestNoMoreThanTheCeilingRunAtOnce(t *testing.T) {
	const slots = 3
	withGate(t, slots, 5*time.Second)

	var mu sync.Mutex
	inside, peak := 0, 0
	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := acquire(context.Background())
			if err != nil {
				return
			}
			mu.Lock()
			inside++
			if inside > peak {
				peak = inside
			}
			mu.Unlock()

			time.Sleep(time.Millisecond) // stand in for the hash

			mu.Lock()
			inside--
			mu.Unlock()
			release()
		}()
	}
	wg.Wait()
	if peak > slots {
		t.Fatalf("%d hashes ran at once against a ceiling of %d — the peak memory of this package is "+
			"%d × 64 MiB, not %d", peak, slots, peak, slots)
	}
	if peak < 2 {
		t.Fatalf("only %d ran at once; the test did not exercise any contention", peak)
	}
}

// TestSlotsAreGivenBack.
//
// A leaked slot is invisible until the deployment has been up for a while and then refuses every sign-in
// permanently — the worst possible shape of bug, because a restart fixes it and nothing explains it.
func TestSlotsAreGivenBack(t *testing.T) {
	withGate(t, 1, 50*time.Millisecond)
	for i := range 5 {
		if _, err := HashPassword(context.Background(), "a-sufficiently-long-password"); err != nil {
			t.Fatalf("hash %d failed with one slot and nothing else running, so a previous call never "+
				"released: %v", i+1, err)
		}
	}
	// Including the paths that return early. A malformed hash is rejected before the gate is taken; a
	// wrong password takes it and must give it back.
	if err := VerifyPassword(context.Background(), "x", "not-a-hash"); !errors.Is(err, ErrBadHash) {
		t.Fatalf("expected ErrBadHash, got %v", err)
	}
	hash, err := HashPassword(context.Background(), "a-sufficiently-long-password")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if err := VerifyPassword(context.Background(), "wrong-but-long-enough", hash); !errors.Is(err, ErrWrongPassword) {
			t.Fatalf("verification %d: %v — a failed verification is not releasing its slot", i+1, err)
		}
	}
}

// TestAnOverloadedServerShedsInsteadOfQueueingForever.
func TestAnOverloadedServerShedsInsteadOfQueueingForever(t *testing.T) {
	withGate(t, 1, 20*time.Millisecond)

	// Hold the only slot for the duration.
	release, err := acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	start := time.Now()
	_, err = HashPassword(context.Background(), "a-sufficiently-long-password")
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy with the gate held, got %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("waited %s before shedding; the queue is not bounded by maxWait", d)
	}
	// 🚫 And the message must not mention the password or the account — this is a fact about the server.
	if s := err.Error(); strings.Contains(s, "password does not match") {
		t.Errorf("the overload error reads like a rejected password: %q", s)
	}
}

// TestAShortPasswordIsRefusedWithoutQueueing.
//
// Rejecting a password for being too short costs nothing, so it must not wait behind work that costs
// 64 MiB — otherwise a server under load stops being able to say the one thing it can answer instantly,
// and a user typing a short password during a busy minute is told the server is broken.
func TestAShortPasswordIsRefusedWithoutQueueing(t *testing.T) {
	withGate(t, 1, 20*time.Millisecond)
	release, err := acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if _, err := HashPassword(context.Background(), "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("with the gate held, a short password reported %v instead of being refused for its "+
			"length", err)
	}
}

// TestACancelledRequestDoesNotTakeASlot.
//
// 🔴 `select` chooses at random among ready cases, so a caller whose context has already expired would
// still take a slot about half the time — and spend 64 MiB producing an answer for a client that has
// already disconnected. Under the flood this whole mechanism exists for, that is the difference between
// a queue that drains and one that only grows.
func TestACancelledRequestDoesNotTakeASlot(t *testing.T) {
	withGate(t, 4, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for range 20 {
		if _, err := acquire(ctx); !errors.Is(err, ErrBusy) {
			t.Fatalf("a cancelled request was given a slot (%v)", err)
		}
	}
	// The slots are all still free, so real work is unaffected.
	if _, err := HashPassword(context.Background(), "a-sufficiently-long-password"); err != nil {
		t.Fatalf("the gate is exhausted after refusing cancelled callers: %v", err)
	}
}

// TestTheDefaultCeilingIsDerivedFromTheCpu.
//
// The right number is not a thing an operator should have to know: argon2id runs Parallelism threads per
// call, so GOMAXPROCS/Parallelism calls already saturate the machine and more only adds memory and
// latency. Deriving it means it follows a container's CPU limit instead of being a constant that is
// wrong on a laptop and wrong again on a large host.
func TestTheDefaultCeilingIsDerivedFromTheCpu(t *testing.T) {
	if got := defaultConcurrency(); got < 2 {
		t.Fatalf("the default ceiling is %d; at one, a single slow hash serialises every sign-in in the "+
			"deployment", got)
	}
	if Concurrency() < 1 {
		t.Fatal("no gate is installed")
	}
}

// TestOverloadLooksTheSameForAKnownAndAnUnknownAddress.
//
// # 🔴 The trap this exists for
//
// `Login` runs a decoy verification when no account matches, so that a missing user costs exactly what a
// wrong password costs — the timing half of refusing to say which addresses are real. The decoy's result
// was discarded, correctly, because checking a password nobody has is meaningless.
//
// Adding a ceiling made that discard a leak. Shedding produces an error, the real branch propagates it as
// ErrBusy, and the decoy branch swallowed it and returned ErrNoSuchUser — so on a busy server a real
// address answered 503 and an invented one answered 401, and anybody who wanted the customer list only
// had to make the server busy first.
//
// A limit, a decoy, and a cache all have this shape: each is a mechanism that behaves differently
// depending on something it is not supposed to reveal.
func TestOverloadLooksTheSameForAKnownAndAnUnknownAddress(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	s := NewStore(db)
	tenant := fmt.Sprintf("t-busy-%d", time.Now().UnixNano())
	if err := s.CreateTenant(ctx, tenant, "Acme"); err != nil {
		t.Fatal(err)
	}
	const password = "a-sufficiently-long-password"
	known := "known@example.test"
	if _, err := s.CreateUser(ctx, tenant, known, password, tenancy.Owner); err != nil {
		t.Fatal(err)
	}

	// Saturate: one slot, held, and no patience.
	withGate(t, 1, 20*time.Millisecond)
	release, err := acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	for _, c := range []struct{ what, email, password string }{
		{"a real address with the right password", known, password},
		{"a real address with the wrong password", known, "wrong-but-long-enough"},
		{"an address with no account", "nobody@example.test", "wrong-but-long-enough"},
	} {
		_, _, err := s.Login(ctx, tenant, c.email, c.password)
		if !errors.Is(err, ErrBusy) {
			t.Errorf("%s: got %v, want ErrBusy.\n"+
				"  On a busy server every one of these must be indistinguishable. If one of them reports "+
				"'no such user' while another reports 'busy', then making the server busy is how you find "+
				"out which addresses are real", c.what, err)
		}
	}
}
