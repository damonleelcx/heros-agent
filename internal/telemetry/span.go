package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// SpanKind names the three levels of the run trace (Requirement: "one run span, one span per node
// execution, and tool calls as child spans").
type SpanKind string

const (
	SpanKindRun  SpanKind = "run"
	SpanKindNode SpanKind = "node"
	SpanKindTool SpanKind = "tool"
)

// SpanStatus is the OTel span status. A failed provider call sets Error so an operator drilling the
// trace sees which node broke, not just that the run did.
type SpanStatus string

const (
	SpanStatusUnset SpanStatus = "unset"
	SpanStatusOK    SpanStatus = "ok"
	SpanStatusError SpanStatus = "error"
)

// Span is one node in the OTel trace, modeled to the GenAI conventions. Attributes carry the seven
// tags plus the gen_ai.* keys and the high-cardinality identifiers (invocation_id, attempt_group) that
// are span attributes precisely because they must NOT be TSDB labels (Decision 4).
//
// IDs are DETERMINISTIC, derived from the run/node coordinates (see TraceID/RunSpanID/NodeSpanID). That
// is what makes span emission idempotent under P2's retry model (Decision 8): a retried invocation
// recomputes the SAME span_id, so the span store overwrites rather than duplicates. Random IDs would
// make every retry a new span and inflate the very trace an operator reads to understand the run.
type Span struct {
	TraceID      string
	SpanID       string
	ParentSpanID string // empty for the run span (the trace root)
	Name         string
	Kind         SpanKind
	StartTime    time.Time
	EndTime      time.Time
	Attributes   map[string]any
	Status       SpanStatus
	StatusMsg    string
}

// Duration is the span's wall-clock span. Zero if it has not ended.
func (s Span) Duration() time.Duration {
	if s.EndTime.IsZero() || s.StartTime.IsZero() {
		return 0
	}
	return s.EndTime.Sub(s.StartTime)
}

// deriveID is the one hashing primitive span/trace ids are built from. Domain-separated so a trace id
// and a span id taken over the same run_id can never collide, and truncated to the OTel id widths
// (16-byte trace, 8-byte span) so the output is a valid OTLP id, not a 32-byte string a real exporter
// would reject.
func deriveID(domain string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(domain))
	for _, p := range parts {
		h.Write([]byte{0}) // separator, so ("a","bc") and ("ab","c") differ
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TraceID is the trace all of a run's spans share — derived from run_id, so the whole run is one
// drillable trace (Requirement: "drillable per-run in the span store"). 16 bytes = 32 hex chars, the
// OTel trace-id width.
func TraceID(runID string) string { return deriveID("heros.trace.v1", runID)[:32] }

// RunSpanID is the root span of the trace. 8 bytes = 16 hex chars, the OTel span-id width.
func RunSpanID(runID string) string { return deriveID("heros.span.run.v1", runID)[:16] }

// RequestSpanID is the root span of a REQUEST that is not a run — a console read, say.
//
// 🔴 Its own derivation rather than reusing RunSpanID with a trace id in place of a run id. The domain
// separator is what stops a request span and a run span colliding, and passing a non-run key to a
// function named for runs is how the two eventually do: the value would still be a valid span id, the
// join would still look correct, and the first sign of trouble would be two unrelated spans sharing a
// parent in a trace viewer.
//
// P37 §5.5 requires every WARN and ERROR on the axis-read paths to carry `request_id`, `trace_id` and
// `span_id`, and those paths have no run to derive one from.
func RequestSpanID(traceID string) string { return deriveID("heros.span.request.v1", traceID)[:16] }

// NodeSpanID is one node execution's span, keyed on the idempotency identity so a retry reuses it.
func NodeSpanID(idempotencyKey string) string {
	return deriveID("heros.span.node.v1", idempotencyKey)[:16]
}

// ToolSpanID is a tool call's child span under a node, keyed on the node identity plus the tool and its
// index so two calls to the same tool in one node are distinct but each is stable across a retry.
func ToolSpanID(idempotencyKey, toolName string, index int) string {
	return deriveID("heros.span.tool.v1", idempotencyKey, toolName, itoa(index))[:16]
}

// itoa avoids strconv just for span-id derivation; span indices are small non-negative ints.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
