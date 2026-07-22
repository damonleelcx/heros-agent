package evalstats

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

// fixtures: two variant pairs that are the whole point of this package.
//
//   trueZeroDelta   — identical generating process, different label. The honest answer is `tie`.
//   knownRealDelta  — a large, real quality gap. The honest answer is a winner.
//
// Both are generated from a fixed RNG so the assertions are about the statistics, not about luck.

// series builds a series over a FIXED eval set.
//
// caseSeed generates the per-case difficulty offsets and is shared by both variants of a pair, so
// the two variants are compared over THE SAME cases with the same intrinsic difficulty. noiseSeed is
// the variant's own stochastic stream. Getting this wrong is not a test-fixture detail: an earlier
// version of this helper drew case difficulty from the variant's stream, which made "identical
// configuration, different label" secretly a different eval set — and the tie rule correctly called
// one of those pairs a winner, because on those two different case sets one variant really was
// better. The fixture had to be fixed, not the rule.
func series(variantID, metric string, nCases, nSeeds int, mean, caseSpread, noise float64, caseSeed, noiseSeed int64) Series {
	caseRNG := rand.New(rand.NewSource(caseSeed))
	offsets := make([]float64, nCases)
	for c := range offsets {
		offsets[c] = (caseRNG.Float64() - 0.5) * 2 * caseSpread
	}
	rng := rand.New(rand.NewSource(noiseSeed))
	s := Series{VariantID: variantID, Metric: metric}
	for c := 0; c < nCases; c++ {
		for seed := 0; seed < nSeeds; seed++ {
			v := mean + offsets[c] + (rng.Float64()-0.5)*2*noise
			s.Obs = append(s.Obs, Observation{
				CaseID: caseID(c),
				Seed:   int64(seed),
				Value:  clamp01(v),
			})
		}
	}
	return s
}

