//go:build pgproof

// The durable half of the identity store, proven against a real Postgres.
//
// It runs the SAME function `memstore_test.go` runs. That is the point: two implementations behind one
// interface are only interchangeable if one suite decides both, and a PGStore-specific test file would
// let the two drift while each stayed green.
//
// Two properties are proven HERE and cannot be proven in memory, because they are properties of
// concurrency against a shared row rather than of the logic:
//
//   - the last-owner refusal survives two concurrent demotions. The tempting implementation — count the
//     owners in Go, then update — passes every serial test and leaves an organization nobody can
//     administer the first time two admins act at once.
//
//   - an invitation is accepted exactly once under concurrency, for the same reason.
//
//     make pg-proof
//     HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/tenancy/
package tenancy

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
)

// openPG applies the real embedded migration set to its own schema, exactly as a booting deployment
// does. It FAILS rather than skips when Postgres is unreachable: a proof that skips itself reports green
// for something it never checked.
func openPG(t *testing.T, schema string) *sql.DB {
	t.Helper()
	db, err := pgtest.Open(schema)
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := pgmigrate.Apply(t.Context(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

// truncate empties the identity tables between suite cases. Order matters: children first.
func truncate(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"device_authorization", "console_session", "api_credential", "invitation", "membership", "platform_user", "tenant"} {
		if _, err := db.Exec(`DELETE FROM ` + table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
}

func TestPGStoreSatisfiesTheStoreContract(t *testing.T) {
	db := openPG(t, "tenancy_suite")
	storeSuite(t, func(t *testing.T) Store {
		truncate(t, db)
		s, err := NewPGStore(db)
		if err != nil {
			t.Fatalf("store: %v", err)
		}
		return s
	})
}

// TestTheLastOwnerRuleSurvivesConcurrentDemotions.
//
// 🔴 This is the assertion the in-memory suite cannot make. Two admins demote the two remaining owners
// at the same moment. A check-then-act implementation lets both succeed — each sees one other owner —
// and the organization is left with nobody who can administer it, which is unrecoverable through the
// product. The refusal has to be evaluated by the database in the same statement as the update.
func TestTheLastOwnerRuleSurvivesConcurrentDemotions(t *testing.T) {
	db := openPG(t, "tenancy_lastowner")
	truncate(t, db)
	s, err := NewPGStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	mustTenant(t, s, "acme", "Acme")
	a := mustUser(t, s, "sub-a", "a@acme.com")
	b := mustUser(t, s, "sub-b", "b@acme.com")
	mustMember(t, s, a.UserID, "acme", RoleOwner)
	mustMember(t, s, b.UserID, "acme", RoleOwner)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, errs[0] = s.SetRole(a.UserID, "acme", RoleMember) }()
	go func() { defer wg.Done(); _, errs[1] = s.SetRole(b.UserID, "acme", RoleMember) }()
	wg.Wait()

	members, err := s.ListMembers("acme")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	owners := 0
	for _, m := range members {
		if m.Active() && m.Role == RoleOwner {
			owners++
		}
	}
	if owners == 0 {
		t.Fatalf("both demotions succeeded and the organization has no owner left "+
			"(errors: %v, %v). The last-owner check must be evaluated by the database in the same "+
			"statement as the update, not counted in Go beforehand.", errs[0], errs[1])
	}
	refused := 0
	for _, e := range errs {
		if errors.Is(e, ErrLastOwner) {
			refused++
		}
	}
	if refused != 1 {
		t.Errorf("expected exactly one refusal with ErrLastOwner, got %d (%v, %v)", refused, errs[0], errs[1])
	}
}

// TestAnInvitationIsAcceptedExactlyOnceUnderConcurrency: same class of bug, different table. Two
// acceptances of one invitation must not both create a membership.
func TestAnInvitationIsAcceptedExactlyOnceUnderConcurrency(t *testing.T) {
	db := openPG(t, "tenancy_invite_race")
	truncate(t, db)
	s, err := NewPGStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	mustTenant(t, s, "acme", "Acme")
	inv, err := s.CreateInvitation(Invitation{
		InvitationID: NewID("inv"), TenantID: "acme", Email: "new@acme.com",
		Role: RoleMember, CreatedAt: t0, ExpiresAt: t0.Add(48 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}

	const attempts = 8
	var wg sync.WaitGroup
	results := make([]error, attempts)
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			_, results[i] = s.AcceptInvitation(inv.InvitationID, t0.Add(time.Hour))
		}(i)
	}
	wg.Wait()

	accepted := 0
	for _, e := range results {
		if e == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("%d of %d concurrent acceptances succeeded; an invitation is single-use", accepted, attempts)
	}
}

// TestRemovalIsAtomicAcrossThreeTables.
//
// The window this closes is small and real: with three separate statements, somebody removed from the
// member list keeps a working key for as long as the second write takes. The proof is indirect — it
// asserts the three effects are all present after one call — because observing the intermediate state
// would require instrumenting the store.
func TestRemovalIsAtomicAcrossThreeTables(t *testing.T) {
	db := openPG(t, "tenancy_removal")
	truncate(t, db)
	s, err := NewPGStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	mustTenant(t, s, "acme", "Acme")
	owner := mustUser(t, s, "sub-owner", "owner@acme.com")
	leaver := mustUser(t, s, "sub-leaver", "leaver@acme.com")
	mustMember(t, s, owner.UserID, "acme", RoleOwner)
	mustMember(t, s, leaver.UserID, "acme", RoleMember)
	cred := mustCredential(t, s, "acme", leaver.UserID, "laptop")
	mustSession(t, s, "acme", leaver.UserID, "sess-leaver")

	if _, err := s.RemoveMember(leaver.UserID, "acme", t0); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Read every effect back through SQL rather than through the store, so a store that reports
	// success while writing nothing is caught.
	var status string
	if err := db.QueryRow(`SELECT status FROM membership WHERE user_id=$1 AND tenant_id='acme'`, leaver.UserID).Scan(&status); err != nil {
		t.Fatalf("read membership: %v", err)
	}
	if status != string(MemberRemoved) {
		t.Errorf("membership status is %q", status)
	}
	var credRevoked sql.NullTime
	if err := db.QueryRow(`SELECT revoked_at FROM api_credential WHERE credential_id=$1`, cred.CredentialID).Scan(&credRevoked); err != nil {
		t.Fatalf("read credential: %v", err)
	}
	if !credRevoked.Valid {
		t.Error("the credential was not revoked in the same transaction")
	}
	var sessRevoked sql.NullInt64
	if err := db.QueryRow(`SELECT revoked_at FROM console_session WHERE user_id=$1`, leaver.UserID).Scan(&sessRevoked); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if !sessRevoked.Valid {
		t.Error("the session was not revoked in the same transaction")
	}
}

// TestTheHashIndexIsUniqueAcrossTheWholeTable: two credentials cannot share a hash, so a resolution can
// never be ambiguous. A collision here would mean one presented secret authenticating as two principals.
func TestTheHashIndexIsUniqueAcrossTheWholeTable(t *testing.T) {
	db := openPG(t, "tenancy_hash_unique")
	truncate(t, db)
	s, err := NewPGStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	mustTenant(t, s, "acme", "Acme")
	mustTenant(t, s, "globex", "Globex")
	h := HashSecret("a-shared-value")
	if _, err := s.CreateCredential(Credential{
		CredentialID: NewID("cred"), TenantID: "acme", Hash: h, CreatedAt: t0,
	}); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err = s.CreateCredential(Credential{
		CredentialID: NewID("cred"), TenantID: "globex", Hash: h, CreatedAt: t0,
	})
	if !errors.Is(err, ErrExists) {
		t.Fatalf("two credentials sharing a hash were accepted across organizations — one presented "+
			"secret would authenticate as two principals. got %v", err)
	}
}

// TestUpsertNeverRewritesTheInternalKey. The federated pair is the identity; the internal id is what
// every other row references. Swapping it on a re-login would orphan them.
func TestUpsertNeverRewritesTheInternalKey(t *testing.T) {
	db := openPG(t, "tenancy_upsert_key")
	truncate(t, db)
	s, err := NewPGStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	first, err := s.UpsertUser(User{Issuer: "https://idp", Subject: "sub-1", Email: "a@acme.com", CreatedAt: t0})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	// A caller that supplies a DIFFERENT candidate id for the same identity must not move the row.
	second, err := s.UpsertUser(User{
		UserID: NewID("usr"), Issuer: "https://idp", Subject: "sub-1", Email: "b@acme.com", CreatedAt: t0,
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if second.UserID != first.UserID {
		t.Fatalf("the internal key moved from %s to %s on a re-login; every membership, credential and "+
			"session naming this person would be orphaned", first.UserID, second.UserID)
	}
	if second.Email != "b@acme.com" {
		t.Errorf("the display address did not refresh: %q", second.Email)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM platform_user`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("re-login created %d user rows", n)
	}
}
