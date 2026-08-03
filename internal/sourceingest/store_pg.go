package sourceingest

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"time"

	"github.com/heros-foreal/agentd/internal/registry"
)

// store_pg.go is the durable BundleStore, backed by migration 0022.
//
// # Why the bytes are not in the row
//
// A source bundle is megabytes and may be gigabytes. Postgres would hold it, and then every backup,
// every replica and every `pg_dump` would carry a copy of the customer's source through infrastructure
// sized for rows. The bytes go to the blob store — which already exists, is content-addressed, and
// dedupes a re-push of an unchanged tree for free — and the row holds the metadata that makes the blob
// findable and deletable.
//
// 🔴 The row is what makes deletion possible. A blob store keyed by content hash cannot be asked "what
// belongs to this tenant"; only this table can answer that. So Delete removes the row LAST, after the
// bytes are gone: the opposite order can leave a blob nothing references, which is a customer's source
// on our disk that no deletion request will ever find again.

// PGBundleStore stores bundle bytes in a blob store and their metadata in Postgres.
type PGBundleStore struct {
	db    *sql.DB
	blobs registry.BlobStore
	now   func() time.Time
}

// NewPGBundleStore returns a durable BundleStore.
func NewPGBundleStore(db *sql.DB, blobs registry.BlobStore) (*PGBundleStore, error) {
	if db == nil {
		return nil, fmt.Errorf("sourceingest: nil database")
	}
	if blobs == nil {
		return nil, fmt.Errorf("sourceingest: nil blob store")
	}
	return &PGBundleStore{db: db, blobs: blobs, now: time.Now}, nil
}

// Put writes the bytes then upserts the row.
//
// Bytes first, deliberately: a row pointing at a blob that was never written is a snapshot the platform
// believes it has and cannot read, which surfaces later as a discovery failure on a bundle the customer
// was told we accepted. A blob with no row is the recoverable direction — it is unreferenced storage,
// found by the same sweep that collects any other orphan.
func (p *PGBundleStore) Put(ctx context.Context, ref Ref, data []byte) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if len(data) == 0 {
		// Matches source_bundle_size_positive. Checked here too so the caller gets a sentence rather
		// than a constraint-violation string: an empty upload is a failed push, not a bad request shape.
		return fmt.Errorf("sourceingest: refusing an empty bundle for %s", ref)
	}
	hash, err := p.blobs.Put(ctx, data)
	if err != nil {
		return fmt.Errorf("sourceingest: store bundle bytes for %s: %w", ref, err)
	}
	_, err = p.db.ExecContext(ctx,
		`INSERT INTO source_bundle (tenant_id, workflow_id, source_revision, content_hash, size_bytes, received_at)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (tenant_id, workflow_id, source_revision) DO UPDATE
		   SET content_hash = EXCLUDED.content_hash,
		       size_bytes   = EXCLUDED.size_bytes,
		       received_at  = EXCLUDED.received_at`,
		ref.TenantID, ref.WorkflowID, ref.SourceRevision, hash, int64(len(data)), p.now().UTC())
	if err != nil {
		return fmt.Errorf("sourceingest: record bundle %s: %w", ref, err)
	}
	return nil
}

// Open returns the bundle bytes, or ErrNoSource when this tenant pushed nothing for this revision.
func (p *PGBundleStore) Open(ctx context.Context, ref Ref) (io.ReadCloser, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	var hash string
	err := p.db.QueryRowContext(ctx,
		`SELECT content_hash FROM source_bundle
		  WHERE tenant_id = $1 AND workflow_id = $2 AND source_revision = $3`,
		ref.TenantID, ref.WorkflowID, ref.SourceRevision).Scan(&hash)
	switch {
	case err == sql.ErrNoRows:
		return nil, ErrNoSource
	case err != nil:
		return nil, fmt.Errorf("sourceingest: look up bundle %s: %w", ref, err)
	}
	data, err := p.blobs.Get(ctx, hash)
	if err != nil {
		// A row whose blob is missing is NOT "no source pushed" — it is a platform that lost the bytes
		// it accepted. Flattening it into ErrNoSource would tell the customer they never pushed, and
		// they would push again into the same hole.
		return nil, fmt.Errorf("sourceingest: bundle %s is recorded but its bytes (%s) are unreadable: %w", ref, hash, err)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// Delete removes the recorded bundle.
//
// The row goes last — see this file's header. Note what this does NOT do: it does not delete the blob,
// because the store is content-addressed and another revision (or another tenant with a byte-identical
// tree) may reference the same hash. Reclaiming bytes is a sweep over unreferenced hashes, and doing it
// inline here would let one tenant's deletion destroy another's snapshot.
func (p *PGBundleStore) Delete(ctx context.Context, ref Ref) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	_, err := p.db.ExecContext(ctx,
		`DELETE FROM source_bundle WHERE tenant_id = $1 AND workflow_id = $2 AND source_revision = $3`,
		ref.TenantID, ref.WorkflowID, ref.SourceRevision)
	if err != nil {
		return fmt.Errorf("sourceingest: delete bundle %s: %w", ref, err)
	}
	return nil
}
