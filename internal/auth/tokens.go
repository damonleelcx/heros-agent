package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/heros-foreal/heros/internal/tenancy"
)

// tokens.go is the three links that stand in for a password: an invitation, a reset, and a confirmation.
//
// # 🔴 Why one error covers unknown, expired and already-used
//
// ErrBadToken, always. Telling somebody a token "has expired" confirms it was real, which turns a
// guessing attempt into a guessing attempt with feedback; telling them it was "already used" tells an
// attacker holding a stolen link that the victim got there first, which is a signal worth having. The
// person who legitimately clicked a stale link needs to know to ask for another one, and that is the
// whole of what the message says.
//
// # 🔴 Why every token is claimed by a conditional UPDATE
//
// Not read-then-check-then-write. Two clicks on the same reset link — a browser prefetching it, a mail
// client scanning it, an impatient person double-clicking — race, and read-then-write lets both through.
// `UPDATE … WHERE used_at IS NULL … RETURNING` is decided by the database, once, and the loser gets
// nothing back.

var (
	// ErrBadToken means a link is unknown, expired, or already used — deliberately one error.
	ErrBadToken = errors.New("auth: that link is no longer valid")
	// ErrAlreadyMember means an invitation was accepted for an address that already has an account here.
	ErrAlreadyMember = errors.New("auth: that address already has an account in this organization")
	// ErrAlreadyVerified means there is nothing to confirm.
	ErrAlreadyVerified = errors.New("auth: that address is already confirmed")
)

// Lifetimes. Each is as short as the job allows.
const (
	// InvitationLifetime is a week: long enough to survive a holiday, short enough that a forgotten
	// invitation in an abandoned mailbox stops being a way in.
	InvitationLifetime = 7 * 24 * time.Hour
	// PasswordResetLifetime is one hour. The link is a complete account takeover for as long as it lives,
	// and the task it exists for takes ninety seconds.
	PasswordResetLifetime = time.Hour
	// EmailVerificationLifetime is a day. It grants nothing, so it can afford to be convenient.
	EmailVerificationLifetime = 24 * time.Hour
)

// ── invitations ──────────────────────────────────────────────────────────────────────────────────

