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
)

// SecretNames is every logical name this package reads. Enumerated so a deployment checklist and the
// tests iterate the REAL set rather than a hand-copied list that stops covering a secret added later.
var SecretNames = []string{SecretAdminSSOSigning, SecretAdminMFASigning, SecretAdminSessionSigning}

// ErrSecretUnavailable is returned when an admin secret cannot be sourced. It FAILS CLOSED: no
// signing key means no session is issued and no session is verified — never a fallback to an
// unsigned or unverified path.
var ErrSecretUnavailable = errors.New("adminidentity: credential unavailable from the secrets manager")

// Secrets supplies the admin identity credentials at the moment of use.
type Secrets interface {
	// SSOSigningKey returns the key admin IdP assertions are verified against.
	SSOSigningKey(ctx context.Context) ([]byte, error)
	// MFASigningKey returns the key MFA factor evidence is verified against.
	MFASigningKey(ctx context.Context) ([]byte, error)
	// SessionSigningKey returns the key admin session tokens are signed with.
	SessionSigningKey(ctx context.Context) ([]byte, error)
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
