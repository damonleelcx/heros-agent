package tenancy

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// store_pg_password.go is the durable half of the P28 storage, over the two tables migration 0041 creates.
//
// The file-level discipline of `store_pg.go` applies unchanged and matters more here than anywhere:
//
//   - **Read-modify-write is done in SQL.** `RecordPasswordFailure` reads and writes the counters in ONE
//     statement, because two simultaneous wrong guesses that each read `attempts = 8` and each write `9` are
//     two attempts that cost one — which is precisely the property a lockout exists to have.
//   - **🔴 No plaintext, ever.** There is no parameter in this file a password could arrive in. What is
//     written is an argon2id encoding produced elsewhere, and the column CHECKs that it is one.

const passwordColumns = `user_id, encoded, updated_at, failed_attempts, window_started_at, locked_until`

func scanUserPassword(row interface{ Scan(...any) error }) (UserPassword, error) {
	var p UserPassword
	var window, locked sql.NullTime
	if err := row.Scan(&p.UserID, &p.Encoded, &p.UpdatedAt, &p.FailedAttempts, &window, &locked); err != nil {
		return UserPassword{}, err
	}
	p.UpdatedAt = p.UpdatedAt.UTC()
	if window.Valid {
		t := window.Time.UTC()
		p.WindowStartedAt = &t
	}
	if locked.Valid {
		t := locked.Time.UTC()
		p.LockedUntil = &t
	}
	return p, nil
}

// SetPassword writes the encoding and clears the lockout, in one statement.
//
// The clearing is not a separate call the caller could forget: a person who has just reset their password
// must not remain locked out by the failures that made them reset, and "remember to also clear the counters"
// is a rule that holds until the second caller.
func (p *PGStore) SetPassword(userID, encoded string, at time.Time) (UserPassword, error) {
	rec, err := validateUserPassword(UserPassword{UserID: userID, Encoded: encoded, UpdatedAt: at.UTC()})
	if err != nil {
		return UserPassword{}, err
	}
	ctx, cancel := p.ctx()
	defer cancel()
	out, err := scanUserPassword(p.db.QueryRowContext(ctx, `
		INSERT INTO user_password (user_id, encoded, updated_at, failed_attempts, window_started_at, locked_until)
		VALUES ($1, $2, $3, 0, NULL, NULL)
		ON CONFLICT (user_id) DO UPDATE
		   SET encoded = EXCLUDED.encoded,
		       updated_at = EXCLUDED.updated_at,
		       failed_attempts = 0,
		       window_started_at = NULL,
		       locked_until = NULL
		RETURNING `+passwordColumns, rec.UserID, rec.Encoded, rec.UpdatedAt))
	if err != nil {
		if isForeignKeyViolation(err) {
			return UserPassword{}, fmt.Errorf("%w: user %s", ErrNotFound, rec.UserID)
		}
		return UserPassword{}, err
	}
	return out, nil
}

func (p *PGStore) GetPassword(userID string) (UserPassword, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	rec, err := scanUserPassword(p.db.QueryRowContext(ctx,
		`SELECT `+passwordColumns+` FROM user_password WHERE user_id = $1`, userID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserPassword{}, fmt.Errorf("%w: %s", ErrNoPassword, userID)
		}
		return UserPassword{}, err
	}
	return rec, nil
}

