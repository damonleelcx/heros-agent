package adminidentity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// session.go implements `admin_session`: short TTL, immediately revocable, and verified against the
// STORE on every request (FR2, design Decision 1).
//
// # Why the store is consulted on every request
//
// A self-contained signed token that vouches for its own expiry cannot be revoked — the holder keeps
// using it until it expires on its own. "Immediately revocable" then quietly degrades into "revocable
// at the next refresh", which is not what the requirement says and not what an incident needs. So the
// signature proves the token was minted here, and the STORE decides whether the session is still
// alive. Both are required: signature alone lets a revoked token through, store alone lets a guessed
// session id through.
//
// # Why there is no grace period
//
// The spec says denied at the NEXT request, no grace. A grace window is a fixed amount of time during
// which a compromised session still works, granted for the operator convenience of not being logged
// out mid-form. On this surface that trade runs the wrong way (L1 安全 over L3/L8), so the code has no
// grace parameter at all — there is nothing to set to a non-zero value by accident.

// DefaultTTL is how long an admin session lives.
//
// Short enough that a stolen session is measured in minutes, long enough that an operator working an
// incident is not re-authenticating mid-action. It is a default rather than a constant because a
// deployment may tighten it; nothing may lengthen it silently, which is why Config validates.
const DefaultTTL = 30 * time.Minute

// MaxTTL is the ceiling a deployment may configure. A "short-lived session" that a config typo turned
// into a week is the failure this bound exists to make impossible.
const MaxTTL = 2 * time.Hour

var (
	// ErrSessionExpired means the session's TTL elapsed. Distinct from revoked so the console can say
	// "your session expired, sign in again" rather than implying an administrative action.
	ErrSessionExpired = errors.New("adminidentity: admin session expired")
	// ErrSessionRevoked means the session was revoked and is denied at the next request, no grace.
	ErrSessionRevoked = errors.New("adminidentity: admin session revoked")
	// ErrSessionUnknown means the token does not verify, or names no session this store issued. It is
	// the answer a forged or replayed token gets, and it is deliberately indistinguishable between
	// those cases so probing learns nothing.
	ErrSessionUnknown = errors.New("adminidentity: admin session not recognised")
)

