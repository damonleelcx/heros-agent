package adminidentity

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

// challenge.go mints the WebAuthn challenge, SERVER-SIDE.
//
// # The defect this file exists to close
//
// The first cut of the federated login route took the challenge from the request body. That reads as
// reasonable — the browser needs it to call `navigator.credentials.get()`, so it seems natural for the
// browser to carry it back. It is not reasonable, and it makes the replay guard decorative: an
// attacker who captured one WebAuthn assertion replays it together with the challenge it was signed
// over, the two agree, and the signature verifies perfectly. A challenge the CLIENT chooses proves
// only that the client can choose a challenge.
//
// The whole value of the challenge is that the SERVER picked it, remembers picking it, and will accept
// it exactly once. So it is minted here, held here, and consumed here.
//
// # Why the store is small and in-process
//
// A challenge is worthless after ~two minutes and after one use, so it needs no durability: losing the
// set on restart costs an operator one retry. Both properties are enforced below rather than
// documented — an expired entry is swept and a consumed entry is deleted before it is returned.

// ChallengeTTL bounds how long a minted challenge may be answered. Short: the gap between asking for a
// challenge and touching a hardware key is seconds, not a workflow.
const ChallengeTTL = 2 * time.Minute

// ChallengeStore mints and redeems single-use WebAuthn challenges.
type ChallengeStore struct {
	mu     sync.Mutex
	issued map[string]challengeRecord
	now    Clock
}

type challengeRecord struct {
	value  []byte
	minted time.Time
}

// NewChallengeStore builds an empty store.
func NewChallengeStore(now Clock) *ChallengeStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ChallengeStore{issued: map[string]challengeRecord{}, now: now}
}

// Mint returns an opaque id and the challenge bytes it names.
//
// The id and the challenge are DIFFERENT random values. If the id were the challenge, holding the
// handle would be holding the secret, and the store would be proving nothing it did not hand out.
func (s *ChallengeStore) Mint() (string, []byte, error) {
	id := make([]byte, 16)
	value := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return "", nil, fmt.Errorf("adminidentity: cannot draw a challenge id: %w", err)
	}
	if _, err := rand.Read(value); err != nil {
		return "", nil, fmt.Errorf("adminidentity: cannot draw a challenge: %w", err)
	}
	handle := base64.RawURLEncoding.EncodeToString(id)

	s.mu.Lock()
	defer s.mu.Unlock()
	// Swept on write rather than on a timer: the store is only touched here and at Consume, so an
	// abandoned challenge costs one map entry until the next login begins.
	cutoff := s.now().Add(-ChallengeTTL)
	for key, record := range s.issued {
		if record.minted.Before(cutoff) {
			delete(s.issued, key)
		}
	}
	s.issued[handle] = challengeRecord{value: value, minted: s.now()}
	return handle, value, nil
}

// Consume returns a challenge ONCE and forgets it, whatever the caller does next.
//
// Deleted before the signature is checked, not after: a failed verification must not leave the
// challenge live for another attempt, or "single-use" would mean "single SUCCESSFUL use".
func (s *ChallengeStore) Consume(handle string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.issued[handle]
	delete(s.issued, handle)
	if !ok || record.minted.Before(s.now().Add(-ChallengeTTL)) {
		return nil, false
	}
	return record.value, true
}
