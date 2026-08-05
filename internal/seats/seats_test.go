package seats

import (
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/metering"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

var (
	julyStart = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	july      = metering.MonthPeriod(julyStart)
)

func member(user string, joined time.Time, removed *time.Time) tenancy.Membership {
	m := tenancy.Membership{
		UserID: user, TenantID: "acme", Role: tenancy.RoleMember,
		Status: tenancy.MemberActive, JoinedAt: joined,
	}
	if removed != nil {
		m.Status, m.RemovedAt = tenancy.MemberRemoved, removed
	}
	return m
}

func at(day int) time.Time { return julyStart.AddDate(0, 0, day-1) }

func ptr(t time.Time) *time.Time { return &t }

type memberList []tenancy.Membership

func (m memberList) ListMembers(string) ([]tenancy.Membership, error) { return m, nil }

// TestCurrentCountsActiveMembershipsOnly.
func TestCurrentCountsActiveMembershipsOnly(t *testing.T) {
	list := memberList{
		member("a", at(1), nil),
		member("b", at(2), nil),
		member("c", at(3), ptr(at(10))), // removed
	}
	n, err := Current(list, "acme")
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if n != 2 {
		t.Fatalf("current seats = %d, want 2 (a removed membership is a row, not a seat)", n)
	}
}

// TestThePeakIsWhatTheOrganizationHeldNotWhatItClosedWith.
//
// 🔴 The scenario the design names: five seats for three weeks, two removed on the last day. The closing
// count is three; the peak is five, and five is what they had.
func TestThePeakIsWhatTheOrganizationHeldNotWhatItClosedWith(t *testing.T) {
	list := memberList{
		member("a", at(1), nil),
		member("b", at(1), nil),
		member("c", at(1), nil),
		member("d", at(2), ptr(at(31))),
		member("e", at(2), ptr(at(31))),
	}
	peak, err := Peak(list, "acme", july.Start, july.End)
	if err != nil {
		t.Fatalf("peak: %v", err)
	}
	if peak != 5 {
		t.Fatalf("peak = %d, want 5 — the closing count is 3, and billing the closing count lets an "+
			"organization hold five seats for three weeks and pay for three", peak)
	}

	current, err := Current(list, "acme")
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if current != 3 {
		t.Fatalf("current = %d, want 3", current)
	}
	if current == peak {
		t.Fatal("the two quantities came out equal in a case designed to separate them — the test is " +
			"not exercising the difference it exists for")
	}
}

// TestAMembershipThatPredatesTheWindowCountsFromItsStart. A member who joined last year is invisible to
// a window that only sees this month's events, and would make the peak too low by one per such member.
func TestAMembershipThatPredatesTheWindowCountsFromItsStart(t *testing.T) {
	lastYear := julyStart.AddDate(-1, 0, 0)
	list := memberList{
		member("old-a", lastYear, nil),
		member("old-b", lastYear, nil),
		member("new", at(15), nil),
	}
	peak := PeakOf(list, july.Start, july.End)
	if peak != 3 {
		t.Fatalf("peak = %d, want 3 — a membership already in place when the window opened is part of "+
			"the starting count, not an event inside it", peak)
	}
}

// TestAMembershipEntirelyOutsideTheWindowDoesNotCount.
func TestAMembershipEntirelyOutsideTheWindowDoesNotCount(t *testing.T) {
	before := memberList{member("gone", julyStart.AddDate(0, -2, 0), ptr(julyStart.AddDate(0, -1, 0)))}
	if got := PeakOf(before, july.Start, july.End); got != 0 {
		t.Errorf("a membership that ended before the window counted: %d", got)
	}
	after := memberList{member("future", july.End.AddDate(0, 0, 1), nil)}
	if got := PeakOf(after, july.Start, july.End); got != 0 {
		t.Errorf("a membership that began after the window counted: %d", got)
	}
}

// TestASameInstantSwapDoesNotInflateThePeak.
//
// One person out and one in at the same moment is a replacement, not a moment of N+1 seats. Ordering
// departures before arrivals is what keeps a swap from adding a seat to the bill.
func TestASameInstantSwapDoesNotInflateThePeak(t *testing.T) {
	moment := at(10)
	list := memberList{
		member("stays", at(1), nil),
		member("leaves", at(1), ptr(moment)),
		member("arrives", moment, nil),
	}
	if got := PeakOf(list, july.Start, july.End); got != 2 {
		t.Fatalf("peak = %d, want 2 — a same-instant replacement is not a third seat", got)
	}
}

// TestThePeakIsIdempotent is the property that makes it the reconciliation point: same memberships in,
// same number out, however many times it runs.
func TestThePeakIsIdempotent(t *testing.T) {
	list := memberList{
		member("a", at(1), nil),
		member("b", at(5), ptr(at(20))),
		member("c", at(6), nil),
	}
	first := PeakOf(list, july.Start, july.End)
	for i := 0; i < 10; i++ {
		if got := PeakOf(list, july.Start, july.End); got != first {
			t.Fatalf("run %d produced %d, first produced %d — a derivation that is not idempotent cannot "+
				"be a reconciliation point", i, got, first)
		}
	}
}

// TestObserveRecordsThePeakAndReRecordingIsANoOp.
func TestObserveRecordsThePeakAndReRecordingIsANoOp(t *testing.T) {
	usage := metering.NewMemUsageStore()
	meter := metering.NewMeter(metering.NewMemCostEvents(), usage)
	meter.SetClock(func() time.Time { return at(20) })

	list := memberList{
		member("a", at(1), nil),
		member("b", at(2), ptr(at(15))),
		member("c", at(3), nil),
	}

	peak, err := Observe(list, meter, "acme", july)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if peak != 3 {
		t.Fatalf("peak = %d, want 3", peak)
	}
	rec, err := usage.Get(metering.Key{CustomerID: "acme", Period: july.ID, Metric: metering.MetricSeats})
	if err != nil {
		t.Fatalf("the seat observation was not recorded: %v", err)
	}
	if rec.Quantity != 3 {
		t.Fatalf("recorded quantity = %v, want 3", rec.Quantity)
	}

	// Re-observing must not change the number. That is what lets it be called after every membership
	// change, and what makes a missed call recoverable by the next one.
	for i := 0; i < 5; i++ {
		if _, err := Observe(list, meter, "acme", july); err != nil {
			t.Fatalf("re-observe %d: %v", i, err)
		}
	}
	rec, err = usage.Get(metering.Key{CustomerID: "acme", Period: july.ID, Metric: metering.MetricSeats})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.Quantity != 3 {
		t.Fatalf("re-observing changed the quantity to %v", rec.Quantity)
	}
}

// TestObserveWithNoMeterIsANoOpRatherThanAnError.
//
// Refusing a membership change because a meter is absent would take an identity operation down over a
// billing dependency — and seats still gate invitations, because that is `Current`, which needs no meter.
func TestObserveWithNoMeterIsANoOpRatherThanAnError(t *testing.T) {
	if _, err := Observe(memberList{member("a", at(1), nil)}, nil, "acme", july); err != nil {
		t.Fatalf("observe with no recorder: %v", err)
	}
}

// TestObserveAfterARemovalReducesTheCurrentCountAndNotThePeak — the two quantities moving differently in
// one call is the whole reason they have two names.
func TestObserveAfterARemovalReducesTheCurrentCountAndNotThePeak(t *testing.T) {
	usage := metering.NewMemUsageStore()
	meter := metering.NewMeter(metering.NewMemCostEvents(), usage)
	meter.SetClock(func() time.Time { return at(28) })

	full := memberList{member("a", at(1), nil), member("b", at(1), nil), member("c", at(1), nil)}
	if _, err := Observe(full, meter, "acme", july); err != nil {
		t.Fatalf("observe: %v", err)
	}

	reduced := memberList{
		member("a", at(1), nil),
		member("b", at(1), ptr(at(28))),
		member("c", at(1), ptr(at(28))),
	}
	peak, err := Observe(reduced, meter, "acme", july)
	if err != nil {
		t.Fatalf("observe after removal: %v", err)
	}
	if peak != 3 {
		t.Fatalf("the recorded peak fell to %d after two removals — removing people on the last day "+
			"must not retroactively unbuy the month", peak)
	}
	current, err := Current(reduced, "acme")
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if current != 1 {
		t.Fatalf("current = %d, want 1 — the seat that gates the next invitation IS freed immediately", current)
	}
}
