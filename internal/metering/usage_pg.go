package metering

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// usage_pg.go is the durable UsageStore, over the `usage_record` table migration 0013 created.
//
// # Why this had to exist before billing could mount
//
// The billing view reads usage records for the SUM figure and every meter row — the number a customer
// is charged on. `MemUsageStore` was the only implementation, so mounting billing over it would have
// shown spend that vanished on the next restart. That is the "installed and quietly lossy" state
// internal/launch/capabilities.go calls a worse lie than an honest 503, and it applies with particular
// force here: a meter that forgets is not a degraded feature, it is a wrong invoice.
//
// The table was already there. As with `billing_event` and `account`, what was missing was Go code that
// used it.
//
// # The no-op rule is the reason Upsert is not a plain INSERT ... ON CONFLICT DO UPDATE
//
// An identical re-derivation — same source digest, same quantity — must leave the row EXACTLY as it is,
// including `updated_at` and the provider hand-off state. A blind upsert would bump updated_at on every
// re-derivation, churn the reconciler, and make "when did this last change" unanswerable. So the
// conflict clause is guarded by a WHERE that fires only on a real change, and `xmax` distinguishes the
// insert from the update.
//
// # A changed quantity is UN-REPORTED
//
// When a re-derivation does change the measurement, the provider hand-off is cleared: the ref belongs to
// the quantity that was reported, so a new quantity has not been reported yet. Carrying the old ref
// forward would mark a number as sent to the provider when a different number was sent.

// PGUsageStore is the durable UsageStore.
type PGUsageStore struct{ db *sql.DB }

// NewPGUsageStore returns a durable UsageStore over an open Postgres handle.
func NewPGUsageStore(db *sql.DB) (*PGUsageStore, error) {
	if db == nil {
		return nil, errors.New("metering: nil database")
	}
	return &PGUsageStore{db: db}, nil
}

func (p *PGUsageStore) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

const usageColumns = `customer_id, period, metric, quantity, source_digest, reported_to_provider,
	provider_usage_ref, updated_at`

