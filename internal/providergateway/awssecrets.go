package providergateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// AWSSecretsManager sources provider credentials from AWS Secrets Manager (tasks 4.5, 6.1).
//
// # Why this manager
//
// The choice is arbitrated at level 1 (safety) of the cost/complexity ladder, where "which secrets
// manager" is mostly a tie — Vault, GCP SM and AWS SM all hold a secret properly. The tie breaks
// lower down, at level 8 (implementation), which is allowed to decide ONLY because the levels above
// it are level:
//
//   - aws-sdk-go-v2 is already a dependency: the Bedrock adapter signs SigV4 with it. Adding the
//     secretsmanager service client adds a client, not a new trust root, a new auth model, or a new
//     operational component. Vault would add all three — an agent to run, a token to renew, a seal
//     to unseal — which is level 4 (operations) cost, and level 4 outranks level 8.
//   - Its authentication is ambient (IRSA / instance role / SSO), so wiring it does not create a
//     bootstrap secret. That is the part that actually matters at level 1: a secrets manager reached
//     with a long-lived key in an env var has moved the secret, not removed it.
//
// This does NOT make AWS SM the only supported manager, and the seam is the proof: everything below
// implements the same Secrets interface EnvSecrets does. A Vault or GCP implementation is a new file,
// not a change here.
//
// # What is held, and for how long
//
// Credentials are fetched at call time and cached for TTL. The interface contract sanctions this
// ("implementations are free to cache, but the gateway holds nothing between calls") and it is not a
// shortcut: without it every model call becomes a GetSecretValue call, which is a per-call charge, a
// per-call latency add, and a throttling ceiling on P4's fan-out — the gateway's whole purpose is to
// be called a lot.
//
// The cache is memory only. It is never written to disk, a DB row, or a log, so it widens no
// persistence surface — TestPG_NoSecretReachesTheStores stays true by construction.
//
// The cost of TTL is rotation latency: a rotated secret keeps working for up to TTL. That is the
// documented tradeoff (see DefaultSecretTTL), and it is bounded and stated rather than discovered.
type AWSSecretsManager struct {
	client *secretsmanager.Client
	// ids maps a provider to its secret ID (name or full ARN). A provider with no entry has no
	// credential HERE — see Credential, which fails closed rather than reaching for a fallback.
	ids map[string]string
	// prefix is set ONLY when the deployment configured this source by naming convention
	// (HEROS_SECRETS_AWS_PREFIX) rather than by explicit IDs. It lets a logical name that cannot be
	// enumerated in advance still resolve — see Credential.
	prefix string
	ttl    time.Duration
	region string

	mu    sync.Mutex
	cache map[string]cachedCredential
	// now is injected so the TTL is testable without sleeping. A cache-expiry test that actually
	// waits five minutes is a test nobody runs.
	now func() time.Time
}

type cachedCredential struct {
	cred      Credential
	expiresAt time.Time
}

// DefaultSecretTTL bounds how long a fetched credential is reused.
//
// Five minutes is the rotation-latency budget: a secret rotated at the manager is honoured by this
// process within five minutes, worst case. Shorter multiplies API calls for no security gain that
// matters (an attacker with process memory has the credential regardless of TTL); longer makes an
// emergency rotation feel broken to the operator who just performed it.
const DefaultSecretTTL = 5 * time.Minute

// ErrSecretMalformed reports a secret whose payload is not the documented shape.
//
// A distinct sentinel because it is a DIFFERENT operator action from ErrNoCredential: the secret
// exists and was fetched and read, and the thing to fix is its contents, not its absence or the
// permissions on it. Collapsing the two would send whoever is paged to check IAM for a JSON typo.
var ErrSecretMalformed = errors.New("providergateway: secret payload is malformed")

// AWSOption configures an AWSSecretsManager.
type AWSOption func(*AWSSecretsManager)

