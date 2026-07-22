package scoring

import (
	"fmt"
	"math"
	"sort"

	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/evalstats"
)

// cache.go is step 1 of the pipeline — NORMALIZE — plus the cache that makes step 3 free.
//
// Normalization is min-max ACROSS THE VARIANT SET (design Q4): the best variant on a metric scores
// 1, the worst 0. That makes dollars and percent comparable, which is the whole point, at the price
// of set-dependence — adding a variant can re-normalize the others. The scale is therefore RECORDED
// on the cache (Scale below), so two boards can be compared by checking whether they were normalized
// against the same reference rather than by assuming it.

// Scale is the reference interval a metric was normalized against.
type Scale struct {
	Metric string  `json:"metric"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	// Degenerate is true when this metric does not SEPARATE the variant set: either every variant
	// scored identically, or every variant's confidence interval overlaps every other's. Every
	// variant then normalizes to 1 (nobody is measurably worse than anybody).
	//
	// The second condition is the one that matters in practice and it was found by a test, not by
	// reasoning: min-max normalization divides by the observed spread, so when that spread is
	// COMPARABLE TO THE NOISE it amplifies measurement error into a full-range normalized value.
	// Three variants that are statistically indistinguishable on latency would otherwise be spread
	// across [0,1] on the normalized latency axis and get a composite interval half the board wide —
	// noise promoted to a ranking signal by the very step meant to make metrics comparable. A metric
	// that does not separate the field must not decide the ranking.
	Degenerate bool `json:"degenerate"`
	// DegenerateReason states which condition fired, so a reader is never left wondering why a
	// metric shows 1.000 for everyone.
	DegenerateReason string `json:"degenerate_reason,omitempty"`
}

// Normalize maps a raw value onto [0,1] on this scale, clamped.
func (s Scale) Normalize(v float64) float64 {
	if s.Degenerate || s.Max <= s.Min {
		return 1
	}
	n := (v - s.Min) / (s.Max - s.Min)
	if n < 0 {
		return 0
	}
	if n > 1 {
		return 1
	}
	return n
}

// CachedMetric is one variant's cached numbers for one metric.
type CachedMetric struct {
	Metric string             `json:"metric"`
	Raw    evalstats.Interval `json:"raw"`
	// Normalized is the min-max normalized mean, in [0,1].
	Normalized float64 `json:"normalized"`
	// replicates are the normalized bootstrap replicates. They are the reason a profile switch can
	// produce a real composite CI without re-executing: the composite is bootstrapped by combining
	// these under the new weights. Unexported because a caller has no business reading 2000 floats;
	// they exist to be recombined, not displayed.
	replicates []float64
}

// CachedVariant is everything ranking needs about one variant, computed once.
type CachedVariant struct {
	VariantID     string                  `json:"variant_id"`
	ConfigHash    string                  `json:"config_hash"`
	Label         string                  `json:"label"`
	Providers     []string                `json:"providers"`
	Metrics       map[string]CachedMetric `json:"metrics"`
	WeakCaseIDs   []string                `json:"weak_case_ids,omitempty"`
	LowConfidence bool                    `json:"low_confidence"`
	// PenaltyInputs are the raw quantities the penalty terms read, cached so a profile switch that
	// changes penalty weights still needs no recomputation of anything measured.
	PenaltyInputs map[string]float64 `json:"penalty_inputs"`
}

// Cache is the per-{variant, eval_set_hash} score cache (Decision 6). It is keyed by the eval-set
// hash because a normalized value is only meaningful against the set it was measured on; changing
// the eval set invalidates the cache rather than silently re-using numbers from a different
// measurement.
type Cache struct {
	EvalSetHash string                   `json:"eval_set_hash"`
	Specs       []MetricSpec             `json:"specs"`
	Scales      map[string]Scale         `json:"scales"`
	Variants    map[string]CachedVariant `json:"variants"`
	// Order is the stable variant order the cache was built in.
	Order []string `json:"order"`
	// Confidence is the coverage of every interval in the cache.
	Confidence float64 `json:"confidence"`
	// Unmeasured names variants that were EXCLUDED because they have no usable measurements — the
	// fan-out was cut short by a budget cap, or a seed floor was never reached. They are excluded
	// rather than scored, and NAMED rather than dropped: a variant nobody measured must not appear
	// on the board with an invented number, and it must not vanish either, because "we ran out of
	// budget before measuring these twelve" is a finding.
	Unmeasured []UnmeasuredVariant `json:"unmeasured,omitempty"`
	// replicateCount is how many bootstrap replicates each metric carries.
	replicateCount int
}

// UnmeasuredVariant is a variant the cache could not score, with the reason.
type UnmeasuredVariant struct {
	VariantID string `json:"variant_id"`
	Reason    string `json:"reason"`
}

// Build runs steps that cost real work exactly ONCE: aggregate every variant's multi-seed series
// into a mean + CI + replicates, derive the normalization scale across the variant set, and
// normalize. Everything Rank does afterwards is arithmetic over these numbers.
func Build(b Board, cfg evalstats.Config) (*Cache, error) {
	if len(b.Variants) == 0 {
		return nil, ErrNoVariants
	}
	specs := b.Specs
	if len(specs) == 0 {
		specs = DefaultSpecs()
	}
	c := &Cache{
		EvalSetHash: b.EvalSetHash,
		Specs:       specs,
		Scales:      map[string]Scale{},
		Variants:    map[string]CachedVariant{},
		Confidence:  cfg.Confidence,
	}
	if c.Confidence <= 0 || c.Confidence >= 1 {
		c.Confidence = 0.95
	}

	// Pass 1 — aggregate every variant x metric, keeping the replicates.
	type agg struct {
		iv   evalstats.Interval
		reps []float64
	}
	raw := map[string]map[string]agg{}
	measured := make([]Variant, 0, len(b.Variants))
	for _, v := range b.Variants {
		byMetric := map[string]agg{}
		var unusable string
		for _, spec := range specs {
			s, ok := v.Metrics[spec.Name]
			if !ok {
				// A DECLARED metric that is entirely absent from the board's input is a wiring
				// error, not a measurement gap, and it stays an error.
				return nil, fmt.Errorf("%w: variant %q lacks %q", ErrMissingMetric, v.VariantID, spec.Name)
			}
			reps, iv, err := evalstats.AggregateReplicates(s, cfg)
			if err != nil {
				// No observations, or fewer seeds than the floor. This is a measurement gap — the
				// fan-out was cut short — so the variant is excluded and named rather than scored
				// from whatever partial data exists. Scoring it would put a number on the board
				// that is not a measurement.
				unusable = err.Error()
				break
			}
			byMetric[spec.Name] = agg{iv: iv, reps: reps}
			if c.replicateCount == 0 {
				c.replicateCount = len(reps)
			}
		}
		if unusable != "" {
			c.Unmeasured = append(c.Unmeasured, UnmeasuredVariant{VariantID: v.VariantID, Reason: unusable})
			continue
		}
		raw[v.VariantID] = byMetric
		measured = append(measured, v)
	}
	sort.Slice(c.Unmeasured, func(i, j int) bool { return c.Unmeasured[i].VariantID < c.Unmeasured[j].VariantID })
	if len(measured) == 0 {
		return nil, fmt.Errorf("%w: none of the %d variants carries a usable measurement", ErrNoVariants, len(b.Variants))
	}

	// Pass 2 — the normalization scale, derived across the whole variant set.
	for _, spec := range specs {
		min, max := math.Inf(1), math.Inf(-1)
		intervals := make([]evalstats.Interval, 0, len(measured))
		for _, v := range measured {
			iv := raw[v.VariantID][spec.Name].iv
			intervals = append(intervals, iv)
			if iv.Mean < min {
				min = iv.Mean
			}
			if iv.Mean > max {
				max = iv.Mean
			}
		}
		sc := Scale{Metric: spec.Name, Min: min, Max: max}
		switch {
		case max-min <= 1e-12:
			sc.Degenerate = true
			sc.DegenerateReason = "every variant scored identically"
		case !separates(intervals):
			sc.Degenerate = true
			sc.DegenerateReason = "every variant's confidence interval overlaps every other's, " +
				"so the observed spread is indistinguishable from measurement noise"
		}
		c.Scales[spec.Name] = sc
	}

	// Pass 3 — normalize. A lower-is-better metric is INVERTED here so every cached normalized value
	// means the same thing: 1 is good. Leaving the inversion to the formula would mean four call
	// sites each had to remember which way round cost runs.
	for _, v := range measured {
		cv := CachedVariant{
			VariantID:     v.VariantID,
			ConfigHash:    v.ConfigHash,
			Label:         v.Label,
			Providers:     sortedStrings(v.Providers),
			Metrics:       map[string]CachedMetric{},
			WeakCaseIDs:   sortedStrings(v.WeakCaseIDs),
			LowConfidence: v.LowConfidence,
			PenaltyInputs: map[string]float64{},
		}
		for _, spec := range specs {
			a := raw[v.VariantID][spec.Name]
			sc := c.Scales[spec.Name]
			norm := sc.Normalize(a.iv.Mean)
			reps := make([]float64, len(a.reps))
			for i, r := range a.reps {
				reps[i] = sc.Normalize(r)
			}
			// The inversion is skipped on a DEGENERATE scale. Normalize already returned 1 there —
			// "nobody is measurably worse than anybody" — and inverting that would turn it into 0,
			// i.e. "everybody is the worst", which on a lower-is-better metric would silently zero
			// out the cost and latency terms for the whole board.
			if spec.Direction == evalstats.LowerIsBetter && !sc.Degenerate {
				norm = 1 - norm
				for i := range reps {
					reps[i] = 1 - reps[i]
				}
			}
			cv.Metrics[spec.Name] = CachedMetric{
				Metric:     spec.Name,
				Raw:        a.iv,
				Normalized: norm,
				replicates: reps,
			}
		}
		cv.PenaltyInputs[PenaltyWeakReference] = weakFraction(v)
		if m, ok := cv.Metrics[evalharness.MetricToolErrorRate]; ok {
			cv.PenaltyInputs[PenaltyToolError] = m.Raw.Mean
		}
		c.Variants[v.VariantID] = cv
		c.Order = append(c.Order, v.VariantID)
	}
	sort.Strings(c.Order)
	return c, nil
}

// separates reports whether a metric distinguishes ANY pair of variants: true as soon as one pair's
// confidence intervals fail to overlap. It is the same predicate evalstats.Compare uses for its tie
// rule, applied here so "this metric cannot tell these variants apart" means the same thing in the
// normalizer as it does in the comparison primitive.
func separates(intervals []evalstats.Interval) bool {
	for i := 0; i < len(intervals); i++ {
		for j := i + 1; j < len(intervals); j++ {
			if !intervals[i].Overlaps(intervals[j]) {
				return true
			}
		}
	}
	return false
}

// weakFraction is the share of the variant's cases whose reference is weak.
func weakFraction(v Variant) float64 {
	if len(v.WeakCaseIDs) == 0 {
		return 0
	}
	total := 0
	for _, s := range v.Metrics {
		if n := len(s.Cases()); n > total {
			total = n
		}
	}
	if total == 0 {
		return 0
	}
	f := float64(len(v.WeakCaseIDs)) / float64(total)
	if f > 1 {
		return 1
	}
	return f
}

// SpecFor returns the declared spec for a metric.
func (c *Cache) SpecFor(metric string) (MetricSpec, bool) {
	for _, s := range c.Specs {
		if s.Name == metric {
			return s, true
		}
	}
	return MetricSpec{}, false
}

// Metric returns one variant's cached numbers for one metric.
func (c *Cache) Metric(variantID, metric string) (CachedMetric, bool) {
	v, ok := c.Variants[variantID]
	if !ok {
		return CachedMetric{}, false
	}
	m, ok := v.Metrics[metric]
	return m, ok
}
