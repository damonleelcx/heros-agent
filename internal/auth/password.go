// Package auth is identity: who somebody is, and the credential that proves it.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/argon2"
)

// password.go hashes passwords with argon2id.
//
// # 🔴 Why argon2id and not something in the standard library
//
// The standard library has SHA-256, and SHA-256 is exactly wrong here: it is fast, which is the property
// an attacker with a stolen table wants. A password hash must be SLOW and memory-hard, so that guessing
// costs the attacker what it costs the server, once per attempt. argon2id is the current answer and is
// what `x/crypto` provides.
//
// 🚫 Nothing here is hand-rolled. The one part I chose is the parameters, and they are encoded INTO each
// hash so they can be raised later without invalidating anybody's existing password.

// Params are the argon2id cost parameters.
//
// These follow the RFC 9106 second recommended option: 64 MiB, three passes. They are deliberately
// expensive — a login takes tens of milliseconds, which is unnoticeable to a person and ruinous to
// somebody trying billions of guesses.
type Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams are used for new passwords.
func DefaultParams() Params {
	return Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 2, SaltLength: 16, KeyLength: 32}
}

var (
	ErrBadHash       = errors.New("auth: password hash is malformed")
	ErrWrongPassword = errors.New("auth: password does not match")
	ErrWeakPassword  = errors.New("auth: password is too short")
	// ErrBusy means the server is already running as many password hashes as it can afford and this one
	// waited too long for a turn.
	//
	// 🔴 A DISTINCT error, and never folded into ErrWrongPassword. Reporting overload as "that password
	// is wrong" tells somebody their correct password is incorrect — so they go and reset it, which spends
	// their reset budget and sends mail, over a server that was merely busy. One outage would become a
	// wave of unnecessary password changes, and the logs would show nothing but ordinary failed logins.
	ErrBusy = errors.New("auth: too many password checks are already running")
)

// MinPasswordLength is the floor.
//
// 🔴 A length floor and nothing else — no character-class rules. Those push people towards `Passw0rd!`,
// which is short, guessable, and satisfies every rule anybody writes. Length is the property that
// actually costs an attacker.
const MinPasswordLength = 12

// HashPassword returns an encoded argon2id hash carrying its own salt and parameters.
//
// The length check happens BEFORE the gate: refusing a short password costs nothing and should not queue
// behind work that costs 64 MiB.
func HashPassword(ctx context.Context, password string) (string, error) {
	if len([]rune(password)) < MinPasswordLength {
		return "", fmt.Errorf("%w: passwords must be at least %d characters", ErrWeakPassword, MinPasswordLength)
	}
	release, err := acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	p := DefaultParams()
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generating salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against an encoded hash.
//
// 🔴 The comparison is constant-time. A byte-by-byte comparison leaks, through timing, how much of the
// hash matched — which is enough to reconstruct it one byte at a time given enough attempts.
func VerifyPassword(ctx context.Context, password, encoded string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return ErrBadHash
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return ErrBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return ErrBadHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return ErrBadHash
	}
	// 🔴 The gate is taken here, AFTER parsing and before the only expensive step. Parsing is free; what
	// has to be bounded is the 64 MiB allocation on the next line.
	release, err := acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrWrongPassword
	}
	return nil
}

// ── bounding how much of this runs at once ───────────────────────────────────────────────────────

// # 🔴 Why argon2id needs a ceiling on concurrency and not just a rate limit
//
// The cost that makes this hash good is the cost that makes it a weapon. Every call allocates 64 MiB and
// holds it for tens of milliseconds, so two hundred simultaneous requests ask for THIRTEEN GIGABYTES —
// and the process is killed by the kernel, taking every unrelated in-flight request with it.
//
// The rate limits do not close this. They are keyed on an account and an inbox, so an attacker spreads
// across a thousand invented addresses and never trips one; and an address with no account still runs a
// full verification against a decoy hash, deliberately, so that a missing user costs what a wrong
// password costs. Every one of those requests is 64 MiB. So is every password reset and every invitation
// accepted with a garbage token, both of which hash before they check anything.
//
// A ceiling turns that from "the server dies" into "the server is slow, and then says so". Shedding beats
// dying: an out-of-memory kill takes down the requests that were perfectly fine, and the ones that would
// have completed in a millisecond, and the console, and the worker.
//
// 🔴 The gate lives INSIDE these two functions rather than in middleware on the login route. Middleware
// would bound the one path somebody remembered, and the paths that hash — accepting an invitation,
// resetting a password, creating a user — are the ones with no rate limit in front of them at all.

