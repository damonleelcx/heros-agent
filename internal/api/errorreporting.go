package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/heros-foreal/agentd/internal/errorcode"
	"github.com/heros-foreal/agentd/internal/erroreport"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// errorreporting.go wires the error-reporting boundary into the served surface (P24 tasks 2.5,
// 2.6, 2.10).
//
// Three things, and they are separable on purpose:
//
//  1. **One trace identity per request**, installed in the context and returned in `X-Trace-Id`. This
//     is not new identity — it is the identity the span store, the structured log and the console's
//     error rendering already use. What was missing was a place that PUTS it on an ordinary request,
//     so an error event could carry the same string a customer sees.
//  2. **A panic becomes a report, not a lost stack.** Before this, an unrecovered panic in a handler
//     killed the connection and left nothing anywhere except the process's stderr, which on a
//     container is wherever the operator's log shipper happens to point.
//  3. **A three-state readiness entry**, so an operator can tell "we chose not to configure this" from
//     "it is configured and failing" — which a boolean cannot express.

// SetErrorReporter installs the reporter. A server with none reports nothing and says `absent` on
// readiness, which is the correct and expected state on every substrate except the platform's own
// hosted deployment.
func (s *Server) SetErrorReporter(r erroreport.Reporter) { s.errorReporter = r }

// errorReporterState is what `/readyz` says about the integration.
//
// 🔴 A degraded reporter does NOT make the aggregate signal not-ready, and that is a decision rather
// than an oversight. Readiness gates traffic. Letting an incident inbox's outage stop a healthy
// platform from serving would mean the reporting integration can take the product down — the exact
// failure mode "no integration is a startup dependency" exists to prevent. The state is reported, named
// and monitorable; it is not a gate.
func (s *Server) errorReporterState() map[string]any { return reporterState(s.errorReporter) }

// reporterState renders a reporter's three-state entry. Shared by both readiness surfaces, so an
// operator reads the same three words on the platform API and on the admin API.
func reporterState(r erroreport.Reporter) map[string]any {
	if r == nil {
		return map[string]any{"state": string(erroreport.StateAbsent)}
	}
	state, class := r.State()
	out := map[string]any{"state": string(state)}
	if class != "" {
		out["failure_class"] = class
	}
	return out
}

// traceAndReport is the outermost handler wrapper for the platform API.
func (s *Server) traceAndReport(next http.Handler) http.Handler {
	return traceAndReport(func() erroreport.Reporter { return s.errorReporter }, "platform.api", next)
}

// traceAndReport is the shared wrapper both served surfaces use.
//
// ONE implementation, taking the reporter through a getter so it can be installed after the handler is
// built. Two copies of a panic-recovery middleware is two chances for one of them to stop reporting,
// and the copy that stops is the one nobody notices — it is the surface that produces fewer panics.
//
// `surface` is an id from the closed enum, never a path.
func traceAndReport(reporter func() erroreport.Reporter, surface string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := traceIDFor(r)
		w.Header().Set(telemetry.TraceHeader, traceID)
		ctx := telemetry.ContextWithTraceID(r.Context(), traceID)
		r = r.WithContext(ctx)

		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// The panic VALUE is never read. It is the same argument as the error message: a panic
			// value is routinely a formatted string containing whatever the caller was working on.
			// What is transmitted is the classification and the stack.
			if rep := reporter(); rep != nil {
				ev := erroreport.Event{
					Type:    "runtime.panic",
					Code:    errorcode.PlatformPanic,
					Level:   erroreport.LevelFatal,
					Frames:  erroreport.CaptureStack(3),
					TraceID: traceID,
					Surface: surface,
				}
				rep.Report(ctx, ev)
			}
			// The customer gets the trace id and nothing else. `SYS_INTERNAL 500 must carry a trace_id`
			// is an existing rule; the body carries the code and the id, never the panic.
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error":    "internal error",
				"code":     string(errorcode.PlatformPanic),
				"trace_id": traceID,
			})
		}()

		next.ServeHTTP(w, r)
	})
}

// traceIDFor decides the request's trace identity. THREE sources, in a fixed order.
//
//  1. An inbound `X-Trace-Id`. The BFF forwards the identity it already has, so a browser → BFF →
//     platform request is one trace rather than three.
//  2. A RUN-SCOPED path derives the run's own trace, `telemetry.TraceID(run_id)`. This is the case that
//     makes the guarantee worth having: a request that reads or streams a run gets the SAME trace the
//     run's spans carry, so an error event, a span and a log line join without a translation table.
//  3. Otherwise a fresh identity for this request.
//
// The third is not a second correlation identity — it is the FIRST one for a request that had none, and
// it is the value that goes into the response header, the log line and any event. What the design
// forbids is a reporting integration minting its own id ALONGSIDE the trace, which nothing here does.
func traceIDFor(r *http.Request) string {
	if inbound := strings.TrimSpace(r.Header.Get(telemetry.TraceHeader)); inbound != "" {
		return inbound
	}
	if runID := runIDFromPath(r.URL.Path); runID != "" {
		return telemetry.TraceID(runID)
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A failure of the system random source is not a reason to serve without an identity: an
		// unidentified request is one nobody can follow through an incident.
		return "trace-unavailable"
	}
	return hex.EncodeToString(b[:])
}

// runIDFromPath returns the run id in a `/runs/{id}` path, or "".
//
// Written as a segment scan rather than a set of route patterns because the run-scoped routes live in
// four files (`/api/v1/runs/…`, `/api/p25/monitor/runs/…`, and the stream variants) and a rule enforced
// route by route fails the first time somebody adds a route.
func runIDFromPath(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range segments {
		if seg == "runs" && i+1 < len(segments) && segments[i+1] != "" {
			return segments[i+1]
		}
	}
	return ""
}
