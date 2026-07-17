package registry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// BlobStore holds content-addressed bytes. Prompt bodies live here and NOT in a DB column, because a
// template can carry PII and PRD §7 requires such content to be stored as content-hashed blobs
// referenced by hash, never inlined. The `blob` table from 0001_p0_lineage is the catalog; these are
// the bytes.
//
// This is the seam the P2 object store (which also holds generated diffs and node I/O) lands behind.
type BlobStore interface {
	// Put stores bytes and returns their SHA-256 content hash (lowercase hex). Put is idempotent:
	// storing identical bytes twice yields the same hash and no second copy.
	Put(ctx context.Context, data []byte) (contentHash string, err error)
	// Get returns the bytes for a content hash, or an error if absent.
	Get(ctx context.Context, contentHash string) ([]byte, error)
}

// FSBlobStore is a filesystem BlobStore, sufficient for P2's single-node internal deployment (PRD
// §12: P2 is internal-only, behind the run queue, no public surface).
type FSBlobStore struct{ root string }

// NewFSBlobStore returns a BlobStore rooted at dir, creating it if absent.
func NewFSBlobStore(dir string) (*FSBlobStore, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("registry: blob store root %s: %w", dir, err)
	}
	return &FSBlobStore{root: dir}, nil
}

// path fans out on the first byte of the hash so one directory does not accumulate every blob.
func (f *FSBlobStore) path(hash string) string {
	return filepath.Join(f.root, hash[:2], hash)
}

func (f *FSBlobStore) Put(ctx context.Context, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	dst := f.path(hash)
	if _, err := os.Stat(dst); err == nil {
		return hash, nil // identical bytes already stored; content-addressed, so nothing to do
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return "", fmt.Errorf("registry: blob put %s: %w", hash, err)
	}
	// Write to a temp file in the same directory and rename, so a concurrent reader never observes a
	// partially-written blob under a hash that promises complete content.
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-"+hash+"-*")
	if err != nil {
		return "", fmt.Errorf("registry: blob put %s: %w", hash, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("registry: blob put %s: %w", hash, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("registry: blob put %s: %w", hash, err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", fmt.Errorf("registry: blob put %s: %w", hash, err)
	}
	return hash, nil
}

func (f *FSBlobStore) Get(ctx context.Context, contentHash string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !isHash(contentHash) {
		return nil, fmt.Errorf("registry: blob get: %q is not a sha256 hex hash", contentHash)
	}
	data, err := os.ReadFile(f.path(contentHash))
	if err != nil {
		return nil, fmt.Errorf("registry: blob get %s: %w", contentHash, err)
	}
	// Verify on read: the hash is a promise about these bytes, so check it rather than trust the
	// filename. A silently-corrupted template would silently change every diff generated from it.
	if got := hashBytes(data); got != contentHash {
		return nil, fmt.Errorf("%w: blob %s contains bytes hashing to %s", ErrCorruptEntry, contentHash, got)
	}
	return data, nil
}

func isHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// putBlob stores bytes and catalogs them in the `blob` table so prompt_entry's FK to
// blob(content_hash) resolves.
//
// The object-store write cannot join the DB transaction, so the order is: bytes first, catalog
// second, entry third. A crash between steps can leave a catalogued or stored blob that no entry
// references — inert garbage, deduped on the next identical Put, and left for the blob GC the PRD
// defers (OQ5). The reverse order could not be made safe: an entry referencing bytes that were never
// written would resolve and then fail to render.
func (s *Store) putBlob(ctx context.Context, data []byte, mediaType string) (string, error) {
	return NewCatalogingBlobStore(s.db, s.blobs, mediaType).Put(ctx, data)
}

// CatalogingBlobStore writes bytes to an object store AND catalogs them in the `blob` table.
//
// # Why this exists, and why it is not optional
//
// 0001's `blob` table is the catalog every blob reference FKs to: prompt_entry.body_blob_hash,
// transform.diff_blob_hash, node_execution.input_blob_hash/output_blob_hash. A bare FSBlobStore
// writes bytes and nothing else, so a hash it returns has no catalog row — and the moment that hash
// is used as a reference, the FK rejects the write.
//
// That is a genuinely good failure (the FK is doing its job: a reference to an uncatalogued blob is
// dangling), but it means "store the bytes" and "make the bytes referenceable" are one operation, not
// two. Anything writing a blob it intends to REFERENCE must use this, not the raw store.
//
// It was found the way these things are found: the bare store passed every unit test, because those
// tests recorded node executions with no I/O. The first run with real per-node I/O failed the FK.
type CatalogingBlobStore struct {
	db    *sql.DB
	inner BlobStore
	// mediaType is what the catalog records. The blob's bytes are opaque here; the caller knows.
	mediaType string
}

// NewCatalogingBlobStore wraps a BlobStore so every Put is also catalogued.
func NewCatalogingBlobStore(db *sql.DB, inner BlobStore, mediaType string) *CatalogingBlobStore {
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	return &CatalogingBlobStore{db: db, inner: inner, mediaType: mediaType}
}

// Put stores the bytes, then catalogs them.
//
// Bytes first: a catalog row pointing at bytes that were never written would resolve and then fail to
// read, which is worse than no row at all. The reverse order cannot be made safe.
func (c *CatalogingBlobStore) Put(ctx context.Context, data []byte) (string, error) {
	hash, err := c.inner.Put(ctx, data)
	if err != nil {
		return "", err
	}
	if _, err := c.db.ExecContext(ctx,
		`INSERT INTO blob (content_hash, size_bytes, media_type) VALUES ($1, $2, $3)
		 ON CONFLICT (content_hash) DO NOTHING`,
		hash, len(data), c.mediaType); err != nil {
		return "", fmt.Errorf("registry: catalog blob %s: %w", hash, err)
	}
	return hash, nil
}

func (c *CatalogingBlobStore) Get(ctx context.Context, hash string) ([]byte, error) {
	return c.inner.Get(ctx, hash)
}