// RecordPasswordFailure advances the lockout state in ONE statement.
//
// 🔴 The whole policy is expressed in SQL rather than read-modified-written in Go, and that is the difference
// between a lockout and the appearance of one. Under the Go shape, two concurrent guesses both read
// `attempts = 8`, both compute `9`, and both write it — so an attacker with any concurrency at all gets
// arbitrarily many attempts out of a ten-attempt budget.
//
// The CASE mirrors `applyFailure` exactly: a window that has run out restarts at one, otherwise the count
// advances; reaching the threshold sets the lock.
func (p *PGStore) RecordPasswordFailure(userID string, at time.Time, pol LockoutPolicy) (UserPassword, error) {
	ts := at.UTC()
	ctx, cancel := p.ctx()
	defer cancel()
	out, err := scanUserPassword(p.db.QueryRowContext(ctx, `
		UPDATE user_password
		   SET failed_attempts = CASE
		           WHEN window_started_at IS NULL OR $2 - window_started_at > $3::interval THEN 1
		           ELSE failed_attempts + 1
		       END,
		       window_started_at = CASE
		           WHEN window_started_at IS NULL OR $2 - window_started_at > $3::interval THEN $2
		           ELSE window_started_at
		       END,
		       locked_until = CASE
		           WHEN (CASE
		                    WHEN window_started_at IS NULL OR $2 - window_started_at > $3::interval THEN 1
		                    ELSE failed_attempts + 1
		                 END) >= $4 THEN $2 + $5::interval
		           ELSE locked_until
		       END
		 WHERE user_id = $1
		RETURNING `+passwordColumns,
		userID, ts, intervalString(pol.Window), pol.Threshold, intervalString(pol.LockFor)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserPassword{}, fmt.Errorf("%w: %s", ErrNoPassword, userID)
		}
		return UserPassword{}, err
	}
	return out, nil
}

// intervalString renders a Go duration as a Postgres interval literal.
//
// Milliseconds, not seconds: a policy expressed in sub-second units is what a test uses to observe a lockout
// without waiting fifteen minutes, and truncating it to zero here would make that test pass for the wrong
// reason — it would observe no window at all rather than a short one.
func intervalString(d time.Duration) string {
	return fmt.Sprintf("%d milliseconds", d.Milliseconds())
}

func (p *PGStore) ClearPasswordFailures(userID string) error {
	ctx, cancel := p.ctx()
	defer cancel()
	res, err := p.db.ExecContext(ctx, `
		UPDATE user_password SET failed_attempts = 0, window_started_at = NULL, locked_until = NULL
		 WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrNoPassword, userID)
	}
	return nil
}

func (p *PGStore) FindUserByEmail(issuer, email string) (User, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	// lower() on both sides rather than a case-sensitive match: `NormalizeEmail` lowercases on the way in,
	// and a row written before P28 by a federated seam may not have been normalised. Matching the way the
	// application normalises is what keeps the two from disagreeing about who a person is.
	u, err := scanUser(p.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM platform_user WHERE issuer = $1 AND lower(email) = $2`,
		issuer, NormalizeEmail(email)))
	if err != nil {
		return User{}, notFound(err, "no user at that address")
	}
	return u, nil
}

// MarkEmailVerified stamps the address as proved, keeping the FIRST time.
//
// `COALESCE(email_verified_at, $2)` rather than an unconditional assignment: an audit reading this column is
// asking when the address was proved, not when somebody last clicked a link they had kept.
func (p *PGStore) MarkEmailVerified(userID string, at time.Time) (User, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	u, err := scanUser(p.db.QueryRowContext(ctx, `
		UPDATE platform_user SET email_verified_at = COALESCE(email_verified_at, $2)
		 WHERE user_id = $1
		RETURNING `+userColumns, userID, at.UTC()))
	if err != nil {
		return User{}, notFound(err, "user "+userID)
	}
	return u, nil
}

const identityTokenColumns = `token_hash, user_id, purpose, email, created_at, expires_at, consumed_at`

func scanIdentityToken(row interface{ Scan(...any) error }) (IdentityToken, error) {
	var t IdentityToken
	var consumed sql.NullTime
	if err := row.Scan(&t.TokenHash, &t.UserID, &t.Purpose, &t.Email, &t.CreatedAt, &t.ExpiresAt, &consumed); err != nil {
		return IdentityToken{}, err
	}
	t.CreatedAt, t.ExpiresAt = t.CreatedAt.UTC(), t.ExpiresAt.UTC()
	if consumed.Valid {
		c := consumed.Time.UTC()
		t.ConsumedAt = &c
	}
	return t, nil
}

