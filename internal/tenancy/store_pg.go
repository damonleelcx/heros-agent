package tenancy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// store_pg.go is the durable identity store, over the six tables migration 0038 creates.
//
// # Read-modify-write is done in SQL, not in Go
//
// `MemStore`'s mutators read the map, edit the struct and write it back under a mutex. The same shape
// here would be a lost update: two admins changing different fields of one membership, each writing back
// a whole row built from what they read, and the second silently reverting the first. So every mutator
// is a single statement naming only the columns it owns, with `RETURNING` to hand back the row as it now
// stands. The database serialises them; nothing is read back and rewritten. `account.PGStore` carries
// the same reasoning and it applies unchanged.
//
// # Where a transaction is genuinely required
//
// One place: `RemoveMember`. Membership, sessions and credentials must move together or somebody is off
// the member list while still holding a working key. Everything else is a single statement, which is its
// own transaction and needs no help.
//
// # 🔴 No plaintext, ever
//
// `hash` is the only credential column, and there is no method here that accepts a plaintext secret —
// so there is no parameter a plaintext could arrive in and be logged from. Verification is a lookup by
// hash, which means no code path in this file compares secret bytes.
type PGStore struct{ db *sql.DB }

// NewPGStore returns a durable identity Store over an open Postgres handle.
func NewPGStore(db *sql.DB) (*PGStore, error) {
	if db == nil {
		return nil, errors.New("tenancy: nil database")
	}
	return &PGStore{db: db}, nil
}

func (p *PGStore) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// notFound turns sql.ErrNoRows into this package's own error so callers branch on one taxonomy.
func notFound(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrNotFound, what)
	}
	return err
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Organizations
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

const tenantColumns = `tenant_id, name, status, created_at`

func scanTenant(row interface{ Scan(...any) error }) (Tenant, error) {
	var t Tenant
	if err := row.Scan(&t.TenantID, &t.Name, &t.Status, &t.CreatedAt); err != nil {
		return Tenant{}, err
	}
	t.CreatedAt = t.CreatedAt.UTC()
	return t, nil
}

func (p *PGStore) CreateTenant(t Tenant) (Tenant, error) {
	t, err := validateTenant(t)
	if err != nil {
		return Tenant{}, err
	}
	ctx, cancel := p.ctx()
	defer cancel()
	row := p.db.QueryRowContext(ctx, `
		INSERT INTO tenant (tenant_id, name, status, created_at)
		VALUES ($1, $2, $3, $4)
		RETURNING `+tenantColumns, t.TenantID, t.Name, string(t.Status), t.CreatedAt)
	out, err := scanTenant(row)
	if err != nil {
		if isUniqueViolation(err) {
			return Tenant{}, fmt.Errorf("%w: organization %s", ErrExists, t.TenantID)
		}
		return Tenant{}, err
	}
	return out, nil
}

func (p *PGStore) GetTenant(tenantID string) (Tenant, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	t, err := scanTenant(p.db.QueryRowContext(ctx,
		`SELECT `+tenantColumns+` FROM tenant WHERE tenant_id = $1`, tenantID))
	if err != nil {
		return Tenant{}, notFound(err, "organization "+tenantID)
	}
	return t, nil
}

