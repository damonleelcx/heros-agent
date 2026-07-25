package forgedelivery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// hostedapp.go is the OPT-IN hosted Git App mode (12b). It is the mode that carries standing write
// access, and this file exists to CONTAIN that access rather than pretend it away:
//
//   - per-repository selection, NEVER org-wide by default (task 8.1);
//   - a least-privilege permission set, no broader than opening and updating pull requests on the
//     selected repositories — broadening it is a SPEC change, not a config choice (task 8.2);
//   - the installation token held in a secrets manager and never logged, embedded, or transmitted
//     outside the platform (task 8.3 / the credential-non-leak requirement);
//   - customer-revocable from their own side without contacting us, with the resulting state reported
//     (task 8.4).

// PermissionSet is a forge-App permission grant: a permission name → the level granted. The vocabulary
// is the forge's; what this type enforces is that a grant is comparable to the least-privilege set, so
// "no broader than delivery requires" is checkable rather than asserted.
type PermissionSet map[string]string

// permissionRank orders the levels so "no higher than" is comparable. Unknown ranks above everything,
// so an unrecognized level is treated as broader (fails closed) rather than silently accepted.
func permissionRank(level string) int {
	switch level {
	case "none", "":
		return 0
	case "read":
		return 1
	case "write":
		return 2
	default:
		return 99
	}
}

// LeastPrivilegePermissions is the DOCUMENTED, minimal set delivery needs and nothing more: write to
// pull requests, and write to contents (to push the platform's own head branch — never to a protected
// branch). This is a spec item; changing it is a spec change (task 8.2 / design Decision 8). The
// narrowest credible ask is what gets an installation approved.
func LeastPrivilegePermissions() PermissionSet {
	return PermissionSet{
		"pull_requests": "write",
		"contents":      "write",
	}
}

// WithinLeastPrivilege reports whether this grant is no broader than delivery requires: every permission
// it holds is in the least-privilege set at a level no higher than that set grants, and it holds no
// permission the set does not. A grant that asks for `administration` or `actions`, or for a level
// above what delivery needs, is refused here — the enforcement point for "broadening is a spec change".
func (p PermissionSet) WithinLeastPrivilege() error {
	allowed := LeastPrivilegePermissions()
	for name, level := range p {
		max, ok := allowed[name]
		if !ok {
			return fmt.Errorf("forgedelivery: permission %q is broader than delivery requires; broadening the App's permission set is a spec change, not a configuration choice", name)
		}
		if permissionRank(level) > permissionRank(max) {
			return fmt.Errorf("forgedelivery: permission %q=%q exceeds the least-privilege level %q", name, level, max)
		}
	}
	return nil
}

// Installation is one hosted Git App installation for a tenant. It is scoped to SELECTED repositories;
// there is deliberately no "all repositories" flag, so org-wide-by-default is not even expressible
// (task 8.1). Adding one would be a spec change, visible in review.
type Installation struct {
	InstallationID string
	TenantID       string
	// Repositories the customer selected, as "owner/repo". At least one is required — an installation
	// that selected nothing is a misconfiguration, not an org-wide grant.
	Repositories []string
	Permissions  PermissionSet
	Active       bool
	// RevokedReason records why an installation is inactive (revoked by the customer / removed).
	RevokedReason string
}

// Validate rejects an installation that would over-reach: no repositories selected (which must never be
// read as org-wide), or a permission set broader than least-privilege.
func (i Installation) Validate() error {
	if len(i.Repositories) == 0 {
		return errors.New("forgedelivery: an installation must select at least one repository (org-wide is never the default)")
	}
	if err := i.Permissions.WithinLeastPrivilege(); err != nil {
		return err
	}
	return nil
}

// Covers reports whether this installation is active and selected the given repository.
func (i Installation) Covers(repo string) bool {
	if !i.Active {
		return false
	}
	for _, r := range i.Repositories {
		if r == repo {
			return true
		}
	}
	return false
}

// ErrNoInstallation / ErrInstallationRevoked are the two capability-loss conditions the App mode
// surfaces. Both are reported states (task 8.4), never silent.
var (
	ErrNoInstallation      = errors.New("forgedelivery: no hosted App installation covers this repository")
	ErrInstallationRevoked = errors.New("forgedelivery: the hosted App installation was revoked")
)

