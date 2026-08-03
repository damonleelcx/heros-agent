package billing

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ledger_pg.go is the durable Ledger, backed by migration 0024.
//
// # Why this is the thing that unblocks a whole capability
//
// internal/launch/capabilities.go left billing unmounted because "its only store implementation is
// in-memory, so mounting it would record and then forget". Billing is the one surface where that
// failure is not a blank page: a charge appended to a map, acknowledged to a payment provider, and lost
// on the next pod restart is a customer billed with no record on our side that it happened. Serving 503
// was the honest choice. This removes the reason for it.
//
// # The invariant that moved into the database
//
// MemLedger's comment says the `byKey` map IS the unique constraint. Here the UNIQUE INDEX is, and that
// is the stronger claim: two pods racing a retry cannot both insert even if this code is wrong. The
// idempotency key is the SAME key handed to the provider, so a second row is a second CHARGE.
//
// Append therefore does not check-then-insert. `ON CONFLICT DO NOTHING` makes the database the arbiter
// and a zero row count the signal — a prior SELECT would lose exactly the race it was written to catch.
//
// # Append-only, and what Settle is allowed to touch
//
// Postgres cannot express "append-only" as a constraint, so it is enforced by this type having no
// UPDATE except Settle, and Settle's statement naming only provider_ref, amount_ref, status and
// settled_at in its SET clause. It cannot move a customer, a period, a quantity or a cause, which is
// what an append-only billing ledger exists to prevent.

// PGLedger is the durable append-only Ledger.
type PGLedger struct {
	db  *sql.DB
	now func() time.Time
}

// NewPGLedger returns a durable Ledger over an open Postgres handle.
func NewPGLedger(db *sql.DB) (*PGLedger, error) {
	if db == nil {
		return nil, errors.New("billing: nil database")
	}
	return &PGLedger{db: db, now: time.Now}, nil
}

// newEventID mints a billing event id.
//
// Random rather than the sequence MemLedger uses. Two reasons, and the second is the real one: a
// sequential id minted in Go is not safe across pods (two would collide), and a monotonic public id
// leaks how many billing events the platform has recorded.
func newEventID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not a condition to paper over with a timestamp: a predictable id on a
		// billing row is a weak identifier on a financial record, and this runs while a request is in
		// flight rather than at boot, so the panic is visible and attributable.
		panic("billing: crypto/rand unavailable while minting a billing event id: " + err.Error())
	}
	return "be_" + hex.EncodeToString(b[:])
}

