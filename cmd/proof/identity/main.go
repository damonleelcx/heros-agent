// Command identity runs the P22 identity stack against the REAL
// github.com/NousResearch/hermes-agent repository.
//
// It follows the convention the other phase demos established (p5hermes … p21hermes): point the phase
// at an actual checkout rather than a fixture, because a fixture proves the code path and nothing about
// any real codebase.
//
// # What this demo is actually about, which is not what the other ones are about
//
// Every previous hermes runner asks the same shape of question: what does the platform do TO this
// repository. P22 asks a different one, and the difference is the point.
//
// P22 is identity for the people who operate the platform and for the tenant that owns the work — it
// is not identity for the transformed program. [ADR-002] draws that line: hermes-agent calls its own
// model providers with its own credentials, and our identity system does not reach into it. A demo
// that "added SSO to hermes-agent" would be demonstrating a boundary violation.
//
// So this runner does three things, and the first is the most useful:
//
//  1. **Surveys the real checkout for identity surface** and reports it as OUT OF SCOPE, with counts.
//     That turns the ADR-002 boundary from a sentence in a document into a number against a real
//     repository — and it is the thing a reviewer asking "did you accidentally couple these" can check.
//  2. **Runs the operator identity path for real**: a real OIDC admin IdP over a real socket, an ID
//     token signed with a real key, and a second factor the PLATFORM verifies rather than one the IdP
//     claims. Including the denial, which is the case that matters.
//  3. **Reports the readiness signal** the platform publishes for identity, including what it says when
//     the IdP is unreachable.
//
// # What is REAL here, and what is NOT — stated, not implied
//
// REAL: the hermes checkout (its files and its HEAD commit are read), the whole operator identity code
// path (`internal/adminidentity` — discovery, JWKS, ID-token verification, TOTP/WebAuthn verification,
// session issue and revocation), and the `/readyz` aggregation.
//
// NOT REAL: the identity provider is in-process. It signs with a real RSA key over a real socket and
// the verifier does not know the difference, but nobody's corporate directory is involved.
//
// NOT COVERED IN THIS PROCESS: the CUSTOMER seam. It lives in the console's BFF (TypeScript, ADR-008),
// so a Go binary cannot exercise it and pretending otherwise would be the demo lying about its own
// coverage. The command to run it is printed at the end, and it is a real end-to-end run against a
// signing IdP — `web/console`'s `tests/sso-identity.test.mjs` and `npm run dev:sso`.
//
// # Usage
//
//	git clone https://github.com/nousresearch/hermes-agent /tmp/hermes-agent
//	go run ./cmd/proof/identity -repo /tmp/hermes-agent
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // RFC 6238 specifies HMAC-SHA1 for TOTP
	"crypto/sha256"
	"crypto/x509"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// Tenant is the customer this checkout belongs to, in the platform's own vocabulary. The same id the
// other hermes runners use, so the four demos describe one tenant rather than four.
const Tenant = "cus_nousresearch"

// AdminIssuer is the operator IdP's issuer identity in this demo. The operator identity domain is
// disjoint from the customer's, and the naming says so.
const AdminID = "adm-hermes-operator"

func main() {
	repo := flag.String("repo", "", "path to a github.com/nousresearch/hermes-agent checkout")
	flag.Parse()
	if strings.TrimSpace(*repo) == "" {
		log.Fatal("p22hermes: -repo is required (git clone https://github.com/nousresearch/hermes-agent)")
	}
	if err := run(*repo); err != nil {
		log.Fatalf("p22hermes: %v", err)
	}
}

