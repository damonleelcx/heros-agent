// Package adminidentity is P8's operator identity: who is allowed to hold an admin session at all,
// how that session is obtained, and how quickly it stops being valid (design Decision 1, FR1/FR2).
//
// # Why this is a separate package from internal/auth
//
// `internal/auth` authenticates a TENANT — a customer principal scoped to one tenant_id. This package
// authenticates an OPERATOR — a principal that crosses tenant boundaries and can halt the autonomous
// fleet. They are two trust domains, and the platform's highest-blast-radius surface must not be
// reachable by anything that came through the customer path.
//
// The separation is structural rather than a convention:
//
//   - The two live in different packages with different principal types, so "a tenant principal used
//     as an admin" is a compile error, not a runtime flag check.
//   - The admin session lives under its OWN context key. There is no code path that promotes an
//     `auth.Principal` — however privileged its tenant-side role string — into an admin session.
//     TestCustomerPrincipalCannotReachAdminCapability walks that path and asserts it dead-ends.
//   - The signing and IdP secrets come from the secrets manager (secrets.go), never from code, git,
//     or telemetry.
//
// # Why sessions are short and revocable rather than long and refreshed
//
// The whole value of an operator credential to an attacker is time. A short TTL plus immediate
// revocation bounds a stolen laptop or a hijacked session to minutes. Authorization therefore reads
// the session store on EVERY request (session.go) instead of trusting a self-contained token's own
// expiry claim: a token that vouches for itself cannot be revoked, and "revocable" that only takes
// effect at the next refresh is not revocable.
package adminidentity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Status is an admin principal's lifecycle state. Deny-by-default applies here too: anything that is
// not explicitly active cannot authenticate.
type Status string

const (
	// StatusActive is an admin who may authenticate.
	StatusActive Status = "active"
	// StatusDisabled is an admin whose access has been withdrawn (offboarded, suspended). A disabled
	// principal cannot obtain a session even with a valid SSO assertion and a verified MFA factor.
	StatusDisabled Status = "disabled"
)

