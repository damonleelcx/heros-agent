// Package password is the one place a human-chosen secret is turned into something storable, and the one
// place a stored value is checked against a typed one.
//
// # Why this is not `tenancy.HashSecret`
//
// `tenancy.HashSecret` is SHA-256, and its own comment gives the reason that is correct: it hashes a 256-bit
// value WE minted with crypto/rand, so there is no dictionary to slow down and no user-chosen entropy to
// compensate for. Every clause of that reasoning inverts for a password. A person picks from a space an
// attacker can enumerate, so the only defence a stored hash can offer is to make each guess expensive — which
// is exactly what a fast hash is designed not to do.
//
// So there are two hash functions in this codebase and that is not a split truth source. The rule is one line:
//
//	a value with 256 bits of crypto/rand behind it is looked up by SHA-256 hash;
//	a value a person typed is verified by argon2id.
//
// It is enforced by a test (`password_fence_test.go`) rather than by discipline, and the database holds its own
// copy — `user_password.encoded` carries a CHECK that the stored value is argon2id-tagged, so a code path that
// forgot cannot write past it.
//
// # 🔴 The parameters are IN the stored value
//
// A bare hash makes the cost parameters a global constant, and raising the cost then means either a migration
// nobody can run (the plaintexts are gone) or a permanently weak floor. Tagged, a sign-in that verifies against
// stale parameters re-hashes on the spot — see `NeedsRehash` — so raising the cost is a deploy rather than a
// one-way door.
//
// # What this package does NOT do
//
// It does not touch a store, a request, or a log. It takes strings and returns strings, so that "did a password
// reach a log line" is a question about the caller and never about this file.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params are the argon2id cost parameters.
//
// They are a VALUE rather than a constant because two of them are in play at once on a running deployment: the
// ones a stored encoding was produced with, and the ones we would produce today. `NeedsRehash` is the whole
// reason that distinction has to be expressible.
type Params struct {
	// Memory is the memory cost in KiB. This is the parameter that makes argon2id memory-hard and is the one
	// worth spending on: it is what stops an attacker from buying parallelism instead of time.
	Memory uint32
	// Time is the number of passes.
	Time uint32
	// Threads is the degree of parallelism.
	Threads uint8
	// KeyLen is the derived tag length in bytes.
	KeyLen uint32
	// SaltLen is the per-row salt length in bytes.
	SaltLen uint32
}

// Current is what a new password is hashed with today: 64 MiB, 3 passes, 4 lanes.
//
// These are the OWASP argon2id floor, not a number picked to feel large. 64 MiB × 4 lanes is real memory per
// concurrent verification, and a deployment sizing the platform should know that — it is recorded in ADR-012's
// consequences rather than left to be discovered under load. Sign-in is not a hot path, and `password_identity`
// bounds per-account attempts, so the exposure is bounded from both ends.
var Current = Params{Memory: 64 * 1024, Time: 3, Threads: 4, KeyLen: 32, SaltLen: 16}

// Errors callers branch on. They are distinguishable because the actions differ: a malformed encoding is an
// operator problem (something wrote a value this package did not produce), a mismatch is an ordinary refusal.
var (
	ErrMalformed = errors.New("password: stored value is not a recognised argon2id encoding")
	ErrEmpty     = errors.New("password: empty password")
)

const scheme = "argon2id"

// Hash derives a storable encoding for a plaintext password.
//
// The returned value is safe to store and to log SHAPE-wise, but nothing in this codebase logs it, because a
// stored hash in a log is a stored hash in a log aggregator with a different retention policy.
func Hash(plaintext string) (string, error) { return hashWith(plaintext, Current) }

func hashWith(plaintext string, p Params) (string, error) {
	if plaintext == "" {
		return "", ErrEmpty
	}
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		// 🔴 A failed CSPRNG read is not something to work around with a weaker salt. It is refused, loudly,
		// and the caller turns it into a 500 — a password stored with a predictable salt is worse than a
		// sign-up that failed and can be retried.
		return "", fmt.Errorf("password: cannot draw a salt: %w", err)
	}
	key := argon2.IDKey([]byte(plaintext), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return encode(p, salt, key), nil
}

