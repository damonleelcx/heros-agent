package telemetry

import (
	"crypto/sha256"
	"encoding/binary"
	"time"
)

// sampling.go is task 4.3 / NFR9: bound the observability system's OWN cost and blast radius. Spans are
// the heavy store — §8.3 sizes them at ~3×10⁶/run, 3–6 GB even sampled — so their volume must be
// bounded by a sampler and a retention window. Per P0 OQ6 / OQ2 this ships the MECHANISM; the numbers
// are tuned against real volume, not guessed at now.

// SpanSampler decides which spans reach the span store. It is HEAD-based and PER-TRACE: the keep
// decision is a deterministic function of the trace id, so every span of a run shares one verdict and a
// kept trace is never half-missing (drillability, task 4.1, would break if a trace were partially
// sampled). Error spans are kept unconditionally so a failure is never sampled away — the one telemetry
// you most need is the one that records something went wrong.
type SpanSampler struct {
	ratio       float64 // fraction of traces kept, [0,1]
	alwaysError bool
}

// NewSpanSampler builds a sampler keeping `ratio` of traces (clamped to [0,1]). alwaysError keeps every
// error span regardless of the trace verdict.
func NewSpanSampler(ratio float64, alwaysError bool) *SpanSampler {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	return &SpanSampler{ratio: ratio, alwaysError: alwaysError}
}

// KeepTrace is the per-trace head decision, deterministic on the trace id. Every span in a trace gets
// the same answer, so traces are kept or dropped whole.
func (s *SpanSampler) KeepTrace(traceID string) bool {
	if s.ratio >= 1 {
		return true
	}
	if s.ratio <= 0 {
		return false
	}
	return traceSampleValue(traceID) < s.ratio
}

// Keep decides one span: keep it if its trace is sampled in, OR (alwaysError) if it records an error.
func (s *SpanSampler) Keep(sp Span) bool {
	if s.alwaysError && sp.Status == SpanStatusError {
		return true
	}
	return s.KeepTrace(sp.TraceID)
}

// traceSampleValue maps a trace id to [0,1) via the first 8 bytes of its SHA-256, so sampling is
// uniform over trace ids and stable across processes (two collectors sample the same trace identically).
func traceSampleValue(traceID string) float64 {
	sum := sha256.Sum256([]byte(traceID))
	u := binary.BigEndian.Uint64(sum[:8])
	return float64(u) / float64(^uint64(0))
}

// RetentionPolicy bounds how long each store keeps telemetry. The defaults are sized from §8.3's
// volumes and value: spans are the largest and least individually valuable, so they expire soonest;
// eval results are the smallest and most valuable (they answer variant-vs-variant), so they live
// longest. These are the MECHANISM's numbers — a deployment tunes them against its real volume.
type RetentionPolicy struct {
	Spans       time.Duration
	Metrics     time.Duration
	EvalResults time.Duration
}

// DefaultRetention is the sized-from-§8.3 starting point. Numbers, not reflexes: spans (3–6 GB/run)
// short, metrics (~20 MB/run) medium, eval results (~200 MB total, high value) long.
func DefaultRetention() RetentionPolicy {
	return RetentionPolicy{
		Spans:       7 * 24 * time.Hour,   // heaviest store, per-run drill-down needed only recently
		Metrics:     30 * 24 * time.Hour,  // tiny; trends want a month
		EvalResults: 180 * 24 * time.Hour, // smallest + highest value; comparisons span releases
	}
}

// Expired reports whether telemetry stamped at `ts` is past its store's retention as of `now`. A
// non-positive TTL means "keep forever" (retention disabled), which a store honors by never evicting.
func (r RetentionPolicy) expired(ttl time.Duration, ts, now time.Time) bool {
	if ttl <= 0 {
		return false
	}
	return now.Sub(ts) > ttl
}