// InstallationStore holds installations. It is the platform's record of what standing write access it
// has been granted, so every write is attributable and revocation is answerable.
type InstallationStore struct {
	mu    sync.Mutex
	byID  map[string]Installation
	byTen map[string][]string // tenant -> installation ids
}

// NewInstallationStore builds an empty store.
func NewInstallationStore() *InstallationStore {
	return &InstallationStore{byID: map[string]Installation{}, byTen: map[string][]string{}}
}

// Install records a validated, active installation (task 8.1).
func (s *InstallationStore) Install(i Installation) error {
	if err := i.Validate(); err != nil {
		return err
	}
	i.Active = true
	i.RevokedReason = ""
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byID[i.InstallationID]; !exists {
		s.byTen[i.TenantID] = append(s.byTen[i.TenantID], i.InstallationID)
	}
	s.byID[i.InstallationID] = i
	return nil
}

// Revoke marks an installation inactive. It models the effect of a customer revoking from THEIR side
// (the platform learns via webhook or a failed token exchange) — the platform can no longer deliver,
// and the state is reported (task 8.4). Revocation needs no action by the platform beyond recording it.
func (s *InstallationStore) Revoke(installationID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.byID[installationID]
	if !ok {
		return ErrNoInstallation
	}
	i.Active = false
	if reason == "" {
		reason = "revoked by the customer from their forge settings"
	}
	i.RevokedReason = reason
	s.byID[installationID] = i
	return nil
}

// ForRepo returns the active installation covering a (tenant, repo), or an error condition.
func (s *InstallationStore) ForRepo(tenantID, repo string) (Installation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := s.byTen[tenantID]
	var revoked bool
	for _, id := range ids {
		i := s.byID[id]
		for _, r := range i.Repositories {
			if r != repo {
				continue
			}
			if i.Active {
				return i, nil
			}
			revoked = true
		}
	}
	if revoked {
		return Installation{}, ErrInstallationRevoked
	}
	return Installation{}, ErrNoInstallation
}

// Capability implements the RouteRegistry capability probe for App-mode tenants: a revoked installation
// is a reported revoked state; no installation at all is not itself degraded (that is route-absence).
func (s *InstallationStore) Capability(_ context.Context, tenantID string) (RouteConditionKind, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := append([]string(nil), s.byTen[tenantID]...)
	sort.Strings(ids)
	for _, id := range ids {
		i := s.byID[id]
		if !i.Active {
			return RouteRevoked, "The hosted Git App installation was removed: " + i.RevokedReason, nil
		}
	}
	return "", "", nil
}

// SecretsManager holds installation tokens. The token is accessed ONLY through UseToken, a closure that
// scopes the token's lifetime and gives a caller no way to return, log, or store it (task 8.3). There is
// deliberately no `GetToken(id) (string, error)` — a method that HANDS OUT the token is one a caller can
// forget to keep out of a log line, and the whole requirement is that it never leaves the platform.
type SecretsManager interface {
	// UseToken invokes fn with the installation token, then the token is out of scope. fn must not
	// persist it. UseToken never itself logs the token.
	UseToken(ctx context.Context, installationID string, fn func(token string) error) error
}

// MemSecretsManager is the in-memory secrets manager for the demo/tests. In production this is backed by
// a real secrets manager; the interface is the same, and the property — the token never crosses out via
// a return value — is enforced by the interface shape, not by this implementation.
type MemSecretsManager struct {
	mu     sync.Mutex
	tokens map[string]string
}

// NewMemSecretsManager builds an empty secrets manager.
func NewMemSecretsManager() *MemSecretsManager { return &MemSecretsManager{tokens: map[string]string{}} }

// Put stores an installation's token. (In production the token is minted by the forge and refreshed;
// this models custody, not the mint.)
func (m *MemSecretsManager) Put(installationID, token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[installationID] = token
}

// UseToken implements SecretsManager.
func (m *MemSecretsManager) UseToken(_ context.Context, installationID string, fn func(token string) error) error {
	m.mu.Lock()
	tok, ok := m.tokens[installationID]
	m.mu.Unlock()
	if !ok {
		return ErrNoInstallation
	}
	return fn(tok)
}

