package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/heros-foreal/heros/internal/tenancy"
)

// store.go holds tenants, users and sessions.

var (
	ErrNoSuchUser = errors.New("auth: no such user")
	ErrNoSession  = errors.New("auth: no such session")
	ErrExpired    = errors.New("auth: session has expired")
	ErrEmailTaken = errors.New("auth: that email already exists in this organization")
	// ErrAccountElsewhere means the address already has an account in a DIFFERENT organization.
	//
	// 🔴 A separate error from ErrAlreadyMember, which means "already in THIS organization". They read
	// alike and are not: one person needs to sign in here, the other needs to know their address is
	// spoken for somewhere they may not remember. Since migration 0008 an address belongs to exactly one
	// organization, so this is the case an invitation now hits whenever the person already has an
	// account anywhere.
	ErrAccountElsewhere = errors.New("auth: that email already has an account in another organization")
	// ErrBadRole means a role string is not one this build knows.
	ErrBadRole = errors.New("auth: unknown role")
	// ErrLastOwner means an operation would leave an organization with nobody who can administer it.
	ErrLastOwner = errors.New("auth: an organization must always have an owner")
	// ErrNoSuchMember means the person named is not in this tenant. 🔴 Also returned when they exist in
	// ANOTHER tenant, so a member id cannot be used to probe for the existence of other customers'
	// accounts.
	ErrNoSuchMember = errors.New("auth: no such member in this organization")
	// ErrRefused means the caller's role does not permit acting on the target.
	ErrRefused = errors.New("auth: your role does not permit that")
)

// SessionLifetime is how long a login lasts.
//
// 🔴 Bounded, and not configurable to "never". A credential with no expiry outlives the reason it was
// issued, and the only way to end it is for somebody to notice it exists.
const SessionLifetime = 14 * 24 * time.Hour

// Store persists identity.
type Store struct{ db *sql.DB }

// NewStore wraps a pool.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// CreateTenant makes an organization, idempotently.
func (s *Store) CreateTenant(ctx context.Context, id, name string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("auth: a tenant needs an id")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tenants (id, name, created_at) VALUES ($1,$2,$3)
		ON CONFLICT (id) DO NOTHING`, id, name, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("auth: create tenant: %w", err)
	}
	return nil
}

// CreateUser adds a person to a tenant in a given role.
//
// 🔴 The role is a required argument with no default. A default would have to be one of "member", which
// silently under-privileges the founding account until somebody notices nobody can invite anyone, or
// "owner", which silently over-privileges every account created by any path added later. Making every
// call site say which it means costs one word and removes the class.
func (s *Store) CreateUser(ctx context.Context, tenant, email, password string, role tenancy.Role) (string, error) {
	if !tenancy.ValidRole(string(role)) {
		return "", fmt.Errorf("%w: %q", ErrBadRole, role)
	}
	hash, err := HashPassword(ctx, password)
	if err != nil {
		return "", err
	}
	id := "usr_" + randomID()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO users (id, tenant, email, password_hash, role, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		id, tenant, normalizeEmail(email), hash, string(role), time.Now().UTC())
	if err != nil {
		// 🔴 BOTH address indexes, not just the per-organization one. Migration 0008 added a global
		// unique index, and that is the one this collides with when somebody who already has an account
		// is invited into another organization — the common case now. Matching only the old name
		// returned the raw driver error to the caller, so an invited person saw a Postgres constraint
		// message instead of being told the address is already in use.
		if isDuplicateEmail(err) {
			return "", ErrEmailTaken
		}
		return "", fmt.Errorf("auth: create user: %w", err)
	}
	return id, nil
}

// dummyHash is verified against when no user matches, so a login attempt costs the same whether or not
// the email exists.
//
// 🔴 Without it, "no such user" returns in microseconds and "wrong password" in tens of milliseconds,
// and the difference is a free oracle for enumerating who has an account. The comparison is thrown away;
// the time it takes is the point.
var dummyHash, _ = HashPassword(context.Background(), "a-password-nobody-has-ever-used")

// Login verifies a password and issues a session, returning the TOKEN — the only time it exists outside
// the customer's browser.
func (s *Store) Login(ctx context.Context, tenant, email, password string) (token string, p tenancy.Principal, err error) {
	var userID, hash, role string

	// 🔴 An EMPTY tenant means "resolve it from the address", which is what self-serve sign-up made
	// necessary: anybody can create an organization, so a sign-in form carrying two fields cannot say
	// which one it is for. Migration 0008 makes an address unique across the whole deployment, so this
	// query returns one row or none — never an ambiguous set.
	//
	// 🔴 It is ONE query either way, and that is deliberate. The obvious shape — look the address up,
	// then log in to whatever came back — reintroduces exactly the oracle the decoy below exists to
	// deny: the lookup would answer "no such address" in microseconds, before any password work, and
	// the constant-cost guarantee would hold only for addresses that exist. Resolving inside the same
	// statement keeps both branches on the identical path.
	//
	// A non-empty tenant still pins the lookup, so an explicit organization is honoured and every
	// existing caller behaves as it did.
	query := `SELECT id, tenant, password_hash, role FROM users WHERE lower(email) = lower($1)`
	args := []any{normalizeEmail(email)}
	if tenant != "" {
		query += ` AND tenant = $2`
		args = append(args, tenant)
	}
	row := s.db.QueryRowContext(ctx, query, args...)
	switch scanErr := row.Scan(&userID, &tenant, &hash, &role); {
	case errors.Is(scanErr, sql.ErrNoRows):
		// 🔴 The decoy's error is INSPECTED, not discarded, and only for ErrBusy.
		//
		// It used to be `_ = VerifyPassword(...)`, because the result of checking a password nobody has is
		// meaningless. Once verification can be SHED under load that stopped being true: a busy server
		// would answer 503 for an address that exists (the real branch, below, propagates it) and 401 for
		// one that does not — and the whole point of running this decoy is that those two cases must be
		// indistinguishable. Overload has to look the same on both branches or it becomes the oracle the
		// decoy exists to prevent, readable by anybody willing to make the server busy.
		if err := VerifyPassword(ctx, password, dummyHash); errors.Is(err, ErrBusy) {
			return "", tenancy.Principal{}, err
		}
		return "", tenancy.Principal{}, ErrNoSuchUser
	case scanErr != nil:
		return "", tenancy.Principal{}, fmt.Errorf("auth: login: %w", scanErr)
	}
	if err := VerifyPassword(ctx, password, hash); err != nil {
		if errors.Is(err, ErrBusy) {
			return "", tenancy.Principal{}, err
		}
		// 🔴 The SAME error for a missing user and a wrong password. Distinguishing them tells an
		// attacker which emails are real, which is the first half of the work.
		return "", tenancy.Principal{}, ErrNoSuchUser
	}

	tok, id := newToken()
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, tenant, user_id, created_at, expires_at) VALUES ($1,$2,$3,$4,$5)`,
		id, tenant, userID, now, now.Add(SessionLifetime)); err != nil {
		return "", tenancy.Principal{}, fmt.Errorf("auth: creating session: %w", err)
	}
	return tok, tenancy.Principal{
		Tenant: tenant, Subject: email, SessionID: id, UserID: userID, Role: roleOf(role, userID),
	}, nil
}