// WithSecretTTL overrides how long a fetched credential is cached. Zero disables caching, making
// every call a fetch — correct, expensive, and occasionally exactly what an audit wants.
func WithSecretTTL(d time.Duration) AWSOption {
	return func(s *AWSSecretsManager) { s.ttl = d }
}

// withSecretClock replaces the clock. Unexported: a production caller controlling the expiry of its
// own credential cache is doing something that needs a different door.
func withSecretClock(now func() time.Time) AWSOption {
	return func(s *AWSSecretsManager) { s.now = now }
}

// WithSecretPrefix declares that this source was configured by NAMING CONVENTION, and that a logical
// name absent from the id map resolves to `prefix + name`.
//
// # Why an unenumerable name needs this
//
// `awsSecretIDs` builds the map from two finite lists — the gateway's adapters and
// `ReservedSecretNames()`. That covers every credential the platform knows about at compile time. It
// cannot cover a PER-PRINCIPAL secret: `adminidentity.TOTPSeedName` derives
// `admin_totp_seed/<admin_id>`, and the set of admin ids is not known until operators exist. Under the
// prefix form those names were unmapped, so every TOTP verification failed with "no secret ID is
// mapped" — the operator sees "that sign-in was not accepted" and there is nothing in the message
// connecting it to a secrets-source naming rule.
//
// # Why this does not weaken the fail-closed property
//
// It changes which ID is looked up, never whether a missing secret is tolerated. A name resolved
// through the prefix still has to exist in the manager and still has to parse; if it does not, the same
// fail-closed error is returned, now naming the ID it tried. And it applies ONLY to the prefix form:
// under the explicit-IDs form an unmapped name stays an error, because a deployment that enumerated its
// secrets meant the enumeration to be exhaustive.
func WithSecretPrefix(prefix string) AWSOption {
	return func(s *AWSSecretsManager) { s.prefix = strings.TrimSpace(prefix) }
}

