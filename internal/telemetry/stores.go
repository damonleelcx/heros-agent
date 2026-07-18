package telemetry

import (
	"context"
	"sync"
	"time"

	"github.com/heros-foreal/agentd/internal/metricevent"
)

// stores.go declares the three-stores-by-shape contract (Decision 5) and ships in-memory
// implementations for hermetic tests. Each store is built for ONE query shape and every record it holds
// is keyed by config_hash:
//
//	Span store (Tempo/Jaeger)  — per-run trace drill-down
//	TSDB (Prometheus/ClickHouse) — metric trend/aggregation over low-cardinality labels
//	Eval store (Postgres)       — per-variant/node/case comparison, NOT NULL tags + FKs
//
// The in-memory versions are the SAME contract a real Tempo/Prometheus/Postgres satisfies, so wiring a
// real backend is swapping the implementation, not re-plumbing collection/tagging/storage. The Postgres
// eval store is in evalstore_pg.go and is proved against a live database.

// ─────────────────────────────────────────────────────────────────────────────
// Span store
// ─────────────────────────────────────────────────────────────────────────────

// SpanStore holds the OTel spans and answers per-run drill-down, filterable by config_hash (task 5.4).
type SpanStore interface {
	PutSpan(ctx context.Context, sp Span)
	// Trace returns every span of a run, the run->node->tool hierarchy an operator drills.
	Trace(runID string) []Span
	// SpansByConfigHash returns every span produced by a configuration (config_hash-filterable drill-down).
	SpansByConfigHash(configHash string) []Span
}

// MemSpanStore is an in-memory SpanStore with retention eviction.
type MemSpanStore struct {
	mu        sync.Mutex
	spans     []Span
	retention time.Duration
	now       func() time.Time
}

// NewMemSpanStore builds an in-memory span store. A non-positive retention keeps spans forever.
func NewMemSpanStore(retention time.Duration) *MemSpanStore {
	return &MemSpanStore{retention: retention, now: time.Now}
}

func (m *MemSpanStore) PutSpan(_ context.Context, sp Span) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spans = append(m.spans, sp)
	m.evictLocked()
}

func (m *MemSpanStore) evictLocked() {
	if m.retention <= 0 {
		return
	}
	cutoff := m.now().Add(-m.retention)
	kept := m.spans[:0]
	for _, s := range m.spans {
		if s.EndTime.IsZero() || s.EndTime.After(cutoff) {
			kept = append(kept, s)
		}
	}
	m.spans = kept
}

func (m *MemSpanStore) Trace(runID string) []Span {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Span
	for _, s := range m.spans {
		if attrStr(s.Attributes, AttrRunID) == runID {
			out = append(out, s)
		}
	}
	return out
}

func (m *MemSpanStore) SpansByConfigHash(configHash string) []Span {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Span
	for _, s := range m.spans {
		if attrStr(s.Attributes, AttrConfigHash) == configHash {
			out = append(out, s)
		}
	}
	return out
}

