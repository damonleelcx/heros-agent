package sourceingest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// store_conn_pg.go is the durable ConnectionStore and SnapshotStore, backed by migration 0049.
//
// # Why the snapshot half lives here and not in store_pg.go
//
// `PGBundleStore` is Mode 1's store and this change does not touch it — §7.11 requires every existing
// bundle behaviour unchanged, and the cheapest way to guarantee that is to add no statement to it.
// The clone path's three operations (write a derived snapshot, cascade-delete by connection, sweep by
// expiry) are additive and live beside it, reading the same two tables. `PGSnapshotStore` embeds the
// bundle store so one value satisfies both interfaces at the wiring site.
//
// # 🔴 The blob is deleted BEFORE the row, and only when nothing else references it
//
// `store_pg.go`'s Delete deliberately leaves the blob, because the store is content-addressed and
// another revision (or another tenant with a byte-identical tree) may reference the same hash —
// deleting inline would let one tenant's deletion destroy another's snapshot.
//
// The cascade cannot accept that. D3 is explicit: a revocation that leaves the platform able to answer
// from cache is not a revocation, and a blob left behind is exactly that cache. So the delete here is
// REFERENCE-COUNTED: the blob goes only when this was the last row pointing at that hash. That check
// and the row delete run in ONE transaction, so a concurrent push of the same tree cannot slip between
// them and lose its bytes.

// PGSnapshotStore is the durable snapshot store: the bundle store plus the clone path's three
// operations.
type PGSnapshotStore struct {
	*PGBundleStore
}

// NewPGSnapshotStore wraps a bundle store with the derived-snapshot operations.
func NewPGSnapshotStore(b *PGBundleStore) (*PGSnapshotStore, error) {
	if b == nil {
		return nil, fmt.Errorf("sourceingest: nil bundle store")
	}
	return &PGSnapshotStore{PGBundleStore: b}, nil
}

// PutDerived writes a cloned snapshot with its connection and expiry.
//
// Bytes first then the row, exactly as Put does, and for the same reason: a row pointing at a blob
// that was never written is a snapshot the platform believes it has and cannot read.
func (p *PGSnapshotStore) PutDerived(ctx context.Context, ref Ref, data []byte, connectionID string, expiresAtMS int64) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	if connectionID == "" {
		return fmt.Errorf("sourceingest: a derived snapshot must name the connection it came from")
	}
	if expiresAtMS <= 0 {
		// Refused rather than defaulted. A derived snapshot with no expiry is a tree held forever
		// under a grant, which is the standing capability ADR-013 bounded — and `source_bundle`'s
		// `source_bundle_derived_pair` constraint would refuse it anyway, one layer down, with a
		// message about a check constraint instead of a sentence.
		return fmt.Errorf("sourceingest: a derived snapshot must carry an expiry (PRD §14 A4)")
	}
	if len(data) == 0 {
		return fmt.Errorf("sourceingest: refusing an empty snapshot for %s", ref)
	}
	hash, err := p.blobs.Put(ctx, data)
	if err != nil {
		return fmt.Errorf("sourceingest: store cloned bytes for %s: %w", ref, err)
	}
	_, err = p.db.ExecContext(ctx,
		`INSERT INTO source_bundle (tenant_id, workflow_id, source_revision, content_hash, size_bytes, received_at, connection_id, expires_at_ms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (tenant_id, workflow_id, source_revision) DO UPDATE
		   SET content_hash  = EXCLUDED.content_hash,
		       size_bytes    = EXCLUDED.size_bytes,
		       received_at   = EXCLUDED.received_at,
		       connection_id = EXCLUDED.connection_id,
		       expires_at_ms = EXCLUDED.expires_at_ms`,
		ref.TenantID, ref.WorkflowID, ref.SourceRevision, hash, int64(len(data)), p.now().UTC(), connectionID, expiresAtMS)
	if err != nil {
		return fmt.Errorf("sourceingest: record cloned snapshot %s: %w", ref, err)
	}
	return nil
}

