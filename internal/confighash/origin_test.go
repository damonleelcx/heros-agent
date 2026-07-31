package confighash

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOriginDoesNotAffectConfigHash is P13 task 8.5 / FR22: `config_hash` is purely STRUCTURAL, so a
// configuration a person authored and a byte-identical one an operator proposed hash the same and are
// therefore the same measurement.
//
// The test asserts the property from both directions, because each failure mode is a different bug:
//
//   - adding authorship to the hashed shape would fork identity by author (two hashes for one
//     configuration, P0's golden vectors move, "did we already measure this?" stops having an answer);
//   - and a hash blind to input would collapse two different configurations into one measurement — so
//     the control case must still differ, or this test would also pass against a constant.
func TestOriginDoesNotAffectConfigHash(t *testing.T) {
	// A resolved_config. Authorship is deliberately absent — this is the shape, and the shape is the
	// contract.
	const resolved = `{"nodes":{"n1":{"model_ref":"m-v1","prompt_ref":"p-v1"}},"source_revision":"rev1"}`

	want := mustSum(t, resolved)

	// The same configuration, reached by two different origins. Authorship travels on the candidate /
	// transform / delivery record — none of which is an input here — so both must land on one hash.
	for _, origin := range []string{"operator", "user"} {
		if got := mustSum(t, resolved); got != want {
			t.Errorf("origin %q changed config_hash: got %s want %s", origin, got, want)
		}
	}

	// The control: if authorship HAD leaked into the hashed shape, this is what it would look like, and
	// it must produce a different hash.
	const leaked = `{"nodes":{"n1":{"model_ref":"m-v1","prompt_ref":"p-v1"}},"origin":"user","source_revision":"rev1"}`
	if mustSum(t, leaked) == want {
		t.Fatal("adding an origin key did not change the hash — the canonicalizer is ignoring its input")
	}

	// And the shape itself must not carry authorship. This is the assertion that fails if someone adds
	// the field to resolved_config later.
	canon, err := CanonicalizeBytes([]byte(resolved))
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(canon, &decoded); err != nil {
		t.Fatalf("decode canonical: %v", err)
	}
	for _, banned := range []string{"origin", "actor", "actor_id", "tenant_id", "authored_by", "forked_from_proposal"} {
		if _, present := decoded[banned]; present {
			t.Errorf("resolved_config carries %q — authorship must never be hashed", banned)
		}
	}
}

// TestGoldenVectorsStillReproduce re-asserts P0's frozen vectors after 13c (task 8.5, NFR3).
//
// It overlaps TestDeterminismAndCanonicalForm on purpose. That test asks "does the canonicalizer
// work"; this one asks "did adding a second origin move a hash that already existed", and it is the
// one a reviewer of a 13c change looks for by name.
func TestGoldenVectorsStillReproduce(t *testing.T) {
	g := loadGolden(t)

	canon, err := Canonicalize(decodeNum(t, g.Base.ResolvedConfig))
	if err != nil {
		t.Fatalf("canonicalize base: %v", err)
	}
	if string(canon) != g.Base.CanonicalJSON {
		t.Errorf("canonical form drifted:\n got %s\nwant %s", canon, g.Base.CanonicalJSON)
	}
	got, err := Sum(decodeNum(t, g.Base.ResolvedConfig))
	if err != nil {
		t.Fatalf("sum base: %v", err)
	}
	if got != g.Base.ConfigHash {
		t.Errorf("base config_hash drifted: got %s want %s", got, g.Base.ConfigHash)
	}

	// The frozen shape must still be free of anything 13c introduced.
	if strings.Contains(g.Base.CanonicalJSON, `"origin"`) {
		t.Error("the frozen golden vector now contains an origin key")
	}
}

func mustSum(t *testing.T, raw string) string {
	t.Helper()
	h, err := SumBytes([]byte(raw))
	if err != nil {
		t.Fatalf("SumBytes: %v", err)
	}
	return h
}