// NewAWSSecretsManager builds a Secrets source over a real AWS Secrets Manager client.
//
// cfg is a normal aws.Config, so authentication is whatever the deployment already uses — IRSA on
// EKS, the task role on ECS, the instance role on EC2, SSO locally. That is deliberate: this
// constructor takes no credential of its own, so there is no bootstrap secret to leak. It is also
// what makes the httptest-backed tests honest — they override cfg.BaseEndpoint and nothing else, so
// the client under test is the real client, signing real requests.
//
// ids maps provider -> secret ID. It is required and must be non-empty: a secrets source configured
// with nothing to fetch is a misconfiguration that would otherwise surface as a confusing 401 from
// the provider, minutes later and one layer away.
func NewAWSSecretsManager(cfg aws.Config, ids map[string]string, opts ...AWSOption) (*AWSSecretsManager, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("providergateway: AWSSecretsManager requires at least one provider -> secret ID mapping")
	}
	own := make(map[string]string, len(ids))
	for k, v := range ids {
		if v == "" {
			return nil, fmt.Errorf("providergateway: provider %q is mapped to an empty secret ID", k)
		}
		own[k] = v
	}
	s := &AWSSecretsManager{
		client: secretsmanager.NewFromConfig(cfg),
		ids:    own,
		ttl:    DefaultSecretTTL,
		region: cfg.Region,
		cache:  map[string]cachedCredential{},
		now:    time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Describe reports this source, its region, and which providers it can serve.
//
// Providers and region, because those are the two things an operator debugging "why is anthropic
// 401ing" needs and neither is sensitive. Secret IDs are NOT included: an ARN names an account and a
// secret path, /readyz is unauthenticated, and a health endpoint should not be a reconnaissance
// endpoint.
func (s *AWSSecretsManager) Describe() SourceInfo {
	names := make([]string, 0, len(s.ids))
	for p := range s.ids {
		names = append(names, p)
	}
	slices.Sort(names) // stable output: /readyz is diffed and alerted on, so it must not shuffle
	// The naming convention is stated when one is in use, because it changes what "providers %v" means:
	// with a prefix that list is what was mapped ahead of time, not the exhaustive set this source can
	// serve. Reporting only the list would make /readyz claim a boundary the source does not have. The
	// prefix is configuration, not a secret — it is a path segment, and it names no account.
	detail := fmt.Sprintf("region %s; providers %v; ttl %s", s.region, names, s.ttl)
	if s.prefix != "" {
		detail += fmt.Sprintf("; naming convention %s<name>", s.prefix)
	}
	return SourceInfo{Kind: SourceKindAWSSecretsManager, Detail: detail}
}

// Credential fetches (or reuses) the provider's credential.
//
// It fails closed on every branch — an unmapped provider, a fetch error, an unreadable payload. There
// is deliberately no fallback to EnvSecrets: a "secrets manager with an env fallback" is an env
// source with extra steps, and its failure mode is the worst kind — the manager breaks, nobody
// notices because calls still succeed against a stale variable, and the deployment has silently been
// something other than what /readyz claims for weeks. If this source is configured, this source is
// the source.
func (s *AWSSecretsManager) Credential(ctx context.Context, provider string) (Credential, error) {
	id, ok := s.ids[provider]
	if !ok {
		// A name this source was not configured with, under the naming-convention form: resolve it by the
		// convention. See WithSecretPrefix for why an unenumerable per-principal name needs this and why
		// it does not weaken the fail-closed posture.
		if s.prefix == "" {
			return Credential{}, fmt.Errorf("%w: no secret ID is mapped for provider %q", ErrNoCredential, provider)
		}
		id = s.prefix + provider
	}

	if c, ok := s.cached(provider); ok {
		return c, nil
	}

	out, err := s.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(id)})
	if err != nil {
		// The secret ID is named (it is not sensitive — it is configuration, and the operator needs to
		// know WHICH secret could not be read). The AWS error is included because it carries the
		// actionable part: AccessDeniedException, ResourceNotFoundException, a throttle. It cannot
		// carry the secret VALUE: the value only ever exists in a successful response body.
		return Credential{}, fmt.Errorf("%w: fetching secret %q for provider %q: %v",
			ErrNoCredential, id, provider, err)
	}

	cred, err := parseSecretPayload(out, provider, id)
	if err != nil {
		return Credential{}, err
	}
	s.store(provider, cred)
	return cred, nil
}

