package auth

import (
	"net/http"
	"strings"
)

// Compose gates /api/* with API key auth; public paths and non-API routes stay open.
func Compose(reg *Registry, mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsPublicPath(r.URL.Path) {
			mux.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			Middleware(reg, mux).ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