func caseID(i int) string {
	return "case-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Task 2.1 — multi-seed is the default and an under-seeded series is REFUSED, not silently
// aggregated: an under-seeded CI understates variance and manufactures false winners.
func TestUnderSeededSeriesIsRefused(t *testing.T) {
	s := series("v-a", "task_success", 20, 3, 0.7, 0.2, 0.05, 900, 1)
	if _, err := Aggregate(s, DefaultConfig()); !errors.Is(err, ErrUnderSeeded) {
		t.Fatalf("3 seeds under a floor of 5 must be refused, got %v", err)
	}
	s5 := series("v-a", "task_success", 20, 5, 0.7, 0.2, 0.05, 900, 1)
	if _, err := Aggregate(s5, DefaultConfig()); err != nil {
		t.Fatalf("5 seeds must aggregate: %v", err)
	}
}

// Task 2.1 — per-seed values are retained and reportable, so a CI can be audited rather than
// believed.
func TestPerSeedValuesArePreserved(t *testing.T) {
	s := series("v-a", "task_success", 10, 5, 0.7, 0.2, 0.05, 900, 2)
	sm := s.SeedMeans()
	if len(sm) != 5 {
		t.Fatalf("want 5 per-seed means, got %d", len(sm))
	}
	for i, m := range sm {
		if m.Seed != int64(i) {
			t.Fatalf("seed means must be seed-ordered, got %v at %d", m.Seed, i)
		}
		if m.NCases != 10 {
			t.Fatalf("seed %d: want 10 cases, got %d", m.Seed, m.NCases)
		}
	}
}

// Task 2.2 — every metric is reported as a mean WITH an interval and its n. A bare point value is
// never the comparison result.
func TestAggregateReportsMeanWithIntervalAndN(t *testing.T) {
	s := series("v-a", "task_success", 30, 5, 0.7, 0.2, 0.05, 900, 3)
	iv, err := Aggregate(s, DefaultConfig())
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if iv.NSeeds != 5 || iv.NCases != 30 || iv.NObs != 150 {
		t.Fatalf("evidence counts wrong: %+v", iv)
	}
	if iv.Low >= iv.High {
		t.Fatalf("interval must have width: %+v", iv)
	}
	if iv.Mean < iv.Low || iv.Mean > iv.High {
		t.Fatalf("the mean must lie inside its own interval: %+v", iv)
	}
	if iv.Method == "" || iv.Confidence != 0.95 {
		t.Fatalf("an interval must state its method and coverage: %+v", iv)
	}
}

// The bootstrap is DETERMINISTIC: the same data yields byte-identical intervals. A CI that moves
// when nothing else did is a CI nobody can act on.
func TestAggregateIsDeterministic(t *testing.T) {
	s := series("v-a", "task_success", 25, 5, 0.7, 0.2, 0.05, 900, 4)
	a, _ := Aggregate(s, DefaultConfig())
	b, _ := Aggregate(s, DefaultConfig())
	if a != b {
		t.Fatalf("bootstrap must be deterministic:\n%+v\n%+v", a, b)
	}
}

// Task 2.4 / 2.5 / 8.2 — THE load-bearing test. A true-zero-delta pair (same generating process,
// different label) over 5 seeds returns `tie`, not a coin-flip winner.
func TestTrueZeroDeltaIsATieNotACoinFlip(t *testing.T) {
	// Same mean, same spread, same noise — only the label and the RNG stream differ, which is
	// exactly "identical configuration, different label".
	a := series("v-a", "task_success", 40, 5, 0.70, 0.25, 0.10, 900, 11)
	b := series("v-b", "task_success", 40, 5, 0.70, 0.25, 0.10, 900, 12)

	cmp, err := Compare(a, b, HigherIsBetter, DefaultConfig())
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if !cmp.A.Overlaps(cmp.B) {
		t.Fatalf("a true-zero-delta pair must have overlapping CIs, got a=[%v,%v] b=[%v,%v]",
			cmp.A.Low, cmp.A.High, cmp.B.Low, cmp.B.High)
	}
	if cmp.Verdict != VerdictTie {
		t.Fatalf("want tie, got %q (reason %q)", cmp.Verdict, cmp.Reason)
	}
	if cmp.Reason == "" {
		t.Fatal("a tie must explain itself; an unexplained tie reads as a broken tool")
	}
	if cmp.Significant {
		t.Fatal("a zero delta must not be reported significant")
	}
}

// Repeat the zero-delta comparison across many independent label pairs: a coin-flip winner would
// show up as a nonzero rate of declared winners. This is the anti-flake version of the claim — one
// tie could be luck, forty cannot.
func TestZeroDeltaNeverDeclaresAWinnerAcrossManyPairs(t *testing.T) {
	winners := 0
	for i := 0; i < 40; i++ {
		a := series("v-a", "task_success", 30, 5, 0.65, 0.25, 0.12, int64(900+i), int64(100+2*i))
		b := series("v-b", "task_success", 30, 5, 0.65, 0.25, 0.12, int64(900+i), int64(101+2*i))
		cmp, err := Compare(a, b, HigherIsBetter, DefaultConfig())
		if err != nil {
			t.Fatalf("compare %d: %v", i, err)
		}
		if cmp.Verdict != VerdictTie {
			winners++
			t.Logf("pair %d declared %q: a=[%.4f,%.4f] b=[%.4f,%.4f] delta=[%.4f,%.4f]",
				i, cmp.Verdict, cmp.A.Low, cmp.A.High, cmp.B.Low, cmp.B.High, cmp.Delta.Low, cmp.Delta.High)
		}
	}
	if winners > 0 {
		t.Fatalf("%d of 40 true-zero-delta pairs declared a winner; the tie rule is not holding", winners)
	}
}

// Task 2.5 / 8.2 — a known-real-delta pair yields the CORRECT winner with non-overlapping CIs.
func TestKnownRealDeltaYieldsTheCorrectWinner(t *testing.T) {
	strong := series("v-strong", "task_success", 40, 5, 0.90, 0.05, 0.03, 900, 21)
	weak := series("v-weak", "task_success", 40, 5, 0.40, 0.05, 0.03, 900, 22)

	cmp, err := Compare(strong, weak, HigherIsBetter, DefaultConfig())
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if cmp.A.Overlaps(cmp.B) {
		t.Fatalf("a real delta must produce non-overlapping CIs, got a=[%v,%v] b=[%v,%v]",
			cmp.A.Low, cmp.A.High, cmp.B.Low, cmp.B.High)
	}
	if !cmp.Significant {
		t.Fatal("a real delta must fire the significance test")
	}
	if cmp.Verdict != VerdictAWins {
		t.Fatalf("want the stronger variant to win, got %q", cmp.Verdict)
	}
	if cmp.PValue > 0.05 {
		t.Fatalf("want p <= 0.05 on a large real delta, got %v", cmp.PValue)
	}
}

// Direction is explicit: on a lower-is-better metric the cheaper variant wins, and inferring the
// direction from the metric name would eventually rank a variant backwards.
func TestLowerIsBetterInvertsTheWinner(t *testing.T) {
	cheap := series("v-cheap", "eval_cost_usd", 40, 5, 0.10, 0.02, 0.01, 900, 31)
	pricey := series("v-pricey", "eval_cost_usd", 40, 5, 0.80, 0.02, 0.01, 900, 32)

	cmp, err := Compare(cheap, pricey, LowerIsBetter, DefaultConfig())
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if cmp.Verdict != VerdictAWins {
		t.Fatalf("on a lower-is-better metric the cheaper variant wins, got %q", cmp.Verdict)
	}

	cmp2, _ := Compare(cheap, pricey, HigherIsBetter, DefaultConfig())
	if cmp2.Verdict != VerdictBWins {
		t.Fatalf("with the direction flipped the other variant wins, got %q", cmp2.Verdict)
	}
}

// A marginal delta whose intervals still touch is a tie: the tie rule cannot be bypassed by a
// significant p-value alone.
func TestOverlappingCIsAreATieEvenWhenTheDeltaLooksSignificant(t *testing.T) {
	a := series("v-a", "task_success", 40, 5, 0.700, 0.30, 0.10, 900, 41)
	b := series("v-b", "task_success", 40, 5, 0.715, 0.30, 0.10, 900, 42)

	cmp, err := Compare(a, b, HigherIsBetter, DefaultConfig())
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if cmp.A.Overlaps(cmp.B) && cmp.Verdict != VerdictTie {
		t.Fatalf("overlapping CIs must force a tie regardless of p, got %q (p=%v)", cmp.Verdict, cmp.PValue)
	}
}

// Comparing variants that share no case is refused rather than silently compared over nothing.
func TestNoCommonCasesIsRefused(t *testing.T) {
	a := Series{VariantID: "a", Metric: "m"}
	b := Series{VariantID: "b", Metric: "m"}
	for seed := 0; seed < 5; seed++ {
		a.Obs = append(a.Obs, Observation{CaseID: "only-a", Seed: int64(seed), Value: 1})
		b.Obs = append(b.Obs, Observation{CaseID: "only-b", Seed: int64(seed), Value: 0})
	}
	if _, err := Compare(a, b, HigherIsBetter, DefaultConfig()); !errors.Is(err, ErrNoCommonCases) {
		t.Fatalf("want ErrNoCommonCases, got %v", err)
	}
}

// An empty series is an error, not an interval of zero: an interval of zero is a measurement claim
// about an unmeasured thing.
func TestEmptySeriesIsRefused(t *testing.T) {
	if _, err := Aggregate(Series{VariantID: "a", Metric: "m"}, DefaultConfig()); !errors.Is(err, ErrNoObservations) {
		t.Fatalf("want ErrNoObservations, got %v", err)
	}
}

// The interval brackets the mean of a degenerate (zero-variance) series exactly.
func TestZeroVarianceSeriesHasAZeroWidthInterval(t *testing.T) {
	s := Series{VariantID: "a", Metric: "m"}
	for c := 0; c < 10; c++ {
		for seed := 0; seed < 5; seed++ {
			s.Obs = append(s.Obs, Observation{CaseID: string(rune('a' + c)), Seed: int64(seed), Value: 0.5})
		}
	}
	iv, err := Aggregate(s, DefaultConfig())
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if math.Abs(iv.Width()) > 1e-12 {
		t.Fatalf("a zero-variance series must have a zero-width interval, got %v", iv.Width())
	}
	if math.Abs(iv.Mean-0.5) > 1e-12 {
		t.Fatalf("mean: want 0.5 got %v", iv.Mean)
	}
}