// LiveSnapshot reports whether an unexpired snapshot DERIVED FROM connectionID exists for ref.
//
// 🔴 `connection_id = $4` is the half that matters. Without it this answers YES about a PUSHED bundle
// at the same revision, and a connected workflow is then served an upload nobody checked against the
// repository — forever, without a single clone. See the interface's comment.
//
// A NULL expiry is live forever — the pushed-bundle rule — and is unreachable here because a row with
// a connection_id always carries an expiry (`source_bundle_derived_pair`). The clause is written out
// anyway rather than relying on that constraint from a different file.
func (p *PGSnapshotStore) LiveSnapshot(ctx context.Context, ref Ref, connectionID string, nowMS int64) (bool, error) {
	if err := ref.Validate(); err != nil {
		return false, err
	}
	if connectionID == "" {
		return false, fmt.Errorf("sourceingest: LiveSnapshot needs the connection asking")
	}
	var n int
	err := p.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM source_bundle
		  WHERE tenant_id = $1 AND workflow_id = $2 AND source_revision = $3
		    AND connection_id = $4
		    AND (expires_at_ms IS NULL OR expires_at_ms > $5)`,
		ref.TenantID, ref.WorkflowID, ref.SourceRevision, connectionID, nowMS).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("sourceingest: check snapshot %s: %w", ref, err)
	}
	return n > 0, nil
}

// DeleteByConnection removes every snapshot derived from a connection — the cascade (D3).
func (p *PGSnapshotStore) DeleteByConnection(ctx context.Context, tenantID, connectionID string) (int, error) {
	if tenantID == "" || connectionID == "" {
		return 0, fmt.Errorf("sourceingest: cascade needs a tenant and a connection")
	}
	return p.deleteWhere(ctx,
		`SELECT content_hash FROM source_bundle WHERE tenant_id = $1 AND connection_id = $2`,
		`DELETE FROM source_bundle WHERE tenant_id = $1 AND connection_id = $2`,
		tenantID, connectionID)
}

// DeleteExpired removes every snapshot past its expiry — the retention sweep.
//
// 🚫 `expires_at_ms IS NOT NULL` is part of the predicate and is not redundant with `<= $1`: SQL's
// NULL comparison would make a pushed bundle's NULL evaluate to unknown and be excluded anyway, but
// relying on that means the safety of every customer's pushed source rests on a reader remembering
// three-valued logic. Stating it is what makes the intent checkable.
func (p *PGSnapshotStore) DeleteExpired(ctx context.Context, nowMS int64) (int, error) {
	return p.deleteWhere(ctx,
		`SELECT content_hash FROM source_bundle WHERE expires_at_ms IS NOT NULL AND expires_at_ms <= $1`,
		`DELETE FROM source_bundle WHERE expires_at_ms IS NOT NULL AND expires_at_ms <= $1`,
		nowMS)
}

// deleteWhere deletes matching rows and reference-counts their blobs, in one transaction.
func (p *PGSnapshotStore) deleteWhere(ctx context.Context, selectSQL, deleteSQL string, args ...any) (int, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("sourceingest: begin delete: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit

	rows, err := tx.QueryContext(ctx, selectSQL, args...)
	if err != nil {
		return 0, fmt.Errorf("sourceingest: enumerate snapshots to delete: %w", err)
	}
	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("sourceingest: scan snapshot hash: %w", err)
		}
		hashes = append(hashes, h)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("sourceingest: enumerate snapshots to delete: %w", err)
	}
	_ = rows.Close()

	res, err := tx.ExecContext(ctx, deleteSQL, args...)
	if err != nil {
		return 0, fmt.Errorf("sourceingest: delete snapshots: %w", err)
	}
	n, _ := res.RowsAffected()

	// Reference-count inside the SAME transaction, so a concurrent push of a byte-identical tree
	// cannot land between the count and the blob delete and lose its bytes.
	var orphaned []string
	for _, h := range hashes {
		var refs int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM source_bundle WHERE content_hash = $1`, h).Scan(&refs); err != nil {
			return 0, fmt.Errorf("sourceingest: reference-count blob: %w", err)
		}
		if refs == 0 {
			orphaned = append(orphaned, h)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("sourceingest: commit snapshot delete: %w", err)
	}

	// The blobs go AFTER the commit. If this half fails the rows are already gone, so nothing can
	// serve the tree — the failure leaves unreferenced storage, which is the recoverable direction and
	// is collected by the same sweep that collects any other orphan.
	for _, h := range orphaned {
		if d, ok := p.blobs.(blobDeleter); ok {
			_ = d.Delete(ctx, h)
		}
	}
	return int(n), nil
}

// PGConnectionStore is the durable ConnectionStore.
type PGConnectionStore struct {
	db *sql.DB
}

// NewPGConnectionStore returns a durable connection store.
func NewPGConnectionStore(db *sql.DB) (*PGConnectionStore, error) {
	if db == nil {
		return nil, fmt.Errorf("sourceingest: nil database")
	}
	return &PGConnectionStore{db: db}, nil
}

