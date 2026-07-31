package adminidentity

import (
	"context"
	"crypto/elliptic"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// oidcprovider.go is the REAL, pluggable OIDC admin identity provider (P22 task 6.1, Decision 5).
//
// # What it replaces, and what it deliberately does not
//
// The shipped `HMACProvider` is a fixture: it runs in `TestMode`, and its own enum comment names the
// seam it fills — *"the integration point a SAML/OIDC admin IdP plugs into"*. This is that plug. It
// implements the SAME `IdentityProvider` interface, returns the SAME `Claims`, and is selected by
// configuration. Nothing above the seam — the authenticator, the session store, the principal
// directory, the RBAC gate — knows which one is live.
//
// # Why it refuses to read the IdP's MFA claim
//
// This is the difference between P22 and what was here before, and it is the whole of NFR8.
//
// An OIDC ID token can carry `amr: ["mfa"]` or an `acr` value saying a second factor was satisfied.
// Reading it would be easy and would look like MFA. It is not: it is a CONFIGURATION CLAIM about a
// system this code does not control, and `authn.go` already says in as many words that a
// configuration claim is not an invariant. On the surface that can halt the autonomous fleet, "MFA
// was required" must be something the platform ASSERTS, not something it is told.
//
// So `Verify` here proves exactly one thing — that the issuer we federate with says this subject
// authenticated — and returns `Claims` with an EMPTY `MFAFactor`. The second factor is verified by
// `mfa.go` at the authenticator, against material the platform itself enrolled. A misconfigured IdP
// MFA policy therefore still results in denial, which is the direction a mistake has to fail in here.
//
// # Fail closed
//
// Discovery and JWKS are cached briefly and a FAILED refresh returns the error rather than serving the
// stale copy. Verifying an operator's assertion against a key set the IdP can no longer vouch for is
// the cached-credential path Decision 8 refuses by name — and on this surface it is the worst possible
// place to take that shortcut.

// Provider kinds for the real, pluggable IdPs. Central enum, alongside ProviderKindHMAC, for the same
// reason: a health signal a monitor matches on must not be a literal typed at three call sites.
const (
	// ProviderKindOIDC is a real OIDC admin identity provider (discovery + JWKS-validated ID token).
	ProviderKindOIDC = "admin-idp-oidc"
	// ProviderKindSAML is a real SAML 2.0 admin identity provider (signed assertion, audience-bound).
	ProviderKindSAML = "admin-idp-saml"
)

// SecretAdminOIDCClientSecret is the reserved logical name for the admin IdP's OIDC client secret.
//
// Declared here rather than in secrets.go's original block only because it arrived with P22; it is
// added to `SecretNames` there, so a deployment checklist that iterates the real set still sees it.
const SecretAdminOIDCClientSecret = "admin_oidc_client_secret"

// jwkCurves maps a JWK curve name to its Go curve. Shared with jws.go.
var jwkCurves = map[string]elliptic.Curve{
	"P-256": elliptic.P256(),
	"P-384": elliptic.P384(),
	"P-521": elliptic.P521(),
}

// ErrIdPUnreachable is the fail-closed signal: no session is issued when the IdP cannot be reached.
//
// Distinct from ErrAssertionInvalid on purpose, and the distinction is for the OPERATOR, not the
// caller: "nobody can sign in because the IdP is down" and "somebody presented a bad assertion" are
// different pages at 3am. The console still shows one generic message to the person signing in.
var ErrIdPUnreachable = errors.New("adminidentity: the admin identity provider could not be reached")

// oidcDiscovery is the subset of the discovery document this provider uses.
type oidcDiscovery struct {
	Issuer   string `json:"issuer"`
	AuthzURL string `json:"authorization_endpoint"`
	TokenURL string `json:"token_endpoint"`
	JWKSURL  string `json:"jwks_uri"`
}

// OIDCProvider verifies an ID token minted by the admin identity provider.
type OIDCProvider struct {
	issuer   string
	clientID string
	client   *http.Client
	now      Clock
	ttl      time.Duration
	// secrets supplies the OIDC client secret at the moment of the code exchange (oidcflow.go). Nil
	// when this provider is only ever used to VERIFY — a deployment whose BFF performs the exchange
	// itself needs no secret here, and demanding one would be demanding a credential for a code path
	// it does not run.
	secrets Secrets
	// redirects is the exact allowlist of callback URIs. Never a prefix or a hostname rule.
	redirects []string

	mu        sync.Mutex
	discovery *oidcDiscovery
	keys      []jwk
	fetchedAt time.Time
}

// OIDCProviderConfig configures an OIDCProvider.
type OIDCProviderConfig struct {
	// Issuer is the admin IdP's issuer identifier, used as the discovery base AND compared against the
	// ID token's `iss`. Required: without it, a token from any issuer whose key set we happened to
	// fetch would be accepted.
	Issuer string
	// ClientID is the operator console's OIDC client. The ID token's audience must be exactly this.
	ClientID string
	// HTTPClient reaches the IdP. Nil uses a client with a bounded timeout — never `http.DefaultClient`,
	// which has no timeout at all and would let a hung IdP hold the sign-in surface open indefinitely.
	HTTPClient *http.Client
	// Now overrides the clock.
	Now Clock
	// MetadataTTL is how long discovery and JWKS are reused. Zero uses five minutes.
	MetadataTTL time.Duration
	// Secrets supplies the OIDC client secret for the authorization-code exchange. Required only when
	// this provider performs the exchange (AuthorizationURL / Exchange).
	Secrets Secrets
	// Redirects is the callback allowlist. Required only when this provider performs the flow; a
	// wildcard entry is refused, because an allowlist with a wildcard is not an allowlist.
	Redirects []string
}

// NewOIDCProvider builds a real OIDC admin identity provider.
func NewOIDCProvider(cfg OIDCProviderConfig) (*OIDCProvider, error) {
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, errors.New("adminidentity: an admin IdP issuer is required — an unchecked issuer accepts assertions from the customer IdP")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, errors.New("adminidentity: an admin OIDC client id is required — an unchecked audience accepts a token minted for another application")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	ttl := cfg.MetadataTTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	redirects := make([]string, 0, len(cfg.Redirects))
	for _, r := range cfg.Redirects {
		if strings.Contains(r, "*") {
			return nil, errors.New("adminidentity: a wildcard redirect entry is an open redirect by construction")
		}
		normalized, err := normalizeRedirect(r)
		if err != nil {
			return nil, err
		}
		redirects = append(redirects, normalized)
	}
	return &OIDCProvider{
		issuer: cfg.Issuer, clientID: cfg.ClientID, client: client, now: now, ttl: ttl,
		secrets: cfg.Secrets, redirects: redirects,
	}, nil
}

// metadata returns the issuer's discovery document, refreshing it when stale.
//
// Same fail-closed rule as the key set: a FAILED refresh returns the error rather than serving the
// stale copy. Following a stale `token_endpoint` during an IdP migration would send an operator's
// authorization code — and this deployment's client secret — to whatever is on the old host.
func (p *OIDCProvider) metadata(ctx context.Context) (*oidcDiscovery, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.discovery != nil && p.now().Sub(p.fetchedAt) < p.ttl {
		return p.discovery, nil
	}
	doc, err := p.fetchDiscovery(ctx)
	if err != nil {
		return nil, err
	}
	p.discovery = doc
	return doc, nil
}

// Describe names the provider for /readyz. `TestMode` is false and cannot be set: this provider only
// exists when a real IdP is configured, and a "real provider in test mode" is a claim with no meaning.
func (p *OIDCProvider) Describe() ProviderInfo {
	return ProviderInfo{Kind: ProviderKindOIDC, Issuer: p.issuer, TestMode: false}
}

// Verify implements IdentityProvider. It proves the SSO subject and NOTHING about a second factor.
func (p *OIDCProvider) Verify(ctx context.Context, a Assertion) (Claims, error) {
	if a.Issuer != "" && a.Issuer != p.issuer {
		// The caller may name the issuer it believes it is talking to; if it does, it must be ours.
		return Claims{}, fmt.Errorf("%w: issuer", ErrAssertionInvalid)
	}
	token := strings.TrimSpace(a.Token)
	if token == "" {
		return Claims{}, fmt.Errorf("%w: no token", ErrAssertionInvalid)
	}

	keys, err := p.signingKeys(ctx)
	if err != nil {
		return Claims{}, err
	}
	payload, err := verifyCompactJWS(token, keys)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrAssertionInvalid, err)
	}

	var claims struct {
		Iss   string          `json:"iss"`
		Sub   string          `json:"sub"`
		Aud   json.RawMessage `json:"aud"`
		Azp   string          `json:"azp"`
		Iat   int64           `json:"iat"`
		Exp   int64           `json:"exp"`
		Nonce string          `json:"nonce"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, fmt.Errorf("%w: payload", ErrAssertionInvalid)
	}
	if claims.Iss != p.issuer {
		return Claims{}, fmt.Errorf("%w: issuer", ErrAssertionInvalid)
	}
	if !audienceContains(claims.Aud, p.clientID) {
		return Claims{}, fmt.Errorf("%w: audience", ErrAssertionInvalid)
	}
	if claims.Azp != "" && claims.Azp != p.clientID {
		return Claims{}, fmt.Errorf("%w: authorized party", ErrAssertionInvalid)
	}
	if strings.TrimSpace(claims.Sub) == "" {
		return Claims{}, fmt.Errorf("%w: no subject", ErrAssertionInvalid)
	}
	if claims.Exp == 0 {
		// Refused rather than defaulted: a default here is a lifetime we invented on the IdP's behalf,
		// and the one we would invent is the one an attacker would ask for.
		return Claims{}, fmt.Errorf("%w: no expiry", ErrAssertionInvalid)
	}

	now := p.now()
	issued := time.Unix(claims.Iat, 0).UTC()
	if now.After(time.Unix(claims.Exp, 0).UTC().Add(MaxAssertionAge)) {
		return Claims{}, fmt.Errorf("%w: expired", ErrAssertionInvalid)
	}
	if claims.Iat != 0 {
		age := now.Sub(issued)
		if age < -MaxAssertionAge || age > MaxAssertionAge {
			return Claims{}, fmt.Errorf("%w: outside the freshness window", ErrAssertionInvalid)
		}
	}
	if a.Nonce != "" && a.Nonce != claims.Nonce {
		// The caller binds the token to the browser flow it began. When it supplies a nonce, a token
		// that does not carry it is a token from somebody else's sign-in.
		return Claims{}, fmt.Errorf("%w: nonce", ErrAssertionInvalid)
	}

	// MFAFactor is deliberately EMPTY. See the module comment: this provider does not read `amr`/`acr`,
	// because a claim from a system we do not control is not an invariant. The authenticator requires a
	// platform-verified factor before any session exists.
	return Claims{Subject: claims.Sub, MFAFactor: "", IssuedAt: issued}, nil
}

// audienceContains handles OIDC's `aud`, which is a string OR an array of strings.
func audienceContains(raw json.RawMessage, want string) bool {
	if len(raw) == 0 {
		return false
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single == want
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return false
	}
	for _, a := range many {
		if a == want {
			return true
		}
	}
	return false
}

// signingKeys returns the issuer's JWKS, refreshing when stale. A failed refresh returns the error.
func (p *OIDCProvider) signingKeys(ctx context.Context) ([]jwk, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.keys != nil && p.now().Sub(p.fetchedAt) < p.ttl {
		return p.keys, nil
	}
	discovery, err := p.fetchDiscovery(ctx)
	if err != nil {
		return nil, err
	}
	var set jwkSet
	if err := p.fetchJSON(ctx, discovery.JWKSURL, &set); err != nil {
		return nil, err
	}
	if len(set.Keys) == 0 {
		return nil, fmt.Errorf("%w: the key set is empty", ErrIdPUnreachable)
	}
	p.discovery = discovery
	p.keys = set.Keys
	p.fetchedAt = p.now()
	return p.keys, nil
}

func (p *OIDCProvider) fetchDiscovery(ctx context.Context) (*oidcDiscovery, error) {
	var doc oidcDiscovery
	base := strings.TrimRight(p.issuer, "/")
	if err := p.fetchJSON(ctx, base+"/.well-known/openid-configuration", &doc); err != nil {
		return nil, err
	}
	if doc.Issuer != p.issuer {
		// Without this, a mis-typed or hijacked discovery URL silently re-points the whole trust anchor
		// — every check downstream would then pass, against the attacker's own keys.
		return nil, fmt.Errorf("%w: the discovery document declares a different issuer", ErrIdPUnreachable)
	}
	if doc.JWKSURL == "" {
		return nil, fmt.Errorf("%w: the discovery document names no key set", ErrIdPUnreachable)
	}
	return &doc, nil
}

func (p *OIDCProvider) fetchJSON(ctx context.Context, url string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIdPUnreachable, err)
	}
	req.Header.Set("accept", "application/json")
	// Lane: direct outbound to the operator's identity provider. Declared here rather than inherited
	// from a package default, so "which client talks to the IdP, with what timeout" has one answer.
	res, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIdPUnreachable, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s returned %d", ErrIdPUnreachable, url, res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIdPUnreachable, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%w: %s did not return JSON", ErrIdPUnreachable, url)
	}
	return nil
}

// Reachable reports whether the IdP answers RIGHT NOW, for the readiness surface (task 6.4 / 7.1).
//
// It clears the cache first. A readiness answer served from a five-minute-old cache is a report about
// the past, and the whole value of putting this on /readyz is that a monitor on the box in question
// can check the claim rather than read a log line that asserts it.
func (p *OIDCProvider) Reachable(ctx context.Context) error {
	p.mu.Lock()
	p.keys = nil
	p.fetchedAt = time.Time{}
	p.mu.Unlock()
	_, err := p.signingKeys(ctx)
	return err
}
