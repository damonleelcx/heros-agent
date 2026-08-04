package providergateway

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Secrets supplies provider credentials at call time (task 4.5).
//
// This is an interface, not a config struct, for one reason: a credential must never be something a
// Variant Spec, a ModelEntry, a config_hash, a generated diff, or a run record can carry. PRD §7 is
// unambiguous — "Provider secrets never appear in the Variant Spec, generated diffs, DB rows, logs,
// error messages, or run records." A registry entry says WHICH provider; this says how to
// authenticate to it, resolved at the moment of the call and never persisted alongside the config.
//
// It is also why the entry has no base URL or key field: if a credential were part of a model entry,
// it would be part of that entry's content hash, and the secret would be baked into a version_id
// that lives forever.
type Secrets interface {
	// Credential returns the credential for a provider. Called on every Complete — implementations
	// are free to cache, but the gateway holds nothing between calls.
	Credential(ctx context.Context, provider string) (Credential, error)

	// Describe names this source so an operator can tell WHICH one is live.
	//
	// On the interface, not an optional type-assertion, on purpose. "Which secrets source is
	// actually running" is a health signal, and health-signal-surface requires it be externally
	// readable rather than inferred. An optional describer would let a future implementation be
	// silently anonymous — and the failure would be invisible, because an un-describable source
	// still serves credentials perfectly well. The compiler asking the question every time is the
	// only version of this that cannot rot.
	Describe() SourceInfo
}

// SourceInfo identifies a live secrets source for /readyz.
//
// It carries no credential and no secret ID — a health endpoint is unauthenticated and its output
// lands in monitoring systems, so it says which DOOR is in use, never what is behind it. Detail is
// for non-sensitive disambiguation only (the region, the variable names), which is what an operator
// staring at a 401 actually needs.
type SourceInfo struct {
	// Kind is the stable machine-readable source name (env, aws-secrets-manager, static).
	Kind string `json:"kind"`
	// Detail is a short non-sensitive human hint. Never a secret, never a secret's value.
	Detail string `json:"detail,omitempty"`
}

// Source kinds. Central enum: a health signal a monitor matches on must not be a literal typed at
// three call sites (logging-conventions §3).
const (
	SourceKindEnv               = "env"
	SourceKindAWSSecretsManager = "aws-secrets-manager"
	SourceKindStatic            = "static"
)

// Credential is one provider's authentication material.
//
// Two shapes because the providers genuinely differ: OpenAI and Anthropic take a bearer-style key in
// a header, Bedrock takes AWS credentials and signs the request. Flattening both into one "api key"
// string would force the Bedrock adapter to re-parse a packed string — a place for a secret to get
// split, logged, or mangled.
type Credential struct {
	// APIKey authenticates bearer-style providers (OpenAI, Anthropic).
	APIKey string
	// AWS authenticates SigV4 providers (Bedrock).
	AWS *AWSCredential
	// Region is required by Bedrock; ignored elsewhere.
	Region string
}

// AWSCredential is an AWS access key pair (plus an optional session token for temporary creds).
type AWSCredential struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// secretValues returns every value in this credential that must never appear in output. The scrubber
// takes this list, so adding a credential field means adding it here — and the compiler will not
// remind you, which is why scrubbing is also asserted end-to-end in the tests rather than trusted.
//
// AccessKeyID is deliberately NOT included: it is an identifier, not a secret (AWS prints it in
// console UIs), and redacting it would make a misconfiguration error unreadable — "credential
// [REDACTED] is not authorized" tells an operator nothing.
func (c Credential) secretValues() []string {
	var out []string
	if c.APIKey != "" {
		out = append(out, c.APIKey)
	}
	if c.AWS != nil {
		if c.AWS.SecretAccessKey != "" {
			out = append(out, c.AWS.SecretAccessKey)
		}
		if c.AWS.SessionToken != "" {
			out = append(out, c.AWS.SessionToken)
		}
	}
	return out
}

// EnvSecrets reads credentials from the process environment — the shape a secrets manager injects
// them in (Kubernetes secrets, ECS task secrets, Vault agent). It is not a secrets manager itself and
// is not trying to be: it is the boundary a real one plugs into, so DevOps can swap the source
// without the gateway changing.
//
// It remains the right choice for local development, and is deliberately NOT deprecated by
// AWSSecretsManager: a developer running the harness on a laptop should not need an AWS account to
// make one model call. What matters is that an operator can tell which of the two is live — hence
// Describe, and /readyz reporting it.
type EnvSecrets struct{}

// Describe reports the env source. The detail names the VARIABLES, never their values — the whole
// point of the env path is that the value is somewhere this process does not print.
func (EnvSecrets) Describe() SourceInfo {
	return SourceInfo{
		Kind:   SourceKindEnv,
		Detail: "process environment (OPENAI_API_KEY / ANTHROPIC_API_KEY / AWS_*)",
	}
}

