// Package tenancy is the identity domain P27 adds: the organization, the person, their relationship,
// the invitation that creates it, and the credential that proves it.
//
// # What was here before, and why a package was needed
//
// A tenant was a key in a map. `auth.Registry` built `map[string]Principal` from `cfg.TenantCredentials`
// at boot, so onboarding a customer meant editing a file and restarting a process — and a tenant created
// at runtime had nowhere to be written. Meanwhile `delivery.tenant_id`, `workflow_ir.tenant_id` and five
// more columns had been foreign keys into nothing for phases. This package owns the row they name.
//
// # The four words this package keeps apart
//
//   - TENANT — an organization we bill. Customers never read this word; the console says "organization".
//   - USER — a person, identified by the verified `(issuer, subject)` pair P22 produces. `Email` is a
//     DISPLAY attribute and never the identity: an address is reassigned inside a company, a subject is
//     not, and keying on email means the new hire who inherits `sales@` inherits the previous holder's
//     account.
//   - MEMBERSHIP — a person's relationship to an organization. A join table rather than a column on the
//     user, because a contractor works for two customers, and because a seat is a property of the
//     organization rather than of the person.
//   - CREDENTIAL — a thing that authenticates. It may name a person (`UserID` set) or not (a machine
//     credential). That single nullable field is what lets member removal revoke a person's keys without
//     breaking the customer's build pipeline, and it is why the removal preview can list what it is NOT
//     revoking.
//
// # 🔴 The credential's plaintext exists for one instant
//
// `NewCredentialSecret` mints it, `HashSecret` is what gets stored, and nothing in this package can
// return the plaintext afterwards because nothing holds it. A `Credential` value has a `Hash` and no
// secret field, so "accidentally log the credential" is not a mistake the type permits.
//
// # Every read returns an error, including the ones that cannot fail in memory
//
// `MemStore` reads a map, which cannot fail, and it still returns an error — because `PGStore` can, and
// a durable store with nowhere to report a failed read is how an outage gets spelled "this platform has
// no customers". `account.Store.List` carries the same comment for the same reason.
package tenancy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Status is an organization's lifecycle state.
//
// It reuses `account.Status`'s vocabulary deliberately. One lifecycle with two enums is a place for two
// answers to disagree, and the one that matters — may this tenant run? — would be read from whichever the
// caller happened to know about.
type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
)

// Suspended reports whether s halts the organization.
func (s Status) Suspended() bool { return s == StatusSuspended }

// Role is a member's authority inside one organization. Three values, closed. A fourth role is a phase,
// not a pull request — a permission model nobody has asked for is the thing this package must not grow.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

// CanInvite reports whether r may spend a seat. A member may not, because invitations cost money.
func (r Role) CanInvite() bool { return r == RoleOwner || r == RoleAdmin }

// CanAdminister reports whether r may change another member's role or remove them.
func (r Role) CanAdminister() bool { return r == RoleOwner || r == RoleAdmin }

// KnownRole reports whether r is one of the three. An invented role would be read by every switch as
// "something else".
func KnownRole(r Role) bool { return r == RoleOwner || r == RoleAdmin || r == RoleMember }

// MemberStatus is a membership's state. Removal is a STATE, not a delete: the audit chain has to keep
// resolving to a name, and a hard delete orphans every attribution at exactly the moment somebody is
// asking who did something.
type MemberStatus string

const (
	MemberActive  MemberStatus = "active"
	MemberRemoved MemberStatus = "removed"
)

