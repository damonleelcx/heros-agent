package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestAPasswordHashIsSaltedAndSlow.
//
// 🔴 The same password must never produce the same hash. A deterministic hash means one precomputed
// table breaks every account at once, and identical hashes in a leaked dump reveal which users share a
// password — which is enough to prioritise the guessing.
func TestAPasswordHashIsSaltedAndSlow(t *testing.T) {
	const pw = "correct horse battery staple"
	a, err := HashPassword(t.Context(), pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	b, err := HashPassword(t.Context(), pw)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if a == b {
		t.Fatal("the same password produced the same hash; it is unsalted, and one table breaks everyone")
	}
	if !strings.HasPrefix(a, "$argon2id$") {
		t.Errorf("hash is not argon2id: %q", a[:min(20, len(a))])
	}
	if !strings.Contains(a, "m=65536,t=3") {
		t.Errorf("hash does not carry its parameters, so the cost can never be raised: %q", a)
	}
	if err := VerifyPassword(t.Context(), pw, a); err != nil {
		t.Errorf("a correct password did not verify: %v", err)
	}
	if err := VerifyPassword(t.Context(), pw, b); err != nil {
		t.Errorf("a correct password did not verify against the second hash: %v", err)
	}

	// Slow enough to matter. 🔴 Asserted as a FLOOR, because the whole defence is that a guess costs the
	// attacker what it costs the server.
	start := time.Now()
	_ = VerifyPassword(t.Context(), pw, a)
	if d := time.Since(start); d < 5*time.Millisecond {
		t.Errorf("verification took %s; a fast hash is the property an attacker with a stolen table wants", d)
	}
}

// TestAWrongPasswordIsRejected, including near misses.
func TestAWrongPasswordIsRejected(t *testing.T) {
	hash, err := HashPassword(t.Context(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []string{
		"correct horse battery stapl", "correct horse battery staple ",
		"Correct horse battery staple", "", "x",
	} {
		if err := VerifyPassword(t.Context(), wrong, hash); !errors.Is(err, ErrWrongPassword) {
			t.Errorf("%q was accepted (%v)", wrong, err)
		}
	}
}

// TestShortPasswordsAreRefusedAtTheDoor.
//
// 🔴 A length floor and no character-class rules. Those push people towards `Passw0rd!` — short,
// guessable, and satisfying every rule anybody writes. Length is what actually costs an attacker.
func TestShortPasswordsAreRefusedAtTheDoor(t *testing.T) {
	for _, weak := range []string{"", "short", "Passw0rd!", strings.Repeat("a", MinPasswordLength-1)} {
		if _, err := HashPassword(t.Context(), weak); !errors.Is(err, ErrWeakPassword) {
			t.Errorf("%q was accepted as a password", weak)
		}
	}
	if _, err := HashPassword(t.Context(), strings.Repeat("a", MinPasswordLength)); err != nil {
		t.Errorf("a password at the floor was refused: %v", err)
	}
}

// TestAMalformedHashIsNotAPass.
//
// 🔴 Every malformed shape must be an ERROR, never a match. A verifier that returns nil on a hash it
// cannot parse turns a corrupted or truncated column into a universal password.
func TestAMalformedHashIsNotAPass(t *testing.T) {
	for name, bad := range map[string]string{
		"empty":          "",
		"not argon":      "$2y$10$abcdefghijklmnopqrstuv",
		"truncated":      "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA",
		"wrong version":  "$argon2id$v=1$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"bad base64":     "$argon2id$v=19$m=65536,t=3,p=2$!!!!$!!!!",
		"missing params": "$argon2id$v=19$$c2FsdA$aGFzaA",
	} {
		if err := VerifyPassword(t.Context(), "anything at all", bad); err == nil {
			t.Errorf("%s: a malformed hash accepted a password — it is a universal login", name)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
