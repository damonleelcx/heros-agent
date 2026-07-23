package proposal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
)

// BlobStore is the content-addressed object store for the large, possibly-PII payloads a proposal
// produces: prompt-optimizer grounding bundles (failing-case traces), rendered candidate prompts, and
// candidate source diffs. DB rows hold only the content hash (design "Data model sketch"); the bytes
// live here, keyed by their SHA-256. This mirrors the registry / worktree / evalrun blob seams already
// in the codebase.
//
// Storing these as content-hashed blobs — never inline in a log line — is a privacy requirement
// (§2.3): a failing-case trace can carry user PII, so it is written once, addressed by hash, and
// referenced by hash everywhere else.
type BlobStore interface {
	// Put stores content and returns its content hash (64-hex SHA-256). Putting identical bytes twice
	// is idempotent and returns the same hash.
	Put(content []byte) (hash string, err error)
	// Get returns the content for a hash, or ok=false if absent.
	Get(hash string) (content []byte, ok bool)
}

// MemBlobStore is an in-memory BlobStore for tests and demos.
type MemBlobStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

// NewMemBlobStore returns an empty in-memory blob store.
func NewMemBlobStore() *MemBlobStore { return &MemBlobStore{data: map[string][]byte{}} }

// Put stores content and returns its content hash.
func (s *MemBlobStore) Put(content []byte) (string, error) {
	sum := sha256.Sum256(content)
	h := hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = map[string][]byte{}
	}
	// Copy so a later mutation of the caller's slice cannot corrupt the stored blob.
	cp := append([]byte(nil), content...)
	s.data[h] = cp
	return h, nil
}

// Get returns the content for a hash.
func (s *MemBlobStore) Get(hash string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.data[hash]
	return b, ok
}

// PersistedRewrite is the set of content-hash references a prompt rewrite leaves behind. Only these
// hashes are safe to log or store in a DB row; the bodies stay in the blob store.
type PersistedRewrite struct {
	// GroundingHash addresses the grounding bundle (which failing cases grounded the edit) — the
	// traceability anchor.
	GroundingHash string `json:"grounding_hash"`
	// PromptHash addresses the rendered candidate prompt body.
	PromptHash string `json:"prompt_hash"`
}

// PersistDiff stores a compiled candidate's source diff as a content-hashed blob and returns the hash
// (§5.5: candidate source diffs are kept as content-hashed blobs, never inline). The returned hash
// MUST equal the patch's own DiffHash — the diff is content-addressed at generation, and persistence
// only moves the bytes to the object store, so a mismatch means the diff was altered in flight.
func PersistDiff(store BlobStore, c Compiled) (string, error) {
	if store == nil {
		return "", fmt.Errorf("proposal: PersistDiff requires a BlobStore")
	}
	if c.Patch == nil {
		return "", nil // no diff (e.g. an empty codemod) — nothing to persist
	}
	h, err := store.Put(c.Patch.Diff)
	if err != nil {
		return "", err
	}
	if c.DiffHash != "" && h != c.DiffHash {
		return "", fmt.Errorf("proposal: persisted diff hash %s != patch DiffHash %s (diff altered in flight)", h, c.DiffHash)
	}
	return h, nil
}

// PersistRewrite stores a prompt rewrite's grounding bundle and rendered prompt body as content-hashed
// blobs and returns their hashes. It is the single sanctioned path that keeps optimizer inputs and
// rendered prompts out of logs (§2.3). The grounding bundle's own Hash MUST match the stored blob's
// hash — a mismatch means the bundle was tampered with between optimization and persistence.
func PersistRewrite(store BlobStore, edit PromptEdit) (PersistedRewrite, error) {
	if store == nil {
		return PersistedRewrite{}, fmt.Errorf("proposal: PersistRewrite requires a BlobStore")
	}
	bundleBytes, err := json.Marshal(edit.Grounding)
	if err != nil {
		return PersistedRewrite{}, err
	}
	gh, err := store.Put(bundleBytes)
	if err != nil {
		return PersistedRewrite{}, err
	}
	ph, err := store.Put([]byte(edit.NewPromptBody))
	if err != nil {
		return PersistedRewrite{}, err
	}
	return PersistedRewrite{GroundingHash: gh, PromptHash: ph}, nil
}