// Tenant is one organization.
type Tenant struct {
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Active reports whether the organization is not suspended.
func (t Tenant) Active() bool { return !t.Status.Suspended() }

// User is a person.
type User struct {
	UserID string `json:"user_id"`
	// Issuer and Subject are the VERIFIED federated identity. Together they are unique; neither alone is.
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
	// Email is a display attribute. 🚫 Never the identity — see the package doc.
	//
	// ⚠️ With one stated exception, added by P28: on the `password` seam the address IS the subject, because
	// there is no identity provider and no `sub` to be stable instead. `passwordidentity.go` carries the whole
	// argument, and ADR-012 Decision 5 records it. The rule above is unchanged for every federated seam.
	Email string `json:"email"`
	// EmailVerifiedAt is when this person proved control of `Email` by following a link sent to it. NULL
	// means unverified — which is what every row that existed before P28 is, honestly, since nobody ever
	// verified them. It gates exactly two actions (inviting a member, moving to a plan that charges) and
	// nothing else; see ADR-012 Decision 7 for why not sign-in itself.
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// EmailVerified reports whether this person has proved control of their address.
func (u User) EmailVerified() bool { return u.EmailVerifiedAt != nil }

// Membership is a person's relationship to one organization.
type Membership struct {
	UserID    string       `json:"user_id"`
	TenantID  string       `json:"tenant_id"`
	Role      Role         `json:"role"`
	Status    MemberStatus `json:"status"`
	InvitedBy string       `json:"invited_by,omitempty"`
	JoinedAt  time.Time    `json:"joined_at"`
	RemovedAt *time.Time   `json:"removed_at,omitempty"`
}

// Active reports whether this membership currently grants anything.
func (m Membership) Active() bool { return m.Status == MemberActive }

// Invitation is a PENDING membership, and is not a membership.
//
// The link that carries `InvitationID` pre-fills the organization and the address; it grants nothing.
// Membership is created only when a completed sign-in yields a VERIFIED address equal to `Email`, so
// forwarding the invitation to the wrong person creates nothing.
type Invitation struct {
	InvitationID string     `json:"invitation_id"`
	TenantID     string     `json:"tenant_id"`
	Email        string     `json:"email"`
	Role         Role       `json:"role"`
	InvitedBy    string     `json:"invited_by,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	AcceptedAt   *time.Time `json:"accepted_at,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

// Pending reports whether this invitation can still be accepted at time `now`.
func (i Invitation) Pending(now time.Time) bool {
	return i.AcceptedAt == nil && i.RevokedAt == nil && now.Before(i.ExpiresAt)
}

// Credential authenticates a caller. It holds a HASH and has no field a plaintext secret would fit in.
type Credential struct {
	CredentialID string `json:"credential_id"`
	TenantID     string `json:"tenant_id"`
	// UserID empty means a MACHINE credential — a CI key, a service — not "unknown owner". The
	// distinction is what member removal acts on, and what the removal preview discloses.
	UserID string `json:"user_id,omitempty"`
	Label  string `json:"label"`
	Role   Role   `json:"role"`
	// Hash is the stored form. 🔴 The plaintext is returned once, by the call that created it, and is
	// never readable again from anywhere.
	Hash      string     `json:"-"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// Personal reports whether this credential names a person.
func (c Credential) Personal() bool { return c.UserID != "" }

// Kind is the word a surface shows: "personal" or "machine". A single word, because the difference
// decides what member removal covers and a reader must not have to infer it from a blank column.
func (c Credential) Kind() string {
	if c.Personal() {
		return "personal"
	}
	return "machine"
}

// Revoked reports whether this credential has been revoked.
func (c Credential) Revoked() bool { return c.RevokedAt != nil }

// Session is one console session.
//
// 🔴 `TokenHash` is the lookup key and `SessionID` is separate, exactly as the in-memory console store
// already separates them: the id is what appears in logs, the token is the bearer, and a log line naming
// a session must not be replayable as that session.
type Session struct {
	TokenHash string `json:"-"`
	SessionID string `json:"session_id"`
	TenantID  string `json:"tenant_id"`
	// Purpose separates the browser's cookie from the credential the console presents upstream.
	//
	// 🔴 They are the same SHAPE and not the same thing. A console session proves which browser this
	// is, to the console. An upstream token is a platform credential. Without the distinction a stolen
	// cookie would authenticate against the whole API — today it reaches only the console, which
	// exposes a closed set of routes. `auth` resolves ONLY PurposeUpstream.
	Purpose Purpose `json:"purpose"`
	// UserID empty means the principal is not a person. Never a placeholder.
	UserID    string `json:"user_id,omitempty"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
	RevokedAt int64  `json:"revoked_at,omitempty"`
}

// Live reports whether the session is usable at `nowMillis`.
func (s Session) Live(nowMillis int64) bool {
	return s.RevokedAt == 0 && nowMillis < s.ExpiresAt
}

// Purpose is what a session row is FOR.
type Purpose string

const (
	// PurposeUpstream is the short-lived credential the console's server side presents to the platform
	// API. It is the default, because every row that existed before the distinction was one of these.
	PurposeUpstream Purpose = "upstream"
	// PurposeConsole is the browser's cookie. It is NOT an API credential and `auth` refuses it as one.
	PurposeConsole Purpose = "console"
)

// KnownPurpose reports whether p is one of the two.
func KnownPurpose(p Purpose) bool { return p == PurposeUpstream || p == PurposeConsole }

// Errors. They are distinguishable because callers act differently on each.
var (
	ErrNotFound      = errors.New("tenancy: no such record")
	ErrExists        = errors.New("tenancy: already exists")
	ErrLastOwner     = errors.New("tenancy: an organization must keep at least one owner")
	ErrUnknownRole   = errors.New("tenancy: unknown role")
	ErrEmptyField    = errors.New("tenancy: a required field is empty")
	ErrNotAMember    = errors.New("tenancy: not an active member of that organization")
	ErrInviteExpired = errors.New("tenancy: that invitation is no longer valid")
	ErrInviteMatch   = errors.New("tenancy: that invitation was issued to a different address")
	ErrSuspended     = errors.New("tenancy: that organization is suspended")
	// ErrDeviceCode is the ONE answer a device authorization gives for denied, expired, already-used and
	// unknown (task 13.7). Distinguishing them helps only somebody enumerating codes, and a real user's
	// next action — run `heros login` again — is the same in all four cases.
	ErrDeviceCode = errors.New("tenancy: that device code is not usable")
	// ErrDevicePollTooFast is the CLI polling faster than it was told to. Its own error because the
	// caller's action differs: back off, do not start over.
	ErrDevicePollTooFast = errors.New("tenancy: polling faster than the interval the server set")
	// ErrNoPassword is a person who has no password set. Distinct from ErrNotFound because the person may
	// exist perfectly well and simply authenticate another way — a federated user has no password and that
	// is not a missing record.
	ErrNoPassword = errors.New("tenancy: that person has no password")
	// ErrNotArgon2id refuses a stored password that is not argon2id-tagged. It is its own error because it
	// means something wrote a value this system does not produce, which is an operator-grade problem and not
	// a user-grade one.
	ErrNotArgon2id = errors.New("tenancy: a stored password must be an argon2id encoding")
	// ErrUnknownTokenPurpose is an identity token minted for something that is not one of the two purposes.
	ErrUnknownTokenPurpose = errors.New("tenancy: unknown identity-token purpose")
	// ErrIdentityToken is the ONE answer for spent, expired, wrong-purpose and unknown. The reasoning is
	// ErrDeviceCode's, unchanged: distinguishing them helps only somebody enumerating tokens, and a real
	// person's next action — request another link — is the same in all four cases.
	ErrIdentityToken = errors.New("tenancy: that link is no longer usable")
)

// RemovalPreview is what a member-removal screen must show BEFORE the removal can be confirmed.
//
// 🔴 `MachineCredentials` is the load-bearing field. An offboarding screen that lists what it revokes
// and hides what it leaves running is worse than no screen: the person confirming it signs an attestation
// that is wrong, and a CI key created by the departing engineer keeps deploying. Both halves, always.
type RemovalPreview struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	// Sessions and PersonalCredentials are what removal WILL end.
	Sessions            int          `json:"sessions"`
	PersonalCredentials []Credential `json:"personal_credentials"`
	// MachineCredentials are what removal will NOT touch, by label.
	MachineCredentials []Credential `json:"machine_credentials"`
	// LastOwner is true when this removal would be refused. The screen says so before the button, not
	// after it.
	LastOwner bool `json:"last_owner"`
}

// RemovalResult reports what a removal actually did, so a caller can log the two numbers rather than
// logging "removed" and hoping.
type RemovalResult struct {
	Membership          Membership `json:"membership"`
	SessionsRevoked     int        `json:"sessions_revoked"`
	CredentialsRevoked  int        `json:"credentials_revoked"`
	MachineCredsUnknown int        `json:"machine_credentials_untouched"`
}

// Store is the identity domain's system of record.
//
// Two implementations, one interface, one behavioural suite (`tenancy_test.go` runs it against both).
// `MemStore` is the hermetic and default-path store; `PGStore` is the durable one. An implementation
// that satisfies the interface and not the suite is not a second implementation, it is a second
// behaviour.
type Store interface {
	// ── organizations ──────────────────────────────────────────────────────────────────────────────
	CreateTenant(t Tenant) (Tenant, error)
	GetTenant(tenantID string) (Tenant, error)
	ListTenants() ([]Tenant, error)
	SetTenantStatus(tenantID string, s Status, at time.Time) (Tenant, error)

	// ── people ─────────────────────────────────────────────────────────────────────────────────────
	// UpsertUser creates the person or updates the DISPLAY attributes of an existing one, keyed by the
	// verified `(issuer, subject)` pair. It never changes `UserID`, because every referencing row would
	// have to be rewritten.
	UpsertUser(u User) (User, error)
	GetUser(userID string) (User, error)
	FindUser(issuer, subject string) (User, error)

	// ── memberships ────────────────────────────────────────────────────────────────────────────────
	PutMembership(m Membership) (Membership, error)
	GetMembership(userID, tenantID string) (Membership, error)
	ListMembers(tenantID string) ([]Membership, error)
	ListMembershipsFor(userID string) ([]Membership, error)
	// SetRole refuses any change that would leave the organization with no active owner.
	SetRole(userID, tenantID string, role Role) (Membership, error)
	// PreviewRemoval reports what a removal would and would NOT do. See RemovalPreview.
	PreviewRemoval(userID, tenantID string) (RemovalPreview, error)
	// RemoveMember is ATOMIC across membership, sessions and credentials. There must be no window in
	// which somebody is off the member list and still holds a working key.
	RemoveMember(userID, tenantID string, at time.Time) (RemovalResult, error)

	// ── invitations ────────────────────────────────────────────────────────────────────────────────
	CreateInvitation(i Invitation) (Invitation, error)
	GetInvitation(invitationID string) (Invitation, error)
	ListInvitations(tenantID string) ([]Invitation, error)
	// AcceptInvitation stamps `accepted_at` and is single-use at the store, not in caller logic.
	AcceptInvitation(invitationID string, at time.Time) (Invitation, error)
	RevokeInvitation(invitationID string, at time.Time) (Invitation, error)

	// ── credentials ────────────────────────────────────────────────────────────────────────────────
	// CreateCredential stores the HASH. The caller mints the secret, shows it once, and drops it.
	CreateCredential(c Credential) (Credential, error)
	// ResolveCredential looks a credential up BY HASH. There is no method that takes a plaintext,
	// because a method that took one could log it.
	ResolveCredential(hash string) (Credential, error)
	ListCredentials(tenantID string) ([]Credential, error)
	RevokeCredential(credentialID string, at time.Time) (Credential, error)

	// ── device authorization (task 13.1) ───────────────────────────────────────────────────────────
	// CreateDeviceAuthorization stores a minted, pending authorization. The caller mints both codes,
	// hands the plaintexts to the CLI and the screen, and keeps neither.
	CreateDeviceAuthorization(d DeviceAuthorization) (DeviceAuthorization, error)
	// FindDeviceByUserCode resolves the SHORT code a person typed, by hash. Used only on the approval
	// path, which already required an authenticated person.
	FindDeviceByUserCode(userCodeHash string) (DeviceAuthorization, error)
	// DecideDevice records an approval or a denial. On approval it takes the organization the approver
	// chose and the credential that was issued, and it is where single-use lives: a second decision on
	// the same record is ErrDeviceCode, at the store, not in caller logic.
	DecideDevice(deviceID string, status DeviceAuthStatus, approvedBy, tenantID, credentialID string, at time.Time) (DeviceAuthorization, error)
	// CollectDevice exchanges the CLI's device code for its issued credential, exactly once. It returns
	// the authorization it collected; the caller reads CredentialID from it.
	CollectDevice(deviceCodeHash string, at time.Time) (DeviceAuthorization, error)

	// ── passwords (P28) ────────────────────────────────────────────────────────────────────────────
	// SetPassword stores an argon2id encoding, creating the record or replacing it, and CLEARS the failure
	// counters. Clearing is part of the same write on purpose: a person who has just proved control of
	// their address by resetting must not still be locked out by the failures that made them reset.
	//
	// 🔴 It takes an ENCODING, never a plaintext. There is no parameter here a password could arrive in.
	SetPassword(userID, encoded string, at time.Time) (UserPassword, error)
	// GetPassword returns the stored record, or ErrNoPassword. It returns the record even while locked, so
	// the caller can tell a person how long the lock has left rather than refusing them opaquely.
	GetPassword(userID string) (UserPassword, error)
	// RecordPasswordFailure advances the lockout state under the given policy and returns the new state, so
	// the caller learns from the same call whether this failure was the one that locked the account.
	RecordPasswordFailure(userID string, at time.Time, pol LockoutPolicy) (UserPassword, error)
	// ClearPasswordFailures is what a successful sign-in does.
	ClearPasswordFailures(userID string) error

	// ── addresses and identity tokens (P28) ────────────────────────────────────────────────────────
	// FindUserByEmail resolves a person by issuer and address. It exists beside FindUser because on the
	// password seam the caller has an address and not a subject, and normalising in two places is how the
	// two would eventually differ.
	FindUserByEmail(issuer, email string) (User, error)
	// MarkEmailVerified stamps the address as proved. Idempotent: verifying twice keeps the first time,
	// because the first time is the one that is true.
	MarkEmailVerified(userID string, at time.Time) (User, error)
	// MintIdentityToken stores a hashed, expiring, purpose-bound token.
	MintIdentityToken(t IdentityToken) (IdentityToken, error)
	// ConsumeIdentityToken spends a token, exactly once, at the store.
	//
	// 🔴 It is ONE conditional statement — not read-then-write — so two clicks on the same link cannot both
	// win. Spent, expired, wrong-purpose and unknown all return ErrIdentityToken; the wire must not learn
	// which.
	ConsumeIdentityToken(tokenHash string, purpose TokenPurpose, at time.Time) (IdentityToken, error)
	// RevokeEverythingFor ends every session and every PERSONAL credential a person holds, across every
	// organization they belong to, and reports the machine credentials it did not touch.
	//
	// It is the store's operation rather than a loop in the caller because a password reset that revokes
	// half of somebody's access is worse than one that revokes none: they would believe they were safe.
	RevokeEverythingFor(userID string, at time.Time) (PersonRevocation, error)

	// ── console sessions ───────────────────────────────────────────────────────────────────────────
	CreateSession(s Session) (Session, error)
	ResolveSession(tokenHash string) (Session, error)
	RevokeSession(tokenHash string, atMillis int64) error
	ListSessionsFor(userID, tenantID string) ([]Session, error)
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Secrets
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

// SecretPrefix marks a value as one of ours in a log, a paste, or a secret scanner's rules. It carries
// no entropy and no meaning beyond that.
const SecretPrefix = "heros_"

// NewCredentialSecret mints a credential's plaintext. 32 bytes of crypto/rand, URL-safe.
//
// The caller shows this value once and drops it. Nothing in this package stores it, and no type here has
// a field it would fit in.
func NewCredentialSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("tenancy: cannot mint a credential: %w", err)
	}
	return SecretPrefix + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// HashSecret is the ONE way a presented secret becomes a stored value.
//
// SHA-256, not bcrypt/argon2, and the reason is the threat model rather than laziness: this is a
// 256-bit random value we minted, not a human-chosen password, so there is no dictionary to slow down
// and no user-chosen entropy to compensate for. A KDF here would add per-request cost to the hot
// authentication path and buy nothing an attacker could not already skip.
//
// Verification is a HASH LOOKUP, never a scan-and-compare: the presented value is hashed and the store
// is asked for that exact hash. No code path compares secret bytes, so there is no comparison to be
// timing-sensitive about. `ConstantTimeEqualHash` exists for the one place a caller holds two hashes
// and wants the comparison not to be a source of doubt in review.
func HashSecret(plaintext string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(plaintext)))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeEqualHash compares two stored hashes without an early exit.
func ConstantTimeEqualHash(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Validation shared by both stores
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

func validateTenant(t Tenant) (Tenant, error) {
	t.TenantID = strings.TrimSpace(t.TenantID)
	t.Name = strings.TrimSpace(t.Name)
	if t.TenantID == "" {
		return Tenant{}, fmt.Errorf("%w: tenant_id", ErrEmptyField)
	}
	if t.Name == "" {
		return Tenant{}, fmt.Errorf("%w: name", ErrEmptyField)
	}
	switch t.Status {
	case "":
		t.Status = StatusActive
	case StatusActive, StatusSuspended:
	default:
		return Tenant{}, fmt.Errorf("tenancy: unknown tenant status %q", t.Status)
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Unix(0, 0).UTC()
	}
	return t, nil
}

func validateUser(u User) (User, error) {
	u.UserID = strings.TrimSpace(u.UserID)
	u.Issuer = strings.TrimSpace(u.Issuer)
	u.Subject = strings.TrimSpace(u.Subject)
	u.Email = strings.TrimSpace(u.Email)
	if u.Issuer == "" {
		return User{}, fmt.Errorf("%w: issuer", ErrEmptyField)
	}
	if u.Subject == "" {
		return User{}, fmt.Errorf("%w: subject", ErrEmptyField)
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Unix(0, 0).UTC()
	}
	return u, nil
}

func validateMembership(m Membership) (Membership, error) {
	m.UserID = strings.TrimSpace(m.UserID)
	m.TenantID = strings.TrimSpace(m.TenantID)
	if m.UserID == "" || m.TenantID == "" {
		return Membership{}, fmt.Errorf("%w: membership needs a user and an organization", ErrEmptyField)
	}
	if !KnownRole(m.Role) {
		return Membership{}, fmt.Errorf("%w: %q", ErrUnknownRole, m.Role)
	}
	if m.Status == "" {
		m.Status = MemberActive
	}
	if m.Status != MemberActive && m.Status != MemberRemoved {
		return Membership{}, fmt.Errorf("tenancy: unknown membership status %q", m.Status)
	}
	if m.JoinedAt.IsZero() {
		m.JoinedAt = time.Unix(0, 0).UTC()
	}
	return m, nil
}

func validateInvitation(i Invitation) (Invitation, error) {
	i.InvitationID = strings.TrimSpace(i.InvitationID)
	i.TenantID = strings.TrimSpace(i.TenantID)
	i.Email = NormalizeEmail(i.Email)
	if i.InvitationID == "" || i.TenantID == "" || i.Email == "" {
		return Invitation{}, fmt.Errorf("%w: an invitation needs an id, an organization and an address", ErrEmptyField)
	}
	if !KnownRole(i.Role) {
		return Invitation{}, fmt.Errorf("%w: %q", ErrUnknownRole, i.Role)
	}
	if i.ExpiresAt.IsZero() {
		return Invitation{}, errors.New("tenancy: an invitation must expire — a standing offer in an inbox is a way in nobody is tracking")
	}
	if i.CreatedAt.IsZero() {
		i.CreatedAt = time.Unix(0, 0).UTC()
	}
	return i, nil
}

func validateCredential(c Credential) (Credential, error) {
	c.CredentialID = strings.TrimSpace(c.CredentialID)
	c.TenantID = strings.TrimSpace(c.TenantID)
	c.UserID = strings.TrimSpace(c.UserID)
	c.Hash = strings.TrimSpace(c.Hash)
	if c.CredentialID == "" || c.TenantID == "" || c.Hash == "" {
		return Credential{}, fmt.Errorf("%w: a credential needs an id, an organization and a hash", ErrEmptyField)
	}
	if c.Role == "" {
		c.Role = RoleMember
	}
	if !KnownRole(c.Role) {
		return Credential{}, fmt.Errorf("%w: %q", ErrUnknownRole, c.Role)
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Unix(0, 0).UTC()
	}
	return c, nil
}

// NormalizeEmail lowercases and trims an address for MATCHING.
//
// It deliberately does not do anything cleverer — no plus-address stripping, no dot folding. Those are
// provider-specific rules, and applying Gmail's to a corporate directory would silently match two people
// an organization considers distinct. Case folding is the one normalisation every provider agrees on.
func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// EmailDomain returns the domain half of an address, lowercased, or "".
func EmailDomain(s string) string {
	e := NormalizeEmail(s)
	if at := strings.LastIndex(e, "@"); at >= 0 && at+1 < len(e) {
		return e[at+1:]
	}
	return ""
}
