package adminidentity

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// oidcflow.go is the OIDC PROTOCOL half of the operator sign-in — the two calls the browser flow needs
// that `oidcprovider.go` (the verifier) deliberately does not do (P22 follow-on to task 6.1).
//
// # Why the exchange happens HERE and not in the operator console's BFF
//
// The BFF could do the whole dance: discovery, authorize, token exchange, and then hand the platform
// an ID token to verify. The customer console does exactly that, and it is right there — because the
// customer seam IS the console (ADR-008), and the console's own `Secrets` source holds its client
// secret.
//
// The operator side is the other way round. `adminidentity` already sources every operator credential
// from `providergateway.Secrets` under reserved logical names, and the operator BFF deliberately holds
// exactly one credential: the platform credential that proves a request came through it. Putting the
// admin IdP's client secret into the Node process would give the operator BFF a SECOND credential, in
// a second secret source, on the highest-blast-radius surface — and `/readyz` would report one source
// while a credential that mints operator sessions came from somewhere else. That is the specific lie
// `secrets-baseline.md` §1.1 exists to prevent.
//
// So the BFF owns the BROWSER (it holds `state`, the PKCE verifier and the nonce, because it is what
// the browser talks to) and this file owns the CREDENTIAL. The BFF sends an authorization code; it
// never sees a client secret and never sees an ID token.
//
// # What the BFF must still do, and why that is not a gap
//
// `state`, the PKCE verifier and the nonce are per-BROWSER-flow, so they belong with the process that
// set the cookie. Handing them to this package would mean a server-side flow store keyed by something
// the browser sends — which is the same store the BFF already has, one hop further from the browser.

// AuthorizationRequest is what the BFF has already minted for one browser flow.
type AuthorizationRequest struct {
	// State travels through the IdP and comes back. The BFF binds it to a browser cookie.
	State string
	// Nonce binds the returned ID token to this flow. Checked at Verify, not here.
	Nonce string
	// CodeChallenge is the S256 challenge for the verifier the BFF holds.
	CodeChallenge string
	// RedirectURI must be one the IdP has registered AND one this provider allows.
	RedirectURI string
}

// ExchangeRequest redeems an authorization code.
type ExchangeRequest struct {
	Code string
	// CodeVerifier is the PKCE verifier the BFF held while the browser was away. Without it an
	// intercepted code is worthless, which is the whole point of PKCE.
	CodeVerifier string
	RedirectURI  string
}

// PKCEChallenge is the S256 challenge for a verifier. Exported so the BFF and this package compute it
// one way — two spellings of a challenge is a PKCE check that silently never matches.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// RedirectAllowed reports whether a redirect URI is on this provider's allowlist.
//
// Exact origin+path equality. Not a prefix test, not a hostname test: every one of those has been the
// shape of a real open redirect, and on this surface the target of the redirect is where an operator
// session ends up.
func (p *OIDCProvider) RedirectAllowed(candidate string) bool {
	want, err := normalizeRedirect(candidate)
	if err != nil {
		return false
	}
	for _, allowed := range p.redirects {
		if allowed == want {
			return true
		}
	}
	return false
}

func normalizeRedirect(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !u.IsAbs() || u.Host == "" {
		return "", errors.New("adminidentity: a redirect URI must be absolute")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("adminidentity: a redirect URI carries no query or fragment")
	}
	return u.Scheme + "://" + u.Host + u.Path, nil
}

// AuthorizationURL builds the authorization request the browser is sent to.
//
// `response_type=code` and nothing else — there is no branch that can emit `id_token` or `token`, so
// the implicit flow is not a configuration mistake this deployment can make. The implicit flow would
// put an operator's token in a URL fragment, in browser history, and in reach of any script on the
// page; on the surface that can halt the fleet that is not a trade worth having available.
func (p *OIDCProvider) AuthorizationURL(ctx context.Context, req AuthorizationRequest) (string, error) {
	for name, value := range map[string]string{
		"state": req.State, "nonce": req.Nonce, "code_challenge": req.CodeChallenge, "redirect_uri": req.RedirectURI,
	} {
		if strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("adminidentity: an authorization request needs a %s", name)
		}
	}
	if !p.RedirectAllowed(req.RedirectURI) {
		// Refused before the browser moves. An off-allowlist redirect is where a session is delivered
		// to somebody else, and finding out at the callback is one hop too late.
		return "", fmt.Errorf("%w: the redirect URI is not on this deployment's allowlist", ErrAssertionInvalid)
	}
	meta, err := p.metadata(ctx)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(meta.AuthzURL)
	if err != nil {
		return "", fmt.Errorf("%w: the discovery document's authorization endpoint is not a URL", ErrIdPUnreachable)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", req.RedirectURI)
	q.Set("scope", "openid email profile")
	q.Set("state", req.State)
	q.Set("nonce", req.Nonce)
	q.Set("code_challenge", req.CodeChallenge)
	q.Set("code_challenge_method", "S256") // S256 only; `plain` sends the verifier, which proves nothing
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Exchange redeems an authorization code for an ID token.
//
// The client secret is resolved through the `Secrets` seam at the MOMENT OF USE and never held in a
// field: a value fetched once at construction survives a rotation, and a value in a struct is a value
// a stack trace can print.
//
// The returned ID token is raw credential material. Its only caller passes it straight to `Verify` and
// drops it; it is never stored, logged, or returned above this package.
func (p *OIDCProvider) Exchange(ctx context.Context, req ExchangeRequest) (string, error) {
	if strings.TrimSpace(req.Code) == "" || strings.TrimSpace(req.CodeVerifier) == "" {
		return "", fmt.Errorf("%w: an exchange needs a code and its PKCE verifier", ErrAssertionInvalid)
	}
	if !p.RedirectAllowed(req.RedirectURI) {
		return "", fmt.Errorf("%w: the redirect URI is not on this deployment's allowlist", ErrAssertionInvalid)
	}
	if p.secrets == nil {
		return "", fmt.Errorf("%w: no secrets source is wired for the admin OIDC client secret", ErrSecretUnavailable)
	}
	meta, err := p.metadata(ctx)
	if err != nil {
		return "", err
	}
	secret, err := p.secrets.Named(ctx, SecretAdminOIDCClientSecret)
	if err != nil {
		// Fail closed. No client secret means no exchange and therefore no session — never a fallback
		// to an unauthenticated token request.
		return "", err
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", req.Code)
	form.Set("redirect_uri", req.RedirectURI)
	form.Set("client_id", p.clientID)
	form.Set("client_secret", string(secret))
	form.Set("code_verifier", req.CodeVerifier)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrIdPUnreachable, err)
	}
	httpReq.Header.Set("content-type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("accept", "application/json")
	res, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrIdPUnreachable, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrIdPUnreachable, err)
	}
	if res.StatusCode != http.StatusOK {
		// A reused or forged code lands here. It is an ordinary REFUSAL, not an outage — calling it
		// unreachability would page an operator for an attacker's failed attempt. The IdP's own error
		// text is NOT propagated: it is attacker-influenced and would end up on our sign-in page.
		return "", fmt.Errorf("%w: the authorization code was not redeemable", ErrAssertionInvalid)
	}
	var token struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil || strings.TrimSpace(token.IDToken) == "" {
		return "", fmt.Errorf("%w: the token response carried no ID token", ErrAssertionInvalid)
	}
	return token.IDToken, nil
}
