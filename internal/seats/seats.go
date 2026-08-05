// Package seats answers the two different questions the word "seats" was being used for.
//
// # The category error this package exists to undo
//
// `plancfg.LimitSeats` and `metering.MetricSeats` have both existed since P7. `entitlement` gates the
// dashboard on the pair, and the plan fixtures price 1 / 5 / 25 / 500 seats. **No code path anywhere
// wrote a `seats` usage record**, so the gate compared an allowance against zero, forever, and passed. A
// plan that sold five seats admitted five hundred.
//
// The root cause is not a missing writer. It is that a SEAT COUNT IS A STATE and it was modelled as a
// FLOW. A flow is accumulated by whoever produces it; a state is read from wherever it lives. Nobody
// wrote the record because there was nothing to accumulate — membership already held the answer — and a
// state nobody writes reads as zero, which passes every allowance check silently.
//
// So there are two functions here and they answer two questions:
//
//   - CURRENT — how many seats does this organization hold *right now*? Read directly from membership.
//     This is what gates the next invitation, and it must never be served from a usage record.
//   - PEAK — what is the highest number it held during a billing period? Derived by replaying the
//     membership timeline. This is what an invoice line may cite.
//
// # Why the peak is DERIVED rather than accumulated
//
// The obvious implementation of "record the peak" is to upsert the current count on every change and
// keep the larger of the two. That has a race (two concurrent changes both read the old maximum) and,
// worse, it makes the number un-recomputable: if a write is lost, nothing can tell.
//
// `Peak` is instead a pure function of `joined_at` / `removed_at` — rows that already exist and that
// removal never deletes, because removal is a state change. Re-running it over the same memberships
// produces the same number, which is what makes it the **idempotent reconciliation point** the design
// requires: the membership change is the event, and this derivation is the read that reconciles it. It
// is the same shape `metering.RecordSUM` already has, and it is safe for exactly the same reason.
//
// # 🔴 What an invoice may cite, and why it is the peak
//
// The closing count would let an organization hold five seats for three weeks and remove two on the last
// day to be billed for three. The peak is what they actually had.
package seats