// Invitation is a pending or historical invitation, as the console lists them.
//
// 🚫 Carries no token and no hash. The only copy of the token is in the mail that was sent.
type Invitation struct {
	ID    string       `json:"id"`
	Email string       `json:"email"`
	Role  tenancy.Role `json:"role"`
	// InvitedBy is the address of whoever sent it, or empty if that account has since been removed.
	InvitedBy string    `json:"invited_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Expired   bool      `json:"expired"`
}

// CreateInvitation records an invitation and returns the TOKEN to mail.
//
// The token is returned rather than stored anywhere reachable, so a caller that fails to send it has
// produced an invitation nobody can accept — which is the correct outcome, and why the API handler
// treats a send failure as a failure of the whole operation.
func (s *Store) CreateInvitation(ctx context.Context, actor tenancy.Principal, email string,
	role tenancy.Role) (token string, inv Invitation, err error) {

	email = normalizeEmail(email)
	if email == "" {
		return "", Invitation{}, fmt.Errorf("auth: an invitation needs an email address")
	}
	// 🔴 Checked here as well as by the database CHECK constraint. The constraint is what makes it true;
	// this is what makes the refusal a sentence rather than a Postgres error string.
	if role == tenancy.Owner {
		return "", Invitation{}, fmt.Errorf("%w: ownership is transferred inside the console, to "+
			"somebody who already has an account — never by a link in an email, which travels through a "+
			"mailbox this organization does not control", ErrRefused)
	}
	if !tenancy.CanGrant(actor.Role, role) {
		return "", Invitation{}, fmt.Errorf("%w: you cannot invite somebody as %s", ErrRefused, role)
	}

	tok, hash := newToken()
	now := time.Now().UTC()
	inv = Invitation{
		ID: "inv_" + randomID(), Email: email, Role: role, InvitedBy: actor.Subject,
		CreatedAt: now, ExpiresAt: now.Add(InvitationLifetime),
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM users WHERE tenant = $1 AND lower(email) = lower($2))`,
			actor.Tenant, email).Scan(&exists); err != nil {
			return fmt.Errorf("auth: checking for an existing account: %w", err)
		}
		if exists {
			return ErrAlreadyMember
		}
		// 🔴 Any previous unaccepted invitation for this address is DELETED, not left beside the new one.
		// Re-inviting somebody is what people do when the first mail went astray, and leaving both live
		// means a link the inviter believes they replaced is still a way in. The partial unique index
		// enforces the same thing if this is ever forgotten.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM invitations WHERE tenant = $1 AND lower(email) = lower($2) AND accepted_at IS NULL`,
			actor.Tenant, email); err != nil {
			return fmt.Errorf("auth: replacing an earlier invitation: %w", err)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO invitations (id, token_hash, tenant, email, role, invited_by, created_at, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			inv.ID, hash, actor.Tenant, email, string(role), nullIfEmpty(actor.UserID),
			inv.CreatedAt, inv.ExpiresAt)
		if err != nil {
			return fmt.Errorf("auth: creating invitation: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", Invitation{}, err
	}
	return tok, inv, nil
}

// ListInvitations returns the invitations of one organization that nobody has accepted yet.
func (s *Store) ListInvitations(ctx context.Context, tenant string) ([]Invitation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.email, i.role, coalesce(u.email, ''), i.created_at, i.expires_at,
		       i.expires_at <= now()
		FROM invitations i LEFT JOIN users u ON u.id = i.invited_by
		WHERE i.tenant = $1 AND i.accepted_at IS NULL
		ORDER BY i.created_at DESC`, tenant)
	if err != nil {
		return nil, fmt.Errorf("auth: listing invitations: %w", err)
	}
	defer rows.Close()
	var out []Invitation
	for rows.Next() {
		var inv Invitation
		var role string
		if err := rows.Scan(&inv.ID, &inv.Email, &role, &inv.InvitedBy, &inv.CreatedAt,
			&inv.ExpiresAt, &inv.Expired); err != nil {
			return nil, fmt.Errorf("auth: listing invitations: %w", err)
		}
		inv.Role = tenancy.Role(role)
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RevokeInvitation withdraws an unaccepted invitation.
//
// 🔴 Scoped by tenant, so an invitation id from one organization cannot be used to withdraw another's.
// An already-accepted invitation is not touched: withdrawing it would not remove the person it created,
// and pretending otherwise is worse than refusing.
func (s *Store) RevokeInvitation(ctx context.Context, tenant, id string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM invitations WHERE id = $1 AND tenant = $2 AND accepted_at IS NULL`, id, tenant)
	if err != nil {
		return fmt.Errorf("auth: revoking invitation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("auth: no invitation is waiting under that id — it may have been accepted or " +
			"already withdrawn")
	}
	return nil
}

// PendingInvitation describes an invitation to the person holding the link, before they accept it.
//
// It names the organization and the address so somebody can see what they are joining and with which
// account. 🚫 It does NOT name the inviter's other colleagues, the tenant id, or anything else about the
// organization — the holder of this link is, until they accept, a stranger.
type PendingInvitation struct {
	Email     string       `json:"email"`
	Role      tenancy.Role `json:"role"`
	OrgName   string       `json:"org_name"`
	Inviter   string       `json:"invited_by"`
	ExpiresAt time.Time    `json:"expires_at"`
}

// LookupInvitation resolves a token for the accept screen.
func (s *Store) LookupInvitation(ctx context.Context, token string) (PendingInvitation, error) {
	var p PendingInvitation
	var role string
	err := s.db.QueryRowContext(ctx, `
		SELECT i.email, i.role, t.name, coalesce(u.email, ''), i.expires_at
		FROM invitations i
		JOIN tenants t ON t.id = i.tenant
		LEFT JOIN users u ON u.id = i.invited_by
		WHERE i.token_hash = $1 AND i.accepted_at IS NULL AND i.expires_at > now()`,
		tokenID(token)).Scan(&p.Email, &role, &p.OrgName, &p.Inviter, &p.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PendingInvitation{}, ErrBadToken
	}
	if err != nil {
		return PendingInvitation{}, fmt.Errorf("auth: looking up invitation: %w", err)
	}
	p.Role = tenancy.Role(role)
	return p, nil
}

