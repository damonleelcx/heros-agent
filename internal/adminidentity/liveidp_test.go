package adminidentity

import (
	"context"
	"os"
	"testing"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// liveidp_test.go probes a REAL operator identity provider, read-only.
//
// # Why this is in the repository rather than a throwaway script
//
// Every other test in this package proves the verifier against an IdP this repository wrote. That is
// the right way to test refusals — a fixture can be told to send a stale assertion, and Okta cannot —
// but it leaves one question open that only a real IdP answers: does the discovery document a real
// provider serves actually parse, and does its key set actually load. A JWKS with an unexpected field
// order, an `e` encoded with padding, a discovery `issuer` with a trailing slash: each of those breaks
// a verifier that passes every fixture test, and each has cost somebody an afternoon.
//
// It SKIPS unless pointed at one, so it is free in CI and available the moment a deployment has an IdP:
//
//	PROBE_ISSUER=https://<org>.okta.com/oauth2/default \
//	PROBE_CLIENT_ID=0oa… \
//	go test ./internal/adminidentity -run TestLiveIdP -v
//
// # What it deliberately does NOT do
//
// It never completes a sign-in. Completing one requires a human to authenticate at their own IdP, with
// their own password, at their own keyboard — which is exactly the boundary this whole phase is built
// to respect, and not a step to automate around. No client secret is used and no code is exchanged.
func TestLiveIdP(t *testing.T) {
	issuer := os.Getenv("PROBE_ISSUER")
	clientID := os.Getenv("PROBE_CLIENT_ID")
	if issuer == "" || clientID == "" {
		t.Skip("no live IdP configured — set PROBE_ISSUER and PROBE_CLIENT_ID")
	}
	const redirect = "https://admin.heros.test/auth/callback"

	secrets, err := NewManagedSecrets(providergateway.StaticSecrets{
		SecretAdminSSOSigning:       {APIKey: "unused-by-this-probe"},
		SecretAdminMFASigning:       {APIKey: "unused-by-this-probe"},
		SecretAdminSessionSigning:   {APIKey: "unused-by-this-probe"},
		SecretAdminOIDCClientSecret: {APIKey: "unused-by-this-probe"},
	})
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	provider, err := NewOIDCProvider(OIDCProviderConfig{
		Issuer: issuer, ClientID: clientID, Secrets: secrets, Redirects: []string{redirect},
	})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}

	// Discovery + JWKS against the real org. `Reachable` clears the cache first, so this is a live
	// answer rather than a report about the past.
	if err := provider.Reachable(context.Background()); err != nil {
		t.Fatalf("the real IdP did not answer: %v", err)
	}
	t.Logf("discovery and key set loaded; Describe() = %+v", provider.Describe())

	url, err := provider.AuthorizationURL(context.Background(), AuthorizationRequest{
		State: "probe-state", Nonce: "probe-nonce",
		CodeChallenge: PKCEChallenge("probe-verifier-long-enough-to-be-a-real-one"),
		RedirectURI:   redirect,
	})
	if err != nil {
		t.Fatalf("authorization URL: %v", err)
	}
	t.Logf("authorization URL: %s", url)
	for _, want := range []string{"response_type=code", "code_challenge_method=S256", "client_id=" + clientID} {
		if !contains(url, want) {
			t.Fatalf("the authorization URL is missing %q", want)
		}
	}
	if contains(url, "response_type=id_token") || contains(url, "response_type=token") {
		t.Fatal("the authorization URL asks for an implicit token")
	}

	// The allowlist holds against the real provider too — the check is ours, not the IdP's, and it
	// runs before the browser moves.
	if _, err := provider.AuthorizationURL(context.Background(), AuthorizationRequest{
		State: "s", Nonce: "n", CodeChallenge: "c", RedirectURI: "https://evil.example/callback",
	}); err == nil {
		t.Fatal("an off-allowlist redirect was accepted")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) <= len(haystack) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