func run(repo string) error {
	head, err := headCommit(repo)
	if err != nil {
		return err
	}
	fmt.Printf("P22 · SSO & Identity — against a real checkout\n")
	fmt.Printf("  repository  github.com/nousresearch/hermes-agent\n")
	fmt.Printf("  path        %s\n", repo)
	fmt.Printf("  HEAD        %s\n", head)
	fmt.Printf("  tenant      %s\n\n", Tenant)

	survey, err := surveyIdentitySurface(repo)
	if err != nil {
		return err
	}
	printSurvey(survey)

	if err := runOperatorIdentity(); err != nil {
		return err
	}
	if err := runReadiness(); err != nil {
		return err
	}
	printCustomerSeamInstructions()
	return nil
}

func headCommit(repo string) (string, error) {
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return "", fmt.Errorf("%s is not a git checkout: %w", repo, err)
	}
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("reading HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ── 1 · The ADR-002 boundary, measured ──────────────────────────────────────────────────────────

type surveyResult struct {
	Files      int
	WithAuth   int
	Categories map[string]int
	Examples   map[string][]string
}

// identityPatterns are the shapes that mean "this file handles identity or credentials".
//
// Deliberately broad. The point of this survey is to find every place P22 might be *thought* to reach,
// and a narrow pattern would produce a comfortingly small number that proves nothing.
var identityPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"api keys / bearer tokens", regexp.MustCompile(`(?i)\b(api[_-]?key|bearer|authorization\s*header|x-api-key)\b`)},
	{"OAuth / OIDC", regexp.MustCompile(`(?i)\b(oauth|oidc|openid|id_token|access_token|refresh_token)\b`)},
	{"SAML", regexp.MustCompile(`(?i)\bsaml\b`)},
	{"sessions / login", regexp.MustCompile(`(?i)\b(session|login|sign[_-]?in|logout)\b`)},
	{"provider credentials", regexp.MustCompile(`(?i)\b(ANTHROPIC_API_KEY|OPENAI_API_KEY|HF_TOKEN|credential)\b`)},
}

func surveyIdentitySurface(repo string) (*surveyResult, error) {
	result := &surveyResult{Categories: map[string]int{}, Examples: map[string][]string{}}
	err := filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable path is skipped, never a reason to abandon the survey
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "__pycache__", "dist", "build", ".venv":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".py", ".ts", ".tsx", ".js", ".go", ".rs", ".java", ".kt":
		default:
			return nil
		}
		result.Files++
		body, err := os.ReadFile(path)
		if err != nil || len(body) > 2<<20 {
			return nil
		}
		text := string(body)
		hit := false
		for _, p := range identityPatterns {
			if p.re.MatchString(text) {
				result.Categories[p.name]++
				rel, _ := filepath.Rel(repo, path)
				if len(result.Examples[p.name]) < 3 {
					result.Examples[p.name] = append(result.Examples[p.name], rel)
				}
				hit = true
			}
		}
		if hit {
			result.WithAuth++
		}
		return nil
	})
	return result, err
}

func printSurvey(s *surveyResult) {
	fmt.Printf("1 · The ADR-002 boundary, measured against this checkout\n")
	fmt.Printf("  %d source files scanned; %d handle identity or credentials in some form.\n\n", s.Files, s.WithAuth)
	for _, p := range identityPatterns {
		n := s.Categories[p.name]
		if n == 0 {
			fmt.Printf("  %-26s  %4d files\n", p.name, n)
			continue
		}
		fmt.Printf("  %-26s  %4d files   e.g. %s\n", p.name, n, strings.Join(s.Examples[p.name], ", "))
	}
	fmt.Printf(`
  🔴 EVERY ONE OF THESE IS OUT OF SCOPE FOR P22, and that is the finding rather than a caveat.

  hermes-agent authenticates to ITS OWN model providers with ITS OWN credentials. P22 supplies
  identity for the people who operate the platform and for the tenant that owns the work — the
  console's tenant principal, and the operator principal that can halt the fleet. It does not reach
  into the transformed program, and if it did, ADR-002 would be violated in the most expensive
  possible direction: our identity system would become a dependency of the customer's runtime.

  The number above is what makes that checkable. A future change that "added SSO to hermes-agent"
  would move it, and moving it is the review conversation.

`)
}

