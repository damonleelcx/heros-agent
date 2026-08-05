package tenancy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// orgcreate.go is sign-up: an organization, its first person, and their owner membership, written
// together or not at all — plus a hook so the BILLING account joins the same transaction without this
// package learning what an account is.
//
// # Why all of it is one write
//
// Every partial state here is unrecoverable THROUGH THE PRODUCT, which is a different and worse thing
// than merely inconsistent:
//
//   - a tenant with no owner has nobody who can invite an owner;
//   - an account with no tenant is a customer nobody can sign in as;
//   - a tenant with no account reaches `StartCheckout → accounts.Get → ErrNotFound`, which is the exact
//     defect P27 exists to close.
//
// None of the three has a screen that could fix it, so each would become an operator ticket. The
// alternative shape — create what you can and let a background job reconcile — is a cleanup job, and a
// cleanup job that runs rarely is a cleanup job nobody notices has stopped.
//
// # Why the account arrives as a HOOK rather than as an import
//
// `account` is a different bounded context. Importing it here would point the identity domain at the
// billing domain, and a constraint or an import in that direction means an identity migration cannot run
// without a billing outage. So the caller — `internal/signup`, which is allowed to know about both —
// passes a closure, and this package gives it the transaction to run in.
//
// `MemStore` passes `nil` to that closure, because there is no transaction to hand over; it achieves
// atomicity by holding its own lock and undoing its three writes if the hook fails. That is not as
// strong as a database transaction and it does not pretend to be — it is exactly as strong as the store
// it belongs to, which loses everything on a restart anyway.

// Execer is the slice of `*sql.Tx` a hook needs to write inside this transaction.
//
// Declared here rather than taking `*sql.Tx` so the in-memory path can pass nil and so a hook cannot
// commit, roll back, or otherwise take over the transaction it was lent.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// NewOrganization is a sign-up request. Everything except `Name` comes from a VERIFIED assertion; the
// name is the one field the person types, and it is a display string.
type NewOrganization struct {
	// TenantID is minted when empty. A caller may supply one — the seed does — but a sign-up should not.
	TenantID string
	Name     string
	Issuer   string
	Subject  string
	Email    string
	At       time.Time
}

// Organization is what sign-up produced.
type Organization struct {
	Tenant     Tenant     `json:"tenant"`
	Owner      User       `json:"owner"`
	Membership Membership `json:"membership"`
}

// OrganizationCreator is the sign-up half of the store, kept separate from `Store` so a caller that only
// signs people up cannot reach the rest of it.
type OrganizationCreator interface {
	// CreateOrganization writes tenant, user and owner membership atomically. `within` runs inside the
	// same transaction and may write to other tables; if it returns an error, nothing is written.
	CreateOrganization(o NewOrganization, within func(Execer) error) (Organization, error)
}

func (o NewOrganization) validate() (NewOrganization, error) {
	o.TenantID = strings.TrimSpace(o.TenantID)
	o.Name = strings.TrimSpace(o.Name)
	o.Issuer = strings.TrimSpace(o.Issuer)
	o.Subject = strings.TrimSpace(o.Subject)
	o.Email = NormalizeEmail(o.Email)
	if o.Name == "" {
		return o, fmt.Errorf("%w: an organization needs a name", ErrEmptyField)
	}
	if o.Issuer == "" || o.Subject == "" {
		return o, fmt.Errorf("%w: sign-up needs a verified identity", ErrEmptyField)
	}
	if o.TenantID == "" {
		o.TenantID = newID("org")
	}
	if o.At.IsZero() {
		o.At = time.Unix(0, 0).UTC()
	}
	return o, nil
}