// Upsert writes rec keyed {customer, period, metric}, returning whether anything CHANGED.
func (p *PGUsageStore) Upsert(rec UsageRecord) (UsageRecord, bool, error) {
	// Same validation as the in-memory store: these rules belong to the type, not to one backing.
	if !KnownMetric(rec.Metric) {
		return UsageRecord{}, false, fmt.Errorf("%w: %q", ErrUnknownMetric, rec.Metric)
	}
	if rec.CustomerID == "" || rec.Period == "" {
		return UsageRecord{}, false, errors.New("metering: usage record needs both a customer and a period")
	}
	if rec.Quantity < 0 {
		return UsageRecord{}, false, fmt.Errorf("%w: %v", ErrNegativeUsage, rec.Quantity)
	}
	if rec.SourceDigest == "" {
		return UsageRecord{}, false, ErrMissingDigest
	}
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = time.Now().UTC()
	}

	ctx, cancel := p.ctx()
	defer cancel()

	// The WHERE on the conflict clause is what makes an identical re-derivation a genuine no-op rather
	// than a same-values UPDATE that still bumps updated_at.
	//
	// `xmax = 0` is the standard way to tell an INSERT from an UPDATE in a RETURNING clause: a freshly
	// inserted tuple has no updating transaction. It is used only to report `changed`, never to decide
	// anything, so the mildly obscure trick stays contained to one boolean.
	var out UsageRecord
	var providerRef sql.NullString
	var inserted bool
	err := p.db.QueryRowContext(ctx,
		`INSERT INTO usage_record (`+usageColumns+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (customer_id, period, metric) DO UPDATE
		   SET quantity = EXCLUDED.quantity,
		       source_digest = EXCLUDED.source_digest,
		       updated_at = EXCLUDED.updated_at,
		       -- A changed measurement is un-reported: the ref belongs to the quantity that was sent.
		       reported_to_provider = FALSE,
		       provider_usage_ref = NULL
		 WHERE usage_record.source_digest IS DISTINCT FROM EXCLUDED.source_digest
		    OR usage_record.quantity IS DISTINCT FROM EXCLUDED.quantity
		 RETURNING `+usageColumns+`, (xmax = 0) AS inserted`,
		rec.CustomerID, rec.Period, string(rec.Metric), rec.Quantity, rec.SourceDigest,
		rec.ReportedToProvider, nullStr(rec.ProviderUsageRef), rec.UpdatedAt.UTC()).
		Scan(&out.CustomerID, &out.Period, &out.Metric, &out.Quantity, &out.SourceDigest,
			&out.ReportedToProvider, &providerRef, &out.UpdatedAt, &inserted)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// The conflict clause's WHERE excluded the row, which means nothing changed. Return the row AS
		// STORED — not the caller's copy, which may differ in fields the no-op deliberately preserved
		// (updated_at, and the provider hand-off state).
		stored, gerr := p.Get(rec.Key())
		if gerr != nil {
			return UsageRecord{}, false, gerr
		}
		return stored, false, nil
	case err != nil:
		return UsageRecord{}, false, fmt.Errorf("metering: upsert usage %s/%s/%s: %w",
			rec.CustomerID, rec.Period, rec.Metric, err)
	}
	out.ProviderUsageRef = providerRef.String
	return out, true, nil
}

// Get returns one record, or ErrUsageNotFound.
func (p *PGUsageStore) Get(k Key) (UsageRecord, error) {
	ctx, cancel := p.ctx()
	defer cancel()

	row := p.db.QueryRowContext(ctx,
		`SELECT `+usageColumns+` FROM usage_record
		  WHERE customer_id = $1 AND period = $2 AND metric = $3`,
		k.CustomerID, k.Period, string(k.Metric))
	rec, err := scanUsage(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return UsageRecord{}, fmt.Errorf("%w: %s/%s/%s", ErrUsageNotFound, k.CustomerID, k.Period, k.Metric)
	case err != nil:
		return UsageRecord{}, fmt.Errorf("metering: get usage %s/%s/%s: %w", k.CustomerID, k.Period, k.Metric, err)
	}
	return rec, nil
}

// Period returns every record for a customer's period, in metric order.
//
// Ordered by the same rank the in-memory store uses rather than alphabetically, so a page rendered from
// either store lists its meters identically — a UI that reorders depending on the backing is one nobody
// can screenshot for support.
func (p *PGUsageStore) Period(customerID, period string) ([]UsageRecord, error) {
	ctx, cancel := p.ctx()
	defer cancel()

	rows, err := p.db.QueryContext(ctx,
		`SELECT `+usageColumns+` FROM usage_record WHERE customer_id = $1 AND period = $2`,
		customerID, period)
	if err != nil {
		return nil, fmt.Errorf("metering: usage for %s/%s: %w", customerID, period, err)
	}
	defer func() { _ = rows.Close() }()

	var out []UsageRecord
	for rows.Next() {
		rec, err := scanUsage(rows)
		if err != nil {
			return nil, fmt.Errorf("metering: usage for %s/%s: %w", customerID, period, err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metering: usage for %s/%s: %w", customerID, period, err)
	}
	sort.Slice(out, func(i, j int) bool { return metricRank(out[i].Metric) < metricRank(out[j].Metric) })
	return out, nil
}

// MarkReported records the provider hand-off ref without touching the quantity.
//
// The SET clause names only the two hand-off columns. Reporting is not a re-measurement, and a
// statement that could move `quantity` is one that eventually does.
func (p *PGUsageStore) MarkReported(k Key, providerUsageRef string) (UsageRecord, error) {
	ctx, cancel := p.ctx()
	defer cancel()

	row := p.db.QueryRowContext(ctx,
		`UPDATE usage_record SET reported_to_provider = TRUE, provider_usage_ref = $4
		  WHERE customer_id = $1 AND period = $2 AND metric = $3
		 RETURNING `+usageColumns,
		k.CustomerID, k.Period, string(k.Metric), nullStr(providerUsageRef))
	rec, err := scanUsage(row)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return UsageRecord{}, fmt.Errorf("%w: %s/%s/%s", ErrUsageNotFound, k.CustomerID, k.Period, k.Metric)
	case err != nil:
		return UsageRecord{}, fmt.Errorf("metering: mark reported %s/%s/%s: %w", k.CustomerID, k.Period, k.Metric, err)
	}
	return rec, nil
}

type usageScanner interface{ Scan(...any) error }

func scanUsage(sc usageScanner) (UsageRecord, error) {
	var rec UsageRecord
	var metric string
	var providerRef sql.NullString
	if err := sc.Scan(&rec.CustomerID, &rec.Period, &metric, &rec.Quantity, &rec.SourceDigest,
		&rec.ReportedToProvider, &providerRef, &rec.UpdatedAt); err != nil {
		return UsageRecord{}, err
	}
	rec.Metric = Metric(metric)
	rec.ProviderUsageRef = providerRef.String
	return rec, nil
}

// nullStr sends NULL for an empty string. Load-bearing: `usage_record_reported_has_ref` reads
// `NOT reported_to_provider OR provider_usage_ref IS NOT NULL`, and "" satisfies IS NOT NULL — so an
// unreported row written with "" would pass a constraint that exists to catch exactly that state.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
