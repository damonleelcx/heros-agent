package adminidentity

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// authn.go is the admin login path: a DEDICATED admin identity provider, SSO plus a verified MFA
// factor, and only then a session (FR1, design Decision 1).
//
// # Why MFA is checked here and not left to the IdP
//
// "The IdP requires MFA" is a configuration claim about a system this code does not control, and a
// configuration claim is not an invariant. The assertion therefore carries EVIDENCE of a verified
// factor, signed with a separate key, and this package refuses to issue a session without it. If the
// IdP's MFA policy is ever misconfigured, the platform still denies — which is the direction a
// mistake has to fail in on the surface that can halt the fleet.
//
// # Why the assertion has a freshness bound
//
// A signed assertion with no age limit is a bearer credential valid forever. The bound turns a
// captured assertion into a few-minute replay window instead of a permanent operator key.

// MaxAssertionAge bounds how old an SSO assertion may be when presented. Short, because the exchange
// between the IdP redirect and the console's callback is a network round trip, not a workflow.
const MaxAssertionAge = 2 * time.Minute

var (
	// ErrAssertionInvalid means the assertion failed verification — wrong issuer, bad signature, or
	// stale. One error for all three so probing cannot distinguish them.
	ErrAssertionInvalid = errors.New("adminidentity: admin SSO assertion did not verify")
	// ErrMFARequired means SSO succeeded but no verified MFA factor was presented. This is the FR1
	// denial: a valid SSO assertion alone issues NO session.
	ErrMFARequired = errors.New("adminidentity: a verified MFA factor is required for admin access")
	// ErrMFANotEnrolled means the principal has no MFA factor registered at all.
	ErrMFANotEnrolled = errors.New("adminidentity: admin principal has no enrolled MFA factor")
)

// MFAEvidence is proof that the admin IdP verified a second factor for this subject, at this time.
//
// It carries the factor's NAME and a signature, never the factor's value: no one-time code, no
// WebAuthn private material, nothing that could authenticate anybody if this struct were logged.
type MFAEvidence struct {
	// Factor names the second factor ("totp", "webauthn"). A name, not a secret.
	Factor string `json:"factor"`
	// VerifiedAt is when the IdP verified it.
	VerifiedAt time.Time `json:"verified_at"`
	// Signature is an HMAC over {subject, factor, verified_at} keyed by the MFA signing secret. A
	// SEPARATE key from the SSO signing secret on purpose: compromise of the assertion-signing key
	// alone must not let an attacker mint MFA evidence too.
	Signature string `json:"signature"`
}

// Present reports whether any MFA evidence was supplied at all.
func (m MFAEvidence) Present() bool { return strings.TrimSpace(m.Factor) != "" && m.Signature != "" }

// Assertion is what the admin identity provider returns after an SSO exchange.
type Assertion struct {
	// Issuer identifies the admin IdP. Checked against the configured issuer so an assertion minted by
	// the CUSTOMER identity provider — even a correctly signed one — is refused here.
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
	// IssuedAt bounds replay together with MaxAssertionAge.
	IssuedAt time.Time `json:"issued_at"`
	// MFA is the second-factor evidence. Absent evidence is the FR1 "no MFA ⇒ denied" case.
	MFA MFAEvidence `json:"mfa"`
	// Signature is an HMAC over {issuer, subject, issued_at} keyed by the SSO signing secret.
	Signature string `json:"signature"`
}

// Claims is a verified assertion's usable content.
type Claims struct {
	Subject   string
	MFAFactor string
	IssuedAt  time.Time
}

// ProviderInfo identifies the live admin identity provider for the readiness surface. It names the
// DOOR, never anything behind it — no key, no secret id, no assertion.
type ProviderInfo struct {
	// Kind is the stable machine-readable provider kind.
	Kind string `json:"kind"`
	// Issuer is the configured admin IdP issuer.
	Issuer string `json:"issuer"`
	// TestMode reports that the IdP is running in test mode — the rollout state task 13.4 requires
	// during 8a. It is a health signal precisely because "is the operator console pointed at the real
	// IdP" is a question an operator asks about the box that is misbehaving, now.
	TestMode bool `json:"test_mode"`
}

