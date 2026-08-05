package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/tenancy"
)

var seedAt = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// fixture builds a store holding one organization, one person, one personal credential and one machine
// credential, and returns the two plaintexts.
func fixture(t *testing.T) (*tenancy.MemStore, string, string, tenancy.User) {
	t.Helper()
	store := tenancy.NewMemStore()
	if _, err := store.CreateTenant(tenancy.Tenant{TenantID: "acme", Name: "Acme", CreatedAt: seedAt}); err != nil {
		t.Fatalf("tenant: %v", err)
	}
	u, err := store.UpsertUser(tenancy.User{Issuer: "https://idp", Subject: "sub-1", Email: "dana@acme.com", CreatedAt: seedAt})
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := store.PutMembership(tenancy.Membership{
		UserID: u.UserID, TenantID: "acme", Role: tenancy.RoleOwner, JoinedAt: seedAt,
	}); err != nil {
		t.Fatalf("membership: %v", err)
	}

	personal, _ := tenancy.NewCredentialSecret()
	if _, err := store.CreateCredential(tenancy.Credential{
		CredentialID: "cred_personal", TenantID: "acme", UserID: u.UserID, Label: "dana's laptop",
		Role: tenancy.RoleOwner, Hash: tenancy.HashSecret(personal), CreatedAt: seedAt,
	}); err != nil {
		t.Fatalf("personal credential: %v", err)
	}
	machine, _ := tenancy.NewCredentialSecret()
	if _, err := store.CreateCredential(tenancy.Credential{
		CredentialID: "cred_machine", TenantID: "acme", Label: "CI",
		Role: tenancy.RoleMember, Hash: tenancy.HashSecret(machine), CreatedAt: seedAt,
	}); err != nil {
		t.Fatalf("machine credential: %v", err)
	}
	return store, personal, machine, u
}

func durableRegistry(t *testing.T, store *tenancy.MemStore) *Registry {
	t.Helper()
	return NewRegistry(config.Config{}).WithSource(store)
}

// TestTheDurableRegistryResolvesAPersonAndAMachine.
func TestTheDurableRegistryResolvesAPersonAndAMachine(t *testing.T) {
	store, personal, machine, u := fixture(t)
	reg := durableRegistry(t, store)

	p, ok := reg.Lookup(personal)
	if !ok {
		t.Fatal("a live personal credential was refused")
	}
	if p.TenantID != "acme" || p.UserID != u.UserID || p.APIKeyID != "cred_personal" {
		t.Fatalf("wrong principal: %+v", p)
	}
	if !p.Personal() {
		t.Error("a credential carrying a user must resolve to a personal principal")
	}
	if p.Role != string(tenancy.RoleOwner) {
		t.Errorf("role did not survive: %q", p.Role)
	}

	m, ok := reg.Lookup(machine)
	if !ok {
		t.Fatal("a live machine credential was refused")
	}
	if m.UserID != "" {
		t.Errorf("a machine credential named a person (%q) — never a placeholder", m.UserID)
	}
	if m.Personal() {
		t.Error("a machine credential reported itself personal")
	}
}

