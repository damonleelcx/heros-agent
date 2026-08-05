package auth

import (
	"net/http"
	"strings"
)

// Middleware enforces API key auth on protected paths.
func Middleware(reg *Registry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := extractAPIKey(r)
		p, ok := reg.Lookup(key)
		if !ok || p.TenantID == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

func extractAPIKey(r *http.Request) string {
	if v := r.Header.Get("X-API-Key"); v != "" {
		return strings.TrimSpace(v)
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// PublicPrefixes are path prefixes that skip auth when mode is required.
var PublicPrefixes = []string{
	"/health",
	"/metrics",
}

// PublicPaths are EXACT paths that skip auth. A prefix would be wrong for these: `/api/v1/device/` as a
// prefix would also open `/api/v1/device/approve`, which is the one step in the flow that must be taken
// by a signed-in person.
//
// 🔴 Device authorization begins before the caller has any credential — that is the entire point of it,
// and `internal/api/deviceauth.go` says so. Both of these are safe unauthenticated for reasons written
// down there: `authorize` accepts one display label that binds nothing, and `token` is guarded by the
// 32 random bytes it was handed.
//
// This list exists because they were NOT here. `auth.Compose` gates everything under `/api/`, so on any
// deployment with `auth_mode=required` — which is every real one — `heros login` could not start a
// device flow at all: it got a flat 401 from the middleware, and the handler it needed never ran.
// The package's own tests could not see it. They drive `s.Mux`, and `Compose` wraps `s.Handler` one
// layer further out, so every device test was calling underneath the thing that was refusing. It was
// found by running the actual CLI against a real deployment, which is the only place the two layers are
// stacked.
var PublicPaths = []string{
	"/api/v1/device/authorize",
	"/api/v1/device/token",
}

func IsPublicPath(path string) bool {
	for _, p := range PublicPaths {
		if path == p {
			return true
		}
	}
	for _, p := range PublicPrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	// Static approval UI (no tenant in browser) — optional open; lock down by putting UI behind reverse proxy.
	if path == "/" || strings.HasPrefix(path, "/static/") {
		return true
	}
	return false
}
