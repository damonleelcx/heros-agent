package launch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	// The platform database is PostgreSQL. The local ledger's driver (modernc.org/sqlite) is registered
	// by internal/db; this registers the other one.
	//
	// Imported for its NAME as well as its side effect: `*pq.Error` is what distinguishes "the server
	// answered and refused us" from "nothing answered yet" in waitForPlatformDB.
	"github.com/lib/pq"
)

// EnvPlatformDSN names the platform database's connection string.
//
// `DATABASE_URL` rather than a HEROS_-prefixed name because cmd/legalretention already reads exactly
// this variable for exactly this database, and two names for one database is two things an operator has
// to keep in step. They would not stay in step.
const EnvPlatformDSN = "DATABASE_URL"

// openPlatformDB opens the platform database, or returns (nil, nil) when the deployment declares none.
//
// # Why an unreachable database is fatal here
//
// The same reason the secrets source is resolved at boot: a process that starts against a database it
// cannot reach looks healthy, serves /healthz, and fails the first real request with an error that reads
// like a query bug. Failing here names the actual problem at the only moment an operator is still
// watching the deployment they just ran.
//
// # Why an ABSENT DSN is not an error
//
// A single-binary or open-core deployment that runs only the local ledger is a supported form, not a
// misconfiguration. It gets the Postgres-backed capabilities registered-and-unsourced, so they answer
// 503 with a reason, and /readyz reports no `postgres` component rather than inventing one.
func openPlatformDB(ctx context.Context) (*sql.DB, error) {
	dsn := strings.TrimSpace(os.Getenv(EnvPlatformDSN))
	if dsn == "" {
		log.Printf("platform database: none declared (%s unset) — the Postgres-backed capabilities will report not-mounted", EnvPlatformDSN)
		return nil, nil
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("platform database: open: %w", err)
	}
	// Bounded, because an unbounded pool against a single-writer Postgres turns a traffic spike into a
	// connection-exhaustion incident on the store every other component depends on (P19 Decision 6: it
	// is a documented single point of failure, so its failure modes are ours to bound).
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := waitForPlatformDB(ctx, platformDBBootBudget, db.PingContext, log.Printf); err != nil {
		_ = db.Close()
		return nil, err
	}
	log.Printf("platform database: connected")
	return db, nil
}

// The retry budget, and why it is this number.
//
// 🔴 It is bounded BY THE LIVENESS PROBE, not by taste. `deploy/k8s/base/agentd.yaml` gives the
// container `initialDelaySeconds: 10`, `periodSeconds: 15`, `failureThreshold: 3` — so kubelet kills it
// roughly 55 s after start if `/healthz` has not answered, and this wait happens BEFORE the listener
// exists. A budget at or above that ceiling does not buy patience; it converts a self-healing restart
// into a kill during the wait, which is strictly worse because the container dies with no boot error to
// read. 30 s leaves the rest of boot — migrations included — inside the same ceiling.
//
// If those probe numbers change, change this one. They are two halves of a single decision and there is
// a test that fails when they drift apart.
const (
	platformDBBootBudget  = 30 * time.Second
	platformDBPingTimeout = 3 * time.Second
	platformDBBackoffMin  = 500 * time.Millisecond
	platformDBBackoffMax  = 5 * time.Second
)

// waitForPlatformDB pings until the database answers, the budget runs out, or the failure is one that
// waiting cannot fix.
//
// # Why this is a retry and not a relaxation
//
// An unreachable platform database is still FATAL, and deliberately — the doc comment on
// `openPlatformDB` says why, and nothing here weakens it. What changed is the question being asked. On
// every substrate this platform ships, Postgres and agentd start at the same moment; a single ping
// against a socket that is milliseconds from accepting is not a measurement of whether the database is
// reachable, it is a measurement of who won a race. agentd lost it on every deploy, exited 1, and was
// restarted into a now-warm Postgres — self-healing, and indistinguishable in the logs from the real
// misconfiguration this fail-fast exists to catch. A slower Postgres makes the same race an outage.
//
// # 🔴 The discriminator: did anything ANSWER
//
// Retrying a wrong password is not patience, it is a loud failure made quiet for thirty seconds. So the
// budget is spent only on errors where nothing answered at all — a dial refusal, an unresolved host, a
// timeout. The moment PostgreSQL itself replies, the server is up and any error it returns is a
// CONFIGURATION answer: bad password, no such database, no pg_hba line. Those fail immediately, with the
// same message and the same exit code they had before this function existed.
//
// `*pq.Error` is exactly that signal — the driver constructs it from a server ErrorResponse packet, so
// its presence proves a live conversation. Anything else is treated as "not up yet".
func waitForPlatformDB(ctx context.Context, budget time.Duration, ping func(context.Context) error, logf func(string, ...any)) error {
	deadline := time.Now().Add(budget)
	backoff := platformDBBackoffMin
	started := time.Now()

	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, platformDBPingTimeout)
		err := ping(pingCtx)
		cancel()
		if err == nil {
			if attempt > 1 {
				// The wait is REPORTED, never silent. A deployment that habitually takes 20 s to reach its
				// database is a finding — and a retry loop that hides it is how the finding is lost.
				logf("platform database: reachable after %s (%d attempt(s)) — it was not accepting connections at boot",
					time.Since(started).Round(100*time.Millisecond), attempt)
			}
			return nil
		}

		if !platformDBWorthRetrying(err) {
			// The server answered and refused us. Waiting changes nothing.
			return platformDBUnreachable(err)
		}
		if ctx.Err() != nil {
			return platformDBUnreachable(err)
		}
		if remaining := time.Until(deadline); remaining <= 0 {
			return fmt.Errorf("platform database: unreachable after %s (%d attempt(s); %s is set; check the "+
				"host, port, database and credentials): %w",
				time.Since(started).Round(100*time.Millisecond), attempt, EnvPlatformDSN, err)
		} else if backoff > remaining {
			backoff = remaining
		}

		if attempt == 1 {
			logf("platform database: not accepting connections yet — retrying for up to %s before giving up", budget)
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return platformDBUnreachable(err)
		}
		if backoff *= 2; backoff > platformDBBackoffMax {
			backoff = platformDBBackoffMax
		}
	}
}

// platformDBWorthRetrying reports whether nothing answered, which is the only case a wait can fix.
func platformDBWorthRetrying(err error) bool {
	var pqErr *pq.Error
	// A server-side error means PostgreSQL is up and talking. Not a startup race — a configuration answer.
	return !errors.As(err, &pqErr)
}

// platformDBUnreachable is the original boot failure, unchanged.
//
// The DSN is NOT in the message. It carries the password, and a boot failure is the single most likely
// line to be pasted into an issue, a chat, or a support ticket.
func platformDBUnreachable(err error) error {
	return fmt.Errorf("platform database: unreachable (%s is set; check the host, port, database and credentials): %w",
		EnvPlatformDSN, err)
}

// logCapabilities prints what this deployment serves, once, at boot.
//
// It is a boot log rather than only an endpoint because the operator most likely to need it is the one
// reading `docker compose logs` right after an install, asking why a console page says a capability is
// not installed. Printing the reason beside the name answers that without a second round trip.
func logCapabilities(caps []Capability) {
	var servedN int
	for _, c := range caps {
		if c.Served {
			servedN++
		}
	}
	log.Printf("capabilities: %d of %d served; the rest are REGISTERED and report 503 not-mounted (never 404)", servedN, len(caps))
	for _, c := range caps {
		if c.Served {
			log.Printf("  served       %s", c.Name)
		} else {
			log.Printf("  not mounted  %s — %s", c.Name, c.Why)
		}
	}
}
