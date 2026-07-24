package billing

import (
	"context"
	"log"

	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// LogSink is a telemetry.Sink that writes each event to the standard logger. It exists for the demo
// and for a deployment that has not stood up a collector yet.
//
// It is NOT a substitute for the collector: the collector is what runs the tag gate, the cardinality
// filter and the scrubber before anything reaches a store, and a log line skips all three. What it is
// good for is making the revenue taxonomy VISIBLE while wiring a dashboard — the alternative, a demo
// that emits into a void, cannot show that the signals exist at all.
//
// It honours the Sink contract: it never blocks meaningfully and never returns an error, so telemetry
// can never fail a charge.
type LogSink struct{}

// EmitMetric logs one metric event's name, value, and dimensions — never a credential, because nothing
// on a revenue event is one.
func (LogSink) EmitMetric(_ context.Context, ev metricevent.Event) {
	v := 0.0
	if ev.Value != nil {
		v = *ev.Value
	}
	log.Printf("metric %s=%v %s customer=%v period=%v dims=%v", ev.MetricName, v, ev.Unit,
		ev.Dimensions[telemetry.AttrCustomerID], ev.Dimensions[telemetry.AttrBillingPeriod], ev.Dimensions)
}

// EmitSpan logs a span's name. Revenue emits no spans today; the method exists to satisfy the contract.
func (LogSink) EmitSpan(_ context.Context, sp telemetry.Span) { log.Printf("span %s", sp.Name) }
