package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/tenancy"
)

// durable.go is the half of `Registry` that reads the identity store instead of a map built from the
// configuration file at boot.
//
// # Why the map does not simply go away
//
// A deployment with no Postgres still has to authenticate, and every existing test constructs a
// `Registry` from a `config.Config`. So the map path is unchanged and the store path is additive: a
// Registry with no source behaves exactly as it did before P27, and a Registry with one reads the store.
// The BOOT SEED (see `tenancy/seed.go`) is what makes those two the same answer on a durable
// deployment — configuration creates what is missing and never overwrites, so the map and the table
// cannot disagree about a key that exists in both.
//
// # 🔴 There is no positive-result cache, and that is a deliberate correction
//
// P27's NFR1 as drafted asked for two things that cannot both be true across more than one replica:
//
//	"served from a bounded in-process cache with a TTL of at most 60 seconds for positive results"
//	"revocation SHALL NOT be cached — a revoked credential is refused at the next request"
//
// A positive entry cached for up to 60 seconds IS a cached non-revocation. Revoke a credential on
// replica A and replica B keeps accepting it until its own entry expires — so "refused at the next
// request" silently becomes "refused within a minute", and the sentence a customer is asked to rely on
// when they offboard somebody changes meaning without anybody editing it.
//
// The priority law settles it: security is level 1 and implementation cost is level 8, and level 8 does
// not get to push back. So resolution is ONE indexed lookup per authenticated request, with no cache —
// which is exactly what `admin_session` has done on the operator side since P8, for the same reason and
// with the same comment. If that lookup ever becomes a measured bottleneck, the fix is a faster store or
// a shorter-lived credential, not a window in which a revoked key still works.

// CredentialSource is the narrow slice of the identity store this package needs.
//
// It is declared HERE, in the transport layer, so the dependency points from transport to domain and not
// back — and it is narrow so that a future `Registry` cannot quietly grow the ability to write.
type CredentialSource interface {
	// ResolveCredential looks up a credential BY HASH. It returns the record even when revoked, so this
	// package can distinguish "unknown" from "revoked" in its own logs while answering the wire
	// identically.
	ResolveCredential(hash string) (tenancy.Credential, error)
	// GetTenant resolves the organization a credential names, so suspension is enforced at
	// authentication rather than at each feature.
	GetTenant(tenantID string) (tenancy.Tenant, error)
	// ResolveSession resolves a SHORT-LIVED scoped token — the thing the console's BFF exchanges its
	// browser session for. It is a session row rather than a second token mechanism because a session
	// already has everything this needs: an expiry, a revocation, an organization, and a person. And
	// because member removal already revokes sessions, a removed member's console token dies with them
	// without any new code path having to remember to kill it.
	ResolveSession(tokenHash string) (tenancy.Session, error)
}

// RefusalCause is why a lookup failed, for the platform's OWN logs.
//
// 🔴 It never reaches the wire. Unknown, revoked and suspended are one response — a generic
// `unauthorized` — because distinguishing them builds a probing oracle that tells an attacker which half
// they got wrong and helps no real user. The operator, who can read a log, gets the distinction; the
// caller does not.
type RefusalCause string

const (
	RefusalNone      RefusalCause = ""
	RefusalNoKey     RefusalCause = "no_credential_presented"
	RefusalUnknown   RefusalCause = "unknown_credential"
	RefusalRevoked   RefusalCause = "revoked_credential"
	RefusalSuspended RefusalCause = "suspended_organization"
	RefusalStoreDown RefusalCause = "identity_store_unavailable"
)

// WithSource points a Registry at the durable identity store. Returns the same Registry for chaining.
func (r *Registry) WithSource(src CredentialSource) *Registry {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.src = src
	return r
}

// HasSource reports whether this Registry resolves against the durable store.
func (r *Registry) HasSource() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.src != nil
}