// AcceptInvitation creates the account and signs the person in, returning a session token.
//
// # 🔴 Why accepting also verifies the address
//
// The token arrived in a mail sent to that address, and the person is holding it. Receipt IS the proof —
// sending a second mail to confirm the address that just confirmed itself would be a step that teaches
// people the confirmation step is noise.
//
// # 🔴 Why the role comes from the row and never from the request
//
// The recipient sends a token and a password, and nothing else that matters. A role in the request body
// is a role the recipient chooses, and the first thing anybody would try is "owner". The database's CHECK
// constraint means even a compromised handler cannot write one.
func (s *Store) AcceptInvitation(ctx context.Context, token, password string) (
	sessionToken string, p tenancy.Principal, err error) {

	// 🔴 The token is checked BEFORE the password is hashed, and this ordering is a defence, not a
	// tidiness.
	//
	// It used to hash first. That made an unauthenticated endpoint where any garbage string cost a full
	// argon2id — 64 MiB and tens of milliseconds — before anything looked at it, which is the cheapest
	// possible way to occupy every hashing slot the server has and starve real sign-ins. A rate limit
	// cannot close it: the only thing to key on here is the token, and an attacker varies the token, so
	// every request would arrive with a fresh budget.
	//
	// 🚫 This lookup is NOT authoritative and must not be treated as one. The claim below — a conditional
	// UPDATE — is what actually decides, atomically, whether this acceptance happens; a token can be
	// accepted by somebody else in the microseconds between the two, and the claim will refuse it. What
	// this check buys is only that known-bad input is rejected for the price of an indexed lookup.
	if _, err := s.LookupInvitation(ctx, token); err != nil {
		return "", tenancy.Principal{}, err
	}

	// Hashed BEFORE the transaction: argon2id is deliberately slow, and holding a row lock for the tens
	// of milliseconds it takes would serialise every acceptance behind it.
	hash, err := HashPassword(ctx, password)
	if err != nil {
		return "", tenancy.Principal{}, err
	}
	now := time.Now().UTC()
	var userID, tenant, email, role string

	err = s.inTx(ctx, func(tx *sql.Tx) error {
		// The claim and the read are one statement. A SELECT followed by an UPDATE would let two
		// simultaneous acceptances both read an unaccepted invitation.
		err := tx.QueryRowContext(ctx, `
			UPDATE invitations SET accepted_at = $2
			WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > now()
			RETURNING tenant, email, role`, tokenID(token), now).Scan(&tenant, &email, &role)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBadToken
		}
		if err != nil {
			return fmt.Errorf("auth: accepting invitation: %w", err)
		}
		userID = "usr_" + randomID()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO users (id, tenant, email, password_hash, role, created_at, email_verified_at)
			VALUES ($1,$2,$3,$4,$5,$6,$6)`, userID, tenant, email, hash, role, now)
		if err != nil {
			if strings.Contains(err.Error(), "users_email_per_tenant") {
				// The whole transaction rolls back, so the invitation stays unaccepted rather than being
				// spent on an acceptance that did not happen.
				return ErrAlreadyMember
			}
			return fmt.Errorf("auth: creating the invited account: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", tenancy.Principal{}, err
	}

	tok, sessionID := newToken()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, tenant, user_id, created_at, expires_at) VALUES ($1,$2,$3,$4,$5)`,
		sessionID, tenant, userID, now, now.Add(SessionLifetime)); err != nil {
		// The account exists; only the convenience of being signed in immediately was lost.
		return "", tenancy.Principal{}, fmt.Errorf("auth: your account was created, but signing you in "+
			"failed — sign in with the password you just chose: %w", err)
	}
	return tok, tenancy.Principal{
		Tenant: tenant, Subject: email, SessionID: sessionID, UserID: userID, Role: roleOf(role, userID),
	}, nil
}

// ── password resets ──────────────────────────────────────────────────────────────────────────────

// CreatePasswordReset issues a reset token for an address, or reports that no such account exists.
//
// 🔴 The caller MUST NOT pass that distinction on to the requester. "I forgot my password" is a request
// anybody can make about anybody's address, and an answer that differs between a real and an unknown
// address is a way to enumerate who has an account here. The API handler answers identically either way;
// this returns the truth because it has to decide whether to send a mail.
func (s *Store) CreatePasswordReset(ctx context.Context, tenant, email string) (
	token, toAddress, orgName string, err error) {

	var userID string
	err = s.db.QueryRowContext(ctx, `
		SELECT u.id, u.email, t.name FROM users u JOIN tenants t ON t.id = u.tenant
		WHERE u.tenant = $1 AND lower(u.email) = lower($2)`,
		tenant, normalizeEmail(email)).Scan(&userID, &toAddress, &orgName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", ErrNoSuchUser
	}
	if err != nil {
		return "", "", "", fmt.Errorf("auth: password reset: %w", err)
	}
	tok, hash := newToken()
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO password_resets (id, token_hash, tenant, user_id, created_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		"pwr_"+randomID(), hash, tenant, userID, now, now.Add(PasswordResetLifetime)); err != nil {
		return "", "", "", fmt.Errorf("auth: password reset: %w", err)
	}
	return tok, toAddress, orgName, nil
}

