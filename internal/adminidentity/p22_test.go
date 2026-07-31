package adminidentity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// p22_test.go is P22's operator-side gate: a REAL OIDC and a REAL SAML admin IdP behind the existing
// seam, and a second factor the PLATFORM verifies rather than one the IdP claims.
//
// # The order, and why it is the adversarial cases first
//
// A federation that logs the right operator in proves nothing on its own. Every negative case below is
// produced by MUTATING a genuinely valid message — a real ID token whose header is swapped to
// `alg: none`, a real signed assertion with one character changed after signing — because a
// hand-written invalid message only proves the verifier rejects garbage, and an attacker does not send
// garbage.
//
// # What makes the SAML half trustworthy
//
// The fixture signer below computes its digest with this package's own canonicalizer, so a round trip
// alone would pass even if that canonicalizer were wrong. The independent leg is
// TestExclusiveCanonicalizationMatchesTheSpecification: the W3C exclusive-c14n specification's own
// worked example, which this repository did not author. Without it, everything after it would only be
// evidence that two halves agree.

// ── The independent leg ─────────────────────────────────────────────────────────────────────────

func TestExclusiveCanonicalizationMatchesTheSpecification(t *testing.T) {
	doc, err := parseXML(
		"<n0:local xmlns:n0=\"foo:bar\" xmlns:n3=\"ftp://example.org\">\n" +
			"  <n1:elem2 xmlns:n1=\"http://example.net\" xml:lang=\"en\">\n" +
			"     <n3:stuff xmlns:n3=\"ftp://example.org\"/>\n" +
			"  </n1:elem2>\n" +
			"</n0:local>")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var elem2 *xmlElement
	walkXML(doc, func(el *xmlElement) {
		if el.local == "elem2" {
			elem2 = el
		}
	})
	want := "<n1:elem2 xmlns:n1=\"http://example.net\" xml:lang=\"en\">\n" +
		"     <n3:stuff xmlns:n3=\"ftp://example.org\"></n3:stuff>\n" +
		"  </n1:elem2>"
	if got := canonicalizeXML(elem2, nil); got != want {
		t.Fatalf("exclusive c14n does not match the specification example.\n got: %q\nwant: %q", got, want)
	}
}

func TestADoctypeInASignedDocumentIsRefused(t *testing.T) {
	// XXE and entity expansion need no clever payload if the parser will not read a DOCTYPE at all.
	if _, err := parseXML(`<!DOCTYPE r [<!ENTITY x "y">]><r/>`); err == nil {
		t.Fatal("a DOCTYPE was accepted in a document whose signature we are about to trust")
	}
}

// ── OIDC ────────────────────────────────────────────────────────────────────────────────────────

type oidcStub struct {
	server  *httptest.Server
	key     *rsa.PrivateKey
	algNone bool
}