// ── 2 · The operator identity path, for real ────────────────────────────────────────────────────

func runOperatorIdentity() error {
	fmt.Printf("2 · The operator identity path — a real IdP, and a factor the PLATFORM verifies\n\n")

	now := time.Now().UTC()
	idp, err := startAdminIdP()
	if err != nil {
		return err
	}
	defer idp.Close()

	provider, err := adminidentity.NewOIDCProvider(adminidentity.OIDCProviderConfig{
		Issuer: idp.URL, ClientID: "heros-operator-console",
	})
	if err != nil {
		return err
	}
	fmt.Printf("  IdP           %s  (%s, test_mode=%v)\n", idp.URL, provider.Describe().Kind, provider.Describe().TestMode)

	// The TOTP seed lives in the secrets manager under a reserved logical name, never in the
	// enrollment directory. The directory is an index; a directory row carrying a seed would be a
	// credential store with an ordinary backup policy.
	seed := []byte("hermes-operator-second-factor-seed")
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(seed)
	secrets, err := adminidentity.NewManagedSecrets(providergateway.StaticSecrets{
		adminidentity.SecretAdminSSOSigning:     {APIKey: "demo-sso"},
		adminidentity.SecretAdminMFASigning:     {APIKey: "demo-mfa"},
		adminidentity.SecretAdminSessionSigning: {APIKey: "demo-session"},
		adminidentity.TOTPSeedName(AdminID):     {APIKey: encoded},
	})
	if err != nil {
		return err
	}
	fmt.Printf("  secrets       %s (%s)\n", secrets.Describe().Kind, secrets.Describe().Detail)

	principals := adminidentity.NewPrincipalStore()
	if err := principals.Put(adminidentity.Principal{
		AdminID: AdminID, SSOSubject: "operator@heros.internal", MFAEnrolled: true,
		Status: adminidentity.StatusActive, CreatedAt: now,
	}); err != nil {
		return err
	}
	sessions, err := adminidentity.NewSessionStore(adminidentity.SessionConfig{
		Secrets: secrets, Principals: principals,
	})
	if err != nil {
		return err
	}
	factors := adminidentity.NewFactorStore()
	webauthn := newDemoCredential("admin.heros.internal")
	if err := factors.Enroll(adminidentity.EnrolledFactor{
		AdminID: AdminID, Kind: adminidentity.FactorWebAuthn,
		CredentialID: webauthn.credID, PublicKeySPKI: webauthn.spki,
	}); err != nil {
		return err
	}
	if err := factors.Enroll(adminidentity.EnrolledFactor{
		AdminID: AdminID, Kind: adminidentity.FactorTOTP, SecretName: adminidentity.TOTPSeedName(AdminID),
	}); err != nil {
		return err
	}
	verifier, err := adminidentity.NewPlatformFactors(adminidentity.PlatformFactorsConfig{
		Factors: factors, Secrets: secrets,
		RPID: "admin.heros.internal", Origins: []string{"https://admin.heros.internal"},
	})
	if err != nil {
		return err
	}
	authn, err := adminidentity.NewAuthenticatorFor(adminidentity.AuthenticatorConfig{
		Provider: provider, Principals: principals, Sessions: sessions, Factors: verifier, Production: true,
	})
	if err != nil {
		return err
	}
	fmt.Printf("  factors       webauthn (preferred), totp (fallback) — both platform-verified\n\n")

	ctx := context.Background()

	// (a) The denial that matters. The ID token CLAIMS multi-factor; the platform has not verified one.
	token := idp.mint(map[string]any{"amr": []string{"mfa", "hwk"}, "acr": "multi-factor"})
	_, _, err = authn.Authenticate(ctx, adminidentity.Assertion{Token: token})
	printOutcome("valid SSO, IdP CLAIMS mfa, no platform-verified factor",
		errors.Is(err, adminidentity.ErrMFARequired), err,
		"denied — the IdP's claim is a configuration of a system we do not control (NFR8)")

	// (b) The same assertion, with a factor the platform verified itself.
	code := totpFor(seed, time.Now())
	sess, bearer, err := authn.Authenticate(ctx, adminidentity.Assertion{
		Token: idp.mint(nil), Factor: adminidentity.PresentedFactor{TOTP: code},
	})
	printOutcome("valid SSO + platform-verified TOTP", err == nil, err,
		fmt.Sprintf("session issued, factor recorded as %q, expires %s", sess.MFAFactor, sess.ExpiresAt.Format(time.RFC3339)))

	// (c) A phished WebAuthn assertion: correct credential, correct challenge, attacker's origin.
	challenge := []byte("server-minted-challenge-for-this-login")
	phished := webauthn.assert(challenge, "https://admin-heros.internal.evil.example", 0x05, 9)
	_, err = verifier.Verify(ctx, AdminID, adminidentity.PresentedFactor{WebAuthn: phished}, challenge)
	printOutcome("WebAuthn signed on an attacker's origin", err != nil, nil,
		"denied — origin binding is the one thing WebAuthn gives over TOTP")

	// (d) Offboarding. Disable ONLY — deliberately not RevokeAllFor — because the invariant has to hold
	// when somebody performs half the procedure.
	if _, err := sessions.Authorize(ctx, bearer); err != nil {
		return fmt.Errorf("precondition: the live session should authorize: %w", err)
	}
	if err := principals.Disable(AdminID); err != nil {
		return err
	}
	_, err = sessions.Authorize(ctx, bearer)
	printOutcome("a DISABLED operator's live session, next request", err != nil, nil,
		"denied — the authorization path reconciles against the principal, so offboarding lands even when only half the procedure was performed")
	fmt.Println()
	return nil
}

