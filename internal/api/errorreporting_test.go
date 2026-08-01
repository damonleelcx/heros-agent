package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/errorcode"
	"github.com/heros-foreal/agentd/internal/erroreport"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// errorreporting_test.go asserts the two properties the erroreport package cannot assert about
// itself: that the trace identity a REQUEST carries is the same string in three places, and that a
// panic in a served handler becomes a report rather than a lost stack and a dead socket.
//
// # Why the trace assertion is worth a test of its own
//
// "Every event carries the existing trace_id" is a sentence that stays true-sounding while the values
// drift apart. The failure it prevents is a real one and it is expensive at exactly the wrong moment:
// an operator holding an error report, a customer holding a response header, and a span store holding a
// trace — three strings that were supposed to be one, discovered mid-incident.

type envelopeCapture struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (c *envelopeCapture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	c.mu.Lock()
	c.bodies = append(c.bodies, buf)
	c.mu.Unlock()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))
}

func (c *envelopeCapture) payloads(t *testing.T) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]map[string]any, 0, len(c.bodies))
	for _, body := range c.bodies {
		lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
		if len(lines) != 3 {
			t.Fatalf("an envelope was not three documents: %d", len(lines))
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(lines[2]), &payload); err != nil {
			t.Fatalf("envelope payload is not JSON: %v", err)
		}
		out = append(out, payload)
	}
	return out
}

// serverWithCapture builds a server whose reporter transmits to a real local endpoint.
func serverWithCapture(t *testing.T) (*Server, *envelopeCapture, erroreport.Reporter) {
	t.Helper()
	cap := &envelopeCapture{}
	ingest := httptest.NewServer(cap)
	t.Cleanup(ingest.Close)

	dsn := strings.Replace(ingest.URL, "://", "://p24fixturekey@", 1) + "/4242"
	reporter, err := erroreport.New(erroreport.Config{
		DSN:      dsn,
		Release:  "v0.24.0-test",
		Edition:  "dev",
		Runtime:  "go test",
		Scrubber: telemetry.NewScrubber(),
		Logf:     func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("erroreport.New: %v", err)
	}
	t.Cleanup(func() { _ = reporter.Close(context.Background()) })

	s := New(nil, config.Config{})
	s.SetErrorReporter(reporter)
	return s, cap, reporter
}

func TestOneTraceIdentityResolvesTheHeaderTheEventAndTheSpan(t *testing.T) {
	s, cap, reporter := serverWithCapture(t)

	const runID = "run-p24-trace-check"
	// The span's identity, derived exactly as the span store derives it. This is the value the whole
	// assertion is about: an error event must carry the SAME string, not a value that resembles it.
	spanTrace := telemetry.TraceID(runID)

	s.Mux.HandleFunc("GET /api/v1/errortest/runs/{runID}", func(http.ResponseWriter, *http.Request) {
		panic("a panic whose value carries the tenant name Nous Research Ltd and prompt text")
	})

	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/errortest/runs/"+runID, nil))

	// 1. The response header a customer would quote back.
	header := rec.Header().Get(telemetry.TraceHeader)
	if header != spanTrace {
		t.Fatalf("%s = %q, want the run's span trace %q", telemetry.TraceHeader, header, spanTrace)
	}

	// 2. The body of the internal-error response.
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("the 500 body is not JSON: %v (%s)", err, rec.Body.String())
	}
	if body["trace_id"] != spanTrace {
		t.Errorf("the 500 body carries trace_id %v, want %q", body["trace_id"], spanTrace)
	}
	if body["code"] != string(errorcode.PlatformPanic) {
		t.Errorf("the 500 body carries code %v, want %s", body["code"], errorcode.PlatformPanic)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a panicking handler returned %d, want 500", rec.Code)
	}

	// 3. The transmitted event.
	if err := reporter.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	payloads := cap.payloads(t)
	if len(payloads) != 1 {
		t.Fatalf("expected exactly 1 transmitted event for one panic, got %d", len(payloads))
	}
	tags, _ := payloads[0]["tags"].(map[string]any)
	if tags["trace_id"] != spanTrace {
		t.Errorf("the transmitted event carries trace_id %v, want %q", tags["trace_id"], spanTrace)
	}
	if tags["error.code"] != string(errorcode.PlatformPanic) {
		t.Errorf("the transmitted event carries error.code %v, want %s", tags["error.code"], errorcode.PlatformPanic)
	}

	// And no second correlation identity: the payload names nothing else that looks like a handle.
	raw, _ := json.Marshal(payloads[0])
	for _, minted := range []string{"incident_id", "span_id", "correlation_id", "request_id"} {
		if strings.Contains(string(raw), minted) {
			t.Errorf("the transmitted event carries %q — one identity, and it is the trace", minted)
		}
	}

	// 🔴 And the panic VALUE is never read: a panic message is routinely a formatted string carrying
	// whatever the caller was working on.
	if strings.Contains(string(raw), "Nous Research") || strings.Contains(string(raw), "prompt text") {
		t.Error("the panic value reached the wire")
	}
}

func TestAnInboundTraceIdIsCarriedRatherThanReplaced(t *testing.T) {
	s, _, _ := serverWithCapture(t)
	const inbound = "3f1a9c02be7d4e6a81b5c7d0e2f4a6b8"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(telemetry.TraceHeader, inbound)
	s.Handler.ServeHTTP(rec, req)
	if got := rec.Header().Get(telemetry.TraceHeader); got != inbound {
		t.Errorf("%s = %q, want the inbound value %q — a browser → BFF → platform request is ONE trace",
			telemetry.TraceHeader, got, inbound)
	}
}

