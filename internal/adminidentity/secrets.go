package adminidentity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// secrets.go sources the admin IdP's assertion-signing secret, the MFA verification secret, and the
// admin session-signing key from a SECRETS MANAGER — never from code, a git-tracked config file, or
// telemetry (task 1.1, FR1; PRD §7).
//
// # Why this reuses providergateway.Secrets rather than adding a mechanism
//
// The platform has exactly one answer to "where do credentials come from at the moment of use", and
// P7's billing secrets already plug into it for the same reason. A third mechanism would split that
// truth again, and the failure is specific: a deployment whose LLM and billing credentials come from a
// manager while the key that signs OPERATOR SESSIONS quietly comes from an environment variable — with
// /readyz confidently reporting the manager. Admin secrets are therefore ordinary entries in the same
// source under reserved logical names, and they inherit the manager, the fail-closed factory, the
// health signal, and the telemetry scrubber by construction.
//
// # What is deliberately absent
//
// Nothing here returns a secret for logging, formatting, or inclusion in an event. `Describe` names
// the SOURCE, never a value and never a secret's id. The only way to obtain a secret is to ask for it
// at the moment of use.

// Reserved logical names under which the secrets manager holds the admin-identity credentials.
// Constants rather than literals at call sites for the same reason metric names are centralized: a
// name spelled two ways is a credential fetched from a place nobody provisioned (logging-conventions
// §3).
const (
	// SecretAdminSSOSigning verifies assertions from the admin identity provider.
	SecretAdminSSOSigning = "admin_sso_signing"
	// SecretAdminMFASigning verifies the MFA factor evidence attached to an assertion.
	SecretAdminMFASigning = "admin_mfa_signing"
	// SecretAdminSessionSigning signs admin session tokens.
	SecretAdminSessionSigning = "admin_session_signing"
	// SecretAdminPlatformCredential is the credential the admin BFF presents on every admin request, and
	// which the platform side compares against.
	//
	// It is here rather than read from an environment variable on the platform side for the reason this
	// file's header gives: a deployment whose LLM, billing and session-signing keys come from a manager
	// while the credential guarding the entire operator surface comes from an env var is a deployment
	// /readyz describes incorrectly. The CONSOLE still takes it from its environment — a Next.js process
	// has no secrets seam — which is why it is injected there from the same secret.
	SecretAdminPlatformCredential = "admin_platform_credential"
)

// TOTPSeedName is the reserved logical name a principal's TOTP seed is held under (P22 task 6.4).
//
// Derived rather than free-form so an enrollment cannot invent a name nobody provisioned — the same
// argument the three constants above make, extended to a per-principal secret. The seed lives in the
// manager under this name and NEVER in the enrollment directory: a directory record carrying a seed
// would be a credential store with an ordinary backup policy.
func TOTPSeedName(adminID string) string { return "admin_totp_seed/" + adminID }

// SecretNames is every logical name this package reads. Enumerated so a deployment checklist and the
// tests iterate the REAL set rather than a hand-copied list that stops covering a secret added later.
var SecretNames = []string{
	SecretAdminSSOSigning, SecretAdminMFASigning, SecretAdminSessionSigning, SecretAdminPlatformCredential,
	// P22 adds the real IdP's client secret. A per-principal TOTP seed is NOT here: it is provisioned
	// at enrollment rather than at deploy, so a deployment checklist that listed it would be asking an
	// operator to provision a secret for a principal that does not exist yet.
	SecretAdminOIDCClientSecret,
}

// ErrSecretUnavailable is returned when an admin secret cannot be sourced. It FAILS CLOSED: no
// signing key means no session is issued and no session is verified — never a fallback to an
// unsigned or unverified path.
var ErrSecretUnavailable = errors.New("adminidentity: credential unavailable from the secrets manager")