// maxWait is how long a request will queue for a turn before being shed.
//
// Long enough that an ordinary burst is absorbed invisibly — a handful of simultaneous sign-ins queue for
// a few hundred milliseconds and nobody notices — and short enough that under a real flood the queue does
// not become its own exhaustion, with thousands of connections held open waiting for a slot that is
// always taken.
var maxWait = 3 * time.Second

// SetMaxWait changes how long a request queues before being shed. Call before serving.
//
// A knob rather than a constant because the right value depends on what is in front of the server: with
// a proxy that gives up after two seconds, queueing for three produces work nobody is waiting for any
// more.
//
// 🔴 Refuses a non-positive value, the same way SetConcurrency refuses a ceiling below one. At zero or
// below, `acquire` finds its deadline already passed and sheds EVERY password check — so sign-in,
// invitations and resets would all fail, reporting that the server is busy, on a server doing nothing.
// Failing here means that mistake stops the process at startup instead of presenting as a total outage
// with a misleading explanation.
func SetMaxWait(d time.Duration) {
	if d <= 0 {
		panic("auth: a max wait of zero or less would shed every password check")
	}
	maxWait = d
}

// gate is a counting semaphore. A buffered channel rather than a sync primitive, because the select below
// is what allows waiting to be abandoned when the caller goes away.
type gate struct{ slots chan struct{} }

func newGate(n int) *gate { return &gate{slots: make(chan struct{}, n)} }

// current is swapped by SetConcurrency. Atomic so a swap cannot race a request; a caller that entered
// through one gate leaves through the same one, because release closes over it.
var current atomic.Pointer[gate]

// gateNow returns the gate, installing the default on first use.
//
// 🔴 Lazily, and NOT from an init function. `dummyHash` is a package-level variable whose initialiser
// calls HashPassword, and Go initialises package variables BEFORE it runs init functions — so an init
// that installed the gate would run after the first call that needed it, and the process would panic on
// a nil pointer at startup, on every deployment, in a way no test that imports this package normally
// would reach. Installing on demand removes the ordering question rather than answering it correctly
// once and hoping the next edit preserves it.
func gateNow() *gate {
	if g := current.Load(); g != nil {
		return g
	}
	fresh := newGate(defaultConcurrency())
	if current.CompareAndSwap(nil, fresh) {
		return fresh
	}
	return current.Load() // somebody else won the race; use theirs
}

// defaultConcurrency is how many hashes this machine can genuinely run at once.
//
// argon2id is configured with Parallelism threads per call, so GOMAXPROCS/Parallelism calls already
// saturate the CPU; more than that adds memory and latency without adding a single hash per second. That
// makes the right number derivable rather than a thing an operator has to know — and it moves with the
// container's CPU limit instead of being a constant that is wrong on both a laptop and a large host.
func defaultConcurrency() int {
	n := runtime.GOMAXPROCS(0) / int(DefaultParams().Parallelism)
	// Never below two: at one, a single slow hash serialises every sign-in in the deployment, and the
	// remedy for a busy server would be a server that can only ever do one thing.
	return max(n, 2)
}

// SetConcurrency changes the ceiling. Call before serving; exported for tests and for an operator whose
// memory budget differs from what the CPU count implies.
func SetConcurrency(n int) {
	if n < 1 {
		panic("auth: a concurrency below 1 would refuse every password check")
	}
	current.Store(newGate(n))
}

// Concurrency reports the ceiling, for a startup banner or a health endpoint.
func Concurrency() int { return cap(gateNow().slots) }