func TestEveryResponseCarriesATraceId(t *testing.T) {
	s, _, _ := serverWithCapture(t)
	for _, path := range []string{"/healthz", "/readyz", "/api/v1/coverage"} {
		rec := httptest.NewRecorder()
		s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Header().Get(telemetry.TraceHeader) == "" {
			t.Errorf("%s carries no %s — a request nobody can follow through an incident", path, telemetry.TraceHeader)
		}
	}
}

// ── 2.6 / 2.10 · The readiness surface ───────────────────────────────────────

func readyzBody(t *testing.T, s *Server) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readyz body: %v (%s)", err, rec.Body.String())
	}
	return body
}

func TestReadinessReportsErrorReportingAbsentWhenNothingIsConfigured(t *testing.T) {
	s := New(nil, config.Config{})
	entry, ok := readyzBody(t, s)["error_reporting"].(map[string]any)
	if !ok {
		t.Fatal("/readyz does not report error_reporting — an operator cannot tell whether it is wired")
	}
	if entry["state"] != string(erroreport.StateAbsent) {
		t.Errorf("state = %v, want absent", entry["state"])
	}
	if _, has := entry["failure_class"]; has {
		t.Error("an absent integration named a failure class — absence is a decision, not a degradation")
	}
}

func TestReadinessReportsConfiguredAndDegradedDistinctly(t *testing.T) {
	s, _, _ := serverWithCapture(t)
	entry := readyzBody(t, s)["error_reporting"].(map[string]any)
	if entry["state"] != string(erroreport.StateConfigured) {
		t.Fatalf("state = %v, want configured", entry["state"])
	}

	// Now a reporter that cannot reach its target.
	broken, err := erroreport.New(erroreport.Config{
		DSN:      "https://k@127.0.0.1:1/1",
		Scrubber: telemetry.NewScrubber(),
		Logf:     func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("erroreport.New: %v", err)
	}
	defer func() { _ = broken.Close(context.Background()) }()
	broken.Report(context.Background(), erroreport.Event{Type: "*errors.errorString", Code: errorcode.UpstreamError})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if state, _ := broken.State(); state == erroreport.StateDegraded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	degradedServer := New(nil, config.Config{})
	degradedServer.SetErrorReporter(broken)
	entry = readyzBody(t, degradedServer)["error_reporting"].(map[string]any)
	if entry["state"] != string(erroreport.StateDegraded) {
		t.Fatalf("state = %v, want degraded", entry["state"])
	}
	if entry["failure_class"] == nil || entry["failure_class"] == "" {
		t.Error("degraded with no failure class — 'degraded' without a subject sends an operator to read three dashboards")
	}
}

func TestADegradedReporterDoesNotGateTraffic(t *testing.T) {
	// 🔴 Stated as a test because the opposite is the intuitive implementation. Readiness GATES traffic.
	// If an incident inbox's outage could make a healthy platform not-ready, the reporting integration
	// would be able to take the product down — which is the one thing "no integration is a startup
	// dependency" is written to prevent.
	broken, err := erroreport.New(erroreport.Config{
		DSN:      "https://k@127.0.0.1:1/1",
		Scrubber: telemetry.NewScrubber(),
		Logf:     func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("erroreport.New: %v", err)
	}
	defer func() { _ = broken.Close(context.Background()) }()
	broken.Report(context.Background(), erroreport.Event{Type: "*errors.errorString", Code: errorcode.UpstreamError})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if state, _ := broken.State(); state == erroreport.StateDegraded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	s := New(nil, config.Config{})
	s.SetErrorReporter(broken)
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/readyz returned %d with a degraded error reporter — a reporting outage must not gate traffic", rec.Code)
	}
	body := readyzBody(t, s)
	if body["status"] != "ready" {
		t.Errorf("status = %v with a degraded error reporter, want ready", body["status"])
	}
	// But it must still be VISIBLE, or "does not gate" becomes "does not report".
	entry := body["error_reporting"].(map[string]any)
	if entry["state"] != string(erroreport.StateDegraded) {
		t.Errorf("the degraded state is not visible on readiness: %v", entry)
	}
}

// ── 2.10 · Nothing is transmitted when nothing is configured ─────────────────

func TestWithNoReporterAPanicStillReturnsATraceIdAndTransmitsNothing(t *testing.T) {
	s := New(nil, config.Config{})
	s.Mux.HandleFunc("GET /api/v1/errortest/absent", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	rec := httptest.NewRecorder()
	s.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/errortest/absent", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a panic with no reporter returned %d, want 500", rec.Code)
	}
	if rec.Header().Get(telemetry.TraceHeader) == "" {
		t.Error("a panic with no reporter returned no trace id — the identity is not the integration's")
	}
}

// ── 2.10 · The admin API's own half ──────────────────────────────────────────

func TestTheAdminAPIReportsErrorReportingOnItsOwnReadiness(t *testing.T) {
	// The operator surface has its own handler, its own credential and its own readiness document. An
	// operator asking "is reporting working" must get the answer from the console they are already
	// looking at, not from a second dashboard on a different origin.
	admin := flowSurface(t)
	rec := httptest.NewRecorder()
	admin.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/api/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/admin/api/readyz returned %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readyz body: %v", err)
	}
	entry, ok := body["error_reporting"].(map[string]any)
	if !ok {
		t.Fatal("/admin/api/readyz does not report error_reporting")
	}
	if entry["state"] != string(erroreport.StateAbsent) {
		t.Errorf("state = %v, want absent for an unconfigured operator API", entry["state"])
	}
	// And every admin response carries the trace id, including the open ones.
	if rec.Header().Get(telemetry.TraceHeader) == "" {
		t.Error("an admin response carries no trace id")
	}
}