func attrStr(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	s, _ := m[k].(string)
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
// TSDB
// ─────────────────────────────────────────────────────────────────────────────

// Sample is one metric point in the TSDB: its low-cardinality labels, value, time, and the
// high-cardinality exemplars that link the bucket back to a representative run/case (never labels).
type Sample struct {
	Labels    map[string]string
	Value     float64
	Timestamp time.Time
	Exemplars map[string]string
}

// TSDB holds metric samples under low-cardinality series labels and answers trend/aggregation queries
// filterable by config_hash (task 5.4). It is the ONLY store that projects to SeriesLabels, which is
// what enforces the cardinality budget at the storage layer (Decision 4).
type TSDB interface {
	PutMetric(ctx context.Context, ev metricevent.Event)
	// Query returns samples whose labels match every key in the matcher. The matcher may only name
	// series-label tags; a high-cardinality key is rejected (it was never a label, so matching on it is
	// a category error the store refuses loudly rather than silently returning nothing).
	Query(matcher map[string]string) ([]Sample, error)
}

// MemTSDB is an in-memory TSDB that stores samples projected through SeriesLabels — so, like a real
// TSDB, it cannot index a high-cardinality tag even if handed one.
type MemTSDB struct {
	mu        sync.Mutex
	samples   []Sample
	retention time.Duration
	now       func() time.Time
}

func NewMemTSDB(retention time.Duration) *MemTSDB {
	return &MemTSDB{retention: retention, now: time.Now}
}

func (m *MemTSDB) PutMetric(_ context.Context, ev metricevent.Event) {
	ts := parseTS(ev.Timestamp, m.now())
	s := Sample{
		Labels:    SeriesLabels(ev), // high-cardinality tags are DROPPED here, at the store boundary
		Value:     valueOf(ev),
		Timestamp: ts,
		Exemplars: Exemplars(ev),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = append(m.samples, s)
	if m.retention > 0 {
		cutoff := m.now().Add(-m.retention)
		kept := m.samples[:0]
		for _, x := range m.samples {
			if x.Timestamp.After(cutoff) {
				kept = append(kept, x)
			}
		}
		m.samples = kept
	}
}

// ErrHighCardinalityMatcher is returned when a query tries to match on a tag that is not a series label
// — matching on case_id/run_id in the TSDB is meaningless because they were never indexed there.
type highCardMatcherError struct{ tag string }

func (e *highCardMatcherError) Error() string {
	return "telemetry: cannot query the TSDB by " + e.tag + " — it is not a series label (query the span store or Postgres for it)"
}

func (m *MemTSDB) Query(matcher map[string]string) ([]Sample, error) {
	for k := range matcher {
		if !IsSeriesLabel(k) {
			return nil, &highCardMatcherError{tag: k}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Sample
	for _, s := range m.samples {
		if labelsMatch(s.Labels, matcher) {
			out = append(out, s)
		}
	}
	return out, nil
}

func labelsMatch(labels, matcher map[string]string) bool {
	for k, v := range matcher {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// ─────────────────────────────────────────────────────────────────────────────
// Eval store
// ─────────────────────────────────────────────────────────────────────────────

// QualityMetricEvent is what an evaluator emits (§7): the seven-tag metric event plus which evaluator
// produced it and the content-hash references to the input/output it scored. It is the eval-store
// record shape and is defined here because the store is what persists it.
type QualityMetricEvent struct {
	metricevent.Event
	EvaluatorName  string
	InputBlobHash  string
	OutputBlobHash string
}

// EvalStore persists quality-metric events to Postgres — low volume, rich joins, NOT NULL tags + FKs —
// and answers per-variant/node/case comparison filterable by config_hash (task 5.4).
type EvalStore interface {
	PutEval(ctx context.Context, ev QualityMetricEvent) error
	ByConfigHash(ctx context.Context, configHash string) ([]QualityMetricEvent, error)
}

// MemEvalStore is an in-memory EvalStore for hermetic tests; PGEvalStore is the real one.
type MemEvalStore struct {
	mu   sync.Mutex
	rows []QualityMetricEvent
}

func NewMemEvalStore() *MemEvalStore { return &MemEvalStore{} }

func (m *MemEvalStore) PutEval(_ context.Context, ev QualityMetricEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, ev)
	return nil
}

func (m *MemEvalStore) ByConfigHash(_ context.Context, configHash string) ([]QualityMetricEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []QualityMetricEvent
	for _, r := range m.rows {
		if r.ConfigHash == configHash {
			out = append(out, r)
		}
	}
	return out, nil
}

// ── small helpers ──

func valueOf(ev metricevent.Event) float64 {
	if ev.Value == nil {
		return 0
	}
	return *ev.Value
}

func parseTS(s string, fallback time.Time) time.Time {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return fallback
}
