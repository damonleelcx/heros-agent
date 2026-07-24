// Package metering is the P7 value meter: **spend under management (SUM)** derived from the P2.5 cost
// events, the idempotent per-period usage records every charge is raised against, the verified billable
// savings gainshare may bill, and the usage↔provider reconciliation.
//
// Two rules shape the whole package.
//
// 1. **SUM is a DERIVATION, never a second pipeline** (design Decision 1). P2.5 already emits a `cost`
// event per provider call, fully tagged and idempotent under retries. Standing up a second usage
// collector would create two ledgers that disagree about what a customer used, and month-end would
// become a reconstruction. So the meter is a QUERY over the existing substrate: it collects nothing.
//
// 2. **Idempotency is a SCHEMA property, not application hope** (design Decision 2). Every meter is a
// `usage_record` keyed `{customer_id, period, metric}` written by an upsert. A duplicate is physically
// impossible, so no number of replays, re-derivations, or reconciliations can double-count a period.
package metering

import (
	"fmt"
	"time"
)

// Period is a billing period as a half-open interval [Start, End). Half-open is deliberate: back-to-back
// periods must partition time exactly, and a closed interval would attribute an event at midnight to
// both months — a double-count in the one place double-counting is most expensive.
type Period struct {
	// ID is the stable period key stored on every usage record (e.g. "2026-07"). It is part of the
	// primary key, so it must be derived, never typed by hand at a call site.
	ID    string    `json:"id"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// MonthPeriod returns the calendar-month period containing t, in UTC. UTC is not a default — a billing
// period whose boundary moves with a server's local zone would silently re-attribute usage when a host
// is re-deployed in another region.
func MonthPeriod(t time.Time) Period {
	u := t.UTC()
	start := time.Date(u.Year(), u.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	return Period{ID: start.Format("2006-01"), Start: start, End: end}
}

// ParsePeriod builds a month period from its ID ("2026-07"), so a period read back out of the database
// reconstructs to the same bounds the meter derived it with.
func ParsePeriod(id string) (Period, error) {
	t, err := time.ParseInLocation("2006-01", id, time.UTC)
	if err != nil {
		return Period{}, fmt.Errorf("metering: bad period id %q: %w", id, err)
	}
	return MonthPeriod(t), nil
}

// Contains reports whether ts falls in [Start, End).
func (p Period) Contains(ts time.Time) bool {
	u := ts.UTC()
	return !u.Before(p.Start) && u.Before(p.End)
}

// Closed reports whether the period has ended as of now. Re-deriving a CLOSED period must be
// deterministic (the metering spec's determinism scenario); an open period is still accruing and is
// expected to move.
func (p Period) Closed(now time.Time) bool { return !now.UTC().Before(p.End) }

// String is the period id, so a period interpolates into a log or an error as its key.
func (p Period) String() string { return p.ID }