// Create inserts a connection, refusing a second one for the same workflow.
//
// The refusal comes from `uq_source_connection_workflow`, not from a SELECT-then-INSERT: a read
// followed by a write is a race between two browser tabs, and this repository has already paid for
// discovering that a unique index is invisible to a test that never contends.
func (p *PGConnectionStore) Create(ctx context.Context, c Connection) error {
	if err := c.Validate(); err != nil {
		return err
	}
	var subPath any
	if c.SubPath != "" {
		subPath = c.SubPath
	}
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO source_connection
		   (connection_id, tenant_id, workflow_id, forge, repository, sub_path, grant_kind, external_id, created_by, created_at_ms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		c.ConnectionID, c.TenantID, c.WorkflowID, c.Forge.String(), c.Repository, subPath,
		c.GrantKind.String(), nullIfEmpty(c.ExternalID), c.CreatedBy, c.CreatedAtMS)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: workflow %s", ErrConnectionExists, c.WorkflowID)
		}
		return fmt.Errorf("sourceingest: create connection for %s: %w", c.WorkflowID, err)
	}
	return nil
}

const connectionColumns = `connection_id, tenant_id, workflow_id, forge, repository,
	COALESCE(sub_path, ''), grant_kind, COALESCE(external_id, ''), created_by, created_at_ms`

func scanConnection(sc interface{ Scan(...any) error }) (Connection, error) {
	var c Connection
	var forge, grant string
	if err := sc.Scan(&c.ConnectionID, &c.TenantID, &c.WorkflowID, &forge, &c.Repository,
		&c.SubPath, &grant, &c.ExternalID, &c.CreatedBy, &c.CreatedAtMS); err != nil {
		return Connection{}, err
	}
	c.Forge = Forge(forge)
	c.GrantKind = GrantKind(grant)
	return c, nil
}

// ForWorkflow returns a workflow's connection, or ErrNoConnection.
func (p *PGConnectionStore) ForWorkflow(ctx context.Context, tenantID, workflowID string) (Connection, error) {
	row := p.db.QueryRowContext(ctx,
		`SELECT `+connectionColumns+` FROM source_connection WHERE tenant_id = $1 AND workflow_id = $2`,
		tenantID, workflowID)
	c, err := scanConnection(row)
	switch {
	case err == sql.ErrNoRows:
		return Connection{}, ErrNoConnection
	case err != nil:
		return Connection{}, fmt.Errorf("sourceingest: read connection for %s: %w", workflowID, err)
	}
	return c, nil
}

