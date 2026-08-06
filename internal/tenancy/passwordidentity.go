package tenancy

import (
	"fmt"
	"strings"
	"time"
)

// passwordidentity.go is the P28 half of the identity domain: the password a person chose, and the
// single-use tokens that confirm an address or let a forgotten password be replaced.
//
// # 🔴 The stored value is opaque to this package
//
// `UserPassword.Encoded` is an argon2id encoding produced by `internal/password`, and NOTHING here parses,
// compares, or produces one. That is deliberate and it is the same discipline `Credential.Hash` follows: this
// package is the system of record, not the verifier, so there is no function here that takes a plaintext and
// therefore no parameter a plaintext could arrive in and be logged from.
//
// The one thing this package does assert is the SHAPE — that a stored value is argon2id-tagged — because that
// is the invariant `password`'s own fence protects from the other side, and the database holds a third copy as
// a CHECK. Three copies of one rule, at three layers that fail independently, is the correct number for a rule
// whose violation is silent and total.
//
// # Why email is the subject on this seam, and why that does not contradict the package doc
//
// The package doc says, correctly, that `Email` is a display attribute and never the identity — because an
// address is reassigned inside a company and an IdP's `sub` is not. That reasoning is about FEDERATION. On the
// password seam there is no IdP and no `sub`: the address is precisely what the person proved, by receiving
// mail at it, and an invented opaque subject would add indirection rather than stability. What survives is the
// part that was load-bearing — `UserID` is still the key every other table references, so changing an address
// rewrites one column — and `platform_user_federated_identity UNIQUE (issuer, subject)` keeps a `password`
// person and an `oidc` person with the same address as two different people. See ADR-012 Decision 5.

// IssuerPassword is the `issuer` value for a person who authenticates with an email and a password.
//
// It is a NAMESPACE, not a URL, for the same reason `console:configured` is: there is no identity provider
// here, and putting a fabricated issuer URL on a person's permanent identity would be a lie that outlives the
// phase that told it.
const IssuerPassword = "password"

// PasswordSubject is the subject for an address on the password seam. One function, so the normalisation can
// never differ between the writer and the reader.
func PasswordSubject(email string) string { return NormalizeEmail(email) }

// UserPassword is one person's stored password and the state that rate-limits guessing at it.
//
// The lockout counters live HERE rather than in a cache because a lock that a restart clears is not a lock,
// and because a per-replica counter means ten attempts becomes ten per replica.
type UserPassword struct {
	UserID string `json:"user_id"`
	// Encoded is the argon2id encoding. 🔴 It is never returned to any surface, never logged, and has no
	// JSON name for the same reason `Credential.Hash` has none.
	Encoded   string    `json:"-"`
	UpdatedAt time.Time `json:"updated_at"`
	// FailedAttempts counts consecutive failures inside the current window.
	FailedAttempts int `json:"failed_attempts"`
	// WindowStartedAt is when the current run of failures began. A window rather than a decaying counter so
	// that "ten failures within fifteen minutes" is the sentence the code implements and the sentence the
	// copy promises — those two drifting apart is how a lockout becomes folklore.
	WindowStartedAt *time.Time `json:"window_started_at,omitempty"`
	// LockedUntil is when sign-in becomes possible again. NULL means not locked — never a zero time, which
	// every comparison would read as "locked until 1970" and therefore as "not locked" by accident.
	LockedUntil *time.Time `json:"locked_until,omitempty"`
}

// Locked reports whether sign-in is currently refused for this person.
func (p UserPassword) Locked(now time.Time) bool {
	return p.LockedUntil != nil && now.Before(*p.LockedUntil)
}

// LockRemaining is how long the lock still has to run, rounded up to a whole minute so the message a person
// reads says "15 minutes" rather than "14m59.2s".
func (p UserPassword) LockRemaining(now time.Time) time.Duration {
	if !p.Locked(now) {
		return 0
	}
	d := p.LockedUntil.Sub(now)
	if r := d % time.Minute; r != 0 {
		d += time.Minute - r
	}
	return d
}

// LockoutPolicy is how many failures inside what window buy how long a lock.
//
// It is a value passed to the store rather than a constant inside it, because the store is the system of
// record and the policy is a product decision — and because a test that had to wait fifteen minutes to observe
// a lock would not be written.
type LockoutPolicy struct {
	Threshold int
	Window    time.Duration
	LockFor   time.Duration
}

// DefaultLockout is PRD FR6: ten consecutive failures within fifteen minutes lock for fifteen minutes.
//
// Per-PERSON, deliberately, and not per network address: an address lock is a denial-of-service surface aimed
// at a shared office, where one person's typo would lock out everybody behind the same NAT.
var DefaultLockout = LockoutPolicy{Threshold: 10, Window: 15 * time.Minute, LockFor: 15 * time.Minute}

// TokenPurpose is what an identity token is FOR. A closed set of two.
//
// 🔴 It is a SEPARATE type from `Purpose` (the session purpose) even though both are strings with two values.
// Merging them would make a reset token and a console session members of one enum, and the entire reason
// `auth` now runs an ALLOWLIST over session purposes is that a token from the identity domain must never be
// resolvable as an API credential. Two types cannot be confused by a caller; two values of one type can.
type TokenPurpose string