func (p *PGStore) ListTenants() ([]Tenant, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	rows, err := p.db.QueryContext(ctx, `SELECT `+tenantColumns+` FROM tenant ORDER BY tenant_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Tenant{}
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *PGStore) SetTenantStatus(tenantID string, st Status, _ time.Time) (Tenant, error) {
	if st != StatusActive && st != StatusSuspended {
		return Tenant{}, fmt.Errorf("tenancy: unknown tenant status %q", st)
	}
	ctx, cancel := p.ctx()
	defer cancel()
	t, err := scanTenant(p.db.QueryRowContext(ctx,
		`UPDATE tenant SET status = $2 WHERE tenant_id = $1 RETURNING `+tenantColumns, tenantID, string(st)))
	if err != nil {
		return Tenant{}, notFound(err, "organization "+tenantID)
	}
	return t, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// People
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

const userColumns = `user_id, issuer, subject, email, created_at`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	if err := row.Scan(&u.UserID, &u.Issuer, &u.Subject, &u.Email, &u.CreatedAt); err != nil {
		return User{}, err
	}
	u.CreatedAt = u.CreatedAt.UTC()
	return u, nil
}

// UpsertUser is ONE statement, and the conflict target is the federated pair.
//
// 🔴 `DO UPDATE SET email = …` and nothing else. The `user_id` is deliberately absent from the update:
// it is the key every other table references, and rewriting it on an identity-provider migration would
// mean rewriting every membership, credential and session that names this person.
func (p *PGStore) UpsertUser(u User) (User, error) {
	u, err := validateUser(u)
	if err != nil {
		return User{}, err
	}
	if u.UserID == "" {
		u.UserID = newID("usr")
	}
	ctx, cancel := p.ctx()
	defer cancel()
	out, err := scanUser(p.db.QueryRowContext(ctx, `
		INSERT INTO platform_user (user_id, issuer, subject, email, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT ON CONSTRAINT platform_user_federated_identity
		DO UPDATE SET email = EXCLUDED.email
		RETURNING `+userColumns, u.UserID, u.Issuer, u.Subject, u.Email, u.CreatedAt))
	if err != nil {
		return User{}, err
	}
	return out, nil
}

func (p *PGStore) GetUser(userID string) (User, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	u, err := scanUser(p.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM platform_user WHERE user_id = $1`, userID))
	if err != nil {
		return User{}, notFound(err, "user "+userID)
	}
	return u, nil
}

