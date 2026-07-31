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

	// Token is the raw federated credential a REAL provider verifies (P22 task 6.1): an OIDC ID token,
	// or a base64 SAML Response.
	//
	// # Why the seam grew a field instead of a second interface
	//
	// `Verify(ctx, Assertion) (Claims, error)` is the contract every P8 caller is written against, and
	// ADR-008's whole argument is that the mechanism must be a replaceable implementation of ONE
	// function rather than a shape the callers learn. A second interface for "the real ones" would be
	// exactly the leak that argument forbids. So the struct carries the material each provider kind
	// needs, and each kind reads only its own: `HMACProvider` ignores `Token` entirely, and the OIDC
	// and SAML providers ignore `Signature` and the IdP's `MFA` claim.
	//
	// It is never logged. `Event` has no field it could travel in, which is the property that survives
	// a debugging session.
	Token string `json:"-"`
	// Nonce binds the token to the browser flow the console began, when the console supplies one. A
	// provider that is given a nonce and receives a token without it refuses.
	Nonce string `json:"-"`
	// Factor is the SECOND FACTOR the operator presented to the PLATFORM (P22 task 6.2) — a TOTP code
	// or a WebAuthn assertion. It is not the IdP's claim about a factor, and that difference is NFR8.
	Factor PresentedFactor `json:"-"`
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
	// factors verifies the second factor the PLATFORM enrolled, rather than the one the IdP claims
	// (P22 task 6.2). Nil is permitted only for the fixture provider — see NewAuthenticatorFor.
	factors FactorVerifier
}

// AuthenticatorConfig wires the login path.
type AuthenticatorConfig struct {
	Provider   IdentityProvider
	Principals *PrincipalStore
	Sessions   *SessionStore
	Observer   Observer
	// Factors verifies a platform-enrolled second factor. REQUIRED for any real provider.
	Factors FactorVerifier
	// Production refuses a test-mode IdP. Set from the deployment environment, not inferred.
	Production bool
	Now        Clock
}

// NewAuthenticatorFor wires the login path and enforces the two invariants P22 adds.
//
// # Why these are construction-time and not runtime checks
//
// Both are properties of a DEPLOYMENT, and both fail in the direction that looks fine:
//
//   - A real IdP with no platform factor verifier authenticates on SSO alone. Every login succeeds,
//     nothing errors, and the operator surface has quietly become single-factor. Refusing to construct
//     is the only version of this check that cannot be missed, because there is no request to observe.
//   - A production console pointed at the fixture `TestMode` issuer accepts assertions signed with a
//     key that exists to make tests runnable. The customer seam already refuses to BOOT in that
//     situation (`identity.ts`, `CONSOLE_TENANT_IDENTITY=dev`); this is the same guard on the surface
//     that can halt the fleet, and it says what is wrong once, to the person doing the deploy.
func NewAuthenticatorFor(cfg AuthenticatorConfig) (*Authenticator, error) {
	if cfg.Provider == nil || cfg.Principals == nil || cfg.Sessions == nil {
		return nil, errors.New("adminidentity: an authenticator needs an admin IdP, a principal store and a session store")
	}
	info := cfg.Provider.Describe()
	if cfg.Production && info.TestMode {
		return nil, fmt.Errorf("adminidentity: the %s provider is running in test mode and must not be used in production", info.Kind)
	}
	if info.Kind != ProviderKindHMAC && cfg.Factors == nil {
		return nil, fmt.Errorf("adminidentity: the %s provider proves only the SSO subject — a platform-verified second factor is required, because the IdP's own MFA claim is a configuration of a system this code does not control", info.Kind)
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Authenticator{
		provider: cfg.Provider, principals: cfg.Principals, sessions: cfg.Sessions,
		observer: cfg.Observer, factors: cfg.Factors, now: now,
	}, nil
}

// NewAuthenticator wires the login path with the fixture provider's shape.
//
// Kept because P8's launch path and every P8 test already call it by this name. It delegates, so the
// invariants above apply to it too — which is why it works for `HMACProvider` and refuses a real one.
func NewAuthenticator(provider IdentityProvider, principals *PrincipalStore, sessions *SessionStore, observer Observer) (*Authenticator, error) {
	return NewAuthenticatorFor(AuthenticatorConfig{
		Provider: provider, Principals: principals, Sessions: sessions, Observer: observer,
	})
}

// Authenticate performs the SSO + MFA exchange and issues a session on success.
//
// Every denial path emits an event before returning, so "no MFA ⇒ denied AND logged" holds without a
// caller remembering to log it.
func (a *Authenticator) Authenticate(ctx context.Context, assertion Assertion) (Session, string, error) {
	return a.AuthenticateWithChallenge(ctx, assertion, nil)
}

// AuthenticateWithChallenge is Authenticate with the WebAuthn challenge this login was issued.
//
// The challenge is per-login and server-minted; a WebAuthn assertion that does not sign over it is a
// replay of an earlier one. It is a separate entry point rather than a fifth field on `Assertion`
// because it is OURS — the console generated it — and mixing what we issued with what the IdP and the
// operator sent is how a verifier ends up trusting the wrong half.
func (a *Authenticator) AuthenticateWithChallenge(ctx context.Context, assertion Assertion, challenge []byte) (Session, string, error) {
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

	// The PLATFORM-VERIFIED second factor (P22 task 6.2, NFR8). This runs for every provider kind that
	// has a verifier wired, and it is the ONLY MFA gate for the real ones — `OIDCProvider` and
	// `SAMLProvider` return an empty `MFAFactor` on purpose, because a claim from a system we do not
	// control is not an invariant. A misconfigured IdP MFA policy therefore still denies here.
	factor := claims.MFAFactor
	if a.factors != nil {
		if !assertion.Factor.Present() {
			a.emit(Event{Kind: EventLoginDeniedNoMFA, AdminID: p.AdminID, SSOSubject: claims.Subject,
				Detail: "no platform-verified second factor was presented", At: a.now()})
			return Session{}, "", ErrMFARequired
		}
		verified, err := a.factors.Verify(ctx, p.AdminID, assertion.Factor, challenge)
		if err != nil {
			a.emit(Event{Kind: EventLoginDeniedNoMFA, AdminID: p.AdminID, SSOSubject: claims.Subject,
				Detail: err.Error(), At: a.now()})
			// One denial for a wrong code, a wrong signature and an unenrolled factor: the operator
			// reads the detail in the event, the person signing in reads one generic message.
			return Session{}, "", fmt.Errorf("%w: %v", ErrMFARequired, err)
		}
		factor = verified
	}
	return sessionAndToken(a.sessions.Issue(ctx, p, factor))
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
