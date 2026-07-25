package configresolver

import (
	"encoding/json"
	"net/http"
)

// HealthHandler serves the resolver's degraded state on a readable endpoint (task 8.3). A resolver
// quietly serving stale configuration is worse than the outage it avoided, so the state is exposed
// here — not left to be inferred from the configuration's contents. Mount it at e.g. GET
// /internal/config-resolver/health.
//
// It always returns 200: "degraded" is a reported operational state, not an HTTP error. A monitor
// reads the `degraded` field, not the status code — the same reason a build-rejected transform is a
// 200 with a status body elsewhere in this codebase.
func HealthHandler(r *Resolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Header().Set("cache-control", "no-store")
		_ = json.NewEncoder(w).Encode(r.Health())
	})
}
