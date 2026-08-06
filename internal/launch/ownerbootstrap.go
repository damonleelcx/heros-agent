package launch

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/mailer"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

// ownerbootstrap.go answers the one question a self-serve sign-up form does not: on a deployment where
// sign-up is OFF — the air-gapped and single-customer shape, and the default — how does the FIRST owner get a
// password?
//
// # Why this is the reset path and not a printed temporary password
//
// The obvious answer is to print a generated password at boot. 🔴 It is the wrong one: a printed password is a
// live credential that sits in a log file, a terminal scrollback, a `journalctl` archive and whatever ships
// logs off the box, and it stays live until somebody happens to change it. A single-use token is spent on
// first use and worthless afterwards, and it is delivered by the same mechanism a reset already uses — so
// there is one recovery path in this system rather than two, and the second one is not a weaker copy of the
// first.
//
// # Idempotent, because boot runs many times
//
// An address that already has a password mints nothing. That is what makes this safe to leave configured
// forever: a restart does not hand out a new way into an account somebody is already using.
//
// # 🔴 What it deliberately does NOT do, and the one thing it now does
//
// It does not create an ORGANIZATION. Inventing one for an address named in an environment variable is
// exactly the "onboarding by deploy" this phase exists to end, wearing a new hat.
//
// What it does do, when `HEROS_BOOTSTRAP_OWNER_TENANT` names an organization that ALREADY EXISTS, is give
// that address a password identity and an OWNER MEMBERSHIP in it. That is the missing half without which
// switching a live deployment to the password seam is a lockout: the shared assertion stops working, nobody
// has a password, and signing up afterwards mints a NEW organization rather than granting the existing one —
// so the tenant that holds all the data has no way in at all.
//
// The adoption is bounded by three refusals, each of which fails LOUDLY rather than doing something
// approximate:
//
//   - the organization must already exist. A missing one is refused, never created.
//   - the organization must not be suspended. Bootstrapping an owner into a halted tenant would be the one
//     way to un-halt it without an operator decision.
//   - an address that already has a password gets NOTHING. That is what makes this safe to leave configured
//     forever: a restart must not mint a new way into an account somebody is already using.
//
// ⚠️ Stated rather than implied: this variable GRANTS OWNERSHIP of an existing organization. Whoever can set
// it can already set `HEROS_SECRETS_SOURCE`, the database URL and the image — so it is not an escalation
// over the authority they hold. It is written down because "an environment variable that makes somebody an
// owner" is a sentence a reviewer should meet in a comment rather than infer from a call.

// BootstrapOwnerEnv names the address this install treats as its first owner.
const BootstrapOwnerEnv = "HEROS_BOOTSTRAP_OWNER_EMAIL"

// BootstrapOwnerTenantEnv names an EXISTING organization the bootstrap owner is adopted into.
//
// Unset keeps the pre-existing behaviour exactly: an address that has no password identity yet is reported
// and nothing is created. Set, it is the seam-switch path — see the package header for the three refusals
// that bound it.
const BootstrapOwnerTenantEnv = "HEROS_BOOTSTRAP_OWNER_TENANT"

// ConsoleURLEnv is the origin the links in outgoing mail point at.
//
// There is no default. A guessed origin produces a link that 404s in somebody's inbox, which is worse than a
// missing one because it looks like the product is broken rather than unconfigured.
const ConsoleURLEnv = "HEROS_CONSOLE_URL"

func consoleBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv(ConsoleURLEnv)), "/")
}

// ConsoleIdentityEnv is the console's declared sign-in mechanism, mirrored onto the platform's readiness
// surface.
//
// 🔴 The console is the authority; this is a REPORT of what it was told, not a second source of truth. The
// platform does not act on it — nothing here branches on the value — it only publishes it, because "which
// front door is this install running" is the first question asked when nobody can sign in, and the answer
// currently lives only in a Kubernetes environment variable on a different deployment.
const ConsoleIdentityEnv = "CONSOLE_TENANT_IDENTITY"

func consoleIdentityKind() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv(ConsoleIdentityEnv)))
}

