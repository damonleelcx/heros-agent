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

// members.go is who is in an organization and what they may do.
//
// # 🔴 Why the authorization rule lives here and not in the handler
//
// Deciding whether an admin may demote somebody needs the TARGET's current role, which the handler does
// not have. Written in the handler it becomes read-the-role, decide, write — and between the read and
// the write the target can be promoted by somebody else, so the decision is made about a role that is no
// longer theirs. Every check here happens inside the transaction that makes the change, against rows the
// transaction holds.
//
// It also means there is one implementation of "may I act on this person", rather than one per handler
// that touches membership, which is how the second handler ends up subtly more permissive than the first.

// Member is somebody in an organization, as the console lists them.
type Member struct {
	ID    string       `json:"id"`
	Email string       `json:"email"`
	Role  tenancy.Role `json:"role"`
	// EmailVerified says whether this address has been proven reachable. Shown, gates nothing — see
	// migration 0006.
	EmailVerified bool      `json:"email_verified"`
	CreatedAt     time.Time `json:"created_at"`
}

// ListMembers returns everybody in one organization, owners first.
//
// 🔴 Scoped by tenant in the QUERY. There is no unscoped variant to call by mistake.
func (s *Store) ListMembers(ctx context.Context, tenant string) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, email, role, email_verified_at IS NOT NULL, created_at
		FROM users WHERE tenant = $1
		ORDER BY CASE role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'member' THEN 2 ELSE 3 END,
		         lower(email)`, tenant)
	if err != nil {
		return nil, fmt.Errorf("auth: listing members: %w", err)
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var role string
		if err := rows.Scan(&m.ID, &m.Email, &role, &m.EmailVerified, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("auth: listing members: %w", err)
		}
		m.Role = roleOf(role, m.ID)
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetRole changes what somebody may do.
//
// The actor is the whole principal rather than just a role, because the answer depends on who they are
// as well as what they are: an owner may demote themselves, but not if they are the last one.
func (s *Store) SetRole(ctx context.Context, actor tenancy.Principal, targetID string, newRole tenancy.Role) error {
	if !tenancy.ValidRole(string(newRole)) {
		return fmt.Errorf("%w: %q", ErrBadRole, newRole)
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		owners, err := lockOwners(ctx, tx, actor.Tenant)
		if err != nil {
			return err
		}
		current, err := lockMember(ctx, tx, actor.Tenant, targetID)
		if err != nil {
			return err
		}
		// You may act on somebody only if you could have granted the role they already hold — otherwise
		// an admin demotes the owner and then holds the organization.
		if !tenancy.CanManage(actor.Role, current) {
			return fmt.Errorf("%w: only an owner can change the role of an owner", ErrRefused)
		}
		// And you may only grant a role you hold the authority to grant, or an admin makes an owner.
		if !tenancy.CanGrant(actor.Role, newRole) {
			return fmt.Errorf("%w: only an owner can make somebody else an owner", ErrRefused)
		}
		if current == newRole {
			return nil // idempotent: two clicks on the same menu item is not an error
		}
		if current == tenancy.Owner && newRole != tenancy.Owner && lastOwner(owners, targetID) {
			return fmt.Errorf("%w: make somebody else an owner first, then change this one", ErrLastOwner)
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE users SET role = $1 WHERE id = $2 AND tenant = $3`,
			string(newRole), targetID, actor.Tenant)
		if err != nil {
			return fmt.Errorf("auth: setting role: %w", err)
		}
		return nil
	})
}

// RemoveMember takes somebody out of an organization.
//
// 🔴 Their sessions go with them, by the ON DELETE CASCADE on `sessions`. Removing an account while a
// session survives is the failure this whole feature exists to prevent: "their access ended" has to be
// true at the next request, not at the next expiry.
func (s *Store) RemoveMember(ctx context.Context, actor tenancy.Principal, targetID string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		owners, err := lockOwners(ctx, tx, actor.Tenant)
		if err != nil {
			return err
		}
		current, err := lockMember(ctx, tx, actor.Tenant, targetID)
		if err != nil {
			return err
		}
		if !tenancy.CanManage(actor.Role, current) {
			return fmt.Errorf("%w: only an owner can remove an owner", ErrRefused)
		}
		if current == tenancy.Owner && lastOwner(owners, targetID) {
			return fmt.Errorf("%w: make somebody else an owner first, then remove this one", ErrLastOwner)
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1 AND tenant = $2`, targetID, actor.Tenant)
		if err != nil {
			return fmt.Errorf("auth: removing member: %w", err)
		}
		return nil
	})
}

// markEmailVerified records that receipt of a token proved an address. Idempotent, so a second
// confirmation of an already-confirmed address is a no-op rather than an error the person has to read.
func (s *Store) markEmailVerified(ctx context.Context, tx *sql.Tx, userID string, at time.Time) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE users SET email_verified_at = $1 WHERE id = $2 AND email_verified_at IS NULL`, at, userID)
	if err != nil {
		return fmt.Errorf("auth: marking address verified: %w", err)
	}
	return nil
}