// EnvSecretName is the environment variable a logical admin-identity name is injected under.
//
// `admin_oidc_client_secret` -> `HEROS_ADMIN_OIDC_CLIENT_SECRET`. The same uppercase-the-logical-name
// convention the customer console uses, so an operator provisioning both surfaces learns one rule.
//
// This is the `env` source's contract and nothing more: a MANAGED deployment resolves the same logical
// name from AWS Secrets Manager (which takes arbitrary names), and an air-gapped one from a mounted
// file. The logical name is the constant across all three — the env var is one source's spelling of it.
func EnvSecretName(logical string) string {
	return "HEROS_" + strings.ToUpper(strings.ReplaceAll(logical, "/", "_"))
}

// Secrets supplies the admin identity credentials at the moment of use.
type Secrets interface {
	// SSOSigningKey returns the key admin IdP assertions are verified against.
	SSOSigningKey(ctx context.Context) ([]byte, error)
	// MFASigningKey returns the key MFA factor evidence is verified against.
	MFASigningKey(ctx context.Context) ([]byte, error)
	// SessionSigningKey returns the key admin session tokens are signed with.
	SessionSigningKey(ctx context.Context) ([]byte, error)
	// Named returns any admin-identity credential by its reserved logical name (P22).
	//
	// Added for the per-principal secrets P22 introduces — a TOTP seed, an OIDC client secret — which
	// cannot have a method each. The three methods above are kept rather than folded into this one on
	// purpose: they are the names a reader of the login path needs to see spelled out, and a call site
	// reading `secrets.Named(ctx, "admin_sso_signing")` is a call site where a typo is a runtime
	// failure instead of a compile error.
	Named(ctx context.Context, name string) ([]byte, error)
	// Describe names the SOURCE for the readiness surface — never a secret, never a secret's id.
	Describe() providergateway.SourceInfo
}

// ManagedSecrets adapts a providergateway.Secrets source to the three admin-identity credentials.
type ManagedSecrets struct{ src providergateway.Secrets }

// NewManagedSecrets wraps a secrets source. A nil source is an error rather than a silently
// credential-less identity layer: "the operator console came up with no secrets source" must be loud
// at construction, not discovered at the first login attempt.
func NewManagedSecrets(src providergateway.Secrets) (*ManagedSecrets, error) {
	if src == nil {
		return nil, errors.New("adminidentity: a secrets source is required — admin signing keys are never read from code or config")
	}
	return &ManagedSecrets{src: src}, nil
}

// SSOSigningKey returns the admin IdP assertion-verification key.
func (m *ManagedSecrets) SSOSigningKey(ctx context.Context) ([]byte, error) {
	return m.fetch(ctx, SecretAdminSSOSigning)
}

// MFASigningKey returns the MFA evidence-verification key.
func (m *ManagedSecrets) MFASigningKey(ctx context.Context) ([]byte, error) {
	return m.fetch(ctx, SecretAdminMFASigning)
}

// SessionSigningKey returns the admin session signing key.
func (m *ManagedSecrets) SessionSigningKey(ctx context.Context) ([]byte, error) {
	return m.fetch(ctx, SecretAdminSessionSigning)
}

// Named returns any admin-identity credential by its reserved logical name.
func (m *ManagedSecrets) Named(ctx context.Context, name string) ([]byte, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: an empty logical name resolves nothing", ErrSecretUnavailable)
	}
	return m.fetch(ctx, name)
}

func (m *ManagedSecrets) fetch(ctx context.Context, name string) ([]byte, error) {
	cred, err := m.src.Credential(ctx, name)
	if err != nil {
		// The error names WHICH credential was missing, never its value.
		return nil, fmt.Errorf("%w: %s: %v", ErrSecretUnavailable, name, err)
	}
	key := strings.TrimSpace(cred.APIKey)
	if key == "" {
		return nil, fmt.Errorf("%w: %s resolved empty", ErrSecretUnavailable, name)
	}
	return []byte(key), nil
}

// Describe names the live secrets source.
func (m *ManagedSecrets) Describe() providergateway.SourceInfo { return m.src.Describe() }