// LookupWithCause is Lookup plus the reason, for a caller that logs.
//
// The store path is one lookup, every time. See the file header for why there is no cache.
func (r *Registry) LookupWithCause(apiKey string) (Principal, RefusalCause) {
	if r == nil {
		return Principal{}, RefusalUnknown
	}
	key := trimSpace(apiKey)
	if key == "" {
		return Principal{}, RefusalNoKey
	}

	r.mu.RLock()
	src := r.src
	r.mu.RUnlock()

	if src == nil {
		// Configuration path, byte-for-byte what it was before P27.
		p, ok := r.lookupConfigured(key)
		if !ok {
			return Principal{}, RefusalUnknown
		}
		return p, RefusalNone
	}

	hash := tenancy.HashSecret(key)

	// A short-lived scoped token is tried FIRST, because it is the hot path on a console deployment:
	// every page render presents one, and every API credential presented is a CLI or CI call, which is
	// rarer. The order is a performance choice and nothing else — both lookups are exact-match on an
	// indexed hash and neither can match the other's value.
	if sess, serr := src.ResolveSession(hash); serr == nil {
		return principalFromSession(src, sess)
	} else if !errors.Is(serr, tenancy.ErrNotFound) {
		return Principal{}, RefusalStoreDown
	}

	cred, err := src.ResolveCredential(hash)
	if err != nil {
		if errors.Is(err, tenancy.ErrNotFound) {
			return Principal{}, RefusalUnknown
		}
		// 🔴 A store that cannot be reached is NOT an unknown credential. Collapsing them would make an
		// outage indistinguishable from a bad key in our own logs — and would send every customer to
		// check their credentials during an incident that has nothing to do with them.
		return Principal{}, RefusalStoreDown
	}
	if cred.Revoked() {
		return Principal{}, RefusalRevoked
	}

	tenant, err := src.GetTenant(cred.TenantID)
	if err != nil {
		if errors.Is(err, tenancy.ErrNotFound) {
			// A credential whose organization is gone. The foreign key makes this unreachable through
			// the store's own writes; it is still checked, because "unreachable" is a claim about code
			// that exists today.
			return Principal{}, RefusalUnknown
		}
		return Principal{}, RefusalStoreDown
	}
	if tenant.Status.Suspended() {
		// 🔴 Suspension is enforced HERE, once, rather than at each feature. A per-feature check is a
		// check somebody eventually forgets to add to a new surface.
		return Principal{}, RefusalSuspended
	}

	return Principal{
		TenantID: cred.TenantID,
		Role:     string(cred.Role),
		APIKeyID: cred.CredentialID,
		UserID:   cred.UserID,
	}, RefusalNone
}

// Describe renders a refusal for a log line. It never contains the presented value.
func (c RefusalCause) Describe() string {
	switch c {
	case RefusalNone:
		return "accepted"
	case RefusalNoKey:
		return "no credential presented"
	case RefusalUnknown:
		return "credential is not recognised"
	case RefusalRevoked:
		return "credential has been revoked"
	case RefusalSuspended:
		return "organization is suspended"
	case RefusalStoreDown:
		return "the identity store could not be reached"
	default:
		return fmt.Sprintf("unknown refusal %q", string(c))
	}
}

// principalFromSession resolves a scoped token to a principal.
//
// It applies the SAME two checks a credential gets — expiry/revocation, then organization suspension —
// because a token that outlived either would be a second, weaker way in, and a second way in is how a
// revocation that works on one path stops meaning anything.
func principalFromSession(src CredentialSource, sess tenancy.Session) (Principal, RefusalCause) {
	// 🔴 A CONSOLE session is the browser's cookie, not a credential.
	//
	// The two share a table because they share a shape — an opaque token, an organization, a person, an
	// expiry, a revocation — and they are emphatically not the same thing. Resolving a console cookie
	// here would mean a stolen cookie authenticates against the whole platform API; today it reaches
	// only the console, which holds the platform credential and exposes a closed set of routes.
	//
	// It is refused as UNKNOWN rather than as its own cause: whoever presented it learns nothing, and
	// the only party who could be presenting one is either the console (a bug) or somebody who stole a
	// cookie (who must not be told what they hold).
	if sess.Purpose == tenancy.PurposeConsole {
		return Principal{}, RefusalUnknown
	}
	if !sess.Live(nowMillis()) {
		// Expired and revoked are ONE answer on the wire and two in our logs, for the same reason
		// unknown and revoked are: distinguishing them tells a holder of a stale token which half they
		// got wrong.
		return Principal{}, RefusalRevoked
	}
	tenant, err := src.GetTenant(sess.TenantID)
	if err != nil {
		if errors.Is(err, tenancy.ErrNotFound) {
			return Principal{}, RefusalUnknown
		}
		return Principal{}, RefusalStoreDown
	}
	if tenant.Status.Suspended() {
		return Principal{}, RefusalSuspended
	}
	return Principal{
		TenantID: sess.TenantID,
		// A scoped token carries no ROLE of its own. The role is a property of the membership, which the
		// account surface reads directly — putting one here would be a second copy that a role change
		// would leave stale for the token's whole lifetime.
		Role:     string(tenancy.RoleMember),
		APIKeyID: sess.SessionID,
		UserID:   sess.UserID,
	}, RefusalNone
}

// nowMillis is the clock the session expiry is read against. A package-level function rather than a
// field so the registry stays constructible from configuration alone; the value it compares is written
// by whoever minted the session, in the same unit.
func nowMillis() int64 { return time.Now().UTC().UnixMilli() }
