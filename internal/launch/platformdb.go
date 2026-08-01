package launch

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	// The platform database is PostgreSQL. The local ledger's driver (modernc.org/sqlite) is registered
	// by internal/db; this registers the other one.
	_ "github.com/lib/pq"
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

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		// The DSN is NOT in the message. It carries the password, and a boot failure is the single most
		// likely line to be pasted into an issue, a chat, or a support ticket.
		return nil, fmt.Errorf("platform database: unreachable (%s is set; check the host, port, database and credentials): %w",
			EnvPlatformDSN, err)
	}
	log.Printf("platform database: connected")
	return db, nil
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
