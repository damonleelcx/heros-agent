package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// secrets.go sources the billing provider's API key and its webhook signing secret from a SECRETS
// MANAGER — never from code, git-tracked config, or telemetry (task 3.3 / design Decision 10).
//
// ## Why this reuses providergateway.Secrets instead of introducing a billing secrets mechanism
//
// The platform already has exactly one answer to "where do credentials come from at call time": the
// `providergateway.Secrets` interface, with an AWS Secrets Manager implementation, a fail-closed
// factory, and a `Describe()` that makes the live source externally readable on /readyz. A second
// mechanism for billing would split that truth in two, and the failure mode is specific and ugly — a
// deployment whose LLM credentials come from a manager while its BILLING credentials quietly come from
// an environment variable, with a health endpoint that is confidently wrong about both.
//
// So billing secrets are ordinary entries in the same source, under reserved logical names. Billing
// gains the manager, the caching, the fail-closed behaviour and the health signal for free, and the
// scrubber that already keeps provider keys out of telemetry covers these by construction.
//
// ## What is deliberately absent
//
// There is no field, method, or struct here that returns a secret for logging, formatting, or
// inclusion in an event. `Describe` returns the SOURCE, never the value. The only way to obtain a
// secret is to ask for it at the moment of use.

// Reserved logical names under which the secrets manager holds the billing credentials. They are
// constants, not literals at call sites, for the same reason metric names are: a name spelled two ways
// is a credential fetched from a place nobody provisioned.
const (
	// SecretBillingAPIKey is the billing provider's server-side API key.
	SecretBillingAPIKey = "billing_provider"
	// SecretBillingWebhookSigning is the webhook signing secret used to verify inbound payloads.
	SecretBillingWebhookSigning = "billing_webhook"
)

// ErrSecretUnavailable is returned when a billing secret cannot be sourced. It FAILS CLOSED: no key
// means no provider call and no webhook verification, never a fallback to an unauthenticated path.
var ErrSecretUnavailable = errors.New("billing: credential unavailable from the secrets manager")

// Secrets supplies the billing provider's credentials at the moment of use.
type Secrets interface {
	// APIKey returns the provider API key.
	APIKey(ctx context.Context) (string, error)
	// WebhookSigningSecret returns the secret inbound webhook signatures are verified against.
	WebhookSigningSecret(ctx context.Context) (string, error)
	// Describe names the SOURCE for the readiness surface — never a secret, never a secret's id.
	Describe() providergateway.SourceInfo
}

// ManagedSecrets adapts a providergateway.Secrets source to the billing capability's two credentials.
type ManagedSecrets struct{ src providergateway.Secrets }

// NewManagedSecrets wraps a secrets source. A nil source is an error rather than a silently
// credential-less service: "the billing integration came up with no secrets source" must be loud at
// construction, not discovered at the first charge.
func NewManagedSecrets(src providergateway.Secrets) (*ManagedSecrets, error) {
	if src == nil {
		return nil, errors.New("billing: a secrets source is required — billing credentials are never read from code or config")
	}
	return &ManagedSecrets{src: src}, nil
}

// APIKey returns the billing provider's API key.
func (m *ManagedSecrets) APIKey(ctx context.Context) (string, error) {
	return m.fetch(ctx, SecretBillingAPIKey)
}

// WebhookSigningSecret returns the webhook signing secret.
func (m *ManagedSecrets) WebhookSigningSecret(ctx context.Context) (string, error) {
	return m.fetch(ctx, SecretBillingWebhookSigning)
}

func (m *ManagedSecrets) fetch(ctx context.Context, name string) (string, error) {
	cred, err := m.src.Credential(ctx, name)
	if err != nil {
		// The error names WHICH credential was missing, never its value or its id.
		return "", fmt.Errorf("%w: %s: %v", ErrSecretUnavailable, name, err)
	}
	key := strings.TrimSpace(cred.APIKey)
	if key == "" {
		return "", fmt.Errorf("%w: %s resolved empty", ErrSecretUnavailable, name)
	}
	return key, nil
}

// Describe names the live secrets source.
func (m *ManagedSecrets) Describe() providergateway.SourceInfo { return m.src.Describe() }