// Provider kinds. Central enum: a health signal a monitor matches on must not be a literal typed at
// three call sites.
const (
	// ProviderKindHMAC is the signed-assertion provider the platform ships. It is the integration
	// point a SAML/OIDC admin IdP plugs into — the platform verifies signed assertions either way.
	ProviderKindHMAC = "admin-idp-hmac"
)

// IdentityProvider verifies an assertion from the ADMIN identity provider.
//
// Its own interface, separate from anything in internal/auth, so that "admin identity is a different
// trust domain" is expressed in the type system rather than in a comment.
type IdentityProvider interface {
	// Verify checks an assertion and returns its claims. It returns ErrAssertionInvalid for any
	// verification failure and ErrMFARequired when no verified factor is present.
	Verify(ctx context.Context, a Assertion) (Claims, error)
	// Describe names the live provider for /readyz.
	Describe() ProviderInfo
}

// HMACProvider verifies assertions signed with the admin IdP's signing secret, sourced from the
// secrets manager.
type HMACProvider struct {
	issuer   string
	secrets  Secrets
	now      Clock
	testMode bool
}

// HMACProviderConfig configures an HMACProvider.
type HMACProviderConfig struct {
	// Issuer is the admin IdP's issuer identifier. Required: without it, an assertion from any issuer
	// that happens to share a signing key would be accepted.
	Issuer string
	// Secrets supplies the SSO and MFA verification keys.
	Secrets Secrets
	// Now overrides the clock.
	Now Clock
	// TestMode marks the provider as running against a test-mode IdP (rollout 8a).
	TestMode bool
}

// NewHMACProvider builds a provider.
func NewHMACProvider(cfg HMACProviderConfig) (*HMACProvider, error) {
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, errors.New("adminidentity: an admin IdP issuer is required — an unchecked issuer accepts assertions from the customer IdP")
	}
	if cfg.Secrets == nil {
		return nil, errors.New("adminidentity: a secrets source is required to verify admin assertions")
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &HMACProvider{issuer: cfg.Issuer, secrets: cfg.Secrets, now: now, testMode: cfg.TestMode}, nil
}

// Describe names the provider.
func (p *HMACProvider) Describe() ProviderInfo {
	return ProviderInfo{Kind: ProviderKindHMAC, Issuer: p.issuer, TestMode: p.testMode}
}

// Verify implements IdentityProvider.
func (p *HMACProvider) Verify(ctx context.Context, a Assertion) (Claims, error) {
	if a.Issuer != p.issuer {
		return Claims{}, fmt.Errorf("%w: issuer", ErrAssertionInvalid)
	}
	if strings.TrimSpace(a.Subject) == "" {
		return Claims{}, fmt.Errorf("%w: no subject", ErrAssertionInvalid)
	}
	now := p.now()
	age := now.Sub(a.IssuedAt)
	if age < -MaxAssertionAge || age > MaxAssertionAge {
		return Claims{}, fmt.Errorf("%w: outside the freshness window", ErrAssertionInvalid)
	}
	ssoKey, err := p.secrets.SSOSigningKey(ctx)
	if err != nil {
		return Claims{}, err
	}
	if !verifyMAC(ssoKey, AssertionPayload(a), a.Signature) {
		return Claims{}, fmt.Errorf("%w: signature", ErrAssertionInvalid)
	}
	// SSO passed. MFA is checked SECOND and separately, so the "valid SSO, no MFA" case reaches a
	// distinct, testable denial rather than being folded into a generic failure.
	if !a.MFA.Present() {
		return Claims{}, ErrMFARequired
	}
	mfaKey, err := p.secrets.MFASigningKey(ctx)
	if err != nil {
		return Claims{}, err
	}
	if !verifyMAC(mfaKey, MFAPayload(a.Subject, a.MFA), a.MFA.Signature) {
		return Claims{}, fmt.Errorf("%w: MFA evidence did not verify", ErrMFARequired)
	}
	mfaAge := now.Sub(a.MFA.VerifiedAt)
	if mfaAge < -MaxAssertionAge || mfaAge > MaxAssertionAge {
		return Claims{}, fmt.Errorf("%w: MFA evidence is stale", ErrMFARequired)
	}
	return Claims{Subject: a.Subject, MFAFactor: a.MFA.Factor, IssuedAt: a.IssuedAt}, nil
}

