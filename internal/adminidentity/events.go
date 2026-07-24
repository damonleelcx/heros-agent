package adminidentity

import (
	"sync"
	"time"
)

// events.go is the identity layer's observation surface. Every authentication outcome — issued,
// denied, and every session verification that failed — is emitted here.
//
// It exists as its own narrow interface rather than a direct dependency on the audit log or the
// telemetry substrate for a specific reason: a FAILED login has no admin session and therefore no
// actor to attribute an audit entry to, but it is still the single most important signal on this
// surface ("somebody is trying the operator door"). Making it an interface lets the audit log
// (internal/adminaudit) and the P2.5 telemetry sink (task 13.1) both subscribe without this package
// depending on either.

// EventKind names one identity-layer outcome. Central enum, never a literal at a call site.
type EventKind string

const (
	// EventLoginIssued is a successful SSO + MFA authentication that issued a session.
	EventLoginIssued EventKind = "admin_login_issued"
	// EventLoginDeniedNoMFA is an SSO assertion presented without verified MFA evidence.
	EventLoginDeniedNoMFA EventKind = "admin_login_denied_no_mfa"
	// EventLoginDeniedBadAssertion is an assertion that failed IdP verification (bad signature, wrong
	// issuer, stale).
	EventLoginDeniedBadAssertion EventKind = "admin_login_denied_bad_assertion"
	// EventLoginDeniedPrincipal is a verified assertion for a subject that is unknown or not active.
	EventLoginDeniedPrincipal EventKind = "admin_login_denied_principal"
	// EventSessionDeniedExpired is a request presenting a session whose TTL has elapsed.
	EventSessionDeniedExpired EventKind = "admin_session_denied_expired"
	// EventSessionDeniedRevoked is a request presenting a revoked session.
	EventSessionDeniedRevoked EventKind = "admin_session_denied_revoked"
	// EventSessionDeniedUnknown is a request presenting a session token that does not verify or names
	// no known session — the shape a forged or replayed token takes.
	EventSessionDeniedUnknown EventKind = "admin_session_denied_unknown"
	// EventSessionRevoked records an explicit revocation.
	EventSessionRevoked EventKind = "admin_session_revoked"
)

// Event is one identity-layer outcome.
//
// It carries identifiers and never credential material: no assertion body, no MFA code, no session
// token, no signing key. The scrubber-by-construction rule for this surface is that there is no field
// here to put a secret in (task 13.2).
type Event struct {
	Kind EventKind `json:"kind"`
	// AdminID is the resolved admin principal, empty when the attempt never resolved to one.
	AdminID string `json:"admin_id,omitempty"`
	// SSOSubject is the asserted subject. An IdP subject handle, not a credential — it is what makes a
	// repeated failed-login pattern attributable at all.
	SSOSubject string `json:"sso_subject,omitempty"`
	// SessionID identifies the session for session-scoped events.
	SessionID string `json:"session_id,omitempty"`
	// Detail is a short non-sensitive reason ("mfa factor not verified"). Never a value.
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

// Observer receives identity-layer events. Implementations must not block: this is on the login and
// per-request authorization path.
type Observer interface {
	AdminIdentityEvent(ev Event)
}

// ObserverFunc adapts a function to Observer.
type ObserverFunc func(ev Event)

// AdminIdentityEvent implements Observer.
func (f ObserverFunc) AdminIdentityEvent(ev Event) { f(ev) }

// MemoryObserver records events in memory. It is the fixture the tests assert "the failed attempt is
// logged" against, and the demo's live event feed — not a stand-in for the production sink, which is
// the P2.5 substrate (task 13.1).
type MemoryObserver struct {
	mu     sync.Mutex
	events []Event
}

// NewMemoryObserver builds an empty recorder.
func NewMemoryObserver() *MemoryObserver { return &MemoryObserver{} }

// AdminIdentityEvent implements Observer.
func (m *MemoryObserver) AdminIdentityEvent(ev Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, ev)
}

// Events returns a copy of everything recorded.
func (m *MemoryObserver) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

// Count returns how many events of a kind were recorded.
func (m *MemoryObserver) Count(kind EventKind) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// observers fans one event out to several sinks. Used so the audit log and the telemetry substrate can
// both observe without either wrapping the other.
type observers []Observer

func (o observers) AdminIdentityEvent(ev Event) {
	for _, s := range o {
		if s != nil {
			s.AdminIdentityEvent(ev)
		}
	}
}

// MultiObserver fans events out to every non-nil sink.
func MultiObserver(sinks ...Observer) Observer { return observers(sinks) }
