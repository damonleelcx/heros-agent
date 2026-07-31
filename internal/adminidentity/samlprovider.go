package adminidentity

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// samlprovider.go is the REAL, pluggable SAML 2.0 admin identity provider (P22 task 6.1, Decision 2).
//
// It is the enterprise alternative to `OIDCProvider`, behind the SAME `IdentityProvider` seam, and it
// makes exactly the same refusal about MFA: it proves the SSO subject and returns `Claims` with an
// EMPTY `MFAFactor`, because a `<AuthnContextClassRef>` saying multi-factor is a claim from a system
// this code does not control. The platform-verified factor in `mfa.go` is the invariant (NFR8).
//
// # The attack this file is really about
//
// **Signature wrapping.** The classic SAML break is not a broken signature; it is a *valid* signature
// over an element the verifier then does not use. An attacker takes a legitimately signed assertion,
// wraps it where the verifier ignores it, and puts a forged one where the verifier looks. Every
// signature check passes and the claims are the attacker's.
//
// The defense is structural and it is the rule this file is built around: **the element whose digest
// was verified is the ONLY element whose claims are read.** `verifyXMLSignature` returns that element,
// and nothing afterwards searches the document for an assertion again.
//
// # The profile, stated rather than assumed
//
// The console performs the browser flow; this package verifies what comes back. Signature on the
// Assertion or on the Response (both are deployed in the wild). RSA/ECDSA with SHA-256/384/512 — SHA-1
// is absent, because a collision-broken digest is not a signature. **No encrypted assertions**: a
// deployment needing `EncryptedAssertion` is refused loudly rather than served by a code path nobody
// exercises.

const (
	samlNSProtocol  = "urn:oasis:names:tc:SAML:2.0:protocol"
	samlNSAssertion = "urn:oasis:names:tc:SAML:2.0:assertion"
	samlNSMetadata  = "urn:oasis:names:tc:SAML:2.0:metadata"
	samlNSDsig      = "http://www.w3.org/2000/09/xmldsig#"
	samlNSXenc      = "http://www.w3.org/2001/04/xmlenc#"
	samlExcC14N     = "http://www.w3.org/2001/10/xml-exc-c14n#"
	samlEnveloped   = "http://www.w3.org/2000/09/xmldsig#enveloped-signature"
)

// samlSignatureAlgs is the allowlist. SHA-1 is absent, deliberately.
var samlSignatureAlgs = map[string]crypto.Hash{
	"http://www.w3.org/2001/04/xmldsig-more#rsa-sha256":   crypto.SHA256,
	"http://www.w3.org/2001/04/xmldsig-more#rsa-sha384":   crypto.SHA384,
	"http://www.w3.org/2001/04/xmldsig-more#rsa-sha512":   crypto.SHA512,
	"http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha256": crypto.SHA256,
	"http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha384": crypto.SHA384,
	"http://www.w3.org/2001/04/xmldsig-more#ecdsa-sha512": crypto.SHA512,
}

var samlDigestAlgs = map[string]crypto.Hash{
	"http://www.w3.org/2001/04/xmlenc#sha256":       crypto.SHA256,
	"http://www.w3.org/2001/04/xmldsig-more#sha384": crypto.SHA384,
	"http://www.w3.org/2001/04/xmlenc#sha512":       crypto.SHA512,
}

// SAMLProvider verifies a SAML Response minted by the admin identity provider.
type SAMLProvider struct {
	entityID    string
	spEntityID  string
	metadataURL string
	acs         []string
	client      *http.Client
	now         Clock
	ttl         time.Duration

	mu           sync.Mutex
	certificates []*x509.Certificate
	fetchedAt    time.Time
}

// SAMLProviderConfig configures a SAMLProvider.
type SAMLProviderConfig struct {
	// EntityID is the admin IdP's entityID — the trust anchor, compared against both the metadata
	// document's own declaration and the assertion's `<Issuer>`.
	EntityID string
	// SPEntityID is the operator console's own entityID, enforced as the assertion's audience.
	SPEntityID string
	// MetadataURL is where the IdP's signing certificates are fetched from.
	MetadataURL string
	// ACS is the allowlist of Assertion Consumer Service URLs a response may be accepted at.
	ACS         []string
	HTTPClient  *http.Client
	Now         Clock
	MetadataTTL time.Duration
}