// AssertionPayload is the canonical byte string an assertion signature covers. Exported because the
// admin IdP side (and the test-mode fixture that stands in for it) must sign exactly what this side
// verifies — two spellings of "canonical" is the classic way a signature check becomes decorative.
func AssertionPayload(a Assertion) []byte {
	return []byte(strings.Join([]string{"v1", a.Issuer, a.Subject, a.IssuedAt.UTC().Format(time.RFC3339Nano)}, "\n"))
}

// MFAPayload is the canonical byte string MFA evidence signs over.
func MFAPayload(subject string, m MFAEvidence) []byte {
	return []byte(strings.Join([]string{"v1", subject, m.Factor, m.VerifiedAt.UTC().Format(time.RFC3339Nano)}, "\n"))
}

// Sign produces the HMAC hex a signer places in Assertion.Signature / MFAEvidence.Signature. It lives
// here so the IdP-side fixture cannot drift from the verifier.
func Sign(key, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func verifyMAC(key, payload []byte, sig string) bool {
	want := Sign(key, payload)
	return subtle.ConstantTimeCompare([]byte(want), []byte(sig)) == 1
}

// ── Authenticator ───────────────────────────────────────────────────────────────────────────────

// Authenticator is the one entry point to an admin session: verify the assertion, require MFA,
// resolve the admin principal, issue a short-TTL session.
//
// There is deliberately no other constructor for a Session outside this package, and none at all that
// skips MFA. "No admin capability is reachable without an authenticated, MFA-verified admin session"
// is then a property of the only door, not of every caller remembering to knock.
type Authenticator struct {
	provider   IdentityProvider
	principals *PrincipalStore
	sessions   *SessionStore
	observer   Observer
	now        Clock
}

// NewAuthenticator wires the login path.
func NewAuthenticator(provider IdentityProvider, principals *PrincipalStore, sessions *SessionStore, observer Observer) (*Authenticator, error) {
	if provider == nil || principals == nil || sessions == nil {
		return nil, errors.New("adminidentity: an authenticator needs an admin IdP, a principal store and a session store")
	}
	return &Authenticator{
		provider: provider, principals: principals, sessions: sessions, observer: observer,
		now: func() time.Time { return time.Now().UTC() },
	}, nil
}

// Authenticate performs the SSO + MFA exchange and issues a session on success.
//
// Every denial path emits an event before returning, so "no MFA ⇒ denied AND logged" holds without a
// caller remembering to log it.
func (a *Authenticator) Authenticate(ctx context.Context, assertion Assertion) (Session, string, error) {
	claims, err := a.provider.Verify(ctx, assertion)
	if err != nil {
		kind := EventLoginDeniedBadAssertion
		if errors.Is(err, ErrMFARequired) {
			kind = EventLoginDeniedNoMFA
		}
		a.emit(Event{Kind: kind, SSOSubject: assertion.Subject, Detail: err.Error(), At: a.now()})
		return Session{}, "", err
	}
	p, err := a.principals.BySubject(claims.Subject)
	if err != nil {
		a.emit(Event{Kind: EventLoginDeniedPrincipal, SSOSubject: claims.Subject, Detail: err.Error(), At: a.now()})
		return Session{}, "", err
	}
	if !p.MFAEnrolled {
		// The IdP produced factor evidence for a principal the platform does not believe is enrolled.
		// That disagreement is a security event, not a login: deny and say which side is inconsistent.
		a.emit(Event{Kind: EventLoginDeniedNoMFA, AdminID: p.AdminID, SSOSubject: claims.Subject,
			Detail: "principal is not MFA-enrolled on the platform side", At: a.now()})
		return Session{}, "", ErrMFANotEnrolled
	}
	return sessionAndToken(a.sessions.Issue(ctx, p, claims.MFAFactor))
}

// Describe reports the live admin IdP for the readiness surface.
func (a *Authenticator) Describe() ProviderInfo { return a.provider.Describe() }

func (a *Authenticator) emit(ev Event) {
	if a.observer != nil {
		a.observer.AdminIdentityEvent(ev)
	}
}

// sessionAndToken exists only to keep Authenticate's success path a single expression; it adds no
// behaviour.
func sessionAndToken(s Session, token string, err error) (Session, string, error) {
	return s, token, err
}