func printOutcome(what string, ok bool, err error, detail string) {
	mark := "✗"
	if ok {
		mark = "✓"
	}
	fmt.Printf("  %s  %-52s  %s\n", mark, what, detail)
	if !ok && err != nil {
		fmt.Printf("       unexpected: %v\n", err)
	}
}

// ── 3 · The readiness signal ────────────────────────────────────────────────────────────────────

func runReadiness() error {
	fmt.Printf("3 · What the platform publishes about identity, for a monitor to read\n\n")

	server := api.New(nil, config.Config{})
	server.SetAdminIdentity(describer{adminidentity.ProviderInfo{
		Kind: adminidentity.ProviderKindOIDC, Issuer: "https://idp.heros.internal", TestMode: false,
	}})

	// A console whose IdP is answering, then one whose IdP is not. The console itself is healthy in
	// both — which is exactly the case a single component would report as fine.
	for _, c := range []struct {
		label     string
		reachable bool
		detail    string
	}{
		{"IdP answering", true, ""},
		{"IdP unreachable", false, "discovery returned 503"},
	} {
		console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"component": "console", "status": "ok",
				"identity_provider": map[string]any{
					"kind": "oidc", "issuer": "https://nousresearch.okta.com",
					"reachable": c.reachable, "detail": c.detail,
				},
			})
		}))
		server.SetIdentityProbe(api.NewHTTPIdentityProbe(console.URL))
		rec := httptest.NewRecorder()
		server.Mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		console.Close()

		var pretty map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &pretty)
		out, _ := json.MarshalIndent(pretty, "  ", "  ")
		fmt.Printf("  %s → HTTP %d\n  %s\n\n", c.label, rec.Code, out)
	}
	fmt.Printf("  Two components, not one: `customer_console` says the surface is down, `identity_provider`\n" +
		"  says the surface is up and nobody can sign in. Collapsing them gives a signal that is true and\n  useless.\n\n")
	return nil
}

type describer struct{ info adminidentity.ProviderInfo }

