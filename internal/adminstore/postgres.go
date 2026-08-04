// Package adminstore is the Postgres backing for the operator directory: admin principals, their role
// grants, and their enrolled second factors.
//
// # Why this package exists
//
// Migration 0014 created `admin_principal`, `admin_role_grant` and `admin_session` when P8 landed, and
// until now NO Go code read or wrote any of them. The stores in `adminidentity` and `adminrbac` were
// process-local maps, so the tables were a schema nobody used and the directory was state nobody kept.
// On a deployment where the operator console is the only way to reach cross-tenant capability, that is
// a restart away from an empty directory — and because enrolling a factor requires a session and
// issuing a session requires a factor, an empty directory is a permanent lockout rather than a
// temporary one.
//
// # Why it is its own package rather than methods in adminidentity
//
// `adminidentity` and `adminrbac` deliberately import no database driver: they are the identity and
// authorization LOGIC, and every test in them runs without a database. Putting SQL in them would make
// the seam they define a seam they also cross. This package implements the three writer ports those
// packages publish (`PrincipalWriter`, `FactorWriter`, `GrantWriter`) and reads the rows back at boot,
// and it is the only file in the tree that names those tables.
//
// # What is deliberately NOT here
//
//   - Sessions. `admin_session` exists, and sessions remain in memory on purpose: a restart signing
//     every operator out is correct behaviour on this surface (there is no grace period on a revoked
//     session either), and a durable session table is a place a session can outlive the process that
//     was supposed to be able to kill it instantly.
//   - Audit. `audit_entry` is hash-chained and append-only with a write-once trigger; giving it a
//     durable writer is a larger piece of work than this one and is NOT done here. The audit log
//     therefore still starts empty on every boot — stated in the deploy notes rather than implied.
//   - Any secret. `admin_factor.secret_name` is the reserved LOGICAL NAME a TOTP seed is held under in
//     the secrets manager. No column in this package holds key material, and none should ever be added:
//     these tables are dumped by pg_dump and kept in backup buckets.
package adminstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminrbac"
)

// writeTimeout bounds every statement this package issues.
//
// The ports it implements take no context — deliberately, see `adminidentity/durable.go` — so the
// timeout is owned here. It is short because every one of these writes is on an interactive path: an
// operator signing in, enrolling, or being offboarded. A directory write that hangs for a minute is a
// sign-in page that hangs for a minute.
const writeTimeout = 5 * time.Second

// Store is the Postgres backing for the operator directory.
type Store struct{ db *sql.DB }

// New wraps a live platform pool. A nil pool is an error rather than a silently in-memory directory:
// "the operator console came up with no durable directory" must be loud at construction, not discovered
// at the first restart.
func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("adminstore: a platform database is required — the operator directory is not kept in memory on a deployment that federates")
	}
	return &Store{db: db}, nil
}

func (s *Store) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), writeTimeout)
}

// ctx bounds the kill switch's statements on the same budget, for the same reason: every one of them is
// on an interactive path, and the fleet brake is the last surface that may hang.
func (k *KillSwitch) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), writeTimeout)
}

// ── Principals ──────────────────────────────────────────────────────────────────────────────────