// Principal is an operator identity — the `admin_principal` of the design's data model.
//
// It carries NO tenant_id, and that absence is the point: an admin is not a member of a tenant, so
// there is no field for code to accidentally read as "which tenant is this admin's". Cross-tenant
// scope comes from a capability grant (internal/adminrbac), never from an identity field.
type Principal struct {
	AdminID string `json:"admin_id"`
	// SSOSubject is the stable subject identifier the admin identity provider asserts. It is an
	// opaque handle from the IdP, never a password, a token, or an MFA seed.
	SSOSubject string `json:"sso_subject"`
	// MFAEnrolled records that this principal has an MFA factor registered with the admin IdP.
	// Enrolment is necessary but not sufficient: every authentication must present a freshly VERIFIED
	// factor (authn.go), because an enrolment flag is a property of the account and an attacker holding
	// a stolen SSO assertion would inherit it.
	MFAEnrolled bool      `json:"mfa_enrolled"`
	Status      Status    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// Active reports whether this principal may hold a session.
func (p Principal) Active() bool { return p.Status == StatusActive }

var (
	// ErrUnknownPrincipal means the SSO subject resolves to no admin principal. Returned rather than
	// auto-provisioning: an operator account is created deliberately by a Superadmin, and "the IdP
	// asserted somebody we have never heard of" is a security event, not a signup.
	ErrUnknownPrincipal = errors.New("adminidentity: no admin principal for that SSO subject")
	// ErrPrincipalDisabled means the principal exists but is not active.
	ErrPrincipalDisabled = errors.New("adminidentity: admin principal is not active")
	// ErrNoAdminSession is what every admin capability returns when the caller presents no live admin
	// session. It is the deny-by-default answer at the identity layer, ahead of any role check.
	ErrNoAdminSession = errors.New("adminidentity: no live admin session on this request")
)

// PrincipalStore holds the admin principal directory. It is deliberately small: this package's job is
// identity, and everything about ROLES lives in internal/adminrbac so a principal record can never
// become a second, drifting place where capability is decided.
type PrincipalStore struct {
	mu sync.RWMutex
	// byID and bySubject index the same records. Two indexes rather than a scan because the
	// authentication path looks up by SSO subject on every login and the authorization path looks up
	// by admin_id on every request.
	byID      map[string]Principal
	bySubject map[string]string
	// writer is the optional durable backing (durable.go). Nil means this directory lives only as long
	// as the process — correct for a test or a demo, refused by `adminlaunch` for a federated one.
	writer PrincipalWriter
}

// NewPrincipalStore builds an empty directory.
func NewPrincipalStore() *PrincipalStore {
	return &PrincipalStore{byID: map[string]Principal{}, bySubject: map[string]string{}}
}

// Put inserts or replaces an admin principal.
func (s *PrincipalStore) Put(p Principal) error {
	if strings.TrimSpace(p.AdminID) == "" {
		return errors.New("adminidentity: an admin principal needs an admin_id")
	}
	if strings.TrimSpace(p.SSOSubject) == "" {
		return errors.New("adminidentity: an admin principal needs an SSO subject — admin identity is IdP-issued, never local")
	}
	if p.Status == "" {
		return fmt.Errorf("adminidentity: admin %s has no status — an unset status is not treated as active", p.AdminID)
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Durable first: if it did not persist, it did not happen. The other order produces a principal who
	// exists until the next restart, which is the failure a durable directory exists to remove.
	if s.writer != nil {
		if err := s.writer.PutPrincipal(p); err != nil {
			return fmt.Errorf("adminidentity: persist admin principal %s: %w", p.AdminID, err)
		}
	}
	if prior, ok := s.byID[p.AdminID]; ok && prior.SSOSubject != p.SSOSubject {
		delete(s.bySubject, prior.SSOSubject)
	}
	s.byID[p.AdminID] = p
	s.bySubject[p.SSOSubject] = p.AdminID
	return nil
}

// ByID returns the principal with this admin_id.
func (s *PrincipalStore) ByID(adminID string) (Principal, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[adminID]
	return p, ok
}

// BySubject resolves an IdP subject to its admin principal.
func (s *PrincipalStore) BySubject(subject string) (Principal, error) {
	s.mu.RLock()
	id, ok := s.bySubject[subject]
	p := s.byID[id]
	s.mu.RUnlock()
	if !ok {
		return Principal{}, ErrUnknownPrincipal
	}
	if !p.Active() {
		return Principal{}, fmt.Errorf("%w: %s", ErrPrincipalDisabled, p.AdminID)
	}
	return p, nil
}

// Disable withdraws a principal's access. Existing sessions are NOT implicitly invalidated here —
// the caller revokes them explicitly (SessionStore.RevokeAllFor), because "offboarding silently left
// a live session running" is exactly the failure this package exists to make impossible, and an
// implicit side effect is harder to test than an explicit call.
func (s *PrincipalStore) Disable(adminID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[adminID]
	if !ok {
		return ErrUnknownPrincipal
	}
	// Durable first, for the direction that matters most on this method: an offboarding that persisted
	// nowhere is an operator who is disabled until the pod restarts and then quietly is not.
	if s.writer != nil {
		if err := s.writer.DisablePrincipal(adminID); err != nil {
			return fmt.Errorf("adminidentity: persist disable of admin principal %s: %w", adminID, err)
		}
	}
	p.Status = StatusDisabled
	s.byID[adminID] = p
	return nil
}

// ── Request context ─────────────────────────────────────────────────────────────────────────────

// ctxKey is this package's private context key type. Private, so no other package — including
// internal/auth — can construct a value that lands under it.
type ctxKey int

const sessionCtxKey ctxKey = 1

// WithSession installs a verified admin session on a request context. Only the session verifier
// (session.go Authorize) calls this, so a context carrying an admin session is always one that passed
// a live-session check.
func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, sessionCtxKey, s)
}

// SessionFrom returns the admin session on this context, if any.
//
// This is the ONLY way an admin session is read, and it recognises exactly one type. A tenant
// principal placed on the same context by internal/auth is invisible here no matter what its role
// string says — which is the mechanism behind FR1's "the customer auth path cannot reach admin
// capabilities."
func SessionFrom(ctx context.Context) (Session, bool) {
	v := ctx.Value(sessionCtxKey)
	if v == nil {
		return Session{}, false
	}
	s, ok := v.(Session)
	return s, ok
}

// RequireSession is the identity-layer gate every admin capability calls first. It returns
// ErrNoAdminSession when the request carries no admin session, so "unauthenticated" and "unauthorized"
// stay distinguishable all the way to the console's four render states.
func RequireSession(ctx context.Context) (Session, error) {
	s, ok := SessionFrom(ctx)
	if !ok {
		return Session{}, ErrNoAdminSession
	}
	return s, nil
}
