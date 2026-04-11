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

func IsPublicPath(path string) bool {
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
