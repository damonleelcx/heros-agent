// platformdb_test.go covers the boot-time wait for the platform database.
//
// The behaviour under test is a narrow one and easy to get subtly wrong in the direction that hurts:
// a retry loop that is too tolerant turns a real misconfiguration into a thirty-second silence, and one
// that is too eager turns a normal cold start into a crash loop. Both failures look like "it works" on a
// warm laptop, so every case here is asserted rather than observed.
package launch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

// dialRefused is what the driver actually returns when nothing is listening yet — the exact shape that
// made agentd exit 1 on every deploy.
func dialRefused() error {
	return &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: errors.New("connect: connection refused"),
	}
}

func discard(string, ...any) {}

func TestWaitRetriesUntilPostgresAcceptsConnections(t *testing.T) {
	attempts := 0
	err := waitForPlatformDB(context.Background(), 5*time.Second, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return dialRefused()
		}
		return nil
	}, discard)
	if err != nil {
		t.Fatalf("a database that came up on the third ping should be reached, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

// 🔴 The discriminator. A server-side error means PostgreSQL answered, so waiting cannot help — and
// spending the budget on it would convert the single most common real misconfiguration (a wrong
// password) from an immediate, readable boot failure into a thirty-second stall.
func TestWaitDoesNotRetryWhenTheServerAnswered(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"wrong password", &pq.Error{Code: "28P01", Message: "password authentication failed for user \"heros\""}},
		{"no such database", &pq.Error{Code: "3D000", Message: "database \"heros\" does not exist"}},
		{"no pg_hba entry", &pq.Error{Code: "28000", Message: "no pg_hba.conf entry for host"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			started := time.Now()
			err := waitForPlatformDB(context.Background(), 30*time.Second, func(context.Context) error {
				attempts++
				return tc.err
			}, discard)
			if err == nil {
				t.Fatal("a server-side refusal must still fail the boot")
			}
			if attempts != 1 {
				t.Errorf("attempts = %d, want 1 — the server answered, so the budget must not be spent", attempts)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Errorf("took %s — a configuration error must fail immediately, not after a wait", elapsed)
			}
			if !strings.Contains(err.Error(), "platform database: unreachable") {
				t.Errorf("the message changed shape: %v", err)
			}
		})
	}
}

// A wrapped pq error must be recognised too — the driver does not always hand it back bare.
func TestWaitUnwrapsToFindTheServerAnswer(t *testing.T) {
	wrapped := fmt.Errorf("connecting: %w", &pq.Error{Code: "28P01"})
	if platformDBWorthRetrying(wrapped) {
		t.Error("a wrapped *pq.Error was treated as 'nothing answered', so a wrong password would stall")
	}
	if !platformDBWorthRetrying(fmt.Errorf("dialing: %w", dialRefused())) {
		t.Error("a wrapped dial refusal was treated as a server answer, so the cold-start race is back")
	}
}

func TestWaitGivesUpAndStillFailsTheBoot(t *testing.T) {
	attempts := 0
	started := time.Now()
	err := waitForPlatformDB(context.Background(), 900*time.Millisecond, func(context.Context) error {
		attempts++
		return dialRefused()
	}, discard)
	if err == nil {
		t.Fatal("a database that never comes up MUST fail the boot — this is not a relaxation")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("overran its budget by a long way (%s) — the budget must bound the wait", elapsed)
	}
	if attempts < 2 {
		t.Errorf("attempts = %d — it gave up without retrying", attempts)
	}
	// The operator needs to know a wait happened, or the failure reads as instant and they look for the
	// wrong cause.
	for _, want := range []string{"unreachable after", "attempt(s)", EnvPlatformDSN} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the exhaustion message does not mention %q: %v", want, err)
		}
	}
}

// The DSN carries the password and must never reach a log line or an error string.
func TestWaitNeverPrintsTheDSN(t *testing.T) {
	err := waitForPlatformDB(context.Background(), 200*time.Millisecond, func(context.Context) error {
		return dialRefused()
	}, discard)
	if err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "postgres://") {
		t.Errorf("the boot failure leaks connection detail: %v", err)
	}
}

func TestWaitStopsWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if err := waitForPlatformDB(ctx, 30*time.Second, func(context.Context) error {
		return dialRefused()
	}, discard); err == nil {
		t.Fatal("a cancelled boot must not report a healthy database")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Errorf("ignored cancellation for %s — a shutdown would hang behind the retry budget", elapsed)
	}
}

// A slow start is REPORTED. A retry loop that hides a database habitually taking 20 s to accept
// connections has removed the only signal that it does.
func TestASlowStartIsLoggedRatherThanSwallowed(t *testing.T) {
	var lines []string
	attempts := 0
	if err := waitForPlatformDB(context.Background(), 5*time.Second, func(context.Context) error {
		attempts++
		if attempts < 2 {
			return dialRefused()
		}
		return nil
	}, func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "not accepting connections") || !strings.Contains(joined, "reachable after") {
		t.Errorf("a database that was down at boot came up silently. Logged:\n%s", joined)
	}
}

// A database that is up on the first ping must not log a retry line — otherwise the signal above
// becomes noise on every healthy boot and stops meaning anything.
func TestAHealthyBootIsSilent(t *testing.T) {
	var lines []string
	if err := waitForPlatformDB(context.Background(), 5*time.Second, func(context.Context) error {
		return nil
	}, func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("a healthy boot logged %d retry line(s): %v", len(lines), lines)
	}
}

// 🔴 The budget and the liveness probe are two halves of one decision.
//
// The wait happens BEFORE the listener exists, so every second of it is a second of `/healthz` refusing
// connections. If the budget ever reaches the kubelet's kill threshold, the container is killed DURING
// the wait — with no boot error written — and the retry has made things strictly worse than the crash
// loop it replaced. This test reads the real manifest so the two numbers cannot drift apart in silence.
func TestTheRetryBudgetStaysInsideTheLivenessProbeBudget(t *testing.T) {
	const manifest = "../../deploy/k8s/base/agentd.yaml"
	b, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("reading %s: %v", manifest, err)
	}
	body := string(b)

	idx := strings.Index(body, "livenessProbe:")
	if idx < 0 {
		t.Fatalf("%s has no livenessProbe — this test can no longer bound anything, which is not a pass", manifest)
	}
	block := body[idx:]
	if end := strings.Index(block, "readinessProbe:"); end > 0 {
		block = block[:end]
	}

	field := func(name string) int {
		m := regexp.MustCompile(name + `:\s*(\d+)`).FindStringSubmatch(block)
		if m == nil {
			t.Fatalf("livenessProbe in %s declares no %s", manifest, name)
		}
		n, convErr := strconv.Atoi(m[1])
		if convErr != nil {
			t.Fatalf("%s = %q: %v", name, m[1], convErr)
		}
		return n
	}

	// When kubelet gives up: the grace before the first probe, plus one period per allowed failure.
	kill := time.Duration(field("initialDelaySeconds"))*time.Second +
		time.Duration(field("periodSeconds")*field("failureThreshold"))*time.Second
	if platformDBBootBudget >= kill {
		t.Fatalf("the platform-database retry budget is %s but kubelet kills the container after about %s "+
			"(initialDelay + periodSeconds*failureThreshold in %s). The wait runs before the listener "+
			"exists, so a budget at or above that threshold means the container is killed mid-wait with no "+
			"boot error to read — worse than the crash loop this retry replaced. Lower the budget or raise "+
			"the probe, together.", platformDBBootBudget, kill, manifest)
	}
	// And it must be worth having: a budget shorter than a couple of ping timeouts cannot survive the
	// race it exists for.
	if platformDBBootBudget < 4*platformDBPingTimeout {
		t.Errorf("a %s budget with a %s ping timeout allows too few attempts to cover a cold start",
			platformDBBootBudget, platformDBPingTimeout)
	}
}
