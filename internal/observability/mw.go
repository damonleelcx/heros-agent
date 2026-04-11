package observability

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync/atomic"
)

var requestCount atomic.Uint64

// Middleware adds X-Request-ID and counts requests (for /metrics).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		rid := r.Header.Get("X-Request-ID")
		if rid == "" {
			var b [8]byte
			_, _ = rand.Read(b[:])
			rid = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", rid)
		next.ServeHTTP(w, r)
	})
}

// MetricsHandler exposes minimal Prometheus-style metrics.
func MetricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "# HELP heros_http_requests_total Total HTTP requests seen by observability middleware\n")
	fmt.Fprintf(w, "# TYPE heros_http_requests_total counter\n")
	fmt.Fprintf(w, "heros_http_requests_total %d\n", requestCount.Load())
}