func newOIDCStub(t *testing.T) *oidcStub {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	s := &oidcStub{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]string{
			"issuer":                 s.server.URL,
			"authorization_endpoint": s.server.URL + "/authorize",
			"token_endpoint":         s.server.URL + "/token",
			"jwks_uri":               s.server.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "stub-1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// mint produces a real ID token. `algNone` swaps the header only — everything else stays valid, so a
// refusal is attributable to the algorithm allowlist and nothing else.
func (s *oidcStub) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	alg := "RS256"
	if s.algNone {
		alg = "none"
	}
	header, _ := json.Marshal(map[string]string{"alg": alg, "kid": "stub-1", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	if s.algNone {
		return signing + "."
	}
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, 5 /* crypto.SHA256 */, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func oidcClaims(issuer string, now time.Time) map[string]any {
	return map[string]any{
		"iss": issuer, "aud": "operator-console", "sub": "op-1",
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		// An IdP that CLAIMS multi-factor. The provider must not read it — that is the whole of NFR8.
		"amr": []string{"mfa", "hwk"}, "acr": "http://schemas.openid.net/pape/policies/2007/06/multi-factor",
	}
}

func newOIDCProviderFor(t *testing.T, stub *oidcStub, now time.Time) *OIDCProvider {
	t.Helper()
	p, err := NewOIDCProvider(OIDCProviderConfig{
		Issuer: stub.server.URL, ClientID: "operator-console",
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	return p
}

func TestOIDCProviderVerifiesARealIDTokenAndReadsNoMFAClaim(t *testing.T) {
	now := time.Now().UTC()
	stub := newOIDCStub(t)
	p := newOIDCProviderFor(t, stub, now)

	claims, err := p.Verify(context.Background(), Assertion{Token: stub.mint(t, oidcClaims(stub.server.URL, now))})
	if err != nil {
		t.Fatalf("a valid ID token was refused: %v", err)
	}
	if claims.Subject != "op-1" {
		t.Fatalf("subject = %q", claims.Subject)
	}
	// The load-bearing assertion of this whole file. The token said `amr: ["mfa"]` and `acr: multi-factor`,
	// and the provider returned NOTHING about a factor — so a misconfigured IdP MFA policy cannot
	// satisfy the platform's requirement.
	if claims.MFAFactor != "" {
		t.Fatalf("the provider read the IdP's MFA claim (%q); NFR8 requires the platform to verify a factor itself", claims.MFAFactor)
	}
	if info := p.Describe(); info.Kind != ProviderKindOIDC || info.TestMode {
		t.Fatalf("Describe() = %+v; a real provider is never in test mode", info)
	}
}

func TestOIDCProviderRefusesTheMutatedToken(t *testing.T) {
	now := time.Now().UTC()
	stub := newOIDCStub(t)
	p := newOIDCProviderFor(t, stub, now)

	cases := []struct {
		name   string
		mutate func(map[string]any)
		before func()
		after  func()
	}{
		{name: "alg:none", before: func() { stub.algNone = true }, after: func() { stub.algNone = false }},
		{name: "another client's audience", mutate: func(c map[string]any) { c["aud"] = "someone-else" }},
		{name: "an authorized party that is not us", mutate: func(c map[string]any) { c["azp"] = "someone-else" }},
		{name: "another issuer", mutate: func(c map[string]any) { c["iss"] = "https://attacker.test" }},
		{name: "no expiry", mutate: func(c map[string]any) { delete(c, "exp") }},
		{name: "an hour stale", mutate: func(c map[string]any) { c["iat"] = now.Add(-time.Hour).Unix() }},
		{name: "no subject", mutate: func(c map[string]any) { delete(c, "sub") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.before != nil {
				tc.before()
				defer tc.after()
			}
			claims := oidcClaims(stub.server.URL, now)
			if tc.mutate != nil {
				tc.mutate(claims)
			}
			if _, err := p.Verify(context.Background(), Assertion{Token: stub.mint(t, claims)}); err == nil {
				t.Fatal("the mutated token was accepted")
			} else if !errors.Is(err, ErrAssertionInvalid) {
				t.Fatalf("err = %v; every verification failure is one error so probing learns nothing", err)
			}
		})
	}
}

func TestOIDCProviderRefusesATamperedPayload(t *testing.T) {
	now := time.Now().UTC()
	stub := newOIDCStub(t)
	p := newOIDCProviderFor(t, stub, now)

	token := stub.mint(t, oidcClaims(stub.server.URL, now))
	parts := strings.Split(token, ".")
	forged := oidcClaims(stub.server.URL, now)
	forged["sub"] = "superadmin"
	payload, _ := json.Marshal(forged)
	parts[1] = base64.RawURLEncoding.EncodeToString(payload)

	if _, err := p.Verify(context.Background(), Assertion{Token: strings.Join(parts, ".")}); err == nil {
		t.Fatal("a payload edited after signing was accepted")
	}
}

func TestOIDCProviderBindsTheTokenToTheBrowserFlow(t *testing.T) {
	now := time.Now().UTC()
	stub := newOIDCStub(t)
	p := newOIDCProviderFor(t, stub, now)

	claims := oidcClaims(stub.server.URL, now)
	claims["nonce"] = "the-flow-that-actually-began"
	token := stub.mint(t, claims)

	if _, err := p.Verify(context.Background(), Assertion{Token: token, Nonce: "the-flow-that-actually-began"}); err != nil {
		t.Fatalf("a correctly bound token was refused: %v", err)
	}
	if _, err := p.Verify(context.Background(), Assertion{Token: token, Nonce: "somebody-else's-flow"}); err == nil {
		t.Fatal("a token from another browser's flow was accepted")
	}
}

func TestOIDCProviderFailsClosedWhenTheIdPIsUnreachable(t *testing.T) {
	now := time.Now().UTC()
	stub := newOIDCStub(t)
	p := newOIDCProviderFor(t, stub, now)
	token := stub.mint(t, oidcClaims(stub.server.URL, now))
	if _, err := p.Verify(context.Background(), Assertion{Token: token}); err != nil {
		t.Fatalf("precondition: %v", err)
	}

	stub.server.Close()
	// The key set is cached, so this asserts the READINESS probe reports the truth rather than the
	// cache — a health signal answered from a five-minute-old cache is a report about the past.
	if err := p.Reachable(context.Background()); err == nil {
		t.Fatal("Reachable() said yes about an IdP that is gone")
	} else if !errors.Is(err, ErrIdPUnreachable) {
		t.Fatalf("err = %v; an outage must be distinguishable from a bad assertion IN THE LOG", err)
	}
	if _, err := p.Verify(context.Background(), Assertion{Token: token}); err == nil {
		t.Fatal("a session was verifiable against a key set the IdP can no longer vouch for")
	}
}

// ── SAML ────────────────────────────────────────────────────────────────────────────────────────

type samlStub struct {
	server   *httptest.Server
	key      *rsa.PrivateKey
	certDER  []byte
	entityID string
	down     bool
}

func newSAMLStub(t *testing.T, entityID string) *samlStub {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "heros-test-admin-idp"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	s := &samlStub{key: key, certDER: der, entityID: entityID}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if s.down {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("content-type", "application/xml")
		fmt.Fprintf(w,
			`<md:EntityDescriptor xmlns:md="%s" entityID="%s"><md:IDPSSODescriptor `+
				`protocolSupportEnumeration="%s"><md:KeyDescriptor use="signing">`+
				`<ds:KeyInfo xmlns:ds="%s"><ds:X509Data><ds:X509Certificate>%s</ds:X509Certificate>`+
				`</ds:X509Data></ds:KeyInfo></md:KeyDescriptor></md:IDPSSODescriptor></md:EntityDescriptor>`,
			samlNSMetadata, entityID, samlNSProtocol, samlNSDsig, base64.StdEncoding.EncodeToString(der))
	}))
	t.Cleanup(s.server.Close)
	return s
}

const testACS = "https://admin.heros.test/auth/saml/acs"

func samlResponseXML(entityID, spEntityID, inResponseTo, assertionID, nameID string, now time.Time) string {
	iso := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	return fmt.Sprintf(
		`<samlp:Response xmlns:samlp="%s" xmlns:saml="%s" ID="_r%s" Version="2.0" IssueInstant="%s" `+
			`Destination="%s" InResponseTo="%s"><saml:Issuer>%s</saml:Issuer>`+
			`<samlp:Status><samlp:StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"/></samlp:Status>`+
			`<saml:Assertion ID="%s" Version="2.0" IssueInstant="%s"><saml:Issuer>%s</saml:Issuer><!--SIGNATURE-->`+
			`<saml:Subject><saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">%s</saml:NameID>`+
			`<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer">`+
			`<saml:SubjectConfirmationData Recipient="%s" InResponseTo="%s" NotOnOrAfter="%s"/>`+
			`</saml:SubjectConfirmation></saml:Subject>`+
			`<saml:Conditions NotBefore="%s" NotOnOrAfter="%s">`+
			`<saml:AudienceRestriction><saml:Audience>%s</saml:Audience></saml:AudienceRestriction>`+
			`</saml:Conditions></saml:Assertion></samlp:Response>`,
		samlNSProtocol, samlNSAssertion, assertionID, iso(now), testACS, inResponseTo, entityID,
		assertionID, iso(now), entityID, nameID, testACS, inResponseTo, iso(now.Add(time.Minute)),
		iso(now.Add(-time.Second)), iso(now.Add(time.Minute)), spEntityID)
}

// sign computes the digest with THIS package's canonicalizer — see the file comment for why that is
// acceptable, and which test makes it so.
func (s *samlStub) sign(t *testing.T, doc, signedID string) string {
	t.Helper()
	parsed, err := parseXML(strings.Replace(doc, "<!--SIGNATURE-->", "", 1))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var target *xmlElement
	walkXML(parsed, func(el *xmlElement) {
		if v, ok := attrOf(el, "ID"); ok && v == signedID {
			target = el
		}
	})
	if target == nil {
		t.Fatalf("no element with ID %s", signedID)
	}
	digest := sha256.Sum256([]byte(canonicalizeXML(target, nil)))
	signedInfo := fmt.Sprintf(
		`<ds:SignedInfo xmlns:ds="%s"><ds:CanonicalizationMethod Algorithm="%s"></ds:CanonicalizationMethod>`+
			`<ds:SignatureMethod Algorithm="http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"></ds:SignatureMethod>`+
			`<ds:Reference URI="#%s"><ds:Transforms><ds:Transform Algorithm="%s"></ds:Transform>`+
			`<ds:Transform Algorithm="%s"></ds:Transform></ds:Transforms>`+
			`<ds:DigestMethod Algorithm="http://www.w3.org/2001/04/xmlenc#sha256"></ds:DigestMethod>`+
			`<ds:DigestValue>%s</ds:DigestValue></ds:Reference></ds:SignedInfo>`,
		samlNSDsig, samlExcC14N, signedID, samlEnveloped, samlExcC14N,
		base64.StdEncoding.EncodeToString(digest[:]))
	siDigest := sha256.Sum256([]byte(signedInfo))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, 5 /* crypto.SHA256 */, siDigest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return strings.Replace(doc, "<!--SIGNATURE-->", fmt.Sprintf(
		`<ds:Signature xmlns:ds="%s">%s<ds:SignatureValue>%s</ds:SignatureValue></ds:Signature>`,
		samlNSDsig, signedInfo, base64.StdEncoding.EncodeToString(sig)), 1)
}

func newSAMLProviderFor(t *testing.T, stub *samlStub, now time.Time) *SAMLProvider {
	t.Helper()
	p, err := NewSAMLProvider(SAMLProviderConfig{
		EntityID: stub.entityID, SPEntityID: "urn:heros:admin-console",
		MetadataURL: stub.server.URL + "/metadata", ACS: []string{testACS},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	return p
}

func TestSAMLProviderVerifiesARealAssertionAndReadsNoMFAClaim(t *testing.T) {
	now := time.Now().UTC()
	stub := newSAMLStub(t, "urn:heros:test:admin-idp")
	p := newSAMLProviderFor(t, stub, now)

	doc := stub.sign(t, samlResponseXML(stub.entityID, "urn:heros:admin-console", "_req1", "_a1", "op@heros.test", now), "_a1")
	claims, err := p.Verify(context.Background(), Assertion{
		Token: base64.StdEncoding.EncodeToString([]byte(doc)), Nonce: "_req1",
	})
	if err != nil {
		t.Fatalf("a valid signed assertion was refused: %v", err)
	}
	if claims.Subject != "op@heros.test" {
		t.Fatalf("subject = %q", claims.Subject)
	}
	if claims.MFAFactor != "" {
		t.Fatalf("the provider reported a factor (%q) it did not itself verify", claims.MFAFactor)
	}
}

func TestSAMLProviderRefusesTheMutatedResponse(t *testing.T) {
	now := time.Now().UTC()
	stub := newSAMLStub(t, "urn:heros:test:admin-idp")
	p := newSAMLProviderFor(t, stub, now)
	base := func() string {
		return samlResponseXML(stub.entityID, "urn:heros:admin-console", "_req1", "_a1", "op@heros.test", now)
	}

	cases := []struct {
		name  string
		build func() string
		nonce string
	}{
		{
			name: "a character changed after signing",
			build: func() string {
				return strings.Replace(stub.sign(t, base(), "_a1"), "op@heros.test", "root@heros.test", 1)
			},
			nonce: "_req1",
		},
		{
			name: "an assertion minted for another service provider",
			build: func() string {
				doc := samlResponseXML(stub.entityID, "urn:somebody:else", "_req1", "_a1", "op@heros.test", now)
				return stub.sign(t, doc, "_a1")
			},
			nonce: "_req1",
		},
		{
			name: "an unsolicited assertion answering no request of ours",
			build: func() string {
				return stub.sign(t, samlResponseXML(stub.entityID, "urn:heros:admin-console", "_never-asked", "_a1", "op@heros.test", now), "_a1")
			},
			nonce: "_req1",
		},
		{
			name:  "no signature at all",
			build: func() string { return strings.Replace(base(), "<!--SIGNATURE-->", "", 1) },
			nonce: "_req1",
		},
		{
			name: "an expired assertion",
			build: func() string {
				old := now.Add(-time.Hour)
				return stub.sign(t, samlResponseXML(stub.entityID, "urn:heros:admin-console", "_req1", "_a1", "op@heros.test", old), "_a1")
			},
			nonce: "_req1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := base64.StdEncoding.EncodeToString([]byte(tc.build()))
			if _, err := p.Verify(context.Background(), Assertion{Token: token, Nonce: tc.nonce}); err == nil {
				t.Fatal("the mutated response was accepted")
			}
		})
	}
}

func TestSAMLProviderIsNotFooledBySignatureWrapping(t *testing.T) {
	now := time.Now().UTC()
	stub := newSAMLStub(t, "urn:heros:test:admin-idp")
	p := newSAMLProviderFor(t, stub, now)

	// A genuinely signed assertion, with a FORGED sibling inserted before it. The verifier may accept
	// the document — the signature IS valid — but the claims must come from the element it verified.
	signed := stub.sign(t, samlResponseXML(stub.entityID, "urn:heros:admin-console", "_req1", "_a1", "op@heros.test", now), "_a1")
	forged := fmt.Sprintf(
		`<saml:Assertion ID="_evil" Version="2.0" IssueInstant="%s"><saml:Issuer>%s</saml:Issuer>`+
			`<saml:Subject><saml:NameID>superadmin@heros.test</saml:NameID></saml:Subject></saml:Assertion>`,
		now.Format(time.RFC3339), stub.entityID)
	wrapped := strings.Replace(signed, `<saml:Assertion ID="_a1"`, forged+`<saml:Assertion ID="_a1"`, 1)

	claims, err := p.Verify(context.Background(), Assertion{
		Token: base64.StdEncoding.EncodeToString([]byte(wrapped)), Nonce: "_req1",
	})
	if err != nil {
		return // refusing outright is also correct
	}
	if claims.Subject != "op@heros.test" {
		t.Fatalf("the verifier read the UNSIGNED assertion — wrapping succeeded (subject = %q)", claims.Subject)
	}
}

func TestSAMLProviderFailsClosedWhenMetadataIsUnreachable(t *testing.T) {
	now := time.Now().UTC()
	stub := newSAMLStub(t, "urn:heros:test:admin-idp")
	p := newSAMLProviderFor(t, stub, now)
	stub.down = true
	if err := p.Reachable(context.Background()); err == nil {
		t.Fatal("Reachable() said yes about an IdP serving 503")
	}
	doc := stub.sign(t, samlResponseXML(stub.entityID, "urn:heros:admin-console", "_req1", "_a1", "op@heros.test", now), "_a1")
	if _, err := p.Verify(context.Background(), Assertion{Token: base64.StdEncoding.EncodeToString([]byte(doc)), Nonce: "_req1"}); err == nil {
		t.Fatal("an assertion was verified with no reachable certificate source")
	}
}

func TestASAMLProviderRefusesAWildcardACS(t *testing.T) {
	_, err := NewSAMLProvider(SAMLProviderConfig{
		EntityID: "urn:a", SPEntityID: "urn:b", MetadataURL: "https://idp.test/metadata",
		ACS: []string{"https://*.heros.test/acs"},
	})
	if err == nil {
		t.Fatal("a wildcard ACS was accepted — an allowlist with a wildcard is not an allowlist")
	}
}

// ── The platform-verified second factor ─────────────────────────────────────────────────────────

func totpSecrets(t *testing.T, adminID, seedBase32 string) Secrets {
	t.Helper()
	s, err := NewManagedSecrets(providergateway.StaticSecrets{
		SecretAdminSSOSigning:     {APIKey: "sso"},
		SecretAdminMFASigning:     {APIKey: "mfa"},
		SecretAdminSessionSigning: {APIKey: "session"},
		TOTPSeedName(adminID):     {APIKey: seedBase32},
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	return s
}

func TestTOTPIsVerifiedAgainstThePlatformsOwnSeedAndIsSingleUse(t *testing.T) {
	now := time.Now().UTC()
	seed := []byte("heros-operator-totp-seed")
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(seed)

	factors := NewFactorStore()
	if err := factors.Enroll(EnrolledFactor{AdminID: "adm-1", Kind: FactorTOTP, SecretName: TOTPSeedName("adm-1")}); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	v, err := NewPlatformFactors(PlatformFactorsConfig{
		Factors: factors, Secrets: totpSecrets(t, "adm-1", encoded), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	code := totpCode(seed, now.Unix()/30)
	kind, err := v.Verify(context.Background(), "adm-1", PresentedFactor{TOTP: code}, nil)
	if err != nil || kind != FactorTOTP {
		t.Fatalf("a correct code was refused: kind=%q err=%v", kind, err)
	}
	// A TOTP code is valid for a whole 30-second step, so without a one-time guard a captured code is
	// usable twice — by the operator, and by whoever was watching.
	if _, err := v.Verify(context.Background(), "adm-1", PresentedFactor{TOTP: code}, nil); err == nil {
		t.Fatal("the same code was accepted twice")
	}
	if _, err := v.Verify(context.Background(), "adm-1", PresentedFactor{TOTP: "000000"}, nil); err == nil {
		t.Fatal("a wrong code was accepted")
	}
}

// webAuthnCredential mints an ES256 credential and signs assertions the way an authenticator does.
type webAuthnCredential struct {
	key      *ecdsa.PrivateKey
	spki     []byte
	credID   []byte
	rpIDHash [32]byte
}

func newWebAuthnCredential(t *testing.T, rpID string) *webAuthnCredential {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("spki: %v", err)
	}
	return &webAuthnCredential{key: key, spki: spki, credID: []byte("credential-1"), rpIDHash: sha256.Sum256([]byte(rpID))}
}

func (c *webAuthnCredential) assert(t *testing.T, challenge []byte, origin string, flags byte, signCount uint32) *WebAuthnAssertion {
	t.Helper()
	authData := make([]byte, 37)
	copy(authData[:32], c.rpIDHash[:])
	authData[32] = flags
	binary.BigEndian.PutUint32(authData[33:37], signCount)
	clientData, _ := json.Marshal(map[string]string{
		"type": "webauthn.get", "challenge": base64.RawURLEncoding.EncodeToString(challenge), "origin": origin,
	})
	clientHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte(nil), authData...), clientHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, c.key, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return &WebAuthnAssertion{CredentialID: c.credID, AuthenticatorData: authData, ClientDataJSON: clientData, Signature: sig}
}

func TestWebAuthnIsOriginBoundAndUserVerified(t *testing.T) {
	const rpID = "admin.heros.test"
	const origin = "https://admin.heros.test"
	now := time.Now().UTC()
	cred := newWebAuthnCredential(t, rpID)

	factors := NewFactorStore()
	if err := factors.Enroll(EnrolledFactor{
		AdminID: "adm-1", Kind: FactorWebAuthn, CredentialID: cred.credID, PublicKeySPKI: cred.spki,
	}); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	v, err := NewPlatformFactors(PlatformFactorsConfig{
		Factors: factors, Secrets: totpSecrets(t, "adm-1", "AAAA"), RPID: rpID,
		Origins: []string{origin}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	challenge := []byte("server-minted-challenge")
	const upUV = 0x01 | 0x04

	if kind, err := v.Verify(context.Background(), "adm-1", PresentedFactor{WebAuthn: cred.assert(t, challenge, origin, upUV, 1)}, challenge); err != nil || kind != FactorWebAuthn {
		t.Fatalf("a valid assertion was refused: kind=%q err=%v", kind, err)
	}

	t.Run("a phishing origin is refused", func(t *testing.T) {
		// The origin check IS the phishing resistance. Without it, WebAuthn is a slower TOTP.
		a := cred.assert(t, challenge, "https://admin-heros.test.evil.example", upUV, 5)
		if _, err := v.Verify(context.Background(), "adm-1", PresentedFactor{WebAuthn: a}, challenge); err == nil {
			t.Fatal("an assertion signed on an attacker's origin was accepted")
		}
	})
	t.Run("a touch without user verification is refused", func(t *testing.T) {
		a := cred.assert(t, challenge, origin, 0x01, 6)
		if _, err := v.Verify(context.Background(), "adm-1", PresentedFactor{WebAuthn: a}, challenge); err == nil {
			t.Fatal("presence without verification was accepted as a second factor")
		}
	})
	t.Run("another login's challenge is refused", func(t *testing.T) {
		a := cred.assert(t, []byte("some-other-login"), origin, upUV, 7)
		if _, err := v.Verify(context.Background(), "adm-1", PresentedFactor{WebAuthn: a}, challenge); err == nil {
			t.Fatal("an assertion over another challenge was accepted")
		}
	})
	t.Run("a counter that did not advance is refused", func(t *testing.T) {
		// Either a replay or a cloned authenticator. Both are denials.
		a := cred.assert(t, challenge, origin, upUV, 1)
		if _, err := v.Verify(context.Background(), "adm-1", PresentedFactor{WebAuthn: a}, challenge); err == nil {
			t.Fatal("a non-advancing signature counter was accepted")
		}
	})
}

// ── The authenticator: the invariants P22 adds ──────────────────────────────────────────────────

func operatorLayer(t *testing.T, provider IdentityProvider, factors FactorVerifier, production bool) (*Authenticator, *PrincipalStore, *SessionStore) {
	t.Helper()
	secrets := totpSecrets(t, "adm-1", "AAAA")
	principals := NewPrincipalStore()
	if err := principals.Put(Principal{AdminID: "adm-1", SSOSubject: "op-1", MFAEnrolled: true, Status: StatusActive}); err != nil {
		t.Fatalf("principal: %v", err)
	}
	sessions, err := NewSessionStore(SessionConfig{Secrets: secrets, Principals: principals})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	authn, err := NewAuthenticatorFor(AuthenticatorConfig{
		Provider: provider, Principals: principals, Sessions: sessions, Factors: factors, Production: production,
	})
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	return authn, principals, sessions
}

func TestARealProviderWithoutAPlatformFactorVerifierRefusesToConstruct(t *testing.T) {
	// The failure this refusal prevents looks like success: every login works, nothing errors, and the
	// operator surface has quietly become single-factor. There is no request to observe it on, so the
	// only version of this check that cannot be missed is one at construction.
	stub := newOIDCStub(t)
	provider := newOIDCProviderFor(t, stub, time.Now().UTC())
	principals := NewPrincipalStore()
	sessions, err := NewSessionStore(SessionConfig{Secrets: totpSecrets(t, "adm-1", "AAAA")})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if _, err := NewAuthenticatorFor(AuthenticatorConfig{
		Provider: provider, Principals: principals, Sessions: sessions,
	}); err == nil {
		t.Fatal("a real OIDC provider was wired with no platform-verified second factor")
	}
}

func TestTheFixtureIssuerRefusesProduction(t *testing.T) {
	secrets, err := FixtureSecrets("sso", "mfa", "session")
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	provider, err := NewHMACProvider(HMACProviderConfig{Issuer: "urn:test", Secrets: secrets, TestMode: true})
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	principals := NewPrincipalStore()
	sessions, err := NewSessionStore(SessionConfig{Secrets: secrets})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if _, err := NewAuthenticatorFor(AuthenticatorConfig{
		Provider: provider, Principals: principals, Sessions: sessions, Production: true,
	}); err == nil {
		t.Fatal("a production operator console accepted the test-mode fixture issuer")
	}
}

func TestValidSSOWithNoPlatformFactorIssuesNoSession(t *testing.T) {
	now := time.Now().UTC()
	seed := []byte("operator-seed")
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(seed)
	stub := newOIDCStub(t)
	provider := newOIDCProviderFor(t, stub, now)

	factorStore := NewFactorStore()
	if err := factorStore.Enroll(EnrolledFactor{AdminID: "adm-1", Kind: FactorTOTP, SecretName: TOTPSeedName("adm-1")}); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	verifier, err := NewPlatformFactors(PlatformFactorsConfig{
		Factors: factorStore, Secrets: totpSecrets(t, "adm-1", encoded), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	authn, _, _ := operatorLayer(t, provider, verifier, false)

	// The ID token CLAIMS `amr: ["mfa"]`. That is the misconfigured-IdP case, and it must still deny.
	token := stub.mint(t, oidcClaims(stub.server.URL, now))
	if _, _, err := authn.Authenticate(context.Background(), Assertion{Token: token}); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("err = %v; a valid SSO assertion with an MFA CLAIM and no platform-verified factor must be ErrMFARequired", err)
	}

	// With a factor the platform itself verifies, the same assertion issues a session.
	sess, bearer, err := authn.Authenticate(context.Background(), Assertion{
		Token: token, Factor: PresentedFactor{TOTP: totpCode(seed, now.Unix()/30)},
	})
	if err != nil {
		t.Fatalf("a verified factor was refused: %v", err)
	}
	if bearer == "" || sess.MFAFactor != FactorTOTP {
		t.Fatalf("session = %+v; the factor the PLATFORM verified is what is recorded", sess)
	}
}

func TestDisablingAnOperatorKillsTheirLiveSessionsAtTheNextRequest(t *testing.T) {
	now := time.Now().UTC()
	seed := []byte("operator-seed")
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(seed)
	stub := newOIDCStub(t)
	provider := newOIDCProviderFor(t, stub, now)
	factorStore := NewFactorStore()
	if err := factorStore.Enroll(EnrolledFactor{AdminID: "adm-1", Kind: FactorTOTP, SecretName: TOTPSeedName("adm-1")}); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	verifier, err := NewPlatformFactors(PlatformFactorsConfig{
		Factors: factorStore, Secrets: totpSecrets(t, "adm-1", encoded), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	authn, principals, sessions := operatorLayer(t, provider, verifier, false)

	_, bearer, err := authn.Authenticate(context.Background(), Assertion{
		Token:  stub.mint(t, oidcClaims(stub.server.URL, now)),
		Factor: PresentedFactor{TOTP: totpCode(seed, now.Unix()/30)},
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := sessions.Authorize(context.Background(), bearer); err != nil {
		t.Fatalf("precondition: %v", err)
	}

	// Only DISABLE — deliberately not RevokeAllFor. "A must be accompanied by B" held by convention is
	// an invariant one hurried call site breaks, so the reconcile read on the authorization path is
	// what actually carries it.
	if err := principals.Disable("adm-1"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := sessions.Authorize(context.Background(), bearer); err == nil {
		t.Fatal("a disabled operator's live session was still authorized")
	}
	// And no new session either, with a valid assertion and a freshly verified factor.
	if _, _, err := authn.Authenticate(context.Background(), Assertion{
		Token:  stub.mint(t, oidcClaims(stub.server.URL, now)),
		Factor: PresentedFactor{TOTP: totpCode(seed, now.Unix()/30+1)},
	}); err == nil {
		t.Fatal("a disabled operator obtained a new session")
	}
}

func TestOffboardDisablesAndRevokesInThatOrder(t *testing.T) {
	secrets, err := FixtureSecrets("sso", "mfa", "session")
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	principals := NewPrincipalStore()
	if err := principals.Put(Principal{AdminID: "adm-9", SSOSubject: "op-9", MFAEnrolled: true, Status: StatusActive}); err != nil {
		t.Fatalf("principal: %v", err)
	}
	sessions, err := NewSessionStore(SessionConfig{Secrets: secrets, Principals: principals})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	p, _ := principals.ByID("adm-9")
	if _, _, err := sessions.Issue(context.Background(), p, FactorWebAuthn); err != nil {
		t.Fatalf("issue: %v", err)
	}
	revoked, err := sessions.Offboard("adm-9", "adm-superadmin")
	if err != nil {
		t.Fatalf("offboard: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("revoked %d sessions, expected 1", revoked)
	}
	if got, _ := principals.ByID("adm-9"); got.Active() {
		t.Fatal("offboarding left the principal active")
	}
}
