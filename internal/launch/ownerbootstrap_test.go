package launch

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/mailer"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

// ownerbootstrap_test.go covers the seam-switch path: giving an existing organization an owner who can sign
// in with a password, so that flipping a live deployment to the password seam is not a lockout.
//
// Every case runs against the REAL store and the REAL operator mailer — which is the code an unconfigured
// deployment actually runs, so reading `Undelivered()` asserts the production path rather than a double.

var adoptAt = time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)

// bootFixture returns a store holding one organization, an operator mailer, and a buffer capturing
// EVERYTHING written to the standard logger.
//
// 🔴 The buffer must capture the DEFAULT logger, not the mailer's. `bootstrapOwner` reports its refusals
// through `log.Printf` — it runs at boot, where there is nobody to return an error to, so the log IS the
// report. An earlier version of this fixture handed a buffer to the mailer only and every assertion about a
// refusal message failed while the refusal itself was working perfectly: the state assertions passed and
// the message assertions did not, which is precisely the shape that gets a correct test deleted as flaky.
func bootFixture(t *testing.T) (*tenancy.MemStore, *mailer.OperatorMailer, *bytes.Buffer) {
	t.Helper()
	store := tenancy.NewMemStore()
	if _, err := store.CreateTenant(tenancy.Tenant{TenantID: "heros", Name: "Heros", CreatedAt: adoptAt}); err != nil {
		t.Fatalf("fixture tenant: %v", err)
	}
	logs := &bytes.Buffer{}
	previous := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	return store, mailer.NewOperatorMailer(log.New(logs, "", 0)), logs
}

// The whole point: an address nobody has ever seen becomes an owner of an organization that already holds
// the data, and gets one single-use link to set a password.
func TestBootstrapAdoptsAnExistingOrganization(t *testing.T) {
	store, mail, _ := bootFixture(t)
	t.Setenv(BootstrapOwnerEnv, "Damon@heros-agent.space")
	t.Setenv(BootstrapOwnerTenantEnv, "heros")

	bootstrapOwner(store, mail, "https://heros-agent.space", adoptAt)

	user, err := store.FindUserByEmail(tenancy.IssuerPassword, "damon@heros-agent.space")
	if err != nil {
		t.Fatalf("no password identity was created: %v", err)
	}
	if user.Issuer != tenancy.IssuerPassword {
		t.Errorf("issuer is %q, want the password seam", user.Issuer)
	}
	m, err := store.GetMembership(user.UserID, "heros")
	if err != nil {
		t.Fatalf("no membership in the adopted organization: %v", err)
	}
	if m.Role != tenancy.RoleOwner || !m.Active() {
		t.Fatalf("membership is %s/%s, want an active owner — without it the adopted person signs in and "+
			"reaches nothing", m.Role, m.Status)
	}

	// 🔴 A link, not a password. A printed temporary password is a live credential that sits in a log until
	// somebody changes it; a single-use token is worthless the moment it is spent.
	held := mail.Undelivered()
	if len(held) != 1 || held[0].Purpose != mailer.PurposeOwnerBootstrap {
		t.Fatalf("expected exactly one bootstrap message, got %+v", held)
	}
	if held[0].To != "damon@heros-agent.space" {
		t.Errorf("the link went to %q", held[0].To)
	}
	if !strings.Contains(held[0].Body, "/reset-password?t=") {
		t.Fatalf("the message carries no password-set link:\n%s", held[0].Body)
	}
	// And the token is real: spending it is what a person will do.
	token := strings.Fields(held[0].Body[strings.Index(held[0].Body, "?t=")+3:])[0]
	if _, err := store.ConsumeIdentityToken(tenancy.HashSecret(token), tenancy.TokenResetPassword, adoptAt); err != nil {
		t.Fatalf("the minted link is not usable: %v", err)
	}
}

