package password

import (
	"strings"
	"testing"
)

// Hashing the same password twice must not produce the same stored value — that is what the per-row salt is
// for, and it is the property a leaked table depends on: without it, one cracked hash cracks everybody who
// chose the same password.
func TestHashIsSaltedPerRow(t *testing.T) {
	a, err := Hash("a reasonable passphrase")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	b, err := Hash("a reasonable passphrase")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if a == b {
		t.Fatal("two hashes of the same password are identical — the salt is not per-row")
	}
	for _, enc := range []string{a, b} {
		if !strings.HasPrefix(enc, "$argon2id$") {
			t.Fatalf("stored value is not argon2id-tagged: %q", enc)
		}
		if strings.Contains(enc, "a reasonable passphrase") {
			t.Fatal("the stored value contains the plaintext")
		}
	}
}

func TestVerify(t *testing.T) {
	enc, err := Hash("a reasonable passphrase")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, rehash, err := Verify(enc, "a reasonable passphrase")
	if err != nil || !ok {
		t.Fatalf("correct password refused: ok=%v err=%v", ok, err)
	}
	if rehash {
		t.Error("a value just produced with the current parameters asked to be re-hashed")
	}
	ok, _, err = Verify(enc, "a reasonable passphras")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatal("a wrong password was accepted")
	}
}

// A stored value produced with weaker parameters verifies AND reports that it should be re-hashed. This is what
// makes raising the cost a deploy rather than a migration, so it is asserted rather than assumed.
func TestNeedsRehashOnWeakerParameters(t *testing.T) {
	weak := Params{Memory: 8 * 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}
	enc, err := hashWith("a reasonable passphrase", weak)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, rehash, err := Verify(enc, "a reasonable passphrase")
	if err != nil || !ok {
		t.Fatalf("a password stored with older parameters must still verify: ok=%v err=%v", ok, err)
	}
	if !rehash {
		t.Fatal("a value stored with weaker parameters did not ask to be re-hashed")
	}
	// And the re-hash is verifiable with the current ones.
	fresh, err := Hash("a reasonable passphrase")
	if err != nil {
		t.Fatalf("rehash: %v", err)
	}
	if _, again, _ := Verify(fresh, "a reasonable passphrase"); again {
		t.Error("the re-hashed value still asks to be re-hashed")
	}
}

// The reverse direction is deliberate: a value stored with STRONGER parameters is left alone. A routine
// sign-in must never downgrade somebody's hash.
func TestStrongerStoredParametersAreNotDowngraded(t *testing.T) {
	strong := Params{Memory: Current.Memory * 2, Time: Current.Time + 1, Threads: Current.Threads, KeyLen: 32, SaltLen: 16}
	if NeedsRehash(strong) {
		t.Fatal("a stronger stored parameter set was reported as needing a re-hash — that is a downgrade")
	}
}

func TestParseRejectsForeignEncodings(t *testing.T) {
	// 🔴 The SHA-256 hex `tenancy.HashSecret` produces is the specific thing that must not be mistaken for a
	// password hash. It is not "a hash that does not match"; it is not a password hash at all.
	for _, bad := range []string{
		"",
		"not-an-encoding",
		"5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8", // sha256("password")
		"$argon2i$v=19$m=65536,t=3,p=4$c2FsdHNhbHQ$aGFzaGhhc2g",
		"$argon2id$v=16$m=65536,t=3,p=4$c2FsdHNhbHQ$aGFzaGhhc2g",
		"$argon2id$v=19$m=65536,t=3$c2FsdHNhbHQ$aGFzaGhhc2g",
	} {
		if _, _, err := Verify(bad, "anything"); err == nil {
			t.Errorf("a foreign encoding was accepted as a password hash: %q", bad)
		}
	}
}

// The decoy must do the real work. If it were a constant-time no-op the enumeration oracle closed on the
// response body would be wide open on the clock.
func TestDecoyPerformsRealWork(t *testing.T) {
	if !strings.HasPrefix(decoyEncoding, "$argon2id$") {
		t.Fatalf("the decoy is not a real argon2id encoding: %q", decoyEncoding)
	}
	d, err := parse(decoyEncoding)
	if err != nil {
		t.Fatalf("the decoy does not parse: %v", err)
	}
	if d.params.Memory != Current.Memory || d.params.Time != Current.Time || d.params.Threads != Current.Threads {
		t.Fatalf("the decoy uses different parameters from a real password (%+v vs %+v) — it would cost a "+
			"different amount of time and reintroduce the oracle it exists to close", d.params, Current)
	}
	VerifyDecoy("anything at all") // must not panic and must not be skippable
}

func TestPolicy(t *testing.T) {
	cases := []struct {
		name, pw, email string
		want            error
	}{
		{"below the floor", "short1234", "", ErrTooShort},
		{"exactly the floor", "abzq7mvt2wpl", "", nil},
		{"a passphrase", "a reasonable passphrase", "", nil},
		{"a long common password", "PasswordPassword", "", ErrCommon},
		{"one character repeated", "aaaaaaaaaaaaaa", "", ErrCommon},
		{"a run up the number line", "123456789012", "", ErrCommon},
		{"a run down the alphabet", "zyxwvutsrqpo", "", ErrCommon},
		{"a keyboard row", "qwertyuiop[]", "", ErrCommon},
		{"contains the whole address", "xx priya@example.com xx", "priya@example.com", ErrLikeEmail},
		{"contains the local part", "priya-is-here-ok", "priya@example.com", ErrLikeEmail},
		{"a short local part is not banned", "opsopsimfinewiththis", "ops@example.com", nil},
		{"absurdly long", strings.Repeat("x", MaxLength+1), "", ErrTooLong},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CheckPolicy(c.pw, c.email)
			if got != c.want {
				t.Fatalf("CheckPolicy(%q, %q) = %v, want %v", c.pw, c.email, got, c.want)
			}
		})
	}
}

// The blocklist must actually be embedded and parsed. A file that failed to embed would leave an empty set and
// every check would pass — silently, which is the failure mode worth a test of its own.
func TestBlocklistIsLoaded(t *testing.T) {
	if len(common()) < 100 {
		t.Fatalf("the common-password list holds %d entries — it did not embed", len(common()))
	}
	for entry := range common() {
		if len([]rune(entry)) < MinLength {
			t.Errorf("blocklist entry %q is shorter than the length floor, so it can never be reached", entry)
		}
	}
}