import (
	"fmt"
	"sort"
	"time"

	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

// Reader is the slice of the identity store this package needs. Read-only, and narrow, so nothing here
// can grow the ability to change a membership while counting it.
type Reader interface {
	ListMembers(tenantID string) ([]tenancy.Membership, error)
}

// Occupies reports whether a membership occupies a seat *right now*.
//
// 🔴 Every role occupies one today, and that is a decision this function makes ON PURPOSE rather than by
// omission. The open question — whether somebody who holds only a personal API credential and never
// opens the console occupies a seat — is unresolved (PRD Open Question 3), and the two candidate answers
// point in opposite commercial directions. Until it is ratified, the enforcement mechanism counts every
// active member, and **no surface may state what a seat includes**. Putting the definition in one named
// function is what makes ratifying it a one-line change rather than a search.
func Occupies(m tenancy.Membership) bool { return m.Active() }

// Current is the number of seats an organization holds now.
//
// 🔴 It reads MEMBERSHIP. It does not consult the usage store, and there is no parameter through which a
// usage store could be passed — which is the structural version of the rule, and the reason
// `TestCurrentNeverConsultsTheUsageStore` can be written at all.
func Current(r Reader, tenantID string) (int, error) {
	members, err := r.ListMembers(tenantID)
	if err != nil {
		return 0, fmt.Errorf("seats: reading the member list: %w", err)
	}
	n := 0
	for _, m := range members {
		if Occupies(m) {
			n++
		}
	}
	return n, nil
}

// Peak is the highest seat count an organization held during [from, to).
//
// It replays the timeline: every membership contributes a +1 at `joined_at` and a −1 at `removed_at`,
// and the answer is the maximum running total inside the window. A membership that began before the
// window and is still active counts from the window's start.
//
// Pure: same memberships in, same number out. That is what makes it re-runnable at any time, and what
// makes a lost write detectable rather than silent — there is no accumulator to lose.
func Peak(r Reader, tenantID string, from, to time.Time) (int, error) {
	members, err := r.ListMembers(tenantID)
	if err != nil {
		return 0, fmt.Errorf("seats: reading the member list: %w", err)
	}
	return PeakOf(members, from, to), nil
}

// PeakOf is Peak's pure half, exposed so a test can drive a timeline without a store.
func PeakOf(members []tenancy.Membership, from, to time.Time) int {
	type change struct {
		at    time.Time
		delta int
	}
	changes := make([]change, 0, len(members)*2)
	running := 0

	for _, m := range members {
		joined := m.JoinedAt.UTC()
		// A membership removed BEFORE the window never contributed to it.
		if m.RemovedAt != nil && !m.RemovedAt.UTC().After(from) {
			continue
		}
		// A membership that began AFTER the window never contributed either.
		if !joined.Before(to) {
			continue
		}
		if joined.Before(from) {
			// Already in place when the window opened. It is part of the starting count rather than an
			// event inside it — otherwise a member who joined last year would be invisible to a
			// window that only sees this month's changes.
			running++
		} else {
			changes = append(changes, change{at: joined, delta: +1})
		}
		if m.RemovedAt != nil && m.RemovedAt.UTC().Before(to) {
			changes = append(changes, change{at: m.RemovedAt.UTC(), delta: -1})
		}
	}

	// Departures before arrivals at the same instant, so a swap — one person out, one in, same second —
	// does not read as a moment of N+1 seats and inflate the bill by one.
	//
	// ⚠️ The same ordering means a membership whose join and removal share a timestamp contributes
	// NOTHING to the peak. That is correct — it held a seat for zero duration — and it is worth knowing
	// because it is easy to produce by accident: a fixture that stamps both from one frozen clock, or a
	// test that removes somebody in the same instant it added them, will see a peak that does not
	// include them. Real usage cannot do this; the two timestamps come from two different requests.
	sort.Slice(changes, func(i, j int) bool {
		if !changes[i].at.Equal(changes[j].at) {
			return changes[i].at.Before(changes[j].at)
		}
		return changes[i].delta < changes[j].delta
	})

	peak := running
	for _, c := range changes {
		running += c.delta
		if running > peak {
			peak = running
		}
	}
	if peak < 0 {
		// Unreachable through this package's own writes; a negative peak would mean a removal with no
		// matching join, which is a data fault rather than a seat count. Zero is the honest floor.
		return 0
	}
	return peak
}

// Recorder writes the period's seat quantity. It is `*metering.Meter`'s shape, narrowed.
type Recorder interface {
	RecordUsage(customerID string, p metering.Period, metric metering.Metric, quantity float64, sourceDigest string) (metering.UsageRecord, bool, error)
}

// Observe re-derives the period's seat peak and records it.
//
// Called after EVERY membership change — an activation, a role change, a removal. It is not an
// increment: it recomputes from the timeline and upserts the result, so calling it twice for one change,
// or missing a call and catching up on the next one, both produce the correct number. `metering.Meter`'s
// own `RecordSUM` works this way for the same reason, and says so in the same words.
//
// A nil recorder is a deployment with no metering, and the call is a no-op rather than an error: seats
// still gate invitations (that is `Current`, which needs no meter), and refusing a membership change
// because a meter is absent would take an identity operation down over a billing dependency.
func Observe(r Reader, rec Recorder, tenantID string, p metering.Period) (int, error) {
	if rec == nil {
		return 0, nil
	}
	peak, err := Peak(r, tenantID, p.Start, p.End)
	if err != nil {
		return 0, err
	}
	// The digest names WHAT the number was derived from, so a later reader can tell a re-derivation from
	// a different period apart from a re-derivation of the same one.
	digest := fmt.Sprintf("seats-peak:%s:%s", tenantID, p.ID)
	if _, _, err := rec.RecordUsage(tenantID, p, metering.MetricSeats, float64(peak), digest); err != nil {
		return peak, fmt.Errorf("seats: recording the period peak: %w", err)
	}
	return peak, nil
}
