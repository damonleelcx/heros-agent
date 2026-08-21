package sourceingest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// connection.go is the repository CONNECTION: a customer authorization for the platform to read one
// repository, read-only, revocable, and recorded per use.
//
// # What makes this different from every credential the platform already holds
//
// Every previous grant expires with an act. A bundle covers one revision. A CI token lives for one job.
// A read grant covers *every revision, at any time, without the customer present* — it is the first
// credential in this system whose whole purpose is to be used when nobody is watching
// ([ADR-013](../../docs/adr/ADR-013-source-acquisition-posture.md)).
//
// That is why three properties are structural here rather than documented:
//
//  1. **One repository.** `Connection` has no field that can express breadth, and `Validate` refuses an
//     authorization whose resulting grant covers a repository the customer did not name (§14 A5).
//  2. **Read-only.** There is no scope field at all. A grant that could express write would eventually
//     hold one, and ADR-005 deliberately keeps a write installation SEPARATE — one credential that both
//     reads source and writes branches is the thing neither ADR wants.
//  3. **Recorded per use.** `CloneRecord` is append-only and customer-readable, and its `Actor`
//     distinguishes a person-initiated read from a scheduled or autonomous one. "Usable without the
//     customer present" is acceptable only if the customer can afterwards read exactly when it was used
//     and for what.
//
// # 🚫 What is deliberately absent
//
// No `scope`, no `permissions`, no `org`, no `all_repositories`, no free-text field an operator could
// paste a token into. The token lives in the deployment's secret store, reached through
// `providergateway.ForgeSecrets`, whose custody shape hands it to a closure and never returns it.
// `TestConnectionHasNoFieldThatCanExpressWriteOrBreadth` reads this struct by reflection, so a field
// added later is caught by a fence rather than by review.

// Forge is the closed set of hosts a connection can read from.
//
// Closed because each one expresses "the narrowest grant" differently (§14 A2) and an open string
// would let a fourth forge arrive with no adapter and no breadth check — which is the same as arriving
// with no breadth check at all.
type Forge string

const (
	// ForgeGitHub — grant kind is an App installation with exactly one selected repository.
	ForgeGitHub Forge = "github"
	// ForgeGitLab — grant kind is a project access token with `read_repository`.
	ForgeGitLab Forge = "gitlab"
	// ForgeBitbucket — grant kind is a repository access token with `repository:read`.
	ForgeBitbucket Forge = "bitbucket"
)