// Authenticate turns a token into a principal.
//
// 🔴 Expiry is checked in the QUERY, not after reading the row. A check in Go is a check somebody can
// return early before, and the row would then authorise a request on a credential that has lapsed.
func (s *Store) Authenticate(ctx context.Context, token string) (tenancy.Principal, error) {
	id := tokenID(token)
	var tenant, userID, email, role string
	// 🔴 The ROLE is read here, on every request, rather than being stamped onto the session at login.
	// Authority that lives in a credential can only be withdrawn by destroying the credential, so
	// somebody demoted this morning would keep yesterday's access for up to a fortnight. One join makes
	// a demotion true on their next click.
	err := s.db.QueryRowContext(ctx, `
		SELECT s.tenant, u.id, u.email, u.role FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND s.expires_at > now()`, id).Scan(&tenant, &userID, &email, &role)
	if errors.Is(err, sql.ErrNoRows) {
		// An expired session and an unknown one are the same answer: neither tells the holder whether
		// the token was ever real.
		return tenancy.Principal{}, ErrNoSession
	}
	if err != nil {
		return tenancy.Principal{}, fmt.Errorf("auth: authenticate: %w", err)
	}
	return tenancy.Principal{
		Tenant: tenant, Subject: email, SessionID: id, UserID: userID, Role: roleOf(role, userID),
	}, nil
}

// roleOf converts a stored role, refusing anything this build does not know.
//
// 🔴 An unrecognised role becomes the EMPTY role, which holds no capabilities — not "member", which
// would quietly grant whatever member holds today. The database has a CHECK constraint that should make
// this impossible, so reaching it means something is wrong that a person needs to look at: it is logged
// with the user id and never silently absorbed.
func roleOf(stored, userID string) tenancy.Role {
	if tenancy.ValidRole(stored) {
		return tenancy.Role(stored)
	}
	log.Printf("WARN auth.role.unknown user=%s role=%q — this account has been granted no capabilities "+
		"until the row is corrected", userID, stored)
	return ""
}

// Logout ends one session.
func (s *Store) Logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = $1`, tokenID(token))
	return err
}

// CountUsers reports how many people exist, used to decide whether bootstrapping is needed.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// newToken returns a fresh session token and the id it is stored under.
//
// 🔴 256 bits from crypto/rand. A guessable session token is a login, and math/rand seeded by anything
// is guessable — the whole security of a session is that nobody can produce another one.
func newToken() (token, id string) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("auth: no entropy available: " + err.Error())
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, tokenID(token)
}

// tokenID is the SHA-256 of a token. What the database stores.
func tokenID(token string) string { return TokenKey(token) }

// TokenKey is the SHA-256 of a token, hex encoded — the form this package stores and looks up by.
//
// 🔴 Exported so that anything outside this package needing to key on a token (a rate limiter, a log
// line) keys on the same value the database does, and never on the raw token. A map key lives for as
// long as the map does, and a raw invitation or reset token sitting in a long-lived map is a credential
// kept somewhere nobody decided to keep it. The hash identifies the token exactly as well and is worth
// nothing to whoever reads it.
func TokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("auth: no entropy available: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
