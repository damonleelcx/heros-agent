package auth

import (
	"strings"
	"sync"

	"github.com/heros-foreal/agentd/internal/config"
)

// Registry resolves a presented credential to a principal.
//
// It has two paths and they are not alternatives so much as two ages of the same idea. The MAP is what
// shipped before P27: `cfg.TenantCredentials`, read once at boot, which is why onboarding a customer was
// a deploy. The SOURCE is the durable identity store, and on a deployment that has one the boot seed
// (`tenancy.Seed`) writes the configured credentials into it, so the two answer identically for every
// key that exists in both.
//
// A Registry with no source behaves exactly as it did before P27. That is what keeps every existing
// deployment, and every existing test, working unchanged.
type Registry struct {
	mu   sync.RWMutex
	keys map[string]Principal // bearer / x-api-key value -> principal (configuration path)
	src  CredentialSource     // durable path; nil means configuration-only
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

// Lookup validates a raw API key.
//
// When a durable source is wired it is ONE indexed lookup, every time, with no positive-result cache —
// see `durable.go`'s header for why a cache and "revoked at the next request" cannot both be true.
func (r *Registry) Lookup(apiKey string) (Principal, bool) {
	p, cause := r.LookupWithCause(apiKey)
	return p, cause == RefusalNone
}

// lookupConfigured is the pre-P27 path, unchanged.
func (r *Registry) lookupConfigured(apiKey string) (Principal, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.keys[apiKey]
	return p, ok
}

// ConfiguredCredentials returns the configuration-path entries, for the boot seed to write into the
// durable store. It returns the KEY as well, which is the one place this package hands a secret back —
// the seed hashes it immediately and nothing else calls this.
func (r *Registry) ConfiguredCredentials() map[string]Principal {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Principal, len(r.keys))
	for k, v := range r.keys {
		out[k] = v
	}
	return out
}

func (r *Registry) HasKeys() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.keys) > 0 || r.src != nil
}

func trimSpace(s string) string { return strings.TrimSpace(s) }
