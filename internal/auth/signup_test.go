package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/heros/internal/tenancy"
)

// 🔴 Sign-up creates an organization the person OWNS. Anything less and the organization is locked out
// of its own administration from the moment it exists.
func TestSignUpCreatesAnOrganizationTheSignerOwns(t *testing.T) {
	s := NewStore(testDB(t))
	ctx := context.Background()
	email := unique("founder")

	tok, p, err := s.SignUp(ctx, "Acme Robotics", email, "a-long-enough-password")
	if err != nil {
		t.Fatalf("sign up: %v", err)
	}
	if tok == "" {
		t.Error("no session token was issued, so the person would have to sign in immediately after signing up")
	}
	if p.Role != tenancy.Owner {
		t.Errorf("the founding account is %q, not owner — nobody could administer this organization", p.Role)
	}
	if !strings.HasPrefix(p.Tenant, "org_") {
		t.Errorf("organization id is %q, expected an org_ id", p.Tenant)
	}

	// The session must actually authenticate, into that organization.
	got, err := s.Authenticate(ctx, tok)
	if err != nil {
		t.Fatalf("the issued session does not authenticate: %v", err)
	}
	if got.Tenant != p.Tenant {
		t.Errorf("the session authenticates into %q, not the organization just created (%q)", got.Tenant, p.Tenant)
	}
}

// 🔴 The address is what resolves the organization at sign-in, so it has to be unique across the
// deployment — not merely within one organization, which is what the schema enforced before.
func TestASecondSignUpWithTheSameAddressIsRefused(t *testing.T) {
	s := NewStore(testDB(t))
	ctx := context.Background()
	email := unique("taken")

	if _, _, err := s.SignUp(ctx, "First Org", email, "a-long-enough-password"); err != nil {
		t.Fatalf("first sign up: %v", err)
	}
	_, _, err := s.SignUp(ctx, "Second Org", email, "a-different-password")
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("second sign up with the same address returned %v, want ErrEmailTaken.\n"+
			"  Without this the same address exists in two organizations and sign-in cannot say which "+
			"one it is for — the whole basis of resolving an organization from an address.", err)
	}
}

// 🔴 A failed sign-up must leave NOTHING. A tenant with no owner is an organization nobody can enter,
// holding an address that can never be used to sign up again — unreachable and unrecoverable.
func TestAFailedSignUpLeavesNoOrganizationBehind(t *testing.T) {
	db := testDB(t)
	s := NewStore(db)
	ctx := context.Background()
	email := unique("orphan")

	if _, _, err := s.SignUp(ctx, "Original", email, "a-long-enough-password"); err != nil {
		t.Fatalf("first sign up: %v", err)
	}
	var before int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tenants`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// This one fails on the duplicate address, AFTER its tenant row has been inserted.
	if _, _, err := s.SignUp(ctx, "Doomed", email, "a-long-enough-password"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("expected ErrEmailTaken, got %v", err)
	}
	var after int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tenants`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("a failed sign-up left %d organization(s) behind. The tenant row is written before the "+
			"user row, so without one transaction a duplicate address creates an organization nobody "+
			"can ever enter", after-before)
	}
}

// 🔴 Sign-in with no organization named resolves it from the address. This is the behaviour the whole
// migration exists for; without it a person in any organization but the boot one cannot sign in at all.
func TestSignInResolvesTheOrganizationFromTheAddress(t *testing.T) {
	s := NewStore(testDB(t))
	ctx := context.Background()
	aEmail, bEmail := unique("a-founder"), unique("b-founder")

	_, a, err := s.SignUp(ctx, "Org A", aEmail, "a-long-enough-password")
	if err != nil {
		t.Fatalf("sign up A: %v", err)
	}
	_, b, err := s.SignUp(ctx, "Org B", bEmail, "another-long-password")
	if err != nil {
		t.Fatalf("sign up B: %v", err)
	}
	if a.Tenant == b.Tenant {
		t.Fatal("two sign-ups landed in the same organization")
	}

	// Empty tenant — exactly what the sign-in form sends.
	_, gotA, err := s.Login(ctx, "", aEmail, "a-long-enough-password")
	if err != nil {
		t.Fatalf("A could not sign in: %v", err)
	}
	if gotA.Tenant != a.Tenant {
		t.Errorf("A signed in to %q, expected %q", gotA.Tenant, a.Tenant)
	}
	_, gotB, err := s.Login(ctx, "", bEmail, "another-long-password")
	if err != nil {
		t.Fatalf("B could not sign in: %v", err)
	}
	if gotB.Tenant != b.Tenant {
		t.Errorf("B signed in to %q, expected %q — sign-in resolved the wrong organization, which is "+
			"cross-organization access, not a routing inconvenience", gotB.Tenant, b.Tenant)
	}
}