// Forges returns every supported forge, sorted. A copy, so no caller can widen the set.
func Forges() []Forge {
	out := []Forge{ForgeBitbucket, ForgeGitHub, ForgeGitLab}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Valid reports membership.
func (f Forge) Valid() bool {
	for _, v := range Forges() {
		if v == f {
			return true
		}
	}
	return false
}

// String makes Forge printable without a conversion at every call site.
func (f Forge) String() string { return string(f) }

// GrantKind is how the forge issued the read grant (§14 A2).
//
// It is recorded per connection rather than derived from the forge, because the answer is per forge
// TODAY and the reason is the forge's product, not ours — GitHub may ship a project-scoped token and
// GitLab may ship an App. A derived value would encode today's answer in a `switch` in three places.
type GrantKind string

const (
	// GrantAppInstallation — a forge App installed against exactly one repository. Revocable on both
	// sides, auditable on theirs, and never a value the customer types.
	GrantAppInstallation GrantKind = "app_installation"
	// GrantAccessToken — a repository- or project-scoped access token the customer issued. Used only
	// where the forge issues no App-shaped equivalent at repository scope.
	GrantAccessToken GrantKind = "access_token"
)

// Valid reports membership.
func (g GrantKind) Valid() bool {
	return g == GrantAppInstallation || g == GrantAccessToken
}

// String makes GrantKind printable.
func (g GrantKind) String() string { return string(g) }

// Actor classifies WHO caused a read (FR9).
//
// Two members, and the split is the one the customer asked about: was somebody there. A finer
// taxonomy (which scheduler, which agent) belongs in `ActorID`, where it is an attribute rather than a
// vocabulary every consumer must switch on.
type Actor string

const (
	// ActorPerson — a signed-in person asked for this read, and was present when it happened.
	ActorPerson Actor = "person"
	// ActorScheduled — a scheduled or autonomous process asked for it, with nobody present. This is
	// the one the whole disclosure exists for.
	ActorScheduled Actor = "scheduled"
)

// Valid reports membership.
func (a Actor) Valid() bool { return a == ActorPerson || a == ActorScheduled }

// String makes Actor printable.
func (a Actor) String() string { return string(a) }

// Connection is one customer authorization to read one repository.
//
// 🚫 Every field here is either an identifier or a classification. None of them can express a scope,
// a permission, a breadth, or a secret — see this file's header for why that is the mechanism rather
// than the policy.
type Connection struct {
	// ConnectionID is the platform's own opaque identifier. Used as the secret-store reference and as
	// the cascade key, so revoking is one row and one predicate.
	ConnectionID string `json:"connection_id"`
	// TenantID scopes the connection. It comes from the authenticated principal, never from a request
	// field (§7.1).
	TenantID string `json:"tenant_id"`
	// WorkflowID is the workflow this connection supplies source for. One repository per connection,
	// one connection per workflow.
	WorkflowID string `json:"workflow_id"`
	// Forge is which host.
	Forge Forge `json:"forge"`
	// Repository is `owner/name` — exactly one, and exactly the one the customer named.
	Repository string `json:"repository"`
	// SubPath is the directory within the repository a snapshot is rooted at, or "" for the whole
	// repository (§14 A3). The GRANT stays repository-scoped because no forge issues a narrower one;
	// this bounds what is actually READ, and the clone record says which revision was taken.
	SubPath string `json:"sub_path,omitempty"`
	// GrantKind is how the forge issued the grant.
	GrantKind GrantKind `json:"grant_kind"`
	// ExternalID is the forge's own identifier for the grant — an installation id, a token id — so a
	// customer can find it on their side and revoke it there too. Not a secret: it names the grant,
	// it does not authenticate it.
	ExternalID string `json:"external_id,omitempty"`
	// CreatedBy is the person who authorized it, and CreatedAtMS when. Milliseconds since the epoch,
	// as an int64 — never a driver-rendered timestamp, which is a second clock.
	CreatedBy   string `json:"created_by"`
	CreatedAtMS int64  `json:"created_at_ms"`
}

// connectionFieldAllowlist is the complete set of field names `Connection` may have.
//
// 🔴 Two fences, not one. A denylist of shapes (`scope`, `write`, `org`, …) catches the field somebody
// names honestly; the exact-set check catches the one they do not — `flags`, `extra`, `metadata`. A
// whitelist alone cannot protect an invariant, and a denylist alone cannot see a euphemism, so the
// fence asserts both and adding a field is a deliberate edit here.
var connectionFieldAllowlist = []string{
	"ConnectionID", "TenantID", "WorkflowID", "Forge", "Repository",
	"SubPath", "GrantKind", "ExternalID", "CreatedBy", "CreatedAtMS",
}

// ConnectionFieldAllowlist returns a copy for the fence.
func ConnectionFieldAllowlist() []string {
	return append([]string(nil), connectionFieldAllowlist...)
}

// Refusals. Typed so a caller branches on the CLASS rather than on a message, and so the console can
// render each as its own sentence.
var (
	// ErrGrantTooBroad: the authorization would cover a repository the customer did not name. Refused
	// at connect, on every forge (ADR-013 Option B).
	ErrGrantTooBroad = errors.New("sourceingest: the authorization covers repositories that were not named")
	// ErrNoConnection: this tenant has no connection for this workflow. A STATE, like ErrNoSource.
	ErrNoConnection = errors.New("sourceingest: no repository connection for this workflow")
	// ErrConnectionExists: a connection already exists for this workflow. One repository per
	// connection, one connection per workflow — a second one is a replacement the customer has to ask
	// for, not something that happens because they clicked twice.
	ErrConnectionExists = errors.New("sourceingest: this workflow already has a repository connection")
)

// Authorization is what a forge's authorization step produced, before anything is stored.
//
// 🔴 `Covers` is what the FORGE says the grant reaches, not what the customer asked for. The whole
// breadth check is the comparison between the two, and a struct that carried only the request would
// make that comparison unwritable — which is exactly how an org-wide installation gets recorded as a
// repository-scoped one.
type Authorization struct {
	Forge      Forge
	GrantKind  GrantKind
	ExternalID string
	// Covers is every repository the resulting grant can read, as reported by the forge.
	Covers []string
	// AccountWide reports a grant the forge scoped to an account, workspace, group or organization
	// rather than to a repository list. Separate from `Covers` because an account-wide grant's reach
	// is NOT enumerable — it includes repositories that do not exist yet — so an empty `Covers` on an
	// account-wide grant must not read as "covers nothing".
	AccountWide bool
	// Token is the credential the forge issued. It is handed to the secret store and then dropped; it
	// is never stored on a Connection, never logged, and never returned by any read path.
	Token string
	// Scopes is what the forge says the grant permits, for the read-only assertion. Refused if it
	// contains anything that can write.
	Scopes []string
}

// writeCapableScopeSubstrings are the scope fragments that mean "can change something".
//
// Substrings rather than exact names because the three forges spell the same power differently
// (`contents:write`, `write_repository`, `repository:write`, `repo`), and a table of exact names is a
// table that is missing the one the forge shipped last month. Matching on the VERB fails toward
// refusal, which is the correct direction for a control whose failure mode is a write grant admitted
// as a read one.
var writeCapableScopeSubstrings = []string{
	"write", "push", "admin", "delete", "maintain", "manage", "create",
}

// Validate refuses an authorization that is broader than the one repository the customer named, or
// that carries a scope which can write (FR6, FR7).
//
// Called at connect, before the token reaches the secret store — so a refused authorization leaves
// nothing behind to clean up.
func (a Authorization) Validate(repository string) error {
	if !a.Forge.Valid() {
		return fmt.Errorf("sourceingest: %q is not a supported forge", a.Forge)
	}
	if !a.GrantKind.Valid() {
		return fmt.Errorf("sourceingest: %q is not a grant kind", a.GrantKind)
	}
	if strings.TrimSpace(repository) == "" {
		return fmt.Errorf("sourceingest: no repository was named")
	}
	if a.Token == "" {
		return fmt.Errorf("sourceingest: the forge returned no credential for %s", repository)
	}
	for _, s := range a.Scopes {
		low := strings.ToLower(s)
		for _, bad := range writeCapableScopeSubstrings {
			if strings.Contains(low, bad) {
				return fmt.Errorf("sourceingest: refusing scope %q — a read connection may not carry a scope that can %s", s, bad)
			}
		}
	}
	if a.AccountWide {
		return fmt.Errorf("%w: the %s grant is account-wide, which reaches repositories that do not exist yet", ErrGrantTooBroad, a.Forge)
	}
	switch len(a.Covers) {
	case 0:
		return fmt.Errorf("%w: the %s grant reports covering no repository at all, so what it reaches cannot be checked", ErrGrantTooBroad, a.Forge)
	case 1:
		if !strings.EqualFold(a.Covers[0], repository) {
			return fmt.Errorf("%w: the %s grant covers %q, and the repository named was %q",
				ErrGrantTooBroad, a.Forge, a.Covers[0], repository)
		}
		return nil
	default:
		return fmt.Errorf("%w: the %s grant covers %d repositories (%s) and exactly one was named (%q)",
			ErrGrantTooBroad, a.Forge, len(a.Covers), strings.Join(a.Covers, ", "), repository)
	}
}

// CloneRecord is one read, appended. Never updated, never deleted while the connection lives.
//
// 🔴 It is the whole justification for admitting a standing capability. ADR-013: *"Usable without the
// customer present is acceptable only if the customer can afterwards read exactly when it was used and
// for what."* A record the customer cannot read would satisfy an auditor and nobody else.
type CloneRecord struct {
	RecordID     string `json:"record_id"`
	TenantID     string `json:"tenant_id"`
	ConnectionID string `json:"connection_id"`
	Repository   string `json:"repository"`
	Revision     string `json:"revision"`
	// Actor is `person` or `scheduled` — the distinction FR9 exists for.
	Actor Actor `json:"actor"`
	// ActorID names the person or the process. An attribute, so the vocabulary above stays two
	// members wide and consumers do not have to know the list of schedulers in advance.
	ActorID string `json:"actor_id,omitempty"`
	// Reason is the run or conversation that caused the read.
	Reason string `json:"reason,omitempty"`
	// Outcome is `succeeded` or the failure CAUSE — one of the four (FR11). Recorded on the same row
	// as the success case, because "when did it try and fail" is the question a customer asks after a
	// rotated token, and a ledger of successes only cannot answer it.
	Outcome Outcome `json:"outcome"`
	// Bytes and Entries are what the read actually took. Zero on a failure.
	Bytes   int64 `json:"bytes"`
	Entries int   `json:"entries"`
	// DurationMS is how long the clone took, so the per-forge duration metric has a source.
	DurationMS int64 `json:"duration_ms"`
	AtMS       int64 `json:"at_ms"`
}

// ConnectionStore is the durable side of a connection and its ledger.
//
// 🚫 There is no `Update`. A connection's repository cannot be changed — changing it would silently
// re-point every future read and every stored graph's provenance at a different codebase. Repointing
// is Revoke plus Create, which is two deliberate acts and two ledger entries.
type ConnectionStore interface {
	// Create records a validated connection. Refuses ErrConnectionExists rather than replacing.
	Create(ctx context.Context, c Connection) error
	// ForWorkflow returns the connection for a workflow, or ErrNoConnection.
	ForWorkflow(ctx context.Context, tenantID, workflowID string) (Connection, error)
	// List returns every connection for a tenant, oldest first.
	List(ctx context.Context, tenantID string) ([]Connection, error)
	// Revoke deletes the grant. The CASCADE to derived trees is the caller's (see RevokeConnection),
	// because the snapshots live in a different store and a store method that could only delete half
	// of it is a method that eventually does.
	Revoke(ctx context.Context, tenantID, connectionID string) error
	// AppendRecord appends one clone record.
	AppendRecord(ctx context.Context, r CloneRecord) error
	// Records returns a connection's records, newest first, capped at limit.
	Records(ctx context.Context, tenantID, connectionID string, limit int) ([]CloneRecord, error)
}

// Validate rejects a partial Connection. Called before every write, for Ref.Validate's reason: a
// connection with an empty TenantID would otherwise read as a legitimate row in a tenant-keyed store.
func (c Connection) Validate() error {
	switch {
	case c.ConnectionID == "":
		return fmt.Errorf("sourceingest: connection has no connection_id")
	case c.TenantID == "":
		return fmt.Errorf("sourceingest: connection has no tenant_id")
	case c.WorkflowID == "":
		return fmt.Errorf("sourceingest: connection has no workflow_id")
	case !c.Forge.Valid():
		return fmt.Errorf("sourceingest: connection names no supported forge (%q)", c.Forge)
	case c.Repository == "":
		return fmt.Errorf("sourceingest: connection has no repository")
	case !c.GrantKind.Valid():
		return fmt.Errorf("sourceingest: connection has no grant kind (%q)", c.GrantKind)
	}
	if strings.Count(c.Repository, "/") != 1 {
		return fmt.Errorf("sourceingest: repository %q is not owner/name — a connection names exactly one repository", c.Repository)
	}
	if err := validateSubPath(c.SubPath); err != nil {
		return err
	}
	return nil
}

// validateSubPath refuses a sub-path that could escape the clone root.
//
// The same rule the TreeGuard applies to entries, applied to the ROOT the guard is pointed at —
// because a sub-path of `../../etc` would move the root itself outside the scratch directory, and
// every per-entry check downstream would then be measuring against the wrong root and passing.
func validateSubPath(p string) error {
	if p == "" {
		return nil
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || strings.Contains(p, ":") {
		return fmt.Errorf("sourceingest: sub-path %q must be relative to the repository root", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("sourceingest: sub-path %q climbs out of the repository", p)
		}
	}
	return nil
}