// encode renders the PHC-style string this package stores.
//
//	$argon2id$v=19$m=65536,t=3,p=4$<b64 salt>$<b64 key>
//
// The format is the de-facto standard one rather than something invented here, so a future migration to another
// implementation reads the same values, and so an operator looking at a row recognises what they are seeing.
func encode(p Params, salt, key []byte) string {
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$%s$v=%d$m=%d,t=%d,p=%d$%s$%s",
		scheme, argon2.Version, p.Memory, p.Time, p.Threads, b64(salt), b64(key))
}

// decoded is a parsed stored value.
type decoded struct {
	params Params
	salt   []byte
	key    []byte
}

// parse reads a stored encoding.
//
// It is strict about the scheme name on purpose: this is the code-side half of the database's CHECK, and a
// value that is not argon2id must not be silently treated as one that happens not to match.
func parse(encoded string) (decoded, error) {
	parts := strings.Split(encoded, "$")
	// "", scheme, version, params, salt, key
	if len(parts) != 6 || parts[0] != "" || parts[1] != scheme {
		return decoded{}, ErrMalformed
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return decoded{}, ErrMalformed
	}
	if version != argon2.Version {
		// A different argon2 version is not a value this build can verify. Refusing is the honest answer;
		// pretending otherwise would silently reject everybody's correct password.
		return decoded{}, fmt.Errorf("%w: unsupported argon2 version %d", ErrMalformed, version)
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return decoded{}, ErrMalformed
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return decoded{}, ErrMalformed
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return decoded{}, ErrMalformed
	}
	p.SaltLen, p.KeyLen = uint32(len(salt)), uint32(len(key))
	return decoded{params: p, salt: salt, key: key}, nil
}

// Verify checks a plaintext against a stored encoding.
//
// It returns THREE things and the third is the interesting one: `rehash` reports that the stored value is
// correct but was produced with parameters we no longer use. The caller re-hashes on success, which is what
// makes raising the cost a deploy instead of a migration.
//
// The comparison is constant-time. That matters less here than it does for a token lookup — the attacker
// already had to spend an argon2id computation to get here — but a variable-time compare on a secret is the
// kind of thing a reviewer should never have to think twice about.
func Verify(encoded, plaintext string) (ok bool, rehash bool, err error) {
	d, err := parse(encoded)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(plaintext), d.salt, d.params.Time, d.params.Memory, d.params.Threads, d.params.KeyLen)
	if subtle.ConstantTimeCompare(got, d.key) != 1 {
		return false, false, nil
	}
	return true, NeedsRehash(d.params), nil
}

// NeedsRehash reports whether a stored value's parameters are weaker than what we produce today.
//
// It is one-directional deliberately: a value produced with STRONGER parameters than Current is left alone.
// Lowering the cost for somebody who already has a more expensive hash would be a downgrade performed by a
// routine sign-in, which is the shape of change nobody would notice.
func NeedsRehash(stored Params) bool {
	return stored.Memory < Current.Memory ||
		stored.Time < Current.Time ||
		stored.Threads < Current.Threads ||
		stored.KeyLen < Current.KeyLen ||
		stored.SaltLen < Current.SaltLen
}

// decoyEncoding is a real argon2id encoding of a value nobody knows, computed once at init.
//
// 🔴 It exists so a sign-in for an address that does not exist costs the same as one for an address that does.
// Without it the account-enumeration oracle that is carefully closed on the response body — one message for
// "no such address" and "wrong password" — is wide open on the clock: the unknown address returns in
// microseconds and the known one in ~50ms, which is not a subtle difference and does not need a lab to measure.
var decoyEncoding string

func init() {
	// 32 bytes of crypto/rand as the "password". Nobody, including this process, can present a plaintext that
	// verifies against it, so `VerifyDecoy` is guaranteed to do the full work and return false.
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A process that cannot read the CSPRNG at startup cannot mint credentials either. Failing here, at
		// boot, is louder than discovering it at the first sign-up.
		panic("password: cannot initialise the timing decoy: " + err.Error())
	}
	enc, err := hashWith(base64.RawStdEncoding.EncodeToString(b[:]), Current)
	if err != nil {
		panic("password: cannot initialise the timing decoy: " + err.Error())
	}
	decoyEncoding = enc
}

// VerifyDecoy performs a full argon2id verification that always fails.
//
// The caller uses it on the "no such user" branch so that branch costs what the real one costs. It returns
// nothing, because a caller that could branch on its result would have re-created the timing difference in the
// control flow instead of the clock.
func VerifyDecoy(plaintext string) {
	_, _, _ = Verify(decoyEncoding, plaintext)
}
