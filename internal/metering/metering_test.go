package metering

import (
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// The fixture period and a clock inside it.
var (
	julyStart = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	july      = MonthPeriod(julyStart)
	afterJuly = time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
)

// costEvent builds a REAL P2.5 cost event — the same shape telemetry.MetricSet emits, carrying the
// seven tags, the cost_usd metric name, and the `invocation_id` retry identity the gateway stamps.
// Building it through metricevent.Event (not a local struct) is what makes these tests evidence about
// the shipped substrate rather than about a mock.
func costEvent(runID, invocationID string, usd float64, ts time.Time) metricevent.Event {
	seed := int64(1)
	v := usd
	return metricevent.Event{
		SchemaVersion: metricevent.SchemaVersion,
		VariantID:     "var-1",
		RunID:         runID,
		NodeID:        "router",
		CaseID:        "case-1",
		Seed:          &seed,
		Timestamp:     ts.UTC().Format(time.RFC3339Nano),
		ConfigHash:    "3a7b9c1d2e4f60718293a4b5c6d7e8f90112233445566778899aabbccddeeff0",
		MetricName:    telemetry.MetricCostUSD,
		Value:         &v,
		Unit:          telemetry.UnitUSD,
		Dimensions: map[string]any{
			telemetry.AttrInvocationID: invocationID,
			telemetry.AttrPriceBookVer: "pb-2026-07",
		},
	}
}

// substrateWithSpend seeds a customer's cost events for July: three priced calls owned by cus_acme and
// one owned by somebody else (attribution must exclude it).
func substrateWithSpend(t *testing.T) *MemCostEvents {
	t.Helper()
	src := NewMemCostEvents()
	src.Attribute("run-a", "cus_acme")
	src.Attribute("run-b", "cus_acme")
	src.Attribute("run-z", "cus_other")

	src.Put(costEvent("run-a", "run-a|router|1", 0.25, julyStart.Add(1*time.Hour)))
	src.Put(costEvent("run-a", "run-a|router|2", 0.50, julyStart.Add(2*time.Hour)))
	src.Put(costEvent("run-b", "run-b|router|1", 1.25, julyStart.Add(72*time.Hour)))
	src.Put(costEvent("run-z", "run-z|router|1", 99.00, julyStart.Add(3*time.Hour)))
	// An event in the NEXT period — must not leak into July's figure.
	src.Put(costEvent("run-a", "run-a|router|3", 7.00, afterJuly))
	return src
}

const wantJulySUM = 0.25 + 0.50 + 1.25

// TestDeriveSUMAggregatesTheP25CostEvents is task 2.1 / the metering spec's first scenario: SUM is the
// aggregate of exactly the customer's cost events in the period — no second collector, and no other
// customer's spend.
func TestDeriveSUMAggregatesTheP25CostEvents(t *testing.T) {
	src := substrateWithSpend(t)

	res, err := DeriveSUM(src, "cus_acme", july)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if res.Quantity != wantJulySUM {
		t.Errorf("SUM = %v, want %v", res.Quantity, wantJulySUM)
	}
	if res.EventCount != 3 {
		t.Errorf("event count = %d, want 3 (the other customer's and the next period's are excluded)", res.EventCount)
	}
	if res.Unit != telemetry.UnitUSD {
		t.Errorf("unit = %q, want %q", res.Unit, telemetry.UnitUSD)
	}

	other, err := DeriveSUM(src, "cus_other", july)
	if err != nil {
		t.Fatalf("derive other: %v", err)
	}
	if other.Quantity != 99.00 {
		t.Errorf("the other customer's SUM = %v, want 99 — attribution is leaking", other.Quantity)
	}
}

// TestReDerivingAClosedPeriodIsDeterministic is task 2.1's determinism requirement, tested the way it
// can actually fail: the events are re-delivered in RANDOM order. Float addition is not associative, so
// an implementation that sums in arrival order would return a different last-bit figure here.
func TestReDerivingAClosedPeriodIsDeterministic(t *testing.T) {
	src := substrateWithSpend(t)
	if !july.Closed(afterJuly) {
		t.Fatal("precondition: July must be closed as of August")
	}

	first, err := DeriveSUM(src, "cus_acme", july)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 25; i++ {
		shuffled := &shuffleSource{inner: src, rng: rng}
		got, err := DeriveSUM(shuffled, "cus_acme", july)
		if err != nil {
			t.Fatalf("re-derive %d: %v", i, err)
		}
		if got.Quantity != first.Quantity {
			t.Fatalf("re-derivation %d returned %v, want the identical %v — the closed period is not deterministic",
				i, got.Quantity, first.Quantity)
		}
		if got.SourceDigest != first.SourceDigest {
			t.Fatalf("re-derivation %d produced digest %s, want %s", i, got.SourceDigest, first.SourceDigest)
		}
	}
}

// shuffleSource re-orders what the substrate returns, so delivery order is under test.
type shuffleSource struct {
	inner *MemCostEvents
	rng   *rand.Rand
}

func (s *shuffleSource) CostEvents(customerID string, p Period) ([]metricevent.Event, error) {
	evs, err := s.inner.CostEvents(customerID, p)
	if err != nil {
		return nil, err
	}
	s.rng.Shuffle(len(evs), func(i, j int) { evs[i], evs[j] = evs[j], evs[i] })
	return evs, nil
}
func (s *shuffleSource) Describe() string { return "shuffled" }

// TestRedeliveredCostEventDoesNotDoubleCountSUM is task 2.3 / the metering spec's redelivery scenario.
// The SAME event (same invocation identity) is delivered five times; SUM must not move.
func TestRedeliveredCostEventDoesNotDoubleCountSUM(t *testing.T) {
	src := substrateWithSpend(t)
	before, err := DeriveSUM(src, "cus_acme", july)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	for i := 0; i < 5; i++ {
		src.Put(costEvent("run-a", "run-a|router|1", 0.25, julyStart.Add(1*time.Hour))) // redelivery
	}

	after, err := DeriveSUM(src, "cus_acme", july)
	if err != nil {
		t.Fatalf("re-derive: %v", err)
	}
	if after.Quantity != before.Quantity {
		t.Errorf("SUM moved on redelivery: %v -> %v", before.Quantity, after.Quantity)
	}
	if after.EventCount != before.EventCount {
		t.Errorf("event count moved on redelivery: %d -> %d", before.EventCount, after.EventCount)
	}
	if after.Duplicates != 5 {
		t.Errorf("duplicates = %d, want 5 — redeliveries must be recognized and surfaced, not silently absorbed", after.Duplicates)
	}
	if after.SourceDigest != before.SourceDigest {
		t.Error("the source digest moved on redelivery — a re-record would look like a real change")
	}
}

// TestReplayedPeriodIsCountedExactlyOnce is task 2.4, the load-bearing never-double-count test.
//
// It replays the whole metering path N times — re-derive AND re-record — and asserts exactly ONE
// `{customer, period, metric}` row exists with the correct quantity. It also asserts the store actually
// SAW N writes, because "one row" is only evidence of idempotency if the code genuinely tried to write
// more than once.
func TestReplayedPeriodIsCountedExactlyOnce(t *testing.T) {
	src := substrateWithSpend(t)
	store := NewMemUsageStore()
	m := NewMeter(src, store)
	m.SetClock(func() time.Time { return afterJuly })

	const replays = 7
	for i := 0; i < replays; i++ {
		if _, _, err := m.RecordSUM("cus_acme", july); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		// Interleave a redelivery of the substrate's events, so the replay exercises both halves of the
		// idempotency story (event redelivery AND record re-reporting).
		src.Put(costEvent("run-b", "run-b|router|1", 1.25, julyStart.Add(72*time.Hour)))
	}

	if store.Writes() < replays {
		t.Fatalf("the store saw %d writes for %d replays — the test did not actually retry, so 'one row' proves nothing",
			store.Writes(), replays)
	}
	if store.Rows() != 1 {
		t.Fatalf("rows = %d after %d replays, want exactly 1", store.Rows(), replays)
	}

	// Downstream READ path, not the setter's return value.
	rec, err := store.Get(Key{"cus_acme", july.ID, MetricSUM})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.Quantity != wantJulySUM {
		t.Errorf("quantity = %v after %d replays, want %v (the period once, not multiplied)", rec.Quantity, replays, wantJulySUM)
	}
}

// TestIdenticalRederivationIsANoOp: an unchanged re-derivation must not churn the row. A reconciler
// running on a schedule would otherwise rewrite updated_at forever and destroy the one signal that
// answers "when did this measurement last actually change".
func TestIdenticalRederivationIsANoOp(t *testing.T) {
	src := substrateWithSpend(t)
	store := NewMemUsageStore()
	m := NewMeter(src, store)
	m.SetClock(func() time.Time { return afterJuly })

	first, _, err := m.RecordSUM("cus_acme", july)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	// Mark it reported, then re-derive: the identical re-derivation must not un-report it.
	if _, err := store.MarkReported(first.Key(), "prov_usage_1"); err != nil {
		t.Fatalf("mark reported: %v", err)
	}

	res, err := m.DeriveSUM("cus_acme", july)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	_, changed, err := m.RecordUsage("cus_acme", july, MetricSUM, res.Quantity, res.SourceDigest)
	if err != nil {
		t.Fatalf("re-record: %v", err)
	}
	if changed {
		t.Error("an identical re-derivation reported a change")
	}
	again, _ := store.Get(first.Key())
	if !again.ReportedToProvider || again.ProviderUsageRef != "prov_usage_1" {
		t.Errorf("the no-op re-record cleared the provider hand-off: %+v", again)
	}
}

// TestChangedQuantityUnreportsTheRecord: when the measurement genuinely moves, the previous provider
// hand-off no longer describes it, so the record must go back to un-reported. Leaving the old ref
// attached would make the reconciler believe a stale quantity was already sent.
func TestChangedQuantityUnreportsTheRecord(t *testing.T) {
	src := substrateWithSpend(t)
	store := NewMemUsageStore()
	m := NewMeter(src, store)
	m.SetClock(func() time.Time { return afterJuly })

	rec, _, err := m.RecordSUM("cus_acme", july)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := store.MarkReported(rec.Key(), "prov_usage_1"); err != nil {
		t.Fatalf("mark: %v", err)
	}

	// A genuinely NEW call lands in the period (not a redelivery — a different invocation identity).
	src.Put(costEvent("run-b", "run-b|router|2", 2.00, julyStart.Add(100*time.Hour)))
	updated, _, err := m.RecordSUM("cus_acme", july)
	if err != nil {
		t.Fatalf("re-record: %v", err)
	}
	if updated.Quantity != wantJulySUM+2.00 {
		t.Errorf("quantity = %v, want %v", updated.Quantity, wantJulySUM+2.00)
	}
	if updated.ReportedToProvider || updated.ProviderUsageRef != "" {
		t.Errorf("a changed quantity kept a stale provider hand-off: %+v", updated)
	}
	if store.Rows() != 1 {
		t.Errorf("rows = %d, want 1 — a changed quantity must update in place, never append", store.Rows())
	}
}

// TestAllFourMetersAreRecordedPerPeriod is the metering spec's "all four meters" scenario: SUM, seats,
// retention and eval compute each get one keyed record.
func TestAllFourMetersAreRecordedPerPeriod(t *testing.T) {
	src := substrateWithSpend(t)
	store := NewMemUsageStore()
	m := NewMeter(src, store)
	m.SetClock(func() time.Time { return afterJuly })

	if _, _, err := m.RecordSUM("cus_acme", july); err != nil {
		t.Fatalf("sum: %v", err)
	}
	for _, tc := range []struct {
		metric Metric
		qty    float64
	}{
		{MetricSeats, 4},
		{MetricRetention, 30},
		{MetricEvalCompute, 128},
	} {
		if _, _, err := m.RecordUsage("cus_acme", july, tc.metric, tc.qty, "digest-"+string(tc.metric)); err != nil {
			t.Fatalf("record %s: %v", tc.metric, err)
		}
	}

	recs, err := store.Period("cus_acme", july.ID)
	if err != nil {
		t.Fatalf("period: %v", err)
	}
	if len(recs) != len(Metrics) {
		t.Fatalf("got %d records, want one per meter (%d)", len(recs), len(Metrics))
	}
	for i, want := range Metrics {
		if recs[i].Metric != want {
			t.Errorf("record %d is %q, want %q (records come back in meter order)", i, recs[i].Metric, want)
		}
		if recs[i].Period != july.ID {
			t.Errorf("record %d has period %q, want %q", i, recs[i].Period, july.ID)
		}
	}
}

// TestUsageStoreRefusesUnsoundRecords: the meter fails LOUD rather than storing a record nothing can
// explain. Each of these would otherwise become an unexplainable line on an invoice.
func TestUsageStoreRefusesUnsoundRecords(t *testing.T) {
	store := NewMemUsageStore()
	base := UsageRecord{CustomerID: "c", Period: "2026-07", Metric: MetricSUM, Quantity: 1, SourceDigest: "d"}

	mutate := map[string]func(UsageRecord) UsageRecord{
		"unknown metric": func(r UsageRecord) UsageRecord { r.Metric = "bandwidth"; return r },
		"no customer":    func(r UsageRecord) UsageRecord { r.CustomerID = ""; return r },
		"no period":      func(r UsageRecord) UsageRecord { r.Period = ""; return r },
		"negative usage": func(r UsageRecord) UsageRecord { r.Quantity = -1; return r },
		"no source digest": func(r UsageRecord) UsageRecord {
			r.SourceDigest = ""
			return r
		},
	}
	for name, mut := range mutate {
		if _, _, err := store.Upsert(mut(base)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if store.Rows() != 0 {
		t.Errorf("a rejected record was stored anyway (rows=%d)", store.Rows())
	}
}

// TestDeriveSUMRefusesUnsoundEvents: the derivation is the last place a bad event can be caught before
// it becomes money. A nil-valued (unpriced) call must NOT be summed as zero — P2.5 surfaces it as a gap
// on purpose, and silently treating it as free would understate spend exactly where a new model is in
// play.
func TestDeriveSUMRefusesUnsoundEvents(t *testing.T) {
	t.Run("nil value", func(t *testing.T) {
		src := NewMemCostEvents()
		src.Attribute("run-a", "c")
		ev := costEvent("run-a", "i1", 1, julyStart.Add(time.Hour))
		ev.Value = nil
		src.Put(ev)
		if _, err := DeriveSUM(src, "c", july); err == nil {
			t.Error("an unpriced cost event was summed instead of surfaced")
		}
	})
	t.Run("wrong metric", func(t *testing.T) {
		bad := &wrongMetricSource{}
		if _, err := DeriveSUM(bad, "c", july); err == nil {
			t.Error("a non-cost event was aggregated into SUM")
		}
	})
	t.Run("event outside the period", func(t *testing.T) {
		bad := &outOfPeriodSource{}
		if _, err := DeriveSUM(bad, "c", july); err == nil {
			t.Error("an out-of-period event was aggregated into the period's SUM")
		}
	})
}

type wrongMetricSource struct{}

func (wrongMetricSource) CostEvents(string, Period) ([]metricevent.Event, error) {
	ev := costEvent("run-a", "i1", 1, julyStart.Add(time.Hour))
	ev.MetricName = telemetry.MetricLatencyTotalMS
	return []metricevent.Event{ev}, nil
}
func (wrongMetricSource) Describe() string { return "bad" }

type outOfPeriodSource struct{}

func (outOfPeriodSource) CostEvents(string, Period) ([]metricevent.Event, error) {
	return []metricevent.Event{costEvent("run-a", "i1", 1, afterJuly)}, nil
}
func (outOfPeriodSource) Describe() string { return "bad" }

// TestPeriodBoundariesPartitionTime: [Start, End) means an event at exactly midnight on the 1st belongs
// to the new period and to no other. A closed interval here would double-count that event across two
// invoices.
func TestPeriodBoundariesPartitionTime(t *testing.T) {
	aug := MonthPeriod(afterJuly)
	if july.ID != "2026-07" || aug.ID != "2026-08" {
		t.Fatalf("period ids: %q, %q", july.ID, aug.ID)
	}
	boundary := aug.Start
	if july.Contains(boundary) {
		t.Error("the boundary instant belongs to July as well as August — periods overlap")
	}
	if !aug.Contains(boundary) {
		t.Error("the boundary instant belongs to no period — periods have a gap")
	}
	if !july.Contains(july.Start) {
		t.Error("a period must contain its own start")
	}

	got, err := ParsePeriod("2026-07")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.Start.Equal(july.Start) || !got.End.Equal(july.End) {
		t.Errorf("a period read back from its id has different bounds: %v..%v vs %v..%v", got.Start, got.End, july.Start, july.End)
	}
	if _, err := ParsePeriod("nonsense"); err == nil {
		t.Error("a malformed period id was accepted")
	}
}

// TestUsageStoreFailsClosedWhenDown: an unavailable meter must return an error, never a zero quantity.
// A silent zero here bills nothing and looks exactly like a customer who used nothing.
func TestUsageStoreFailsClosedWhenDown(t *testing.T) {
	store := NewMemUsageStore()
	m := NewMeter(substrateWithSpend(t), store)
	m.SetClock(func() time.Time { return afterJuly })
	if _, _, err := m.RecordSUM("cus_acme", july); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store.SetDown(true)

	if _, _, err := m.RecordSUM("cus_acme", july); !errors.Is(err, ErrStoreUnavail) {
		t.Errorf("want ErrStoreUnavail on record, got %v", err)
	}
	if _, err := store.Get(Key{"cus_acme", july.ID, MetricSUM}); !errors.Is(err, ErrStoreUnavail) {
		t.Errorf("want ErrStoreUnavail on get, got %v", err)
	}
	if _, err := store.Period("cus_acme", july.ID); !errors.Is(err, ErrStoreUnavail) {
		t.Errorf("want ErrStoreUnavail on period, got %v", err)
	}
}