// 🔴 Idempotent. A restart must not mint a new way into an account somebody is already using — that is what
// makes this safe to leave configured forever rather than something to remember to unset.
func TestBootstrapMintsNothingForAnAccountThatAlreadyHasAPassword(t *testing.T) {
	store, mail, _ := bootFixture(t)
	t.Setenv(BootstrapOwnerEnv, "damon@heros-agent.space")
	t.Setenv(BootstrapOwnerTenantEnv, "heros")

	bootstrapOwner(store, mail, "https://heros-agent.space", adoptAt)
	user, err := store.FindUserByEmail(tenancy.IssuerPassword, "damon@heros-agent.space")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	// They use the link and choose a password.
	if _, err := store.SetPassword(user.UserID,
		"$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0c2ExMg$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaDI", adoptAt); err != nil {
		t.Fatalf("set password: %v", err)
	}

	before := len(mail.Undelivered())
	for i := 0; i < 3; i++ {
		bootstrapOwner(store, mail, "https://heros-agent.space", adoptAt.Add(time.Duration(i)*time.Hour))
	}
	if got := len(mail.Undelivered()); got != before {
		t.Fatalf("three restarts minted %d further link(s) into an account that is already in use", got-before)
	}
}

// 🔴 It refuses to CREATE an organization. Inventing one for an address in an environment variable is the
// deploy-to-onboard shape this whole phase exists to end.
func TestBootstrapRefusesToCreateAnOrganization(t *testing.T) {
	store, mail, logs := bootFixture(t)
	t.Setenv(BootstrapOwnerEnv, "damon@heros-agent.space")
	t.Setenv(BootstrapOwnerTenantEnv, "an-organization-that-does-not-exist")

	bootstrapOwner(store, mail, "https://heros-agent.space", adoptAt)

	if tenants, _ := store.ListTenants(); len(tenants) != 1 {
		t.Fatalf("%d organizations exist, want the 1 in the fixture — an organization was created", len(tenants))
	}
	if _, err := store.FindUserByEmail(tenancy.IssuerPassword, "damon@heros-agent.space"); err == nil {
		t.Error("a person was created for an organization that does not exist")
	}
	if len(mail.Undelivered()) != 0 {
		t.Error("a link was sent for an adoption that did not happen")
	}
	out := logs.String()
	if !strings.Contains(out, "WARN") || !strings.Contains(out, "no such organization") {
		t.Errorf("the refusal is not reported in a way an operator can act on:\n%s", out)
	}
}

// A suspended organization is halted by an operator decision; bootstrapping an owner into it would be a way
// to reach it without that decision.
func TestBootstrapRefusesASuspendedOrganization(t *testing.T) {
	store, mail, logs := bootFixture(t)
	if _, err := store.SetTenantStatus("heros", tenancy.StatusSuspended, adoptAt); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	t.Setenv(BootstrapOwnerEnv, "damon@heros-agent.space")
	t.Setenv(BootstrapOwnerTenantEnv, "heros")

	bootstrapOwner(store, mail, "https://heros-agent.space", adoptAt)

	if _, err := store.FindUserByEmail(tenancy.IssuerPassword, "damon@heros-agent.space"); err == nil {
		t.Error("an owner was adopted into a suspended organization")
	}
	if !strings.Contains(logs.String(), "suspended") {
		t.Errorf("the refusal does not name the reason:\n%s", logs.String())
	}
}

// Without the tenant variable the behaviour is exactly what it was before: report, create nothing.
func TestBootstrapWithoutATenantCreatesNothingAndSaysWhy(t *testing.T) {
	store, mail, logs := bootFixture(t)
	t.Setenv(BootstrapOwnerEnv, "damon@heros-agent.space")
	t.Setenv(BootstrapOwnerTenantEnv, "")

	bootstrapOwner(store, mail, "https://heros-agent.space", adoptAt)

	if _, err := store.FindUserByEmail(tenancy.IssuerPassword, "damon@heros-agent.space"); err == nil {
		t.Error("a person was created with no organization named")
	}
	out := logs.String()
	// The message must name all three ways forward — an operator who set the variable expected something.
	for _, want := range []string{BootstrapOwnerTenantEnv, SelfServeSignupEnv, "invite"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q:\n%s", want, out)
		}
	}
}