const (
	// TokenVerifyEmail proves control of an address.
	TokenVerifyEmail TokenPurpose = "verify_email"
	// TokenResetPassword authorises replacing a password without knowing the old one.
	TokenResetPassword TokenPurpose = "reset_password"
)

// KnownTokenPurpose reports whether p is one of the two.
func KnownTokenPurpose(p TokenPurpose) bool {
	return p == TokenVerifyEmail || p == TokenResetPassword
}

// IdentityToken is a single-use, expiring, purpose-bound token sent to an address.
//
// 🔴 Single use is a property of the STORE, not of caller logic — `ConsumeIdentityToken` is one conditional
// UPDATE, so two clicks on the same link cannot both win. `invitation.accepted_at` established that pattern
// and this follows it.
type IdentityToken struct {
	// TokenHash is SHA-256 of a value we minted with crypto/rand. That is the correct hash here for exactly
	// the reason it is the wrong one for a password: there is no dictionary to slow down.
	TokenHash string       `json:"-"`
	UserID    string       `json:"user_id"`
	Purpose   TokenPurpose `json:"purpose"`
	// Email is the address this token proves control of. It is stored rather than read from the user, so
	// confirming a CHANGED address is expressible without the old one having to be overwritten first.
	Email      string     `json:"email"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
}

// Live reports whether this token can still be spent at `now`.
func (t IdentityToken) Live(now time.Time) bool {
	return t.ConsumedAt == nil && now.Before(t.ExpiresAt)
}

// TokenTTL is how long a confirmation or reset link lasts.
//
// One hour for a reset: long enough to survive a slow mail server, short enough that a link sitting in an
// inbox is not a standing way in. Twenty-four hours for a confirmation, because it is not a credential — it
// proves an address and grants nothing on its own.
const (
	ResetTokenTTL     = time.Hour
	VerifyTokenTTL    = 24 * time.Hour
	BootstrapTokenTTL = 24 * time.Hour
)

// PersonRevocation is what `RevokeEverythingFor` did, and what it deliberately did not do.
//
// 🔴 `MachineCredentials` is the load-bearing field, exactly as it is in `RemovalPreview`. A password reset
// screen that lists what it revoked and hides what it left running tells somebody who is resetting because
// they were compromised that they are now safe, when a CI key created by the same person is still deploying.
type PersonRevocation struct {
	SessionsRevoked    int          `json:"sessions_revoked"`
	CredentialsRevoked int          `json:"credentials_revoked"`
	MachineCredentials []Credential `json:"machine_credentials_untouched"`
}

// ArgonPrefix is the tag every stored password must carry. Declared here because the store validates the
// shape and the migration CHECKs the same string; two spellings of one rule is one spelling too many.
const ArgonPrefix = "$argon2id$"

func validateUserPassword(p UserPassword) (UserPassword, error) {
	p.UserID = strings.TrimSpace(p.UserID)
	p.Encoded = strings.TrimSpace(p.Encoded)
	if p.UserID == "" {
		return UserPassword{}, fmt.Errorf("%w: user_id", ErrEmptyField)
	}
	if p.Encoded == "" {
		return UserPassword{}, fmt.Errorf("%w: a password record needs a stored encoding", ErrEmptyField)
	}
	if !strings.HasPrefix(p.Encoded, ArgonPrefix) {
		// 🔴 The store refuses a value that is not argon2id-tagged. This is the code-side copy of the
		// database CHECK, and it exists so the in-memory store does not quietly permit what the durable one
		// forbids — the same reason `account.MemStore.SetPlan` re-states its CHECK.
		return UserPassword{}, ErrNotArgon2id
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Unix(0, 0).UTC()
	}
	return p, nil
}

func validateIdentityToken(t IdentityToken) (IdentityToken, error) {
	t.TokenHash = strings.TrimSpace(t.TokenHash)
	t.UserID = strings.TrimSpace(t.UserID)
	t.Email = NormalizeEmail(t.Email)
	if t.TokenHash == "" || t.UserID == "" || t.Email == "" {
		return IdentityToken{}, fmt.Errorf("%w: an identity token needs a hash, a user and an address", ErrEmptyField)
	}
	if !KnownTokenPurpose(t.Purpose) {
		return IdentityToken{}, fmt.Errorf("%w: %q", ErrUnknownTokenPurpose, t.Purpose)
	}
	if t.ExpiresAt.IsZero() {
		return IdentityToken{}, fmt.Errorf("%w: an identity token must expire — a link that never dies is a way "+
			"in nobody is tracking", ErrEmptyField)
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Unix(0, 0).UTC()
	}
	return t, nil
}

// applyFailure computes the next lockout state from the current one. Pure, so both stores share it and cannot
// implement two different lockout policies.
func applyFailure(cur UserPassword, at time.Time, pol LockoutPolicy) UserPassword {
	at = at.UTC()
	next := cur
	// A new window starts when there is no window, or when the old one has run out. This is what makes the
	// rule "ten failures WITHIN fifteen minutes" rather than "ten failures ever".
	if next.WindowStartedAt == nil || at.Sub(*next.WindowStartedAt) > pol.Window {
		start := at
		next.WindowStartedAt = &start
		next.FailedAttempts = 0
	}
	next.FailedAttempts++
	if next.FailedAttempts >= pol.Threshold {
		until := at.Add(pol.LockFor)
		next.LockedUntil = &until
	}
	return next
}
