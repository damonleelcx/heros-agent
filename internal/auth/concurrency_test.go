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
	// 🔴 Unique per run, like the tenant above. Addresses are unique across the WHOLE deployment since
	// migration 0008, so a fixed address makes this test pass once and fail for everybody afterwards —
	// including on the next run on the same machine. The address is incidental to what is asserted here.
	known := fmt.Sprintf("known-%d@example.test", time.Now().UnixNano())
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

// ── configuration ────────────────────────────────────────────────────────────────────────────────

// withEnvGate isolates a configuration test: the gate and the wait are package-level, so a test that
// changed them and returned early would leave every later test in the package running against them.
func withEnvGate(t *testing.T) {
	t.Helper()
	oldWait, oldGate := maxWait, current.Load()
	t.Cleanup(func() {
		maxWait = oldWait
		current.Store(oldGate)
	})
	t.Setenv(EnvConcurrency, "")
	t.Setenv(EnvMaxWait, "")
}

// TestConfigureFromEnvRefusesValuesThatWouldBreakTheDeployment.
//
// # 🔴 Why these are refusals and not "best effort"
//
// These two numbers ARE the protection. A wait of zero sheds every password check in the deployment —
// sign-in, invitations, resets — reporting that the server is busy while it sits idle; a ceiling of zero
// does the same. Both are one typo away, and neither is diagnosable from the symptom, because the symptom
// is a server confidently blaming load it does not have.
//
// Refusing at startup puts the mistake in front of the person who made it, seconds after they made it.
func TestConfigureFromEnvRefusesValuesThatWouldBreakTheDeployment(t *testing.T) {
	for name, c := range map[string]struct{ key, value, mustMention string }{
		"concurrency is not a number": {EnvConcurrency, "lots", EnvConcurrency},
		"concurrency is zero":         {EnvConcurrency, "0", EnvConcurrency},
		"concurrency is negative":     {EnvConcurrency, "-4", EnvConcurrency},
		"concurrency is a duration":   {EnvConcurrency, "3s", EnvConcurrency},
		"wait has no unit":            {EnvMaxWait, "3", EnvMaxWait},
		"wait is not a duration":      {EnvMaxWait, "soon", EnvMaxWait},
		"wait is zero":                {EnvMaxWait, "0s", EnvMaxWait},
		"wait is negative":            {EnvMaxWait, "-1s", EnvMaxWait},
	} {
		t.Run(name, func(t *testing.T) {
			withEnvGate(t)
			t.Setenv(c.key, c.value)
			err := ConfigureFromEnv()
			if err == nil {
				t.Fatalf("%s=%q was accepted", c.key, c.value)
			}
			// 🔴 The error must name the variable. "invalid configuration" sends somebody to read source;
			// naming the variable and the value they set ends the investigation at the first line.
			if !strings.Contains(err.Error(), c.mustMention) {
				t.Errorf("the error does not name %s: %v", c.mustMention, err)
			}
			if !strings.Contains(err.Error(), c.value) {
				t.Errorf("the error does not quote the value that was set: %v", err)
			}
		})
	}
}

// TestConfigureFromEnvAppliesValidValues.
func TestConfigureFromEnvAppliesValidValues(t *testing.T) {
	withEnvGate(t)
	t.Setenv(EnvConcurrency, "3")
	t.Setenv(EnvMaxWait, "250ms")
	if err := ConfigureFromEnv(); err != nil {
		t.Fatalf("a perfectly ordinary configuration was refused: %v", err)
	}
	if got := Concurrency(); got != 3 {
		t.Errorf("concurrency is %d, not the configured 3", got)
	}
	if got := MaxWait(); got != 250*time.Millisecond {
		t.Errorf("max wait is %s, not the configured 250ms", got)
	}
	// And it is really in force, not merely reported: with one slot held out of three, two more fit and
	// the fourth is shed.
	var held []func()
	for range 3 {
		release, err := acquire(context.Background())
		if err != nil {
			t.Fatalf("the configured ceiling of 3 did not admit 3: %v", err)
		}
		held = append(held, release)
	}
	if _, err := acquire(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("a fourth was admitted against a configured ceiling of 3: %v", err)
	}
	for _, release := range held {
		release()
	}
}

// TestAnUnsetEnvironmentLeavesTheDerivedDefault.
//
// The overwhelmingly common case. Almost no deployment should set these, and the derived value follows a
// container's CPU limit rather than being a constant that is wrong on both a laptop and a large host.
func TestAnUnsetEnvironmentLeavesTheDerivedDefault(t *testing.T) {
	withEnvGate(t)
	SetConcurrency(defaultConcurrency())
	SetMaxWait(3 * time.Second)
	if err := ConfigureFromEnv(); err != nil {
		t.Fatalf("an unset environment was an error: %v", err)
	}
	if got, want := Concurrency(), defaultConcurrency(); got != want {
		t.Errorf("concurrency is %d, not the derived %d", got, want)
	}
	if got := MaxWait(); got != 3*time.Second {
		t.Errorf("max wait is %s, not the default 3s", got)
	}
}

// TestSetMaxWaitRefusesAValueThatWouldShedEverything.
//
// Defence in depth behind ConfigureFromEnv: any caller reaching the setter directly gets the same answer,
// so the guard cannot be walked around by a second configuration path added later.
func TestSetMaxWaitRefusesAValueThatWouldShedEverything(t *testing.T) {
	withEnvGate(t)
	for _, d := range []time.Duration{0, -time.Second} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("SetMaxWait(%s) was accepted; every password check would be shed", d)
				}
			}()
			SetMaxWait(d)
		}()
	}
}