// TestRevocationIsEffectiveOnTheVeryNextRequest.
//
// 🔴 This is the assertion NFR1's cache would have broken. The credential is used SUCCESSFULLY several
// times first, deliberately: a version of this test that revokes a cold credential passes against an
// implementation that caches accepts for a minute, which is exactly the implementation this test exists
// to forbid.
func TestRevocationIsEffectiveOnTheVeryNextRequest(t *testing.T) {
	store, personal, _, _ := fixture(t)
	reg := durableRegistry(t, store)

	// Warm every path a cache could plausibly live in.
	for i := 0; i < 25; i++ {
		if _, ok := reg.Lookup(personal); !ok {
			t.Fatalf("warm-up request %d was refused", i)
		}
	}

	if _, err := store.RevokeCredential("cred_personal", seedAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if _, ok := reg.Lookup(personal); ok {
		t.Fatal("a revoked credential was accepted on the request immediately after revocation.\n" +
			"There must be no positive-result cache: a cached accept IS a cached non-revocation, and " +
			"\"refused at the next request\" quietly becomes \"refused within a minute\".")
	}
	if _, cause := reg.LookupWithCause(personal); cause != RefusalRevoked {
		t.Errorf("our own logs should say revoked, got %q", cause)
	}
}

// TestRemovingAMemberEndsTheirCredentialAtTheNextRequest is the offboarding claim, end to end through
// the registry rather than through the store.
func TestRemovingAMemberEndsTheirCredentialAtTheNextRequest(t *testing.T) {
	store, personal, machine, u := fixture(t)
	// A second owner, so the removal is not refused as last-owner.
	second, _ := store.UpsertUser(tenancy.User{Issuer: "https://idp", Subject: "sub-2", Email: "sam@acme.com", CreatedAt: seedAt})
	if _, err := store.PutMembership(tenancy.Membership{
		UserID: second.UserID, TenantID: "acme", Role: tenancy.RoleOwner, JoinedAt: seedAt,
	}); err != nil {
		t.Fatalf("second owner: %v", err)
	}
	reg := durableRegistry(t, store)

	if _, ok := reg.Lookup(personal); !ok {
		t.Fatal("setup: the personal credential should work")
	}
	if _, err := store.RemoveMember(u.UserID, "acme", seedAt); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if _, ok := reg.Lookup(personal); ok {
		t.Error("the removed person's credential still authenticates")
	}
	if _, ok := reg.Lookup(machine); !ok {
		t.Error("removing a person revoked the organization's machine credential — that breaks the " +
			"customer's build, and the removal screen promised it would not")
	}
}

// TestASuspendedOrganizationIsRefusedAtAuthentication is 3.6: once, not per feature.
func TestASuspendedOrganizationIsRefusedAtAuthentication(t *testing.T) {
	store, personal, machine, _ := fixture(t)
	reg := durableRegistry(t, store)

	if _, err := store.SetTenantStatus("acme", tenancy.StatusSuspended, seedAt); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if _, ok := reg.Lookup(personal); ok {
		t.Error("a suspended organization's credential still authenticates")
	}
	if _, ok := reg.Lookup(machine); ok {
		t.Error("a suspended organization's machine credential still authenticates")
	}
	if _, cause := reg.LookupWithCause(personal); cause != RefusalSuspended {
		t.Errorf("our own logs should distinguish suspension, got %q", cause)
	}

	if _, err := store.SetTenantStatus("acme", tenancy.StatusActive, seedAt); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if _, ok := reg.Lookup(personal); !ok {
		t.Error("reactivation did not restore access")
	}
}

// TestEveryRefusalIsIndistinguishableOnTheWire.
//
// Unknown, revoked and suspended are three causes and ONE answer. The distinction lives in our logs,
// where an operator can read it and an attacker cannot — the same split `federation.ts` already makes
// for identity refusals.
func TestEveryRefusalIsIndistinguishableOnTheWire(t *testing.T) {
	store, personal, _, _ := fixture(t)
	reg := durableRegistry(t, store)

	unknown, _ := reg.Lookup("heros_definitely-not-a-real-credential")
	if unknown != (Principal{}) {
		t.Error("an unknown credential produced a non-empty principal")
	}

	if _, err := store.RevokeCredential("cred_personal", seedAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revoked, revokedOK := reg.Lookup(personal)
	unknownAgain, unknownOK := reg.Lookup("heros_still-not-real")
	if revokedOK || unknownOK {
		t.Fatal("a refused lookup reported success")
	}
	if revoked != unknownAgain {
		t.Error("a revoked credential and an unknown one produced different principals — that is a " +
			"probing oracle: it tells an attacker which half they got wrong")
	}
}

// TestAnUnreachableStoreIsNotAnUnknownCredential.
//
// Collapsing them makes an outage indistinguishable from a bad key in our own logs, and sends every
// customer to check their credentials during an incident that has nothing to do with them.
func TestAnUnreachableStoreIsNotAnUnknownCredential(t *testing.T) {
	reg := NewRegistry(config.Config{}).WithSource(brokenSource{})
	p, cause := reg.LookupWithCause("heros_anything")
	if cause != RefusalStoreDown {
		t.Fatalf("want RefusalStoreDown, got %q", cause)
	}
	if p != (Principal{}) {
		t.Error("a store outage must not produce a principal")
	}
	// And it still fails CLOSED on the wire.
	if _, ok := reg.Lookup("heros_anything"); ok {
		t.Error("a store outage authenticated a request")
	}
}

type brokenSource struct{}

func (brokenSource) ResolveCredential(string) (tenancy.Credential, error) {
	return tenancy.Credential{}, errors.New("connection refused")
}
func (brokenSource) GetTenant(string) (tenancy.Tenant, error) {
	return tenancy.Tenant{}, errors.New("connection refused")
}
func (brokenSource) ResolveSession(string) (tenancy.Session, error) {
	return tenancy.Session{}, errors.New("connection refused")
}

// TestTheConfigurationPathIsUnchangedWhenNoSourceIsWired: every existing deployment and every existing
// test constructs a Registry this way, and P27 must not have moved it.
func TestTheConfigurationPathIsUnchangedWhenNoSourceIsWired(t *testing.T) {
	reg := NewRegistry(config.Config{TenantCredentials: []config.TenantCredential{
		{TenantID: "acme", APIKey: "static-key", Role: "admin", KeyID: "k1"},
	}})
	if reg.HasSource() {
		t.Fatal("a configuration-only registry reported a durable source")
	}
	p, ok := reg.Lookup("static-key")
	if !ok || p.TenantID != "acme" || p.Role != "admin" || p.APIKeyID != "k1" {
		t.Fatalf("the configuration path changed: %+v ok=%v", p, ok)
	}
	if p.UserID != "" {
		t.Error("a configured credential belongs to the organization, not to a person")
	}
	if _, ok := reg.Lookup("wrong"); ok {
		t.Error("an unknown key was accepted")
	}
}

// TestTheSeedIsCreateIfAbsentAndIdempotent is D4, at the level the store can prove it.
func TestTheSeedIsCreateIfAbsentAndIdempotent(t *testing.T) {
	store := tenancy.NewMemStore()
	entries := []tenancy.SeedEntry{
		{TenantID: "acme", APIKey: "acme-key", Role: "admin", KeyID: "k1"},
		{TenantID: "globex", APIKey: "globex-key", Role: "member", KeyID: "k2"},
	}

	first, err := tenancy.Seed(store, entries, seedAt)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if first.TenantsCreated != 2 || first.CredentialsCreated != 2 {
		t.Fatalf("first seed: %+v", first)
	}

	// A tenant created at RUNTIME, which configuration knows nothing about.
	if _, err := store.CreateTenant(tenancy.Tenant{TenantID: "runtime", Name: "Signed up at 02:00", CreatedAt: seedAt}); err != nil {
		t.Fatalf("runtime tenant: %v", err)
	}

	second, err := tenancy.Seed(store, entries, seedAt)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if second.TenantsCreated != 0 || second.CredentialsCreated != 0 {
		t.Errorf("the second seed wrote something: %+v", second)
	}
	if second.TenantsExisting != 2 || second.CredentialsPresent != 2 {
		t.Errorf("the second seed did not recognise what was already there: %+v", second)
	}

	// 🔴 The runtime tenant must still be there. The opposite reading of this file deletes it.
	if _, err := store.GetTenant("runtime"); err != nil {
		t.Fatal("the seed deleted an organization created at runtime — a customer who signed up at " +
			"02:00 would be gone after the 03:00 restart")
	}

	// And configured keys authenticate through the durable path afterwards.
	reg := NewRegistry(config.Config{}).WithSource(store)
	p, ok := reg.Lookup("acme-key")
	if !ok || p.TenantID != "acme" || p.Role != "admin" {
		t.Fatalf("a configured key stopped working after the seed: %+v ok=%v", p, ok)
	}
	if p.UserID != "" {
		t.Error("a configured credential must be a MACHINE credential — it belongs to nobody")
	}
}

// TestTheSeedNeverOverwritesAnExistingRow: an operator renames an organization in the console, and the
// next restart must not put the id back.
func TestTheSeedNeverOverwritesAnExistingRow(t *testing.T) {
	store := tenancy.NewMemStore()
	entries := []tenancy.SeedEntry{{TenantID: "acme", APIKey: "acme-key", Role: "admin", KeyID: "k1"}}
	if _, err := tenancy.Seed(store, entries, seedAt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Simulate the organization being renamed and suspended after the first boot.
	if _, err := store.SetTenantStatus("acme", tenancy.StatusSuspended, seedAt); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if _, err := tenancy.Seed(store, entries, seedAt); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	got, err := store.GetTenant("acme")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Status.Suspended() {
		t.Error("the seed reactivated a suspended organization — configuration creates what is missing " +
			"and corrects nothing")
	}
}

// TestASeedFailureIsReportedRatherThanSkipped: an entry that cannot become a row is NAMED. A credential
// somebody believes they configured and that silently does not work is a support call with no evidence.
func TestASeedFailureIsReportedRatherThanSkipped(t *testing.T) {
	store := tenancy.NewMemStore()
	res, err := tenancy.Seed(store, []tenancy.SeedEntry{
		{TenantID: "acme", APIKey: "acme-key", Role: "admin", KeyID: "k1"},
		{TenantID: "", APIKey: "orphan-key", KeyID: "k2"},
		{TenantID: "globex", APIKey: "", KeyID: "k3"},
	}, seedAt)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("expected two named skips, got %v", res.Skipped)
	}
	if res.Total() != 3 {
		t.Errorf("Total() should account for every configured entry, got %d", res.Total())
	}

	// An unknown role is a hard failure, not a silent downgrade: seeding it as `member` would grant
	// less authority than the operator believes they configured.
	if _, err := tenancy.Seed(store, []tenancy.SeedEntry{
		{TenantID: "zenith", APIKey: "zenith-key", Role: "superadmin", KeyID: "k4"},
	}, seedAt); err == nil {
		t.Error("a configured credential with an unknown role was seeded anyway")
	}
}

// TestAScopedTokenResolvesToItsOrganizationAndExpires is the console's half of the isolation fix, at the
// registry: the token carries the organization, so `auth` derives scope from a value the platform
// verified rather than from a header it used to ignore.
func TestAScopedTokenResolvesToItsOrganizationAndExpires(t *testing.T) {
	store, _, _, u := fixture(t)
	reg := durableRegistry(t, store)

	secret, _ := tenancy.NewCredentialSecret()
	if _, err := store.CreateSession(tenancy.Session{
		TokenHash: tenancy.HashSecret(secret), SessionID: "tok_1",
		TenantID: "acme", UserID: u.UserID,
		IssuedAt: time.Now().UTC().UnixMilli(), ExpiresAt: time.Now().UTC().Add(10 * time.Minute).UnixMilli(),
	}); err != nil {
		t.Fatalf("mint token: %v", err)
	}

	p, ok := reg.Lookup(secret)
	if !ok {
		t.Fatal("a live scoped token was refused")
	}
	if p.TenantID != "acme" || p.UserID != u.UserID {
		t.Fatalf("wrong principal: %+v", p)
	}

	// An EXPIRED token is refused, and the refusal is indistinguishable on the wire from an unknown one.
	expired, _ := tenancy.NewCredentialSecret()
	if _, err := store.CreateSession(tenancy.Session{
		TokenHash: tenancy.HashSecret(expired), SessionID: "tok_old",
		TenantID: "acme", UserID: u.UserID,
		IssuedAt: 1, ExpiresAt: 2,
	}); err != nil {
		t.Fatalf("mint expired token: %v", err)
	}
	if _, ok := reg.Lookup(expired); ok {
		t.Error("an expired scoped token still authenticates")
	}
}

// TestRemovingAMemberKillsTheirScopedTokens.
//
// 🔴 This is why the scoped token is a SESSION row rather than a second token mechanism. `RemoveMember`
// already revokes sessions, so a removed member's console token dies with them — with no new code path
// that somebody has to remember to add, and therefore no path that can be forgotten.
func TestRemovingAMemberKillsTheirScopedTokens(t *testing.T) {
	store, _, _, u := fixture(t)
	second, _ := store.UpsertUser(tenancy.User{Issuer: "https://idp", Subject: "sub-2", Email: "sam@acme.com", CreatedAt: seedAt})
	if _, err := store.PutMembership(tenancy.Membership{
		UserID: second.UserID, TenantID: "acme", Role: tenancy.RoleOwner, JoinedAt: seedAt,
	}); err != nil {
		t.Fatalf("second owner: %v", err)
	}
	reg := durableRegistry(t, store)

	secret, _ := tenancy.NewCredentialSecret()
	if _, err := store.CreateSession(tenancy.Session{
		TokenHash: tenancy.HashSecret(secret), SessionID: "tok_1",
		TenantID: "acme", UserID: u.UserID,
		IssuedAt: time.Now().UTC().UnixMilli(), ExpiresAt: time.Now().UTC().Add(10 * time.Minute).UnixMilli(),
	}); err != nil {
		t.Fatalf("mint token: %v", err)
	}
	if _, ok := reg.Lookup(secret); !ok {
		t.Fatal("setup: the token should work")
	}

	if _, err := store.RemoveMember(u.UserID, "acme", seedAt); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := reg.Lookup(secret); ok {
		t.Fatal("a removed member's console token still authenticates — their browser would keep working " +
			"until it expired, and the offboarding claim would be false in the one place a person actually is")
	}
}

// TestTheDeviceFlowStartsWithoutACredentialAndApprovalStillDoesNot is P27 §13 at the MIDDLEWARE.
//
// 🔴 This is the layer every device test was running underneath. They drive `s.Mux`; `auth.Compose`
// wraps `s.Handler` one layer further out and gates everything under `/api/`. So on a deployment with
// `auth_mode=required` — every real one — `heros login` got a flat 401 from the middleware and the
// handler it needed never ran, while the whole live pgproof walk stayed green.
//
// Found by running the actual CLI against a real local deployment. The lesson is the one this repository
// keeps relearning: a test that calls beneath a layer proves nothing about the layer.
func TestTheDeviceFlowStartsWithoutACredentialAndApprovalStillDoesNot(t *testing.T) {
	// The two steps that happen BEFORE the caller has anything to authenticate with.
	for _, path := range []string{"/api/v1/device/authorize", "/api/v1/device/token"} {
		if !IsPublicPath(path) {
			t.Errorf("%s requires authentication.\n"+
				"The CLI has no credential at this point — obtaining one is what it is asking for. Gating "+
				"it means `heros login` cannot start a device flow on any deployment with auth required, "+
				"which is every real one.", path)
		}
	}

	// 🔴 And the step that MUST stay gated. A prefix rule would have opened this by accident: approving
	// issues a credential that names the approving PERSON, so an unauthenticated approval would mint a
	// personal credential for nobody.
	if IsPublicPath("/api/v1/device/approve") {
		t.Error("/api/v1/device/approve is public. Approving is what turns a code into a credential " +
			"naming a person; it needs the person.")
	}

	// Neighbouring routes stay gated too — the exact-match list must not have become a prefix.
	for _, path := range []string{"/api/v1/device", "/api/v1/device/authorize/extra", "/api/v1/runs"} {
		if IsPublicPath(path) {
			t.Errorf("%s became public; the device exception must be two exact paths and nothing else", path)
		}
	}
}
