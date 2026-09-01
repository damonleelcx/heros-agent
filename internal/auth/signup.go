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

// ErrOrgNameRequired means the sign-up carried no organization name.
var ErrOrgNameRequired = errors.New("auth: an organization needs a name")

// MaxOrgNameLength bounds what a stranger can write into a column every member of that organization
// will read. Not a security boundary — the console escapes it — but an unbounded name is a row that
// breaks every layout that renders it, and the limit belongs where the value is created.
const MaxOrgNameLength = 80

// SignUp creates a NEW organization and its first account, and signs that account in.
//
// # 🔴 Why the whole thing is one transaction
//
// It creates three rows across three tables, and every partial outcome is worse than a failure. A
// tenant with no owner is an organization nobody can enter, holding an address that can never be used
// to sign up again — the account is unreachable AND unrecoverable, because the second attempt fails on
// the unique index the first one populated. A user with no session is merely an odd first minute.
//
// Failing atomically means a failed sign-up leaves nothing at all, so the person simply tries again.
//
// # 🔴 Why the caller is an OWNER, and why that is not a default
//
// The founding account is the only one in the organization. Anything less than owner would be an
// organization locked out of its own administration from the moment it existed — nobody able to change
// a setting or add a member, recoverable only with database access. CreateUser refuses to guess at this
// for the same reason; here the answer is forced by there being exactly one person.
func (s *Store) SignUp(ctx context.Context, orgName, email, password string) (
	token string, p tenancy.Principal, err error) {

	orgName = strings.TrimSpace(orgName)
	if orgName == "" {
		return "", tenancy.Principal{}, ErrOrgNameRequired
	}
	if len([]rune(orgName)) > MaxOrgNameLength {
		return "", tenancy.Principal{}, fmt.Errorf("%w: at most %d characters",
			ErrOrgNameRequired, MaxOrgNameLength)
	}
	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return "", tenancy.Principal{}, ErrNoSuchUser
	}

	// 🔴 Hashed BEFORE the transaction opens. Hashing costs 64 MiB and hundreds of milliseconds, and
	// holding a database transaction open across it would pin a connection for the duration of a cost
	// an unauthenticated stranger controls the arrival rate of. It also lets the weak-password refusal
	// happen without having touched the database at all.
	hash, err := HashPassword(ctx, password)
	if err != nil {
		return "", tenancy.Principal{}, err
	}

	tenantID := "org_" + randomID()
	userID := "usr_" + randomID()
	tok, sessionID := newToken()
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", tenancy.Principal{}, fmt.Errorf("auth: sign up: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tenants (id, name, created_at, created_by) VALUES ($1,$2,$3,$4)`,
		tenantID, orgName, now, email); err != nil {
		return "", tenancy.Principal{}, fmt.Errorf("auth: sign up: creating organization: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, tenant, email, password_hash, role, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		userID, tenantID, email, hash, string(tenancy.Owner), now); err != nil {
		// 🔴 Both indexes are named. users_email_global is the one that fires on this path — the address
		// is already in SOME organization — but naming only it would turn a per-organization collision
		// into an unexplained 500 if the global index is ever dropped.
		if isDuplicateEmail(err) {
			return "", tenancy.Principal{}, ErrEmailTaken
		}
		return "", tenancy.Principal{}, fmt.Errorf("auth: sign up: creating account: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (id, tenant, user_id, created_at, expires_at) VALUES ($1,$2,$3,$4,$5)`,
		sessionID, tenantID, userID, now, now.Add(SessionLifetime)); err != nil {
		return "", tenancy.Principal{}, fmt.Errorf("auth: sign up: creating session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", tenancy.Principal{}, fmt.Errorf("auth: sign up: %w", err)
	}
	return tok, tenancy.Principal{
		Tenant: tenantID, Subject: email, SessionID: sessionID, UserID: userID, Role: tenancy.Owner,
	}, nil
}

// isDuplicateEmail reports whether an insert failed on either address-uniqueness index.
//
// ⚠️ Matched on index NAME rather than on a driver error code, which is what the rest of this package
// already does. The name is stable because it is written in the migration; a pgconn code would be more
// precise but would make this the only place in the package that knows about the driver.
func isDuplicateEmail(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "users_email_global") || strings.Contains(msg, "users_email_per_tenant")
}

// OrganizationOf reports the organization an address belongs to.
//
// 🔴 NOT for use on any unauthenticated path. It answers "does this address exist", which is exactly
// the question Login and the password-reset endpoint go to great lengths to refuse — a constant answer
// and a constant cost on both branches. This exists for authenticated callers and for tests.
func (s *Store) OrganizationOf(ctx context.Context, email string) (string, error) {
	var tenant string
	err := s.db.QueryRowContext(ctx,
		`SELECT tenant FROM users WHERE lower(email) = lower($1)`, normalizeEmail(email)).Scan(&tenant)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoSuchUser
	}
	return tenant, err
}

// RememberSubject records which repository an organization has loaded.
//
// # 🔴 Why this lives in the identity store
//
// It is a column on `tenants`, and this package owns that table. Putting the accessor anywhere else
// would mean a second package writing to a table it does not own — which is how a schema ends up with
// two writers and no agreed shape.
//
// What is stored is the REFERENCE the person typed and the revision it resolved to, never the corpus:
// the clone and the index are rebuilt from it. See migration 0009.
func (s *Store) RememberSubject(ctx context.Context, tenant, ref, revision string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE tenants SET subject_ref = $2, subject_revision = $3 WHERE id = $1`,
		tenant, ref, revision)
	if err != nil {
		return fmt.Errorf("auth: remembering the subject: %w", err)
	}
	return nil
}

// RememberedSubject returns the repository an organization last loaded, if any.
//
// An organization that has never loaded one returns empty strings and no error — that is a normal
// state, not a fault, and making the caller distinguish it from a failure would push a nil check into
// every path that just wants to know whether to offer a restore.
func (s *Store) RememberedSubject(ctx context.Context, tenant string) (ref, revision string, err error) {
	var r, v sql.NullString
	err = s.db.QueryRowContext(ctx,
		`SELECT subject_ref, subject_revision FROM tenants WHERE id = $1`, tenant).Scan(&r, &v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("auth: reading the remembered subject: %w", err)
	}
	return r.String, v.String, nil
}