func (p *PGStore) MintIdentityToken(t IdentityToken) (IdentityToken, error) {
	t, err := validateIdentityToken(t)
	if err != nil {
		return IdentityToken{}, err
	}
	ctx, cancel := p.ctx()
	defer cancel()
	out, err := scanIdentityToken(p.db.QueryRowContext(ctx, `
		INSERT INTO identity_token (token_hash, user_id, purpose, email, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+identityTokenColumns,
		t.TokenHash, t.UserID, string(t.Purpose), t.Email, t.CreatedAt, t.ExpiresAt))
	if err != nil {
		if isForeignKeyViolation(err) {
			return IdentityToken{}, fmt.Errorf("%w: user %s", ErrNotFound, t.UserID)
		}
		if isUniqueViolation(err) {
			return IdentityToken{}, fmt.Errorf("%w: identity token", ErrExists)
		}
		return IdentityToken{}, err
	}
	return out, nil
}

// ConsumeIdentityToken spends a token exactly once.
//
// 🔴 ONE conditional UPDATE. `consumed_at IS NULL AND expires_at > $3` are in the WHERE clause, not in a
// preceding SELECT, so two clicks on the same link race in the database and exactly one wins. The read-then-
// write shape would let both pass the check before either wrote — which for a reset link means one person's
// link setting two passwords, and for the second click a confusing success.
//
// Zero rows means spent, expired, wrong purpose, or unknown, and all four return ErrIdentityToken. The
// distinction helps only somebody enumerating tokens; a real person's next action is the same in all four.
func (p *PGStore) ConsumeIdentityToken(tokenHash string, purpose TokenPurpose, at time.Time) (IdentityToken, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	out, err := scanIdentityToken(p.db.QueryRowContext(ctx, `
		UPDATE identity_token SET consumed_at = $3
		 WHERE token_hash = $1 AND purpose = $2 AND consumed_at IS NULL AND expires_at > $3
		RETURNING `+identityTokenColumns, tokenHash, string(purpose), at.UTC()))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IdentityToken{}, ErrIdentityToken
		}
		return IdentityToken{}, err
	}
	return out, nil
}

// RevokeEverythingFor ends every session and personal credential a person holds, and reports what it left.
//
// One transaction: a reset that revoked the sessions and failed on the credentials would tell somebody who is
// resetting because they were compromised that they are now safe.
func (p *PGStore) RevokeEverythingFor(userID string, at time.Time) (PersonRevocation, error) {
	ts := at.UTC()
	ctx, cancel := p.ctx()
	defer cancel()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return PersonRevocation{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM platform_user WHERE user_id = $1`, userID).Scan(new(int)); err != nil {
		return PersonRevocation{}, notFound(err, "user "+userID)
	}

	var out PersonRevocation
	sessRes, err := tx.ExecContext(ctx,
		`UPDATE console_session SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, ts.UnixMilli())
	if err != nil {
		return PersonRevocation{}, err
	}
	n, _ := sessRes.RowsAffected()
	out.SessionsRevoked = int(n)

	credRes, err := tx.ExecContext(ctx,
		`UPDATE api_credential SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`, userID, ts)
	if err != nil {
		return PersonRevocation{}, err
	}
	n, _ = credRes.RowsAffected()
	out.CredentialsRevoked = int(n)

	// What was NOT revoked: the machine credentials of every organization this person belongs to. Scoped by
	// membership rather than by credential owner, because a machine credential has no owner — that is what
	// makes it a machine credential.
	rows, err := tx.QueryContext(ctx, `
		SELECT `+credentialColumns+`
		  FROM api_credential
		 WHERE user_id IS NULL AND revoked_at IS NULL
		   AND tenant_id IN (SELECT tenant_id FROM membership WHERE user_id = $1)
		 ORDER BY credential_id`, userID)
	if err != nil {
		return PersonRevocation{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		c, serr := scanCredential(rows)
		if serr != nil {
			return PersonRevocation{}, serr
		}
		out.MachineCredentials = append(out.MachineCredentials, c)
	}
	if err := rows.Err(); err != nil {
		return PersonRevocation{}, err
	}
	if err := tx.Commit(); err != nil {
		return PersonRevocation{}, err
	}
	return out, nil
}
