package telemetry

import "context"

// tracecontext.go carries the ONE correlation identity across a request.
//
// # Why this lives in telemetry and not in the error reporter that needed it
//
// P24 added an incident inbox, and adding an incident system is the classic moment a second correlation
// identity appears: the new system mints its own id, and afterwards two systems hold half an incident
// each with no join key. The platform already made this choice and stated it — every metric and trace
// event carries the same identity, because a claim you cannot join to its evidence is not a claim.
//
// So the accessor belongs to the package that OWNS that identity. If the error reporter defined its own
// context key, "the trace id" would be two values that happen to agree, and the day they stop agreeing
// nothing would notice.
//
// # Why it is a plain string and not a span
//
// The value that has to travel is the one an operator types: it is on the span, in the structured log
// line, and in the `X-Trace-Id` response header of an internal-error response. Carrying a span here
// would couple every request path to the span lifecycle for the sake of one field.

// TraceHeader is the response header the trace id is returned in. One spelling, because a header whose
// name is written at three call sites is a header that is `X-Trace-ID` at one of them.
const TraceHeader = "X-Trace-Id"

type traceIDKey struct{}

// ContextWithTraceID returns a context carrying id. An empty id returns ctx unchanged, so a caller
// cannot accidentally install an empty identity that later reads as "there was one".
func ContextWithTraceID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, traceIDKey{}, id)
}

// TraceIDFromContext returns the trace id, or "" when the context carries none.
//
// 🔴 It returns empty rather than minting one. An event with no trace id is honestly uncorrelated;
// an invented one resolves nothing while looking exactly like a value that does, which is worse than
// the gap it fills.
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(traceIDKey{}).(string)
	return id
}