// PutPrincipal inserts or replaces one admin principal.
//
// Upsert on `admin_id` rather than insert-or-fail, because `PrincipalStore.Put` is documented as
// insert-or-replace and a backing that refused the replace would make the two disagree about what the
// directory holds — with memory winning, which is the direction this whole file exists to prevent.
func (s *Store) PutPrincipal(p adminidentity.Principal) error {
	ctx, cancel := s.ctx()
	defer cancel()
	created := p.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_principal (admin_id, sso_subject, mfa_enrolled, status, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (admin_id) DO UPDATE SET
			sso_subject  = EXCLUDED.sso_subject,
			mfa_enrolled = EXCLUDED.mfa_enrolled,
			status       = EXCLUDED.status`,
		p.AdminID, p.SSOSubject, p.MFAEnrolled, string(p.Status), created)
	if err != nil {
		return fmt.Errorf("adminstore: put admin principal: %w", err)
	}
	return nil
}

// DisablePrincipal marks a principal disabled.
//
// A row that is not there is an ERROR, not a no-op. `PrincipalStore.Disable` has already established
// the principal exists in memory, so a missing row means the two have diverged — and on an offboarding
// path, "we thought we disabled somebody who is not in the durable directory" is the one outcome that
// must not pass quietly.
func (s *Store) DisablePrincipal(adminID string) error {
	ctx, cancel := s.ctx()
	defer cancel()
	res, err := s.db.ExecContext(ctx,
		`UPDATE admin_principal SET status = 'disabled' WHERE admin_id = $1`, adminID)
	if err != nil {
		return fmt.Errorf("adminstore: disable admin principal: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("adminstore: disable admin principal: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("adminstore: no durable row for admin principal %q — the in-memory directory and the database disagree", adminID)
	}
	return nil
}

// Principals reads the whole directory back, for replay at boot.
func (s *Store) Principals(ctx context.Context) ([]adminidentity.Principal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT admin_id, sso_subject, mfa_enrolled, status, created_at
		FROM admin_principal ORDER BY created_at, admin_id`)
	if err != nil {
		return nil, fmt.Errorf("adminstore: read admin principals: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []adminidentity.Principal
	for rows.Next() {
		var p adminidentity.Principal
		var status string
		if err := rows.Scan(&p.AdminID, &p.SSOSubject, &p.MFAEnrolled, &status, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("adminstore: scan admin principal: %w", err)
		}
		p.Status = adminidentity.Status(status)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminstore: read admin principals: %w", err)
	}
	return out, nil
}

// ── Enrolled factors ────────────────────────────────────────────────────────────────────────────

// factorID derives the primary key from the material that identifies a factor.
//
// Derived rather than random so a repeated enrolment of the SAME authenticator updates its row instead
// of adding a second one that also verifies. Two rows for one key is not merely untidy: revoking a
// factor would then leave a duplicate behind that still authenticates, which is the shape of an
// offboarding that did not take.
//
// It is a hash rather than the material itself because a WebAuthn credential id is arbitrary bytes and
// a primary key is quoted in error messages and logs.
func factorID(f adminidentity.EnrolledFactor) string {
	h := sha256.New()
	h.Write([]byte(f.AdminID))
	h.Write([]byte{0})
	h.Write([]byte(f.Kind))
	h.Write([]byte{0})
	if f.Kind == adminidentity.FactorWebAuthn {
		h.Write(f.CredentialID)
	} else {
		h.Write([]byte(f.SecretName))
	}
	return "fac-" + hex.EncodeToString(h.Sum(nil))[:32]
}

// EnrollFactor inserts or replaces one enrolled factor.
func (s *Store) EnrollFactor(f adminidentity.EnrolledFactor) error {
	ctx, cancel := s.ctx()
	defer cancel()
	enrolled := f.EnrolledAt
	if enrolled.IsZero() {
		enrolled = time.Now().UTC()
	}
	// NOT NULL DEFAULT ''::bytea columns: a nil slice would insert NULL and violate the constraint, and
	// the failure would appear as an enrolment that "sometimes" fails depending on the factor kind.
	credID, spki := f.CredentialID, f.PublicKeySPKI
	if credID == nil {
		credID = []byte{}
	}
	if spki == nil {
		spki = []byte{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_factor (factor_id, admin_id, kind, credential_id, public_key_spki, secret_name, sign_count, enrolled_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (factor_id) DO UPDATE SET
			public_key_spki = EXCLUDED.public_key_spki,
			secret_name     = EXCLUDED.secret_name,
			sign_count      = EXCLUDED.sign_count,
			enrolled_at     = EXCLUDED.enrolled_at`,
		factorID(f), f.AdminID, f.Kind, credID, spki, f.SecretName, int64(f.SignCount), enrolled)
	if err != nil {
		return fmt.Errorf("adminstore: enrol factor: %w", err)
	}
	return nil
}

// RecordSignCount advances a WebAuthn credential's clone-detection counter.
//
// The counter only ever moves FORWARD (`sign_count < $3`). A write that would lower it is not an error
// and not applied: the caller has already verified the assertion, and a lower counter is the very
// signal clone detection reads. Letting it write would erase the evidence.
func (s *Store) RecordSignCount(adminID string, credentialID []byte, count uint32) error {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.db.ExecContext(ctx, `
		UPDATE admin_factor SET sign_count = $3
		WHERE admin_id = $1 AND kind = 'webauthn' AND credential_id = $2 AND sign_count < $3`,
		adminID, credentialID, int64(count))
	if err != nil {
		return fmt.Errorf("adminstore: record sign count: %w", err)
	}
	return nil
}

// Factors reads the whole enrolment directory back, for replay at boot.
func (s *Store) Factors(ctx context.Context) ([]adminidentity.EnrolledFactor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT admin_id, kind, credential_id, public_key_spki, secret_name, sign_count, enrolled_at
		FROM admin_factor ORDER BY enrolled_at, factor_id`)
	if err != nil {
		return nil, fmt.Errorf("adminstore: read enrolled factors: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []adminidentity.EnrolledFactor
	for rows.Next() {
		var f adminidentity.EnrolledFactor
		var signCount int64
		if err := rows.Scan(&f.AdminID, &f.Kind, &f.CredentialID, &f.PublicKeySPKI, &f.SecretName, &signCount, &f.EnrolledAt); err != nil {
			return nil, fmt.Errorf("adminstore: scan enrolled factor: %w", err)
		}
		// Round-trip the empty-vs-nil distinction back the way the in-memory store expects it: a TOTP row
		// has zero-length WebAuthn columns, and `Enroll`'s validation reads length, not nil-ness.
		if len(f.CredentialID) == 0 {
			f.CredentialID = nil
		}
		if len(f.PublicKeySPKI) == 0 {
			f.PublicKeySPKI = nil
		}
		f.SignCount = uint32(signCount)
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminstore: read enrolled factors: %w", err)
	}
	return out, nil
}

// ── Role grants ─────────────────────────────────────────────────────────────────────────────────

// AppendGrant writes one row of the append-only role-grant log.
//
// A plain INSERT with no conflict clause, because the table is append-only and a duplicate `grant_id`
// means the in-process sequence has collided with a row already written — which is exactly the
// condition `LoadGrants` restores the counter to prevent, and exactly the condition that must fail
// loudly if that restore ever stops working.
func (s *Store) AppendGrant(g adminrbac.RoleGrant) error {
	ctx, cancel := s.ctx()
	defer cancel()
	// The schema's CHECK requires exactly one of the two timestamps per row, matching the action.
	var grantedAt, revokedAt any
	if g.Action == adminrbac.GrantActionRevoke {
		at := g.RevokedAt
		if at.IsZero() {
			at = time.Now().UTC()
		}
		revokedAt = at
	} else {
		at := g.GrantedAt
		if at.IsZero() {
			at = time.Now().UTC()
		}
		grantedAt = at
	}
	var revokes any
	if g.Revokes != "" {
		revokes = g.Revokes
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_role_grant (grant_id, admin_id, role, action, granted_by, reason, granted_at, revoked_at, revokes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		g.GrantID, g.AdminID, string(g.Role), string(g.Action), g.GrantedBy, g.Reason, grantedAt, revokedAt, revokes)
	if err != nil {
		return fmt.Errorf("adminstore: append role grant: %w", err)
	}
	return nil
}

// Grants reads the whole append-only log back, in application order, for replay at boot.
//
// Ordered by the timestamp each row actually carries — a grant stamps `granted_at`, a revoke stamps
// `revoked_at`, and exactly one is non-NULL. Ordering by either column alone would sort every row of
// the other kind together under NULL, and `Live()` folds the log in order: get that wrong and a revoke
// replays before the grant it withdraws, so a revoked role comes back.
func (s *Store) Grants(ctx context.Context) ([]adminrbac.RoleGrant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT grant_id, admin_id, role, action, granted_by, reason, granted_at, revoked_at, COALESCE(revokes, '')
		FROM admin_role_grant ORDER BY COALESCE(granted_at, revoked_at), grant_id`)
	if err != nil {
		return nil, fmt.Errorf("adminstore: read role grants: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []adminrbac.RoleGrant
	for rows.Next() {
		var g adminrbac.RoleGrant
		var role, action string
		var grantedAt, revokedAt sql.NullTime
		if err := rows.Scan(&g.GrantID, &g.AdminID, &role, &action, &g.GrantedBy, &g.Reason, &grantedAt, &revokedAt, &g.Revokes); err != nil {
			return nil, fmt.Errorf("adminstore: scan role grant: %w", err)
		}
		g.Role, g.Action = adminrbac.Role(role), adminrbac.GrantAction(action)
		if grantedAt.Valid {
			g.GrantedAt = grantedAt.Time
		}
		if revokedAt.Valid {
			g.RevokedAt = revokedAt.Time
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adminstore: read role grants: %w", err)
	}
	return out, nil
}
