package adminlaunch

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/adminidentity"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/adminstore"
	"github.com/heros-foreal/agentd/internal/providergateway"
)

// bootstrap.go creates the FIRST operator, which nothing else in the tree could do.
//
// # The deadlock, stated plainly
//
// On a federated deployment the platform will not issue a session without a platform-verified second
// factor (`authn.go` refuses to construct an authenticator for a real provider that has no verifier),
// enrolling a factor requires a session (`POST /admin/api/mfa/enroll` is behind `session` + `role.grant`),
// and at install time nobody has either. `internal/api/identityflow.go` says so in its own comment —
// "an operator cannot bootstrap their own first factor" — and calls it a two-person operation by design.
//
// What did not exist was the other person's tool. `internal/adminfixture` breaks the deadlock with
// `ADMIN_DEMO_SSO_SUBJECT` and per-run TOTP seeds it PRINTS, which `deploy/AWS.md` correctly lists among
// the inputs that do not belong in a production manifest. So the production path was documented as
// "provisioned out of band" with no out-of-band mechanism anywhere: no CLI, no seed script, no admin
// route. This is that mechanism.
//
// # Why it refuses to invent the seed's storage
//
// A TOTP seed is a credential, and this process resolves credentials through
// `providergateway.Secrets` — which is READ-ONLY, deliberately: agentd's role should not be able to
// write to the secret store. So the flow is two passes, and the split is not a limitation but the check:
//
//	pass 1  the seed does not resolve → generate a candidate, print the `otpauth://` URI and the exact
//	        command to store it, and TOUCH NOTHING. Nothing is enrolled, so a half-done bootstrap leaves
//	        no directory row behind.
//	pass 2  the seed resolves → verify it parses, then write the principal, the role grant and the factor
//	        index.
//
// Pass 2's read goes through the SAME seam that will fetch the seed at every future sign-in. That is the
// point of doing it at all: it converts "the secret was stored somewhere with the wrong name, in the
// wrong region, or under a policy this role cannot read" from a sign-in that fails at the factor step
// with a message about the factor, into a bootstrap that fails while the person who can fix it is
// looking at the terminal.

// BootstrapRequest is one operator to create.
type BootstrapRequest struct {
	// Subject is the IdP's own subject claim — Okta issues `00u…`. Required, and NOT an email: the
	// subject is the stable identifier, an email is a mutable attribute, and binding an operator to a
	// mutable attribute means a rename at the IdP silently transfers or destroys their access.
	Subject string
	// AdminID is the platform's own id for this principal. Defaults to `adm-<role>`.
	AdminID string
	// Role is the role to grant. Defaults to superadmin — the first operator has to be able to grant the
	// others, and a bootstrap that created somebody with no authority would need a second bootstrap.
	Role adminrbac.Role
}

// BootstrapResult reports what happened, so the caller can print it rather than this package doing I/O.
type BootstrapResult struct {
	// Enrolled is true when the directory was written (pass 2).
	Enrolled bool
	AdminID  string
	Role     adminrbac.Role
	// SecretName is the reserved logical name the seed is (or must be) held under.
	SecretName string
	// Seed is the generated candidate, set ONLY on pass 1. It is a credential: the caller prints it once
	// to a terminal and it is never logged, stored or returned again.
	Seed string
	// OTPAuthURI is the enrolment URI for an authenticator app, set only on pass 1. It CONTAINS the seed.
	OTPAuthURI string
	// AlreadyEnrolled is true when this principal already had a factor, so pass 2 was a no-op on the
	// factor and only reconciled the principal and grant.
	AlreadyEnrolled bool
}

// ErrSeedNotProvisioned is pass 1's outcome: the seed is not in the secrets manager yet.
var ErrSeedNotProvisioned = errors.New("adminlaunch: the operator's TOTP seed is not in the secrets manager yet")