// acquire waits for a turn, and returns the function that gives it back.
func acquire(ctx context.Context) (release func(), err error) {
	g := gateNow()
	// 🔴 The caller's context is honoured as well as the deadline. A client that has given up and
	// disconnected should free its place in the queue immediately, rather than holding it — under a flood
	// that is the difference between a queue that drains and one that only grows.
	wait := maxWait
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	// 🔴 Checked before the select, not only inside it. A `select` chooses at RANDOM among cases that are
	// both ready, so a context that has already expired would still take a slot about half the time — and
	// the half that matters is the one where the client has already gone and the server is about to spend
	// 64 MiB producing an answer for nobody. Checking first makes "the caller is no longer waiting" mean
	// what it says.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: the request was no longer waiting after %s", ErrBusy, wait)
	}
	select {
	case g.slots <- struct{}{}:
		return func() { <-g.slots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: waited %s for one of %d slots", ErrBusy, wait, cap(g.slots))
	}
}

// MaxWait reports how long a request will queue before being shed, for a startup banner.
func MaxWait() time.Duration { return maxWait }

// ConfigureFromEnv applies HEROS_PASSWORD_CONCURRENCY and HEROS_PASSWORD_MAX_WAIT.
//
// # 🔴 Why this validates rather than accepting what it is given
//
// These two numbers ARE the protection. A concurrency of 200 re-opens the exhaustion the ceiling exists
// to close, and a wait of zero refuses every password check in the deployment — sign-in, invitation,
// reset, all of it — with an error about the server being busy while it sits idle. Both are one typo
// away, both fail at the worst possible moment, and neither would be obvious from the symptom.
//
// So: nonsense is refused at startup, where an operator is watching, and a value that is merely
// surprising is applied with a warning that says what it will cost. A server that will not boot is a far
// better outcome than one that boots wrong — the same argument the bootstrap credentials make.
//
// Unset means the derived default, which is what almost every deployment should use.
func ConfigureFromEnv() error {
	if raw := strings.TrimSpace(os.Getenv(EnvConcurrency)); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("%s is %q, which is not a whole number. It is how many password hashes may "+
				"run at once; leave it unset to use %d, derived from this machine's CPU count",
				EnvConcurrency, raw, defaultConcurrency())
		}
		if n < 1 {
			return fmt.Errorf("%s is %d. At zero or below, every sign-in, invitation and password reset "+
				"in this deployment would be refused as 'server busy' while the server sat idle",
				EnvConcurrency, n)
		}
		// 🔴 A warning, not a refusal. Only the operator knows how much memory the container has, and
		// refusing a large value on a large host would be this code overruling somebody who knows better.
		// What it can do is state the two facts they need: what it will cost, and that beyond the derived
		// figure it buys no throughput, because argon2id already runs Parallelism threads per call.
		if derived := defaultConcurrency(); n > derived*2 {
			log.Printf("WARN auth.concurrency.above_cpu %s=%d — that is %d MiB of argon2id live at once "+
				"(resident memory settles well above that), and beyond %d it adds no hashes per second "+
				"on a machine with %d CPUs. Set it lower unless you know the memory is there",
				EnvConcurrency, n, n*64, derived, runtime.GOMAXPROCS(0))
		}
		SetConcurrency(n)
		log.Printf("auth: password concurrency set to %d by %s", n, EnvConcurrency)
	}

	if raw := strings.TrimSpace(os.Getenv(EnvMaxWait)); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("%s is %q, which is not a duration. Write it with a unit — 3s, 1500ms, 1m",
				EnvMaxWait, raw)
		}
		if d <= 0 {
			return fmt.Errorf("%s is %s. At zero or below, no request would ever wait for a free slot, so "+
				"every password check beyond the first few would be refused as 'server busy'",
				EnvMaxWait, d)
		}
		// Long waits are not wrong, but they trade one exhaustion for another: nothing is hashing, and
		// thousands of connections are held open waiting for a slot.
		if d > 30*time.Second {
			log.Printf("WARN auth.maxwait.long %s=%s — under load that many requests will sit holding "+
				"connections rather than being shed, which is its own way to run out of resources",
				EnvMaxWait, d)
		}
		SetMaxWait(d)
		log.Printf("auth: password max wait set to %s by %s", d, EnvMaxWait)
	}
	return nil
}

// The environment variables ConfigureFromEnv reads. Named constants so the error messages, the tests and
// the documentation cannot drift from what is actually read.
const (
	EnvConcurrency = "HEROS_PASSWORD_CONCURRENCY"
	EnvMaxWait     = "HEROS_PASSWORD_MAX_WAIT"
)
