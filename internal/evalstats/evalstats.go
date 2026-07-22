// Package evalstats is P4 Decision 2: statistical honesty as a PRIMITIVE, not a report.
//
// LLM outputs are stochastic, so a single-run comparison lies. The classic failure is reading a
// reordering delta inside the noise band as a win and shipping it. This package makes that failure
// structurally hard: multi-seed aggregation, a confidence interval on every metric, a significance
// test on every pairwise delta, and — the load-bearing part — `tie` as a first-class VERDICT of the
// comparison function rather than a UI annotation. Every consumer (leaderboard, P5.5 verification,
// P6 optimizer) goes through Compare, so no consumer can accidentally rank noise by forgetting to
// check something.
package evalstats

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// DefaultMinSeeds is the floor the harness runs at unless configured otherwise. Five is not a
// statistical constant; it is the smallest N at which a bootstrap over seeds says anything at all,
// and it is written here once so "N >= 5" is a value rather than a habit.
const DefaultMinSeeds = 5

var (
	// ErrNoObservations means there is nothing to aggregate. Returning an interval of zero would be
	// a measurement claim about an unmeasured thing.
	ErrNoObservations = errors.New("evalstats: no observations")
	// ErrUnderSeeded means the series carries fewer seeds than the configured floor. It is an ERROR,
	// not a warning, because an under-seeded CI understates variance and therefore manufactures the
	// non-overlap that produces a false winner.
	ErrUnderSeeded = errors.New("evalstats: fewer seeds than the configured minimum")
	// ErrNoCommonCases means two series share no case, so no paired comparison exists.
	ErrNoCommonCases = errors.New("evalstats: the two variants share no eval case")
)

// Observation is one metric value from one case under one seed. Case and seed are both carried
// because they are DIFFERENT sources of variance: two cases differ because the task differs, two
// seeds of one case differ because the model is stochastic. Collapsing them loses the distinction
// the bootstrap depends on.
type Observation struct {
	CaseID string  `json:"case_id"`
	Seed   int64   `json:"seed"`
	Value  float64 `json:"value"`
}

// Series is every observation of one metric for one variant.
type Series struct {
	VariantID  string        `json:"variant_id"`
	ConfigHash string        `json:"config_hash"`
	Metric     string        `json:"metric"`
	Obs        []Observation `json:"observations"`
}