// AppForgeWriter is the hosted-App ForgeWriter. It holds a platform-side credential (via the secrets
// manager) — HoldsForgeCredential reports true, honestly — and delegates the actual forge calls to an
// authenticated delegate built from the token inside UseToken. It refuses to write to a repository the
// installation does not cover or that has been revoked, so standing write access is bounded to exactly
// what was granted.
//
// The token is acquired inside UseToken for each operation and never returned, logged, or placed in any
// argument that outlives the closure. The delegate is the credential-USING client (a GitHub client in
// production; InMemForge in the demo/tests); the token authenticates it and is discarded.
type AppForgeWriter struct {
	store    *InstallationStore
	secrets  SecretsManager
	tenantID string
	// newDelegate builds an authenticated forge client from the token. In tests it returns a shared
	// InMemForge and ignores the token's value (but proves the token path was taken by being called
	// only inside UseToken).
	newDelegate func(token string) ForgeWriter
}

// NewAppForgeWriter builds an App-mode writer for a tenant.
func NewAppForgeWriter(store *InstallationStore, secrets SecretsManager, tenantID string, newDelegate func(token string) ForgeWriter) *AppForgeWriter {
	return &AppForgeWriter{store: store, secrets: secrets, tenantID: tenantID, newDelegate: newDelegate}
}

// Kind reports the forge (GitHub for the hosted App).
func (w *AppForgeWriter) Kind() ForgeKind { return ForgeGitHub }

// HoldsForgeCredential reports true — honestly. This mode IS standing write access.
func (w *AppForgeWriter) HoldsForgeCredential() bool { return true }

// withDelegate resolves the installation for a repo, acquires the token, and runs fn against an
// authenticated delegate. It centralizes the coverage/revocation check and the token custody.
func (w *AppForgeWriter) withDelegate(ctx context.Context, repo string, fn func(ForgeWriter) error) error {
	inst, err := w.store.ForRepo(w.tenantID, repo)
	if err != nil {
		return err
	}
	return w.secrets.UseToken(ctx, inst.InstallationID, func(token string) error {
		return fn(w.newDelegate(token))
	})
}

func (w *AppForgeWriter) EnsureBranch(ctx context.Context, t Target, head string) error {
	return w.withDelegate(ctx, t.Owner+"/"+t.Repo, func(d ForgeWriter) error {
		return d.EnsureBranch(ctx, t, head)
	})
}

func (w *AppForgeWriter) OpenOrUpdatePR(ctx context.Context, req OpenRequest) (PullRequest, bool, error) {
	var pr PullRequest
	var created bool
	err := w.withDelegate(ctx, req.Target.Owner+"/"+req.Target.Repo, func(d ForgeWriter) error {
		var e error
		pr, created, e = d.OpenOrUpdatePR(ctx, req)
		return e
	})
	return pr, created, err
}

func (w *AppForgeWriter) ClosePR(ctx context.Context, ref, reason string) error {
	repo := repoOfRef(ref)
	return w.withDelegate(ctx, repo, func(d ForgeWriter) error { return d.ClosePR(ctx, ref, reason) })
}

func (w *AppForgeWriter) MergePR(ctx context.Context, ref string) (string, error) {
	var mc string
	err := w.withDelegate(ctx, repoOfRef(ref), func(d ForgeWriter) error {
		var e error
		mc, e = d.MergePR(ctx, ref)
		return e
	})
	return mc, err
}

func (w *AppForgeWriter) OpenPRCount(ctx context.Context, t Target) (int, error) {
	var n int
	err := w.withDelegate(ctx, t.Owner+"/"+t.Repo, func(d ForgeWriter) error {
		var e error
		n, e = d.OpenPRCount(ctx, t)
		return e
	})
	return n, err
}

// repoOfRef extracts "owner/repo" from a forge ref like "owner/repo#42".
func repoOfRef(ref string) string {
	for i := 0; i < len(ref); i++ {
		if ref[i] == '#' {
			return ref[:i]
		}
	}
	return ref
}

// compile-time assertions.
var (
	_ ForgeWriter       = (*AppForgeWriter)(nil)
	_ CredentialCarrier = (*AppForgeWriter)(nil)
)