// NewSAMLProvider builds a real SAML admin identity provider.
func NewSAMLProvider(cfg SAMLProviderConfig) (*SAMLProvider, error) {
	if strings.TrimSpace(cfg.EntityID) == "" {
		return nil, errors.New("adminidentity: an admin IdP entityID is required — an unchecked issuer accepts assertions from the customer IdP")
	}
	if strings.TrimSpace(cfg.SPEntityID) == "" {
		return nil, errors.New("adminidentity: a service-provider entityID is required — an unchecked audience accepts an assertion minted for another application")
	}
	if strings.TrimSpace(cfg.MetadataURL) == "" {
		return nil, errors.New("adminidentity: an IdP metadata URL is required — signing certificates are fetched, never compiled in")
	}
	if len(cfg.ACS) == 0 {
		return nil, errors.New("adminidentity: at least one allowlisted ACS URL is required — a flow with no allowlist is an open redirect")
	}
	for _, a := range cfg.ACS {
		if strings.Contains(a, "*") {
			return nil, errors.New("adminidentity: a wildcard ACS entry is an open redirect by construction")
		}
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
	return &SAMLProvider{
		entityID: cfg.EntityID, spEntityID: cfg.SPEntityID, metadataURL: cfg.MetadataURL,
		acs: append([]string(nil), cfg.ACS...), client: client, now: now, ttl: ttl,
	}, nil
}

// Describe names the provider for /readyz.
func (p *SAMLProvider) Describe() ProviderInfo {
	return ProviderInfo{Kind: ProviderKindSAML, Issuer: p.entityID, TestMode: false}
}

// Verify implements IdentityProvider. It proves the SSO subject and NOTHING about a second factor.
func (p *SAMLProvider) Verify(ctx context.Context, a Assertion) (Claims, error) {
	if a.Issuer != "" && a.Issuer != p.entityID {
		return Claims{}, fmt.Errorf("%w: issuer", ErrAssertionInvalid)
	}
	raw := strings.TrimSpace(a.Token)
	if raw == "" {
		return Claims{}, fmt.Errorf("%w: no response", ErrAssertionInvalid)
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: response is not base64", ErrAssertionInvalid)
	}
	doc, err := parseXML(string(decoded))
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrAssertionInvalid, err)
	}
	if namespaceOf(doc) != samlNSProtocol || doc.local != "Response" {
		return Claims{}, fmt.Errorf("%w: not a SAML Response", ErrAssertionInvalid)
	}

	// Refused loudly rather than half-handled: an encrypted assertion needs the SP decryption key and a
	// code path this deployment does not exercise, and a verifier that quietly ignored it would report
	// "no assertion" for a response that has one.
	var encrypted bool
	walkXML(doc, func(el *xmlElement) {
		if el.local == "EncryptedAssertion" || namespaceOf(el) == samlNSXenc {
			encrypted = true
		}
	})
	if encrypted {
		return Claims{}, fmt.Errorf("%w: encrypted assertions are not accepted by this deployment", ErrAssertionInvalid)
	}

	if destination, ok := attrOf(doc, "Destination"); ok && !p.allowedACS(destination) {
		return Claims{}, fmt.Errorf("%w: Destination is not an allowlisted ACS", ErrAssertionInvalid)
	}
	status := childrenNamed(doc, samlNSProtocol, "Status")
	if len(status) != 1 {
		return Claims{}, fmt.Errorf("%w: no status", ErrAssertionInvalid)
	}
	codes := childrenNamed(status[0], samlNSProtocol, "StatusCode")
	if len(codes) != 1 {
		return Claims{}, fmt.Errorf("%w: no status code", ErrAssertionInvalid)
	}
	if v, _ := attrOf(codes[0], "Value"); v != "urn:oasis:names:tc:SAML:2.0:status:Success" {
		return Claims{}, fmt.Errorf("%w: status is not Success", ErrAssertionInvalid)
	}
	if a.Nonce != "" {
		if got, _ := attrOf(doc, "InResponseTo"); got != a.Nonce {
			// The caller binds the response to the request this browser began. An unsolicited assertion
			// — however validly signed — answers no request of ours and issues no session.
			return Claims{}, fmt.Errorf("%w: InResponseTo", ErrAssertionInvalid)
		}
	}

	certificates, err := p.signingCertificates(ctx)
	if err != nil {
		return Claims{}, err
	}

	// Verify EVERY signature present, keeping what each covered. An unverifiable signature anywhere is
	// a refusal, not something to skip past on the way to a good one.
	var covered []*xmlElement
	var sigErr error
	walkXML(doc, func(el *xmlElement) {
		if sigErr != nil || namespaceOf(el) != samlNSDsig || el.local != "Signature" {
			return
		}
		target, err := verifyXMLSignature(doc, el, certificates)
		if err != nil {
			sigErr = err
			return
		}
		covered = append(covered, target)
	})
	if sigErr != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrAssertionInvalid, sigErr)
	}
	if len(covered) == 0 {
		return Claims{}, fmt.Errorf("%w: the response carries no signature", ErrAssertionInvalid)
	}

	// THE anti-wrapping step. The assertion is taken from inside a VERIFIED element, and the only
	// assertions considered are the ones that element actually contains.
	seen := map[*xmlElement]bool{}
	var assertions []*xmlElement
	for _, element := range covered {
		walkXML(element, func(el *xmlElement) {
			if namespaceOf(el) == samlNSAssertion && el.local == "Assertion" && !seen[el] {
				seen[el] = true
				assertions = append(assertions, el)
			}
		})
	}
	if len(assertions) != 1 {
		return Claims{}, fmt.Errorf("%w: the signed content does not carry exactly one assertion", ErrAssertionInvalid)
	}
	assertion := assertions[0]

	issuers := childrenNamed(assertion, samlNSAssertion, "Issuer")
	if len(issuers) != 1 || strings.TrimSpace(textOfXML(issuers[0])) != p.entityID {
		return Claims{}, fmt.Errorf("%w: assertion issuer", ErrAssertionInvalid)
	}
	conditions := childrenNamed(assertion, samlNSAssertion, "Conditions")
	if len(conditions) != 1 {
		return Claims{}, fmt.Errorf("%w: no Conditions", ErrAssertionInvalid)
	}
	audienceOK := false
	for _, restriction := range childrenNamed(conditions[0], samlNSAssertion, "AudienceRestriction") {
		for _, audience := range childrenNamed(restriction, samlNSAssertion, "Audience") {
			if strings.TrimSpace(textOfXML(audience)) == p.spEntityID {
				audienceOK = true
			}
		}
	}
	if !audienceOK {
		return Claims{}, fmt.Errorf("%w: audience", ErrAssertionInvalid)
	}

	subjects := childrenNamed(assertion, samlNSAssertion, "Subject")
	if len(subjects) != 1 {
		return Claims{}, fmt.Errorf("%w: no Subject", ErrAssertionInvalid)
	}
	nameIDs := childrenNamed(subjects[0], samlNSAssertion, "NameID")
	if len(nameIDs) != 1 {
		return Claims{}, fmt.Errorf("%w: no NameID", ErrAssertionInvalid)
	}
	subject := strings.TrimSpace(textOfXML(nameIDs[0]))
	if subject == "" {
		return Claims{}, fmt.Errorf("%w: empty NameID", ErrAssertionInvalid)
	}

	// The recipient must be one of OUR ACS URLs. Without it, a signed assertion minted for another
	// service provider is replayable here — the SAML shape of an audience confusion.
	recipientOK := false
	for _, confirmation := range childrenNamed(subjects[0], samlNSAssertion, "SubjectConfirmation") {
		for _, data := range childrenNamed(confirmation, samlNSAssertion, "SubjectConfirmationData") {
			recipient, ok := attrOf(data, "Recipient")
			if !ok || !p.allowedACS(recipient) {
				continue
			}
			if answers, has := attrOf(data, "InResponseTo"); has && a.Nonce != "" && answers != a.Nonce {
				continue
			}
			recipientOK = true
		}
	}
	if !recipientOK {
		return Claims{}, fmt.Errorf("%w: no SubjectConfirmationData names an allowlisted ACS", ErrAssertionInvalid)
	}

	now := p.now()
	notBefore, hasNotBefore := samlInstant(conditions[0], "NotBefore")
	notOnOrAfter, hasNotOnOrAfter := samlInstant(conditions[0], "NotOnOrAfter")
	if !hasNotOnOrAfter {
		// Refused rather than defaulted: a default here is a lifetime we invented on the IdP's behalf.
		return Claims{}, fmt.Errorf("%w: the assertion carries no expiry", ErrAssertionInvalid)
	}
	if hasNotBefore && now.Add(MaxAssertionAge).Before(notBefore) {
		return Claims{}, fmt.Errorf("%w: not yet valid", ErrAssertionInvalid)
	}
	if !now.Before(notOnOrAfter.Add(MaxAssertionAge)) {
		return Claims{}, fmt.Errorf("%w: expired", ErrAssertionInvalid)
	}
	issued, _ := samlInstant(assertion, "IssueInstant")

	// MFAFactor is EMPTY. `<AuthnContextClassRef>` is not read: see the module comment.
	return Claims{Subject: subject, MFAFactor: "", IssuedAt: issued}, nil
}

