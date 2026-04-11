package auth

import (
	"strings"
	"sync"

	"github.com/heros-foreal/agentd/internal/config"
)

// Registry maps API keys to principals (loaded from config).
type Registry struct {
	mu   sync.RWMutex
	keys map[string]Principal // bearer / x-api-key value -> principal
}

func NewRegistry(cfg config.Config) *Registry {
	r := &Registry{keys: make(map[string]Principal)}
	for _, t := range cfg.TenantCredentials {
		k := strings.TrimSpace(t.APIKey)
		if k == "" {
			continue
		}
		role := strings.TrimSpace(t.Role)
		if role == "" {
			role = "member"
		}
		r.keys[k] = Principal{
			TenantID: strings.TrimSpace(t.TenantID),
			Role:     role,
			APIKeyID: strings.TrimSpace(t.KeyID),
		}
	}
	return r
}

// Lookup validates a raw API key (constant-time compare not critical for huge maps; use single keys per env).
func (r *Registry) Lookup(apiKey string) (Principal, bool) {
	if r == nil {
		return Principal{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.keys[strings.TrimSpace(apiKey)]
	return p, ok
}

func (r *Registry) HasKeys() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.keys) > 0
}
