package adminidentity

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// wire.go selects the live operator identity provider from the DEPLOYMENT's environment.
//
// # Why this exists, and why it is the only place that chooses
//
// Before it, `adminfixture` constructed an `HMACProvider` unconditionally, which meant a deployment
// could not point the operator console at a real IdP however it was configured — the real providers
// existed and nothing selected them. That is the specific gap this file closes.
//
// It is one function rather than a branch at each call site for the reason `logging-conventions` gives
// about event names: a decision spelled in three places is three decisions, and the one that matters
// here is "is this deployment's operator sign-in real". `Describe()` reports the answer on `/readyz`,
// so what was chosen is externally checkable rather than inferred from a config file nobody can read
// from the outside.
//
// # Fail static, at boot
//
// A mode this file does not recognise, or a mode missing the values it needs, is an error at STARTUP.
// The alternative — boot and refuse every login — is the failure mode `identity.ts` already rejects on
// the customer side: a process that starts and then denies everyone looks like a broken product, while
// a process that will not start says exactly what is wrong, once, to the person doing the deploy.
//
// The third possibility is the dangerous one and is what the explicit `test` mode is for: booting with
// a FIXTURE issuer while an operator believes the real IdP is live. `ADMIN_IDENTITY_MODE` has no
// default for that reason — a deployment states which it is.

// Admin identity modes. `test` is the fixture issuer; the other two are real.
const (
	// ModeTest is the shipped HMAC fixture. Refused by NewAuthenticatorFor when Production is set.
	ModeTest = "test"
	// ModeOIDC federates against a real OIDC admin IdP.
	ModeOIDC = "oidc"
	// ModeSAML federates against a real SAML 2.0 admin IdP.
	ModeSAML = "saml"
)

// WireConfig is what ProviderFromEnv resolved, plus everything the caller must wire alongside it.
type WireConfig struct {
	// Provider is the live IdP. Always non-nil on success.
	Provider IdentityProvider
	// OIDC is the same provider, typed, when the mode is `oidc` — the flow routes need
	// AuthorizationURL/Exchange, which are not on the IdentityProvider interface (and should not be:
	// the seam is about VERIFYING, and a SAML deployment has no authorization URL of that shape).
	OIDC *OIDCProvider
	// SAML is the same provider, typed, when the mode is `saml`.
	SAML *SAMLProvider
	// Mode is the resolved mode, for the readiness surface and the logs.
	Mode string
	// Production is whether this deployment refuses a test-mode issuer.
	Production bool
}

// ProviderFromEnv resolves the operator identity provider this deployment runs.
//
//	ADMIN_IDENTITY_MODE   test | oidc | saml            (required — no default; see the file comment)
//	ADMIN_IDP_ISSUER      the OIDC issuer / SAML entityID
//	ADMIN_IDP_CLIENT_ID   the operator console's OIDC client
//	ADMIN_IDP_REDIRECTS   JSON array of exact callback URIs (OIDC)
//	ADMIN_SAML_SP_ENTITY_ID / ADMIN_SAML_METADATA_URL / ADMIN_SAML_ACS   (SAML)
//	HEROS_ENV             `production` refuses a test-mode issuer
//
// The client SECRET is not here and never will be: it is resolved through `secrets` at the moment of
// the code exchange, under the reserved logical name `admin_oidc_client_secret`.
func ProviderFromEnv(secrets Secrets, issuerForTest string) (*WireConfig, error) {
	if secrets == nil {
		return nil, errors.New("adminidentity: a secrets source is required — operator identity keys are never read from code or config")
	}
	mode := strings.TrimSpace(strings.ToLower(os.Getenv("ADMIN_IDENTITY_MODE")))
	production := strings.EqualFold(strings.TrimSpace(os.Getenv("HEROS_ENV")), "production")

	switch mode {
	case ModeTest:
		if production {
			// Belt and braces with NewAuthenticatorFor: refusing here names the ENV VAR that is wrong,
			// which is what the person doing the deploy needs, rather than the provider that resulted.
			return nil, errors.New("adminidentity: ADMIN_IDENTITY_MODE=test is a fixture issuer and must not run with HEROS_ENV=production")
		}
		issuer := firstNonEmpty(os.Getenv("ADMIN_IDP_ISSUER"), issuerForTest)
		p, err := NewHMACProvider(HMACProviderConfig{Issuer: issuer, Secrets: secrets, TestMode: true})
		if err != nil {
			return nil, err
		}
		return &WireConfig{Provider: p, Mode: ModeTest, Production: production}, nil

	case ModeOIDC:
		issuer, err := requireEnv("ADMIN_IDP_ISSUER")
		if err != nil {
			return nil, err
		}
		clientID, err := requireEnv("ADMIN_IDP_CLIENT_ID")
		if err != nil {
			return nil, err
		}
		redirects, err := jsonList("ADMIN_IDP_REDIRECTS")
		if err != nil {
			return nil, err
		}
		p, err := NewOIDCProvider(OIDCProviderConfig{
			Issuer: issuer, ClientID: clientID, Secrets: secrets, Redirects: redirects,
		})
		if err != nil {
			return nil, err
		}
		return &WireConfig{Provider: p, OIDC: p, Mode: ModeOIDC, Production: production}, nil

	case ModeSAML:
		entityID, err := requireEnv("ADMIN_IDP_ISSUER")
		if err != nil {
			return nil, err
		}
		sp, err := requireEnv("ADMIN_SAML_SP_ENTITY_ID")
		if err != nil {
			return nil, err
		}
		metadata, err := requireEnv("ADMIN_SAML_METADATA_URL")
		if err != nil {
			return nil, err
		}
		acs, err := jsonList("ADMIN_SAML_ACS")
		if err != nil {
			return nil, err
		}
		p, err := NewSAMLProvider(SAMLProviderConfig{
			EntityID: entityID, SPEntityID: sp, MetadataURL: metadata, ACS: acs,
		})
		if err != nil {
			return nil, err
		}
		return &WireConfig{Provider: p, SAML: p, Mode: ModeSAML, Production: production}, nil

	case "":
		return nil, errors.New(
			"adminidentity: ADMIN_IDENTITY_MODE is unset and has no default — a deployment states whether its " +
				"operator sign-in is `test` (fixture issuer), `oidc`, or `saml`, because booting with a fixture " +
				"while an operator believes the real IdP is live is the failure this refusal exists to prevent")
	default:
		return nil, fmt.Errorf("adminidentity: ADMIN_IDENTITY_MODE=%q is not one of test, oidc, saml", mode)
	}
}

func requireEnv(name string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", fmt.Errorf("adminidentity: %s is required for this ADMIN_IDENTITY_MODE", name)
	}
	return v, nil
}

func jsonList(name string) ([]string, error) {
	raw, err := requireEnv(name)
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out) == 0 {
		return nil, fmt.Errorf("adminidentity: %s must be a non-empty JSON array of absolute URLs", name)
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
