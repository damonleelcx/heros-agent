package adminidentity

import (
	"testing"
	"time"
)

// challenge_test.go fences the defect that made the WebAuthn replay guard decorative.
//
// The first cut of the federated login route took the challenge from the REQUEST BODY. Every signature
// check still passed, every fixture test still went green, and the guard protected nothing: an attacker
// replaying a captured assertion simply sends the challenge it was signed over. The property that
// matters is not "a challenge was checked" — it is "the SERVER picked this challenge, remembers picking
// it, and will accept it exactly once".

func TestAChallengeIsRedeemableExactlyOnce(t *testing.T) {
	store := NewChallengeStore(func() time.Time { return time.Now().UTC() })
	id, value, err := store.Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(value) != 32 {
		t.Fatalf("a challenge is 32 bytes of CSPRNG, got %d", len(value))
	}
	if id == "" || string(value) == id {
		// Different values on purpose: if the handle WERE the challenge, holding the handle would be
		// holding the secret and the store would prove nothing it did not hand out.
		t.Fatal("the challenge id and the challenge must be different values")
	}

	got, ok := store.Consume(id)
	if !ok || string(got) != string(value) {
		t.Fatal("a freshly minted challenge did not redeem")
	}
	if _, ok := store.Consume(id); ok {
		t.Fatal("the same challenge redeemed twice — this is the replay the guard exists to stop")
	}
}

func TestAnUnknownOrExpiredChallengeIsRefused(t *testing.T) {
	now := time.Now().UTC()
	clock := now
	store := NewChallengeStore(func() time.Time { return clock })

	if _, ok := store.Consume("a-handle-nobody-minted"); ok {
		t.Fatal("a challenge this platform never minted was accepted")
	}

	id, _, err := store.Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	clock = now.Add(ChallengeTTL + time.Second)
	if _, ok := store.Consume(id); ok {
		t.Fatal("a challenge older than its TTL was accepted")
	}
}

func TestTwoChallengesAreNeverTheSame(t *testing.T) {
	// A store that reissued a value would let one captured assertion answer a later login.
	store := NewChallengeStore(nil)
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id, value, err := store.Mint()
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if seen[id] || seen[string(value)] {
			t.Fatal("a challenge or its handle repeated")
		}
		seen[id], seen[string(value)] = true, true
	}
}