func (p *SAMLProvider) allowedACS(candidate string) bool {
	for _, allowed := range p.acs {
		if allowed == candidate {
			return true
		}
	}
	return false
}

func samlInstant(el *xmlElement, name string) (time.Time, bool) {
	raw, ok := attrOf(el, name)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// verifyXMLSignature verifies one `<ds:Signature>` and returns the element it ACTUALLY covered.
//
// The return value is the point. A function returning a boolean leaves the caller to decide what was
// signed, and that decision is where signature wrapping lives.
func verifyXMLSignature(doc, signature *xmlElement, certificates []*x509.Certificate) (*xmlElement, error) {
	signedInfos := childrenNamed(signature, samlNSDsig, "SignedInfo")
	if len(signedInfos) != 1 {
		return nil, errors.New("signature has no single SignedInfo")
	}
	signedInfo := signedInfos[0]

	c14n := childrenNamed(signedInfo, samlNSDsig, "CanonicalizationMethod")
	if len(c14n) != 1 {
		return nil, errors.New("no canonicalization method")
	}
	if alg, _ := attrOf(c14n[0], "Algorithm"); alg != samlExcC14N {
		return nil, errors.New("unsupported canonicalization method")
	}
	methods := childrenNamed(signedInfo, samlNSDsig, "SignatureMethod")
	if len(methods) != 1 {
		return nil, errors.New("no signature method")
	}
	sigAlgName, _ := attrOf(methods[0], "Algorithm")
	sigHash, ok := samlSignatureAlgs[sigAlgName]
	if !ok {
		return nil, errors.New("unsupported signature algorithm")
	}

	references := childrenNamed(signedInfo, samlNSDsig, "Reference")
	// Exactly one. Several references let an attacker add a harmless second one and hope the verifier
	// reports "signature valid" for the pair.
	if len(references) != 1 {
		return nil, errors.New("signature does not cover exactly one reference")
	}
	reference := references[0]
	uri, _ := attrOf(reference, "URI")
	if !strings.HasPrefix(uri, "#") || len(uri) < 2 {
		return nil, errors.New("reference does not point at an element in this document")
	}
	id := uri[1:]

	var matches []*xmlElement
	walkXML(doc, func(el *xmlElement) {
		for _, key := range []string{"ID", "Id", "id"} {
			if v, ok := attrOf(el, key); ok && v == id {
				matches = append(matches, el)
				return
			}
		}
	})
	// Duplicate IDs are the raw material of a wrapping attack: two elements answer to one reference and
	// the verifier and the reader can pick different ones. Refused rather than resolved by a rule.
	if len(matches) != 1 {
		return nil, errors.New("reference resolves to no unique element")
	}
	covered := matches[0]

	transformSets := childrenNamed(reference, samlNSDsig, "Transforms")
	var algorithms []string
	if len(transformSets) == 1 {
		for _, t := range childrenNamed(transformSets[0], samlNSDsig, "Transform") {
			alg, _ := attrOf(t, "Algorithm")
			algorithms = append(algorithms, alg)
		}
	}
	enveloped := false
	for _, alg := range algorithms {
		switch alg {
		case samlEnveloped:
			enveloped = true
		case samlExcC14N:
		default:
			return nil, errors.New("reference carries an unsupported transform")
		}
	}
	if !enveloped {
		return nil, errors.New("reference is not an enveloped signature")
	}
	inside := false
	for n := signature.parent; n != nil; n = n.parent {
		if n == covered {
			inside = true
			break
		}
	}
	if !inside {
		return nil, errors.New("the signature is not enveloped in the element it covers")
	}

	digestMethods := childrenNamed(reference, samlNSDsig, "DigestMethod")
	if len(digestMethods) != 1 {
		return nil, errors.New("no digest method")
	}
	digestAlgName, _ := attrOf(digestMethods[0], "Algorithm")
	digestHash, ok := samlDigestAlgs[digestAlgName]
	if !ok {
		return nil, errors.New("unsupported digest algorithm")
	}
	digestValues := childrenNamed(reference, samlNSDsig, "DigestValue")
	if len(digestValues) != 1 {
		return nil, errors.New("no digest value")
	}
	want := strings.TrimSpace(textOfXML(digestValues[0]))

	canonical := canonicalizeXML(covered, signature)
	got := base64.StdEncoding.EncodeToString(hashBytes(digestHash, []byte(canonical)))
	if got != want {
		return nil, errors.New("the signed digest does not match the element")
	}

	signedInfoBytes := []byte(canonicalizeXML(signedInfo, nil))
	values := childrenNamed(signature, samlNSDsig, "SignatureValue")
	if len(values) != 1 {
		return nil, errors.New("no signature value")
	}
	sigBytes, err := base64.StdEncoding.DecodeString(stripSpace(textOfXML(values[0])))
	if err != nil {
		return nil, errors.New("signature value is not base64")
	}
	digest := hashBytes(sigHash, signedInfoBytes)
	for _, cert := range certificates {
		switch key := cert.PublicKey.(type) {
		case *rsa.PublicKey:
			if rsa.VerifyPKCS1v15(key, sigHash, digest, sigBytes) == nil {
				return covered, nil
			}
		case *ecdsa.PublicKey:
			// XML-DSig ECDSA signatures are the raw R‖S pair, like JWS and unlike WebAuthn's DER.
			if len(sigBytes)%2 == 0 && ecdsaVerifyRaw(key, digest, sigBytes) {
				return covered, nil
			}
		}
	}
	return nil, errors.New("signature did not verify against any metadata certificate")
}

func hashBytes(h crypto.Hash, data []byte) []byte {
	switch h {
	case crypto.SHA256:
		d := sha256.Sum256(data)
		return d[:]
	case crypto.SHA384:
		d := sha512.Sum384(data)
		return d[:]
	default:
		d := sha512.Sum512(data)
		return d[:]
	}
}

func ecdsaVerifyRaw(key *ecdsa.PublicKey, digest, signature []byte) bool {
	half := len(signature) / 2
	r := new(big.Int).SetBytes(signature[:half])
	s := new(big.Int).SetBytes(signature[half:])
	return ecdsa.Verify(key, digest, r, s)
}

func stripSpace(v string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, v)
}

