package tenancy

import (
	"strings"
	"testing"
)

// TestMemStoreSatisfiesTheStoreContract runs the shared suite against the in-memory implementation.
// `store_pg_pgproof_test.go` runs the identical function against Postgres.
func TestMemStoreSatisfiesTheStoreContract(t *testing.T) {
	storeSuite(t, func(t *testing.T) Store { return NewMemStore() })
}

// TestASecretIsNeverRecoverableFromAStoredCredential is the shape of the guarantee rather than the
// guarantee itself: the type has no field a plaintext fits in, so "accidentally log the credential" is
// not a mistake this package permits.
func TestASecretIsNeverRecoverableFromAStoredCredential(t *testing.T) {
	s := NewMemStore()
	mustTenant(t, s, "acme", "Acme")
	secret, err := NewCredentialSecret()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	c, err := s.CreateCredential(Credential{
		CredentialID: NewID("cred"), TenantID: "acme", Label: "CI",
		Hash: HashSecret(secret), CreatedAt: t0,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if strings.Contains(c.Hash, secret) {
		t.Fatal("the stored hash contains the plaintext")
	}
	// The listing surface is where a leak would actually reach a screen.
	list, err := s.ListCredentials("acme")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, got := range list {
		if strings.Contains(got.Hash, secret) {
			t.Fatal("a listed credential carries its plaintext")
		}
	}
}

// TestTheSecretIsHighEntropyAndPrefixed. The prefix is for a secret scanner and a human reading a paste;
// the entropy is what makes SHA-256 the right store (see HashSecret's comment).
func TestTheSecretIsHighEntropyAndPrefixed(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s, err := NewCredentialSecret()
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if !strings.HasPrefix(s, SecretPrefix) {
			t.Fatalf("secret is not prefixed: %q", s)
		}
		body := strings.TrimPrefix(s, SecretPrefix)
		if len(body) < 40 {
			t.Fatalf("secret body is only %d chars — that is not 32 bytes of entropy", len(body))
		}
		if seen[s] {
			t.Fatal("two mints produced the same secret")
		}
		seen[s] = true
	}
}

// TestConstantTimeEqualHashHasNoEarlyExit is a behavioural check of the helper, not a timing measurement:
// it asserts the function is total over differing lengths and positions rather than that it is fast.
func TestConstantTimeEqualHashHasNoEarlyExit(t *testing.T) {
	a := HashSecret("one")
	if !ConstantTimeEqualHash(a, HashSecret("one")) {
		t.Error("equal hashes did not compare equal")
	}
	if ConstantTimeEqualHash(a, HashSecret("two")) {
		t.Error("different hashes compared equal")
	}
	if ConstantTimeEqualHash(a, a[:len(a)-1]) {
		t.Error("a prefix compared equal to the whole")
	}
	if ConstantTimeEqualHash("", a) {
		t.Error("empty compared equal")
	}
}

// TestEmailNormalisationIsCaseFoldingAndNothingElse.
//
// Plus-address stripping and dot folding are PROVIDER-SPECIFIC. Applying Gmail's rules to a corporate
// directory silently matches two addresses an organization considers different people — which, on an
// invitation match, admits the wrong person.
func TestEmailNormalisationIsCaseFoldingAndNothingElse(t *testing.T) {
	if got := NormalizeEmail("  Dana.Smith@ACME.com "); got != "dana.smith@acme.com" {
		t.Errorf("case folding and trimming: got %q", got)
	}
	if got := NormalizeEmail("dana+ci@acme.com"); got != "dana+ci@acme.com" {
		t.Errorf("plus addresses must NOT be stripped: got %q", got)
	}
	if got := EmailDomain("Dana@Sub.Acme.COM"); got != "sub.acme.com" {
		t.Errorf("domain: got %q", got)
	}
	if got := EmailDomain("not-an-address"); got != "" {
		t.Errorf("a value with no @ has no domain: got %q", got)
	}
}