func (s *AWSSecretsManager) cached(provider string) (Credential, bool) {
	if s.ttl <= 0 {
		return Credential{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[provider]
	if !ok || !s.now().Before(e.expiresAt) {
		return Credential{}, false
	}
	return e.cred, true
}

func (s *AWSSecretsManager) store(provider string, c Credential) {
	if s.ttl <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[provider] = cachedCredential{cred: c, expiresAt: s.now().Add(s.ttl)}
}

// secretPayload is the documented JSON shape of a provider secret.
//
// Two shapes in one struct because Credential has two shapes, for the reason recorded there: bearer
// providers (OpenAI, Anthropic) take a key, SigV4 providers (Bedrock) take an AWS credential. Serving
// only the bearer half would make this source a downgrade from EnvSecrets, which serves all three —
// and a "real" secrets manager that supports fewer providers than the env fallback is one an operator
// cannot actually adopt.
//
//	bearer: {"api_key": "sk-..."}
//	sigv4:  {"access_key_id": "...", "secret_access_key": "...", "session_token": "...", "region": "..."}
type secretPayload struct {
	APIKey          string `json:"api_key"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token"`
	Region          string `json:"region"`
}

// parseSecretPayload turns a GetSecretValue response into a Credential.
//
// # Why nothing here ever formats the payload into an error
//
// This function holds the plaintext secret, and it is the one place in the package where a careless
// %q of the payload would put a live credential into an error string that a caller logs. The rule is
// absolute and local: no error below interpolates out, *out.SecretString, or any field of p.
//
// It matters MORE here than anywhere else in the package, because the usual safety net is absent:
// the scrubber in Complete redacts the secrets of a credential that was successfully PARSED, and
// every path below is one where parsing failed — so there is no Credential, no secretValues(), and
// nothing for the scrubber to look for. An error escaping this function is unredacted by
// construction. That is why the guard is the discipline rather than the scrubber.
//
// To be precise about the threat, since precision is the difference between a guard and a ritual:
// encoding/json's own errors do NOT quote the offending input (verified — a truncated payload yields
// "unexpected end of JSON input", a bare token yields "invalid character 's'", neither carrying more
// than one character of it). So wrapping err would not leak today. The guard exists for the edit that
// has not happened yet — the plausible, helpful-looking `fmt.Errorf("bad payload %q", *out.SecretString)`
// added by someone debugging a malformed secret at 2am. TestAWSSecrets_AMalformedPayloadNeverAppearsInTheError
// is what stops that edit from shipping.
//
// # Why JSON only
//
// A raw non-JSON string is a common way to store a key, and it is deliberately rejected. Accepting
// both means guessing: malformed JSON would be indistinguishable from a raw key, so a typo'd payload
// would be silently handed to the provider as a credential, and the operator would get a 401 that
// says nothing about the actual mistake. One shape, named in the error, is the failure that is
// obvious (失败要显眼).
func parseSecretPayload(out *secretsmanager.GetSecretValueOutput, provider, id string) (Credential, error) {
	if out == nil || out.SecretString == nil {
		// Binary secrets are not supported rather than guessed at: SecretBinary has no agreed encoding,
		// so "decode it and hope" is how a credential becomes mojibake at the provider.
		return Credential{}, fmt.Errorf("%w: secret %q for provider %q has no SecretString "+
			"(binary secrets are not supported; store the credential as JSON)", ErrSecretMalformed, id, provider)
	}

	var p secretPayload
	if err := json.Unmarshal([]byte(*out.SecretString), &p); err != nil {
		// err is deliberately not wrapped. It is safe today (see the header note), but wrapping it
		// would make this line's safety depend on the internals of encoding/json's error strings —
		// a promise the standard library never made and could revise. Dropping it costs nothing: the
		// operator's next step is "look at the secret", and the position of a syntax error inside a
		// credential they can read is not what is stopping them.
		return Credential{}, fmt.Errorf("%w: secret %q for provider %q is not a JSON object "+
			`(expected {"api_key": "..."} or {"access_key_id": ..., "secret_access_key": ...})`,
			ErrSecretMalformed, id, provider)
	}

	switch {
	case p.APIKey != "":
		return Credential{APIKey: p.APIKey, Region: p.Region}, nil

	case p.AccessKeyID != "" || p.SecretAccessKey != "":
		if p.AccessKeyID == "" || p.SecretAccessKey == "" {
			return Credential{}, fmt.Errorf("%w: secret %q for provider %q has only half an AWS "+
				"credential (both access_key_id and secret_access_key are required)",
				ErrSecretMalformed, id, provider)
		}
		region := p.Region
		if region == "" {
			return Credential{}, fmt.Errorf("%w: secret %q for provider %q has no region "+
				"(SigV4 endpoints are per-region)", ErrSecretMalformed, id, provider)
		}
		return Credential{
			Region: region,
			AWS: &AWSCredential{
				AccessKeyID:     p.AccessKeyID,
				SecretAccessKey: p.SecretAccessKey,
				SessionToken:    p.SessionToken,
			},
		}, nil

	default:
		return Credential{}, fmt.Errorf("%w: secret %q for provider %q is a JSON object with no "+
			`recognised credential field (expected "api_key", or "access_key_id" + "secret_access_key")`,
			ErrSecretMalformed, id, provider)
	}
}