func (p *PGLedger) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// Append records ev, or returns the EXISTING row for a duplicate key.
//
// The duplicate path returns the stored row together with ErrDuplicateKey, exactly as MemLedger does:
// a retry is a NORMAL outcome, and the caller reads the existing row and returns it rather than
// treating the retry as a failure.
func (p *PGLedger) Append(ev BillingEvent) (BillingEvent, error) {
	// Same validation as the in-memory ledger, called here rather than duplicated: the rules about a
	// missing cause or a correction with no reason are the ledger's, not one implementation's.
	if err := validate(ev); err != nil {
		return BillingEvent{}, err
	}
	if ev.EventID == "" {
		ev.EventID = newEventID()
	}
	if ev.Status == "" {
		ev.Status = StatusPending
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = p.now().UTC()
	}
	evidence, err := json.Marshal(ev.Evidence)
	if err != nil {
		return BillingEvent{}, fmt.Errorf("billing: encode evidence for %s: %w", ev.IdempotencyKey, err)
	}
	if len(ev.Evidence) == 0 {
		evidence = []byte("[]")
	}

	ctx, cancel := p.ctx()
	defer cancel()

	res, err := p.db.ExecContext(ctx,
		`INSERT INTO billing_event
		   (event_id, customer_id, period, type, kind, idempotency_key, provider_ref, amount_ref,
		    caused_by, reason, quantity, status, evidence, created_at, settled_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		 ON CONFLICT (idempotency_key) DO NOTHING`,
		ev.EventID, ev.CustomerID, ev.Period, string(ev.Type), string(ev.Kind), ev.IdempotencyKey,
		ev.ProviderRef, ev.AmountRef, ev.CausedBy, ev.Reason, ev.Quantity, string(ev.Status),
		evidence, ev.CreatedAt.UTC(), nullTime(ev.SettledAt))
	if err != nil {
		return BillingEvent{}, fmt.Errorf("billing: append %s: %w", ev.IdempotencyKey, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return BillingEvent{}, fmt.Errorf("billing: append %s: %w", ev.IdempotencyKey, err)
	}
	if n == 0 {
		existing, gerr := p.ByKey(ev.IdempotencyKey)
		if gerr != nil {
			// The insert conflicted but the row cannot be read back. Do NOT report ErrDuplicateKey with
			// a zero row: the caller would return that empty row to a provider as though it were the
			// original charge.
			return BillingEvent{}, fmt.Errorf("billing: %s conflicted but could not be read back: %w",
				ev.IdempotencyKey, gerr)
		}
		return existing, fmt.Errorf("%w: %s", ErrDuplicateKey, ev.IdempotencyKey)
	}
	return ev, nil
}

// ByKey returns the row for an idempotency key.
func (p *PGLedger) ByKey(key string) (BillingEvent, error) {
	ctx, cancel := p.ctx()
	defer cancel()

	row := p.db.QueryRowContext(ctx, `SELECT `+billingEventColumns+` FROM billing_event WHERE idempotency_key = $1`, key)
	ev, err := scanBillingEvent(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return BillingEvent{}, fmt.Errorf("%w: %s", ErrEventNotFound, key)
	case err != nil:
		return BillingEvent{}, fmt.Errorf("billing: read %s: %w", key, err)
	}
	return ev, nil
}

// Settle stamps the provider's refs onto a pending row and marks it recorded.
//
// The WHERE clause carries `status <> recorded`, so an already-settled row updates ZERO rows and the
// caller is told ErrAlreadySettled. Doing it in the statement rather than as a read-then-write is what
// makes it safe under concurrency: two recovery sweeps racing the same pending row cannot both stamp
// it, which would let the second provider call's receipt overwrite the first's.
func (p *PGLedger) Settle(key, providerRef, amountRef string, at time.Time) (BillingEvent, error) {
	ctx, cancel := p.ctx()
	defer cancel()

	res, err := p.db.ExecContext(ctx,
		`UPDATE billing_event
		    SET provider_ref = $2, amount_ref = $3, status = $4, settled_at = $5
		  WHERE idempotency_key = $1 AND status <> $4`,
		key, providerRef, amountRef, string(StatusRecorded), at.UTC())
	if err != nil {
		return BillingEvent{}, fmt.Errorf("billing: settle %s: %w", key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return BillingEvent{}, fmt.Errorf("billing: settle %s: %w", key, err)
	}
	if n == 0 {
		// Zero rows means either "no such key" or "already recorded", and those are different answers.
		// Read the row to say which — a caller retrying a settle needs to know whether to stop because
		// it is done, or to investigate because the row is missing.
		existing, gerr := p.ByKey(key)
		if gerr != nil {
			return BillingEvent{}, gerr // already ErrEventNotFound-wrapped
		}
		return existing, fmt.Errorf("%w: %s", ErrAlreadySettled, key)
	}
	return p.ByKey(key)
}

// Events returns a customer's rows in append order. An empty period returns all of them.
func (p *PGLedger) Events(customerID, period string) ([]BillingEvent, error) {
	ctx, cancel := p.ctx()
	defer cancel()

	// event_id breaks a created_at tie so "append order" is stable rather than whatever the planner
	// returns — a reconciliation that reorders between reads is one nobody can check twice.
	q := `SELECT ` + billingEventColumns + ` FROM billing_event
	       WHERE customer_id = $1 AND ($2 = '' OR period = $2)
	       ORDER BY created_at ASC, event_id ASC`
	rows, err := p.db.QueryContext(ctx, q, customerID, period)
	if err != nil {
		return nil, fmt.Errorf("billing: events for %s: %w", customerID, err)
	}
	defer func() { _ = rows.Close() }()
	return scanBillingEvents(rows, fmt.Sprintf("events for %s", customerID))
}

// Pending returns every unsettled row, oldest first — the outage buffer recovery drains.
func (p *PGLedger) Pending() ([]BillingEvent, error) {
	ctx, cancel := p.ctx()
	defer cancel()

	rows, err := p.db.QueryContext(ctx,
		`SELECT `+billingEventColumns+` FROM billing_event
		  WHERE status = $1 ORDER BY created_at ASC, event_id ASC`, string(StatusPending))
	if err != nil {
		return nil, fmt.Errorf("billing: pending buffer: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanBillingEvents(rows, "pending buffer")
}

// billingEventColumns is the SELECT list every reader shares, so a column added for one cannot read as
// its zero value in another.
const billingEventColumns = `event_id, customer_id, period, type, kind, idempotency_key, provider_ref,
	amount_ref, caused_by, reason, quantity, status, evidence, created_at, settled_at`

type scanner interface{ Scan(...any) error }

func scanBillingEvent(sc scanner) (BillingEvent, error) {
	var ev BillingEvent
	var typ, kind, status string
	var evidence []byte
	var settled sql.NullTime
	if err := sc.Scan(&ev.EventID, &ev.CustomerID, &ev.Period, &typ, &kind, &ev.IdempotencyKey,
		&ev.ProviderRef, &ev.AmountRef, &ev.CausedBy, &ev.Reason, &ev.Quantity, &status,
		&evidence, &ev.CreatedAt, &settled); err != nil {
		return BillingEvent{}, err
	}
	ev.Type, ev.Kind, ev.Status = EventType(typ), ChargeKind(kind), Status(status)
	if len(evidence) > 0 {
		if err := json.Unmarshal(evidence, &ev.Evidence); err != nil {
			return BillingEvent{}, fmt.Errorf("decode evidence for %s: %w", ev.EventID, err)
		}
	}
	if settled.Valid {
		t := settled.Time.UTC()
		ev.SettledAt = &t
	}
	return ev, nil
}

func scanBillingEvents(rows *sql.Rows, what string) ([]BillingEvent, error) {
	var out []BillingEvent
	for rows.Next() {
		ev, err := scanBillingEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("billing: %s: %w", what, err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing: %s: %w", what, err)
	}
	return out, nil
}

// nullTime renders an optional timestamp for the driver. NULL rather than the zero time: "not yet
// settled" and "settled at year zero" must not be the same value on a row that decides what a customer
// was charged.
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}