func (d describer) Describe() adminidentity.ProviderInfo { return d.info }

// ── The customer seam, and why this binary does not pretend to run it ───────────────────────────

func printCustomerSeamInstructions() {
	fmt.Printf(`4 · The CUSTOMER seam — not exercisable from this binary, and here is how to run it

  The customer identity seam is the console's, in TypeScript (ADR-008: the console binds an abstract
  tenant principal through one function, and that function lives with the console). A Go process
  cannot exercise it, and a demo that printed a green tick for something it did not run would be the
  exact failure the phase's own tests exist to prevent.

  It IS covered, end to end, against a real signing identity provider:

    cd web/console && npm test                 # 44 P22 cases: OIDC, SAML, replay, CSRF, cross-tenant
    cd web/console && npm run dev:sso          # OIDC in a real browser, tenant %s
    cd web/console && npm run dev:sso -- saml  # SAML in a real browser

  For a console pointed at this tenant with the open-core (unfederated) form:

    cd web/console && CONSOLE_TENANT_IDENTITY=configured \
      CONSOLE_TENANT_ASSERTIONS='{"local-dev-assertion":"%s"}' \
      CONSOLE_PLATFORM_CREDENTIAL=local-dev-credential-not-a-secret npm run dev

`, Tenant, Tenant)
}

// ── A real OIDC identity provider, in-process ───────────────────────────────────────────────────

type adminIdP struct {
	*httptest.Server
	key *rsa.PrivateKey
}

func startAdminIdP() (*adminIdP, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	idp := &adminIdP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 idp.URL,
			"authorization_endpoint": idp.URL + "/authorize",
			"token_endpoint":         idp.URL + "/token",
			"jwks_uri":               idp.URL + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "operator-1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	idp.Server = httptest.NewServer(mux)
	return idp, nil
}

func (i *adminIdP) mint(extra map[string]any) string {
	now := time.Now().UTC()
	claims := map[string]any{
		"iss": i.URL, "aud": "heros-operator-console", "sub": "operator@heros.internal",
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
	}
	for k, v := range extra {
		claims[k] = v
	}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "operator-1", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signing))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, i.key, 5 /* crypto.SHA256 */, digest[:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// ── A WebAuthn authenticator, in-process ────────────────────────────────────────────────────────

type demoCredential struct {
	key      *ecdsa.PrivateKey
	spki     []byte
	credID   []byte
	rpIDHash [32]byte
}

func newDemoCredential(rpID string) *demoCredential {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	spki, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	return &demoCredential{key: key, spki: spki, credID: []byte("hermes-operator-key"), rpIDHash: sha256.Sum256([]byte(rpID))}
}

func (c *demoCredential) assert(challenge []byte, origin string, flags byte, signCount uint32) *adminidentity.WebAuthnAssertion {
	authData := make([]byte, 37)
	copy(authData[:32], c.rpIDHash[:])
	authData[32] = flags
	binary.BigEndian.PutUint32(authData[33:37], signCount)
	clientData, _ := json.Marshal(map[string]string{
		"type": "webauthn.get", "challenge": base64.RawURLEncoding.EncodeToString(challenge), "origin": origin,
	})
	clientHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte(nil), authData...), clientHash[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, c.key, digest[:])
	return &adminidentity.WebAuthnAssertion{
		CredentialID: c.credID, AuthenticatorData: authData, ClientDataJSON: clientData, Signature: sig,
	}
}

// totpFor mirrors RFC 6238 so the demo can present a code the verifier will accept. It is the SIGNER's
// side of the same algorithm, not a second implementation of the check.
func totpFor(seed []byte, at time.Time) string {
	step := at.Unix() / 30
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	h := hmac.New(sha1.New, seed)
	h.Write(counter[:])
	mac := h.Sum(nil)
	offset := mac[len(mac)-1] & 0x0f
	value := binary.BigEndian.Uint32(mac[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}