// Session is one live operator session — the design's `admin_session`.
//
// It carries no role. Roles are resolved LIVE by internal/adminrbac at authorization time, so
// revoking a role takes effect on the next request rather than at the next login; a role baked into
// the session would be a stale capability snapshot with a TTL-long lifetime.
type Session struct {
	SessionID string    `json:"session_id"`
	AdminID   string    `json:"admin_id"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// RevokedAt is set when the session was revoked. Zero means live.
	RevokedAt time.Time `json:"revoked_at,omitempty"`
	// MFAFactor names the factor verified at login (totp, webauthn). The factor NAME, never its
	// value — it is what lets an auditor tell a hardware-key login from a one-time-code login.
	MFAFactor string `json:"mfa_factor,omitempty"`
}

// Live reports whether the session is unexpired and unrevoked at now.
func (s Session) Live(now time.Time) bool {
	return s.RevokedAt.IsZero() && now.Before(s.ExpiresAt)
}

// Clock supplies the current time. Injected so the expiry tests advance time rather than sleep — a
// test that sleeps for a TTL is a test nobody runs.
type Clock func() time.Time

// SessionStore issues, verifies and revokes admin sessions.
//
// The in-memory map is the store's index; the durable shape is the `admin_session` table in the P8
// migration. Both answer the same question — is this session id live right now — and the interface
// below is what a persistent implementation satisfies.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]Session
	// byAdmin lets an offboarding revoke every session a principal holds without scanning.
	byAdmin map[string]map[string]struct{}

	ttl      time.Duration
	now      Clock
	secrets  Secrets
	observer Observer
	// principals is the RECONCILE READ for offboarding (P22 task 6.3, event-write-reconcile-read).
	//
	// `Disable` and `RevokeAllFor` are two writes, and "A must be accompanied by B" held only by
	// convention is an invariant one hurried call site breaks. So authorization also asks whether the
	// principal is still active, on the path every admin request must pass through. Offboarding then
	// takes effect even if somebody disables a principal and forgets to revoke — the sessions are dead
	// at the next request either way.
	//
	// Optional so a unit test that only exercises session mechanics need not build a directory; the
	// wired console always supplies one, and `Offboard` is the explicit door that does both writes.
	principals *PrincipalStore
}

// SessionConfig configures a store.
type SessionConfig struct {
	// TTL is the session lifetime. Zero uses DefaultTTL; anything above MaxTTL is rejected.
	TTL time.Duration
	// Now overrides the clock. Nil uses time.Now().UTC().
	Now Clock
	// Secrets supplies the session signing key. Required — an unsigned session token would let any
	// caller mint a session id.
	Secrets Secrets
	// Observer receives session-denial events. Nil records nothing, which is acceptable only in a
	// unit test that asserts something else; the wired console always passes one.
	Observer Observer
	// Principals lets Authorize deny a disabled principal's live sessions. See the field comment.
	Principals *PrincipalStore
}

// NewSessionStore builds a store.
func NewSessionStore(cfg SessionConfig) (*SessionStore, error) {
	if cfg.Secrets == nil {
		return nil, errors.New("adminidentity: a secrets source is required to sign admin sessions")
	}
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if ttl <= 0 || ttl > MaxTTL {
		return nil, fmt.Errorf("adminidentity: session TTL %s is outside the permitted range (0, %s]", ttl, MaxTTL)
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &SessionStore{
		sessions:   map[string]Session{},
		byAdmin:    map[string]map[string]struct{}{},
		ttl:        ttl,
		now:        now,
		secrets:    cfg.Secrets,
		observer:   cfg.Observer,
		principals: cfg.Principals,
	}, nil
}

// TTL reports the configured session lifetime, so the console can show an operator how long they have
// rather than guessing.
func (s *SessionStore) TTL() time.Duration { return s.ttl }

// Issue mints a session for an authenticated admin principal and returns the session plus its bearer
// token. Only authn.go calls this — a session is never issued without a verified SSO + MFA outcome.
func (s *SessionStore) Issue(ctx context.Context, p Principal, mfaFactor string) (Session, string, error) {
	if !p.Active() {
		return Session{}, "", fmt.Errorf("%w: %s", ErrPrincipalDisabled, p.AdminID)
	}
	key, err := s.secrets.SessionSigningKey(ctx)
	if err != nil {
		// Fail closed: with no signing key we cannot mint a token anybody could later verify, and
		// issuing an unsigned one would be worse than issuing none.
		return Session{}, "", err
	}
	id, err := newSessionID()
	if err != nil {
		return Session{}, "", err
	}
	now := s.now()
	sess := Session{
		SessionID: id,
		AdminID:   p.AdminID,
		IssuedAt:  now,
		ExpiresAt: now.Add(s.ttl),
		MFAFactor: mfaFactor,
	}
	s.mu.Lock()
	s.sessions[id] = sess
	if s.byAdmin[p.AdminID] == nil {
		s.byAdmin[p.AdminID] = map[string]struct{}{}
	}
	s.byAdmin[p.AdminID][id] = struct{}{}
	s.mu.Unlock()

	s.emit(Event{Kind: EventLoginIssued, AdminID: p.AdminID, SSOSubject: p.SSOSubject, SessionID: id, At: now})
	return sess, signToken(key, id), nil
}

// Authorize verifies a bearer token and returns the live session it names.
//
// This is the function every admin request runs first. It denies — with no grace — an expired
// session, a revoked session, and a token that does not verify, and it logs each denial.
func (s *SessionStore) Authorize(ctx context.Context, token string) (Session, error) {
	key, err := s.secrets.SessionSigningKey(ctx)
	if err != nil {
		// Fail closed: if the signing key cannot be sourced we cannot distinguish a genuine token from
		// a forged one, so no session is authorized.
		return Session{}, err
	}
	id, ok := verifyToken(key, token)
	if !ok {
		s.emit(Event{Kind: EventSessionDeniedUnknown, Detail: "token signature did not verify", At: s.now()})
		return Session{}, ErrSessionUnknown
	}
	s.mu.RLock()
	sess, found := s.sessions[id]
	s.mu.RUnlock()
	if !found {
		s.emit(Event{Kind: EventSessionDeniedUnknown, SessionID: id, Detail: "no such session", At: s.now()})
		return Session{}, ErrSessionUnknown
	}
	now := s.now()
	if !sess.RevokedAt.IsZero() {
		s.emit(Event{Kind: EventSessionDeniedRevoked, AdminID: sess.AdminID, SessionID: id, At: now})
		return Session{}, ErrSessionRevoked
	}
	if !now.Before(sess.ExpiresAt) {
		s.emit(Event{Kind: EventSessionDeniedExpired, AdminID: sess.AdminID, SessionID: id, At: now})
		return Session{}, ErrSessionExpired
	}
	if s.principals != nil {
		// The reconcile read. A disabled principal holds no live session, whether or not anybody
		// remembered to revoke — which is what makes "disable ⇒ revoked" an invariant rather than a
		// two-step procedure somebody performs correctly most of the time.
		if p, ok := s.principals.ByID(sess.AdminID); !ok || !p.Active() {
			s.emit(Event{Kind: EventSessionDeniedRevoked, AdminID: sess.AdminID, SessionID: id,
				Detail: "the principal is no longer active", At: now})
			return Session{}, ErrSessionRevoked
		}
	}
	return sess, nil
}

// Offboard disables a principal AND revokes every session it holds, in that order.
//
// # Why the order, and why this exists beside the two primitives
//
// Disable first: between the two writes, a principal that is already disabled cannot obtain a NEW
// session while the old ones are being revoked. Revoking first would leave a window in which the
// operator being offboarded can sign in again.
//
// It exists as one function because P22 task 6.3 makes "disable ⇒ revoke all sessions" a requirement,
// and a requirement expressed as two calls is a requirement somebody half-performs. `Disable` and
// `RevokeAllFor` remain, because an incident responder revoking sessions without withdrawing access is
// a real and different action.
func (s *SessionStore) Offboard(adminID, byAdminID string) (int, error) {
	if s.principals == nil {
		return 0, errors.New("adminidentity: offboarding needs the principal directory — wire SessionConfig.Principals")
	}
	if err := s.principals.Disable(adminID); err != nil {
		return 0, err
	}
	return s.RevokeAllFor(adminID, byAdminID), nil
}

// Revoke ends a session immediately. The next request presenting it is denied.
func (s *SessionStore) Revoke(sessionID, byAdminID string) error {
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return ErrSessionUnknown
	}
	if sess.RevokedAt.IsZero() {
		sess.RevokedAt = s.now()
		s.sessions[sessionID] = sess
	}
	s.mu.Unlock()
	s.emit(Event{
		Kind: EventSessionRevoked, AdminID: sess.AdminID, SessionID: sessionID,
		Detail: "revoked by " + byAdminID, At: s.now(),
	})
	return nil
}

// RevokeAllFor ends every session an admin principal holds. This is what offboarding calls, and what
// an incident responder calls on a suspected compromise.
func (s *SessionStore) RevokeAllFor(adminID, byAdminID string) int {
	s.mu.Lock()
	ids := make([]string, 0, len(s.byAdmin[adminID]))
	for id := range s.byAdmin[adminID] {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	n := 0
	for _, id := range ids {
		if err := s.Revoke(id, byAdminID); err == nil {
			n++
		}
	}
	return n
}

// Lookup returns a session by id without verifying a token. Used by the console's own session
// introspection ("how long do I have left"), never as an authorization path.
func (s *SessionStore) Lookup(sessionID string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[sessionID]
	return sess, ok
}

func (s *SessionStore) emit(ev Event) {
	if s.observer != nil {
		s.observer.AdminIdentityEvent(ev)
	}
}

// ── Token minting and verification ──────────────────────────────────────────────────────────────

// tokenSeparator splits the session id from its signature. A single reserved byte that cannot appear
// in a hex session id, so parsing is unambiguous.
const tokenSeparator = "."

// newSessionID draws a 256-bit random identifier. Random rather than sequential: a guessable session
// id turns "verify the signature" into the only barrier, and defense in depth means neither half is
// permitted to be the weak one.
func newSessionID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("adminidentity: cannot draw a session id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func signToken(key []byte, sessionID string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(sessionID))
	return sessionID + tokenSeparator + hex.EncodeToString(mac.Sum(nil))
}

// verifyToken checks a token's signature in constant time and returns the session id it names.
func verifyToken(key []byte, token string) (string, bool) {
	id, sig, ok := strings.Cut(token, tokenSeparator)
	if !ok || id == "" || sig == "" {
		return "", false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id))
	want := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return "", false
	}
	return id, true
}

// Sessions returns every session the store holds, newest first.
//
// It exists for the operator oversight surface (P26 task 6.1): "which factor authenticated this
// session, and when" is a question a reviewer must be able to answer without inferring it from a login
// event, and inferring it is what a reviewer does when the surface does not say.
//
// 🔴 It returns [Session] values, which carry no token and no factor VALUE — the factor NAME only.
// The bearer token is never stored: `Issue` returns it once and the store keeps the session id. So
// there is no shape of this method that could leak a credential, which is why it is a plain listing
// rather than a redacting projection somebody has to remember to keep correct.
func (s *SessionStore) Sessions() []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		out = append(out, sess)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IssuedAt.Equal(out[j].IssuedAt) {
			return out[i].SessionID < out[j].SessionID
		}
		return out[i].IssuedAt.After(out[j].IssuedAt)
	})
	return out
}