// Bootstrap creates or reconciles the first operator. See the file comment for the two passes.
func Bootstrap(ctx context.Context, secretsSrc providergateway.Secrets, platformDB *sql.DB, req BootstrapRequest) (*BootstrapResult, error) {
	now := func() time.Time { return time.Now().UTC() }

	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		return nil, errors.New("adminlaunch: bootstrap needs the IdP subject (Okta's `sub`, e.g. 00u…) — " +
			"an operator is bound to the stable subject, never to an email, because an email is a mutable " +
			"attribute and a rename at the IdP would silently move or destroy their access")
	}
	if strings.Contains(subject, "@") {
		return nil, fmt.Errorf("adminlaunch: %q looks like an email address, not an IdP subject claim — "+
			"bind the operator to the `sub` the IdP asserts (Okta issues 00u…), which does not change when "+
			"the address does", subject)
	}
	role := req.Role
	if role == "" {
		role = adminrbac.RoleSuperadmin
	}
	if !role.Valid() {
		return nil, fmt.Errorf("%w: %q", adminrbac.ErrUnknownRole, role)
	}
	adminID := strings.TrimSpace(req.AdminID)
	if adminID == "" {
		adminID = "adm-" + string(role)
	}
	if secretsSrc == nil {
		return nil, errors.New("adminlaunch: bootstrap needs the deployment's secrets source")
	}
	if platformDB == nil {
		return nil, errors.New("adminlaunch: bootstrap needs the platform database (DATABASE_URL) — the " +
			"directory it writes is the thing that has to survive a restart")
	}

	secrets, err := adminidentity.NewManagedSecrets(secretsSrc)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	secretName := adminidentity.TOTPSeedName(adminID)

	// Pass 1 / pass 2 is decided by ONE question, asked through the same seam a sign-in uses.
	raw, fetchErr := secrets.Named(ctx, secretName)
	if fetchErr != nil {
		seed, gErr := generateSeed()
		if gErr != nil {
			return nil, gErr
		}
		return &BootstrapResult{
			AdminID: adminID, Role: role, SecretName: secretName,
			Seed: seed, OTPAuthURI: otpAuthURI(adminID, seed),
		}, fmt.Errorf("%w (%s): %v", ErrSeedNotProvisioned, secretName, fetchErr)
	}

	seed := strings.TrimSpace(string(raw))
	if err := validateSeed(seed); err != nil {
		// A stored seed that cannot be decoded is a bootstrap that would "succeed" and then refuse every
		// code the operator's phone generates, with the console saying only that the sign-in was not
		// accepted. Caught here, where the message can name the secret.
		return nil, fmt.Errorf("adminlaunch: the seed stored under %s is not usable: %w", secretName, err)
	}

	store, err := adminstore.New(platformDB)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}

	// The principal FIRST: `admin_role_grant` and `admin_factor` both carry a foreign key to it, so any
	// other order fails on a fresh install with a constraint error instead of doing the work.
	//
	// Written through the in-memory store with its writer attached rather than straight to SQL, so the
	// bootstrap goes down the same validation path a running process does — a bootstrap that could create
	// a principal the running platform would reject is a bootstrap that produces an operator who cannot
	// sign in.
	principals := adminidentity.NewPrincipalStore()
	if err := principals.SetWriter(store); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	existing, err := store.Principals(ctx)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	if err := adminidentity.LoadPrincipals(principals, existing); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	// Refuse to move an EXISTING admin_id onto a different subject.
	//
	// `PrincipalStore.Put` is insert-or-replace, so a typo'd subject on a re-run would silently rebind
	// the account — and because the old subject's index entry is deleted, the operator who could sign in
	// yesterday cannot today, with no record of what changed. Re-running with the SAME subject stays a
	// no-op, which is what makes this command safe to run twice.
	if prior, ok := principals.ByID(adminID); ok && prior.SSOSubject != subject {
		return nil, fmt.Errorf("adminlaunch: %s is already bound to a different IdP subject — rebinding an "+
			"existing operator to a new subject is an account takeover if the subject is wrong, so it is not "+
			"something a bootstrap does; disable the principal and create a new one instead", adminID)
	}
	created := now()
	if prior, ok := principals.ByID(adminID); ok {
		created = prior.CreatedAt
	}
	if err := principals.Put(adminidentity.Principal{
		AdminID: adminID, SSOSubject: subject, MFAEnrolled: true,
		Status: adminidentity.StatusActive, CreatedAt: created,
	}); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}

	// The role grant, folded from the append-only log so a re-run does not stack duplicate rows.
	grants := adminrbac.NewGrantStore(now)
	if err := grants.SetWriter(store); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	grantRows, err := store.Grants(ctx)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	if err := adminrbac.LoadGrants(grants, grantRows); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	held := false
	for _, r := range grants.Live(adminID) {
		if r == role {
			held = true
			break
		}
	}
	if !held {
		if _, err := grants.Seed(adminID, role, "deployment bootstrap: the first operator, created out of band"); err != nil {
			return nil, fmt.Errorf("adminlaunch: %w", err)
		}
	}

	// The factor INDEX — the seed itself stays in the secrets manager and is never written here.
	factors := adminidentity.NewFactorStore()
	if err := factors.SetWriter(store); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	factorRows, err := store.Factors(ctx)
	if err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	if err := adminidentity.LoadFactors(factors, factorRows); err != nil {
		return nil, fmt.Errorf("adminlaunch: %w", err)
	}
	already := false
	for _, f := range factors.For(adminID) {
		if f.Kind == adminidentity.FactorTOTP && f.SecretName == secretName {
			already = true
			break
		}
	}
	if !already {
		if err := factors.Enroll(adminidentity.EnrolledFactor{
			AdminID: adminID, Kind: adminidentity.FactorTOTP, SecretName: secretName, EnrolledAt: now(),
		}); err != nil {
			return nil, fmt.Errorf("adminlaunch: %w", err)
		}
	}

	return &BootstrapResult{
		Enrolled: true, AdminID: adminID, Role: role, SecretName: secretName, AlreadyEnrolled: already,
	}, nil
}

// generateSeed mints a 160-bit base32 TOTP seed — RFC 4226's recommended length, and what an
// authenticator app expects.
func generateSeed() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("adminlaunch: generate TOTP seed: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// validateSeed checks a stored seed decodes the way the verifier will decode it.
//
// The verifier reads the seed with unpadded base32 (see `verifyTOTP`), so this uses the identical
// decoding rather than a lenient one — a check more permissive than the thing it is checking passes
// exactly the values that then fail.
func validateSeed(seed string) error {
	if seed == "" {
		return errors.New("it is empty")
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(seed))
	if err != nil {
		return fmt.Errorf("it is not unpadded base32 (%w) — store the seed exactly as printed, with no "+
			"padding, no whitespace and no surrounding quotes or JSON", err)
	}
	if len(decoded) < 16 {
		return fmt.Errorf("it decodes to %d bytes; RFC 4226 wants at least 16", len(decoded))
	}
	return nil
}

// otpAuthURI builds the `otpauth://` URI an authenticator app scans.
//
// 🔴 It CONTAINS the seed. The caller prints it to a terminal once; it must not be logged, stored, or
// put in a ticket, and this package never returns it on the enrolment pass.
func otpAuthURI(adminID, seed string) string {
	label := url.PathEscape("Heros Operator:" + adminID)
	q := url.Values{}
	q.Set("secret", seed)
	q.Set("issuer", "Heros Operator")
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + label + "?" + q.Encode()
}