// signingCertificates returns the IdP's signing certificates, refreshing when stale.
//
// A FAILED refresh returns the error rather than serving the stale copy: verifying an operator's
// assertion against a key set the IdP can no longer vouch for is the cached-credential path Decision 8
// refuses by name, and this is the worst surface to take that shortcut on.
func (p *SAMLProvider) signingCertificates(ctx context.Context) ([]*x509.Certificate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.certificates != nil && p.now().Sub(p.fetchedAt) < p.ttl {
		return p.certificates, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdPUnreachable, err)
	}
	// Lane: direct outbound to the operator's identity provider.
	res, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdPUnreachable, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: metadata returned %d", ErrIdPUnreachable, res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdPUnreachable, err)
	}
	root, err := parseXML(string(body))
	if err != nil {
		return nil, fmt.Errorf("%w: metadata is not well-formed XML", ErrIdPUnreachable)
	}
	if namespaceOf(root) != samlNSMetadata || root.local != "EntityDescriptor" {
		return nil, fmt.Errorf("%w: metadata is not an EntityDescriptor", ErrIdPUnreachable)
	}
	if got, _ := attrOf(root, "entityID"); strings.TrimSpace(got) != p.entityID {
		// Without this a mis-typed or hijacked metadata URL silently re-points the whole trust anchor.
		return nil, fmt.Errorf("%w: metadata declares a different entityID", ErrIdPUnreachable)
	}
	var certificates []*x509.Certificate
	var parseErr error
	walkXML(root, func(el *xmlElement) {
		if parseErr != nil || namespaceOf(el) != samlNSDsig || el.local != "X509Certificate" {
			return
		}
		der, err := base64.StdEncoding.DecodeString(stripSpace(textOfXML(el)))
		if err != nil {
			parseErr = err
			return
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			parseErr = err
			return
		}
		certificates = append(certificates, cert)
	})
	if parseErr != nil {
		return nil, fmt.Errorf("%w: metadata certificate: %v", ErrIdPUnreachable, parseErr)
	}
	if len(certificates) == 0 {
		return nil, fmt.Errorf("%w: metadata carries no signing certificate", ErrIdPUnreachable)
	}
	p.certificates = certificates
	p.fetchedAt = p.now()
	return certificates, nil
}

// Reachable reports whether the IdP answers RIGHT NOW, for the readiness surface.
func (p *SAMLProvider) Reachable(ctx context.Context) error {
	p.mu.Lock()
	p.certificates = nil
	p.fetchedAt = time.Time{}
	p.mu.Unlock()
	_, err := p.signingCertificates(ctx)
	return err
}