// bootstrapOwner mints a single-use password-set link for the declared first owner, if they need one.
//
// It returns nothing and fails nothing. Every outcome is logged, because this runs at boot where there is
// nobody to return an error to — and because the operator reading that log is the delivery mechanism when
// mail is unconfigured.
func bootstrapOwner(store tenancy.Store, mail mailer.Mailer, consoleURL string, now time.Time) {
	email := tenancy.NormalizeEmail(os.Getenv(BootstrapOwnerEnv))
	if email == "" {
		return
	}
	if store == nil {
		log.Printf("account system: WARN %s is set but this deployment has no identity store", BootstrapOwnerEnv)
		return
	}

	tenantID := strings.TrimSpace(os.Getenv(BootstrapOwnerTenantEnv))

	user, err := store.FindUserByEmail(tenancy.IssuerPassword, email)
	if err != nil {
		if tenantID == "" {
			// 🔴 No person, no organization named, and none of either is created. Minting an organization
			// for an address in an environment variable is the deploy-to-onboard shape this phase removes.
			// The message names the three ways forward, because an operator who set this variable expected
			// something to happen and deserves to know why nothing did.
			log.Printf("account system: WARN %s names %s, but no password-signin person exists at that "+
				"address. Nothing was created. Either enable %s so they can sign up, invite them from an "+
				"existing organization, or set %s to adopt them into one that already exists.",
				BootstrapOwnerEnv, email, SelfServeSignupEnv, BootstrapOwnerTenantEnv)
			return
		}
		if user, err = adoptOwner(store, email, tenantID, now); err != nil {
			log.Printf("account system: WARN %s could not adopt %s into %q: %v", BootstrapOwnerTenantEnv, email, tenantID, err)
			return
		}
	} else if tenantID != "" {
		// The person exists — from an earlier boot, or from a sign-up. Make sure the membership exists too,
		// because the two writes are separate and a boot that died between them would otherwise leave a
		// password identity with nothing to sign in to, permanently.
		if err := ensureOwnerMembership(store, user, tenantID, now); err != nil {
			log.Printf("account system: WARN %s could not confirm %s owns %q: %v", BootstrapOwnerTenantEnv, email, tenantID, err)
			return
		}
	}

	if _, perr := store.GetPassword(user.UserID); perr == nil {
		// Already has one. Idempotent, silently — a line every restart would train an operator to ignore
		// this log, and this is the branch every healthy restart takes.
		return
	}

	secret, err := tenancy.NewCredentialSecret()
	if err != nil {
		log.Printf("account system: WARN could not mint a bootstrap link for %s: %v", email, err)
		return
	}
	if _, err := store.MintIdentityToken(tenancy.IdentityToken{
		TokenHash: tenancy.HashSecret(secret),
		UserID:    user.UserID,
		// The RESET purpose, not a third one. Setting a first password and replacing a forgotten one are the
		// same act against the same store; a separate purpose would be a second code path with the same job
		// and one fewer test.
		Purpose:   tenancy.TokenResetPassword,
		Email:     email,
		CreatedAt: now,
		ExpiresAt: now.Add(tenancy.BootstrapTokenTTL),
	}); err != nil {
		log.Printf("account system: WARN could not store a bootstrap link for %s: %v", email, err)
		return
	}

	link := consoleURL + "/reset-password?t=" + secret
	msg := mailer.OwnerBootstrap(consoleURL, link, tenancy.BootstrapTokenTTL)
	msg.To = email
	if err := mail.Send(context.Background(), msg); err != nil {
		log.Printf("account system: WARN the bootstrap link for %s could not be sent: %v", email, err)
		return
	}
	// 🔴 The FACT, never the link. When mail is unconfigured the fallback holds the body on the operator
	// surface and logs its own WARN; duplicating the token here would put a live credential in the log
	// pipeline, which is the thing that mechanism was built to avoid.
	log.Printf("account system: a single-use password-set link was issued for the bootstrap owner %s "+
		"(valid %s). It was handed to the mail seam; if mail is unconfigured it is held on the operator "+
		"surface.", email, tenancy.BootstrapTokenTTL)
}

// adoptOwner creates the password identity and the owner membership, in an organization that must already
// exist.
//
// The ORDER is deliberate: the organization is checked first, so a typo in the variable creates no person.
// A user with no membership is recoverable (the next boot adds it); a person created for an organization
// that does not exist is a row nobody asked for.
func adoptOwner(store tenancy.Store, email, tenantID string, now time.Time) (tenancy.User, error) {
	tenant, err := store.GetTenant(tenantID)
	if err != nil {
		// 🔴 Refused, never created. The message names the alternative rather than leaving an operator to
		// wonder whether the id was wrong or the feature was.
		return tenancy.User{}, fmt.Errorf("no such organization — this adopts an owner into one that already "+
			"exists and never creates one; check the id, or let them sign up with %s enabled: %w",
			SelfServeSignupEnv, err)
	}
	if tenant.Status.Suspended() {
		// Bootstrapping an owner into a halted tenant would be a way to reach a suspended organization
		// without the operator decision that suspended it.
		return tenancy.User{}, fmt.Errorf("organization %q is suspended", tenantID)
	}

	user, err := store.UpsertUser(tenancy.User{
		Issuer: tenancy.IssuerPassword,
		// 🔴 The address IS the subject on this seam — there is no identity provider and no `sub` to be
		// stable instead. See tenancy/passwordidentity.go and ADR-012 Decision 5.
		Subject:   tenancy.PasswordSubject(email),
		Email:     email,
		CreatedAt: now,
	})
	if err != nil {
		return tenancy.User{}, fmt.Errorf("creating the person: %w", err)
	}
	if err := ensureOwnerMembership(store, user, tenantID, now); err != nil {
		return tenancy.User{}, err
	}
	log.Printf("account system: %s was adopted as an OWNER of %q (%s) by %s. They hold no password yet; a "+
		"single-use link follows.", email, tenantID, tenant.Name, BootstrapOwnerTenantEnv)
	return user, nil
}

// ensureOwnerMembership makes the person an active owner of the organization, idempotently.
//
// An existing ACTIVE membership is left exactly as it is, whatever its role. That is not laziness: silently
// promoting an existing member to owner because an environment variable named their address would be a
// privilege change nobody performed, and the variable's job is to create a way in where there is none — not
// to administer the memberships that already exist.
func ensureOwnerMembership(store tenancy.Store, user tenancy.User, tenantID string, now time.Time) error {
	existing, err := store.GetMembership(user.UserID, tenantID)
	if err == nil && existing.Active() {
		if existing.Role != tenancy.RoleOwner {
			log.Printf("account system: %s is already an active %s of %q — %s left that unchanged rather "+
				"than promoting them; change the role from the members page if that is wrong.",
				user.Email, existing.Role, tenantID, BootstrapOwnerTenantEnv)
		}
		return nil
	}
	if _, err := store.PutMembership(tenancy.Membership{
		UserID:   user.UserID,
		TenantID: tenantID,
		Role:     tenancy.RoleOwner,
		Status:   tenancy.MemberActive,
		JoinedAt: now,
	}); err != nil {
		return fmt.Errorf("granting the owner membership: %w", err)
	}
	return nil
}
