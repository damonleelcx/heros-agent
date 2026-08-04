package providergateway

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// The environment that selects and configures the secrets source.
//
// Central enum rather than literals at the call sites (logging-conventions §3): these names are the
// deployment contract — they appear in Helm charts, task definitions and runbooks — and a contract
// spelled out at three call sites is a contract that drifts at two of them.
const (
	// EnvSecretsSource selects the source: "env" (default) or "aws-secrets-manager".
	EnvSecretsSource = "HEROS_SECRETS_SOURCE"
	// EnvSecretsAWSRegion overrides the region for the AWS source. Optional: the SDK's own chain
	// (AWS_REGION, the instance profile, the SSO profile) is used when unset.
	EnvSecretsAWSRegion = "HEROS_SECRETS_AWS_REGION"
	// EnvSecretsAWSPrefix maps providers to secret IDs by prefix: <prefix><provider>.
	EnvSecretsAWSPrefix = "HEROS_SECRETS_AWS_PREFIX"
	// EnvSecretsAWSIDs maps providers to secret IDs explicitly: "openai=id1,anthropic=id2". Wins over
	// the prefix — an explicit ARN is the escape hatch for secrets that do not share a naming scheme.
	EnvSecretsAWSIDs = "HEROS_SECRETS_AWS_IDS"
)

// NewSecretsFromEnv builds the configured Secrets source.
//
// # Why a factory, and why it is the only way to choose
//
// This is the single place the active choice is made, so "which secrets source is live" has one
// answer derived from one input. The alternative — each caller constructing its own — is how a
// deployment ends up with the gateway on AWS SM while /readyz reports env, and at that point the
// health signal is worse than none, because it is confidently wrong.
//
// # Why it fails closed rather than defaulting
//
// An unrecognised HEROS_SECRETS_SOURCE is an error, not a silent fall back to env. A typo
// ("aws-secretsmanager") that quietly degraded to reading env vars would be a deployment that
// believes it is on a secrets manager and is not — a level 1 (safety) regression bought with the
// convenience of not writing an error branch, which is exactly the trade the ladder forbids (禁止静默
// 回落). The same reason there is no fallback INSIDE AWSSecretsManager.
//
// The default when nothing is set is env, and that is a real default rather than a fallback: it is
// what a developer running the harness locally needs, and it is chosen by the ABSENCE of
// configuration, not by the failure of it.
func NewSecretsFromEnv(ctx context.Context) (Secrets, error) {
	switch src := strings.TrimSpace(os.Getenv(EnvSecretsSource)); src {
	case "", SourceKindEnv:
		return EnvSecrets{}, nil

	case SourceKindAWSSecretsManager:
		ids, err := awsSecretIDs()
		if err != nil {
			return nil, err
		}
		// Carried through so a logical name that CANNOT be enumerated at compile time — a per-principal
		// `admin_totp_seed/<admin_id>` — still resolves under the naming-convention form. Empty under the
		// explicit-IDs form, where an unmapped name stays an error on purpose.
		prefix := strings.TrimSpace(os.Getenv(EnvSecretsAWSPrefix))
		var opts []func(*awsconfig.LoadOptions) error
		if r := strings.TrimSpace(os.Getenv(EnvSecretsAWSRegion)); r != "" {
			opts = append(opts, awsconfig.WithRegion(r))
		}
		// LoadDefaultConfig resolves ambient credentials (IRSA, task role, instance profile, SSO). It
		// does NOT verify them — that costs a network call, and a health check that calls AWS on every
		// probe is a health check that pages you when AWS is slow. The first Credential call is where a
		// bad role surfaces, named and fail-closed.
		cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("providergateway: %s=%s but the AWS config could not be loaded: %w",
				EnvSecretsSource, src, err)
		}
		if cfg.Region == "" {
			return nil, fmt.Errorf("providergateway: %s=%s but no region is set; set %s or AWS_REGION "+
				"(secrets manager endpoints are per-region)", EnvSecretsSource, src, EnvSecretsAWSRegion)
		}
		return NewAWSSecretsManager(cfg, ids, WithSecretPrefix(prefix))

	default:
		return nil, fmt.Errorf("providergateway: %s=%q is not a known secrets source (want %q or %q)",
			EnvSecretsSource, src, SourceKindEnv, SourceKindAWSSecretsManager)
	}
}

