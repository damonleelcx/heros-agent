// Package auth is identity: who somebody is, and the credential that proves it.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// password.go hashes passwords with argon2id.
//
// # 🔴 Why argon2id and not something in the standard library
//
// The standard library has SHA-256, and SHA-256 is exactly wrong here: it is fast, which is the property
// an attacker with a stolen table wants. A password hash must be SLOW and memory-hard, so that guessing
// costs the attacker what it costs the server, once per attempt. argon2id is the current answer and is
// what `x/crypto` provides.
//
// 🚫 Nothing here is hand-rolled. The one part I chose is the parameters, and they are encoded INTO each
// hash so they can be raised later without invalidating anybody's existing password.

// Params are the argon2id cost parameters.
//
// These follow the RFC 9106 second recommended option: 64 MiB, three passes. They are deliberately
// expensive — a login takes tens of milliseconds, which is unnoticeable to a person and ruinous to
// somebody trying billions of guesses.
type Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams are used for new passwords.
func DefaultParams() Params {
	return Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 2, SaltLength: 16, KeyLength: 32}
}

var (
	ErrBadHash       = errors.New("auth: password hash is malformed")
	ErrWrongPassword = errors.New("auth: password does not match")
	ErrWeakPassword  = errors.New("auth: password is too short")
)

// MinPasswordLength is the floor.
//
// 🔴 A length floor and nothing else — no character-class rules. Those push people towards `Passw0rd!`,
// which is short, guessable, and satisfies every rule anybody writes. Length is the property that
// actually costs an attacker.
const MinPasswordLength = 12

// HashPassword returns an encoded argon2id hash carrying its own salt and parameters.
func HashPassword(password string) (string, error) {
	if len([]rune(password)) < MinPasswordLength {
		return "", fmt.Errorf("%w: passwords must be at least %d characters", ErrWeakPassword, MinPasswordLength)
	}
	p := DefaultParams()
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generating salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against an encoded hash.
//
// 🔴 The comparison is constant-time. A byte-by-byte comparison leaks, through timing, how much of the
// hash matched — which is enough to reconstruct it one byte at a time given enough attempts.
func VerifyPassword(password, encoded string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return ErrBadHash
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return ErrBadHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return ErrBadHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return ErrBadHash
	}
	got := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrWrongPassword
	}
	return nil
}