// CreateOrganization (in-memory) holds the store's lock for the whole operation and undoes its writes if
// the hook fails. See the file header for what that guarantee is and is not.
func (s *MemStore) CreateOrganization(o NewOrganization, within func(Execer) error) (Organization, error) {
	o, err := o.validate()
	if err != nil {
		return Organization{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, clash := s.tenants[o.TenantID]; clash {
		return Organization{}, fmt.Errorf("%w: organization %s", ErrExists, o.TenantID)
	}

	tenant := Tenant{TenantID: o.TenantID, Name: o.Name, Status: StatusActive, CreatedAt: o.At}

	// The person may already exist — a contractor signing up their second organization is the ordinary
	// case, not an edge one — so this is an upsert and the undo has to know which it was.
	userKey := fedKey(o.Issuer, o.Subject)
	existingUserID, userExisted := s.byFederated[userKey]
	var user User
	if userExisted {
		user = s.users[existingUserID]
		user.Email = o.Email
	} else {
		s.nextID++
		user = User{
			UserID:    fmt.Sprintf("usr_%06d", s.nextID),
			Issuer:    o.Issuer,
			Subject:   o.Subject,
			Email:     o.Email,
			CreatedAt: o.At,
		}
	}
	membership := Membership{
		UserID: user.UserID, TenantID: tenant.TenantID,
		Role: RoleOwner, Status: MemberActive, JoinedAt: o.At,
	}

	s.tenants[tenant.TenantID] = tenant
	s.users[user.UserID] = user
	s.byFederated[userKey] = user.UserID
	s.members[memKey(user.UserID, tenant.TenantID)] = membership

	undo := func() {
		delete(s.tenants, tenant.TenantID)
		delete(s.members, memKey(user.UserID, tenant.TenantID))
		if !userExisted {
			delete(s.users, user.UserID)
			delete(s.byFederated, userKey)
			s.nextID--
		}
	}

	if within != nil {
		if err := within(nil); err != nil {
			undo()
			return Organization{}, err
		}
	}
	return Organization{Tenant: tenant, Owner: user, Membership: membership}, nil
}

// CreateOrganization (Postgres) is one transaction across three tables plus whatever the hook writes.
func (p *PGStore) CreateOrganization(o NewOrganization, within func(Execer) error) (Organization, error) {
	o, err := o.validate()
	if err != nil {
		return Organization{}, err
	}
	ctx, cancel := p.ctx()
	defer cancel()

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return Organization{}, err
	}
	// Rollback on every path that is not an explicit commit. A committed transaction's Rollback is a
	// no-op, so this is safe on the success path too — and it is the reason no early return below has
	// to remember to clean up.
	defer func() { _ = tx.Rollback() }()

	tenant, err := scanTenant(tx.QueryRowContext(ctx, `
		INSERT INTO tenant (tenant_id, name, status, created_at)
		VALUES ($1, $2, 'active', $3)
		RETURNING `+tenantColumns, o.TenantID, o.Name, o.At))
	if err != nil {
		if isUniqueViolation(err) {
			return Organization{}, fmt.Errorf("%w: organization %s", ErrExists, o.TenantID)
		}
		return Organization{}, err
	}

	user, err := scanUser(tx.QueryRowContext(ctx, `
		INSERT INTO platform_user (user_id, issuer, subject, email, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT ON CONSTRAINT platform_user_federated_identity
		DO UPDATE SET email = EXCLUDED.email
		RETURNING `+userColumns, newID("usr"), o.Issuer, o.Subject, o.Email, o.At))
	if err != nil {
		return Organization{}, err
	}

	membership, err := scanMembership(tx.QueryRowContext(ctx, `
		INSERT INTO membership (user_id, tenant_id, role, status, invited_by, joined_at)
		VALUES ($1, $2, 'owner', 'active', '', $3)
		RETURNING `+membershipColumns, user.UserID, tenant.TenantID, o.At))
	if err != nil {
		return Organization{}, err
	}

	if within != nil {
		if err := within(tx); err != nil {
			return Organization{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Organization{}, err
	}
	return Organization{Tenant: tenant, Owner: user, Membership: membership}, nil
}

// ErrNoOrganizationCreator is returned when a store cannot sign anybody up. Every store this package
// ships can; the error exists because `Store` is an interface a deployment could implement elsewhere,
// and a nil-pointer panic is a worse way to learn that.
var ErrNoOrganizationCreator = errors.New("tenancy: this store cannot create organizations")

// AsOrganizationCreator narrows a Store, or explains why it cannot be narrowed.
func AsOrganizationCreator(s Store) (OrganizationCreator, error) {
	if c, ok := s.(OrganizationCreator); ok {
		return c, nil
	}
	return nil, ErrNoOrganizationCreator
}
