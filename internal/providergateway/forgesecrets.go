package providergateway

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// forgesecrets.go adds the **forge-read** credential kind to this package's Secrets source (P32 task
// 3.1). Until now the source was provider-scoped: `Credential(ctx, provider)` answers for OpenAI,
// Anthropic, Bedrock and the two billing names, and there was nowhere for a credential that
// authenticates to a code host rather than to a model vendor.
//
// # Why a second interface rather than another reserved provider name
//
// Reserving `forge_read:<connection>` in `Secrets.Credential` was the cheaper option and it was
// rejected at level 1 of the ladder, not at level 7. `Credential` RETURNS the secret. Every caller
// then holds a `Credential` value, and the whole of the control is that no caller ever puts it in a
// log line, an error, a struct that gets marshalled, or a `%+v`. ADR-005 already refused that shape
// for the write kind, in `forgedelivery.SecretsManager`:
//
//	"There is deliberately no GetToken(id) (string, error) — a method that HANDS OUT the token is one a
//	caller can forget to keep out of a log line, and the whole requirement is that it never leaves the
//	platform."
//
// So the read kind gets the same custody shape as the write kind: the token is handed to a closure and
// is out of scope when it returns. Doing it any other way would mean the read credential — the one
// used while nobody is watching — had weaker custody than the write one.
//
// # Why the lifecycle is on the interface and not a runbook
//
// PRD §7.1: *"Rotation and revocation are lifecycle operations with tests, not a runbook step."* A
// rotation that exists only as a wiki page is a rotation that happens once, badly, during an incident.
// `Store` and `Revoke` are on the interface so that every implementation has to answer for them, and
// so that revoking a connection can delete its credential in the same call path that deletes its
// grant — see `sourceingest.RevokeConnection`, where a credential left behind after a revocation is
// the exact failure D3 describes: invisible from the inside, indefensible from the outside.

// ErrNoForgeCredential reports that no credential is stored for this connection.
//
// Distinct from ErrNoCredential (the provider one) so a caller cannot accidentally treat "this tenant
// has not connected a repository" and "this deployment has no model key" as one condition. They have
// different owners and different remedies.
var ErrNoForgeCredential = errors.New("providergateway: no forge credential is stored for this connection")

// ForgeRef names one stored forge credential.
//
// Keyed by connection rather than by repository: two workflows may read the same repository under two
// grants, and revoking one must not delete the other's credential. The forge is carried so an
// implementation backed by a real secret manager can lay out paths per forge without parsing an
// opaque id.
type ForgeRef struct {
	// Forge is `github` | `gitlab` | `bitbucket`. A label, never a secret.
	Forge string
	// ConnectionID is the platform's own opaque connection identifier.
	ConnectionID string
}

// Validate rejects a partial ref. A ref with an empty ConnectionID would otherwise read as a
// legitimate lookup and, in a map-backed implementation, return whatever was stored under "".
func (r ForgeRef) Validate() error {
	if strings.TrimSpace(r.Forge) == "" {
		return fmt.Errorf("providergateway: forge ref has no forge")
	}
	if strings.TrimSpace(r.ConnectionID) == "" {
		return fmt.Errorf("providergateway: forge ref has no connection_id")
	}
	return nil
}

// String renders a ref for logs. 🔴 It renders the REF, which is two labels — there is no code path
// that renders the value, because there is no accessor that returns it.
func (r ForgeRef) String() string { return r.Forge + "/" + r.ConnectionID }

// ForgeSecrets is the forge-read credential kind: custody plus lifecycle.
//
// 🚫 There is deliberately no `Get`. See this file's header.
type ForgeSecrets interface {
	// UseForgeToken invokes fn with the connection's read token, then the token is out of scope. fn
	// must not persist it. Returns ErrNoForgeCredential when nothing is stored.
	UseForgeToken(ctx context.Context, ref ForgeRef, fn func(token string) error) error
	// Store records a credential, replacing any previous value for the same ref. ROTATION IS THIS
	// CALL — there is no separate `Rotate`, because a rotation that is a distinct method is a method
	// somebody forgets to implement, and a replace-in-place is the same operation.
	Store(ctx context.Context, ref ForgeRef, token string) error
	// Revoke removes the credential. Idempotent: revoking twice is not an error, because the caller's
	// intent ("this must not exist") is satisfied either way and a second revocation during an
	// incident must not fail.
	Revoke(ctx context.Context, ref ForgeRef) error
	// Describe names this source so /readyz can report WHICH forge-credential store is live, for the
	// reason Secrets.Describe is on the interface rather than an optional assertion.
	Describe() SourceInfo
}

// SourceKindMemoryForge is the in-process forge-credential store's name on /readyz.
//
// It is reported honestly, and an operator seeing it in production is seeing a real finding: an
// in-process store loses every connection's credential on restart, so every connected workflow would
// need re-authorizing. It is the correct default for a single-node or development deployment and the
// wrong one for a cluster, and the only way anybody learns which they have is by being told.
const SourceKindMemoryForge = "memory-forge"

// MemForgeSecrets is the in-process forge-credential store.
//
// It models CUSTODY, not durability: the property being demonstrated — that a token is reachable only
// inside a closure — is enforced by the interface shape and is identical in a Vault-backed
// implementation. A deployment that needs the credential to survive a restart supplies its own
// implementation of the same four methods.
type MemForgeSecrets struct {
	mu     sync.Mutex
	tokens map[ForgeRef]string
}

// NewMemForgeSecrets builds an empty store.
func NewMemForgeSecrets() *MemForgeSecrets {
	return &MemForgeSecrets{tokens: map[ForgeRef]string{}}
}

// Describe reports the in-process source, and says plainly what it costs.
func (m *MemForgeSecrets) Describe() SourceInfo {
	return SourceInfo{
		Kind:   SourceKindMemoryForge,
		Detail: "in-process forge credentials — lost on restart; connections must be re-authorized",
	}
}

// Store records or rotates a credential.
func (m *MemForgeSecrets) Store(_ context.Context, ref ForgeRef, token string) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if token == "" {
		// An empty token stored is a connection that will fail at first use with `credential
		// rejected`, which sends the customer to rotate a token that was never there. Refuse at the
		// write, where the remedy is still "finish the authorization".
		return fmt.Errorf("providergateway: refusing to store an empty forge credential for %s", ref)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[ref] = token
	return nil
}

// Revoke removes a credential. Idempotent.
func (m *MemForgeSecrets) Revoke(_ context.Context, ref ForgeRef) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tokens, ref)
	return nil
}

// UseForgeToken hands the token to fn and drops it.
func (m *MemForgeSecrets) UseForgeToken(_ context.Context, ref ForgeRef, fn func(token string) error) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("providergateway: UseForgeToken called with no closure")
	}
	m.mu.Lock()
	tok, ok := m.tokens[ref]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoForgeCredential, ref)
	}
	return fn(tok)
}

// Refs returns the stored refs, sorted. For tests and for the revocation fence, which has to assert
// that a credential is ABSENT after a revoke — an assertion that needs a way to enumerate without a
// way to read values.
func (m *MemForgeSecrets) Refs() []ForgeRef {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ForgeRef, 0, len(m.tokens))
	for r := range m.tokens {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
