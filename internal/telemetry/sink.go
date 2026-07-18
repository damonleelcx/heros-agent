package telemetry

import (
	"context"

	"github.com/heros-foreal/agentd/internal/metricevent"
)

// Sink is where the instrument hands finished telemetry. The production Sink is the Collector (§5),
// which runs the tag-gate / cardinality / scrub pipeline and fans out to the three stores; tests use a
// recording sink.
//
// # Contract: non-blocking and unfailing
//
// A Sink method MUST NOT block the caller and MUST NOT return an error, because the caller is on (or
// one hop off) a paid provider call's path and Decision 7 forbids a telemetry backend's health from
// affecting a run. An implementation that talks to a store does so ASYNC internally and drops or
// buffers on outage; it never propagates failure upward. The instrument additionally guards each call
// with recover() so a buggy Sink cannot panic a run.
type Sink interface {
	EmitMetric(ctx context.Context, ev metricevent.Event)
	EmitSpan(ctx context.Context, sp Span)
}

// Logger is the minimal logging seam. A dropped-because-untagged event or a pricing gap is logged, not
// swallowed (backend rule: a fallback path always carries a WARN). Defaults to a no-op so the zero
// Instrument is usable in a test without wiring logging.
type Logger interface {
	Warnf(format string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Warnf(string, ...any) {}

// safeMetric / safeSpan wrap a Sink call so a panic in a Sink implementation degrades telemetry rather
// than failing the run (Decision 7). The recover is the last line under "never fail a paid run".
func safeMetric(ctx context.Context, s Sink, log Logger, ev metricevent.Event) {
	defer func() {
		if r := recover(); r != nil {
			log.Warnf("telemetry: sink panicked emitting metric %q (dropped): %v", ev.MetricName, r)
		}
	}()
	s.EmitMetric(ctx, ev)
}

func safeSpan(ctx context.Context, s Sink, log Logger, sp Span) {
	defer func() {
		if r := recover(); r != nil {
			log.Warnf("telemetry: sink panicked emitting span %q (dropped): %v", sp.Name, r)
		}
	}()
	s.EmitSpan(ctx, sp)
}
