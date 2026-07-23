package proposal

import (
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/transform"
)

// §5.5: a candidate's source diff is kept as a content-hashed blob, and the stored hash equals the
// patch's own DiffHash (content-addressed at generation, only moved to the store on persist).
func TestPersistDiff_ContentHashedMatchesDiffHash(t *testing.T) {
	diff := []byte("--- a/pipeline.go\n+++ b/pipeline.go\n@@ -1 +1 @@\n-old\n+new\n")
	store := NewMemBlobStore()
	// DiffHash is the store's own hash of the bytes, so a faithful persist round-trips to it.
	want, _ := store.Put(diff)
	c := Compiled{DiffHash: want, Patch: &transform.Patch{Diff: diff, DiffHash: want}}

	h, err := PersistDiff(store, c)
	if err != nil {
		t.Fatalf("PersistDiff: %v", err)
	}
	if h != want {
		t.Errorf("persisted diff hash %s != content hash %s", h, want)
	}
	got, ok := store.Get(h)
	if !ok || string(got) != string(diff) {
		t.Error("source diff was not stored content-addressably")
	}
}

// §5.5: a diff whose declared DiffHash does not match its bytes is rejected — persistence must not
// silently store a diff under a hash that lies about its content.
func TestPersistDiff_RejectsTamperedHash(t *testing.T) {
	c := Compiled{DiffHash: strings.Repeat("f", 64), Patch: &transform.Patch{Diff: []byte("real"), DiffHash: strings.Repeat("f", 64)}}
	if _, err := PersistDiff(NewMemBlobStore(), c); err == nil {
		t.Fatal("a diff whose DiffHash disagrees with its bytes must be rejected")
	}
}