// Seeds returns the distinct seeds present, sorted.
func (s Series) Seeds() []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, o := range s.Obs {
		if !seen[o.Seed] {
			seen[o.Seed] = true
			out = append(out, o.Seed)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Cases returns the distinct case ids present, sorted. Sorted because the bootstrap indexes into
// this slice: a map-order walk would make the same data produce a different CI on every run.
func (s Series) Cases() []string {
	seen := map[string]bool{}
	var out []string
	for _, o := range s.Obs {
		if !seen[o.CaseID] {
			seen[o.CaseID] = true
			out = append(out, o.CaseID)
		}
	}
	sort.Strings(out)
	return out
}

// Mean is the plain arithmetic mean over every observation.
func (s Series) Mean() float64 {
	if len(s.Obs) == 0 {
		return 0
	}
	var sum float64
	for _, o := range s.Obs {
		sum += o.Value
	}
	return sum / float64(len(s.Obs))
}

// SeedMeans returns the per-seed mean, seed-sorted. Task 2.1 requires per-seed values to be
// PERSISTED, not just consumed: a CI whose per-seed inputs are gone cannot be audited, and an
// unauditable CI is a number a reader has to take on faith.
func (s Series) SeedMeans() []SeedMean {
	bySeed := map[int64][]float64{}
	for _, o := range s.Obs {
		bySeed[o.Seed] = append(bySeed[o.Seed], o.Value)
	}
	out := make([]SeedMean, 0, len(bySeed))
	for seed, vals := range bySeed {
		var sum float64
		for _, v := range vals {
			sum += v
		}
		out = append(out, SeedMean{Seed: seed, Mean: sum / float64(len(vals)), NCases: len(vals)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seed < out[j].Seed })
	return out
}

// SeedMean is one seed's mean over the eval set.
type SeedMean struct {
	Seed   int64   `json:"seed"`
	Mean   float64 `json:"mean"`
	NCases int     `json:"n_cases"`
}

// byCase groups observations by case, each group's values in seed order.
func (s Series) byCase() map[string][]Observation {
	m := map[string][]Observation{}
	for _, o := range s.Obs {
		m[o.CaseID] = append(m[o.CaseID], o)
	}
	for k := range m {
		v := m[k]
		sort.Slice(v, func(i, j int) bool { return v[i].Seed < v[j].Seed })
		m[k] = v
	}
	return m
}

// Config controls aggregation. Every knob is explicit and every default is a value in code, so a
// board can state the method it used rather than implying one.
type Config struct {
	// MinSeeds is the floor below which aggregation refuses.
	MinSeeds int
	// Bootstrap is the number of resamples. 2000 is enough for a stable 95% percentile interval and
	// cheap enough to run per metric per variant on every board render.
	Bootstrap int
	// Confidence is the two-sided coverage, e.g. 0.95.
	Confidence float64
	// RNGSeed makes the bootstrap DETERMINISTIC. A CI that moves when nothing else did is a CI
	// nobody can act on, and "the interval shifted" would become indistinguishable from "the system
	// changed".
	RNGSeed int64
}

// DefaultConfig is the harness default: N >= 5 seeds, 2000 bootstrap resamples, 95% percentile
// interval, fixed RNG seed.
func DefaultConfig() Config {
	return Config{MinSeeds: DefaultMinSeeds, Bootstrap: 2000, Confidence: 0.95, RNGSeed: 0x5EED}
}

func (c Config) normalized() Config {
	if c.MinSeeds <= 0 {
		c.MinSeeds = DefaultMinSeeds
	}
	if c.Bootstrap <= 0 {
		c.Bootstrap = 2000
	}
	if c.Confidence <= 0 || c.Confidence >= 1 {
		c.Confidence = 0.95
	}
	return c
}

// Interval is a metric's mean with its confidence interval and the evidence behind it. NSeeds and
// NCases ride along because an interval without its n is a number a reader cannot weigh.
type Interval struct {
	Mean   float64 `json:"mean"`
	Low    float64 `json:"ci_low"`
	High   float64 `json:"ci_high"`
	NSeeds int     `json:"n_seeds"`
	NCases int     `json:"n_cases"`
	NObs   int     `json:"n_obs"`
	// Method names how the interval was produced, so a stored number is interpretable years later.
	Method string `json:"method"`
	// Confidence is the coverage the interval claims.
	Confidence float64 `json:"confidence"`
}

// Overlaps reports whether two intervals share any point. This is the tie predicate, and it is
// deliberately conservative: a non-overlap is a strong claim, and demanding it is what makes a
// declared winner mean something.
func (i Interval) Overlaps(o Interval) bool {
	return i.Low <= o.High && o.Low <= i.High
}

// Width is the interval's span — a one-number read on how much evidence is behind it.
func (i Interval) Width() float64 { return i.High - i.Low }

// Aggregate computes the mean and a bootstrap confidence interval for one series.
//
// Method (design Q1): resample CASES with replacement, and for each drawn case draw one of its
// per-seed observations uniformly. That treats seeds as a variance component nested inside cases,
// which is what they are — it captures both "some tasks are harder" and "the model is stochastic".
// Bootstrapping the seed means alone would understate case variance; bootstrapping flat over all
// observations would treat five seeds of one case as five independent cases and understate it the
// other way.
func Aggregate(s Series, cfg Config) (Interval, error) {
	_, iv, err := AggregateReplicates(s, cfg)
	return iv, err
}

// AggregateReplicates returns the raw bootstrap replicate means alongside the interval.
//
// The replicates are what makes a profile switch cheap AND honest downstream: the composite score's
// confidence interval is bootstrapped by combining per-metric replicates under the new weights,
// rather than propagating per-metric CIs analytically (which would lose the correlation between a
// variant's cost and its quality) or re-executing anything (which would make exploring profiles cost
// money). Callers that only want the interval use Aggregate.
func AggregateReplicates(s Series, cfg Config) ([]float64, Interval, error) {
	cfg = cfg.normalized()
	if len(s.Obs) == 0 {
		return nil, Interval{}, fmt.Errorf("%w: variant %q metric %q", ErrNoObservations, s.VariantID, s.Metric)
	}
	seeds := s.Seeds()
	if len(seeds) < cfg.MinSeeds {
		return nil, Interval{}, fmt.Errorf("%w: variant %q metric %q has %d seeds, floor is %d",
			ErrUnderSeeded, s.VariantID, s.Metric, len(seeds), cfg.MinSeeds)
	}
	cases := s.Cases()
	byCase := s.byCase()

	rng := rand.New(rand.NewSource(cfg.RNGSeed))
	reps := make([]float64, 0, cfg.Bootstrap)
	for r := 0; r < cfg.Bootstrap; r++ {
		var sum float64
		for i := 0; i < len(cases); i++ {
			obs := byCase[cases[rng.Intn(len(cases))]]
			sum += obs[rng.Intn(len(obs))].Value
		}
		reps = append(reps, sum/float64(len(cases)))
	}
	low, high := percentileInterval(reps, cfg.Confidence)
	return reps, Interval{
		Mean:       s.Mean(),
		Low:        low,
		High:       high,
		NSeeds:     len(seeds),
		NCases:     len(cases),
		NObs:       len(s.Obs),
		Method:     "bootstrap_case_resample_seed_nested",
		Confidence: cfg.Confidence,
	}, nil
}

// PercentileInterval returns the two-sided percentile interval of a bootstrap distribution. It is
// exported so the scoring layer can bootstrap the COMPOSITE score the same way — by combining
// per-metric replicates under a weight profile and taking the percentile interval of the result —
// rather than inventing a second CI method that would disagree with this one.
func PercentileInterval(reps []float64, confidence float64) (low, high float64) {
	return percentileInterval(reps, confidence)
}

// percentileInterval returns the two-sided percentile interval of a bootstrap distribution.
func percentileInterval(reps []float64, confidence float64) (low, high float64) {
	if len(reps) == 0 {
		return 0, 0
	}
	sorted := append([]float64(nil), reps...)
	sort.Float64s(sorted)
	alpha := (1 - confidence) / 2
	return quantile(sorted, alpha), quantile(sorted, 1-alpha)
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo < 0 {
		lo = 0
	}
	if hi >= len(sorted) {
		hi = len(sorted) - 1
	}
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// Direction says which way is better for a metric. It is explicit rather than inferred from the
// metric name, because inferring "cost" means lower-is-better from a substring is exactly the kind
// of name-guessing that eventually ranks a variant backwards.
type Direction int

const (
	HigherIsBetter Direction = iota
	LowerIsBetter
)

// Verdict is the comparison outcome. `tie` is a first-class verdict, not an absence of one.
type Verdict string

const (
	VerdictAWins Verdict = "a>b"
	VerdictBWins Verdict = "b>a"
	VerdictTie   Verdict = "tie"
)

// Comparison is the single primitive every consumer goes through.
type Comparison struct {
	Metric      string    `json:"metric"`
	VariantA    string    `json:"variant_a"`
	VariantB    string    `json:"variant_b"`
	A           Interval  `json:"a"`
	B           Interval  `json:"b"`
	Delta       Interval  `json:"delta"`
	PValue      float64   `json:"p_value"`
	Significant bool      `json:"significant"`
	Verdict     Verdict   `json:"verdict"`
	Direction   Direction `json:"direction"`
	// Reason states, in words a UI can render verbatim, WHY the verdict is what it is. A tie with no
	// stated reason gets read as "the tool did not work".
	Reason string `json:"reason"`
	// NCommonCases is how many cases the paired delta was computed over.
	NCommonCases int `json:"n_common_cases"`
}

// Compare runs the significance test on the pairwise delta and applies the tie rule.
//
// The tie rule is wired in HERE, in the primitive, and not left to the caller. That is the whole
// design: a consumer cannot rank two variants without going through this function, and this function
// cannot return a winner when the confidence intervals overlap. Two independent conditions must both
// hold before a winner is declared:
//
//  1. the two variants' CIs on the metric do NOT overlap, and
//  2. the bootstrapped paired-delta interval excludes zero.
//
// Requiring both is deliberately conservative. A false tie costs one missed improvement; a false
// winner ships noise as a win and then gets built on.
func Compare(a, b Series, dir Direction, cfg Config) (Comparison, error) {
	cfg = cfg.normalized()
	ia, err := Aggregate(a, cfg)
	if err != nil {
		return Comparison{}, err
	}
	ib, err := Aggregate(b, cfg)
	if err != nil {
		return Comparison{}, err
	}
	cmp := Comparison{
		Metric:    a.Metric,
		VariantA:  a.VariantID,
		VariantB:  b.VariantID,
		A:         ia,
		B:         ib,
		Direction: dir,
	}

	common := intersect(a.Cases(), b.Cases())
	if len(common) == 0 {
		return Comparison{}, fmt.Errorf("%w: %q vs %q", ErrNoCommonCases, a.VariantID, b.VariantID)
	}
	cmp.NCommonCases = len(common)

	aByCase, bByCase := a.byCase(), b.byCase()
	rng := rand.New(rand.NewSource(cfg.RNGSeed))
	deltas := make([]float64, 0, cfg.Bootstrap)
	var geZero, leZero int
	for r := 0; r < cfg.Bootstrap; r++ {
		var sum float64
		for i := 0; i < len(common); i++ {
			c := common[rng.Intn(len(common))]
			ao, bo := aByCase[c], bByCase[c]
			sum += ao[rng.Intn(len(ao))].Value - bo[rng.Intn(len(bo))].Value
		}
		d := sum / float64(len(common))
		deltas = append(deltas, d)
		if d >= 0 {
			geZero++
		}
		if d <= 0 {
			leZero++
		}
	}
	dLow, dHigh := percentileInterval(deltas, cfg.Confidence)
	var dsum float64
	for _, d := range deltas {
		dsum += d
	}
	cmp.Delta = Interval{
		Mean:       ia.Mean - ib.Mean,
		Low:        dLow,
		High:       dHigh,
		NSeeds:     minInt(ia.NSeeds, ib.NSeeds),
		NCases:     len(common),
		NObs:       len(a.Obs) + len(b.Obs),
		Method:     "bootstrap_paired_delta",
		Confidence: cfg.Confidence,
	}
	// Two-sided bootstrap p-value: how often the resampled delta lands on the other side of zero.
	tail := minInt(geZero, leZero)
	cmp.PValue = 2 * float64(tail) / float64(cfg.Bootstrap)
	if cmp.PValue > 1 {
		cmp.PValue = 1
	}
	deltaExcludesZero := dLow > 0 || dHigh < 0
	cmp.Significant = deltaExcludesZero

	switch {
	case ia.Overlaps(ib):
		cmp.Verdict = VerdictTie
		cmp.Reason = fmt.Sprintf("confidence intervals overlap ([%.4f,%.4f] vs [%.4f,%.4f]); no winner",
			ia.Low, ia.High, ib.Low, ib.High)
	case !deltaExcludesZero:
		cmp.Verdict = VerdictTie
		cmp.Reason = fmt.Sprintf("the paired-delta interval [%.4f,%.4f] includes zero; no winner", dLow, dHigh)
	default:
		aBetter := ia.Mean > ib.Mean
		if dir == LowerIsBetter {
			aBetter = ia.Mean < ib.Mean
		}
		if aBetter {
			cmp.Verdict = VerdictAWins
		} else {
			cmp.Verdict = VerdictBWins
		}
		cmp.Reason = fmt.Sprintf("non-overlapping intervals and a delta interval [%.4f,%.4f] excluding zero (p=%.4f)",
			dLow, dHigh, cmp.PValue)
	}
	return cmp, nil
}

func intersect(a, b []string) []string {
	set := map[string]bool{}
	for _, s := range b {
		set[s] = true
	}
	var out []string
	for _, s := range a {
		if set[s] {
			out = append(out, s)
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