// ResetPassword sets a new password from a reset token.
//
// # 🔴 Why every session is destroyed
//
// The commonest reason to reset a password is that somebody else may have it. If the old sessions
// survive, the person who took the account keeps it — the reset changes the lock while leaving the
// intruder inside the house. This is also the claim the console makes in the reset mail, and a claim
// about security that is true in some configurations and not others is worse than no claim.
//
// Every OTHER outstanding reset token for the same account is destroyed too, so a second link mailed
// during a confused ten minutes cannot be used afterwards to take the account back.
func (s *Store) ResetPassword(ctx context.Context, token, newPassword string) error {
	hash, err := HashPassword(ctx, newPassword)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var userID string
		err := tx.QueryRowContext(ctx, `
			UPDATE password_resets SET used_at = $2
			WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
			RETURNING user_id`, tokenID(token), now).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBadToken
		}
		if err != nil {
			return fmt.Errorf("auth: resetting password: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET password_hash = $1 WHERE id = $2`, hash, userID); err != nil {
			return fmt.Errorf("auth: resetting password: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
			return fmt.Errorf("auth: signing out other sessions: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM password_resets WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
			return fmt.Errorf("auth: invalidating other reset links: %w", err)
		}
		return nil
	})
}

// ── email verification ───────────────────────────────────────────────────────────────────────────

// CreateEmailVerification issues a confirmation token for somebody's current address.
func (s *Store) CreateEmailVerification(ctx context.Context, tenant, userID string) (
	token, toAddress, orgName string, err error) {

	var verified sql.NullTime
	err = s.db.QueryRowContext(ctx, `
		SELECT u.email, t.name, u.email_verified_at FROM users u JOIN tenants t ON t.id = u.tenant
		WHERE u.id = $1 AND u.tenant = $2`, userID, tenant).Scan(&toAddress, &orgName, &verified)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", ErrNoSuchMember
	}
	if err != nil {
		return "", "", "", fmt.Errorf("auth: email verification: %w", err)
	}
	if verified.Valid {
		return "", "", "", ErrAlreadyVerified
	}
	tok, hash := newToken()
	now := time.Now().UTC()
	// Any earlier unused link is discarded, so "resend" means one live link rather than a growing pile.
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM email_verifications WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		return "", "", "", fmt.Errorf("auth: email verification: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO email_verifications (id, token_hash, tenant, user_id, email, created_at, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		"evr_"+randomID(), hash, tenant, userID, toAddress, now,
		now.Add(EmailVerificationLifetime)); err != nil {
		return "", "", "", fmt.Errorf("auth: email verification: %w", err)
	}
	return tok, toAddress, orgName, nil
}

// VerifyEmail marks an address proven.
//
// 🔴 The address recorded on the token must still be the account's address. Otherwise a link issued for
// an old address confirms whatever the account was changed to afterwards — which is exactly backwards,
// since the point of the record is that somebody proved they receive mail at the address it names.
func (s *Store) VerifyEmail(ctx context.Context, token string) (email string, err error) {
	now := time.Now().UTC()
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		var userID, forEmail string
		err := tx.QueryRowContext(ctx, `
			UPDATE email_verifications SET used_at = $2
			WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
			RETURNING user_id, email`, tokenID(token), now).Scan(&userID, &forEmail)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBadToken
		}
		if err != nil {
			return fmt.Errorf("auth: confirming address: %w", err)
		}
		var current string
		if err := tx.QueryRowContext(ctx,
			`SELECT email FROM users WHERE id = $1`, userID).Scan(&current); err != nil {
			return fmt.Errorf("auth: confirming address: %w", err)
		}
		if !strings.EqualFold(current, forEmail) {
			return ErrBadToken
		}
		email = current
		return s.markEmailVerified(ctx, tx, userID, now)
	})
	return email, err
}

// nullIfEmpty keeps an empty foreign key out of the database, where it would fail the reference rather
// than record "nobody".
func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
