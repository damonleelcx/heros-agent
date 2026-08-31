package deepseek

import (
	"testing"
	"time"

	"github.com/heros-foreal/heros/internal/provider"
)

// TestPeakAndOffPeakAreBothSelected.
//
// 🔴 DeepSeek charges DOUBLE in peak windows. A static price table is right about half the time and
// understates the rest — and understating is the direction that lets a run sail past a money ceiling
// the customer agreed to while the ledger still says there is room.
func TestPeakAndOffPeakAreBothSelected(t *testing.T) {
	c := New("unused")
	// Monday 02:00 UTC is inside the first peak window.
	peakAt := time.Date(2026, 8, 31, 2, 0, 0, 0, time.UTC)
	// Monday 14:00 UTC is outside both.
	offAt := time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC)

	peak, ok := c.PriceFor(ModelFlash, peakAt)
	if !ok {
		t.Fatal("no price for flash")
	}
	off, ok := c.PriceFor(ModelFlash, offAt)
	if !ok {
		t.Fatal("no off-peak price for flash")
	}
	if peak.InputCentsPerMTok <= off.InputCentsPerMTok {
		t.Fatalf("peak input %.2f is not above off-peak %.2f",
			peak.InputCentsPerMTok, off.InputCentsPerMTok)
	}
	// The published rule is that off-peak is exactly half of peak, on every line.
	for _, pair := range []struct {
		name      string
		peak, off float64
	}{
		{"input", peak.InputCentsPerMTok, off.InputCentsPerMTok},
		{"cached", peak.CachedInputCentsPerMTok, off.CachedInputCentsPerMTok},
		{"output", peak.OutputCentsPerMTok, off.OutputCentsPerMTok},
	} {
		if pair.peak != pair.off*2 {
			t.Errorf("%s: peak %.3f is not twice off-peak %.3f", pair.name, pair.peak, pair.off)
		}
	}
}

// TestWeekendsAreNeverPeak — the published windows are Monday to Friday.
func TestWeekendsAreNeverPeak(t *testing.T) {
	// 2026-09-05 is a Saturday, 02:00 UTC — inside the weekday peak hour, on a weekend.
	sat := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)
	if isPeak(sat) {
		t.Error("Saturday 02:00 UTC was priced as peak; the published windows are Monday to Friday")
	}
	mon := time.Date(2026, 8, 31, 2, 0, 0, 0, time.UTC)
	if !isPeak(mon) {
		t.Error("Monday 02:00 UTC was not priced as peak")
	}
	// The gap between the two windows: 04:00-06:00 is off-peak.
	if isPeak(time.Date(2026, 8, 31, 5, 0, 0, 0, time.UTC)) {
		t.Error("Monday 05:00 UTC is between the two peak windows and must be off-peak")
	}
	if !isPeak(time.Date(2026, 8, 31, 9, 59, 0, 0, time.UTC)) {
		t.Error("Monday 09:59 UTC is inside the second peak window")
	}
	if isPeak(time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)) {
		t.Error("Monday 10:00 UTC is the end of the window and must be off-peak")
	}
}

// TestAnUnknownModelHasNoPrice. A call with no price would report zero spend against a money ceiling,
// which is the one direction of error nobody investigates.
func TestAnUnknownModelHasNoPrice(t *testing.T) {
	c := New("unused")
	if _, ok := c.PriceFor("deepseek-chat", time.Now()); ok {
		t.Error("the retired `deepseek-chat` name resolved to a price; it no longer exists")
	}
	if _, ok := c.PriceFor("gpt-9", time.Now()); ok {
		t.Error("an unknown model resolved to a price")
	}
}

// TestSubCentCallsAreNotRoundedTo AWholeCent is the regression fence for a bug a REAL call found.
//
// A 300-token deepseek-v4-flash completion costs 0.0127 cents. Rounding up to the cent — the correct
// instinct, since under-reporting is the dangerous direction — overstated it by 79x, and a nine-axis
// assessment billed 9 cents instead of 0.11. A ceiling denominated in a unit coarser than the thing it
// measures is not conservative; it trips on runs that spent almost nothing.
func TestSubCentCallsAreNotRoundedToAWholeCent(t *testing.T) {
	c := New("unused")
	off := time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC)
	price, _ := c.PriceFor(ModelFlash, off)

	// The exact usage from the first real call made against this client.
	real := provider.Usage{InputTokens: 162, OutputTokens: 138}
	got := price.CostMicroCents(real)

	// 162/1e6*22 + 138/1e6*66 = 0.003564 + 0.009108 = 0.012672 cents = 12672 micro-cents.
	if got < 12_000 || got > 13_000 {
		t.Fatalf("300 real tokens cost %d micro-cents; expected about 12,672 (0.0127 cents)", got)
	}
	if got >= provider.MicroCentsPerCent {
		t.Fatalf("a 300-token call was priced at %d micro-cents, which is a whole cent or more — "+
			"this is the 79x overstatement the micro-cent unit exists to prevent", got)
	}
}

// TestRoundingIsUpwardsAtMicroCentResolution. Under-reporting remains the dangerous direction; the fix
// was the unit, not the direction.
func TestRoundingIsUpwardsAtMicroCentResolution(t *testing.T) {
	c := New("unused")
	price, _ := c.PriceFor(ModelFlash, time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC))
	// One output token: 1/1e6 * 66 cents = 0.000066 cents = 66 micro-cents exactly.
	if got := price.CostMicroCents(provider.Usage{OutputTokens: 1}); got != 66 {
		t.Errorf("one output token cost %d micro-cents, want 66", got)
	}
	// A cost that does not land on a whole micro-cent rounds UP, never down or to zero.
	tiny := price.CostMicroCents(provider.Usage{InputTokens: 1})
	if tiny <= 0 {
		t.Errorf("a real token cost rounded to %d — a ledger that rounds to zero lets a long run of "+
			"cheap calls accumulate spend the record never shows", tiny)
	}
}

// TestCachedInputIsPricedSeparately. Folding it into input would overstate cost by the cache discount
// on every cached call — and the discount here is a factor of about 31.
func TestCachedInputIsPricedSeparately(t *testing.T) {
	c := New("unused")
	price, _ := c.PriceFor(ModelFlash, time.Date(2026, 8, 31, 14, 0, 0, 0, time.UTC))
	allFresh := price.CostMicroCents(provider.Usage{InputTokens: 100_000})
	allCached := price.CostMicroCents(provider.Usage{InputTokens: 100_000, CachedInputTokens: 100_000})
	if allCached >= allFresh {
		t.Fatalf("cached input (%d) is not cheaper than fresh (%d)", allCached, allFresh)
	}
	if ratio := float64(allFresh) / float64(allCached); ratio < 20 {
		t.Errorf("the cache discount is only %.1fx; the published rates differ by about 31x", ratio)
	}
}