// ── the transaction helpers the rules depend on ──────────────────────────────────────────────────

// lockOwners takes a row lock on every owner of a tenant and returns their ids.
//
// # 🔴 Why locking, and why before anything is decided
//
// "There must always be an owner" is an invariant ACROSS rows, and checking it with a plain SELECT is
// wrong under concurrency in a way that leaves no trace: two owners demoting each other at the same
// moment each see the other still in place, each allow it, and the organization ends with nobody who can
// administer it — recoverable only by someone with database access.
//
// Locking the owner set first serialises every operation that could shrink it. The second transaction
// re-evaluates after the first commits, sees one owner left, and refuses.
func lockOwners(ctx context.Context, tx *sql.Tx, tenant string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM users WHERE tenant = $1 AND role = 'owner' ORDER BY id FOR UPDATE`, tenant)
	if err != nil {
		return nil, fmt.Errorf("auth: locking owners: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("auth: locking owners: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// lockMember reads and locks one member of one tenant.
//
// 🔴 The tenant is in the WHERE clause. Somebody else's user id must be indistinguishable from a
// nonexistent one — otherwise "remove member" doubles as a probe for which account ids are real across
// every customer on the deployment.
func lockMember(ctx context.Context, tx *sql.Tx, tenant, userID string) (tenancy.Role, error) {
	var role string
	err := tx.QueryRowContext(ctx,
		`SELECT role FROM users WHERE id = $1 AND tenant = $2 FOR UPDATE`, userID, tenant).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoSuchMember
	}
	if err != nil {
		return "", fmt.Errorf("auth: reading member: %w", err)
	}
	return roleOf(role, userID), nil
}

// lastOwner reports whether the given id is the only owner left.
func lastOwner(owners []string, id string) bool {
	return len(owners) == 1 && owners[0] == id
}

// inTx runs fn in a transaction, rolling back on any error.
//
// 🔴 Rollback is deferred, not written at each return. Every early return in the functions above would
// otherwise need its own rollback, and the one that is forgotten leaves a transaction holding the owner
// lock until the connection is recycled — which presents as the whole organization's member management
// hanging, with nothing in the logs.
func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("auth: commit: %w", err)
	}
	return nil
}

// normalizeEmail is the one place an address is canonicalised, so a lookup and an insert cannot disagree
// about what "the same address" means.
//
// 🚫 Only trimmed and compared case-insensitively — NOT stripped of dots or plus-suffixes. Those rules
// are one provider's, applying them to every provider merges addresses that belong to different people.
func normalizeEmail(s string) string { return strings.TrimSpace(s) }

// Member looks up one person in one organization.
//
// 🔴 Tenant-scoped like everything else here: a user id from another organization returns ErrNoSuchMember
// rather than a row, so an id cannot be used to confirm that an account exists elsewhere.
func (s *Store) Member(ctx context.Context, tenant, userID string) (Member, error) {
	var m Member
	var role string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, email, role, email_verified_at IS NOT NULL, created_at
		FROM users WHERE id = $1 AND tenant = $2`, userID, tenant).
		Scan(&m.ID, &m.Email, &role, &m.EmailVerified, &m.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Member{}, ErrNoSuchMember
	}
	if err != nil {
		return Member{}, fmt.Errorf("auth: reading member: %w", err)
	}
	m.Role = roleOf(role, m.ID)
	return m, nil
}

// OrgName returns an organization's display name, for mail that has to say which one.
func (s *Store) OrgName(ctx context.Context, tenant string) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT name FROM tenants WHERE id = $1`, tenant).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("auth: no such organization")
	}
	return name, err
}
