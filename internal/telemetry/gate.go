package telemetry

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/heros-foreal/agentd/internal/metricevent"
)

// Gate is the emission boundary (Decision 2/3): the first processor every event and span passes
// through on its way to a store. It enforces two invariants and forwards only survivors to the next
// Sink:
//
//  1. Tag completeness (task 2.2 / 2.3). A metric event missing any of the seven tags is REJECTED and
//     reaches no store — it is dropped here, logged, and never forwarded. A span missing config_hash
//     (or its core attribution) is rejected the same way, so all telemetry is attributable to an exact
//     configuration (task 2.3). This is the first of the two defense-in-depth layers; the Postgres NOT
//     NULL columns are the second (a row that somehow bypassed this is still refused by the DB).
//
//  2. Idempotency (task 2.4 / Decision 8). A retried invocation keyed on {run_id, node_id,
//     attempt_group} is measured ONCE: the first per-call event for an (invocation_id, metric_name) is
//     forwarded, a duplicate is dropped — no double-counted cost. Spans dedup on their deterministic
//     span_id, so a retry recomputes the same id and the duplicate is dropped rather than double-written.
//
// The Gate is a Sink decorator so it composes: the production Collector chains Gate -> cardinality
// filter -> scrubber -> fan-out (§3/§5/§6).
type Gate struct {
	next Sink
	log  Logger

	mu          sync.Mutex
	seenMetrics map[string]struct{} // invocation_id|metric_name
	seenSpans   map[string]struct{} // span_id
	rejected    int64
	deduped     int64
}

// NewGate builds the boundary gate in front of a downstream Sink.
func NewGate(next Sink, opts ...GateOption) *Gate {
	g := &Gate{next: next, log: nopLogger{}, seenMetrics: map[string]struct{}{}, seenSpans: map[string]struct{}{}}
	for _, o := range opts {
		o(g)
	}
	return g
}

// GateOption configures a Gate.
type GateOption func(*Gate)

// WithGateLogger sets where rejections and dedups are logged (they are logged, never silent — a
// fallback/drop path always carries a WARN).
func WithGateLogger(l Logger) GateOption { return func(g *Gate) { g.log = l } }

// Stats is the gate's externally-readable health: how many events it rejected and deduped. A gate that
// silently dropped everything would look identical to one that passed everything; a monitor reads these
// to tell the difference (health-signal-surface).
type Stats struct {
	Rejected int64
	Deduped  int64
}

// Stats returns a snapshot of the gate's counters.
func (g *Gate) Stats() Stats {
	g.mu.Lock()
	defer g.mu.Unlock()
	return Stats{Rejected: g.rejected, Deduped: g.deduped}
}

// EmitMetric applies the tag-completeness rule then the idempotency rule, forwarding only survivors.
// When used as a bare boundary (no downstream Sink, e.g. inside the Collector, which calls admitMetric
// directly), next is nil and this is not the path taken.
func (g *Gate) EmitMetric(ctx context.Context, ev metricevent.Event) {
	if g.admitMetric(ev) && g.next != nil {
		g.next.EmitMetric(ctx, ev)
	}
}

// EmitSpan enforces config_hash-on-every-span (task 2.3) plus span idempotency (task 2.4).
func (g *Gate) EmitSpan(ctx context.Context, sp Span) {
	if g.admitSpan(sp) && g.next != nil {
		g.next.EmitSpan(ctx, sp)
	}
}

// admitMetric is the boundary decision reused by every route that persists a metric-shaped event
// (operational metrics -> TSDB, quality events -> Postgres): tag completeness then idempotency. Extracted
// so the Collector runs the SAME gate on all three paths rather than a second copy that could drift.
func (g *Gate) admitMetric(ev metricevent.Event) bool {
	// (1) Tag completeness — the SAME rule Postgres enforces, applied before any store sees the event.
	if err := ev.Validate(); err != nil {
		g.mu.Lock()
		g.rejected++
		g.mu.Unlock()
		g.log.Warnf("telemetry: gate REJECTED metric %q (reaches no store): %v", ev.MetricName, err)
		return false
	}
	// (2) Idempotency — only per-call events carry an invocation_id. Run-scoped metrics (throughput/
	// concurrency) legitimately repeat over a run, so they are NOT deduped: they are a time series.
	if inv, ok := invocationID(ev); ok {
		key := inv + "|" + ev.MetricName
		g.mu.Lock()
		if _, dup := g.seenMetrics[key]; dup {
			g.deduped++
			g.mu.Unlock()
			g.log.Warnf("telemetry: gate deduped retried metric %q for invocation %s (measured once)", ev.MetricName, inv)
			return false
		}
		g.seenMetrics[key] = struct{}{}
		g.mu.Unlock()
	}
	return true
}

// admitSpan is the span-side boundary decision: config_hash attribution then idempotency on span_id.
func (g *Gate) admitSpan(sp Span) bool {
	if err := spanAttributable(sp); err != nil {
		g.mu.Lock()
		g.rejected++
		g.mu.Unlock()
		g.log.Warnf("telemetry: gate REJECTED span %q (reaches no store): %v", sp.Name, err)
		return false
	}
	if sp.SpanID == "" {
		g.mu.Lock()
		g.rejected++
		g.mu.Unlock()
		g.log.Warnf("telemetry: gate REJECTED span %q: empty span_id", sp.Name)
		return false
	}
	g.mu.Lock()
	if _, dup := g.seenSpans[sp.SpanID]; dup {
		g.deduped++
		g.mu.Unlock()
		g.log.Warnf("telemetry: gate deduped span %s (retry recomputed the same span_id)", sp.SpanID)
		return false
	}
	g.seenSpans[sp.SpanID] = struct{}{}
	g.mu.Unlock()
	return true
}

// invocationID reads the invocation identity a per-call event carries, if any.
func invocationID(ev metricevent.Event) (string, bool) {
	if ev.Dimensions == nil {
		return "", false
	}
	v, ok := ev.Dimensions[AttrInvocationID].(string)
	if !ok || strings.TrimSpace(v) == "" {
		return "", false
	}
	return v, true
}

// spanAttributable enforces that a span is attributable to an exact configuration and run. config_hash
// is the task-2.3 requirement ("config_hash present on every ... span"); run_id and variant_id are the
// minimum lineage that makes the config_hash meaningful. Node/tool spans additionally carry node_id,
// but the run span legitimately has none, so it is not required here.
func spanAttributable(sp Span) error {
	var missing []string
	need := func(key string) {
		v, _ := sp.Attributes[key].(string)
		if strings.TrimSpace(v) == "" {
			missing = append(missing, key)
		}
	}
	need(AttrConfigHash)
	need(AttrRunID)
	need(AttrVariantID)
	if len(missing) > 0 {
		return fmt.Errorf("span missing attribution attributes: %s", strings.Join(missing, ", "))
	}
	// config_hash must be a real hash, not any non-empty string, for it to key a store.
	if ch, _ := sp.Attributes[AttrConfigHash].(string); !isSHA256Hex(ch) {
		return fmt.Errorf("span config_hash is not 64 lowercase hex chars")
	}
	return nil
}

// isSHA256Hex mirrors metricevent's check (kept local so the span rule does not reach into another
// package's unexported helper). 64 lowercase hex chars.
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !isLowerHexDigit(c) {
			return false
		}
	}
	return true
}

func isLowerHexDigit(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}