func (p *PGStore) FindUser(issuer, subject string) (User, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	u, err := scanUser(p.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM platform_user WHERE issuer = $1 AND subject = $2`, issuer, subject))
	if err != nil {
		return User{}, notFound(err, "no user for that identity")
	}
	return u, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Memberships
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

const membershipColumns = `user_id, tenant_id, role, status, invited_by, joined_at, removed_at`

func scanMembership(row interface{ Scan(...any) error }) (Membership, error) {
	var m Membership
	var removed sql.NullTime
	if err := row.Scan(&m.UserID, &m.TenantID, &m.Role, &m.Status, &m.InvitedBy, &m.JoinedAt, &removed); err != nil {
		return Membership{}, err
	}
	m.JoinedAt = m.JoinedAt.UTC()
	if removed.Valid {
		t := removed.Time.UTC()
		m.RemovedAt = &t
	}
	return m, nil
}

func (p *PGStore) PutMembership(m Membership) (Membership, error) {
	m, err := validateMembership(m)
	if err != nil {
		return Membership{}, err
	}
	ctx, cancel := p.ctx()
	defer cancel()
	out, err := scanMembership(p.db.QueryRowContext(ctx, `
		INSERT INTO membership (user_id, tenant_id, role, status, invited_by, joined_at, removed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, tenant_id) DO UPDATE
		   SET role = EXCLUDED.role, status = EXCLUDED.status,
		       invited_by = EXCLUDED.invited_by, joined_at = EXCLUDED.joined_at,
		       removed_at = EXCLUDED.removed_at
		RETURNING `+membershipColumns,
		m.UserID, m.TenantID, string(m.Role), string(m.Status), m.InvitedBy, m.JoinedAt, m.RemovedAt))
	if err != nil {
		if isForeignKeyViolation(err) {
			return Membership{}, fmt.Errorf("%w: the organization or the user does not exist", ErrNotFound)
		}
		return Membership{}, err
	}
	return out, nil
}

func (p *PGStore) GetMembership(userID, tenantID string) (Membership, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	m, err := scanMembership(p.db.QueryRowContext(ctx,
		`SELECT `+membershipColumns+` FROM membership WHERE user_id = $1 AND tenant_id = $2`, userID, tenantID))
	if err != nil {
		return Membership{}, notFound(err, "membership")
	}
	return m, nil
}

func (p *PGStore) listMemberships(query string, args ...any) ([]Membership, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Membership{}
	for rows.Next() {
		m, err := scanMembership(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (p *PGStore) ListMembers(tenantID string) ([]Membership, error) {
	return p.listMemberships(
		`SELECT `+membershipColumns+` FROM membership WHERE tenant_id = $1 ORDER BY user_id`, tenantID)
}

func (p *PGStore) ListMembershipsFor(userID string) ([]Membership, error) {
	return p.listMemberships(
		`SELECT `+membershipColumns+` FROM membership WHERE user_id = $1 ORDER BY tenant_id`, userID)
}

// SetRole refuses to demote the last owner, and does it in ONE statement.
//
// The tempting shape — count owners in Go, then update — is a check-then-act race: two admins demoting
// the two remaining owners concurrently each count one other owner and both succeed, leaving an
// organization nobody can administer. The `WHERE NOT (…)` predicate makes the database evaluate the
// count and the update together.
func (p *PGStore) SetRole(userID, tenantID string, role Role) (Membership, error) {
	if !KnownRole(role) {
		return Membership{}, fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	ctx, cancel := p.ctx()
	defer cancel()
	m, err := scanMembership(p.db.QueryRowContext(ctx, `
		UPDATE membership SET role = $3
		 WHERE user_id = $1 AND tenant_id = $2
		   AND NOT ($3 <> 'owner' AND role = 'owner' AND status = 'active'
		            AND (SELECT count(*) FROM membership o
		                  WHERE o.tenant_id = $2 AND o.user_id <> $1
		                    AND o.role = 'owner' AND o.status = 'active') = 0)
		RETURNING `+membershipColumns, userID, tenantID, string(role)))
	if errors.Is(err, sql.ErrNoRows) {
		// Either there is no such membership, or the last-owner predicate refused. Distinguish them,
		// because "not found" and "you would leave nobody in charge" need different words on screen.
		if _, gerr := p.GetMembership(userID, tenantID); gerr != nil {
			return Membership{}, gerr
		}
		return Membership{}, ErrLastOwner
	}
	if err != nil {
		return Membership{}, err
	}
	return m, nil
}

func (p *PGStore) PreviewRemoval(userID, tenantID string) (RemovalPreview, error) {
	m, err := p.GetMembership(userID, tenantID)
	if err != nil {
		return RemovalPreview{}, err
	}
	u, err := p.GetUser(userID)
	if err != nil {
		return RemovalPreview{}, err
	}
	creds, err := p.ListCredentials(tenantID)
	if err != nil {
		return RemovalPreview{}, err
	}
	p2 := RemovalPreview{
		UserID:              userID,
		Email:               u.Email,
		PersonalCredentials: []Credential{},
		MachineCredentials:  []Credential{},
	}
	for _, c := range creds {
		if c.Revoked() {
			continue
		}
		switch {
		case c.UserID == userID:
			p2.PersonalCredentials = append(p2.PersonalCredentials, c)
		case c.UserID == "":
			p2.MachineCredentials = append(p2.MachineCredentials, c)
		}
	}

	ctx, cancel := p.ctx()
	defer cancel()
	if err := p.db.QueryRowContext(ctx, `
		SELECT count(*) FROM console_session
		 WHERE user_id = $1 AND tenant_id = $2 AND revoked_at IS NULL`, userID, tenantID).Scan(&p2.Sessions); err != nil {
		return RemovalPreview{}, err
	}
	var otherOwners int
	if err := p.db.QueryRowContext(ctx, `
		SELECT count(*) FROM membership
		 WHERE tenant_id = $2 AND user_id <> $1 AND role = 'owner' AND status = 'active'`,
		userID, tenantID).Scan(&otherOwners); err != nil {
		return RemovalPreview{}, err
	}
	p2.LastOwner = m.Active() && m.Role == RoleOwner && otherOwners == 0
	return p2, nil
}

// RemoveMember is the one place this store opens a transaction. See the file header.
func (p *PGStore) RemoveMember(userID, tenantID string, at time.Time) (RemovalResult, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return RemovalResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	ts := at.UTC()
	m, err := scanMembership(tx.QueryRowContext(ctx, `
		UPDATE membership SET status = 'removed', removed_at = $3
		 WHERE user_id = $1 AND tenant_id = $2
		   AND NOT (role = 'owner' AND status = 'active'
		            AND (SELECT count(*) FROM membership o
		                  WHERE o.tenant_id = $2 AND o.user_id <> $1
		                    AND o.role = 'owner' AND o.status = 'active') = 0)
		RETURNING `+membershipColumns, userID, tenantID, ts))
	if errors.Is(err, sql.ErrNoRows) {
		if _, gerr := p.GetMembership(userID, tenantID); gerr != nil {
			return RemovalResult{}, gerr
		}
		return RemovalResult{}, ErrLastOwner
	}
	if err != nil {
		return RemovalResult{}, err
	}

	res := RemovalResult{Membership: m}
	credRes, err := tx.ExecContext(ctx, `
		UPDATE api_credential SET revoked_at = $3
		 WHERE tenant_id = $2 AND user_id = $1 AND revoked_at IS NULL`, userID, tenantID, ts)
	if err != nil {
		return RemovalResult{}, err
	}
	n, _ := credRes.RowsAffected()
	res.CredentialsRevoked = int(n)

	sessRes, err := tx.ExecContext(ctx, `
		UPDATE console_session SET revoked_at = $3
		 WHERE tenant_id = $2 AND user_id = $1 AND revoked_at IS NULL`, userID, tenantID, ts.UnixMilli())
	if err != nil {
		return RemovalResult{}, err
	}
	n, _ = sessRes.RowsAffected()
	res.SessionsRevoked = int(n)

	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM api_credential
		 WHERE tenant_id = $1 AND user_id IS NULL AND revoked_at IS NULL`, tenantID).Scan(&res.MachineCredsUnknown); err != nil {
		return RemovalResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RemovalResult{}, err
	}
	return res, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Invitations
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

const invitationColumns = `invitation_id, tenant_id, email, role, invited_by, created_at, expires_at, accepted_at, revoked_at`

func scanInvitation(row interface{ Scan(...any) error }) (Invitation, error) {
	var i Invitation
	var accepted, revoked sql.NullTime
	if err := row.Scan(&i.InvitationID, &i.TenantID, &i.Email, &i.Role, &i.InvitedBy,
		&i.CreatedAt, &i.ExpiresAt, &accepted, &revoked); err != nil {
		return Invitation{}, err
	}
	i.CreatedAt, i.ExpiresAt = i.CreatedAt.UTC(), i.ExpiresAt.UTC()
	if accepted.Valid {
		t := accepted.Time.UTC()
		i.AcceptedAt = &t
	}
	if revoked.Valid {
		t := revoked.Time.UTC()
		i.RevokedAt = &t
	}
	return i, nil
}

func (p *PGStore) CreateInvitation(i Invitation) (Invitation, error) {
	i, err := validateInvitation(i)
	if err != nil {
		return Invitation{}, err
	}
	ctx, cancel := p.ctx()
	defer cancel()
	out, err := scanInvitation(p.db.QueryRowContext(ctx, `
		INSERT INTO invitation (invitation_id, tenant_id, email, role, invited_by, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+invitationColumns,
		i.InvitationID, i.TenantID, i.Email, string(i.Role), i.InvitedBy, i.CreatedAt, i.ExpiresAt))
	if err != nil {
		if isUniqueViolation(err) {
			return Invitation{}, fmt.Errorf("%w: invitation", ErrExists)
		}
		if isForeignKeyViolation(err) {
			return Invitation{}, fmt.Errorf("%w: organization %s", ErrNotFound, i.TenantID)
		}
		return Invitation{}, err
	}
	return out, nil
}

func (p *PGStore) GetInvitation(id string) (Invitation, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	i, err := scanInvitation(p.db.QueryRowContext(ctx,
		`SELECT `+invitationColumns+` FROM invitation WHERE invitation_id = $1`, id))
	if err != nil {
		return Invitation{}, notFound(err, "invitation")
	}
	return i, nil
}

func (p *PGStore) ListInvitations(tenantID string) ([]Invitation, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+invitationColumns+` FROM invitation WHERE tenant_id = $1 ORDER BY invitation_id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Invitation{}
	for rows.Next() {
		i, err := scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// AcceptInvitation stamps `accepted_at` only if the invitation is still pending — in the WHERE clause,
// so two concurrent acceptances cannot both succeed.
func (p *PGStore) AcceptInvitation(id string, at time.Time) (Invitation, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	i, err := scanInvitation(p.db.QueryRowContext(ctx, `
		UPDATE invitation SET accepted_at = $2
		 WHERE invitation_id = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $2
		RETURNING `+invitationColumns, id, at.UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		if _, gerr := p.GetInvitation(id); gerr != nil {
			return Invitation{}, gerr
		}
		return Invitation{}, ErrInviteExpired
	}
	if err != nil {
		return Invitation{}, err
	}
	return i, nil
}

func (p *PGStore) RevokeInvitation(id string, at time.Time) (Invitation, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	i, err := scanInvitation(p.db.QueryRowContext(ctx, `
		UPDATE invitation SET revoked_at = COALESCE(revoked_at, $2)
		 WHERE invitation_id = $1
		RETURNING `+invitationColumns, id, at.UTC()))
	if err != nil {
		return Invitation{}, notFound(err, "invitation")
	}
	return i, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Credentials
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

const credentialColumns = `credential_id, tenant_id, user_id, label, role, hash, created_at, revoked_at`

func scanCredential(row interface{ Scan(...any) error }) (Credential, error) {
	var c Credential
	var user sql.NullString
	var revoked sql.NullTime
	if err := row.Scan(&c.CredentialID, &c.TenantID, &user, &c.Label, &c.Role, &c.Hash, &c.CreatedAt, &revoked); err != nil {
		return Credential{}, err
	}
	// NULL means MACHINE credential. It becomes the empty string in Go, which is the same distinction —
	// `Credential.Personal()` is the single place that reads it, so the two spellings cannot diverge.
	c.UserID = user.String
	c.CreatedAt = c.CreatedAt.UTC()
	if revoked.Valid {
		t := revoked.Time.UTC()
		c.RevokedAt = &t
	}
	return c, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (p *PGStore) CreateCredential(c Credential) (Credential, error) {
	c, err := validateCredential(c)
	if err != nil {
		return Credential{}, err
	}
	ctx, cancel := p.ctx()
	defer cancel()
	out, err := scanCredential(p.db.QueryRowContext(ctx, `
		INSERT INTO api_credential (credential_id, tenant_id, user_id, label, role, hash, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+credentialColumns,
		c.CredentialID, c.TenantID, nullIfEmpty(c.UserID), c.Label, string(c.Role), c.Hash, c.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return Credential{}, fmt.Errorf("%w: credential", ErrExists)
		}
		if isForeignKeyViolation(err) {
			return Credential{}, fmt.Errorf("%w: the organization or the user does not exist", ErrNotFound)
		}
		return Credential{}, err
	}
	return out, nil
}

func (p *PGStore) ResolveCredential(hash string) (Credential, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	c, err := scanCredential(p.db.QueryRowContext(ctx,
		`SELECT `+credentialColumns+` FROM api_credential WHERE hash = $1`, hash))
	if err != nil {
		return Credential{}, notFound(err, "credential")
	}
	return c, nil
}

func (p *PGStore) ListCredentials(tenantID string) ([]Credential, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+credentialColumns+` FROM api_credential WHERE tenant_id = $1 ORDER BY credential_id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Credential{}
	for rows.Next() {
		c, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (p *PGStore) RevokeCredential(credentialID string, at time.Time) (Credential, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	c, err := scanCredential(p.db.QueryRowContext(ctx, `
		UPDATE api_credential SET revoked_at = COALESCE(revoked_at, $2)
		 WHERE credential_id = $1
		RETURNING `+credentialColumns, credentialID, at.UTC()))
	if err != nil {
		return Credential{}, notFound(err, "credential")
	}
	return c, nil
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Console sessions
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

const sessionColumns = `token_hash, session_id, tenant_id, user_id, issued_at, expires_at, revoked_at, purpose`

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var s Session
	var user sql.NullString
	var revoked sql.NullInt64
	if err := row.Scan(&s.TokenHash, &s.SessionID, &s.TenantID, &user, &s.IssuedAt, &s.ExpiresAt, &revoked, &s.Purpose); err != nil {
		return Session{}, err
	}
	s.UserID = user.String
	s.RevokedAt = revoked.Int64
	return s, nil
}

func (p *PGStore) CreateSession(s Session) (Session, error) {
	if s.TokenHash == "" || s.SessionID == "" || s.TenantID == "" {
		return Session{}, fmt.Errorf("%w: a session needs a token hash, an id and an organization", ErrEmptyField)
	}
	if s.Purpose == "" {
		s.Purpose = PurposeUpstream
	}
	if !KnownPurpose(s.Purpose) {
		return Session{}, fmt.Errorf("tenancy: unknown session purpose %q", s.Purpose)
	}
	ctx, cancel := p.ctx()
	defer cancel()
	out, err := scanSession(p.db.QueryRowContext(ctx, `
		INSERT INTO console_session (token_hash, session_id, tenant_id, user_id, issued_at, expires_at, purpose)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+sessionColumns,
		s.TokenHash, s.SessionID, s.TenantID, nullIfEmpty(s.UserID), s.IssuedAt, s.ExpiresAt, string(s.Purpose)))
	if err != nil {
		if isForeignKeyViolation(err) {
			return Session{}, fmt.Errorf("%w: the organization or the user does not exist", ErrNotFound)
		}
		return Session{}, err
	}
	return out, nil
}

func (p *PGStore) ResolveSession(tokenHash string) (Session, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	s, err := scanSession(p.db.QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM console_session WHERE token_hash = $1`, tokenHash))
	if err != nil {
		return Session{}, notFound(err, "session")
	}
	return s, nil
}

func (p *PGStore) RevokeSession(tokenHash string, atMillis int64) error {
	ctx, cancel := p.ctx()
	defer cancel()
	res, err := p.db.ExecContext(ctx, `
		UPDATE console_session SET revoked_at = COALESCE(revoked_at, $2) WHERE token_hash = $1`,
		tokenHash, atMillis)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: session", ErrNotFound)
	}
	return nil
}

func (p *PGStore) ListSessionsFor(userID, tenantID string) ([]Session, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+sessionColumns+` FROM console_session WHERE user_id = $1 AND tenant_id = $2 ORDER BY session_id`,
		userID, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Session{}
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────────────────────────────────────
// Device authorization (task 13.1)
// ─────────────────────────────────────────────────────────────────────────────────────────────────────

const deviceColumns = `device_id, user_code_hash, device_code_hash, label, status, created_at,
	expires_at, coalesce(approved_by,''), coalesce(tenant_id,''), decided_at,
	coalesce(credential_id,''), collected_at`

func scanDevice(sc interface{ Scan(...any) error }) (DeviceAuthorization, error) {
	var d DeviceAuthorization
	var status string
	var decided, collected sql.NullTime
	if err := sc.Scan(&d.DeviceID, &d.UserCodeHash, &d.DeviceCodeHash, &d.Label, &status,
		&d.CreatedAt, &d.ExpiresAt, &d.ApprovedBy, &d.TenantID, &decided,
		&d.CredentialID, &collected); err != nil {
		return DeviceAuthorization{}, err
	}
	d.Status = DeviceAuthStatus(status)
	if decided.Valid {
		t := decided.Time.UTC()
		d.DecidedAt = &t
	}
	if collected.Valid {
		t := collected.Time.UTC()
		d.CollectedAt = &t
	}
	return d, nil
}

func (p *PGStore) CreateDeviceAuthorization(d DeviceAuthorization) (DeviceAuthorization, error) {
	d, err := validateDeviceAuthorization(d)
	if err != nil {
		return DeviceAuthorization{}, err
	}
	ctx, cancel := p.ctx()
	defer cancel()
	if _, err := p.db.ExecContext(ctx,
		`INSERT INTO device_authorization (device_id, user_code_hash, device_code_hash, label, status, created_at, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		d.DeviceID, d.UserCodeHash, d.DeviceCodeHash, d.Label, string(d.Status), d.CreatedAt.UTC(), d.ExpiresAt.UTC(),
	); err != nil {
		if isUniqueViolation(err) {
			// Either code already exists on a live row. Both are unique by index, and a collision is an
			// error rather than an overwrite: two authorizations sharing a user code would make "which one
			// did they approve" unanswerable. `MemStore` refuses the same write for the same reason.
			return DeviceAuthorization{}, fmt.Errorf("%w: device authorization", ErrExists)
		}
		return DeviceAuthorization{}, fmt.Errorf("tenancy: create device authorization: %w", err)
	}
	return d, nil
}

func (p *PGStore) FindDeviceByUserCode(userCodeHash string) (DeviceAuthorization, error) {
	ctx, cancel := p.ctx()
	defer cancel()
	d, err := scanDevice(p.db.QueryRowContext(ctx,
		`SELECT `+deviceColumns+` FROM device_authorization WHERE user_code_hash = $1`, userCodeHash))
	if errors.Is(err, sql.ErrNoRows) {
		return DeviceAuthorization{}, fmt.Errorf("%w: device authorization", ErrNotFound)
	}
	if err != nil {
		return DeviceAuthorization{}, fmt.Errorf("tenancy: find device authorization: %w", err)
	}
	return d, nil
}

// DecideDevice records the approval or denial.
//
// 🔴 ONE conditional UPDATE, never read-then-write. `WHERE status = 'pending' AND expires_at > $n` is what
// makes the flow single-use under the replica count the console runs: two tabs, a double-clicked Approve
// or a retried request all reach here, and exactly one of them updates a row. A check-then-act in Go would
// let two of them issue two credentials, and the second one would be a working key nobody remembers
// approving.
func (p *PGStore) DecideDevice(deviceID string, status DeviceAuthStatus, approvedBy, tenantID, credentialID string, at time.Time) (DeviceAuthorization, error) {
	if status != DeviceApproved && status != DeviceDenied {
		return DeviceAuthorization{}, fmt.Errorf("%w: a decision is approved or denied", ErrEmptyField)
	}
	ctx, cancel := p.ctx()
	defer cancel()
	res, err := p.db.ExecContext(ctx,
		`UPDATE device_authorization
		    SET status = $2, approved_by = nullif($3,''), tenant_id = nullif($4,''),
		        credential_id = nullif($5,''), decided_at = $6
		  WHERE device_id = $1 AND status = 'pending' AND expires_at > $6`,
		deviceID, string(status), approvedBy, tenantID, credentialID, at.UTC())
	if err != nil {
		if isForeignKeyViolation(err) {
			// The approver or the organization named in the decision does not exist. Reported as a refusal
			// rather than as a 500: it is reachable from an approval racing a member removal.
			return DeviceAuthorization{}, fmt.Errorf("%w: approver or organization", ErrNotFound)
		}
		return DeviceAuthorization{}, fmt.Errorf("tenancy: decide device authorization: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return DeviceAuthorization{}, fmt.Errorf("tenancy: decide device authorization: %w", err)
	}
	if n == 0 {
		// Already decided, expired, or never existed — one answer, deliberately (task 13.7).
		return DeviceAuthorization{}, ErrDeviceCode
	}
	return p.getDevice(ctx, deviceID)
}

// CollectDevice exchanges the CLI's device code for its credential, exactly once.
//
// A PENDING authorization is returned WITHOUT being collected and without an error: that is the one
// non-terminal answer, and it is what the CLI keeps polling on. Everything else — denied, expired, already
// collected, unknown — is `ErrDeviceCode`, so a caller cannot tell them apart.
func (p *PGStore) CollectDevice(deviceCodeHash string, at time.Time) (DeviceAuthorization, error) {
	ctx, cancel := p.ctx()
	defer cancel()

	// The collecting UPDATE first, so the common case is one round trip and the stamp is atomic.
	res, err := p.db.ExecContext(ctx,
		`UPDATE device_authorization
		    SET collected_at = $2
		  WHERE device_code_hash = $1 AND status = 'approved' AND collected_at IS NULL
		    AND credential_id IS NOT NULL AND expires_at > $2`,
		deviceCodeHash, at.UTC())
	if err != nil {
		return DeviceAuthorization{}, fmt.Errorf("tenancy: collect device authorization: %w", err)
	}
	if n, aerr := res.RowsAffected(); aerr == nil && n == 1 {
		d, gerr := p.getDeviceByCode(ctx, deviceCodeHash)
		if gerr != nil {
			return DeviceAuthorization{}, gerr
		}
		return d, nil
	}

	// Nothing collected. Distinguish ONLY "still pending" from everything else.
	d, gerr := p.getDeviceByCode(ctx, deviceCodeHash)
	if gerr != nil {
		return DeviceAuthorization{}, ErrDeviceCode
	}
	if d.Pending(at) {
		return d, nil
	}
	return DeviceAuthorization{}, ErrDeviceCode
}

func (p *PGStore) getDevice(ctx context.Context, deviceID string) (DeviceAuthorization, error) {
	d, err := scanDevice(p.db.QueryRowContext(ctx,
		`SELECT `+deviceColumns+` FROM device_authorization WHERE device_id = $1`, deviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return DeviceAuthorization{}, fmt.Errorf("%w: device authorization", ErrNotFound)
	}
	if err != nil {
		return DeviceAuthorization{}, fmt.Errorf("tenancy: read device authorization: %w", err)
	}
	return d, nil
}

func (p *PGStore) getDeviceByCode(ctx context.Context, deviceCodeHash string) (DeviceAuthorization, error) {
	d, err := scanDevice(p.db.QueryRowContext(ctx,
		`SELECT `+deviceColumns+` FROM device_authorization WHERE device_code_hash = $1`, deviceCodeHash))
	if errors.Is(err, sql.ErrNoRows) {
		return DeviceAuthorization{}, fmt.Errorf("%w: device authorization", ErrNotFound)
	}
	if err != nil {
		return DeviceAuthorization{}, fmt.Errorf("tenancy: read device authorization: %w", err)
	}
	return d, nil
}