// awsSecretIDs resolves provider -> secret ID from the explicit map or the prefix.
//
// Both are supported because they answer different questions and neither subsumes the other: a
// greenfield deployment names its secrets by convention (prefix), while one adopting this into an
// existing account has ARNs it does not control (explicit). Requiring the second everywhere would
// make the common case verbose; offering only the first would make the real case impossible.
func awsSecretIDs() (map[string]string, error) {
	if raw := strings.TrimSpace(os.Getenv(EnvSecretsAWSIDs)); raw != "" {
		ids := map[string]string{}
		for _, pair := range strings.Split(raw, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			provider, id, ok := strings.Cut(pair, "=")
			provider, id = strings.TrimSpace(provider), strings.TrimSpace(id)
			if !ok || provider == "" || id == "" {
				// Names the malformed PAIR, which is configuration, not a secret. A secret ID is not
				// sensitive; the value behind it is, and that value is not here.
				return nil, fmt.Errorf("providergateway: %s entry %q is not provider=secret_id",
					EnvSecretsAWSIDs, pair)
			}
			ids[provider] = id
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("providergateway: %s is set but empty", EnvSecretsAWSIDs)
		}
		return ids, nil
	}

	prefix := strings.TrimSpace(os.Getenv(EnvSecretsAWSPrefix))
	if prefix == "" {
		return nil, fmt.Errorf("providergateway: %s=%s requires either %s or %s to locate the secrets",
			EnvSecretsSource, SourceKindAWSSecretsManager, EnvSecretsAWSIDs, EnvSecretsAWSPrefix)
	}
	// The providers the gateway actually has adapters for. Derived from that list rather than a second
	// hardcoded one: a provider added to the gateway must not need a matching edit here to be
	// credentialable (禁止硬编码组件列表).
	ids := map[string]string{}
	for _, p := range SupportedProviders() {
		ids[p] = prefix + p
	}
	// 🔴 …AND every RESERVED logical name, which this branch used to omit.
	//
	// The explicit-IDs form above maps whatever it is given, so a billing credential worked there. This
	// one derived its whole key set from `SupportedProviders()` — the LLM adapters — so `billing_provider`
	// and `billing_webhook` were simply absent from the map, and Credential returned "no secret ID is
	// mapped for provider". That contradicted internal/billing/secrets.go, whose entire argument for
	// reusing this seam is that billing secrets are "ordinary entries in the same source, under reserved
	// logical names" — true of one of the two configuration forms.
	//
	// The failure is quiet in the worst way. Nothing refuses at boot: the source constructs, /readyz
	// reports it healthy and NAMES the providers it serves, and billing mounts. The credential is
	// resolved at the moment of use, so the deployment looks correct until the first checkout — or,
	// with the pricing preflight wired, until the boot log reports all references unresolved.
	for _, n := range ReservedSecretNames() {
		ids[n] = prefix + n
	}
	return ids, nil
}

// ReservedSecretNames are the logical names the PLATFORM resolves from this source that are not gateway
// providers. They live here rather than beside their consumers because this package owns the source's
// namespace: it is what decides which names a prefix expands to, and a name it does not know is a
// credential nobody can fetch.
//
// The values are duplicated as constants in internal/billing (which imports this package, so the
// dependency cannot run the other way). TestReservedSecretNamesCoverBilling in that package fails if
// the two ever drift — a name spelled two ways is a credential fetched from a place nobody provisioned.
func ReservedSecretNames() []string {
	return []string{
		"billing_provider", "billing_webhook",
		// The operator-identity credentials (P22). Same argument as billing, and the same bug: they are
		// documented in internal/adminidentity/secrets.go as "ordinary entries in the same source under
		// reserved logical names", which was true of the explicit-IDs form and false of the prefix form,
		// where they were simply absent from the map. Their per-principal sibling
		// `admin_totp_seed/<admin_id>` is deliberately NOT here — it cannot be enumerated before the
		// principals exist, and the prefix fallback in AWSSecretsManager.Credential is what serves it.
		"admin_sso_signing", "admin_mfa_signing", "admin_session_signing", "admin_oidc_client_secret",
		// The admin BFF's own credential, which the platform side must compare against.
		"admin_platform_credential",
	}
}

// SupportedProviders lists the providers the gateway has adapters for, sorted.
//
// Derived from allAdapters — the same list New dispatches on — rather than restating it. Exported
// because it is the honest answer to "what can this thing talk to", and because the secrets factory
// needs it rather than a copy of it.
func SupportedProviders() []string {
	out := make([]string, 0, 3)
	for _, a := range allAdapters() {
		out = append(out, a.name())
	}
	slices.Sort(out)
	return out
}