// 🔴 An existing ACTIVE membership is left alone, whatever its role. Silently promoting a member to owner
// because an environment variable named their address is a privilege change nobody performed.
func TestBootstrapDoesNotPromoteAnExistingMember(t *testing.T) {
	store, mail, logs := bootFixture(t)
	existing, err := store.UpsertUser(tenancy.User{
		Issuer: tenancy.IssuerPassword, Subject: tenancy.PasswordSubject("damon@heros-agent.space"),
		Email: "damon@heros-agent.space", CreatedAt: adoptAt,
	})
	if err != nil {
		t.Fatalf("fixture user: %v", err)
	}
	if _, err := store.PutMembership(tenancy.Membership{
		UserID: existing.UserID, TenantID: "heros", Role: tenancy.RoleMember,
		Status: tenancy.MemberActive, JoinedAt: adoptAt,
	}); err != nil {
		t.Fatalf("fixture membership: %v", err)
	}

	t.Setenv(BootstrapOwnerEnv, "damon@heros-agent.space")
	t.Setenv(BootstrapOwnerTenantEnv, "heros")
	bootstrapOwner(store, mail, "https://heros-agent.space", adoptAt)

	m, err := store.GetMembership(existing.UserID, "heros")
	if err != nil {
		t.Fatalf("membership: %v", err)
	}
	if m.Role != tenancy.RoleMember {
		t.Fatalf("the bootstrap promoted an existing member to %s — that is a privilege change nobody "+
			"performed", m.Role)
	}
	// But it says so, because an operator who expected an owner needs to know they did not get one.
	if !strings.Contains(logs.String(), "left that unchanged") {
		t.Errorf("the non-promotion is silent:\n%s", logs.String())
	}
	// They still get their password link — the person exists and cannot sign in without one.
	if len(mail.Undelivered()) != 1 {
		t.Errorf("no password-set link was issued for a member who has no password")
	}
}

// A boot that died between the two writes must be recoverable: the next one adds the missing membership
// rather than leaving a password identity with nothing to sign in to.
func TestBootstrapRepairsAPersonWithNoMembership(t *testing.T) {
	store, mail, _ := bootFixture(t)
	orphan, err := store.UpsertUser(tenancy.User{
		Issuer: tenancy.IssuerPassword, Subject: tenancy.PasswordSubject("damon@heros-agent.space"),
		Email: "damon@heros-agent.space", CreatedAt: adoptAt,
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	t.Setenv(BootstrapOwnerEnv, "damon@heros-agent.space")
	t.Setenv(BootstrapOwnerTenantEnv, "heros")
	bootstrapOwner(store, mail, "https://heros-agent.space", adoptAt)

	m, err := store.GetMembership(orphan.UserID, "heros")
	if err != nil || !m.Active() || m.Role != tenancy.RoleOwner {
		t.Fatalf("the missing membership was not repaired: %+v / %v", m, err)
	}
}

// The link reaches a real send path when one is configured, and the act does not depend on it.
func TestBootstrapSurvivesAMailFailure(t *testing.T) {
	store, _, _ := bootFixture(t)
	t.Setenv(BootstrapOwnerEnv, "damon@heros-agent.space")
	t.Setenv(BootstrapOwnerTenantEnv, "heros")

	bootstrapOwner(store, failingMailer{}, "https://heros-agent.space", adoptAt)

	// 🔴 The adoption STANDS. Rolling back an owner grant because a mail server was down would leave the
	// organization with no way in over an outage that is ours — and the token is in the store, so a second
	// boot re-sends rather than re-mints.
	user, err := store.FindUserByEmail(tenancy.IssuerPassword, "damon@heros-agent.space")
	if err != nil {
		t.Fatalf("a mail failure undid the adoption: %v", err)
	}
	if m, merr := store.GetMembership(user.UserID, "heros"); merr != nil || !m.Active() {
		t.Fatalf("a mail failure undid the membership: %+v / %v", m, merr)
	}
}

type failingMailer struct{}

func (failingMailer) Send(context.Context, mailer.Message) error { return errContext }
func (failingMailer) Configured() bool                           { return true }
func (failingMailer) From() string                               { return "support@heros-agent.space" }

var errContext = context.DeadlineExceeded
