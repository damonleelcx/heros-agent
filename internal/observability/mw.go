package observability

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

var requestCount atomic.Uint64
var requestErrors atomic.Uint64
var requestLatencyMicros atomic.Uint64

// Middleware adds X-Request-ID and counts requests (for /metrics).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestCount.Add(1)
		rid := r.Header.Get("X-Request-ID")
		if rid == "" {
			var b [8]byte
			_, _ = rand.Read(b[:])
			rid = hex.EncodeToString(b[:])
		}
		traceID := traceParentOrRequestID(r.Header.Get("traceparent"), rid)
		w.Header().Set("X-Request-ID", rid)
		w.Header().Set("traceparent", traceID)
		lw := &loggedResponseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(lw, r)
		if lw.status >= 400 {
			requestErrors.Add(1)
		}
		requestLatencyMicros.Add(uint64(time.Since(start).Microseconds()))
		log.Printf("heros.http method=%s path=%s status=%d request_id=%s trace_id=%s latency_ms=%d", r.Method, r.URL.Path, lw.status, rid, traceID, time.Since(start).Milliseconds())
	})
}

type loggedResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *loggedResponseWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func traceParentOrRequestID(tp, rid string) string {
	tp = strings.TrimSpace(tp)
	if tp != "" {
		return tp
	}
	rid = strings.TrimSpace(rid)
	if rid == "" {
		rid = "0000000000000000"
	}
	if len(rid) < 16 {
		rid = rid + strings.Repeat("0", 16-len(rid))
	}
	return "00-" + rid + "-" + rid[:16] + "-01"
}

// MetricsHandler exposes minimal Prometheus-style metrics.
func MetricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "# HELP heros_http_requests_total Total HTTP requests seen by observability middleware\n")
	fmt.Fprintf(w, "# TYPE heros_http_requests_total counter\n")
	fmt.Fprintf(w, "heros_http_requests_total %d\n", requestCount.Load())
	fmt.Fprintf(w, "# HELP heros_http_request_errors_total HTTP requests returning 4xx/5xx\n")
	fmt.Fprintf(w, "# TYPE heros_http_request_errors_total counter\n")
	fmt.Fprintf(w, "heros_http_request_errors_total %d\n", requestErrors.Load())
	fmt.Fprintf(w, "# HELP heros_http_request_latency_microseconds_total Aggregate request latency in microseconds\n")
	fmt.Fprintf(w, "# TYPE heros_http_request_latency_microseconds_total counter\n")
	fmt.Fprintf(w, "heros_http_request_latency_microseconds_total %d\n", requestLatencyMicros.Load())
}

func Snapshot() map[string]any {
	total := requestCount.Load()
	errors := requestErrors.Load()
	latMicros := requestLatencyMicros.Load()
	avgMs := 0.0
	if total > 0 {
		avgMs = float64(latMicros) / 1000.0 / float64(total)
	}
	errorRate := 0.0
	if total > 0 {
		errorRate = float64(errors) / float64(total)
	}
	return map[string]any{
		"requests_total":             total,
		"errors_total":               errors,
		"error_rate":                 errorRate,
		"latency_microseconds_total": latMicros,
		"latency_ms_avg":             avgMs,
	}
}

func SLOStatus() map[string]any {
	snap := Snapshot()
	errorRate, _ := snap["error_rate"].(float64)
	latencyMs, _ := snap["latency_ms_avg"].(float64)
	level := "green"
	switch {
	case errorRate >= 0.05 || latencyMs >= 1000:
		level = "red"
	case errorRate >= 0.01 || latencyMs >= 300:
		level = "yellow"
	}
	return map[string]any{
		"level":      level,
		"error_rate": errorRate,
		"latency_ms": latencyMs,
		"targets": map[string]any{
			"error_rate_warn": 0.01,
			"error_rate_fail": 0.05,
			"latency_warn_ms": 300,
			"latency_fail_ms": 1000,
		},
	}
}