// 🔴 A wrong password must not resolve an organization either. The constant answer that hides whether
// an address exists is only worth having if it also hides which organization the address is in.
func TestAWrongPasswordRevealsNothingAboutTheOrganization(t *testing.T) {
	s := NewStore(testDB(t))
	ctx := context.Background()
	email := unique("careful")
	if _, _, err := s.SignUp(ctx, "Careful Org", email, "a-long-enough-password"); err != nil {
		t.Fatalf("sign up: %v", err)
	}
	_, p, err := s.Login(ctx, "", email, "the-wrong-password-entirely")
	if !errors.Is(err, ErrNoSuchUser) {
		t.Fatalf("wrong password returned %v, want ErrNoSuchUser", err)
	}
	if p.Tenant != "" {
		t.Errorf("a failed sign-in returned organization %q; it must return nothing", p.Tenant)
	}
	// And an address that does not exist gives the identical error.
	if _, _, err := s.Login(ctx, "", unique("ghost"), "any-password-at-all"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("an unknown address returned %v, want the same ErrNoSuchUser a wrong password gives", err)
	}
}

// 🔴 Password reset resolves the organization the same way. It used to be pinned to the boot
// organization, so after self-serve sign-up every other organization's reset would silently find
// nothing — and the endpoint answers "a link is on its way" either way, so nobody would learn.
func TestPasswordResetResolvesTheOrganizationFromTheAddress(t *testing.T) {
	s := NewStore(testDB(t))
	ctx := context.Background()
	email := unique("resetter")
	if _, _, err := s.SignUp(ctx, "Reset Org", email, "a-long-enough-password"); err != nil {
		t.Fatalf("sign up: %v", err)
	}
	tok, to, org, err := s.CreatePasswordReset(ctx, "", email)
	if err != nil {
		t.Fatalf("reset with no organization named: %v", err)
	}
	if tok == "" || to == "" {
		t.Error("no token or address came back")
	}
	if org != "Reset Org" {
		t.Errorf("the reset names organization %q, expected %q — the mail would tell the person the "+
			"wrong organization", org, "Reset Org")
	}
}

func TestOrgNameIsRequiredAndBounded(t *testing.T) {
	s := NewStore(testDB(t))
	ctx := context.Background()
	if _, _, err := s.SignUp(ctx, "   ", unique("noname"), "a-long-enough-password"); !errors.Is(err, ErrOrgNameRequired) {
		t.Errorf("a blank organization name returned %v, want ErrOrgNameRequired", err)
	}
	long := strings.Repeat("x", MaxOrgNameLength+1)
	if _, _, err := s.SignUp(ctx, long, unique("longname"), "a-long-enough-password"); !errors.Is(err, ErrOrgNameRequired) {
		t.Errorf("an over-long organization name returned %v, want a refusal", err)
	}
}

func TestAShortPasswordIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	db := testDB(t)
	s := NewStore(db)
	ctx := context.Background()
	var before int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tenants`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SignUp(ctx, "Weak Org", unique("weak"), "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("a short password returned %v, want ErrWeakPassword", err)
	}
	var after int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tenants`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Error("a refused password still created an organization")
	}
}

// unique keeps these tests independent of each other and of whatever the database already holds — the
// address is now globally unique, so a fixed literal would make the second run of any test fail.
func unique(prefix string) string { return prefix + "-" + randomID() + "@example.test" }

// 🔴 The loaded repository must survive a restart. It lived only in a Go map on the server, so every
// deploy silently emptied it for every organization — the header went blank and the next question was
// refused for having no subject, exactly as though nobody had ever loaded one.
func TestTheLoadedRepositoryIsRememberedAcrossRestarts(t *testing.T) {
	s := NewStore(testDB(t))
	ctx := context.Background()
	_, p, err := s.SignUp(ctx, "Recall Org", unique("recall"), "a-long-enough-password")
	if err != nil {
		t.Fatalf("sign up: %v", err)
	}

	// Nothing loaded yet is a normal state, not a fault.
	ref, rev, err := s.RememberedSubject(ctx, p.Tenant)
	if err != nil {
		t.Fatalf("recalling before anything was loaded: %v", err)
	}
	if ref != "" || rev != "" {
		t.Errorf("a fresh organization already remembers %q@%q", ref, rev)
	}

	if err := s.RememberSubject(ctx, p.Tenant, "github.com/acme/bot", "abc1234"); err != nil {
		t.Fatalf("remembering: %v", err)
	}
	ref, rev, err = s.RememberedSubject(ctx, p.Tenant)
	if err != nil {
		t.Fatalf("recalling: %v", err)
	}
	if ref != "github.com/acme/bot" || rev != "abc1234" {
		t.Errorf("recalled %q@%q, want github.com/acme/bot@abc1234", ref, rev)
	}

	// Loading a different repository replaces it rather than accumulating.
	if err := s.RememberSubject(ctx, p.Tenant, "github.com/acme/other", "def5678"); err != nil {
		t.Fatalf("re-remembering: %v", err)
	}
	ref, _, _ = s.RememberedSubject(ctx, p.Tenant)
	if ref != "github.com/acme/other" {
		t.Errorf("after loading a second repository the organization remembers %q", ref)
	}

	// 🔴 Scoped to the organization. Another organization must not see it.
	_, q, err := s.SignUp(ctx, "Other Org", unique("other"), "a-long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	if r2, _, _ := s.RememberedSubject(ctx, q.Tenant); r2 != "" {
		t.Errorf("a different organization sees %q — the remembered subject leaks across tenants", r2)
	}
}