// List returns a tenant's connections, oldest first.
func (p *PGConnectionStore) List(ctx context.Context, tenantID string) ([]Connection, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+connectionColumns+` FROM source_connection WHERE tenant_id = $1 ORDER BY created_at_ms, connection_id`,
		tenantID)
	if err != nil {
		return nil, fmt.Errorf("sourceingest: list connections: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Connection{}
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, fmt.Errorf("sourceingest: scan connection: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Revoke deletes the grant row. The ledger goes with it through the FK cascade — see the migration's
// comment for why that trade was chosen.
func (p *PGConnectionStore) Revoke(ctx context.Context, tenantID, connectionID string) error {
	res, err := p.db.ExecContext(ctx,
		`DELETE FROM source_connection WHERE tenant_id = $1 AND connection_id = $2`, tenantID, connectionID)
	if err != nil {
		return fmt.Errorf("sourceingest: revoke connection %s: %w", connectionID, err)
	}
	// 🔴 Reports ErrNoConnection when nothing was deleted, unlike the bundle DELETE route which is
	// deliberately silent about absence. The difference is who is asking: that route is a customer
	// saying "stop holding this", where absence is the outcome either way. This is an internal step in
	// a THREE-PART cascade, and a step that cannot tell the caller "there was nothing here" would let
	// a cascade over a mistyped id report success.
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoConnection
	}
	return nil
}

// AppendRecord appends one clone record.
//
// 🔴 The write is checked and its error returned. A ledger entry silently dropped is the failure mode
// where the platform reads a customer's repository and no record of it exists — which is the one thing
// the whole disclosure rests on. The CALLER decides whether losing the note is worse than losing the
// read; this function does not decide for it by swallowing.
func (p *PGConnectionStore) AppendRecord(ctx context.Context, r CloneRecord) error {
	if r.RecordID == "" || r.ConnectionID == "" || r.TenantID == "" {
		return fmt.Errorf("sourceingest: a clone record needs a record_id, a tenant and a connection")
	}
	if !r.Actor.Valid() {
		return fmt.Errorf("sourceingest: %q is not an actor", r.Actor)
	}
	// 🔴 Checked here as well as by `source_clone_record_outcome_known`. The constraint is the real
	// guard; this one exists so the caller gets a sentence rather than a SQLSTATE, because a ledger
	// write that fails is a read the platform performed and did not record, and whoever debugs it
	// needs the outcome value in the message.
	if !r.Outcome.Valid() {
		return fmt.Errorf("sourceingest: %q is not a clone outcome", r.Outcome)
	}
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO source_clone_record
		   (record_id, tenant_id, connection_id, repository, revision, actor, actor_id, reason, outcome, bytes, entries, duration_ms, at_ms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		r.RecordID, r.TenantID, r.ConnectionID, r.Repository, r.Revision, r.Actor.String(),
		nullIfEmpty(r.ActorID), nullIfEmpty(r.Reason), r.Outcome.String(), r.Bytes, int64(r.Entries), r.DurationMS, r.AtMS)
	if err != nil {
		return fmt.Errorf("sourceingest: append clone record for %s: %w", r.ConnectionID, err)
	}
	return nil
}

// Records returns a connection's ledger, newest first.
func (p *PGConnectionStore) Records(ctx context.Context, tenantID, connectionID string, limit int) ([]CloneRecord, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT record_id, tenant_id, connection_id, repository, revision, actor,
		        COALESCE(actor_id, ''), COALESCE(reason, ''), outcome, bytes, entries, duration_ms, at_ms
		   FROM source_clone_record
		  WHERE tenant_id = $1 AND connection_id = $2
		  ORDER BY at_ms DESC, record_id DESC
		  LIMIT $3`,
		tenantID, connectionID, limit)
	if err != nil {
		return nil, fmt.Errorf("sourceingest: read clone records: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []CloneRecord{}
	for rows.Next() {
		var r CloneRecord
		var actor, outcome string
		var entries int64
		if err := rows.Scan(&r.RecordID, &r.TenantID, &r.ConnectionID, &r.Repository, &r.Revision,
			&actor, &r.ActorID, &r.Reason, &outcome, &r.Bytes, &entries, &r.DurationMS, &r.AtMS); err != nil {
			return nil, fmt.Errorf("sourceingest: scan clone record: %w", err)
		}
		r.Actor = Actor(actor)
		r.Outcome = Outcome(outcome)
		r.Entries = int(entries)
		out = append(out, r)
	}
	return out, rows.Err()
}

// blobDeleter is the OPTIONAL blob-store capability the cascade uses.
//
// `registry.BlobStore` has `Put` and `Get` and no `Delete`, by design — it is content-addressed and
// append-only, and a delete on a shared hash is how one tenant's deletion destroys another's snapshot.
// The cascade nonetheless has to reclaim bytes that nothing references any more, so it asks whether
// this store can delete and proceeds without if it cannot.
//
// 🔴 A store that cannot delete leaves UNREFERENCED bytes after a revocation. That is not a silent
// compromise of D3: the ROW is gone in every case, and the row is the only thing that can find a blob
// for a tenant, so nothing can serve the tree. What remains is storage nobody can address, collected
// by the same sweep that collects any other orphan.
type blobDeleter interface {
	Delete(ctx context.Context, contentHash string) error
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// isUniqueViolation reports a Postgres unique-constraint violation without importing the driver's
// error type.
//
// String matching, and the reason is honest rather than lazy: `lib/pq` exposes `*pq.Error` with code
// 23505, and this package does not import `lib/pq` — nothing else in it needs the driver, and adding
// the import to classify one error would couple the domain to a driver the deployment may swap. The
// SQLSTATE appears in the message text for every Postgres driver, so the match is on the code rather
// than on prose.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "23505") || strings.Contains(s, "duplicate key value")
}

// ── PairingStore, on the same connection ────────────────────────────────────────────────────────

// CreatePairing records a pending pairing.
func (p *PGConnectionStore) CreatePairing(ctx context.Context, pr Pairing) error {
	if err := pr.Validate(); err != nil {
		return err
	}
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO source_local_pairing
		   (pairing_id, tenant_id, workflow_id, state, user_code, machine_name, revision, created_at_ms, claimed_at_ms, expires_at_ms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		pr.PairingID, pr.TenantID, pr.WorkflowID, pr.State.String(), pr.UserCode,
		nullIfEmpty(pr.MachineName), nullIfEmpty(pr.Revision), pr.CreatedAtMS,
		nullIfZero(pr.ClaimedAtMS), pr.ExpiresAtMS)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("sourceingest: that pairing code is already in use")
		}
		return fmt.Errorf("sourceingest: create pairing: %w", err)
	}
	return nil
}

// ClaimPairing moves a pending pairing to paired.
//
// # 🔴 One UPDATE, not a SELECT-then-UPDATE
//
// `WHERE state = 'pending' AND expires_at_ms > $n` inside the write is what makes the claim atomic. A
// read followed by a write is a race between two agents typing the same code, and the loser would get
// a success — attributing a workflow to the wrong machine, in the one table whose whole purpose is
// saying which machine reads which workflow.
//
// The expiry is in the predicate too, so an expired code cannot be claimed even by a process whose own
// clock disagrees: the database decides, once.
func (p *PGConnectionStore) ClaimPairing(ctx context.Context, userCode, machineName, revision string, atMS int64) (Pairing, error) {
	row := p.db.QueryRowContext(ctx,
		`UPDATE source_local_pairing
		    SET state = 'paired', machine_name = $2, revision = $3, claimed_at_ms = $4
		  WHERE user_code = $1 AND state = 'pending' AND expires_at_ms > $4
		  RETURNING pairing_id, tenant_id, workflow_id, state, user_code,
		            COALESCE(machine_name,''), COALESCE(revision,''), created_at_ms,
		            COALESCE(claimed_at_ms,0), expires_at_ms`,
		userCode, machineName, nullIfEmpty(revision), atMS)
	pr, err := scanPairing(row)
	if err == sql.ErrNoRows {
		// Nothing matched. It could be an unknown code, an already-claimed one, or an expired one, and
		// only the last is safe to distinguish — so a second, read-only query asks which.
		var state string
		var expires int64
		qerr := p.db.QueryRowContext(ctx,
			`SELECT state, expires_at_ms FROM source_local_pairing WHERE user_code = $1`, userCode).
			Scan(&state, &expires)
		if qerr == nil && state == string(PairingPending) && expires <= atMS {
			return Pairing{}, ErrPairingExpired
		}
		return Pairing{}, ErrNoPairing
	}
	if err != nil {
		return Pairing{}, fmt.Errorf("sourceingest: claim pairing: %w", err)
	}
	return pr, nil
}

// PairingByID returns one pairing within a tenant.
func (p *PGConnectionStore) PairingByID(ctx context.Context, tenantID, pairingID string, nowMS int64) (Pairing, error) {
	row := p.db.QueryRowContext(ctx, pairingSelect+` WHERE tenant_id = $1 AND pairing_id = $2`, tenantID, pairingID)
	pr, err := scanPairing(row)
	switch {
	case err == sql.ErrNoRows:
		return Pairing{}, ErrNoPairing
	case err != nil:
		return Pairing{}, fmt.Errorf("sourceingest: read pairing: %w", err)
	}
	// Expiry applied at READ — see Pairing.StateAt for why it is not a sweeper's job.
	pr.State = pr.StateAt(nowMS)
	return pr, nil
}

// PairingsForTenant returns a tenant's pairings, newest first.
func (p *PGConnectionStore) PairingsForTenant(ctx context.Context, tenantID string, nowMS int64) ([]Pairing, error) {
	rows, err := p.db.QueryContext(ctx, pairingSelect+` WHERE tenant_id = $1 ORDER BY created_at_ms DESC, pairing_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("sourceingest: list pairings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Pairing{}
	for rows.Next() {
		pr, serr := scanPairing(rows)
		if serr != nil {
			return nil, fmt.Errorf("sourceingest: scan pairing: %w", serr)
		}
		pr.State = pr.StateAt(nowMS)
		out = append(out, pr)
	}
	return out, rows.Err()
}

const pairingSelect = `SELECT pairing_id, tenant_id, workflow_id, state, user_code,
	COALESCE(machine_name,''), COALESCE(revision,''), created_at_ms, COALESCE(claimed_at_ms,0), expires_at_ms
	FROM source_local_pairing`

func scanPairing(sc interface{ Scan(...any) error }) (Pairing, error) {
	var pr Pairing
	var state string
	if err := sc.Scan(&pr.PairingID, &pr.TenantID, &pr.WorkflowID, &state, &pr.UserCode,
		&pr.MachineName, &pr.Revision, &pr.CreatedAtMS, &pr.ClaimedAtMS, &pr.ExpiresAtMS); err != nil {
		return Pairing{}, err
	}
	pr.State = PairingState(state)
	return pr, nil
}

// nullIfZero writes NULL for a zero timestamp.
//
// 🔴 Zero is not "the epoch" here, it is "has not happened". Writing 0 would make `claimed_at_ms` an
// answerable number for a pairing nobody claimed, and every reader that formats it would render 1970.
func nullIfZero(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