// The billing capability's reserved logical names, duplicated here rather than imported.
//
// `internal/billing` imports THIS package, so importing it back would be a cycle. billing owns the
// names (billing.SecretBillingAPIKey / SecretBillingWebhookSigning) and a fence over there asserts
// these two match — a name spelled two ways is a credential fetched from a place nobody provisioned.
const (
	ProviderBillingAPIKey  = "billing_provider"
	ProviderBillingWebhook = "billing_webhook"
)

func (EnvSecrets) Credential(_ context.Context, provider string) (Credential, error) {
	switch provider {
	case ProviderOpenAI:
		return envKey("OPENAI_API_KEY", provider)
	case ProviderAnthropic:
		return envKey("ANTHROPIC_API_KEY", provider)
	// ── The billing capability's two credentials ────────────────────────────────────────────────
	//
	// 🔴 THESE CASES ARE WHAT MAKE A DECLARED BILLING MODE WORK ON COMPOSE. billing.ManagedSecrets
	// asks this source for `billing_provider` and `billing_webhook`; without a case they fell to the
	// `default` below and returned ErrNoCredential — so a deployment that set BILLING_MODE mounted
	// checkout and then failed on the FIRST customer's first click, with "credential unavailable".
	// The aws-secrets-manager source resolves them by logical name and never had this gap; the env
	// source, which is the only one an open-core install has, did.
	//
	// They are ordinary entries under reserved names, exactly as billing/secrets.go intends — billing
	// gains this source's caching, fail-closed behaviour and /readyz signal rather than inventing a
	// second mechanism whose health endpoint would be confidently wrong about one of the two.
	case ProviderBillingAPIKey:
		return envKey("BILLING_PROVIDER_API_KEY", provider)
	case ProviderBillingWebhook:
		return envKey("BILLING_WEBHOOK_SECRET", provider)
	case ProviderBedrock:
		id, secret := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY")
		region := os.Getenv("AWS_REGION")
		if id == "" || secret == "" {
			return Credential{}, fmt.Errorf("%w: AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY are not set", ErrNoCredential)
		}
		if region == "" {
			return Credential{}, fmt.Errorf("%w: AWS_REGION is not set (bedrock endpoints are per-region)", ErrNoCredential)
		}
		return Credential{Region: region, AWS: &AWSCredential{
			AccessKeyID: id, SecretAccessKey: secret, SessionToken: os.Getenv("AWS_SESSION_TOKEN"),
		}}, nil
	default:
		return Credential{}, fmt.Errorf("%w: no credential source for provider %q", ErrNoCredential, provider)
	}
}

func envKey(env, provider string) (Credential, error) {
	v := os.Getenv(env)
	if v == "" {
		// Names the variable, never a value — an error about a missing secret must not become an
		// error containing one when the variable is merely wrong rather than absent.
		return Credential{}, fmt.Errorf("%w: %s is not set (provider %q)", ErrNoCredential, env, provider)
	}
	return Credential{APIKey: v}, nil
}

// StaticSecrets is a fixed credential map, for tests and single-tenant deployments.
type StaticSecrets map[string]Credential

// Describe reports the static source, and says so plainly: a deployment whose /readyz reports
// "static" is holding credentials in memory from its own configuration, which is a thing an operator
// should be able to discover without reading the source.
func (s StaticSecrets) Describe() SourceInfo {
	return SourceInfo{Kind: SourceKindStatic, Detail: "in-process credential map"}
}

func (s StaticSecrets) Credential(_ context.Context, provider string) (Credential, error) {
	c, ok := s[provider]
	if !ok {
		return Credential{}, fmt.Errorf("%w: no credential configured for provider %q", ErrNoCredential, provider)
	}
	return c, nil
}

// scrub replaces every secret value in s with a redaction marker.
//
// This is the LAST line, not the first. The first line is not putting secrets in strings: the gateway
// never formats a request body or an Authorization header into an error. But an error that reaches a
// log has usually passed through code we do not own — net/http embeds the request URL in its errors,
// and a provider that authenticates by query parameter would leak the key through a transport error
// nobody wrote. Redacting on the way out costs one pass over a short string and closes that whole
// class, including the ones added later by someone who did not read this comment.
//
// Short values are not scrubbed: redacting a 3-character "secret" would redact fragments of ordinary
// text everywhere and make errors useless. A credential that short is not a credential.
const redacted = "[REDACTED]"

const minScrubbableSecret = 8

func scrub(s string, secrets []string) string {
	for _, sec := range secrets {
		if len(sec) < minScrubbableSecret {
			continue
		}
		s = strings.ReplaceAll(s, sec, redacted)
	}
	return s
}

// scrubErr rewrites an error's message with every secret redacted.
//
// It deliberately returns a NEW error rather than wrapping, breaking the chain to the original. That
// is the point: a wrapped error keeps the unredacted message reachable through Unwrap, and something
// downstream — a log formatter, an error reporter, a run record — will eventually print it. The
// sentinel is re-attached explicitly so errors.Is still works on what callers actually branch on.
func scrubErr(err error, secrets []string, sentinel error) error {
	if err == nil {
		return nil
	}
	msg := scrub(err.Error(), secrets)
	if sentinel != nil {
		return fmt.Errorf("%w: %s", sentinel, msg)
	}
	return fmt.Errorf("%s", msg)
}
